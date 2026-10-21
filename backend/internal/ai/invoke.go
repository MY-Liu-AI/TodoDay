package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"tododay/internal/secretbox"
	"tododay/internal/store"
)

// ContextProvider 按技能声明的键装配输入数据（服务端取数，不经前端转发，
// 防篡改也防前后端口径漂移）。产出是任意可 JSON 序列化的值。
type ContextProvider func(ctx context.Context, st *store.Store, wid int64) (any, error)

// Validator 校验并归一化模型的原始输出。ctxVal 是同一轮装配好的上下文，
// 用于核对模型产出是否越界（比如引用了不存在的任务 id）。
type Validator func(ctx context.Context, raw json.RawMessage, ctxVal any) (any, error)

// Invoker 是技能调用器：查注册表 → 装配上下文 → 调模型 → 校验产出。
// 自身无对话状态；有状态的编排属于适配层（企业微信多轮在 assistant 包）。
type Invoker struct {
	st         *store.Store
	box        *secretbox.Box
	reg        *Registry
	providers  map[string]ContextProvider
	validators map[string]Validator
	mu         sync.Mutex // 护 quota
	quota      map[int64]quotaEntry
}

type quotaEntry struct {
	day string
	n   int
}

// 每工作区每日 AI 调用上限。技能调用有真实模型成本，第一天就要有闸。
const dailyQuotaPerWorkspace = 60

// ErrQuotaExceeded 触发限流；ErrNotConfigured 表示模型凭据缺失。
var (
	ErrQuotaExceeded = errors.New("今日 AI 调用次数已达上限，明天再来")
	ErrNotConfigured = errors.New("尚未配置 AI 解析，请工作区所有者在设置页填写 Kimi API Key")
)

func NewInvoker(st *store.Store, box *secretbox.Box) *Invoker {
	v := &Invoker{
		st:         st,
		box:        box,
		providers:  map[string]ContextProvider{},
		validators: map[string]Validator{},
		quota:      map[int64]quotaEntry{},
	}
	v.providers["today_tasks"] = todayTasksProvider
	v.validators["quadrant_result"] = quadrantValidator
	return v
}

// SetRegistry 挂技能注册表；未加载时为 nil，Invoke 一律报技能不存在。
func (v *Invoker) SetRegistry(r *Registry) { v.reg = r }

// Skills 代理到注册表（注册表未加载时返回空表而不是 nil）。
func (v *Invoker) Skills() []Skill {
	if v.reg == nil {
		return []Skill{}
	}
	return v.reg.List()
}

// ReloadSkills 重新扫描技能目录（管理端触发）。
func (v *Invoker) ReloadSkills() error {
	if v.reg == nil {
		return errors.New("未配置技能目录")
	}
	return v.reg.Reload()
}

// Invoke 执行一次技能调用。错误都带用户可读文案，适配层如实透传。
func (v *Invoker) Invoke(ctx context.Context, wid int64, skillName string) (map[string]any, error) {
	if v.reg == nil {
		return nil, errors.New("技能系统未启用（服务端未配置技能目录）")
	}
	sk, ok := v.reg.Get(skillName)
	if !ok {
		return nil, fmt.Errorf("技能不存在：%s", skillName)
	}
	if !hasEntry(sk.Entries, "web") {
		return nil, fmt.Errorf("技能 %s 未开放 web 调用", skillName)
	}
	provider, ok := v.providers[sk.Context]
	if !ok {
		return nil, fmt.Errorf("技能 %s 声明的上下文 %q 没有对应的数据供给（平台能力未注册）", skillName, sk.Context)
	}
	validator, ok := v.validators[sk.Output]
	if !ok {
		return nil, fmt.Errorf("技能 %s 声明的产出 %q 没有对应的校验器（平台能力未注册）", skillName, sk.Output)
	}

	if err := v.checkQuota(wid); err != nil {
		return nil, err
	}

	loc, today, err := v.workspaceToday(ctx, wid)
	if err != nil {
		return nil, err
	}
	ctxVal, err := provider(ctx, v.st, wid)
	if err != nil {
		return nil, fmt.Errorf("装配技能数据失败：%w", err)
	}
	rawCtx, err := json.Marshal(ctxVal)
	if err != nil {
		return nil, err
	}

	apiKey, base, model, err := v.resolveLLM(ctx, wid)
	if err != nil {
		return nil, err
	}

	// 超时给足：流式下首包通常 1–3 秒，但思考型模型的完整生成
	// 可能拖到一两分钟（k2.6 实测），这里是一次性点击能容忍的上限。
	httpClient := &http.Client{
		Timeout: 120 * time.Second,
		// 与 parse.Kimi 同样的 SSRF 收紧：base 是 owner 可配置的，
		// 跟随重定向可能把 Bearer 密钥带进内网。
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	system := fmt.Sprintf("%s\n\n今天的日期是 %s（时区 %s）。\n只输出 JSON，不要输出任何其他文字。",
		sk.Prompt, today, loc.String())
	payload := &ChatRequest{
		// 推理模型（k2/k3）非流式会憋到思考结束才回响应头，超过任何
		// 合理的 HTTP 超时；流式首包约 1 秒，生成期间数据持续流动。
		Stream: true,
		Model:  model,
		Messages: []ChatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: string(rawCtx)},
		},
	}
	out, err := DoChat(ctx, httpClient, base, apiKey, payload)
	if err != nil {
		return nil, err
	}
	content := stripCodeFence(out.Choices[0].Message.Content)
	result, err := validator(ctx, json.RawMessage(content), ctxVal)
	if err != nil {
		return nil, err
	}
	return map[string]any{"skill": sk.Name, "version": sk.Version, "result": result}, nil
}

func hasEntry(entries []string, want string) bool {
	for _, e := range entries {
		if e == want {
			return true
		}
	}
	return false
}

func (v *Invoker) checkQuota(wid int64) error {
	day := time.Now().Format("2006-01-02")
	v.mu.Lock()
	defer v.mu.Unlock()
	q := v.quota[wid]
	if q.day != day {
		q = quotaEntry{day: day}
	}
	if q.n >= dailyQuotaPerWorkspace {
		return ErrQuotaExceeded
	}
	q.n++
	v.quota[wid] = q
	return nil
}

// workspaceToday 取工作区时区和「今天」的日期串。时区不可识别时回落
// Asia/Shanghai，与 assistant.Manager.location 同口径。
func (v *Invoker) workspaceToday(ctx context.Context, wid int64) (*time.Location, string, error) {
	tz, err := v.st.WorkspaceTimezone(ctx, wid)
	if err != nil {
		return nil, "", err
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		log.Printf("ai: 时区 %q 无法识别，改用 Asia/Shanghai", tz)
		loc, _ = time.LoadLocation("Asia/Shanghai")
	}
	now := time.Now().In(loc)
	return loc, now.Format("2006-01-02"), nil
}

// resolveLLM 解析模型凭据：后端配置行为准（含其密文为空——撤销后不得回落
// 环境变量，与助手凭据的三态语义一致）；无配置行时才用环境变量兜底。
func (v *Invoker) resolveLLM(ctx context.Context, wid int64) (apiKey, base, model string, err error) {
	cfg, err := v.st.GetAssistantConfig(ctx, wid)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return "", "", "", fmt.Errorf("读取 AI 配置失败：%w", err)
	}
	if err == nil && cfg.KimiAPIKeyEnc != "" {
		if v.box == nil {
			return "", "", "", errors.New("服务端未配置主密钥，无法解密 Kimi Key")
		}
		k, kerr := v.box.Unseal(cfg.KimiAPIKeyEnc)
		if kerr != nil || k == "" {
			return "", "", "", errors.New("Kimi Key 解密失败，请重新在设置页填写")
		}
		return k, orDefault(cfg.KimiBase, "https://api.moonshot.cn/v1"), orDefault(cfg.KimiModel, defaultModel), nil
	}
	// 无配置行或密钥已撤销：环境变量兜底（KIMI_API_KEY / MOONSHOT_API_KEY）。
	apiKey = strings.TrimSpace(os.Getenv("KIMI_API_KEY"))
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("MOONSHOT_API_KEY"))
	}
	if apiKey == "" {
		return "", "", "", ErrNotConfigured
	}
	return apiKey, orDefault(os.Getenv("KIMI_BASE_URL"), "https://api.moonshot.cn/v1"),
		orDefault(os.Getenv("ASSISTANT_MODEL"), defaultModel), nil
}

// defaultModel 是未显式配置模型时的兜底。注意 Moonshot 新平台账号
// 已没有 moonshot-v1-* 系列（404）；kimi-k2.6 这类推理模型非流式响应
// 极慢，k2.7-code-highspeed 实测四象限全量 prompt 约 5 秒、且支持
// function calling，做默认最稳。
const defaultModel = "kimi-k2.7-code-highspeed"

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

// stripCodeFence 去掉模型输出可能带的 ```json 围栏，只留 JSON 本体。
func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

// todayTasksProvider 装配「今天要处理的事」：今天计划的（含已完成）
// + 今天之前未完成的（逾期）。与前端 FocusView 同一口径，
// 服务端取数保证 AI 看到的是用户看到的同一批任务。
// taskView 是供给给模型的任务视图；provider 产出、validator 校验共用这个类型。
type taskView struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Group   string `json:"group"`
	P       int    `json:"priority"` // 1 重要 / 2 中等 / 3 普通
	Due     string `json:"due"`      // YYYY-MM-DD，未排期为 ""
	Done    bool   `json:"done"`
	Overdue bool   `json:"overdue"` // 计划日期早于今天且未完成
}

func todayTasksProvider(ctx context.Context, st *store.Store, wid int64) (any, error) {
	_, today, err := workspaceTodayOf(ctx, st, wid)
	if err != nil {
		return nil, err
	}
	tasks, _, err := st.ListTasks(ctx, wid, store.ListQuery{Page: 1, PageSize: 500})
	if err != nil {
		return nil, err
	}
	out := make([]taskView, 0, len(tasks))
	for _, t := range tasks {
		if t.Due == "" {
			continue
		}
		if t.Due > today {
			continue
		}
		out = append(out, taskView{
			ID: t.ID, Title: t.T, Group: t.G, P: t.P, Due: t.Due,
			Done: t.Done, Overdue: !t.Done && t.Due < today,
		})
	}
	return map[string]any{"tasks": out}, nil
}

func workspaceTodayOf(ctx context.Context, st *store.Store, wid int64) (*time.Location, string, error) {
	tz, err := st.WorkspaceTimezone(ctx, wid)
	if err != nil {
		return nil, "", err
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc, _ = time.LoadLocation("Asia/Shanghai")
	}
	return loc, time.Now().In(loc).Format("2006-01-02"), nil
}

// quadrantItem 是四象限产出的单条结果；validator 产出、API 响应共用。
type quadrantItem struct {
	ID       string `json:"id"`
	Quadrant string `json:"quadrant"`
	Reason   string `json:"reason"`
}

// quadrantValidator 校验四象限产出：items[].{id, quadrant, reason}。
// quadrant ∈ IU|IN|NU|NN；模型引用的任务 id 必须在输入集合内，越界的
// 直接丢弃并计数——如实但不因为一个幻觉 id 让整个调用失败。
func quadrantValidator(_ context.Context, raw json.RawMessage, ctxVal any) (any, error) {
	var in struct {
		Items []struct {
			ID      string `json:"id"`
			Quadrant string `json:"quadrant"`
			Reason  string `json:"reason"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("模型产出不是合法 JSON：%w", err)
	}
	ctxMap, ok := ctxVal.(map[string]any)
	if !ok {
		return nil, errors.New("上下文类型不对")
	}
	tasks, _ := ctxMap["tasks"].([]taskView)
	valid := map[string]bool{}
	for _, t := range tasks {
		valid[t.ID] = true
	}
	out := make([]quadrantItem, 0, len(in.Items))
	dropped := 0
	for _, it := range in.Items {
		q := strings.ToUpper(strings.TrimSpace(it.Quadrant))
		if q != "IU" && q != "IN" && q != "NU" && q != "NN" {
			dropped++
			continue
		}
		if !valid[it.ID] {
			dropped++
			continue
		}
		out = append(out, quadrantItem{ID: it.ID, Quadrant: q, Reason: strings.TrimSpace(it.Reason)})
	}
	return map[string]any{"items": out, "dropped": dropped}, nil
}
