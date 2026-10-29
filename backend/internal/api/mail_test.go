package api

import (
	"net/http"
	"testing"
)

// 邮箱账单导入端点的 API 层测试：权限、掩码视图、授权码加密落库、
// 无凭据启动映射。真实 IMAP 导入流程（memserver + 假 Kimi）在
// mailimport 包内覆盖。

func TestMailConfigViewAndSave(t *testing.T) {
	e := newTestEnv(t)
	e.withEncryption(t)
	e.register("mail-v", "password123")
	wid := e.wid()

	resp, out := e.do("GET", wf("/api/workspaces/%v/mail", wid), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET mail 应 200，得到 %d", resp.StatusCode)
	}
	if out["configured"] != false {
		t.Fatalf("未配置时 configured 应为 false：%v", out)
	}
	if out["authCodeSet"] != false {
		t.Fatalf("未配置时 authCodeSet 应为 false：%v", out)
	}
	// 视图不得泄露任何凭据字段：密文、授权码明文都不存在。
	for _, banned := range []string{"authCodeEnc", "authCode"} {
		if _, ok := out[banned]; ok {
			t.Fatalf("视图不应包含 %s", banned)
		}
	}

	resp, out = e.do("PUT", wf("/api/workspaces/%v/mail", wid), map[string]any{
		"mailbox": "a@qq.com", "authCode": "secret-code", "senders": "service@bank.com|bank.com", "version": 0,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT mail 应 200，得到 %d", resp.StatusCode)
	}
	if out["configured"] != true || out["mailbox"] != "a@qq.com" || out["senders"] != "service@bank.com|bank.com" {
		t.Fatalf("保存后视图不符：%v", out)
	}
	if out["authCodeSet"] != true {
		t.Fatalf("保存授权码后 authCodeSet 应为 true：%v", out)
	}
	if out["enabled"] != false {
		t.Fatalf("保存配置不应改启用状态")
	}

	// 落库的是密文不是明文。
	var enc string
	if err := e.s.st.DB().QueryRowContext(t.Context(),
		`SELECT auth_code_enc FROM workspace_mail_config WHERE workspace_id = ?`, int64(wid)).Scan(&enc); err != nil {
		t.Fatal(err)
	}
	if enc == "" || enc == "secret-code" {
		t.Fatalf("授权码应以密文落库，得到 %q", enc)
	}

	// 留空授权码 = 不修改；显式 clearAuthCode = 删除（可撤销）。
	resp, out = e.do("PUT", wf("/api/workspaces/%v/mail", wid), map[string]any{
		"senders": "x", "version": out["version"],
	})
	if resp.StatusCode != http.StatusOK || out["authCodeSet"] != true {
		t.Fatalf("留空授权码应保留原值：%d %v", resp.StatusCode, out)
	}
	resp, out = e.do("PUT", wf("/api/workspaces/%v/mail", wid), map[string]any{
		"clearAuthCode": true, "version": out["version"],
	})
	if resp.StatusCode != http.StatusOK || out["authCodeSet"] != false {
		t.Fatalf("clearAuthCode 应删除授权码：%d %v", resp.StatusCode, out)
	}

	// 普通成员可读视图，但不能改。
	e.do("POST", "/api/auth/logout", nil)
	e.register("mail-m", "password123")
	_, me := e.do("GET", "/api/me", nil)
	memberID := int64(me["user"].(map[string]any)["id"].(float64))
	if _, err := e.s.st.DB().ExecContext(t.Context(),
		`INSERT INTO workspace_members (workspace_id, user_id, role, joined_at) VALUES (?, ?, 'member', '2026-10-27 00:00:00')`,
		int64(wid), memberID); err != nil {
		t.Fatalf("插入成员失败：%v", err)
	}
	resp, _ = e.do("GET", wf("/api/workspaces/%v/mail", wid), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("成员 GET mail 应 200，得到 %d", resp.StatusCode)
	}
	resp, _ = e.do("PUT", wf("/api/workspaces/%v/mail", wid), map[string]any{"senders": "x", "version": 0})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("成员 PUT mail 应 403，得到 %d", resp.StatusCode)
	}
}

// 主密钥缺失：存新授权码必须 400 encryption_unavailable（绝不退化成明文）；
// 撤销（删除）不需要主密钥。
func TestMailSaveWithoutMasterKey(t *testing.T) {
	e := newTestEnv(t) // 刻意不调 withEncryption
	e.register("mail-k", "password123")
	wid := e.wid()

	resp, out := e.do("PUT", wf("/api/workspaces/%v/mail", wid), map[string]any{
		"mailbox": "a@qq.com", "authCode": "secret-code", "version": 0,
	})
	if resp.StatusCode != http.StatusBadRequest || out["error"] != "encryption_unavailable" {
		t.Fatalf("缺主密钥存授权码应 400 encryption_unavailable，得到 %d %v", resp.StatusCode, out)
	}

	// 没有主密钥时 clearAuthCode 仍应可用：撤销是删除，不是存储。
	resp, out = e.do("PUT", wf("/api/workspaces/%v/mail", wid), map[string]any{"senders": "", "version": 0})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("非机密字段保存不应需要主密钥：%d %v", resp.StatusCode, out)
	}
	resp, out = e.do("PUT", wf("/api/workspaces/%v/mail", wid), map[string]any{
		"clearAuthCode": true, "version": out["version"],
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("缺主密钥时撤销授权码仍应可用：%d %v", resp.StatusCode, out)
	}
}

func TestMailStartRequiresConfig(t *testing.T) {
	e := newTestEnv(t)
	e.register("mail-s", "password123")
	wid := e.wid()

	resp, out := e.do("POST", wf("/api/workspaces/%v/mail/start", wid), nil)
	if resp.StatusCode != http.StatusNotFound || out["error"] != "not_found" {
		t.Fatalf("未配置启动应 404，得到 %d %v", resp.StatusCode, out)
	}
}

// 配置存在但凭据缺失（未填或已撤销）：start 回滚 enabled 并报
// no_credentials——无凭据一律不连接，绝不假装能导入。
func TestMailStartWithoutCredentials(t *testing.T) {
	e := newTestEnv(t)
	e.register("mail-n", "password123")
	wid := e.wid()
	e.do("PUT", wf("/api/workspaces/%v/mail", wid), map[string]any{"senders": "", "version": 0})

	resp, out := e.do("POST", wf("/api/workspaces/%v/mail/start", wid), nil)
	if resp.StatusCode != http.StatusBadRequest || out["error"] != "no_credentials" {
		t.Fatalf("无凭据 start 应 400 no_credentials，得到 %d %v", resp.StatusCode, out)
	}
	// 启动失败应回滚 enabled，避免下次 SyncAll 反复尝试一个永远起不来的配置。
	_, out = e.do("GET", wf("/api/workspaces/%v/mail", wid), nil)
	if out["enabled"] != false {
		t.Fatalf("启动失败后 enabled 应回滚为 false：%v", out)
	}

	// stop 幂等成功。
	resp, _ = e.do("POST", wf("/api/workspaces/%v/mail/stop", wid), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stop 应幂等 200，得到 %d", resp.StatusCode)
	}
}

// 发现发件人：无凭据时如实回 ok:false（HTTP 仍 200）；成员无权调用。
// 真实 IMAP 扫描流程在 mailimport 包内用 memserver 覆盖。
func TestMailDiscoverHonestFailureAndPermission(t *testing.T) {
	e := newTestEnv(t)
	e.register("mail-d", "password123")
	wid := e.wid()

	resp, out := e.do("POST", wf("/api/workspaces/%v/mail/discover", wid), map[string]any{})
	if resp.StatusCode != http.StatusOK || out["ok"] != false {
		t.Fatalf("无凭据 discover 应 200 ok:false，得到 %d %v", resp.StatusCode, out)
	}

	e.do("POST", "/api/auth/logout", nil)
	e.register("mail-dm", "password123")
	_, me := e.do("GET", "/api/me", nil)
	memberID := int64(me["user"].(map[string]any)["id"].(float64))
	if _, err := e.s.st.DB().ExecContext(t.Context(),
		`INSERT INTO workspace_members (workspace_id, user_id, role, joined_at) VALUES (?, ?, 'member', '2026-10-29 00:00:00')`,
		int64(wid), memberID); err != nil {
		t.Fatalf("插入成员失败：%v", err)
	}
	resp, _ = e.do("POST", wf("/api/workspaces/%v/mail/discover", wid), map[string]any{})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("成员 discover 应 403，得到 %d", resp.StatusCode)
	}
}

// 测试连接：没填凭据也无已存凭据时返回 ok:false（HTTP 仍 200，
// 失败是正常结果）；填了表单值则交由服务端用表单值验证。
func TestMailTestEndpointHonestFailure(t *testing.T) {
	e := newTestEnv(t)
	e.register("mail-t", "password123")
	wid := e.wid()

	resp, out := e.do("POST", wf("/api/workspaces/%v/mail/test", wid), map[string]any{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("test 应 200，得到 %d", resp.StatusCode)
	}
	if out["ok"] != false {
		t.Fatalf("无凭据时 ok 应为 false：%v", out)
	}
}
