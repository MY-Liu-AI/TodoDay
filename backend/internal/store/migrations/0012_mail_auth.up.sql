-- 0012_mail_auth: 邮箱账单导入从 agently-cli（OAuth 凭据存服务器本机）
-- 切换为原生 IMAP + 授权码。凭据（邮箱地址 + 授权码）以 AES-256-GCM
-- 密文落在本表，主密钥 TODO_CONFIG_KEY，与助手 Bot Secret / Kimi Key
-- 同一套 secretbox 规则：auth_code_enc 空串 = 未配置或已被显式撤销；
-- 授权码在服务商处的有效期由用户在邮箱设置里管理（删除即彻底失效）。
ALTER TABLE workspace_mail_config ADD COLUMN auth_code_enc TEXT NOT NULL DEFAULT '';
