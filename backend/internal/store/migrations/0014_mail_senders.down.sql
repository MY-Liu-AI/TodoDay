-- SQLite 3.35+ 支持 DROP COLUMN（modernc.org/sqlite 满足）。
-- 回滚后退回纯关键词预筛。
ALTER TABLE workspace_mail_config DROP COLUMN senders;
