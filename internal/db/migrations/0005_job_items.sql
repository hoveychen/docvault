-- Sliced sync: a job is no longer run end-to-end in one shot. On first claim the
-- engine snapshots the account's full item list into sync_job_items, then each
-- claim processes a time-bounded slice and re-queues the job until no pending
-- items remain. This makes big accounts resumable and lets the single worker
-- time-share fairly across users (one Export at a time, no provider rate-limit).

-- last_sliced_at drives round-robin claim ordering: never-sliced jobs (NULL) go
-- first, then the job whose last slice is oldest.
ALTER TABLE sync_jobs ADD COLUMN IF NOT EXISTS last_sliced_at TIMESTAMPTZ;

CREATE TABLE IF NOT EXISTS sync_job_items (
    id                 BIGSERIAL PRIMARY KEY,
    job_id             UUID NOT NULL REFERENCES sync_jobs(id) ON DELETE CASCADE,
    external_id        TEXT NOT NULL,
    title              TEXT NOT NULL DEFAULT '',
    doc_type           TEXT NOT NULL DEFAULT '',
    source_path        TEXT NOT NULL DEFAULT '',
    owner_external_id  TEXT NOT NULL DEFAULT '',
    is_folder          BOOLEAN NOT NULL DEFAULT false,
    status             TEXT NOT NULL DEFAULT 'pending', -- pending | done | failed
    attempts           INT NOT NULL DEFAULT 0,
    error              TEXT NOT NULL DEFAULT '',
    UNIQUE (job_id, external_id)
);

-- Slice loop pulls the next pending rows for a job.
CREATE INDEX IF NOT EXISTS idx_job_items_pending ON sync_job_items(job_id, status);

-- Re-point the queue index at the round-robin ordering (status, last_sliced_at, created_at).
DROP INDEX IF EXISTS idx_sync_jobs_queue;
CREATE INDEX IF NOT EXISTS idx_sync_jobs_queue ON sync_jobs(status, last_sliced_at, created_at);
