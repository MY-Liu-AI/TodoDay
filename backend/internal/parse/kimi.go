package parse

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"tododay/internal/ai"
)

// apiBase 默认取 Moonshot（Kimi）的 OpenAI 兼容地址；测试时替换为 httptest 地址。
var apiBase = "https://api.moonshot.cn/v1"

const toolName = "record_plan"

// Kimi 是 Moonshot 的 Kimi 客户端。协议传输（DoChat）在 ai 包，
// 这里只保留解析语义：工具 schema、参数清洗与收敛。
//
// 走 OpenAI 兼容的 /chat/completions，用 function calling 拿结构化结果：
// 模型必须按参数 schema 填，省掉一层「解析模型自由输出」的脆弱逻辑。
type Kimi struct {
	APIKey string
	Model  string
	HTTP   *http.Client
	base   string
}

func NewKimi(apiKey, baseURL, model string, timeout time.Duration) *Kimi {
	if model == "" {
		// Moonshot 新平台账号已没有 moonshot-v1-* 系列（404）；
		// 与 ai.defaultModel 保持一致。
		model = "kimi-k2.7-code-highspeed"
	}
	if baseURL == "" {
		baseURL = apiBase
	}
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	return &Kimi{APIKey: apiKey, Model: model, HTTP: &http.Client{
		Timeout: timeout,
		// 不跟随重定向。base 是 owner 可配置的 KimiBase：若一个看似可信的
		// 主机 302 到内网地址（云元数据、内网服务），跟随重定向会把它变成
		// SSRF 并把 Bearer 密钥带过去。直接连接（含本地 LLM）不受影响。
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, base: baseURL}
}

const systemPrompt = `你是一个任务记录助手。用户会用一句中文或英文描述一件要做的事，你把它整理成一条待办任务。

规则：
- title：任务内容本身，去掉日期、分组、优先级这些修饰语，保留原始语言，240 字以内。
- due：截止日期，格式必须是 YYYY-MM-DD。下面会给出今天的日期和时区，所有相对说法（明天、下周三、后天）都要据此换算成具体日期。无法确定时填空字符串，不要猜。
- priority：1 重要/紧急，2 中等，3 普通。用户没强调时填 3。
- group：任务归类。用户明确说了就用原词，没说就按任务性质给一个简短的词。
- 用户可能用「#词」标分组（如 #工作）、用 !!!/!!/! 标优先级（!!! 重要、!! 中等、! 普通）：据此填 group 与 priority，这些标记本身不要留在 title 里。

必须调用 record_plan 工具提交结果，不要用自然语言回答。`

// toolSchema 声明参数约束。枚举与必填项在 API 层就拦住越界值。
var toolSchema = map[string]any{
	"type": "function",
	"function": map[string]any{
		"name":        toolName,
		"description": "记录一条待办任务",
		"parameters": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"title": map[string]any{
					"type":        "string",
					"description": "任务内容，不含日期/分组/优先级修饰语",
				},
				"due": map[string]any{
					"type":        "string",
					"description": "截止日期 YYYY-MM-DD；无法确定时填空字符串",
				},
				"priority": map[string]any{
					"type":        "integer",
					"enum":        []int{1, 2, 3},
					"description": "1 重要 / 2 中等 / 3 普通",
				},
				"group": map[string]any{
					"type":        "string",
					"description": "任务分组名称",
				},
			},
			"required": []string{"title", "due", "priority", "group"},
		},
	},
}

type planInput struct {
	Title    string `json:"title"`
	Due      string `json:"due"`
	Priority int    `json:"priority"`
	Group    string `json:"group"`
}

// Parse 调 Kimi 解析一句话。模型输出会先本地清洗一遍，
// 字段最终合法性仍由后端把关。
func (k *Kimi) Parse(ctx context.Context, text string, now time.Time, loc *time.Location) (Plan, error) {
	if k.APIKey == "" {
		return Plan{}, fmt.Errorf("未配置 KIMI_API_KEY")
	}
	today := dateValue(now, loc)
	userText := fmt.Sprintf("今天的日期是 %s（时区 %s）。\n用户的计划：%s", today, loc.String(), text)

	payload := ai.ChatRequest{
		Model: k.Model,
		Messages: []ai.ChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userText},
		},
		Tools:      []map[string]any{toolSchema},
		ToolChoice: "auto",
	}
	out, err := ai.DoChat(ctx, k.HTTP, k.base, k.APIKey, &payload)
	if err != nil {
		return Plan{}, err
	}

	for _, call := range out.Choices[0].Message.ToolCalls {
		if call.Function.Name != toolName {
			continue
		}
		var in planInput
		if err := json.Unmarshal([]byte(call.Function.Arguments), &in); err != nil {
			return Plan{}, fmt.Errorf("解析工具参数失败：%w", err)
		}
		plan := Plan{
			Title:    strings.TrimSpace(in.Title),
			Due:      strings.TrimSpace(in.Due),
			Priority: in.Priority,
			Group:    strings.TrimSpace(in.Group),
		}
		plan.normalize()
		if plan.Title == "" {
			return Plan{}, fmt.Errorf("模型没有给出任务内容")
		}
		return plan, nil
	}
	return Plan{}, fmt.Errorf("Kimi 没有调用 %s 工具", toolName)
}

// Verify 用一次最小调用验证 API 是否可用（key、接口地址、模型都生效），
// 供设置页「LLM API 配置」的「测试连接」按钮使用。成功返回 nil；
// 失败返回带 HTTP 状态码或具体原因的 error，文案可安全展示给用户。
//
// 走的是同一条 SSRF 收紧过的 HTTP 客户端（不跟随重定向）。
func (k *Kimi) Verify(ctx context.Context) error {
	if k.APIKey == "" {
		return fmt.Errorf("未配置 LLM API Key")
	}
	payload := ai.ChatRequest{
		Model: k.Model,
		Messages: []ai.ChatMessage{
			{Role: "user", Content: "ping"},
		},
	}
	_, err := ai.DoChatLenient(ctx, k.HTTP, k.base, k.APIKey, &payload)
	return err
}

// normalize 收敛字段到后端可接受的范围。防御性的：即便模型给出越界值，
// 也不该让建任务请求被 400 打回。
func (p *Plan) normalize() {
	if p.Priority < 1 || p.Priority > 3 {
		p.Priority = 3
	}
	if p.Group == "" {
		if p.Priority == 1 {
			p.Group = GroupWork
		} else {
			p.Group = GroupLife
		}
	}
	if len([]rune(p.Group)) > 40 {
		p.Group = string([]rune(p.Group)[:40])
	}
	if len([]rune(p.Title)) > 240 {
		p.Title = strings.TrimSpace(string([]rune(p.Title)[:240]))
	}
	if p.Due != "" {
		if _, err := time.ParseInLocation("2006-01-02", p.Due, time.UTC); err != nil {
			p.Due = ""
		}
	}
}
