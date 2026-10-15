package config

import (
	"strings"
	"testing"
)

// setAll 把全部必填项设成合法值，单个测试再按需覆盖。
func setAll(t *testing.T) {
	t.Helper()
	t.Setenv("WECOM_BOT_ID", "BOTID-abc")
	t.Setenv("WECOM_BOT_SECRET", "bot-secret-value")
	t.Setenv("TODO_USERNAME", "bot")
	t.Setenv("TODO_PASSWORD", "password123")
	t.Setenv("TODO_WORKSPACE_ID", "7")
}

func TestLoadDefaults(t *testing.T) {
	setAll(t)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Addr != ":8090" {
		t.Errorf("默认监听地址应为 :8090，得到 %q", c.Addr)
	}
	if c.APIBase != "http://127.0.0.1:8080" {
		t.Errorf("默认后端地址不正确：%q", c.APIBase)
	}
	if c.WorkspaceID != 7 {
		t.Errorf("工作区 ID 不正确：%d", c.WorkspaceID)
	}
	if c.Model != "moonshot-v1-8k" {
		t.Errorf("默认模型不正确：%q", c.Model)
	}
	if c.ParseTimeout.String() != "8s" {
		t.Errorf("默认解析超时应为 8s，得到 %v", c.ParseTimeout)
	}
	// 长连接地址必须有默认值：这是唯一的接入通道。
	if c.WSURL != "wss://openws.work.weixin.qq.com" {
		t.Errorf("默认长连接地址不正确：%q", c.WSURL)
	}
	if c.KimiEnabled() {
		t.Error("未设置密钥时 AI 解析应当是关闭的")
	}
}

func TestLoadCustomValues(t *testing.T) {
	setAll(t)
	t.Setenv("ASSISTANT_ADDR", ":9000")
	t.Setenv("TODO_API_BASE", "http://api.internal:8080/")
	t.Setenv("WECOM_BOT_WS_URL", "ws://127.0.0.1:9999/ws")
	t.Setenv("KIMI_API_KEY", "sk-test")
	t.Setenv("ASSISTANT_MODEL", "kimi-k2-0905-preview")
	t.Setenv("PARSE_TIMEOUT", "3s")

	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Addr != ":9000" {
		t.Errorf("监听地址不正确：%q", c.Addr)
	}
	// 末尾斜杠要去掉，否则拼出来的路径会变成 //api/...
	if c.APIBase != "http://api.internal:8080" {
		t.Errorf("后端地址应当去掉末尾斜杠，得到 %q", c.APIBase)
	}
	if c.WSURL != "ws://127.0.0.1:9999/ws" {
		t.Errorf("自定义长连接地址未生效：%q", c.WSURL)
	}
	if !c.KimiEnabled() || c.Model != "kimi-k2-0905-preview" {
		t.Errorf("AI 配置不正确：enabled=%v model=%q", c.KimiEnabled(), c.Model)
	}
	if c.ParseTimeout.String() != "3s" {
		t.Errorf("解析超时不正确：%v", c.ParseTimeout)
	}
}

// MOONSHOT_API_KEY 是 KIMI_API_KEY 的别名，两个都能用。
func TestMoonshotKeyAlias(t *testing.T) {
	setAll(t)
	t.Setenv("MOONSHOT_API_KEY", "sk-moonshot")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !c.KimiEnabled() || c.KimiKey != "sk-moonshot" {
		t.Fatalf("别名键未被识别：enabled=%v", c.KimiEnabled())
	}
}

// 缺配置必须在启动时就失败，并且报出缺了哪些键名（而不是只报第一个）。
func TestLoadReportsAllMissingKeys(t *testing.T) {
	for _, k := range required {
		t.Setenv(k, "")
	}
	_, err := Load()
	if err == nil {
		t.Fatal("缺少必填配置时应当报错")
	}
	msg := err.Error()
	for _, k := range []string{"WECOM_BOT_ID", "TODO_USERNAME", "TODO_WORKSPACE_ID"} {
		if !strings.Contains(msg, k) {
			t.Errorf("错误信息应包含缺失的键名 %s：%s", k, msg)
		}
	}
}

// 只有空白的值等同于未设置：粘贴配置时很容易带上空格。
func TestWhitespaceIsTreatedAsMissing(t *testing.T) {
	setAll(t)
	t.Setenv("WECOM_BOT_SECRET", "   ")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "WECOM_BOT_SECRET") {
		t.Fatalf("纯空白应当视为未设置，得到 %v", err)
	}
}

func TestInvalidWorkspaceID(t *testing.T) {
	for _, bad := range []string{"abc", "0", "-3", "  "} {
		setAll(t)
		t.Setenv("TODO_WORKSPACE_ID", bad)
		if _, err := Load(); err == nil {
			t.Errorf("工作区 ID %q 应当被拒绝", bad)
		}
	}
}

// 长连接地址写错（比如漏了协议、或写成 http://）要在启动时就拦住，
// 否则会等到重连循环里才以一个难懂的握手错误暴露出来。
func TestInvalidWebSocketURL(t *testing.T) {
	for _, bad := range []string{
		"openws.work.weixin.qq.com",         // 缺协议
		"https://openws.work.weixin.qq.com", // 协议写错
		"127.0.0.1:9999",
	} {
		setAll(t)
		t.Setenv("WECOM_BOT_WS_URL", bad)
		if _, err := Load(); err == nil {
			t.Errorf("长连接地址 %q 应当被拒绝", bad)
		}
	}
}

func TestInvalidParseTimeout(t *testing.T) {
	setAll(t)
	t.Setenv("PARSE_TIMEOUT", "八秒")
	if _, err := Load(); err == nil {
		t.Fatal("非法时长应当被拒绝")
	}
}

// 旧的自建应用配置项不该再被当作必填：迁移后只剩 BotID + Secret。
func TestLongConnectionCredentialsReplaceOldFields(t *testing.T) {
	setAll(t)
	// 故意设置旧键，它们不应影响任何行为。
	t.Setenv("WECOM_CORP_ID", "wx-old")
	t.Setenv("WECOM_AGENT_ID", "1000002")
	t.Setenv("WECOM_TOKEN", "old-token")
	t.Setenv("WECOM_ENCODING_AES_KEY", "old-key")

	c, err := Load()
	if err != nil {
		t.Fatalf("旧键不应导致失败：%v", err)
	}
	if c.BotID != "BOTID-abc" || c.BotSecret != "bot-secret-value" {
		t.Fatalf("应使用长连接凭据：%+v", c)
	}
}
