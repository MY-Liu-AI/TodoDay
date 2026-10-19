package api

import (
	"net/http"
	"strconv"
	"testing"
)

// 目标模块的端到端契约：创建/挂载/进度视图/软删恢复/409 冲突/越权。
func TestGoalsLifecycle(t *testing.T) {
	e := newTestEnv(t)
	e.register("goalie", "password123")
	wid := e.wid()

	// 建三个任务，一个目标，挂两个任务。
	var taskIDs []string
	for _, title := range []string{"定场地", "发邀请", "彩排"} {
		resp, out := e.do("POST", wu(wid)+"/tasks", map[string]any{
			"t": title, "p": 3, "due": "", "g": "工作",
		})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create task %s: %d", title, resp.StatusCode)
		}
		taskIDs = append(taskIDs, out["id"].(string))
	}

	resp, out := e.do("POST", wu(wid)+"/goals", map[string]any{
		"title": "发布会筹备", "note": "月底发布", "mode": "sequential", "due": "2026-10-31", "status": "active",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create goal: %d", resp.StatusCode)
	}
	gid := int64(out["id"].(float64))
	if out["mode"] != "sequential" || out["due"] != "2026-10-31" || out["createdAt"] == "" {
		t.Fatalf("目标字段不对：%v", out)
	}

	resp, out = e.do("POST", wu(wid)+"/goals/"+itoa(gid)+"/tasks", map[string]any{"taskId": taskIDs[0]})
	if resp.StatusCode != http.StatusOK || len(out["tasks"].([]any)) != 1 {
		t.Fatalf("attach: %d %v", resp.StatusCode, out)
	}
	e.do("POST", wu(wid)+"/goals/"+itoa(gid)+"/tasks", map[string]any{"taskId": taskIDs[1]})

	// 一条任务只能属于一个目标：再挂一次 → 409。
	resp, _ = e.do("POST", wu(wid)+"/goals/"+itoa(gid)+"/tasks", map[string]any{"taskId": taskIDs[0]})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("重复挂载应 409，得到 %d", resp.StatusCode)
	}

	// 列表：active 目标带 2 个任务视图。
	resp, out = e.do("GET", wu(wid)+"/goals", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: %d", resp.StatusCode)
	}
	goals := out["goals"].([]any)
	if len(goals) != 1 || len(goals[0].(map[string]any)["tasks"].([]any)) != 2 {
		t.Fatalf("list 内容不对：%v", out)
	}

	// 完成任务 → 目标视图的 done 反映出来（进度推导的输入）。
	resp, _ = e.do("POST", wu(wid)+"/tasks/"+taskIDs[0]+"/completion", map[string]any{"done": true, "version": 1})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("completion: %d", resp.StatusCode)
	}
	_, out = e.do("GET", wu(wid)+"/goals", nil)
	tasks := out["goals"].([]any)[0].(map[string]any)["tasks"].([]any)
	if tasks[0].(map[string]any)["done"] != true || tasks[1].(map[string]any)["done"] != false {
		t.Fatalf("done 未反映：%v", tasks)
	}

	// 乐观并发：过期 version → 409。
	resp, _ = e.do("PATCH", wu(wid)+"/goals/"+itoa(gid), map[string]any{
		"title": "改名", "mode": "sequential", "status": "active", "version": 99,
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("过期 version 应 409，得到 %d", resp.StatusCode)
	}

	// 软删 + 恢复。
	resp, _ = e.do("POST", wu(wid)+"/goals/"+itoa(gid)+"/delete", map[string]any{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	_, out = e.do("GET", wu(wid)+"/goals", nil)
	if len(out["goals"].([]any)) != 0 {
		t.Fatalf("软删后不该列出：%v", out)
	}
	resp, out = e.do("POST", wu(wid)+"/goals/"+itoa(gid)+"/restore", map[string]any{})
	if resp.StatusCode != http.StatusOK || len(out["tasks"].([]any)) != 2 {
		t.Fatalf("restore: %d %v", resp.StatusCode, out)
	}

	// 移出任务后任务本身还在任务列表里。
	resp, out = e.do("POST", wu(wid)+"/goals/"+itoa(gid)+"/tasks/remove", map[string]any{"taskId": taskIDs[1]})
	if resp.StatusCode != http.StatusOK || len(out["tasks"].([]any)) != 1 {
		t.Fatalf("detach: %d %v", resp.StatusCode, out)
	}
	resp, _ = e.do("GET", wu(wid)+"/tasks?q=发邀请", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("detach 不应删任务")
	}

	// 重排：ids 数量不符 → 400。
	resp, _ = e.do("POST", wu(wid)+"/goals/"+itoa(gid)+"/tasks/reorder", map[string]any{"ids": []string{taskIDs[0], taskIDs[1]}})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("重排数量不符应 400，得到 %d", resp.StatusCode)
	}
}

// 输入校验：空标题、非法日期、非法 mode/status 都被 400 挡住或收敛。
func TestGoalsValidation(t *testing.T) {
	e := newTestEnv(t)
	e.register("goal-check", "password123")
	wid := e.wid()

	resp, _ := e.do("POST", wu(wid)+"/goals", map[string]any{"title": ""})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("空标题应 400，得到 %d", resp.StatusCode)
	}
	resp, _ = e.do("POST", wu(wid)+"/goals", map[string]any{"title": "x", "due": "2026-13-40"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("非法日期应 400，得到 %d", resp.StatusCode)
	}
	resp, out := e.do("POST", wu(wid)+"/goals", map[string]any{"title": "x", "mode": "bogus", "status": "bogus"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("非法 mode/status 应收敛为默认值，得到 %d", resp.StatusCode)
	}
	if out["mode"] != "parallel" || out["status"] != "active" {
		t.Fatalf("收敛结果不对：%v", out)
	}

	// 不存在的目标 → 404；挂载不存在的任务 → 404。
	resp, _ = e.do("PATCH", wu(wid)+"/goals/424242", map[string]any{"title": "x", "version": 1})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("不存在目标应 404，得到 %d", resp.StatusCode)
	}
	resp, out = e.do("POST", wu(wid)+"/goals", map[string]any{"title": "挂载目标"})
	gid := int64(out["id"].(float64))
	resp, _ = e.do("POST", wu(wid)+"/goals/"+itoa(gid)+"/tasks", map[string]any{"taskId": "t_nope"})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("挂载不存在任务应 404，得到 %d", resp.StatusCode)
	}
}

// 越权用例：同一服务上两个账号，A 的 Cookie 拿不到 B 的工作区数据。
// 用快照/恢复 cookie jar 的方式在同一测试环境里切换身份。
func TestGoalsCrossWorkspaceDenied(t *testing.T) {
	e := newTestEnv(t)
	e.register("goal-owner", "password123")
	jarOwner := cloneJar(e.cj)
	widB := e.wid()
	resp, out := e.do("POST", wu(widB)+"/goals", map[string]any{"title": "私有目标"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d", resp.StatusCode)
	}
	gid := int64(out["id"].(float64))

	e.register("goal-intruder", "password123")
	jarIntruder := cloneJar(e.cj)

	cases := []struct {
		method, path string
		body         any
	}{
		{"GET", wu(widB) + "/goals", nil},
		{"POST", wu(widB) + "/goals", map[string]any{"title": "x"}},
		{"PATCH", wu(widB) + "/goals/" + itoa(gid), map[string]any{"title": "x", "version": 1}},
		{"POST", wu(widB) + "/goals/" + itoa(gid) + "/delete", map[string]any{}},
		{"POST", wu(widB) + "/goals/" + itoa(gid) + "/restore", map[string]any{}},
	}
	// 入侵者一律 403。
	e.cj = jarIntruder
	for _, tc := range cases {
		resp, _ := e.do(tc.method, tc.path, tc.body)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("入侵者 %s %s 应 403，得到 %d", tc.method, tc.path, resp.StatusCode)
		}
	}
	// 拥有者本人正常。
	e.cj = jarOwner
	resp, _ = e.do("GET", wu(widB)+"/goals", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("拥有者读目标应 200，得到 %d", resp.StatusCode)
	}
}

func cloneJar(j map[string]string) map[string]string {
	out := make(map[string]string, len(j))
	for k, v := range j {
		out[k] = v
	}
	return out
}

func wu(wid float64) string {
	return "/api/workspaces/" + strconv.FormatInt(int64(wid), 10)
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
