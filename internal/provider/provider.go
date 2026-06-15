// Package provider defines the seam that keeps docvault multi-source. Each cloud-document
// source (Feishu today; Google Workspace / Office 365 later) implements Provider.
package provider

import (
	"context"
	"time"
)

// Token is a provider OAuth token pair.
type Token struct {
	AccessToken      string
	RefreshToken     string
	AccessExpiresAt  time.Time
	RefreshExpiresAt time.Time
}

// Expired reports whether the access token is past (or near) expiry.
func (t *Token) Expired() bool {
	if t.AccessExpiresAt.IsZero() {
		return false
	}
	return time.Now().After(t.AccessExpiresAt.Add(-60 * time.Second))
}

// Identity is the authorizing end user as the provider sees them.
type Identity struct {
	ExternalUserID string
	DisplayName    string
	Email          string
	AvatarURL      string
}

// Item is one document/file/folder discovered during List.
type Item struct {
	ExternalID string // provider token/id
	Title      string
	DocType    string // docx, doc, sheet, bitable, slides, file, folder, ...
	SourcePath string // parent folder path (for a folder, its containing path)
	OwnerID    string // provider id of the owner (for delete-permission gating); empty if unknown
	IsFolder   bool   // true when this item is a folder container, not an archivable doc
}

// Blob is exported bytes ready to store.
type Blob struct {
	Filename    string
	Format      string // extension without dot: docx, xlsx, pdf, ...
	ContentType string
	Data        []byte
}

// Provider abstracts one cloud-document source.
type Provider interface {
	// Key is the stable provider identifier, e.g. "feishu".
	Key() string
	// AuthCodeURL builds the OAuth authorization URL for step 1 of the flow.
	AuthCodeURL(state, redirectURI string) string
	// Exchange swaps an authorization code for tokens and the authorizing
	// user's identity (both come from the provider's token response).
	Exchange(ctx context.Context, code, redirectURI string) (*Token, *Identity, error)
	// Refresh obtains a fresh access token from a refresh token.
	Refresh(ctx context.Context, refreshToken string) (*Token, error)
	// List enumerates everything the token can reach.
	List(ctx context.Context, tok *Token) ([]Item, error)
	// Export downloads/exports a single item to portable bytes.
	Export(ctx context.Context, tok *Token, item Item) (*Blob, error)
	// Delete removes the cloud original (moved to the provider's trash where
	// supported). Callers must enforce ownership/archival guards first.
	Delete(ctx context.Context, tok *Token, item Item) error
}

// Registry holds the configured providers by key.
type Registry struct {
	providers map[string]Provider
}

func NewRegistry(ps ...Provider) *Registry {
	m := make(map[string]Provider, len(ps))
	for _, p := range ps {
		m[p.Key()] = p
	}
	return &Registry{providers: m}
}

// Get returns the provider for key, or nil.
func (r *Registry) Get(key string) Provider { return r.providers[key] }

// Keys lists registered provider keys.
func (r *Registry) Keys() []string {
	out := make([]string, 0, len(r.providers))
	for k := range r.providers {
		out = append(out, k)
	}
	return out
}
