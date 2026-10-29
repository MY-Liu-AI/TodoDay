-- 0011_mail_seen: 已处理过的邮件。每封邮件只处理一次：账单子表存关联的
-- 账目 id（mail_seen.entry_id），非账单存 NULL。与 ledger 的 client_id
-- （mail:<message_id>）双保险防重复：即便 seen 表丢了重放，落库也是幂等的。

CREATE TABLE mail_seen (
  workspace_id INTEGER NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
  message_id   TEXT NOT NULL,  -- msg_xxx
  entry_id     INTEGER,        -- 关联账目；非账单为 NULL
  created_at   TEXT NOT NULL,
  PRIMARY KEY (workspace_id, message_id)
);
