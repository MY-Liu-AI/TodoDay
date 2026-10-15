package api

import (
	"net/http"
	"strconv"
	"time"

	"tododay/internal/store"
)

// createFocusSession records a finished (or abandoned) pomodoro. Timestamps
// come from the client in Unix ms; the server clamps obviously bad values and
// enforces that "completed" really ran the planned length.
func (s *Server) createFocusSession(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	var in struct {
		TaskID      string `json:"taskId"`
		PlannedSec  int    `json:"plannedSec"`
		DurationSec int    `json:"durationSec"`
		Status      string `json:"status"`
		StartedAt   int64  `json:"startedAt"`
		EndedAt     int64  `json:"endedAt"`
		ClientID    string `json:"clientId"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", "请求格式错误")
		return
	}
	if in.PlannedSec == 0 {
		in.PlannedSec = 25 * 60
	}
	now := time.Now()
	ended := now
	if in.EndedAt > 0 {
		ended = time.UnixMilli(in.EndedAt)
		// A client clock far in the future would land the session on a day
		// that hasn't happened; cap at server time (+1 min tolerance).
		if ended.After(now.Add(time.Minute)) {
			ended = now
		}
	}
	started := ended.Add(-time.Duration(in.DurationSec) * time.Second)
	if in.StartedAt > 0 {
		if t := time.UnixMilli(in.StartedAt); !t.After(ended) {
			started = t
		}
	}
	f, err := s.st.CreateFocusSession(r.Context(), wid, u.ID, store.FocusInput{
		TaskID: in.TaskID, PlannedSec: in.PlannedSec, DurationSec: in.DurationSec,
		Status: in.Status, StartedAt: started, EndedAt: ended, ClientID: in.ClientID,
	})
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, f)
}

func (s *Server) focusStats(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	tz, err := s.st.WorkspaceTimezone(r.Context(), wid)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	st, err := s.st.FocusStats(r.Context(), wid, days, tz)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}
