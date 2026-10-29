-- 0013_habits 回滚：习惯退回目标下的周期事项；habit_id 改回 routine_id。
-- 习惯已无目标归属，goal_routines 的 NOT NULL goal_id 接不回这批数据——
-- 按 RollbackAll 的破坏性语义（文档明确「会删用户数据」），重建空表结构、
-- 丢弃习惯行；被 0013 归档的并行目标也不在此恢复归档状态。
--
-- 与 up 同理：RENAME 会改写 goal_tasks 的外键文本，且 DROP 被引用的旧表
-- 会触发 FK 检查，所以 tasks 与 goal_tasks 一起按 12-step 重建，顺序
-- 先复制、改指回新表、最后 DROP 旧表。

CREATE TABLE goal_routines (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  goal_id      INTEGER NOT NULL REFERENCES goals (id) ON DELETE CASCADE,
  workspace_id INTEGER NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
  title        TEXT NOT NULL,
  weekday_mask INTEGER NOT NULL CHECK (weekday_mask BETWEEN 1 AND 127),
  position     INTEGER NOT NULL DEFAULT 0,
  version      INTEGER NOT NULL DEFAULT 1,
  client_id    TEXT,
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL,
  UNIQUE (goal_id, client_id)
);
CREATE INDEX idx_goal_routines_goal ON goal_routines (goal_id, position);

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
  routine_id   INTEGER REFERENCES goal_routines (id) ON DELETE SET NULL,
  UNIQUE (workspace_id, client_id)
);
INSERT INTO tasks (id, workspace_id, created_by, group_id, title, priority, due_date, position, completed_at, deleted_at, client_id, version, created_at, updated_at, routine_id)
  SELECT id, workspace_id, created_by, group_id, title, priority, due_date, position, completed_at, deleted_at, client_id, version, created_at, updated_at, NULL
  FROM tasks_old;

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
-- 索引名全局唯一：等旧表 DROP 释放名字后再重建。
DROP TABLE goal_tasks_old;
CREATE INDEX idx_goal_tasks_goal ON goal_tasks (goal_id, position);
CREATE INDEX idx_goal_tasks_ms ON goal_tasks (milestone_id);

DROP TABLE tasks_old;
CREATE INDEX idx_tasks_ws_due ON tasks (workspace_id, due_date) WHERE deleted_at IS NULL;
CREATE INDEX idx_tasks_ws_pos ON tasks (workspace_id, position) WHERE deleted_at IS NULL;
CREATE INDEX idx_tasks_ws_del ON tasks (workspace_id, deleted_at);
CREATE INDEX idx_tasks_routine ON tasks (routine_id) WHERE deleted_at IS NULL;

DROP TABLE habits;
