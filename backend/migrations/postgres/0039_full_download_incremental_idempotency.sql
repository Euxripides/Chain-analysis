DROP INDEX IF EXISTS download_jobs_reusable_fingerprint_key;

-- Completed full-range jobs are watermarks, not permanent idempotency hits:
-- a later full request must create a new job that plans only the per-address
-- tail. Active requests remain unique so concurrent submissions collapse.
CREATE UNIQUE INDEX download_jobs_reusable_fingerprint_key
    ON download_jobs (request_fingerprint)
    WHERE request_fingerprint IS NOT NULL
      AND status IN ('CREATED','PLANNING','QUEUED','RUNNING','PAUSE_REQUESTED','PAUSED','RESUMING','WAITING_USER_CONFIRMATION');
