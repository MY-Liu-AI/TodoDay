package api

import (
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/crypto/bcrypt"

	"tododay/internal/store"
)

// loginLimiter is a small fixed-window rate limiter for auth endpoints.
type loginLimiter struct {
	mu    sync.Mutex
	hits  map[string][]time.Time
	limit int
	win   time.Duration
}

func newLoginLimiter(limit int, win time.Duration) *loginLimiter {
	return &loginLimiter{hits: map[string][]time.Time{}, limit: limit, win: win}
}

func (l *loginLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	cut := now.Add(-l.win)
	list := l.hits[key]
	kept := list[:0]
	for _, t := range list {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.limit {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)
	return true
}

var limiter = newLoginLimiter(10, time.Minute)

func clientIP(r *http.Request) string {
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return host
}

func cleanUsername(s string) (string, bool) {
	u := strings.TrimSpace(s)
	runes := []rune(u)
	if len(runes) < 2 || len(runes) > 32 {
		return "", false
	}
	for _, r := range runes {
		if unicode.IsControl(r) || r == ' ' {
			return "", false
		}
	}
	return u, true
}

func cleanPassword(s string) bool {
	l := len(s)
	return l >= 8 && l <= 72
}

type authRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.allow(clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "尝试过于频繁，请稍后再试")
		return
	}
	var in authRequest
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", "请求格式错误")
		return
	}
	username, ok := cleanUsername(in.Username)
	if !ok || !cleanPassword(in.Password) {
		writeError(w, http.StatusBadRequest, "validation_failed", "用户名需 2–32 个字符，密码至少 8 位")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
		return
	}
	u, err := s.st.CreateUser(r.Context(), username, string(hash))
	if err != nil {
		mapStoreError(w, err)
		return
	}
	// Every new account gets its own personal workspace (中文默认名).
	ws, err := s.st.CreateWorkspaceWithOwner(r.Context(), u.ID, "个人工作区", "Asia/Shanghai")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "初始化工作区失败")
		return
	}
	if err := s.startSession(w, r, u.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "创建会话失败")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"user": u, "workspace": ws,
	})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.allow(clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "尝试过于频繁，请稍后再试")
		return
	}
	var in authRequest
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", "请求格式错误")
		return
	}
	u, hash, err := s.st.UserByUsername(r.Context(), in.Username)
	if err != nil {
		// Same timing whether or not the account exists.
		_ = bcrypt.CompareHashAndPassword([]byte("$2a$10$7EqJtq98hPqEX7fNZaFWoOhi8BihRSE0WmE7sKpNYkFjXkr/ZQKqS"), []byte(in.Password))
		writeError(w, http.StatusUnauthorized, "unauthorized", "用户名或密码不正确")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)) != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "用户名或密码不正确")
		return
	}
	if err := s.startSession(w, r, u.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "创建会话失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": u})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = s.st.DeleteSession(r.Context(), c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	ws, err := s.st.WorkspacesForUser(r.Context(), u.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
		return
	}
	if ws == nil {
		ws = []store.Workspace{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": u, "workspaces": ws})
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, userID int64) error {
	token := newSessionToken()
	if err := s.st.CreateSession(r.Context(), token, userID, s.sessionTTL); err != nil {
		return err
	}
	s.setSessionCookie(w, token, s.sessionTTL)
	return nil
}
