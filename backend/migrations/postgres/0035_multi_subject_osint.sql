CREATE TABLE IF NOT EXISTS investigation_subjects (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 investigation_id UUID NOT NULL REFERENCES investigations(id) ON DELETE CASCADE,
 subject_type TEXT NOT NULL,
 subject_value TEXT NOT NULL,
 label TEXT NOT NULL DEFAULT '',
 source TEXT NOT NULL,
 confidence DOUBLE PRECISION NOT NULL,
 evidence_refs JSONB NOT NULL DEFAULT '[]',
 provenance JSONB NOT NULL DEFAULT '{}',
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(investigation_id, subject_type, subject_value),
 CHECK(subject_type IN ('ADDRESS','DOMAIN','IP','SOCIAL_ACCOUNT','APK','ORGANIZATION','FILE','CONTRACT','TRANSACTION')),
 CHECK(source IN ('SYSTEM','USER','PROVIDER','OSINT','HEURISTIC','AGENT','CASE')),
 CHECK(confidence >= 0 AND confidence <= 1)
);

CREATE TABLE IF NOT EXISTS entity_relationships (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 investigation_id UUID NOT NULL REFERENCES investigations(id) ON DELETE CASCADE,
 source_subject_id UUID NOT NULL REFERENCES investigation_subjects(id) ON DELETE CASCADE,
 target_subject_id UUID NOT NULL REFERENCES investigation_subjects(id) ON DELETE CASCADE,
 relation_type TEXT NOT NULL,
 source TEXT NOT NULL,
 confidence DOUBLE PRECISION NOT NULL,
 evidence_refs JSONB NOT NULL DEFAULT '[]',
 provenance JSONB NOT NULL DEFAULT '{}',
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(investigation_id, source_subject_id, target_subject_id, relation_type),
 CHECK(source_subject_id <> target_subject_id),
 CHECK(source IN ('SYSTEM','USER','PROVIDER','OSINT','HEURISTIC','AGENT','CASE')),
 CHECK(confidence >= 0 AND confidence <= 1)
);

CREATE INDEX IF NOT EXISTS idx_investigation_subjects_case ON investigation_subjects(investigation_id,created_at);
CREATE INDEX IF NOT EXISTS idx_entity_relationships_case ON entity_relationships(investigation_id,created_at);

INSERT INTO schema_registry(schema_name,schema_version,definition) VALUES
 ('investigation.multi_subject','1.0','{"types":["ADDRESS","DOMAIN","IP","SOCIAL_ACCOUNT","APK","ORGANIZATION","FILE","CONTRACT","TRANSACTION"],"source_required":true,"evidence_refs_required":true}')
 ON CONFLICT DO NOTHING;
