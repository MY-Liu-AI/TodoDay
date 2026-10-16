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

// 解析器只走 AI：接上 Kimi 时返回它的结果。
func TestParserUsesKimi(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(toolCallResponse(`{"title":"AI 给的标题","due":"2026-11-01","priority":2,"group":"AI分组"}`))
	}))
	defer srv.Close()

	p := NewParser(NewKimi("k", srv.URL, "", 5*time.Second), testLoc)
	plan, err := p.Parse(context.Background(), "随便说点什么")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Title != "AI 给的标题" {
		t.Fatalf("应当采用 AI 的结果，得到 %+v", plan)
	}
}

// 未配置密钥时如实报错：没有关键词兜底，编排层负责告知用户。
func TestParserWithoutKimiFails(t *testing.T) {
	p := NewParser(nil, testLoc)
	if _, err := p.Parse(context.Background(), "明天 交周报 #工作 !!!"); err == nil {
		t.Fatal("未配置 AI 时应当报错，而不是静默成功")
	} else if !strings.Contains(err.Error(), "未配置") {
		t.Fatalf("错误应说明未配置：%v", err)
	}
}

// AI 报错时如实上抛，不静默成功、不退回规则。
func TestParserSurfacesKimiError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
	}))
	defer srv.Close()

	p := NewParser(NewKimi("k", srv.URL, "", 5*time.Second), testLoc)
	if _, err := p.Parse(context.Background(), "明天 交周报"); err == nil {
		t.Fatal("AI 失败应当报错")
	}
}

// AI 超时同样如实上抛。
func TestParserSurfacesKimiTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_, _ = w.Write(toolCallResponse(`{"title":"慢","due":"","priority":3,"group":"生活"}`))
	}))
	defer srv.Close()

	p := NewParser(NewKimi("k", srv.URL, "", 30*time.Millisecond), testLoc)
	if _, err := p.Parse(context.Background(), "明天 交周报"); err == nil {
		t.Fatal("超时应当报错")
	}
}

func TestParserLocationDefaultsToUTC(t *testing.T) {
	p := NewParser(nil, nil)
	if p.Location() != time.UTC {
		t.Fatalf("未指定时区时应当默认 UTC，得到 %v", p.Location())
	}
}

// KimiBase 是 owner 可控的配置项，客户端不得跟随重定向：否则一个看似可信
// 的主机 302 到内网地址（云元数据等）会被当作 SSRF，把 Bearer 密钥带过去。
func TestKimiDoesNotFollowRedirects(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 若重定向被跟随，这里会收到带 Bearer 的请求并回 200。
		if r.Header.Get("Authorization") != "" {
			_, _ = w.Write(toolCallResponse(`{"title":"泄漏","due":"","priority":3,"group":"生活"}`))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer target.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirector.Close()

	k := NewKimi("k", redirector.URL, "", 5*time.Second)
	if _, err := k.Parse(context.Background(), "明天 交周报", time.Now(), time.UTC); err == nil {
		t.Fatal("重定向不应被跟随；跟随后会拿到 200 与解析结果")
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

// 端到端校验：即便模型给出越界输出，经过解析器产出的字段也必须
// 落在后端可接受的范围内，否则建任务会被 400 打回。
func TestParserOutputIsAlwaysServerAcceptable(t *testing.T) {
	outputs := []string{
		`{"title":"明天 交周报 #工作 !!!","due":"2026-10-16","priority":1,"group":"工作"}`,
		`{"title":"买牛奶","due":"","priority":3,"group":""}`,
		`{"title":"任务","due":"2026-13-45","priority":9,"group":"非常非常非常长的分组名称超过四十个字符是非常非常非常长的分组名称超过四十个字符"}`,
	}
	for _, out := range outputs {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(toolCallResponse(out))
		}))
		p := NewParser(NewKimi("k", srv.URL, "", 5*time.Second), testLoc)
		plan, err := p.Parse(context.Background(), "随便说点什么")
		srv.Close()
		if err != nil {
			t.Fatalf("输出 %s 不应报错：%v", out, err)
		}
		if n := len([]rune(plan.Title)); n > 240 {
			t.Errorf("输出 %s 的标题超长：%d", out, n)
		}
		if n := len([]rune(plan.Group)); n > 40 {
			t.Errorf("输出 %s 的分组超长：%d", out, n)
		}
		if plan.Priority < 1 || plan.Priority > 3 {
			t.Errorf("输出 %s 的优先级越界：%d", out, plan.Priority)
		}
		if plan.Due != "" {
			if _, err := time.ParseInLocation("2006-01-02", plan.Due, time.UTC); err != nil {
				t.Errorf("输出 %s 的日期格式非法：%q", out, plan.Due)
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
