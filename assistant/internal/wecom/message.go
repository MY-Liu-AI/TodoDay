package wecom

import (
	"encoding/xml"
	"fmt"
)

// envelope 是回调 POST 的外层 XML。
// 需要注意的是：这只是用来取出 Encrypt，签名校验要用它的值而不是整个 body。
type envelope struct {
	XMLName    xml.Name `xml:"xml"`
	ToUserName string   `xml:"ToUserName"`
	AgentID    string   `xml:"AgentID"`
	Encrypt    string   `xml:"Encrypt"`
}

// Message 是解密后的入站消息。字段名与官方 XML 元素同名，
// 因此结构体上必须显式写 xml tag。
type Message struct {
	ToUserName   string `xml:"ToUserName"`
	FromUserName string `xml:"FromUserName"`
	CreateTime   int64  `xml:"CreateTime"`
	MsgType      string `xml:"MsgType"`
	Content      string `xml:"Content"`
	MsgID        string `xml:"MsgId"`
	AgentID      string `xml:"AgentID"`
}

// IsText 判断是否为文本消息。非文本一律忽略：本服务只懂文本。
func (m *Message) IsText() bool { return m.MsgType == "text" }

// parseMessage 反序列化解密后的明文。
func parseMessage(plain string) (*Message, error) {
	var m Message
	if err := xml.Unmarshal([]byte(plain), &m); err != nil {
		return nil, fmt.Errorf("解析消息 XML 失败：%w", err)
	}
	if m.FromUserName == "" {
		return nil, fmt.Errorf("消息缺少 FromUserName")
	}
	return &m, nil
}

// replyEnvelope 是加密回复的外层结构。仅在需要被动回复时使用；
// 本服务默认走「秒回 success + 异步推送」，因此这条路径主要供测试与
// 未来扩展使用。
type replyEnvelope struct {
	XMLName      xml.Name `xml:"xml"`
	Encrypt      string   `xml:"Encrypt"`
	MsgSignature string   `xml:"MsgSignature"`
	TimeStamp    int64    `xml:"TimeStamp"`
	Nonce        string   `xml:"Nonce"`
}

// buildReply 构造加密回复的 XML 文本。
func buildReply(encrypt, signature string, timestamp int64, nonce string) (string, error) {
	out, err := xml.Marshal(replyEnvelope{
		Encrypt:      encrypt,
		MsgSignature: signature,
		TimeStamp:    timestamp,
		Nonce:        nonce,
	})
	if err != nil {
		return "", fmt.Errorf("构造回复 XML 失败：%w", err)
	}
	return string(out), nil
}
