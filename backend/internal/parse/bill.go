package parse

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"tododay/internal/ai"
)

// Bill 是 Kimi 从一封账单邮件里解析出的一笔账。IsBill=false 表示
// 模型判断这不是消费/收入账单（营销、对账单通知等），调用方应跳过不落库。
type Bill struct {
	IsBill    bool
	Type      string // exp | inc
	AmountFen int64
	Category  string // 支出分类；空由调用方归「其他」
	Date      string // YYYY-MM-DD；空由调用方回退到邮件日期
	Merchant  string // 商户/对方，进备注方便核对
}

const billToolName = "record_bill"

const billSystemPrompt = `你是一个账单解析助手。用户会给你一封银行或支付机构推送的邮件（发件人、主题、正文），你要判断它是不是一笔真实的消费或收入账单，并提取结构化信息。

规则：
- is_bill：只有「真实发生了一笔钱的变化」的邮件才是账单（消费扣款、转账收入、工资入账、还款等）。营销推荐、额度通知、月度对账单汇总、验证码、还款提醒（尚未扣款）都不是账单，is_bill 填 false，其余字段留空。
- type：exp 支出 / inc 收入。
- amount_yuan：这笔交易的金额，人民币元，数值。必须来自邮件正文里的明确金额，不要自己加总或猜测；有退款/冲正说明且无法确定净额时 is_bill 填 false。
- category：给这笔支出一个简短的中文分类名（如 餐饮、交通、购物、日用、住房、娱乐）。收入填空字符串。
- date：交易发生的日期 YYYY-MM-DD。邮件里只有时间没有日期时，用邮件自己的日期；实在没有填空字符串。
- merchant：商户名或对方户名，简短。
- 只输出工具调用，不要用自然语言回答。`

var billToolSchema = map[string]any{
	"type": "function",
	"function": map[string]any{
		"name":        billToolName,
		"description": "记录一笔从邮件解析出的账单",
		"parameters": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"is_bill": map[string]any{
					"type":        "boolean",
					"description": "是否为真实发生的消费/收入账单",
				},
				"type": map[string]any{
					"type":        "string",
					"enum":        []string{"exp", "inc"},
					"description": "exp 支出 / inc 收入",
				},
				"amount_yuan": map[string]any{
					"type":        "number",
					"description": "交易金额（人民币元）",
				},
				"category": map[string]any{
					"type":        "string",
					"description": "支出分类，简短中文；收入留空",
				},
				"date": map[string]any{
					"type":        "string",
					"description": "交易日期 YYYY-MM-DD；不确定留空",
				},
				"merchant": map[string]any{
					"type":        "string",
					"description": "商户名或对方户名",
				},
			},
			"required": []string{"is_bill", "type", "amount_yuan", "category", "date", "merchant"},
		},
	},
}

type billInput struct {
	IsBill     bool    `json:"is_bill"`
	Type       string  `json:"type"`
	AmountYuan float64 `json:"amount_yuan"`
	Category   string  `json:"category"`
	Date       string  `json:"date"`
	Merchant   string  `json:"merchant"`
}

// ParseBill 调 Kimi 解析一封邮件。与 Parse 同理：模型输出先本地清洗，
// 字段最终合法性仍由后端把关（金额范围、日期格式在落库时校验）。
func (k *Kimi) ParseBill(ctx context.Context, from, subject, body string, now time.Time, loc *time.Location) (Bill, error) {
	if k.APIKey == "" {
		return Bill{}, fmt.Errorf("未配置 LLM API Key")
	}
	// 正文截断：账单金额信息通常在邮件头部，且单次调用要控制 token。
	body = truncateRunes(body, 4000)
	userText := fmt.Sprintf("今天的日期是 %s（时区 %s）。\n发件人：%s\n主题：%s\n正文：\n%s",
		dateValue(now, loc), loc.String(), from, subject, body)

	payload := ai.ChatRequest{
		Model: k.Model,
		Messages: []ai.ChatMessage{
			{Role: "system", Content: billSystemPrompt},
			{Role: "user", Content: userText},
		},
		Tools:      []map[string]any{billToolSchema},
		ToolChoice: "auto",
	}
	out, err := ai.DoChat(ctx, k.HTTP, k.base, k.APIKey, &payload)
	if err != nil {
		return Bill{}, err
	}

	for _, call := range out.Choices[0].Message.ToolCalls {
		if call.Function.Name != billToolName {
			continue
		}
		var in billInput
		if err := json.Unmarshal([]byte(call.Function.Arguments), &in); err != nil {
			return Bill{}, fmt.Errorf("解析工具参数失败：%w", err)
		}
		return normalizeBill(in), nil
	}
	return Bill{}, fmt.Errorf("Kimi 没有调用 %s 工具", billToolName)
}

// normalizeBill 收敛模型输出到后端可接受的范围。is_bill 为 false 时
// 其余字段无意义；金额换算成分为避免浮点误差落库的唯一形态。
func normalizeBill(in billInput) Bill {
	b := Bill{
		IsBill:   in.IsBill,
		Type:     in.Type,
		Merchant: strings.TrimSpace(in.Merchant),
	}
	if !b.IsBill {
		return b
	}
	if b.Type != "exp" && b.Type != "inc" {
		b.Type = "exp"
	}
	b.AmountFen = int64(math.Round(in.AmountYuan * 100))
	b.Category = strings.TrimSpace(in.Category)
	if len([]rune(b.Category)) > 40 {
		b.Category = string([]rune(b.Category)[:40])
	}
	b.Date = strings.TrimSpace(in.Date)
	if _, err := time.ParseInLocation("2006-01-02", b.Date, time.UTC); err != nil {
		b.Date = ""
	}
	return b
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
