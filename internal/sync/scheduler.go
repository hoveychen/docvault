package sync

import (
	"context"
	"log/slog"
	"time"

	"github.com/hoveychen/docvault/internal/db"
)

// Scheduler periodically enqueues sync jobs for accounts whose last successful
// sync is older than the configured interval, giving docvault continuous backup.
// Disabled when interval <= 0 (sync stays on-demand).
type Scheduler struct {
	repo     *db.Repo
	interval time.Duration
	log      *slog.Logger
}

func NewScheduler(repo *db.Repo, interval time.Duration, log *slog.Logger) *Scheduler {
	return &Scheduler{repo: repo, interval: interval, log: log}
}

// Start blocks, enqueuing due accounts on each check until ctx is cancelled.
// The check cadence is capped at 1 minute so newly-linked accounts don't wait a
// full interval, while the per-account due test still uses the full interval.
func (s *Scheduler) Start(ctx context.Context) {
	if s.interval <= 0 {
		s.log.Info("scheduled sync disabled (set DOCVAULT_SYNC_INTERVAL to enable)")
		return
	}
	check := s.interval
	if check > time.Minute {
		check = time.Minute
	}
	s.log.Info("scheduled sync enabled", "interval", s.interval.String(), "check_every", check.String())

	ticker := time.NewTicker(check)
	defer ticker.Stop()
	for {
		s.enqueueDue(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Scheduler) enqueueDue(ctx context.Context) {
	cutoff := time.Now().Add(-s.interval)
	due, err := s.repo.AccountsDueForSync(ctx, cutoff)
	if err != nil {
		s.log.Error("scheduler: query due accounts failed", "err", err)
		return
	}
	for _, a := range due {
		if _, err := s.repo.EnqueueSyncJob(ctx, a.UserID, a.AccountID, a.Provider); err != nil {
			s.log.Error("scheduler: enqueue failed", "account", a.AccountID, "err", err)
			continue
		}
		s.log.Info("scheduled sync enqueued", "account", a.AccountID, "provider", a.Provider)
	}
}
