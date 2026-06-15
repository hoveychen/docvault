# Deploying docvault

The whole stack — API server, sync worker, Postgres, MinIO — runs from one image
([`Dockerfile`](../Dockerfile)) via [`docker-compose.prod.yml`](../docker-compose.prod.yml).

## 1. Configure

```bash
cp .env.example .env
echo "DOCVAULT_TOKEN_ENC_KEY=$(openssl rand -base64 32)" >> .env
echo "DOCVAULT_JWT_SECRET=$(openssl rand -base64 48)" >> .env
```

Then edit `.env`:

- `DOCVAULT_PUBLIC_URL` — the public URL browsers hit (e.g. `https://vault.example.com`). The
  Feishu OAuth `redirect_uri` is derived from it.
- `DOCVAULT_FEISHU_APP_ID` / `DOCVAULT_FEISHU_APP_SECRET` — from your self-built app.
- `DOCVAULT_FEISHU_DOMAIN` — `feishu` (open.feishu.cn) or `lark` (open.larksuite.com); must
  match the console where the app was created. See the README for the per-domain setup steps.
- `DOCVAULT_HOST_PORT` — optional; host port to publish the server on (default `8080`). Set this
  if `8080` is already taken on the host.
- `POSTGRES_PASSWORD` — optional; defaults to `docvault`. Set a real one in production.
- `DOCVAULT_S3_ACCESS_KEY` / `DOCVAULT_S3_SECRET_KEY` — MinIO root creds; set real values.

> The DB and S3 **endpoints** in `.env` are ignored by this stack — compose overrides them to the
> `postgres` and `minio` service names. The same `.env` therefore works for both local dev and
> this containerized stack.

In the Feishu app console, set the redirect URL to
`<DOCVAULT_PUBLIC_URL>/api/auth/feishu/callback` and grant the read-only scopes
(`drive:drive:readonly`, `docs:document:readonly`, `wiki:wiki:readonly`).

## 2. Launch

```bash
docker compose -f docker-compose.prod.yml up -d --build
```

This builds the image (frontend + both Go binaries), starts Postgres and MinIO, waits for them to
be healthy, then starts `server` (`:8080`) and `worker`. Migrations run automatically on server
start; the MinIO bucket is created automatically on first connect.

Check health: `curl http://localhost:8080/healthz` → `{"status":"ok"}`.

## 3. Front it with TLS

Put a reverse proxy (Caddy / nginx / Traefik) in front of `:8080` terminating TLS for
`DOCVAULT_PUBLIC_URL`. The session cookie is marked `Secure` automatically when `DOCVAULT_PUBLIC_URL`
is `https://…`. Only the `server` port needs to be public; the MinIO console (`:9001`) and Postgres
should stay private.

## Operating

- **Logs:** `docker compose -f docker-compose.prod.yml logs -f server worker`
- **Scale workers:** `docker compose -f docker-compose.prod.yml up -d --scale worker=3`
  (the Postgres queue uses `FOR UPDATE SKIP LOCKED`, so workers never double-claim a job).
- **Backups:** persist the `pg-data` and `minio-data` volumes; that's all the durable state.
- **Upgrades:** `git pull && docker compose -f docker-compose.prod.yml up -d --build`.
