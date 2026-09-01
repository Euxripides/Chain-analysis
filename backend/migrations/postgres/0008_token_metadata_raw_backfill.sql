-- Raw JSON 历史恢复阶段的可审计进度。
ALTER TABLE token_metadata_backfill_jobs ADD COLUMN IF NOT EXISTS raw_objects BIGINT NOT NULL DEFAULT 0;
ALTER TABLE token_metadata_backfill_jobs ADD COLUMN IF NOT EXISTS raw_observations BIGINT NOT NULL DEFAULT 0;
