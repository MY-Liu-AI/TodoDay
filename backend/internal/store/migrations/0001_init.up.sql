-- 0001_init: initial full-stack schema for Todo Day.
-- See FULLSTACK.md: users, workspaces, workspace_members, task_groups, tasks, task_events.

CREATE TABLE users (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  username      TEXT NOT NULL UNIQUE COLLATE NOCASE,
  password_hash TEXT NOT NULL,
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL
);

CREATE TABLE sessions (
  token      TEXT PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL
);
CREATE INDEX idx_sessions_user ON sessions (user_id);

CREATE TABLE workspaces (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  name       TEXT NOT NULL,
  timezone   TEXT NOT NULL DEFAULT 'Asia/Shanghai',
  created_by INTEGER NOT NULL REFERENCES users (id),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE workspace_members (
  workspace_id INTEGER NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
  user_id      INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  role         TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('owner', 'member')),
  joined_at    TEXT NOT NULL,
  PRIMARY KEY (workspace_id, user_id)
);

CREATE TABLE task_groups (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  workspace_id INTEGER NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
  name         TEXT NOT NULL,
  position     INTEGER NOT NULL DEFAULT 0,
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL,
  UNIQUE (workspace_id, name)
);

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
  UNIQUE (workspace_id, client_id)
);
CREATE INDEX idx_tasks_ws_due ON tasks (workspace_id, due_date) WHERE deleted_at IS NULL;
CREATE INDEX idx_tasks_ws_pos ON tasks (workspace_id, position) WHERE deleted_at IS NULL;
CREATE INDEX idx_tasks_ws_del ON tasks (workspace_id, deleted_at);

-- 历史事件在软删除后保留：完成统计只数 completed，删除/恢复只影响 tasks.deleted_at。
CREATE TABLE task_events (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id      TEXT NOT NULL,
  workspace_id INTEGER NOT NULL,
  user_id      INTEGER NOT NULL,
  type         TEXT NOT NULL CHECK (type IN ('created', 'updated', 'completed', 'reopened', 'deleted', 'restored')),
  occurred_at  TEXT NOT NULL
);
CREATE INDEX idx_events_ws_time ON task_events (workspace_id, occurred_at);
