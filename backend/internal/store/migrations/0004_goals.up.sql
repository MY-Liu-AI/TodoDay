-- 0004_goals: 目标模块。目标把任务串联成可跟踪的整体。
-- goals 与 tasks 通过 goal_tasks 关联：任务是唯一执行单元，进度由
-- 关联任务的完成情况推导（不落库）；一条任务同一时间只属于一个目标
-- （goal_tasks.task_id 唯一约束保证）。
-- 目标删除是软删（deleted_at），删除期间 goal_tasks 保留 —— 任务撤销
-- 删除后归属自动恢复；任务软删后不计入进度（查询带 t.deleted_at IS NULL）。
-- mode 区分串联语义：sequential 下任务按 position 依次解锁（前端推导，
-- 服务端不强制）；weight 预留加权，当前一律 1。

CREATE TABLE goals (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  workspace_id INTEGER NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
  created_by   INTEGER NOT NULL REFERENCES users (id),
  title        TEXT NOT NULL,
  note         TEXT NOT NULL DEFAULT '',
  mode         TEXT NOT NULL DEFAULT 'parallel' CHECK (mode IN ('parallel', 'sequential')),
  due          TEXT, -- YYYY-MM-DD 或 NULL（无排期）
  status       TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'done', 'archived')),
  position     INTEGER NOT NULL DEFAULT 0,
  version      INTEGER NOT NULL DEFAULT 1,
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL,
  deleted_at   TEXT
);
CREATE INDEX idx_goals_ws ON goals (workspace_id, deleted_at, status, position);

CREATE TABLE goal_tasks (
  goal_id    INTEGER NOT NULL REFERENCES goals (id) ON DELETE CASCADE,
  task_id    TEXT NOT NULL REFERENCES tasks (id),
  position   INTEGER NOT NULL DEFAULT 0,
  weight     INTEGER NOT NULL DEFAULT 1 CHECK (weight >= 1),
  created_at TEXT NOT NULL,
  UNIQUE (goal_id, task_id),
  UNIQUE (task_id)
);
CREATE INDEX idx_goal_tasks_goal ON goal_tasks (goal_id, position);
