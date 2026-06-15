# docvault architecture

docvault is a multi-source cloud-document archival SaaS. A user signs in by OAuth-authorizing
a cloud-document provider (first provider: Feishu/Lark; Google Workspace and Office 365 are
planned). A background worker walks everything that user's token can reach, exports each online
document to a portable format (docx / xlsx / pdf / markdown), stores it in object storage, and
the web UI lets the user browse and download their archive.

## Why per-user OAuth (and not a tenant-wide token)

Feishu's OpenAPI has no admin/tenant scope that lets one app read every member's private
documents. A `tenant_access_token` only sees what the app itself owns or was granted; a
`user_access_token` only sees what that one authorizing user can access. So "back up the whole
org" is fundamentally **per-user**: every member must personally authorize once. docvault models
this directly — each signed-in user authorizes their own account, and the archive is scoped to
that user. Google Workspace / Office 365 will slot in behind the same per-account model.

## Components

```
                ┌─────────────┐         ┌──────────────────────────────┐
  Browser ────► │  server     │ ──────► │ Postgres                     │
  (React/Vite)  │ (cmd/server)│         │  users / provider_accounts   │
                │  REST API   │ ◄────── │  sync_jobs (queue) / documents│
                └─────┬───────┘         └──────────────────────────────┘
                      │ enqueue sync_job             ▲
                      ▼                              │ claim / update
                ┌─────────────┐   provider API  ┌────┴─────────┐   PUT   ┌─────────┐
                │  worker     │ ───────────────►│ Feishu / Lark │        │  MinIO  │
                │ (cmd/worker)│ ◄───────────────│  (oapi-sdk-go)│ ──────►│  (S3)   │
                └─────────────┘   export+download└──────────────┘        └─────────┘
```

- **server** (`cmd/server`) — HTTP API: OAuth login/callback, list documents, trigger sync,
  issue pre-signed download URLs. Stateless; JWT cookie sessions.
- **worker** (`cmd/worker`) — claims `sync_jobs` from Postgres (`SELECT ... FOR UPDATE SKIP
  LOCKED`), drives the provider to list + export + download, uploads bytes to S3, records a row
  in `documents`. No Redis — Postgres is the queue, keeping infra to two services.
- **Postgres** — metadata + durable job queue.
- **MinIO / S3** — object storage for the exported bytes. Objects are keyed
  `u/<user_id>/<provider>/<document_id>/<filename>`.

## Provider abstraction

`internal/provider.Provider` is the seam that keeps docvault multi-source:

```go
type Provider interface {
    Key() string                                           // "feishu", later "google", "o365"
    AuthCodeURL(state, redirectURI string) string          // step 1 of OAuth
    Exchange(ctx, code, redirectURI) (*Token, error)       // code -> tokens
    Refresh(ctx, refreshToken string) (*Token, error)
    Identity(ctx, tok *Token) (*Identity, error)           // who authorized
    List(ctx, tok *Token) ([]Item, error)                  // everything reachable
    Export(ctx, tok *Token, item Item) (*Blob, error)      // bytes + filename + mime
}
```

`internal/provider/feishu` implements it with `github.com/larksuite/oapi-sdk-go/v3`. Adding
Google Workspace later means a new package implementing the same interface plus a row in the
provider registry — nothing else changes.

## Sync lifecycle

1. User clicks "Sync now" → server inserts a `sync_jobs` row (`status=queued`).
2. Worker claims it, sets `status=running`.
3. Worker `List`s the account, then for each item `Export`s bytes and `PUT`s to S3, upserting a
   `documents` row (`object_key`, `format`, `size`, `source_path`).
4. Worker sets `status=succeeded` (or `failed` with `error`), recording counts.
5. UI polls `GET /api/sync/status`.

## Security

- Provider OAuth tokens are encrypted at rest with AES-256-GCM (`internal/crypto`), key from
  `DOCVAULT_TOKEN_ENC_KEY`.
- Sessions are JWT in an HttpOnly cookie signed with `DOCVAULT_JWT_SECRET`.
- Download URLs are short-lived S3 pre-signed URLs; bytes never proxy through the app.
- All document/job queries are scoped by `user_id` from the session — no cross-user access.
