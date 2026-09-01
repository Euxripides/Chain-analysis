-- 损坏历史 gzip 不阻断回填，但必须在报告中可审计。
ALTER TABLE token_metadata_backfill_jobs ADD COLUMN IF NOT EXISTS raw_failed_objects BIGINT NOT NULL DEFAULT 0;
ALTER TABLE token_metadata_backfill_jobs ADD COLUMN IF NOT EXISTS raw_failed_parts BIGINT NOT NULL DEFAULT 0;
