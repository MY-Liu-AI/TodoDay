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

	// Embedded timezone database: Windows hosts usually lack zoneinfo files,
	// and workspace stats aggregate by workspace timezone.
	_ "time/tzdata"

	"tododay/internal/api"
	"tododay/internal/secretbox"
	"tododay/internal/store"
)

func main() {
	addr := flag.String("addr", env("TODO_ADDR", ":8080"), "HTTP listen address")
	dbPath := flag.String("db", env("TODO_DB", "tododay.db"), "SQLite database file path")
	staticDir := flag.String("static", env("TODO_STATIC", ""), "directory with the built Vue frontend (optional)")
	genKey := flag.Bool("gen-config-key", false, "生成一把新的 TODO_CONFIG_KEY 后退出（不启动服务）")
	flag.Parse()

	// 生成主密钥是部署前的一次性操作，不需要数据库。
	if *genKey {
		k, err := secretbox.GenerateKey()
		if err != nil {
			log.Fatalf("生成密钥失败：%v", err)
		}
		// 只打印密钥本身，便于直接重定向进环境配置。
		fmt.Println(k)
		return
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(context.Background()); err != nil {
		log.Fatalf("migrate database: %v", err)
	}

	// 助手凭据的主密钥。缺失不阻止启动，但网页上将无法保存密钥 ——
	// 在这里就说清楚，免得用户点保存才发现。
	if _, err := secretbox.Open(); err != nil {
		if errors.Is(err, secretbox.ErrNoKey) {
			log.Printf("提示：未设置 %s，设置页将无法保存机器人 Secret 与 Kimi Key（其余功能不受影响）。"+
				"生成一把：go run ./cmd/server -gen-config-key", secretbox.EnvKey)
		} else {
			log.Printf("警告：%s 无法使用：%v", secretbox.EnvKey, err)
		}
	}

	srv := api.NewServer(st, *staticDir)

	// 恢复所有「已启用」工作区的助手连接；进程退出时统一断开。
	srv.SyncAssistants(context.Background())
	defer srv.CloseAssistants()

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		log.Printf("todo-day server listening on %s (db=%s)", *addr, *dbPath)
		if *staticDir != "" {
			log.Printf("serving frontend static files from %s", *staticDir)
		}
		if err := httpSrv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server: %v", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
