package db

import (
	"context"
	"testing"

	"github.com/hoveychen/docvault/internal/models"
)

func mustDoc(t *testing.T, r *Repo, ctx context.Context, userID, extID, docType, objectKey string) {
	t.Helper()
	if err := r.UpsertDocument(ctx, &models.Document{
		UserID:     userID,
		Provider:   "feishu",
		ExternalID: extID,
		Title:      extID,
		DocType:    docType,
		ObjectKey:  objectKey,
	}); err != nil {
		t.Fatalf("upsert doc %s: %v", extID, err)
	}
}

// TestAdminArchiveAndFailureStats covers the three admin-diagnostics queries:
// per-user archive totals, unarchived counts by doc_type, and the failure-reason
// breakdown.
func TestAdminArchiveAndFailureStats(t *testing.T) {
	r, ctx := testRepo(t)
	u1 := link(t, r, ctx, "u1")
	u2 := link(t, r, ctx, "u2")

	// u1: 2 archived docx + 1 unarchived mindnote. u2: 1 archived sheet.
	mustDoc(t, r, ctx, u1, "d1", "docx", "k1")
	mustDoc(t, r, ctx, u1, "d2", "docx", "k2")
	mustDoc(t, r, ctx, u1, "d3", "mindnote", "")
	mustDoc(t, r, ctx, u2, "d4", "sheet", "k4")

	// --- per-user archive stats ---
	us, err := r.ListUserArchiveStats(ctx)
	if err != nil {
		t.Fatalf("user stats: %v", err)
	}
	if len(us) != 2 {
		t.Fatalf("want 2 users, got %d", len(us))
	}
	// u1 has unarchived items, so it sorts first.
	if us[0].UserID != u1 {
		t.Fatalf("want u1 first (has unarchived), got %s", us[0].DisplayName)
	}
	if us[0].Total != 3 || us[0].Archived != 2 || us[0].Unarchived != 1 {
		t.Fatalf("u1 stats: want 3/2/1, got %d/%d/%d", us[0].Total, us[0].Archived, us[0].Unarchived)
	}
	if us[0].DisplayName != "u1" {
		t.Fatalf("want display name u1, got %q", us[0].DisplayName)
	}
	if us[1].Total != 1 || us[1].Archived != 1 || us[1].Unarchived != 0 {
		t.Fatalf("u2 stats: want 1/1/0, got %d/%d/%d", us[1].Total, us[1].Archived, us[1].Unarchived)
	}

	// --- unarchived by type ---
	bt, err := r.UnarchivedByType(ctx)
	if err != nil {
		t.Fatalf("by type: %v", err)
	}
	if len(bt) != 1 || bt[0].DocType != "mindnote" || bt[0].Unarchived != 1 {
		t.Fatalf("want [mindnote unarchived 1], got %+v", bt)
	}

	// --- failure reasons ---
	acct, err := r.GetAccountForUser(ctx, u1, "feishu")
	if err != nil {
		t.Fatal(err)
	}
	job, err := r.EnqueueSyncJob(ctx, u1, acct.ID, "feishu")
	if err != nil {
		t.Fatal(err)
	}
	add := func(ext, errMsg string) {
		if _, err := r.pool.Exec(ctx,
			`INSERT INTO sync_job_items(job_id, external_id, status, error) VALUES($1,$2,'failed',$3)`,
			job, ext, errMsg); err != nil {
			t.Fatalf("insert item %s: %v", ext, err)
		}
	}
	add("a", `doc type "mindnote" is not exportable`)
	add("b", `doc type "mindnote" is not exportable`)
	add("c", "permission denied")
	// A non-failed item must not be counted.
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO sync_job_items(job_id, external_id, status) VALUES($1,'d','done')`, job); err != nil {
		t.Fatal(err)
	}

	fr, err := r.SyncFailureReasons(ctx, 10)
	if err != nil {
		t.Fatalf("failure reasons: %v", err)
	}
	if len(fr) != 2 {
		t.Fatalf("want 2 distinct reasons, got %d (%+v)", len(fr), fr)
	}
	if fr[0].Error != `doc type "mindnote" is not exportable` || fr[0].Count != 2 {
		t.Fatalf("want top reason mindnote x2, got %+v", fr[0])
	}
}
