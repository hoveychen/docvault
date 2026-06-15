-- Track each archived doc's owner (to gate deletion to owner-only) and whether
-- its cloud original has been deleted (data privatized into docvault).

ALTER TABLE documents
    ADD COLUMN IF NOT EXISTS owner_external_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS source_deleted_at TIMESTAMPTZ;
