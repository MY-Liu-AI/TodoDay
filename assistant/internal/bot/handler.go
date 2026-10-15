// Package bot 把「收到一句话」编排成「记下一条任务并回复」。
//
// 三个依赖都用接口声明，便于测试时替换：
// 解析器、任务记录器、消息发送器。生产实现分别是 parse.Parser、
// todoclient.Client、wecom.Client。
package bot

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"assistant/internal/parse"
)

// Parser 把一句自然语言解析成结构化任务字段。
type Parser interface {
	Parse(ctx context.Context, text string) (parse.Plan, parse.Source)
}

// Recorder 把任务写进 TodoDay。clientID 用于幂等。
type Recorder interface {
	CreatePlan(ctx context.Context, plan parse.Plan, clientID string) error
}

// Sender 把回执发回给用户。
type Sender interface {
	SendText(ctx context.Context, toUser, content string) error
}

// Incoming 是编排所需的入站消息字段。
type Incoming struct {
	FromUser string
	Text     string
	MsgID    string
}

// seenLimit 是去重缓存的容量上限。
const seenLimit = 2000

type Handler struct {
	parser   Parser
	recorder Recorder
	sender   Sender
	aiOn     bool // AI 是否已配置：决定降级时要不要在回执里说明

	mu    sync.Mutex
	seen  map[string]time.Time
	order []string
}

// New 构造编排器。aiOn 为 true 时，若实际走了规则解析会在回执里说明，
// 避免「AI 明明没生效却看起来正常」。
func New(parser Parser, recorder Recorder, sender Sender, aiOn bool) *Handler {
	return &Handler{parser: parser, recorder: recorder, sender: sender, aiOn: aiOn, seen: map[string]time.Time{}}
}

// Handle 处理一条消息：解析 → 建任务 → 回复。由回调处理器异步调用。
func (h *Handler) Handle(ctx context.Context, msg Incoming) {
	if !h.firstSight(msg) {
		log.Printf("bot: 重复投递，已跳过 msgId=%s", msg.MsgID)
		return
	}

	text := strings.TrimSpace(msg.Text)
	if text == "" {
		h.reply(ctx, msg.FromUser, "没收到内容。直接说要做的事就行，比如：明天 交周报 #工作 !!!")
		return
	}

	plan, source := h.parser.Parse(ctx, text)
	if strings.TrimSpace(plan.Title) == "" {
		h.reply(ctx, msg.FromUser, "没能从这句话里认出计划内容。试试这种写法：明天 交周报 #工作 !!!")
		return
	}

	brief := fmt.Sprintf("%s\n%s · %s · %s", plan.Title, dueLabel(plan.Due), plan.Group, priorityLabel(plan.Priority))

	// 幂等键加前缀，既避开前端 clientId 的 `c_` 命名，也便于在库里认出
	// 哪些任务是助手建的。
	clientID := "wx_" + msg.MsgID
	if msg.MsgID == "" {
		// 极少数消息没有 MsgId，退化为「发送人 + 内容」哈希，仍能挡住重复投递。
		clientID = fmt.Sprintf("wx_%s_%d", msg.FromUser, hash(text))
	}

	if err := h.recorder.CreatePlan(ctx, plan, clientID); err != nil {
		log.Printf("bot: 建任务失败：%v", err)
		h.reply(ctx, msg.FromUser, recordFailure(err))
		return
	}

	note := ""
	if h.aiOn && source == parse.SourceRule {
		// 降级是正常的，但要让人知道这次是按关键词理解的。
		note = "\n（AI 解析未生效，按关键词理解）"
	}
	h.reply(ctx, msg.FromUser, "已记录：\n"+brief+note)
}

// reply 发回执。发送失败只记日志：用户少一条回执，不该影响已经落库的任务。
func (h *Handler) reply(ctx context.Context, toUser, content string) {
	if err := h.sender.SendText(ctx, toUser, content); err != nil {
		log.Printf("bot: 回执发送失败：%v", err)
	}
}

// firstSight 判断这条消息是否是第一次见到。企业微信在超时后会重投同一条
// 消息，重投时不必再跑一遍解析，也不必再回一次。
func (h *Handler) firstSight(msg Incoming) bool {
	key := msg.MsgID
	if key == "" {
		key = msg.FromUser + "\x00" + msg.Text
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.seen[key]; ok {
		return false
	}
	h.seen[key] = time.Now()
	h.order = append(h.order, key)
	if len(h.order) > seenLimit {
		oldest := h.order[0]
		h.order = h.order[1:]
		delete(h.seen, oldest)
	}
	return true
}

// recordFailure 把记录失败翻译成用户能看懂、且能指向排查方向的文案。
// 工作区配错时说成「内容不合法」会把排查引到完全错误的方向。
func recordFailure(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "无权访问该工作区"):
		return "记录失败：助手账号没有这个工作区的权限。\n请检查 TODO_WORKSPACE_ID 是否配错。"
	case strings.Contains(msg, "登录被拒绝"):
		return "记录失败：助手登录被拒绝。\n请检查 TODO_USERNAME / TODO_PASSWORD。"
	case strings.Contains(msg, "内容不符合要求"):
		return "记录失败：内容不符合要求。" + strings.TrimPrefix(msg, "内容不符合要求：")
	default:
		return "记录失败，请稍后重试。\n（" + msg + "）"
	}
}

func dueLabel(due string) string {
	if due == "" {
		return "未排期"
	}
	t, err := time.ParseInLocation("2006-01-02", due, time.UTC)
	if err != nil {
		return due
	}
	return due + " " + weekdayCN(t.Weekday())
}

func weekdayCN(w time.Weekday) string {
	return [...]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}[w]
}

func priorityLabel(p int) string {
	switch p {
	case 1:
		return "高优先级"
	case 2:
		return "中优先级"
	default:
		return "普通"
	}
}

// hash 是去掉 MsgId 时的退化幂等键。用简单累加即可：它只用来避免
// 同一人短时间内重复提交完全相同的内容，不承担安全职责。
func hash(s string) uint32 {
	var h uint32
	for _, r := range s {
		h = h*31 + uint32(r)
	}
	return h
}
