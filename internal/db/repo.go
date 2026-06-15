package db

import (
	"context"
	"errors"
	"time"

	"github.com/hoveychen/docvault/internal/models"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// Repo is the data-access layer over the Postgres pool.
type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

// LinkAccount upserts a user + provider account on OAuth callback. If the
// (provider, external_user_id) pair already exists, it refreshes the stored
// tokens and profile; otherwise it creates a new user and account. Returns the
// docvault user id and provider account id.
func (r *Repo) LinkAccount(ctx context.Context, p ProviderAccountUpsert) (userID, accountID string, err error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	err = tx.QueryRow(ctx,
		`SELECT id, user_id FROM provider_accounts WHERE provider=$1 AND external_user_id=$2`,
		p.Provider, p.ExternalUserID,
	).Scan(&accountID, &userID)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// The very first user to ever sign in becomes the initial admin.
		role := "member"
		var userCount int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&userCount); err != nil {
			return "", "", err
		}
		if userCount == 0 {
			role = "admin"
		}
		if err = tx.QueryRow(ctx,
			`INSERT INTO users(display_name, email, avatar_url, role) VALUES($1,$2,$3,$4) RETURNING id`,
			p.DisplayName, p.Email, p.AvatarURL, role,
		).Scan(&userID); err != nil {
			return "", "", err
		}
		if err = tx.QueryRow(ctx,
			`INSERT INTO provider_accounts
			   (user_id, provider, external_user_id, access_token_enc, refresh_token_enc,
			    access_token_expires, refresh_token_expires)
			 VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
			userID, p.Provider, p.ExternalUserID, p.AccessTokenEnc, p.RefreshTokenEnc,
			p.AccessTokenExpires, p.RefreshTokenExpires,
		).Scan(&accountID); err != nil {
			return "", "", err
		}
	case err != nil:
		return "", "", err
	default:
		if _, err = tx.Exec(ctx,
			`UPDATE provider_accounts
			    SET access_token_enc=$1, refresh_token_enc=$2,
			        access_token_expires=$3, refresh_token_expires=$4, updated_at=now()
			  WHERE id=$5`,
			p.AccessTokenEnc, p.RefreshTokenEnc, p.AccessTokenExpires, p.RefreshTokenExpires, accountID,
		); err != nil {
			return "", "", err
		}
		if _, err = tx.Exec(ctx,
			`UPDATE users SET display_name=$1, email=$2, avatar_url=$3 WHERE id=$4`,
			p.DisplayName, p.Email, p.AvatarURL, userID,
		); err != nil {
			return "", "", err
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return "", "", err
	}
	return userID, accountID, nil
}

// ProviderAccountUpsert carries the data needed to link/refresh an account.
type ProviderAccountUpsert struct {
	Provider            string
	ExternalUserID      string
	DisplayName         string
	Email               string
	AvatarURL           string
	AccessTokenEnc      string
	RefreshTokenEnc     string
	AccessTokenExpires  time.Time
	RefreshTokenExpires time.Time
}

const userCols = `SELECT id, display_name, email, avatar_url, role, banned, created_at FROM users`

func scanUser(row pgx.Row) (*models.User, error) {
	u := &models.User{}
	err := row.Scan(&u.ID, &u.DisplayName, &u.Email, &u.AvatarURL, &u.Role, &u.Banned, &u.CreatedAt)
	return u, err
}

func (r *Repo) GetUser(ctx context.Context, id string) (*models.User, error) {
	u, err := scanUser(r.pool.QueryRow(ctx, userCols+` WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

// ListUsers returns all users (admin view), newest first.
func (r *Repo) ListUsers(ctx context.Context) ([]models.User, error) {
	rows, err := r.pool.Query(ctx, userCols+` ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

// SetUserRole updates a user's role.
func (r *Repo) SetUserRole(ctx context.Context, id, role string) error {
	ct, err := r.pool.Exec(ctx, `UPDATE users SET role=$1 WHERE id=$2`, role, id)
	if err == nil && ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// SetUserBanned bans/unbans a user.
func (r *Repo) SetUserBanned(ctx context.Context, id string, banned bool) error {
	ct, err := r.pool.Exec(ctx, `UPDATE users SET banned=$1 WHERE id=$2`, banned, id)
	if err == nil && ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// CountAdmins returns how many non-banned admins exist (to prevent removing the last one).
func (r *Repo) CountAdmins(ctx context.Context) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE role='admin' AND NOT banned`).Scan(&n)
	return n, err
}

func (r *Repo) GetAccount(ctx context.Context, id string) (*models.ProviderAccount, error) {
	return r.scanAccount(r.pool.QueryRow(ctx, accountCols+` WHERE id=$1`, id))
}

// GetAccountForUser returns the user's account for a given provider.
func (r *Repo) GetAccountForUser(ctx context.Context, userID, provider string) (*models.ProviderAccount, error) {
	return r.scanAccount(r.pool.QueryRow(ctx, accountCols+` WHERE user_id=$1 AND provider=$2`, userID, provider))
}

// GetAccountsForUser returns all provider accounts linked to a user (normally one).
func (r *Repo) GetAccountsForUser(ctx context.Context, userID string) ([]*models.ProviderAccount, error) {
	rows, err := r.pool.Query(ctx, accountCols+` WHERE user_id=$1 ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.ProviderAccount
	for rows.Next() {
		a, err := r.scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

const accountCols = `SELECT id, user_id, provider, external_user_id, access_token_enc, refresh_token_enc,
	access_token_expires, refresh_token_expires, created_at, updated_at FROM provider_accounts`

func (r *Repo) scanAccount(row pgx.Row) (*models.ProviderAccount, error) {
	a := &models.ProviderAccount{}
	var atExp, rtExp *time.Time
	err := row.Scan(&a.ID, &a.UserID, &a.Provider, &a.ExternalUserID,
		&a.AccessTokenEnc, &a.RefreshTokenEnc, &atExp, &rtExp, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if atExp != nil {
		a.AccessTokenExpires = *atExp
	}
	if rtExp != nil {
		a.RefreshTokenExpires = *rtExp
	}
	return a, nil
}

// UpdateAccountTokens persists refreshed tokens.
func (r *Repo) UpdateAccountTokens(ctx context.Context, accountID, accessEnc, refreshEnc string, accessExp, refreshExp time.Time) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE provider_accounts
		    SET access_token_enc=$1, refresh_token_enc=$2,
		        access_token_expires=$3, refresh_token_expires=$4, updated_at=now()
		  WHERE id=$5`,
		accessEnc, refreshEnc, accessExp, refreshExp, accountID)
	return err
}

// --- sync jobs (queue) ---

func (r *Repo) EnqueueSyncJob(ctx context.Context, userID, accountID, provider string) (string, error) {
	var id string
	err := r.pool.QueryRow(ctx,
		`INSERT INTO sync_jobs(user_id, provider_account_id, provider, status)
		 VALUES($1,$2,$3,'queued') RETURNING id`,
		userID, accountID, provider).Scan(&id)
	return id, err
}

// ClaimJob atomically claims the oldest queued job, marking it running.
// Returns ErrNotFound when the queue is empty.
func (r *Repo) ClaimJob(ctx context.Context) (*models.SyncJob, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var id string
	err = tx.QueryRow(ctx,
		`SELECT id FROM sync_jobs WHERE status='queued'
		 ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	job := &models.SyncJob{}
	err = tx.QueryRow(ctx,
		`UPDATE sync_jobs SET status='running', started_at=now() WHERE id=$1
		 RETURNING id, user_id, provider_account_id, provider, status,
		           total_items, done_items, failed_items, created_at, started_at`,
		id,
	).Scan(&job.ID, &job.UserID, &job.ProviderAccountID, &job.Provider, &job.Status,
		&job.TotalItems, &job.DoneItems, &job.FailedItems, &job.CreatedAt, &job.StartedAt)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return job, nil
}

func (r *Repo) UpdateJobProgress(ctx context.Context, jobID string, total, done, failed int) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE sync_jobs SET total_items=$1, done_items=$2, failed_items=$3 WHERE id=$4`,
		total, done, failed, jobID)
	return err
}

func (r *Repo) FinishJob(ctx context.Context, jobID string, status models.SyncJobStatus, errMsg string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE sync_jobs SET status=$1, error=$2, finished_at=now() WHERE id=$3`,
		status, errMsg, jobID)
	return err
}

// LatestJob returns the most recent job for a user, or ErrNotFound.
func (r *Repo) LatestJob(ctx context.Context, userID string) (*models.SyncJob, error) {
	job := &models.SyncJob{}
	err := r.pool.QueryRow(ctx,
		`SELECT id, user_id, provider_account_id, provider, status,
		        total_items, done_items, failed_items, error, created_at, started_at, finished_at
		   FROM sync_jobs WHERE user_id=$1 ORDER BY created_at DESC LIMIT 1`, userID,
	).Scan(&job.ID, &job.UserID, &job.ProviderAccountID, &job.Provider, &job.Status,
		&job.TotalItems, &job.DoneItems, &job.FailedItems, &job.Error,
		&job.CreatedAt, &job.StartedAt, &job.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return job, err
}

// HasActiveJob reports whether the user already has a queued/running job.
func (r *Repo) HasActiveJob(ctx context.Context, userID string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM sync_jobs WHERE user_id=$1 AND status IN ('queued','running'))`,
		userID).Scan(&exists)
	return exists, err
}

// --- documents ---

// UpsertDocument records (or refreshes) an archived document by natural key.
func (r *Repo) UpsertDocument(ctx context.Context, d *models.Document) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO documents
		   (user_id, provider, external_id, title, doc_type, format, source_path, object_key, size_bytes, owner_external_id, synced_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10, now())
		 ON CONFLICT (user_id, provider, external_id) DO UPDATE SET
		   title=EXCLUDED.title, doc_type=EXCLUDED.doc_type, format=EXCLUDED.format,
		   source_path=EXCLUDED.source_path, object_key=EXCLUDED.object_key,
		   size_bytes=EXCLUDED.size_bytes, owner_external_id=EXCLUDED.owner_external_id, synced_at=now()`,
		d.UserID, d.Provider, d.ExternalID, d.Title, d.DocType, d.Format,
		d.SourcePath, d.ObjectKey, d.SizeBytes, d.OwnerExternalID)
	return err
}

const docCols = `SELECT id, user_id, provider, external_id, title, doc_type, format,
	source_path, object_key, size_bytes, owner_external_id, source_deleted_at, synced_at FROM documents`

func scanDocument(row pgx.Row) (*models.Document, error) {
	d := &models.Document{}
	err := row.Scan(&d.ID, &d.UserID, &d.Provider, &d.ExternalID, &d.Title, &d.DocType,
		&d.Format, &d.SourcePath, &d.ObjectKey, &d.SizeBytes, &d.OwnerExternalID,
		&d.SourceDeletedAt, &d.SyncedAt)
	return d, err
}

func (r *Repo) ListDocuments(ctx context.Context, userID string) ([]models.Document, error) {
	rows, err := r.pool.Query(ctx, docCols+` WHERE user_id=$1 ORDER BY synced_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.Document
	for rows.Next() {
		d, err := scanDocument(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// ArchiveStats reports the per-type archived/unarchived breakdown for a user,
// plus folder count. "archived" means a downloadable copy exists (object_key set);
// "unarchived" means the item was recorded but never exported (object_key empty).
func (r *Repo) ArchiveStats(ctx context.Context, userID string) (*models.ArchiveStats, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT doc_type,
		       count(*)                                              AS total,
		       count(*) FILTER (WHERE object_key <> '')              AS archived,
		       count(*) FILTER (WHERE object_key = '')               AS unarchived,
		       count(*) FILTER (WHERE source_deleted_at IS NOT NULL) AS deleted
		FROM documents WHERE user_id=$1
		GROUP BY doc_type ORDER BY archived DESC, total DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stats := &models.ArchiveStats{ByType: []models.TypeStat{}}
	for rows.Next() {
		var t models.TypeStat
		var deleted int
		if err := rows.Scan(&t.DocType, &t.Total, &t.Archived, &t.Unarchived, &deleted); err != nil {
			return nil, err
		}
		stats.Total += t.Total
		stats.Archived += t.Archived
		stats.Unarchived += t.Unarchived
		stats.SourceDeleted += deleted
		stats.ByType = append(stats.ByType, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM folders WHERE user_id=$1`, userID).Scan(&stats.Folders); err != nil {
		return nil, err
	}
	return stats, nil
}

// GetDocument fetches a single document scoped to the owning user.
func (r *Repo) GetDocument(ctx context.Context, userID, id string) (*models.Document, error) {
	d, err := scanDocument(r.pool.QueryRow(ctx, docCols+` WHERE id=$1 AND user_id=$2`, id, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

// GetDocumentsByIDs returns the user's documents for the given ids (scoped to the user).
func (r *Repo) GetDocumentsByIDs(ctx context.Context, userID string, ids []string) ([]models.Document, error) {
	rows, err := r.pool.Query(ctx, docCols+` WHERE user_id=$1 AND id = ANY($2)`, userID, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.Document
	for rows.Next() {
		d, err := scanDocument(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// MarkSourceDeleted records that a document's cloud original has been deleted.
func (r *Repo) MarkSourceDeleted(ctx context.Context, userID, id string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE documents SET source_deleted_at=now() WHERE id=$1 AND user_id=$2`, id, userID)
	return err
}

// --- folders ---

// UpsertFolder records (or refreshes) a source folder by natural key.
func (r *Repo) UpsertFolder(ctx context.Context, f *models.Folder) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO folders (user_id, provider, external_id, title, source_path, owner_external_id, synced_at)
		 VALUES ($1,$2,$3,$4,$5,$6, now())
		 ON CONFLICT (user_id, provider, external_id) DO UPDATE SET
		   title=EXCLUDED.title, source_path=EXCLUDED.source_path,
		   owner_external_id=EXCLUDED.owner_external_id, synced_at=now()`,
		f.UserID, f.Provider, f.ExternalID, f.Title, f.SourcePath, f.OwnerExternalID)
	return err
}

const folderCols = `SELECT id, user_id, provider, external_id, title, source_path,
	owner_external_id, source_deleted_at, synced_at FROM folders`

func scanFolder(row pgx.Row) (*models.Folder, error) {
	f := &models.Folder{}
	err := row.Scan(&f.ID, &f.UserID, &f.Provider, &f.ExternalID, &f.Title,
		&f.SourcePath, &f.OwnerExternalID, &f.SourceDeletedAt, &f.SyncedAt)
	return f, err
}

func (r *Repo) ListFolders(ctx context.Context, userID string) ([]models.Folder, error) {
	rows, err := r.pool.Query(ctx, folderCols+` WHERE user_id=$1 ORDER BY source_path`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Folder
	for rows.Next() {
		f, err := scanFolder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *f)
	}
	return out, rows.Err()
}

func (r *Repo) GetFoldersByIDs(ctx context.Context, userID string, ids []string) ([]models.Folder, error) {
	rows, err := r.pool.Query(ctx, folderCols+` WHERE user_id=$1 AND id = ANY($2)`, userID, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Folder
	for rows.Next() {
		f, err := scanFolder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *f)
	}
	return out, rows.Err()
}

// FolderDeletability reports whether a folder's cloud original can be safely
// deleted: the user must own the folder, it must not already be deleted, and
// every document anywhere under its path must be archived and owned by the user
// (so nothing un-backed-up or belonging to someone else is lost in the cascade).
func (r *Repo) FolderDeletability(ctx context.Context, userID string, f *models.Folder, userExtID string) (bool, string) {
	if f.SourceDeletedAt != nil {
		return false, "原件已删除"
	}
	if userExtID == "" || f.OwnerExternalID == "" || f.OwnerExternalID != userExtID {
		return false, "非本人拥有，无法删除"
	}
	var unarchived, notOwned int
	err := r.pool.QueryRow(ctx,
		`SELECT
		   count(*) FILTER (WHERE object_key='' AND source_deleted_at IS NULL),
		   count(*) FILTER (WHERE owner_external_id IS DISTINCT FROM $3)
		 FROM documents
		 WHERE user_id=$1 AND (source_path=$2 OR starts_with(source_path, $2 || '/'))`,
		userID, f.SourcePath, userExtID).Scan(&unarchived, &notOwned)
	if err != nil {
		return false, "无法校验文件夹内容"
	}
	if unarchived > 0 {
		return false, "文件夹内有未归档的文档"
	}
	if notOwned > 0 {
		return false, "文件夹内有非本人拥有的文档"
	}
	return true, ""
}

// MarkFolderTreeSourceDeleted marks the folder and every document under its path
// as having their cloud originals deleted.
func (r *Repo) MarkFolderTreeSourceDeleted(ctx context.Context, userID string, f *models.Folder) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx,
		`UPDATE documents SET source_deleted_at=now()
		   WHERE user_id=$1 AND (source_path=$2 OR starts_with(source_path, $2 || '/'))
		     AND source_deleted_at IS NULL`,
		userID, f.SourcePath); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE folders SET source_deleted_at=now() WHERE id=$1 AND user_id=$2`,
		f.ID, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// --- connections (admin-managed provider configs) ---

// ConnectionConfig carries a connection plus its encrypted secret, for building
// providers in the app layer (which holds the cipher).
type ConnectionConfig struct {
	Key          string
	Label        string
	AppID        string
	Domain       string
	AppSecretEnc string
}

// ListConnectionConfigs returns every connection with its encrypted secret.
func (r *Repo) ListConnectionConfigs(ctx context.Context) ([]ConnectionConfig, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT key, label, app_id, domain, app_secret_enc FROM feishu_connections ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConnectionConfig
	for rows.Next() {
		var c ConnectionConfig
		if err := rows.Scan(&c.Key, &c.Label, &c.AppID, &c.Domain, &c.AppSecretEnc); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListConnections returns connections for the admin UI (no secrets).
func (r *Repo) ListConnections(ctx context.Context) ([]models.Connection, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, key, label, app_id, domain, app_secret_enc <> '', created_at, updated_at
		   FROM feishu_connections ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Connection
	for rows.Next() {
		var c models.Connection
		if err := rows.Scan(&c.ID, &c.Key, &c.Label, &c.AppID, &c.Domain, &c.HasSecret,
			&c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *Repo) CountConnections(ctx context.Context) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `SELECT count(*) FROM feishu_connections`).Scan(&n)
	return n, err
}

// CreateConnection inserts a new connection (secret already encrypted).
func (r *Repo) CreateConnection(ctx context.Context, key, label, appID, domain, secretEnc string) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO feishu_connections(key, label, app_id, app_secret_enc, domain)
		 VALUES($1,$2,$3,$4,$5)`,
		key, label, appID, secretEnc, domain)
	return err
}

// UpdateConnection updates a connection by id. A nil secretEnc keeps the existing secret.
func (r *Repo) UpdateConnection(ctx context.Context, id, label, appID, domain string, secretEnc *string) error {
	var ct interface{ RowsAffected() int64 }
	var err error
	if secretEnc != nil {
		ct, err = r.pool.Exec(ctx,
			`UPDATE feishu_connections SET label=$1, app_id=$2, domain=$3, app_secret_enc=$4, updated_at=now() WHERE id=$5`,
			label, appID, domain, *secretEnc, id)
	} else {
		ct, err = r.pool.Exec(ctx,
			`UPDATE feishu_connections SET label=$1, app_id=$2, domain=$3, updated_at=now() WHERE id=$4`,
			label, appID, domain, id)
	}
	if err == nil && ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (r *Repo) DeleteConnection(ctx context.Context, id string) error {
	ct, err := r.pool.Exec(ctx, `DELETE FROM feishu_connections WHERE id=$1`, id)
	if err == nil && ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// DueAccount identifies a provider account that should be auto-synced.
type DueAccount struct {
	AccountID string
	UserID    string
	Provider  string
}

// AccountsDueForSync returns accounts with no queued/running job and no
// successful sync finished after cutoff (= now - interval). Banned users are
// excluded. Used by the scheduler for periodic background sync.
func (r *Repo) AccountsDueForSync(ctx context.Context, cutoff time.Time) ([]DueAccount, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT pa.id, pa.user_id, pa.provider
		   FROM provider_accounts pa
		   JOIN users u ON u.id = pa.user_id AND NOT u.banned
		  WHERE NOT EXISTS (
		          SELECT 1 FROM sync_jobs j
		           WHERE j.provider_account_id = pa.id AND j.status IN ('queued','running'))
		    AND NOT EXISTS (
		          SELECT 1 FROM sync_jobs j
		           WHERE j.provider_account_id = pa.id
		             AND j.status = 'succeeded' AND j.finished_at > $1)`,
		cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DueAccount
	for rows.Next() {
		var d DueAccount
		if err := rows.Scan(&d.AccountID, &d.UserID, &d.Provider); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
