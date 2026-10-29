package mailimport

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"

	"tododay/internal/secretbox"
	"tododay/internal/store"
)

const testMailUser = "test@example.com"
const testMailCode = "authcode123"

// testLiteral 是 imap.LiteralReader 的最小实现（memserver Append 用）。
type testLiteral struct{ r *strings.Reader }

func newTestLiteral(s string) testLiteral        { return testLiteral{strings.NewReader(s)} }
func (l testLiteral) Read(p []byte) (int, error) { return l.r.Read(p) }
func (l testLiteral) Size() int64                { return int64(l.r.Len()) }

// newMemIMAP 起一个真的 IMAP 服务（go-imap 内存实现）：TLS + LOGIN +
// INBOX，并塞入三封测试邮件（1 消费、1 营销、1 工资）。返回服务端点
// （host:port）与校验配置。Manager.imapAddr/tlsConfig 指向它即完成
// 注入——传输层不再有任何假实现。
func newMemIMAP(t *testing.T, wrongCode bool) (string, map[string]imap.UID, *tls.Config) {
	t.Helper()

	code := testMailCode
	if wrongCode {
		code = "right-code"
	}

	mem := imapmemserver.New()
	user := imapmemserver.NewUser(testMailUser, code)
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	mem.AddUser(user)

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "localhost"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		DNSNames:              []string{"localhost"},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	tmplCert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	tlsConfig := &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	srv := imapserver.New(&imapserver.Options{
		NewSession: func(conn *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		TLSConfig:    tlsConfig,
		InsecureAuth: true,
		Caps: imap.CapSet{
			imap.CapIMAP4rev1: {},
			imap.CapIMAP4rev2: {},
		},
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	// 生产路径走隐式 TLS（imap.qq.com:993），所以测试服务也包一层
	// tls.Listener——服务端 Options.TLSConfig 本身只声明 STARTTLS 能力。
	tlsLn := tls.NewListener(ln, tlsConfig)
	go func() { _ = srv.Serve(tlsLn) }()
	t.Cleanup(func() { srv.Close() })

	appendMsg := func(subject, from, body string) imap.UID {
		raw := "From: " + from + "\r\n" +
			"To: " + testMailUser + "\r\n" +
			"Subject: " + subject + "\r\n" +
			"Date: Mon, 26 Oct 2026 10:00:00 +0800\r\n" +
			"Content-Type: text/plain; charset=utf-8\r\n" +
			"\r\n" + body + "\r\n"
		data, err := user.Append("INBOX", newTestLiteral(raw), &imap.AppendOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return data.UID
	}

	uids := map[string]imap.UID{
		"消费": appendMsg("您有一笔新的消费", "bank@example.com", "正文（假）：您消费 88.50 元"),
		"营销": appendMsg("周末观影优惠", "promo@example.com", "正文（假）：优惠券"),
		"工资": appendMsg("工资入账", "hr@example.com", "正文（假）：本月工资已发放"),
	}
	pool := x509.NewCertPool()
	pool.AddCert(tmplCert)
	clientTLS := &tls.Config{RootCAs: pool, ServerName: "127.0.0.1"}
	return ln.Addr().String(), uids, clientTLS
}

// newMailTestManager 造一套完整测试环境：内存 SQLite + 迁移 + owner 工作区 +
// 主密钥 + 假 Kimi（按主题返回 canned 解析结果）+ 指向 memserver 的 Manager。
func newMailTestManager(t *testing.T) (*Manager, *store.Store, int64, map[string]imap.UID) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	u, err := st.CreateUser(t.Context(), "owner", "hash")
	if err != nil {
		t.Fatal(err)
	}
	ws, err := st.CreateWorkspaceWithOwner(t.Context(), u.ID, "测试工作区", "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	box, err := secretbox.FromKey("MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	if err != nil {
		t.Fatal(err)
	}

	// 假 Kimi：按正文里的主题判断返回哪笔账（营销 → is_bill=false）。
	kimiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		userText := ""
		for _, m := range req.Messages {
			if m.Role == "user" {
				userText = m.Content
			}
		}
		var args string
		switch {
		case strings.Contains(userText, "周末观影优惠"):
			args = `{"is_bill":false,"type":"","amount_yuan":0,"category":"","date":"","merchant":""}`
		case strings.Contains(userText, "工资入账"):
			args = `{"is_bill":true,"type":"inc","amount_yuan":15000,"category":"","date":"","merchant":"某公司"}`
		default:
			args = `{"is_bill":true,"type":"exp","amount_yuan":88.5,"category":"餐饮","date":"2026-10-26","merchant":"某某超市"}`
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"record_bill","arguments":%q}}]}}]}`, args)
	}))
	t.Cleanup(kimiSrv.Close)

	// 助手配置：Kimi Key（加密）+ 指向假服务的 base。
	enc, err := box.Seal("kimi-key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveAssistantConfig(t.Context(), ws.ID, u.ID, store.AssistantConfigInput{
		KimiAPIKeyEnc: enc, KimiBase: kimiSrv.URL,
	}, 0); err != nil {
		t.Fatal(err)
	}

	addr, uids, clientTLS := newMemIMAP(t, false)
	m := NewManager(st, box)
	m.imapAddr = addr
	m.tlsConfig = clientTLS
	return m, st, ws.ID, uids
}

// saveMailCfg 保存邮箱配置：凭据走密文（与生产路径一致）。
func saveMailCfg(t *testing.T, st *store.Store, box *secretbox.Box, wid, uid int64, enabled bool, senders string) {
	t.Helper()
	enc, err := box.Seal(testMailCode)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveMailConfig(t.Context(), wid, uid, store.MailConfigInput{
		Enabled: enabled, Mailbox: testMailUser, AuthCodeEnc: enc, Senders: senders,
	}, 0); err != nil {
		t.Fatal(err)
	}
}

// 端到端：memserver 里 3 封邮件（1 消费、1 营销、1 工资），假 Kimi 解析，
// 应落 2 笔账；营销邮件标记已处理但不落库。再跑一轮应零导入（幂等）。
func TestRunOnceEndToEnd(t *testing.T) {
	m, st, wid, uids := newMailTestManager(t)
	uID, err := st.WorkspaceOwnerID(t.Context(), wid)
	if err != nil {
		t.Fatal(err)
	}
	saveMailCfg(t, st, m.box, wid, uID, true, "")

	if err := m.RunOnce(t.Context(), wid); err != nil {
		t.Fatal(err)
	}
	entries, stats, err := st.ListLedger(t.Context(), wid, "2026-10")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("应导入 2 笔，得到 %d：%+v", len(entries), entries)
	}
	if stats.Inc != 1500000 || stats.Exp != 8850 {
		t.Fatalf("汇总不符：%+v", stats)
	}
	for _, e := range entries {
		if e.Type == "exp" && e.Category != "餐饮" {
			t.Fatalf("支出分类应保留模型值，得到 %q", e.Category)
		}
		if e.Note == "" {
			t.Fatalf("备注应含商户/主题，便于核对")
		}
	}

	// 非账单邮件应已标记（防止每天重复调模型）。
	seen, _ := st.MailSeen(t.Context(), wid, fmt.Sprintf("%d", uids["营销"]))
	if !seen {
		t.Fatalf("营销邮件应标记为已处理")
	}

	// 第二轮：全部已处理，零导入。
	if err := m.RunOnce(t.Context(), wid); err != nil {
		t.Fatal(err)
	}
	cfg, _ := st.GetMailConfig(t.Context(), wid)
	if cfg.LastImported != 0 {
		t.Fatalf("第二轮应零导入，得到 %d", cfg.LastImported)
	}
}


// 按发件人预筛：只配了来源时不看关键词——只有工资邮件通过。
func TestRunOnceSenderFilter(t *testing.T) {
	m, st, wid, uids := newMailTestManager(t)
	uID, _ := st.WorkspaceOwnerID(t.Context(), wid)
	// senders 只放行 hr@example.com：消费/营销邮件被来源拦下（标记 seen），
	// 只有工资邮件进入 Kimi 解析。
	saveMailCfg(t, st, m.box, wid, uID, true, "hr@example.com")

	if err := m.RunOnce(t.Context(), wid); err != nil {
		t.Fatal(err)
	}
	entries, _, err := st.ListLedger(t.Context(), wid, "2026-10")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Type != "inc" {
		t.Fatalf("只应导入工资一笔，得到 %+v", entries)
	}
	for _, name := range []string{"消费", "营销"} {
		seen, _ := st.MailSeen(t.Context(), wid, fmt.Sprintf("%d", uids[name]))
		if !seen {
			t.Fatalf("%s 邮件应被来源预筛拦下并标记已处理", name)
		}
	}
}


// 从邮箱发现发件人：按 From 聚合封数，样本主题为最近一封。
func TestDiscoverSenders(t *testing.T) {
	m, st, wid, _ := newMailTestManager(t)
	uID, _ := st.WorkspaceOwnerID(t.Context(), wid)
	saveMailCfg(t, st, m.box, wid, uID, true, "")

	senders, err := m.Discover(t.Context(), wid, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(senders) != 3 {
		t.Fatalf("应发现 3 个发件人，得到 %+v", senders)
	}
	byFrom := map[string]DiscoveredSender{}
	for _, d := range senders {
		if d.Count != 1 || d.Sample == "" {
			t.Fatalf("封数与样本主题不符：%+v", d)
		}
		byFrom[d.From] = d
	}
	if byFrom["hr@example.com"].Sample != "工资入账" {
		t.Fatalf("样本主题应为最近一封：%+v", byFrom["hr@example.com"])
	}

	// 无凭据工作区如实失败（ErrNoCredentials），不假装能发现。
	st2, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st2.Close() })
	if err := st2.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	m2 := NewManager(st2, m.box)
	if _, err := m2.Discover(t.Context(), 999, "", ""); !errors.Is(err, ErrNoCredentials) {
		t.Fatalf("无凭据应 ErrNoCredentials，得到 %v", err)
	}
}

// 授权码错误：IMAP LOGIN 失败 → ErrAuth，运行状态如实记录、提示核对授权码。
func TestRunOnceAuthExpired(t *testing.T) {
	m, st, wid, _ := newMailTestManager(t)
	uID, _ := st.WorkspaceOwnerID(t.Context(), wid)
	// memserver 上真实的授权码是 right-code，存进去的是错的。
	addr, _, clientTLS := newMemIMAP(t, true)
	m.imapAddr = addr
	m.tlsConfig = clientTLS
	saveMailCfg(t, st, m.box, wid, uID, true, "")

	if err := m.RunOnce(t.Context(), wid); err == nil {
		t.Fatalf("授权码错误应返回错误")
	}
	if !isErrAuth(t, st, wid) {
		t.Fatalf("错误应可经 errors.Is 识别为授权码问题")
	}
	cfg, _ := st.GetMailConfig(t.Context(), wid)
	if cfg.LastRunOk == nil || *cfg.LastRunOk {
		t.Fatalf("运行状态应记录失败：%v", cfg.LastRunOk)
	}
	if !strings.Contains(cfg.LastRunError, "授权码") {
		t.Fatalf("失败原因应提示授权码，得到 %q", cfg.LastRunError)
	}
}

func isErrAuth(t *testing.T, st *store.Store, wid int64) bool {
	t.Helper()
	cfg, err := st.GetMailConfig(t.Context(), wid)
	if err != nil {
		t.Fatal(err)
	}
	return cfg != nil && strings.Contains(cfg.LastRunError, "授权码")
}

// 无凭据/已撤销：RunOnce 不发起任何连接，如实失败。
func TestRunOnceNoCredentials(t *testing.T) {
	m, st, wid, _ := newMailTestManager(t)
	uID, _ := st.WorkspaceOwnerID(t.Context(), wid)
	// 只配置邮箱地址，授权码密文为空（等价于已撤销）。
	if _, err := st.SaveMailConfig(t.Context(), wid, uID, store.MailConfigInput{
		Enabled: true, Mailbox: testMailUser,
	}, 0); err != nil {
		t.Fatal(err)
	}

	if err := m.RunOnce(t.Context(), wid); err == nil {
		t.Fatalf("无凭据应失败")
	}
	cfg, _ := st.GetMailConfig(t.Context(), wid)
	if cfg.LastRunOk == nil || *cfg.LastRunOk {
		t.Fatalf("无凭据应记录失败状态")
	}
}

// 未配置 Kimi：如实失败，不静默跳过解析。
func TestRunOnceNoKimi(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	u, _ := st.CreateUser(t.Context(), "owner", "hash")
	ws, _ := st.CreateWorkspaceWithOwner(t.Context(), u.ID, "ws", "Asia/Shanghai")
	box, _ := secretbox.FromKey("MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	addr, _, clientTLS := newMemIMAP(t, false)
	m := NewManager(st, box)
	m.imapAddr = addr
	m.tlsConfig = clientTLS
	saveMailCfg(t, st, box, ws.ID, u.ID, true, "")

	if err := m.RunOnce(t.Context(), ws.ID); err == nil {
		t.Fatalf("缺 Kimi 应失败")
	}
	cfg, _ := st.GetMailConfig(t.Context(), ws.ID)
	if cfg.LastRunOk == nil || *cfg.LastRunOk {
		t.Fatalf("缺 Kimi 应记录失败状态")
	}
	if !strings.Contains(cfg.LastRunError, "Kimi") {
		t.Fatalf("失败原因应提到 Kimi，得到 %q", cfg.LastRunError)
	}
}

func TestManagerStartStop(t *testing.T) {
	m, st, wid, _ := newMailTestManager(t)
	uID, _ := st.WorkspaceOwnerID(t.Context(), wid)
	saveMailCfg(t, st, m.box, wid, uID, true, "")

	if err := m.Start(t.Context(), wid); err != nil {
		t.Fatal(err)
	}
	if !m.IsRunning(wid) {
		t.Fatalf("启动后应处于 running")
	}
	if err := m.Start(t.Context(), wid); err != nil {
		t.Fatalf("重复启动应幂等：%v", err)
	}
	m.Stop(wid)
	if m.IsRunning(wid) {
		t.Fatalf("停止后不应 running")
	}
	m.Close()
}

// 凭据缺失/已撤销：Start 同步拒绝（不连接），供启动端点回滚 enabled。
func TestManagerStartRequiresCredentials(t *testing.T) {
	m, st, wid, _ := newMailTestManager(t)
	uID, _ := st.WorkspaceOwnerID(t.Context(), wid)
	if _, err := st.SaveMailConfig(t.Context(), wid, uID, store.MailConfigInput{
		Enabled: true, Mailbox: testMailUser, // 没有授权码
	}, 0); err != nil {
		t.Fatal(err)
	}

	err := m.Start(t.Context(), wid)
	if err == nil {
		t.Fatalf("无凭据不应启动")
	}
	if !strings.Contains(err.Error(), "未配置") {
		t.Fatalf("错误应提示凭据缺失，得到 %v", err)
	}
	if m.IsRunning(wid) {
		t.Fatalf("失败后不应处于 running")
	}
}

// Test（测试连接）：表单值直接验证；空值回退已存配置；两者皆无则拒绝。
func TestManagerTestConnection(t *testing.T) {
	m, st, wid, _ := newMailTestManager(t)
	uID, _ := st.WorkspaceOwnerID(t.Context(), wid)
	saveMailCfg(t, st, m.box, wid, uID, true, "")

	// 表单值（未保存也能测）。
	got, err := m.Test(t.Context(), wid, testMailUser, testMailCode)
	if err != nil || got != testMailUser {
		t.Fatalf("表单值测试应成功，得到 %q %v", got, err)
	}
	// 已存配置。
	got, err = m.Test(t.Context(), wid, "", "")
	if err != nil || got != testMailUser {
		t.Fatalf("已存凭据测试应成功，得到 %q %v", got, err)
	}
	// 错误授权码。
	if _, err := m.Test(t.Context(), wid, testMailUser, "wrong"); err == nil {
		t.Fatalf("错误授权码应失败")
	}
	// 无凭据工作区。
	st2, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st2.Close() })
	if err := st2.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	u2, _ := st2.CreateUser(t.Context(), "o2", "hash")
	ws2, _ := st2.CreateWorkspaceWithOwner(t.Context(), u2.ID, "ws2", "Asia/Shanghai")
	m2 := NewManager(st2, m.box)
	m2.imapAddr = m.imapAddr
	m2.tlsConfig = m.tlsConfig
	if _, err := m2.Test(t.Context(), ws2.ID, "", ""); err == nil {
		t.Fatalf("无凭据应返回错误")
	}
}

// 传输层单测：正文提取（纯文本 / multipart 优先 text/plain / HTML 去标签）。
func TestExtractBody(t *testing.T) {
	rawText := "Subject: x\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n你好 88.50 元\r\n"
	if got := extractBody([]byte(rawText)); !strings.Contains(got, "88.50") {
		t.Fatalf("纯文本正文提取失败：%q", got)
	}

	multi := "Subject: x\r\n" +
		"Content-Type: multipart/alternative; boundary=bb\r\n\r\n" +
		"--bb\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<p>html 部分</p>\r\n" +
		"--bb\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n纯文本部分 12.00\r\n--bb--\r\n"
	if got := extractBody([]byte(multi)); !strings.Contains(got, "纯文本部分") || strings.Contains(got, "html 部分") {
		t.Fatalf("multipart 应优先 text/plain：%q", got)
	}

	htmlOnly := "Subject: x\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<p>消费 <b>66.00</b> 元</p>\r\n"
	if got := extractBody([]byte(htmlOnly)); strings.Contains(got, "<b>") || !strings.Contains(got, "66.00") {
		t.Fatalf("HTML 应去标签：%q", got)
	}

	base64Part := "Subject: x\r\n" +
		"Content-Type: multipart/mixed; boundary=cc\r\n\r\n" +
		"--cc\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n\r\n" +
		"5rWL6K+V5paH5pys77yMNTUuMDDph5Hpop0=\r\n--cc--\r\n"
	if got := extractBody([]byte(base64Part)); !strings.Contains(got, "55.00") {
		t.Fatalf("base64 段应解码：%q", got)
	}
}

func TestDecodeWords(t *testing.T) {
	enc := "=?UTF-8?B?" + base64.StdEncoding.EncodeToString([]byte("我的账单")) + "?="
	if got := decodeWords(enc); got != "我的账单" {
		t.Fatalf("encoded-word 解码失败：%q", got)
	}
	if got := decodeWords("plain subject"); got != "plain subject" {
		t.Fatalf("普通主题应原样返回：%q", got)
	}
}


func TestSplitAndMatchSenders(t *testing.T) {
	s := splitSenders(" A@Bank.com |bank.com||A@bank.com ")
	if len(s) != 2 || s[0] != "A@Bank.com" || s[1] != "bank.com" {
		t.Fatalf("去空去重（按小写）不符：%v", s)
	}
	if got := splitSenders(""); len(got) != 0 {
		t.Fatalf("senders 没有默认词表，空配置应为空：%v", got)
	}
	// 子串匹配：完整地址、「Name <addr>」形态、域名配法都要命中。
	if !matchSenders([]string{"service@bank.com"}, "service@bank.com") {
		t.Fatalf("完整地址应命中")
	}
	if !matchSenders([]string{"service@bank.com"}, "Bank 账单 <Service@Bank.Com>") {
		t.Fatalf("Name <addr> 形态且大小写混合应命中")
	}
	if !matchSenders([]string{"bank.com"}, "Bank 账单 <service@bank.com>") {
		t.Fatalf("域名配法应命中")
	}
	if matchSenders([]string{"other.com"}, "service@bank.com") {
		t.Fatalf("不相关地址不应命中")
	}
	if matchSenders(nil, "service@bank.com") {
		t.Fatalf("未配置来源时不应放行")
	}
}

func TestValidYYYYMMDD(t *testing.T) {
	if !validYYYYMMDD("2026-10-26") || validYYYYMMDD("2026-13-01") || validYYYYMMDD("26-10-26") {
		t.Fatalf("日期校验不符")
	}
}

var _ io.Reader = testLiteral{}
