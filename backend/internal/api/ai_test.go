package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeLLM 返回一个假模型服务：把请求里 tasks 的每个 id 都判成 IU，
// 让端到端测试不依赖真实模型。
func fakeLLM(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var tasks struct {
			Tasks []struct {
				ID string `json:"id"`
			} `json:"tasks"`
		}
		for _, m := range req.Messages {
			if m.Role == "user" {
				_ = json.Unmarshal([]byte(m.Content), &tasks)
			}
		}
		items := make([]map[string]any, 0, len(tasks.Tasks))
		for _, tk := range tasks.Tasks {
			items = append(items, map[string]any{"id": tk.ID, "quadrant": "IU", "reason": "今天该做"})
		}
		content, _ := json.Marshal(map[string]any{"items": items})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": string(content)}}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// seedQuadrantSkill 造一个最小技能目录（等价于仓库 skills/quadrant）。
func seedQuadrantSkill(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	d := filepath.Join(dir, "quadrant")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"quadrant","version":"0.1.0","description":"四象限","kind":"llm","entries":["web"],"permission":"read","context":"today_tasks","output":"quadrant_result"}`
	if err := os.WriteFile(filepath.Join(d, "skill.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "prompt.md"), []byte("划分四象限"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestAIInvokeQuadrantEndToEnd(t *testing.T) {
	llm := fakeLLM(t)
	t.Setenv("KIMI_API_KEY", "test-key")
	t.Setenv("MOONSHOT_API_KEY", "")
	t.Setenv("KIMI_BASE_URL", llm.URL)
	t.Setenv("ASSISTANT_MODEL", "test-model")

	e := newTestEnv(t)
	if err := e.s.LoadSkills(seedQuadrantSkill(t)); err != nil {
		t.Fatal(err)
	}
	e.register("aiuser", "password123")
	wid := e.wid()

	var ids []string
	var resp *http.Response
	var out map[string]any
	for _, title := range []string{"写周报", "回邮件"} {
		resp, out = e.do("POST", wu(wid)+"/tasks", map[string]any{"t": title, "p": 2, "due": "", "g": "工作"})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create task: %d", resp.StatusCode)
		}
		ids = append(ids, out["id"].(string))
	}

	// 未排期的任务不进今日清单，模型只会看到 0 条——给一个今天的日期。
	resp, _ = e.do("PATCH", wu(wid)+"/tasks/"+ids[0], map[string]any{
		"t": "写周报", "p": 2, "due": todayStr(), "g": "工作", "version": 1,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("set due: %d", resp.StatusCode)
	}

	resp, out = e.do("POST", wu(wid)+"/ai/invoke", map[string]any{"skill": "quadrant"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("invoke: %d %v", resp.StatusCode, out)
	}
	if out["skill"] != "quadrant" || out["version"] != "0.1.0" {
		t.Fatalf("响应缺少技能信息：%v", out)
	}
	result := out["result"].(map[string]any)
	items := result["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("只有 1 个今日任务，得到 %d 条划分", len(items))
	}
	it := items[0].(map[string]any)
	if it["id"] != ids[0] || it["quadrant"] != "IU" {
		t.Fatalf("划分结果不对：%v", it)
	}
}

func TestAIInvokeErrors(t *testing.T) {
	e := newTestEnv(t)
	if err := e.s.LoadSkills(seedQuadrantSkill(t)); err != nil {
		t.Fatal(err)
	}
	e.register("aiuser2", "password123")
	wid := e.wid()

	// 未知技能 → 502（如实报错，不编造结果）。
	resp, _ := e.do("POST", wu(wid)+"/ai/invoke", map[string]any{"skill": "nope"})
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("未知技能应 502，得到 %d", resp.StatusCode)
	}
	// 缺 skill 字段 → 400。
	resp, _ = e.do("POST", wu(wid)+"/ai/invoke", map[string]any{})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("缺字段应 400，得到 %d", resp.StatusCode)
	}

	// 未配置模型凭据（清空所有兜底环境变量）→ 409。
	t.Setenv("KIMI_API_KEY", "")
	t.Setenv("MOONSHOT_API_KEY", "")
	resp, _ = e.do("POST", wu(wid)+"/ai/invoke", map[string]any{"skill": "quadrant"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("未配置应 409，得到 %d", resp.StatusCode)
	}

	// 技能列表成员可看。
	resp, out := e.do("GET", wu(wid)+"/ai/skills", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: %d", resp.StatusCode)
	}
	skills := out["skills"].([]any)
	if len(skills) != 1 || skills[0].(map[string]any)["name"] != "quadrant" {
		t.Fatalf("技能列表不对：%v", out)
	}
}

// todayStr 返回本机今天的日期串（测试断言用）。
func todayStr() string {
	return time.Now().Format("2006-01-02")
}
