-- TRON V5.1: persist per-page enrichment structure without conflating
-- logical Receipt calls with HTTP requests or ordinary provider usage rows.
ALTER TABLE provider_usage ADD COLUMN IF NOT EXISTS logical_calls INTEGER NOT NULL DEFAULT 0;
ALTER TABLE provider_usage ADD COLUMN IF NOT EXISTS http_requests INTEGER NOT NULL DEFAULT 0;
ALTER TABLE provider_usage ADD COLUMN IF NOT EXISTS logs_requests INTEGER NOT NULL DEFAULT 0;
ALTER TABLE provider_usage ADD COLUMN IF NOT EXISTS matched_rows INTEGER NOT NULL DEFAULT 0;
ALTER TABLE provider_usage ADD COLUMN IF NOT EXISTS fallback_rows INTEGER NOT NULL DEFAULT 0;
ALTER TABLE provider_usage ADD COLUMN IF NOT EXISTS saved_calls INTEGER NOT NULL DEFAULT 0;
ALTER TABLE provider_usage ADD COLUMN IF NOT EXISTS failure_reasons JSONB NOT NULL DEFAULT '{}'::jsonb;

CREATE INDEX IF NOT EXISTS idx_provider_usage_trc20_summary
    ON provider_usage (job_id, request_time DESC)
    WHERE usage_type = 'trc20_enrichment_summary';
