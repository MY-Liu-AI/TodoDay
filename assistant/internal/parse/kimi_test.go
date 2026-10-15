package parse

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// kimiServer 模拟 Moonshot 的 OpenAI 兼容接口。
func kimiServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

// toolCallResponse 构造一个标准的 function calling 响应。
func toolCallResponse(arguments string) []byte {
	payload := map[string]any{
		"choices": []map[string]any{{
			"finish_reason": "tool_calls",
			"message": map[string]any{
				"role":    "assistant",
				"content": "",
				"tool_calls": []map[string]any{{
					"id":   "call_1",
					"type": "function",
					"function": map[string]any{
						"name":      toolName,
						"arguments": arguments,
					},
				}},
			},
		}},
	}
	raw, _ := json.Marshal(payload)
	return raw
}

func TestKimiParseSuccess(t *testing.T) {
	var gotAuth, gotModel string
	srv := kimiServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotModel = r.Header.Get("Content-Type")
		var req struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			ToolChoice string `json:"tool_choice"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Model == "" {
			t.Error("请求缺少 model")
		}
		// 提示词里必须带上今天的日期，否则相对日期无法换算。
		joined := ""
		for _, m := range req.Messages {
			joined += m.Content
		}
		if !strings.Contains(joined, "2026-10-15") {
			t.Errorf("提示词里没有今天的日期：%s", joined)
		}
		if req.ToolChoice != "auto" {
			t.Errorf("tool_choice 应为 auto，得到 %q", req.ToolChoice)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(toolCallResponse(`{"title":"跟老王开会","due":"2026-10-16","priority":1,"group":"客户"}`))
	})

	k := NewKimi("test-key", srv.URL, "moonshot-v1-8k", 5*time.Second)
	plan, err := k.Parse(context.Background(), "明天跟老王开会，重要", testNow, testLoc)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Title != "跟老王开会" || plan.Due != "2026-10-16" || plan.Priority != 1 || plan.Group != "客户" {
		t.Fatalf("解析结果不正确：%+v", plan)
	}
	if gotAuth != "Bearer test-key" {
		t.Fatalf("Authorization 头不正确：%q", gotAuth)
	}
	if gotModel != "application/json" {
		t.Fatalf("Content-Type 不正确：%q", gotModel)
	}
}

func TestKimiParseWithoutKey(t *testing.T) {
	k := NewKimi("", "http://unused", "", time.Second)
	if _, err := k.Parse(context.Background(), "任何内容", testNow, testLoc); err == nil {
		t.Fatal("没有密钥时应当报错，由上层降级")
	}
}

func TestKimiParseSurfacesAPIError(t *testing.T) {
	srv := kimiServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","message":"Invalid API key"}}`))
	})
	k := NewKimi("bad", srv.URL, "", 5*time.Second)
	_, err := k.Parse(context.Background(), "开会", testNow, testLoc)
	if err == nil {
		t.Fatal("HTTP 401 应当报错")
	}
	if !strings.Contains(err.Error(), "Invalid API key") {
		t.Fatalf("错误信息应当带上服务端说明：%v", err)
	}
}

func TestKimiParseRequiresToolCall(t *testing.T) {
	srv := kimiServer(t, func(w http.ResponseWriter, r *http.Request) {
		// 模型用自然语言回答而不是调工具。
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"好的"}}]}`))
	})
	k := NewKimi("k", srv.URL, "", 5*time.Second)
	if _, err := k.Parse(context.Background(), "开会", testNow, testLoc); err == nil {
		t.Fatal("没有工具调用时应当报错")
	}
}

func TestKimiParseRejectsEmptyTitle(t *testing.T) {
	srv := kimiServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(toolCallResponse(`{"title":"   ","due":"","priority":3,"group":"生活"}`))
	})
	k := NewKimi("k", srv.URL, "", 5*time.Second)
	if _, err := k.Parse(context.Background(), "???", testNow, testLoc); err == nil {
		t.Fatal("标题为空时应当报错，让上层走规则解析")
	}
}

func TestKimiParseRejectsBadArgumentsJSON(t *testing.T) {
	srv := kimiServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(toolCallResponse(`{"title": 这不是 JSON`))
	})
	k := NewKimi("k", srv.URL, "", 5*time.Second)
	if _, err := k.Parse(context.Background(), "开会", testNow, testLoc); err == nil {
		t.Fatal("参数不是合法 JSON 时应当报错")
	}
}

// 模型的越界输出要被本地收敛，否则建任务会被后端 400 打回。
func TestKimiNormalizesOutOfRangeFields(t *testing.T) {
	srv := kimiServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(toolCallResponse(`{"title":"任务","due":"2026-13-45","priority":9,"group":""}`))
	})
	k := NewKimi("k", srv.URL, "", 5*time.Second)
	plan, err := k.Parse(context.Background(), "任务", testNow, testLoc)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Priority != 3 {
		t.Errorf("越界优先级应收敛为 3，得到 %d", plan.Priority)
	}
	if plan.Due != "" {
		t.Errorf("非法日期应清空，得到 %q", plan.Due)
	}
	if plan.Group == "" {
		t.Error("分组不应为空，应回落到默认值")
	}
}

func TestKimiNormalizeTruncatesLongFields(t *testing.T) {
	long := strings.Repeat("字", 300)
	srv := kimiServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := json.Marshal(map[string]any{
			"title": long, "due": "", "priority": 3, "group": strings.Repeat("组", 60),
		})
		_, _ = w.Write(toolCallResponse(string(body)))
	})
	k := NewKimi("k", srv.URL, "", 5*time.Second)
	plan, err := k.Parse(context.Background(), "x", testNow, testLoc)
	if err != nil {
		t.Fatal(err)
	}
	// 后端限制：标题 240 字、分组 40 字。
	if n := len([]rune(plan.Title)); n > 240 {
		t.Errorf("标题应截断到 240 字，得到 %d", n)
	}
	if n := len([]rune(plan.Group)); n > 40 {
		t.Errorf("分组应截断到 40 字，得到 %d", n)
	}
}

func TestKimiParseRespectsContextTimeout(t *testing.T) {
	srv := kimiServer(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		_, _ = w.Write(toolCallResponse(`{"title":"慢","due":"","priority":3,"group":"生活"}`))
	})
	k := NewKimi("k", srv.URL, "", 50*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := k.Parse(ctx, "开会", testNow, testLoc); err == nil {
		t.Fatal("超时应当报错，由上层降级")
	}
}
