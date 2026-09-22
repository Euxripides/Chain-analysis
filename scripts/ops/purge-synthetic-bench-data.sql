-- 清退合成压测数据（synthetic / bench dataset）
--
-- 背景（2026-09-23 定位）：
--   ClickHouse 的 blockchain 库里混进了一批**压测生成**的数据：符号统一 MCK、tx_hash 是十六进制
--   计数器（如 0x1dcd6500）、from 是常量 0x1111…1111、to/token 地址是计数器截出来的短串；
--   transactions 里还有一条链名直接叫 bench（60 万行，全部落在 2026-08-31 半秒内）。
--   它污染了 address_activity / token_transfers / transactions / logs，并让"代币图标覆盖率"
--   之类的统计完全失真（13,866 个假代币合约占掉了分母）。
--
-- 判定指纹（三者互证，不是只按时间窗删）：
--   * 长度：EVM tx_hash 66（0x+64）、TRON 64、Solana base58 签名 87/88；
--     其余长度（3~11 位）只可能来自生成器 —— 实测这些行只出现在 bsc / eth / bench 且符号为 MCK。
--   * 符号：token_transfers.token_symbol = 'MCK'。
--   * 链：transactions.chain = 'bench'。
--
-- 前置（必须先做完，缺一不可）：
--   1. ALTER TABLE blockchain.<t> FREEZE WITH NAME 'mck-purge-<date>'   -- 快照（硬链接，秒级）
--      实测：shadow/mck-purge-20260922/store/**，5 张表 25 GiB（硬链接，不额外占盘）
--   2. 抽样留证：SELECT * ... WHERE <predicate> LIMIT 2000 FORMAT Parquet
--   3. PostgreSQL 侧 token_metadata/token_logo_assets 的 pg_dump（L3 会写这些表）
--
-- 执行方式：**轻量 DELETE**（ClickHouse 23.3+ 的 DELETE FROM，只打删除标记，不重写分区）。
--   不要用 ALTER TABLE ... DELETE（mutation，会重写 8.5 GiB 分区、几十分钟）。
--   顺序按"最小的表先来"：logs → transactions → token_transfers → address_activity。
--
-- 实测规模（清退前，2026-09-23）：
--   logs              11,616,178 / 12,411,229
--   transactions      10,212,300 / 32,715,702（含 bench 600,000）
--   token_transfers   25,746,206 / 92,304,565
--   address_activity  17,852,924 / 108,139,836
--   internal_calls    0（那 4 条短 hash 是合法的 Solana 87 字签名，不动）
--   合计清退 65,427,608 行。

-- ---------- 清退前计数（逐表执行，留证据） ----------
SELECT 'logs' AS t, count() AS before FROM blockchain.logs;
SELECT 'transactions', count() FROM blockchain.transactions;
SELECT 'token_transfers', count() FROM blockchain.token_transfers;
SELECT 'address_activity', count() FROM blockchain.address_activity;

-- ---------- 清退（逐条执行，每条执行后再跑一次计数） ----------
DELETE FROM blockchain.logs
WHERE length(tx_hash) NOT IN (64, 66, 87, 88);

DELETE FROM blockchain.transactions
WHERE length(tx_hash) NOT IN (64, 66, 87, 88);

DELETE FROM blockchain.token_transfers
WHERE length(tx_hash) NOT IN (64, 66, 87, 88);

DELETE FROM blockchain.address_activity
WHERE length(tx_hash) NOT IN (64, 66, 87, 88);

-- ---------- 清退后校验 ----------
-- ① 剩余异常行必须为 0
SELECT 'logs' AS t, count() FROM blockchain.logs WHERE length(tx_hash) NOT IN (64, 66, 87, 88)
UNION ALL SELECT 'transactions', count() FROM blockchain.transactions WHERE length(tx_hash) NOT IN (64, 66, 87, 88)
UNION ALL SELECT 'token_transfers', count() FROM blockchain.token_transfers WHERE length(tx_hash) NOT IN (64, 66, 87, 88)
UNION ALL SELECT 'address_activity', count() FROM blockchain.address_activity WHERE length(tx_hash) NOT IN (64, 66, 87, 88);

-- ② bench 链、MCK 符号必须为 0
SELECT count() FROM blockchain.transactions WHERE chain = 'bench';
SELECT count() FROM blockchain.token_transfers WHERE token_symbol = 'MCK';

-- ③ 真实数据规模（对账用；清退前 token_transfers 92,304,565 / address_activity 108,139,836）
SELECT count() FROM blockchain.token_transfers;
SELECT count() FROM blockchain.address_activity;

-- ④ 真实行抽查：随机取 5 个近期真实地址，确认页面能正常出数
SELECT address, count() AS rows FROM blockchain.address_activity
WHERE length(tx_hash) IN (64, 66, 87, 88) AND timestamp >= now() - INTERVAL 7 DAY
GROUP BY address ORDER BY rows DESC LIMIT 5;

-- ---------- 空间回收（可选，低峰执行） ----------
-- 轻量 DELETE 只打标记，磁盘要等后台 merge 才会释放；要立即回收再跑：
-- OPTIMIZE TABLE blockchain.logs FINAL;
-- OPTIMIZE TABLE blockchain.transactions FINAL;
-- OPTIMIZE TABLE blockchain.token_transfers FINAL;
-- OPTIMIZE TABLE blockchain.address_activity FINAL;
-- 验证通过、确认不再需要回滚后，才删除 FREEZE 快照：
--   rm -rf /data/blockchain-platform/clickhouse/shadow/mck-purge-20260922
