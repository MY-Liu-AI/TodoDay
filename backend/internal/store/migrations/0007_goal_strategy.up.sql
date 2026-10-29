-- 0007_goal_strategy: 目标「备注」升级为「战略决策」三问
-- （我的目标 / 验收标准 / 失败代价）；目标不再有截止日期
-- （due 列保留但不再读写，历史数据不丢）。
-- 旧 note 内容迁移进 goal_text，不丢用户已有内容。

ALTER TABLE goals ADD COLUMN goal_text TEXT NOT NULL DEFAULT '';
ALTER TABLE goals ADD COLUMN acceptance TEXT NOT NULL DEFAULT '';
ALTER TABLE goals ADD COLUMN failure_cost TEXT NOT NULL DEFAULT '';

UPDATE goals SET goal_text = note WHERE note <> '';
