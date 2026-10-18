// Package assistant 把「收到一句话」编排成「记下一条任务并回复」，
// 并提供内嵌后端的 per-workspace 连接管理（Manager）。
//
// 编排层的三个依赖都用接口声明，便于测试时替换：解析器、任务记录器、
// 消息发送器。内嵌模式下生产实现分别是 parse.Parser、store 记录器、
// aibot.Client。
package assistant

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"tododay/internal/parse"
)

// Parser 把一句自然语言解析成结构化任务字段。
type Parser interface {
	Parse(ctx context.Context, text string) (parse.Plan, error)
}

// Recorder 把任务写进 TodoDay。clientID 用于幂等。
type Recorder interface {
	CreatePlan(ctx context.Context, plan parse.Plan, clientID string) error
}

// Sender 把回执发回给用户。
//
// 回复与 reqID 绑定：长连接模式下回复的是「某一次回调」而不是「某个人」，
// 丢掉 reqID 就没法送达。
type Sender interface {
	Reply(ctx context.Context, reqID, content string) error
}

// Planner 用多轮历史做一步规划：信息够就产出待写任务，不够就产出要问的问题。
// 为 nil 表示未配置 LLM，走单轮解析路径。
type Planner interface {
	Plan(ctx context.Context, history []parse.Message) (parse.Decision, error)
}

// Incoming 是编排所需的入站消息字段。
type Incoming struct {
	FromUser string
	ChatID   string // 群聊会话标识；单聊为空
	Text     string
	MsgID    string
	ReqID    string // 回复时原样带回
}

// seenLimit 是去重缓存的容量上限。
const seenLimit = 2000

// 多轮追问的会话治理：过期时长、追问轮次上限、会话缓存上限。
// 追问状态存在内存里（与 seen 去重缓存同一量级、同一生命周期），后端重启后
// 未完成的追问作废——用户重说一遍即可，不追求跨重启持久化。
const (
	sessionTTL       = 10 * time.Minute
	maxClarifyRounds = 3
	sessionLimit     = 200
)

// session 是一段「追问 ↔ 补充」的对话状态。
type session struct {
	history []parse.Message
	rounds  int // 已经追问过的轮数，用来兜底「别无限追问」
	last    time.Time
}

type Handler struct {
	parser   Parser
	planner  Planner // 为 nil 表示未配置 LLM，走单轮解析
	recorder Recorder
	sender   Sender

	mu        sync.Mutex
	seen      map[string]time.Time
	order     []string
	sessions  map[string]*session
	sessOrder []string
}

// New 构造编排器。
func New(parser Parser, recorder Recorder, sender Sender) *Handler {
	return &Handler{
		parser: parser, recorder: recorder, sender: sender,
		seen: map[string]time.Time{}, sessions: map[string]*session{},
	}
}

// SetPlanner 挂多轮规划器。未挂时走单轮解析路径（保持向后兼容）。
func (h *Handler) SetPlanner(p Planner) {
	h.planner = p
}

// Handle 处理一条消息：解析 → 建任务 → 回复。由回调处理器异步调用。
func (h *Handler) Handle(ctx context.Context, msg Incoming) {
	if !h.firstSight(msg) {
		log.Printf("assistant: 重复投递，已跳过 msgId=%s", msg.MsgID)
		return
	}

	text := strings.TrimSpace(msg.Text)
	if text == "" {
		h.reply(ctx, msg.ReqID, "没收到内容。直接说要做的事就行，比如：明天 交周报 #工作 !!!")
		return
	}

	if h.planner != nil {
		h.handlePlanned(ctx, msg, text)
		return
	}

	// 单轮 AI 解析。未配置或失败时如实告知，不静默成功、不退回规则。
	plan, err := h.parser.Parse(ctx, text)
	if err != nil {
		h.reply(ctx, msg.ReqID, parseFailure(err))
		return
	}
	h.writeSingle(ctx, msg, text, plan)
}

// writeSingle 把一条任务写库并回复回执，供单轮路径与多轮降级路径共用。
func (h *Handler) writeSingle(ctx context.Context, msg Incoming, text string, plan parse.Plan) {
	brief := fmt.Sprintf("%s\n%s · %s · %s", plan.Title, dueLabel(plan.Due), plan.Group, priorityLabel(plan.Priority))

	// 幂等键加前缀，既避开前端 clientId 的 `c_` 命名，也便于在库里认出
	// 哪些任务是助手建的。
	clientID := "wx_" + msg.MsgID
	if msg.MsgID == "" {
		// 极少数消息没有 MsgId，退化为「发送人 + 内容」哈希，仍能挡住重复投递。
		clientID = fmt.Sprintf("wx_%s_%d", msg.FromUser, hash(text))
	}

	if err := h.recorder.CreatePlan(ctx, plan, clientID); err != nil {
		log.Printf("assistant: 建任务失败：%v", err)
		h.reply(ctx, msg.ReqID, recordFailure(err))
		return
	}
	h.reply(ctx, msg.ReqID, "已记录：\n"+brief)
}

// handlePlanned 走多轮规划：信息够就写多条，不够就追问，并维护追问会话。
func (h *Handler) handlePlanned(ctx context.Context, msg Incoming, text string) {
	key := userKey(msg)

	// 追问中用户明确要作废：清掉会话，不再调模型。
	if sess := h.getSession(key); sess != nil && isCancel(text) {
		h.deleteSession(key)
		h.reply(ctx, msg.ReqID, "已取消。之前的问题作废，直接说要做的事就行。")
		return
	}

	sess := h.takeSession(key, text)
	dec, err := h.planner.Plan(ctx, sess.history)
	if err != nil {
		// 多轮规划失败：清会话再走一次单轮 AI 解析；仍失败就如实告知。
		log.Printf("assistant: 多轮规划失败，退回单轮解析：%v", err)
		h.deleteSession(key)
		plan, perr := h.parser.Parse(ctx, text)
		if perr != nil {
			h.reply(ctx, msg.ReqID, parseFailure(perr))
			return
		}
		h.writeSingle(ctx, msg, text, plan)
		return
	}

	switch dec.Kind {
	case parse.DecisionAsk:
		if sess.rounds >= maxClarifyRounds {
			// 追问太多仍没定下来：放弃追问，给一个能一次说清的示例。
			h.deleteSession(key)
			h.reply(ctx, msg.ReqID, "还是没能确认清楚。请用「明天 交周报 !!!」这种写法一次说清时间、内容和重要程度，或回复「取消」作废。")
			return
		}
		replyText := withCancelHint(dec.Message)
		sess.history = append(sess.history, parse.Message{Role: "assistant", Content: replyText})
		sess.rounds++
		sess.last = time.Now()
		h.putSession(key, sess)
		h.reply(ctx, msg.ReqID, replyText)
	case parse.DecisionWrite:
		h.deleteSession(key)
		h.writePlans(ctx, msg, dec.Message, dec.Plans)
	}
}

// writePlans 把多条任务逐条写库。每条带 `_i` 后缀的幂等键：同一条消息重投
// 由 firstSight 挡住，而一次拆分出的多条任务彼此不撞键。
// opening 是模型生成的自然回执开头；为空时退回固定模板。明细行始终由系统
// 按实际写入内容拼装——口吻可以交给模型，日期/分组/优先级的真实性不靠它。
func (h *Handler) writePlans(ctx context.Context, msg Incoming, opening string, plans []parse.Plan) {
	base := msg.MsgID
	if base == "" {
		base = fmt.Sprintf("%s_%d", msg.FromUser, hash(msg.Text))
	}

	written := make([]string, 0, len(plans))
	for i, plan := range plans {
		clientID := fmt.Sprintf("wx_%s_%d", base, i)
		if err := h.recorder.CreatePlan(ctx, plan, clientID); err != nil {
			// 单条失败不中止其余；已写的保留，回执里如实说明。
			log.Printf("assistant: 建任务失败（第 %d 条）：%v", i+1, err)
			h.reply(ctx, msg.ReqID, recordFailure(err))
			return
		}
		written = append(written, fmt.Sprintf("• %s\n  %s · %s · %s", plan.Title, dueLabel(plan.Due), plan.Group, priorityLabel(plan.Priority)))
	}
	head := strings.TrimSpace(opening)
	if head == "" {
		head = fmt.Sprintf("已记录 %d 条：", len(written))
	}
	h.reply(ctx, msg.ReqID, head+"\n"+strings.Join(written, "\n"))
}

// userKey 把「人 + 会话」当作追问状态的隔离键。单聊时 ChatID 为空，等于按人隔离。
func userKey(msg Incoming) string {
	return msg.FromUser + "\x00" + msg.ChatID
}

// isCancel 判断用户在追问中是否要作废。
func isCancel(text string) bool {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "取消", "算了", "不用了", "作废", "cancel", "quit":
		return true
	}
	return false
}

// withCancelHint 保证追问里始终带作废出口：模型口吻可以自由发挥，
// 但「怎么反悔」这句必须一直在。
func withCancelHint(msg string) string {
	if strings.Contains(msg, "取消") {
		return msg
	}
	return msg + "\n（回复「取消」可作废）"
}

// getSession 取会话；过期则顺手清掉并返回 nil。
func (h *Handler) getSession(key string) *session {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.sessions[key]
	if s != nil && time.Since(s.last) > sessionTTL {
		h.removeSessionLocked(key)
		return nil
	}
	return s
}

// takeSession 取旧会话并追加本条用户消息，或新建会话。
func (h *Handler) takeSession(key, text string) *session {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.sessions[key]
	if s == nil {
		s = &session{history: []parse.Message{{Role: "user", Content: text}}, last: time.Now()}
		h.sessions[key] = s
		h.sessOrder = append(h.sessOrder, key)
		h.trimSessionsLocked()
		return s
	}
	s.history = append(s.history, parse.Message{Role: "user", Content: text})
	s.last = time.Now()
	return s
}

// putSession 存回会话并维护容量上限（与 seen 的 FIFO 淘汰同款）。
func (h *Handler) putSession(key string, s *session) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.sessions[key]; !ok {
		h.sessOrder = append(h.sessOrder, key)
	}
	h.sessions[key] = s
	h.trimSessionsLocked()
}

func (h *Handler) deleteSession(key string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.removeSessionLocked(key)
}

func (h *Handler) removeSessionLocked(key string) {
	delete(h.sessions, key)
}

// trimSessionsLocked 会话缓存上限。sessOrder 里可能残留已删键，只做容量收敛，
// 正确性不依赖逐个清除（与 seen 的淘汰策略一致）。
func (h *Handler) trimSessionsLocked() {
	for len(h.sessOrder) > sessionLimit {
		oldest := h.sessOrder[0]
		h.sessOrder = h.sessOrder[1:]
		delete(h.sessions, oldest)
	}
}

// reply 发回执。回执与 reqID 绑定：长连接模式下回复的是「某次回调」，
// 丢掉 reqID 就发不出去。发送失败只记日志：用户少一条回执，
// 不该影响已经落库的任务。
func (h *Handler) reply(ctx context.Context, reqID, content string) {
	if err := h.sender.Reply(ctx, reqID, content); err != nil {
		log.Printf("assistant: 回执发送失败：%v", err)
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

// parseFailure 把解析失败翻译成用户能看懂、且能指向排查方向的文案。
// AI 是单点：未配置或失败都不静默成功、不退回规则解析。
func parseFailure(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "未配置"):
		return "AI 解析未配置，无法记录。请在网页「设置」页填写 LLM API Key 后再试。"
	case strings.Contains(msg, "没有给出任务内容"):
		return "没能从这句话里认出计划内容。试试这种写法：明天 交周报 #工作 !!!"
	default:
		return "AI 解析失败，请稍后重试，或换个说法再发一遍。\n（" + msg + "）"
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
