// Package config 读取并校验助手服务的全部配置。
//
// 缺失必填项时启动即失败，并且只打印「缺了哪些键名」而不打印任何值 ——
// 半套配置跑起来比起不来更难排查。
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	// 本服务（长连接模式下只用于健康检查，不再是回调入口）
	Addr string

	// 企业微信智能机器人（长连接模式）：BotID + Secret。
	// 注意这与「接收 URL」模式的 Token + EncodingAESKey 是两套凭据，不能混用。
	BotID     string
	BotSecret string
	WSURL     string

	// TodoDay 后端
	APIBase     string
	Username    string
	Password    string
	WorkspaceID int64

	// 解析（Kimi / Moonshot，OpenAI 兼容接口）
	KimiKey      string
	KimiBase     string
	Model        string
	ParseTimeout time.Duration
}

// required 列出必填键，缺失时按此顺序报出。
//
// 机器人凭据不再必填：现在可以在网页设置页里填，助手启动后从后端取。
// 环境变量里的 WECOM_BOT_ID / WECOM_BOT_SECRET 仍可用，作为后端未配置时的兜底。
var required = []string{
	"TODO_USERNAME",
	"TODO_PASSWORD",
	"TODO_WORKSPACE_ID",
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// firstOf 返回第一个有值的环境变量，用于同一含义的多个键名。
func firstOf(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

// Load 读取配置。返回的错误会列出所有缺失或非法的键名。
func Load() (*Config, error) {
	c := &Config{
		Addr:      env("ASSISTANT_ADDR", ":8090"),
		BotID:     env("WECOM_BOT_ID", ""),
		BotSecret: env("WECOM_BOT_SECRET", ""),
		WSURL:     env("WECOM_BOT_WS_URL", "wss://openws.work.weixin.qq.com"),

		APIBase:  strings.TrimRight(env("TODO_API_BASE", "http://127.0.0.1:8080"), "/"),
		Username: env("TODO_USERNAME", ""),
		Password: env("TODO_PASSWORD", ""),

		// 兼容 MOONSHOT_API_KEY：两者都是 Moonshot 的密钥，取先设置的那个。
		KimiKey:  firstOf("KIMI_API_KEY", "MOONSHOT_API_KEY"),
		KimiBase: strings.TrimRight(env("KIMI_BASE_URL", "https://api.moonshot.cn/v1"), "/"),
		Model:    env("ASSISTANT_MODEL", "moonshot-v1-8k"),
	}

	var missing []string
	for _, key := range required {
		if strings.TrimSpace(os.Getenv(key)) == "" {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("缺少必填配置：%s", strings.Join(missing, "、"))
	}

	widRaw := strings.TrimSpace(os.Getenv("TODO_WORKSPACE_ID"))
	wid, err := strconv.ParseInt(widRaw, 10, 64)
	if err != nil || wid <= 0 {
		return nil, fmt.Errorf("TODO_WORKSPACE_ID 必须是正整数，当前值 %q", widRaw)
	}
	c.WorkspaceID = wid

	// 长连接地址必须是 wss:// 开头：ws:// 只在本地调试时才有意义，
	// 这里允许，以便对着测试服务器跑。
	if !strings.HasPrefix(c.WSURL, "wss://") && !strings.HasPrefix(c.WSURL, "ws://") {
		return nil, fmt.Errorf("WECOM_BOT_WS_URL 必须以 wss:// 或 ws:// 开头，当前值 %q", c.WSURL)
	}

	d, err := time.ParseDuration(env("PARSE_TIMEOUT", "8s"))
	if err != nil || d <= 0 {
		return nil, fmt.Errorf("PARSE_TIMEOUT 不是合法时长（如 8s）")
	}
	c.ParseTimeout = d

	return c, nil
}

// KimiEnabled 说明是否配置了 AI 解析。未配置时全部走规则解析。
func (c *Config) KimiEnabled() bool { return c.KimiKey != "" }

// HasEnvBot 说明环境变量里是否给了机器人凭据（兜底路径）。
func (c *Config) HasEnvBot() bool { return c.BotID != "" && c.BotSecret != "" }

// Credentials 是最终生效的一组凭据。来源可能是后端或环境变量。
type Credentials struct {
	// Enabled 只由后端决定：网页上停用就是停用，不因为环境变量里还留着
	// 凭据就继续连接。环境变量路径（后端未配置）视为启用。
	Enabled   bool
	BotID     string
	BotSecret string
	KimiKey   string
	KimiBase  string
	Model     string
	Source    string // "backend" | "env"
}

// Resolve 把「后端返回的配置」与「环境变量」合并成最终生效值。
//
// 规则分两种情形：
//
//   - 后端**没有**配置行（remote == nil）：全用环境变量。这是现有 .env 部署
//     的路径，必须继续可用。
//   - 后端**有**配置行：以它为准，**机密与身份字段不回落环境变量**
//     （BotID / BotSecret / KimiKey）。只有接口地址、模型名这类非机密项
//     在后端留空时才取配置里的默认值。
//
// 为什么不逐字段回落：那会让「撤销」失效。所有者把后端的密钥清空后，
// 环境变量里残留的旧凭据会把它悄悄顶回来，助手照旧带着一份用户以为已经
// 撤掉的凭据运行 —— 属于 fail-open。凭据类字段跨来源静默混用还可能出现
// 「后端的新 Secret 配上环境变量的旧 BotID」这种错配。安全上的账不划算。
func (c *Config) Resolve(remote *RemoteCredentials) Credentials {
	if remote == nil {
		return Credentials{
			// 走环境变量意味着运维是特意配的，视为启用。
			Enabled:   true,
			BotID:     c.BotID,
			BotSecret: c.BotSecret,
			KimiKey:   c.KimiKey,
			KimiBase:  c.KimiBase,
			Model:     c.Model,
			Source:    "env",
		}
	}
	out := Credentials{
		// 启用状态同样只由后端决定：停用是用户的显式意图。
		Enabled:   remote.Enabled,
		BotID:     remote.BotID,
		BotSecret: remote.BotSecret,
		KimiKey:   remote.KimiAPIKey,
		KimiBase:  strings.TrimRight(remote.KimiBase, "/"),
		Model:     remote.KimiModel,
		Source:    "backend",
	}
	// 非机密项允许取默认值：它们不影响凭据的有效性。
	if out.KimiBase == "" {
		out.KimiBase = c.KimiBase
	}
	if out.Model == "" {
		out.Model = c.Model
	}
	return out
}

// RemoteCredentials 是后端返回的凭据形状。与 todoclient.BotCredentials 字段
// 一致，但在这里独立声明以避免 config 依赖 todoclient。
type RemoteCredentials struct {
	Enabled    bool
	BotID      string
	BotSecret  string
	KimiAPIKey string
	KimiBase   string
	KimiModel  string
}
