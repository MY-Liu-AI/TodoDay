-- 0009_goal_routines 回滚：实例任务先解除回指，再删事项表。
DROP INDEX IF EXISTS idx_tasks_routine;
ALTER TABLE tasks DROP COLUMN routine_id;
DROP TABLE IF EXISTS goal_routines;
