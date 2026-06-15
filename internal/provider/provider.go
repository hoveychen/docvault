// Package provider defines the seam that keeps docvault multi-source. Each cloud-document
// source (Feishu today; Google Workspace / Office 365 later) implements Provider.
package provider

import (
	"context"
	"sync"
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

// Attachment is one embedded object (e.g. a file-attachment block) extracted
// from a document that the main Export does not include. It is stored as a
// sidecar object alongside the parent document. ExternalID is the provider's
// stable token for the embedded object, used to dedupe across re-syncs.
type Attachment struct {
	ExternalID string
	Blob       *Blob
}

// Provider abstracts one cloud-document source (one org connection).
type Provider interface {
	// Key is the stable provider identifier used in routes and stored on
	// documents/accounts (e.g. "feishu" or "org-acme").
	Key() string
	// Label is the human-readable name shown on the login page.
	Label() string
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

// AttachmentExporter is an optional Provider capability: returning the embedded
// objects (file-attachment blocks, etc.) that an item's main Export omits. The
// sync engine type-asserts for it and skips providers that don't implement it,
// so adding it to one provider requires no change to the others. Returning an
// empty slice (nil) means "no embedded objects" and is not an error.
type AttachmentExporter interface {
	Attachments(ctx context.Context, tok *Token, item Item) ([]Attachment, error)
}

// Registry holds the configured providers by key. It is safe for concurrent use
// and can be hot-reloaded (Replace) when an admin edits connections.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

func NewRegistry(ps ...Provider) *Registry {
	r := &Registry{providers: map[string]Provider{}}
	r.Replace(ps)
	return r
}

// Replace atomically swaps the registered providers.
func (r *Registry) Replace(ps []Provider) {
	m := make(map[string]Provider, len(ps))
	for _, p := range ps {
		m[p.Key()] = p
	}
	r.mu.Lock()
	r.providers = m
	r.mu.Unlock()
}

// Get returns the provider for key, or nil.
func (r *Registry) Get(key string) Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.providers[key]
}

// Keys lists registered provider keys.
func (r *Registry) Keys() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.providers))
	for k := range r.providers {
		out = append(out, k)
	}
	return out
}

// Info is a provider's key + display label, for the login page.
type Info struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// List returns key+label for every registered provider.
func (r *Registry) List() []Info {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Info, 0, len(r.providers))
	for _, p := range r.providers {
		out = append(out, Info{Key: p.Key(), Label: p.Label()})
	}
	return out
}
