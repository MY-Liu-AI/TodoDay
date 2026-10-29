package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// 习惯：工作区级的定期练习，独立于目标。实例任务按星期滚动生成未来
// 8 周；确定性 client_id 保证重复读取/重试不产生重复实例。
func TestHabitLifecycle(t *testing.T) {
	st, wsID, uid := goalTestStore(t)
	ctx := context.Background()

	h, err := st.CreateHabit(ctx, wsID, uid, HabitInput{Title: "练胸", Weekdays: []int{1}}, "hb-client-1")
	if err != nil {
		t.Fatal(err)
	}
	if h.Title != "练胸" || len(h.Weekdays) != 1 || h.Weekdays[0] != 1 {
		t.Fatalf("习惯字段不对：%+v", h)
	}

	// 周一习惯：未来 8 周恰好 8 个实例（今天起 56 天内周一的个数）。
	habits, err := st.ListHabits(ctx, wsID, uid)
	if err != nil {
		t.Fatal(err)
	}
	if len(habits) != 1 {
		t.Fatalf("应有 1 个习惯，得到 %d", len(habits))
	}
	n := len(habits[0].Tasks)
	if n < 7 || n > 9 {
		t.Fatalf("周一习惯应有 7~9 个实例（含本周），得到 %d", n)
	}
	for _, tk := range habits[0].Tasks {
		if tk.Done {
			t.Fatalf("新实例不应已完成：%+v", tk)
		}
		if wd := weekdayBit(mustParseDay(t, tk.Due)); wd != 0 {
			t.Fatalf("实例应落在周一，得到 %s", tk.Due)
		}
	}

	// 幂等：同 clientId 重放返回同一习惯；重复读取不新增实例。
	h2, err := st.CreateHabit(ctx, wsID, uid, HabitInput{Title: "练胸", Weekdays: []int{1}}, "hb-client-1")
	if err != nil {
		t.Fatal(err)
	}
	if h2.ID != h.ID {
		t.Fatalf("clientId 重放应返回原习惯：%d != %d", h2.ID, h.ID)
	}
	habits, _ = st.ListHabits(ctx, wsID, uid)
	if len(habits[0].Tasks) != n {
		t.Fatalf("重复读取不应新增实例：%d != %d", len(habits[0].Tasks), n)
	}

	// 勾选今天的实例（若今天不是周一则勾第一个实例）。
	tk := habits[0].Tasks[0]
	if _, err := st.SetCompletion(ctx, wsID, uid, tk.TaskID, true, 1, nowUTC()); err != nil {
		t.Fatal(err)
	}
	habits, _ = st.ListHabits(ctx, wsID, uid)
	done := 0
	for _, x := range habits[0].Tasks {
		if x.Done {
			done++
		}
	}
	if done != 1 {
		t.Fatalf("完成后应有 1 个已完成实例，得到 %d", done)
	}
}

// 编辑习惯：改标题同步未来实例；取消星期删除对应未来实例；新增星期补出
// 实例。version 乐观并发：过期版本返回 ErrConflict。
func TestHabitUpdateSyncsOccurrences(t *testing.T) {
	st, wsID, uid := goalTestStore(t)
	ctx := context.Background()
	h, err := st.CreateHabit(ctx, wsID, uid, HabitInput{Title: "练胸", Weekdays: []int{1}}, "")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := st.UpdateHabit(ctx, wsID, uid, h.ID, HabitInput{Title: "练胸（上斜）", Weekdays: []int{1, 3}}, 999); !errors.Is(err, ErrConflict) {
		t.Fatalf("过期版本应 409，得到 %v", err)
	}
	up, err := st.UpdateHabit(ctx, wsID, uid, h.ID, HabitInput{Title: "练胸（上斜）", Weekdays: []int{1, 3}}, h.Version)
	if err != nil {
		t.Fatal(err)
	}
	if len(up.Weekdays) != 2 || up.Weekdays[0] != 1 || up.Weekdays[1] != 3 {
		t.Fatalf("星期应为 [1 3]，得到 %v", up.Weekdays)
	}
	mon, wed := 0, 0
	for _, tk := range up.Tasks {
		switch weekdayBit(mustParseDay(t, tk.Due)) {
		case 0:
			mon++
			if tk.Title != "练胸（上斜）" {
				t.Fatalf("未来实例标题应同步，得到 %q", tk.Title)
			}
		case 2:
			wed++
		}
	}
	if mon < 7 || wed < 7 {
		t.Fatalf("周一/周三实例都应滚动生成：mon=%d wed=%d", mon, wed)
	}

	// 取消周三：未来周三实例删除，周一保留。
	up2, err := st.UpdateHabit(ctx, wsID, uid, h.ID, HabitInput{Title: "练胸（上斜）", Weekdays: []int{1}}, up.Version)
	if err != nil {
		t.Fatal(err)
	}
	for _, tk := range up2.Tasks {
		if weekdayBit(mustParseDay(t, tk.Due)) != 0 {
			t.Fatalf("取消星期后不应有周三实例：%s", tk.Due)
		}
	}
}

// 删除习惯：未来未完成实例随之删除；历史完成实例保留为普通任务。
func TestHabitDeleteKeepsCompletedHistory(t *testing.T) {
	st, wsID, uid := goalTestStore(t)
	ctx := context.Background()
	h, err := st.CreateHabit(ctx, wsID, uid, HabitInput{Title: "跑步", Weekdays: []int{1, 2, 3, 4, 5, 6, 7}}, "")
	if err != nil {
		t.Fatal(err)
	}
	// 完成今天与明天的实例（每天都排 = 前两个任务即今明）。
	habits, _ := st.ListHabits(ctx, wsID, uid)
	for i := 0; i < 2 && i < len(habits[0].Tasks); i++ {
		if _, err := st.SetCompletion(ctx, wsID, uid, habits[0].Tasks[i].TaskID, true, 1, nowUTC()); err != nil {
			t.Fatal(err)
		}
	}
	var completed, live int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(1) FROM tasks WHERE habit_id = ? AND completed_at IS NOT NULL`, h.ID).Scan(&completed); err != nil {
		t.Fatal(err)
	}
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(1) FROM tasks WHERE habit_id = ? AND deleted_at IS NULL`, h.ID).Scan(&live); err != nil {
		t.Fatal(err)
	}

	n, err := st.DeleteHabits(ctx, wsID, []int64{h.ID})
	if err != nil || n != 1 {
		t.Fatalf("删除习惯：n=%d err=%v", n, err)
	}
	// 历史完成实例保留为普通任务（habit_id 被外键置空，任务本体仍在）。
	var history int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(1) FROM tasks WHERE title = '跑步' AND completed_at IS NOT NULL AND deleted_at IS NULL`).Scan(&history); err != nil {
		t.Fatal(err)
	}
	if history != completed {
		t.Fatalf("历史完成实例应保留（%d），得到 %d", completed, history)
	}
	// 未完成实例随习惯删除（未来窗口清空）。
	var remaining int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(1) FROM tasks WHERE title = '跑步' AND completed_at IS NULL AND deleted_at IS NULL`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("未完成实例应随习惯删除，剩 %d", remaining)
	}
	habits, err = st.ListHabits(ctx, wsID, uid)
	if err != nil {
		t.Fatal(err)
	}
	if len(habits) != 0 {
		t.Fatalf("习惯列表应为空，得到 %+v", habits)
	}
}

// 跨工作区隔离：习惯只属于创建工作区，别的工作区读写一律 NotFound。
func TestHabitGuards(t *testing.T) {
	st, wsID, uid := goalTestStore(t)
	ctx := context.Background()
	h, err := st.CreateHabit(ctx, wsID, uid, HabitInput{Title: "练胸", Weekdays: []int{1}}, "")
	if err != nil {
		t.Fatal(err)
	}

	ws2, err := st.CreateWorkspaceWithOwner(ctx, uid, "第二个工作区", "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdateHabit(ctx, ws2.ID, uid, h.ID, HabitInput{Title: "x", Weekdays: []int{1}}, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("跨工作区编辑应按 NotFound，得到 %v", err)
	}
	if _, err := st.DeleteHabits(ctx, ws2.ID, []int64{h.ID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("跨工作区删除应按 NotFound，得到 %v", err)
	}
	habits, err := st.ListHabits(ctx, ws2.ID, uid)
	if err != nil {
		t.Fatal(err)
	}
	if len(habits) != 0 {
		t.Fatalf("第二个工作区不应看到别区的习惯：%+v", habits)
	}
}

func mustParseDay(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.ParseInLocation("2006-01-02", s, time.UTC)
	if err != nil {
		t.Fatal(fmt.Errorf("parse %s: %w", s, err))
	}
	return d
}
