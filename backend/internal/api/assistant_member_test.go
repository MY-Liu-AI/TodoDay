package api

import (
	"net/http"
	"testing"
	"time"
)

// withWorkspaceOwner 的核心场景：**同一个工作区里的普通成员**。
//
// 这条必须单独测，因为跨工作区的 403 是 withWorkspace 的成员校验挡下的，
// 跟角色判断无关 —— 只测跨工作区的话，即使 withWorkspaceOwner 完全没写
// 角色判断（把 owner 检查删掉），测试也照样全绿。
func TestAssistantConfigMemberCannotWriteOrReadSecrets(t *testing.T) {
	e := newTestEnv(t)
	e.withEncryption(t)
	ctx := t.Context()

	// owner 建工作区并写好配置。
	e.register("owner2", "password123")
	wid := e.wid()
	if resp, out := e.putAssistant(wid, map[string]any{
		"botId": "BOT-OWNER", "botSecret": "owner-secret",
		"kimiApiKey": "owner-kimi", "kimiBase": "", "kimiModel": "", "version": 0,
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("owner 保存应成功：%d %v", resp.StatusCode, out)
	}

	// 第二个账号，注册后直接插一行 workspace_members 把 TA 加成**同一工作区
	// 的普通成员**（没有邀请接口，只能直接写库 —— 这是构造该场景的唯一办法）。
	e.do("POST", "/api/auth/logout", nil)
	e.register("member2", "password123")
	_, me := e.do("GET", "/api/me", nil)
	memberID := int64(me["user"].(map[string]any)["id"].(float64))

	if _, err := e.s.st.DB().ExecContext(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role, joined_at) VALUES (?, ?, 'member', ?)`,
		int64(wid), memberID, time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z")); err != nil {
		t.Fatalf("插入成员失败：%v", err)
	}

	// 先确认 TA 确实已经**是成员**：掩码视图读得到。
	// 没有这一步，后面的 403 就可能只是「不是成员」造成的，又回到上面那个陷阱。
	resp, out := e.getAssistant(wid)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("成员应能读掩码视图（否则测的就不是角色判断），得到 %d %v", resp.StatusCode, out)
	}
	if out["botSecretSet"] != true || out["botId"] != "BOT-OWNER" {
		t.Fatalf("成员应看到已配置的掩码状态：%v", out)
	}
	// 掩码视图本身也不能泄露密钥。
	if s, _ := out["botSecret"].(string); s != "" {
		t.Fatalf("掩码视图不应包含密钥：%v", out)
	}

	// 但不能改配置。
	resp, _ = e.putAssistant(wid, map[string]any{
		"botId": "HACKED", "botSecret": "hacked",
		"kimiApiKey": "", "kimiBase": "", "kimiModel": "", "version": 1,
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("成员写配置应 403，得到 %d", resp.StatusCode)
	}

	// 也不能取明文凭据 —— 能拿到密钥就等于能冒充这个机器人。
	resp, _ = e.getCredentials(wid)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("成员取明文凭据应 403，得到 %d", resp.StatusCode)
	}

	// 被拒的写入不能改动任何东西。
	_, out = e.getAssistant(wid)
	if out["botId"] != "BOT-OWNER" {
		t.Fatalf("被拒的写入不应改动配置：%v", out["botId"])
	}
	if out["version"].(float64) != 1 {
		t.Fatalf("被拒的写入不应改变 version：%v", out["version"])
	}

	// 换回 owner：仍能取明文，证明上面的 403 确实是角色造成的，
	// 而不是这条链路本身坏了。
	e.do("POST", "/api/auth/logout", nil)
	if resp, _ := e.do("POST", "/api/auth/login", map[string]string{
		"username": "owner2", "password": "password123",
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("owner 重新登录失败：%d", resp.StatusCode)
	}
	resp, creds := e.getCredentials(wid)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("owner 应能取明文凭据：%d %v", resp.StatusCode, creds)
	}
	if creds["botSecret"] != "owner-secret" {
		t.Fatalf("owner 取回的密钥不正确：%v", creds["botSecret"])
	}
}

// 跨工作区（根本不是成员）走的是 withWorkspace 的成员校验，
// 与角色无关。保留这条是为了区分两种 403 的来源。
func TestAssistantConfigCrossWorkspaceForbidden(t *testing.T) {
	e := newTestEnv(t)
	e.withEncryption(t)

	e.register("owner3", "password123")
	wid := e.wid()
	e.putAssistant(wid, map[string]any{
		"botId": "B", "botSecret": "s", "kimiApiKey": "",
		"kimiBase": "", "kimiModel": "", "version": 0,
	})

	e.do("POST", "/api/auth/logout", nil)
	e.register("outsider3", "password123")
	other := e.wid()

	if resp, _ := e.getAssistant(other); resp.StatusCode != http.StatusOK {
		t.Fatalf("自己的工作区应可读：%d", resp.StatusCode)
	}
	for _, tc := range []struct {
		name string
		call func() (*http.Response, map[string]any)
	}{
		{"读掩码", func() (*http.Response, map[string]any) { return e.getAssistant(wid) }},
		{"取凭据", func() (*http.Response, map[string]any) { return e.getCredentials(wid) }},
		{"写配置", func() (*http.Response, map[string]any) {
			return e.putAssistant(wid, map[string]any{
				"botId": "X", "botSecret": "", "kimiApiKey": "",
				"kimiBase": "", "kimiModel": "", "version": 0,
			})
		}},
	} {
		if resp, _ := tc.call(); resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s 跨工作区应 403，得到 %d", tc.name, resp.StatusCode)
		}
	}
}
