-- 0003_assistant_config rollback: drops the per-workspace assistant config.
-- Destructive: encrypted credentials are lost. They cannot be recovered without
-- the original plaintext, so they must be re-entered after re-applying.

DROP TABLE IF EXISTS workspace_assistant_config;
