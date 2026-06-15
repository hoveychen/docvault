-- Admin roles + ban flag on users, and DB-stored provider connections so an
-- admin can configure orgs from the web UI (app secret encrypted at rest).

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS role   TEXT    NOT NULL DEFAULT 'member',
    ADD COLUMN IF NOT EXISTS banned BOOLEAN NOT NULL DEFAULT false;

CREATE TABLE IF NOT EXISTS feishu_connections (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    key             TEXT NOT NULL UNIQUE,
    label           TEXT NOT NULL DEFAULT '',
    app_id          TEXT NOT NULL DEFAULT '',
    app_secret_enc  TEXT NOT NULL DEFAULT '',
    domain          TEXT NOT NULL DEFAULT 'feishu',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
