# docvault architecture

docvault is a multi-source cloud-document archival SaaS. A user signs in by OAuth-authorizing
a cloud-document provider — today Feishu/Lark, Google Workspace, Office 365 / Microsoft 365, and
腾讯文档 (Tencent Docs). A background worker walks everything that user's token can reach, exports
each online document to a portable format (docx / xlsx / pptx / pdf), stores it in object storage,
and the web UI lets the user browse and download their archive.

## Why per-user OAuth (and not a tenant-wide token)

Feishu's OpenAPI has no admin/tenant scope that lets one app read every member's private
documents. A `tenant_access_token` only sees what the app itself owns or was granted; a
`user_access_token` only sees what that one authorizing user can access. So "back up the whole
org" is fundamentally **per-user**: every member must personally authorize once. docvault models
this directly — each signed-in user authorizes their own account, and the archive is scoped to
that user. The other providers (Google Workspace, Microsoft 365, Tencent Docs) sit behind the same
per-account model — every one is a user-scoped OAuth token, not a tenant-admin token.

## Components

```
                ┌─────────────┐         ┌──────────────────────────────┐
  Browser ────► │  server     │ ──────► │ Postgres                     │
  (React/Vite)  │ (cmd/server)│         │  users / provider_accounts   │
                │  REST API   │ ◄────── │  sync_jobs (queue) / documents│
                └─────┬───────┘         └──────────────────────────────┘
                      │ enqueue sync_job             ▲
                      ▼                              │ claim / update
                ┌─────────────┐   provider API  ┌──────────────────┐  PUT  ┌─────────┐
                │  worker     │ ───────────────►│ provider.Provider │      │  MinIO  │
                │ (cmd/worker)│ ◄───────────────│ feishu/google/    │ ────►│  (S3)   │
                └─────────────┘   export+download│ microsoft/tencent │      └─────────┘
                                                 └──────────────────┘
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
    Key() string                                              // routing/storage id (the connection key)
    Label() string                                            // shown on the login page
    AuthCodeURL(state, redirectURI string) string             // step 1 of OAuth
    Exchange(ctx, code, redirectURI) (*Token, *Identity, error) // code -> tokens + who authorized
    Refresh(ctx, refreshToken string) (*Token, error)
    List(ctx, tok *Token) ([]Item, error)                     // everything reachable
    Export(ctx, tok *Token, item Item) (*Blob, error)         // bytes + filename + mime
    Delete(ctx, tok *Token, item Item) error                  // move cloud original to trash
}
```

### Connection types and the factory registry

A **connection** (the `provider_connections` table, admin-managed) carries a `provider_type`
discriminator alongside the OAuth client credential (`app_id` / `app_secret`) and a type-specific
`domain` (Feishu/Lark variant, or the Entra tenant for Microsoft). `Key()` is the per-connection
routing id stored on every account/document/folder; `provider_type` selects the implementation.

Each implementation package registers a factory from its `init()`:

```go
func init() {
    provider.RegisterFactory("google", func(def provider.ConnDef) (provider.Provider, error) { … })
}
```

`internal/app` blank-imports every implementation so those `init()`s run, then
`provider.Build(ConnDef{Type: …})` constructs the right provider per connection and
`ReloadProviders` hot-swaps the registry whenever an admin edits a connection. **Adding a new
source is one new package** (implement `Provider` + `RegisterFactory` in `init()`) plus a one-line
blank import — no central switch to edit.

Implementations: `feishu` (via `larksuite/oapi-sdk-go/v3`), `google` (via
`google.golang.org/api/drive/v3` — Docs/Sheets/Slides exported to docx/xlsx/pptx, binaries
downloaded raw), `microsoft` (Microsoft Graph REST — OneDrive items download as native OOXML, no
conversion), and `tencent` (docs.qq.com/open REST — async export task → poll → download). All four
share `golang.org/x/time/rate` limiting and a `call()` retry helper.

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
- Downloads stream through the server from object storage (so single-origin deployments work
  without exposing the object store to the browser); access is gated by the session's `user_id`.
- All document/job queries are scoped by `user_id` from the session — no cross-user access.
