-- Embedded objects (e.g. Feishu file-attachment blocks) that a document's main
-- export does not include. Each is stored as a sidecar object in object storage
-- and recorded here, linked to its parent document.

CREATE TABLE IF NOT EXISTS document_attachments (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id  UUID NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    external_id  TEXT NOT NULL,            -- provider media/file token (dedupe key)
    filename     TEXT NOT NULL DEFAULT '',
    format       TEXT NOT NULL DEFAULT '', -- extension without dot
    content_type TEXT NOT NULL DEFAULT '',
    object_key   TEXT NOT NULL DEFAULT '', -- S3 key for the sidecar bytes
    size_bytes   BIGINT NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (document_id, external_id)
);

CREATE INDEX IF NOT EXISTS idx_doc_attachments_document ON document_attachments(document_id);
