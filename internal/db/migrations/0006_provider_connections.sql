-- Generalize the admin-managed connection table from Feishu-only to any provider
-- type. The schema (key/label/app_id/app_secret/domain) already maps cleanly onto
-- an OAuth2 client credential, so we only add a provider_type discriminator and
-- rename the table. Existing rows are Feishu connections by construction.

ALTER TABLE feishu_connections RENAME TO provider_connections;

ALTER TABLE provider_connections
    ADD COLUMN IF NOT EXISTS provider_type TEXT NOT NULL DEFAULT 'feishu';

-- domain is reused per provider type: 'feishu'/'lark' variant for Feishu, the
-- Entra tenant ('common'/'organizations'/<tenant-id>) for Microsoft, unused for
-- Google and Tencent.
