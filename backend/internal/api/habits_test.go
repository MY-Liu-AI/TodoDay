package api

import (
	"net/http"
	"testing"
)

// 习惯端到端契约：建习惯 → 实例任务滚动生成且出现在任务列表 → 编辑同步
// 未来实例 → 删除清未来留历史 → 幂等/校验/越权。
func TestHabitsLifecycle(t *testing.T) {
	e := newTestEnv(t)
	e.register("habituer", "password123")
	wid := e.wid()

	// 周一/周四习惯。
	resp, out := e.do("POST", wu(wid)+"/habits", map[string]any{
		"title": "练胸", "weekdays": []any{1, 4}, "clientId": "hc-1",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create habit: %d %v", resp.StatusCode, out)
	}
	hid := int64(out["id"].(float64))
	if out["title"] != "练胸" {
		t.Fatalf("习惯字段不对：%v", out)
	}
	days := out["weekdays"].([]any)
	if len(days) != 2 || days[0].(float64) != 1 || days[1].(float64) != 4 {
		t.Fatalf("星期应为 [1 4]：%v", days)
	}
	tasks := out["tasks"].([]any)
	if len(tasks) < 14 {
		t.Fatalf("周一+周四 8 周应 ≥16 实例里本周起至少 14：%d", len(tasks))
	}

	// clientId 幂等重放：同一习惯，实例不翻倍。
	resp, replay := e.do("POST", wu(wid)+"/habits", map[string]any{
		"title": "练胸", "weekdays": []any{1, 4}, "clientId": "hc-1",
	})
	if resp.StatusCode != http.StatusCreated || int64(replay["id"].(float64)) != hid {
		t.Fatalf("clientId 重放应返回原习惯：%d %v", resp.StatusCode, replay)
	}
	if len(replay["tasks"].([]any)) != len(tasks) {
		t.Fatalf("重放不应新增实例：%d != %d", len(replay["tasks"].([]any)), len(tasks))
	}

	// 实例是真实任务：任务列表里可见。
	resp, out = e.do("GET", wu(wid)+"/tasks", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list tasks: %d", resp.StatusCode)
	}
	items := out["tasks"].([]any)
	if len(items) < 14 {
		t.Fatalf("实例应出现在任务列表：%d", len(items))
	}
	found := false
	for _, it := range items {
		m := it.(map[string]any)
		if m["t"] == "练胸" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("任务列表里找不到练胸实例：%v", items[0])
	}

	// 习惯列表：1 个习惯，实例齐。
	resp, out = e.do("GET", wu(wid)+"/habits", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list habits: %d", resp.StatusCode)
	}
	hs := out["habits"].([]any)
	if len(hs) != 1 || len(hs[0].(map[string]any)["tasks"].([]any)) != len(tasks) {
		t.Fatalf("习惯列表不对：%v", out)
	}

	// 编辑：加周三、改标题；过期 version 409。
	resp, _ = e.do("PATCH", wu(wid)+"/habits/"+itoa(hid), map[string]any{
		"title": "练胸（上斜）", "weekdays": []any{1, 3, 4}, "version": 99,
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("过期 version 应 409，得到 %d", resp.StatusCode)
	}
	resp, out = e.do("PATCH", wu(wid)+"/habits/"+itoa(hid), map[string]any{
		"title": "练胸（上斜）", "weekdays": []any{1, 3, 4}, "version": 1,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update habit: %d %v", resp.StatusCode, out)
	}
	if len(out["tasks"].([]any)) <= len(tasks) {
		t.Fatalf("加星期三后实例应变多：%d <= %d", len(out["tasks"].([]any)), len(tasks))
	}

	// 校验：没选星期 / 星期越界 / 空标题 → 400。
	resp, _ = e.do("POST", wu(wid)+"/habits", map[string]any{"title": "跑步", "weekdays": []any{}})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("空星期应 400，得到 %d", resp.StatusCode)
	}
	resp, _ = e.do("POST", wu(wid)+"/habits", map[string]any{"title": "跑步", "weekdays": []any{8}})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("星期越界应 400，得到 %d", resp.StatusCode)
	}
	resp, _ = e.do("POST", wu(wid)+"/habits", map[string]any{"title": "", "weekdays": []any{1}})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("空标题应 400，得到 %d", resp.StatusCode)
	}

	// 跨工作区越权 → 403（非成员）。同一服务两个账号，靠 cookie jar 切换；
	// 与目标模块的 TestGoalsCrossWorkspaceDenied 同法。
	jar := cloneJar(e.cj)
	e.register("intruder", "password123")
	resp, _ = e.do("GET", wu(wid)+"/habits", nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("非成员读取应 403，得到 %d", resp.StatusCode)
	}
	resp, _ = e.do("PATCH", wu(wid)+"/habits/"+itoa(hid), map[string]any{
		"title": "x", "weekdays": []any{1}, "version": 1,
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("非成员编辑应 403，得到 %d", resp.StatusCode)
	}
	e.cj = jar

	// 删除习惯：200 + deleted 计数；再读为空。
	resp, out = e.do("POST", wu(wid)+"/habits/delete", map[string]any{"ids": []any{hid}})
	if resp.StatusCode != http.StatusOK || int64(out["deleted"].(float64)) != 1 {
		t.Fatalf("delete habits: %d %v", resp.StatusCode, out)
	}
	resp, out = e.do("GET", wu(wid)+"/habits", nil)
	if hs := out["habits"].([]any); len(hs) != 0 {
		t.Fatalf("删除后习惯应为空：%v", hs)
	}
}
