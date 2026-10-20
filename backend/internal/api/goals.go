package api

import (
	"net/http"
	"strconv"

	"tododay/internal/store"
)

// parseGoalInput validates fields shared by create/update.
func parseGoalInput(w http.ResponseWriter, in struct {
	Title  string `json:"title"`
	Note   string `json:"note"`
	Group  string `json:"group"`
	Mode   string `json:"mode"`
	Due    string `json:"due"`
	Status string `json:"status"`
}) (store.GoalInput, bool) {
	title, ok := cleanTitle(in.Title)
	if !ok {
		writeError(w, http.StatusBadRequest, "validation_failed", "目标名称需 1–240 个字符")
		return store.GoalInput{}, false
	}
	note := in.Note
	if len([]rune(note)) > 500 {
		writeError(w, http.StatusBadRequest, "validation_failed", "备注过长")
		return store.GoalInput{}, false
	}
	group := in.Group
	if len([]rune(group)) > 40 {
		writeError(w, http.StatusBadRequest, "validation_failed", "分组标签过长")
		return store.GoalInput{}, false
	}
	mode := in.Mode
	if mode != "parallel" && mode != "sequential" {
		mode = "parallel"
	}
	status := in.Status
	if status != "active" && status != "done" && status != "archived" {
		status = "active"
	}
	due := in.Due
	if due != "" && !validDate(due) {
		writeError(w, http.StatusBadRequest, "validation_failed", "日期需为 YYYY-MM-DD")
		return store.GoalInput{}, false
	}
	return store.GoalInput{Title: title, Note: note, Group: group, Mode: mode, Due: due, Status: status}, true
}

type goalPayload struct {
	Title  string `json:"title"`
	Note   string `json:"note"`
	Group  string `json:"group"`
	Mode   string `json:"mode"`
	Due    string `json:"due"`
	Status string `json:"status"`
}

func (s *Server) listGoals(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	goals, err := s.st.ListGoals(r.Context(), wid)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"goals": goals})
}

func (s *Server) createGoal(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	var in goalPayload
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", "请求格式错误")
		return
	}
	gi, ok := parseGoalInput(w, in)
	if !ok {
		return
	}
	g, err := s.st.CreateGoal(r.Context(), wid, u.ID, gi)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, g)
}

func (s *Server) updateGoal(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	var in struct {
		goalPayload
		Version int64 `json:"version"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", "请求格式错误")
		return
	}
	gi, ok := parseGoalInput(w, in.goalPayload)
	if !ok {
		return
	}
	gid, err := strconv.ParseInt(r.PathValue("gid"), 10, 64)
	if err != nil || gid <= 0 {
		writeError(w, http.StatusBadRequest, "validation_failed", "无效的目标 ID")
		return
	}
	g, err := s.st.UpdateGoal(r.Context(), wid, gid, gi, in.Version)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, g)
}

func (s *Server) deleteGoal(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	gid, ok := parseGID(w, r)
	if !ok {
		return
	}
	if err := s.st.DeleteGoal(r.Context(), wid, gid); err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": gid, "restorable": true})
}

func (s *Server) restoreGoal(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	gid, ok := parseGID(w, r)
	if !ok {
		return
	}
	g, err := s.st.RestoreGoal(r.Context(), wid, gid)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, g)
}

func (s *Server) attachGoalTask(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	gid, ok := parseGID(w, r)
	if !ok {
		return
	}
	var in struct {
		TaskID string `json:"taskId"`
	}
	if err := decodeJSON(r, &in); err != nil || in.TaskID == "" {
		writeError(w, http.StatusBadRequest, "validation_failed", "请求格式错误")
		return
	}
	g, err := s.st.AttachGoalTask(r.Context(), wid, gid, in.TaskID)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, g)
}

func (s *Server) detachGoalTask(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	gid, ok := parseGID(w, r)
	if !ok {
		return
	}
	var in struct {
		TaskID string `json:"taskId"`
	}
	if err := decodeJSON(r, &in); err != nil || in.TaskID == "" {
		writeError(w, http.StatusBadRequest, "validation_failed", "请求格式错误")
		return
	}
	g, err := s.st.DetachGoalTask(r.Context(), wid, gid, in.TaskID)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, g)
}

func (s *Server) reorderGoalTasks(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	gid, ok := parseGID(w, r)
	if !ok {
		return
	}
	ids, ok := readIDs(w, r)
	if !ok {
		return
	}
	g, err := s.st.ReorderGoalTasks(r.Context(), wid, gid, ids)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, g)
}

func (s *Server) reorderGoals(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	var in struct {
		IDs []int64 `json:"ids"`
	}
	if err := decodeJSON(r, &in); err != nil || len(in.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "validation_failed", "请求格式错误")
		return
	}
	if len(in.IDs) > maxBatch {
		writeError(w, http.StatusBadRequest, "validation_failed", "单次最多 500 项")
		return
	}
	if err := s.st.ReorderGoals(r.Context(), wid, in.IDs); err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func parseGID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	gid, err := strconv.ParseInt(r.PathValue("gid"), 10, 64)
	if err != nil || gid <= 0 {
		writeError(w, http.StatusBadRequest, "validation_failed", "无效的目标 ID")
		return 0, false
	}
	return gid, true
}
