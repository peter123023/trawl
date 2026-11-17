package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/trawlhq/trawl/internal/httpapi"
	"github.com/trawlhq/trawl/internal/judge"
	"github.com/trawlhq/trawl/internal/store"
	"github.com/trawlhq/trawl/internal/writer"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	dataDir := envOr("TRAWL_DATA_DIR", "./data")
	addr := envOr("TRAWL_ADDR", ":8080")

	st, err := store.Open(dataDir)
	if err != nil {
		slog.Error("open store", "error", err)
		os.Exit(1)
	}
	defer st.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	projectID, pk, sk, err := st.Bootstrap(ctx)
	if err != nil {
		slog.Error("bootstrap", "error", err)
		os.Exit(1)
	}
	if sk != "" {
		keysPath := filepath.Join(dataDir, "keys.txt")
		body := fmt.Sprintf("project: %s\npublic:  %s\nsecret:  %s\n", projectID, pk, sk)
		if err := os.WriteFile(keysPath, []byte(body), 0o600); err != nil {
			slog.Warn("write keys file", "error", err)
		}
		fmt.Printf("\n  Trawl first run — credentials written to %s\n", keysPath)
		fmt.Printf("  public %s\n  secret %s\n\n", pk, sk)
	}
	if projectID == "" {
		projectID, err = st.DefaultProject(ctx)
		if err != nil {
			slog.Error("default project", "error", err)
			os.Exit(1)
		}
	}

	prices, err := st.Prices(ctx)
	if err != nil {
		slog.Warn("load prices", "error", err)
		prices = map[string]store.Price{}
	}

	judgeCap := 500
	if v := envOr("TRAWL_JUDGE_DAILY_CAP", ""); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			judgeCap = n
		}
	}

	judgeClient, jerr := judge.New(judge.Config{
		BaseURL:      envOr("TRAWL_JUDGE_BASE_URL", ""),
		APIKey:       envOr("TRAWL_JUDGE_API_KEY", ""),
		Model:        envOr("TRAWL_JUDGE_MODEL", "gpt-4o-mini"),
		AllowPrivate: envOr("TRAWL_JUDGE_ALLOW_PRIVATE", "") == "1",
	})
	if jerr != nil {
		slog.Info("llm judge disabled", "reason", jerr)
	} else {
		slog.Info("llm judge enabled", "model", judgeClient.Model(), "daily_cap", judgeCap)
	}

	wr := writer.New(st, 2048)
	wr.Start(ctx)

	api := httpapi.New(st, wr, httpapi.Options{
		ProjectID:         projectID,
		Prices:            prices,
		Judge:             judgeClient,
		JudgeCap:          judgeCap,
		PricesURL:         envOr("TRAWL_PRICES_URL", ""),
		AllowPrivateFetch: envOr("TRAWL_ALLOW_PRIVATE_FETCH", "") == "1",
	})
	srv := &http.Server{
		Addr:              addr,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		slog.Info("trawl listening", "addr", addr, "db", st.Path())
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server", "error", err)
			stop()
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
