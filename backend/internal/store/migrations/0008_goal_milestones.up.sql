-- 0008_goal_milestones: 目标拆解为 目标 → 里程碑 → 任务 三层。
-- 里程碑是独立实体：自带标题与开始/截止日期（与计划页日历联动），
-- 任务经 goal_tasks.milestone_id 挂到里程碑下；一条任务仍只属于一个
-- 目标（goal_tasks.task_id 唯一约束不变）。里程碑软删，删除时其下
-- 挂载关系解除（任务回到普通清单，任务本身不动）。
--
-- 旧数据迁移：既有 goal_tasks 里的每条挂载任务包成一个同名里程碑
-- （标题 = 任务标题），任务原样挂进该里程碑，归属与进度推导不断档。

CREATE TABLE goal_milestones (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  goal_id      INTEGER NOT NULL REFERENCES goals (id) ON DELETE CASCADE,
  workspace_id INTEGER NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
  title        TEXT NOT NULL,
  start_date   TEXT, -- YYYY-MM-DD 本地日历日期，可空（未设开始）
  due_date     TEXT, -- YYYY-MM-DD 本地日历日期，可空（未设截止）
  position     INTEGER NOT NULL DEFAULT 0,
  version      INTEGER NOT NULL DEFAULT 1,
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL,
  deleted_at   TEXT
);
CREATE INDEX idx_goal_ms_goal ON goal_milestones (goal_id, deleted_at, position);

ALTER TABLE goal_tasks ADD COLUMN milestone_id INTEGER REFERENCES goal_milestones (id);
CREATE INDEX idx_goal_tasks_ms ON goal_tasks (milestone_id);

-- 每条旧挂载任务 → 一个同名里程碑（位置沿用挂载顺序）。
INSERT INTO goal_milestones (goal_id, workspace_id, title, position, version, created_at, updated_at)
SELECT gt.goal_id, g.workspace_id, t.title, gt.position, 1, gt.created_at, gt.created_at
FROM goal_tasks gt
JOIN goals g ON g.id = gt.goal_id
JOIN tasks t ON t.id = gt.task_id;

-- 挂载关系指回新建的里程碑（goal_id + position 定位，迁移刚写入、一一对应）。
UPDATE goal_tasks SET milestone_id = m.id
FROM goal_milestones m
WHERE m.goal_id = goal_tasks.goal_id AND m.position = goal_tasks.position;
