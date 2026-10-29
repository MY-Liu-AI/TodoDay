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
		Title: title, Status: "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func mustCreateMilestone(t *testing.T, st *Store, wsID, goalID int64, in MilestoneInput) *Goal {
	t.Helper()
	g, err := st.CreateGoalMilestone(context.Background(), wsID, goalID, in)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// 目标下最后一个里程碑（创建即追加在末尾）。
func lastMilestone(t *testing.T, g *Goal) *GoalMilestone {
	t.Helper()
	if len(g.Milestones) == 0 {
		t.Fatalf("目标下没有里程碑：%+v", g)
	}
	return &g.Milestones[len(g.Milestones)-1]
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
	if g.ID == 0 || g.Status != "active" || g.Version != 1 {
		t.Fatalf("新目标字段不对：%+v", g)
	}
	if len(g.Milestones) != 0 {
		t.Fatalf("新目标不该有里程碑：%+v", g.Milestones)
	}

	goals, err := st.ListGoals(ctx, wsID)
	if err != nil || len(goals) != 1 {
		t.Fatalf("list: %v %d", err, len(goals))
	}

	// 分组标签：创建可带、可改，列表一并返回。
	ga, err := st.CreateGoal(ctx, wsID, uid, GoalInput{
		Title: "带分组的目标", Group: "工作", Status: "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ga.Group != "工作" {
		t.Fatalf("分组标签未落库：%+v", ga)
	}
	gb, err := st.UpdateGoal(ctx, wsID, ga.ID, GoalInput{
		Title: ga.Title, Group: "学习", Status: ga.Status,
	}, ga.Version)
	if err != nil {
		t.Fatal(err)
	}
	if gb.Group != "学习" {
		t.Fatalf("分组标签未更新：%+v", gb)
	}
	goals, err = st.ListGoals(ctx, wsID)
	if err != nil {
		t.Fatal(err)
	}
	for _, goal := range goals {
		if goal.ID == ga.ID && goal.Group != "学习" {
			t.Fatalf("列表未带回分组标签：%+v", goal)
		}
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

// 里程碑 CRUD：标题与起止日期落库、可改（乐观并发）、按 position 排序。
func TestGoalMilestoneCRUD(t *testing.T) {
	st, wsID, uid := goalTestStore(t)
	ctx := context.Background()
	g := mustCreateGoal(t, st, wsID, uid, "发布会筹备")

	g = mustCreateMilestone(t, st, wsID, g.ID, MilestoneInput{Title: "预热", Start: "2026-11-01", Due: "2026-11-07"})
	g = mustCreateMilestone(t, st, wsID, g.ID, MilestoneInput{Title: "举办", Due: "2026-11-15"})
	if len(g.Milestones) != 2 {
		t.Fatalf("应有 2 个里程碑：%+v", g.Milestones)
	}
	m := lastMilestone(t, g)
	if m.Title != "举办" || m.Start != "" || m.Due != "2026-11-15" || m.Version != 1 {
		t.Fatalf("里程碑字段不对：%+v", m)
	}
	if g.Milestones[0].Title != "预热" || g.Milestones[0].Start != "2026-11-01" {
		t.Fatalf("开始日期未落库：%+v", g.Milestones[0])
	}

	// 编辑标题与起止；start > due 由 API 层校验，这里走 store 只验乐观并发。
	updated, err := st.UpdateGoalMilestone(ctx, wsID, g.ID, m.ID,
		MilestoneInput{Title: "正式举办", Start: "2026-11-10", Due: "2026-11-16"}, m.Version)
	if err != nil {
		t.Fatal(err)
	}
	um := lastMilestone(t, updated)
	if um.Title != "正式举办" || um.Start != "2026-11-10" || um.Due != "2026-11-16" || um.Version != 2 {
		t.Fatalf("里程碑更新结果不对：%+v", um)
	}
	if _, err := st.UpdateGoalMilestone(ctx, wsID, g.ID, m.ID,
		MilestoneInput{Title: "第三次"}, m.Version); !errors.Is(err, ErrConflict) {
		t.Fatalf("过期版本应为 ErrConflict，得到 %v", err)
	}
	if _, err := st.UpdateGoalMilestone(ctx, wsID, g.ID, 9999,
		MilestoneInput{Title: "x"}, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在的里程碑应为 NotFound，得到 %v", err)
	}
}

// 里程碑删除：软删 + 其下任务解除挂载（任务本身保留在清单）；
// 恢复找回里程碑本身，任务归属不自动回填。
func TestGoalMilestoneDeleteAndRestore(t *testing.T) {
	st, wsID, uid := goalTestStore(t)
	ctx := context.Background()
	g := mustCreateGoal(t, st, wsID, uid, "发布会筹备")
	g = mustCreateMilestone(t, st, wsID, g.ID, MilestoneInput{Title: "预热", Start: "2026-11-01", Due: "2026-11-07"})
	ms := lastMilestone(t, g)
	tk := mustCreateTaskForGoal(t, st, wsID, uid, "发预告")
	g, err := st.AttachGoalTask(ctx, wsID, g.ID, ms.ID, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(lastMilestone(t, g).Tasks) != 1 {
		t.Fatalf("任务应挂进里程碑：%+v", lastMilestone(t, g))
	}

	g, err = st.DeleteGoalMilestone(ctx, wsID, g.ID, ms.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Milestones) != 0 {
		t.Fatalf("软删后不该列出里程碑：%+v", g.Milestones)
	}
	// 任务解除挂载但本身还在清单里。
	if _, err := st.taskByID(ctx, wsID, tk.ID, false); err != nil {
		t.Fatalf("删除里程碑不该删任务：%v", err)
	}

	restored, err := st.RestoreGoalMilestone(ctx, wsID, g.ID, ms.ID)
	if err != nil {
		t.Fatal(err)
	}
	rm := lastMilestone(t, restored)
	if rm.Title != "预热" || rm.Start != "2026-11-01" || len(rm.Tasks) != 0 {
		t.Fatalf("恢复应找回标题与起止、任务不自动回填：%+v", rm)
	}
}

func TestGoalAttachDetachAndProgressView(t *testing.T) {
	st, wsID, uid := goalTestStore(t)
	ctx := context.Background()
	g := mustCreateGoal(t, st, wsID, uid, "发布会筹备")
	g = mustCreateMilestone(t, st, wsID, g.ID, MilestoneInput{Title: "预热", Due: "2026-11-07"})
	g = mustCreateMilestone(t, st, wsID, g.ID, MilestoneInput{Title: "举办", Due: "2026-11-15"})
	m1, m2 := &g.Milestones[0], &g.Milestones[1]

	t1 := mustCreateTaskForGoal(t, st, wsID, uid, "定场地")
	t2 := mustCreateTaskForGoal(t, st, wsID, uid, "发邀请")

	g, err := st.AttachGoalTask(ctx, wsID, g.ID, m1.ID, t1.ID)
	if err != nil {
		t.Fatal(err)
	}
	g, err = st.AttachGoalTask(ctx, wsID, g.ID, m1.ID, t2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Milestones[0].Tasks) != 2 || g.Milestones[0].Tasks[0].TaskID != t1.ID || g.Milestones[0].Tasks[1].TaskID != t2.ID {
		t.Fatalf("挂载顺序不对：%+v", g.Milestones[0].Tasks)
	}
	if len(g.Milestones[1].Tasks) != 0 {
		t.Fatalf("第二个里程碑不该有任务：%+v", g.Milestones[1].Tasks)
	}

	// 一条任务只能属于一个目标（含跨里程碑）。
	g2 := mustCreateGoal(t, st, wsID, uid, "另一个目标")
	g2 = mustCreateMilestone(t, st, wsID, g2.ID, MilestoneInput{Title: "别的里程碑"})
	if _, err := st.AttachGoalTask(ctx, wsID, g2.ID, lastMilestone(t, g2).ID, t1.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("重复挂载应为 ErrConflict，得到 %v", err)
	}
	// 里程碑不属于这个目标 → NotFound。
	if _, err := st.AttachGoalTask(ctx, wsID, g2.ID, m1.ID, t1.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("跨目标里程碑应为 NotFound，得到 %v", err)
	}

	// 不存在的任务 → NotFound；别的任务所在工作区 → NotFound（不泄露归属）。
	if _, err := st.AttachGoalTask(ctx, wsID, g.ID, m1.ID, "t_nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在任务应为 NotFound，得到 %v", err)
	}
	other, err := st.CreateWorkspaceWithOwner(ctx, uid, "别的", "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	foreign := mustCreateTaskForGoal(t, st, other.ID, uid, "别人的任务")
	if _, err := st.AttachGoalTask(ctx, wsID, g.ID, m1.ID, foreign.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("跨工作区任务应为 NotFound，得到 %v", err)
	}

	// 完成任务后视图里的 done 跟着变（进度由前端由任务推导，这里钉死视图口径）。
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
	if found == nil || len(found.Milestones[0].Tasks) != 2 || !found.Milestones[0].Tasks[0].Done || found.Milestones[0].Tasks[1].Done {
		t.Fatalf("完成状态没反映到目标视图：%+v", found.Milestones[0])
	}

	// 移出目标后任务本身还在。
	g, err = st.DetachGoalTask(ctx, wsID, g.ID, t2.ID)
	if err != nil || len(g.Milestones[0].Tasks) != 1 || g.Milestones[0].Tasks[0].TaskID != t1.ID {
		t.Fatalf("detach: %v %+v", err, g.Milestones[0])
	}
	if _, err := st.taskByID(ctx, wsID, t2.ID, false); err != nil {
		t.Fatalf("detach 不该删任务：%v", err)
	}
	_ = m2
}

func TestGoalSoftDeleteKeepsMembership(t *testing.T) {
	st, wsID, uid := goalTestStore(t)
	ctx := context.Background()
	g := mustCreateGoal(t, st, wsID, uid, "发布会筹备")
	g = mustCreateMilestone(t, st, wsID, g.ID, MilestoneInput{Title: "预热"})
	ms := lastMilestone(t, g)
	tk := mustCreateTaskForGoal(t, st, wsID, uid, "定场地")
	if _, err := st.AttachGoalTask(ctx, wsID, g.ID, ms.ID, tk.ID); err != nil {
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
	if len(restored.Milestones) != 1 || len(restored.Milestones[0].Tasks) != 0 {
		t.Fatalf("任务软删期间不该计入目标：%+v", restored.Milestones)
	}
	if _, err := st.RestoreTasks(ctx, wsID, uid, []string{tk.ID}); err != nil {
		t.Fatal(err)
	}
	restored, err = st.RestoreGoal(ctx, wsID, g.ID) // 幂等再恢复一次
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Milestones[0].Tasks) != 1 || !restored.Milestones[0].Tasks[0].Done {
		t.Fatalf("任务撤销删除后归属应自动恢复：%+v", restored.Milestones[0].Tasks)
	}
}

// 战略决策三问（我的目标 / 验收标准 / 失败代价）：创建可带、可改，列表与
// 详情读法一致返回。
func TestGoalStrategyFieldsRoundtrip(t *testing.T) {
	st, wsID, uid := goalTestStore(t)
	ctx := context.Background()
	g := mustCreateGoal(t, st, wsID, uid, "发布会筹备")

	created, err := st.CreateGoal(ctx, wsID, uid, GoalInput{
		Title: "带战略决策的目标", GoalText: "办成一场发布会", Acceptance: "到场 200 人",
		FailureCost: "错过窗口期", Status: "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.GoalText != "办成一场发布会" || created.Acceptance != "到场 200 人" || created.FailureCost != "错过窗口期" {
		t.Fatalf("三问未落库：%+v", created)
	}

	updated, err := st.UpdateGoal(ctx, wsID, created.ID, GoalInput{
		Title: created.Title, GoalText: "改成线上直播", Acceptance: created.Acceptance,
		FailureCost: created.FailureCost, Group: created.Group, Status: created.Status,
	}, created.Version)
	if err != nil {
		t.Fatal(err)
	}
	if updated.GoalText != "改成线上直播" || updated.Version != 2 {
		t.Fatalf("三问未更新：%+v", updated)
	}

	// 老目标三问为空字符串而不是缺失。
	listed, err := st.ListGoals(ctx, wsID)
	if err != nil {
		t.Fatal(err)
	}
	for i := range listed {
		if listed[i].ID == g.ID && (listed[i].GoalText != "" || listed[i].Acceptance != "" || listed[i].FailureCost != "") {
			t.Fatalf("未填三问的目标应为空串：%+v", listed[i])
		}
	}
}

func TestGoalUpdateVersionConflict(t *testing.T) {
	st, wsID, uid := goalTestStore(t)
	ctx := context.Background()
	g := mustCreateGoal(t, st, wsID, uid, "旧名字")

	updated, err := st.UpdateGoal(ctx, wsID, g.ID, GoalInput{
		Title: "新名字", Status: "active",
	}, g.Version)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != "新名字" || updated.Version != 2 {
		t.Fatalf("更新结果不对：%+v", updated)
	}
	// 过期版本 → ErrConflict；不存在的目标 → NotFound。
	if _, err := st.UpdateGoal(ctx, wsID, g.ID, GoalInput{
		Title: "第三次", Status: "active",
	}, g.Version); !errors.Is(err, ErrConflict) {
		t.Fatalf("应为 ErrConflict，得到 %v", err)
	}
	if _, err := st.UpdateGoal(ctx, wsID, 9999, GoalInput{
		Title: "x", Status: "active",
	}, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("应为 ErrNotFound，得到 %v", err)
	}
}

// 目标整体重排：ids 必须正好是当前全部目标（含已落定），单事务落 position。
func TestGoalReorder(t *testing.T) {
	st, wsID, uid := goalTestStore(t)
	ctx := context.Background()
	g1 := mustCreateGoal(t, st, wsID, uid, "甲")
	g2 := mustCreateGoal(t, st, wsID, uid, "乙")
	g3 := mustCreateGoal(t, st, wsID, uid, "丙")
	// 创建顺序 丙→乙→甲（新目标排最前）。

	if err := st.ReorderGoals(ctx, wsID, []int64{g3.ID, g1.ID, g2.ID}); err != nil {
		t.Fatal(err)
	}
	goals, err := st.ListGoals(ctx, wsID)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []int64{g3.ID, g1.ID, g2.ID} {
		if goals[i].ID != want {
			t.Fatalf("位置 %d 应为 %d，得到 %+v", i, want, goals[i])
		}
	}

	// 数量不符拒绝，且不产生部分写入。
	if err := st.ReorderGoals(ctx, wsID, []int64{g1.ID}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("应为 ErrInvalid，得到 %v", err)
	}
	if err := st.ReorderGoals(ctx, wsID, []int64{g1.ID, g2.ID, 9999}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("应为 ErrNotFound，得到 %v", err)
	}
	goals, err = st.ListGoals(ctx, wsID)
	if err != nil || goals[0].ID != g3.ID || goals[1].ID != g1.ID || goals[2].ID != g2.ID {
		t.Fatalf("失败的 reorder 不应产生副作用：%+v %v", goals, err)
	}
}

// 挂载类的写操作必须用 wsID 把关目标归属：不带校验时，跨工作区的 goalID
// 会「先写成功、再报 NotFound」——返回 404 但副作用已经发生。这里钉死：
// 对外工作区的目标返回 NotFound，且对方的目标内容原封不动。
func TestGoalMutationsScopedToWorkspace(t *testing.T) {
	st, wsID, uid := goalTestStore(t)
	ctx := context.Background()
	g := mustCreateGoal(t, st, wsID, uid, "本区目标")
	g = mustCreateMilestone(t, st, wsID, g.ID, MilestoneInput{Title: "预热"})
	ms := lastMilestone(t, g)
	t1 := mustCreateTaskForGoal(t, st, wsID, uid, "任务一")
	t2 := mustCreateTaskForGoal(t, st, wsID, uid, "任务二")
	for _, tk := range []*Task{t1, t2} {
		if _, err := st.AttachGoalTask(ctx, wsID, g.ID, ms.ID, tk.ID); err != nil {
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
	if _, err := st.AttachGoalTask(ctx, other.ID, g.ID, ms.ID, foreignTask.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("attach 跨区目标应为 NotFound，得到 %v", err)
	}
	if _, err := st.DeleteGoalMilestone(ctx, other.ID, g.ID, ms.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删除跨区目标里程碑应为 NotFound，得到 %v", err)
	}

	// 对方工作区视角：目标、里程碑、挂载关系都原样（没有发生任何写入）。
	goals, err := st.ListGoals(ctx, wsID)
	if err != nil || len(goals) != 1 {
		t.Fatalf("list: %v %d", err, len(goals))
	}
	if len(goals[0].Milestones) != 1 || len(goals[0].Milestones[0].Tasks) != 2 ||
		goals[0].Milestones[0].Tasks[0].TaskID != t1.ID || goals[0].Milestones[0].Tasks[1].TaskID != t2.ID {
		t.Fatalf("跨区调用产生了副作用：%+v", goals[0].Milestones)
	}
	// 对方任务也没被挂进任何目标。
	otherGoals, err := st.ListGoals(ctx, other.ID)
	if err != nil || len(otherGoals) != 0 {
		t.Fatalf("对方工作区不应多出目标：%v %+v", err, otherGoals)
	}
}
