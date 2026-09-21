-- 0010_solana_drpc.sql — Solana dRPC 接入的 ClickHouse 扩展列与程序日志表（规范 §2.2 / 文档 §8.4）
--
-- 背景：统一事实表此前是 EVM/TRON 字段模型。Solana 的一笔交易有多个签名者、账户键与程序 ID，
--   手续费口径也不是 gas，token transfer 需要指令下标才能与 EVM 的 log_index 语义对齐，
--   程序日志更不是 EVM topics。缺列会让归一行在写入时被静默丢掉。
-- 幂等：ADD COLUMN IF NOT EXISTS / CREATE TABLE IF NOT EXISTS。既有行取默认值，不重写历史数据。
-- 注意：迁移执行器按分号切分语句，本文件任何位置（含注释与字符串）都不得出现分号。

ALTER TABLE blockchain.transactions
    ADD COLUMN IF NOT EXISTS fee_raw String DEFAULT '',
    ADD COLUMN IF NOT EXISTS signers Array(String) DEFAULT [],
    ADD COLUMN IF NOT EXISTS account_keys Array(String) DEFAULT [],
    ADD COLUMN IF NOT EXISTS program_ids Array(String) DEFAULT [],
    ADD COLUMN IF NOT EXISTS source_api LowCardinality(String) DEFAULT '',
    ADD COLUMN IF NOT EXISTS source_method LowCardinality(String) DEFAULT '',
    ADD COLUMN IF NOT EXISTS source_request_id String DEFAULT '',
    ADD COLUMN IF NOT EXISTS chain_payload_json String DEFAULT '';

-- from_token_account / to_token_account 已由 0008 添加，这里不重复。
ALTER TABLE blockchain.token_transfers
    ADD COLUMN IF NOT EXISTS instruction_index UInt32 DEFAULT 0,
    ADD COLUMN IF NOT EXISTS inner_instruction_index Int32 DEFAULT -1,
    ADD COLUMN IF NOT EXISTS source_api LowCardinality(String) DEFAULT '',
    ADD COLUMN IF NOT EXISTS source_request_id String DEFAULT '';

ALTER TABLE blockchain.nft_transfers
    ADD COLUMN IF NOT EXISTS instruction_index UInt32 DEFAULT 0,
    ADD COLUMN IF NOT EXISTS inner_instruction_index Int32 DEFAULT -1,
    ADD COLUMN IF NOT EXISTS source_api LowCardinality(String) DEFAULT '',
    ADD COLUMN IF NOT EXISTS source_request_id String DEFAULT '';

-- 程序日志独立成表：EVM 的 topics/ABI 解析套不到 Solana 的 logMessages。
-- 读路径不分叉：dataset=logs 且 chain=solana 时同时写 blockchain.logs 与本表（规范 §2.3）。
CREATE TABLE IF NOT EXISTS blockchain.solana_program_logs
(
    chain LowCardinality(String),
    slot UInt64,
    timestamp DateTime64(3, 'UTC'),
    signature String,
    log_index UInt32,
    program_id String,
    message String,
    invoke_depth UInt16,
    success UInt8,
    source_provider LowCardinality(String),
    source_api LowCardinality(String),
    source_request_id String,
    source_ingested_at DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(source_ingested_at)
PARTITION BY toYYYYMM(timestamp)
ORDER BY (chain, signature, log_index);
