-- Record source folders discovered during sync so a whole folder's cloud
-- original can be deleted once everything under it is safely archived.

CREATE TABLE IF NOT EXISTS folders (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id           UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider          TEXT NOT NULL,
    external_id       TEXT NOT NULL,   -- folder token
    title             TEXT NOT NULL DEFAULT '',
    source_path       TEXT NOT NULL DEFAULT '',  -- the folder's own full path
    owner_external_id TEXT NOT NULL DEFAULT '',
    synced_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    source_deleted_at TIMESTAMPTZ,
    UNIQUE (user_id, provider, external_id)
);

CREATE INDEX IF NOT EXISTS idx_folders_user ON folders(user_id, source_path);
