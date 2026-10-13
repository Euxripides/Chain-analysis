


SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', '', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

SET default_tablespace = '';

SET default_table_access_method = heap;


CREATE TABLE public.address_dataset_sync_state (
    chain text NOT NULL,
    subject text NOT NULL,
    dataset text NOT NULL,
    complete_from timestamp with time zone NOT NULL,
    complete_through timestamp with time zone NOT NULL,
    source_job_id uuid,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT address_dataset_sync_state_check CHECK ((complete_through >= complete_from))
);



CREATE TABLE public.checkpoints (
    shard_id uuid NOT NULL,
    job_id uuid NOT NULL,
    range_start bigint NOT NULL,
    range_end bigint NOT NULL,
    next_page_token text DEFAULT ''::text NOT NULL,
    page_no integer DEFAULT 0 NOT NULL,
    downloaded_rows bigint DEFAULT 0 NOT NULL,
    downloaded_bytes bigint DEFAULT 0 NOT NULL,
    last_success_at timestamp with time zone,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);



CREATE TABLE public.dataset_coverage (
    id uuid NOT NULL,
    chain text NOT NULL,
    subject_type text DEFAULT 'address'::text NOT NULL,
    subject text NOT NULL,
    dataset text NOT NULL,
    from_block bigint NOT NULL,
    to_block bigint NOT NULL,
    status text DEFAULT 'COMPLETE'::text NOT NULL,
    provider text DEFAULT 'ankr'::text NOT NULL,
    row_count bigint DEFAULT 0 NOT NULL,
    raw_bytes bigint DEFAULT 0 NOT NULL,
    head_block bigint DEFAULT 0 NOT NULL,
    committed_at timestamp with time zone DEFAULT now() NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);



CREATE TABLE public.download_datasets (
    id uuid NOT NULL,
    job_id uuid NOT NULL,
    subject_id uuid NOT NULL,
    dataset text NOT NULL,
    status text DEFAULT 'PENDING'::text NOT NULL,
    progress real DEFAULT 0 NOT NULL,
    weight real DEFAULT 1 NOT NULL,
    stats jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);



CREATE TABLE public.download_jobs (
    id uuid NOT NULL,
    job_no text NOT NULL,
    status text NOT NULL,
    priority integer DEFAULT 100 NOT NULL,
    source text DEFAULT 'user_download'::text NOT NULL,
    chain text NOT NULL,
    range_type text NOT NULL,
    from_block bigint,
    to_block bigint,
    from_timestamp timestamp with time zone,
    to_timestamp timestamp with time zone,
    datasets text[] NOT NULL,
    reuse_policy text DEFAULT 'prefer_local'::text NOT NULL,
    head_block bigint DEFAULT 0 NOT NULL,
    created_by uuid,
    error text DEFAULT ''::text NOT NULL,
    stats jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    organization_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    project_id uuid,
    resolved_at timestamp with time zone,
    fetch_started_at timestamp with time zone,
    fetch_completed_at timestamp with time zone,
    ingest_started_at timestamp with time zone,
    ingest_completed_at timestamp with time zone,
    validation_completed_at timestamp with time zone,
    mode text DEFAULT 'balanced'::text NOT NULL,
    requested_mode text DEFAULT 'fast'::text NOT NULL,
    effective_mode text DEFAULT 'fast'::text NOT NULL,
    mode_reason text DEFAULT 'default_profile'::text NOT NULL,
    mode_changed_at timestamp with time zone DEFAULT now() NOT NULL,
    request_fingerprint text,
    created_by_identity uuid,
    CONSTRAINT download_jobs_effective_mode_check CHECK ((effective_mode = ANY (ARRAY['safe'::text, 'fast'::text, 'turbo'::text]))),
    CONSTRAINT download_jobs_requested_mode_check CHECK ((requested_mode = ANY (ARRAY['safe'::text, 'fast'::text, 'turbo'::text])))
);



CREATE TABLE public.download_pages (
    id uuid NOT NULL,
    job_id uuid NOT NULL,
    shard_id uuid NOT NULL,
    dataset text NOT NULL,
    page_no integer NOT NULL,
    query_hash text DEFAULT ''::text NOT NULL,
    status text DEFAULT 'DURABLE'::text NOT NULL,
    page_size integer DEFAULT 0 NOT NULL,
    wire_bytes bigint DEFAULT 0 NOT NULL,
    decoded_bytes bigint DEFAULT 0 NOT NULL,
    raw_path text DEFAULT ''::text NOT NULL,
    raw_sha256 text DEFAULT ''::text NOT NULL,
    rows_fetched integer DEFAULT 0 NOT NULL,
    rows_parsed integer DEFAULT 0 NOT NULL,
    rows_inserted integer DEFAULT 0 NOT NULL,
    unique_rows integer DEFAULT 0 NOT NULL,
    next_page_token_encrypted text DEFAULT ''::text NOT NULL,
    next_page_token_hash text DEFAULT ''::text NOT NULL,
    from_block bigint DEFAULT 0 NOT NULL,
    to_block bigint DEFAULT 0 NOT NULL,
    fetch_started_at timestamp with time zone,
    body_completed_at timestamp with time zone,
    durable_at timestamp with time zone,
    ingested_at timestamp with time zone,
    validated_at timestamp with time zone,
    error text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);



CREATE TABLE public.download_planning_decisions (
    id uuid NOT NULL,
    job_id uuid NOT NULL,
    subject_id uuid NOT NULL,
    dataset text NOT NULL,
    count_source text DEFAULT ''::text NOT NULL,
    predicted_rows bigint DEFAULT '-1'::integer NOT NULL,
    predicted_pages bigint DEFAULT '-1'::integer NOT NULL,
    profile_hit boolean DEFAULT false NOT NULL,
    preferred_shards integer DEFAULT 0 NOT NULL,
    initial_shards integer DEFAULT 0 NOT NULL,
    provider_safe_slots integer DEFAULT 0 NOT NULL,
    global_active_slots integer DEFAULT 0 NOT NULL,
    decision_reason text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);



CREATE TABLE public.download_shards (
    id uuid NOT NULL,
    job_id uuid NOT NULL,
    subject_id uuid NOT NULL,
    dataset_id uuid NOT NULL,
    dataset text NOT NULL,
    from_block bigint NOT NULL,
    to_block bigint NOT NULL,
    from_timestamp timestamp with time zone,
    to_timestamp timestamp with time zone,
    page_token text DEFAULT ''::text NOT NULL,
    page_no integer DEFAULT 0 NOT NULL,
    status text DEFAULT 'PENDING'::text NOT NULL,
    priority integer DEFAULT 100 NOT NULL,
    retries integer DEFAULT 0 NOT NULL,
    max_retries integer DEFAULT 5 NOT NULL,
    lease_owner text DEFAULT ''::text NOT NULL,
    leased_until timestamp with time zone,
    downloaded_rows bigint DEFAULT 0 NOT NULL,
    downloaded_bytes bigint DEFAULT 0 NOT NULL,
    raw_object text DEFAULT ''::text NOT NULL,
    error text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    page_size integer DEFAULT 0 NOT NULL,
    page_state jsonb DEFAULT '{}'::jsonb NOT NULL,
    fetched_at timestamp with time zone,
    ingested_at timestamp with time zone,
    validated_at timestamp with time zone,
    fetch_duration_ms bigint DEFAULT 0 NOT NULL,
    ingest_duration_ms bigint DEFAULT 0 NOT NULL,
    wire_bytes bigint DEFAULT 0 NOT NULL,
    decoded_bytes bigint DEFAULT 0 NOT NULL,
    page_count integer DEFAULT 0 NOT NULL,
    page_gap_avg_ms bigint DEFAULT 0 NOT NULL,
    lease_epoch bigint DEFAULT 0 NOT NULL,
    work_score double precision DEFAULT 0 NOT NULL,
    tail_boost boolean DEFAULT false NOT NULL,
    provider_key text DEFAULT ''::text NOT NULL,
    api_channel text DEFAULT ''::text NOT NULL,
    operation_class text DEFAULT ''::text NOT NULL,
    routing_policy_key text DEFAULT ''::text NOT NULL,
    routing_policy_revision bigint DEFAULT 0 NOT NULL,
    profile_revision bigint DEFAULT 0 NOT NULL
);



CREATE TABLE public.download_subjects (
    id uuid NOT NULL,
    job_id uuid NOT NULL,
    subject_type text DEFAULT 'address'::text NOT NULL,
    subject text NOT NULL,
    status text DEFAULT 'PENDING'::text NOT NULL,
    progress real DEFAULT 0 NOT NULL,
    stats jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);



CREATE TABLE public.download_volume_guard_commits (
    shard_id uuid NOT NULL,
    page_no integer NOT NULL,
    dataset_id uuid NOT NULL,
    rows_committed bigint NOT NULL,
    provider_calls bigint DEFAULT 1 NOT NULL,
    estimated_cost_usd double precision DEFAULT 0 NOT NULL,
    min_block bigint DEFAULT 0 NOT NULL,
    max_block bigint DEFAULT 0 NOT NULL,
    full_page boolean DEFAULT false NOT NULL,
    committed_at timestamp with time zone DEFAULT now() NOT NULL
);



CREATE TABLE public.download_volume_guards (
    job_id uuid NOT NULL,
    subject_id uuid NOT NULL,
    dataset_id uuid NOT NULL,
    dataset text NOT NULL,
    state text DEFAULT 'RUNNING'::text NOT NULL,
    reason text DEFAULT ''::text NOT NULL,
    risk_level text DEFAULT 'NORMAL'::text NOT NULL,
    actual_rows bigint DEFAULT 0 NOT NULL,
    projected_rows_low bigint DEFAULT 0 NOT NULL,
    projected_rows_mid bigint DEFAULT 0 NOT NULL,
    projected_rows_high bigint DEFAULT 0 NOT NULL,
    confidence double precision DEFAULT 0 NOT NULL,
    actual_provider_calls bigint DEFAULT 0 NOT NULL,
    projected_provider_calls bigint DEFAULT 0 NOT NULL,
    actual_cost_usd double precision DEFAULT 0 NOT NULL,
    projected_cost_usd double precision DEFAULT 0 NOT NULL,
    elapsed_seconds double precision DEFAULT 0 NOT NULL,
    projected_duration_seconds double precision DEFAULT 0 NOT NULL,
    guard_override boolean DEFAULT false NOT NULL,
    guard_triggered_at timestamp with time zone,
    guard_approved_at timestamp with time zone,
    guard_approved_by text DEFAULT ''::text NOT NULL,
    guard_approved_projection bigint DEFAULT 0 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);



ALTER TABLE ONLY public.address_dataset_sync_state
    ADD CONSTRAINT address_dataset_sync_state_pkey PRIMARY KEY (chain, subject, dataset);



ALTER TABLE ONLY public.checkpoints
    ADD CONSTRAINT checkpoints_pkey PRIMARY KEY (shard_id);



ALTER TABLE ONLY public.dataset_coverage
    ADD CONSTRAINT dataset_coverage_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.download_datasets
    ADD CONSTRAINT download_datasets_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.download_datasets
    ADD CONSTRAINT download_datasets_subject_id_dataset_key UNIQUE (subject_id, dataset);



ALTER TABLE ONLY public.download_jobs
    ADD CONSTRAINT download_jobs_job_no_key UNIQUE (job_no);



ALTER TABLE ONLY public.download_jobs
    ADD CONSTRAINT download_jobs_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.download_pages
    ADD CONSTRAINT download_pages_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.download_pages
    ADD CONSTRAINT download_pages_shard_id_page_no_key UNIQUE (shard_id, page_no);



ALTER TABLE ONLY public.download_planning_decisions
    ADD CONSTRAINT download_planning_decisions_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.download_shards
    ADD CONSTRAINT download_shards_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.download_subjects
    ADD CONSTRAINT download_subjects_job_id_subject_key UNIQUE (job_id, subject);



ALTER TABLE ONLY public.download_subjects
    ADD CONSTRAINT download_subjects_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.download_volume_guard_commits
    ADD CONSTRAINT download_volume_guard_commits_pkey PRIMARY KEY (shard_id, page_no);



ALTER TABLE ONLY public.download_volume_guards
    ADD CONSTRAINT download_volume_guards_job_id_subject_id_dataset_key UNIQUE (job_id, subject_id, dataset);



ALTER TABLE ONLY public.download_volume_guards
    ADD CONSTRAINT download_volume_guards_pkey PRIMARY KEY (dataset_id);



CREATE INDEX download_jobs_identity_scope_idx ON public.download_jobs USING btree (organization_id, workspace_id, created_by_identity, updated_at DESC);



CREATE UNIQUE INDEX download_jobs_reusable_fingerprint_key ON public.download_jobs USING btree (request_fingerprint) WHERE ((request_fingerprint IS NOT NULL) AND (status = ANY (ARRAY['CREATED'::text, 'PLANNING'::text, 'QUEUED'::text, 'RUNNING'::text, 'PAUSE_REQUESTED'::text, 'PAUSED'::text, 'RESUMING'::text, 'WAITING_USER_CONFIRMATION'::text])));



CREATE INDEX download_jobs_scope_time_idx ON public.download_jobs USING btree (organization_id, workspace_id, updated_at DESC);



CREATE INDEX idx_checkpoints_job ON public.checkpoints USING btree (job_id);



CREATE INDEX idx_coverage_blocks ON public.dataset_coverage USING btree (chain, subject_type, subject, dataset, from_block, to_block);



CREATE INDEX idx_coverage_lookup ON public.dataset_coverage USING btree (chain, subject_type, subject, dataset, status);



CREATE INDEX idx_datasets_subject ON public.download_datasets USING btree (subject_id);



CREATE INDEX idx_download_pages_job_status ON public.download_pages USING btree (job_id, status);



CREATE INDEX idx_pages_shard ON public.download_pages USING btree (shard_id, page_no);



CREATE INDEX idx_pages_status ON public.download_pages USING btree (status) WHERE (status = ANY (ARRAY['DURABLE'::text, 'FAILED'::text]));



CREATE INDEX idx_planning_decisions_job ON public.download_planning_decisions USING btree (job_id);



CREATE INDEX idx_planning_decisions_time ON public.download_planning_decisions USING btree (created_at DESC);



CREATE INDEX idx_shards_dataset ON public.download_shards USING btree (dataset_id);



CREATE INDEX idx_shards_dispatch ON public.download_shards USING btree (priority DESC, created_at) WHERE (status = ANY (ARRAY['PENDING'::text, 'FAILED_RETRYABLE'::text]));



CREATE INDEX idx_shards_job ON public.download_shards USING btree (job_id);



CREATE INDEX idx_subjects_job ON public.download_subjects USING btree (job_id);



CREATE INDEX idx_volume_guard_commits_dataset ON public.download_volume_guard_commits USING btree (dataset_id, committed_at);



CREATE INDEX idx_volume_guards_job_state ON public.download_volume_guards USING btree (job_id, state);


CREATE TABLE public.ankr_page_profiles (
    chain TEXT NOT NULL,
    dataset TEXT NOT NULL,
    query_shape TEXT NOT NULL,
    preferred_page_size INT NOT NULL DEFAULT 10000,
    avg_rows INT NOT NULL DEFAULT 0,
    avg_wire_bytes BIGINT NOT NULL DEFAULT 0,
    avg_decoded_bytes BIGINT NOT NULL DEFAULT 0,
    avg_latency_ms BIGINT NOT NULL DEFAULT 0,
    p95_latency_ms BIGINT NOT NULL DEFAULT 0,
    timeout_rate DOUBLE PRECISION NOT NULL DEFAULT 0,
    oversized_rate DOUBLE PRECISION NOT NULL DEFAULT 0,
    pages_observed BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain, dataset, query_shape)
);
