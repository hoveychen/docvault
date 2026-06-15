package auth

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/hoveychen/docvault/internal/crypto"
	"github.com/hoveychen/docvault/internal/db"
	"github.com/hoveychen/docvault/internal/provider"
)

// rotatingProvider models Feishu's single-use refresh-token rotation: each
// Refresh returns a brand-new refresh token and invalidates the one it was
// handed. Reusing an already-consumed refresh token fails exactly like Feishu's
// "code=20026 msg=refresh token is invalid, it may has been used".
type rotatingProvider struct {
	mu       sync.Mutex
	calls    int
	consumed map[string]bool // refresh tokens already spent
	next     int
}

func (p *rotatingProvider) Key() string                              { return "feishu" }
func (p *rotatingProvider) Label() string                            { return "Rotating" }
func (p *rotatingProvider) AuthCodeURL(state, redirectURI string) string { return "" }
func (p *rotatingProvider) Exchange(ctx context.Context, code, redirectURI string) (*provider.Token, *provider.Identity, error) {
	return nil, nil, nil
}
func (p *rotatingProvider) Refresh(ctx context.Context, refreshToken string) (*provider.Token, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.consumed[refreshToken] {
		return nil, fmt.Errorf("feishu refresh: code=20026 msg=refresh token is invalid, it may has been used")
	}
	p.consumed[refreshToken] = true
	p.next++
	return &provider.Token{
		AccessToken:      fmt.Sprintf("access-%d", p.next),
		RefreshToken:     fmt.Sprintf("refresh-%d", p.next),
		AccessExpiresAt:  time.Now().Add(time.Hour),
		RefreshExpiresAt: time.Now().Add(24 * time.Hour),
	}, nil
}
func (p *rotatingProvider) List(ctx context.Context, tok *provider.Token) ([]provider.Item, error) {
	return nil, nil
}
func (p *rotatingProvider) Export(ctx context.Context, tok *provider.Token, item provider.Item) (*provider.Blob, error) {
	return nil, nil
}
func (p *rotatingProvider) Delete(ctx context.Context, tok *provider.Token, item provider.Item) error {
	return nil
}

func testTokenManager(t *testing.T, prov provider.Provider) (*TokenManager, *db.Repo, context.Context, string) {
	t.Helper()
	url := os.Getenv("DOCVAULT_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set DOCVAULT_TEST_DATABASE_URL to run token integration tests")
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
	if _, err := pool.Exec(ctx, `TRUNCATE users, provider_accounts, documents, folders, sync_jobs, sync_job_items, feishu_connections RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	repo := db.NewRepo(pool)
	cipher, err := crypto.New(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	reg := provider.NewRegistry(prov)
	tm := NewTokenManager(repo, cipher, reg)

	accEnc, _ := cipher.Encrypt("access-0")
	refEnc, _ := cipher.Encrypt("refresh-0")
	_, accID, err := repo.LinkAccount(ctx, db.ProviderAccountUpsert{
		Provider: "feishu", ExternalUserID: "u1", DisplayName: "u1",
		AccessTokenEnc: accEnc, RefreshTokenEnc: refEnc,
		// Access token already expired -> ValidToken must refresh on first use.
		AccessTokenExpires:  time.Now().Add(-time.Hour),
		RefreshTokenExpires: time.Now().Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("link account: %v", err)
	}
	return tm, repo, ctx, accID
}

// Reusing the same in-memory account across consecutive refreshes (as the sync
// engine does: it loads acct once and calls ValidToken per item) must not
// re-spend an already-rotated refresh token. ValidToken persists the refreshed
// token to the DB, but before the fix it left the in-memory acct holding the
// stale expired access token + spent refresh token, so the second ValidToken on
// the same acct refreshed again with the consumed token and Feishu rejected it
// (code=20026). This reproduces the "token refresh mid-slice" failures that
// scheduled sync surfaced.
func TestValidTokenReusesRefreshedAccountInMemory(t *testing.T) {
	prov := &rotatingProvider{consumed: map[string]bool{}}
	tm, repo, ctx, accID := testTokenManager(t, prov)

	acct, err := repo.GetAccount(ctx, accID)
	if err != nil {
		t.Fatalf("get account: %v", err)
	}

	// First call: access expired -> exactly one refresh, rotating refresh-0 -> refresh-1.
	if _, err := tm.ValidToken(ctx, acct); err != nil {
		t.Fatalf("first ValidToken: %v", err)
	}

	// Second call on the SAME acct: the token is now fresh, so no refresh should
	// happen. Before the fix, acct still looked expired and ValidToken refreshed
	// again with the already-spent refresh-0, which Feishu rejects.
	if _, err := tm.ValidToken(ctx, acct); err != nil {
		t.Fatalf("second ValidToken must not re-refresh a rotated token: %v", err)
	}

	if prov.calls != 1 {
		t.Fatalf("want exactly 1 refresh across two ValidToken calls, got %d", prov.calls)
	}
}
