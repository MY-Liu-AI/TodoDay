package api

import (
	"net/http"
	"strconv"
	"time"

	"tododay/internal/store"
)

const maxBatch = 500

// parseTaskInput validates the shared task fields carried by create/update/import.
func parseTaskInput(w http.ResponseWriter, in struct {
	T   string `json:"t"`
	P   int    `json:"p"`
	Due string `json:"due"`
	G   string `json:"g"`
}) (store.TaskInput, bool) {
	title, ok := cleanTitle(in.T)
	if !ok {
		writeError(w, http.StatusBadRequest, "validation_failed", "任务内容需 1–240 个字符")
		return store.TaskInput{}, false
	}
	group, ok := cleanGroup(in.G)
	if !ok {
		writeError(w, http.StatusBadRequest, "validation_failed", "分组名过长")
		return store.TaskInput{}, false
	}
	p := in.P
	if p < 1 || p > 3 {
		p = 3
	}
	due := in.Due
	if due != "" && !validDate(due) {
		writeError(w, http.StatusBadRequest, "validation_failed", "日期需为 YYYY-MM-DD")
		return store.TaskInput{}, false
	}
	return store.TaskInput{Title: title, Priority: p, Due: due, Group: group}, true
}

type taskPayload struct {
	T   string `json:"t"`
	P   int    `json:"p"`
	Due string `json:"due"`
	G   string `json:"g"`
}

func (s *Server) listTasks(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	pageSize, _ := strconv.Atoi(q.Get("pageSize"))
	tasks, total, err := s.st.ListTasks(r.Context(), wid, store.ListQuery{
		Query:    q.Get("q"),
		Group:    q.Get("group"),
		Status:   q.Get("status"),
		From:     q.Get("from"),
		To:       q.Get("to"),
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tasks": tasks, "total": total,
		"page": max(page, 1), "pageSize": pageSizeOr(pageSize),
	})
}

func pageSizeOr(n int) int {
	if n < 1 || n > 500 {
		return 500
	}
	return n
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (s *Server) createTask(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	var in struct {
		taskPayload
		ClientID string `json:"clientId"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", "请求格式错误")
		return
	}
	ti, ok := parseTaskInput(w, in.taskPayload)
	if !ok {
		return
	}
	if len(in.ClientID) > 64 {
		writeError(w, http.StatusBadRequest, "validation_failed", "clientId 过长")
		return
	}
	t, err := s.st.CreateTask(r.Context(), wid, u.ID, ti, in.ClientID)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	// Idempotent replays return the canonical task (same id, no duplicate).
	writeJSON(w, http.StatusCreated, t)
}

func (s *Server) updateTask(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	var in struct {
		taskPayload
		Version int64 `json:"version"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", "请求格式错误")
		return
	}
	ti, ok := parseTaskInput(w, in.taskPayload)
	if !ok {
		return
	}
	t, err := s.st.UpdateTask(r.Context(), wid, u.ID, r.PathValue("tid"), ti, in.Version)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) setCompletion(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	var in struct {
		Done    bool  `json:"done"`
		Version int64 `json:"version"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", "请求格式错误")
		return
	}
	t, err := s.st.SetCompletion(r.Context(), wid, u.ID, r.PathValue("tid"), in.Done, in.Version, time.Now())
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

type idsPayload struct {
	IDs []string `json:"ids"`
}

func readIDs(w http.ResponseWriter, r *http.Request) ([]string, bool) {
	var in idsPayload
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", "请求格式错误")
		return nil, false
	}
	if len(in.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "validation_failed", "ids 不能为空")
		return nil, false
	}
	if len(in.IDs) > maxBatch {
		writeError(w, http.StatusBadRequest, "validation_failed", "单次最多 500 项")
		return nil, false
	}
	return in.IDs, true
}

func (s *Server) reorderTasks(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	ids, ok := readIDs(w, r)
	if !ok {
		return
	}
	if err := s.st.ReorderTasks(r.Context(), wid, ids); err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) deleteTasks(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	ids, ok := readIDs(w, r)
	if !ok {
		return
	}
	n, err := s.st.DeleteTasks(r.Context(), wid, u.ID, ids)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": n, "restorable": true})
}

func (s *Server) restoreTasks(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	ids, ok := readIDs(w, r)
	if !ok {
		return
	}
	tasks, err := s.st.RestoreTasks(r.Context(), wid, u.ID, ids)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks})
}

func (s *Server) importTasks(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	var in struct {
		Items []struct {
			taskPayload
			Done bool  `json:"done"`
			At   int64 `json:"at"`
		} `json:"items"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", "请求格式错误")
		return
	}
	if len(in.Items) == 0 || len(in.Items) > maxBatch {
		writeError(w, http.StatusBadRequest, "validation_failed", "导入数量需在 1–500 之间")
		return
	}
	items := make([]store.TaskInput, 0, len(in.Items))
	for _, it := range in.Items {
		title, ok := cleanTitle(it.T)
		if !ok {
			writeError(w, http.StatusBadRequest, "validation_failed", "任务内容需 1–240 个字符")
			return
		}
		group, ok := cleanGroup(it.G)
		if !ok {
			writeError(w, http.StatusBadRequest, "validation_failed", "分组名过长")
			return
		}
		p := it.P
		if p < 1 || p > 3 {
			p = 3
		}
		due := it.Due
		if due != "" && !validDate(due) {
			writeError(w, http.StatusBadRequest, "validation_failed", "日期需为 YYYY-MM-DD")
			return
		}
		ti := store.TaskInput{Title: title, Priority: p, Due: due, Group: group, Done: it.Done}
		if it.Done && it.At > 0 {
			ti.At = time.UnixMilli(it.At).UTC()
		}
		items = append(items, ti)
	}
	imported, skipped, err := s.st.ImportTasks(r.Context(), wid, u.ID, items)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"imported": imported, "skipped": skipped})
}

func (s *Server) activity(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	weeks, _ := strconv.Atoi(r.URL.Query().Get("weeks"))
	tz, err := s.st.WorkspaceTimezone(r.Context(), wid)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	counts, err := s.st.ActivityCounts(r.Context(), wid, weeks, tz)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"timezone": tz, "counts": counts, "generatedAt": time.Now().UTC().Format(time.RFC3339),
	})
}
