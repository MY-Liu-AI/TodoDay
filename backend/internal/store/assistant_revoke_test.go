package store

import (
	"context"
	"testing"
)

// 撤销的存储层语义。两条必须同时成立，任何一条单独成立都不够：
//
//	不带清除标志 + 空密文 → 保留原值（前端不回显密钥，没重填不能算删除）
//	带清除标志             → 真的删掉
//
// 只有前者会「无法撤销」；只有后者会「误删密钥」。
func TestSaveAssistantConfigRevokeSemantics(t *testing.T) {
	st, wsID, uID := assistantTestEnv(t)
	ctx := context.Background()

	if _, err := st.SaveAssistantConfig(ctx, wsID, uID, AssistantConfigInput{
		Enabled: true, BotID: "B1", BotSecretEnc: "enc-bot", KimiAPIKeyEnc: "enc-kimi",
	}, 0); err != nil {
		t.Fatal(err)
	}

	// 1) 留空但不清除 → 保留。
	cfg, err := st.SaveAssistantConfig(ctx, wsID, uID, AssistantConfigInput{
		Enabled: true, BotID: "B1", KimiModel: "m2",
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BotSecretEnc != "enc-bot" || cfg.KimiAPIKeyEnc != "enc-kimi" {
		t.Fatalf("未要求清除时不应删除密文：%+v", cfg)
	}

	// 2) 显式清除 → 真的删掉。
	cfg, err = st.SaveAssistantConfig(ctx, wsID, uID, AssistantConfigInput{
		Enabled: true, BotID: "B1", ClearBotSecret: true,
	}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BotSecretEnc != "" {
		t.Fatalf("清除后机器人密文应为空，得到 %q", cfg.BotSecretEnc)
	}
	if cfg.KimiAPIKeyEnc != "enc-kimi" {
		t.Fatalf("未要求清除的 Kimi 密文应保留，得到 %q", cfg.KimiAPIKeyEnc)
	}

	// 3) 同时给「清除」和「新值」是矛盾输入，以清除为准。
	//
	//    方向的选择有理由：删掉之后凭据缺失是可见的（助手立刻报没有凭据），
	//    而留着一份用户以为已删的凭据是静默的危险，正是要堵的 fail-open。
	cfg, err = st.SaveAssistantConfig(ctx, wsID, uID, AssistantConfigInput{
		BotSecretEnc: "enc-new", ClearBotSecret: true,
	}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BotSecretEnc != "" {
		t.Fatalf("矛盾输入时应以清除为准，得到 %q", cfg.BotSecretEnc)
	}
}

// 首次写入时带清除标志应当是安全的空操作，不能报错、也不能留下垃圾。
func TestSaveAssistantConfigRevokeOnFirstWrite(t *testing.T) {
	st, wsID, uID := assistantTestEnv(t)
	ctx := context.Background()

	cfg, err := st.SaveAssistantConfig(ctx, wsID, uID, AssistantConfigInput{
		Enabled: true, ClearBotSecret: true, ClearKimiAPIKey: true,
	}, 0)
	if err != nil {
		t.Fatalf("首次写入带清除标志不应报错：%v", err)
	}
	if cfg.BotSecretEnc != "" || cfg.KimiAPIKeyEnc != "" {
		t.Fatalf("不应有密文：%+v", cfg)
	}
}

// 撤销之后仍能重新配置 —— 否则用户撤销一次就再也填不回来了。
func TestSaveAssistantConfigCanReconfigureAfterRevoke(t *testing.T) {
	st, wsID, uID := assistantTestEnv(t)
	ctx := context.Background()

	if _, err := st.SaveAssistantConfig(ctx, wsID, uID, AssistantConfigInput{
		BotSecretEnc: "enc-1",
	}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveAssistantConfig(ctx, wsID, uID, AssistantConfigInput{
		ClearBotSecret: true,
	}, 1); err != nil {
		t.Fatal(err)
	}
	cfg, err := st.SaveAssistantConfig(ctx, wsID, uID, AssistantConfigInput{
		BotSecretEnc: "enc-2",
	}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BotSecretEnc != "enc-2" {
		t.Fatalf("撤销后应能重新配置，得到 %q", cfg.BotSecretEnc)
	}
}
