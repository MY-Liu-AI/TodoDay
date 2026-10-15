package store

import (
	"context"
	"sort"
	"time"
)

// ActivityCounts returns completion counts keyed by YYYY-MM-DD in the
// workspace timezone. Semantics (single source of truth for heat/streak/
// 7-day stats): a task contributes +1 on the day of its LATEST completed
// event, only while it is still completed. Reopening removes it; completing
// again re-attributes it to the new day. Deleted tasks keep their events, so
// clearing done does not rewrite history.
func (s *Store) ActivityCounts(ctx context.Context, wsID int64, weeks int, timezone string) (map[string]int, error) {
	if weeks < 1 || weeks > 53 {
		weeks = 18
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.UTC
	}
	// Fetch everything in the window plus one day of slack for tz conversion.
	now := nowUTC()
	from := now.In(loc).AddDate(0, 0, -(weeks*7 + 1))
	rows, err := s.db.QueryContext(ctx,
		`SELECT task_id, type, occurred_at FROM task_events
		 WHERE workspace_id = ? AND type IN ('completed','reopened') AND occurred_at >= ?
		 ORDER BY occurred_at ASC, id ASC`, wsID, formatTime(from))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type ev struct {
		typ string
		day string
		at  time.Time
	}
	events := map[string][]ev{}
	var order []string
	for rows.Next() {
		var taskID, typ, atStr string
		if err := rows.Scan(&taskID, &typ, &atStr); err != nil {
			return nil, err
		}
		at, err := time.ParseInLocation(timeLayout, atStr, time.UTC)
		if err != nil {
			continue
		}
		if _, ok := events[taskID]; !ok {
			order = append(order, taskID)
		}
		events[taskID] = append(events[taskID], ev{typ, at.In(loc).Format("2006-01-02"), at})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	counts := map[string]int{}
	// Iterate in event order per task; last completed day wins while done.
	for _, taskID := range order {
		done := false
		day := ""
		for _, e := range events[taskID] {
			if e.typ == "completed" {
				done, day = true, e.day
			} else {
				done = false
			}
		}
		if done && day != "" {
			counts[day]++
		}
	}
	return counts, nil
}

// SortedActivityDates is a helper for tests and diagnostics.
func SortedActivityDates(counts map[string]int) []string {
	dates := make([]string, 0, len(counts))
	for d := range counts {
		dates = append(dates, d)
	}
	sort.Strings(dates)
	return dates
}
