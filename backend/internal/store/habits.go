package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// 「习惯」：无终点的定期练习（健身、阅读），独立于目标的一级实体。
// 习惯 = 标题 + 每周重复的星期（可多选）。服务端把习惯滚动展开成真实
// 任务实例（tasks.habit_id 回指，client_id 确定性生成、天然幂等），实例
// 出现在计划页/日历/今天清单、可勾选完成。
//
// 滚动窗口：工作区时区的今天起未来 8 周。今天的实例在首次读取时生成；
// 窗口随每天第一次访问自然前移。已过期未完成的实例保留（如实呈现逾期），
// 删除习惯只清掉「未来未完成」的实例，历史完成记录仍是真实任务。

// HabitRollDays 是实例滚动生成的前瞻天数（8 周）。
const HabitRollDays = 56

// HabitTask 是习惯的一次实例（真实任务的线表示）。
type HabitTask struct {
	TaskID  string `json:"taskId"`
	Title   string `json:"title"`
	Done    bool   `json:"done"`
	Due     string `json:"due"` // YYYY-MM-DD
	Version int64  `json:"version"`
}

// Habit 是习惯的线表示。Weekdays 为 [1..7]（1=周一 … 7=周日），
// 升序去重。Tasks 是本周一起的实例（习惯页本周打卡条用）。
type Habit struct {
	ID       int64       `json:"id"`
	Title    string      `json:"title"`
	Weekdays []int       `json:"weekdays"`
	Position int64       `json:"position"`
	Version  int64       `json:"version"`
	Tasks    []HabitTask `json:"tasks"`
}

// HabitInput carries validated fields for create/update.
type HabitInput struct {
	Title    string
	Weekdays []int
}

// maskOf 把 [1..7]（周一=1 … 周日=7）折成位掩码 bit(weekday-1)。
func maskOf(days []int) int64 {
	var m int64
	for _, d := range days {
		m |= 1 << (d - 1)
	}
	return m
}

// daysOfMask 把位掩码展开成升序 [1..7]。
func daysOfMask(mask int64) []int {
	out := []int{}
	for d := 1; d <= 7; d++ {
		if mask&(1<<(d-1)) != 0 {
			out = append(out, d)
		}
	}
	return out
}

// weekdayBit 返回 date 在工作区时区位掩码里的 bit 位（周一=0 … 周日=6）。
func weekdayBit(date time.Time) int {
	return (int(date.Weekday()) + 6) % 7
}

// workspaceTodayTx 读工作区时区并返回「今天」的日历日期（YYYY-MM-DD），
// 与 focus.go / events.go 的口径一致：按工作区时区聚合，不做 UTC 换算。
func workspaceTodayTx(ctx context.Context, tx *sql.Tx, wsID int64) (time.Time, *time.Location, error) {
	var tz string
	if err := tx.QueryRowContext(ctx, `SELECT timezone FROM workspaces WHERE id = ?`, wsID).Scan(&tz); err != nil {
		return time.Time{}, nil, err
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	now := nowUTC().In(loc)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc), loc, nil
}

func habitClientID(hid int64, date string) string {
	return fmt.Sprintf("habit-%d-%s", hid, date)
}

// ensureHabitOccurrencesTx 为习惯补齐 [today, today+HabitRollDays) 内
// 命中星期的实例任务。client_id 确定性生成，重复执行/并发重试都只写一次
// （UNIQUE(workspace_id, client_id) 兜底，命中即跳过）。
func ensureHabitOccurrencesTx(ctx context.Context, tx *sql.Tx, wsID, userID, hid int64, title string, mask int64, today time.Time) error {
	now := nowUTC()
	nowStr := formatTime(now)
	for d := 0; d < HabitRollDays; d++ {
		day := today.AddDate(0, 0, d)
		if mask&(1<<weekdayBit(day)) == 0 {
			continue
		}
		dateStr := day.Format("2006-01-02")
		var minPos sql.NullInt64
		if err := tx.QueryRowContext(ctx,
			`SELECT MIN(position) FROM tasks WHERE workspace_id = ? AND deleted_at IS NULL`, wsID).Scan(&minPos); err != nil {
			return err
		}
		pos := int64(0)
		if minPos.Valid {
			pos = minPos.Int64 - 1
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO tasks (id, workspace_id, created_by, title, priority, due_date, position, client_id, habit_id, version, created_at, updated_at)
			 VALUES (?, ?, ?, ?, 3, ?, ?, ?, ?, 1, ?, ?)`,
			newTaskID(), wsID, userID, title, dateStr, pos, habitClientID(hid, dateStr), hid, nowStr, nowStr)
		if err != nil {
			if isUniqueViolation(err) {
				continue // 该日实例已存在（幂等重放）
			}
			return err
		}
	}
	return nil
}

// habitTasksTx 读习惯的实例任务：本周一起（覆盖习惯页本周打卡条）且未删除。
func habitTasksTx(ctx context.Context, tx *sql.Tx, hid int64, today time.Time) ([]HabitTask, error) {
	weekStart := today.AddDate(0, 0, -weekdayBit(today)).Format("2006-01-02")
	rows, err := tx.QueryContext(ctx,
		`SELECT id, title, completed_at IS NOT NULL, due_date, version
		 FROM tasks t
		 WHERE t.habit_id = ? AND t.deleted_at IS NULL AND t.due_date >= ?
		 ORDER BY t.due_date ASC, t.created_at ASC`, hid, weekStart)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HabitTask{}
	for rows.Next() {
		var ht HabitTask
		if err := rows.Scan(&ht.TaskID, &ht.Title, &ht.Done, &ht.Due, &ht.Version); err != nil {
			return nil, err
		}
		out = append(out, ht)
	}
	return out, rows.Err()
}

// loadHabitTx 读单个习惯（含本周起的实例任务）。
func loadHabitTx(ctx context.Context, tx *sql.Tx, wsID, hid int64, today time.Time) (*Habit, error) {
	var h Habit
	var mask int64
	err := tx.QueryRowContext(ctx,
		`SELECT id, title, weekday_mask, position, version
		 FROM habits WHERE id = ? AND workspace_id = ?`, hid, wsID).
		Scan(&h.ID, &h.Title, &mask, &h.Position, &h.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	h.Weekdays = daysOfMask(mask)
	h.Tasks, err = habitTasksTx(ctx, tx, hid, today)
	if err != nil {
		return nil, err
	}
	return &h, nil
}

// requireHabitTx 校验习惯属于该工作区，否则 ErrNotFound（不泄露跨工作区
// 资源的存在性，与目标层同一道 IDOR 防线）。
func requireHabitTx(ctx context.Context, tx *sql.Tx, wsID, hid int64) error {
	var exists int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM habits WHERE id = ? AND workspace_id = ?`, hid, wsID).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return ErrNotFound
	}
	return nil
}

// ListHabits 返回工作区的全部习惯（含实例任务）。读取时先滚动补齐未来
// 实例——服务端是唯一展开入口，任何客户端首次打开习惯页都会把今天到
// 未来 8 周的实例落库。
func (s *Store) ListHabits(ctx context.Context, wsID, userID int64) ([]Habit, error) {
	var out []Habit
	err := s.ExecTx(ctx, func(tx *sql.Tx) error {
		today, _, err := workspaceTodayTx(ctx, tx, wsID)
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx,
			`SELECT id, title, weekday_mask, position, version FROM habits
			 WHERE workspace_id = ? ORDER BY position ASC, id ASC`, wsID)
		if err != nil {
			return err
		}
		type rawHabit struct {
			id       int64
			title    string
			mask     int64
			position int64
			version  int64
		}
		raw := []rawHabit{}
		for rows.Next() {
			var rh rawHabit
			if err := rows.Scan(&rh.id, &rh.title, &rh.mask, &rh.position, &rh.version); err != nil {
				rows.Close()
				return err
			}
			raw = append(raw, rh)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, rh := range raw {
			if err := ensureHabitOccurrencesTx(ctx, tx, wsID, userID, rh.id, rh.title, rh.mask, today); err != nil {
				return err
			}
		}
		out = []Habit{}
		for _, rh := range raw {
			h, err := loadHabitTx(ctx, tx, wsID, rh.id, today)
			if err != nil {
				return err
			}
			out = append(out, *h)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// CreateHabit 新建习惯并立即补齐未来实例。clientID 幂等：同 clientID
// 重放返回已创建的习惯，不产生重复。
func (s *Store) CreateHabit(ctx context.Context, wsID, userID int64, in HabitInput, clientID string) (*Habit, error) {
	var created *Habit
	err := s.ExecTx(ctx, func(tx *sql.Tx) error {
		today, _, err := workspaceTodayTx(ctx, tx, wsID)
		if err != nil {
			return err
		}
		now := formatTime(nowUTC())
		var maxPos sql.NullInt64
		if err := tx.QueryRowContext(ctx,
			`SELECT MAX(position) FROM habits WHERE workspace_id = ?`, wsID).Scan(&maxPos); err != nil {
			return err
		}
		pos := int64(0)
		if maxPos.Valid {
			pos = maxPos.Int64 + 1
		}
		mask := maskOf(in.Weekdays)
		res, err := tx.ExecContext(ctx,
			`INSERT INTO habits (workspace_id, title, weekday_mask, position, version, client_id, created_at, updated_at)
			 VALUES (?, ?, ?, ?, 1, NULLIF(?, ''), ?, ?)`,
			wsID, in.Title, mask, pos, clientID, now, now)
		if err != nil {
			if isUniqueViolation(err) && clientID != "" {
				var hid int64
				if err := tx.QueryRowContext(ctx,
					`SELECT id FROM habits WHERE workspace_id = ? AND client_id = ?`, wsID, clientID).Scan(&hid); err == nil {
					h, lerr := loadHabitTx(ctx, tx, wsID, hid, today)
					if lerr != nil {
						return lerr
					}
					created = h
					return nil
				}
			}
			return err
		}
		hid, err := res.LastInsertId()
		if err != nil {
			return err
		}
		if err := ensureHabitOccurrencesTx(ctx, tx, wsID, userID, hid, in.Title, mask, today); err != nil {
			return err
		}
		h, err := loadHabitTx(ctx, tx, wsID, hid, today)
		if err != nil {
			return err
		}
		created = h
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

// UpdateHabit 编辑习惯标题与重复星期（乐观并发，version 不匹配返回
// ErrConflict）。未来未完成的实例同步：标题跟着改；被取消的星期删掉；
// 新增的星期由 ensure 补齐（已存在的日期因确定性 client_id 不受影响）。
func (s *Store) UpdateHabit(ctx context.Context, wsID, userID, hid int64, in HabitInput, version int64) (*Habit, error) {
	var out *Habit
	err := s.ExecTx(ctx, func(tx *sql.Tx) error {
		if err := requireHabitTx(ctx, tx, wsID, hid); err != nil {
			return err
		}
		today, _, err := workspaceTodayTx(ctx, tx, wsID)
		if err != nil {
			return err
		}
		now := formatTime(nowUTC())
		todayStr := today.Format("2006-01-02")
		mask := maskOf(in.Weekdays)
		res, err := tx.ExecContext(ctx,
			`UPDATE habits SET title = ?, weekday_mask = ?, version = version + 1, updated_at = ?
			 WHERE id = ? AND version = ?`,
			in.Title, mask, now, hid, version)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return ErrConflict
		}
		// 未来未完成实例：标题跟随习惯；被取消星期的整批删除。
		if _, err := tx.ExecContext(ctx,
			`UPDATE tasks SET title = ?, updated_at = ? WHERE habit_id = ? AND due_date >= ? AND completed_at IS NULL AND deleted_at IS NULL`,
			in.Title, now, hid, todayStr); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx,
			`SELECT due_date FROM tasks WHERE habit_id = ? AND due_date >= ? AND completed_at IS NULL AND deleted_at IS NULL`,
			hid, todayStr)
		if err != nil {
			return err
		}
		var drop []string
		for rows.Next() {
			var due string
			if err := rows.Scan(&due); err != nil {
				rows.Close()
				return err
			}
			d, err := time.ParseInLocation("2006-01-02", due, today.Location())
			if err != nil {
				rows.Close()
				return err
			}
			if mask&(1<<weekdayBit(d)) == 0 {
				drop = append(drop, due)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, due := range drop {
			if _, err := tx.ExecContext(ctx,
				`DELETE FROM tasks WHERE habit_id = ? AND due_date = ? AND completed_at IS NULL AND deleted_at IS NULL`,
				hid, due); err != nil {
				return err
			}
		}
		if err := ensureHabitOccurrencesTx(ctx, tx, wsID, userID, hid, in.Title, mask, today); err != nil {
			return err
		}
		h, err := loadHabitTx(ctx, tx, wsID, hid, today)
		if err != nil {
			return err
		}
		out = h
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteHabits 硬删除习惯并清掉其「未来未完成」实例（due >= 今天且未
// 完成）。历史实例（已完成或已过期）保留为普通任务——完成记录是真实
// 数据，habit_id 由外键置空。返回删除的习惯数。
func (s *Store) DeleteHabits(ctx context.Context, wsID int64, ids []int64) (int, error) {
	deleted := 0
	err := s.ExecTx(ctx, func(tx *sql.Tx) error {
		today, _, err := workspaceTodayTx(ctx, tx, wsID)
		if err != nil {
			return err
		}
		todayStr := today.Format("2006-01-02")
		for _, hid := range ids {
			if err := requireHabitTx(ctx, tx, wsID, hid); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx,
				`DELETE FROM tasks WHERE habit_id = ? AND due_date >= ? AND completed_at IS NULL AND deleted_at IS NULL`,
				hid, todayStr); err != nil {
				return err
			}
			res, err := tx.ExecContext(ctx,
				`DELETE FROM habits WHERE id = ? AND workspace_id = ?`, hid, wsID)
			if err != nil {
				return err
			}
			n, err := res.RowsAffected()
			if err != nil {
				return err
			}
			deleted += int(n)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return deleted, nil
}
