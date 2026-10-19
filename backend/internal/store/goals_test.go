package store

import (
	"context"
	"errors"
	"testing"
)

func goalTestStore(t *testing.T) (*Store, int64, int64) {
	t.Helper()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	u, err := st.CreateUser(ctx, "goal-user", "x")
	if err != nil {
		t.Fatal(err)
	}
	ws, err := st.CreateWorkspaceWithOwner(ctx, u.ID, "个人工作区", "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	return st, ws.ID, u.ID
}

func mustCreateGoal(t *testing.T, st *Store, wsID, userID int64, title string) *Goal {
	t.Helper()
	g, err := st.CreateGoal(context.Background(), wsID, userID, GoalInput{
		Title: title, Mode: "parallel", Status: "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func mustCreateTaskForGoal(t *testing.T, st *Store, wsID, userID int64, title string) *Task {
	t.Helper()
	tk, err := st.CreateTask(context.Background(), wsID, userID, TaskInput{
		Title: title, Priority: 3, Group: "工作",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

func TestGoalCreateAndList(t *testing.T) {
	st, wsID, uid := goalTestStore(t)
	ctx := context.Background()

	g := mustCreateGoal(t, st, wsID, uid, "发布会筹备")
	if g.ID == 0 || g.Status != "active" || g.Mode != "parallel" || g.Version != 1 {
		t.Fatalf("新目标字段不对：%+v", g)
	}
	if len(g.Tasks) != 0 {
		t.Fatalf("新目标不该有任务：%+v", g.Tasks)
	}

	goals, err := st.ListGoals(ctx, wsID)
	if err != nil || len(goals) != 1 {
		t.Fatalf("list: %v %d", err, len(goals))
	}

	// 无目标的工作区返回空数组而不是 nil（前端依赖 [] 渲染空态）。
	other, err := st.CreateWorkspaceWithOwner(ctx, uid, "另一个", "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	empty, err := st.ListGoals(ctx, other.ID)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("空工作区应返回空数组：%v %v", empty, err)
	}
}

func TestGoalAttachDetachAndProgressView(t *testing.T) {
	st, wsID, uid := goalTestStore(t)
	ctx := context.Background()
	g := mustCreateGoal(t, st, wsID, uid, "发布会筹备")

	t1 := mustCreateTaskForGoal(t, st, wsID, uid, "定场地")
	t2 := mustCreateTaskForGoal(t, st, wsID, uid, "发邀请")

	g, err := st.AttachGoalTask(ctx, wsID, g.ID, t1.ID)
	if err != nil {
		t.Fatal(err)
	}
	g, err = st.AttachGoalTask(ctx, wsID, g.ID, t2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Tasks) != 2 || g.Tasks[0].TaskID != t1.ID || g.Tasks[1].TaskID != t2.ID {
		t.Fatalf("挂载顺序不对：%+v", g.Tasks)
	}

	// 一条任务只能属于一个目标。
	g2 := mustCreateGoal(t, st, wsID, uid, "另一个目标")
	if _, err := st.AttachGoalTask(ctx, wsID, g2.ID, t1.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("重复挂载应为 ErrConflict，得到 %v", err)
	}

	// 不存在的任务 → NotFound；别的任务所在工作区 → NotFound（不泄露归属）。
	if _, err := st.AttachGoalTask(ctx, wsID, g.ID, "t_nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在任务应为 NotFound，得到 %v", err)
	}
	other, err := st.CreateWorkspaceWithOwner(ctx, uid, "别的", "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	foreign := mustCreateTaskForGoal(t, st, other.ID, uid, "别人的任务")
	if _, err := st.AttachGoalTask(ctx, wsID, g.ID, foreign.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("跨工作区任务应为 NotFound，得到 %v", err)
	}

	// 完成任务后视图里的 done 跟着变（进度由前端由 Tasks 推导，这里钉死视图口径）。
	if _, err := st.SetCompletion(ctx, wsID, uid, t1.ID, true, t1.Version, nowUTC()); err != nil {
		t.Fatal(err)
	}
	goals, err := st.ListGoals(ctx, wsID)
	if err != nil {
		t.Fatal(err)
	}
	var found *Goal
	for i := range goals {
		if goals[i].ID == g.ID {
			found = &goals[i]
		}
	}
	if found == nil || len(found.Tasks) != 2 || !found.Tasks[0].Done || found.Tasks[1].Done {
		t.Fatalf("完成状态没反映到目标视图：%+v", found)
	}

	// 移出目标后任务本身还在。
	g, err = st.DetachGoalTask(ctx, wsID, g.ID, t2.ID)
	if err != nil || len(g.Tasks) != 1 {
		t.Fatalf("detach: %v %+v", err, g)
	}
	if _, err := st.taskByID(ctx, wsID, t2.ID, false); err != nil {
		t.Fatalf("detach 不该删任务：%v", err)
	}
}

func TestGoalSoftDeleteKeepsMembership(t *testing.T) {
	st, wsID, uid := goalTestStore(t)
	ctx := context.Background()
	g := mustCreateGoal(t, st, wsID, uid, "发布会筹备")
	tk := mustCreateTaskForGoal(t, st, wsID, uid, "定场地")
	if _, err := st.AttachGoalTask(ctx, wsID, g.ID, tk.ID); err != nil {
		t.Fatal(err)
	}

	if err := st.DeleteGoal(ctx, wsID, g.ID); err != nil {
		t.Fatal(err)
	}
	goals, err := st.ListGoals(ctx, wsID)
	if err != nil || len(goals) != 0 {
		t.Fatalf("软删后不该列出：%v %d", err, len(goals))
	}
	if err := st.DeleteGoal(ctx, wsID, g.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("重复删除应为 NotFound，得到 %v", err)
	}

	// 任务软删后不计入进度视图，撤销删除后自动恢复。
	if _, err := st.SetCompletion(ctx, wsID, uid, tk.ID, true, tk.Version, nowUTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DeleteTasks(ctx, wsID, uid, []string{tk.ID}); err != nil {
		t.Fatal(err)
	}
	restored, err := st.RestoreGoal(ctx, wsID, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Tasks) != 0 {
		t.Fatalf("任务软删期间不该计入目标：%+v", restored.Tasks)
	}
	if _, err := st.RestoreTasks(ctx, wsID, uid, []string{tk.ID}); err != nil {
		t.Fatal(err)
	}
	restored, err = st.RestoreGoal(ctx, wsID, g.ID) // 幂等再恢复一次
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Tasks) != 1 || !restored.Tasks[0].Done {
		t.Fatalf("任务撤销删除后归属应自动恢复：%+v", restored.Tasks)
	}
}

func TestGoalUpdateVersionConflict(t *testing.T) {
	st, wsID, uid := goalTestStore(t)
	ctx := context.Background()
	g := mustCreateGoal(t, st, wsID, uid, "旧名字")

	updated, err := st.UpdateGoal(ctx, wsID, g.ID, GoalInput{
		Title: "新名字", Mode: "sequential", Status: "active",
	}, g.Version)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != "新名字" || updated.Mode != "sequential" || updated.Version != 2 {
		t.Fatalf("更新结果不对：%+v", updated)
	}
	// 过期版本 → ErrConflict；不存在的目标 → NotFound。
	if _, err := st.UpdateGoal(ctx, wsID, g.ID, GoalInput{
		Title: "第三次", Mode: "parallel", Status: "active",
	}, g.Version); !errors.Is(err, ErrConflict) {
		t.Fatalf("应为 ErrConflict，得到 %v", err)
	}
	if _, err := st.UpdateGoal(ctx, wsID, 9999, GoalInput{
		Title: "x", Mode: "parallel", Status: "active",
	}, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("应为 ErrNotFound，得到 %v", err)
	}
}

func TestGoalReorderTasks(t *testing.T) {
	st, wsID, uid := goalTestStore(t)
	ctx := context.Background()
	g := mustCreateGoal(t, st, wsID, uid, "串行目标")
	t1 := mustCreateTaskForGoal(t, st, wsID, uid, "第一步")
	t2 := mustCreateTaskForGoal(t, st, wsID, uid, "第二步")
	t3 := mustCreateTaskForGoal(t, st, wsID, uid, "第三步")
	for _, tk := range []*Task{t1, t2, t3} {
		if _, err := st.AttachGoalTask(ctx, wsID, g.ID, tk.ID); err != nil {
			t.Fatal(err)
		}
	}

	// 逆序：解锁顺序变成 3→2→1。
	g, err := st.ReorderGoalTasks(ctx, wsID, g.ID, []string{t3.ID, t2.ID, t1.ID})
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{t3.ID, t2.ID, t1.ID} {
		if g.Tasks[i].TaskID != want {
			t.Fatalf("位置 %d 应为 %s，得到 %+v", i, want, g.Tasks[i])
		}
	}

	// ids 数量不符要拒绝（部分写入不能发生）。
	if _, err := st.ReorderGoalTasks(ctx, wsID, g.ID, []string{t1.ID}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("应为 ErrInvalid，得到 %v", err)
	}
	if _, err := st.ReorderGoalTasks(ctx, wsID, g.ID, []string{t1.ID, t2.ID, "t_nope"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("应为 ErrNotFound，得到 %v", err)
	}
}

// 挂载类的写操作必须用 wsID 把关目标归属：不带校验时，跨工作区的 goalID
// 会「先写成功、再报 NotFound」——返回 404 但副作用已经发生。这里钉死：
// 对外工作区的目标返回 NotFound，且对方的目标内容原封不动。
func TestGoalMutationsScopedToWorkspace(t *testing.T) {
	st, wsID, uid := goalTestStore(t)
	ctx := context.Background()
	g := mustCreateGoal(t, st, wsID, uid, "本区目标")
	t1 := mustCreateTaskForGoal(t, st, wsID, uid, "任务一")
	t2 := mustCreateTaskForGoal(t, st, wsID, uid, "任务二")
	for _, tk := range []*Task{t1, t2} {
		if _, err := st.AttachGoalTask(ctx, wsID, g.ID, tk.ID); err != nil {
			t.Fatal(err)
		}
	}

	other, err := st.CreateWorkspaceWithOwner(ctx, uid, "别的区", "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	foreignTask := mustCreateTaskForGoal(t, st, other.ID, uid, "对方的任务")

	if _, err := st.DetachGoalTask(ctx, other.ID, g.ID, t1.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("detach 跨区目标应为 NotFound，得到 %v", err)
	}
	if _, err := st.ReorderGoalTasks(ctx, other.ID, g.ID, []string{t2.ID, t1.ID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reorder 跨区目标应为 NotFound，得到 %v", err)
	}
	if _, err := st.AttachGoalTask(ctx, other.ID, g.ID, foreignTask.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("attach 跨区目标应为 NotFound，得到 %v", err)
	}

	// 对方工作区视角：目标、挂载关系、顺序都原样（没有发生任何写入）。
	goals, err := st.ListGoals(ctx, wsID)
	if err != nil || len(goals) != 1 {
		t.Fatalf("list: %v %d", err, len(goals))
	}
	if len(goals[0].Tasks) != 2 ||
		goals[0].Tasks[0].TaskID != t1.ID || goals[0].Tasks[1].TaskID != t2.ID {
		t.Fatalf("跨区调用产生了副作用：%+v", goals[0].Tasks)
	}
	// 对方任务也没被挂进任何目标。
	otherGoals, err := st.ListGoals(ctx, other.ID)
	if err != nil || len(otherGoals) != 0 {
		t.Fatalf("对方工作区不应多出目标：%v %+v", err, otherGoals)
	}
}
