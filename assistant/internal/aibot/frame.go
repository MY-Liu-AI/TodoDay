// Package aibot 接入企业微信「智能机器人」的长连接模式。
//
// 为什么是这个模式而不是自建应用的接收消息回调：长连接由我方主动连出去
// （wss://openws.work.weixin.qq.com），**不需要公网 HTTPS、不需要反向代理、
// 消息是明文 JSON 不需要加解密**。代价是要常驻进程、保持心跳、自己处理重连。
//
// 几个必须守住的点：
//   - 订阅用 BotID + Secret（不是 CorpID / Token / EncodingAESKey —— 那是
//     接收 URL 模式的凭据，两者不通用）。
//   - 同一个机器人**只能保持一条有效长连接**，新连接订阅成功会踢掉旧的。
//     所以断线重连要退避，避免和别的实例互踢。
//   - 回复必须带上回调里的 req_id（放在 headers 里）。没有它回复无处可去。
//   - ping 命令名就是 "ping"，建议 30 秒一次；久不发服务端会主动断开。
package aibot

import "encoding/json"

// 帧命令名。拼错一个都表现为「连上了但什么都不发生」，很难排查。
const (
	CmdSubscribe     = "aibot_subscribe"
	CmdPing          = "ping"
	CmdRespondMsg    = "aibot_respond_msg"
	CmdSendMsg       = "aibot_send_msg"
	CmdMsgCallback   = "aibot_msg_callback"
	CmdEventCallback = "aibot_event_callback"
)

// 消息类型。msgtype 的值同时也是承载内容的字段名（见 Frame.Body）。
const (
	MsgTypeText     = "text"
	MsgTypeMarkdown = "markdown"
	MsgTypeStream   = "stream"
	MsgTypeImage    = "image"
	MsgTypeFile     = "file"
	MsgTypeVoice    = "voice"
	MsgTypeVideo    = "video"
)

// Frame 是长连接上的统一帧结构。入站与出站共用：
// 入站时 cmd 是 aibot_msg_callback / aibot_event_callback，
// 出站时 cmd 是 aibot_respond_msg / aibot_send_msg / aibot_subscribe / ping。
type Frame struct {
	Cmd     string          `json:"cmd"`
	Headers FrameHeaders    `json:"headers"`
	Body    json.RawMessage `json:"body,omitempty"`

	// 以下字段只在服务端响应我方请求时出现（如订阅结果、ping 回应）。
	ErrCode int    `json:"errcode,omitempty"`
	ErrMsg  string `json:"errmsg,omitempty"`
}

// FrameHeaders 目前只有 req_id。
//
// req_id 是这条链路的枢纽：订阅/心跳时由我方生成；消息回调时由服务端下发，
// 回复时必须原样透传回去。同一次回调的所有流式刷新共用同一个 req_id。
type FrameHeaders struct {
	ReqID string `json:"req_id"`
}

// TextBody 是 text 类型的内容载体。
type TextBody struct {
	Content string `json:"content"`
}

// SubscribeBody 是订阅请求的 body。
type SubscribeBody struct {
	BotID  string `json:"bot_id"`
	Secret string `json:"secret"`
}

// InboundBody 是消息回调的 body。字段名与官方一致，不能改。
type InboundBody struct {
	MsgID    string `json:"msgid"`    // 排重用；网络原因可能重复推送
	AIBotID  string `json:"aibotid"`  // 机器人 ID
	ChatID   string `json:"chatid"`   // 仅群聊返回
	ChatType string `json:"chattype"` // single / group
	MsgType  string `json:"msgtype"`  // text / image / mixed / voice / file ...

	From struct {
		UserID string `json:"userid"`
	} `json:"from"`

	// 内容字段名随 MsgType 变化，逐个列出已知的类型。
	Text  *TextBody `json:"text,omitempty"`
	Voice *TextBody `json:"voice,omitempty"` // 语音已转文本，同样放在 content 里

	// 多媒体消息带独立的 aeskey 用于解密，且 URL 5 分钟失效。
	Image *struct {
		URL    string `json:"url"`
		AESKey string `json:"aeskey"`
	} `json:"image,omitempty"`
	File *struct {
		URL    string `json:"url"`
		AESKey string `json:"aeskey"`
	} `json:"file,omitempty"`
}

// EventBody 是事件回调的 body（进入会话、卡片点击等）。
// 本服务只关心是否有内容要记录，事件一律忽略，但把它们解出来便于记日志。
type EventBody struct {
	MsgID    string `json:"msgid"`
	ChatType string `json:"chattype"`
	From     struct {
		UserID string `json:"userid"`
	} `json:"from"`
	Event struct {
		EventType string `json:"eventtype"` // enter_chat / template_card_event / feedback_event ...
	} `json:"event"`
}

// Content 取出文本内容。非文本消息返回空串，由调用方决定忽略。
func (b *InboundBody) Content() string {
	switch b.MsgType {
	case MsgTypeText:
		if b.Text != nil {
			return b.Text.Content
		}
	case MsgTypeVoice:
		if b.Voice != nil {
			return b.Voice.Content
		}
	}
	return ""
}

// IsText 判断是否是可记录的文本消息。语音已转文本，同样算。
func (b *InboundBody) IsText() bool {
	return b.MsgType == MsgTypeText || b.MsgType == MsgTypeVoice
}

// NewTextReply 构造一条文本回复帧。
//
// reqID 必须是回调里带的那个值：长连接的回复不是「回给某个人」，
// 而是「回给某次回调」，丢了 req_id 就发不出去。
func NewTextReply(reqID, content string) *Frame {
	body, _ := json.Marshal(map[string]any{MsgTypeText: TextBody{Content: content}})
	return &Frame{
		Cmd:     CmdRespondMsg,
		Headers: FrameHeaders{ReqID: reqID},
		Body:    body,
	}
}

// NewSubscribe 构造订阅帧。
func NewSubscribe(reqID, botID, secret string) *Frame {
	body, _ := json.Marshal(SubscribeBody{BotID: botID, Secret: secret})
	return &Frame{
		Cmd:     CmdSubscribe,
		Headers: FrameHeaders{ReqID: reqID},
		Body:    body,
	}
}

// NewPing 构造心跳帧。
func NewPing(reqID string) *Frame {
	return &Frame{Cmd: CmdPing, Headers: FrameHeaders{ReqID: reqID}}
}

// DecodeBody 把 Body 解成入站消息。
func (f *Frame) DecodeBody() (*InboundBody, error) {
	var b InboundBody
	if err := json.Unmarshal(f.Body, &b); err != nil {
		return nil, err
	}
	return &b, nil
}
