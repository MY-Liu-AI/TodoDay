package config

import (
	"strings"
	"testing"
)

// 企业微信的 EncodingAESKey 固定 43 个字符。
const validKey = "jWmYm7qr5nMoAUwZRjGtBxmz3KA1tkAj3ykkR6q2B2C"

// setAll 把全部必填项设成合法值，单个测试再按需覆盖。
func setAll(t *testing.T) {
	t.Helper()
	t.Setenv("WECOM_CORP_ID", "wx5823bf96d3bd56c7")
	t.Setenv("WECOM_AGENT_ID", "1000002")
	t.Setenv("WECOM_SECRET", "secret-value")
	t.Setenv("WECOM_TOKEN", "QDG6eK")
	t.Setenv("WECOM_ENCODING_AES_KEY", validKey)
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
	if c.KimiEnabled() {
		t.Error("未设置密钥时 AI 解析应当是关闭的")
	}
}

func TestLoadCustomValues(t *testing.T) {
	setAll(t)
	t.Setenv("ASSISTANT_ADDR", ":9000")
	t.Setenv("TODO_API_BASE", "http://api.internal:8080/")
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
	// 一个都不设。
	for _, k := range required {
		t.Setenv(k, "")
	}
	_, err := Load()
	if err == nil {
		t.Fatal("缺少必填配置时应当报错")
	}
	msg := err.Error()
	for _, k := range []string{"WECOM_CORP_ID", "TODO_USERNAME", "TODO_WORKSPACE_ID"} {
		if !strings.Contains(msg, k) {
			t.Errorf("错误信息应包含缺失的键名 %s：%s", k, msg)
		}
	}
}

// 只有空白的值等同于未设置：粘贴配置时很容易带上空格。
func TestWhitespaceIsTreatedAsMissing(t *testing.T) {
	setAll(t)
	t.Setenv("WECOM_TOKEN", "   ")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "WECOM_TOKEN") {
		t.Fatalf("纯空白应当视为未设置，得到 %v", err)
	}
}

// AESKey 长度不对要在启动时报错，不能等到第一条消息进来才失败。
func TestInvalidAESKeyLength(t *testing.T) {
	setAll(t)
	t.Setenv("WECOM_ENCODING_AES_KEY", "tooshort")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "43") {
		t.Fatalf("应当报出 AESKey 长度问题，得到 %v", err)
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

func TestInvalidParseTimeout(t *testing.T) {
	setAll(t)
	t.Setenv("PARSE_TIMEOUT", "八秒")
	if _, err := Load(); err == nil {
		t.Fatal("非法时长应当被拒绝")
	}
}
