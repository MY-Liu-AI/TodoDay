-- 0005_goal_group: 目标增加分组标签。自由文本，与任务分组同名同色系
-- （颜色由前端 palette 按名称推导，服务端只如实存储，不解释语义）。
-- 空串 = 未分组。

ALTER TABLE goals ADD COLUMN group_name TEXT NOT NULL DEFAULT '';
