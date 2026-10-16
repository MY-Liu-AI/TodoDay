package assistant

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"tododay/internal/aibot"
	"tododay/internal/parse"
	"tododay/internal/secretbox"
	"tododay/internal/store"
)

// Manager 内嵌运行助手：每个工作区一条长连接，作为后台 goroutine 跑。
//
// 与独立进程的区别：凭据直接从 store 读（密文由 box 解），不经过 HTTP、
// 不需要登录密码；任务写入也直接调 store，复用同一套校验与幂等。
type Manager struct {
	st  *store.Store
	box *secretbox.Box

	// wsURL 默认空串表示企业微信正式地址；测试里注入本地 mock 服务地址，
	// 避免测试真的去连外网。与测试里直接赋 s.box / s.limiter 的做法一致。
	wsURL string

	// ctx 是全部 run 的父上下文，生命周期与后端进程一致（Stop 只取消单个
	// run，Close 才取消全部）。不能从 HTTP 请求上下文派生：请求一结束，
	// run 会被立刻取消。
	ctx    context.Context
	cancel context.CancelFunc

	mu   sync.Mutex
	runs map[int64]*run
}

type run struct {
	cancel context.CancelFunc
	client *aibot.Client
	done   chan struct{}
}

// 供 API 层映射成具体 HTTP 状态的错误。
var (
	ErrNotConfigured         = errors.New("尚未配置助手")
	ErrEncryptionUnavailable = errors.New("服务端未配置主密钥，无法解密机器人 Secret")
)

func NewManager(st *store.Store, box *secretbox.Box) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{st: st, box: box, ctx: ctx, cancel: cancel, runs: map[int64]*run{}}
}

// IsRunning 报告工作区的助手是否处于「已启动」状态（含正在重连中）。
func (m *Manager) IsRunning(wid int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.runs[wid]
	return ok
}

// Start 启动工作区的助手。ctx 只用于读取 store，不成为 run 的父上下文。
// 已启动时幂等返回 nil。
func (m *Manager) Start(ctx context.Context, wid, userID int64) error {
	m.mu.Lock()
	if _, ok := m.runs[wid]; ok {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()

	cfg, err := m.st.GetAssistantConfig(ctx, wid)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotConfigured
	}
	if err != nil {
		return err
	}
	if !cfg.Enabled {
		return aibot.ErrDisabled
	}
	if cfg.BotID == "" || cfg.BotSecretEnc == "" {
		return aibot.ErrNoCredentials
	}
	if m.box == nil {
		return ErrEncryptionUnavailable
	}
	secret, err := m.box.Unseal(cfg.BotSecretEnc)
	if err != nil || secret == "" {
		return aibot.ErrNoCredentials
	}

	loc, err := m.location(ctx, wid)
	if err != nil {
		return err
	}

	var kimi *parse.Kimi
	if cfg.KimiAPIKeyEnc != "" {
		if k, kerr := m.box.Unseal(cfg.KimiAPIKeyEnc); kerr == nil && k != "" {
			kimi = parse.NewKimi(k, cfg.KimiBase, cfg.KimiModel, 8*time.Second)
		}
	}
	parser := parse.NewParser(kimi, loc)

	// 任务写入：直接调 store，复用同一套校验/幂等/授权边界（凭据是 owner）。
	recorder := storeRecorder{st: m.st, wsID: wid, userID: userID}

	client := aibot.NewClient(cfg.BotID, secret, m.wsURL, nil)
	client.SetCredentialSource(m.credentialSource(wid))

	handler := New(parser, recorder, client)
	if kimi != nil {
		handler.SetPlanner(kimiPlanner{kimi: kimi, loc: loc})
	}
	client.SetCallback(&callbackAdapter{handler: handler})

	runCtx, cancel := context.WithCancel(m.ctx)
	r := &run{cancel: cancel, client: client, done: make(chan struct{})}

	m.mu.Lock()
	// 双检：并发 Start 可能同时穿过上面的检查。
	if _, ok := m.runs[wid]; ok {
		m.mu.Unlock()
		cancel()
		return nil
	}
	m.runs[wid] = r
	m.mu.Unlock()

	go func() {
		defer close(r.done)
		_ = client.Run(runCtx)
	}()

	log.Printf("assistant: 工作区 %d 助手已启动（BotID=%s）", wid, cfg.BotID)
	return nil
}

// Stop 停止工作区的助手：取消 run 并主动断开当前连接。
func (m *Manager) Stop(wid int64) {
	m.mu.Lock()
	r, ok := m.runs[wid]
	if ok {
		delete(m.runs, wid)
	}
	m.mu.Unlock()
	if !ok {
		return
	}
	r.cancel()
	_ = r.client.Close()
	log.Printf("assistant: 工作区 %d 助手已停止", wid)
}

// SyncAll 在进程启动时恢复所有「已启用」的工作区。
func (m *Manager) SyncAll(ctx context.Context) {
	ids, err := m.st.EnabledAssistantWorkspaceIDs(ctx)
	if err != nil {
		log.Printf("assistant: 读取启用的助手失败：%v", err)
		return
	}
	for _, wid := range ids {
		owner, err := m.st.WorkspaceOwnerID(ctx, wid)
		if err != nil {
			log.Printf("assistant: 工作区 %d 找不到 owner，跳过恢复：%v", wid, err)
			continue
		}
		if err := m.Start(ctx, wid, owner); err != nil {
			log.Printf("assistant: 恢复工作区 %d 助手失败：%v", wid, err)
		}
	}
}

// Close 停止全部 run 并等待它们退出，供进程优雅关闭时调用。
func (m *Manager) Close() {
	m.cancel()
	m.mu.Lock()
	runs := make([]*run, 0, len(m.runs))
	for _, r := range m.runs {
		runs = append(runs, r)
	}
	m.runs = map[int64]*run{}
	m.mu.Unlock()

	for _, r := range runs {
		r.cancel()
		_ = r.client.Close()
	}

	done := make(chan struct{})
	go func() {
		for _, r := range runs {
			<-r.done
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		log.Printf("assistant: 部分连接未在 2 秒内退出")
	}
}

// location 取工作区时区，用于相对日期解析（与统计口径一致）。
func (m *Manager) location(ctx context.Context, wid int64) (*time.Location, error) {
	tz, err := m.st.WorkspaceTimezone(ctx, wid)
	if err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		log.Printf("assistant: 时区 %q 无法识别，改用 Asia/Shanghai", tz)
		loc, _ = time.LoadLocation("Asia/Shanghai")
	}
	return loc, nil
}

// credentialSource 是 aibot 每次（重）连前与心跳里的凭据来源。
//
// 这是三条安全不变式的落点：
//  1. 撤销（clearBotSecret）→ 密文空 → ErrNoCredentials → 主动断开；
//  2. 三态分离：停用 → ErrDisabled，无凭据/已撤销 → ErrNoCredentials，
//     只有读 store 的临时失败才返回普通错误（客户端沿用旧值）；
//  3. 撤销对已建立的连接生效：客户端心跳里每 30s 调一次这里。
func (m *Manager) credentialSource(wid int64) aibot.CredentialSource {
	return func(ctx context.Context) (string, string, error) {
		cfg, err := m.st.GetAssistantConfig(ctx, wid)
		if errors.Is(err, store.ErrNotFound) {
			return "", "", aibot.ErrNoCredentials
		}
		if err != nil {
			// 暂时读不到：普通错误，客户端沿用旧值继续连。
			return "", "", err
		}
		if !cfg.Enabled {
			return "", "", aibot.ErrDisabled
		}
		if cfg.BotID == "" || cfg.BotSecretEnc == "" {
			return "", "", aibot.ErrNoCredentials
		}
		if m.box == nil {
			return "", "", aibot.ErrNoCredentials
		}
		secret, err := m.box.Unseal(cfg.BotSecretEnc)
		if err != nil || secret == "" {
			return "", "", aibot.ErrNoCredentials
		}
		return cfg.BotID, secret, nil
	}
}

// storeRecorder 把解析结果写进 store，实现 Handler 的 Recorder 接口。
type storeRecorder struct {
	st     *store.Store
	wsID   int64
	userID int64
}

func (r storeRecorder) CreatePlan(ctx context.Context, plan parse.Plan, clientID string) error {
	_, err := r.st.CreateTask(ctx, r.wsID, r.userID, store.TaskInput{
		Title:    plan.Title,
		Priority: plan.Priority,
		Due:      plan.Due,
		Group:    plan.Group,
	}, clientID)
	return err
}

// kimiPlanner 把 parse.Kimi 的多轮规划适配成 Handler 的 Planner 接口。
// 时区取工作区时区，与单轮解析、统计口径一致。
type kimiPlanner struct {
	kimi *parse.Kimi
	loc  *time.Location
}

func (p kimiPlanner) Plan(ctx context.Context, history []parse.Message) (parse.Decision, error) {
	return p.kimi.Plan(ctx, history, time.Now(), p.loc)
}

// callbackAdapter 把长连接的回调转给编排层。
type callbackAdapter struct {
	handler *Handler
}

func (c *callbackAdapter) OnMessage(ctx context.Context, reqID string, body *aibot.InboundBody) {
	// 非文本消息没有可记录的文本内容，直接忽略（但仍要记日志，
	// 否则用户发了图片会觉得机器人静默失效）。
	if !body.IsText() {
		log.Printf("assistant: 忽略非文本消息 type=%s from=%s", body.MsgType, body.From.UserID)
		return
	}
	c.handler.Handle(ctx, Incoming{
		FromUser: body.From.UserID,
		ChatID:   body.ChatID,
		Text:     body.Content(),
		MsgID:    body.MsgID,
		ReqID:    reqID,
	})
}

func (c *callbackAdapter) OnEvent(ctx context.Context, reqID string, body *aibot.EventBody) {
	log.Printf("assistant: 忽略事件 type=%s from=%s", body.Event.EventType, body.From.UserID)
}
