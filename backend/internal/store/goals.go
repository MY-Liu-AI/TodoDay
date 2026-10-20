package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Goal 是目标的线表示。Tasks 携带关联任务（进度由它推导，不落库）：
// 已完成任务权重之和 / 全部权重 = 进度百分比；sequential 模式按 position
// 依次解锁（纯视图推导，服务端不强制顺序执行）。
type Goal struct {
	ID        int64      `json:"id"`
	Title     string     `json:"title"`
	Note      string     `json:"note"`
	Group     string     `json:"group"` // 分组标签，"" = 未分组（与任务分组同名同色）
	Mode      string     `json:"mode"`  // parallel | sequential
	Due       string     `json:"due"`   // YYYY-MM-DD 或 ""
	Status    string     `json:"status"`
	Position  int64      `json:"position"`
	Version   int64      `json:"version"`
	CreatedAt string     `json:"createdAt"` // YYYY-MM-DD（健康度的时间起点）
	Tasks     []GoalTask `json:"tasks"`
}

// GoalTask 是目标下的一条挂载任务（任务的视图快照字段实时联表读取，
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
type GoalInput struct {
	Title  string
	Note   string
	Group  string // 分组标签，"" = 未分组
	Mode   string
	Due    string // "" 无排期
	Status string // active | done | archived
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

// goalTasksTx loads the live tasks attached to a goal. 软删的任务不计入：
// 它们只是「暂时不在」，撤销删除后归属和进度自动恢复。
func goalTasksTx(ctx context.Context, tx *sql.Tx, goalID int64) ([]GoalTask, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT `+goalTaskCols+` FROM goal_tasks gt JOIN tasks t ON t.id = gt.task_id
		 WHERE gt.goal_id = ? AND t.deleted_at IS NULL ORDER BY gt.position ASC, gt.created_at ASC`, goalID)
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

func loadGoalTx(ctx context.Context, tx *sql.Tx, wsID, id int64) (*Goal, error) {
	var g Goal
	var due sql.NullString
	err := tx.QueryRowContext(ctx,
		`SELECT id, title, note, group_name, mode, due, status, position, version, substr(created_at, 1, 10)
		 FROM goals WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL`, id, wsID).
		Scan(&g.ID, &g.Title, &g.Note, &g.Group, &g.Mode, &due, &g.Status, &g.Position, &g.Version, &g.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if due.Valid {
		g.Due = due.String
	}
	g.Tasks, err = goalTasksTx(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	return &g, nil
}

// ListGoals 返回工作区的全部目标（含挂载任务），active 在前、已达成/归档
// 沉底，同状态按 position。进度不入库，读时由 Tasks 推导。
func (s *Store) ListGoals(ctx context.Context, wsID int64) ([]Goal, error) {
	var goals []Goal
	err := s.ExecTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx,
			`SELECT id, title, note, group_name, mode, due, status, position, version, substr(created_at, 1, 10)
			 FROM goals WHERE workspace_id = ? AND deleted_at IS NULL
			 ORDER BY CASE status WHEN 'active' THEN 0 ELSE 1 END, position ASC, id ASC`, wsID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var g Goal
			var due sql.NullString
			if err := rows.Scan(&g.ID, &g.Title, &g.Note, &g.Group, &g.Mode, &due, &g.Status, &g.Position, &g.Version, &g.CreatedAt); err != nil {
				return err
			}
			if due.Valid {
				g.Due = due.String
			}
			goals = append(goals, g)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for i := range goals {
			tasks, err := goalTasksTx(ctx, tx, goals[i].ID)
			if err != nil {
				return err
			}
			goals[i].Tasks = tasks
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
		var due any
		if in.Due != "" {
			due = in.Due
		}
		res, err := tx.ExecContext(ctx,
			`INSERT INTO goals (workspace_id, created_by, title, note, group_name, mode, due, status, position, version, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)`,
			wsID, userID, in.Title, in.Note, in.Group, in.Mode, due, in.Status, pos, now, now)
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
		var due any
		if in.Due != "" {
			due = in.Due
		}
		res, err := tx.ExecContext(ctx,
			`UPDATE goals SET title = ?, note = ?, group_name = ?, mode = ?, due = ?, status = ?, version = version + 1, updated_at = ?
			 WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL AND version = ?`,
			in.Title, in.Note, in.Group, in.Mode, due, in.Status, now, id, wsID, version)
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

// DeleteGoal 软删目标；挂载关系保留，撤销删除后完整恢复。
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

// AttachGoalTask 把任务挂进目标。任务必须属于该工作区且未删除，且未挂载
// 到其他目标（goal_tasks.task_id 唯一约束兜底，冲突映射为 ErrConflict）。
// 新挂载的任务排在最后（解锁顺序 = 挂载顺序）。
func (s *Store) AttachGoalTask(ctx context.Context, wsID, goalID int64, taskID string) (*Goal, error) {
	now := formatTime(nowUTC())
	var out *Goal
	err := s.ExecTx(ctx, func(tx *sql.Tx) error {
		if err := requireGoalTx(ctx, tx, wsID, goalID); err != nil {
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
			`SELECT MAX(position) FROM goal_tasks WHERE goal_id = ?`, goalID).Scan(&maxPos); err != nil {
			return err
		}
		pos := int64(0)
		if maxPos.Valid {
			pos = maxPos.Int64 + 1
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO goal_tasks (goal_id, task_id, position, created_at) VALUES (?, ?, ?, ?)`,
			goalID, taskID, pos, now)
		if err != nil {
			if isUniqueViolation(err) {
				return ErrConflict // 已挂在别的目标下
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

// ReorderGoalTasks 原子地重排目标内任务顺序（串行模式的解锁顺序）。
// ids 必须正好是当前挂载的全部任务 id。
func (s *Store) ReorderGoalTasks(ctx context.Context, wsID, goalID int64, ids []string) (*Goal, error) {
	var out *Goal
	err := s.ExecTx(ctx, func(tx *sql.Tx) error {
		if err := requireGoalTx(ctx, tx, wsID, goalID); err != nil {
			return err
		}
		var count int64
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(1) FROM goal_tasks WHERE goal_id = ?`, goalID).Scan(&count); err != nil {
			return err
		}
		if int(count) != len(ids) {
			return fmt.Errorf("%w: reorder expects %d ids, got %d", ErrInvalid, count, len(ids))
		}
		for pos, id := range ids {
			res, err := tx.ExecContext(ctx,
				`UPDATE goal_tasks SET position = ? WHERE goal_id = ? AND task_id = ?`,
				pos, goalID, id)
			if err != nil {
				return err
			}
			n, err := res.RowsAffected()
			if err != nil {
				return err
			}
			if n == 0 {
				return fmt.Errorf("%w: unknown task %q", ErrNotFound, id)
			}
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
