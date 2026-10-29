-- 0008 回滚：拆掉里程碑层。挂载关系降级为目标 ↔ 任务直连
-- （保留 goal_tasks 行、丢弃 milestone_id）；里程碑表整体删除。
-- SQLite 3.35+ 支持 DROP COLUMN（modernc.org/sqlite 满足）。

DROP INDEX IF EXISTS idx_goal_tasks_ms;
ALTER TABLE goal_tasks DROP COLUMN milestone_id;
DROP TABLE IF EXISTS goal_milestones;
