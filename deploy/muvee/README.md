# Deploying docvault on Muvee

Muvee runs multi-service apps as `--compose` projects using **pre-built images**
(no `build:`). docvault ships its image to GHCR via GitHub Actions, and
[`docker-compose.yml`](docker-compose.yml) here wires server + worker + Postgres +
MinIO, exposing only the `server` on port 8080.

## 1. Build the image (GHCR)

Pushing to `main` runs [`.github/workflows/docker-image.yml`](../../.github/workflows/docker-image.yml),
which builds `linux/amd64` and pushes `ghcr.io/hoveychen/docvault:latest`.

Make the GHCR package **public** (simplest — no pull secret needed): GitHub →
Packages → docvault → Package settings → Change visibility → Public. Otherwise
create a registry pull secret on Muvee:

```bash
muveectl --profile momoso secrets create --name GHCR_PULL --type registry \
  --registry-addr ghcr.io --registry-username hoveychen --value <gh_PAT_read:packages>
```

## 2. Create the project

```bash
muveectl --profile momoso projects create \
  --name docvault \
  --compose \
  --git-url https://github.com/hoveychen/docvault \
  --compose-file deploy/muvee/docker-compose.yml \
  --expose-service server --expose-port 8080 \
  --domain docvault \
  --description "Cloud-document archive" \
  --tags "docs,archive,feishu"
```

The app will be served at `https://docvault.<momoso-base-domain>`.

## 3. Inject config / secrets

The compose file reads these from the environment (none are committed). Set them
as project env / secrets before deploying:

| Var | Value |
|-----|-------|
| `DOCVAULT_PUBLIC_URL` | `https://docvault.<base-domain>` (the project's public URL) |
| `DOCVAULT_TOKEN_ENC_KEY` | `openssl rand -base64 32` |
| `DOCVAULT_JWT_SECRET` | `openssl rand -base64 48` |
| `DOCVAULT_FEISHU_APP_ID` / `_APP_SECRET` | your Lark self-built app creds |
| `DOCVAULT_FEISHU_DOMAIN` | `lark` (or `feishu`) |
| `POSTGRES_PASSWORD` | a real password |
| `DOCVAULT_S3_ACCESS_KEY` / `_SECRET_KEY` | MinIO root creds |

(Once deployed, additional orgs can be added in the in-app admin UI instead of
`DOCVAULT_FEISHU_CONNECTIONS`.)

## 4. Register the OAuth redirect

In the Lark/Feishu app console, add the redirect URL:
`https://docvault.<base-domain>/api/auth/feishu/callback`
and grant `drive:drive:readonly`, `wiki:wiki:readonly` (and `drive:drive` for delete).

## 5. Deploy & verify

```bash
muveectl --profile momoso projects deploy <project-id>
muveectl --profile momoso projects events <project-id> --follow      # watch lifecycle
muveectl --profile momoso projects runtime-logs <project-id> --tail 50
muveectl --profile momoso projects curl <project-id> /healthz        # -> {"status":"ok"}
```

Then open `https://docvault.<base-domain>`, authorize with Lark (first user becomes
admin), and sync. Downloads stream through the server, so MinIO stays internal.

## Notes

- **Persistence:** `pg-data` and `minio-data` are named volumes; Muvee pins compose
  projects to one node so they survive redeploys. Back these up for real data.
- **Redeploy on new image:** enable auto-deploy (`projects update <id> --auto-deploy`)
  to redeploy when `ghcr.io/hoveychen/docvault:latest` is repushed.
