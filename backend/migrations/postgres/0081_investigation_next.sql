-- Additive investigation control-plane state. Existing export/download job tables are untouched.
CREATE TABLE investigation_analysis_jobs (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), organization_id uuid NOT NULL, workspace_id uuid NOT NULL,
 operation text NOT NULL, version text NOT NULL, request jsonb NOT NULL, request_sha text NOT NULL,
 idempotency_key text NOT NULL, boundary jsonb NOT NULL DEFAULT '{}',
 status text NOT NULL DEFAULT 'queued' CHECK(status IN ('queued','running','completed','failed','cancelled')),
 phase text NOT NULL DEFAULT 'queued', attempts integer NOT NULL DEFAULT 0, fence bigint NOT NULL DEFAULT 0,
 lease_owner uuid, lease_until timestamptz, input_object text NOT NULL DEFAULT '', input_sha text NOT NULL DEFAULT '',
 result_object text NOT NULL DEFAULT '', result_sha text NOT NULL DEFAULT '', result_meta jsonb NOT NULL DEFAULT '{}',
 cursor_key bytea NOT NULL CHECK(octet_length(cursor_key)=32), error_code text, error_message text,
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), completed_at timestamptz,
 UNIQUE(organization_id,workspace_id,idempotency_key)
);
CREATE INDEX investigation_analysis_claim ON investigation_analysis_jobs(status,lease_until,created_at) WHERE status IN ('queued','running');
CREATE TABLE investigation_analysis_rows (
 analysis_id uuid NOT NULL REFERENCES investigation_analysis_jobs(id) ON DELETE CASCADE,
 section text NOT NULL, ordinal bigint NOT NULL CHECK(ordinal>=0), data jsonb NOT NULL,
 PRIMARY KEY(analysis_id,section,ordinal)
);
CREATE TABLE investigation_evidence_bundles (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), organization_id uuid NOT NULL, workspace_id uuid NOT NULL,
 analysis_id uuid NOT NULL REFERENCES investigation_analysis_jobs(id), object_key text NOT NULL, sha256 text NOT NULL,
 size_bytes bigint NOT NULL CHECK(size_bytes>=0), manifest jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(organization_id,workspace_id,analysis_id,sha256)
);
CREATE TABLE investigation_watch_rules (
 sequence bigserial UNIQUE,
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), organization_id uuid NOT NULL, workspace_id uuid NOT NULL,
 name text NOT NULL, rule_type text NOT NULL CHECK(rule_type IN ('balance_change','allowance_change','control_change')),
 request jsonb NOT NULL, revision integer NOT NULL DEFAULT 1,
 state text NOT NULL DEFAULT 'paused' CHECK(state IN ('active','paused','disabled')),
 interval_seconds integer NOT NULL CHECK(interval_seconds BETWEEN 60 AND 86400), confirmations bigint NOT NULL CHECK(confirmations BETWEEN 2 AND 1000),
 last_snapshot jsonb, last_hash text, next_run timestamptz NOT NULL DEFAULT now(), lease_owner uuid, lease_until timestamptz,
 last_error text, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX investigation_watch_claim ON investigation_watch_rules(next_run,lease_until) WHERE state='active';
CREATE TABLE investigation_watch_events (
 sequence bigserial PRIMARY KEY, rule_id uuid NOT NULL REFERENCES investigation_watch_rules(id), revision integer NOT NULL,
 organization_id uuid NOT NULL, workspace_id uuid NOT NULL, event_key text NOT NULL UNIQUE,
 kind text NOT NULL, data jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX investigation_watch_inbox ON investigation_watch_events(organization_id,workspace_id,sequence);
CREATE TABLE investigation_workspace_cursors (
 organization_id uuid NOT NULL, workspace_id uuid NOT NULL, cursor_key bytea NOT NULL CHECK(octet_length(cursor_key)=32),
 PRIMARY KEY(organization_id,workspace_id)
);
-- Runtime role gets only normal CRUD; no schema privileges or TRUNCATE.
GRANT SELECT, INSERT, UPDATE, DELETE ON investigation_analysis_jobs, investigation_analysis_rows,
 investigation_evidence_bundles, investigation_watch_rules, investigation_watch_events, investigation_workspace_cursors TO blockchain_app;
GRANT USAGE, SELECT ON SEQUENCE investigation_watch_events_sequence_seq TO blockchain_app;
GRANT USAGE, SELECT ON SEQUENCE investigation_watch_rules_sequence_seq TO blockchain_app;
