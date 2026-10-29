-- 0009_goal_routines: 并行目标的「周期事项」。
-- 周期事项 = 标题 + 每周重复的星期（weekday_mask 位掩码：bit0=周一 …
-- bit6=周日）。服务端把事项滚动展开成真实任务实例（tasks.routine_id
-- 回指，client_id 确定性 = routine-<rid>-<YYYY-MM-DD>，天然幂等），
-- 实例出现在计划页/日历/今天清单，可勾选；进度 = 实例完成数/总数。
-- 并行目标的里程碑拆解对旧数据不再使用（用户确认无需迁移）。

CREATE TABLE goal_routines (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  goal_id      INTEGER NOT NULL REFERENCES goals (id) ON DELETE CASCADE,
  workspace_id INTEGER NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
  title        TEXT NOT NULL,
  weekday_mask INTEGER NOT NULL CHECK (weekday_mask BETWEEN 1 AND 127),
  position     INTEGER NOT NULL DEFAULT 0,
  version      INTEGER NOT NULL DEFAULT 1,
  client_id    TEXT,
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL,
  UNIQUE (goal_id, client_id)
);
CREATE INDEX idx_goal_routines_goal ON goal_routines (goal_id, position);

-- 实例任务回指事项；事项删除时历史实例保留为普通任务（routine_id 置空）。
ALTER TABLE tasks ADD COLUMN routine_id INTEGER REFERENCES goal_routines (id) ON DELETE SET NULL;
CREATE INDEX idx_tasks_routine ON tasks (routine_id) WHERE deleted_at IS NULL;
