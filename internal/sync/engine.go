// Package sync contains the document-archival engine and the worker loop that
// drains the Postgres-backed job queue.
package sync

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"time"

	"github.com/hoveychen/docvault/internal/auth"
	"github.com/hoveychen/docvault/internal/db"
	"github.com/hoveychen/docvault/internal/models"
	"github.com/hoveychen/docvault/internal/provider"
	"github.com/hoveychen/docvault/internal/store"
)

// Engine runs a single sync job end to end.
type Engine struct {
	repo     *db.Repo
	tokens   *auth.TokenManager
	registry *provider.Registry
	store    *store.Store
	log      *slog.Logger
}

func NewEngine(repo *db.Repo, tokens *auth.TokenManager, registry *provider.Registry, st *store.Store, log *slog.Logger) *Engine {
	return &Engine{repo: repo, tokens: tokens, registry: registry, store: st, log: log}
}

// Run executes one job: list the account, export each exportable item, store it,
// and record progress. Items whose doc type can't be exported are skipped.
func (e *Engine) Run(ctx context.Context, job *models.SyncJob) error {
	acct, err := e.repo.GetAccount(ctx, job.ProviderAccountID)
	if err != nil {
		return fmt.Errorf("load account: %w", err)
	}
	prov := e.registry.Get(acct.Provider)
	if prov == nil {
		return fmt.Errorf("unknown provider %q", acct.Provider)
	}
	tok, err := e.tokens.ValidToken(ctx, acct)
	if err != nil {
		return fmt.Errorf("token: %w", err)
	}

	items, err := prov.List(ctx, tok)
	if err != nil {
		return fmt.Errorf("list: %w", err)
	}

	total := len(items)
	done, failed := 0, 0
	_ = e.repo.UpdateJobProgress(ctx, job.ID, total, done, failed)

	for _, item := range items {
		// Refresh the token mid-run if it expired during a long sync.
		tok, err = e.tokens.ValidToken(ctx, acct)
		if err != nil {
			return fmt.Errorf("token refresh mid-sync: %w", err)
		}

		blob, err := prov.Export(ctx, tok, item)
		if err != nil {
			// Non-exportable types and per-item failures are skipped, not fatal.
			e.log.Warn("skip item", "title", item.Title, "doc_type", item.DocType, "err", err)
			failed++
			_ = e.repo.UpdateJobProgress(ctx, job.ID, total, done, failed)
			continue
		}

		key := objectKey(job.UserID, acct.Provider, item.ExternalID, blob.Filename)
		if err := e.store.Put(ctx, key, blob.Data, blob.ContentType); err != nil {
			return fmt.Errorf("store %q: %w", key, err)
		}

		doc := &models.Document{
			UserID:     job.UserID,
			Provider:   acct.Provider,
			ExternalID: item.ExternalID,
			Title:      item.Title,
			DocType:    item.DocType,
			Format:     blob.Format,
			SourcePath: item.SourcePath,
			ObjectKey:  key,
			SizeBytes:  int64(len(blob.Data)),
		}
		if err := e.repo.UpsertDocument(ctx, doc); err != nil {
			return fmt.Errorf("record document: %w", err)
		}
		done++
		_ = e.repo.UpdateJobProgress(ctx, job.ID, total, done, failed)
	}
	return nil
}

func objectKey(userID, providerKey, externalID, filename string) string {
	return path.Join("u", userID, providerKey, externalID, filename)
}

// Worker polls the queue and runs claimed jobs until the context is cancelled.
type Worker struct {
	repo     *db.Repo
	engine   *Engine
	log      *slog.Logger
	interval time.Duration
}

func NewWorker(repo *db.Repo, engine *Engine, log *slog.Logger) *Worker {
	return &Worker{repo: repo, engine: engine, log: log, interval: 2 * time.Second}
}

// Start blocks, draining the queue until ctx is cancelled.
func (w *Worker) Start(ctx context.Context) {
	w.log.Info("worker started", "poll_interval", w.interval.String())
	for {
		select {
		case <-ctx.Done():
			w.log.Info("worker stopping")
			return
		default:
		}

		job, err := w.repo.ClaimJob(ctx)
		if err == db.ErrNotFound {
			select {
			case <-ctx.Done():
				return
			case <-time.After(w.interval):
			}
			continue
		}
		if err != nil {
			w.log.Error("claim job failed", "err", err)
			time.Sleep(w.interval)
			continue
		}

		w.runJob(ctx, job)
	}
}

func (w *Worker) runJob(ctx context.Context, job *models.SyncJob) {
	w.log.Info("running sync job", "job_id", job.ID, "user_id", job.UserID, "provider", job.Provider)
	jobCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	if err := w.engine.Run(jobCtx, job); err != nil {
		w.log.Error("sync job failed", "job_id", job.ID, "err", err)
		_ = w.repo.FinishJob(ctx, job.ID, models.SyncFailed, err.Error())
		return
	}
	_ = w.repo.FinishJob(ctx, job.ID, models.SyncSucceeded, "")
	w.log.Info("sync job done", "job_id", job.ID)
}
