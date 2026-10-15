package config

import (
	"strings"
	"testing"
)

// setAll 把全部必填项设成合法值，单个测试再按需覆盖。
// 机器人凭据已不是必填：可以在网页设置页里填，助手从后端取。
func setAll(t *testing.T) {
	t.Helper()
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
	if c.WSURL != "wss://openws.work.weixin.qq.com" {
		t.Errorf("默认长连接地址不正确：%q", c.WSURL)
	}
	if c.KimiEnabled() {
		t.Error("未设置密钥时 AI 解析应当是关闭的")
	}
	if c.HasEnvBot() {
		t.Error("未设置机器人凭据时 HasEnvBot 应为 false")
	}
}

// 机器人凭据不再是启动必填：没有也能起来，由后端配置决定是否可连。
func TestBotCredentialsAreOptionalAtLoad(t *testing.T) {
	setAll(t)
	t.Setenv("WECOM_BOT_ID", "")
	t.Setenv("WECOM_BOT_SECRET", "")
	if _, err := Load(); err != nil {
		t.Fatalf("缺少机器人凭据不应阻止启动：%v", err)
	}
}

func TestLoadCustomValues(t *testing.T) {
	setAll(t)
	t.Setenv("ASSISTANT_ADDR", ":9000")
	t.Setenv("TODO_API_BASE", "http://api.internal:8080/")
	t.Setenv("WECOM_BOT_ID", "B1")
	t.Setenv("WECOM_BOT_SECRET", "S1")
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
	if c.APIBase != "http://api.internal:8080" {
		t.Errorf("后端地址应当去掉末尾斜杠，得到 %q", c.APIBase)
	}
	if !c.HasEnvBot() || c.BotID != "B1" || c.BotSecret != "S1" {
		t.Errorf("环境变量机器人凭据未生效：%+v", c)
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

func TestLoadReportsAllMissingKeys(t *testing.T) {
	for _, k := range required {
		t.Setenv(k, "")
	}
	_, err := Load()
	if err == nil {
		t.Fatal("缺少必填配置时应当报错")
	}
	msg := err.Error()
	for _, k := range []string{"TODO_USERNAME", "TODO_PASSWORD", "TODO_WORKSPACE_ID"} {
		if !strings.Contains(msg, k) {
			t.Errorf("错误信息应包含缺失的键名 %s：%s", k, msg)
		}
	}
	// 机器人凭据不应出现在「缺失必填」里。
	if strings.Contains(msg, "WECOM_BOT_ID") {
		t.Error("机器人凭据已不是必填，不应报缺失")
	}
}

func TestWhitespaceIsTreatedAsMissing(t *testing.T) {
	setAll(t)
	t.Setenv("TODO_PASSWORD", "   ")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "TODO_PASSWORD") {
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

func TestInvalidWebSocketURL(t *testing.T) {
	for _, bad := range []string{"openws.work.weixin.qq.com", "https://openws.work.weixin.qq.com", "127.0.0.1:9999"} {
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

/* ── Resolve：后端优先、环境变量兜底 ────────────────────────────────── */

func envCfg() *Config {
	return &Config{
		BotID: "env-bot", BotSecret: "env-secret",
		KimiKey: "env-kimi", KimiBase: "https://env.example/v1", Model: "env-model",
	}
}

// 后端没配（nil）→ 全部用环境变量。这是现有 .env 部署不被破坏的保证。
func TestResolveFallsBackToEnvWhenNoRemote(t *testing.T) {
	got := envCfg().Resolve(nil)
	if got.Source != "env" {
		t.Fatalf("来源应为 env，得到 %s", got.Source)
	}
	if got.BotID != "env-bot" || got.BotSecret != "env-secret" || got.KimiKey != "env-kimi" {
		t.Fatalf("应全部取环境变量：%+v", got)
	}
}

// 后端配了 → 后端优先覆盖。这是网页配置能生效的保证。
func TestResolvePrefersRemote(t *testing.T) {
	got := envCfg().Resolve(&RemoteCredentials{
		BotID: "web-bot", BotSecret: "web-secret",
		KimiAPIKey: "web-kimi", KimiBase: "https://web.example/v1/", KimiModel: "web-model",
	})
	if got.Source != "backend" {
		t.Fatalf("来源应为 backend，得到 %s", got.Source)
	}
	if got.BotID != "web-bot" || got.BotSecret != "web-secret" {
		t.Fatalf("机器人凭据应取后端：%+v", got)
	}
	if got.KimiKey != "web-kimi" || got.Model != "web-model" {
		t.Fatalf("Kimi 配置应取后端：%+v", got)
	}
	// 末尾斜杠要去掉，否则拼路径会出现 //chat/completions。
	if got.KimiBase != "https://web.example/v1" {
		t.Fatalf("KimiBase 应去掉末尾斜杠：%q", got.KimiBase)
	}
}

// 后端某个字段为空 → 该字段回落到环境变量，其余仍取后端。
// 典型场景：用户只在网页填了机器人，Kimi 仍用 .env 里的。
func TestResolveFieldLevelFallback(t *testing.T) {
	got := envCfg().Resolve(&RemoteCredentials{
		BotID: "web-bot", BotSecret: "web-secret",
		// Kimi 三项都留空
	})
	if got.BotID != "web-bot" {
		t.Fatalf("机器人应取后端：%+v", got)
	}
	if got.KimiKey != "env-kimi" || got.KimiBase != "https://env.example/v1" || got.Model != "env-model" {
		t.Fatalf("后端为空的字段应回落到环境变量：%+v", got)
	}
	if got.Source != "backend" {
		t.Fatalf("只要后端有配置，来源就应标为 backend：%s", got.Source)
	}
}

// 两边都没有 → 结果为空，由调用方决定是否致命。
func TestResolveWithNothing(t *testing.T) {
	got := (&Config{}).Resolve(nil)
	if got.BotID != "" || got.BotSecret != "" {
		t.Fatalf("两边都没有时应为空：%+v", got)
	}
}
