-- Phase 0 architecture baseline: durable investigation state, unified jobs,
-- version registries, projections, evidence and transactional outbox.
CREATE TABLE IF NOT EXISTS investigations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID REFERENCES organizations(id),
    workspace_id UUID REFERENCES workspaces(id),
    case_id UUID REFERENCES cases(id),
    subject JSONB NOT NULL,
    goal TEXT NOT NULL,
    mode VARCHAR(16) NOT NULL DEFAULT 'assisted',
    status VARCHAR(32) NOT NULL DEFAULT 'created',
    budget JSONB NOT NULL DEFAULT '{}',
    stop_conditions JSONB NOT NULL DEFAULT '{}',
    state JSONB NOT NULL DEFAULT '{}',
    contract_version VARCHAR(16) NOT NULL DEFAULT '1.0',
    revision BIGINT NOT NULL DEFAULT 1,
    idempotency_key TEXT,
    created_by UUID REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (mode IN ('manual','assisted','auto')),
    CHECK (status IN ('created','running','waiting_data','waiting_review','paused','completed','failed','cancelled'))
);
CREATE INDEX IF NOT EXISTS idx_investigations_scope ON investigations (organization_id, workspace_id, updated_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS uq_investigations_idempotency ON investigations (organization_id, idempotency_key) WHERE idempotency_key IS NOT NULL;

CREATE TABLE IF NOT EXISTS platform_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    job_type VARCHAR(64) NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    priority INTEGER NOT NULL DEFAULT 100,
    resource_class VARCHAR(32) NOT NULL DEFAULT 'background',
    aggregate_type VARCHAR(64),
    aggregate_id UUID,
    payload JSONB NOT NULL DEFAULT '{}',
    checkpoint JSONB NOT NULL DEFAULT '{}',
    request_context JSONB NOT NULL DEFAULT '{}',
    attempt INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 3,
    lease_owner TEXT,
    leased_until TIMESTAMPTZ,
    error TEXT NOT NULL DEFAULT '',
    contract_version VARCHAR(16) NOT NULL DEFAULT '1.0',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (resource_class IN ('interactive','background','backfill','maintenance'))
);
CREATE INDEX IF NOT EXISTS idx_platform_jobs_ready ON platform_jobs (resource_class, priority, created_at) WHERE status IN ('pending','retry');

CREATE TABLE IF NOT EXISTS schema_registry (
    schema_name VARCHAR(128) NOT NULL,
    schema_version VARCHAR(32) NOT NULL,
    compatibility VARCHAR(16) NOT NULL DEFAULT 'backward',
    definition JSONB NOT NULL DEFAULT '{}',
    active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (schema_name, schema_version)
);

CREATE TABLE IF NOT EXISTS projection_registry (
    projection_name VARCHAR(128) NOT NULL,
    projection_version VARCHAR(32) NOT NULL,
    source_schema VARCHAR(128) NOT NULL,
    source_version VARCHAR(32) NOT NULL,
    status VARCHAR(24) NOT NULL DEFAULT 'active',
    checkpoint JSONB NOT NULL DEFAULT '{}',
    rebuilt_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (projection_name, projection_version)
);

CREATE TABLE IF NOT EXISTS investigation_findings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    investigation_id UUID NOT NULL REFERENCES investigations(id) ON DELETE CASCADE,
    finding_type VARCHAR(64) NOT NULL,
    title TEXT NOT NULL,
    summary TEXT NOT NULL DEFAULT '',
    confidence DOUBLE PRECISION NOT NULL DEFAULT 0,
    status VARCHAR(24) NOT NULL DEFAULT 'candidate',
    properties JSONB NOT NULL DEFAULT '{}',
    algorithm VARCHAR(128) NOT NULL DEFAULT '',
    algorithm_version VARCHAR(32) NOT NULL DEFAULT '',
    contract_version VARCHAR(16) NOT NULL DEFAULT '1.0',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (confidence >= 0 AND confidence <= 1)
);

CREATE TABLE IF NOT EXISTS investigation_evidence (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    investigation_id UUID NOT NULL REFERENCES investigations(id) ON DELETE CASCADE,
    evidence_type VARCHAR(64) NOT NULL,
    title TEXT NOT NULL,
    source_refs JSONB NOT NULL DEFAULT '[]',
    content_hash VARCHAR(128) NOT NULL DEFAULT '',
    object_ref TEXT NOT NULL DEFAULT '',
    provenance JSONB NOT NULL,
    contract_version VARCHAR(16) NOT NULL DEFAULT '1.0',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS finding_evidence_links (
    finding_id UUID NOT NULL REFERENCES investigation_findings(id) ON DELETE CASCADE,
    evidence_id UUID NOT NULL REFERENCES investigation_evidence(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (finding_id, evidence_id)
);

CREATE TABLE IF NOT EXISTS graph_patches (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    investigation_id UUID NOT NULL REFERENCES investigations(id) ON DELETE CASCADE,
    canvas_id UUID,
    base_revision BIGINT NOT NULL,
    revision BIGINT NOT NULL,
    patch JSONB NOT NULL,
    contract_version VARCHAR(16) NOT NULL DEFAULT '1.0',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (investigation_id, canvas_id, revision)
);

CREATE TABLE IF NOT EXISTS outbox_events (
    id UUID PRIMARY KEY,
    event_type VARCHAR(128) NOT NULL,
    event_version VARCHAR(32) NOT NULL,
    aggregate_type VARCHAR(64) NOT NULL,
    aggregate_id TEXT NOT NULL,
    sequence BIGINT NOT NULL,
    subject VARCHAR(255) NOT NULL,
    envelope JSONB NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (aggregate_type, aggregate_id, sequence)
);
CREATE INDEX IF NOT EXISTS idx_outbox_pending ON outbox_events (available_at, created_at) WHERE published_at IS NULL;

INSERT INTO schema_registry (schema_name, schema_version, definition) VALUES
('platform.contracts', '1.0', '{"frozen":["SubjectRef","ChainAdapter","DataRequirement","SemanticEvent","GraphNode","GraphEdge","GraphPatch","Finding","Evidence","Capability","Job","EventEnvelope","RequestContext"]}'),
('semantic.event', '1.0', '{"format":"SemanticEvent"}'),
('graph.patch', '1.0', '{"format":"GraphPatch"}')
ON CONFLICT (schema_name, schema_version) DO NOTHING;

INSERT INTO projection_registry (projection_name, projection_version, source_schema, source_version) VALUES
('investigation_graph', '1.0', 'graph.patch', '1.0'),
('investigation_findings', '1.0', 'semantic.event', '1.0')
ON CONFLICT (projection_name, projection_version) DO NOTHING;
