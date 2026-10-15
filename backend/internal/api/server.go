package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tododay/internal/store"
)

const sessionCookie = "tododay_session"

type ctxKey string

const ctxUser ctxKey = "user"

type Server struct {
	st         *store.Store
	staticDir  string
	sessionTTL time.Duration
	limiter    *loginLimiter
	mux        *http.ServeMux
}

func NewServer(st *store.Store, staticDir string) *Server {
	s := &Server{
		st:         st,
		staticDir:  staticDir,
		sessionTTL: 7 * 24 * time.Hour,
		limiter:    newLoginLimiter(10, time.Minute),
		mux:        http.NewServeMux(),
	}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("POST /api/auth/register", s.register)
	s.mux.HandleFunc("POST /api/auth/login", s.login)
	s.mux.HandleFunc("POST /api/auth/logout", s.logout)
	s.mux.HandleFunc("GET /api/me", s.withAuth(s.me))

	s.mux.HandleFunc("GET /api/workspaces/{wid}/tasks", s.withAuth(s.withWorkspace(s.listTasks)))
	s.mux.HandleFunc("POST /api/workspaces/{wid}/tasks", s.withAuth(s.withWorkspace(s.createTask)))
	s.mux.HandleFunc("PATCH /api/workspaces/{wid}/tasks/{tid}", s.withAuth(s.withWorkspace(s.updateTask)))
	s.mux.HandleFunc("POST /api/workspaces/{wid}/tasks/{tid}/completion", s.withAuth(s.withWorkspace(s.setCompletion)))
	s.mux.HandleFunc("POST /api/workspaces/{wid}/tasks/reorder", s.withAuth(s.withWorkspace(s.reorderTasks)))
	s.mux.HandleFunc("POST /api/workspaces/{wid}/tasks/delete", s.withAuth(s.withWorkspace(s.deleteTasks)))
	s.mux.HandleFunc("POST /api/workspaces/{wid}/tasks/restore", s.withAuth(s.withWorkspace(s.restoreTasks)))
	s.mux.HandleFunc("POST /api/workspaces/{wid}/tasks/import", s.withAuth(s.withWorkspace(s.importTasks)))
	s.mux.HandleFunc("GET /api/workspaces/{wid}/activity", s.withAuth(s.withWorkspace(s.activity)))
}

// Handler wraps the mux with security and panic-recovery middleware.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic: %v", rec)
				writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
			}
		}()
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")

		// Mutating API calls must carry a non-simple header. Cross-site forms
		// cannot set it without a CORS preflight, which we never answer, so this
		// blocks CSRF even though the session cookie is SameSite=Lax.
		if strings.HasPrefix(r.URL.Path, "/api/") &&
			(r.Method == http.MethodPost || r.Method == http.MethodPatch || r.Method == http.MethodDelete) {
			if r.Header.Get("X-Requested-With") != "XMLHttpRequest" {
				writeError(w, http.StatusForbidden, "csrf", "缺少请求头")
				return
			}
		}

		if strings.HasPrefix(r.URL.Path, "/api/") {
			lw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()
			s.mux.ServeHTTP(lw, r)
			log.Printf("%s %s → %d (%s)", r.Method, r.URL.Path, lw.status, time.Since(start).Round(time.Millisecond))
			return
		}
		s.serveStatic(w, r)
	})
}

// statusWriter captures the status code for the access log.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// serveStatic serves the built Vue app with SPA fallback (excluding /api).
func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request) {
	if s.staticDir == "" {
		writeError(w, http.StatusNotFound, "not_found", "前端构建目录未配置")
		return
	}
	fs := http.FileServer(http.Dir(s.staticDir))
	// If a real file exists, serve it; otherwise fall back to index.html so
	// client-side routes resolve. /api never reaches here.
	p := filepath.Clean(strings.TrimPrefix(r.URL.Path, "/"))
	if p == "." {
		p = "index.html"
	}
	if _, err := os.Stat(filepath.Join(s.staticDir, p)); err != nil {
		// Rewrite to "/" rather than "/index.html": http.FileServer 301-redirects
		// any path ending in "/index.html" to "./", which would turn the SPA
		// fallback into a redirect loop for deep client-side routes.
		u := *r.URL
		u.Path = "/"
		r2 := r.Clone(r.Context())
		r2.URL = &u
		fs.ServeHTTP(w, r2)
		return
	}
	fs.ServeHTTP(w, r)
}

// withAuth resolves the session cookie into a store.User, rejecting with 401.
func (s *Server) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "请先登录")
			return
		}
		u, err := s.st.UserBySession(r.Context(), c.Value, s.sessionTTL)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "登录已过期，请重新登录")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxUser, u)))
	}
}

// withWorkspace authorizes the caller against the {wid} path value. Request
// bodies are never trusted for workspace identity.
func (s *Server) withWorkspace(next func(http.ResponseWriter, *http.Request, *store.User, int64)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := r.Context().Value(ctxUser).(*store.User)
		wid, err := parseWID(r)
		if err != nil || wid <= 0 {
			writeError(w, http.StatusBadRequest, "validation_failed", "无效的工作区 ID")
			return
		}
		if _, err := s.st.Membership(r.Context(), u.ID, wid); err != nil {
			mapStoreError(w, err)
			return
		}
		next(w, r, u, wid)
	}
}

func userFrom(r *http.Request) *store.User {
	return r.Context().Value(ctxUser).(*store.User)
}

func newSessionToken() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

func (s *Server) setSessionCookie(w http.ResponseWriter, token string, maxAge time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   os.Getenv("TODO_SECURE_COOKIES") == "1",
		Expires:  time.Now().Add(maxAge),
		MaxAge:   int(maxAge.Seconds()),
	})
}
