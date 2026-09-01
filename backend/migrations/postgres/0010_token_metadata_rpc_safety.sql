-- Stop the runaway metadata backfill and remove malformed/truncated token
-- identifiers before enforcing the RPC boundary at the database layer.
UPDATE token_metadata_backfill_jobs
SET status='failed', completed_at=now(), updated_at=now(),
    last_error='stopped by token metadata RPC safety migration'
WHERE status IN ('pending','running');

-- A process stop can leave claimed rows in running forever. Do not replay
-- them automatically during this safety migration; an administrator can
-- explicitly enqueue a valid contract again if needed.
UPDATE token_metadata_resolve_queue
SET status='failed_final', next_retry_at=NULL, updated_at=now(),
    last_error='interrupted by token metadata RPC safety migration'
WHERE status='running';

DELETE FROM token_metadata_resolve_queue
WHERE normalized_address !~ '^0x[0-9a-f]{40}$';
DELETE FROM token_metadata_overrides
WHERE normalized_address !~ '^0x[0-9a-f]{40}$';
DELETE FROM token_metadata_observations
WHERE normalized_address !~ '^0x[0-9a-f]{40}$';
DELETE FROM token_contract_catalog
WHERE normalized_address !~ '^0x[0-9a-f]{40}$';
DELETE FROM token_metadata
WHERE normalized_address !~ '^0x[0-9a-f]{40}$';

ALTER TABLE token_metadata_resolve_queue
    ADD CONSTRAINT token_metadata_resolve_queue_valid_address
    CHECK (normalized_address ~ '^0x[0-9a-f]{40}$');
ALTER TABLE token_contract_catalog
    ADD CONSTRAINT token_contract_catalog_valid_address
    CHECK (normalized_address ~ '^0x[0-9a-f]{40}$');
ALTER TABLE token_metadata
    ADD CONSTRAINT token_metadata_valid_address
    CHECK (normalized_address ~ '^0x[0-9a-f]{40}$');
