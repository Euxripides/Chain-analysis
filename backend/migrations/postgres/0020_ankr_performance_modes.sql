-- V1.4.2 SAFE / FAST / TURBO production controls.
ALTER TABLE download_jobs ADD COLUMN IF NOT EXISTS requested_mode TEXT NOT NULL DEFAULT 'fast';
ALTER TABLE download_jobs ADD COLUMN IF NOT EXISTS effective_mode TEXT NOT NULL DEFAULT 'fast';
ALTER TABLE download_jobs ADD COLUMN IF NOT EXISTS mode_reason TEXT NOT NULL DEFAULT 'default_profile';
ALTER TABLE download_jobs ADD COLUMN IF NOT EXISTS mode_changed_at TIMESTAMPTZ NOT NULL DEFAULT now();

DO $$ BEGIN
  ALTER TABLE download_jobs ADD CONSTRAINT download_jobs_requested_mode_check CHECK (requested_mode IN ('safe','fast','turbo'));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;
DO $$ BEGIN
  ALTER TABLE download_jobs ADD CONSTRAINT download_jobs_effective_mode_check CHECK (effective_mode IN ('safe','fast','turbo'));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

CREATE TABLE IF NOT EXISTS ankr_production_profiles (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(), scope TEXT NOT NULL, chain TEXT NOT NULL DEFAULT '', dataset TEXT NOT NULL DEFAULT '',
  default_mode TEXT NOT NULL CHECK (default_mode IN ('safe','fast','turbo')),
  safe_concurrency INT NOT NULL, fast_concurrency INT NOT NULL, turbo_concurrency INT NOT NULL,
  hard_ceiling INT NOT NULL CHECK (hard_ceiling <= 48), reject_boundary INT NOT NULL,
  memory_budget_bytes BIGINT NOT NULL, absolute_rss_limit_bytes BIGINT NOT NULL,
  page_size_default INT NOT NULL, request_timeout_seconds INT NOT NULL, range_sharding_enabled BOOLEAN NOT NULL,
  version TEXT NOT NULL, verified_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_by TEXT NOT NULL,
  UNIQUE(scope, chain, dataset)
);

INSERT INTO ankr_production_profiles (scope,chain,dataset,default_mode,safe_concurrency,fast_concurrency,turbo_concurrency,hard_ceiling,reject_boundary,memory_budget_bytes,absolute_rss_limit_bytes,page_size_default,request_timeout_seconds,range_sharding_enabled,version,verified_at,updated_by)
VALUES ('global','','','fast',24,32,48,48,64,6442450944,12884901888,10000,180,true,'1.4.2',now(),'migration-0020')
ON CONFLICT (scope,chain,dataset) DO NOTHING;

CREATE TABLE IF NOT EXISTS ankr_performance_control (
  singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
  configured_mode TEXT NOT NULL DEFAULT 'fast' CHECK (configured_mode IN ('safe','fast','turbo')),
  requested_mode TEXT NOT NULL DEFAULT 'fast' CHECK (requested_mode IN ('safe','fast','turbo')),
  effective_mode TEXT NOT NULL DEFAULT 'fast' CHECK (effective_mode IN ('safe','fast','turbo')),
  target_concurrency INT NOT NULL DEFAULT 32,
  active_concurrency INT NOT NULL DEFAULT 0,
  mode_reason TEXT NOT NULL DEFAULT 'default_profile',
  mode_changed_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO ankr_performance_control(singleton) VALUES(TRUE) ON CONFLICT(singleton) DO NOTHING;
