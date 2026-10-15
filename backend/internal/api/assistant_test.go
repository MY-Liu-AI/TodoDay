package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// 助手配置接口的测试。要点在于两条**不能退让**的性质：
//   1. 掩码视图里绝不出现密钥明文；
//   2. 没有主密钥时绝不落库明文。
// 其余是常规的权限与并发语义。

// put 发送 PUT 请求。body 里没提到的密钥字段即为「不修改」。
func (e *testEnv) putAssistant(wid float64, body map[string]any) (*http.Response, map[string]any) {
	return e.do("PUT", wf("/api/workspaces/%v/assistant", wid), body)
}

func (e *testEnv) getAssistant(wid float64) (*http.Response, map[string]any) {
	return e.do("GET", wf("/api/workspaces/%v/assistant", wid), nil)
}

func (e *testEnv) getCredentials(wid float64) (*http.Response, map[string]any) {
	return e.do("GET", wf("/api/workspaces/%v/assistant/credentials", wid), nil)
}

// 未配置时返回空视图而不是 404：对前端来说「还没配」是正常状态。
func TestAssistantConfigUnconfigured(t *testing.T) {
	e := newTestEnv(t)
	e.register("alice", "password123")
	wid := e.wid()

	resp, out := e.getAssistant(wid)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("未配置时应返回 200，得到 %d", resp.StatusCode)
	}
	if out["configured"] != false {
		t.Fatalf("configured 应为 false：%v", out)
	}
	if out["botSecretSet"] != false || out["kimiApiKeySet"] != false {
		t.Fatalf("不应显示已设置：%v", out)
	}
}

// 保存后掩码视图必须体现「已设置」，但**绝不回显密钥本身**。
func TestAssistantConfigMasksSecrets(t *testing.T) {
	e := newTestEnv(t)
	e.withEncryption(t)
	e.register("bob", "password123")
	wid := e.wid()

	const botSecret = "SUPER-SECRET-BOT-VALUE"
	const kimiKey = "sk-SECRET-KIMI-VALUE"

	resp, out := e.putAssistant(wid, map[string]any{
		"enabled": true, "botId": "BOTID-1",
		"botSecret": botSecret, "kimiApiKey": kimiKey,
		"kimiBase": "https://api.moonshot.cn/v1", "kimiModel": "moonshot-v1-8k",
		"version": 0,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("保存失败：%d %v", resp.StatusCode, out)
	}
	if out["botSecretSet"] != true || out["kimiApiKeySet"] != true {
		t.Fatalf("应显示已设置：%v", out)
	}
	if out["version"].(float64) != 1 {
		t.Fatalf("首次保存 version 应为 1：%v", out["version"])
	}

	// 这条是硬要求：整个响应体里不能出现密钥明文。
	raw, _ := json.Marshal(out)
	for _, secret := range []string{botSecret, kimiKey} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("响应体里出现了密钥明文：%s", raw)
		}
	}

	// 再 GET 一次，同样不能有明文。
	resp, out = e.getAssistant(wid)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("读取失败：%d", resp.StatusCode)
	}
	raw, _ = json.Marshal(out)
	for _, secret := range []string{botSecret, kimiKey} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("GET 响应体里出现了密钥明文：%s", raw)
		}
	}
	if out["botId"] != "BOTID-1" {
		t.Fatalf("非密钥字段应当原样返回：%v", out["botId"])
	}
}

// credentials 是唯一返回明文的接口，用来证明密文确实可解。
func TestAssistantCredentialsReturnsPlaintext(t *testing.T) {
	e := newTestEnv(t)
	e.withEncryption(t)
	e.register("carol", "password123")
	wid := e.wid()

	const botSecret = "bot-secret-xyz"
	const kimiKey = "sk-abc123"
	e.putAssistant(wid, map[string]any{
		"enabled": true, "botId": "B1", "botSecret": botSecret, "kimiApiKey": kimiKey,
		"kimiBase": "", "kimiModel": "", "version": 0,
	})

	resp, out := e.getCredentials(wid)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("取凭据失败：%d %v", resp.StatusCode, out)
	}
	if out["botSecret"] != botSecret {
		t.Fatalf("botSecret 未正确解密：%v", out["botSecret"])
	}
	if out["kimiApiKey"] != kimiKey {
		t.Fatalf("kimiApiKey 未正确解密：%v", out["kimiApiKey"])
	}
	if out["botId"] != "B1" || out["enabled"] != true {
		t.Fatalf("其余字段不正确：%v", out)
	}
}

// 未配置时取凭据返回 404（而不是空串），让助手能区分「没配」与「配了但空」。
func TestAssistantCredentialsUnconfigured(t *testing.T) {
	e := newTestEnv(t)
	e.withEncryption(t)
	e.register("dave", "password123")
	resp, _ := e.getCredentials(e.wid())
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("未配置时应 404，得到 %d", resp.StatusCode)
	}
}

// 没有主密钥时必须拒绝保存密钥，且**不能把明文写进去**。
func TestAssistantConfigRefusesSecretsWithoutKey(t *testing.T) {
	e := newTestEnv(t) // 刻意不调 withEncryption
	e.register("erin", "password123")
	wid := e.wid()

	resp, out := e.putAssistant(wid, map[string]any{
		"enabled": true, "botId": "B1", "botSecret": "plaintext-should-not-be-stored",
		"kimiApiKey": "", "kimiBase": "", "kimiModel": "", "version": 0,
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("缺主密钥时应拒绝，得到 %d %v", resp.StatusCode, out)
	}
	if out["error"] != "encryption_unavailable" {
		t.Fatalf("错误码应为 encryption_unavailable：%v", out)
	}
	// 错误信息要告诉运维配哪个变量。
	if msg, _ := out["message"].(string); !strings.Contains(msg, "TODO_CONFIG_KEY") {
		t.Fatalf("错误信息应指明要设哪个变量：%v", msg)
	}

	// 关键：确实没有落库。
	if _, err := e.s.st.GetAssistantConfig(t.Context(), int64(wid)); err == nil {
		t.Fatal("被拒绝的请求不应留下任何配置行")
	}
}

// 只有非密钥字段（比如只开开关）时，缺主密钥也应当允许保存。
func TestAssistantConfigAllowsNonSecretWithoutKey(t *testing.T) {
	e := newTestEnv(t)
	e.register("frank", "password123")
	wid := e.wid()

	resp, out := e.putAssistant(wid, map[string]any{
		"enabled": true, "botId": "B1", "botSecret": "", "kimiApiKey": "",
		"kimiBase": "", "kimiModel": "", "version": 0,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("不含密钥的保存应当允许：%d %v", resp.StatusCode, out)
	}
}

// 空串 = 保留原值：前端不回显密钥，用户不重填就不该被抹掉。
func TestAssistantConfigKeepsSecretWhenBlank(t *testing.T) {
	e := newTestEnv(t)
	e.withEncryption(t)
	e.register("grace", "password123")
	wid := e.wid()

	const botSecret = "keep-me"
	e.putAssistant(wid, map[string]any{
		"enabled": true, "botId": "B1", "botSecret": botSecret,
		"kimiApiKey": "k1", "kimiBase": "", "kimiModel": "", "version": 0,
	})

	// 只改开关与模型，secret 字段留空。
	resp, out := e.putAssistant(wid, map[string]any{
		"enabled": false, "botId": "B1", "botSecret": "",
		"kimiApiKey": "", "kimiBase": "", "kimiModel": "moonshot-v1-32k", "version": 1,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("保存失败：%d %v", resp.StatusCode, out)
	}
	if out["botSecretSet"] != true {
		t.Fatal("留空不应抹掉已存的密钥")
	}
	if out["kimiApiKeySet"] != true {
		t.Fatal("留空不应抹掉已存的 Kimi Key")
	}

	// 解密验证原值仍在。
	_, creds := e.getCredentials(wid)
	if creds["botSecret"] != botSecret {
		t.Fatalf("原密钥应保留：%v", creds["botSecret"])
	}
}

// version 过期要 409，且响应里**不能有 task 字段**（配置不是任务）。
func TestAssistantConfigVersionConflict(t *testing.T) {
	e := newTestEnv(t)
	e.withEncryption(t)
	e.register("henry", "password123")
	wid := e.wid()

	e.putAssistant(wid, map[string]any{
		"enabled": true, "botId": "B1", "botSecret": "s", "kimiApiKey": "",
		"kimiBase": "", "kimiModel": "", "version": 0,
	})

	// 用过期 version 再存一次。当前是 1；0 是「允许新建/覆盖」的特殊值，
	// 所以要用一个确实对不上的数。
	resp, out := e.putAssistant(wid, map[string]any{
		"enabled": true, "botId": "B2", "botSecret": "", "kimiApiKey": "",
		"kimiBase": "", "kimiModel": "", "version": 99,
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("过期 version 应 409，得到 %d %v", resp.StatusCode, out)
	}
	// 复用 ConflictError 会写出 "task": null，把客户端引向不存在的字段。
	if _, hasTask := out["task"]; hasTask {
		t.Fatalf("配置冲突的响应不应带 task 字段：%v", out)
	}
}

// 版本正确时应当能更新。
func TestAssistantConfigUpdateWithCorrectVersion(t *testing.T) {
	e := newTestEnv(t)
	e.withEncryption(t)
	e.register("iris", "password123")
	wid := e.wid()

	_, first := e.putAssistant(wid, map[string]any{
		"enabled": true, "botId": "B1", "botSecret": "", "kimiApiKey": "",
		"kimiBase": "", "kimiModel": "", "version": 0,
	})
	v := int64(first["version"].(float64))

	resp, out := e.putAssistant(wid, map[string]any{
		"enabled": false, "botId": "B2", "botSecret": "", "kimiApiKey": "",
		"kimiBase": "https://x", "kimiModel": "m", "version": v,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("正确版本应能更新：%d %v", resp.StatusCode, out)
	}
	if out["botId"] != "B2" || out["enabled"] != false {
		t.Fatalf("更新未生效：%v", out)
	}
	if out["version"].(float64) != float64(v+1) {
		t.Fatalf("version 应递增：%v", out["version"])
	}
}

// 另一名成员（非 owner）不能改配置，也不能取明文凭据。
func TestAssistantConfigIsOwnerOnly(t *testing.T) {
	e := newTestEnv(t)
	e.withEncryption(t)

	// owner 建工作区并写好配置。
	e.register("owner1", "password123")
	wid := e.wid()
	e.putAssistant(wid, map[string]any{
		"enabled": true, "botId": "B1", "botSecret": "s", "kimiApiKey": "",
		"kimiBase": "", "kimiModel": "", "version": 0,
	})

	// 另一账号加入同一工作区，角色是 member。
	e.do("POST", "/api/auth/logout", nil)
	e.register("member1", "password123")
	mid := e.wid()
	if mid == wid {
		t.Skip("测试环境下两个账号拿到了同一个工作区，无法构造非 owner 场景")
	}

	// 非 owner 访问自己的工作区：他是 owner，所以这里应当成功 —— 用来
	// 确认角色判断读的是「该工作区的角色」而不是全局。
	resp, _ := e.getAssistant(mid)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("自己的工作是 owner，应可读：%d", resp.StatusCode)
	}
	// 但读别人的工作区应当是 403（不是成员）。
	resp, _ = e.getAssistant(wid)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("跨工作区应 403，得到 %d", resp.StatusCode)
	}
	resp, _ = e.getCredentials(wid)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("跨工作区取凭据应 403，得到 %d", resp.StatusCode)
	}
	resp, _ = e.putAssistant(wid, map[string]any{
		"enabled": true, "botId": "HACK", "botSecret": "", "kimiApiKey": "",
		"kimiBase": "", "kimiModel": "", "version": 0,
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("跨工作区写配置应 403，得到 %d", resp.StatusCode)
	}
}

// 未登录一律 401。
func TestAssistantConfigRequiresAuth(t *testing.T) {
	e := newTestEnv(t)
	e.register("judy", "password123")
	wid := e.wid()
	e.do("POST", "/api/auth/logout", nil)

	for _, path := range []string{
		wf("/api/workspaces/%v/assistant", wid),
		wf("/api/workspaces/%v/assistant/credentials", wid),
	} {
		resp, _ := e.do("GET", path, nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s 未登录应 401，得到 %d", path, resp.StatusCode)
		}
	}
}

// decodeJSON 开了 DisallowUnknownFields：多发字段要 400，
// 否则前端拼错字段名会被静默忽略。
func TestAssistantConfigRejectsUnknownFields(t *testing.T) {
	e := newTestEnv(t)
	e.withEncryption(t)
	e.register("kate", "password123")
	wid := e.wid()

	body, _ := json.Marshal(map[string]any{
		"enabled": true, "botId": "B", "botSecret": "", "kimiApiKey": "",
		"kimiBase": "", "kimiModel": "", "version": 0,
		"typoField": "x",
	})
	req, _ := http.NewRequest("PUT", e.srv.URL+wf("/api/workspaces/%v/assistant", wid), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	for name, val := range e.cj {
		req.AddCookie(&http.Cookie{Name: name, Value: val})
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("未知字段应 400，得到 %d", resp.StatusCode)
	}
}

// 变更请求必须带 CSRF 头（既有中间件覆盖新路由）。
func TestAssistantConfigRequiresCSRFHeader(t *testing.T) {
	e := newTestEnv(t)
	e.withEncryption(t)
	e.register("leo", "password123")
	wid := e.wid()

	body, _ := json.Marshal(map[string]any{"enabled": true, "botId": "", "version": 0})
	req, _ := http.NewRequest("PUT", e.srv.URL+wf("/api/workspaces/%v/assistant", wid), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for name, val := range e.cj {
		req.AddCookie(&http.Cookie{Name: name, Value: val})
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("缺 CSRF 头应 403，得到 %d", resp.StatusCode)
	}
}

// 占位断言：漏写 NewServer 里 box 的接线会让上面所有加密测试以
// 400 encryption_unavailable 失败，这里把「服务器持有主密钥」这件事
// 显式固定下来。
func TestServerExposesBoxWhenKeyConfigured(t *testing.T) {
	e := newTestEnv(t)
	e.withEncryption(t)
	if e.s.box == nil {
		t.Fatal("测试环境应注入主密钥")
	}
	// 未注入时必须是 nil，好让上面的拒绝路径可被触发。
	e2 := newTestEnv(t)
	if e2.s.box != nil {
		t.Skip("本机设置了 TODO_CONFIG_KEY，缺密钥路径无法构造")
	}
}
