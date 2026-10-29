// Package mailimport 通过原生 IMAP（邮箱地址 + 服务商授权码）定时拉取
// 账单邮件，用 Kimi 解析后落进账本。
//
// 凭据边界：授权码由用户在网页设置页填写，服务端以 AES-256-GCM 密文
// （主密钥 TODO_CONFIG_KEY）落库；本包只在运行时用密文解密出明文去 LOGIN，
// 明文不出现在任何视图与日志里。撤销 = 显式清除密文（clearAuthCode），
// 已运行的循环下一轮凭据解析失败即如实退出——不存在「删了还在跑」。
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
	"net/mail"
	"net/textproto"
	"regexp"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// ErrAuth 表示 IMAP 登录失败：授权码无效、IMAP 服务未开启或密码错误。
// 这类错误重试无意义，如实记进运行状态，提示用户去邮箱设置核对。
var ErrAuth = errors.New("邮箱授权码无效或 IMAP 服务未开启")

const defaultIMAPAddr = "imap.qq.com:993"

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
type Session struct {
	c   *imapclient.Client
	loc *time.Location
}

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
	c, err := imapclient.DialTLS(t.addr(), &imapclient.Options{
		TLSConfig: tlsCfg.Clone(),
	})
	if err != nil {
		return nil, fmt.Errorf("连接邮箱服务器失败：%w", err)
	}
	if err := c.Login(mailbox, authCode).Wait(); err != nil {
		_ = c.Close()
		var imapErr *imap.Error
		if errors.As(err, &imapErr) && imapErr.Type == imap.StatusResponseTypeNo {
			return nil, fmt.Errorf("%w：%s", ErrAuth, imapErr.Text)
		}
		return nil, fmt.Errorf("邮箱登录失败：%w", err)
	}
	if _, err := c.Select("INBOX", nil).Wait(); err != nil {
		_ = c.Logout().Wait()
		_ = c.Close()
		return nil, fmt.Errorf("打开收件箱失败：%w", err)
	}
	return &Session{c: c, loc: loc}, nil
}

// Close 优雅登出并断开。重复调用安全。
func (s *Session) Close() {
	if s.c == nil {
		return
	}
	_ = s.c.Logout().Wait()
	_ = s.c.Close()
	s.c = nil
}

// List 列出 INBOX 里内部日期晚于 since 的邮件（最多 limit 封）。
// 账单导入只要增量窗口，不翻页：单次 daily run 窗口 7 天、上限 50 封，
// 超出留到明天也算符合「每日一次」的语义。
func (s *Session) List(ctx context.Context, since time.Time, limit int) ([]Message, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

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
