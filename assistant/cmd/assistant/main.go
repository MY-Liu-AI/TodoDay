// 助手服务入口：接收企业微信回调 → 解析计划 → 写进 TodoDay → 回复。
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
	"syscall"
	"time"

	// 内嵌 IANA 时区库：Windows 上没有系统 zoneinfo，
	// 而相对日期的解析依赖工作区时区。
	_ "time/tzdata"

	"assistant/internal/bot"
	"assistant/internal/config"
	"assistant/internal/parse"
	"assistant/internal/todoclient"
	"assistant/internal/wecom"
)

const callbackPath = "/wecom/callback"

func main() {
	printHandshake := flag.Bool("print-handshake", false,
		"生成一条可用于本地自测的回调验证 URL 后退出（不需要真实企业微信后台）")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("配置有误：%v", err)
	}

	aesKey, err := wecom.DecodeAESKey(cfg.EncodingAESKey)
	if err != nil {
		log.Fatalf("WECOM_ENCODING_AES_KEY 无法使用：%v", err)
	}

	if *printHandshake {
		h := wecom.NewHandler(cfg.CorpID, cfg.Token, aesKey, nil)
		sig, ts, echo, err := h.VerifyURLForTest("testnonce")
		if err != nil {
			log.Fatalf("生成验证参数失败：%v", err)
		}
		fmt.Printf("curl -i 'http://localhost%s%s?msg_signature=%s&timestamp=%s&nonce=testnonce&echostr=%s'\n",
			cfg.Addr, callbackPath, sig, ts, echo)
		return
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

	var kimi *parse.Kimi
	if cfg.KimiEnabled() {
		kimi = parse.NewKimi(cfg.KimiKey, cfg.KimiBase, cfg.Model, cfg.ParseTimeout)
		log.Printf("AI 解析已启用：%s @ %s", cfg.Model, cfg.KimiBase)
	} else {
		log.Printf("未配置 KIMI_API_KEY，全部使用规则解析")
	}
	parser := parse.NewParser(kimi, loc)

	sender := wecom.NewClient(cfg.CorpID, cfg.Secret, cfg.AgentID)
	handler := bot.New(parser, todo, sender, cfg.KimiEnabled())

	mux := http.NewServeMux()
	mux.Handle(callbackPath, wecom.NewHandler(cfg.CorpID, cfg.Token, aesKey, func(ctx context.Context, m *wecom.Message) {
		handler.Handle(ctx, bot.Incoming{FromUser: m.FromUserName, Text: m.Content, MsgID: m.MsgID})
	}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	srv := &http.Server{
		Addr:    cfg.Addr,
		Handler: mux,
		// 回调要求快速响应，读超时不能长。
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		log.Printf("助手服务监听 %s，回调路径 %s", cfg.Addr, callbackPath)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("服务启动失败：%v", err)
		}
	}()

	<-ctx.Done()
	log.Printf("收到停止信号，准备退出")
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("关闭服务时出错：%v", err)
	}
}
