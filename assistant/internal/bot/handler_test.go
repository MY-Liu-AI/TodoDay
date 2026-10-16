package bot

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"assistant/internal/parse"
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

// AI 未配置时明确告知去哪配置。独立进程的两条配置路径（设置页、环境变量）
// 都要提，少提一条就会把用户引去翻不存在的地方。
func TestHandleReportsUnconfiguredAI(t *testing.T) {
	p := &fakeParser{err: errors.New("未配置 AI 解析")}
	r := &fakeRecorder{}
	s := &fakeSender{}
	h := newTestHandler(p, r, s)

	h.Handle(context.Background(), Incoming{FromUser: "ZhangSan", Text: "买牛奶", MsgID: "5", ReqID: "req-5"})

	if len(r.created) != 0 {
		t.Fatal("AI 未配置时不该建任务")
	}
	if len(s.sent) != 1 || !strings.Contains(s.sent[0].Text, "AI 解析未配置") {
		t.Fatalf("应当说明未配置 AI：%+v", s.sent)
	}
	if !strings.Contains(s.sent[0].Text, "设置」页") || !strings.Contains(s.sent[0].Text, "KIMI_API_KEY") {
		t.Fatalf("两条配置路径都应提及：%s", s.sent[0].Text)
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
