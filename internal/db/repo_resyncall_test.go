package db

import (
	"testing"
)

// TestEnqueueAllAccounts verifies the admin "re-sync all" enqueues one job per
// eligible account, skips accounts that already have an active job, and excludes
// banned users.
func TestEnqueueAllAccounts(t *testing.T) {
	r, ctx := testRepo(t)
	u1 := link(t, r, ctx, "u1")
	u2 := link(t, r, ctx, "u2")
	u3 := link(t, r, ctx, "u3")
	a2, err := r.GetAccountForUser(ctx, u2, "feishu")
	if err != nil {
		t.Fatal(err)
	}

	// u2 already has an active (queued) job -> must not be double-enqueued.
	if _, err := r.EnqueueSyncJob(ctx, u2, a2.ID, "feishu"); err != nil {
		t.Fatal(err)
	}
	// u3 is banned -> excluded.
	if err := r.SetUserBanned(ctx, u3, true); err != nil {
		t.Fatal(err)
	}

	// Only u1 is eligible (u2 active, u3 banned).
	n, err := r.EnqueueAllAccounts(ctx)
	if err != nil {
		t.Fatalf("enqueue all: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 enqueued (only u1), got %d", n)
	}
	if active, _ := r.HasActiveJob(ctx, u1); !active {
		t.Fatal("u1 should now have an active job")
	}
	if active, _ := r.HasActiveJob(ctx, u3); active {
		t.Fatal("banned u3 must not have a job")
	}
}
