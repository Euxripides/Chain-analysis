-- Durable archive retries do not consume provider API quota.
ALTER TABLE download_pages ADD COLUMN IF NOT EXISTS raw_object_key text NOT NULL DEFAULT '';
ALTER TABLE download_pages ADD COLUMN IF NOT EXISTS raw_uploaded_at timestamptz;
ALTER TABLE download_pages ADD COLUMN IF NOT EXISTS upload_attempts integer NOT NULL DEFAULT 0;
ALTER TABLE download_pages ADD COLUMN IF NOT EXISTS next_upload_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE download_pages ADD COLUMN IF NOT EXISTS raw_archive_enabled boolean NOT NULL DEFAULT false;
ALTER TABLE download_pages ALTER COLUMN raw_archive_enabled SET DEFAULT true;

CREATE INDEX IF NOT EXISTS idx_download_pages_archive_pending ON download_pages(next_upload_at,id) WHERE raw_archive_enabled AND raw_uploaded_at IS NULL;
