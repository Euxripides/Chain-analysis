CREATE TABLE IF NOT EXISTS investigation_reports (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 investigation_id UUID NOT NULL REFERENCES investigations(id) ON DELETE CASCADE,
 revision BIGINT NOT NULL,
 format TEXT NOT NULL DEFAULT 'markdown',
 object_key TEXT NOT NULL,
 content_hash TEXT NOT NULL,
 size_bytes BIGINT NOT NULL,
 metadata JSONB NOT NULL DEFAULT '{}',
 generated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(investigation_id, revision)
);

CREATE INDEX IF NOT EXISTS idx_investigation_reports_latest
 ON investigation_reports(investigation_id, revision DESC);

INSERT INTO schema_registry(schema_name,schema_version,definition) VALUES
 ('investigation.report','1.0','{"storage":"object","formats":["markdown"],"requires":["finding","evidence","provenance","content_hash"]}')
 ON CONFLICT DO NOTHING;
