-- 0005_goal_group rollback: drops the group label column (goals and their
-- task links survive; only the label is lost).

ALTER TABLE goals DROP COLUMN group_name;
