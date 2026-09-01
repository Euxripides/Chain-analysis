-- 0004_method_abi.sql — V1.3 Transaction Method / ABI 解析增强（§7/§22/§29）
-- 原始 input 必须保留（§6）：本迁移只新增解析结果列，不改动 input。
-- transactions 是 ReplacingMergeTree(source_ingested_at)，重解析写入新版本即可覆盖旧解析（§33/§62-18）。
ALTER TABLE blockchain.transactions
    ADD COLUMN IF NOT EXISTS method_name String DEFAULT '';

ALTER TABLE blockchain.transactions
    ADD COLUMN IF NOT EXISTS method_signature String DEFAULT '';

ALTER TABLE blockchain.transactions
    ADD COLUMN IF NOT EXISTS method_source LowCardinality(String) DEFAULT 'unknown';

ALTER TABLE blockchain.transactions
    ADD COLUMN IF NOT EXISTS method_verified UInt8 DEFAULT 0;

ALTER TABLE blockchain.transactions
    ADD COLUMN IF NOT EXISTS method_confidence LowCardinality(String) DEFAULT 'unknown';

ALTER TABLE blockchain.transactions
    ADD COLUMN IF NOT EXISTS method_type LowCardinality(String) DEFAULT '';

ALTER TABLE blockchain.transactions
    ADD COLUMN IF NOT EXISTS method_resolved_at Nullable(DateTime64(3, 'UTC'));

-- Method 搜索/筛选（§28）：selector 与 name 都是高选择性等值/前缀查询
ALTER TABLE blockchain.transactions
    ADD INDEX IF NOT EXISTS idx_transactions_method_id method_id TYPE bloom_filter GRANULARITY 4;

-- Decoded Input 单独存表（§22）：主 transactions 只保留轻量 Method 信息
CREATE TABLE IF NOT EXISTS blockchain.transaction_decoded_inputs
(
    chain LowCardinality(String),
    tx_hash String,
    method_id String,
    method_name String,
    method_signature String,
    decoded_json String,
    decoded_logs_json String,
    decode_source LowCardinality(String),
    verified UInt8 DEFAULT 0,
    decoded_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(decoded_at)
PARTITION BY chain
ORDER BY (chain, tx_hash);

-- Event Logs 解析预留（§29）：topic0 → event name/signature
ALTER TABLE blockchain.logs
    ADD COLUMN IF NOT EXISTS event_name String DEFAULT '';

ALTER TABLE blockchain.logs
    ADD COLUMN IF NOT EXISTS event_signature String DEFAULT '';

ALTER TABLE blockchain.logs
    ADD COLUMN IF NOT EXISTS event_source LowCardinality(String) DEFAULT 'unknown';

ALTER TABLE blockchain.logs
    ADD COLUMN IF NOT EXISTS event_verified UInt8 DEFAULT 0;
