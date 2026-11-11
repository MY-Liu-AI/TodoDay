package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// 缓存策略契约：index.html 与 SPA 回退必须 no-cache（否则浏览器启发式
// 缓存会让旧 bundle 在新版部署后继续运行）；带 hash 的 /assets/ 文件
// 可永久 immutable 缓存。
func TestStaticCacheHeaders(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "app-abc123.js"), []byte("/* js */"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewServer(nil, dir)
	cases := []struct {
		path string
		want string
	}{
		{"/", "no-cache"},
		{"/index.html", "no-cache"},
		{"/goals/123", "no-cache"}, // SPA 回退也下发 index.html
		{"/assets/app-abc123.js", "public, max-age=31536000, immutable"},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, c.path, nil)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if got := rec.Header().Get("Cache-Control"); got != c.want {
			t.Errorf("GET %s: Cache-Control = %q, want %q", c.path, got, c.want)
		}
	}
}
