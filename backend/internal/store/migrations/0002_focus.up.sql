-- 0002_focus: 番茄专注记录。
-- 每次专注（完整 25 分钟 = completed；中途放弃 = abandoned）写一行。
-- task_title 为开始时的快照：任务之后被改名/删除，历史统计不变。
-- 统计按 ended_at 在工作区时区所属的日期归属（一个番茄算在它结束的那天）。

CREATE TABLE focus_sessions (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  workspace_id INTEGER NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
  user_id      INTEGER NOT NULL REFERENCES users (id),
  task_id      TEXT,
  task_title   TEXT NOT NULL DEFAULT '',
  planned_sec  INTEGER NOT NULL CHECK (planned_sec BETWEEN 60 AND 7200),
  duration_sec INTEGER NOT NULL CHECK (duration_sec BETWEEN 1 AND 7200),
  status       TEXT NOT NULL CHECK (status IN ('completed', 'abandoned')),
  started_at   TEXT NOT NULL,
  ended_at     TEXT NOT NULL,
  client_id    TEXT,
  created_at   TEXT NOT NULL,
  UNIQUE (workspace_id, client_id)
);
CREATE INDEX idx_focus_ws_time ON focus_sessions (workspace_id, ended_at);
