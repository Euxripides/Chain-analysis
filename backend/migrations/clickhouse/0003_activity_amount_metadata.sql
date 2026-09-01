-- Activity rows retain the source token precision and raw value so displayed
-- amounts are auditable and are never inferred from the Decimal column scale.
ALTER TABLE blockchain.address_activity
    ADD COLUMN IF NOT EXISTS log_index UInt32 AFTER tx_hash;

ALTER TABLE blockchain.address_activity
    ADD COLUMN IF NOT EXISTS token_symbol String AFTER token_address;

ALTER TABLE blockchain.address_activity
    ADD COLUMN IF NOT EXISTS token_decimals UInt16 AFTER token_symbol;

ALTER TABLE blockchain.address_activity
    ADD COLUMN IF NOT EXISTS amount_raw String AFTER token_decimals;
