-- 0016_ledger_budgets: 分类预算。每个分类一个长期月度预算（设置一次、
-- 每月沿用直到修改），不设月份列；考核口径与流水一致——当月该分类
-- 未删除支出合计，由前端从当月流水推导（与三卡汇总同口径）。
-- 与账目同为成员级数据：不设 owner 边界，工作区成员皆可看改。
CREATE TABLE ledger_budgets (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  workspace_id INTEGER NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
  category     TEXT NOT NULL,
  amount_fen   INTEGER NOT NULL CHECK (amount_fen > 0),
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL,
  UNIQUE (workspace_id, category)
);
CREATE INDEX idx_ledger_budgets_ws ON ledger_budgets (workspace_id);
