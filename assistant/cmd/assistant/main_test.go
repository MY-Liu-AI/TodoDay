package main

import (
	"net/url"
	"strings"
	"testing"
)

// echostr 是 base64，含有 + / = 三个在 query string 里有特殊含义的字符。
// 这里断言「拼出来的 URL 解析回来必须还原成原值」—— 手工拼接时 + 会被
// 解析成空格，签名必然不匹配、解密也失败，而这个自测工具恰恰是用来
// 验证签名与解密的，出错时会把人引向完全错误的方向。
func TestHandshakeURLRoundTripsBase64(t *testing.T) {
	cases := []string{
		"8Z80liJZsdz0HNcuJ0V3FJunfSA5r1apIVe7WMobfoA71NWz1fUsUd5syWi/IAg4JPyqk4gJHfYV8X8H+IR/EQ==",
		"a+b/c==",
		"plus+plus",
		"slash/slash",
		"equal==",
		"plain",
		"",
	}
	for _, echo := range cases {
		raw := handshakeURL(":8090", "/wecom/callback", "SIG", "1409659589", "nonce1", echo)

		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("生成的 URL 无法解析：%v（%s）", err, raw)
		}
		got := u.Query().Get("echostr")
		if got != echo {
			t.Errorf("echostr 往返不一致：\n输入 %q\n得到 %q\nURL  %s", echo, got, raw)
		}
		if sig := u.Query().Get("msg_signature"); sig != "SIG" {
			t.Errorf("msg_signature 不正确：%q", sig)
		}
		if ts := u.Query().Get("timestamp"); ts != "1409659589" {
			t.Errorf("timestamp 不正确：%q", ts)
		}
		if n := u.Query().Get("nonce"); n != "nonce1" {
			t.Errorf("nonce 不正确：%q", n)
		}
	}
}

// 生成的地址必须带上协议与路径，能直接粘进 curl。
func TestHandshakeURLShape(t *testing.T) {
	raw := handshakeURL(":8090", "/wecom/callback", "S", "1", "n", "e")
	if !strings.HasPrefix(raw, "http://localhost:8090/wecom/callback?") {
		t.Fatalf("URL 形状不正确：%s", raw)
	}
}

func TestDueOrNone(t *testing.T) {
	if dueOrNone("") != "未排期" {
		t.Error("空日期应当显示未排期")
	}
	if dueOrNone("2026-10-16") != "2026-10-16" {
		t.Error("有日期时应原样返回")
	}
}
