CREATE TABLE IF NOT EXISTS semantic_events (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(), chain TEXT NOT NULL, subject TEXT NOT NULL, tx_hash TEXT NOT NULL,
 event_type TEXT NOT NULL, confidence DOUBLE PRECISION NOT NULL, payload JSONB NOT NULL DEFAULT '{}', provenance JSONB NOT NULL DEFAULT '{}',
 projection_version TEXT NOT NULL, algorithm_version TEXT NOT NULL, data_version TEXT NOT NULL, occurred_at TIMESTAMPTZ,
 computed_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE(chain,subject,tx_hash,event_type,projection_version)
);
CREATE INDEX IF NOT EXISTS idx_semantic_events_subject_time ON semantic_events(chain,subject,occurred_at DESC);
INSERT INTO schema_registry(schema_name,schema_version,definition) VALUES
 ('protocol.semantic_event','1.0','{"types":["SWAP","BRIDGE","DEPOSIT","WITHDRAW","ADD_LIQUIDITY","REMOVE_LIQUIDITY","CONTRACT"]}')
 ON CONFLICT DO NOTHING;
INSERT INTO projection_registry(projection_name,projection_version,source_schema,source_version,status) VALUES
 ('protocol_semantics','1.0','semantic.event','1.0','active')
 ON CONFLICT (projection_name,projection_version) DO UPDATE SET status=EXCLUDED.status;
