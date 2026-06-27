package db

import (
	"testing"
)

// TestSyncFailureReasonsLatestJobOnly pins the fix for round-mixing: failure
// reasons must reflect only each account's most recent sync, not every job ever
// run. Otherwise a doc that fails every scheduled sync is counted once per round
// and the totals balloon far past the real number of failing items.
func TestSyncFailureReasonsLatestJobOnly(t *testing.T) {
	r, ctx := testRepo(t)
	uid := link(t, r, ctx, "u1")
	acct, err := r.GetAccountForUser(ctx, uid, "feishu")
	if err != nil {
		t.Fatal(err)
	}

	addItem := func(job, ext, errMsg string) {
		if _, err := r.pool.Exec(ctx,
			`INSERT INTO sync_job_items(job_id, external_id, status, error) VALUES($1,$2,'failed',$3)`,
			job, ext, errMsg); err != nil {
			t.Fatalf("insert item %s: %v", ext, err)
		}
	}

	// An older sync that failed three items, then a newer sync that failed one.
	oldJob, err := r.EnqueueSyncJob(ctx, uid, acct.ID, "feishu")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(ctx,
		`UPDATE sync_jobs SET created_at = now() - interval '1 hour' WHERE id=$1`, oldJob); err != nil {
		t.Fatal(err)
	}
	newJob, err := r.EnqueueSyncJob(ctx, uid, acct.ID, "feishu")
	if err != nil {
		t.Fatal(err)
	}
	addItem(oldJob, "a", "stale permission error")
	addItem(oldJob, "b", "stale permission error")
	addItem(oldJob, "c", "stale permission error")
	addItem(newJob, "d", "current no permission")

	fr, err := r.SyncFailureReasons(ctx, 10)
	if err != nil {
		t.Fatalf("failure reasons: %v", err)
	}
	if len(fr) != 1 {
		t.Fatalf("want only the latest sync's failures (1 reason), got %d: %+v", len(fr), fr)
	}
	if fr[0].Error != "current no permission" || fr[0].Count != 1 {
		t.Fatalf("want [current no permission x1], got %+v", fr[0])
	}
}
