package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tododay/internal/secretbox"
	"tododay/internal/store"
)

type testEnv struct {
	t   *testing.T
	srv *httptest.Server
	cj  map[string]string
	s   *Server
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	// 与主密钥无关的用例（缺主密钥路径）要求服务器一定没有 box：
	// 测试进程的环境变量不能让 NewServer 悄悄拿到真主密钥。
	t.Setenv(secretbox.EnvKey, "")
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	s := NewServer(st, "")
	s.limiter = newLoginLimiter(1<<20, time.Minute) // tests: no practical auth throttle
	env := &testEnv{t: t, srv: httptest.NewServer(s.Handler()), cj: map[string]string{}, s: s}
	t.Cleanup(env.srv.Close)
	return env
}

// withEncryption 给测试服务器装上一把固定主密钥，让保存密钥的路径可用。
// 直接赋字段是刻意的：测试与实现在同一个包内，这与既有的
// `s.limiter = ...` 覆盖做法一致。
func (e *testEnv) withEncryption(t *testing.T) {
	t.Helper()
	box, err := secretbox.FromKey(testConfigKey)
	if err != nil {
		t.Fatal(err)
	}
	e.s.box = box
}

// testConfigKey 是测试专用的 32 字节 base64 主密钥。不是真实密钥。
const testConfigKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="

func (e *testEnv) do(method, path string, body any) (*http.Response, map[string]any) {
	e.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, e.srv.URL+path, rd)
	if err != nil {
		e.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	// attach cookies manually
	for name, val := range e.cj {
		req.AddCookie(&http.Cookie{Name: name, Value: val})
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, c := range resp.Cookies() {
		e.cj[c.Name] = c.Value
	}
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		e.t.Logf("%s %s → %d: %v", method, path, resp.StatusCode, out)
	}
	return resp, out
}

func (e *testEnv) register(username, password string) (*http.Response, map[string]any) {
	return e.do("POST", "/api/auth/register", map[string]string{"username": username, "password": password})
}

func (e *testEnv) wid() float64 {
	_, me := e.do("GET", "/api/me", nil)
	ws := me["workspaces"].([]any)
	if len(ws) == 0 {
		e.t.Fatal("no workspace")
	}
	return ws[0].(map[string]any)["id"].(float64)
}

func TestRegisterLoginMeLogout(t *testing.T) {
	e := newTestEnv(t)
	resp, _ := e.register("alice", "password123")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register: %d", resp.StatusCode)
	}
	resp, me := e.do("GET", "/api/me", nil)
	if resp.StatusCode != http.StatusOK || me["user"].(map[string]any)["username"] != "alice" {
		t.Fatalf("me: %d %v", resp.StatusCode, me)
	}
	if e.wid() < 1 {
		t.Fatal("expected default workspace")
	}
	resp, _ = e.do("POST", "/api/auth/logout", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("logout: %d", resp.StatusCode)
	}
	resp, _ = e.do("GET", "/api/me", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("me after logout: %d", resp.StatusCode)
	}
}

func TestLoginRejectsBadPassword(t *testing.T) {
	e := newTestEnv(t)
	e.register("bob", "password123")
	e.do("POST", "/api/auth/logout", nil)
	resp, _ := e.do("POST", "/api/auth/login", map[string]string{"username": "bob", "password": "wrong-password"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestDuplicateUsernameRejected(t *testing.T) {
	e := newTestEnv(t)
	e.register("carol", "password123")
	resp, _ := e.register("CAROL", "password123")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409, got %d", resp.StatusCode)
	}
}

func TestTaskCRUDAndIdempotency(t *testing.T) {
	e := newTestEnv(t)
	e.register("dave", "password123")
	wid := e.wid()

	resp, out := e.do("POST", wf("/api/workspaces/%v/tasks", wid), map[string]any{
		"t": "交周报", "p": 1, "due": "2026-10-16", "g": "工作", "clientId": "c-1",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d %v", resp.StatusCode, out)
	}
	id := out["id"].(string)
	if out["version"].(float64) != 1 || out["due"] != "2026-10-16" {
		t.Fatalf("task shape: %v", out)
	}

	// Same clientId replays without creating a duplicate.
	resp, replay := e.do("POST", wf("/api/workspaces/%v/tasks", wid), map[string]any{
		"t": "交周报", "p": 1, "due": "2026-10-16", "g": "工作", "clientId": "c-1",
	})
	if resp.StatusCode != http.StatusCreated || replay["id"].(string) != id {
		t.Fatalf("idempotent replay: %d %v", resp.StatusCode, replay)
	}
	_, list := e.do("GET", wf("/api/workspaces/%v/tasks", wid), nil)
	if list["total"].(float64) != 1 {
		t.Fatalf("expected 1 task, got %v", list["total"])
	}

	// Update with correct version.
	resp, upd := e.do("PATCH", wf("/api/workspaces/%v/tasks/%s", wid, id), map[string]any{
		"t": "交周报（修订）", "p": 2, "due": "2026-10-17", "g": "工作", "version": 1,
	})
	if resp.StatusCode != http.StatusOK || upd["version"].(float64) != 2 {
		t.Fatalf("update: %d %v", resp.StatusCode, upd)
	}

	// Stale version → 409 with current task.
	resp, conflict := e.do("PATCH", wf("/api/workspaces/%v/tasks/%s", wid, id), map[string]any{
		"t": "覆盖他人修改", "p": 3, "due": "2026-10-18", "g": "工作", "version": 1,
	})
	if resp.StatusCode != http.StatusConflict || conflict["task"].(map[string]any)["version"].(float64) != 2 {
		t.Fatalf("conflict: %d %v", resp.StatusCode, conflict)
	}

	// List shows the updated title (no lost update).
	_, list = e.do("GET", wf("/api/workspaces/%v/tasks", wid), nil)
	tasks := list["tasks"].([]any)
	if tasks[0].(map[string]any)["t"] != "交周报（修订）" {
		t.Fatalf("update lost: %v", tasks[0])
	}
}

func TestCompletionEventsDriveActivity(t *testing.T) {
	e := newTestEnv(t)
	e.register("erin", "password123")
	wid := e.wid()

	_, out := e.do("POST", wf("/api/workspaces/%v/tasks", wid), map[string]any{
		"t": "游泳", "p": 3, "due": "2026-10-15", "g": "健康",
	})
	id := out["id"].(string)

	resp, done := e.do("POST", wf("/api/workspaces/%v/tasks/%s/completion", wid, id), map[string]any{"done": true, "version": 1})
	if resp.StatusCode != http.StatusOK || !done["done"].(bool) || done["at"].(float64) <= 0 {
		t.Fatalf("completion: %d %v", resp.StatusCode, done)
	}

	_, act := e.do("GET", wf("/api/workspaces/%v/activity?weeks=18", wid), nil)
	counts := act["counts"].(map[string]any)
	today := timeNowLocal(t, act["timezone"].(string))
	if counts[today] != float64(1) {
		t.Fatalf("expected 1 completion today (%s), got %v", today, counts)
	}

	// Reopen removes it from the same source of truth.
	e.do("POST", wf("/api/workspaces/%v/tasks/%s/completion", wid, id), map[string]any{"done": false, "version": 2})
	_, act = e.do("GET", wf("/api/workspaces/%v/activity?weeks=18", wid), nil)
	counts = act["counts"].(map[string]any)
	if counts[today] != nil && counts[today] != float64(0) {
		t.Fatalf("reopen should clear today's count: %v", counts)
	}
}

func TestReorderDeleteRestore(t *testing.T) {
	e := newTestEnv(t)
	e.register("frank", "password123")
	wid := e.wid()

	ids := []string{}
	for _, title := range []string{"A", "B", "C"} {
		_, out := e.do("POST", wf("/api/workspaces/%v/tasks", wid), map[string]any{"t": title, "p": 3, "g": "生活"})
		ids = append(ids, out["id"].(string))
	}

	// Manual order is persisted atomically (reverse: C, B, A).
	resp, _ := e.do("POST", wf("/api/workspaces/%v/tasks/reorder", wid), map[string]any{"ids": []string{ids[2], ids[1], ids[0]}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reorder: %d", resp.StatusCode)
	}
	_, list := e.do("GET", wf("/api/workspaces/%v/tasks", wid), nil)
	got := []string{}
	for _, tk := range list["tasks"].([]any) {
		got = append(got, tk.(map[string]any)["t"].(string))
	}
	if got[0] != "C" || got[2] != "A" {
		t.Fatalf("order not persisted: %v", got)
	}

	// Partial id list is rejected (no silent partial reorder).
	resp, _ = e.do("POST", wf("/api/workspaces/%v/tasks/reorder", wid), map[string]any{"ids": []string{ids[0]}})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("partial reorder: %d", resp.StatusCode)
	}

	// Soft delete excludes from list; restore brings back at the top.
	e.do("POST", wf("/api/workspaces/%v/tasks/delete", wid), map[string]any{"ids": []string{ids[1]}})
	_, list = e.do("GET", wf("/api/workspaces/%v/tasks", wid), nil)
	if list["total"].(float64) != 2 {
		t.Fatalf("delete failed: %v", list["total"])
	}
	resp, rst := e.do("POST", wf("/api/workspaces/%v/tasks/restore", wid), map[string]any{"ids": []string{ids[1]}})
	if resp.StatusCode != http.StatusOK || len(rst["tasks"].([]any)) != 1 {
		t.Fatalf("restore: %d %v", resp.StatusCode, rst)
	}
	_, list = e.do("GET", wf("/api/workspaces/%v/tasks", wid), nil)
	if list["total"].(float64) != 3 {
		t.Fatalf("restore failed: %v", list["total"])
	}
}

func TestCrossWorkspaceAuthorization(t *testing.T) {
	e := newTestEnv(t)
	e.register("grace", "password123")
	wid1 := e.wid()
	e.do("POST", "/api/auth/logout", nil)
	e.register("henry", "password123")
	wid2 := e.wid()

	_, out := e.do("POST", wf("/api/workspaces/%v/tasks", wid2), map[string]any{"t": "私密任务", "p": 1, "g": "工作"})
	id2 := out["id"].(string)

	// Henry cannot touch Grace's workspace (403 via membership check).
	resp, _ := e.do("GET", wf("/api/workspaces/%v/tasks", wid1), nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-workspace read: %d", resp.StatusCode)
	}

	// Guessing a task id inside the wrong workspace → 404, never leaks.
	resp, _ = e.do("PATCH", wf("/api/workspaces/%v/tasks/%s", wid1, id2), map[string]any{
		"t": "越权改写", "p": 1, "due": "", "g": "工作", "version": 1,
	})
	if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-workspace write: %d", resp.StatusCode)
	}

	// Unauthenticated → 401.
	e.do("POST", "/api/auth/logout", nil)
	resp, _ = e.do("GET", wf("/api/workspaces/%v/tasks", wid2), nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: %d", resp.StatusCode)
	}
}

func timeNowLocal(t *testing.T, tz string) string {
	t.Helper()
	loc, err := time.LoadLocation(tz)
	if err != nil {
		t.Fatal(err)
	}
	return time.Now().In(loc).Format("2006-01-02")
}

func TestImportDedupesAndValidates(t *testing.T) {
	e := newTestEnv(t)
	e.register("iris", "password123")
	wid := e.wid()

	resp, out := e.do("POST", wf("/api/workspaces/%v/tasks/import", wid), map[string]any{
		"items": []map[string]any{
			{"t": "旧任务A", "p": 1, "due": "2026-10-01", "g": "工作", "done": true, "at": 1759300000000},
			{"t": "旧任务A", "p": 1, "due": "2026-10-01", "g": "工作"}, // payload-internal duplicate
			{"t": "旧任务B", "p": 3, "due": "", "g": "生活"},
			{"t": "   ", "p": 3, "due": "", "g": "生活"},           // invalid, rejected before store
			{"t": "坏日期", "p": 3, "due": "2026-13-40", "g": "生活"}, // invalid date
		},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid import payload should fail fast: %d", resp.StatusCode)
	}

	resp, out = e.do("POST", wf("/api/workspaces/%v/tasks/import", wid), map[string]any{
		"items": []map[string]any{
			{"t": "旧任务A", "p": 1, "due": "2026-10-01", "g": "工作", "done": true, "at": 1759300000000},
			{"t": "旧任务A", "p": 1, "due": "2026-10-01", "g": "工作"},
			{"t": "旧任务B", "p": 3, "due": "", "g": "生活"},
		},
	})
	if resp.StatusCode != http.StatusOK || out["imported"].(float64) != 2 || out["skipped"].(float64) != 1 {
		t.Fatalf("import: %d %v", resp.StatusCode, out)
	}

	// Re-importing the same payload skips everything (no duplicates).
	resp, out = e.do("POST", wf("/api/workspaces/%v/tasks/import", wid), map[string]any{
		"items": []map[string]any{
			{"t": "旧任务A", "p": 1, "due": "2026-10-01", "g": "工作"},
			{"t": "旧任务B", "p": 3, "due": "", "g": "生活"},
		},
	})
	if out["imported"].(float64) != 0 || out["skipped"].(float64) != 2 {
		t.Fatalf("re-import should skip all: %v", out)
	}
}

func TestMutationRequiresCSRFHeader(t *testing.T) {
	e := newTestEnv(t)
	e.register("judy", "password123")
	body, _ := json.Marshal(map[string]string{"t": "X", "p": "3", "g": "生活"})
	req, _ := http.NewRequest("POST", e.srv.URL+wf("/api/workspaces/%v/tasks", e.wid()), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for name, val := range e.cj {
		req.AddCookie(&http.Cookie{Name: name, Value: val})
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("missing CSRF header should be rejected: %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func wf(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}

func TestFocusSessionsAndStats(t *testing.T) {
	e := newTestEnv(t)
	e.register("kate", "password123")
	wid := e.wid()

	_, task := e.do("POST", wf("/api/workspaces/%v/tasks", wid), map[string]any{"t": "写报告", "p": 1, "g": "工作"})
	tid := task["id"].(string)

	// Empty stats before any session.
	resp, st := e.do("GET", wf("/api/workspaces/%v/focus/stats", wid), nil)
	if resp.StatusCode != http.StatusOK || st["todayAgg"].(map[string]any)["pomodoros"].(float64) != 0 {
		t.Fatalf("initial stats: %d %v", resp.StatusCode, st)
	}

	now := time.Now().UnixMilli()
	// A full pomodoro bound to a task, with a clientId for idempotency.
	resp, out := e.do("POST", wf("/api/workspaces/%v/focus/sessions", wid), map[string]any{
		"taskId": tid, "plannedSec": 1500, "durationSec": 1500, "status": "completed",
		"startedAt": now - 1500_000, "endedAt": now, "clientId": "f-1",
	})
	if resp.StatusCode != http.StatusCreated || out["taskTitle"] != "写报告" {
		t.Fatalf("create: %d %v", resp.StatusCode, out)
	}
	id := out["id"].(float64)

	// Replay with the same clientId → same row, no duplicate.
	resp, replay := e.do("POST", wf("/api/workspaces/%v/focus/sessions", wid), map[string]any{
		"taskId": tid, "plannedSec": 1500, "durationSec": 1500, "status": "completed",
		"startedAt": now - 1500_000, "endedAt": now, "clientId": "f-1",
	})
	if resp.StatusCode != http.StatusCreated || replay["id"].(float64) != id {
		t.Fatalf("idempotent replay: %d %v", resp.StatusCode, replay)
	}

	// An abandoned session adds time but not a tomato.
	resp, _ = e.do("POST", wf("/api/workspaces/%v/focus/sessions", wid), map[string]any{
		"plannedSec": 1500, "durationSec": 300, "status": "abandoned", "endedAt": now,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("abandoned: %d", resp.StatusCode)
	}

	// "completed" without running the planned length is rejected.
	resp, _ = e.do("POST", wf("/api/workspaces/%v/focus/sessions", wid), map[string]any{
		"plannedSec": 1500, "durationSec": 100, "status": "completed", "endedAt": now,
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("short completed should be 400, got %d", resp.StatusCode)
	}

	// A task id from another workspace is refused.
	e.do("POST", "/api/auth/logout", nil)
	e.register("leo", "password123")
	wid2 := e.wid()
	resp, _ = e.do("POST", wf("/api/workspaces/%v/focus/sessions", wid2), map[string]any{
		"taskId": tid, "plannedSec": 1500, "durationSec": 1500, "status": "completed", "endedAt": now,
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign task should be 404, got %d", resp.StatusCode)
	}
	resp, _ = e.do("GET", wf("/api/workspaces/%v/focus/stats", wid), nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-workspace stats should be 403, got %d", resp.StatusCode)
	}

	// Back as kate: today = 1 pomodoro, 1800 s; week/month include it; one
	// session list entry per recorded row (2), newest first.
	e.do("POST", "/api/auth/logout", nil)
	e.do("POST", "/api/auth/login", map[string]string{"username": "kate", "password": "password123"})
	_, st = e.do("GET", wf("/api/workspaces/%v/focus/stats?days=30", wid), nil)
	today := st["todayAgg"].(map[string]any)
	if today["pomodoros"].(float64) != 1 || today["seconds"].(float64) != 1800 {
		t.Fatalf("today agg: %v", today)
	}
	week := st["week"].(map[string]any)
	month := st["month"].(map[string]any)
	if week["seconds"].(float64) != 1800 || month["pomodoros"].(float64) != 1 {
		t.Fatalf("week/month agg: %v %v", week, month)
	}
	sessions := st["sessions"].([]any)
	if len(sessions) != 2 || sessions[0].(map[string]any)["status"] != "abandoned" {
		t.Fatalf("sessions: %v", sessions)
	}
	daily := st["daily"].(map[string]any)
	if daily[st["today"].(string)].(map[string]any)["seconds"].(float64) != 1800 {
		t.Fatalf("daily: %v", daily)
	}
}

func TestStaticSPAFallbackServesIndexWithoutRedirect(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html><title>专注. 任务</title>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.js"), []byte("console.log(1)"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewServer(st, dir)
	s.limiter = newLoginLimiter(1<<20, time.Minute)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)

	// Real file is served as-is.
	resp, err := http.Get(srv.URL + "/app.js")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("static file: %d", resp.StatusCode)
	}

	noRedirect := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// Deep client-side routes must serve index.html with 200 on the first
	// response — a 301 here (e.g. FileServer's /index.html redirect) breaks
	// SPA routing. (The literal "/index.html" may canonicalize to "/"; that
	// redirect is fine, so only the deep routes are asserted.)
	for _, p := range []string{"/some/deep/route", "/tasks", "/"} {
		resp, err := noRedirect.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d (location %q)", p, resp.StatusCode, resp.Header.Get("Location"))
		}
		if !bytes.Contains(body, []byte("专注. 任务")) {
			t.Fatalf("%s: SPA fallback did not serve index.html: %q", p, body)
		}
	}
}
