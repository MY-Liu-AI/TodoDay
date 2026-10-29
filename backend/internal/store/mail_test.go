package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func mailTestEnv(t *testing.T) (*Store, int64, int64) {
	t.Helper()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	u, err := st.CreateUser(t.Context(), "owner", "hash")
	if err != nil {
		t.Fatal(err)
	}
	ws, err := st.CreateWorkspaceWithOwner(t.Context(), u.ID, "测试工作区", "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	return st, ws.ID, u.ID
}

func TestMailConfigCRUD(t *testing.T) {
	st, wsID, uID := mailTestEnv(t)
	ctx := context.Background()

	_, err := st.GetMailConfig(ctx, wsID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("未配置时应 ErrNotFound，得到 %v", err)
	}

	cfg, err := st.SaveMailConfig(ctx, wsID, uID, MailConfigInput{
		Mailbox: "a@agent.qq.com", Senders: "service@bank.com|bank.com",
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Enabled {
		t.Fatalf("新配置应默认停用")
	}
	if cfg.Mailbox != "a@agent.qq.com" || cfg.Senders != "service@bank.com|bank.com" || cfg.Version != 1 {
		t.Fatalf("字段回读不符：%+v", cfg)
	}

	// version 乐观并发：拿过期 version 再存应冲突。
	if _, err := st.SaveMailConfig(ctx, wsID, uID, MailConfigInput{Senders: "x"}, cfg.Version+99); !errors.Is(err, ErrConfigConflict) {
		t.Fatalf("旧 version 应冲突，得到 %v", err)
	}
	// version=0 覆盖当前版本：机器回写 mailbox 的路径。
	cfg2, err := st.SaveMailConfig(ctx, wsID, uID, MailConfigInput{Senders: "x", Mailbox: "b@agent.qq.com"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.Mailbox != "b@agent.qq.com" || cfg2.Senders != "x" {
		t.Fatalf("覆盖保存不符：%+v", cfg2)
	}
	// 留空 mailbox = 保留原值。
	cfg3, err := st.SaveMailConfig(ctx, wsID, uID, MailConfigInput{}, cfg2.Version)
	if err != nil {
		t.Fatal(err)
	}
	if cfg3.Mailbox != "b@agent.qq.com" {
		t.Fatalf("留空 mailbox 应保留原值，得到 %q", cfg3.Mailbox)
	}
	// senders 每次整体写入，没有「留空保留」语义：cfg2 提交 "x" 落 "x"，
	// cfg3 未提交即清空。
	if cfg2.Senders != "x" || cfg3.Senders != "" {
		t.Fatalf("senders 应整体写入，空串即清空：cfg2=%q cfg3=%q", cfg2.Senders, cfg3.Senders)
	}
	// 再次保存即替换清单。
	cfg5, err := st.SaveMailConfig(ctx, wsID, uID, MailConfigInput{Senders: "hr@corp.com"}, cfg3.Version)
	if err != nil {
		t.Fatal(err)
	}
	if cfg5.Senders != "hr@corp.com" {
		t.Fatalf("senders 应整体替换，得到 %q", cfg5.Senders)
	}
	// 显式清除。
	cfg4, err := st.SaveMailConfig(ctx, wsID, uID, MailConfigInput{ClearMailbox: true}, cfg5.Version)
	if err != nil {
		t.Fatal(err)
	}
	if cfg4.Mailbox != "" {
		t.Fatalf("ClearMailbox 应清空，得到 %q", cfg4.Mailbox)
	}
}

// 授权码密文的两条语义：留空 = 保留原值；显式 ClearAuthCode = 删除。
func TestMailAuthCodeSemantics(t *testing.T) {
	st, wsID, uID := mailTestEnv(t)
	ctx := context.Background()

	cfg, err := st.SaveMailConfig(ctx, wsID, uID, MailConfigInput{
		Mailbox: "a@qq.com", AuthCodeEnc: "sealed-1",
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AuthCodeEnc != "sealed-1" {
		t.Fatalf("授权码密文应原样落库：%+v", cfg)
	}

	// 留空 = 保留。
	cfg2, err := st.SaveMailConfig(ctx, wsID, uID, MailConfigInput{Mailbox: "keep@agent.qq.com"}, cfg.Version)
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.AuthCodeEnc != "sealed-1" {
		t.Fatalf("留空授权码应保留原值，得到 %q", cfg2.AuthCodeEnc)
	}

	// 显式清除。
	cfg3, err := st.SaveMailConfig(ctx, wsID, uID, MailConfigInput{ClearAuthCode: true}, cfg2.Version)
	if err != nil {
		t.Fatal(err)
	}
	if cfg3.AuthCodeEnc != "" {
		t.Fatalf("ClearAuthCode 应清空，得到 %q", cfg3.AuthCodeEnc)
	}
}

func TestMailConfigValidation(t *testing.T) {
	st, wsID, uID := mailTestEnv(t)
	ctx := context.Background()
	manySenders := make([]string, 0, 31)
	for i := 0; i < 31; i++ {
		manySenders = append(manySenders, "a@b.com")
	}
	cases := []MailConfigInput{
		{Mailbox: longString(201)},
		{Senders: longString(2001)},
		{Senders: strings.Join(manySenders, "|")},
		{Senders: longString(99) + "@b.com"}, // 单条超 100 字
	}
	for i, in := range cases {
		if _, err := st.SaveMailConfig(ctx, wsID, uID, in, 0); !errors.Is(err, ErrInvalid) {
			t.Errorf("case %d 应校验失败，得到 %v", i, err)
		}
	}
}

func TestMailEnabledAndRunState(t *testing.T) {
	st, wsID, uID := mailTestEnv(t)
	ctx := context.Background()

	if _, err := st.SaveMailConfig(ctx, wsID, uID, MailConfigInput{}, 0); err != nil {
		t.Fatal(err)
	}
	ids, err := st.EnabledMailWorkspaceIDs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("默认停用，不应有启用工作区：%v", ids)
	}
	if err := st.SetMailEnabled(ctx, wsID, true); err != nil {
		t.Fatal(err)
	}
	ids, _ = st.EnabledMailWorkspaceIDs(ctx)
	if len(ids) != 1 || ids[0] != wsID {
		t.Fatalf("启用后应列出该工作区：%v", ids)
	}

	if err := st.RecordMailRun(ctx, wsID, true, "", 3); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordMailRun(ctx, wsID, false, "boom", 0); err != nil {
		t.Fatal(err)
	}
	cfg, err := st.GetMailConfig(ctx, wsID)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LastRunOk == nil || *cfg.LastRunOk != false || cfg.LastRunError != "boom" || cfg.LastImported != 0 || cfg.LastRunAt == "" {
		t.Fatalf("运行状态不符：%+v", cfg)
	}
}

func TestMailSeenDedupe(t *testing.T) {
	st, wsID, _ := mailTestEnv(t)
	ctx := context.Background()

	seen, err := st.MailSeen(ctx, wsID, "msg_1")
	if err != nil || seen {
		t.Fatalf("初始应未处理：seen=%v err=%v", seen, err)
	}
	if err := st.MarkMailSeen(ctx, wsID, "msg_1", 42); err != nil {
		t.Fatal(err)
	}
	seen, _ = st.MailSeen(ctx, wsID, "msg_1")
	if !seen {
		t.Fatalf("标记后应已处理")
	}
	// 幂等：重复标记不报错、不覆盖 entry_id。
	if err := st.MarkMailSeen(ctx, wsID, "msg_1", 99); err != nil {
		t.Fatal(err)
	}
	var entryID *int64
	if err := st.DB().QueryRowContext(ctx,
		`SELECT entry_id FROM mail_seen WHERE workspace_id = ? AND message_id = ?`, wsID, "msg_1").Scan(&entryID); err != nil {
		t.Fatal(err)
	}
	if entryID == nil || *entryID != 42 {
		t.Fatalf("entry_id 不应被覆盖：%v", entryID)
	}
	// 非账单：entry_id 存 NULL。
	if err := st.MarkMailSeen(ctx, wsID, "msg_ad", 0); err != nil {
		t.Fatal(err)
	}
}

func longString(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}
