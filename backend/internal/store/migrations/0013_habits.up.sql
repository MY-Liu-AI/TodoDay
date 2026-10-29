-- 0013_habits: 「周期事项」从并行目标独立为工作区级「习惯」实体。
-- 概念拆分：目标是「有终点的产出」（只有里程碑拆解一种形态），习惯是
-- 「无终点的练习」（每周哪几天 + 打卡），两者不再共用一个 mode 枚举。
--
-- 结构变化：goal_routines.goal_id 不复存在（习惯直属工作区，幂等键从
-- UNIQUE(goal_id, client_id) 变为 UNIQUE(workspace_id, client_id)）；
-- tasks.routine_id 改名 tasks.habit_id，外键改指 habits。
--
-- SQLite 约束：被外键引用的表不能直接 DROP（隐式 DELETE 会触发 FK 检查），
-- 被唯一约束占用的列也删不掉，所以按官方 12-step 流程重建。tasks 与
-- goal_routines 都要重建，且 goal_tasks（task_id → tasks）也要跟着重建：
-- RENAME 会把其他表里的外键文本从 "tasks" 改写成 "tasks_old"（迁移在
-- 事务里跑，PRAGMA legacy_alter_table 是 no-op，挡不住这一步），不重建
-- goal_tasks 的话，DROP tasks_old 在有挂载数据时必然 FK 失败、空库也会
-- 留下指向 tasks_old 的坏外键。重建顺序：先复制数据、再把引用方改指回
-- 新表、最后才 DROP 旧表。

CREATE TABLE habits (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  workspace_id INTEGER NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
  title        TEXT NOT NULL,
  weekday_mask INTEGER NOT NULL CHECK (weekday_mask BETWEEN 1 AND 127),
  position     INTEGER NOT NULL DEFAULT 0,
  version      INTEGER NOT NULL DEFAULT 1,
  client_id    TEXT,
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL,
  UNIQUE (workspace_id, client_id)
);
CREATE INDEX idx_habits_ws ON habits (workspace_id, position);

-- 周期事项原样迁入 habits（id 保留，历史实例回指不断）。
INSERT INTO habits (id, workspace_id, title, weekday_mask, position, version, client_id, created_at, updated_at)
  SELECT id, workspace_id, title, weekday_mask, position, version, client_id, created_at, updated_at
  FROM goal_routines;

-- 重建 tasks：routine_id → habit_id，列序与约束照旧（0001 原序 + 末列）。
ALTER TABLE tasks RENAME TO tasks_old;
CREATE TABLE tasks (
  id           TEXT PRIMARY KEY,
  workspace_id INTEGER NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
  created_by   INTEGER NOT NULL REFERENCES users (id),
  group_id     INTEGER REFERENCES task_groups (id) ON DELETE SET NULL,
  title        TEXT NOT NULL,
  priority     INTEGER NOT NULL DEFAULT 3 CHECK (priority BETWEEN 1 AND 3),
  due_date     TEXT CHECK (due_date IS NULL OR due_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
  position     INTEGER NOT NULL DEFAULT 0,
  completed_at TEXT,
  deleted_at   TEXT,
  client_id    TEXT,
  version      INTEGER NOT NULL DEFAULT 1,
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL,
  habit_id     INTEGER REFERENCES habits (id) ON DELETE SET NULL,
  UNIQUE (workspace_id, client_id)
);
INSERT INTO tasks (id, workspace_id, created_by, group_id, title, priority, due_date, position, completed_at, deleted_at, client_id, version, created_at, updated_at, habit_id)
  SELECT id, workspace_id, created_by, group_id, title, priority, due_date, position, completed_at, deleted_at, client_id, version, created_at, updated_at, routine_id
  FROM tasks_old;

-- 重建 goal_tasks：把被 RENAME 改写成 "tasks_old" 的外键文本改回新 tasks，
-- 列序与约束照旧（0004 原序 + 0008 的 milestone_id）。
ALTER TABLE goal_tasks RENAME TO goal_tasks_old;
CREATE TABLE goal_tasks (
  goal_id      INTEGER NOT NULL REFERENCES goals (id) ON DELETE CASCADE,
  task_id      TEXT NOT NULL REFERENCES tasks (id),
  position     INTEGER NOT NULL DEFAULT 0,
  weight       INTEGER NOT NULL DEFAULT 1 CHECK (weight >= 1),
  milestone_id INTEGER REFERENCES goal_milestones (id),
  created_at   TEXT NOT NULL,
  UNIQUE (goal_id, task_id),
  UNIQUE (task_id)
);
INSERT INTO goal_tasks (goal_id, task_id, position, weight, milestone_id, created_at)
  SELECT goal_id, task_id, position, weight, milestone_id, created_at
  FROM goal_tasks_old;
-- 索引名在库里全局唯一：RENAME 只是给旧表换名，索引名还被旧表占着，
-- 必须等 DROP 之后才能用同名重建。
DROP TABLE goal_tasks_old;
CREATE INDEX idx_goal_tasks_goal ON goal_tasks (goal_id, position);
CREATE INDEX idx_goal_tasks_ms ON goal_tasks (milestone_id);

-- 现在没有任何表引用 tasks_old / goal_routines，可以安全 DROP。
DROP TABLE tasks_old;
CREATE INDEX idx_tasks_ws_due ON tasks (workspace_id, due_date) WHERE deleted_at IS NULL;
CREATE INDEX idx_tasks_ws_pos ON tasks (workspace_id, position) WHERE deleted_at IS NULL;
CREATE INDEX idx_tasks_ws_del ON tasks (workspace_id, deleted_at);
CREATE INDEX idx_tasks_habit ON tasks (habit_id) WHERE deleted_at IS NULL;

DROP TABLE goal_routines;

-- 并行目标归档（进行中 → 归档；已达成/已归档不动）。目标壳（含决策三问）
-- 保留可查，状态可经 API 改回；旧并行目标的里程碑数据仍留在库中（0009 起
-- 已不再展示），与 0009 的「不迁移」承诺一致。
UPDATE goals SET status = 'archived',
  updated_at = strftime('%Y-%m-%dT%H:%M:%S', 'now') || '.000000000Z'
 WHERE mode = 'parallel' AND status = 'active' AND deleted_at IS NULL;
