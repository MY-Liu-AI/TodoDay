-- 0006_ledger rollback: drops the ledger (a pure add-on module; tasks and
-- goals are unaffected, nothing else references these rows).

DROP TABLE IF EXISTS ledger_entries;
