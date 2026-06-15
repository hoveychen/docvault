# docvault

Self-hostable, multi-source **cloud-document archive**. A user signs in by OAuth-authorizing a
cloud-document provider, a background worker walks everything that user's token can reach,
exports each document to a portable format (docx / xlsx / …), stores it in object storage, and
the web UI lets them browse and download their archive.

Providers implemented today: **Feishu / Lark**, **Google Workspace**, **Office 365 / Microsoft 365**,
and **腾讯文档 (Tencent Docs)** — all behind the same
[`provider.Provider`](internal/provider/provider.go) interface. Each connection picks its
implementation by `provider_type`; adding another source is a new package that self-registers a
factory (see [Provider abstraction](docs/architecture.md#provider-abstraction)).

> **Why per-user, not "whole org in one click"?** Feishu's OpenAPI has no admin/tenant token
> that can read every member's private documents — a `tenant_access_token` only sees what the app
> owns, a `user_access_token` only sees what that one user can access. So org-wide backup is
> fundamentally per-user: each member authorizes once. docvault models that directly. See
> [docs/architecture.md](docs/architecture.md).

## Stack

- **Backend** — Go. Feishu via [`larksuite/oapi-sdk-go`](https://github.com/larksuite/oapi-sdk-go);
  Google via `google.golang.org/api/drive/v3`; Microsoft Graph and Tencent Docs via raw REST + `golang.org/x/oauth2`
- **Metadata + job queue** — Postgres (the queue is `SELECT … FOR UPDATE SKIP LOCKED`, no Redis)
- **Object storage** — S3-compatible (MinIO in dev)
- **Frontend** — React + Vite + TypeScript

## Quick start (local dev)

```bash
# 1. Infra: Postgres + MinIO
make infra-up

# 2. Config
cp .env.example .env
# generate secrets:
echo "DOCVAULT_TOKEN_ENC_KEY=$(openssl rand -base64 32)" >> .env
echo "DOCVAULT_JWT_SECRET=$(openssl rand -base64 48)" >> .env
# then fill in DOCVAULT_FEISHU_APP_ID / _APP_SECRET (see below)

# 3. Run backend (two terminals)
make server   # HTTP API on :8080
make worker   # sync worker

# 4. Frontend dev server (proxies /api to :8080)
make web-install
make web-dev   # http://localhost:5173
```

For a production-style single-origin run, `cd web && pnpm build` then just `make server` —
the server serves `web/dist` and the API from the same origin on `:8080`.

## Feishu / Lark app setup

docvault supports **both** Feishu (open.feishu.cn, mainland China) and Lark
(open.larksuite.com, international) — pick one with `DOCVAULT_FEISHU_DOMAIN`:

| Your tenant | Console | `DOCVAULT_FEISHU_DOMAIN` |
|-------------|---------|--------------------------|
| 飞书 (mainland) | <https://open.feishu.cn/app> | `feishu` (default) |
| **Lark** (international) | <https://open.larksuite.com/app> | `lark` |

The app is created in the matching console — the two are **separate ecosystems**,
so a Lark App ID/Secret only works with `DOCVAULT_FEISHU_DOMAIN=lark` (and vice versa).

**Steps (Lark shown; Feishu is identical on its own console):**

1. Create a **self-built app (自建应用)** at <https://open.larksuite.com/app>.
2. Copy **App ID** / **App Secret** into `.env`, and set `DOCVAULT_FEISHU_DOMAIN=lark`.
3. Under **Security settings → Redirect URLs (安全设置 → 重定向 URL)**, add the **exact**
   callback: `<DOCVAULT_PUBLIC_URL>/api/auth/feishu/callback`
   (e.g. `http://localhost:8080/api/auth/feishu/callback`). `http://localhost` is allowed
   for development.
4. Under **Permissions (权限管理)**, grant read-only scopes:
   `drive:drive:readonly`, `docs:document:readonly`, `wiki:wiki:readonly`.
   To use the **delete cloud original** feature, also grant write access (`drive:drive`)
   so the owner can move documents to trash.
5. Publish/enable the app for your org so the scopes take effect.

> The OAuth route is `/api/auth/feishu/callback` for both domains — `feishu` is the internal
> provider key, not the tenant type. The actual host (open.feishu.cn vs open.larksuite.com) is
> chosen by `DOCVAULT_FEISHU_DOMAIN`.

### Multiple orgs

A self-built app belongs to one org, so to serve **several** Feishu/Lark orgs set
`DOCVAULT_FEISHU_CONNECTIONS` to a JSON array — one object per org, each with a unique `key`:

```json
[{"key":"acme","label":"Acme (Lark)","app_id":"cli_xxx","app_secret":"yyy","domain":"lark"},
 {"key":"globex","label":"Globex (飞书)","app_id":"cli_zzz","app_secret":"www","domain":"feishu"}]
```

Each org gets its own OAuth route `/api/auth/<key>/callback` (register that exact URL in *that*
org's app console) and the login page lists one button per org. Data is isolated per org by the
`key` (it's stored as the provider on every account/document/folder). When
`DOCVAULT_FEISHU_CONNECTIONS` is set it takes precedence over the single-org vars above. (Truly
cross-tenant "install from the app store" requires an ISV/Marketplace app — a separate, heavier
app type — not covered here.)

**Local OAuth works on `localhost`** — the redirect happens in *your browser* (which can reach
localhost) and the token exchange is an *outbound* call from your server. No public ingress is
needed. Just register the exact `http://localhost:8080/...` callback and set
`DOCVAULT_PUBLIC_URL=http://localhost:8080`. For local testing use the single-origin mode
(`pnpm build` + server on `:8080`); don't run OAuth through the Vite `:5173` dev port.

## Other providers (Google Workspace / Office 365 / 腾讯文档)

Every provider is one connection with a `provider_type`. You add connections either from the
**admin UI** (recommended) or by seeding `DOCVAULT_PROVIDER_CONNECTIONS` (a JSON array, one object
per connection) on first boot. The OAuth route is always `/api/auth/<key>/callback` — register
that exact redirect URL in the provider's app console. Common fields: `key` (unique route/id),
`label`, `app_id` (OAuth client id), `app_secret`, and the type-specific `domain`.

| `provider_type` | App console | `app_id` / `app_secret` | `domain` | Notes |
|---|---|---|---|---|
| `feishu` | open.feishu.cn / open.larksuite.com | App ID / App Secret | `feishu` or `lark` | see above |
| `google` | console.cloud.google.com (OAuth client) | Client ID / Client secret | *(unused)* | Drive API; full `drive` scope so trash-delete works; export has a ~10 MB cap; My Drive only |
| `microsoft` | Entra ID app registration | Application (client) ID / secret | Entra **tenant** (`common` / `organizations` / tenant-id) | Microsoft Graph; OneDrive only (SharePoint not yet); files download as native OOXML |
| `tencent` | docs.qq.com/open | Client ID / Client secret | *(unused)* | OAuth scope `all`; async export → docx/xlsx/pptx; delete moves to recycle bin |

Example seed (merged with the Feishu vars; all `key`s must be globally unique):

```json
[{"provider_type":"google","key":"gws","label":"Acme (Google)","app_id":"xxx.apps.googleusercontent.com","app_secret":"yyy","domain":""},
 {"provider_type":"microsoft","key":"o365","label":"Acme (Microsoft 365)","app_id":"<client-id>","app_secret":"zzz","domain":"common"}]
```

> **Not yet verified end-to-end.** No provider (including Feishu) has been run against the live API
> with real credentials yet — the code compiles and is unit-tested, but live OAuth + sync needs
> app credentials. The Tencent Docs REST surface was built against the official docs.qq.com/open
> documentation; two runtime details (the exact OOXML container its export produces, and whether
> folder-list items carry `ownerID`) are marked `// UNVERIFIED:` in the code pending a live run.

## Admin backend

- **The first user to ever sign in becomes the initial admin.** Everyone after is a member.
- Admins see an **管理后台 / Admin** panel in the web UI to:
  - **Manage members** — promote/demote between admin and member, ban/unban. Banned users are
    blocked from the whole app; the last remaining admin can't be demoted or banned.
  - **Manage connections** — add/edit/delete connections of any provider type (pick the type, then
    key, label, App ID, App Secret, and the type-specific domain/tenant) *from the UI*. The
    `provider_type` is fixed at creation. Connections live in the DB (secret encrypted at rest);
    the provider registry hot-reloads on every change, so no restart is needed.
- `DOCVAULT_FEISHU_CONNECTIONS` / `DOCVAULT_PROVIDER_CONNECTIONS` (or the single-app Feishu vars)
  are only a **seed**: on first boot, when the connections table is empty, env connections are
  imported into the DB. After that, the DB is the source of truth and you manage connections in the
  admin UI. Remember to register each connection's redirect URL
  (`<PUBLIC_URL>/api/auth/<key>/callback`) in that provider's app console.

## Scheduled sync

By default sync is **on-demand** (the "Sync now" button enqueues one job). Set
`DOCVAULT_SYNC_INTERVAL` (a Go duration, e.g. `6h`) to enable **continuous** sync:
the worker then auto-enqueues a job for every linked account whose last successful
sync is older than the interval (skipping accounts with an in-flight job and
banned users). Leave it empty/`0` to keep sync on-demand only.

## Deploy

One image, one compose file — see [docs/deploy.md](docs/deploy.md):

```bash
cp .env.example .env   # add secrets + Feishu creds
docker compose -f docker-compose.prod.yml up -d --build
```

Runs server + worker + Postgres + MinIO; workers scale horizontally
(`--scale worker=N`) since the Postgres queue uses `FOR UPDATE SKIP LOCKED`.

## Layout

```
cmd/server      HTTP API + serves frontend
cmd/worker      sync-job queue drainer
internal/
  app           shared dependency graph
  config        env config
  crypto        AES-256-GCM for tokens at rest
  db            pgx pool, embedded migrations, repositories
  store         S3/MinIO object storage
  provider      Provider interface + factory registry (feishu/ google/ microsoft/ tencent/ impls)
  auth          JWT sessions + token refresh
  sync          archival engine + worker loop
  api           REST handlers
web/            React/Vite frontend
```

## Status

Implemented: a multi-provider connection layer (`provider_type` + factory registry) with **four**
providers — Feishu/Lark, Google Workspace, Office 365 / Microsoft Graph, and 腾讯文档 — each behind
the same [`provider.Provider`](internal/provider/provider.go) interface; Feishu/Lark OAuth login
(multi-org), recursive drive sync (export to `docx`/`xlsx`/`pptx`/`pdf` + binary files + Feishu Wiki
spaces), object-storage archival, browse + streamed download, batch deletion of cloud originals
(documents and whole folders, owner- and archival-gated, to trash), an admin backend (roles, ban,
UI-managed connections of any type), and a production Docker stack.
