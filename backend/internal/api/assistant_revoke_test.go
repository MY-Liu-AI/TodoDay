package api

import (
	"net/http"
	"testing"
)

// 撤销。这一组针对的是安全复核提出的 fail-open 状态漂移：
// 若「清除密钥」无法表达，或清除后助手还能拿旧凭据跑，撤销就等于没撤。

// 显式清除后：掩码显示未设置，凭据接口返回空 —— 而且原密文确实从库里删掉了。
func TestAssistantConfigRevokeSecrets(t *testing.T) {
	e := newTestEnv(t)
	e.withEncryption(t)
	ctx := t.Context()

	e.register("revoker", "password123")
	wid := e.wid()

	const botSecret = "SECRET-TO-BE-REVOKED"
	const kimiKey = "sk-KIMI-TO-BE-REVOKED"
	if resp, out := e.putAssistant(wid, map[string]any{
		"botId": "B1", "botSecret": botSecret, "kimiApiKey": kimiKey,
		"kimiBase": "", "kimiModel": "", "version": 0,
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("保存失败：%d %v", resp.StatusCode, out)
	}

	// 撤销两个密钥（只给清除标志，不带新值）。
	resp, out := e.putAssistant(wid, map[string]any{
		"botId": "B1", "botSecret": "", "kimiApiKey": "",
		"kimiBase": "", "kimiModel": "", "version": 1,
		"clearBotSecret": true, "clearKimiApiKey": true,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("撤销失败：%d %v", resp.StatusCode, out)
	}
	if out["botSecretSet"] != false || out["kimiApiKeySet"] != false {
		t.Fatalf("撤销后应显示未设置：%v", out)
	}

	// 库里的密文必须真的没了，而不只是掩码显示成空。
	cfg, err := e.s.st.GetAssistantConfig(ctx, int64(wid))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BotSecretEnc != "" || cfg.KimiAPIKeyEnc != "" {
		t.Fatalf("撤销后密文应被删除，得到 bot=%q kimi=%q", cfg.BotSecretEnc, cfg.KimiAPIKeyEnc)
	}

	// 凭据接口不再返回那个值。
	resp, creds := e.getCredentials(wid)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("取凭据失败：%d", resp.StatusCode)
	}
	if creds["botSecret"] != "" || creds["kimiApiKey"] != "" {
		t.Fatalf("撤销后凭据应为空，得到 %v / %v", creds["botSecret"], creds["kimiApiKey"])
	}
}

// 只撤销其中一个时，另一个必须原封不动 —— 别把用户没让删的一起删了。
func TestAssistantConfigRevokeOnlyRequested(t *testing.T) {
	e := newTestEnv(t)
	e.withEncryption(t)

	e.register("partial", "password123")
	wid := e.wid()
	e.putAssistant(wid, map[string]any{
		"botId": "B1", "botSecret": "keep-this",
		"kimiApiKey": "delete-this", "kimiBase": "", "kimiModel": "", "version": 0,
	})

	resp, out := e.putAssistant(wid, map[string]any{
		"botId": "B1", "botSecret": "", "kimiApiKey": "",
		"kimiBase": "", "kimiModel": "", "version": 1,
		"clearKimiApiKey": true,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("撤销失败：%d %v", resp.StatusCode, out)
	}
	if out["botSecretSet"] != true {
		t.Fatal("没要求清除的机器人 Secret 不应被删掉")
	}
	if out["kimiApiKeySet"] != false {
		t.Fatal("被要求清除的 Kimi Key 应当已删除")
	}

	_, creds := e.getCredentials(wid)
	if creds["botSecret"] != "keep-this" {
		t.Fatalf("保留的密钥应完好：%v", creds["botSecret"])
	}
	if creds["kimiApiKey"] != "" {
		t.Fatalf("已撤销的密钥不应还在：%v", creds["kimiApiKey"])
	}
}

// 撤销是「删除」而不是「存储」，所以缺主密钥时也必须能撤销 ——
// 否则主密钥一丢，用户连清理自己凭据的办法都没有。
func TestAssistantConfigRevokeWorksWithoutMasterKey(t *testing.T) {
	e := newTestEnv(t) // 刻意不装加密
	e.register("nokey-revoke", "password123")
	wid := e.wid()

	resp, out := e.putAssistant(wid, map[string]any{
		"botId": "", "botSecret": "", "kimiApiKey": "",
		"kimiBase": "", "kimiModel": "", "version": 0,
		"clearBotSecret": true, "clearKimiApiKey": true,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("缺主密钥时也应能撤销：%d %v", resp.StatusCode, out)
	}
}

// 撤销不能被「留空保留原值」的语义吃掉：不带清除标志时，留空仍必须保留。
// 这两条语义必须同时成立，否则要么无法撤销、要么会误删。
func TestAssistantConfigBlankWithoutClearStillKeeps(t *testing.T) {
	e := newTestEnv(t)
	e.withEncryption(t)

	e.register("keepblank", "password123")
	wid := e.wid()
	e.putAssistant(wid, map[string]any{
		"botId": "B1", "botSecret": "must-survive",
		"kimiApiKey": "", "kimiBase": "", "kimiModel": "", "version": 0,
	})

	resp, out := e.putAssistant(wid, map[string]any{
		"botId": "B1", "botSecret": "", "kimiApiKey": "",
		"kimiBase": "", "kimiModel": "", "version": 1,
		// 没有 clearBotSecret
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("保存失败：%d %v", resp.StatusCode, out)
	}
	if out["botSecretSet"] != true {
		t.Fatal("未勾选清除时，留空必须保留原密钥")
	}
	_, creds := e.getCredentials(wid)
	if creds["botSecret"] != "must-survive" {
		t.Fatalf("原密钥应保留：%v", creds["botSecret"])
	}
}
