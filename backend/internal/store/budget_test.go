package store

import (
	"context"
	"errors"
	"testing"
)

// 分类预算契约：upsert 按分类收敛（建/改/幂等）、列表稳定序、删除、
// 校验（空分类 / 非法金额）、跨工作区隔离。
func TestLedgerBudgetUpsertAndList(t *testing.T) {
	st, wsID, _ := ledgerTestStore(t)
	ctx := context.Background()

	b1, err := st.UpsertLedgerBudget(ctx, wsID, "餐饮", 300000)
	if err != nil {
		t.Fatal(err)
	}
	if b1.ID == 0 || b1.Category != "餐饮" || b1.AmountFen != 300000 || b1.CreatedAt == "" {
		t.Fatalf("字段不对：%+v", b1)
	}

	// 同分类再写：改金额而不是新建（LWW），id 不变。
	b2, err := st.UpsertLedgerBudget(ctx, wsID, "餐饮", 500000)
	if err != nil {
		t.Fatal(err)
	}
	if b2.ID != b1.ID || b2.AmountFen != 500000 {
		t.Fatalf("upsert 应改原行：%+v", b2)
	}
	list, err := st.ListLedgerBudgets(ctx, wsID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("应只有 1 条预算：%v", list)
	}

	// 插入序 = 列表序（第二个分类排后）。
	if _, err := st.UpsertLedgerBudget(ctx, wsID, "购物", 100000); err != nil {
		t.Fatal(err)
	}
	list, _ = st.ListLedgerBudgets(ctx, wsID)
	if len(list) != 2 || list[0].Category != "餐饮" || list[1].Category != "购物" {
		t.Fatalf("列表顺序应为插入序：%v", list)
	}

	// 跨工作区隔离。
	u2, err := st.CreateUser(ctx, "budget-other", "x")
	if err != nil {
		t.Fatal(err)
	}
	ws2, err := st.CreateWorkspaceWithOwner(ctx, u2.ID, "另一个", "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertLedgerBudget(ctx, ws2.ID, "餐饮", 99900); err != nil {
		t.Fatal(err)
	}
	list, _ = st.ListLedgerBudgets(ctx, wsID)
	if len(list) != 2 {
		t.Fatalf("工作区 1 不应看到工作区 2 的预算：%v", list)
	}
}

func TestLedgerBudgetDelete(t *testing.T) {
	st, wsID, _ := ledgerTestStore(t)
	ctx := context.Background()

	if _, err := st.UpsertLedgerBudget(ctx, wsID, "餐饮", 300000); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteLedgerBudget(ctx, wsID, "餐饮"); err != nil {
		t.Fatal(err)
	}
	list, _ := st.ListLedgerBudgets(ctx, wsID)
	if len(list) != 0 {
		t.Fatalf("删除后应为空：%v", list)
	}
	// 重复删除 → 404 语义。
	if err := st.DeleteLedgerBudget(ctx, wsID, "餐饮"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("重复删除应 ErrNotFound，得到 %v", err)
	}
}

func TestLedgerBudgetValidation(t *testing.T) {
	st, wsID, _ := ledgerTestStore(t)
	ctx := context.Background()

	cases := []struct {
		category string
		amount   int64
	}{
		{"", 100},
		{"餐饮", 0},
		{"餐饮", -1},
		{"餐饮", maxLedgerAmountFen + 1},
	}
	for i, c := range cases {
		if _, err := st.UpsertLedgerBudget(ctx, wsID, c.category, c.amount); !errors.Is(err, ErrInvalid) {
			t.Fatalf("用例 %d 应 ErrInvalid，得到 %v", i, err)
		}
	}
	if err := st.DeleteLedgerBudget(ctx, wsID, " "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("空分类删除应 ErrInvalid，得到 %v", err)
	}
}
