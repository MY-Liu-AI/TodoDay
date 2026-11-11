-- 记账本分类体系（2026-10-29）：per-workspace 分类清单。
-- 此前 category 是纯自由文本（ledger_entries/ledger_budgets 不动，兼容旧数据）；
-- 本表让分类可管理（增/改名/删），邮箱账单解析按工作区分类清单收敛。
-- 首次读取时由服务端播种默认六分类（见 store/category.go），migration 不带数据。
CREATE TABLE ledger_categories (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id INTEGER NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    color TEXT NOT NULL,
    position INTEGER NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (workspace_id, name)
);

CREATE INDEX idx_ledger_categories_ws ON ledger_categories (workspace_id);
