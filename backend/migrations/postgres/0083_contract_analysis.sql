-- Additive, tenant-scoped contract analysis records. Existing processor versions are unchanged.
CREATE TABLE contract_analysis_snapshots (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 organization_id uuid NOT NULL, workspace_id uuid NOT NULL,
 analysis_id uuid NOT NULL UNIQUE REFERENCES investigation_analysis_jobs(id),
 chain_id text NOT NULL, block_number bigint NOT NULL CHECK(block_number>=0), block_hash text NOT NULL,
 entry_address text NOT NULL, code_address text NOT NULL, storage_address text NOT NULL, code_hash text NOT NULL,
 snapshot_manifest_object text NOT NULL, sha256 text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX contract_analysis_snapshot_scope ON contract_analysis_snapshots(organization_id,workspace_id,created_at);
CREATE TABLE contract_analysis_engine_runs (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), analysis_id uuid NOT NULL REFERENCES investigation_analysis_jobs(id),
 engine text NOT NULL, engine_digest text NOT NULL, adapter_version text NOT NULL, options_hash text NOT NULL,
 status text NOT NULL CHECK(status IN ('running','completed','partial','failed')),
 attempt integer NOT NULL, fence bigint NOT NULL, started_at timestamptz NOT NULL DEFAULT now(), finished_at timestamptz,
 duration_ms bigint, peak_rss_bytes bigint, exit_code integer, timed_out boolean NOT NULL DEFAULT false,
 output_manifest jsonb NOT NULL DEFAULT '{}', error_code text,
 UNIQUE(analysis_id,engine,options_hash,attempt,fence)
);
CREATE INDEX contract_analysis_run_lookup ON contract_analysis_engine_runs(analysis_id,options_hash,status);
CREATE TABLE contract_analysis_artifacts (
 analysis_id uuid NOT NULL REFERENCES investigation_analysis_jobs(id), artifact_id text NOT NULL,
 organization_id uuid NOT NULL, workspace_id uuid NOT NULL,
 kind text NOT NULL, filename text NOT NULL, media_type text NOT NULL,
 object_key text NOT NULL, byte_size bigint NOT NULL CHECK(byte_size>=0), sha256 text NOT NULL,
 method_id text NOT NULL DEFAULT '', created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(analysis_id,artifact_id)
);
CREATE INDEX contract_analysis_artifact_scope ON contract_analysis_artifacts(organization_id,workspace_id,analysis_id);
CREATE INDEX contract_analysis_row_method ON investigation_analysis_rows(analysis_id,section,(data->>'method_id'))
 WHERE section IN ('methods','steps','storage','calls','permissions','funds','claims','evidence','expressions');
CREATE INDEX contract_analysis_claim ON investigation_analysis_jobs(version,created_at) WHERE version='contract-analysis-v1' AND status IN ('queued','running');
GRANT SELECT,INSERT,UPDATE,DELETE ON contract_analysis_snapshots,contract_analysis_engine_runs,contract_analysis_artifacts TO blockchain_app;

-- Admission rows precede enqueue so snapshot-pinning requests share the same
-- durable budget. Failed submissions remain auditable without a job FK.
CREATE TABLE contract_analysis_budgets (
 analysis_id uuid PRIMARY KEY, organization_id uuid NOT NULL, workspace_id uuid NOT NULL,
 max_requests bigint NOT NULL, max_credits bigint NOT NULL, max_state_reads integer NOT NULL,
 reserved_requests bigint NOT NULL DEFAULT 0, reserved_credits numeric NOT NULL DEFAULT 0,
 state_reads integer NOT NULL DEFAULT 0, processing_ms bigint NOT NULL DEFAULT 0,
 created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX contract_analysis_budget_scope ON contract_analysis_budgets(organization_id,workspace_id,created_at);
GRANT SELECT,INSERT,UPDATE,DELETE ON contract_analysis_budgets TO blockchain_app;
