package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Task is the wire representation sent to the Vue client.
// Due is a YYYY-MM-DD calendar date in the client's timezone semantics
// (no midnight-UTC serialization, which shifts the day across timezones).
type Task struct {
	ID       string `json:"id"`
	T        string `json:"t"`
	P        int    `json:"p"`
	Due      string `json:"due"` // "YYYY-MM-DD" or ""
	G        string `json:"g"`
	Done     bool   `json:"done"`
	At       int64  `json:"at"` // completion time, Unix ms; 0 when not done
	Version  int64  `json:"version"`
	Position int64  `json:"position"`
}

// TaskInput carries validated fields for create/update/import.
type TaskInput struct {
	Title    string
	Priority int
	Due      string // "" means no date
	Group    string
	Done     bool
	At       time.Time // completion time for Done tasks (import)
}

// ConflictError carries the current server task so the client can reconcile.
type ConflictError struct {
	Current *Task
}

func (e *ConflictError) Error() string { return ErrConflict.Error() }

func newTaskID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:])
	return "t_" + strings.ToLower(enc)
}

// ensureGroup returns the group id, creating the group if needed.
func ensureGroup(ctx context.Context, tx *sql.Tx, wsID int64, name string) (int64, error) {
	now := formatTime(nowUTC())
	_, err := tx.ExecContext(ctx,
		`INSERT INTO task_groups (workspace_id, name, position, created_at, updated_at)
		 VALUES (?, ?, 0, ?, ?) ON CONFLICT (workspace_id, name) DO NOTHING`, wsID, name, now, now)
	if err != nil {
		return 0, err
	}
	var id int64
	err = tx.QueryRowContext(ctx,
		`SELECT id FROM task_groups WHERE workspace_id = ? AND name = ?`, wsID, name).Scan(&id)
	return id, err
}

type scanTask struct {
	id        string
	title     string
	priority  int
	due       sql.NullString
	groupName string
	completed sql.NullString
	position  int64
	version   int64
}

const taskCols = `t.id, t.title, t.priority, t.due_date,
	COALESCE((SELECT g.name FROM task_groups g WHERE g.id = t.group_id), ''),
	t.completed_at, t.position, t.version`

func scanOneTask(row interface{ Scan(...any) error }) (*scanTask, error) {
	var st scanTask
	err := row.Scan(&st.id, &st.title, &st.priority, &st.due, &st.groupName, &st.completed, &st.position, &st.version)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &st, nil
}

func (st *scanTask) toTask() *Task {
	t := &Task{
		ID:       st.id,
		T:        st.title,
		P:        st.priority,
		G:        st.groupName,
		Position: st.position,
		Version:  st.version,
	}
	if st.due.Valid {
		t.Due = st.due.String
	}
	if st.completed.Valid && st.completed.String != "" {
		if tp, err := time.ParseInLocation(timeLayout, st.completed.String, time.UTC); err == nil {
			t.Done = true
			t.At = tp.UnixMilli()
		}
	}
	return t
}

func (s *Store) taskByID(ctx context.Context, wsID int64, id string, includeDeleted bool) (*Task, error) {
	q := `SELECT ` + taskCols + ` FROM tasks t WHERE t.id = ? AND t.workspace_id = ?`
	if !includeDeleted {
		q += ` AND t.deleted_at IS NULL`
	}
	st, err := scanOneTask(s.db.QueryRowContext(ctx, q, id, wsID))
	if err != nil {
		return nil, err
	}
	return st.toTask(), nil
}

// ListQuery filters for ListTasks.
type ListQuery struct {
	Query    string // substring of title/group (case-insensitive)
	Group    string // exact group name
	Status   string // "all" | "open" | "done"
	From     string // due_date >= From (YYYY-MM-DD)
	To       string // due_date <= To
	Page     int    // 1-based
	PageSize int
}

// ListTasks returns tasks (never deleted) ordered by manual position.
func (s *Store) ListTasks(ctx context.Context, wsID int64, q ListQuery) ([]Task, int64, error) {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.PageSize < 1 || q.PageSize > 500 {
		q.PageSize = 500
	}
	where := []string{`t.workspace_id = ?`, `t.deleted_at IS NULL`}
	args := []any{wsID}
	if q.Query != "" {
		where = append(where, `(LOWER(t.title) LIKE ? OR LOWER(COALESCE((SELECT g.name FROM task_groups g WHERE g.id = t.group_id), '')) LIKE ?)`)
		like := "%" + strings.ToLower(q.Query) + "%"
		args = append(args, like, like)
	}
	if q.Group != "" {
		where = append(where, `COALESCE((SELECT g.name FROM task_groups g WHERE g.id = t.group_id), '') = ?`)
		args = append(args, q.Group)
	}
	if q.Status == "open" {
		where = append(where, `t.completed_at IS NULL`)
	} else if q.Status == "done" {
		where = append(where, `t.completed_at IS NOT NULL`)
	}
	if q.From != "" {
		where = append(where, `t.due_date >= ?`)
		args = append(args, q.From)
	}
	if q.To != "" {
		where = append(where, `t.due_date <= ?`)
		args = append(args, q.To)
	}
	whereSQL := strings.Join(where, " AND ")

	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM tasks t WHERE `+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT `+taskCols+` FROM tasks t WHERE `+whereSQL+` ORDER BY t.position ASC, t.created_at ASC LIMIT ? OFFSET ?`,
		append(append([]any{}, args...), q.PageSize, (q.Page-1)*q.PageSize)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Task{}
	for rows.Next() {
		st, err := scanOneTask(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *st.toTask())
	}
	return out, total, rows.Err()
}

func insertEvent(ctx context.Context, tx *sql.Tx, taskID string, wsID, userID int64, typ string, at time.Time) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO task_events (task_id, workspace_id, user_id, type, occurred_at) VALUES (?, ?, ?, ?, ?)`,
		taskID, wsID, userID, typ, formatTime(at.UTC()))
	return err
}

// CreateTask inserts a task. clientID provides idempotency: retrying the same
// clientID returns the original task instead of creating a duplicate.
func (s *Store) CreateTask(ctx context.Context, wsID, userID int64, in TaskInput, clientID string) (*Task, error) {
	id := newTaskID()
	now := nowUTC()
	var created *Task
	err := s.ExecTx(ctx, func(tx *sql.Tx) error {
		groupID, err := ensureGroup(ctx, tx, wsID, in.Group)
		if err != nil {
			return err
		}
		// New tasks go to the top of the manual order (lowest position wins).
		var minPos sql.NullInt64
		if err := tx.QueryRowContext(ctx,
			`SELECT MIN(position) FROM tasks WHERE workspace_id = ? AND deleted_at IS NULL`, wsID).Scan(&minPos); err != nil {
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
		var completed any
		if in.Done {
			completed = formatTime(in.At.UTC())
		}
		// Empty client ids are stored as NULL so the (workspace_id, client_id)
		// unique index never fires for plain creates.
		var clientArg any
		if clientID != "" {
			clientArg = clientID
		}
		_, err = tx.ExecContext(ctx,
			`INSERT INTO tasks (id, workspace_id, created_by, group_id, title, priority, due_date, position, completed_at, client_id, version, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)`,
			id, wsID, userID, groupID, in.Title, in.Priority, due, pos, completed, clientArg, formatTime(now), formatTime(now))
		if err != nil {
			if isUniqueViolation(err) && clientID != "" {
				// Idempotent replay: return the existing task.
				t, gerr := s.taskByIDTx(ctx, tx, wsID, clientID)
				if gerr == nil {
					created = t
					return nil
				}
				return err
			}
			return err
		}
		if err := insertEvent(ctx, tx, id, wsID, userID, "created", now); err != nil {
			return err
		}
		if in.Done {
			if err := insertEvent(ctx, tx, id, wsID, userID, "completed", in.At); err != nil {
				return err
			}
		}
		t, err := s.taskByIDTx(ctx, tx, wsID, id)
		if err != nil {
			return err
		}
		created = t
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

func (s *Store) taskByIDTx(ctx context.Context, tx *sql.Tx, wsID int64, idOrClient string) (*Task, error) {
	st, err := scanOneTask(tx.QueryRowContext(ctx,
		`SELECT `+taskCols+` FROM tasks t WHERE t.workspace_id = ? AND (t.id = ? OR (t.client_id = ? AND t.client_id != '')) AND t.deleted_at IS NULL`,
		wsID, idOrClient, idOrClient))
	if err != nil {
		return nil, err
	}
	return st.toTask(), nil
}

// UpdateTask applies an optimistic-concurrency guarded edit. version must
// equal the current row version, otherwise a *ConflictError with the current
// task is returned.
func (s *Store) UpdateTask(ctx context.Context, wsID, userID int64, id string, in TaskInput, version int64) (*Task, error) {
	now := nowUTC()
	var out *Task
	err := s.ExecTx(ctx, func(tx *sql.Tx) error {
		groupID, err := ensureGroup(ctx, tx, wsID, in.Group)
		if err != nil {
			return err
		}
		var due any
		if in.Due != "" {
			due = in.Due
		}
		res, err := tx.ExecContext(ctx,
			`UPDATE tasks SET title = ?, priority = ?, due_date = ?, group_id = ?, version = version + 1, updated_at = ?
			 WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL AND version = ?`,
			in.Title, in.Priority, due, groupID, formatTime(now), id, wsID, version)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			cur, cerr := s.taskByIDTx(ctx, tx, wsID, id)
			if cerr != nil {
				return cerr
			}
			return &ConflictError{Current: cur}
		}
		if err := insertEvent(ctx, tx, id, wsID, userID, "updated", now); err != nil {
			return err
		}
		t, err := s.taskByIDTx(ctx, tx, wsID, id)
		if err != nil {
			return err
		}
		out = t
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// SetCompletion marks a task done/undone, writing task_events. completedAt is
// ignored when marking undone (completed_at becomes NULL).
func (s *Store) SetCompletion(ctx context.Context, wsID, userID int64, id string, done bool, version int64, completedAt time.Time) (*Task, error) {
	now := nowUTC()
	if completedAt.IsZero() {
		completedAt = now
	}
	var out *Task
	err := s.ExecTx(ctx, func(tx *sql.Tx) error {
		var completed any
		if done {
			completed = formatTime(completedAt.UTC())
		}
		res, err := tx.ExecContext(ctx,
			`UPDATE tasks SET completed_at = ?, version = version + 1, updated_at = ?
			 WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL AND version = ?`,
			completed, formatTime(now), id, wsID, version)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			cur, cerr := s.taskByIDTx(ctx, tx, wsID, id)
			if cerr != nil {
				return cerr
			}
			return &ConflictError{Current: cur}
		}
		typ := "reopened"
		eventAt := now
		if done {
			typ = "completed"
			eventAt = completedAt.UTC()
		}
		if err := insertEvent(ctx, tx, id, wsID, userID, typ, eventAt); err != nil {
			return err
		}
		t, err := s.taskByIDTx(ctx, tx, wsID, id)
		if err != nil {
			return err
		}
		out = t
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ReorderTasks persists an explicit manual order atomically. ids must contain
// every visible (non-deleted) task id; positions become their slice indexes.
func (s *Store) ReorderTasks(ctx context.Context, wsID int64, ids []string) error {
	return s.ExecTx(ctx, func(tx *sql.Tx) error {
		var count int64
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(1) FROM tasks WHERE workspace_id = ? AND deleted_at IS NULL`, wsID).Scan(&count); err != nil {
			return err
		}
		if int(count) != len(ids) {
			return fmt.Errorf("%w: reorder expects %d ids, got %d", ErrInvalid, count, len(ids))
		}
		now := formatTime(nowUTC())
		for i, id := range ids {
			res, err := tx.ExecContext(ctx,
				`UPDATE tasks SET position = ?, updated_at = ? WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL`,
				int64(i), now, id, wsID)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return fmt.Errorf("%w: unknown task %q", ErrNotFound, id)
			}
		}
		return nil
	})
}

// DeleteTasks soft-deletes in one transaction and records a deleted event per
// task. Events are retained (see FULLSTACK.md), so stats history survives.
func (s *Store) DeleteTasks(ctx context.Context, wsID, userID int64, ids []string) (int, error) {
	now := nowUTC()
	deleted := 0
	err := s.ExecTx(ctx, func(tx *sql.Tx) error {
		for _, id := range ids {
			res, err := tx.ExecContext(ctx,
				`UPDATE tasks SET deleted_at = ?, version = version + 1, updated_at = ?
				 WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL`,
				formatTime(now), formatTime(now), id, wsID)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n > 0 {
				deleted++
				if err := insertEvent(ctx, tx, id, wsID, userID, "deleted", now); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return deleted, err
}

// RestoreTasks un-deletes tasks, putting them back at the top of the manual
// order (mirrors the client's restore-to-front behaviour).
func (s *Store) RestoreTasks(ctx context.Context, wsID, userID int64, ids []string) ([]Task, error) {
	now := nowUTC()
	restored := []Task{}
	err := s.ExecTx(ctx, func(tx *sql.Tx) error {
		var minPos sql.NullInt64
		if err := tx.QueryRowContext(ctx,
			`SELECT MIN(position) FROM tasks WHERE workspace_id = ? AND deleted_at IS NULL`, wsID).Scan(&minPos); err != nil {
			return err
		}
		pos := int64(0)
		if minPos.Valid {
			pos = minPos.Int64
		}
		for i, id := range ids {
			res, err := tx.ExecContext(ctx,
				`UPDATE tasks SET deleted_at = NULL, position = ?, version = version + 1, updated_at = ?
				 WHERE id = ? AND workspace_id = ? AND deleted_at IS NOT NULL`,
				pos-int64(i+1), formatTime(now), id, wsID)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n > 0 {
				if err := insertEvent(ctx, tx, id, wsID, userID, "restored", now); err != nil {
					return err
				}
				t, err := s.taskByIDTx(ctx, tx, wsID, id)
				if err != nil {
					return err
				}
				restored = append(restored, *t)
			}
		}
		return nil
	})
	return restored, err
}

// ImportTasks bulk-creates validated tasks in one transaction, skipping exact
// duplicates already present (same title+group+due among visible tasks) or
// duplicated inside the payload. Returns (imported, skipped).
func (s *Store) ImportTasks(ctx context.Context, wsID, userID int64, items []TaskInput) (int, int, error) {
	now := nowUTC()
	imported, skipped := 0, 0
	err := s.ExecTx(ctx, func(tx *sql.Tx) error {
		seen := map[string]bool{}
		for _, in := range items {
			key := in.Title + "\x00" + in.Group + "\x00" + in.Due
			if seen[key] {
				skipped++
				continue
			}
			seen[key] = true
			var dup int
			if err := tx.QueryRowContext(ctx,
				`SELECT COUNT(1) FROM tasks t WHERE t.workspace_id = ? AND t.deleted_at IS NULL
				 AND t.title = ? AND t.due_date IS ? AND COALESCE((SELECT g.name FROM task_groups g WHERE g.id = t.group_id), '') = ?`,
				wsID, in.Title, nullableDue(in.Due), in.Group).Scan(&dup); err != nil {
				return err
			}
			if dup > 0 {
				skipped++
				continue
			}
			groupID, err := ensureGroup(ctx, tx, wsID, in.Group)
			if err != nil {
				return err
			}
			var minPos sql.NullInt64
			if err := tx.QueryRowContext(ctx,
				`SELECT MIN(position) FROM tasks WHERE workspace_id = ? AND deleted_at IS NULL`, wsID).Scan(&minPos); err != nil {
				return err
			}
			pos := int64(0)
			if minPos.Valid {
				pos = minPos.Int64 - 1
			}
			id := newTaskID()
			var due any
			if in.Due != "" {
				due = in.Due
			}
			var completed any
			at := now
			if in.Done {
				if !in.At.IsZero() {
					at = in.At.UTC()
				}
				completed = formatTime(at)
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO tasks (id, workspace_id, created_by, group_id, title, priority, due_date, position, completed_at, version, created_at, updated_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)`,
				id, wsID, userID, groupID, in.Title, in.Priority, due, pos, completed, formatTime(now), formatTime(now)); err != nil {
				return err
			}
			if err := insertEvent(ctx, tx, id, wsID, userID, "created", now); err != nil {
				return err
			}
			if in.Done {
				if err := insertEvent(ctx, tx, id, wsID, userID, "completed", at); err != nil {
					return err
				}
			}
			imported++
		}
		return nil
	})
	return imported, skipped, err
}

func nullableDue(due string) any {
	if due == "" {
		return nil
	}
	return due
}

// GroupNames 返回工作区的全部分组名（按现有排序），供助手规划时把
// 「已有分组」注入提示词——模型不知道有哪些分组，就只能瞎猜类型。
func (s *Store) GroupNames(ctx context.Context, wsID int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT name FROM task_groups WHERE workspace_id = ? ORDER BY position, name`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}
