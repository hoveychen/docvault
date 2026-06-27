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

// Connection is a DB-stored provider org connection, editable by admins. Type
// selects the provider implementation (feishu, google, microsoft, tencent). The
// app secret is never serialized to clients (HasSecret signals whether one is set).
type Connection struct {
	ID        string    `json:"id"`
	Type      string    `json:"provider_type"`
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

// AdminSyncJob is a sync job enriched with its owner's display name, for the
// admin sync-queue panel. The embedded SyncJob's fields flatten into the same
// JSON object.
type AdminSyncJob struct {
	SyncJob
	DisplayName string `json:"display_name"`
}

// JobItem statuses: one row per source item snapshotted for a sliced sync job.
const (
	JobItemPending = "pending"
	JobItemDone    = "done"
	JobItemFailed  = "failed"
)

// JobItem is one unit of work in a sliced sync job. On first claim the engine
// snapshots every item the provider lists into sync_job_items, then processes
// them across time-bounded slices, marking each done/failed as it goes.
type JobItem struct {
	ID              int64  `json:"id"`
	JobID           string `json:"job_id"`
	ExternalID      string `json:"external_id"`
	Title           string `json:"title"`
	DocType         string `json:"doc_type"`
	SourcePath      string `json:"source_path"`
	OwnerExternalID string `json:"owner_external_id"`
	IsFolder        bool   `json:"is_folder"`
	Status          string `json:"status"` // pending | done | failed
	Attempts        int    `json:"attempts"`
	Error           string `json:"error,omitempty"`
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
	// Attachments is computed per request: the embedded objects (e.g. Feishu
	// file-attachment blocks) stored as sidecars for this document.
	Attachments []Attachment `json:"attachments,omitempty"`
}

// Attachment is an embedded object (e.g. a Feishu file-attachment block) that
// the parent document's main export does not include. It is stored as a sidecar
// object and linked back to its document.
type Attachment struct {
	ID          string    `json:"id"`
	DocumentID  string    `json:"document_id"`
	ExternalID  string    `json:"-"` // provider media/file token (dedupe key)
	Filename    string    `json:"filename"`
	Format      string    `json:"format"`
	ContentType string    `json:"-"`
	ObjectKey   string    `json:"-"`
	SizeBytes   int64     `json:"size_bytes"`
	CreatedAt   time.Time `json:"created_at"`
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

// ArchiveStats summarizes a user's archive: how much is actually downloadable
// (object_key present) vs. recorded-but-not-archived (export failed / unsupported
// type), broken down by document type. Powers an ops/health view.
type ArchiveStats struct {
	Total         int        `json:"total"`          // all documents
	Archived      int        `json:"archived"`       // have a downloadable copy (object_key<>'')
	Unarchived    int        `json:"unarchived"`     // no copy (object_key='')
	SourceDeleted int        `json:"source_deleted"` // cloud original moved to trash
	Folders       int        `json:"folders"`        // source folders recorded
	ByType        []TypeStat `json:"by_type"`        // per doc_type breakdown, archived desc
}

// TypeStat is the archived/unarchived split for one document type.
type TypeStat struct {
	DocType    string `json:"doc_type"`
	Total      int    `json:"total"`
	Archived   int    `json:"archived"`
	Unarchived int    `json:"unarchived"`
}

// UserArchiveStat is one user's archive totals, for the admin per-user panel.
type UserArchiveStat struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
	Total       int    `json:"total"`
	Archived    int    `json:"archived"`
	Unarchived  int    `json:"unarchived"`
}

// FailureReason is a distinct sync-item error message and how many items hit it,
// for the admin failure-diagnostics view.
type FailureReason struct {
	Error string `json:"error"`
	Count int    `json:"count"`
}
