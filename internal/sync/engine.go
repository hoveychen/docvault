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

// SliceBudget bounds how long RunSlice keeps starting new items before yielding
// the worker back to the queue. Kept well under the worker's per-slice ctx cap so
// a normal slice ends by yielding, never by ctx cancellation. A var so tests can
// shrink it; every slice still processes at least one item (see RunSlice) so a
// tiny budget can never stall the job — it just yields after each item.
var SliceBudget = 5 * time.Minute

// pendingBatch is how many pending items RunSlice pulls per DB round-trip.
const pendingBatch = 50

// RunSlice processes one time-bounded slice of a job and reports whether the job
// is now complete. On the job's first claim it lists the account and snapshots
// every item into the durable work list (sync_job_items); subsequent claims skip
// listing and resume from the pending rows. It exports pending items until the
// slice budget elapses or none remain, marking each done/failed. A per-item
// export failure is recorded (object_key stays empty) and does not abort the
// slice; only store/DB faults abort. complete=false means the worker should
// re-queue the job for another slice.
func (e *Engine) RunSlice(ctx context.Context, job *models.SyncJob) (complete bool, err error) {
	acct, err := e.repo.GetAccount(ctx, job.ProviderAccountID)
	if err != nil {
		return false, fmt.Errorf("load account: %w", err)
	}
	prov := e.registry.Get(acct.Provider)
	if prov == nil {
		return false, fmt.Errorf("unknown provider %q", acct.Provider)
	}
	tok, err := e.tokens.ValidToken(ctx, acct)
	if err != nil {
		return false, fmt.Errorf("token: %w", err)
	}

	// First claim: enumerate the account once and snapshot the work list. Later
	// slices already have rows, so they never re-walk the provider's tree.
	count, err := e.repo.JobItemCount(ctx, job.ID)
	if err != nil {
		return false, fmt.Errorf("job item count: %w", err)
	}
	if count == 0 {
		items, err := prov.List(ctx, tok)
		if err != nil {
			return false, fmt.Errorf("list: %w", err)
		}
		snap := make([]models.JobItem, len(items))
		for i, it := range items {
			snap[i] = models.JobItem{
				ExternalID:      it.ExternalID,
				Title:           it.Title,
				DocType:         it.DocType,
				SourcePath:      it.SourcePath,
				OwnerExternalID: it.OwnerID,
				IsFolder:        it.IsFolder,
			}
		}
		if _, err := e.repo.SnapshotJobItems(ctx, job.ID, snap); err != nil {
			return false, fmt.Errorf("snapshot items: %w", err)
		}
		e.syncProgress(ctx, job.ID) // surface total_items immediately
	}

	// Process pending items until the budget elapses or none remain. At least one
	// item is always processed per slice (the budget check is gated on
	// processedAny) so even a tiny budget guarantees forward progress and can
	// never leave the job re-queuing without advancing.
	deadline := time.Now().Add(SliceBudget)
	processedAny := false
	for {
		if processedAny && !time.Now().Before(deadline) {
			break
		}
		if err := ctx.Err(); err != nil {
			return false, err
		}
		batch, err := e.repo.PendingJobItems(ctx, job.ID, pendingBatch)
		if err != nil {
			return false, fmt.Errorf("pending items: %w", err)
		}
		if len(batch) == 0 {
			break // nothing left to do this slice
		}
		for _, it := range batch {
			if processedAny && !time.Now().Before(deadline) {
				break
			}
			if err := ctx.Err(); err != nil {
				return false, err
			}
			// Refresh the token mid-slice if it expired during a long run.
			tok, err = e.tokens.ValidToken(ctx, acct)
			if err != nil {
				return false, fmt.Errorf("token refresh mid-slice: %w", err)
			}
			if err := e.processItem(ctx, job, acct.Provider, prov, tok, it); err != nil {
				return false, err
			}
			processedAny = true
		}
	}

	e.syncProgress(ctx, job.ID)
	_, _, _, pending, err := e.repo.JobItemStats(ctx, job.ID)
	if err != nil {
		return false, fmt.Errorf("job item stats: %w", err)
	}
	return pending == 0, nil
}

// processItem archives one work-list item and marks its outcome. It returns an
// error only for faults that should abort the slice (store/DB failures); a
// provider export failure is recorded and the item marked failed, returning nil.
func (e *Engine) processItem(ctx context.Context, job *models.SyncJob, providerKey string, prov provider.Provider, tok *provider.Token, it models.JobItem) error {
	// Folders aren't exported; record them so a whole folder can later be deleted
	// once everything under it is archived.
	if it.IsFolder {
		folder := &models.Folder{
			UserID:          job.UserID,
			Provider:        providerKey,
			ExternalID:      it.ExternalID,
			Title:           it.Title,
			SourcePath:      it.SourcePath,
			OwnerExternalID: it.OwnerExternalID,
		}
		if err := e.repo.UpsertFolder(ctx, folder); err != nil {
			return fmt.Errorf("record folder: %w", err)
		}
		return e.repo.MarkJobItem(ctx, it.ID, models.JobItemDone, "")
	}

	// Incremental: an item already archived in a prior sync (object_key present)
	// is skipped — no re-download — and marked done immediately.
	archived, err := e.repo.IsArchived(ctx, job.UserID, providerKey, it.ExternalID)
	if err != nil {
		return fmt.Errorf("check archived: %w", err)
	}
	if archived {
		return e.repo.MarkJobItem(ctx, it.ID, models.JobItemDone, "")
	}

	item := provider.Item{
		ExternalID: it.ExternalID,
		Title:      it.Title,
		DocType:    it.DocType,
		SourcePath: it.SourcePath,
		OwnerID:    it.OwnerExternalID,
	}
	// Base record captures the item's existence + owner even if export fails, so
	// folder-deletion can tell whether everything under a folder is archived.
	doc := &models.Document{
		UserID:          job.UserID,
		Provider:        providerKey,
		ExternalID:      it.ExternalID,
		Title:           it.Title,
		DocType:         it.DocType,
		SourcePath:      it.SourcePath,
		OwnerExternalID: it.OwnerExternalID,
	}

	blob, err := prov.Export(ctx, tok, item)
	if err != nil {
		// Non-exportable types and per-item failures are recorded (object_key stays
		// empty = not archived) but don't abort the slice.
		e.log.Warn("skip item", "title", it.Title, "doc_type", it.DocType, "err", err)
		if uerr := e.repo.UpsertDocument(ctx, doc); uerr != nil {
			return fmt.Errorf("record unarchived document: %w", uerr)
		}
		return e.repo.MarkJobItem(ctx, it.ID, models.JobItemFailed, err.Error())
	}

	key := objectKey(job.UserID, providerKey, it.ExternalID, blob.Filename)
	if err := e.store.Put(ctx, key, blob.Data, blob.ContentType); err != nil {
		return fmt.Errorf("store %q: %w", key, err)
	}
	doc.Format = blob.Format
	doc.ObjectKey = key
	doc.SizeBytes = int64(len(blob.Data))
	if err := e.repo.UpsertDocument(ctx, doc); err != nil {
		return fmt.Errorf("record document: %w", err)
	}
	return e.repo.MarkJobItem(ctx, it.ID, models.JobItemDone, "")
}

// syncProgress mirrors the work-list counts onto the job row for the UI.
func (e *Engine) syncProgress(ctx context.Context, jobID string) {
	total, done, failed, _, err := e.repo.JobItemStats(ctx, jobID)
	if err != nil {
		e.log.Warn("job progress refresh failed", "job_id", jobID, "err", err)
		return
	}
	_ = e.repo.UpdateJobProgress(ctx, jobID, total, done, failed)
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

		w.runSlice(ctx, job)
	}
}

// sliceHardCap bounds one slice's wall-clock as a safety net against a single
// hung export. It is larger than SliceBudget so a healthy slice always ends by
// yielding at the budget, not by ctx cancellation.
const sliceHardCap = 20 * time.Minute

// runSlice executes one slice of a claimed job. When the slice leaves work
// pending it re-queues the job so the worker round-robins to other users; only a
// completed job (or a hard fault) finishes the job row.
func (w *Worker) runSlice(ctx context.Context, job *models.SyncJob) {
	w.log.Info("running sync slice", "job_id", job.ID, "user_id", job.UserID, "provider", job.Provider)
	sliceCtx, cancel := context.WithTimeout(ctx, sliceHardCap)
	defer cancel()

	complete, err := w.engine.RunSlice(sliceCtx, job)
	if err != nil {
		w.log.Error("sync slice failed", "job_id", job.ID, "err", err)
		_ = w.repo.FinishJob(ctx, job.ID, models.SyncFailed, err.Error())
		return
	}
	if !complete {
		// Still pending — yield the worker to other users' slices.
		if rerr := w.repo.RequeueJob(ctx, job.ID); rerr != nil {
			w.log.Error("requeue slice failed", "job_id", job.ID, "err", rerr)
			_ = w.repo.FinishJob(ctx, job.ID, models.SyncFailed, rerr.Error())
		}
		return
	}
	_ = w.repo.FinishJob(ctx, job.ID, models.SyncSucceeded, "")
	w.log.Info("sync job done", "job_id", job.ID)
}
