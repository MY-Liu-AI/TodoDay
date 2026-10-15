package main

import (
	"context"
	"errors"
	"flag"
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
	"tododay/internal/store"
)

func main() {
	addr := flag.String("addr", env("TODO_ADDR", ":8080"), "HTTP listen address")
	dbPath := flag.String("db", env("TODO_DB", "tododay.db"), "SQLite database file path")
	staticDir := flag.String("static", env("TODO_STATIC", ""), "directory with the built Vue frontend (optional)")
	flag.Parse()

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(context.Background()); err != nil {
		log.Fatalf("migrate database: %v", err)
	}

	srv := api.NewServer(st, *staticDir)
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
