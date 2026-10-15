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

// AI 可用时优先用 AI 的结果。
func TestParserPrefersKimi(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(toolCallResponse(`{"title":"AI 给的标题","due":"2026-11-01","priority":2,"group":"AI分组"}`))
	}))
	defer srv.Close()

	p := NewParser(NewKimi("k", srv.URL, "", 5*time.Second), testLoc)
	plan, source := p.Parse(context.Background(), "随便说点什么")
	if source != SourceKimi {
		t.Fatalf("应当使用 AI 解析，得到 %s", source)
	}
	if plan.Title != "AI 给的标题" {
		t.Fatalf("应当采用 AI 的结果，得到 %+v", plan)
	}
}

// 未配置密钥时全部走规则解析。
func TestParserFallsBackWhenNoKey(t *testing.T) {
	p := NewParser(nil, testLoc)
	plan, source := p.Parse(context.Background(), "明天 交周报 #工作 !!!")
	if source != SourceRule {
		t.Fatalf("应当走规则解析，得到 %s", source)
	}
	if plan.Title != "交周报" || plan.Due != "2026-10-16" {
		t.Fatalf("规则解析结果不正确：%+v", plan)
	}
}

// AI 报错时必须降级，而不是让整条消息失败。
func TestParserFallsBackOnAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
	}))
	defer srv.Close()

	p := NewParser(NewKimi("k", srv.URL, "", 5*time.Second), testLoc)
	plan, source := p.Parse(context.Background(), "明天 交周报")
	if source != SourceRule {
		t.Fatalf("AI 失败后应当降级到规则解析，得到 %s", source)
	}
	if plan.Title != "交周报" {
		t.Fatalf("降级后应当仍能得到正确结果，得到 %+v", plan)
	}
}

// AI 超时同样降级。
func TestParserFallsBackOnTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_, _ = w.Write(toolCallResponse(`{"title":"慢","due":"","priority":3,"group":"生活"}`))
	}))
	defer srv.Close()

	p := NewParser(NewKimi("k", srv.URL, "", 30*time.Millisecond), testLoc)
	plan, source := p.Parse(context.Background(), "明天 交周报")
	if source != SourceRule {
		t.Fatalf("超时后应当降级，得到 %s", source)
	}
	if !strings.Contains(plan.Title, "交周报") {
		t.Fatalf("降级结果不正确：%+v", plan)
	}
}

// AI 返回了标题但内容为空时也要降级。
func TestParserFallsBackOnEmptyTitle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(toolCallResponse(`{"title":"","due":"","priority":3,"group":"生活"}`))
	}))
	defer srv.Close()

	p := NewParser(NewKimi("k", srv.URL, "", 5*time.Second), testLoc)
	_, source := p.Parse(context.Background(), "明天 交周报")
	if source != SourceRule {
		t.Fatalf("AI 没有给出内容时应当降级，得到 %s", source)
	}
}

func TestParserLocationDefaultsToUTC(t *testing.T) {
	p := NewParser(nil, nil)
	if p.Location() != time.UTC {
		t.Fatalf("未指定时区时应当默认 UTC，得到 %v", p.Location())
	}
}

func TestIsEmpty(t *testing.T) {
	if !IsEmpty("   \n\t ") {
		t.Error("纯空白应当判为空")
	}
	if IsEmpty("有事") {
		t.Error("有内容不应判为空")
	}
}

// 端到端校验：无论走哪条路径，产出的字段都必须落在后端可接受的范围内。
func TestParserOutputIsAlwaysServerAcceptable(t *testing.T) {
	inputs := []string{"明天 交周报 #工作 !!!", "买牛奶", "!!!", "1/5 报名", "随便说说"}
	p := NewParser(nil, testLoc)
	for _, in := range inputs {
		plan, _ := p.Parse(context.Background(), in)
		if n := len([]rune(plan.Title)); n > 240 {
			t.Errorf("输入 %q 的标题超长：%d", in, n)
		}
		if n := len([]rune(plan.Group)); n > 40 {
			t.Errorf("输入 %q 的分组超长：%d", in, n)
		}
		if plan.Priority < 1 || plan.Priority > 3 {
			t.Errorf("输入 %q 的优先级越界：%d", in, plan.Priority)
		}
		if plan.Due != "" {
			if _, err := time.ParseInLocation("2006-01-02", plan.Due, time.UTC); err != nil {
				t.Errorf("输入 %q 的日期格式非法：%q", in, plan.Due)
			}
		}
	}
}

// 保证 JSON 往返可用：这是与 Kimi 通信的实际格式。
func TestToolArgumentsAreValidJSON(t *testing.T) {
	raw := toolCallResponse(`{"title":"x","due":"2026-10-16","priority":1,"group":"工作"}`)
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
}
