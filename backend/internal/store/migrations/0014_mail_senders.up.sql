-- 0014_mail_senders: 账单预筛支持按发件人配置。senders 为 | 分隔的
-- 发件地址/域名子串清单（非机密，与 keywords 同级）：空 = 未配置，
-- 预筛退回关键词（默认词表）；与关键词同时配置则两者都命中才进入解析。
-- 用户可从自己邮箱「发现」真实发件人来勾选，不硬编码银行地址目录。
ALTER TABLE workspace_mail_config ADD COLUMN senders TEXT NOT NULL DEFAULT '';
