// Package mailimport 通过原生 IMAP（邮箱地址 + 服务商授权码）主动监听
// 账单邮件：长连接挂 IDLE，新邮件到达（EXISTS 推送）即增量解析落进账本。
//
// 凭据边界：授权码由用户在网页设置页填写，服务端以 AES-256-GCM 密文
// （主密钥 TODO_CONFIG_KEY）落库；本包只在运行时用密文解密出明文去 LOGIN，
// 明文不出现在任何视图与日志里。撤销 = 显式清除密文（clearAuthCode），
// 监听期间的心跳复查发现撤销/停用会主动断开——不存在「删了还在跑」。
package mailimport

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/textproto"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// ErrAuth 表示 IMAP 登录失败：授权码无效、IMAP 服务未开启或密码错误。
// 这类错误重试无意义，如实记进运行状态，提示用户去邮箱设置核对。
var ErrAuth = errors.New("邮箱授权码无效或 IMAP 服务未开启")

const defaultIMAPAddr = "imap.qq.com:993"

// openHardTimeout 是建连全程（握手 → 问候 → LOGIN → SELECT）的封顶时长。
const openHardTimeout = 30 * time.Second

// Message 是邮件摘要（列表阶段）。
type Message struct {
	ID string // IMAP UID（十进制字符串）。UID 在邮箱内单调且唯一，
	// 与 UIDVALIDITY 组合即可稳定标识一封邮件；mail_seen 以此为幂等键。
	Subject string
	From    string
	Date    string // YYYY-MM-DD（工作区时区）
}

// FullMessage 是完整邮件（含正文）。
type FullMessage struct {
	Message
	To   []string
	Body string
}

// Transport 是一个 IMAP 服务端点。Addr 默认腾讯邮箱（imap.qq.com:993），
// 可用环境变量 TODO_MAIL_IMAP_ADDR 覆盖（测试注入本地 memserver）。
// TLSConfig 默认按系统根证书校验；测试可注入自签 CA。
type Transport struct {
	Addr      string
	Timeout   time.Duration // 单次命令超时；0 用 60s
	TLSConfig *tls.Config
}

func (t *Transport) addr() string {
	if t.Addr != "" {
		return t.Addr
	}
	return defaultIMAPAddr
}

func (t *Transport) timeout() time.Duration {
	if t.Timeout > 0 {
		return t.Timeout
	}
	return 60 * time.Second
}

// Session 是一条已登录并选中 INBOX 的连接。用完必须 Close。
//
// mail 是新邮件通知 channel（容量 1）：服务端推送 EXISTS（新邮件到达）
// 时收到信号。容量 1 是有意设计——监听期间的多封新邮件合并为一次通知，
// 唤醒方做增量扫描时以 mail_seen/账本幂等键兜底，不假设「一封一信号」；
// 而唤醒消费后 channel 必空，扫描期间后到的推送至多丢失信号不丢邮件
// （下一轮扫描覆盖窗口内全部未处理邮件）。
//
// 看门狗（beginCmd/endCmd 驱动）：go-imap 库自己会读读超时（30s），但
// 实测对「对端静默吊住」会连成 2×30s 才报错，Open 阶段实测挂过 15 分钟
// 直到服务端杀连接（deadline 被库的读循环清理由不得我们）。所以不用
// SetDeadline 与库抢——改为武装看门狗：命令进行中（armed）超时未续期
// 就直接 Close 连接，阻塞读立即报错返回。conn 关闭无法被吞。IDLE 长挂
// 期间不武装（心跳 1 分钟一轮自行收放），连接不受误伤。
type Session struct {
	c    *imapclient.Client
	tc   *tls.Conn
	loc  *time.Location
	mail chan struct{}

	wdStop chan struct{} // Close 时停看门狗
	wdKick chan struct{} // beginCmd/refreshCmd 续期（cap 1）
	wdArmed atomic.Bool  // 命令进行中：超时未续期 → Close 连接
}

// cmdTimeout 是单条 IMAP 命令（SEARCH/FETCH/IDLE 收放）的硬超时，
// 与 List/Read 里 60s 的 ctx 语义一致——ctx 负责取消语义，看门狗
// 真正打断阻塞读。
const cmdTimeout = 60 * time.Second

// watchdog 是 Session 的看门狗 goroutine：armed 状态下 cmdTimeout 内
// 没有被续期（kick）就关闭连接。返回前一直运行，Close 时经 wdStop 退出。
func (s *Session) watchdog() {
	timer := time.NewTimer(cmdTimeout)
	defer timer.Stop()
	for {
		select {
		case <-s.wdStop:
			return
		case <-s.wdKick:
			if !timer.Stop() {
				<-timer.C
			}
			timer.Reset(cmdTimeout)
		case <-timer.C:
			if s.wdArmed.Load() {
				// 命令吊住：强断连接。库的所有 pending 命令立即收到
				// closed-conn 错误，扫描/监听向上抛错走重连。
				s.tc.Close()
				return
			}
			// 未武装（IDLE 长挂等推送）：空转再等一轮。
			timer.Reset(cmdTimeout)
		}
	}
}

// beginCmd 标记命令开始并续期看门狗；refreshCmd 在流式逐封读取时续期；
// endCmd 命令结束解除武装。
func (s *Session) beginCmd() {
	s.wdArmed.Store(true)
	select {
	case s.wdKick <- struct{}{}:
	default:
	}
}
func (s *Session) refreshCmd() { s.beginCmd() }
func (s *Session) endCmd()     { s.wdArmed.Store(false) }

// Open 建立 TLS 连接并 LOGIN + SELECT INBOX。登录失败映射为 ErrAuth。
func (t *Transport) Open(ctx context.Context, mailbox, authCode string, loc *time.Location) (*Session, error) {
	if loc == nil {
		loc = time.UTC
	}
	ctx, cancel := context.WithTimeout(ctx, t.timeout())
	defer cancel()

	tlsCfg := t.TLSConfig
	if tlsCfg == nil {
		host := t.addr()
		if i := strings.LastIndex(host, ":"); i > 0 {
			host = host[:i]
		}
		tlsCfg = &tls.Config{ServerName: host}
	}
	mail := make(chan struct{}, 1)
	d := net.Dialer{Timeout: 20 * time.Second}
	raw, err := d.DialContext(ctx, "tcp", t.addr())
	if err != nil {
		return nil, fmt.Errorf("连接邮箱服务器失败：%w", err)
	}
	tc := tls.Client(raw, tlsCfg)
	// 建连看门狗：握手 → 问候语 → LOGIN → SELECT 全程 openHardTimeout
	// 封顶。不用 SetDeadline——库的读循环会清 deadline 抢时序（实测把
	// 30s 拖成 60s、乃至挂 15 分钟直到服务端杀连接）；到点直接 Close
	// 连接，任何阶段的吊住都被打断且无法被吞。
	var timedOut atomic.Bool
	openDone := make(chan struct{})
	go func() {
		select {
		case <-openDone:
		case <-time.After(openHardTimeout):
			timedOut.Store(true)
			tc.Close()
		}
	}()
	closeOpenWatchdog := func() { close(openDone) }
	if err := tc.HandshakeContext(ctx); err != nil {
		closeOpenWatchdog()
		if timedOut.Load() {
			return nil, fmt.Errorf("连接邮箱服务器超时（%d 秒无响应）：网络异常或邮箱服务商临时限流", int(openHardTimeout/time.Second))
		}
		_ = raw.Close()
		return nil, fmt.Errorf("连接邮箱服务器失败（TLS）：%w", err)
	}
	c := imapclient.New(tc, &imapclient.Options{
		// 只认 EXISTS（NumMessages）：邮箱里别的变化（标记、移动）不产生
		// 账单；EXPUNGE 走单独的 handler，不在这里。回调跑在解码 goroutine
		// 上，绝不能阻塞——cap-1 + default 丢弃，保住的连接比多扫一轮重要。
		UnilateralDataHandler: &imapclient.UnilateralDataHandler{
			Mailbox: func(data *imapclient.UnilateralDataMailbox) {
				if data.NumMessages == nil {
					return
				}
				select {
				case mail <- struct{}{}:
				default:
				}
			},
		},
	})
	// 必须等问候语落袋再发命令：imapclient.New 不等 greeting 就返回，
	// 而 QQ 邮箱会静默丢弃先于问候语到达的命令（连接随后被吊死到超时，
	// 2026-11-17 矩阵探测实锤：greeting 后写 LOGIN 秒回，先写 LOGIN
	// 再读 greeting 则 12s 零响应）。其他服务端（ Dovecot/memserver ）
	// 不挑时序，但等 greeting 是 RFC 3501 的保守做法，对所有服务端安全。
	if err := c.WaitGreeting(); err != nil {
		closeOpenWatchdog()
		_ = tc.Close()
		if timedOut.Load() {
			return nil, fmt.Errorf("连接邮箱服务器超时（%d 秒无响应）：网络异常或邮箱服务商临时限流", int(openHardTimeout/time.Second))
		}
		return nil, fmt.Errorf("读取邮箱问候语失败：%w", err)
	}
	if err := c.Login(mailbox, authCode).Wait(); err != nil {
		closeOpenWatchdog()
		_ = tc.Close()
		if timedOut.Load() {
			return nil, fmt.Errorf("连接邮箱服务器超时（%d 秒无响应）：网络异常或邮箱服务商临时限流", int(openHardTimeout/time.Second))
		}
		var imapErr *imap.Error
		if errors.As(err, &imapErr) && imapErr.Type == imap.StatusResponseTypeNo {
			return nil, fmt.Errorf("%w：%s", ErrAuth, imapErr.Text)
		}
		return nil, fmt.Errorf("邮箱登录失败：%w", err)
	}
	if _, err := c.Select("INBOX", nil).Wait(); err != nil {
		closeOpenWatchdog()
		_ = c.Logout().Wait()
		_ = tc.Close()
		if timedOut.Load() {
			return nil, fmt.Errorf("连接邮箱服务器超时（%d 秒无响应）：网络异常或邮箱服务商临时限流", int(openHardTimeout/time.Second))
		}
		return nil, fmt.Errorf("打开收件箱失败：%w", err)
	}
	closeOpenWatchdog()
	s := &Session{c: c, tc: tc, loc: loc, mail: mail, wdStop: make(chan struct{}), wdKick: make(chan struct{}, 1)}
	go s.watchdog()
	return s, nil
}

// Notify 返回新邮件通知 channel：服务端推送新邮件（EXISTS）时收到信号。
// 容量 1，爆发更新合并为一次；消费方应做增量扫描而不是假设一封。
func (s *Session) Notify() <-chan struct{} { return s.mail }

// Idle 进入 IDLE 主动监听，返回进行中的监听句柄。服务端推送新邮件时
// Notify 收到信号；库内部每 28 分钟自动重发 IDLE 保活。IDLE 期间同一
// 连接不能发其他命令——调用方必须先 Close（+WaitIdle 等服务器确认）
// 再扫描。
//
// 库在收到 IDLE 的 continuation(+) 后才返回，因此这里收放硬超时安全：
// 进入成功后连接转长挂（等推送），deadline 已清除。
func (s *Session) Idle() (*Idle, error) {
	s.beginCmd()
	cmd, err := s.c.Idle()
	if err != nil {
		s.endCmd()
		return nil, fmt.Errorf("进入 IDLE 监听失败：%w", err)
	}
	s.endCmd()
	return &Idle{cmd: cmd}, nil
}

// WaitIdle 带硬超时地等服务器确认 IDLE 结束（发下一条命令前必须完成）。
// 对端吊住时 deadline 强制打断，错误向上抛给重连逻辑。
func (s *Session) WaitIdle(i *Idle) error {
	s.beginCmd()
	defer s.endCmd()
	return i.Wait()
}

// Idle 是进行中的 IDLE 监听。Close 停止监听；Wait 等服务器确认（发其他
// 命令前调用，连接已死时返回错误，调用方应重连而不是继续用）。
type Idle struct{ cmd *imapclient.IdleCommand }

func (i *Idle) Close() error { return i.cmd.Close() }
func (i *Idle) Wait() error  { return i.cmd.Wait() }

// Close 优雅登出并断开。重复调用安全。Logout 也可能被对端吊住——交给
// 看门狗咬断（武装 60s），不再用 deadline 硬抢。
func (s *Session) Close() {
	if s.c == nil {
		return
	}
	s.beginCmd()
	defer s.endCmd()
	_ = s.c.Logout().Wait()
	_ = s.c.Close()
	s.c = nil
	close(s.wdStop)
}

// List 列出 INBOX 里内部日期晚于 since 的邮件（最多 limit 封）。
// 账单导入只要增量窗口，不翻页：单次 daily run 窗口 7 天、上限 50 封，
// 超出留到明天也算符合「每日一次」的语义。
func (s *Session) List(ctx context.Context, since time.Time, limit int) ([]Message, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	s.beginCmd()
	defer s.endCmd()

	res, err := s.c.UIDSearch(&imap.SearchCriteria{Since: since}, nil).Wait()
	if err != nil {
		return nil, fmt.Errorf("搜索邮件失败：%w", err)
	}
	uids := res.AllUIDs()
	if len(uids) == 0 {
		return nil, nil
	}
	if len(uids) > limit {
		uids = uids[len(uids)-limit:]
	}

	cmd := s.c.Fetch(imap.UIDSetNum(uids...), &imap.FetchOptions{
		UID:      true,
		Envelope: true,
	})
	defer cmd.Close()

	out := []Message{}
	for {
		s.refreshCmd() // 逐封续期：慢速滴水不能把整轮拖死
		msg := cmd.Next()
		if msg == nil {
			break
		}
		buf, err := msg.Collect()
		if err != nil {
			return nil, fmt.Errorf("读取邮件摘要失败：%w", err)
		}
		if buf.Envelope == nil {
			continue
		}
		env := buf.Envelope
		date := env.Date.In(s.loc).Format("2006-01-02")
		out = append(out, Message{
			ID:      fmt.Sprintf("%d", buf.UID),
			Subject: decodeWords(env.Subject),
			From:    formatAddresses(env.From),
			Date:    date,
		})
	}
	if err := cmd.Close(); err != nil {
		return nil, fmt.Errorf("读取邮件摘要失败：%w", err)
	}
	// UIDSearch 按 UID 升序返回，limit 截断保留最新的，顺序本身无所谓
	// （逐封独立处理），这里不再排序。
	return out, nil
}

// Read 读取一封完整邮件（含正文）。body 取 text/plain，没有则对
// text/html 做极简去标签——解析只需让 Kimi 看到金额与商户，不需要渲染。
func (s *Session) Read(ctx context.Context, id string) (*FullMessage, error) {
	var uid uint32
	if _, err := fmt.Sscanf(id, "%d", &uid); err != nil || uid == 0 {
		return nil, fmt.Errorf("非法邮件 UID：%q", id)
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	s.beginCmd()
	defer s.endCmd()

	cmd := s.c.Fetch(imap.UIDSetNum(imap.UID(uid)), &imap.FetchOptions{
		UID:      true,
		Envelope: true,
		// 空 BodySection 表示整封邮件（BODY.PEEK[]）。
		BodySection: []*imap.FetchItemBodySection{{Peek: true}},
	})
	defer cmd.Close()

	msg := cmd.Next()
	if msg == nil {
		if err := cmd.Close(); err != nil {
			return nil, fmt.Errorf("读取邮件失败：%w", err)
		}
		return nil, fmt.Errorf("邮件 %s 不存在", id)
	}
	buf, err := msg.Collect()
	if err != nil {
		return nil, fmt.Errorf("读取邮件失败：%w", err)
	}
	if err := cmd.Close(); err != nil {
		return nil, fmt.Errorf("读取邮件失败：%w", err)
	}
	if buf.Envelope == nil {
		return nil, fmt.Errorf("邮件 %s 缺少信封信息", id)
	}

	full := &FullMessage{
		Message: Message{
			ID:      id,
			Subject: decodeWords(buf.Envelope.Subject),
			From:    formatAddresses(buf.Envelope.From),
			Date:    buf.Envelope.Date.In(s.loc).Format("2006-01-02"),
		},
	}
	for _, addr := range buf.Envelope.To {
		full.To = append(full.To, formatAddress(addr))
	}
	for _, bs := range buf.BodySection {
		if len(bs.Bytes) > 0 {
			full.Body = extractBody(bs.Bytes)
			break
		}
	}
	return full, nil
}

// formatAddress 渲染一个地址为 "Name <addr>" 或裸地址。
func formatAddress(a imap.Address) string {
	addr := a.Mailbox
	if a.Host != "" {
		addr += "@" + a.Host
	}
	if a.Name != "" {
		return fmt.Sprintf("%s <%s>", decodeWords(a.Name), addr)
	}
	return addr
}

func formatAddresses(addrs []imap.Address) string {
	parts := make([]string, 0, len(addrs))
	for _, a := range addrs {
		parts = append(parts, formatAddress(a))
	}
	return strings.Join(parts, ", ")
}

// decodeWords 解码 RFC 2047 encoded-word；失败/未编码时原样返回。
var wordDecoder = mime.WordDecoder{}

func decodeWords(s string) string {
	if s == "" {
		return ""
	}
	if dec, err := wordDecoder.DecodeHeader(s); err == nil && dec != "" {
		return dec
	}
	return s
}

const maxBodyRunes = 100_000

// extractBody 从整封邮件字节里提取纯文本正文：优先全部 text/plain 段；
// 没有则取第一个 text/html 段去标签。上限 10 万字符，超出截断。
func extractBody(raw []byte) string {
	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		return ""
	}
	plain, html := walkParts(textproto.MIMEHeader(msg.Header), msg.Body)
	if plain != "" {
		return trimBody(plain)
	}
	if html != "" {
		return trimBody(stripHTML(html))
	}
	return ""
}

// walkParts 递归收集 multipart 各段的 text/plain / text/html。
func walkParts(h textproto.MIMEHeader, body io.Reader) (plain, html string) {
	mediaType, params, err := mime.ParseMediaType(h.Get("Content-Type"))
	if err != nil {
		mediaType = "text/plain"
	}
	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return "", ""
		}
		mr := multipart.NewReader(body, boundary)
		var plains, htmls []string
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			pl, ht := walkParts(p.Header, p)
			if pl != "" {
				plains = append(plains, pl)
			}
			if ht != "" {
				htmls = append(htmls, ht)
			}
		}
		return strings.Join(plains, "\n"), strings.Join(htmls, "\n")
	}
	b, _ := io.ReadAll(body)
	text := decodeTransfer(b, h.Get("Content-Transfer-Encoding"), params)
	switch mediaType {
	case "text/plain":
		return text, ""
	case "text/html":
		return "", text
	}
	return "", ""
}

// decodeTransfer 按 Content-Transfer-Encoding 解码文本段（base64 /
// quoted-printable；7bit/8bit/binary 原样）。字符集按 UTF-8 近似处理——
// 账单邮件以国内服务商为主，GBK 近似会带来少量乱码但不影响金额/商户提取；
// 完整字符集处理是已知 TODO。
func decodeTransfer(b []byte, cte string, params map[string]string) string {
	switch strings.ToLower(strings.TrimSpace(cte)) {
	case "base64":
		if dec, err := base64.StdEncoding.DecodeString(string(b)); err == nil {
			return string(dec)
		}
	case "quoted-printable":
		if dec, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(string(b)))); err == nil {
			return string(dec)
		}
	}
	return string(b)
}

var (
	htmlTagRe = regexp.MustCompile(`(?s)<[^>]*>`)
	htmlBRRe  = regexp.MustCompile(`(?i)<br\s*/?>`)
)

// stripHTML 对 text/html 做极简去标签：先把块级换行元素换成换行，
// 再去掉其余标签。不做完整 HTML 解析——给 Kimi 的只要可读文本。
func stripHTML(s string) string {
	repl := htmlBRRe.ReplaceAllString(s, "\n")
	repl = regexp.MustCompile(`(?i)</(p|div|tr|li|table|h[1-6])>`).ReplaceAllString(repl, "\n")
	return htmlTagRe.ReplaceAllString(repl, "")
}

func trimBody(s string) string {
	r := []rune(s)
	if len(r) > maxBodyRunes {
		return string(r[:maxBodyRunes])
	}
	return s
}
