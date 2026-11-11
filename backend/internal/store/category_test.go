package store

import (
	"context"
	"errors"
	"testing"
)

// 首次读取播种默认六分类（用户指定：餐饮/交通/购物/住房/娱乐/其他），
// 色板六色各占一席；重复读取不再播种。
func TestListLedgerCategoriesSeedsDefaults(t *testing.T) {
	st, wsID, _ := ledgerTestStore(t)
	ctx := context.Background()

	list, err := st.ListLedgerCategories(ctx, wsID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 6 {
		t.Fatalf("默认应播种 6 个分类，得到 %d", len(list))
	}
	want := []struct{ name, color string }{
		{"餐饮", "peach"}, {"交通", "slate"}, {"购物", "coral"},
		{"住房", "teal"}, {"娱乐", "cyan"}, {"其他", "yellow"},
	}
	for i, w := range want {
		if list[i].Name != w.name || list[i].Color != w.color || list[i].Position != i {
			t.Fatalf("第 %d 个分类不符：%+v，想要 %s/%s", i, list[i], w.name, w.color)
		}
	}
	again, err := st.ListLedgerCategories(ctx, wsID)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 6 || again[5].ID != list[5].ID {
		t.Fatalf("重复读取不应重复播种：%+v", again)
	}
}

// 新增：排到末尾、颜色按色板轮转；重名/空名拒绝（ErrInvalid）；
// 跨工作区同名不冲突。
func TestAddLedgerCategory(t *testing.T) {
	st, wsID, _ := ledgerTestStore(t)
	ctx := context.Background()
	if _, err := st.ListLedgerCategories(ctx, wsID); err != nil {
		t.Fatal(err)
	}

	c, err := st.AddLedgerCategory(ctx, wsID, "医疗")
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "医疗" || c.Position != 6 || c.Color != ledgerCategoryPalette[6%len(ledgerCategoryPalette)] {
		t.Fatalf("新分类不符：%+v", c)
	}
	if _, err := st.AddLedgerCategory(ctx, wsID, "  餐饮 "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("重名（含空白）应 ErrInvalid，得到 %v", err)
	}
	if _, err := st.AddLedgerCategory(ctx, wsID, "   "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("空名应 ErrInvalid，得到 %v", err)
	}

	st2, ws2, _ := ledgerTestStore(t)
	if _, err := st2.ListLedgerCategories(ctx, ws2); err != nil {
		t.Fatal(err)
	}
	if _, err := st2.AddLedgerCategory(ctx, ws2, "医疗"); err != nil {
		t.Fatalf("跨工作区重名不应冲突：%v", err)
	}
}

// 改名级联：账本流水（含软删，恢复后可见）与预算行一起改，
// 流水 version +1；无关流水不动；重名拒绝。
func TestRenameLedgerCategoryCascades(t *testing.T) {
	st, wsID, uid := ledgerTestStore(t)
	ctx := context.Background()
	cats, err := st.ListLedgerCategories(ctx, wsID)
	if err != nil {
		t.Fatal(err)
	}
	food := cats[0] // 餐饮

	mk := func(date, cat string, fen int64) *LedgerEntry {
		e, err := st.CreateLedgerEntry(ctx, wsID, uid, LedgerInput{
			Date: date, Type: "exp", Category: cat, AmountFen: fen,
		})
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	e1 := mk("2026-10-25", "餐饮", 1000)
	e2 := mk("2026-10-26", "餐饮", 2000)
	traffic := mk("2026-10-26", "交通", 500)
	if _, err := st.UpsertLedgerBudget(ctx, wsID, "餐饮", 50000); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteLedgerEntries(ctx, wsID, []int64{e2.ID}); err != nil {
		t.Fatal(err)
	}

	renamed, err := st.RenameLedgerCategory(ctx, wsID, food.ID, "吃饭")
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Name != "吃饭" {
		t.Fatalf("改名结果不符：%+v", renamed)
	}

	entries, _, err := st.ListLedger(ctx, wsID, "2026-10")
	if err != nil {
		t.Fatal(err)
	}
	var gotE1, gotTraffic *LedgerEntry
	for i := range entries {
		switch entries[i].ID {
		case e1.ID:
			gotE1 = &entries[i]
		case traffic.ID:
			gotTraffic = &entries[i]
		}
	}
	if gotE1 == nil || gotE1.Category != "吃饭" || gotE1.Version != e1.Version+1 {
		t.Fatalf("活流水应级联改名且 version+1：%+v", gotE1)
	}
	if gotTraffic == nil || gotTraffic.Category != "交通" || gotTraffic.Version != 1 {
		t.Fatalf("无关流水不应被级联：%+v", gotTraffic)
	}
	// 软删行同样级联：恢复回来验证。
	restored, err := st.RestoreLedgerEntries(ctx, wsID, []int64{e2.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(restored) != 1 || restored[0].Category != "吃饭" {
		t.Fatalf("软删流水恢复后应为新名：%+v", restored)
	}
	budgets, err := st.ListLedgerBudgets(ctx, wsID)
	if err != nil {
		t.Fatal(err)
	}
	if len(budgets) != 1 || budgets[0].Category != "吃饭" || budgets[0].AmountFen != 50000 {
		t.Fatalf("预算应级联改名：%+v", budgets)
	}
	if _, err := st.RenameLedgerCategory(ctx, wsID, renamed.ID, "交通"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("改成已存在的名应 ErrInvalid，得到 %v", err)
	}
}

// 删除保护：分类下有流水（含软删）时拒绝并给笔数；空闲分类可删且
// 其预算行一并清掉；删不存在的分类返回 ErrNotFound；跨工作区不可见。
func TestDeleteLedgerCategoryGuards(t *testing.T) {
	st, wsID, uid := ledgerTestStore(t)
	ctx := context.Background()
	cats, err := st.ListLedgerCategories(ctx, wsID)
	if err != nil {
		t.Fatal(err)
	}
	food, other := cats[0], cats[5] // 餐饮（要造流水）、其他（保持空闲）

	if _, err := st.CreateLedgerEntry(ctx, wsID, uid, LedgerInput{
		Date: "2026-10-26", Type: "exp", Category: "餐饮", AmountFen: 100,
	}); err != nil {
		t.Fatal(err)
	}
	err = st.DeleteLedgerCategory(ctx, wsID, food.ID)
	var ciu *CategoryInUseError
	if !errors.As(err, &ciu) || ciu.Count != 1 {
		t.Fatalf("有流水的分类删除应 CategoryInUseError{1}，得到 %v", err)
	}

	if _, err := st.AddLedgerCategory(ctx, wsID, "宠物"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertLedgerBudget(ctx, wsID, "宠物", 30000); err != nil {
		t.Fatal(err)
	}
	list, err := st.ListLedgerCategories(ctx, wsID)
	if err != nil {
		t.Fatal(err)
	}
	var petID int64
	for _, c := range list {
		if c.Name == "宠物" {
			petID = c.ID
		}
	}
	if petID == 0 {
		t.Fatal("找不到「宠物」分类")
	}
	if err := st.DeleteLedgerCategory(ctx, wsID, petID); err != nil {
		t.Fatalf("空闲分类应可删：%v", err)
	}
	budgets, err := st.ListLedgerBudgets(ctx, wsID)
	if err != nil {
		t.Fatal(err)
	}
	if len(budgets) != 0 {
		t.Fatalf("分类删除后其预算应一并清掉：%+v", budgets)
	}
	if err := st.DeleteLedgerCategory(ctx, wsID, other.ID); err != nil {
		t.Fatalf("空闲的「其他」应可删：%v", err)
	}
	if err := st.DeleteLedgerCategory(ctx, wsID, 99999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删不存在的分类应 ErrNotFound，得到 %v", err)
	}

	ws2Row, err := st.CreateWorkspaceWithOwner(ctx, uid, "第二个工作区", "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ListLedgerCategories(ctx, ws2Row.ID); err != nil {
		t.Fatal(err)
	}
	ws2Cats, err := st.ListLedgerCategories(ctx, ws2Row.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteLedgerCategory(ctx, wsID, ws2Cats[0].ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("跨工作区删除应 NotFound，得到 %v", err)
	}
}
