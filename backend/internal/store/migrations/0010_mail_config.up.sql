-- 0010_mail_config: 邮箱账单导入（腾讯官方 Agently Mail skill / agently-cli）。
-- 凭据不在本表：OAuth 授权后凭据由 agently-cli 存于服务器本机，
-- 本应用只存非机密配置与运行状态。mailbox 是最近一次 +me 看到的邮箱
-- 地址，仅用于展示。keywords 是账单预筛关键词（| 分隔），减少 LLM 调用；
-- 预筛只是粗筛，是否账单以 Kimi 解析为准。

CREATE TABLE workspace_mail_config (
  workspace_id   INTEGER PRIMARY KEY REFERENCES workspaces (id) ON DELETE CASCADE,
  enabled        INTEGER NOT NULL DEFAULT 0,
  mailbox        TEXT NOT NULL DEFAULT '',
  keywords       TEXT NOT NULL DEFAULT '',
  last_run_at    TEXT,
  last_run_ok    INTEGER,                  -- 1 成功 / 0 失败 / NULL 未跑过
  last_run_error TEXT NOT NULL DEFAULT '',
  last_imported  INTEGER NOT NULL DEFAULT 0, -- 最近一次导入了几笔
  version        INTEGER NOT NULL DEFAULT 1,
  updated_by     INTEGER REFERENCES users (id),
  updated_at     TEXT NOT NULL
);
