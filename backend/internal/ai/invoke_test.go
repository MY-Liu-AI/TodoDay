package ai

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"tododay/internal/store"
)

func invokeTestStore(t *testing.T) (*store.Store, int64) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	u, err := st.CreateUser(ctx, "ai-user", "x")
	if err != nil {
		t.Fatal(err)
	}
	ws, err := st.CreateWorkspaceWithOwner(ctx, u.ID, "个人工作区", "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	return st, ws.ID
}

func mustTask(t *testing.T, st *store.Store, wsID, userID int64, in store.TaskInput) *store.Task {
	t.Helper()
	tk, err := st.CreateTask(context.Background(), wsID, userID, in, "")
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

func TestQuadrantValidator(t *testing.T) {
	ctxVal := map[string]any{"tasks": []taskView{
		{ID: "a", Title: "写周报"},
		{ID: "b", Title: "回邮件"},
	}}
	raw := json.RawMessage(`{"items":[
		{"id":"a","quadrant":"iu","reason":"今天截止","junk":"x"},
		{"id":"b","quadrant":"XX","reason":"象限非法"},
		{"id":"ghost","quadrant":"IN","reason":"不存在的任务"},
		{"id":"a","quadrant":"IN","reason":"重复引用"}
	]}`)
	out, err := quadrantValidator(context.Background(), raw, ctxVal)
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	items := m["items"].([]quadrantItem)
	if len(items) != 2 {
		t.Fatalf("小写 iu 归一化后应保留 2 条，得到 %d", len(items))
	}
	if items[0].Quadrant != "IU" || items[1].Quadrant != "IN" {
		t.Fatalf("象限归一化/保留不对：%+v", items)
	}
	if m["dropped"].(int) != 2 {
		t.Fatalf("非法象限与幽灵 id 应计 2 个 dropped，得到 %v", m["dropped"])
	}

	if _, err := quadrantValidator(context.Background(), json.RawMessage(`不是json`), ctxVal); err == nil {
		t.Fatal("非 JSON 产出应报错")
	}
}

func TestTodayTasksProvider(t *testing.T) {
	st, wsID := invokeTestStore(t)
	ctx := context.Background()
	u, _, err := st.UserByUsername(ctx, "ai-user")
	if err != nil {
		t.Fatal(err)
	}
	today := time.Now().In(mustLoc(t)).Format("2006-01-02")
	yesterday := time.Now().In(mustLoc(t)).Add(-24 * time.Hour).Format("2006-01-02")
	tomorrow := time.Now().In(mustLoc(t)).Add(24 * time.Hour).Format("2006-01-02")

	a := mustTask(t, st, wsID, u.ID, store.TaskInput{Title: "今天的事", Due: today, Priority: 1, Group: "工作"})
	b := mustTask(t, st, wsID, u.ID, store.TaskInput{Title: "逾期的事", Due: yesterday, Priority: 2, Group: "工作"})
	c := mustTask(t, st, wsID, u.ID, store.TaskInput{Title: "明天的", Due: tomorrow, Priority: 3, Group: "生活"})
	mustTask(t, st, wsID, u.ID, store.TaskInput{Title: "未排期", Due: "", Priority: 3, Group: "生活"})
	done := mustTask(t, st, wsID, u.ID, store.TaskInput{Title: "今天已完成", Due: today, Priority: 3, Group: "工作", Done: true, At: time.Now()})

	got, err := todayTasksProvider(ctx, st, wsID)
	if err != nil {
		t.Fatal(err)
	}
	m := got.(map[string]any)
	tasks := m["tasks"].([]taskView)
	// 今天未完成 + 昨天未完成 + 今天已完成 = 3 条（未排期与明天排除）。
	if len(tasks) != 3 {
		t.Fatalf("今天+逾期未完成+今天已完成 = 3 条，得到 %d", len(tasks))
	}
	byID := map[string]taskView{}
	for _, tv := range tasks {
		byID[tv.ID] = tv
	}
	if !byID[a.ID].Overdue && !byID[a.ID].Done {
		// ok
	} else {
		t.Fatal("今天未完成不应标 overdue/done")
	}
	if !byID[b.ID].Overdue {
		t.Fatal("昨天未完成应标 overdue")
	}
	if byID[done.ID].Done != true {
		t.Fatal("今天已完成任务的 done 应保留")
	}
	if _, ok := byID[c.ID]; ok {
		t.Fatal("明天的任务不应出现")
	}
}

func mustLoc(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}
