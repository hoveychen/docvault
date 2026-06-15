package db

import (
	"context"
	"os"
	"testing"
)

// These tests run only when DOCVAULT_TEST_DATABASE_URL points at a disposable
// Postgres (the schema is mutated). They verify the admin bootstrap + ban logic
// against a real database, since that logic lives in SQL.
func testRepo(t *testing.T) (*Repo, context.Context) {
	t.Helper()
	url := os.Getenv("DOCVAULT_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set DOCVAULT_TEST_DATABASE_URL to run db integration tests")
	}
	ctx := context.Background()
	pool, err := Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Clean slate.
	if _, err := pool.Exec(ctx, `TRUNCATE users, provider_accounts, documents, folders, sync_jobs, provider_connections RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return NewRepo(pool), ctx
}

func link(t *testing.T, r *Repo, ctx context.Context, ext string) string {
	t.Helper()
	uid, _, err := r.LinkAccount(ctx, ProviderAccountUpsert{
		Provider: "feishu", ExternalUserID: ext, DisplayName: ext,
	})
	if err != nil {
		t.Fatalf("link %s: %v", ext, err)
	}
	return uid
}

func TestFirstUserBecomesAdmin(t *testing.T) {
	r, ctx := testRepo(t)

	first := link(t, r, ctx, "u1")
	second := link(t, r, ctx, "u2")

	u1, _ := r.GetUser(ctx, first)
	u2, _ := r.GetUser(ctx, second)
	if !u1.IsAdmin() {
		t.Errorf("first user should be admin, got role=%q", u1.Role)
	}
	if u2.IsAdmin() {
		t.Errorf("second user should be member, got role=%q", u2.Role)
	}
}

func TestBanAndRoleAndAdminCount(t *testing.T) {
	r, ctx := testRepo(t)
	first := link(t, r, ctx, "u1")
	second := link(t, r, ctx, "u2")

	if n, _ := r.CountAdmins(ctx); n != 1 {
		t.Fatalf("want 1 admin, got %d", n)
	}
	if err := r.SetUserRole(ctx, second, "admin"); err != nil {
		t.Fatal(err)
	}
	if n, _ := r.CountAdmins(ctx); n != 2 {
		t.Fatalf("want 2 admins, got %d", n)
	}
	// Ban the second admin -> not counted, and banned flag set.
	if err := r.SetUserBanned(ctx, second, true); err != nil {
		t.Fatal(err)
	}
	if n, _ := r.CountAdmins(ctx); n != 1 {
		t.Fatalf("after ban want 1 admin, got %d", n)
	}
	u2, _ := r.GetUser(ctx, second)
	if !u2.Banned {
		t.Error("second user should be banned")
	}
	_ = first
}

func TestConnectionCRUD(t *testing.T) {
	r, ctx := testRepo(t)
	if err := r.CreateConnection(ctx, "feishu", "acme", "Acme", "cli_a", "lark", "enc-secret"); err != nil {
		t.Fatal(err)
	}
	conns, err := r.ListConnections(ctx)
	if err != nil || len(conns) != 1 {
		t.Fatalf("list: %v len=%d", err, len(conns))
	}
	if !conns[0].HasSecret || conns[0].Key != "acme" || conns[0].Type != "feishu" {
		t.Errorf("bad conn: %+v", conns[0])
	}
	cfgs, _ := r.ListConnectionConfigs(ctx)
	if len(cfgs) != 1 || cfgs[0].AppSecretEnc != "enc-secret" {
		t.Errorf("config secret round-trip failed: %+v", cfgs)
	}
	// Update without secret keeps it.
	if err := r.UpdateConnection(ctx, conns[0].ID, "Acme2", "cli_b", "feishu", nil); err != nil {
		t.Fatal(err)
	}
	cfgs, _ = r.ListConnectionConfigs(ctx)
	if cfgs[0].AppSecretEnc != "enc-secret" || cfgs[0].AppID != "cli_b" {
		t.Errorf("update-keep-secret failed: %+v", cfgs[0])
	}
	if err := r.DeleteConnection(ctx, conns[0].ID); err != nil {
		t.Fatal(err)
	}
	if n, _ := r.CountConnections(ctx); n != 0 {
		t.Fatalf("want 0 connections after delete, got %d", n)
	}
}
