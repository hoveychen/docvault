package sync

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/hoveychen/docvault/internal/auth"
	"github.com/hoveychen/docvault/internal/config"
	"github.com/hoveychen/docvault/internal/crypto"
	"github.com/hoveychen/docvault/internal/db"
	"github.com/hoveychen/docvault/internal/models"
	"github.com/hoveychen/docvault/internal/provider"
	"github.com/hoveychen/docvault/internal/store"
)

// fakeProvider lists a fixed set of items and exports them, recording how many
// times each item's Export was called so tests can assert incremental skipping.
type fakeProvider struct {
	items     []provider.Item
	exportErr map[string]error // externalID -> error Export should return
	mu        sync.Mutex
	calls     map[string]int
}

func (f *fakeProvider) Key() string                            { return "feishu" }
func (f *fakeProvider) Label() string                          { return "Fake" }
func (f *fakeProvider) AuthCodeURL(state, redirectURI string) string { return "" }
func (f *fakeProvider) Exchange(ctx context.Context, code, redirectURI string) (*provider.Token, *provider.Identity, error) {
	return nil, nil, nil
}
func (f *fakeProvider) Refresh(ctx context.Context, refreshToken string) (*provider.Token, error) {
	return &provider.Token{AccessToken: "refreshed"}, nil
}
func (f *fakeProvider) List(ctx context.Context, tok *provider.Token) ([]provider.Item, error) {
	return f.items, nil
}
func (f *fakeProvider) Export(ctx context.Context, tok *provider.Token, item provider.Item) (*provider.Blob, error) {
	f.mu.Lock()
	f.calls[item.ExternalID]++
	f.mu.Unlock()
	if err := f.exportErr[item.ExternalID]; err != nil {
		return nil, err
	}
	return &provider.Blob{
		Filename:    item.Title + ".docx",
		Format:      "docx",
		ContentType: "application/octet-stream",
		Data:        []byte("body-" + item.ExternalID),
	}, nil
}
func (f *fakeProvider) Delete(ctx context.Context, tok *provider.Token, item provider.Item) error {
	return nil
}
func (f *fakeProvider) callCount(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[id]
}

func testEngine(t *testing.T, fp *fakeProvider) (*Engine, *db.Repo, context.Context, string, string) {
	t.Helper()
	url := os.Getenv("DOCVAULT_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set DOCVAULT_TEST_DATABASE_URL to run sync integration tests")
	}
	ctx := context.Background()
	pool, err := db.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE users, provider_accounts, documents, folders, sync_jobs, sync_job_items, provider_connections RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	repo := db.NewRepo(pool)

	cipher, err := crypto.New(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	reg := provider.NewRegistry(fp)
	tokens := auth.NewTokenManager(repo, cipher, reg)

	st, err := store.New(ctx, config.S3Config{
		Endpoint: "localhost:9000", AccessKey: "minioadmin", SecretKey: "minioadmin",
		Bucket: "docvault-test", UseSSL: false, Region: "us-east-1",
	})
	if err != nil {
		t.Skipf("object store unavailable (start MinIO to run sync integration tests): %v", err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng := NewEngine(repo, tokens, reg, st, log)

	accEnc, _ := cipher.Encrypt("access")
	refEnc, _ := cipher.Encrypt("refresh")
	uid, accID, err := repo.LinkAccount(ctx, db.ProviderAccountUpsert{
		Provider: "feishu", ExternalUserID: "u1", DisplayName: "u1",
		AccessTokenEnc: accEnc, RefreshTokenEnc: refEnc,
		AccessTokenExpires: time.Now().Add(time.Hour), RefreshTokenExpires: time.Now().Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("link account: %v", err)
	}
	return eng, repo, ctx, uid, accID
}

// driveToCompletion claims+runs slices until the job reports complete, re-queuing
// between slices exactly as the worker would. Returns the number of slices it took.
func driveToCompletion(t *testing.T, eng *Engine, repo *db.Repo, ctx context.Context) int {
	t.Helper()
	slices := 0
	for {
		if slices > 200 {
			t.Fatal("job never completed — forward progress not guaranteed")
		}
		job, err := repo.ClaimJob(ctx)
		if errors.Is(err, db.ErrNotFound) {
			return slices // nothing left queued
		}
		if err != nil {
			t.Fatalf("claim: %v", err)
		}
		slices++
		complete, err := eng.RunSlice(ctx, job)
		if err != nil {
			t.Fatalf("run slice: %v", err)
		}
		if complete {
			_ = repo.FinishJob(ctx, job.ID, models.SyncSucceeded, "")
			return slices
		}
		if err := repo.RequeueJob(ctx, job.ID); err != nil {
			t.Fatalf("requeue: %v", err)
		}
	}
}

// A tiny budget makes each slice process exactly one item; the job must still
// resume across slices and finish, recording successes and failures correctly.
func TestRunSliceResumeAndComplete(t *testing.T) {
	fp := &fakeProvider{
		items: []provider.Item{
			{ExternalID: "F", Title: "Folder", DocType: "folder", IsFolder: true},
			{ExternalID: "A", Title: "DocA", DocType: "docx"},
			{ExternalID: "B", Title: "DocB", DocType: "docx"},
			{ExternalID: "C", Title: "DocC", DocType: "sheet"},
			{ExternalID: "D", Title: "DocD", DocType: "mindnote"},
		},
		exportErr: map[string]error{"D": errors.New("unsupported type")},
		calls:     map[string]int{},
	}
	eng, repo, ctx, uid, accID := testEngine(t, fp)

	// One item per slice.
	orig := SliceBudget
	SliceBudget = time.Nanosecond
	defer func() { SliceBudget = orig }()

	jobID, err := repo.EnqueueSyncJob(ctx, uid, accID, "feishu")
	if err != nil {
		t.Fatal(err)
	}

	// First slice: snapshots 5 items, processes exactly one, leaves 4 pending.
	job, err := repo.ClaimJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	complete, err := eng.RunSlice(ctx, job)
	if err != nil {
		t.Fatalf("first slice: %v", err)
	}
	if complete {
		t.Fatal("first slice should not complete a 5-item job with a 1ns budget")
	}
	if _, _, _, pending, _ := repo.JobItemStats(ctx, jobID); pending != 4 {
		t.Fatalf("after first slice want 4 pending, got %d", pending)
	}
	_ = repo.RequeueJob(ctx, jobID)

	// Drive the rest to completion.
	driveToCompletion(t, eng, repo, ctx)

	total, done, failed, pending, _ := repo.JobItemStats(ctx, jobID)
	if total != 5 || done != 4 || failed != 1 || pending != 0 {
		t.Fatalf("final stats want 5/4/1/0, got %d/%d/%d/%d", total, done, failed, pending)
	}

	// Successful docs are archived; the failed one is recorded without a copy.
	for _, id := range []string{"A", "B", "C"} {
		archived, _ := repo.IsArchived(ctx, uid, "feishu", id)
		if !archived {
			t.Fatalf("doc %s should be archived", id)
		}
	}
	if archived, _ := repo.IsArchived(ctx, uid, "feishu", "D"); archived {
		t.Fatal("failed doc D should not be archived")
	}

	// The job row mirrors the work-list counts for the UI.
	jr, _ := repo.LatestJob(ctx, uid)
	if jr.TotalItems != 5 || jr.DoneItems != 4 || jr.FailedItems != 1 || jr.Status != models.SyncSucceeded {
		t.Fatalf("job row want 5/4/1 succeeded, got %d/%d/%d %s", jr.TotalItems, jr.DoneItems, jr.FailedItems, jr.Status)
	}
}

// A second sync of the same account must skip re-exporting already-archived items
// and only retry the one that previously failed.
func TestRunSliceSkipsAlreadyArchived(t *testing.T) {
	fp := &fakeProvider{
		items: []provider.Item{
			{ExternalID: "A", Title: "DocA", DocType: "docx"},
			{ExternalID: "B", Title: "DocB", DocType: "docx"},
			{ExternalID: "D", Title: "DocD", DocType: "mindnote"},
		},
		exportErr: map[string]error{"D": errors.New("unsupported type")},
		calls:     map[string]int{},
	}
	eng, repo, ctx, uid, accID := testEngine(t, fp)

	// First full sync (generous budget — one slice).
	if _, err := repo.EnqueueSyncJob(ctx, uid, accID, "feishu"); err != nil {
		t.Fatal(err)
	}
	driveToCompletion(t, eng, repo, ctx)
	if fp.callCount("A") != 1 || fp.callCount("B") != 1 || fp.callCount("D") != 1 {
		t.Fatalf("first sync export calls want 1/1/1, got %d/%d/%d", fp.callCount("A"), fp.callCount("B"), fp.callCount("D"))
	}

	// Second sync: A and B are archived -> skipped (no re-export); D still has no
	// copy -> retried.
	if _, err := repo.EnqueueSyncJob(ctx, uid, accID, "feishu"); err != nil {
		t.Fatal(err)
	}
	driveToCompletion(t, eng, repo, ctx)
	if fp.callCount("A") != 1 || fp.callCount("B") != 1 {
		t.Fatalf("archived docs must not be re-exported; calls A=%d B=%d", fp.callCount("A"), fp.callCount("B"))
	}
	if fp.callCount("D") != 2 {
		t.Fatalf("unarchived doc D should be retried; want 2 export calls, got %d", fp.callCount("D"))
	}
}

// Export errors wrapping provider.ErrPermissionDenied / ErrNotExportable must be
// recorded as skipped (not failed), so the failure diagnostics show only genuine
// errors and the skip diagnostics show the no-permission / unsupported ones.
func TestRunSliceClassifiesSkipsVsFailures(t *testing.T) {
	fp := &fakeProvider{
		items: []provider.Item{
			{ExternalID: "A", Title: "DocA", DocType: "docx"},   // ok
			{ExternalID: "P", Title: "NoPerm", DocType: "docx"}, // no permission -> skipped
			{ExternalID: "U", Title: "Mind", DocType: "mindnote"}, // unsupported -> skipped
			{ExternalID: "F", Title: "Boom", DocType: "docx"},   // genuine -> failed
		},
		exportErr: map[string]error{
			"P": fmt.Errorf("create export task: code=1069902 msg=no permission: %w", provider.ErrPermissionDenied),
			"U": fmt.Errorf("doc type %q: %w", "mindnote", provider.ErrNotExportable),
			"F": errors.New("server exploded"),
		},
		calls: map[string]int{},
	}
	eng, repo, ctx, uid, accID := testEngine(t, fp)

	if _, err := repo.EnqueueSyncJob(ctx, uid, accID, "feishu"); err != nil {
		t.Fatal(err)
	}
	driveToCompletion(t, eng, repo, ctx)

	// Only the genuine error counts as failed; the two skips are excluded.
	jr, _ := repo.LatestJob(ctx, uid)
	if jr.FailedItems != 1 {
		t.Fatalf("want 1 failed (genuine error only), got %d", jr.FailedItems)
	}

	fails, err := repo.SyncFailureReasons(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(fails) != 1 || fails[0].Error != "server exploded" {
		t.Fatalf("failure reasons should be only the genuine error, got %+v", fails)
	}

	skips, err := repo.SyncSkippedReasons(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(skips) != 2 {
		t.Fatalf("want 2 skip reasons (no-permission + unsupported), got %d: %+v", len(skips), skips)
	}
	// A archived; the rest (skipped or failed) have no copy.
	if archived, _ := repo.IsArchived(ctx, uid, "feishu", "A"); !archived {
		t.Fatal("doc A should be archived")
	}
	for _, id := range []string{"P", "U", "F"} {
		if archived, _ := repo.IsArchived(ctx, uid, "feishu", id); archived {
			t.Fatalf("doc %s should not be archived", id)
		}
	}
}
