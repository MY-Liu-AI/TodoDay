package store

import (
	"context"
	"errors"
	"testing"
)

// assistantTestEnv 建一个内存库并注册一个用户/工作区。
func assistantTestEnv(t *testing.T) (*Store, int64, int64) {
	t.Helper()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	u, err := st.CreateUser(ctx, "owner1", "hash")
	if err != nil {
		t.Fatal(err)
	}
	ws, err := st.CreateWorkspaceWithOwner(ctx, u.ID, "个人工作区", "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	return st, ws.ID, u.ID
}

func TestAssistantConfigNotFoundInitially(t *testing.T) {
	st, wsID, _ := assistantTestEnv(t)
	if _, err := st.GetAssistantConfig(context.Background(), wsID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("未配置时应返回 ErrNotFound，得到 %v", err)
	}
}

func TestSaveAssistantConfigCreatesThenUpdates(t *testing.T) {
	st, wsID, uID := assistantTestEnv(t)
	ctx := context.Background()

	// 首次写入。
	cfg, err := st.SaveAssistantConfig(ctx, wsID, uID, AssistantConfigInput{
		Enabled: true, BotID: "B1", BotSecretEnc: "enc-bot", KimiAPIKeyEnc: "enc-kimi",
		KimiBase: "https://api.moonshot.cn/v1", KimiModel: "moonshot-v1-8k",
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Version != 1 {
		t.Fatalf("首次写入 version 应为 1，得到 %d", cfg.Version)
	}
	if cfg.BotSecretEnc != "enc-bot" || cfg.KimiAPIKeyEnc != "enc-kimi" {
		t.Fatalf("密文未写入：%+v", cfg)
	}

	// 带上正确版本再更新。
	cfg2, err := st.SaveAssistantConfig(ctx, wsID, uID, AssistantConfigInput{
		Enabled: false, BotID: "B2",
	}, cfg.Version)
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.Version != 2 {
		t.Fatalf("version 应递增到 2，得到 %d", cfg2.Version)
	}
	if cfg2.BotID != "B2" || cfg2.Enabled {
		t.Fatalf("更新未生效：%+v", cfg2)
	}
}

// 空密文 = 保留原值。这是「前端不回显密钥」能成立的前提。
func TestSaveAssistantConfigKeepsExistingSecretsWhenBlank(t *testing.T) {
	st, wsID, uID := assistantTestEnv(t)
	ctx := context.Background()

	st.SaveAssistantConfig(ctx, wsID, uID, AssistantConfigInput{
		Enabled: true, BotID: "B1", BotSecretEnc: "enc-bot", KimiAPIKeyEnc: "enc-kimi",
	}, 0)

	// 只改非密钥字段，密文留空。
	cfg, err := st.SaveAssistantConfig(ctx, wsID, uID, AssistantConfigInput{
		Enabled: false, BotID: "B2", KimiModel: "moonshot-v1-32k",
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BotSecretEnc != "enc-bot" {
		t.Fatalf("留空不应抹掉已存的密文，得到 %q", cfg.BotSecretEnc)
	}
	if cfg.KimiAPIKeyEnc != "enc-kimi" {
		t.Fatalf("留空不应抹掉已存的 Kimi 密文，得到 %q", cfg.KimiAPIKeyEnc)
	}
	if cfg.BotID != "B2" || cfg.KimiModel != "moonshot-v1-32k" {
		t.Fatalf("非密钥字段应被更新：%+v", cfg)
	}
}

// 传入新密文时要真的覆盖。
func TestSaveAssistantConfigReplacesSecretsWhenProvided(t *testing.T) {
	st, wsID, uID := assistantTestEnv(t)
	ctx := context.Background()

	st.SaveAssistantConfig(ctx, wsID, uID, AssistantConfigInput{
		BotSecretEnc: "old", KimiAPIKeyEnc: "old-kimi",
	}, 0)

	cfg, err := st.SaveAssistantConfig(ctx, wsID, uID, AssistantConfigInput{
		BotSecretEnc: "new", KimiAPIKeyEnc: "new-kimi",
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BotSecretEnc != "new" || cfg.KimiAPIKeyEnc != "new-kimi" {
		t.Fatalf("新密文未覆盖旧值：%+v", cfg)
	}
}

// 版本过期要报错，且必须是 ErrConfigConflict（不是 ErrConflict）——
// API 层据此返回不带 task 字段的 409。
func TestSaveAssistantConfigDetectsVersionConflict(t *testing.T) {
	st, wsID, uID := assistantTestEnv(t)
	ctx := context.Background()

	st.SaveAssistantConfig(ctx, wsID, uID, AssistantConfigInput{Enabled: true}, 0)

	_, err := st.SaveAssistantConfig(ctx, wsID, uID, AssistantConfigInput{Enabled: false}, 99)
	if err == nil {
		t.Fatal("过期 version 应当报错")
	}
	if !errors.Is(err, ErrConfigConflict) {
		t.Fatalf("应当是 ErrConfigConflict，得到 %v", err)
	}
	// 不应被误判成任务冲突，否则 mapStoreError 会写出 "task" 字段。
	if errors.Is(err, ErrConflict) {
		t.Fatal("不应与任务冲突共用 sentinel")
	}

	// 冲突时不能改动已存数据。
	cfg, err := st.GetAssistantConfig(ctx, wsID)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled {
		t.Fatal("冲突的写入不应修改已存配置")
	}
	if cfg.Version != 1 {
		t.Fatalf("冲突的写入不应改变 version，得到 %d", cfg.Version)
	}
}

// 首次写入时若客户端声称已有版本，说明它看到的是别处的旧状态 —— 也按冲突处理。
func TestSaveAssistantConfigFirstWriteWithVersionIsConflict(t *testing.T) {
	st, wsID, uID := assistantTestEnv(t)
	ctx := context.Background()

	_, err := st.SaveAssistantConfig(ctx, wsID, uID, AssistantConfigInput{Enabled: true}, 5)
	if !errors.Is(err, ErrConfigConflict) {
		t.Fatalf("首次写入带版本应视为冲突，得到 %v", err)
	}
}

// 一个工作区只能有一份配置：并发/重复写入不会产生第二行。
func TestAssistantConfigIsSingleRowPerWorkspace(t *testing.T) {
	st, wsID, uID := assistantTestEnv(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if _, err := st.SaveAssistantConfig(ctx, wsID, uID, AssistantConfigInput{BotID: "B"}, int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(1) FROM workspace_assistant_config WHERE workspace_id = ?`, wsID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("每个工作区应只有一行配置，得到 %d 行", n)
	}
}

// 删除工作区应级联删除配置（ON DELETE CASCADE）。
func TestAssistantConfigCascadesOnWorkspaceDelete(t *testing.T) {
	st, wsID, uID := assistantTestEnv(t)
	ctx := context.Background()

	if _, err := st.SaveAssistantConfig(ctx, wsID, uID, AssistantConfigInput{BotID: "B"}, 0); err != nil {
		t.Fatal(err)
	}
	// workspace_members 有外键约束，先删成员再删工作区。
	if _, err := st.DB().ExecContext(ctx, `DELETE FROM workspace_members WHERE workspace_id = ?`, wsID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx, `DELETE FROM workspaces WHERE id = ?`, wsID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(1) FROM workspace_assistant_config WHERE workspace_id = ?`, wsID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("删除工作区后配置应被级联删除")
	}
}
