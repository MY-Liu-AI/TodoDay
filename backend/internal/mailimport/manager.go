package mailimport

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"tododay/internal/parse"
	"tododay/internal/secretbox"
	"tododay/internal/store"
)

// Manager 内嵌运行邮箱账单导入：每个工作区一条后台 goroutine，每日
// 拉一次增量邮件，Kimi 解析后落账本。与助手 Manager 同构：
// enabled 是持久化意图，running 是此刻有没有在跑。
//
// 凭据来源（与助手同一套三态规则）：邮箱地址 + IMAP 授权码以密文落库。
// 「已停用」「无凭据／已被撤销」一律不连接——Start 同步检查凭据存在性，
// 缺失即失败；撤销（clearAuthCode）后下一轮凭据解析失败如实退出，
// 不存在「所有者以为删了、循环还在跑」。只有「密文暂时无法解密」这类
// 情况才把失败原因写进运行状态。
type Manager struct {
	st  *store.Store
	box *secretbox.Box

	// imapAddr 是 IMAP 服务端点，默认 imap.qq.com:993（腾讯邮箱）；
	// 可用 TODO_MAIL_IMAP_ADDR 覆盖；测试直接改这个字段注入 memserver。
	imapAddr  string
	tlsConfig *tls.Config // 测试注入自签 CA；生产走系统根证书

	// window/limits 是每日拉取的窗口：7 天、50 封上限。设成字段以便测试收紧。
	window     time.Duration
	listLimit  int
	pollEvery  time.Duration // 每日一次；启动后先立即补一轮再按周期跑
	firstRunIn time.Duration

	// discoverWindow/limits 是「从邮箱发现发件人」的扫描窗口：30 天、200 封。
	// 窗口比每日拉取宽——来源清单应当覆盖到账单邮件可能出现的频率。
	discoverWindow time.Duration
	discoverLimit  int

	ctx    context.Context
	cancel context.CancelFunc

	mu   sync.Mutex
	runs map[int64]*run
}

type run struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// 供 API 层映射成具体 HTTP 状态的错误。
var (
	ErrNotConfigured  = errors.New("尚未配置邮箱账单导入")
	ErrNoCredentials  = errors.New("邮箱地址或授权码未配置")
	errNoMasterKey    = errors.New("服务端未配置主密钥（TODO_CONFIG_KEY），无法使用已保存的授权码")
	errAuthCodeBroken = errors.New("授权码无法解密（主密钥可能已更换），请重新填写")
)

const (
	defaultWindow        = 7 * 24 * time.Hour
	defaultListLimit     = 50
	defaultPollEvery     = 24 * time.Hour
	defaultFirstRunIn    = time.Minute
	defaultDiscoverWin   = 30 * 24 * time.Hour
	defaultDiscoverLimit = 200
)

func NewManager(st *store.Store, box *secretbox.Box) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		st: st, box: box,
		imapAddr: os.Getenv("TODO_MAIL_IMAP_ADDR"),
		window:   defaultWindow, listLimit: defaultListLimit,
		pollEvery: defaultPollEvery, firstRunIn: defaultFirstRunIn,
		discoverWindow: defaultDiscoverWin, discoverLimit: defaultDiscoverLimit,
		ctx: ctx, cancel: cancel,
		runs: map[int64]*run{},
	}
}

// IsRunning 报告工作区的导入循环是否在跑。
func (m *Manager) IsRunning(wid int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.runs[wid]
	return ok
}

// Start 启动工作区的每日导入循环。已启动时幂等返回 nil。
func (m *Manager) Start(ctx context.Context, wid int64) error {
	m.mu.Lock()
	if _, ok := m.runs[wid]; ok {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()

	cfg, err := m.st.GetMailConfig(ctx, wid)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotConfigured
	}
	if err != nil {
		return err
	}
	if !cfg.Enabled {
		return errors.New("邮箱账单导入已停用")
	}
	// 同步检查凭据存在性：无凭据/已撤销一律不连接，立即如实失败
	// （启动端点据此回滚 enabled），而不是等一分钟后才在运行状态里看到失败。
	if cfg.Mailbox == "" || cfg.AuthCodeEnc == "" {
		return ErrNoCredentials
	}

	runCtx, cancel := context.WithCancel(m.ctx)
	r := &run{cancel: cancel, done: make(chan struct{})}

	m.mu.Lock()
	if _, ok := m.runs[wid]; ok { // 双检
		m.mu.Unlock()
		cancel()
		return nil
	}
	m.runs[wid] = r
	m.mu.Unlock()

	go func() {
		defer close(r.done)
		m.loop(runCtx, wid)
	}()
	log.Printf("mailimport: 工作区 %d 邮箱账单导入已启动（每日一次）", wid)
	return nil
}

// Stop 停止工作区的导入循环。
func (m *Manager) Stop(wid int64) {
	m.mu.Lock()
	r, ok := m.runs[wid]
	if ok {
		delete(m.runs, wid)
	}
	m.mu.Unlock()
	if !ok {
		return
	}
	r.cancel()
	log.Printf("mailimport: 工作区 %d 邮箱账单导入已停止", wid)
}

// SyncAll 进程启动时恢复所有「已启用」工作区的导入循环。
func (m *Manager) SyncAll(ctx context.Context) {
	ids, err := m.st.EnabledMailWorkspaceIDs(ctx)
	if err != nil {
		log.Printf("mailimport: 读取启用的邮箱导入失败：%v", err)
		return
	}
	for _, wid := range ids {
		if err := m.Start(ctx, wid); err != nil {
			log.Printf("mailimport: 恢复工作区 %d 邮箱导入失败：%v", wid, err)
		}
	}
}

// Close 停止全部循环并等待退出（进程优雅关闭时调用）。
func (m *Manager) Close() {
	m.cancel()
	m.mu.Lock()
	runs := make([]*run, 0, len(m.runs))
	for _, r := range m.runs {
		runs = append(runs, r)
	}
	m.runs = map[int64]*run{}
	m.mu.Unlock()
	for _, r := range runs {
		r.cancel()
	}
	done := make(chan struct{})
	go func() {
		for _, r := range runs {
			<-r.done
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		log.Printf("mailimport: 部分导入循环未在 2 秒内退出")
	}
}

// loop 每日循环：启动后先补一轮（保证重启当天的账单不丢），再按周期跑。
func (m *Manager) loop(ctx context.Context, wid int64) {
	timer := time.NewTimer(m.firstRunIn)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if err := m.RunOnce(ctx, wid); err != nil {
				// 失败已记入运行状态（设置页可见），这里只留日志。
				log.Printf("mailimport: 工作区 %d 本轮导入失败：%v", wid, err)
			}
			timer.Reset(m.pollEvery)
		}
	}
}

// RunOnce 执行一轮导入（启动补轮与每日轮询共用，测试也直接调它）。
// 只把「本轮整体失败」作为 error 返回；单封邮件的处理失败记入运行状态
// 且不中断其余邮件。
func (m *Manager) RunOnce(ctx context.Context, wid int64) error {
	cfg, err := m.st.GetMailConfig(ctx, wid)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotConfigured
	}
	if err != nil {
		return err
	}

	ownerID, err := m.st.WorkspaceOwnerID(ctx, wid)
	if err != nil {
		return err
	}

	// 凭据解析：邮箱地址 + 授权码。无凭据/已撤销不连接；密文无法解密
	// 是「暂时取不到」之外的硬失败，如实记录并返回。
	mailbox, authCode, cerr := m.credentials(ctx, cfg)
	if cerr != nil {
		m.recordRun(ctx, wid, cerr)
		return cerr
	}

	// Kimi 凭据共用助手配置：没配就连解析都谈不上，如实失败。
	kimi, err := m.kimi(ctx, wid)
	if err != nil {
		m.recordRun(ctx, wid, err)
		return err
	}

	loc, err := m.location(ctx, wid)
	if err != nil {
		return err
	}

	sess, err := m.transport().Open(ctx, mailbox, authCode, loc)
	if err != nil {
		m.recordRun(ctx, wid, err)
		return err
	}
	defer sess.Close()

	msgs, err := sess.List(ctx, time.Now().Add(-m.window), m.listLimit)
	if err != nil {
		m.recordRun(ctx, wid, err)
		return err
	}

	senders := splitSenders(cfg.Senders)
	now := time.Now()
	imported, skipped, parseFailed := 0, 0, 0
	for _, msg := range msgs {
		if ctx.Err() != nil {
			break
		}
		seen, err := m.st.MailSeen(ctx, wid, msg.ID)
		if err != nil || seen {
			continue
		}
		// 预筛只按「账单邮件来源」：配了发件人就只放行 From 命中的邮件；
		// 没配则窗口内全部交给 Kimi 判定（is_bill 把关，营销邮件标记
		// seen 后不再回看）。命中与否都只是粗筛，是否账单以 Kimi 为准。
		if len(senders) > 0 && !matchSenders(senders, msg.From) {
			if err := m.st.MarkMailSeen(ctx, wid, msg.ID, 0); err != nil {
				log.Printf("mailimport: 标记邮件失败 %s：%v", msg.ID, err)
			}
			skipped++
			continue
		}
		full, err := sess.Read(ctx, msg.ID)
		if err != nil {
			// 单封读取失败（如已被删除）不拖垮整轮。
			log.Printf("mailimport: 读取邮件 %s 失败：%v", msg.ID, err)
			parseFailed++
			continue
		}
		bill, err := kimi.ParseBill(ctx, full.From, full.Subject, full.Body, now, loc)
		if err != nil {
			log.Printf("mailimport: 解析邮件 %s 失败：%v", msg.ID, err)
			parseFailed++
			continue
		}
		if !bill.IsBill {
			if err := m.st.MarkMailSeen(ctx, wid, msg.ID, 0); err != nil {
				log.Printf("mailimport: 标记邮件失败 %s：%v", msg.ID, err)
			}
			skipped++
			continue
		}
		entryID, err := m.importBill(ctx, wid, ownerID, msg, full, bill)
		if err != nil {
			log.Printf("mailimport: 邮件 %s 落账失败：%v", msg.ID, err)
			parseFailed++
			continue
		}
		if err := m.st.MarkMailSeen(ctx, wid, msg.ID, entryID); err != nil {
			log.Printf("mailimport: 标记邮件失败 %s：%v", msg.ID, err)
		}
		imported++
	}

	errMsg := ""
	if parseFailed > 0 {
		errMsg = fmt.Sprintf("%d 封邮件处理失败（下次运行会重试未标记的）", parseFailed)
	}
	if err := m.st.RecordMailRun(ctx, wid, parseFailed == 0, errMsg, imported); err != nil {
		log.Printf("mailimport: 记录运行状态失败：%v", err)
	}
	log.Printf("mailimport: 工作区 %d 本轮完成：导入 %d，跳过 %d，失败 %d", wid, imported, skipped, parseFailed)
	return nil
}

// Test 验证一组凭据能否登录 IMAP（设置页「测试连接」）。mailbox/authCode
// 为空时回退到已保存的配置；都没有则返回 ErrNoCredentials。
// 成功返回实际使用的邮箱地址。
func (m *Manager) Test(ctx context.Context, wid int64, mailbox, authCode string) (string, error) {
	if strings.TrimSpace(mailbox) == "" || strings.TrimSpace(authCode) == "" {
		cfg, err := m.st.GetMailConfig(ctx, wid)
		if errors.Is(err, store.ErrNotFound) {
			return "", ErrNoCredentials
		}
		if err != nil {
			return "", err
		}
		if mailbox == "" {
			mailbox = cfg.Mailbox
		}
		if authCode == "" {
			_, code, cerr := m.credentials(ctx, cfg)
			if cerr != nil {
				return "", cerr
			}
			authCode = code
		}
	}
	if mailbox == "" || authCode == "" {
		return "", ErrNoCredentials
	}
	loc, err := m.location(ctx, wid)
	if err != nil {
		return "", err
	}
	sess, err := m.transport().Open(ctx, mailbox, authCode, loc)
	if err != nil {
		return "", err
	}
	sess.Close()
	return mailbox, nil
}

func (m *Manager) transport() *Transport {
	return &Transport{Addr: m.imapAddr, TLSConfig: m.tlsConfig}
}

// credentials 从配置行解析出明文凭据。三态规则：无凭据/已撤销（空串）
// 返回 ErrNoCredentials；主密钥缺失/密文损坏如实返回对应错误。
func (m *Manager) credentials(ctx context.Context, cfg *store.MailConfig) (string, string, error) {
	if cfg.Mailbox == "" || cfg.AuthCodeEnc == "" {
		return "", "", ErrNoCredentials
	}
	if m.box == nil {
		return "", "", errNoMasterKey
	}
	code, err := m.box.Unseal(cfg.AuthCodeEnc)
	if err != nil || code == "" {
		return "", "", errAuthCodeBroken
	}
	return cfg.Mailbox, code, nil
}

// importBill 把一笔解析结果落进账本。client_id 用 mail:<UID>：
// 即使 mail_seen 表丢过、邮件被重放，CreateLedgerEntry 的幂等键也保证
// 不会产生重复账目。日期回退顺序：交易日期 → 邮件日期 → 今天。
func (m *Manager) importBill(ctx context.Context, wid, ownerID int64, msg Message, full *FullMessage, bill parse.Bill) (int64, error) {
	category := bill.Category
	if category == "" {
		category = "其他" // 用户指定：解析出的分类先归「其他」，在账本页可再改
	}
	date := bill.Date
	if date == "" {
		date = strings.TrimSpace(full.Date)
	}
	if !validYYYYMMDD(date) {
		date = time.Now().Format("2006-01-02")
	}
	// 备注保留商户与原主题，方便在账本页核对来源。
	note := strings.TrimSpace(strings.Join([]string{bill.Merchant, full.Subject}, " · "))
	if len([]rune(note)) > 240 {
		note = string([]rune(note)[:240])
	}
	e, err := m.st.CreateLedgerEntry(ctx, wid, ownerID, store.LedgerInput{
		Date:      date,
		Type:      bill.Type,
		Category:  category,
		AmountFen: bill.AmountFen,
		Note:      note,
		ClientID:  "mail:" + msg.ID,
	})
	if err != nil {
		return 0, err
	}
	return e.ID, nil
}

// recordRun 把本轮失败写进配置行（设置页可见）。
func (m *Manager) recordRun(ctx context.Context, wid int64, runErr error) {
	msg := runErr.Error()
	if errors.Is(runErr, ErrAuth) {
		msg = "邮箱授权码无效或 IMAP 服务未开启：请到邮箱设置核对授权码，并确认已开启 IMAP/SMTP 服务"
	}
	if err := m.st.RecordMailRun(ctx, wid, false, msg, 0); err != nil {
		log.Printf("mailimport: 记录运行状态失败：%v", err)
	}
}

// kimi 从助手配置取 Kimi 凭据（用户确认：与助手共用一份 LLM 配置）。
// 没有助手配置 / 没有 Key / 主密钥缺失，都如实返回错误，不静默跳过解析。
func (m *Manager) kimi(ctx context.Context, wid int64) (*parse.Kimi, error) {
	cfg, err := m.st.GetAssistantConfig(ctx, wid)
	if errors.Is(err, store.ErrNotFound) {
		return nil, fmt.Errorf("未配置 Kimi：请在「LLM API 配置」里填写 Kimi API Key")
	}
	if err != nil {
		return nil, err
	}
	if cfg.KimiAPIKeyEnc == "" {
		return nil, fmt.Errorf("未配置 Kimi：请在「LLM API 配置」里填写 Kimi API Key")
	}
	if m.box == nil {
		return nil, fmt.Errorf("服务端未配置主密钥（TODO_CONFIG_KEY），无法解密 Kimi Key")
	}
	key, err := m.box.Unseal(cfg.KimiAPIKeyEnc)
	if err != nil || key == "" {
		return nil, fmt.Errorf("Kimi Key 无法解密（主密钥可能已更换），请在设置页重新填写")
	}
	return parse.NewKimi(key, cfg.KimiBase, cfg.KimiModel, 30*time.Second), nil
}

func (m *Manager) location(ctx context.Context, wid int64) (*time.Location, error) {
	tz, err := m.st.WorkspaceTimezone(ctx, wid)
	if err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		log.Printf("mailimport: 时区 %q 无法识别，改用 Asia/Shanghai", tz)
		loc, _ = time.LoadLocation("Asia/Shanghai")
	}
	return loc, nil
}

// splitSenders 把 "a@bank.com|bank.com" 拆成去空、按小写去重的来源清单。
// 没有默认来源——空配置 = 未配置来源，窗口内全部交给 Kimi 判定。
func splitSenders(raw string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, "|") {
		p := strings.TrimSpace(part)
		if p == "" {
			continue
		}
		k := strings.ToLower(p)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, p)
	}
	return out
}

// matchSenders 判断 From 是否命中任一已配来源：小写不敏感子串匹配。
// From 可能是「Name <addr>」形态，子串对完整地址（service@bank.com）
// 与域名（bank.com）两种配法都适用。
func matchSenders(senders []string, from string) bool {
	if len(senders) == 0 || from == "" {
		return false
	}
	hay := strings.ToLower(from)
	for _, s := range senders {
		if s != "" && strings.Contains(hay, strings.ToLower(s)) {
			return true
		}
	}
	return false
}

// DiscoveredSender 是「从邮箱发现」返回的一个发件来源：Count 与 Sample
// （最近一封的主题）帮助用户在设置页辨认这是账单来源还是营销来源。
type DiscoveredSender struct {
	From   string `json:"from"`
	Count  int    `json:"count"`
	Sample string `json:"sample"`
}

// Discover 用已存（或表单）凭据连一次 IMAP，列出扫描窗口内出现过的
// 发件人，供用户勾选「账单邮件来源」。只列信封不取正文、不标记任何
// 邮件，凭据语义与 Test 完全一致（留空回退已存配置，都没有如实失败）。
func (m *Manager) Discover(ctx context.Context, wid int64, mailbox, authCode string) ([]DiscoveredSender, error) {
	if strings.TrimSpace(mailbox) == "" || strings.TrimSpace(authCode) == "" {
		cfg, err := m.st.GetMailConfig(ctx, wid)
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrNoCredentials
		}
		if err != nil {
			return nil, err
		}
		if mailbox == "" {
			mailbox = cfg.Mailbox
		}
		if authCode == "" {
			_, code, cerr := m.credentials(ctx, cfg)
			if cerr != nil {
				return nil, cerr
			}
			authCode = code
		}
	}
	if mailbox == "" || authCode == "" {
		return nil, ErrNoCredentials
	}
	loc, err := m.location(ctx, wid)
	if err != nil {
		return nil, err
	}
	sess, err := m.transport().Open(ctx, mailbox, authCode, loc)
	if err != nil {
		return nil, err
	}
	defer sess.Close()
	msgs, err := sess.List(ctx, time.Now().Add(-m.discoverWindow), m.discoverLimit)
	if err != nil {
		return nil, err
	}
	// 聚合：按小写 From 去重；List 按 UID 升序返回，后见者更新，
	// Sample 即窗口内最近一封的主题。
	agg := map[string]*DiscoveredSender{}
	order := []string{}
	for _, msg := range msgs {
		key := strings.ToLower(msg.From)
		d, ok := agg[key]
		if !ok {
			d = &DiscoveredSender{From: msg.From}
			agg[key] = d
			order = append(order, key)
		}
		d.Count++
		d.Sample = msg.Subject
	}
	out := make([]DiscoveredSender, 0, len(order))
	for _, k := range order {
		out = append(out, *agg[k])
	}
	return out, nil
}

func validYYYYMMDD(s string) bool {
	if len(s) != 10 || s[4] != '-' || s[7] != '-' {
		return false
	}
	_, err := time.ParseInLocation("2006-01-02", s, time.UTC)
	return err == nil
}
