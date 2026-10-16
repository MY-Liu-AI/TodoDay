package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"tododay/internal/store"
)

type errorBody struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, errorBody{Error: code, Message: msg})
}

func decodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

// decodeOptionalJSON 与 decodeJSON 相同，但允许空请求体：没有 body（或 body 为空）时
// dst 保持零值、不报错。用于「可带可不带表单值的探测请求」——测试连接端点既可能
// 带上当前表单值，也可能为了沿用已保存值而发空 body。
func decodeOptionalJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// mapStoreError converts store sentinel errors to HTTP responses.
func mapStoreError(w http.ResponseWriter, err error) {
	var ce *store.ConflictError
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "资源不存在")
	case errors.Is(err, store.ErrNotMember):
		writeError(w, http.StatusForbidden, "forbidden", "无权访问该工作区")
	// 助手配置的冲突不带 task 字段：它不是任务，写出去会把客户端引偏。
	case errors.Is(err, store.ErrConfigConflict):
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "conflict", "message": "配置已被其他地方修改，请刷新后重试",
		})
	case errors.As(err, &ce):
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "conflict", "message": "数据已被其他设备修改", "task": ce.Current,
		})
	case errors.Is(err, store.ErrUsernameTaken):
		writeError(w, http.StatusConflict, "username_taken", "用户名已被占用")
	case errors.Is(err, store.ErrInvalid):
		writeError(w, http.StatusBadRequest, "validation_failed", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
	}
}

// parseWID extracts and validates the {wid} path value.
func parseWID(r *http.Request) (int64, error) {
	return strconv.ParseInt(r.PathValue("wid"), 10, 64)
}

// validDate reports whether s is a real YYYY-MM-DD calendar date.
func validDate(s string) bool {
	if len(s) != 10 || s[4] != '-' || s[7] != '-' {
		return false
	}
	t, err := time.ParseInLocation("2006-01-02", s, time.UTC)
	return err == nil && t.Format("2006-01-02") == s
}

func cleanTitle(s string) (string, bool) {
	t := strings.TrimSpace(s)
	if t == "" || len([]rune(t)) > 240 {
		return "", false
	}
	return t, true
}

func cleanGroup(s string) (string, bool) {
	g := strings.TrimSpace(s)
	if g == "" {
		g = "生活"
	}
	if len([]rune(g)) > 40 {
		return "", false
	}
	return g, true
}
