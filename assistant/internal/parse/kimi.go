package parse

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// apiBase 默认取 Moonshot（Kimi）的 OpenAI 兼容地址；测试时替换为 httptest 地址。
var apiBase = "https://api.moonshot.cn/v1"

const toolName = "record_plan"

// Kimi 是 Moonshot 的 Kimi 客户端。
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
		model = "moonshot-v1-8k"
	}
	if baseURL == "" {
		baseURL = apiBase
	}
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	return &Kimi{APIKey: apiKey, Model: model, HTTP: &http.Client{Timeout: timeout}, base: baseURL}
}

const systemPrompt = `你是一个任务记录助手。用户会用一句中文或英文描述一件要做的事，你把它整理成一条待办任务。

规则：
- title：任务内容本身，去掉日期、分组、优先级这些修饰语，保留原始语言，240 字以内。
- due：截止日期，格式必须是 YYYY-MM-DD。下面会给出今天的日期和时区，所有相对说法（明天、下周三、后天）都要据此换算成具体日期。无法确定时填空字符串，不要猜。
- priority：1 重要/紧急，2 中等，3 普通。用户没强调时填 3。
- group：任务归类。用户明确说了就用原词，没说就按任务性质给一个简短的词。

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

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string           `json:"model"`
	Messages    []chatMessage    `json:"messages"`
	Tools       []map[string]any `json:"tools"`
	ToolChoice  string           `json:"tool_choice"`
	Temperature float64          `json:"temperature"`
}

type chatResponse struct {
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Function struct {
					Name string `json:"name"`
					// OpenAI 兼容格式里，工具参数是 JSON 字符串而不是对象。
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
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

	payload := chatRequest{
		Model: k.Model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userText},
		},
		Tools:      []map[string]any{toolSchema},
		ToolChoice: "auto",
		// 抽取任务，不需要创造性。
		Temperature: 0.1,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return Plan{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, k.base+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return Plan{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+k.APIKey)

	resp, err := k.HTTP.Do(req)
	if err != nil {
		return Plan{}, fmt.Errorf("调用 Kimi 失败：%w", err)
	}
	defer resp.Body.Close()

	var out chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Plan{}, fmt.Errorf("Kimi 响应不是合法 JSON：%w", err)
	}
	if resp.StatusCode != http.StatusOK {
		if out.Error != nil {
			return Plan{}, fmt.Errorf("Kimi 返回 %d：%s", resp.StatusCode, out.Error.Message)
		}
		return Plan{}, fmt.Errorf("Kimi 返回 %d", resp.StatusCode)
	}
	if len(out.Choices) == 0 {
		return Plan{}, fmt.Errorf("Kimi 没有返回任何结果")
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
