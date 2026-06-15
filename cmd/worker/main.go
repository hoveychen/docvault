// Command worker drains the docvault sync-job queue, archiving documents to object storage.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/hoveychen/docvault/internal/app"
	"github.com/hoveychen/docvault/internal/config"
	syncpkg "github.com/hoveychen/docvault/internal/sync"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load()
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	a, err := app.Build(ctx, cfg, log)
	if err != nil {
		log.Error("startup", "err", err)
		os.Exit(1)
	}
	defer a.Close()

	engine := syncpkg.NewEngine(a.Repo, a.Tokens, a.Registry, a.Store, log)

	// Optional scheduled sync: enqueues due accounts in the background.
	scheduler := syncpkg.NewScheduler(a.Repo, cfg.SyncInterval, log)
	go scheduler.Start(ctx)

	worker := syncpkg.NewWorker(a.Repo, engine, log)
	worker.Start(ctx)
}
