package auth

import (
	"context"
	"fmt"

	"github.com/hoveychen/docvault/internal/crypto"
	"github.com/hoveychen/docvault/internal/db"
	"github.com/hoveychen/docvault/internal/models"
	"github.com/hoveychen/docvault/internal/provider"
)

// TokenManager decrypts stored provider tokens, refreshes them when expired,
// and persists the refreshed values.
type TokenManager struct {
	repo     *db.Repo
	cipher   *crypto.Cipher
	registry *provider.Registry
}

func NewTokenManager(repo *db.Repo, cipher *crypto.Cipher, registry *provider.Registry) *TokenManager {
	return &TokenManager{repo: repo, cipher: cipher, registry: registry}
}

// Encrypt returns the encrypted access/refresh token pair for storage.
func (m *TokenManager) Encrypt(tok *provider.Token) (accessEnc, refreshEnc string, err error) {
	if accessEnc, err = m.cipher.Encrypt(tok.AccessToken); err != nil {
		return "", "", err
	}
	if refreshEnc, err = m.cipher.Encrypt(tok.RefreshToken); err != nil {
		return "", "", err
	}
	return accessEnc, refreshEnc, nil
}

// ValidToken returns a non-expired token for the account, refreshing and
// persisting it if the stored access token has expired.
func (m *TokenManager) ValidToken(ctx context.Context, acct *models.ProviderAccount) (*provider.Token, error) {
	access, err := m.cipher.Decrypt(acct.AccessTokenEnc)
	if err != nil {
		return nil, fmt.Errorf("decrypt access token: %w", err)
	}
	refresh, err := m.cipher.Decrypt(acct.RefreshTokenEnc)
	if err != nil {
		return nil, fmt.Errorf("decrypt refresh token: %w", err)
	}

	tok := &provider.Token{
		AccessToken:      access,
		RefreshToken:     refresh,
		AccessExpiresAt:  acct.AccessTokenExpires,
		RefreshExpiresAt: acct.RefreshTokenExpires,
	}
	if !tok.Expired() {
		return tok, nil
	}

	prov := m.registry.Get(acct.Provider)
	if prov == nil {
		return nil, fmt.Errorf("unknown provider %q", acct.Provider)
	}
	refreshed, err := prov.Refresh(ctx, refresh)
	if err != nil {
		return nil, fmt.Errorf("refresh token: %w", err)
	}
	// Feishu rotates the refresh token; keep the new one if returned.
	if refreshed.RefreshToken == "" {
		refreshed.RefreshToken = refresh
	}
	accessEnc, refreshEnc, err := m.Encrypt(refreshed)
	if err != nil {
		return nil, err
	}
	if err := m.repo.UpdateAccountTokens(ctx, acct.ID, accessEnc, refreshEnc,
		refreshed.AccessExpiresAt, refreshed.RefreshExpiresAt); err != nil {
		return nil, fmt.Errorf("persist refreshed token: %w", err)
	}
	// Mirror the refreshed token back onto the in-memory account so a later call
	// on the same acct (the sync engine reuses one acct across every item in a
	// slice) sees the fresh token instead of re-refreshing with the now-rotated
	// refresh token, which Feishu rejects (code=20026, "refresh token ... used").
	acct.AccessTokenEnc = accessEnc
	acct.RefreshTokenEnc = refreshEnc
	acct.AccessTokenExpires = refreshed.AccessExpiresAt
	acct.RefreshTokenExpires = refreshed.RefreshExpiresAt
	return refreshed, nil
}
