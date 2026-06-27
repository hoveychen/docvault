package db

import (
	"testing"

	"github.com/hoveychen/docvault/internal/models"
)

// TestListRecentJobs verifies the admin queue listing returns jobs across users
// enriched with the owner's display name, ordered with active jobs
// (running, then queued) ahead of finished ones.
func TestListRecentJobs(t *testing.T) {
	r, ctx := testRepo(t)
	uid := link(t, r, ctx, "u1")
	acct, err := r.GetAccountForUser(ctx, uid, "feishu")
	if err != nil {
		t.Fatalf("get account: %v", err)
	}

	// A finished job, a queued job, and a running job (created in that order so
	// created_at DESC alone would NOT put running first — ordering must key on
	// status).
	done, err := r.EnqueueSyncJob(ctx, uid, acct.ID, "feishu")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.FinishJob(ctx, done, models.SyncSucceeded, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := r.EnqueueSyncJob(ctx, uid, acct.ID, "feishu"); err != nil {
		t.Fatal(err)
	}
	running, err := r.EnqueueSyncJob(ctx, uid, acct.ID, "feishu")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(ctx, `UPDATE sync_jobs SET status='running' WHERE id=$1`, running); err != nil {
		t.Fatal(err)
	}

	jobs, err := r.ListRecentJobs(ctx, 50)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(jobs) != 3 {
		t.Fatalf("want 3 jobs, got %d", len(jobs))
	}
	if jobs[0].Status != models.SyncRunning {
		t.Fatalf("want running job first, got %q", jobs[0].Status)
	}
	if jobs[1].Status != models.SyncQueued {
		t.Fatalf("want queued job second, got %q", jobs[1].Status)
	}
	for _, j := range jobs {
		if j.DisplayName != "u1" {
			t.Fatalf("want display name u1, got %q for job %s", j.DisplayName, j.ID)
		}
		if j.Provider != "feishu" {
			t.Fatalf("want provider feishu, got %q", j.Provider)
		}
	}
}
