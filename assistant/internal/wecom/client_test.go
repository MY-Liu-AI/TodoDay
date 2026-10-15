package wecom

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// withAPIServer 把包级 apiBase 指向测试服务器，测试结束后还原。
func withAPIServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	old := apiBase
	apiBase = srv.URL
	t.Cleanup(func() {
		apiBase = old
		srv.Close()
	})
	return srv
}

func TestAccessTokenIsCached(t *testing.T) {
	var calls int32
	withAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if !strings.Contains(r.URL.Path, "/cgi-bin/gettoken") {
			t.Errorf("意外的路径：%s", r.URL.Path)
		}
		if r.URL.Query().Get("corpid") == "" || r.URL.Query().Get("corpsecret") == "" {
			t.Error("缺少 corpid 或 corpsecret")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"errcode": 0, "errmsg": "ok", "access_token": "TOKEN-1", "expires_in": 7200,
		})
	})

	c := NewClient("corp", "secret", "1000002")
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		tok, err := c.AccessToken(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if tok != "TOKEN-1" {
			t.Fatalf("令牌不正确：%q", tok)
		}
	}
	// 缓存生效时只换一次令牌。每次重取会触发官方限流。
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("换令牌应当只调用 1 次，实际 %d 次", n)
	}
}

func TestAccessTokenRejectsErrorResponse(t *testing.T) {
	withAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 40001, "errmsg": "invalid secret"})
	})
	c := NewClient("corp", "bad", "1000002")
	if _, err := c.AccessToken(context.Background()); err == nil {
		t.Fatal("errcode 非 0 时应当报错")
	}
}

func TestSendText(t *testing.T) {
	var sent struct {
		ToUser  string
		Msgtype string
		AgentID string
		Content string
	}
	withAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "gettoken"):
			_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "access_token": "T", "expires_in": 7200})
		case strings.Contains(r.URL.Path, "message/send"):
			if got := r.URL.Query().Get("access_token"); got != "T" {
				t.Errorf("access_token 未带上：%q", got)
			}
			raw, _ := io.ReadAll(r.Body)
			var payload struct {
				ToUser  string `json:"touser"`
				Msgtype string `json:"msgtype"`
				AgentID string `json:"agentid"`
				Text    struct {
					Content string `json:"content"`
				} `json:"text"`
			}
			if err := json.Unmarshal(raw, &payload); err != nil {
				t.Errorf("请求体不是合法 JSON：%v", err)
			}
			sent.ToUser = payload.ToUser
			sent.Msgtype = payload.Msgtype
			sent.AgentID = payload.AgentID
			sent.Content = payload.Text.Content
			_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "errmsg": "ok"})
		default:
			t.Errorf("意外路径：%s", r.URL.Path)
		}
	})

	c := NewClient("corp", "secret", "1000002")
	if err := c.SendText(context.Background(), "ZhangSan", "已记录"); err != nil {
		t.Fatal(err)
	}
	if sent.ToUser != "ZhangSan" || sent.Msgtype != "text" || sent.AgentID != "1000002" {
		t.Fatalf("发送参数不正确：%+v", sent)
	}
	if sent.Content != "已记录" {
		t.Fatalf("内容不正确：%q", sent.Content)
	}
}

// 42001 表示 access_token 过期：应当清缓存并重试一次，而不是直接失败。
func TestSendTextRefreshesExpiredToken(t *testing.T) {
	var tokenCalls, sendCalls int32
	withAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "gettoken"):
			n := atomic.AddInt32(&tokenCalls, 1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"errcode": 0, "access_token": "T" + string(rune('0'+n)), "expires_in": 7200,
			})
		case strings.Contains(r.URL.Path, "message/send"):
			n := atomic.AddInt32(&sendCalls, 1)
			if n == 1 {
				_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 42001, "errmsg": "expired"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "errmsg": "ok"})
		}
	})

	c := NewClient("corp", "secret", "1000002")
	if err := c.SendText(context.Background(), "ZhangSan", "hi"); err != nil {
		t.Fatalf("应当自动刷新令牌并重试，却失败了：%v", err)
	}
	if atomic.LoadInt32(&tokenCalls) != 2 {
		t.Fatalf("应当换两次令牌，实际 %d", tokenCalls)
	}
	if atomic.LoadInt32(&sendCalls) != 2 {
		t.Fatalf("应当发送两次，实际 %d", sendCalls)
	}
}

func TestSendTextSurfacesOtherErrors(t *testing.T) {
	withAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "gettoken") {
			_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "access_token": "T", "expires_in": 7200})
			return
		}
		// 60020：调用方 IP 不在企业可信 IP 列表里，这是部署时的常见问题。
		_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 60020, "errmsg": "not allow to access from your ip"})
	})
	c := NewClient("corp", "secret", "1000002")
	err := c.SendText(context.Background(), "ZhangSan", "hi")
	if err == nil {
		t.Fatal("应当返回错误")
	}
	if !strings.Contains(err.Error(), "60020") {
		t.Fatalf("错误信息应当带上 errcode 便于排查：%v", err)
	}
}

func TestAccessTokenCachingRespectsExpiry(t *testing.T) {
	// 直接验证缓存字段的行为，不依赖真实时间流逝。
	c := NewClient("corp", "secret", "1")
	c.token = "SHORT"
	c.expires = time.Now().Add(-time.Second)
	withAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "access_token": "FRESH", "expires_in": 7200})
	})
	tok, err := c.AccessToken(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tok != "FRESH" {
		t.Fatalf("过期的缓存应当被刷新，得到 %q", tok)
	}
}
