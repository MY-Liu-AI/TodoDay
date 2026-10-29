package api

import (
	"net/http"
	"strconv"

	"tododay/internal/store"
)

// 习惯：工作区级的定期练习（健身、阅读），独立于目标。习惯 = 标题 +
// 每周重复的星期（1=周一 … 7=周日，可多选）。服务端滚动展开成真实任务
// 实例（见 store/habits.go），这里只做校验与路由。成员级，不设 owner 边界。

type habitPayload struct {
	Title    string `json:"title"`
	Weekdays []int  `json:"weekdays"`
	ClientID string `json:"clientId"`
}

// parseHabitInput 校验习惯输入：标题 1–240 字符；星期为 1..7 的不重复
// 集合，至少选一天。
func parseHabitInput(w http.ResponseWriter, in habitPayload) (store.HabitInput, bool) {
	title, ok := cleanTitle(in.Title)
	if !ok {
		writeError(w, http.StatusBadRequest, "validation_failed", "习惯名称需 1–240 个字符")
		return store.HabitInput{}, false
	}
	seen := map[int]bool{}
	days := []int{}
	for _, d := range in.Weekdays {
		if d < 1 || d > 7 {
			writeError(w, http.StatusBadRequest, "validation_failed", "星期取值应为 1–7（1=周一）")
			return store.HabitInput{}, false
		}
		if !seen[d] {
			seen[d] = true
			days = append(days, d)
		}
	}
	if len(days) == 0 {
		writeError(w, http.StatusBadRequest, "validation_failed", "至少选择一个星期")
		return store.HabitInput{}, false
	}
	// 冒泡升序（集合小，免引 sort）。
	for i := 0; i < len(days); i++ {
		for j := i + 1; j < len(days); j++ {
			if days[j] < days[i] {
				days[i], days[j] = days[j], days[i]
			}
		}
	}
	return store.HabitInput{Title: title, Weekdays: days}, true
}

func (s *Server) listHabits(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	habits, err := s.st.ListHabits(r.Context(), wid, u.ID)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"habits": habits})
}

func (s *Server) createHabit(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	var in habitPayload
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", "请求格式错误")
		return
	}
	hi, ok := parseHabitInput(w, in)
	if !ok {
		return
	}
	h, err := s.st.CreateHabit(r.Context(), wid, u.ID, hi, in.ClientID)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, h)
}

func (s *Server) updateHabit(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
	hid, ok := parseHID(w, r)
	if !ok {
		return
	}
	var in struct {
		habitPayload
		Version int64 `json:"version"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", "请求格式错误")
		return
	}
	hi, ok := parseHabitInput(w, in.habitPayload)
	if !ok {
		return
	}
	h, err := s.st.UpdateHabit(r.Context(), wid, u.ID, hid, hi, in.Version)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h)
}

func (s *Server) deleteHabits(w http.ResponseWriter, r *http.Request, u *store.User, wid int64) {
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
	n, err := s.st.DeleteHabits(r.Context(), wid, in.IDs)
	if err != nil {
		mapStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": n})
}

// parseHID 解析习惯路径参数；习惯不属于该工作区时由 store 层
// requireHabitTx 按 NotFound 处理（不泄露跨工作区资源存在性）。
func parseHID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	hid, err := strconv.ParseInt(r.PathValue("hid"), 10, 64)
	if err != nil || hid <= 0 {
		writeError(w, http.StatusBadRequest, "validation_failed", "无效的习惯 ID")
		return 0, false
	}
	return hid, true
}
