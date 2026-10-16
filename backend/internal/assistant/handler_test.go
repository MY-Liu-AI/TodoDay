package assistant

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"tododay/internal/parse"
)

// 三个依赖都用假实现，编排逻辑可以完全脱离网络与数据库测试。

type fakeParser struct {
	plan  parse.Plan
	err   error
	calls []string
}

func (f *fakeParser) Parse(_ context.Context, text string) (parse.Plan, error) {
	f.calls = append(f.calls, text)
	if f.err != nil {
		return parse.Plan{}, f.err
	}
	return f.plan, nil
}

type fakeRecorder struct {
	mu       sync.Mutex
	created  []parse.Plan
	clientID []string
	err      error
}

func (f *fakeRecorder) CreatePlan(_ context.Context, plan parse.Plan, clientID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.created = append(f.created, plan)
	f.clientID = append(f.clientID, clientID)
	return nil
}

type fakeSender struct {
	mu   sync.Mutex
	sent []struct{ ReqID, Text string }
	err  error
}

// Reply 是长连接的回复：回给「某一次回调」而不是某个人，所以断言的是 reqID。
func (f *fakeSender) Reply(_ context.Context, reqID, content string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, struct{ ReqID, Text string }{reqID, content})
	return nil
}

func newTestHandler(p *fakeParser, r *fakeRecorder, s *fakeSender) *Handler {
	return New(p, r, s)
}

func TestHandleRecordsTaskAndReplies(t *testing.T) {
	p := &fakeParser{plan: parse.Plan{Title: "交周报", Due: "2026-10-16", Priority: 1, Group: "工作"}}
	r := &fakeRecorder{}
	s := &fakeSender{}
	h := newTestHandler(p, r, s)

	h.Handle(context.Background(), Incoming{FromUser: "ZhangSan", Text: "明天 交周报 #工作 !!!", MsgID: "1234", ReqID: "req-1"})

	if len(r.created) != 1 {
		t.Fatalf("应当建 1 条任务，实际 %d", len(r.created))
	}
	if r.created[0].Title != "交周报" {
		t.Fatalf("任务内容不正确：%+v", r.created[0])
	}
	// 幂等键必须带上 MsgId，让服务端能识别重投。
	if r.clientID[0] != "wx_1234" {
		t.Fatalf("clientId 不正确：%q", r.clientID[0])
	}
	if len(s.sent) != 1 {
		t.Fatalf("回执未发送：%+v", s.sent)
	}
	// 回复必须带回回调的 req_id，否则长连接上发不出去。
	if s.sent[0].ReqID != "req-1" {
		t.Fatalf("req_id 未透传：%q", s.sent[0].ReqID)
	}
	// 回执里要能看出记了什么、什么时候、哪个分组、什么优先级。
	for _, want := range []string{"交周报", "2026-10-16", "工作", "高优先级", "已记录"} {
		if !strings.Contains(s.sent[0].Text, want) {
			t.Errorf("回执缺少 %q：%s", want, s.sent[0].Text)
		}
	}
}

// 企业微信会重投同一条消息：不能重复建任务，也不能重复回复。
func TestHandleDeduplicatesRetries(t *testing.T) {
	p := &fakeParser{plan: parse.Plan{Title: "x", Priority: 3, Group: "生活"}}
	r := &fakeRecorder{}
	s := &fakeSender{}
	h := newTestHandler(p, r, s)

	msg := Incoming{FromUser: "ZhangSan", Text: "买牛奶", MsgID: "same-id", ReqID: "req-1"}
	h.Handle(context.Background(), msg)
	h.Handle(context.Background(), msg)
	h.Handle(context.Background(), msg)

	if len(r.created) != 1 {
		t.Fatalf("重投应当只建 1 条任务，实际 %d", len(r.created))
	}
	if len(s.sent) != 1 {
		t.Fatalf("重投应当只回一次，实际 %d", len(s.sent))
	}
	if len(p.calls) != 1 {
		t.Fatalf("重投不该再跑一遍解析，实际 %d 次", len(p.calls))
	}
}

// 没有 MsgId 的消息用「发送人 + 内容」兜底去重。
func TestHandleDeduplicatesWithoutMsgID(t *testing.T) {
	p := &fakeParser{plan: parse.Plan{Title: "x", Priority: 3, Group: "生活"}}
	r := &fakeRecorder{}
	s := &fakeSender{}
	h := newTestHandler(p, r, s)

	msg := Incoming{FromUser: "ZhangSan", Text: "买牛奶", ReqID: "req-1"}
	h.Handle(context.Background(), msg)
	h.Handle(context.Background(), msg)
	if len(r.created) != 1 {
		t.Fatalf("无 MsgId 时也应当去重，实际建了 %d 条", len(r.created))
	}
}

func TestHandleRejectsEmptyText(t *testing.T) {
	p := &fakeParser{plan: parse.Plan{Title: "x", Priority: 3, Group: "生活"}}
	r := &fakeRecorder{}
	s := &fakeSender{}
	h := newTestHandler(p, r, s)

	h.Handle(context.Background(), Incoming{FromUser: "ZhangSan", Text: "   ", MsgID: "1", ReqID: "req-1"})

	if len(r.created) != 0 {
		t.Fatal("空消息不该建任务")
	}
	if len(s.sent) != 1 || !strings.Contains(s.sent[0].Text, "没收到内容") {
		t.Fatalf("应当提示用户重新表述，得到 %+v", s.sent)
	}
	if len(p.calls) != 0 {
		t.Fatal("空消息不该调用解析器")
	}
}

// 解析不出内容时不建任务，但要告诉用户怎么写。AI 是单点：
// 解析失败必须如实告知，不静默成功、也不退回关键词猜测。
func TestHandleRejectsUnparseableText(t *testing.T) {
	p := &fakeParser{err: errors.New("模型没有给出任务内容")}
	r := &fakeRecorder{}
	s := &fakeSender{}
	h := newTestHandler(p, r, s)

	h.Handle(context.Background(), Incoming{FromUser: "ZhangSan", Text: "。。。", MsgID: "2", ReqID: "req-2"})

	if len(r.created) != 0 {
		t.Fatal("没解析出内容时不该建任务")
	}
	if len(s.sent) != 1 || !strings.Contains(s.sent[0].Text, "明天 交周报") {
		t.Fatalf("应当给出正确格式示例，得到 %+v", s.sent)
	}
}

// AI 未配置时明确告知去哪配置，而不是留下「没反应」或假装成功。
func TestHandleReportsUnconfiguredAI(t *testing.T) {
	p := &fakeParser{err: errors.New("未配置 AI 解析")}
	r := &fakeRecorder{}
	s := &fakeSender{}
	h := newTestHandler(p, r, s)

	h.Handle(context.Background(), Incoming{FromUser: "ZhangSan", Text: "买牛奶", MsgID: "5", ReqID: "req-5"})

	if len(r.created) != 0 {
		t.Fatal("AI 未配置时不该建任务")
	}
	if len(s.sent) != 1 || !strings.Contains(s.sent[0].Text, "AI 解析未配置") || !strings.Contains(s.sent[0].Text, "设置") {
		t.Fatalf("应当指引到设置页：%+v", s.sent)
	}
}

// 工作区配错时的提示必须指向配置，而不是说成内容问题。
func TestRecordFailureMessagesAreActionable(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{errors.New("无权访问该工作区：x"), "TODO_WORKSPACE_ID"},
		{errors.New("登录被拒绝：状态码 401"), "TODO_USERNAME"},
		{errors.New("内容不符合要求：任务内容需 1–240 个字符"), "内容不符合要求"},
		{errors.New("后端返回 500：boom"), "稍后重试"},
	}
	for _, tc := range cases {
		got := recordFailure(tc.err)
		if !strings.Contains(got, tc.want) {
			t.Errorf("错误 %v 的提示应包含 %q，得到 %q", tc.err, tc.want, got)
		}
	}
}

func TestHandleReportsRecordFailure(t *testing.T) {
	p := &fakeParser{plan: parse.Plan{Title: "x", Priority: 3, Group: "生活"}}
	r := &fakeRecorder{err: errors.New("无权访问该工作区：x")}
	s := &fakeSender{}
	h := newTestHandler(p, r, s)

	h.Handle(context.Background(), Incoming{FromUser: "ZhangSan", Text: "买牛奶", MsgID: "3", ReqID: "req-3"})

	if len(s.sent) != 1 {
		t.Fatal("记录失败时也应当回复，否则用户以为没收到")
	}
	if !strings.Contains(s.sent[0].Text, "TODO_WORKSPACE_ID") {
		t.Fatalf("应当提示配置问题：%s", s.sent[0].Text)
	}
}

// 发送失败不该影响已经落库的任务，也不该 panic。
func TestSendFailureDoesNotBreak(t *testing.T) {
	p := &fakeParser{plan: parse.Plan{Title: "x", Priority: 3, Group: "生活"}}
	r := &fakeRecorder{}
	s := &fakeSender{err: errors.New("长连接当前不可用")}
	h := newTestHandler(p, r, s)

	h.Handle(context.Background(), Incoming{FromUser: "ZhangSan", Text: "买牛奶", MsgID: "6", ReqID: "req-6"})
	if len(r.created) != 1 {
		t.Fatal("回执发送失败不应影响任务落库")
	}
}

func TestDueLabelAndPriorityLabel(t *testing.T) {
	if dueLabel("") != "未排期" {
		t.Errorf("空日期应当显示未排期，得到 %q", dueLabel(""))
	}
	// 2026-10-16 是周五。
	if got := dueLabel("2026-10-16"); !strings.Contains(got, "周五") {
		t.Errorf("应当带上星期，得到 %q", got)
	}
	if priorityLabel(1) != "高优先级" || priorityLabel(2) != "中优先级" || priorityLabel(3) != "普通" {
		t.Error("优先级文案不正确")
	}
	// 非法日期原样返回，不要吞掉。
	if dueLabel("bad-date") != "bad-date" {
		t.Error("无法解析的日期应原样返回")
	}
}

// 去重缓存必须有上限，否则长跑服务会无限增长。
func TestSeenCacheIsBounded(t *testing.T) {
	h := newTestHandler(&fakeParser{}, &fakeRecorder{}, &fakeSender{})
	for i := 0; i < seenLimit+100; i++ {
		h.firstSight(Incoming{FromUser: "u", Text: "t", MsgID: string(rune(i))})
	}
	h.mu.Lock()
	n := len(h.order)
	h.mu.Unlock()
	if n > seenLimit {
		t.Fatalf("去重缓存应当有上限 %d，实际 %d", seenLimit, n)
	}
}

// —— 多轮规划相关 ——

// fakePlanner 按顺序吐出预设的 Decision；errs 队列非空时先吐错误。
type fakePlanner struct {
	mu        sync.Mutex
	decisions []parse.Decision
	errs      []error
	calls     int
	history   [][]parse.Message
}

func (f *fakePlanner) Plan(_ context.Context, h []parse.Message) (parse.Decision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.history = append(f.history, h)
	if len(f.errs) > 0 {
		err := f.errs[0]
		f.errs = f.errs[1:]
		return parse.Decision{}, err
	}
	if len(f.decisions) > 0 {
		d := f.decisions[0]
		f.decisions = f.decisions[1:]
		return d, nil
	}
	return parse.Decision{Kind: parse.DecisionWrite}, nil
}

func newPlannedHandler(p *fakeParser, pl *fakePlanner, r *fakeRecorder, s *fakeSender) *Handler {
	h := New(p, r, s)
	h.SetPlanner(pl)
	return h
}

// 一句话里提到的多件事要拆成多条，每条用带下标的幂等键。
func TestHandlePlannedWritesMultipleTasks(t *testing.T) {
	p := &fakeParser{}
	pl := &fakePlanner{decisions: []parse.Decision{{
		Kind: parse.DecisionWrite,
		Plans: []parse.Plan{
			{Title: "交周报", Due: "2026-10-16", Priority: 1, Group: "工作"},
			{Title: "回邮件", Due: "2026-10-17", Priority: 3, Group: "生活"},
		},
	}}}
	r := &fakeRecorder{}
	s := &fakeSender{}
	h := newPlannedHandler(p, pl, r, s)

	h.Handle(context.Background(), Incoming{FromUser: "ZhangSan", Text: "明天交周报，后天回邮件", MsgID: "1234", ReqID: "req-1"})

	if len(r.created) != 2 {
		t.Fatalf("应当建 2 条任务，实际 %d", len(r.created))
	}
	if r.clientID[0] != "wx_1234_0" || r.clientID[1] != "wx_1234_1" {
		t.Fatalf("多条任务的幂等键应带下标：%v", r.clientID)
	}
	if len(s.sent) != 1 || !strings.Contains(s.sent[0].Text, "已记录 2 条") {
		t.Fatalf("回执应说明记录条数：%+v", s.sent)
	}
}

// 信息不足时先追问；下一句补充后按同一会话写库。
func TestHandlePlannedAsksThenWrites(t *testing.T) {
	p := &fakeParser{}
	pl := &fakePlanner{decisions: []parse.Decision{
		{Kind: parse.DecisionAsk, Questions: []string{"截止日期是哪天？"}},
		{Kind: parse.DecisionWrite, Plans: []parse.Plan{{Title: "交报告", Due: "2026-10-23", Priority: 1, Group: "工作"}}},
	}}
	r := &fakeRecorder{}
	s := &fakeSender{}
	h := newPlannedHandler(p, pl, r, s)

	h.Handle(context.Background(), Incoming{FromUser: "ZhangSan", Text: "交个报告", MsgID: "1", ReqID: "req-1"})
	if len(r.created) != 0 {
		t.Fatal("信息不足时不该写任务")
	}
	if len(s.sent) != 1 || !strings.Contains(s.sent[0].Text, "截止日期是哪天") || !strings.Contains(s.sent[0].Text, "取消") {
		t.Fatalf("应当追问并提示可取消：%+v", s.sent)
	}

	h.Handle(context.Background(), Incoming{FromUser: "ZhangSan", Text: "下周五下午，很重要", MsgID: "2", ReqID: "req-2"})
	if len(r.created) != 1 || r.created[0].Title != "交报告" {
		t.Fatalf("补充后应当写入 1 条：%+v", r.created)
	}
	if pl.calls != 2 {
		t.Fatalf("应当调用两次规划，实际 %d", pl.calls)
	}
	// 第二条消息带着上一轮历史进入规划（同一会话）。
	if len(pl.history[1]) != 3 { // 用户首句 + 助手追问 + 本次补充
		t.Fatalf("第二轮历史应含上一轮追问，长度 %d", len(pl.history[1]))
	}
}

// 追问中回复「取消」要清会话，且不再调模型。
func TestHandlePlannedCancel(t *testing.T) {
	p := &fakeParser{}
	pl := &fakePlanner{decisions: []parse.Decision{{Kind: parse.DecisionAsk, Questions: []string{"几号？"}}}}
	r := &fakeRecorder{}
	s := &fakeSender{}
	h := newPlannedHandler(p, pl, r, s)

	h.Handle(context.Background(), Incoming{FromUser: "ZhangSan", Text: "交报告", MsgID: "1", ReqID: "req-1"})
	h.Handle(context.Background(), Incoming{FromUser: "ZhangSan", Text: "取消", MsgID: "2", ReqID: "req-2"})

	if len(r.created) != 0 {
		t.Fatal("取消后不该写任务")
	}
	if pl.calls != 1 {
		t.Fatalf("取消不该再调模型，实际 %d 次", pl.calls)
	}
	if len(s.sent) != 2 || !strings.Contains(s.sent[1].Text, "已取消") {
		t.Fatalf("应回复已取消：%+v", s.sent)
	}
}

// 追问太多轮仍没定下来：放弃追问，给一次能说清的示例。
func TestHandlePlannedRoundCap(t *testing.T) {
	p := &fakeParser{}
	ask := parse.Decision{Kind: parse.DecisionAsk, Questions: []string{"几号？"}}
	pl := &fakePlanner{decisions: []parse.Decision{ask, ask, ask, ask}}
	r := &fakeRecorder{}
	s := &fakeSender{}
	h := newPlannedHandler(p, pl, r, s)

	for i := 1; i <= 4; i++ {
		h.Handle(context.Background(), Incoming{FromUser: "ZhangSan", Text: "交报告", MsgID: string(rune('0' + i)), ReqID: "req"})
	}
	if len(r.created) != 0 {
		t.Fatal("没定下来就不该写任务")
	}
	if len(s.sent) != 4 || !strings.Contains(s.sent[3].Text, "还是没能确认清楚") {
		t.Fatalf("第 4 次应放弃追问：%+v", s.sent)
	}
}

// LLM 失败要退回单轮解析再试一次，仍失败才如实告知。
func TestHandlePlannedFallsBackOnError(t *testing.T) {
	p := &fakeParser{plan: parse.Plan{Title: "交周报", Due: "", Priority: 3, Group: "生活"}}
	pl := &fakePlanner{errs: []error{errors.New("调用 Kimi 失败：超时")}}
	r := &fakeRecorder{}
	s := &fakeSender{}
	h := newPlannedHandler(p, pl, r, s)

	h.Handle(context.Background(), Incoming{FromUser: "ZhangSan", Text: "交周报", MsgID: "1", ReqID: "req-1"})

	if len(r.created) != 1 {
		t.Fatalf("LLM 失败应退回单轮解析并写入，实际 %d", len(r.created))
	}
	if len(s.sent) != 1 || !strings.Contains(s.sent[0].Text, "已记录") {
		t.Fatalf("应正常回执：%+v", s.sent)
	}
}

// 不同会话（同一人但不同 ChatID）的追问状态互不串。
func TestHandlePlannedIsolatesByChatID(t *testing.T) {
	p := &fakeParser{}
	pl := &fakePlanner{decisions: []parse.Decision{
		{Kind: parse.DecisionAsk, Questions: []string{"几号？"}},
		{Kind: parse.DecisionWrite, Plans: []parse.Plan{{Title: "另一件事", Due: "", Priority: 3, Group: "生活"}}},
	}}
	r := &fakeRecorder{}
	s := &fakeSender{}
	h := newPlannedHandler(p, pl, r, s)

	h.Handle(context.Background(), Incoming{FromUser: "ZhangSan", ChatID: "", Text: "交报告", MsgID: "1", ReqID: "req-1"})
	h.Handle(context.Background(), Incoming{FromUser: "ZhangSan", ChatID: "room1", Text: "另一件事", MsgID: "2", ReqID: "req-2"})

	// 第二个消息是全新会话：只带自己的历史（长度 1），不带上一条的追问。
	if len(pl.history[1]) != 1 {
		t.Fatalf("不同 ChatID 的会话不应共享历史，长度 %d", len(pl.history[1]))
	}
	if len(r.created) != 1 {
		t.Fatalf("第二个会话应当独立写入，实际 %d", len(r.created))
	}
}

// 过期会话应被当作新对话，不再续上旧历史。
func TestHandlePlannedSessionExpires(t *testing.T) {
	p := &fakeParser{}
	pl := &fakePlanner{decisions: []parse.Decision{
		{Kind: parse.DecisionAsk, Questions: []string{"几号？"}},
		{Kind: parse.DecisionAsk, Questions: []string{"再问一次？"}},
	}}
	r := &fakeRecorder{}
	s := &fakeSender{}
	h := newPlannedHandler(p, pl, r, s)

	h.Handle(context.Background(), Incoming{FromUser: "ZhangSan", Text: "交报告", MsgID: "1", ReqID: "req-1"})

	// 手动把会话时间拨旧，模拟过期。
	key := userKey(Incoming{FromUser: "ZhangSan"})
	h.mu.Lock()
	h.sessions[key].last = time.Now().Add(-sessionTTL - time.Minute)
	h.mu.Unlock()

	h.Handle(context.Background(), Incoming{FromUser: "ZhangSan", Text: "周五", MsgID: "2", ReqID: "req-2"})

	if len(pl.history[1]) != 1 {
		t.Fatalf("过期会话应作为新对话，历史长度应为 1，实际 %d", len(pl.history[1]))
	}
}
