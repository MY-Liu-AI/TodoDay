package ai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// 流式（SSE）响应要聚合成与非流式一致的 ChatResponse；
// 服务端忽略 stream 字段直接回整包 JSON 时按 Content-Type 自动退回普通解码。
func TestDoChatStreamAccumulates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"{\\\"items\\\":\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"[]}\"},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	out, err := DoChat(context.Background(), srv.Client(), srv.URL, "k", &ChatRequest{
		Model:  "m",
		Stream: true,
		Messages: []ChatMessage{
			{Role: "user", Content: "hi"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Choices) != 1 {
		t.Fatalf("choices = %d", len(out.Choices))
	}
	if got, want := out.Choices[0].Message.Content, `{"items":[]}`; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
	if out.Choices[0].FinishReason != "stop" {
		t.Fatalf("finish_reason = %q", out.Choices[0].FinishReason)
	}
}

// 服务端不认 stream（比如假的 LLM 测试桩）直接回普通 JSON：DoChat 照常解码。
func TestDoChatStreamFallsBackToJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer srv.Close()

	out, err := DoChat(context.Background(), srv.Client(), srv.URL, "k", &ChatRequest{
		Model:    "m",
		Stream:   true,
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Choices[0].Message.Content != "ok" {
		t.Fatalf("content = %q", out.Choices[0].Message.Content)
	}
}

// 流式中途吐 error 事件要如实失败，不静默给半个结果。
func TestDoChatStreamErrorEvent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"error\":{\"message\":\"quota busted\"}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	_, err := DoChat(context.Background(), srv.Client(), srv.URL, "k", &ChatRequest{
		Model:    "m",
		Stream:   true,
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("期望流式错误被返回")
	}
}
