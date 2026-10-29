package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Goal 是目标的线表示。目标 = 有终点的产出，统一拆解为
// 目标 → 里程碑 → 任务 三层（进度由里程碑下挂载任务的完成情况推导，不
// 落库）：已完成任务权重之和 / 全部权重 = 进度百分比；里程碑按 position
// 依次解锁（纯视图推导，服务端不强制）。
type Goal struct {
	ID          int64           `json:"id"`
	Title       string          `json:"title"`
	GoalText    string          `json:"goalText"`    // 战略决策·我的目标
	Acceptance  string          `json:"acceptance"`  // 战略决策·验收标准
	FailureCost string          `json:"failureCost"` // 战略决策·失败代价
	Group       string          `json:"group"`       // 分组标签，"" = 未分组（与任务分组同名同色）
	Status      string          `json:"status"`
	Position    int64           `json:"position"`
	Version     int64           `json:"version"`
	CreatedAt   string          `json:"createdAt"` // YYYY-MM-DD
	Milestones  []GoalMilestone `json:"milestones"`
}

// GoalMilestone 是目标下的里程碑：时间层的拆解单元，自带开始/截止日期
// （计划页日历按这两个端点展示）。任务挂在里程碑下（goal_tasks.milestone_id）。
// 里程碑软删：删除时解除其下挂载关系，任务回到普通清单；恢复找回里程碑
// 本身（标题与起止日期），任务归属不自动回填。
type GoalMilestone struct {
	ID       int64      `json:"id"`
	Title    string     `json:"title"`
	Start    string     `json:"start"` // YYYY-MM-DD 或 ""
	Due      string     `json:"due"`   // YYYY-MM-DD 或 ""
	Position int64      `json:"position"`
	Version  int64      `json:"version"`
	Tasks    []GoalTask `json:"tasks"`
}

// GoalTask 是里程碑下的一条挂载任务（任务的视图快照字段实时联表读取，
// 改任务本身两边同时生效——任务是唯一事实来源）。
type GoalTask struct {
	TaskID   string `json:"taskId"`
	Title    string `json:"title"`
	Done     bool   `json:"done"`
	Due      string `json:"due"`
	Group    string `json:"group"`
	Priority int    `json:"priority"`
	Position int64  `json:"position"`
}

// GoalInput carries validated fields for create/update.
// 目标没有截止日期：排期在里程碑的开始/截止日期上。
type GoalInput struct {
	Title       string
	GoalText    string
	Acceptance  string
	FailureCost string
	Group       string // 分组标签，"" = 未分组
	Status      string // active | done | archived
}

// MilestoneInput carries validated fields for milestone create/update.
type MilestoneInput struct {
	Title string
	Start string // YYYY-MM-DD 或 ""
	Due   string // YYYY-MM-DD 或 ""
}

const goalCols = `id, title, goal_text, acceptance, failure_cost, group_name, status, position, version, substr(created_at, 1, 10)`

func scanGoal(row interface{ Scan(...any) error }) (*Goal, error) {
	var g Goal
	err := row.Scan(&g.ID, &g.Title, &g.GoalText, &g.Acceptance, &g.FailureCost, &g.Group, &g.Status, &g.Position, &g.Version, &g.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &g, nil
}

const goalTaskCols = `gt.task_id, t.title, t.completed_at IS NOT NULL, COALESCE(t.due_date, ''),
	COALESCE((SELECT g.name FROM task_groups g WHERE g.id = t.group_id), ''), t.priority, gt.position`

func scanGoalTask(row interface{ Scan(...any) error }) (*GoalTask, error) {
	var gt GoalTask
	err := row.Scan(&gt.TaskID, &gt.Title, &gt.Done, &gt.Due, &gt.Group, &gt.Priority, &gt.Position)
	if err != nil {
		return nil, err
	}
	return &gt, nil
}

// milestoneTasksTx loads the live tasks under a milestone. 软删的任务不计入：
// 它们只是「暂时不在」，撤销删除后归属和进度自动恢复。
func milestoneTasksTx(ctx context.Context, tx *sql.Tx, goalID, msID int64) ([]GoalTask, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT `+goalTaskCols+` FROM goal_tasks gt JOIN tasks t ON t.id = gt.task_id
		 WHERE gt.goal_id = ? AND gt.milestone_id = ? AND t.deleted_at IS NULL
		 ORDER BY gt.position ASC, gt.created_at ASC`, goalID, msID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GoalTask{}
	for rows.Next() {
		gt, err := scanGoalTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *gt)
	}
	return out, rows.Err()
}

// goalMilestonesTx loads the live milestones of a goal (with their tasks).
func goalMilestonesTx(ctx context.Context, tx *sql.Tx, goalID int64) ([]GoalMilestone, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT id, title, COALESCE(start_date, ''), COALESCE(due_date, ''), position, version
		 FROM goal_milestones WHERE goal_id = ? AND deleted_at IS NULL
		 ORDER BY position ASC, id ASC`, goalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GoalMilestone{}
	for rows.Next() {
		var m GoalMilestone
		if err := rows.Scan(&m.ID, &m.Title, &m.Start, &m.Due, &m.Position, &m.Version); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		tasks, err := milestoneTasksTx(ctx, tx, goalID, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Tasks = tasks
	}
	return out, nil
}

func loadGoalTx(ctx context.Context, tx *sql.Tx, wsID, id int64) (*Goal, error) {
	g, err := scanGoal(tx.QueryRowContext(ctx,
		`SELECT `+goalCols+`
		 FROM goals WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL`, id, wsID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	g.Milestones, err = goalMilestonesTx(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	return g, nil
}

// ListGoals 返回工作区的全部目标（含里程碑与挂载任务），active 在前、
// 已达成/归档沉底，同状态按 position。进度不入库，读时由任务推导。
func (s *Store) ListGoals(ctx context.Context, wsID int64) ([]Goal, error) {
	var goals []Goal
	err := s.ExecTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx,
			`SELECT `+goalCols+`
			 FROM goals WHERE workspace_id = ? AND deleted_at IS NULL
			 ORDER BY CASE status WHEN 'active' THEN 0 ELSE 1 END, position ASC, id ASC`, wsID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			g, err := scanGoal(rows)
			if err != nil {
				return err
			}
			goals = append(goals, *g)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for i := range goals {
			ms, err := goalMilestonesTx(ctx, tx, goals[i].ID)
			if err != nil {
				return err
			}
			goals[i].Milestones = ms
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if goals == nil {
		goals = []Goal{}
	}
	return goals, nil
}

// CreateGoal 创建目标。新目标排在最前（最小 position）。
func (s *Store) CreateGoal(ctx context.Context, wsID, userID int64, in GoalInput) (*Goal, error) {
	now := formatTime(nowUTC())
	var created *Goal
	err := s.ExecTx(ctx, func(tx *sql.Tx) error {
		var minPos sql.NullInt64
		if err := tx.QueryRowContext(ctx,
			`SELECT MIN(position) FROM goals WHERE workspace_id = ? AND deleted_at IS NULL`, wsID).Scan(&minPos); err != nil {
			return err
		}
		pos := int64(0)
		if minPos.Valid {
			pos = minPos.Int64 - 1
		}
		res, err := tx.ExecContext(ctx,
			`INSERT INTO goals (workspace_id, created_by, title, goal_text, acceptance, failure_cost, group_name, mode, status, position, version, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, 'sequential', ?, ?, 1, ?, ?)`,
			wsID, userID, in.Title, in.GoalText, in.Acceptance, in.FailureCost, in.Group, in.Status, pos, now, now)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		g, err := loadGoalTx(ctx, tx, wsID, id)
		if err != nil {
			return err
		}
		created = g
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

// UpdateGoal 应用乐观并发保护的编辑。version 不匹配返回 ErrConflict
//（目标没有「携带最新目标返回」的约定，客户端收到 409 后重拉列表即可）。
func (s *Store) UpdateGoal(ctx context.Context, wsID, id int64, in GoalInput, version int64) (*Goal, error) {
	now := formatTime(nowUTC())
	var out *Goal
	err := s.ExecTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE goals SET title = ?, goal_text = ?, acceptance = ?, failure_cost = ?, group_name = ?, status = ?, version = version + 1, updated_at = ?
			 WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL AND version = ?`,
			in.Title, in.GoalText, in.Acceptance, in.FailureCost, in.Group, in.Status, now, id, wsID, version)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			// 区分「不存在」与「版本冲突」：存在但版本不对 → conflict。
			var exists int
			if err := tx.QueryRowContext(ctx,
				`SELECT COUNT(1) FROM goals WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL`, id, wsID).Scan(&exists); err != nil {
				return err
			}
			if exists == 0 {
				return ErrNotFound
			}
			return ErrConflict
		}
		g, err := loadGoalTx(ctx, tx, wsID, id)
		if err != nil {
			return err
		}
		out = g
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteGoal 软删目标；里程碑与挂载关系保留，撤销删除后完整恢复。
func (s *Store) DeleteGoal(ctx context.Context, wsID, id int64) error {
	now := formatTime(nowUTC())
	return s.ExecTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE goals SET deleted_at = ?, updated_at = ? WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL`,
			now, now, id, wsID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// RestoreGoal 撤销目标删除。
func (s *Store) RestoreGoal(ctx context.Context, wsID, id int64) (*Goal, error) {
	now := formatTime(nowUTC())
	var out *Goal
	err := s.ExecTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE goals SET deleted_at = NULL, version = version + 1, updated_at = ? WHERE id = ? AND workspace_id = ?`,
			now, id, wsID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		g, err := loadGoalTx(ctx, tx, wsID, id)
		if err != nil {
			return err
		}
		out = g
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// requireGoalTx 校验目标属于该工作区且未删除，否则 ErrNotFound。挂载类的
// 写操作先过这道关：不带归属校验直接写，跨工作区的 goalID 会「先写成功、
// 再在 loadGoalTx 报 NotFound」——返回 404 但副作用已经发生（IDOR）。
func requireGoalTx(ctx context.Context, tx *sql.Tx, wsID, goalID int64) error {
	var exists int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM goals WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL`, goalID, wsID).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return ErrNotFound
	}
	return nil
}

// requireMilestoneTx 校验里程碑属于该目标且未删除（里程碑操作第二道关，
// 防跨目标写 milestone_id：goalID 对了不代表 milestoneID 也是它的）。
func requireMilestoneTx(ctx context.Context, tx *sql.Tx, goalID, msID int64) error {
	var exists int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM goal_milestones WHERE id = ? AND goal_id = ? AND deleted_at IS NULL`, msID, goalID).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return ErrNotFound
	}
	return nil
}

// CreateGoalMilestone 在目标下新建里程碑。排在最后（串行解锁顺序 =
// 里程碑顺序）。start/due 为 YYYY-MM-DD 或 ""（未设），已由上层校验。
func (s *Store) CreateGoalMilestone(ctx context.Context, wsID, goalID int64, in MilestoneInput) (*Goal, error) {
	now := formatTime(nowUTC())
	var out *Goal
	err := s.ExecTx(ctx, func(tx *sql.Tx) error {
		if err := requireGoalTx(ctx, tx, wsID, goalID); err != nil {
			return err
		}
		var wsCol int64
		if err := tx.QueryRowContext(ctx, `SELECT workspace_id FROM goals WHERE id = ?`, goalID).Scan(&wsCol); err != nil {
			return err
		}
		var maxPos sql.NullInt64
		if err := tx.QueryRowContext(ctx,
			`SELECT MAX(position) FROM goal_milestones WHERE goal_id = ? AND deleted_at IS NULL`, goalID).Scan(&maxPos); err != nil {
			return err
		}
		pos := int64(0)
		if maxPos.Valid {
			pos = maxPos.Int64 + 1
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO goal_milestones (goal_id, workspace_id, title, start_date, due_date, position, version, created_at, updated_at)
			 VALUES (?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, 1, ?, ?)`,
			goalID, wsCol, in.Title, in.Start, in.Due, pos, now, now)
		if err != nil {
			return err
		}
		g, err := loadGoalTx(ctx, tx, wsID, goalID)
		if err != nil {
			return err
		}
		out = g
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateGoalMilestone 编辑里程碑标题与起止日期（乐观并发，version 不匹配
// 返回 ErrConflict；客户端 409 后重拉列表对齐，与目标同一约定）。
func (s *Store) UpdateGoalMilestone(ctx context.Context, wsID, goalID, msID int64, in MilestoneInput, version int64) (*Goal, error) {
	now := formatTime(nowUTC())
	var out *Goal
	err := s.ExecTx(ctx, func(tx *sql.Tx) error {
		if err := requireGoalTx(ctx, tx, wsID, goalID); err != nil {
			return err
		}
		if err := requireMilestoneTx(ctx, tx, goalID, msID); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx,
			`UPDATE goal_milestones SET title = ?, start_date = NULLIF(?, ''), due_date = NULLIF(?, ''), version = version + 1, updated_at = ?
			 WHERE id = ? AND version = ?`,
			in.Title, in.Start, in.Due, now, msID, version)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return ErrConflict
		}
		g, err := loadGoalTx(ctx, tx, wsID, goalID)
		if err != nil {
			return err
		}
		out = g
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteGoalMilestone 软删里程碑并解除其下挂载关系（goal_tasks 行删除，
// 任务回到普通清单、任务本身不动）。恢复里程碑只找回标题与起止日期。
func (s *Store) DeleteGoalMilestone(ctx context.Context, wsID, goalID, msID int64) (*Goal, error) {
	now := formatTime(nowUTC())
	var out *Goal
	err := s.ExecTx(ctx, func(tx *sql.Tx) error {
		if err := requireGoalTx(ctx, tx, wsID, goalID); err != nil {
			return err
		}
		if err := requireMilestoneTx(ctx, tx, goalID, msID); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx,
			`UPDATE goal_milestones SET deleted_at = ?, version = version + 1, updated_at = ? WHERE id = ? AND deleted_at IS NULL`,
			now, now, msID)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return ErrNotFound
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM goal_tasks WHERE milestone_id = ?`, msID); err != nil {
			return err
		}
		g, err := loadGoalTx(ctx, tx, wsID, goalID)
		if err != nil {
			return err
		}
		out = g
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// RestoreGoalMilestone 撤销里程碑删除（找回标题与起止日期；挂载关系在
// 删除时已解除，任务需重新挂入）。
func (s *Store) RestoreGoalMilestone(ctx context.Context, wsID, goalID, msID int64) (*Goal, error) {
	now := formatTime(nowUTC())
	var out *Goal
	err := s.ExecTx(ctx, func(tx *sql.Tx) error {
		if err := requireGoalTx(ctx, tx, wsID, goalID); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx,
			`UPDATE goal_milestones SET deleted_at = NULL, version = version + 1, updated_at = ?
			 WHERE id = ? AND goal_id = ? AND deleted_at IS NOT NULL`,
			now, msID, goalID)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return ErrNotFound
		}
		g, err := loadGoalTx(ctx, tx, wsID, goalID)
		if err != nil {
			return err
		}
		out = g
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// AttachGoalTask 把任务挂进目标下的某个里程碑。任务必须属于该工作区且
// 未删除，且未挂载到其他目标/里程碑（goal_tasks.task_id 唯一约束兜底，
// 冲突映射为 ErrConflict）。新挂载的任务排在里程碑内最后。
func (s *Store) AttachGoalTask(ctx context.Context, wsID, goalID, msID int64, taskID string) (*Goal, error) {
	now := formatTime(nowUTC())
	var out *Goal
	err := s.ExecTx(ctx, func(tx *sql.Tx) error {
		if err := requireGoalTx(ctx, tx, wsID, goalID); err != nil {
			return err
		}
		if err := requireMilestoneTx(ctx, tx, goalID, msID); err != nil {
			return err
		}
		var taskWs sql.NullInt64
		var taskDeleted sql.NullString
		if err := tx.QueryRowContext(ctx,
			`SELECT workspace_id, deleted_at FROM tasks WHERE id = ?`, taskID).
			Scan(&taskWs, &taskDeleted); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if !taskWs.Valid || taskWs.Int64 != wsID {
			return ErrNotFound // 跨工作区的任务当作不存在，不泄露其归属
		}
		if taskDeleted.Valid {
			return fmt.Errorf("%w: 任务已删除", ErrInvalid)
		}
		var maxPos sql.NullInt64
		if err := tx.QueryRowContext(ctx,
			`SELECT MAX(position) FROM goal_tasks WHERE milestone_id = ?`, msID).Scan(&maxPos); err != nil {
			return err
		}
		pos := int64(0)
		if maxPos.Valid {
			pos = maxPos.Int64 + 1
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO goal_tasks (goal_id, task_id, milestone_id, position, created_at) VALUES (?, ?, ?, ?, ?)`,
			goalID, taskID, msID, pos, now)
		if err != nil {
			if isUniqueViolation(err) {
				return ErrConflict // 已挂在别的目标/里程碑下
			}
			return err
		}
		g, err := loadGoalTx(ctx, tx, wsID, goalID)
		if err != nil {
			return err
		}
		out = g
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// DetachGoalTask 把任务从目标移回普通清单（任务本身不动）。
func (s *Store) DetachGoalTask(ctx context.Context, wsID, goalID int64, taskID string) (*Goal, error) {
	var out *Goal
	err := s.ExecTx(ctx, func(tx *sql.Tx) error {
		if err := requireGoalTx(ctx, tx, wsID, goalID); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx,
			`DELETE FROM goal_tasks WHERE goal_id = ? AND task_id = ?`, goalID, taskID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		g, err := loadGoalTx(ctx, tx, wsID, goalID)
		if err != nil {
			return err
		}
		out = g
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ReorderGoals 原子地重排工作区内全部未删除目标。展示顺序（active 在前、
// settled 沉底）由调用方决定，这里只如实落 position。ids 必须正好是当前
// 全部目标 id，数量不符拒绝（部分写入不能发生）。
func (s *Store) ReorderGoals(ctx context.Context, wsID int64, ids []int64) error {
	now := formatTime(nowUTC())
	return s.ExecTx(ctx, func(tx *sql.Tx) error {
		var count int64
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(1) FROM goals WHERE workspace_id = ? AND deleted_at IS NULL`, wsID).Scan(&count); err != nil {
			return err
		}
		if int(count) != len(ids) {
			return fmt.Errorf("%w: reorder expects %d ids, got %d", ErrInvalid, count, len(ids))
		}
		for pos, id := range ids {
			res, err := tx.ExecContext(ctx,
				`UPDATE goals SET position = ?, updated_at = ? WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL`,
				pos, now, id, wsID)
			if err != nil {
				return err
			}
			n, err := res.RowsAffected()
			if err != nil {
				return err
			}
			if n == 0 {
				return fmt.Errorf("%w: unknown goal %d", ErrNotFound, id)
			}
		}
		return nil
	})
}
