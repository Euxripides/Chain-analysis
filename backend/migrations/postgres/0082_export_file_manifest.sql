ALTER TABLE exports ADD COLUMN IF NOT EXISTS file_manifest jsonb NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE exports ADD COLUMN IF NOT EXISTS files_deleted_at timestamptz;
ALTER TABLE export_tickets ADD COLUMN IF NOT EXISTS file_dataset text NOT NULL DEFAULT '';
