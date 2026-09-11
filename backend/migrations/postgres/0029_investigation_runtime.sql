-- Phase 1: durable investigation execution hierarchy and auditable capability calls.
CREATE TABLE IF NOT EXISTS investigation_runs (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(), investigation_id UUID NOT NULL REFERENCES investigations(id) ON DELETE CASCADE,
 status TEXT NOT NULL DEFAULT 'pending', objective TEXT NOT NULL, plan JSONB NOT NULL DEFAULT '[]', frontier JSONB NOT NULL DEFAULT '[]',
 visited_subjects JSONB NOT NULL DEFAULT '[]', pending_data_jobs JSONB NOT NULL DEFAULT '[]', pending_analysis_jobs JSONB NOT NULL DEFAULT '[]',
 remaining_budget JSONB NOT NULL DEFAULT '{}', last_event_id TEXT NOT NULL DEFAULT '', checkpoint JSONB NOT NULL DEFAULT '{}',
 attempt INT NOT NULL DEFAULT 1, started_at TIMESTAMPTZ, finished_at TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_investigation_runs_active ON investigation_runs(status,updated_at) WHERE status IN ('pending','running','waiting','paused');
CREATE TABLE IF NOT EXISTS investigation_stages (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(), run_id UUID NOT NULL REFERENCES investigation_runs(id) ON DELETE CASCADE,
 name TEXT NOT NULL, ordinal INT NOT NULL, status TEXT NOT NULL DEFAULT 'pending', started_at TIMESTAMPTZ, finished_at TIMESTAMPTZ, UNIQUE(run_id,ordinal)
);
CREATE TABLE IF NOT EXISTS investigation_steps (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(), run_id UUID NOT NULL REFERENCES investigation_runs(id) ON DELETE CASCADE,
 stage_id UUID NOT NULL REFERENCES investigation_stages(id) ON DELETE CASCADE, ordinal INT NOT NULL, action TEXT NOT NULL,
 capability TEXT NOT NULL, capability_version TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'pending', input JSONB NOT NULL DEFAULT '{}',
 output JSONB NOT NULL DEFAULT '{}', output_hash TEXT NOT NULL DEFAULT '', data_version TEXT NOT NULL DEFAULT '', algorithm_version TEXT NOT NULL DEFAULT '',
 provider TEXT NOT NULL DEFAULT '', dataset TEXT NOT NULL DEFAULT '', external_job_id UUID, error TEXT NOT NULL DEFAULT '',
 started_at TIMESTAMPTZ, finished_at TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE(run_id,ordinal)
);
ALTER TABLE investigations ADD COLUMN IF NOT EXISTS active_run_id UUID;
ALTER TABLE investigations ADD COLUMN IF NOT EXISTS data_requirement JSONB NOT NULL DEFAULT '{}';
INSERT INTO schema_registry(schema_name,schema_version,definition) VALUES
('investigation.runtime','1.0','{"hierarchy":["Investigation","Run","Stage","Step","Action"],"states":["pending","running","waiting","completed","failed","skipped","cancelled"]}')
ON CONFLICT DO NOTHING;
