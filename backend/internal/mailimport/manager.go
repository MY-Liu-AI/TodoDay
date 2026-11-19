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

// Manager 内嵌运行邮箱账单导入：每个工作区一条后台 goroutine，挂一条
// IMAP IDLE 长连接主动监听——新邮件到达（EXISTS 推送）即增量拉取，
// Kimi 解析后落账本。与助手 Manager 同构：enabled 是持久化意图，
// running 是此刻有没有在跑。
//
// 凭据来源（与助手同一套三态规则）：邮箱地址 + IMAP 授权码以密文落库。
// 「已停用」「无凭据／已被撤销」一律不连接——Start 同步检查凭据存在性，
// 缺失即失败；监听期间心跳周期性复查配置，撤销/停用对已建立的连接
// 生效（主动断开并退出），不存在「所有者以为删了、监听还在跑」。只有
// 「密文暂时无法解密/配置暂时读不到」这类情况才沿用旧连接或按退避重试。
type Manager struct {
	st  *store.Store
	box *secretbox.Box

	// imapAddr 是 IMAP 服务端点，默认 imap.qq.com:993（腾讯邮箱）；
	// 可用 TODO_MAIL_IMAP_ADDR 覆盖；测试直接改这个字段注入 memserver。
	imapAddr  string
	tlsConfig *tls.Config // 测试注入自签 CA；生产走系统根证书

	// window/limits 是每轮增量扫描的窗口：7 天、50 封上限。新邮件唤醒时
	// 以 mail_seen 去重，窗口只需覆盖「扫描间隔内可能积压的量」。设成
	// 字段以便测试收紧。
	window    time.Duration
	listLimit int

	// heartbeatEvery 是监听期间的心跳复查周期（凭据撤销/停用/变更对
	// 已建立连接生效的落点，与助手心跳同一安全不变式）。连接级失败按
	// retryInitial 起步指数退避重连，封顶 retryMax。
	heartbeatEvery time.Duration
	retryInitial   time.Duration
	retryMax       time.Duration

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

// run/serve 之间的私有哨兵：errRevoked = 凭据被撤销/已停用，监听彻底退出
// （不再重连）；errCredsChanged = 邮箱地址/授权码已变更，关闭当前连接换
// 新凭据重连。两者都不写入运行状态——不是失败，是状态迁移。
var (
	errRevoked      = errors.New("邮箱账单导入已停用或凭据已撤销")
	errCredsChanged = errors.New("邮箱凭据已变更")
)

const (
	defaultWindow        = 7 * 24 * time.Hour
	defaultListLimit     = 50
	defaultHeartbeat     = time.Minute
	defaultRetryInitial  = 5 * time.Second
	defaultRetryMax      = 5 * time.Minute
	defaultDiscoverWin   = 30 * 24 * time.Hour
	defaultDiscoverLimit = 200
	// maxConsecFailures 是防重试风暴的熔断：连续 N 次连接/登录失败就自动
	// 停止导入（持久化 enabled=false，重启也不再拉起）。服务商对「频繁
	// 失败连接」有反滥用限流（静默吊起/断连），无限退避重试会把工作区 IP
	// 喂进黑名单；自动停止 + 设置页留痕让用户核对后手动再启动。
	maxConsecFailures = 5
	// healthyServe 是「健康连接」的判定线：一次 serve 存活满这个时长后
	// 断开，失败计数与退避都归零，按首次失败重计。QQ 每约 2 小时掐一次
	// IDLE 长连接是常规行为（2026-11-18 实发：重连每次都成功，但计数
	// 不清零，四次常规断连 + 一次触发被误熔断、导入半夜停摆）；真重试
	// 风暴单次只活约 30 秒（建连看门狗封顶），远在阈值之下不受影响。
	healthyServe = 15 * time.Minute
)

func NewManager(st *store.Store, box *secretbox.Box) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		st: st, box: box,
		imapAddr: os.Getenv("TODO_MAIL_IMAP_ADDR"),
		window:   defaultWindow, listLimit: defaultListLimit,
		heartbeatEvery: defaultHeartbeat,
		retryInitial:   defaultRetryInitial, retryMax: defaultRetryMax,
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

	go m.run(runCtx, wid, r)
	log.Printf("mailimport: 工作区 %d 邮箱账单导入已启动（IDLE 主动监听）", wid)
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

// run 是工作区监听的完整生命周期：一条连接 serve 到底，连接级失败按
// 指数退避重连；凭据撤销/停用立即退出并自行从 runs 摘除（removeRun 只
// 摘仍是自己的条目——Stop/Close 可能已经摘掉）。失败记录遵循「谁发现谁
// 记录」：serve/scan 在失败发生处写运行状态，这里只负责调度。
func (m *Manager) run(ctx context.Context, wid int64, r *run) {
	defer close(r.done)
	defer m.removeRun(wid, r)
	backoff := m.retryInitial
	consec := 0
	for {
		if ctx.Err() != nil {
			return
		}
		started := time.Now()
		err := m.serve(ctx, wid)
		if ctx.Err() != nil {
			return
		}
		// 健康运行过的连接断开不算「连续」失败（见 healthyServe 注释），
		// 先归零再进分支计数。
		if time.Since(started) >= healthyServe {
			consec = 0
			backoff = m.retryInitial
		}
		switch {
		case err == nil:
			return
		case errors.Is(err, errRevoked):
			// 撤销/停用：如实留痕（设置页可见监听为何停止）并退出，不重连。
			m.recordRun(ctx, wid, err)
			log.Printf("mailimport: 工作区 %d 监听退出：凭据已撤销或已停用", wid)
			return
		case errors.Is(err, errCredsChanged):
			log.Printf("mailimport: 工作区 %d 凭据已变更，换新凭据重连", wid)
			backoff = m.retryInitial
			consec = 0
		case errors.Is(err, ErrAuth):
			// 认证失败重试无意义，持续重试还会触发服务商限流：如实留痕、
			// 持久化停用（进程重启不再自动拉起），核对凭据后手动启动。
			m.recordRun(ctx, wid, errors.New("邮箱授权码无效或 IMAP 服务未开启，导入已自动停止：请核对授权码并确认已开启 IMAP/SMTP 服务，然后在设置页重新启动"))
			if derr := m.st.SetMailEnabled(ctx, wid, false); derr != nil {
				log.Printf("mailimport: 记录停止状态失败：%v", derr)
			}
			log.Printf("mailimport: 工作区 %d 认证失败，已自动停止导入（防重试风暴）", wid)
			return
		default:
			consec++
			if consec >= maxConsecFailures {
				m.recordRun(ctx, wid, errors.New("连续多次连接失败，导入已自动停止：可能是网络异常或邮箱服务商临时限流，请稍后在设置页重新启动"))
				if derr := m.st.SetMailEnabled(ctx, wid, false); derr != nil {
					log.Printf("mailimport: 记录停止状态失败：%v", derr)
				}
				log.Printf("mailimport: 工作区 %d 连续 %d 次连接失败，已自动停止导入（防重试风暴）：%v", wid, consec, err)
				return
			}
			log.Printf("mailimport: 工作区 %d 连接中断，%v 后重连：%v", wid, backoff, err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < m.retryMax {
			backoff *= 2
			if backoff > m.retryMax {
				backoff = m.retryMax
			}
		}
	}
}

// removeRun 让退出的 run 自己从 runs 摘除（只摘除仍是自己——Stop/Close
// 可能已经摘掉并换上了新 run，不能误删）。
func (m *Manager) removeRun(wid int64, r *run) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cur, ok := m.runs[wid]; ok && cur == r {
		delete(m.runs, wid)
	}
}

// serve 单条 IMAP 连接的生命周期：先补一轮（重启/断线期间攒下的邮件不
// 因换连接而丢），再进入 IDLE 主动监听——新邮件到达（EXISTS 推送）即增
// 量导入；心跳周期性复查凭据，撤销/停用生效于当前连接，变更触发重连。
// 返回：nil = ctx 取消正常退出；errRevoked = 撤销/停用；errCredsChanged =
// 凭据变更；其余 = 连接级/扫描级失败，外层按退避重连。
func (m *Manager) serve(ctx context.Context, wid int64) error {
	cfg, err := m.st.GetMailConfig(ctx, wid)
	if errors.Is(err, store.ErrNotFound) {
		return errRevoked
	}
	if err != nil {
		return err
	}
	if !cfg.Enabled {
		return errRevoked
	}
	// 凭据解析（三态规则见 credentials）：无凭据/已撤销 → 退出监听；
	// 主密钥缺失/密文损坏如实记录后按退避重试（用户可能在服务端补配）。
	mailbox, authCode, cerr := m.credentials(ctx, cfg)
	if cerr != nil {
		m.recordRun(ctx, wid, cerr)
		if errors.Is(cerr, ErrNoCredentials) {
			return errRevoked
		}
		return cerr
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

	if err := m.scan(ctx, wid, sess, cfg); err != nil {
		return err
	}

	hb := time.NewTicker(m.heartbeatEvery)
	defer hb.Stop()
	for {
		idle, err := sess.Idle()
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			_ = idle.Close()
			return nil
		case <-sess.Notify():
			if err := idle.Close(); err != nil {
				return err
			}
			// Wait 等服务器确认 IDLE 结束再发命令；连接已死则返回重连。
			if err := sess.WaitIdle(idle); err != nil {
				return err
			}
			// 增量扫描；扫描期间后到的推送会留在 Notify（容量 1），
			// 下一轮 select 立即再扫一轮，窗口内不丢邮件。
			if err := m.scan(ctx, wid, sess, cfg); err != nil {
				return err
			}
		case <-hb.C:
			if err := idle.Close(); err != nil {
				return err
			}
			if err := sess.WaitIdle(idle); err != nil {
				return err
			}
			// 心跳复查凭据：撤销必须对已建立的连接生效。
			if err := m.recheck(ctx, wid, cfg.Mailbox, cfg.AuthCodeEnc); err != nil {
				return err
			}
		}
	}
}

// recheck 是监听期间的心跳复查（与助手心跳同一安全不变式）：停用/
// 无凭据/已撤销 → errRevoked（主动断开并退出）；邮箱地址或授权码密文
// 变了 → errCredsChanged（换新凭据重连）。AES-GCM 随机 nonce，重存即
// 变，密文相同即凭据未动，无需解密。读 store 暂时失败沿用旧连接——
// 把暂时故障误判成撤销是 fail-open，反过来才对。
func (m *Manager) recheck(ctx context.Context, wid int64, mailbox, authCodeEnc string) error {
	cfg, err := m.st.GetMailConfig(ctx, wid)
	if errors.Is(err, store.ErrNotFound) {
		return errRevoked
	}
	if err != nil {
		log.Printf("mailimport: 工作区 %d 心跳复查读配置失败（沿用旧连接）：%v", wid, err)
		return nil
	}
	if !cfg.Enabled || cfg.Mailbox == "" || cfg.AuthCodeEnc == "" {
		return errRevoked
	}
	if cfg.Mailbox != mailbox || cfg.AuthCodeEnc != authCodeEnc {
		return errCredsChanged
	}
	return nil
}

// RunOnce 执行一轮导入：建一条临时会话补扫窗口内未处理的邮件。运行期
// 不走这里——serve 的长连接先补轮、之后靠 IDLE 唤醒逐轮 scan；保留它
// 是给测试与手动触发一个入口。
func (m *Manager) RunOnce(ctx context.Context, wid int64) error {
	cfg, err := m.st.GetMailConfig(ctx, wid)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotConfigured
	}
	if err != nil {
		return err
	}

	// 凭据解析：无凭据/已撤销不连接；密文无法解密是硬失败，如实记录。
	mailbox, authCode, cerr := m.credentials(ctx, cfg)
	if cerr != nil {
		m.recordRun(ctx, wid, cerr)
		return cerr
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

	return m.scan(ctx, wid, sess, cfg)
}

// scan 用一条已建立的会话跑一轮增量导入：serve 的补轮与 IDLE 唤醒、
// RunOnce 都走这里。只把「本轮整体失败」作为 error 返回；单封邮件的
// 处理失败记入运行状态且不中断其余邮件。
func (m *Manager) scan(ctx context.Context, wid int64, sess *Session, cfg *store.MailConfig) error {
	ownerID, err := m.st.WorkspaceOwnerID(ctx, wid)
	if err != nil {
		return err
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

	msgs, err := sess.List(ctx, time.Now().Add(-m.window), m.listLimit)
	if err != nil {
		m.recordRun(ctx, wid, err)
		return err
	}

	senders := splitSenders(cfg.Senders)
	// 分类清单驱动解析收敛：Kimi 只能从这份工作区分类里选（用户在前端
	// 调整分类后，后续解析随之生效）。读取失败不拖垮整轮——用兜底清单。
	cats, err := m.st.ListLedgerCategories(ctx, wid)
	if err != nil {
		log.Printf("mailimport: 读取分类清单失败，本轮用默认分类：%v", err)
		cats = nil
	}
	catNames := make([]string, len(cats))
	for i, c := range cats {
		catNames[i] = c.Name
	}
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
		bill, err := kimi.ParseBill(ctx, full.From, full.Subject, full.Body, now, loc, catNames)
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
