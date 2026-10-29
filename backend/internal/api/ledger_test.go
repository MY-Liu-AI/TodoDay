package api

import (
	"net/http"
	"testing"
)

// 记账本端到端契约：创建（幂等）/列表汇总/编辑（409）/软删/恢复/校验/越权。
func TestLedgerLifecycle(t *testing.T) {
	e := newTestEnv(t)
	e.register("ledger-user", "password123")
	wid := e.wid()

	// 记一笔支出（带 clientId）。
	resp, out := e.do("POST", wu(wid)+"/ledger", map[string]any{
		"date": "2026-10-26", "type": "exp", "category": "餐饮", "amountFen": 3250, "note": "午餐", "clientId": "tx-1",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d", resp.StatusCode)
	}
	eid := int64(out["id"].(float64))
	if out["type"] != "exp" || out["category"] != "餐饮" || out["amountFen"] != float64(3250) || out["version"] != float64(1) {
		t.Fatalf("字段不对：%v", out)
	}

	// 幂等重放：同 clientId 返回同一行，不产生重复。
	resp, out2 := e.do("POST", wu(wid)+"/ledger", map[string]any{
		"date": "2099-01-01", "type": "inc", "amountFen": 1, "clientId": "tx-1",
	})
	if resp.StatusCode != http.StatusCreated || int64(out2["id"].(float64)) != eid {
		t.Fatalf("幂等重放应返回原行：%d %v", resp.StatusCode, out2)
	}

	// 再记一笔收入、一笔上月支出（月过滤用）。
	e.do("POST", wu(wid)+"/ledger", map[string]any{"date": "2026-10-10", "type": "inc", "amountFen": 1850000, "note": "工资"})
	e.do("POST", wu(wid)+"/ledger", map[string]any{"date": "2026-09-30", "type": "exp", "category": "购物", "amountFen": 12900})

	// 列表：只含当月 2 笔，汇总与列表同口径。
	resp, out = e.do("GET", wu(wid)+"/ledger?month=2026-10", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: %d", resp.StatusCode)
	}
	entries := out["entries"].([]any)
	if len(entries) != 2 {
		t.Fatalf("当月应有 2 笔：%v", out)
	}
	stats := out["stats"].(map[string]any)
	if stats["exp"] != float64(3250) || stats["inc"] != float64(1850000) || stats["net"] != float64(1846750) {
		t.Fatalf("汇总不对：%v", stats)
	}
	resp, out = e.do("GET", wu(wid)+"/ledger?month=2026-09", nil)
	if len(out["entries"].([]any)) != 1 {
		t.Fatalf("9 月应有 1 笔：%v", out)
	}

	// 编辑：version 1 → 2；过期 version → 409。
	resp, out = e.do("PATCH", wu(wid)+"/ledger/"+itoa(eid), map[string]any{
		"date": "2026-10-26", "type": "exp", "category": "餐饮", "amountFen": 3350, "note": "午餐+咖啡", "version": 1,
	})
	if resp.StatusCode != http.StatusOK || out["version"] != float64(2) || out["amountFen"] != float64(3350) {
		t.Fatalf("update: %d %v", resp.StatusCode, out)
	}
	resp, _ = e.do("PATCH", wu(wid)+"/ledger/"+itoa(eid), map[string]any{
		"date": "2026-10-26", "type": "exp", "category": "餐饮", "amountFen": 1, "version": 1,
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("过期 version 应 409，得到 %d", resp.StatusCode)
	}

	// 批量软删 → 列表消失；恢复 → 回来。
	resp, _ = e.do("POST", wu(wid)+"/ledger/delete", map[string]any{"ids": []int64{eid}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	_, out = e.do("GET", wu(wid)+"/ledger?month=2026-10", nil)
	if len(out["entries"].([]any)) != 1 {
		t.Fatalf("软删后应剩 1 笔：%v", out)
	}
	resp, out = e.do("POST", wu(wid)+"/ledger/restore", map[string]any{"ids": []int64{eid}})
	if resp.StatusCode != http.StatusOK || len(out["entries"].([]any)) != 1 {
		t.Fatalf("restore: %d %v", resp.StatusCode, out)
	}
	_, out = e.do("GET", wu(wid)+"/ledger?month=2026-10", nil)
	if len(out["entries"].([]any)) != 2 {
		t.Fatalf("恢复后应回到 2 笔：%v", out)
	}
}

func TestLedgerValidation(t *testing.T) {
	e := newTestEnv(t)
	e.register("ledger-check", "password123")
	wid := e.wid()

	// 支出无分类 / 金额 0 / 类型非法 / 日期非法 / month 非法。
	cases := []map[string]any{
		{"date": "2026-10-26", "type": "exp", "amountFen": 100},
		{"date": "2026-10-26", "type": "exp", "category": "餐饮", "amountFen": 0},
		{"date": "2026-10-26", "type": "gift", "amountFen": 100},
		{"date": "2026-13-40", "type": "exp", "category": "餐饮", "amountFen": 100},
	}
	for i, body := range cases {
		resp, _ := e.do("POST", wu(wid)+"/ledger", body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("用例 %d 应 400，得到 %d", i, resp.StatusCode)
		}
	}
	resp, _ := e.do("GET", wu(wid)+"/ledger?month=2026-13", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("非法 month 应 400，得到 %d", resp.StatusCode)
	}
	resp, _ = e.do("GET", wu(wid)+"/ledger", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("缺 month 应 400，得到 %d", resp.StatusCode)
	}

	// 不存在的账目 → 404。
	resp, _ = e.do("PATCH", wu(wid)+"/ledger/424242", map[string]any{
		"date": "2026-10-26", "type": "exp", "category": "餐饮", "amountFen": 100, "version": 1,
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("不存在账目应 404，得到 %d", resp.StatusCode)
	}
}

// 越权用例：入侵者对他人工作区的账目全部 403。
func TestLedgerCrossWorkspaceDenied(t *testing.T) {
	e := newTestEnv(t)
	e.register("ledger-owner", "password123")
	jarOwner := cloneJar(e.cj)
	widB := e.wid()
	resp, out := e.do("POST", wu(widB)+"/ledger", map[string]any{
		"date": "2026-10-26", "type": "exp", "category": "餐饮", "amountFen": 100,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d", resp.StatusCode)
	}
	eid := int64(out["id"].(float64))

	e.register("ledger-intruder", "password123")
	jarIntruder := cloneJar(e.cj)

	cases := []struct {
		method, path string
		body         any
	}{
		{"GET", wu(widB) + "/ledger?month=2026-10", nil},
		{"POST", wu(widB) + "/ledger", map[string]any{"date": "2026-10-26", "type": "exp", "category": "餐饮", "amountFen": 100}},
		{"PATCH", wu(widB) + "/ledger/" + itoa(eid), map[string]any{"date": "2026-10-26", "type": "exp", "category": "餐饮", "amountFen": 100, "version": 1}},
		{"POST", wu(widB) + "/ledger/delete", map[string]any{"ids": []int64{eid}}},
		{"POST", wu(widB) + "/ledger/restore", map[string]any{"ids": []int64{eid}}},
	}
	e.cj = jarIntruder
	for _, tc := range cases {
		resp, _ := e.do(tc.method, tc.path, tc.body)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("入侵者 %s %s 应 403，得到 %d", tc.method, tc.path, resp.StatusCode)
		}
	}
	e.cj = jarOwner
	resp, _ = e.do("GET", wu(widB)+"/ledger?month=2026-10", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("拥有者读账目应 200，得到 %d", resp.StatusCode)
	}
}

// 分类预算契约：PUT upsert（建/改）、GET 全量、DELETE 按分类、
// 写操作响应回全量列表、校验 400、跨工作区 403、删不存在 404。
// DELETE 路径里的中文分类经 URL 编码，mux 解码后应正确匹配。
func TestLedgerBudgetLifecycle(t *testing.T) {
	e := newTestEnv(t)
	e.register("budget-user", "password123")
	wid := e.wid()

	// 建两条预算。
	resp, out := e.do("PUT", wu(wid)+"/ledger/budgets", map[string]any{"category": "餐饮", "amountFen": 300000})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put: %d", resp.StatusCode)
	}
	budgets := out["budgets"].([]any)
	if len(budgets) != 1 || budgets[0].(map[string]any)["category"] != "餐饮" || budgets[0].(map[string]any)["amountFen"] != float64(300000) {
		t.Fatalf("put 响应应回全量列表：%v", out)
	}
	e.do("PUT", wu(wid)+"/ledger/budgets", map[string]any{"category": "购物", "amountFen": 100000})

	// 同分类再 PUT = 改金额，不产生第二条。
	resp, out = e.do("PUT", wu(wid)+"/ledger/budgets", map[string]any{"category": "餐饮", "amountFen": 500000})
	budgets = out["budgets"].([]any)
	if len(budgets) != 2 {
		t.Fatalf("upsert 后仍应 2 条：%v", out)
	}
	for _, b := range budgets {
		if b.(map[string]any)["category"] == "餐饮" && b.(map[string]any)["amountFen"] != float64(500000) {
			t.Fatalf("改额未生效：%v", out)
		}
	}

	// GET 与写响应同口径。
	resp, out = e.do("GET", wu(wid)+"/ledger/budgets", nil)
	if resp.StatusCode != http.StatusOK || len(out["budgets"].([]any)) != 2 {
		t.Fatalf("get: %d %v", resp.StatusCode, out)
	}

	// 校验：空分类 / 金额 0 / 请求格式错误。
	for i, body := range []map[string]any{
		{"category": "", "amountFen": 100},
		{"category": "餐饮", "amountFen": 0},
	} {
		resp, _ := e.do("PUT", wu(wid)+"/ledger/budgets", body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("用例 %d 应 400，得到 %d", i, resp.StatusCode)
		}
	}

	// 删除（URL 编码的中文分类）；响应回全量列表；再删 → 404。
	resp, out = e.do("DELETE", wu(wid)+"/ledger/budgets/%E9%A4%90%E9%A5%AE", nil)
	if resp.StatusCode != http.StatusOK || len(out["budgets"].([]any)) != 1 {
		t.Fatalf("delete: %d %v", resp.StatusCode, out)
	}
	resp, _ = e.do("DELETE", wu(wid)+"/ledger/budgets/%E9%A4%90%E9%A5%AE", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("删不存在预算应 404，得到 %d", resp.StatusCode)
	}
}

// 预算与账目同边界：其他工作区成员对预算读写删一律 403。
func TestLedgerBudgetCrossWorkspaceDenied(t *testing.T) {
	e := newTestEnv(t)
	e.register("budget-owner", "password123")
	widB := e.wid()
	e.do("PUT", wu(widB)+"/ledger/budgets", map[string]any{"category": "餐饮", "amountFen": 300000})

	e.register("budget-intruder", "password123")
	// register 后 e.cj 已是入侵者会话，直接用。
	cases := []struct {
		method, path string
		body         any
	}{
		{"GET", wu(widB) + "/ledger/budgets", nil},
		{"PUT", wu(widB) + "/ledger/budgets", map[string]any{"category": "餐饮", "amountFen": 100}},
		{"DELETE", wu(widB) + "/ledger/budgets/%E9%A4%90%E9%A5%AE", nil},
	}
	for _, tc := range cases {
		resp, _ := e.do(tc.method, tc.path, tc.body)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("入侵者 %s %s 应 403，得到 %d", tc.method, tc.path, resp.StatusCode)
		}
	}
}
