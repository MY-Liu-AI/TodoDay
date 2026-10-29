package store

import (
	"context"
	"strings"
	"testing"
)

func TestMigrateAndRollback(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()

	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	var version int
	if err := st.DB().QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != 15 {
		t.Fatalf("expected latest version 15, got %d", version)
	}

	// 每个 migration 都要有对应的 down：漏了 down 会让 RollbackAll 只回滚
	// 一部分，留下半旧半新的库。
	names, err := migrationFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	ups, downs := map[string]bool{}, map[string]bool{}
	for _, e := range names {
		n := e.Name()
		if strings.HasSuffix(n, ".up.sql") {
			ups[strings.TrimSuffix(n, ".up.sql")] = true
		}
		if strings.HasSuffix(n, ".down.sql") {
			downs[strings.TrimSuffix(n, ".down.sql")] = true
		}
	}
	for base := range ups {
		if !downs[base] {
			t.Errorf("migration %s 缺少对应的 .down.sql", base)
		}
	}
	for base := range downs {
		if !ups[base] {
			t.Errorf("%s.down.sql 没有对应的 .up.sql", base)
		}
	}

	// Re-running migrations is a no-op.
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("re-migrate: %v", err)
	}

	if err := st.RollbackAll(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	var tables int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(1) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 0 {
		t.Fatalf("expected all tables dropped, %d remain", tables)
	}

	// And migration can be applied again from scratch.
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("re-migrate after rollback: %v", err)
	}
}

// 0008 数据迁移：note/goal_tasks 时代的库升级后，每条已挂载任务包成
// 一个同名里程碑（标题 = 任务标题，位置沿用挂载顺序），挂载关系指回
// 新里程碑 —— 归属与进度推导不断档。
func TestGoalMilestonesMigration(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()

	// 手动应用到 0007（里程碑时代之前）：逐条执行 up 并登记版本，
	// 让后续的 Migrate 只补 0008。
	if _, err := st.DB().ExecContext(ctx, `CREATE TABLE schema_migrations (
		version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	names, err := migrationFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range names {
		name := e.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		v, err := migrationVersion(strings.TrimSuffix(name, ".up.sql"))
		if err != nil {
			t.Fatal(err)
		}
		if v > 7 {
			continue
		}
		b, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.DB().ExecContext(ctx, string(b)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
		if _, err := st.DB().ExecContext(ctx,
			`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`, v, "test"); err != nil {
			t.Fatal(err)
		}
	}

	u, err := st.CreateUser(ctx, "mig2-user", "x")
	if err != nil {
		t.Fatal(err)
	}
	ws, err := st.CreateWorkspaceWithOwner(ctx, u.ID, "个人工作区", "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	res, err := st.DB().ExecContext(ctx,
		`INSERT INTO goals (workspace_id, created_by, title, goal_text, acceptance, failure_cost, mode, status, position, version, created_at, updated_at)
		 VALUES (?, ?, '老目标', '', '', '', 'parallel', 'active', 0, 1, '2026-10-01 00:00:00', '2026-10-01 00:00:00')`,
		ws.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	gid, _ := res.LastInsertId()
	// 旧挂载：两条任务，position 0 和 1。
	mkTask := func(id, title string, pos int) {
		t.Helper()
		if _, err := st.DB().ExecContext(ctx,
			`INSERT INTO tasks (id, workspace_id, created_by, title, priority, position, version, created_at, updated_at)
			 VALUES (?, ?, ?, ?, 3, 0, 1, '2026-10-01 00:00:00', '2026-10-01 00:00:00')`,
			id, ws.ID, u.ID, title); err != nil {
			t.Fatal(err)
		}
		if _, err := st.DB().ExecContext(ctx,
			`INSERT INTO goal_tasks (goal_id, task_id, position, weight, created_at) VALUES (?, ?, ?, 1, '2026-10-01 00:00:00')`,
			gid, id, pos); err != nil {
			t.Fatal(err)
		}
	}
	mkTask("legacy-a", "定场地", 0)
	mkTask("legacy-b", "发邀请", 1)

	// 升级到 0008：每条挂载变成同名里程碑，挂载关系指回新里程碑。
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate to 8: %v", err)
	}
	rows, err := st.DB().QueryContext(ctx,
		`SELECT m.title, m.position, gt.task_id FROM goal_milestones m
		 JOIN goal_tasks gt ON gt.milestone_id = m.id
		 WHERE m.goal_id = ? ORDER BY m.position`, gid)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type row struct {
		title, taskID string
		position      int
	}
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.title, &r.position, &r.taskID); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	if len(got) != 2 || got[0].title != "定场地" || got[0].taskID != "legacy-a" ||
		got[1].title != "发邀请" || got[1].taskID != "legacy-b" {
		t.Fatalf("旧挂载应包成同名里程碑：%+v", got)
	}

	// 迁移后正常走 store API：目标视图里里程碑与任务齐全。
	goals, err := st.ListGoals(ctx, ws.ID)
	if err != nil || len(goals) != 1 || len(goals[0].Milestones) != 2 ||
		len(goals[0].Milestones[0].Tasks) != 1 || goals[0].Milestones[0].Tasks[0].TaskID != "legacy-a" {
		t.Fatalf("迁移后 store 视图不对：%v %+v", err, goals)
	}
}

// 0007 数据迁移：note 时代的库升级到战略决策三问后，旧备注必须搬进
// goal_text（不丢用户已有内容）；note 列本身保留原文。
func TestGoalStrategyMigration(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()

	// 手动应用到 0006（note 时代）：逐条执行 up 并登记版本，
	// 让后续的 Migrate 只补 0007。
	if _, err := st.DB().ExecContext(ctx, `CREATE TABLE schema_migrations (
		version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	names, err := migrationFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range names {
		name := e.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		v, err := migrationVersion(strings.TrimSuffix(name, ".up.sql"))
		if err != nil {
			t.Fatal(err)
		}
		if v > 6 {
			continue
		}
		b, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.DB().ExecContext(ctx, string(b)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
		if _, err := st.DB().ExecContext(ctx,
			`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`, v, "test"); err != nil {
			t.Fatal(err)
		}
	}

	// note 时代的目标：只有 note，没有三问列。
	u, err := st.CreateUser(ctx, "mig-user", "x")
	if err != nil {
		t.Fatal(err)
	}
	ws, err := st.CreateWorkspaceWithOwner(ctx, u.ID, "个人工作区", "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	res, err := st.DB().ExecContext(ctx,
		`INSERT INTO goals (workspace_id, created_by, title, note, mode, status, position, version, created_at, updated_at)
		 VALUES (?, ?, '老目标', '月底发布', 'parallel', 'active', 0, 1, '2026-10-01 00:00:00', '2026-10-01 00:00:00')`,
		ws.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	gid, _ := res.LastInsertId()

	// 升级到 0007：goal_text 应收到旧备注。
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate to 7: %v", err)
	}
	var goalText, note string
	if err := st.DB().QueryRowContext(ctx,
		`SELECT goal_text, note FROM goals WHERE id = ?`, gid).Scan(&goalText, &note); err != nil {
		t.Fatal(err)
	}
	if goalText != "月底发布" {
		t.Fatalf("旧备注应迁移进 goal_text，得到 %q", goalText)
	}
	if note != "月底发布" {
		t.Fatalf("note 列应保留原文，得到 %q", note)
	}
}
