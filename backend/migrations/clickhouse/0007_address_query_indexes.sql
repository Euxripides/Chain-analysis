-- 0007_address_query_indexes.sql — accelerate authoritative address lookups.
--
-- The primary keys are transaction identities, so address overview/filter
-- queries otherwise scan every row in a chain partition. Keep the canonical
-- tables as the source of truth and add data-skipping indexes for both sides
-- of an address relationship.
ALTER TABLE blockchain.transactions
    ADD INDEX IF NOT EXISTS idx_transactions_from from_address TYPE bloom_filter(0.01) GRANULARITY 1;

ALTER TABLE blockchain.transactions
    ADD INDEX IF NOT EXISTS idx_transactions_to to_address TYPE bloom_filter(0.01) GRANULARITY 1;

ALTER TABLE blockchain.token_transfers
    ADD INDEX IF NOT EXISTS idx_token_transfers_from from_address TYPE bloom_filter(0.01) GRANULARITY 1;

ALTER TABLE blockchain.token_transfers
    ADD INDEX IF NOT EXISTS idx_token_transfers_to to_address TYPE bloom_filter(0.01) GRANULARITY 1;

