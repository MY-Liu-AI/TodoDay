package wecom

import (
	"context"
	"encoding/xml"
	"log"
	"net/http"
	"strconv"
	"time"
)

// maxBody 限制回调请求体大小。真实消息只有几 KB，给足余量即可。
const maxBody = 1 << 20

// Processor 处理一条已解密的文本消息。回调处理器会在返回 success
// 之后异步调用它，因此实现方不能依赖请求上下文。
type Processor func(ctx context.Context, msg *Message)

// Handler 实现企业微信自建应用的「接收消息」回调。
//
// 两条路径的职责差别很大：
//   - GET 是配置回调地址时的 URL 验证，必须在 1 秒内回明文 echostr；
//   - POST 是真实消息，必须 5 秒内响应，否则企业微信重试 3 次。
//
// 因此 POST 只做「解密 + 校验」（微秒级），立刻回 success，
// 解析与建任务交给 Processor 异步处理，完成后另行推送回执。
type Handler struct {
	corpID    string
	token     string
	aesKey    []byte
	processor Processor
}

// NewHandler 构造回调处理器。aesKey 必须是 32 字节。
func NewHandler(corpID, token string, aesKey []byte, p Processor) *Handler {
	return &Handler{corpID: corpID, token: token, aesKey: aesKey, processor: p}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.verifyURL(w, r)
	case http.MethodPost:
		h.receive(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// verifyURL 处理配置回调地址时的验证请求。
//
// 返回内容必须是**解密后的明文 echostr**，不能加引号、BOM 或换行 ——
// 官方文档把这一点列为最常见的失败原因。
func (h *Handler) verifyURL(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	signature := q.Get("msg_signature")
	timestamp := q.Get("timestamp")
	nonce := q.Get("nonce")
	echoStr := q.Get("echostr")
	if signature == "" || timestamp == "" || nonce == "" || echoStr == "" {
		http.Error(w, "missing parameters", http.StatusBadRequest)
		return
	}

	// GET 用 echostr 参与签名。
	expected := Signature(h.token, timestamp, nonce, echoStr)
	if !SignatureMatches(expected, signature) {
		log.Printf("wecom: URL 验证签名不匹配")
		http.Error(w, "invalid signature", http.StatusForbidden)
		return
	}

	plain, receiveID, err := Decrypt(h.aesKey, echoStr)
	if err != nil {
		log.Printf("wecom: 解密 echostr 失败：%v", err)
		http.Error(w, "decrypt failed", http.StatusBadRequest)
		return
	}
	if receiveID != h.corpID {
		log.Printf("wecom: echostr 的 receiveid 与 CorpID 不符")
		http.Error(w, "receiveid mismatch", http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(plain))
}

// receive 处理真实消息回调。
func (h *Handler) receive(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	signature := q.Get("msg_signature")
	timestamp := q.Get("timestamp")
	nonce := q.Get("nonce")

	body, err := ReadAllLimited(r.Body, maxBody)
	if err != nil {
		http.Error(w, "read failed", http.StatusBadRequest)
		return
	}
	var env envelope
	if err := xml.Unmarshal(body, &env); err != nil || env.Encrypt == "" {
		log.Printf("wecom: 回调 body 缺少 Encrypt：%v", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// POST 用 body 里的 Encrypt 参与签名（不是 GET 的 echostr）。
	expected := Signature(h.token, timestamp, nonce, env.Encrypt)
	if !SignatureMatches(expected, signature) {
		log.Printf("wecom: 消息回调签名不匹配")
		http.Error(w, "invalid signature", http.StatusForbidden)
		return
	}

	plain, receiveID, err := Decrypt(h.aesKey, env.Encrypt)
	if err != nil {
		log.Printf("wecom: 解密消息失败：%v", err)
		http.Error(w, "decrypt failed", http.StatusBadRequest)
		return
	}
	if receiveID != h.corpID {
		log.Printf("wecom: 消息的 receiveid 与 CorpID 不符")
		http.Error(w, "receiveid mismatch", http.StatusForbidden)
		return
	}

	msg, err := parseMessage(plain)
	if err != nil {
		log.Printf("wecom: 解析消息失败：%v", err)
		http.Error(w, "bad message", http.StatusBadRequest)
		return
	}

	// 先回 success，再异步处理。顺序不能反：解析要调 Claude，耗时不可控，
	// 扛不住 5 秒硬墙。
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("success"))

	if !msg.IsText() {
		log.Printf("wecom: 忽略非文本消息 type=%s from=%s", msg.MsgType, msg.FromUserName)
		return
	}
	if h.processor != nil {
		go h.processor(context.Background(), msg)
	}
}

// VerifyURLForTest 生成一条可用于手工自测的验证请求参数，
// 方便在没有真实企业微信后台的情况下先跑通握手。
// 仅供测试与调试辅助使用。
func (h *Handler) VerifyURLForTest(nonce string) (signature, timestamp, echoStr string, err error) {
	timestamp = strconv.FormatInt(time.Now().Unix(), 10)
	echoStr, err = Encrypt(h.aesKey, "test-echo", h.corpID)
	if err != nil {
		return "", "", "", err
	}
	return Signature(h.token, timestamp, nonce, echoStr), timestamp, echoStr, nil
}
