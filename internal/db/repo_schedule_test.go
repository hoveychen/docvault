package db

import (
	"testing"
	"time"

	"github.com/hoveychen/docvault/internal/models"
)

func TestAccountsDueForSync(t *testing.T) {
	r, ctx := testRepo(t)
	uid := link(t, r, ctx, "u1")
	acct, err := r.GetAccountForUser(ctx, uid, "feishu")
	if err != nil {
		t.Fatalf("get account: %v", err)
	}

	now := time.Now()

	// No jobs yet -> account is due.
	due, err := r.AccountsDueForSync(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].AccountID != acct.ID {
		t.Fatalf("want account due initially, got %+v", due)
	}

	// A queued job makes it not due (active).
	jobID, err := r.EnqueueSyncJob(ctx, uid, acct.ID, "feishu")
	if err != nil {
		t.Fatal(err)
	}
	if d, _ := r.AccountsDueForSync(ctx, now); len(d) != 0 {
		t.Fatalf("want not due while a job is active, got %+v", d)
	}

	// Finish it succeeded (finished_at = now). With a cutoff in the past, the
	// recent success means not due.
	if err := r.FinishJob(ctx, jobID, models.SyncSucceeded, ""); err != nil {
		t.Fatal(err)
	}
	if d, _ := r.AccountsDueForSync(ctx, now.Add(-time.Hour)); len(d) != 0 {
		t.Fatalf("want not due right after a success, got %+v", d)
	}

	// With a cutoff in the future (interval elapsed), it's due again.
	if d, _ := r.AccountsDueForSync(ctx, now.Add(time.Hour)); len(d) != 1 {
		t.Fatalf("want due after interval elapsed, got %+v", d)
	}

	// Banned user is excluded.
	if err := r.SetUserBanned(ctx, uid, true); err != nil {
		t.Fatal(err)
	}
	if d, _ := r.AccountsDueForSync(ctx, now.Add(time.Hour)); len(d) != 0 {
		t.Fatalf("want banned user excluded, got %+v", d)
	}
}
