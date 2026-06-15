// Package models holds docvault's core domain types, mirroring the Postgres schema.
package models

import "time"

// User is a docvault account. Identity is derived from the first provider the user
// authorizes; additional provider accounts can be linked to the same user later.
type User struct {
	ID          string    `json:"id"`
	DisplayName string    `json:"display_name"`
	Email       string    `json:"email"`
	AvatarURL   string    `json:"avatar_url"`
	CreatedAt   time.Time `json:"created_at"`
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
}
