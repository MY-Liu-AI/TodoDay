-- SQLite 3.35+ 支持 DROP COLUMN（modernc.org/sqlite 满足）。
-- 回滚后退回 agently-cli 方案：本表不再持有凭据密文。
ALTER TABLE workspace_mail_config DROP COLUMN auth_code_enc;
