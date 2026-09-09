-- V1.4.2: persist worker-process runtime truth for /fetch-runtime.
-- Existing installations are upgraded idempotently; no historical rows are
-- deleted and old rows remain explicitly identifiable by their zero defaults.
ALTER TABLE ankr_scheduler_state ADD COLUMN IF NOT EXISTS configured_initial_concurrency INT NOT NULL DEFAULT 0;
ALTER TABLE ankr_scheduler_state ADD COLUMN IF NOT EXISTS configured_max_concurrency INT NOT NULL DEFAULT 0;
ALTER TABLE ankr_scheduler_state ADD COLUMN IF NOT EXISTS global_hard_ceiling INT NOT NULL DEFAULT 0;
ALTER TABLE ankr_scheduler_state ADD COLUMN IF NOT EXISTS ready_queue_enabled BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE ankr_scheduler_state ADD COLUMN IF NOT EXISTS ready_queue_shadow BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE ankr_scheduler_state ADD COLUMN IF NOT EXISTS ready_queue_mode TEXT NOT NULL DEFAULT '';
ALTER TABLE ankr_scheduler_state ADD COLUMN IF NOT EXISTS auto_tune_enabled BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE ankr_scheduler_state ADD COLUMN IF NOT EXISTS controller_mode TEXT NOT NULL DEFAULT '';
