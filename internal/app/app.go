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
	// provider implementations register their factories via init(); blank-import
	// each one so provider.Build can construct it by type.
	_ "github.com/hoveychen/docvault/internal/provider/feishu"
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
	Cipher   *crypto.Cipher
	Log      *slog.Logger
}

// Build connects to Postgres (running migrations), connects to object storage,
// and assembles the provider registry and auth managers.
func Build(ctx context.Context, cfg *config.Config, log *slog.Logger) (*App, error) {
	// Make this the default logger so library code (e.g. the Feishu provider's
	// best-effort wiki/folder skip warnings) writes to the same stream.
	slog.SetDefault(log)

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

	repo := db.NewRepo(pool)
	registry := provider.NewRegistry()

	a := &App{
		Config:   cfg,
		Pool:     pool,
		Repo:     repo,
		Store:    st,
		Registry: registry,
		Tokens:   auth.NewTokenManager(repo, cipher, registry),
		Sessions: auth.NewSessionManager(cfg.JWTSecret),
		Cipher:   cipher,
		Log:      log,
	}

	// Seed DB connections from env on first run (table empty), then load providers.
	if err := a.seedConnectionsFromEnv(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("seed connections: %w", err)
	}
	if err := a.ReloadProviders(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("load providers: %w", err)
	}
	return a, nil
}

// seedConnectionsFromEnv imports env-configured connections into the DB the first
// time (when the table is empty), so existing deployments migrate seamlessly and
// admins can thereafter manage connections from the web UI.
func (a *App) seedConnectionsFromEnv(ctx context.Context) error {
	if len(a.Config.Connections) == 0 {
		return nil
	}
	n, err := a.Repo.CountConnections(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	for _, conn := range a.Config.Connections {
		secretEnc, err := a.Cipher.Encrypt(conn.AppSecret)
		if err != nil {
			return err
		}
		if err := a.Repo.CreateConnection(ctx, conn.Type, conn.Key, conn.Label, conn.AppID, conn.Domain, secretEnc); err != nil {
			return err
		}
		a.Log.Info("seeded connection from env", "key", conn.Key, "type", conn.Type)
	}
	return nil
}

// ReloadProviders rebuilds the provider registry from DB connections. Called at
// startup and whenever an admin changes connections.
func (a *App) ReloadProviders(ctx context.Context) error {
	cfgs, err := a.Repo.ListConnectionConfigs(ctx)
	if err != nil {
		return err
	}
	var provs []provider.Provider
	for _, c := range cfgs {
		secret, err := a.Cipher.Decrypt(c.AppSecretEnc)
		if err != nil {
			a.Log.Error("decrypt connection secret failed", "key", c.Key, "err", err)
			continue
		}
		prov, err := provider.Build(provider.ConnDef{
			Type: c.Type, Key: c.Key, Label: c.Label, AppID: c.AppID, AppSecret: secret, Domain: c.Domain,
		})
		if err != nil {
			a.Log.Error("build provider failed", "key", c.Key, "type", c.Type, "err", err)
			continue
		}
		provs = append(provs, prov)
	}
	a.Registry.Replace(provs)
	a.Log.Info("providers loaded", "count", len(provs))
	return nil
}

// Close releases resources.
func (a *App) Close() {
	if a.Pool != nil {
		a.Pool.Close()
	}
}
