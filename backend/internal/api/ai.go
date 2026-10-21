package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"tododay/internal/ai"
	"tododay/internal/store"
)

// AI 技能调用：Web 端按名直调注册表里的技能。
// 适配层约定：这里不出现按技能名的分支，一律经注册表 + 调用器。

// invokeSkill 处理 POST /api/workspaces/{wid}/ai/invoke，body: {"skill":"quadrant"}。
// 成员可用（产出是工作区内任务的分析视图，读权限与任务列表一致）；
// 模型调用有每工作区每日配额。
func (s *Server) invokeSkill(w http.ResponseWriter, r *http.Request, _ *store.User, wid int64) {
	var body struct {
		Skill string `json:"skill"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Skill == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "请求体需要 skill 字段")
		return
	}
	out, err := s.ai.Invoke(r.Context(), wid, body.Skill)
	if err != nil {
		s.writeAIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// listSkills 管理视图：列出已加载的技能（不含提示词）。
// 成员可看，便于前端展示「有哪些 AI 能力可用」。
func (s *Server) listSkills(w http.ResponseWriter, _ *http.Request, _ *store.User, _ int64) {
	writeJSON(w, http.StatusOK, map[string]any{"skills": s.ai.Skills()})
}

// reloadSkills 仅 owner：重新扫描技能目录。技能文件改完不用重启进程。
func (s *Server) reloadSkills(w http.ResponseWriter, r *http.Request, _ *store.User, _ int64) {
	if err := s.ai.ReloadSkills(); err != nil {
		writeError(w, http.StatusBadRequest, "reload_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"skills": s.ai.Skills()})
}

// writeAIError 把调用器的错误映射成诚实的 HTTP 状态，文案原样透出
// （调用器的错误都是写给用户看的）。未配置/超限分别给 409/429。
func (s *Server) writeAIError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ai.ErrNotConfigured):
		writeError(w, http.StatusConflict, "ai_not_configured", err.Error())
	case errors.Is(err, ai.ErrQuotaExceeded):
		writeError(w, http.StatusTooManyRequests, "ai_quota_exceeded", err.Error())
	default:
		// 技能不存在/未开放入口/模型失败/产出非法：统一 502，
		// 让前端如实提示「AI 分析失败」，绝不编造一份假结果。
		writeError(w, http.StatusBadGateway, "ai_invoke_failed", err.Error())
	}
}
