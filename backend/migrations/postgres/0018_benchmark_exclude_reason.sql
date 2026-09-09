-- Align the V1.2 migration typo (excluded_reason) with the runtime model and
-- queries (exclude_reason). Kept additive for safe rolling deployment.
ALTER TABLE ankr_benchmark_runs
    ADD COLUMN IF NOT EXISTS exclude_reason TEXT NOT NULL DEFAULT '';
