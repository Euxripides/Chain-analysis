-- One-time correction for rows written before token-specific decimal scaling.
-- Run with clickhouse-client --multiquery after applying schema migrations.

SET mutations_sync = 2;

ALTER TABLE blockchain.token_transfers UPDATE
    amount = divideDecimal(
        toDecimal256(amount_raw, 0),
        toDecimal256(concat('1', repeat('0', toUInt64(token_decimals))), 0),
        38
    )
WHERE amount_raw != ''
  AND length(amount_raw) <= toUInt64(token_decimals) + 38;

ALTER TABLE blockchain.address_balances UPDATE
    balance = divideDecimal(
        toDecimal256(balance_raw, 0),
        toDecimal256(concat('1', repeat('0', toUInt64(token_decimals))), 0),
        38
    )
WHERE balance_raw != ''
  AND length(balance_raw) <= toUInt64(token_decimals) + 38;

-- Freeze the existing activity subjects before rebuilding. Without this set,
-- the first insert would make counterparties eligible for the second insert.
DROP TABLE IF EXISTS blockchain.amount_backfill_subjects;
CREATE TABLE blockchain.amount_backfill_subjects (address String) ENGINE = Set;
INSERT INTO blockchain.amount_backfill_subjects
SELECT DISTINCT address FROM blockchain.address_activity FINAL;

-- Correct/recreate transfer activity from the authoritative transfer table.
-- ReplacingMergeTree uses the newer source_ingested_at for matching legacy keys.
INSERT INTO blockchain.address_activity
    (chain, address, counterparty, direction, activity_type, timestamp,
     block_number, tx_hash, log_index, token_address, token_symbol,
     token_decimals, amount_raw, amount, amount_usd, dataset,
     source_provider, source_ingested_at)
SELECT
    chain, from_address, to_address, if(from_address = to_address, 'self', 'out'),
    'transfer', timestamp, block_number, tx_hash, log_index, token_address,
    token_symbol, token_decimals, amount_raw,
    divideDecimal(
        toDecimal256(amount_raw, 0),
        toDecimal256(concat('1', repeat('0', toUInt64(token_decimals))), 0),
        38
    ),
    '', 'token_transfers', source_provider, now64(3)
FROM blockchain.token_transfers FINAL
WHERE from_address IN blockchain.amount_backfill_subjects
  AND amount_raw != ''
  AND length(amount_raw) <= toUInt64(token_decimals) + 38;

INSERT INTO blockchain.address_activity
    (chain, address, counterparty, direction, activity_type, timestamp,
     block_number, tx_hash, log_index, token_address, token_symbol,
     token_decimals, amount_raw, amount, amount_usd, dataset,
     source_provider, source_ingested_at)
SELECT
    chain, to_address, from_address, if(from_address = to_address, 'self', 'in'),
    'transfer', timestamp, block_number, tx_hash, log_index, token_address,
    token_symbol, token_decimals, amount_raw,
    divideDecimal(
        toDecimal256(amount_raw, 0),
        toDecimal256(concat('1', repeat('0', toUInt64(token_decimals))), 0),
        38
    ),
    '', 'token_transfers', source_provider, now64(3)
FROM blockchain.token_transfers FINAL
WHERE to_address IN blockchain.amount_backfill_subjects
  AND amount_raw != ''
  AND length(amount_raw) <= toUInt64(token_decimals) + 38;

DROP TABLE blockchain.amount_backfill_subjects;
