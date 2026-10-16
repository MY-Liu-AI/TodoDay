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

// Message 是一条多轮对话消息，供上层拼历史。
type Message struct {
	Role    string // "user" / "assistant"
	Content string
}

// DecisionKind 说明规划这一步的结论：要么能落库，要么需要追问。
type DecisionKind int

const (
	DecisionWrite DecisionKind = iota // 信息足够，产出待写任务
	DecisionAsk                       // 信息不足，需要追问
)

// Decision 是 Plan 一步的结果。
type Decision struct {
	Kind      DecisionKind
	Plans     []Plan   // DecisionWrite 时
	Questions []string // DecisionAsk 时
}

const (
	recordPlansTool = "record_plans"
	askUserTool     = "ask_user"
)

// planningSystemPrompt 与单轮 systemPrompt 分开：多轮规划要能「一次提交多条」
// 也能「缺信息时追问」，但两者都坚持同一底线——只提取用户提到的任务、不臆造。
const planningSystemPrompt = `你是一个任务规划助手。用户会用一句或多句话描述要做的事，可能一次说好几件，也可能在上一轮你追问后补充信息。

规则：
- 把用户明确提到的每一件事各自整理成一条任务，不要臆造用户没提的子步骤。
- title：任务内容本身，去掉日期/分组/优先级修饰语，保留原始语言，240 字以内。用户若明确说了具体时刻（如「下午3点」），把它写进标题（如「交报告 15:00」），因为日历只记录到天。
- due：截止日期 YYYY-MM-DD。下面会给今天的日期和时区，所有相对说法（今天/明天/后天/下周X/周X/N天后）都要换算成具体日期。确实无法确定时留空，不要猜。
- priority：1 重要/紧急，2 中等，3 普通。用户没强调时填 3。
- group：任务归类。用户明确说了用原词，没说就按任务性质给一个简短词。
- 用户可能用「#词」标分组（如 #工作）、用 !!!/!!/! 标优先级（!!! 重要、!! 中等、! 普通）：据此填 group 与 priority，这些标记本身不要留在 title 里。
- 只有当「时间、重要程度或任务内容本身」缺失、且会影响正确落库时才调用 ask_user 追问，问题要简明、一次别问太多；能合理默认的（如分组）不要追问。
- 信息足够时调用 record_plans 一次提交全部任务，按截止日期（未排期靠后）再按优先级（1 在前）排序。
- 必须调用 record_plans 或 ask_user 其中之一，不要用自然语言回答。`

// recordPlansSchema / askUserSchema 沿用 toolSchema 的写法：用 function calling
// 拿结构化结果，省掉「解析模型自由输出」的脆弱逻辑。
var recordPlansSchema = map[string]any{
	"type": "function",
	"function": map[string]any{
		"name":        recordPlansTool,
		"description": "提交要记录的多条任务",
		"parameters": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"plans": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"title":    map[string]any{"type": "string", "description": "任务内容，不含日期/分组/优先级修饰语"},
							"due":      map[string]any{"type": "string", "description": "截止日期 YYYY-MM-DD；无法确定时留空字符串"},
							"priority": map[string]any{"type": "integer", "enum": []int{1, 2, 3}, "description": "1 重要 / 2 中等 / 3 普通"},
							"group":    map[string]any{"type": "string", "description": "任务分组名称"},
						},
						"required": []string{"title", "due", "priority", "group"},
					},
				},
			},
			"required": []string{"plans"},
		},
	},
}

var askUserSchema = map[string]any{
	"type": "function",
	"function": map[string]any{
		"name":        askUserTool,
		"description": "信息不足以正确落库时向用户提问",
		"parameters": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"questions": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "要问用户的问题列表，简明扼要",
				},
			},
			"required": []string{"questions"},
		},
	},
}

type plansInput struct {
	Plans []planInput `json:"plans"`
}

type askInput struct {
	Questions []string `json:"questions"`
}

// Plan 调 Kimi 做一步多轮规划：给定历史，判断该落库还是该追问。
//
// 与 Parse 同一条 SSRF 收紧过的 HTTP 客户端（不跟随重定向），只是消息里
// 带上完整历史、工具换成 record_plans / ask_user 两个。
func (k *Kimi) Plan(ctx context.Context, history []Message, now time.Time, loc *time.Location) (Decision, error) {
	if k.APIKey == "" {
		return Decision{}, fmt.Errorf("未配置 KIMI_API_KEY")
	}
	system := fmt.Sprintf("%s\n\n今天的日期是 %s（时区 %s）。", planningSystemPrompt, dateValue(now, loc), loc.String())

	messages := make([]chatMessage, 0, len(history)+1)
	messages = append(messages, chatMessage{Role: "system", Content: system})
	for _, m := range history {
		messages = append(messages, chatMessage{Role: m.Role, Content: m.Content})
	}

	payload := chatRequest{
		Model:      k.Model,
		Messages:   messages,
		Tools:      []map[string]any{recordPlansSchema, askUserSchema},
		ToolChoice: "auto",
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return Decision{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, k.base+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return Decision{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+k.APIKey)

	resp, err := k.HTTP.Do(req)
	if err != nil {
		return Decision{}, fmt.Errorf("调用 Kimi 失败：%w", err)
	}
	defer resp.Body.Close()

	var out chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Decision{}, fmt.Errorf("Kimi 响应不是合法 JSON：%w", err)
	}
	if resp.StatusCode != http.StatusOK {
		if out.Error != nil {
			return Decision{}, fmt.Errorf("Kimi 返回 %d：%s", resp.StatusCode, out.Error.Message)
		}
		return Decision{}, fmt.Errorf("Kimi 返回 %d", resp.StatusCode)
	}
	if len(out.Choices) == 0 {
		return Decision{}, fmt.Errorf("Kimi 没有返回任何结果")
	}

	var write Decision
	writeSet := false
	var ask Decision
	for _, call := range out.Choices[0].Message.ToolCalls {
		switch call.Function.Name {
		case recordPlansTool:
			var in plansInput
			if err := json.Unmarshal([]byte(call.Function.Arguments), &in); err != nil {
				return Decision{}, fmt.Errorf("解析 record_plans 参数失败：%w", err)
			}
			plans := make([]Plan, 0, len(in.Plans))
			for _, pi := range in.Plans {
				p := Plan{
					Title:    strings.TrimSpace(pi.Title),
					Due:      strings.TrimSpace(pi.Due),
					Priority: pi.Priority,
					Group:    strings.TrimSpace(pi.Group),
				}
				p.normalize()
				if p.Title == "" {
					continue
				}
				plans = append(plans, p)
			}
			if len(plans) == 0 {
				return Decision{}, fmt.Errorf("模型没有给出任务内容")
			}
			write = Decision{Kind: DecisionWrite, Plans: plans}
			writeSet = true
		case askUserTool:
			var in askInput
			if err := json.Unmarshal([]byte(call.Function.Arguments), &in); err != nil {
				return Decision{}, fmt.Errorf("解析 ask_user 参数失败：%w", err)
			}
			qs := make([]string, 0, len(in.Questions))
			for _, q := range in.Questions {
				if q = strings.TrimSpace(q); q != "" {
					qs = append(qs, q)
				}
			}
			if len(qs) == 0 {
				return Decision{}, fmt.Errorf("模型没有给出要问的问题")
			}
			ask = Decision{Kind: DecisionAsk, Questions: qs}
		}
	}
	// 两个工具同现时，能落库就落库（Write 优先）。
	if writeSet {
		return write, nil
	}
	if ask.Kind == DecisionAsk {
		return ask, nil
	}
	return Decision{}, fmt.Errorf("Kimi 没有调用 record_plans 或 ask_user 工具")
}
