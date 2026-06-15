-- docvault initial schema: users, provider accounts, sync jobs (queue), documents.

CREATE TABLE IF NOT EXISTS users (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    display_name  TEXT NOT NULL DEFAULT '',
    email         TEXT NOT NULL DEFAULT '',
    avatar_url    TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS provider_accounts (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id                UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider               TEXT NOT NULL,
    external_user_id       TEXT NOT NULL,
    access_token_enc       TEXT NOT NULL DEFAULT '',
    refresh_token_enc      TEXT NOT NULL DEFAULT '',
    access_token_expires   TIMESTAMPTZ,
    refresh_token_expires  TIMESTAMPTZ,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (provider, external_user_id)
);

CREATE INDEX IF NOT EXISTS idx_provider_accounts_user ON provider_accounts(user_id);

CREATE TABLE IF NOT EXISTS sync_jobs (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id              UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider_account_id  UUID NOT NULL REFERENCES provider_accounts(id) ON DELETE CASCADE,
    provider             TEXT NOT NULL,
    status               TEXT NOT NULL DEFAULT 'queued',
    total_items          INT NOT NULL DEFAULT 0,
    done_items           INT NOT NULL DEFAULT 0,
    failed_items         INT NOT NULL DEFAULT 0,
    error                TEXT NOT NULL DEFAULT '',
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at           TIMESTAMPTZ,
    finished_at          TIMESTAMPTZ
);

-- Worker claims with: ... WHERE status='queued' ORDER BY created_at FOR UPDATE SKIP LOCKED.
CREATE INDEX IF NOT EXISTS idx_sync_jobs_queue ON sync_jobs(status, created_at);
CREATE INDEX IF NOT EXISTS idx_sync_jobs_user ON sync_jobs(user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS documents (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider      TEXT NOT NULL,
    external_id   TEXT NOT NULL,
    title         TEXT NOT NULL DEFAULT '',
    doc_type      TEXT NOT NULL DEFAULT '',
    format        TEXT NOT NULL DEFAULT '',
    source_path   TEXT NOT NULL DEFAULT '',
    object_key    TEXT NOT NULL DEFAULT '',
    size_bytes    BIGINT NOT NULL DEFAULT 0,
    synced_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, provider, external_id)
);

CREATE INDEX IF NOT EXISTS idx_documents_user ON documents(user_id, synced_at DESC);
