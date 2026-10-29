-- 0015_mail_drop_keywords: 关键词预筛整体下线（2026-10-29 用户决定）：
-- 预筛只保留「账单邮件来源」发件人清单；未配置来源时窗口内全部
-- 交给 Kimi 判定（is_bill 把关），营销邮件标记 seen 后不再回看。
ALTER TABLE workspace_mail_config DROP COLUMN keywords;
