package aibot

import (
	"encoding/json"
	"testing"
)

func TestNewMarkdownReplyCarriesReqID(t *testing.T) {
	f := NewMarkdownReply("req-abc", "已记录")
	if f.Cmd != CmdRespondMsg {
		t.Fatalf("cmd 应为 %s，得到 %s", CmdRespondMsg, f.Cmd)
	}
	// req_id 是回复的唯一去路，丢了就发不出去。
	if f.Headers.ReqID != "req-abc" {
		t.Fatalf("req_id 未透传：%q", f.Headers.ReqID)
	}
	var body struct {
		Markdown struct {
			Content string `json:"content"`
		} `json:"markdown"`
	}
	if err := json.Unmarshal(f.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body.Markdown.Content != "已记录" {
		t.Fatalf("内容不正确：%q", body.Markdown.Content)
	}
}

// 出站帧的字段名必须是官方那一套，改了服务端就不认。
func TestReplyFrameJSONShape(t *testing.T) {
	raw, err := json.Marshal(NewMarkdownReply("r1", "hi"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["cmd"] != "aibot_respond_msg" {
		t.Errorf("cmd 不正确：%v", m["cmd"])
	}
	headers, ok := m["headers"].(map[string]any)
	if !ok || headers["req_id"] != "r1" {
		t.Errorf("headers.req_id 不正确：%v", m["headers"])
	}
	body, ok := m["body"].(map[string]any)
	if !ok {
		t.Fatalf("body 缺失：%v", m)
	}
	if body["msgtype"] != "markdown" {
		t.Errorf("msgtype 应为 markdown，得到 %v", body["msgtype"])
	}
	if _, ok := body["markdown"].(map[string]any); !ok {
		t.Errorf("缺少 body.markdown：%v", body)
	}
}

func TestNewSubscribeAndPing(t *testing.T) {
	sub := NewSubscribe("sub_1", "BOTID", "SECRET")
	if sub.Cmd != CmdSubscribe || sub.Headers.ReqID != "sub_1" {
		t.Fatalf("订阅帧不正确：%+v", sub)
	}
	var sb SubscribeBody
	if err := json.Unmarshal(sub.Body, &sb); err != nil {
		t.Fatal(err)
	}
	if sb.BotID != "BOTID" || sb.Secret != "SECRET" {
		t.Fatalf("订阅凭据不正确：%+v", sb)
	}

	ping := NewPing("ping_1")
	if ping.Cmd != CmdPing || ping.Headers.ReqID != "ping_1" {
		t.Fatalf("心跳帧不正确：%+v", ping)
	}
	if ping.Body != nil {
		t.Errorf("心跳不需要 body：%s", ping.Body)
	}
}

// 入站消息的解析：字段名与官方一致，内容随 msgtype 落在对应子结构里。
func TestDecodeInboundMessage(t *testing.T) {
	raw := []byte(`{
	  "cmd":"aibot_msg_callback",
	  "headers":{"req_id":"req-1"},
	  "body":{
	    "msgid":"MSGID-1",
	    "aibotid":"BOTID",
	    "chattype":"single",
	    "msgtype":"text",
	    "from":{"userid":"ZhangSan"},
	    "text":{"content":"明天 交周报 #工作 !!!"}
	  }
	}`)
	var f Frame
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	if f.Cmd != CmdMsgCallback {
		t.Fatalf("cmd 不正确：%s", f.Cmd)
	}
	if f.Headers.ReqID != "req-1" {
		t.Fatalf("req_id 不正确：%q", f.Headers.ReqID)
	}
	b, err := f.DecodeBody()
	if err != nil {
		t.Fatal(err)
	}
	if b.MsgID != "MSGID-1" || b.From.UserID != "ZhangSan" || b.ChatType != "single" {
		t.Fatalf("字段解析不正确：%+v", b)
	}
	if b.Content() != "明天 交周报 #工作 !!!" {
		t.Fatalf("内容解析不正确：%q", b.Content())
	}
	if !b.IsText() {
		t.Error("应当识别为文本消息")
	}
}

func TestDecodeInboundGroupMessage(t *testing.T) {
	raw := []byte(`{"cmd":"aibot_msg_callback","headers":{"req_id":"r"},"body":{
	  "msgid":"M","chatid":"CHAT1","chattype":"group","msgtype":"text",
	  "from":{"userid":"LiSi"},"text":{"content":"买牛奶"}}}`)
	var f Frame
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	b, err := f.DecodeBody()
	if err != nil {
		t.Fatal(err)
	}
	if b.ChatType != "group" || b.ChatID != "CHAT1" {
		t.Fatalf("群聊字段不正确：%+v", b)
	}
}

// 语音消息已转文本，同样可记录。
func TestVoiceIsTreatableAsText(t *testing.T) {
	raw := []byte(`{"cmd":"aibot_msg_callback","headers":{"req_id":"r"},"body":{
	  "msgid":"M","msgtype":"voice","from":{"userid":"u"},
	  "voice":{"content":"明天开会"}}}`)
	var f Frame
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	b, err := f.DecodeBody()
	if err != nil {
		t.Fatal(err)
	}
	if !b.IsText() {
		t.Fatal("语音应当可当作文本处理")
	}
	if b.Content() != "明天开会" {
		t.Fatalf("语音转文本内容不正确：%q", b.Content())
	}
}

// 非文本消息必须被判为不可处理，否则会把空字符串当任务建出去。
func TestNonTextIsNotTreatable(t *testing.T) {
	for _, mt := range []string{"image", "file", "mixed", "video"} {
		b := &InboundBody{MsgType: mt}
		if b.IsText() {
			t.Errorf("%s 不应被当作文本", mt)
		}
		if b.Content() != "" {
			t.Errorf("%s 不应当有文本内容", mt)
		}
	}
}

func TestDecodeEventCallback(t *testing.T) {
	raw := []byte(`{"cmd":"aibot_event_callback","headers":{"req_id":"r"},"body":{
	  "msgid":"M","chattype":"single","from":{"userid":"u"},
	  "event":{"eventtype":"enter_chat"}}}`)
	var f Frame
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	if f.Cmd != CmdEventCallback {
		t.Fatalf("cmd 不正确：%s", f.Cmd)
	}
	var b EventBody
	if err := json.Unmarshal(f.Body, &b); err != nil {
		t.Fatal(err)
	}
	if b.Event.EventType != "enter_chat" || b.From.UserID != "u" {
		t.Fatalf("事件字段不正确：%+v", b)
	}
}

// 订阅/心跳的回执带 errcode，非 0 时应当能被读出来用于报错。
func TestDecodeAckFrame(t *testing.T) {
	var f Frame
	if err := json.Unmarshal([]byte(`{"cmd":"aibot_subscribe","headers":{"req_id":"s"},"errcode":40001,"errmsg":"invalid secret"}`), &f); err != nil {
		t.Fatal(err)
	}
	if f.ErrCode != 40001 || f.ErrMsg != "invalid secret" {
		t.Fatalf("回执错误信息未解析：%+v", f)
	}
}

// 命令名常量必须与官方一致 —— 拼错一个都表现为「连上了但什么都不发生」。
func TestCommandConstants(t *testing.T) {
	cases := map[string]string{
		CmdSubscribe:     "aibot_subscribe",
		CmdPing:          "ping",
		CmdRespondMsg:    "aibot_respond_msg",
		CmdSendMsg:       "aibot_send_msg",
		CmdMsgCallback:   "aibot_msg_callback",
		CmdEventCallback: "aibot_event_callback",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("命令名不符：得到 %q，期望 %q", got, want)
		}
	}
}
