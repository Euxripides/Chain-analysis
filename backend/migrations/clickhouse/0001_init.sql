-- 0001_init.sql — ClickHouse 数据面（统一表，Provider 只是来源属性）
CREATE DATABASE IF NOT EXISTS blockchain;

CREATE TABLE IF NOT EXISTS blockchain.transactions
(
    chain LowCardinality(String),
    block_number UInt64,
    block_hash String,
    tx_hash String,
    timestamp DateTime64(3, 'UTC'),
    from_address String,
    to_address String,
    value_raw String,
    nonce UInt64,
    gas UInt64,
    gas_used UInt64,
    gas_price String,
    tx_type String,
    status UInt8,
    input String,
    method_id String,
    source_provider LowCardinality(String),
    source_ingested_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(source_ingested_at)
PARTITION BY (chain, toYYYYMM(timestamp))
ORDER BY (chain, tx_hash);

CREATE TABLE IF NOT EXISTS blockchain.token_transfers
(
    chain LowCardinality(String),
    block_number UInt64,
    timestamp DateTime64(3, 'UTC'),
    tx_hash String,
    log_index UInt32,
    token_address String,
    token_symbol String,
    token_decimals UInt16,
    from_address String,
    to_address String,
    amount_raw String,
    amount Decimal256(38),
    source_provider LowCardinality(String),
    source_ingested_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(source_ingested_at)
PARTITION BY (chain, toYYYYMM(timestamp))
ORDER BY (chain, tx_hash, log_index, token_address);

CREATE TABLE IF NOT EXISTS blockchain.nft_transfers
(
    chain LowCardinality(String),
    block_number UInt64,
    timestamp DateTime64(3, 'UTC'),
    tx_hash String,
    log_index UInt32,
    contract_address String,
    token_id String,
    from_address String,
    to_address String,
    token_name String,
    token_symbol String,
    amount_raw String,
    source_provider LowCardinality(String),
    source_ingested_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(source_ingested_at)
PARTITION BY (chain, toYYYYMM(timestamp))
ORDER BY (chain, tx_hash, log_index, contract_address, token_id);

CREATE TABLE IF NOT EXISTS blockchain.logs
(
    chain LowCardinality(String),
    block_number UInt64,
    block_hash String,
    tx_hash String,
    log_index UInt32,
    address String,
    topics Array(String),
    data String,
    removed UInt8 DEFAULT 0,
    timestamp DateTime64(3, 'UTC'),
    source_provider LowCardinality(String),
    source_ingested_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(source_ingested_at)
PARTITION BY (chain, toYYYYMM(timestamp))
ORDER BY (chain, tx_hash, log_index);

CREATE TABLE IF NOT EXISTS blockchain.address_balances
(
    chain LowCardinality(String),
    address String,
    token_symbol String,
    token_name String,
    token_address String,
    token_type String,
    token_decimals UInt16,
    balance_raw String,
    balance Decimal256(38),
    balance_usd String,
    token_price String,
    snapshot_block UInt64,
    timestamp DateTime64(3, 'UTC') DEFAULT now64(3),
    source_provider LowCardinality(String),
    source_ingested_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(source_ingested_at)
PARTITION BY (chain, toYYYYMM(timestamp))
ORDER BY (chain, address, token_symbol, token_address);
