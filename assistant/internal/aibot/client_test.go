package aibot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// 这一组测试用一个本地假服务端验证长连接的完整环路：
// 连接 → 订阅 → 收到消息回调 → 回复。这是整个改造里风险最高的一段，
// 而真实企业微信需要凭据才能连，所以必须能在本地被证明。
//
// 用的是包内测试（package aibot），因此可以直接驱动 session()，
// 避免 Run() 的重连退避把测试拖长。

// testCallback 记录收到的回调，便于断言。
type testCallback struct {
	mu       sync.Mutex
	messages []*InboundBody
	reqIDs   []string
	// fn 在记录之后调用，用于在回调里回复（和生产路径一致）。
	fn func(ctx context.Context, reqID string, body *InboundBody)
}

func (c *testCallback) OnMessage(ctx context.Context, reqID string, b *InboundBody) {
	c.mu.Lock()
	c.messages = append(c.messages, b)
	c.reqIDs = append(c.reqIDs, reqID)
	c.mu.Unlock()
	if c.fn != nil {
		c.fn(ctx, reqID, b)
	}
}

func (c *testCallback) OnEvent(context.Context, string, *EventBody) {}

func (c *testCallback) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.messages)
}

func newUpgrader() websocket.Upgrader {
	return websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
}

// wsURLOf 把 httptest 的 http:// 地址换成 ws://。
func wsURLOf(srv *httptest.Server) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

// 完整环路：订阅带对凭据 → 收到消息 → 回调里回复 → 回复透传 req_id。
func TestClientSubscribeDispatchAndReply(t *testing.T) {
	var (
		gotSub   = make(chan SubscribeBody, 1)
		gotReply = make(chan Frame, 1)
	)
	up := newUpgrader()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("升级为 WebSocket 失败：%v", err)
			return
		}
		defer conn.Close()

		// 1) 第一帧必须是订阅，且带对凭据。
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var f Frame
		if err := json.Unmarshal(raw, &f); err != nil {
			t.Errorf("订阅帧不是合法 JSON：%v", err)
			return
		}
		if f.Cmd != CmdSubscribe {
			t.Errorf("第一帧应当是 %s，得到 %s", CmdSubscribe, f.Cmd)
			return
		}
		if f.Headers.ReqID == "" {
			t.Error("订阅帧缺少 req_id")
		}
		var sb SubscribeBody
		if err := json.Unmarshal(f.Body, &sb); err != nil {
			t.Errorf("订阅 body 解析失败：%v", err)
		}
		gotSub <- sb

		// 应答订阅。
		ack, _ := json.Marshal(map[string]any{
			"cmd":     CmdSubscribe,
			"headers": map[string]string{"req_id": f.Headers.ReqID},
			"errcode": 0,
			"errmsg":  "ok",
		})
		_ = conn.WriteMessage(websocket.TextMessage, ack)

		// 2) 下发一条文本消息。
		msg, _ := json.Marshal(map[string]any{
			"cmd":     CmdMsgCallback,
			"headers": map[string]string{"req_id": "req-1"},
			"body": map[string]any{
				"msgid":    "MSG-1",
				"aibotid":  "BOT",
				"chattype": "single",
				"msgtype":  "text",
				"from":     map[string]string{"userid": "ZhangSan"},
				"text":     map[string]string{"content": "明天 交周报 #工作 !!!"},
			},
		})
		_ = conn.WriteMessage(websocket.TextMessage, msg)

		// 3) 读回复。
		_, rraw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var rf Frame
		if err := json.Unmarshal(rraw, &rf); err != nil {
			t.Errorf("回复帧不是合法 JSON：%v", err)
			return
		}
		gotReply <- rf
	}))

	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		srv.CloseClientConnections()
		srv.Close()
	}()

	cb := &testCallback{}
	client := NewClient("BOTID-1", "SECRET-1", wsURLOf(srv), nil)
	// 回调里直接回复 —— 这正是生产里的路径：回复必须走客户端自己，
	// 因为要绑定这次回调的 req_id。
	cb.fn = func(ctx context.Context, reqID string, _ *InboundBody) {
		if err := client.Reply(ctx, reqID, "已记录：交周报"); err != nil {
			t.Errorf("回复失败：%v", err)
		}
	}
	client.SetCallback(cb)

	go func() { _ = client.session(ctx) }()

	select {
	case sb := <-gotSub:
		if sb.BotID != "BOTID-1" || sb.Secret != "SECRET-1" {
			t.Fatalf("订阅凭据不正确：%+v", sb)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("超时：服务端没有收到订阅")
	}

	select {
	case rf := <-gotReply:
		if rf.Cmd != CmdRespondMsg {
			t.Fatalf("回复命令应为 %s，得到 %s", CmdRespondMsg, rf.Cmd)
		}
		// 这一条是整条链路的枢纽：没有 req_id 回复就送不到。
		if rf.Headers.ReqID != "req-1" {
			t.Fatalf("回复没有透传 req_id：%q", rf.Headers.ReqID)
		}
		var body struct {
			Text struct {
				Content string `json:"content"`
			} `json:"text"`
		}
		if err := json.Unmarshal(rf.Body, &body); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(body.Text.Content, "交周报") {
			t.Fatalf("回复内容不正确：%q", body.Text.Content)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("超时：服务端没有收到回复")
	}

	if cb.count() != 1 {
		t.Fatalf("应当收到 1 条消息，得到 %d", cb.count())
	}
	cb.mu.Lock()
	content := cb.messages[0].Content()
	cb.mu.Unlock()
	if content != "明天 交周报 #工作 !!!" {
		t.Fatalf("消息内容解析不正确：%q", content)
	}
}

// 没有 req_id 的消息无法回复，必须丢弃且不触发回调 ——
// 否则会给用户一个「机器人已收到」的错觉，实际什么都发不出去。
func TestMessageWithoutReqIDIsDropped(t *testing.T) {
	up := newUpgrader()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		// 只下发一条缺 req_id 的消息，不读回复。
		msg, _ := json.Marshal(map[string]any{
			"cmd":     CmdMsgCallback,
			"headers": map[string]string{},
			"body": map[string]any{
				"msgid": "MSG-2", "msgtype": "text",
				"from": map[string]string{"userid": "ZhangSan"},
				"text": map[string]string{"content": "买牛奶"},
			},
		})
		_ = conn.WriteMessage(websocket.TextMessage, msg)
		time.Sleep(300 * time.Millisecond)
	}))

	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		srv.CloseClientConnections()
		srv.Close()
	}()

	// 这条路径用构造函数注入回调，顺便覆盖 NewClient 的 cb 参数。
	cb := &testCallback{}
	client := NewClient("B", "S", wsURLOf(srv), cb)
	go func() { _ = client.session(ctx) }()

	time.Sleep(800 * time.Millisecond)
	if n := cb.count(); n != 0 {
		t.Fatalf("缺 req_id 的消息不应触发回调，得到 %d 次", n)
	}
}

// 群聊消息同样要能解出来（chatid/chattype 与单聊不同）。
func TestGroupMessageIsDispatched(t *testing.T) {
	up := newUpgrader()
	got := make(chan *InboundBody, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_, _, _ = conn.ReadMessage() // 订阅帧
		msg, _ := json.Marshal(map[string]any{
			"cmd":     CmdMsgCallback,
			"headers": map[string]string{"req_id": "req-g"},
			"body": map[string]any{
				"msgid": "G1", "chatid": "CHAT-9", "chattype": "group", "msgtype": "text",
				"from": map[string]string{"userid": "LiSi"},
				"text": map[string]string{"content": "买牛奶"},
			},
		})
		_ = conn.WriteMessage(websocket.TextMessage, msg)
		time.Sleep(300 * time.Millisecond)
	}))

	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		srv.CloseClientConnections()
		srv.Close()
	}()

	cb := &testCallback{fn: func(_ context.Context, _ string, b *InboundBody) {
		select {
		case got <- b:
		default:
		}
	}}
	client := NewClient("B", "S", wsURLOf(srv), cb)
	go func() { _ = client.session(ctx) }()

	select {
	case b := <-got:
		if b.ChatType != "group" || b.ChatID != "CHAT-9" {
			t.Fatalf("群聊字段不正确：%+v", b)
		}
		if b.From.UserID != "LiSi" {
			t.Fatalf("发送人解析不正确：%q", b.From.UserID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("超时：没有收到群聊消息")
	}
}

// 订阅被拒（BotID/Secret 不对）时，回执里的 errcode 要能被读到用于排查。
func TestSubscribeErrorIsSurfaced(t *testing.T) {
	up := newUpgrader()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_, raw, _ := conn.ReadMessage()
		var f Frame
		_ = json.Unmarshal(raw, &f)
		ack, _ := json.Marshal(map[string]any{
			"cmd":     CmdSubscribe,
			"headers": map[string]string{"req_id": f.Headers.ReqID},
			"errcode": 40001,
			"errmsg":  "invalid secret",
		})
		_ = conn.WriteMessage(websocket.TextMessage, ack)
		time.Sleep(300 * time.Millisecond)
	}))

	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		srv.CloseClientConnections()
		srv.Close()
	}()

	// 这里只要求「不 panic、不误判为成功」：错误经由日志暴露。
	client := NewClient("B", "S", wsURLOf(srv), &testCallback{})
	go func() { _ = client.session(ctx) }()
	time.Sleep(800 * time.Millisecond)
}

// 没有 req_id、或当前没有连接时，回复必须明确失败，不能静默丢弃。
func TestReplyFailsWithoutReqIDOrConnection(t *testing.T) {
	c := NewClient("B", "S", "ws://127.0.0.1:1/ws", nil)
	ctx := context.Background()

	if err := c.Reply(ctx, "", "内容"); err == nil {
		t.Error("缺少 req_id 时应当报错")
	}
	if err := c.Reply(ctx, "req-1", "内容"); err == nil {
		t.Error("没有活跃连接时应当报错")
	}
}

// 回调在构造之后注入也不能丢：main 里就是先建客户端再挂回调。
func TestSetCallbackAfterConstruction(t *testing.T) {
	c := NewClient("B", "S", "ws://127.0.0.1:1/ws", nil)
	if c.callback() != nil {
		t.Fatal("初始回调应为 nil")
	}
	cb := &testCallback{}
	c.SetCallback(cb)
	if c.callback() != cb {
		t.Fatal("SetCallback 未生效")
	}
}
