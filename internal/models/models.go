// Package models holds docvault's core domain types, mirroring the Postgres schema.
package models

import "time"

// Roles for docvault accounts.
const (
	RoleAdmin  = "admin"
	RoleMember = "member"
)

// User is a docvault account. Identity is derived from the first provider the user
// authorizes; additional provider accounts can be linked to the same user later.
type User struct {
	ID          string    `json:"id"`
	DisplayName string    `json:"display_name"`
	Email       string    `json:"email"`
	AvatarURL   string    `json:"avatar_url"`
	Role        string    `json:"role"`   // "admin" | "member"
	Banned      bool      `json:"banned"` // banned users cannot access the system
	CreatedAt   time.Time `json:"created_at"`
}

// IsAdmin reports whether the user has the admin role.
func (u *User) IsAdmin() bool { return u.Role == RoleAdmin }

// Connection is a DB-stored Feishu/Lark org connection, editable by admins. The
// app secret is never serialized to clients (HasSecret signals whether one is set).
type Connection struct {
	ID        string    `json:"id"`
	Key       string    `json:"key"`
	Label     string    `json:"label"`
	AppID     string    `json:"app_id"`
	Domain    string    `json:"domain"`
	HasSecret bool      `json:"has_secret"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ProviderAccount is a user's OAuth link to one cloud-document provider.
// Access/refresh tokens are stored encrypted (see internal/crypto).
type ProviderAccount struct {
	ID                  string    `json:"id"`
	UserID              string    `json:"user_id"`
	Provider            string    `json:"provider"` // "feishu", later "google", "o365"
	ExternalUserID      string    `json:"external_user_id"`
	AccessTokenEnc      string    `json:"-"`
	RefreshTokenEnc     string    `json:"-"`
	AccessTokenExpires  time.Time `json:"access_token_expires"`
	RefreshTokenExpires time.Time `json:"refresh_token_expires"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// SyncJobStatus enumerates the lifecycle of a sync job.
type SyncJobStatus string

const (
	SyncQueued    SyncJobStatus = "queued"
	SyncRunning   SyncJobStatus = "running"
	SyncSucceeded SyncJobStatus = "succeeded"
	SyncFailed    SyncJobStatus = "failed"
)

// SyncJob is one durable unit of work in the Postgres-backed queue.
type SyncJob struct {
	ID                string        `json:"id"`
	UserID            string        `json:"user_id"`
	ProviderAccountID string        `json:"provider_account_id"`
	Provider          string        `json:"provider"`
	Status            SyncJobStatus `json:"status"`
	TotalItems        int           `json:"total_items"`
	DoneItems         int           `json:"done_items"`
	FailedItems       int           `json:"failed_items"`
	Error             string        `json:"error,omitempty"`
	CreatedAt         time.Time     `json:"created_at"`
	StartedAt         *time.Time    `json:"started_at,omitempty"`
	FinishedAt        *time.Time    `json:"finished_at,omitempty"`
}

// Document is one archived file stored in object storage.
type Document struct {
	ID         string    `json:"id"`
	UserID     string    `json:"user_id"`
	Provider   string    `json:"provider"`
	ExternalID string    `json:"external_id"` // provider's token/id for the source doc
	Title      string    `json:"title"`
	DocType    string    `json:"doc_type"`    // docx, sheet, bitable, slides, file, ...
	Format     string    `json:"format"`      // exported extension: docx, xlsx, pdf, ...
	SourcePath string    `json:"source_path"` // human-readable folder path in the provider
	ObjectKey  string    `json:"object_key"`  // S3 key
	SizeBytes  int64     `json:"size_bytes"`
	SyncedAt   time.Time `json:"synced_at"`

	// OwnerExternalID is the provider id of the document owner. Deletion of the
	// cloud original is gated to the owning user only.
	OwnerExternalID string `json:"-"`
	// SourceDeletedAt is set once the cloud original has been deleted (the data
	// now lives only in docvault).
	SourceDeletedAt *time.Time `json:"source_deleted_at,omitempty"`
	// Deletable is computed per request: true when the signed-in user owns the
	// doc, it is archived, and the original has not already been deleted.
	Deletable bool `json:"deletable"`
}

// Folder is a source folder discovered during sync. Its cloud original can be
// deleted (cascading to trash) only once everything under it is archived.
type Folder struct {
	ID              string     `json:"id"`
	UserID          string     `json:"user_id"`
	Provider        string     `json:"provider"`
	ExternalID      string     `json:"external_id"` // folder token
	Title           string     `json:"title"`
	SourcePath      string     `json:"source_path"` // the folder's own full path
	OwnerExternalID string     `json:"-"`
	SyncedAt        time.Time  `json:"synced_at"`
	SourceDeletedAt *time.Time `json:"source_deleted_at,omitempty"`

	// Computed per request.
	Deletable    bool   `json:"deletable"`
	NotDeletable string `json:"not_deletable_reason,omitempty"` // why it can't be deleted
}
