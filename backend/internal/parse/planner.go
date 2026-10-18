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
	Kind    DecisionKind
	Plans   []Plan // DecisionWrite 时
	Message string // Ask：要问用户的完整消息；Write：回执的自然开头（为空则回退固定模板）
}

const (
	recordPlansTool = "record_plans"
	askUserTool     = "ask_user"
)

// planningSystemPrompt 与单轮 systemPrompt 分开：多轮规划要能「一次提交多条」
// 也能「缺信息时追问」，但两者都坚持同一底线——只提取用户提到的任务、不臆造。
//
// 追问策略在这里定死，不交给模型临场发挥：日期、重要程度缺失先问（曾经让
// 模型「无法确定就留空」，结果任务全落在未排期里）；分组从工作区现有分组里
// 选、不追问（分组只是归类，问起来太啰嗦）。
const planningSystemPrompt = `你是一个任务规划助手。用户会用一句或多句话描述要做的事，可能一次说好几件，也可能在上一轮你追问后补充信息。

规则：
- 把用户明确提到的每一件事各自整理成一条任务，不要臆造用户没提的子步骤。
- title：任务内容本身，去掉日期/分组/优先级修饰语，保留原始语言，240 字以内。用户若明确说了具体时刻（如「下午3点」），把它写进标题（如「交报告 15:00」），因为日历只记录到天。
- due：截止日期 YYYY-MM-DD。下面会给今天的日期和时区，相对说法（今天/明天/下周X/周X/N天后）都要换算成具体日期。用户没提时间或说法无法确定时，不要留空落库，用 ask_user 问清楚；用户明确表示不用排期（如「不排期」「随时」）才留空。
- priority：1 重要/紧急，2 中等，3 普通。用户用 !!!/!!/! 标记（!!! 重要、!! 中等、! 普通）或「很重要」「不急」等说法时按语义填；完全没表达重要程度时不要默认，用 ask_user 一并问一句。
- group：优先从下面「工作区现有分组」里选一个最贴切的，没有合适的才回落 工作（偏公事）或 生活（偏私事），分组不要追问。「#词」是用户在指定分组，用原词；这些标记本身不要留在 title 里。
- 追问要一次问全：日期、重要程度通常一条消息两个问题就够，口语、简短，像同事随口确认，不要像表单。能从上下文合理推断的就别问。用户说过「直接记/不用问/随便」时不再追问，按默认落库（日期留空、优先级 3）。
- 信息足够时调用 record_plans 一次提交全部任务，按截止日期（未排期靠后）再按优先级（1 在前）排序；message 里用一句自然的话确认（如「好，周报明天交、邮件后天发，都记上了」），像真人回话，别用「已记录」这种机器人腔，也不要编造实际没写入的日期或分组。
- 必须调用 record_plans 或 ask_user 其中之一，不要用自然语言回答。`

// recordPlansSchema / askUserSchema 沿用 toolSchema 的写法：用 function calling
// 拿结构化结果，省掉「解析模型自由输出」的脆弱逻辑。message 字段只承载
// 「说话的口吻」，任务字段本身仍由 schema 约束、落库前还会本地收敛。
var recordPlansSchema = map[string]any{
	"type": "function",
	"function": map[string]any{
		"name":        recordPlansTool,
		"description": "提交要记录的多条任务",
		"parameters": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"message": map[string]any{
					"type":        "string",
					"description": "回执开头的一句自然口语确认，说明记了什么；留空则系统用默认模板",
				},
				"plans": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"title":    map[string]any{"type": "string", "description": "任务内容，不含日期/分组/优先级修饰语"},
							"due":      map[string]any{"type": "string", "description": "截止日期 YYYY-MM-DD；用户明确不用排期时留空字符串"},
							"priority": map[string]any{"type": "integer", "enum": []int{1, 2, 3}, "description": "1 重要 / 2 中等 / 3 普通"},
							"group":    map[string]any{"type": "string", "description": "任务分组名称，优先从工作区现有分组里选"},
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
				"message": map[string]any{
					"type":        "string",
					"description": "要发给用户的完整消息：用自然口语把缺的信息一次问清楚，可以一次问几件事",
				},
			},
			"required": []string{"message"},
		},
	},
}

type plansInput struct {
	Plans   []planInput `json:"plans"`
	Message string      `json:"message"`
}

type askInput struct {
	Message string `json:"message"`
}

// Plan 调 Kimi 做一步多轮规划：给定历史，判断该落库还是该追问。
//
// groups 是工作区现有分组名，会注入提示词——模型不知道有哪些分组就只能在
// 工作/生活里瞎猜，这是「类型划分不对」的根因。与 Parse 同一条 SSRF 收紧过
// 的 HTTP 客户端（不跟随重定向），消息里带上完整历史、工具换成
// record_plans / ask_user 两个。
func (k *Kimi) Plan(ctx context.Context, history []Message, now time.Time, loc *time.Location, groups []string) (Decision, error) {
	if k.APIKey == "" {
		return Decision{}, fmt.Errorf("未配置 KIMI_API_KEY")
	}
	system := fmt.Sprintf("%s\n\n今天的日期是 %s（时区 %s）。", planningSystemPrompt, dateValue(now, loc), loc.String())
	if len(groups) > 0 {
		system += "\n工作区现有分组：" + strings.Join(groups, "、") + "。"
	}

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
			write = Decision{Kind: DecisionWrite, Plans: plans, Message: strings.TrimSpace(in.Message)}
			writeSet = true
		case askUserTool:
			var in askInput
			if err := json.Unmarshal([]byte(call.Function.Arguments), &in); err != nil {
				return Decision{}, fmt.Errorf("解析 ask_user 参数失败：%w", err)
			}
			msg := strings.TrimSpace(in.Message)
			if msg == "" {
				// 模型没给出消息也别让追问落空：宁可问得笨一点，也不能因为
				// 一条空消息报错、退回单轮解析，把没日期的任务默默写进去。
				msg = "想确认下：这件事什么时候要？重要程度如何？"
			}
			ask = Decision{Kind: DecisionAsk, Message: msg}
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
