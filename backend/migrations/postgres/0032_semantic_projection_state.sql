CREATE TABLE IF NOT EXISTS semantic_projection_state (
 chain TEXT NOT NULL,
 subject TEXT NOT NULL,
 projection_version TEXT NOT NULL,
 algorithm_version TEXT NOT NULL,
 data_version TEXT NOT NULL,
 event_count BIGINT NOT NULL DEFAULT 0,
 computed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(chain, subject, projection_version)
);

CREATE INDEX IF NOT EXISTS idx_semantic_projection_state_subject
 ON semantic_projection_state(chain, subject, computed_at DESC);
