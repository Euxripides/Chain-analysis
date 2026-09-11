CREATE TABLE IF NOT EXISTS advanced_analysis_results (
 chain TEXT NOT NULL,
 subject TEXT NOT NULL,
 projection_version TEXT NOT NULL,
 algorithm_version TEXT NOT NULL,
 data_version TEXT NOT NULL,
 result JSONB NOT NULL DEFAULT '{}',
 computed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(chain, subject, projection_version)
);

CREATE INDEX IF NOT EXISTS idx_advanced_analysis_results_subject
 ON advanced_analysis_results(chain, subject, computed_at DESC);

INSERT INTO schema_registry(schema_name,schema_version,definition) VALUES
 ('analysis.advanced_result','1.0','{"signals":["FAN_IN","FAN_OUT","CONSOLIDATION","DISTRIBUTION","RAPID_FORWARD"],"profit_basis":"NET_FLOW_ONLY","related_wallet_kind":"HEURISTIC"}')
 ON CONFLICT DO NOTHING;

INSERT INTO projection_registry(projection_name,projection_version,source_schema,source_version,status) VALUES
 ('advanced_investigation','1.0','semantic.event','1.0','active')
 ON CONFLICT (projection_name,projection_version) DO UPDATE SET status=EXCLUDED.status;
