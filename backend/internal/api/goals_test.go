package api

import (
	"net/http"
	"strconv"
	"testing"
)

// 目标模块的端到端契约：创建/里程碑/挂载/进度视图/软删恢复/409 冲突/越权。
func TestGoalsLifecycle(t *testing.T) {
	e := newTestEnv(t)
	e.register("goalie", "password123")
	wid := e.wid()

	// 建三个任务，一个目标，一个里程碑，挂两个任务。
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
		"title": "发布会筹备", "goalText": "办成一场发布会", "acceptance": "到场 200 人",
		"failureCost": "错过窗口期", "group": "工作", "status": "active",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create goal: %d", resp.StatusCode)
	}
	gid := int64(out["id"].(float64))
	if out["goalText"] != "办成一场发布会" ||
		out["acceptance"] != "到场 200 人" || out["failureCost"] != "错过窗口期" ||
		out["createdAt"] == "" || out["group"] != "工作" {
		t.Fatalf("目标字段不对：%v", out)
	}
	// 目标不再有截止日期：响应里不应再出现 due。
	if _, exists := out["due"]; exists {
		t.Fatalf("目标不应再返回 due：%v", out)
	}
	// mode 已随并行目标一并下架：响应里不应再出现 mode。
	if _, exists := out["mode"]; exists {
		t.Fatalf("目标不应再返回 mode：%v", out)
	}

	// 分组标签超长 → 400。
	resp, _ = e.do("POST", wu(wid)+"/goals", map[string]any{"title": "x", "group": string(make([]byte, 41))})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("超长分组应 400，得到 %d", resp.StatusCode)
	}

	// 里程碑：创建（带起止日期）→ 编辑（乐观并发）→ 删除/恢复。
	resp, out = e.do("POST", wu(wid)+"/goals/"+itoa(gid)+"/milestones", map[string]any{
		"title": "预热", "start": "2026-11-01", "due": "2026-11-07",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create milestone: %d", resp.StatusCode)
	}
	mstones := out["milestones"].([]any)
	if len(mstones) != 1 {
		t.Fatalf("应返回 1 个里程碑：%v", out)
	}
	ms := mstones[0].(map[string]any)
	mid := int64(ms["id"].(float64))
	if ms["title"] != "预热" || ms["start"] != "2026-11-01" || ms["due"] != "2026-11-07" {
		t.Fatalf("里程碑字段不对：%v", ms)
	}

	resp, out = e.do("PATCH", wu(wid)+"/goals/"+itoa(gid)+"/milestones/"+itoa(mid), map[string]any{
		"title": "正式预热", "start": "2026-11-02", "due": "2026-11-08", "version": 99,
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("里程碑过期 version 应 409，得到 %d", resp.StatusCode)
	}
	resp, out = e.do("PATCH", wu(wid)+"/goals/"+itoa(gid)+"/milestones/"+itoa(mid), map[string]any{
		"title": "正式预热", "start": "2026-11-02", "due": "2026-11-08", "version": 1,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update milestone: %d", resp.StatusCode)
	}

	// 挂载两个任务到里程碑。
	resp, out = e.do("POST", wu(wid)+"/goals/"+itoa(gid)+"/milestones/"+itoa(mid)+"/tasks", map[string]any{"taskId": taskIDs[0]})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("attach: %d %v", resp.StatusCode, out)
	}
	e.do("POST", wu(wid)+"/goals/"+itoa(gid)+"/milestones/"+itoa(mid)+"/tasks", map[string]any{"taskId": taskIDs[1]})

	// 一条任务只能属于一个目标：再挂一次 → 409。
	resp, _ = e.do("POST", wu(wid)+"/goals/"+itoa(gid)+"/milestones/"+itoa(mid)+"/tasks", map[string]any{"taskId": taskIDs[0]})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("重复挂载应 409，得到 %d", resp.StatusCode)
	}

	// 列表：active 目标带 1 个里程碑、2 个任务视图。
	resp, out = e.do("GET", wu(wid)+"/goals", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: %d", resp.StatusCode)
	}
	goals := out["goals"].([]any)
	g0 := goals[0].(map[string]any)
	if len(goals) != 1 || len(g0["milestones"].([]any)) != 1 {
		t.Fatalf("list 内容不对：%v", out)
	}
	msTasks := g0["milestones"].([]any)[0].(map[string]any)["tasks"].([]any)
	if len(msTasks) != 2 {
		t.Fatalf("里程碑下应有 2 个任务：%v", msTasks)
	}

	// 完成任务 → 目标视图的 done 反映出来（进度推导的输入）。
	resp, _ = e.do("POST", wu(wid)+"/tasks/"+taskIDs[0]+"/completion", map[string]any{"done": true, "version": 1})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("completion: %d", resp.StatusCode)
	}
	_, out = e.do("GET", wu(wid)+"/goals", nil)
	msTasks = out["goals"].([]any)[0].(map[string]any)["milestones"].([]any)[0].(map[string]any)["tasks"].([]any)
	if msTasks[0].(map[string]any)["done"] != true || msTasks[1].(map[string]any)["done"] != false {
		t.Fatalf("done 未反映：%v", msTasks)
	}

	// 乐观并发：过期 version → 409。
	resp, _ = e.do("PATCH", wu(wid)+"/goals/"+itoa(gid), map[string]any{
		"title": "改名", "status": "active", "version": 99,
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("过期 version 应 409，得到 %d", resp.StatusCode)
	}

	// 里程碑删除（软删，其下任务解除挂载）+ 恢复。
	resp, out = e.do("POST", wu(wid)+"/goals/"+itoa(gid)+"/milestones/"+itoa(mid)+"/delete", map[string]any{})
	if resp.StatusCode != http.StatusOK || len(out["milestones"].([]any)) != 0 {
		t.Fatalf("delete milestone: %d %v", resp.StatusCode, out)
	}
	resp, _ = e.do("GET", wu(wid)+"/tasks?q=定场地", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("删除里程碑不应删任务")
	}
	resp, out = e.do("POST", wu(wid)+"/goals/"+itoa(gid)+"/milestones/"+itoa(mid)+"/restore", map[string]any{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("restore milestone: %d", resp.StatusCode)
	}

	// 目标软删 + 恢复。
	resp, _ = e.do("POST", wu(wid)+"/goals/"+itoa(gid)+"/delete", map[string]any{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	_, out = e.do("GET", wu(wid)+"/goals", nil)
	if len(out["goals"].([]any)) != 0 {
		t.Fatalf("软删后不该列出：%v", out)
	}
	resp, out = e.do("POST", wu(wid)+"/goals/"+itoa(gid)+"/restore", map[string]any{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("restore: %d %v", resp.StatusCode, out)
	}

	// 里程碑删除时任务已解除挂载（恢复不自动回填）：重新挂两个再测移出。
	e.do("POST", wu(wid)+"/goals/"+itoa(gid)+"/milestones/"+itoa(mid)+"/tasks", map[string]any{"taskId": taskIDs[0]})
	e.do("POST", wu(wid)+"/goals/"+itoa(gid)+"/milestones/"+itoa(mid)+"/tasks", map[string]any{"taskId": taskIDs[1]})

	// 移出任务后任务本身还在任务列表里。
	resp, out = e.do("POST", wu(wid)+"/goals/"+itoa(gid)+"/tasks/remove", map[string]any{"taskId": taskIDs[1]})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("detach: %d %v", resp.StatusCode, out)
	}
	msTasks = out["milestones"].([]any)[0].(map[string]any)["tasks"].([]any)
	if len(msTasks) != 1 {
		t.Fatalf("detach 后里程碑应剩 1 个任务：%v", msTasks)
	}
	resp, _ = e.do("GET", wu(wid)+"/tasks?q=发邀请", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("detach 不应删任务")
	}

	// 目标整体重排：再建两个目标，把三个逆序提交，列表顺序跟着变。
	resp, _ = e.do("POST", wu(wid)+"/goals", map[string]any{"title": "目标二"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create 2: %d", resp.StatusCode)
	}
	resp, _ = e.do("POST", wu(wid)+"/goals", map[string]any{"title": "目标三"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create 3: %d", resp.StatusCode)
	}
	_, out = e.do("GET", wu(wid)+"/goals", nil)
	var all []any
	for _, g := range out["goals"].([]any) {
		all = append(all, g.(map[string]any)["id"])
	}
	if len(all) != 3 {
		t.Fatalf("应有 3 个目标：%v", all)
	}
	reversed := []any{all[2], all[1], all[0]}
	resp, _ = e.do("POST", wu(wid)+"/goals/reorder", map[string]any{"ids": reversed})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reorder: %d", resp.StatusCode)
	}
	_, out = e.do("GET", wu(wid)+"/goals", nil)
	got := out["goals"].([]any)
	for i := range reversed {
		if got[i].(map[string]any)["id"] != reversed[i] {
			t.Fatalf("目标顺序未生效：%v", got)
		}
	}
	// 数量不符 → 400。
	resp, _ = e.do("POST", wu(wid)+"/goals/reorder", map[string]any{"ids": all[:2]})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("目标重排数量不符应 400，得到 %d", resp.StatusCode)
	}
}

// 里程碑输入校验：空标题、日期格式、开始晚于截止、非法目标/里程碑 id。
func TestGoalMilestonesValidation(t *testing.T) {
	e := newTestEnv(t)
	e.register("goal-ms-check", "password123")
	wid := e.wid()

	resp, out := e.do("POST", wu(wid)+"/goals", map[string]any{"title": "校验目标"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create goal: %d", resp.StatusCode)
	}
	gid := int64(out["id"].(float64))

	resp, _ = e.do("POST", wu(wid)+"/goals/"+itoa(gid)+"/milestones", map[string]any{"title": ""})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("空里程碑标题应 400，得到 %d", resp.StatusCode)
	}
	resp, _ = e.do("POST", wu(wid)+"/goals/"+itoa(gid)+"/milestones", map[string]any{"title": "x", "start": "11/01"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("非法日期格式应 400，得到 %d", resp.StatusCode)
	}
	resp, _ = e.do("POST", wu(wid)+"/goals/"+itoa(gid)+"/milestones", map[string]any{"title": "x", "start": "2026-11-09", "due": "2026-11-01"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("开始晚于截止应 400，得到 %d", resp.StatusCode)
	}
	// 开始可单独设置、截止可单独设置。
	resp, out = e.do("POST", wu(wid)+"/goals/"+itoa(gid)+"/milestones", map[string]any{"title": "只有截止", "due": "2026-11-30"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("只有截止应可创建：%d", resp.StatusCode)
	}
	mid := int64(out["milestones"].([]any)[0].(map[string]any)["id"].(float64))

	// 里程碑 id 不属于该目标 → 404（不泄露存在性）。
	resp, _ = e.do("PATCH", wu(wid)+"/goals/"+itoa(gid)+"/milestones/424242", map[string]any{
		"title": "x", "version": 1,
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("不存在里程碑应 404，得到 %d", resp.StatusCode)
	}
	resp, _ = e.do("POST", wu(wid)+"/goals/424242/milestones/"+itoa(mid)+"/delete", map[string]any{})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("不存在目标的里程碑删除应 404，得到 %d", resp.StatusCode)
	}

	// 挂载不存在的任务 → 404。
	resp, _ = e.do("POST", wu(wid)+"/goals/"+itoa(gid)+"/milestones/"+itoa(mid)+"/tasks", map[string]any{"taskId": "t_nope"})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("挂载不存在任务应 404，得到 %d", resp.StatusCode)
	}
}

// 输入校验：空标题、战略决策超长、非法 status 被 400 挡住或收敛。
func TestGoalsValidation(t *testing.T) {
	e := newTestEnv(t)
	e.register("goal-check", "password123")
	wid := e.wid()

	resp, _ := e.do("POST", wu(wid)+"/goals", map[string]any{"title": ""})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("空标题应 400，得到 %d", resp.StatusCode)
	}
	// 战略决策单问超长 → 400。
	resp, _ = e.do("POST", wu(wid)+"/goals", map[string]any{"title": "x", "acceptance": string(make([]byte, 1001))})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("超长验收标准应 400，得到 %d", resp.StatusCode)
	}
	resp, out := e.do("POST", wu(wid)+"/goals", map[string]any{"title": "x", "status": "bogus"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("非法 status 应收敛为默认值，得到 %d", resp.StatusCode)
	}
	if out["status"] != "active" {
		t.Fatalf("收敛结果不对：%v", out)
	}

	// 不存在的目标 → 404。
	resp, _ = e.do("PATCH", wu(wid)+"/goals/424242", map[string]any{"title": "x", "version": 1})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("不存在目标应 404，得到 %d", resp.StatusCode)
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
	resp, out = e.do("POST", wu(widB)+"/goals/"+itoa(gid)+"/milestones", map[string]any{"title": "私有里程碑"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create milestone: %d", resp.StatusCode)
	}
	mid := int64(out["milestones"].([]any)[0].(map[string]any)["id"].(float64))

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
		{"POST", wu(widB) + "/goals/" + itoa(gid) + "/milestones", map[string]any{"title": "x"}},
		{"PATCH", wu(widB) + "/goals/" + itoa(gid) + "/milestones/" + itoa(mid), map[string]any{"title": "x", "version": 1}},
		{"POST", wu(widB) + "/goals/" + itoa(gid) + "/milestones/" + itoa(mid) + "/delete", map[string]any{}},
		{"POST", wu(widB) + "/goals/" + itoa(gid) + "/milestones/" + itoa(mid) + "/tasks", map[string]any{"taskId": "t_x"}},
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
