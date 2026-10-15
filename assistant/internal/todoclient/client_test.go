package todoclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"assistant/internal/parse"
)

// fakeBackend 模拟 TodoDay 后端的三个端点。loginState 控制登录是否成功。
type fakeBackend struct {
	loginCalls   int32
	taskCalls    int32
	taskStatus   int
	taskError    string
	loginFails   bool
	taskAuthFail int32 // 前 N 次建任务返回 401，用于测试自动重登
	seenClientID atomic.Value
	seenCSRF     atomic.Bool
}

func (f *fakeBackend) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/api/auth/login"):
			atomic.AddInt32(&f.loginCalls, 1)
			if f.loginFails {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"unauthorized","message":"用户名或密码不正确"}`))
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "tododay_session", Value: "sess"})
			_, _ = w.Write([]byte(`{"user":{"id":1,"username":"bot"},"workspace":{"id":7}}`))

		case strings.HasSuffix(r.URL.Path, "/api/me"):
			_, _ = w.Write([]byte(`{"user":{"id":1,"username":"bot"},"workspaces":[{"id":7,"name":"个人工作区","timezone":"Asia/Shanghai"}]}`))

		case strings.HasSuffix(r.URL.Path, "/tasks"):
			atomic.AddInt32(&f.taskCalls, 1)
			// CSRF 头缺失时后端一律 403，这里断言客户端确实带上了。
			f.seenCSRF.Store(r.Header.Get("X-Requested-With") == "XMLHttpRequest")
			if n := atomic.LoadInt32(&f.taskAuthFail); n > 0 {
				atomic.AddInt32(&f.taskAuthFail, -1)
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"unauthorized","message":"请先登录"}`))
				return
			}
			var body struct {
				T        string `json:"t"`
				P        int    `json:"p"`
				Due      string `json:"due"`
				G        string `json:"g"`
				ClientID string `json:"clientId"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.seenClientID.Store(body.ClientID)

			if f.taskStatus != 0 {
				w.WriteHeader(f.taskStatus)
				_, _ = w.Write([]byte(`{"error":"x","message":"` + f.taskError + `"}`))
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"t_1","t":"x","version":1}`))

		default:
			t.Errorf("意外的请求路径：%s", r.URL.Path)
		}
	}
}

func newFake(t *testing.T, f *fakeBackend) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, "bot", "password123", 7)
	if err != nil {
		t.Fatal(err)
	}
	return c, srv
}

func TestCreatePlanSuccess(t *testing.T) {
	f := &fakeBackend{}
	c, _ := newFake(t, f)
	ctx := context.Background()

	if err := c.Login(ctx); err != nil {
		t.Fatal(err)
	}
	plan := parse.Plan{Title: "交周报", Due: "2026-10-16", Priority: 1, Group: "工作"}
	if err := c.CreatePlan(ctx, plan, "wx_123"); err != nil {
		t.Fatal(err)
	}
	if !f.seenCSRF.Load() {
		t.Fatal("建任务请求必须带 X-Requested-With 头，否则后端会 403")
	}
	if got := f.seenClientID.Load(); got != "wx_123" {
		t.Fatalf("clientId 未正确传递：%v", got)
	}
}

func TestLoginFailureIsReported(t *testing.T) {
	f := &fakeBackend{loginFails: true}
	c, _ := newFake(t, f)
	err := c.Login(context.Background())
	if err == nil {
		t.Fatal("登录失败应当报错")
	}
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("应当归类为认证错误，得到 %v", err)
	}
}

// 会话过期时应当自动重登并重试一次，用同一个 clientId（幂等，不会重复建任务）。
func TestCreatePlanReloginsOn401(t *testing.T) {
	f := &fakeBackend{taskAuthFail: 1}
	c, _ := newFake(t, f)
	ctx := context.Background()

	if err := c.Login(ctx); err != nil {
		t.Fatal(err)
	}
	before := atomic.LoadInt32(&f.loginCalls)
	if err := c.CreatePlan(ctx, parse.Plan{Title: "x", Priority: 3, Group: "生活"}, "wx_1"); err != nil {
		t.Fatalf("自动重登后应当成功：%v", err)
	}
	if atomic.LoadInt32(&f.loginCalls) != before+1 {
		t.Fatal("应当自动重登一次")
	}
	if atomic.LoadInt32(&f.taskCalls) != 2 {
		t.Fatal("应当重试一次建任务")
	}
}

// 工作区 ID 配错时后端返回 403：这条错误必须能与「内容不合法」区分开，
// 否则排查方向会完全错。
func TestForbiddenIsDistinctFromValidation(t *testing.T) {
	f := &fakeBackend{taskStatus: http.StatusForbidden, taskError: "无权访问该工作区"}
	c, _ := newFake(t, f)
	ctx := context.Background()
	if err := c.Login(ctx); err != nil {
		t.Fatal(err)
	}
	err := c.CreatePlan(ctx, parse.Plan{Title: "x", Priority: 3, Group: "生活"}, "wx_1")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("403 应当归类为权限错误，得到 %v", err)
	}
	if errors.Is(err, ErrValidation) {
		t.Fatal("403 不应被当作内容校验错误")
	}

	f2 := &fakeBackend{taskStatus: http.StatusBadRequest, taskError: "任务内容需 1–240 个字符"}
	c2, _ := newFake(t, f2)
	if err := c2.Login(ctx); err != nil {
		t.Fatal(err)
	}
	err2 := c2.CreatePlan(ctx, parse.Plan{Title: "x", Priority: 3, Group: "生活"}, "wx_2")
	if !errors.Is(err2, ErrValidation) {
		t.Fatalf("400 应当归类为内容校验错误，得到 %v", err2)
	}
	if errors.Is(err2, ErrForbidden) {
		t.Fatal("400 不应被当作权限错误")
	}
}

func TestWorkspaceTimezone(t *testing.T) {
	f := &fakeBackend{}
	c, _ := newFake(t, f)
	ctx := context.Background()
	if err := c.Login(ctx); err != nil {
		t.Fatal(err)
	}
	tz, err := c.WorkspaceTimezone(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if tz != "Asia/Shanghai" {
		t.Fatalf("时区读取错误：%q", tz)
	}
}

// 账号不属于目标工作区时，必须明确报错——相对日期会按错的时区换算。
func TestWorkspaceTimezoneRejectsForeignWorkspace(t *testing.T) {
	f := &fakeBackend{}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	c, err := New(srv.URL, "bot", "password123", 999)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Login(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.WorkspaceTimezone(context.Background()); !errors.Is(err, ErrForbidden) {
		t.Fatalf("应当报权限错误，得到 %v", err)
	}
}

/* ── FetchCredentials：网页配置流向助手的通道 ───────────────────────── */

// credBackend 在 fakeBackend 之上加一个 credentials 端点。
type credBackend struct {
	*fakeBackend
	credStatus int
	credBody   string
	credCalls  int32
	credAuth   int32 // 前 N 次返回 401
}

func (f *credBackend) handler(t *testing.T) http.HandlerFunc {
	base := f.fakeBackend.handler(t)
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/assistant/credentials") {
			atomic.AddInt32(&f.credCalls, 1)
			if n := atomic.LoadInt32(&f.credAuth); n > 0 {
				atomic.AddInt32(&f.credAuth, -1)
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
				return
			}
			w.WriteHeader(f.credStatus)
			_, _ = w.Write([]byte(f.credBody))
			return
		}
		base(w, r)
	}
}

func newCredFake(t *testing.T, f *credBackend) *Client {
	t.Helper()
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, "bot", "password123", 7)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Login(context.Background()); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestFetchCredentialsSuccess(t *testing.T) {
	f := &credBackend{
		fakeBackend: &fakeBackend{},
		credStatus:  http.StatusOK,
		credBody:    `{"enabled":true,"botId":"B1","botSecret":"S1","kimiApiKey":"K1","kimiBase":"https://x/v1","kimiModel":"m"}`,
	}
	c := newCredFake(t, f)
	got, err := c.FetchCredentials(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.BotID != "B1" || got.BotSecret != "S1" || got.KimiAPIKey != "K1" || !got.Enabled {
		t.Fatalf("凭据解析不正确：%+v", got)
	}
}

// 404 必须映射成 ErrNotConfigured：调用方据此回落到环境变量，
// 而不是把「还没配」当成故障。
func TestFetchCredentialsNotConfigured(t *testing.T) {
	f := &credBackend{
		fakeBackend: &fakeBackend{},
		credStatus:  http.StatusNotFound,
		credBody:    `{"error":"not_found","message":"尚未配置助手"}`,
	}
	c := newCredFake(t, f)
	_, err := c.FetchCredentials(context.Background())
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("404 应映射为 ErrNotConfigured，得到 %v", err)
	}
}

// 会话过期自动重登再试。
func TestFetchCredentialsReloginsOn401(t *testing.T) {
	f := &credBackend{
		fakeBackend: &fakeBackend{},
		credStatus:  http.StatusOK,
		credBody:    `{"enabled":true,"botId":"B1","botSecret":"S1"}`,
		credAuth:    1,
	}
	c := newCredFake(t, f)
	before := atomic.LoadInt32(&f.loginCalls)
	got, err := c.FetchCredentials(context.Background())
	if err != nil {
		t.Fatalf("自动重登后应成功：%v", err)
	}
	if got.BotID != "B1" {
		t.Fatalf("重登后应拿到凭据：%+v", got)
	}
	if atomic.LoadInt32(&f.loginCalls) != before+1 {
		t.Fatal("应当自动重登一次")
	}
	if atomic.LoadInt32(&f.credCalls) != 2 {
		t.Fatal("应当重试一次取凭据")
	}
}

// 非 owner 取凭据会被后端 403：要归类成权限错误，让日志指向排查方向。
func TestFetchCredentialsForbidden(t *testing.T) {
	f := &credBackend{
		fakeBackend: &fakeBackend{},
		credStatus:  http.StatusForbidden,
		credBody:    `{"error":"forbidden","message":"只有工作区所有者可以修改助手配置"}`,
	}
	c := newCredFake(t, f)
	_, err := c.FetchCredentials(context.Background())
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("403 应归类为权限错误，得到 %v", err)
	}
}

func TestUnreachableBackend(t *testing.T) {
	// 端口上没有任何服务。
	c, err := New("http://127.0.0.1:1", "bot", "pw", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Login(context.Background()); err == nil {
		t.Fatal("后端不可达时应当报错")
	}
}
