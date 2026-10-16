package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// 助手运行控制端点（test/start/stop）的测试。真实的企业微信长连接已在
// aibot 与 assistant 包内用本地假服务覆盖，这里只测 API 层的权限与错误边界：
// owner 边界、未配置、无凭据、缺主密钥、CSRF。

func (e *testEnv) postControl(wid float64, action string) (*http.Response, map[string]any) {
	return e.do("POST", wf("/api/workspaces/%v/assistant/%s", wid, action), nil)
}

// 三个控制端点都只允许 owner。
func TestAssistantControlRequiresOwner(t *testing.T) {
	e := newTestEnv(t)
	e.withEncryption(t)
	ctx := t.Context()

	e.register("owner-ctl", "password123")
	wid := e.wid()
	e.putAssistant(wid, map[string]any{
		"botId": "B1", "botSecret": "s", "kimiApiKey": "",
		"kimiBase": "", "kimiModel": "", "version": 0,
	})

	// 第二个账号，直接插库加成同一工作区的普通成员（没有邀请接口）。
	e.do("POST", "/api/auth/logout", nil)
	e.register("member-ctl", "password123")
	_, me := e.do("GET", "/api/me", nil)
	memberID := int64(me["user"].(map[string]any)["id"].(float64))
	if _, err := e.s.st.DB().ExecContext(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role, joined_at) VALUES (?, ?, 'member', ?)`,
		int64(wid), memberID, time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z")); err != nil {
		t.Fatalf("插入成员失败：%v", err)
	}

	for _, action := range []string{"test", "start", "stop"} {
		resp, _ := e.postControl(wid, action)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("成员 %s 应 403，得到 %d", action, resp.StatusCode)
		}
	}
}

// 未配置时：test 也能测「表单当前值」，所以无 body 且无保存 → 明确「没东西可测」
// （400 not_configured）；start 仍 404（它只作用于已保存的配置）；stop 幂等成功。
func TestAssistantControlUnconfigured(t *testing.T) {
	e := newTestEnv(t)
	e.register("unconf-ctl", "password123")
	wid := e.wid()

	resp, out := e.postControl(wid, "test")
	if resp.StatusCode != http.StatusBadRequest || out["error"] != "not_configured" {
		t.Fatalf("未配置且无表单值时 test 应 400 not_configured，得到 %d %v", resp.StatusCode, out)
	}
	resp, out = e.postControl(wid, "start")
	if resp.StatusCode != http.StatusNotFound || out["error"] != "not_found" {
		t.Fatalf("未配置 start 应 404 not_found，得到 %d %v", resp.StatusCode, out)
	}
	// stop 对未配置也应当安全地成功（幂等），不报错。
	resp, _ = e.postControl(wid, "stop")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("未配置 stop 应 200，得到 %d", resp.StatusCode)
	}
}

// LLM「测试连接」测的是表单当前值：填了模型就用填的，key 留空才回退到已保存值。
// 用本地 httptest 冒充 Kimi 接口，验证请求确实带上了「这次填的模型」和「已保存的 key」。
func TestAssistantTestLlmUsesTypedValues(t *testing.T) {
	e := newTestEnv(t)
	e.withEncryption(t)
	e.register("llm-typed", "password123")
	wid := e.wid()

	var gotModel, gotAuth string
	kimi := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var req struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotModel = req.Model
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer kimi.Close()

	// 已保存：旧 key + 旧模型 + 指向本地假接口的地址。
	e.putAssistant(wid, map[string]any{
		"botId": "B1", "botSecret": "", "kimiApiKey": "saved-key",
		"kimiBase": kimi.URL, "kimiModel": "saved-model", "version": 0,
	})

	// 只填模型、不动 key：应带上这次填的模型，key 回退到已保存值，地址用已保存值。
	resp, out := e.do("POST", wf("/api/workspaces/%v/assistant/test-llm", wid), map[string]any{
		"kimiApiKey": "", "kimiModel": "typed-model",
	})
	if resp.StatusCode != http.StatusOK || out["ok"] != true {
		t.Fatalf("测试应通过：%d %v", resp.StatusCode, out)
	}
	if gotModel != "typed-model" {
		t.Fatalf("应使用这次填的模型，得到 %q", gotModel)
	}
	if gotAuth != "Bearer saved-key" {
		t.Fatalf("key 留空应回退到已保存值，得到 %q", gotAuth)
	}
}

// 还没保存过 key 也能测：先存好非机密的接口地址，再只填 key/模型即可测通。
// 这正是「测试通过之前不保存密钥」这条流程成立的关键。
func TestAssistantTestLlmTypedKeyWithoutSavedKey(t *testing.T) {
	e := newTestEnv(t)
	e.withEncryption(t)
	e.register("llm-fresh", "password123")
	wid := e.wid()

	var gotAuth, gotModel string
	kimi := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var req struct{ Model string `json:"model"` }
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotModel = req.Model
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer kimi.Close()

	// 只存地址、不存 key（key 留空）。地址是 SSRF 敏感字段，只能走保存路径。
	e.putAssistant(wid, map[string]any{
		"botId": "B1", "botSecret": "", "kimiApiKey": "",
		"kimiBase": kimi.URL, "kimiModel": "", "version": 0,
	})

	resp, out := e.do("POST", wf("/api/workspaces/%v/assistant/test-llm", wid), map[string]any{
		"kimiApiKey": "fresh-key", "kimiModel": "moonshot-v1-8k",
	})
	if resp.StatusCode != http.StatusOK || out["ok"] != true {
		t.Fatalf("只填 key/模型（未保存 key）也应能测通：%d %v", resp.StatusCode, out)
	}
	if gotAuth != "Bearer fresh-key" {
		t.Fatalf("应使用这次填的 key，得到 %q", gotAuth)
	}
	if gotModel != "moonshot-v1-8k" {
		t.Fatalf("应使用这次填的模型，得到 %q", gotModel)
	}
}

// 接口地址不能随测试请求传入（否则测试端点就成了新的 SSRF 起点）：带了
// kimiBase 会被 DisallowUnknownFields 直接 400。
func TestAssistantTestLlmRejectsBaseInBody(t *testing.T) {
	e := newTestEnv(t)
	e.withEncryption(t)
	e.register("llm-base", "password123")
	wid := e.wid()

	resp, out := e.do("POST", wf("/api/workspaces/%v/assistant/test-llm", wid), map[string]any{
		"kimiApiKey": "k", "kimiBase": "http://169.254.169.254", "kimiModel": "m",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("测试请求不应接受 kimiBase，得到 %d %v", resp.StatusCode, out)
	}
}

// 配置了但没有机器人凭据时，test/start 都要给出「没有凭据」，而不是去连接。
func TestAssistantControlWithoutCredentials(t *testing.T) {
	e := newTestEnv(t)
	e.withEncryption(t)
	e.register("noconf-creds", "password123")
	wid := e.wid()
	e.putAssistant(wid, map[string]any{
		"botId": "B1", "botSecret": "", "kimiApiKey": "",
		"kimiBase": "", "kimiModel": "", "version": 0,
	})

	resp, out := e.postControl(wid, "test")
	if resp.StatusCode != http.StatusBadRequest || out["error"] != "not_configured" {
		t.Fatalf("无凭据 test 应 400 not_configured，得到 %d %v", resp.StatusCode, out)
	}
	resp, out = e.postControl(wid, "start")
	if resp.StatusCode != http.StatusBadRequest || out["error"] != "not_configured" {
		t.Fatalf("无凭据 start 应 400 not_configured，得到 %d %v", resp.StatusCode, out)
	}
}

// 主密钥缺失（配置存好了但密钥被移除）时，test/start 必须拒绝，而不是崩掉。
func TestAssistantControlWithoutMasterKey(t *testing.T) {
	e := newTestEnv(t)
	e.withEncryption(t)
	ctx := t.Context()

	e.register("nokey-ctl", "password123")
	wid := e.wid()
	e.putAssistant(wid, map[string]any{
		"botId": "B1", "botSecret": "s", "kimiApiKey": "",
		"kimiBase": "", "kimiModel": "", "version": 0,
	})

	// 模拟「换机器后主密钥没设」：库里有密文，但服务器拿不到密钥。
	e.s.box = nil

	resp, out := e.postControl(wid, "test")
	if resp.StatusCode != http.StatusBadRequest || out["error"] != "encryption_unavailable" {
		t.Fatalf("缺主密钥 test 应 400 encryption_unavailable，得到 %d %v", resp.StatusCode, out)
	}

	// start 还要先过 enabled 检查，所以把 enabled 直接置真，才能走到主密钥分支。
	if err := e.s.st.SetAssistantEnabled(ctx, int64(wid), true); err != nil {
		t.Fatal(err)
	}
	resp, out = e.postControl(wid, "start")
	if resp.StatusCode != http.StatusBadRequest || out["error"] != "encryption_unavailable" {
		t.Fatalf("缺主密钥 start 应 400 encryption_unavailable，得到 %d %v", resp.StatusCode, out)
	}
}

// 控制端点是变更请求，必须带 CSRF 头。
func TestAssistantControlRequiresCSRFHeader(t *testing.T) {
	e := newTestEnv(t)
	e.register("csrf-ctl", "password123")
	wid := e.wid()

	for _, action := range []string{"test", "start", "stop"} {
		req, _ := http.NewRequest("POST", e.srv.URL+wf("/api/workspaces/%v/assistant/%s", wid, action), bytes.NewReader(nil))
		req.Header.Set("Content-Type", "application/json")
		for name, val := range e.cj {
			req.AddCookie(&http.Cookie{Name: name, Value: val})
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s 缺 CSRF 头应 403，得到 %d", action, resp.StatusCode)
		}
	}
}
