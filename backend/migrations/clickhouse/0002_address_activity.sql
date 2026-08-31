-- 0002_address_activity.sql — 地址活动汇总表（§65/§87：资金分析/智能调查统一查询面）
CREATE TABLE IF NOT EXISTS blockchain.address_activity
(
    chain LowCardinality(String),
    address String,
    counterparty String,
    direction LowCardinality(String),
    activity_type LowCardinality(String),
    timestamp DateTime64(3, 'UTC'),
    block_number UInt64,
    tx_hash String,
    token_address String,
    amount Decimal256(38),
    amount_usd String,
    dataset LowCardinality(String),
    source_provider LowCardinality(String),
    source_ingested_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(source_ingested_at)
PARTITION BY (chain, toYYYYMM(timestamp))
ORDER BY (chain, address, timestamp, tx_hash, activity_type, counterparty, token_address);
