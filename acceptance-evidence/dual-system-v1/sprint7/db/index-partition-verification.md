# S7-DB-01 核对报告（二）：查询索引、分区与归档

> 对应计划 §22.1 中「0048 查询索引、分区和归档任务」的语义。
> 实际迁移编号：**0057_s7_indexes_partitions.sql**。

## 1. 缺失索引：定位方法

先用 `pg_stat_user_tables` 找真实的全表扫描热点（不是猜），再逐条对照代码里的查询谓词：

```text
relname          seq_scan    idx_scan    n_live_tup
download_pages   4,438,266   1,721,450      165,469
checkpoints            876     393,524       19,962
login_events           598         136        2,241
audit_events_v2      1,062       1,075        1,046
sessions               793      16,124        1,197
```

## 2. 新增索引（每条都有代码位置）

| 索引 | 定义 | 依据 |
|---|---|---|
| `idx_download_pages_job_status` | `download_pages (job_id, status)` | `engine.go:387` `WHERE job_id=$1 AND status IN ('INGESTED','VALIDATED')` |
| `idx_checkpoints_job` | `checkpoints (job_id)` | `sprint5_jobs.go:127/194`（PK 只有 `shard_id`） |
| `idx_exports_job_org_created` | `exports (job_id, organization_id, created_at DESC)` | `sprint5_jobs.go:273` |
| `idx_audit_v2_seq_desc` | `audit_events_v2 (seq DESC)` | `adminv1.go:609-616` `ORDER BY seq DESC LIMIT/OFFSET` |
| `idx_audit_v2_result_seq` | `audit_events_v2 (result, seq DESC)` | `adminv1.go:583-585` |
| `idx_login_events_seq_desc` | `login_events (seq DESC)` | `loginevent.go:296-305` |
| `idx_login_events_org_seq` | `login_events (organization_id, seq DESC)` | `loginevent.go:261-263` |
| `idx_login_events_type_seq` | `login_events (event_type, seq DESC)` | `loginevent.go:269-271` |
| `idx_investigations_case_status` | `investigations (case_id, status)` | `cases.go:371` |
| `idx_sessions_org_last_seen` | `sessions (organization_id, last_seen_at DESC)` | `session.go:576-578` |

## 3. 真实数据 A/B 实测（不是估算）

方法：把迁移前的生产备份 `pre-s7-pg-20260912T162853Z.dump` 还原为独立库
`blockchain_platform_dr_restore`（**与生产库隔离**），在**真实生产数据**上先跑 10 条代表性查询，
再执行本迁移的 10 条 `CREATE INDEX` + `ANALYZE`，用同样的 SQL 复跑。
原始计划见 `before.txt` / `after.txt`，差见 `diff.txt`。

| 查询 | BEFORE (ms) | AFTER (ms) | 倍数 |
|---|---:|---:|---:|
| Q1 download_pages 按 job 汇总 rows_inserted | 120.801 | **0.205** | 589× |
| Q2 checkpoints 按 job 取 max(last_success_at) | 15.948 | **0.426** | 37× |
| Q3 exports 按 job+org 列表 | 11.885 | **0.245** | 49× |
| Q4 audit_events_v2 ORDER BY seq DESC LIMIT 50 | 43.086 | **0.117** | 368× |
| Q5 audit_events_v2 result='allow' + seq DESC | 0.565 | 0.061 | 9× |
| Q6 login_events ORDER BY seq DESC LIMIT 50 | 5.979 | 0.122 | 49× |
| Q7 login_events 按组织 + seq DESC | 0.845 | 0.278 | 3× |
| Q8 login_events event_type + seq DESC | 0.819 | 0.109 | 8× |
| Q9 investigations 按 case + 状态计数 | 3.798 | 0.079 | 48× |
| Q10 sessions 按组织 + last_seen DESC | 14.182 | 0.950 | 15× |

**10/10 全部下降**，无一条「无增益也算成功」。索引总数 345 → 377。

> 说明：A/B 在还原库上进行，避免对生产写入路径造成扰动；生产库已应用同一组索引 DDL，
> 结构一致性由 §5 的索引清单核对。

## 4. 分区改造：user_activity_events

§22.4 明确要求该表「按日或月分区，90 天自动归档/删除」。改造方式（**不复制、不删除、不改写任何业务行**）：

```text
1) ALTER TABLE user_activity_events RENAME TO user_activity_events_hist   -- 旧表原样保留（ACL 一并保留）
2) 移除旧表 PK(id) 与两个二级索引（ATTACH 会按父表索引重建等价索引；旧 PK 与父 PK(id, occurred_at) 冲突 42P16）
3) 历史边界按真实数据推导：hist_upper = date_trunc('month', max(occurred_at)) + 1 month（空表取当月起点）
4) CREATE TABLE user_activity_events (LIKE ..._hist INCLUDING ...) PARTITION BY RANGE (occurred_at); PK(id, occurred_at)
5) ATTACH PARTITION user_activity_events_hist FOR VALUES FROM (MINVALUE) TO (hist_upper)
6) 建 DEFAULT 分区兜底（漏建分区时写入不失败、不静默丢弃）
7) 父表建 identity/time/org 三个索引并由 PostgreSQL 下发到各分区
8) 重新授予 blockchain_app 的 SELECT/INSERT，并**重新收紧** UPDATE/DELETE/TRUNCATE（0054 语义不因重建丢失）
```

实测分区清单（`select * from s7_partition_status()`）：

```text
user_activity_events | user_activity_events_hist     | FROM (MINVALUE) TO ('2026-09-01')  | 0 rows
user_activity_events | user_activity_events_202609   | FROM ('2026-09-01') TO ('2026-10-01') | 0 rows
user_activity_events | user_activity_events_202610   | FROM ('2026-10-01') TO ('2026-11-01') | 0 rows
user_activity_events | user_activity_events_202611   | FROM ('2026-11-01') TO ('2026-12-01') | 0 rows
user_activity_events | user_activity_events_default  | DEFAULT                            | 0 rows
```

配套函数与轨迹：

| 对象 | 作用 |
|---|---|
| `s7_ensure_user_activity_partitions(months_ahead, start)` | 幂等创建月度分区（含授权）；运维/定时任务调用 |
| `s7_partition_status()` | 只读分区清单（父表、边界、行数、大小） |
| `s7_partition_archive_candidates(retention_days)` | 只读返回「整体超出保留期」的分区，供「导出→双人审批→DETACH」流程取数 |
| `partition_maintenance_log` | 分区创建/候选计算的审计轨迹 |

**实际删除仍然关闭**：本迁移不包含任何 DETACH/DROP；删除必须走 S6 的保留策略审批链
（`deletion_requests`，执行默认 412）。

## 5. login_events / audit_events_v2：分区就绪门禁（明确的「不做+原因」）

`select * from s7_partition_readiness()` → 两条链均 `ready=false`，阻塞点写进数据库而不是文档：

```text
login_events    : UNIQUE(chain,seq) 需要全局唯一（PostgreSQL 分区表不支持全局唯一索引）
                | organization_id 参与哈希链规范化，重写数据会破坏 AUDIT-004
                | append-only 行触发器 audit_append_only_guard 属 S6 完整性边界（0054/0055）
audit_events_v2 : UNIQUE(chain,seq) 需要全局唯一
                | organization_id 参与哈希链规范化
                | 含 ADR-018 不可恢复缺口的封存边界行，重写会造成 boundary_conflict
```

即：**为了保住 Sprint 6 刚验收通过的不可变完整性边界，这两张表本轮不分区**。
这不是「没做」，而是有实测阻塞点、并已固化为机器可读门禁的工程决定；
放行条件明确：只有 `(chain,seq)` 全局唯一不再由单表唯一索引承担（例如改用独立链序号表）时才可重评。

## 6. ClickHouse 侧核对

```text
表                  engine              partition_key                    rows         size
address_activity    ReplacingMergeTree  (chain, toYYYYMM(timestamp))     74,448,981   5.43 GiB
token_transfers     ReplacingMergeTree  (chain, toYYYYMM(timestamp))     56,600,060   4.51 GiB
transactions        ReplacingMergeTree  (chain, toYYYYMM(timestamp))     35,986,150   5.63 GiB
logs                ReplacingMergeTree  (chain, toYYYYMM(timestamp))     12,345,849   481 MiB
```

- 分区键已按 `(chain, 月)` 建立，无需改造；`system.parts` 显示各表活跃分区 1–411 个，无碎片堆积异常。
- 跳数索引（bloom_filter）已覆盖 `transactions.from/to/method_id`、`token_transfers.from/to`、
  `internal_calls.from/to`；`logs` 与 `address_activity` 的地址列**没有**跳数索引，
  是否追加由 S7-PERF-01 用真实查询的 `EXPLAIN`/`query_log` 实测后再决定（避免无依据加索引）。

## 7. 回滚

`backend/migrations/rollback/rollback-0057.sql`：删除 10 个索引与 4 个函数。
分区结构**不自动回滚**（DETACH + RENAME 需人工确认数据归属），见 `docs/manuals/rollback.md`。
