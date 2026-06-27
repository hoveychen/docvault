package db

import (
	"context"
	"testing"
	"time"
)

// jobStatus reads a single job's status straight from the row.
func jobStatus(t *testing.T, r *Repo, ctx context.Context, id string) string {
	t.Helper()
	var s string
	if err := r.pool.QueryRow(ctx, `SELECT status FROM sync_jobs WHERE id=$1`, id).Scan(&s); err != nil {
		t.Fatalf("read status %s: %v", id, err)
	}
	return s
}

// TestReclaimStaleJobs verifies orphaned running jobs (worker died mid-slice,
// so the row is stuck in 'running' with no lease) get reset to 'queued' once
// their last activity is older than the cutoff, while a freshly-active running
// job and an already-queued job are left untouched.
func TestReclaimStaleJobs(t *testing.T) {
	r, ctx := testRepo(t)
	uid := link(t, r, ctx, "u1")
	acct, err := r.GetAccountForUser(ctx, uid, "feishu")
	if err != nil {
		t.Fatalf("get account: %v", err)
	}

	now := time.Now()

	// J1: orphaned running, last activity 1h ago -> must be reclaimed.
	stale, err := r.EnqueueSyncJob(ctx, uid, acct.ID, "feishu")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(ctx,
		`UPDATE sync_jobs SET status='running', started_at=$2, last_sliced_at=$2 WHERE id=$1`,
		stale, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	// J2: actively-running, last activity just now -> must be left alone.
	fresh, err := r.EnqueueSyncJob(ctx, uid, acct.ID, "feishu")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(ctx,
		`UPDATE sync_jobs SET status='running', started_at=$2, last_sliced_at=$2 WHERE id=$1`,
		fresh, now); err != nil {
		t.Fatal(err)
	}

	// J3: already queued -> untouched, and not counted as reclaimed.
	queued, err := r.EnqueueSyncJob(ctx, uid, acct.ID, "feishu")
	if err != nil {
		t.Fatal(err)
	}

	// Cutoff 30m ago: only the 1h-stale running job is past it.
	n, err := r.ReclaimStaleJobs(ctx, now.Add(-30*time.Minute))
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 job reclaimed, got %d", n)
	}
	if got := jobStatus(t, r, ctx, stale); got != "queued" {
		t.Fatalf("stale job: want queued after reclaim, got %q", got)
	}
	if got := jobStatus(t, r, ctx, fresh); got != "running" {
		t.Fatalf("fresh job: want still running, got %q", got)
	}
	if got := jobStatus(t, r, ctx, queued); got != "queued" {
		t.Fatalf("already-queued job: want still queued, got %q", got)
	}

	// Startup reclaim uses cutoff=now: every running job (single-worker = all
	// orphans) flips to queued, including the just-now one.
	n2, err := r.ReclaimStaleJobs(ctx, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("reclaim all: %v", err)
	}
	if n2 != 1 {
		t.Fatalf("want the remaining running job reclaimed, got %d", n2)
	}
	if got := jobStatus(t, r, ctx, fresh); got != "queued" {
		t.Fatalf("fresh job after full reclaim: want queued, got %q", got)
	}
}
