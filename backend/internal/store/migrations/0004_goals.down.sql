-- 0004_goals rollback: drops goals and their task links (goal progress
-- history is derived from tasks/task_events, which survive; only the grouping
-- and ordering are lost).

DROP TABLE IF EXISTS goal_tasks;
DROP TABLE IF EXISTS goals;
