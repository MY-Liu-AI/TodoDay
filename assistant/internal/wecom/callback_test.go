package wecom

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// testHandler 构造一个用官方向量密钥的处理器，processor 记录收到的消息。
func testHandler(t *testing.T, got *[]*Message) *Handler {
	t.Helper()
	key, err := DecodeAESKey(testAESKey)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	return NewHandler(testCorpID, testToken, key, func(_ context.Context, m *Message) {
		mu.Lock()
		defer mu.Unlock()
		*got = append(*got, m)
	})
}

// GET 验证：必须返回解密后的**明文** echostr，不能加引号、BOM 或换行。
// 官方文档把这一点列为最常见的接入失败原因。
func TestVerifyURLReturnsPlaintextEcho(t *testing.T) {
	got := []*Message{}
	h := testHandler(t, &got)

	// 自造一条 echostr（官方向量的明文是未知的，这里测的是链路）。
	key, _ := DecodeAESKey(testAESKey)
	echo, err := Encrypt(key, "4861718792544685389", testCorpID)
	if err != nil {
		t.Fatal(err)
	}
	ts := "1409659589"
	nonce := "263014780"
	sig := Signature(testToken, ts, nonce, echo)

	q := url.Values{}
	q.Set("msg_signature", sig)
	q.Set("timestamp", ts)
	q.Set("nonce", nonce)
	q.Set("echostr", echo)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/wecom/callback?"+q.Encode(), nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("状态码应为 200，得到 %d：%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if body != "4861718792544685389" {
		t.Fatalf("响应体必须是明文 echostr，得到 %q", body)
	}
	// 常见错误：返回了带引号的 JSON 或密文。
	if strings.ContainsAny(body, "\"{}") || strings.Contains(body, echo) {
		t.Fatalf("响应体不应包含引号或密文：%q", body)
	}
}

func TestVerifyURLRejectsBadSignature(t *testing.T) {
	got := []*Message{}
	h := testHandler(t, &got)
	key, _ := DecodeAESKey(testAESKey)
	echo, _ := Encrypt(key, "hello", testCorpID)

	q := url.Values{}
	q.Set("msg_signature", "deadbeef")
	q.Set("timestamp", "1")
	q.Set("nonce", "n")
	q.Set("echostr", echo)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/wecom/callback?"+q.Encode(), nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("签名错误应当 403，得到 %d", rec.Code)
	}
}

// 用别的企业的 CorpID 加密的 echostr 必须被拒绝：否则等于接受任意来源的密文。
func TestVerifyURLRejectsForeignReceiveID(t *testing.T) {
	got := []*Message{}
	h := testHandler(t, &got)
	key, _ := DecodeAESKey(testAESKey)
	echo, _ := Encrypt(key, "hello", "wx_other_corp")

	ts, nonce := "1", "n"
	q := url.Values{}
	q.Set("msg_signature", Signature(testToken, ts, nonce, echo))
	q.Set("timestamp", ts)
	q.Set("nonce", nonce)
	q.Set("echostr", echo)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/wecom/callback?"+q.Encode(), nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("receiveid 不符应当 403，得到 %d", rec.Code)
	}
}

func TestVerifyURLRejectsMissingParams(t *testing.T) {
	got := []*Message{}
	h := testHandler(t, &got)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/wecom/callback", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("缺参数应当 400，得到 %d", rec.Code)
	}
}

// POST 端到端：构造合法密文，断言秒回 success 且消息被交给 processor。
func TestReceiveEncryptedMessage(t *testing.T) {
	got := []*Message{}
	h := testHandler(t, &got)

	key, _ := DecodeAESKey(testAESKey)
	encrypted, err := Encrypt(key, textXML, testCorpID)
	if err != nil {
		t.Fatal(err)
	}
	body := `<xml><ToUserName><![CDATA[` + testCorpID + `]]></ToUserName><Encrypt><![CDATA[` + encrypted + `]]></Encrypt><AgentID><![CDATA[1000002]]></AgentID></xml>`

	ts, nonce := "1409659589", "263014780"
	q := url.Values{}
	// POST 用 body 里的 Encrypt 参与签名，不是 GET 的 echostr。
	q.Set("msg_signature", Signature(testToken, ts, nonce, encrypted))
	q.Set("timestamp", ts)
	q.Set("nonce", nonce)

	req := httptest.NewRequest(http.MethodPost, "/wecom/callback?"+q.Encode(), strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("状态码应为 200，得到 %d", rec.Code)
	}
	if rec.Body.String() != "success" {
		t.Fatalf("响应体应为 success，得到 %q", rec.Body.String())
	}

	// processor 在 goroutine 里跑，等它一下。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(got) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if len(got) != 1 {
		t.Fatalf("应当收到 1 条消息，得到 %d", len(got))
	}
	if got[0].Content != "明天 交周报 #工作 !!!" {
		t.Fatalf("消息内容解密错误：%q", got[0].Content)
	}
	if got[0].FromUserName != "ZhangSan" {
		t.Fatalf("发送人解析错误：%q", got[0].FromUserName)
	}
}

func TestReceiveRejectsBadSignature(t *testing.T) {
	got := []*Message{}
	h := testHandler(t, &got)
	key, _ := DecodeAESKey(testAESKey)
	encrypted, _ := Encrypt(key, textXML, testCorpID)
	body := `<xml><Encrypt><![CDATA[` + encrypted + `]]></Encrypt></xml>`

	q := url.Values{}
	q.Set("msg_signature", "deadbeef")
	q.Set("timestamp", "1")
	q.Set("nonce", "n")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/wecom/callback?"+q.Encode(), strings.NewReader(body)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("签名错误应当 403，得到 %d", rec.Code)
	}
	if len(got) != 0 {
		t.Fatal("签名不通过时不应处理消息")
	}
}

// 非文本消息：仍回 success（否则微信会重试），但不交给 processor。
func TestReceiveIgnoresNonText(t *testing.T) {
	got := []*Message{}
	h := testHandler(t, &got)
	key, _ := DecodeAESKey(testAESKey)
	img := `<xml><FromUserName><![CDATA[ZhangSan]]></FromUserName><MsgType><![CDATA[image]]></MsgType></xml>`
	encrypted, _ := Encrypt(key, img, testCorpID)
	body := `<xml><Encrypt><![CDATA[` + encrypted + `]]></Encrypt></xml>`

	ts, nonce := "1", "n"
	q := url.Values{}
	q.Set("msg_signature", Signature(testToken, ts, nonce, encrypted))
	q.Set("timestamp", ts)
	q.Set("nonce", nonce)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/wecom/callback?"+q.Encode(), strings.NewReader(body)))
	if rec.Code != http.StatusOK || rec.Body.String() != "success" {
		t.Fatalf("非文本消息也应回 success，得到 %d %q", rec.Code, rec.Body.String())
	}
	time.Sleep(50 * time.Millisecond)
	if len(got) != 0 {
		t.Fatal("非文本消息不应被处理")
	}
}

func TestMethodNotAllowed(t *testing.T) {
	got := []*Message{}
	h := testHandler(t, &got)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/wecom/callback", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("应为 405，得到 %d", rec.Code)
	}
}

// VerifyURLForTest 生成的参数必须能通过自己的验证 —— 这是本地自测手段，
// 如果它自己都过不了就毫无意义。
func TestVerifyURLForTestProducesValidParams(t *testing.T) {
	got := []*Message{}
	h := testHandler(t, &got)

	sig, ts, echo, err := h.VerifyURLForTest("mynonce")
	if err != nil {
		t.Fatal(err)
	}
	q := url.Values{}
	q.Set("msg_signature", sig)
	q.Set("timestamp", ts)
	q.Set("nonce", "mynonce")
	q.Set("echostr", echo)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/wecom/callback?"+q.Encode(), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("自测参数应当通过验证，得到 %d", rec.Code)
	}
	if rec.Body.String() != "test-echo" {
		t.Fatalf("应当返回明文 test-echo，得到 %q", rec.Body.String())
	}
}
