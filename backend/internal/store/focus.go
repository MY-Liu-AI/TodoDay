package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// FocusSession is one recorded pomodoro (or a partial, abandoned one).
type FocusSession struct {
	ID          int64  `json:"id"`
	TaskID      string `json:"taskId"`
	TaskTitle   string `json:"taskTitle"`
	PlannedSec  int    `json:"plannedSec"`
	DurationSec int    `json:"durationSec"`
	Status      string `json:"status"` // completed | abandoned
	StartedAt   int64  `json:"startedAt"` // Unix ms
	EndedAt     int64  `json:"endedAt"`   // Unix ms
}

// FocusInput carries validated fields for recording a session.
type FocusInput struct {
	TaskID      string
	PlannedSec  int
	DurationSec int
	Status      string
	StartedAt   time.Time
	EndedAt     time.Time
	ClientID    string
}

// FocusDay is the per-day aggregate: pomodoros counts only completed
// sessions; seconds sums every recorded session (abandoned time still counts
// as focus time, it just isn't a whole tomato).
type FocusDay struct {
	Pomodoros int `json:"pomodoros"`
	Seconds   int `json:"seconds"`
}

// FocusStats is the single read model for the focus page. Today/week/month
// are computed in the workspace timezone; week starts Monday to match the
// calendar; month is the calendar month.
type FocusStats struct {
	Timezone string              `json:"timezone"`
	Today    string              `json:"today"` // YYYY-MM-DD in workspace tz
	TodayAgg FocusDay            `json:"todayAgg"`
	Week     FocusDay            `json:"week"`
	Month    FocusDay            `json:"month"`
	Daily    map[string]FocusDay `json:"daily"`
	Sessions []FocusSession      `json:"sessions"` // today's sessions, newest first
}

// CreateFocusSession records one session. clientID makes it idempotent
// (a retry after a dropped response returns the original row, no duplicate).
// Completed sessions must have run at least their planned length.
func (s *Store) CreateFocusSession(ctx context.Context, wsID, userID int64, in FocusInput) (*FocusSession, error) {
	if in.PlannedSec < 60 || in.PlannedSec > 7200 {
		return nil, fmt.Errorf("%w: plannedSec 需在 60–7200 之间", ErrInvalid)
	}
	if in.DurationSec < 1 || in.DurationSec > 7200 {
		return nil, fmt.Errorf("%w: durationSec 需在 1–7200 之间", ErrInvalid)
	}
	switch in.Status {
	case "completed":
		if in.DurationSec < in.PlannedSec {
			return nil, fmt.Errorf("%w: 未满计划时长不能记为完成", ErrInvalid)
		}
	case "abandoned":
	default:
		return nil, fmt.Errorf("%w: status 无效", ErrInvalid)
	}
	if in.EndedAt.Before(in.StartedAt) {
		return nil, fmt.Errorf("%w: 结束时间早于开始时间", ErrInvalid)
	}
	if len(in.ClientID) > 64 {
		return nil, fmt.Errorf("%w: clientId 过长", ErrInvalid)
	}

	var out *FocusSession
	err := s.ExecTx(ctx, func(tx *sql.Tx) error {
		// Idempotent replay.
		if in.ClientID != "" {
			existing, err := focusByClientTx(ctx, tx, wsID, in.ClientID)
			if err == nil {
				out = existing
				return nil
			}
			if !errors.Is(err, ErrNotFound) {
				return err
			}
		}
		// Snapshot the task title so history survives renames and deletes.
		// The task must belong to this workspace (never trust a body id).
		title := ""
		var taskArg any
		if in.TaskID != "" {
			err := tx.QueryRowContext(ctx,
				`SELECT title FROM tasks WHERE id = ? AND workspace_id = ?`, in.TaskID, wsID).Scan(&title)
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			if err != nil {
				return err
			}
			taskArg = in.TaskID
		}
		var clientArg any
		if in.ClientID != "" {
			clientArg = in.ClientID
		}
		res, err := tx.ExecContext(ctx,
			`INSERT INTO focus_sessions
			   (workspace_id, user_id, task_id, task_title, planned_sec, duration_sec, status, started_at, ended_at, client_id, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			wsID, userID, taskArg, title, in.PlannedSec, in.DurationSec, in.Status,
			formatTime(in.StartedAt), formatTime(in.EndedAt), clientArg, formatTime(nowUTC()))
		if err != nil {
			if isUniqueViolation(err) && in.ClientID != "" {
				existing, err2 := focusByClientTx(ctx, tx, wsID, in.ClientID)
				if err2 != nil {
					return err2
				}
				out = existing
				return nil
			}
			return err
		}
		id, _ := res.LastInsertId()
		out = &FocusSession{
			ID: id, TaskID: in.TaskID, TaskTitle: title,
			PlannedSec: in.PlannedSec, DurationSec: in.DurationSec, Status: in.Status,
			StartedAt: in.StartedAt.UnixMilli(), EndedAt: in.EndedAt.UnixMilli(),
		}
		return nil
	})
	return out, err
}

const focusCols = `id, COALESCE(task_id, ''), task_title, planned_sec, duration_sec, status, started_at, ended_at`

func scanFocus(row interface{ Scan(...any) error }) (*FocusSession, error) {
	var f FocusSession
	var started, ended string
	err := row.Scan(&f.ID, &f.TaskID, &f.TaskTitle, &f.PlannedSec, &f.DurationSec, &f.Status, &started, &ended)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if t, err := time.ParseInLocation(timeLayout, started, time.UTC); err == nil {
		f.StartedAt = t.UnixMilli()
	}
	if t, err := time.ParseInLocation(timeLayout, ended, time.UTC); err == nil {
		f.EndedAt = t.UnixMilli()
	}
	return &f, nil
}

func focusByClientTx(ctx context.Context, tx *sql.Tx, wsID int64, clientID string) (*FocusSession, error) {
	return scanFocus(tx.QueryRowContext(ctx,
		`SELECT `+focusCols+` FROM focus_sessions WHERE workspace_id = ? AND client_id = ?`, wsID, clientID))
}

// FocusStats aggregates the last `days` days (1–366, default 182) in the
// workspace timezone. Attribution is by end time: a pomodoro counts on the
// day it finished.
func (s *Store) FocusStats(ctx context.Context, wsID int64, days int, timezone string) (*FocusStats, error) {
	if days < 1 || days > 366 {
		days = 182
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.UTC
	}
	now := nowUTC().In(loc)
	todayKey := now.Format("2006-01-02")
	// Cutoff: local midnight `days-1` days ago, with one extra day of slack
	// for the UTC→local conversion of rows near the boundary.
	cutoff := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -days)

	rows, err := s.db.QueryContext(ctx,
		`SELECT `+focusCols+` FROM focus_sessions
		 WHERE workspace_id = ? AND ended_at >= ?
		 ORDER BY ended_at DESC, id DESC`, wsID, formatTime(cutoff))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	st := &FocusStats{
		Timezone: timezone,
		Today:    todayKey,
		Daily:    map[string]FocusDay{},
		Sessions: []FocusSession{},
	}
	// Monday-start week and calendar month, both in workspace tz.
	weekday := (int(now.Weekday()) + 6) % 7
	weekStart := time.Date(now.Year(), now.Month(), now.Day()-weekday, 0, 0, 0, 0, loc).Format("2006-01-02")
	monthPrefix := now.Format("2006-01")

	for rows.Next() {
		f, err := scanFocus(rows)
		if err != nil {
			return nil, err
		}
		day := time.UnixMilli(f.EndedAt).In(loc).Format("2006-01-02")
		if day > todayKey {
			// Clock skew from a client; never attribute to the future.
			day = todayKey
		}
		d := st.Daily[day]
		d.Seconds += f.DurationSec
		if f.Status == "completed" {
			d.Pomodoros++
		}
		st.Daily[day] = d
		if day == todayKey {
			st.Sessions = append(st.Sessions, *f)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	st.TodayAgg = st.Daily[todayKey]
	for day, d := range st.Daily {
		if day >= weekStart && day <= todayKey {
			st.Week.Pomodoros += d.Pomodoros
			st.Week.Seconds += d.Seconds
		}
		if strings.HasPrefix(day, monthPrefix) {
			st.Month.Pomodoros += d.Pomodoros
			st.Month.Seconds += d.Seconds
		}
	}
	return st, nil
}

// SortedFocusDays is a helper for tests and diagnostics.
func SortedFocusDays(daily map[string]FocusDay) []string {
	keys := make([]string, 0, len(daily))
	for k := range daily {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
