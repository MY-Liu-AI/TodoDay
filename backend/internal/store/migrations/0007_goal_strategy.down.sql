-- 0007_goal_strategy rollback: 三问列整体删除；note / due 列保留
-- （0007 up 未动它们，note 原文仍在；goal_text 的内容不回迁——
-- 降级即放弃战略决策内容，原 note 备份不受影响）。

ALTER TABLE goals DROP COLUMN goal_text;
ALTER TABLE goals DROP COLUMN acceptance;
ALTER TABLE goals DROP COLUMN failure_cost;
