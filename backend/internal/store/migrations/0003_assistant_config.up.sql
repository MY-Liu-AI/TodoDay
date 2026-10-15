-- 0003_assistant_config: 每个工作区一份的助手配置。
--
-- bot_secret_enc / kimi_api_key_enc 存的是密文（AES-256-GCM，主密钥来自
-- 环境变量 TODO_CONFIG_KEY），不是明文。这两个值必须能解密回明文交给
-- 企业微信与 Moonshot，所以是仓库里第一处可逆存储的凭据。
-- bot_id / kimi_base / kimi_model 不是密钥，保持明文便于排查。
--
-- workspace_id 直接做主键：一个工作区只能有一份配置，由数据库保证。

CREATE TABLE workspace_assistant_config (
  workspace_id     INTEGER PRIMARY KEY REFERENCES workspaces (id) ON DELETE CASCADE,
  enabled          INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
  bot_id           TEXT NOT NULL DEFAULT '',
  bot_secret_enc   TEXT NOT NULL DEFAULT '',
  kimi_api_key_enc TEXT NOT NULL DEFAULT '',
  kimi_base        TEXT NOT NULL DEFAULT '',
  kimi_model       TEXT NOT NULL DEFAULT '',
  updated_by       INTEGER NOT NULL REFERENCES users (id),
  updated_at       TEXT NOT NULL,
  version          INTEGER NOT NULL DEFAULT 1
);
