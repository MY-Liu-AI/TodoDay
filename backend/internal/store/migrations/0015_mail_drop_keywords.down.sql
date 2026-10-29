-- SQLite 3.35+ 支持 DROP COLUMN（modernc.org/sqlite 满足）。
-- 回滚后恢复关键词列（空默认，预筛退回关键词逻辑需代码层同步恢复）。
ALTER TABLE workspace_mail_config ADD COLUMN keywords TEXT NOT NULL DEFAULT '';
