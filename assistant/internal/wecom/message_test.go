package wecom

import (
	"encoding/xml"
	"strings"
	"testing"
)

const textXML = `<xml><ToUserName><![CDATA[wx5823bf96d3bd56c7]]></ToUserName><FromUserName><![CDATA[ZhangSan]]></FromUserName><CreateTime>1348831860</CreateTime><MsgType><![CDATA[text]]></MsgType><Content><![CDATA[明天 交周报 #工作 !!!]]></Content><MsgId>1234567890123456</MsgId><AgentID>1000002</AgentID></xml>`

func TestParseMessage(t *testing.T) {
	m, err := parseMessage(textXML)
	if err != nil {
		t.Fatal(err)
	}
	if m.FromUserName != "ZhangSan" {
		t.Fatalf("FromUserName 解析错误：%q", m.FromUserName)
	}
	if m.Content != "明天 交周报 #工作 !!!" {
		t.Fatalf("Content 解析错误：%q", m.Content)
	}
	if m.MsgID != "1234567890123456" {
		t.Fatalf("MsgId 解析错误：%q", m.MsgID)
	}
	if !m.IsText() {
		t.Fatal("应当识别为文本消息")
	}
}

func TestParseMessageRejectsGarbage(t *testing.T) {
	if _, err := parseMessage("这不是 XML"); err == nil {
		t.Fatal("非 XML 应当报错")
	}
	// 缺少 FromUserName 的消息无法回复，属于异常输入。
	if _, err := parseMessage(`<xml><Content>hi</Content></xml>`); err == nil {
		t.Fatal("缺少 FromUserName 应当报错")
	}
}

func TestNonTextIsIgnored(t *testing.T) {
	img := `<xml><FromUserName><![CDATA[ZhangSan]]></FromUserName><MsgType><![CDATA[image]]></MsgType><PicUrl><![CDATA[http://x/y.jpg]]></PicUrl></xml>`
	m, err := parseMessage(img)
	if err != nil {
		t.Fatal(err)
	}
	if m.IsText() {
		t.Fatal("图片消息不应被当作文本处理")
	}
}

func TestEnvelopeParsing(t *testing.T) {
	raw := `<xml><ToUserName><![CDATA[corp]]></ToUserName><AgentID><![CDATA[1000002]]></AgentID><Encrypt><![CDATA[ABCDEF]]></Encrypt></xml>`
	var env envelope
	if err := xml.Unmarshal([]byte(raw), &env); err != nil {
		t.Fatal(err)
	}
	if env.Encrypt != "ABCDEF" {
		t.Fatalf("Encrypt 解析错误：%q", env.Encrypt)
	}
	if env.AgentID != "1000002" {
		t.Fatalf("AgentID 解析错误：%q", env.AgentID)
	}
}

func TestBuildReplyXML(t *testing.T) {
	out, err := buildReply("CIPHER", "SIG", 1348831860, "nonce123")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"CIPHER", "SIG", "nonce123", "1348831860"} {
		if !strings.Contains(out, want) {
			t.Fatalf("回复 XML 缺少 %q：%s", want, out)
		}
	}
}
