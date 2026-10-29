package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tododay/internal/ai"
	"tododay/internal/assistant"
	"tododay/internal/mailimport"
	"tododay/internal/secretbox"
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
	// box 负责助手凭据的加解密。为 nil 表示未配置主密钥：
	// 读取掩码视图仍可用，但保存密钥会被拒绝。
	box *secretbox.Box
	// assistant 内嵌运行助手连接（per-workspace）。测试/启动/停止都走它。
	assistant *assistant.Manager
	// mail 是邮箱账单导入（per-workspace 每日轮询）。凭据（邮箱地址 +
	// IMAP 授权码）以密文落库，这里只持有运行循环。
	mail *mailimport.Manager
	// ai 是技能调用器：技能定义从文件目录加载（LoadSkills），不在代码里写死。
	ai *ai.Invoker
}

func NewServer(st *store.Store, staticDir string) *Server {
	// 主密钥缺失不阻止启动：其余功能不该被牵连。这里拿到的 nil 会让
	// 保存密钥的请求被明确拒绝，而不是悄悄退化成明文存储。
	box, err := secretbox.Open()
	if err != nil && !errors.Is(err, secretbox.ErrNoKey) {
		log.Printf("读取 %s 失败：%v", secretbox.EnvKey, err)
	}
	s := &Server{
		st:         st,
		staticDir:  staticDir,
		sessionTTL: 7 * 24 * time.Hour,
		limiter:    newLoginLimiter(10, time.Minute),
		mux:        http.NewServeMux(),
		box:        box,
		assistant:  assistant.NewManager(st, box),
		mail:       mailimport.NewManager(st, box),
		ai:         ai.NewInvoker(st, box),
	}
	s.routes()
	return s
}

// LoadSkills 从 dir 加载 AI 技能定义（每个子目录一个技能）。
// 目录缺失不阻止启动：技能系统降级为「未启用」，调用如实报错。
func (s *Server) LoadSkills(dir string) error {
	reg, err := ai.LoadRegistry(dir)
	if err != nil {
		return err
	}
	s.ai.SetRegistry(reg)
	log.Printf("ai: 已加载 %d 个技能（目录 %s）", len(reg.List()), dir)
	return nil
}

// SyncAssistants 在进程启动后恢复所有「已启用」工作区的助手连接。
func (s *Server) SyncAssistants(ctx context.Context) {
	s.assistant.SyncAll(ctx)
	s.mail.SyncAll(ctx)
}

// CloseAssistants 停止全部助手连接，供进程优雅退出时调用。
func (s *Server) CloseAssistants() {
	s.assistant.Close()
	s.mail.Close()
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

	// 番茄专注：记录一次专注、读取今日/本周/本月/每日统计。
	s.mux.HandleFunc("POST /api/workspaces/{wid}/focus/sessions", s.withAuth(s.withWorkspace(s.createFocusSession)))
	s.mux.HandleFunc("GET /api/workspaces/{wid}/focus/stats", s.withAuth(s.withWorkspace(s.focusStats)))

	// 目标：有终点的产出，统一拆解为 目标 → 里程碑 → 任务 三层，
	// 进度由挂载任务的完成情况推导（不落库）。
	s.mux.HandleFunc("GET /api/workspaces/{wid}/goals", s.withAuth(s.withWorkspace(s.listGoals)))
	s.mux.HandleFunc("POST /api/workspaces/{wid}/goals", s.withAuth(s.withWorkspace(s.createGoal)))
	s.mux.HandleFunc("PATCH /api/workspaces/{wid}/goals/{gid}", s.withAuth(s.withWorkspace(s.updateGoal)))
	s.mux.HandleFunc("POST /api/workspaces/{wid}/goals/reorder", s.withAuth(s.withWorkspace(s.reorderGoals)))
	s.mux.HandleFunc("POST /api/workspaces/{wid}/goals/{gid}/delete", s.withAuth(s.withWorkspace(s.deleteGoal)))
	s.mux.HandleFunc("POST /api/workspaces/{wid}/goals/{gid}/restore", s.withAuth(s.withWorkspace(s.restoreGoal)))
	s.mux.HandleFunc("POST /api/workspaces/{wid}/goals/{gid}/milestones", s.withAuth(s.withWorkspace(s.createGoalMilestone)))
	s.mux.HandleFunc("PATCH /api/workspaces/{wid}/goals/{gid}/milestones/{mid}", s.withAuth(s.withWorkspace(s.updateGoalMilestone)))
	s.mux.HandleFunc("POST /api/workspaces/{wid}/goals/{gid}/milestones/{mid}/delete", s.withAuth(s.withWorkspace(s.deleteGoalMilestone)))
	s.mux.HandleFunc("POST /api/workspaces/{wid}/goals/{gid}/milestones/{mid}/restore", s.withAuth(s.withWorkspace(s.restoreGoalMilestone)))
	s.mux.HandleFunc("POST /api/workspaces/{wid}/goals/{gid}/milestones/{mid}/tasks", s.withAuth(s.withWorkspace(s.attachGoalTask)))
	s.mux.HandleFunc("POST /api/workspaces/{wid}/goals/{gid}/tasks/remove", s.withAuth(s.withWorkspace(s.detachGoalTask)))

	// 习惯：工作区级的定期练习（健身、阅读），独立于目标。服务端滚动
	// 展开成真实任务实例（未来 8 周），实例出现在计划页/日历/今天清单。
	s.mux.HandleFunc("GET /api/workspaces/{wid}/habits", s.withAuth(s.withWorkspace(s.listHabits)))
	s.mux.HandleFunc("POST /api/workspaces/{wid}/habits", s.withAuth(s.withWorkspace(s.createHabit)))
	s.mux.HandleFunc("PATCH /api/workspaces/{wid}/habits/{hid}", s.withAuth(s.withWorkspace(s.updateHabit)))
	s.mux.HandleFunc("POST /api/workspaces/{wid}/habits/delete", s.withAuth(s.withWorkspace(s.deleteHabits)))

	// 记账本：每月收支流水。成员级（账目是个人数据，不设 owner 边界）；
	// client_id 幂等创建，version 乐观并发，软删 + 恢复（撤销删除）。
	s.mux.HandleFunc("GET /api/workspaces/{wid}/ledger", s.withAuth(s.withWorkspace(s.listLedger)))
	s.mux.HandleFunc("POST /api/workspaces/{wid}/ledger", s.withAuth(s.withWorkspace(s.createLedgerEntry)))
	s.mux.HandleFunc("PATCH /api/workspaces/{wid}/ledger/{eid}", s.withAuth(s.withWorkspace(s.updateLedgerEntry)))
	s.mux.HandleFunc("POST /api/workspaces/{wid}/ledger/delete", s.withAuth(s.withWorkspace(s.deleteLedgerEntries)))
	s.mux.HandleFunc("POST /api/workspaces/{wid}/ledger/restore", s.withAuth(s.withWorkspace(s.restoreLedgerEntries)))
	// 分类预算：长期月度预算（每分类一条，upsert 按分类收敛）；成员级，
	// 与账目同边界。写操作响应回全量列表。
	s.mux.HandleFunc("GET /api/workspaces/{wid}/ledger/budgets", s.withAuth(s.withWorkspace(s.listLedgerBudgets)))
	s.mux.HandleFunc("PUT /api/workspaces/{wid}/ledger/budgets", s.withAuth(s.withWorkspace(s.putLedgerBudget)))
	s.mux.HandleFunc("DELETE /api/workspaces/{wid}/ledger/budgets/{category}", s.withAuth(s.withWorkspace(s.deleteLedgerBudget)))

	// 助手配置：成员可看掩码，只有 owner 能改、能取明文凭据。
	s.mux.HandleFunc("GET /api/workspaces/{wid}/assistant", s.withAuth(s.withWorkspace(s.getAssistantConfig)))
	s.mux.HandleFunc("PUT /api/workspaces/{wid}/assistant", s.withAuth(s.withWorkspaceOwner(s.putAssistantConfig)))
	s.mux.HandleFunc("GET /api/workspaces/{wid}/assistant/credentials", s.withAuth(s.withWorkspaceOwner(s.assistantCredentials)))

	// 助手运行控制（仅 owner）：测试凭据、启动/停止内嵌运行的连接。
	s.mux.HandleFunc("POST /api/workspaces/{wid}/assistant/test", s.withAuth(s.withWorkspaceOwner(s.testAssistant)))
	s.mux.HandleFunc("POST /api/workspaces/{wid}/assistant/test-llm", s.withAuth(s.withWorkspaceOwner(s.testAssistantLlm)))
	s.mux.HandleFunc("POST /api/workspaces/{wid}/assistant/start", s.withAuth(s.withWorkspaceOwner(s.startAssistant)))
	s.mux.HandleFunc("POST /api/workspaces/{wid}/assistant/stop", s.withAuth(s.withWorkspaceOwner(s.stopAssistant)))

	// 邮箱账单导入（原生 IMAP）：邮箱地址 + 授权码以密文落库，
	// 这里只持有 per-workspace 运行循环与启停端点。
	s.mux.HandleFunc("GET /api/workspaces/{wid}/mail", s.withAuth(s.withWorkspace(s.getMailConfig)))
	s.mux.HandleFunc("PUT /api/workspaces/{wid}/mail", s.withAuth(s.withWorkspaceOwner(s.putMailConfig)))
	s.mux.HandleFunc("POST /api/workspaces/{wid}/mail/test", s.withAuth(s.withWorkspaceOwner(s.testMail)))
	s.mux.HandleFunc("POST /api/workspaces/{wid}/mail/discover", s.withAuth(s.withWorkspaceOwner(s.discoverMailSenders)))
	s.mux.HandleFunc("POST /api/workspaces/{wid}/mail/start", s.withAuth(s.withWorkspaceOwner(s.startMailImport)))
	s.mux.HandleFunc("POST /api/workspaces/{wid}/mail/stop", s.withAuth(s.withWorkspaceOwner(s.stopMailImport)))

	// AI 技能：Web 端按名调用（成员可用，每工作区每日有配额）；
	// 技能列表成员可看，热重载仅 owner（技能是文件，改完 reload 即生效）。
	s.mux.HandleFunc("POST /api/workspaces/{wid}/ai/invoke", s.withAuth(s.withWorkspace(s.invokeSkill)))
	s.mux.HandleFunc("GET /api/workspaces/{wid}/ai/skills", s.withAuth(s.withWorkspace(s.listSkills)))
	s.mux.HandleFunc("POST /api/workspaces/{wid}/ai/skills/reload", s.withAuth(s.withWorkspaceOwner(s.reloadSkills)))
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
		//
		// 用「不是 GET/HEAD/OPTIONS 就要校验」而不是枚举写方法：之前只列了
		// POST/PATCH/DELETE，新增 PUT 路由时就漏掉了，被测试抓到。白名单
		// 读方法比黑名单写方法安全 —— 漏一个只会多校验，不会少校验。
		isRead := r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions
		if strings.HasPrefix(r.URL.Path, "/api/") && !isRead {
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

// withWorkspaceOwner 在 withWorkspace 之上再加一层角色检查。
//
// withWorkspace 拿到角色后丢掉了（它只关心「是不是成员」），所以这是仓库里
// 第一处 owner-only 的边界 —— 需要把 Membership 的返回值接住再比对。
func (s *Server) withWorkspaceOwner(next func(http.ResponseWriter, *http.Request, *store.User, int64)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := r.Context().Value(ctxUser).(*store.User)
		wid, err := parseWID(r)
		if err != nil || wid <= 0 {
			writeError(w, http.StatusBadRequest, "validation_failed", "无效的工作区 ID")
			return
		}
		role, err := s.st.Membership(r.Context(), u.ID, wid)
		if err != nil {
			mapStoreError(w, err)
			return
		}
		if role != "owner" {
			writeError(w, http.StatusForbidden, "forbidden", "只有工作区所有者可以修改助手配置")
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
