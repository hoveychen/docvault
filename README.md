# docvault

Self-hostable, multi-source **cloud-document archive**. A user signs in by OAuth-authorizing a
cloud-document provider, a background worker walks everything that user's token can reach,
exports each document to a portable format (docx / xlsx / …), stores it in object storage, and
the web UI lets them browse and download their archive.

First provider: **Feishu / Lark**. Google Workspace and Office 365 slot in behind the same
[`provider.Provider`](internal/provider/provider.go) interface.

> **Why per-user, not "whole org in one click"?** Feishu's OpenAPI has no admin/tenant token
> that can read every member's private documents — a `tenant_access_token` only sees what the app
> owns, a `user_access_token` only sees what that one user can access. So org-wide backup is
> fundamentally per-user: each member authorizes once. docvault models that directly. See
> [docs/architecture.md](docs/architecture.md).

## Stack

- **Backend** — Go, [`larksuite/oapi-sdk-go`](https://github.com/larksuite/oapi-sdk-go) for Feishu
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

## Feishu app setup

1. Create a **self-built app** at <https://open.feishu.cn/app>.
2. Copy **App ID** / **App Secret** into `.env`.
3. Add a **redirect URL**: `<DOCVAULT_PUBLIC_URL>/api/auth/feishu/callback`
   (e.g. `http://localhost:8080/api/auth/feishu/callback`).
4. Grant read-only scopes: `drive:drive:readonly`, `docs:document:readonly`.

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
  provider      Provider interface (+ feishu/ impl)
  auth          JWT sessions + token refresh
  sync          archival engine + worker loop
  api           REST handlers
web/            React/Vite frontend
```

## Status

MVP. Implemented end to end: Feishu OAuth login, recursive drive listing, export of
`docx`/`doc`/`sheet`, object-storage archival, browse + pre-signed download. Known follow-ups:
binary file download, `bitable`/`slides`/`mindnote` export, Wiki spaces, and additional providers.
