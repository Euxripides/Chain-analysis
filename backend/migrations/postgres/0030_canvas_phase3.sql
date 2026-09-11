-- Phase 3: case-scoped durable analysis canvases and immutable snapshots.
CREATE TABLE IF NOT EXISTS investigation_canvases (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id),
    workspace_id UUID NOT NULL REFERENCES workspaces(id),
    case_id UUID REFERENCES cases(id) ON DELETE CASCADE,
    investigation_id UUID REFERENCES investigations(id) ON DELETE SET NULL,
    parent_canvas_id UUID REFERENCES investigation_canvases(id) ON DELETE SET NULL,
    name TEXT NOT NULL,
    node_refs JSONB NOT NULL DEFAULT '[]'::jsonb,
    edge_refs JSONB NOT NULL DEFAULT '[]'::jsonb,
    groups JSONB NOT NULL DEFAULT '[]'::jsonb,
    annotations JSONB NOT NULL DEFAULT '[]'::jsonb,
    filters JSONB NOT NULL DEFAULT '{}'::jsonb,
    layout JSONB NOT NULL DEFAULT '{}'::jsonb,
    camera JSONB NOT NULL DEFAULT '{}'::jsonb,
    view_state JSONB NOT NULL DEFAULT '{}'::jsonb,
    revision BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(case_id, name)
);

CREATE INDEX IF NOT EXISTS idx_investigation_canvases_case_updated
    ON investigation_canvases(case_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_investigation_canvases_investigation
    ON investigation_canvases(investigation_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS canvas_snapshots (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    canvas_id UUID NOT NULL REFERENCES investigation_canvases(id) ON DELETE CASCADE,
    canvas_revision BIGINT NOT NULL,
    name TEXT NOT NULL,
    state JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_canvas_snapshots_canvas_created
    ON canvas_snapshots(canvas_id, created_at DESC);

INSERT INTO schema_registry(schema_name, schema_version, compatibility, active)
VALUES ('canvas.state', '1.0', 'backward', true),
       ('canvas.snapshot', '1.0', 'immutable', true)
ON CONFLICT (schema_name, schema_version) DO UPDATE SET active=EXCLUDED.active;

INSERT INTO projection_registry(projection_name, projection_version, source_schema, source_version, status)
VALUES ('canvas_graph', '1.0', 'graph.patch', '1.0', 'active')
ON CONFLICT (projection_name, projection_version) DO UPDATE SET status=EXCLUDED.status;
