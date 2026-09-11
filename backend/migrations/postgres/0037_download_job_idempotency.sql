ALTER TABLE download_jobs
    ADD COLUMN IF NOT EXISTS request_fingerprint TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS download_jobs_reusable_fingerprint_key
    ON download_jobs (request_fingerprint)
    WHERE request_fingerprint IS NOT NULL
      AND status IN ('CREATED','PLANNING','QUEUED','RUNNING','PAUSE_REQUESTED','PAUSED','RESUMING','WAITING_USER_CONFIRMATION','COMPLETED');
