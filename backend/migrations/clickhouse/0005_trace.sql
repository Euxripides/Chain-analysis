-- 0005_trace.sql — V1.5 Trace / Internal Transactions（§106）
-- 规范化调用树与派生内部活动分表：前者用于回放，后者用于查询与资金分析。
-- 巨大的原始 Trace 不进这里，落 MinIO（§56/§107）。
CREATE TABLE IF NOT EXISTS blockchain.transaction_traces
(
    chain LowCardinality(String),
    tx_hash String,
    source LowCardinality(String),
    frame_count UInt32,
    max_depth UInt16,
    root_json String,
    raw_object_key String,
    raw_bytes UInt32,
    traced_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(traced_at)
PARTITION BY chain
ORDER BY (chain, tx_hash);

CREATE TABLE IF NOT EXISTS blockchain.internal_calls
(
    chain LowCardinality(String),
    tx_hash String,
    call_index UInt32,
    depth UInt16,
    trace_path String,
    call_type LowCardinality(String),
    activity_type LowCardinality(String),
    from_address String,
    to_address String,
    value_raw String,
    method_id String,
    gas UInt64,
    gas_used UInt64,
    success UInt8,
    error String,
    traced_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(traced_at)
PARTITION BY chain
ORDER BY (chain, tx_hash, call_index);

-- 内部转账按地址查询（资金分析入口）
ALTER TABLE blockchain.internal_calls
    ADD INDEX IF NOT EXISTS idx_internal_calls_from from_address TYPE bloom_filter GRANULARITY 4;

ALTER TABLE blockchain.internal_calls
    ADD INDEX IF NOT EXISTS idx_internal_calls_to to_address TYPE bloom_filter GRANULARITY 4;
