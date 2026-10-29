-- 0006_ledger: 记账本。每行是一笔收入或支出。
-- entry_date 是 YYYY-MM-DD 本地日历日期（客户端语义，与 tasks.due_date
-- 同约定，不做时区换算）；金额以分存储（amount_fen），避免浮点误差。
-- category 是自由文本、服务端不解释（与 goals.group_name 同约定），
-- 收入（type = 'inc'）不占分类、存 NULL。删除是软删，撤销走恢复接口；
-- client_id 是创建幂等键：响应丢失后重试返回原行，不产生重复账目。

CREATE TABLE ledger_entries (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  workspace_id INTEGER NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
  created_by   INTEGER NOT NULL REFERENCES users (id),
  entry_date   TEXT NOT NULL, -- YYYY-MM-DD
  type         TEXT NOT NULL CHECK (type IN ('exp', 'inc')),
  category     TEXT,          -- 支出分类；收入为 NULL
  amount_fen   INTEGER NOT NULL CHECK (amount_fen > 0),
  note         TEXT NOT NULL DEFAULT '',
  client_id    TEXT,
  version      INTEGER NOT NULL DEFAULT 1,
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL,
  deleted_at   TEXT,
  UNIQUE (workspace_id, client_id)
);
CREATE INDEX idx_ledger_ws_date ON ledger_entries (workspace_id, entry_date);
CREATE INDEX idx_ledger_ws_live ON ledger_entries (workspace_id) WHERE deleted_at IS NULL;
