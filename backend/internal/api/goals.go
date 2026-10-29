package api

import (
	"net/http"
	"regexp"
	"strconv"

	"tododay/internal/store"
)

// parseGoalInput validates fields shared by create/update.
// 战略决策三问各限 1000 字符（原备注上限 500，问答式内容放宽一倍）。
const maxGoalStrategyLen = 1000

// 里程碑日期：YYYY-MM-DD 本地日历日期（与 tasks.due_date 同约定），
// 可空字符串 = 未设置；start 与 due 都有时 start 不得晚于 due。
var dateValueRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

type goalPayload struct {
	Title       string `json:"title"`
	GoalText    string `json:"goalText"`
	Acceptance  string `json:"acceptance"`
	FailureCost string `json:"failureCost"`
	Group       string `json:"group"`
	Status      string `json:"status"`
}

type milestonePayload struct {
	Title string `json:"title"`
	Start string `json:"start"`
	Due   string `json:"due"`
}

func parseMilestoneInput(w http.ResponseWriter, in milestonePayload) (store.MilestoneInput, bool) {
	title, ok := cleanTitle(in.Title)
	if !ok {
		writeError(w, http.StatusBadRequest, "validation_failed", "里程碑名称需 1–240 个字符")
		return store.MilestoneInput{}, false
	}
	for _, d := range []struct {
		val, name string
	}{
		{in.Start, "开始日期"}, {in.Due, "截止日期"},
	} {
		if d.val != "" && !dateValueRe.MatchString(d.val) {
			writeError(w, http.StatusBadRequest, "validation_failed", d.name+"格式应为 YYYY-MM-DD")
			return store.MilestoneInput{}, false
		}
	}
	if in.Start != "" && in.Due != "" && in.Start > in.Due {
		writeError(w, http.StatusBadRequest, "validation_failed", "开始日期不能晚于截止日期")
		return store.MilestoneInput{}, false
	}
	return store.MilestoneInput{Title: title, Start: in.Start, Due: in.Due}, true
}

func parseGoalInput(w http.ResponseWriter, in goalPayload) (store.GoalInput, bool) {
	title, ok := cleanTitle(in.Title)
	if !ok {
		writeError(w, http.StatusBadRequest, "validation_failed", "目标名称需 1–240 个字符")
		return store.GoalInput{}, false
	}
	for _, field := range []struct {
		val, name string
	}{
		{in.GoalText, "我的目标"}, {in.Acceptance, "验收标准"}, {in.FailureCost, "失败代价"},
	} {
		if len([]rune(field.val)) > maxGoalStrategyLen {
			writeError(w, http.StatusBadRequest, "validation_failed", field.name+"过长")
			return store.GoalInput{}, false
		}
	}
	group := in.Group
	if len([]rune(group)) > 40 {
		writeError(w, http.StatusBadRequest, "validation_failed", "分组标签过长")
		return store.GoalInput{}, false
	}
	status := in.Status
	if status != "active" && status != "done" && status != "archived" {
		status = "active"
	}
	return store.GoalInput{
		Title: title, GoalText: in.GoalText, Acceptance: in.Acceptance,
		FailureCost: in.FailureCost, Group: group, Status: status,
	}, true
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

func (s *Server) createGoalMilestone(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	gid, ok := parseGID(w, r)
	if !ok {
		return
	}
	var in milestonePayload
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", "请求格式错误")
		return
	}
	mi, ok := parseMilestoneInput(w, in)
	if !ok {
		return
	}
	g, err := s.st.CreateGoalMilestone(r.Context(), wid, gid, mi)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, g)
}

func (s *Server) updateGoalMilestone(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	gid, mid, ok := parseGIDMID(w, r)
	if !ok {
		return
	}
	var in struct {
		milestonePayload
		Version int64 `json:"version"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", "请求格式错误")
		return
	}
	mi, ok := parseMilestoneInput(w, in.milestonePayload)
	if !ok {
		return
	}
	g, err := s.st.UpdateGoalMilestone(r.Context(), wid, gid, mid, mi, in.Version)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, g)
}

func (s *Server) deleteGoalMilestone(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	gid, mid, ok := parseGIDMID(w, r)
	if !ok {
		return
	}
	g, err := s.st.DeleteGoalMilestone(r.Context(), wid, gid, mid)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, g)
}

func (s *Server) restoreGoalMilestone(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	gid, mid, ok := parseGIDMID(w, r)
	if !ok {
		return
	}
	g, err := s.st.RestoreGoalMilestone(r.Context(), wid, gid, mid)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, g)
}

func (s *Server) attachGoalTask(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	gid, mid, ok := parseGIDMID(w, r)
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
	g, err := s.st.AttachGoalTask(r.Context(), wid, gid, mid, in.TaskID)
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

// parseGIDMID 解析目标 + 里程碑两个路径参数；里程碑 id 不属于该目标时
// 由 store 层 requireMilestoneTx 按 NotFound 处理（不泄露跨目标资源存在性）。
func parseGIDMID(w http.ResponseWriter, r *http.Request) (int64, int64, bool) {
	gid, ok := parseGID(w, r)
	if !ok {
		return 0, 0, false
	}
	mid, err := strconv.ParseInt(r.PathValue("mid"), 10, 64)
	if err != nil || mid <= 0 {
		writeError(w, http.StatusBadRequest, "validation_failed", "无效的里程碑 ID")
		return 0, 0, false
	}
	return gid, mid, true
}
