-- TRON V1 reuses the unified fact tables. Only missing cross-chain fields are added.
ALTER TABLE blockchain.transactions ADD COLUMN IF NOT EXISTS native_symbol LowCardinality(String) DEFAULT '';
ALTER TABLE blockchain.transactions ADD COLUMN IF NOT EXISTS confirmation LowCardinality(String) DEFAULT '';
ALTER TABLE blockchain.transactions ADD COLUMN IF NOT EXISTS raw_type String DEFAULT '';
ALTER TABLE blockchain.transactions ADD COLUMN IF NOT EXISTS raw_status String DEFAULT '';

ALTER TABLE blockchain.internal_calls ADD COLUMN IF NOT EXISTS block_number UInt64 DEFAULT 0;
ALTER TABLE blockchain.internal_calls ADD COLUMN IF NOT EXISTS timestamp DateTime64(3, 'UTC') DEFAULT toDateTime64(0, 3, 'UTC');
ALTER TABLE blockchain.internal_calls ADD COLUMN IF NOT EXISTS source_provider LowCardinality(String) DEFAULT '';
