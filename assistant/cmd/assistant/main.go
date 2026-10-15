// 助手服务入口：企业微信智能机器人长连接 → 解析计划 → 写进 TodoDay → 回复。
//
// 走长连接（WebSocket）而不是接收 URL 回调：不需要公网 HTTPS、不需要反向
// 代理、消息是明文 JSON 不需要加解密。代价是进程必须常驻并自行重连。
//
// 配置来源分两层：环境变量给出「怎么连后端」，机器人与 AI 的凭据则优先
// 从后端取（网页设置页填的），环境变量只作兜底。重连时会重新取一次，
// 所以网页上改配置不用重启进程。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	// 内嵌 IANA 时区库：Windows 上没有系统 zoneinfo，
	// 而相对日期的解析依赖工作区时区。
	_ "time/tzdata"

	"assistant/internal/aibot"
	"assistant/internal/bot"
	"assistant/internal/config"
	"assistant/internal/parse"
	"assistant/internal/todoclient"
)

func main() {
	recordTest := flag.String("record-test", "",
		"不连企业微信，直接把这句话按「解析 → 建任务」跑一遍后退出，用于验证配置")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("配置有误：%v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 连后端并确认账号确实属于目标工作区。配错了就在这里失败，
	// 而不是等第一条消息进来才报错。
	todo, err := todoclient.New(cfg.APIBase, cfg.Username, cfg.Password, cfg.WorkspaceID)
	if err != nil {
		log.Fatalf("构造后端客户端失败：%v", err)
	}
	loginCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := todo.Login(loginCtx); err != nil {
		log.Fatalf("登录 TodoDay 失败：%v", err)
	}
	tzName, err := todo.WorkspaceTimezone(loginCtx)
	if err != nil {
		log.Fatalf("读取工作区信息失败：%v", err)
	}
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		log.Printf("时区 %q 无法识别，改用 Asia/Shanghai", tzName)
		loc, _ = time.LoadLocation("Asia/Shanghai")
	}
	log.Printf("已连接 %s，工作区 %d，时区 %s", cfg.APIBase, cfg.WorkspaceID, loc)

	// 第一次解析凭据：后端优先，环境变量兜底。
	creds := resolveCredentials(loginCtx, cfg, todo)
	log.Printf("凭据来源：%s", creds.Source)

	var kimi *parse.Kimi
	if creds.KimiKey != "" {
		kimi = parse.NewKimi(creds.KimiKey, creds.KimiBase, creds.Model, cfg.ParseTimeout)
		log.Printf("AI 解析已启用：%s @ %s", creds.Model, creds.KimiBase)
	} else {
		log.Printf("未配置 Kimi 密钥，全部使用规则解析")
	}
	parser := parse.NewParser(kimi, loc)

	if *recordTest != "" {
		if err := runRecordTest(ctx, parser, todo, *recordTest); err != nil {
			log.Fatalf("试记录失败：%v", err)
		}
		return
	}

	// 「停用」与「没配置」要分开报：前者是用户在设置页的正常选择，
	// 后者才是配置缺失，需要指出该去哪填。
	if !creds.Enabled {
		log.Printf("助手在网页设置里处于停用状态：进程保持运行，重新启用后会自动连接")
	} else if creds.BotID == "" || creds.BotSecret == "" {
		log.Fatalf("没有可用的机器人凭据：请在网页「设置」页填写 BotID 与 Secret，" +
			"或设置环境变量 WECOM_BOT_ID / WECOM_BOT_SECRET")
	}

	// 长连接客户端既负责收消息，也负责回执（回复要带回 req_id）。
	client := aibot.NewClient(creds.BotID, creds.BotSecret, cfg.WSURL, nil)
	// 每次重连前重新从后端取凭据：网页上改了配置（含启用/停用），重连就生效。
	client.SetCredentialSource(func(ctx context.Context) (string, string, error) {
		c := resolveCredentials(ctx, cfg, todo)
		if !c.Enabled {
			// 开关被关掉：不连接，客户端会保持轮询等待重新启用。
			return "", "", aibot.ErrDisabled
		}
		if c.BotID == "" || c.BotSecret == "" {
			// 用 ErrNoCredentials 而不是普通错误：普通错误会被当作「暂时
			// 取不到」而沿用旧凭据继续连，于是凭据被撤销后助手照旧运行。
			return "", "", aibot.ErrNoCredentials
		}
		return c.BotID, c.BotSecret, nil
	})
	handler := bot.New(parser, todo, client, kimi != nil)
	client.SetCallback(&botCallback{handler: handler})

	// 健康检查端口。长连接模式下它不是消息入口，仅供探活使用。
	go healthServer(ctx, cfg.Addr)

	if creds.Enabled {
		log.Printf("正在连接企业微信长连接 %s（BotID=%s）", cfg.WSURL, creds.BotID)
	}
	if err := client.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("长连接运行结束：%v", err)
	}
	log.Printf("已退出")
}

// resolveCredentials 从后端取凭据并与环境变量合并。
// 后端 404（未配置）或出错都退回环境变量，不让一次网络抖动打断服务。
func resolveCredentials(ctx context.Context, cfg *config.Config, todo *todoclient.Client) config.Credentials {
	remote, err := todo.FetchCredentials(ctx)
	switch {
	case err == nil:
		return cfg.Resolve(&config.RemoteCredentials{
			Enabled:    remote.Enabled,
			BotID:      remote.BotID,
			BotSecret:  remote.BotSecret,
			KimiAPIKey: remote.KimiAPIKey,
			KimiBase:   remote.KimiBase,
			KimiModel:  remote.KimiModel,
		})
	case errors.Is(err, todoclient.ErrNotConfigured):
		// 正常情况：用户还没在网页上配，走环境变量。
		return cfg.Resolve(nil)
	default:
		log.Printf("从后端读取凭据失败，改用环境变量：%v", err)
		return cfg.Resolve(nil)
	}
}

// botCallback 把长连接的回调转给编排层。
type botCallback struct {
	handler *bot.Handler
}

func (c *botCallback) OnMessage(ctx context.Context, reqID string, body *aibot.InboundBody) {
	// 非文本消息没有可记录的文本内容，直接忽略（但仍要记日志，
	// 否则用户发了图片会觉得机器人静默失效）。
	if !body.IsText() {
		log.Printf("aibot: 忽略非文本消息 type=%s from=%s", body.MsgType, body.From.UserID)
		return
	}
	c.handler.Handle(ctx, bot.Incoming{
		FromUser: body.From.UserID,
		Text:     body.Content(),
		MsgID:    body.MsgID,
		ReqID:    reqID,
	})
}

func (c *botCallback) OnEvent(ctx context.Context, reqID string, body *aibot.EventBody) {
	// 进入会话、卡片点击等事件本服务不处理，记一行便于排查。
	log.Printf("aibot: 忽略事件 type=%s from=%s", body.Event.EventType, body.From.UserID)
}

// healthServer 提供一个探活端点，便于容器/进程管理器检查存活。
func healthServer(ctx context.Context, addr string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("健康检查端口启动失败：%v", err)
		}
	}()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

// runRecordTest 执行一次「解析 → 建任务」，不经过企业微信。
// 用来在配置机器人之前先确认凭据、工作区与解析链路都是通的。
func runRecordTest(ctx context.Context, parser *parse.Parser, recorder interface {
	CreatePlan(context.Context, parse.Plan, string) error
}, text string) error {
	plan, source := parser.Parse(ctx, text)
	if strings.TrimSpace(plan.Title) == "" {
		return fmt.Errorf("没能从 %q 里解析出任务内容", text)
	}
	clientID := fmt.Sprintf("manual-%d", time.Now().Unix())
	if err := recorder.CreatePlan(ctx, plan, clientID); err != nil {
		return err
	}
	log.Printf("已记录：%s ｜ 日期 %s ｜ 分组 %s ｜ 优先级 %d（解析来源：%s）",
		plan.Title, dueOrNone(plan.Due), plan.Group, plan.Priority, source)
	return nil
}

func dueOrNone(due string) string {
	if due == "" {
		return "未排期"
	}
	return due
}
