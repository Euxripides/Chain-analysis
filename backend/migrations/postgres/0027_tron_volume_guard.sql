-- TRON V6: durable, dataset-local address volume guard.
CREATE TABLE IF NOT EXISTS download_volume_guards (
  job_id UUID NOT NULL REFERENCES download_jobs(id) ON DELETE CASCADE,
  subject_id UUID NOT NULL REFERENCES download_subjects(id) ON DELETE CASCADE,
  dataset_id UUID PRIMARY KEY REFERENCES download_datasets(id) ON DELETE CASCADE,
  dataset TEXT NOT NULL,
  state TEXT NOT NULL DEFAULT 'RUNNING',
  reason TEXT NOT NULL DEFAULT '',
  risk_level TEXT NOT NULL DEFAULT 'NORMAL',
  actual_rows BIGINT NOT NULL DEFAULT 0,
  projected_rows_low BIGINT NOT NULL DEFAULT 0,
  projected_rows_mid BIGINT NOT NULL DEFAULT 0,
  projected_rows_high BIGINT NOT NULL DEFAULT 0,
  confidence DOUBLE PRECISION NOT NULL DEFAULT 0,
  actual_provider_calls BIGINT NOT NULL DEFAULT 0,
  projected_provider_calls BIGINT NOT NULL DEFAULT 0,
  actual_cost_usd DOUBLE PRECISION NOT NULL DEFAULT 0,
  projected_cost_usd DOUBLE PRECISION NOT NULL DEFAULT 0,
  elapsed_seconds DOUBLE PRECISION NOT NULL DEFAULT 0,
  projected_duration_seconds DOUBLE PRECISION NOT NULL DEFAULT 0,
  guard_override BOOLEAN NOT NULL DEFAULT false,
  guard_triggered_at TIMESTAMPTZ,
  guard_approved_at TIMESTAMPTZ,
  guard_approved_by TEXT NOT NULL DEFAULT '',
  guard_approved_projection BIGINT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(job_id, subject_id, dataset)
);

CREATE TABLE IF NOT EXISTS download_volume_guard_commits (
  shard_id UUID NOT NULL REFERENCES download_shards(id) ON DELETE CASCADE,
  page_no INTEGER NOT NULL,
  dataset_id UUID NOT NULL REFERENCES download_datasets(id) ON DELETE CASCADE,
  rows_committed BIGINT NOT NULL,
  provider_calls BIGINT NOT NULL DEFAULT 1,
  estimated_cost_usd DOUBLE PRECISION NOT NULL DEFAULT 0,
  min_block BIGINT NOT NULL DEFAULT 0,
  max_block BIGINT NOT NULL DEFAULT 0,
  full_page BOOLEAN NOT NULL DEFAULT false,
  committed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY(shard_id, page_no)
);

CREATE INDEX IF NOT EXISTS idx_volume_guards_job_state ON download_volume_guards(job_id, state);
CREATE INDEX IF NOT EXISTS idx_volume_guard_commits_dataset ON download_volume_guard_commits(dataset_id, committed_at);
