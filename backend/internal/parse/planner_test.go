package parse

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// planCallResponse 构造一个指定工具名的 function calling 响应（多轮规划用）。
func planCallResponse(name, arguments string) []byte {
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
						"name":      name,
						"arguments": arguments,
					},
				}},
			},
		}},
	}
	raw, _ := json.Marshal(payload)
	return raw
}

func TestPlanWritesMultipleTasks(t *testing.T) {
	srv := kimiServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		joined := ""
		for _, m := range req.Messages {
			joined += m.Content
		}
		// 多轮规划的提示词同样要带今天的日期，相对日期才能换算。
		if !strings.Contains(joined, "2026-10-15") {
			t.Errorf("提示词里没有今天的日期：%s", joined)
		}
		args, _ := json.Marshal(map[string]any{
			"plans": []map[string]any{
				{"title": "交周报", "due": "2026-10-16", "priority": 1, "group": "工作"},
				{"title": "回邮件", "due": "2026-10-17", "priority": 3, "group": "生活"},
			},
		})
		_, _ = w.Write(planCallResponse(recordPlansTool, string(args)))
	})

	k := NewKimi("k", srv.URL, "", 5*time.Second)
	dec, err := k.Plan(context.Background(), []Message{{Role: "user", Content: "明天交周报，后天回邮件"}}, testNow, testLoc)
	if err != nil {
		t.Fatal(err)
	}
	if dec.Kind != DecisionWrite || len(dec.Plans) != 2 {
		t.Fatalf("应当一次拆出 2 条任务：%+v", dec)
	}
	if dec.Plans[0].Title != "交周报" || dec.Plans[0].Due != "2026-10-16" || dec.Plans[0].Priority != 1 {
		t.Fatalf("第一条不正确：%+v", dec.Plans[0])
	}
}

func TestPlanAsksQuestions(t *testing.T) {
	srv := kimiServer(t, func(w http.ResponseWriter, r *http.Request) {
		args, _ := json.Marshal(map[string]any{
			"questions": []string{"截止日期是哪天？", "重要程度如何？"},
		})
		_, _ = w.Write(planCallResponse(askUserTool, string(args)))
	})
	k := NewKimi("k", srv.URL, "", 5*time.Second)
	dec, err := k.Plan(context.Background(), []Message{{Role: "user", Content: "交个报告"}}, testNow, testLoc)
	if err != nil {
		t.Fatal(err)
	}
	if dec.Kind != DecisionAsk || len(dec.Questions) != 2 {
		t.Fatalf("应当追问 2 个问题：%+v", dec)
	}
}

func TestPlanNormalizesOutOfRange(t *testing.T) {
	srv := kimiServer(t, func(w http.ResponseWriter, r *http.Request) {
		args, _ := json.Marshal(map[string]any{
			"plans": []map[string]any{
				{"title": "任务", "due": "2026-13-99", "priority": 9, "group": ""},
			},
		})
		_, _ = w.Write(planCallResponse(recordPlansTool, string(args)))
	})
	k := NewKimi("k", srv.URL, "", 5*time.Second)
	dec, err := k.Plan(context.Background(), []Message{{Role: "user", Content: "任务"}}, testNow, testLoc)
	if err != nil {
		t.Fatal(err)
	}
	p := dec.Plans[0]
	if p.Priority != 3 || p.Due != "" || p.Group == "" {
		t.Fatalf("越界字段应收敛：%+v", p)
	}
}

func TestPlanSkipsEmptyTitles(t *testing.T) {
	srv := kimiServer(t, func(w http.ResponseWriter, r *http.Request) {
		args, _ := json.Marshal(map[string]any{
			"plans": []map[string]any{
				{"title": "   ", "due": "", "priority": 3, "group": "生活"},
				{"title": "有效任务", "due": "", "priority": 3, "group": "生活"},
			},
		})
		_, _ = w.Write(planCallResponse(recordPlansTool, string(args)))
	})
	k := NewKimi("k", srv.URL, "", 5*time.Second)
	dec, err := k.Plan(context.Background(), []Message{{Role: "user", Content: "有效任务"}}, testNow, testLoc)
	if err != nil {
		t.Fatal(err)
	}
	if len(dec.Plans) != 1 || dec.Plans[0].Title != "有效任务" {
		t.Fatalf("空标题应被跳过：%+v", dec.Plans)
	}
}

func TestPlanRequiresToolCall(t *testing.T) {
	srv := kimiServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"好的"}}]}`))
	})
	k := NewKimi("k", srv.URL, "", 5*time.Second)
	if _, err := k.Plan(context.Background(), []Message{{Role: "user", Content: "开会"}}, testNow, testLoc); err == nil {
		t.Fatal("没有工具调用时应当报错")
	}
}

func TestPlanWithoutKey(t *testing.T) {
	k := NewKimi("", "http://unused", "", time.Second)
	if _, err := k.Plan(context.Background(), []Message{{Role: "user", Content: "x"}}, testNow, testLoc); err == nil {
		t.Fatal("没有密钥时应当报错，由上层降级")
	}
}

// 两个工具同现时，能落库就落库（Write 优先）。
func TestPlanWriteWinsOverAsk(t *testing.T) {
	srv := kimiServer(t, func(w http.ResponseWriter, r *http.Request) {
		args, _ := json.Marshal(map[string]any{
			"plans": []map[string]any{{"title": "任务", "due": "", "priority": 3, "group": "生活"}},
		})
		payload := map[string]any{
			"choices": []map[string]any{{
				"finish_reason": "tool_calls",
				"message": map[string]any{
					"role": "assistant",
					"tool_calls": []map[string]any{
						{"id": "a", "type": "function", "function": map[string]any{"name": askUserTool, "arguments": `{"questions":["几号？"]}`}},
						{"id": "b", "type": "function", "function": map[string]any{"name": recordPlansTool, "arguments": string(args)}},
					},
				},
			}},
		}
		raw, _ := json.Marshal(payload)
		_, _ = w.Write(raw)
	})
	k := NewKimi("k", srv.URL, "", 5*time.Second)
	dec, err := k.Plan(context.Background(), []Message{{Role: "user", Content: "任务"}}, testNow, testLoc)
	if err != nil {
		t.Fatal(err)
	}
	if dec.Kind != DecisionWrite {
		t.Fatalf("两个工具同现时应 Write 优先：%+v", dec)
	}
}
