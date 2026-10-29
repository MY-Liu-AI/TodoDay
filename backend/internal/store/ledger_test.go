package store

import (
	"context"
	"errors"
	"testing"
)

func ledgerTestStore(t *testing.T) (*Store, int64, int64) {
	t.Helper()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	u, err := st.CreateUser(ctx, "ledger-user", "x")
	if err != nil {
		t.Fatal(err)
	}
	ws, err := st.CreateWorkspaceWithOwner(ctx, u.ID, "个人工作区", "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	return st, ws.ID, u.ID
}

func mustCreateEntry(t *testing.T, st *Store, wsID, userID int64, in LedgerInput) *LedgerEntry {
	t.Helper()
	e, err := st.CreateLedgerEntry(context.Background(), wsID, userID, in)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestLedgerCreateIdempotent(t *testing.T) {
	st, wsID, userID := ledgerTestStore(t)
	ctx := context.Background()

	e1 := mustCreateEntry(t, st, wsID, userID, LedgerInput{
		Date: "2026-10-26", Type: "exp", Category: "餐饮", AmountFen: 3250, Note: "午餐", ClientID: "c-1",
	})
	if e1.ID == 0 || e1.Version != 1 || e1.Category != "餐饮" || e1.AmountFen != 3250 {
		t.Fatalf("字段不对：%+v", e1)
	}

	// 同 client_id 重放：返回原行，不产生重复。
	e2 := mustCreateEntry(t, st, wsID, userID, LedgerInput{
		Date: "2026-10-27", Type: "exp", Category: "购物", AmountFen: 99900, ClientID: "c-1",
	})
	if e2.ID != e1.ID {
		t.Fatalf("幂等重放应返回原行：%d != %d", e2.ID, e1.ID)
	}
	entries, _, err := st.ListLedger(ctx, wsID, "2026-10")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("重放产生了重复账目：%d 条", len(entries))
	}

	// 空 client_id 不参与幂等：连记两笔各自成行。
	mustCreateEntry(t, st, wsID, userID, LedgerInput{Date: "2026-10-26", Type: "exp", Category: "餐饮", AmountFen: 1200})
	mustCreateEntry(t, st, wsID, userID, LedgerInput{Date: "2026-10-26", Type: "exp", Category: "餐饮", AmountFen: 1200})
	entries, stats, err := st.ListLedger(ctx, wsID, "2026-10")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("应有 3 笔：%d", len(entries))
	}
	// 3250 + 1200 + 1200 = 5650
	if stats.Exp != 5650 || stats.Inc != 0 || stats.Net != -5650 {
		t.Fatalf("汇总不对：%+v", stats)
	}
}

func TestLedgerUpdateConflict(t *testing.T) {
	st, wsID, userID := ledgerTestStore(t)
	ctx := context.Background()
	e := mustCreateEntry(t, st, wsID, userID, LedgerInput{
		Date: "2026-10-26", Type: "exp", Category: "餐饮", AmountFen: 100,
	})

	got, err := st.UpdateLedgerEntry(ctx, wsID, e.ID, LedgerInput{
		Date: "2026-10-26", Type: "inc", AmountFen: 50000,
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != "inc" || got.Category != "" || got.Version != 2 {
		t.Fatalf("更新结果不对：%+v", got)
	}

	// 过期 version → ErrConflict。
	if _, err := st.UpdateLedgerEntry(ctx, wsID, e.ID, LedgerInput{
		Date: "2026-10-26", Type: "exp", Category: "餐饮", AmountFen: 100,
	}, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("过期 version 应 ErrConflict，得到 %v", err)
	}
	// 不存在的 id → ErrNotFound。
	if _, err := st.UpdateLedgerEntry(ctx, wsID, 424242, LedgerInput{
		Date: "2026-10-26", Type: "exp", Category: "餐饮", AmountFen: 100,
	}, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在应 ErrNotFound，得到 %v", err)
	}
}

func TestLedgerDeleteRestore(t *testing.T) {
	st, wsID, userID := ledgerTestStore(t)
	ctx := context.Background()
	a := mustCreateEntry(t, st, wsID, userID, LedgerInput{Date: "2026-10-01", Type: "exp", Category: "餐饮", AmountFen: 1000})
	b := mustCreateEntry(t, st, wsID, userID, LedgerInput{Date: "2026-10-02", Type: "inc", AmountFen: 50000})

	// 软删 a 后列表与汇总都不含它。
	if err := st.DeleteLedgerEntries(ctx, wsID, []int64{a.ID}); err != nil {
		t.Fatal(err)
	}
	entries, stats, err := st.ListLedger(ctx, wsID, "2026-10")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || stats.Inc != 50000 || stats.Exp != 0 {
		t.Fatalf("软删后口径不对：%d 条 %+v", len(entries), stats)
	}

	// 恢复后回来，version 递增。
	restored, err := st.RestoreLedgerEntries(ctx, wsID, []int64{a.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(restored) != 1 || restored[0].ID != a.ID || restored[0].Version != 2 {
		t.Fatalf("恢复结果不对：%+v", restored)
	}
	entries, stats, err = st.ListLedger(ctx, wsID, "2026-10")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || stats.Exp != 1000 || stats.Net != 49000 {
		t.Fatalf("恢复后口径不对：%d 条 %+v", len(entries), stats)
	}

	// 删除不存在的 → ErrNotFound（整体回滚）。
	if err := st.DeleteLedgerEntries(ctx, wsID, []int64{b.ID, 424242}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("批量删除含不存在 id 应 ErrNotFound，得到 %v", err)
	}
	entries, _, err = st.ListLedger(ctx, wsID, "2026-10")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("失败的批量删除必须整体回滚：%d 条", len(entries))
	}
}

func TestLedgerValidation(t *testing.T) {
	st, wsID, userID := ledgerTestStore(t)
	ctx := context.Background()

	bad := []LedgerInput{
		{Date: "2026-10-26", Type: "exp", Category: "餐饮", AmountFen: 0},        // 金额 0
		{Date: "2026-10-26", Type: "exp", Category: "餐饮", AmountFen: -5},       // 负数
		{Date: "2026-10-26", Type: "exp", AmountFen: 100},                        // 支出无分类
		{Date: "2026-10-26", Type: "bogus", AmountFen: 100},                      // 类型非法
		{Date: "2026-10-26", Type: "exp", Category: "餐饮", AmountFen: 100, Note: string(make([]byte, 241))}, // 备注超长（241 字节、241 字符 ASCII）
	}
	for i, in := range bad {
		if _, err := st.CreateLedgerEntry(ctx, wsID, userID, in); !errors.Is(err, ErrInvalid) {
			t.Fatalf("用例 %d 应 ErrInvalid，得到 %v", i, err)
		}
	}
}
