// Package app wires docvault's shared dependencies so both the server and the
// worker construct the same object graph from config.
package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/hoveychen/docvault/internal/auth"
	"github.com/hoveychen/docvault/internal/config"
	"github.com/hoveychen/docvault/internal/crypto"
	"github.com/hoveychen/docvault/internal/db"
	"github.com/hoveychen/docvault/internal/provider"
	"github.com/hoveychen/docvault/internal/provider/feishu"
	"github.com/hoveychen/docvault/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

// App holds the shared dependency graph.
type App struct {
	Config   *config.Config
	Pool     *pgxpool.Pool
	Repo     *db.Repo
	Store    *store.Store
	Registry *provider.Registry
	Tokens   *auth.TokenManager
	Sessions *auth.SessionManager
	Log      *slog.Logger
}

// Build connects to Postgres (running migrations), connects to object storage,
// and assembles the provider registry and auth managers.
func Build(ctx context.Context, cfg *config.Config, log *slog.Logger) (*App, error) {
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("connect db: %w", err)
	}
	if err := db.Migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	st, err := store.New(ctx, cfg.S3)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("object store: %w", err)
	}

	cipher, err := crypto.New(cfg.TokenEncKey)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("token cipher: %w", err)
	}

	var provs []provider.Provider
	for _, conn := range cfg.FeishuConnections {
		provs = append(provs, feishu.New(conn))
		log.Info("provider enabled", "provider", conn.Key, "domain", conn.Domain, "label", conn.Label)
	}
	if len(provs) == 0 {
		log.Warn("no feishu/lark connections configured")
	}
	registry := provider.NewRegistry(provs...)

	repo := db.NewRepo(pool)
	return &App{
		Config:   cfg,
		Pool:     pool,
		Repo:     repo,
		Store:    st,
		Registry: registry,
		Tokens:   auth.NewTokenManager(repo, cipher, registry),
		Sessions: auth.NewSessionManager(cfg.JWTSecret),
		Log:      log,
	}, nil
}

// Close releases resources.
func (a *App) Close() {
	if a.Pool != nil {
		a.Pool.Close()
	}
}
