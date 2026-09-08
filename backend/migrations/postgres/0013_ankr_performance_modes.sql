-- V1.4.2 production performance modes. Benchmark evidence remains separate.
ALTER TABLE download_jobs
    ADD COLUMN IF NOT EXISTS requested_mode TEXT NOT NULL DEFAULT 'fast',
    ADD COLUMN IF NOT EXISTS effective_mode TEXT NOT NULL DEFAULT 'fast',
    ADD COLUMN IF NOT EXISTS mode_reason TEXT NOT NULL DEFAULT 'system_default',
    ADD COLUMN IF NOT EXISTS mode_changed_at TIMESTAMPTZ NOT NULL DEFAULT now();

DO $$ BEGIN
    ALTER TABLE download_jobs ADD CONSTRAINT download_jobs_requested_mode_check
        CHECK (requested_mode IN ('safe', 'fast', 'turbo'));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;
DO $$ BEGIN
    ALTER TABLE download_jobs ADD CONSTRAINT download_jobs_effective_mode_check
        CHECK (effective_mode IN ('safe', 'fast', 'turbo'));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

CREATE TABLE IF NOT EXISTS ankr_production_profiles (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    scope TEXT NOT NULL DEFAULT 'global', chain TEXT NOT NULL DEFAULT '', dataset TEXT NOT NULL DEFAULT '',
    default_mode TEXT NOT NULL DEFAULT 'fast' CHECK (default_mode IN ('safe', 'fast', 'turbo')),
    safe_concurrency INT NOT NULL CHECK (safe_concurrency > 0),
    fast_concurrency INT NOT NULL CHECK (fast_concurrency > safe_concurrency),
    turbo_concurrency INT NOT NULL CHECK (turbo_concurrency > fast_concurrency),
    hard_ceiling INT NOT NULL CHECK (hard_ceiling > 0 AND hard_ceiling <= 48),
    reject_boundary INT NOT NULL CHECK (reject_boundary > hard_ceiling),
    memory_budget_bytes BIGINT NOT NULL, absolute_rss_limit_bytes BIGINT NOT NULL,
    page_size_default INT NOT NULL CHECK (page_size_default BETWEEN 1 AND 10000),
    request_timeout_seconds INT NOT NULL CHECK (request_timeout_seconds > 0),
    range_sharding_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    version TEXT NOT NULL, verified_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_by TEXT NOT NULL DEFAULT 'migration',
    UNIQUE (scope, chain, dataset)
);

INSERT INTO ankr_production_profiles (
    scope, chain, dataset, default_mode, safe_concurrency, fast_concurrency,
    turbo_concurrency, hard_ceiling, reject_boundary, memory_budget_bytes,
    absolute_rss_limit_bytes, page_size_default, request_timeout_seconds,
    range_sharding_enabled, version, verified_at, updated_by
) VALUES (
    'global', '', '', 'fast', 24, 32, 48, 48, 64,
    6442450944, 12884901888, 10000, 180, TRUE, 'v1.4.2', now(), 'v1.4.2-migration'
) ON CONFLICT (scope, chain, dataset) DO NOTHING;

CREATE TABLE IF NOT EXISTS ankr_scheduler_state (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    configured_mode TEXT NOT NULL DEFAULT 'fast' CHECK (configured_mode IN ('safe', 'fast', 'turbo')),
    requested_mode TEXT NOT NULL DEFAULT 'fast' CHECK (requested_mode IN ('safe', 'fast', 'turbo')),
    effective_mode TEXT NOT NULL DEFAULT 'fast' CHECK (effective_mode IN ('safe', 'fast', 'turbo')),
    target_concurrency INT NOT NULL DEFAULT 32 CHECK (target_concurrency > 0 AND target_concurrency <= 48),
    active_concurrency INT NOT NULL DEFAULT 0 CHECK (active_concurrency >= 0),
    mode_reason TEXT NOT NULL DEFAULT 'system_default',
    mode_changed_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO ankr_scheduler_state (singleton) VALUES (TRUE) ON CONFLICT (singleton) DO NOTHING;
