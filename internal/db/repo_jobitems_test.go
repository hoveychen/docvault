package db

import (
	"testing"

	"github.com/hoveychen/docvault/internal/models"
)

// snapshot + per-status counts + pending paging + marking outcomes.
func TestJobItemsLifecycle(t *testing.T) {
	r, ctx := testRepo(t)
	uid := link(t, r, ctx, "u1")
	acct, err := r.GetAccountForUser(ctx, uid, "feishu")
	if err != nil {
		t.Fatalf("get account: %v", err)
	}
	jobID, err := r.EnqueueSyncJob(ctx, uid, acct.ID, "feishu")
	if err != nil {
		t.Fatal(err)
	}

	// Before snapshot: no items.
	if n, _ := r.JobItemCount(ctx, jobID); n != 0 {
		t.Fatalf("want 0 items before snapshot, got %d", n)
	}

	items := []models.JobItem{
		{ExternalID: "a", Title: "Folder A", DocType: "folder", IsFolder: true},
		{ExternalID: "b", Title: "Doc B", DocType: "docx"},
		{ExternalID: "c", Title: "Doc C", DocType: "sheet"},
	}
	ins, err := r.SnapshotJobItems(ctx, jobID, items)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if ins != 3 {
		t.Fatalf("want 3 inserted, got %d", ins)
	}

	// Re-snapshot is idempotent (ON CONFLICT DO NOTHING) — inserts nothing.
	if ins2, err := r.SnapshotJobItems(ctx, jobID, items); err != nil || ins2 != 0 {
		t.Fatalf("want idempotent re-snapshot (0, nil), got (%d, %v)", ins2, err)
	}
	if n, _ := r.JobItemCount(ctx, jobID); n != 3 {
		t.Fatalf("want 3 items after snapshot, got %d", n)
	}

	total, done, failed, pending, err := r.JobItemStats(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || done != 0 || failed != 0 || pending != 3 {
		t.Fatalf("fresh stats want 3/0/0/3, got %d/%d/%d/%d", total, done, failed, pending)
	}

	// Paging: limit caps the batch.
	batch, err := r.PendingJobItems(ctx, jobID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch) != 2 {
		t.Fatalf("want 2 pending with limit 2, got %d", len(batch))
	}

	// Mark first done, second failed.
	if err := r.MarkJobItem(ctx, batch[0].ID, models.JobItemDone, ""); err != nil {
		t.Fatal(err)
	}
	if err := r.MarkJobItem(ctx, batch[1].ID, models.JobItemFailed, "export boom"); err != nil {
		t.Fatal(err)
	}

	total, done, failed, pending, _ = r.JobItemStats(ctx, jobID)
	if total != 3 || done != 1 || failed != 1 || pending != 1 {
		t.Fatalf("after marking want 3/1/1/1, got %d/%d/%d/%d", total, done, failed, pending)
	}

	// Only the still-pending item comes back now.
	rest, _ := r.PendingJobItems(ctx, jobID, 10)
	if len(rest) != 1 || rest[0].ExternalID == batch[0].ExternalID || rest[0].ExternalID == batch[1].ExternalID {
		t.Fatalf("want the one untouched item pending, got %+v", rest)
	}
}

// Re-queued jobs round-robin by last_sliced_at; a brand-new job (NULL) preempts.
func TestClaimJobRoundRobin(t *testing.T) {
	r, ctx := testRepo(t)
	u1 := link(t, r, ctx, "u1")
	u2 := link(t, r, ctx, "u2")
	a1, _ := r.GetAccountForUser(ctx, u1, "feishu")
	a2, _ := r.GetAccountForUser(ctx, u2, "feishu")

	j1, _ := r.EnqueueSyncJob(ctx, u1, a1.ID, "feishu")
	j2, _ := r.EnqueueSyncJob(ctx, u2, a2.ID, "feishu")

	// j1 created first -> claimed first (both NULL last_sliced_at, created_at order).
	c1, err := r.ClaimJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if c1.ID != j1 {
		t.Fatalf("want j1 claimed first, got %s", c1.ID)
	}
	started := c1.StartedAt

	// j1 yields after a slice -> goes behind j2 (which is still NULL/never-sliced).
	if err := r.RequeueJob(ctx, j1); err != nil {
		t.Fatal(err)
	}
	c2, err := r.ClaimJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if c2.ID != j2 {
		t.Fatalf("after j1 re-queued, want j2 next (never-sliced preempts), got %s", c2.ID)
	}

	// j2 yields too -> now j1 (older last_sliced_at) comes back.
	if err := r.RequeueJob(ctx, j2); err != nil {
		t.Fatal(err)
	}
	c3, err := r.ClaimJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if c3.ID != j1 {
		t.Fatalf("want j1 back (oldest slice), got %s", c3.ID)
	}
	// started_at preserved across re-claims.
	if c3.StartedAt == nil || started == nil || !c3.StartedAt.Equal(*started) {
		t.Fatalf("want started_at preserved across re-claim, first=%v now=%v", started, c3.StartedAt)
	}
}
