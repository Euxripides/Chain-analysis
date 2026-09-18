# 容量预测（Capacity Forecast）— 区块链取证平台

> **方法可复现**：本文所有数字均来自部署机上的**只读**实测（SQL / `system.part_log` / S3 ListObjects / `df`），每条结论都附实际执行的命令与真实输出。
> 采集时间（数据库时钟，UTC）：**2026-09-12 21:49:04 UTC**（部分查询延至 21:56 UTC）。窗口口径中的「今天」= 2026-09-12。
> 配套文档：`docs/data-dictionary.md`（表结构、保留期、敏感级）。

## 修订说明（Revision 2，独立复核后修正）

本文件为 **Revision 1**（首版，sha256 `31ef37e1af683120ce73a86280a836eb12bbdaef4851130d3dccd79ec80850fe`，含后续 3 处小修版 `7cad0b280a30cd92bc7f6df2c1c763aeaa0108d949d460ff5b9ccafd3f4d6347`）的修订版。独立复核提出 6 项口径问题，逐条修正如下；**原始采样与附录全部保留**，仅在被指出的位置修正并补充新证据。

| # | 复核意见 | 修正位置 | 修正内容 |
|---:|---|---|---|
| 1 | CH `part_log` NewPart 是 **gross 写入量**，不是磁盘净增，**也不是净增长下界**；不得直接算 daysToExhaust 或称为"实测预测" | §0.2、§0.3、§2.1、§2.5 | 采用复核意见的 **(a)+(b) 双轨**：`NewPart` 明确标注为 **gross 模型**并给出显式假设；同时**补齐真实净增证据**——两个时点 `system.parts` 活跃 part 快照 + 全生命周期净增 + `part_log` 的 Merge/RemovePart 事件量。**取消**由 CH 单独推导的确定耗尽日，改为**区间**并标注「净增长无保证、可能更小」 |
| 2 | PG 13.09× 含本次 PITR 失败演练的多个 base/clone 目录，属一次性占用，不能外推；未知量须写 NOT VERIFIED | §0.3、§4.2、§4.7（新） | 删除 13.09× 表述；改为**逐目录实测清单**（8 个 `pitr-*` 目录），并把「备份频率 × 每份 base 体积 + WAL/day × 保留窗口 × 副本数/恢复空间」**单列为敏感性分析**，未知量全部标 **NOT VERIFIED** |
| 3 | MinIO `LastModified` 只覆盖**当前存活对象**，不含已删除与旧版本 | §0.2、§3.1、§3.2 | 标注为 **survivor / current-version 口径**，明确**不是全部历史净增** |
| 4 | "CH 无 TTL" 必须用实际部署 DDL 核实 | §2.6（新） | 补 `select create_table_query from system.tables` 实测，**完整 DDL 片段贴入**；migration grep 降级为佐证 |
| 5 | `stats_reset=NULL` 不证明从未重置 | §1.1 | 改为「当前未记录 / 不可判断」 |
| 6 | 「保留期」未定义部分不算治理已落地 | §0.4（新） | 新增「文档交付 vs 治理能力 NV」分离陈述（与 `data-dictionary.md` §2.2 一致） |

**修订引入的新实测证据（UTC 2026-09-12 22:04–22:13）**：

```
T_A  2026-09-12 22:10:10   system.parts active: 17,204,614,409 B / 178,897,405 rows / 982 parts
T_B  2026-09-12 22:12:04   system.parts active: 17,204,614,409 B / 178,897,405 rows / 982 parts   → 净增 0

df -B1 /data  @21:49:04   Used  64,576,253,952   Avail  991,482,462,208
df -B1 /data  @22:12:25   Used  86,406,115,328   Avail  969,652,600,832   ← 23 分钟内 +20.33 GiB（PITR 演练）

du -sb（容器 root）/var/lib/postgresql @22:12:25 = 28,497,407,919 B (26.54 GiB)
      ├ 18/docker（在线 PGDATA）            3,113,687,297 B (2.90 GiB)
      └ wal-archive                          1,275,069,780 B (1.19 GiB)，79 段
du -sb /data/blockchain-platform/clickhouse @22:12:25 = 40,730,444,188 B (37.93 GiB)
      ├ store/                               23,516,769,451 B (21.90 GiB)  ← 含全部 part
      └ backups/20260912T171120Z/            17,215,465,503 B (16.03 GiB)  ← **一次性 CH 备份副本**
du -sb /data/blockchain-platform/minio      @22:12:25 = 15,137,696,227 B (14.10 GiB)
```

> **复核意见 1、2、3 全部成立**，相关结论已在下方逐节改写。**最重要的新发现**：当前 `/data` 的增长**主要由 PITR/RPO 演练驱动**（23 分钟 +20.33 GiB），而**不是**由业务写入驱动——因此 Revision 1 的耗尽日预测**既不成立也不应给出确定值**。

---

## 0. 结论摘要（先看这里）

### 0.1 PostgreSQL 日增（逻辑库口径）

| 口径 | 窗口 | rows/day（组合） | bytes/day | 依据 |
|---|---|---:|---:|---|
| **L 稳健中位** | 全历史 13 天（08-31..09-12）逐日取中位 | 53,731 | **32.16 MiB/d** | §1.3 组合级逐日实测 |
| **M 完整日均值** | 12 个完整日（08-31..09-11）均值 | 88,513 | **55.73 MiB/d** | §1.3 |
| **H 近期均值** | 最近 7 个完整日（09-05..09-11）均值 | 127,455 | **81.63 MiB/d** | §1.3 |
| P 峰值日 | 全历史单日最大（2026-09-08） | 275,907 | **189.93 MiB/d** | §1.3 |
| T 当日折算 | 2026-09-12（不完整日，已过 21.817 h；实测 120,507 行 / 155.50 MiB，÷0.9090 折全日） | 132,571 | **171.07 MiB/d** | §1.3 |

> 当前 PostgreSQL 逻辑库大小 **861 MB（902,608,575 B）**，149 张表 `pg_total_relation_size` 之和 **847.28 MiB**。

### 0.2 ClickHouse / MinIO 日增（Revision 2 修正口径）

**ClickHouse 有两个互不等价的口径，必须分开读（复核意见 1）**：

| 口径 | 定义 | bytes/day | 性质与限制 |
|---|---|---:|---|
| **G — gross 写入量** | `system.part_log` 中 `event_type='NewPart'` 的 `sum(size_in_bytes)` | **1.61 GiB/d**（13 天均值）；1.74 GiB/d（12 完整日均值）；2.28 GiB/d（近 7 完整日均值） | **毛写入**：不含 merge 去重、不含 `RemovePart`/`MutatePart`。**不是磁盘净增，也不是净增长下界**（净增可能更小，甚至为负）。**不得**据此推导确定耗尽日 |
| **N1 — 全生命周期净增** | 当前活跃 part 字节 ÷ 观测天数 | **1.29 GiB/d**（17,204,614,409 B ÷ 12.43 d = 1,384,112,341 B/d） | 真实净增，但依赖「存储起始为空」假设 |
| **N2 — 两点实测净增** | 两个时点 `system.parts` 活跃 part 快照之差 | **0 B / 20.0 min**（21:5x → 22:12:04） | 真实净增，但窗口短且处于空闲期，**不可外推日均** |

> **净增长无保证**：`ReplacingMergeTree` 的后台 merge 会**去重**，`RemovePart`/`MutatePart` 会**释放**空间，因此任何时刻的净增都可能**小于** gross 值，极端情况下**为负**。本文**不再给出由 ClickHouse 单独推导的确定耗尽日**，只给区间。详见 §2.1、§2.5。

**MinIO 口径（复核意见 3）**：

| 存储 | 口径 | bytes/day | 依据 |
|---|---|---:|---|
| MinIO `blockchain-raw` | **survivor / current-version 口径**：`ListObjectsV2` 只枚举**当前存活对象**，**不含已删除对象、不含历史版本**；`LastModified` 为最后修改时间 | **1.08 GiB/d**（13 天）；1.17 GiB/d（12 完整日）；1.48 GiB/d（近 7 日） | §3 |

> 该口径**不是全部历史净增**——被删除的对象完全不在统计内，被覆盖写的对象只计入最后一次修改日。误差方向不可量化（§7）。

### 0.3 磁盘水位与预计耗尽（Revision 2 重写）

**最新实测水位（UTC 2026-09-12 22:12:25，`df -B1 /data`）**：

| 项 | 值 |
|---|---|
| `/data` 总容量 | 1,105,357,205,504 B（1.01 TiB） |
| `/data` 已用 | **86,406,115,328 B（80.47 GiB）** |
| `/data` 可用 | **969,652,600,832 B（903.05 GiB）** |

**组件分解（同一时点 `du -sb`，PostgreSQL 以容器 root 读取）**：

| 组件 | 字节 | 说明 |
|---|---:|---|
| `clickhouse/` | 40,730,444,188（37.93 GiB） | `store/` 21.90 GiB（全部 part；与 `system.parts` 活跃 17.20 GB 之差为非活跃 part）+ **`backups/20260912T171120Z/` 16.03 GiB（一次性 ClickHouse 备份副本）** + 其他 <1 MiB |
| 容器 `/var/lib/postgresql` | 28,497,407,919（26.54 GiB） | 在线 PGDATA 2.90 GiB + **8 个 `pitr-*` 演练目录 ≈22.4 GiB** + `wal-archive` 1.19 GiB |
| `minio/` | 15,137,696,227（14.10 GiB） | S3 对象合计 14.03 GiB |
| `nats/` | **不可得** | `Permission denied`（未以容器 root 读取） |

> **关键事实（Revision 1 完全遗漏）**：`/data` 已用在 **23 分钟内从 60.14 GiB 涨到 80.47 GiB（+20.33 GiB）**——这**不是业务增长**，而是 **PITR/RPO 演练**在写 base 备份：
>
> ```
> df -B1 /data @21:49:04  Used 64,576,253,952  Avail 991,482,462,208
> df -B1 /data @22:12:25  Used 86,406,115,328  Avail 969,652,600,832
> ```
>
> 因此 **Revision 1 的 339 / 311 / 240 天耗尽预测作废**。本文不再给出单一确定耗尽日，改为「仅稳态业务写入」的情景区间 + 显式前提。

**仅计稳态业务写入（剔除演练与 CH 备份副本）**：

| 情景 | PG（逻辑库） | ClickHouse（取 N1 净增） | MinIO（survivor 口径） | 合计日增 | 以 903.05 GiB 可用计 |
|---|---:|---:|---:|---:|---:|
| L 稳健中位 | 32.16 MiB（0.0314 GiB） | 1.29 GiB | 1.08 GiB | **2.40 GiB/d** | ≈ **376 天** |
| M 完整日均值 | 55.73 MiB（0.0544 GiB） | 1.29 GiB | 1.17 GiB | **2.51 GiB/d** | ≈ **359 天** |
| H 近期均值 | 81.63 MiB（0.0797 GiB） | 1.29 GiB | 1.48 GiB | **2.85 GiB/d** | ≈ **316 天** |

> **上表的三个前提**（任一不成立则数字失效）：
> 1. **无 PITR/备份演练、无 ClickHouse 备份副本新增**（当前正在发生，见上）；
> 2. ClickHouse 净增取 N1（全生命周期均值），**实际净增无保证**，可能更小甚至为负（§0.2）；
> 3. **WAL 归档未计入**。若 `archive_mode=on` + `archive_timeout=60` 长期生效且归档目录无清理，须另加 **≈21.0 – 27.0 GiB/日**（§4.6 实测），届时耗尽期缩短到 **约 33 天**（§4.8 情景 B）。
>
> **结论**：在**当前演练配置下**，「真实耗尽日」**无法给出确定答案**。本文只给区间、前提与 NOT VERIFIED 清单。

### 0.4 文档交付 vs 治理能力（Revision 2 新增，复核意见 6）

| 维度 | 状态 | 证据 |
|---|---|---|
| **文档交付**：本文件与 `data-dictionary.md` 给出的保留期数字 | 已交付（23/149 张表有定义，126 张「未定义」） | `data-dictionary.md` §2.2.1 |
| **治理能力**：保留策略是否真正生效 | **NOT VERIFIED / 未生效** | `retention_policies` 6 条 `enabled` **全为 false**；`governance.retention.scheduler_enabled=false`；`governance.deletion.execution_enabled=false`；MinIO 6 桶无 lifecycle（404）；ClickHouse 9 表无 TTL（在线 DDL 实测）；`data_asset_registry` 仅 **17/149 = 11.4%** 覆盖 |

> **「保留期未定义」是治理缺口，不是本数据字典的遗漏**；反过来，本文件写出的保留期数字**也不代表已落地执行**。因此本文全部容量预测均按「**无界增长**」口径给出。详见 `data-dictionary.md` §2.2.2。

---

## 1. PostgreSQL 日增实测

### 1.1 口径与可复现性声明

- **行的归属口径**：以表内**时间列**为准（insert 时间或业务时间），不是以物理写入时间为准。每张表使用的时间列已在 §1.2 明确列出。
- **字节口径**：PostgreSQL **不保留历史体积快照**，无法直接测「昨日 `pg_total_relation_size`」。因此日增字节采用**推导口径**：
  ```
  bytes/day = (pg_total_relation_size(表) / count(*)(表)) × rows/day(表)
  ```
  即用「当前平均行宽（含堆+TOAST+索引）」乘以「实测日增行数」。该口径已包含索引开销，但**不含**页填充空洞、死元组与膨胀（bloat）引起的额外增长，因此**系统性低估**。
- **`stats_reset` 为 NULL（Revision 2 修正措辞）**：`SELECT stats_reset FROM pg_stat_database WHERE datname='blockchain_platform'` 返回空值。这**只表示当前未记录该时间戳 / 无法据其判断计数器窗口起点**，**不能**据此断言「统计计数器从未被重置」——NULL 也可能由统计系统初始化方式、版本差异或写入缺失造成，本次**未进一步验证成因**（标 **NOT VERIFIED**）。因此本文件**不使用** `pg_stat_user_tables.n_tup_ins` 推算速率。

### 1.2 逐表实测（按时间列分日 `count(*)`）

使用的 SQL 模板（对每张表各生成一个 `UNION ALL` 分支）：

```sql
SELECT '<表名>' AS tbl, '<时间列>' AS col,
       to_char(date_trunc('day', <时间列>), 'YYYY-MM-DD') AS d, count(*)::text AS c
  FROM public.<表名> WHERE <时间列> IS NOT NULL GROUP BY 3;
```

窗口汇总 SQL（权威口径）：

```sql
SELECT count(*) FILTER (WHERE <时间列> >= '2026-09-05' AND <时间列> < '2026-09-12') AS n_7d,
       count(DISTINCT date_trunc('day',<时间列>))
         FILTER (WHERE <时间列> >= '2026-09-05' AND <时间列> < '2026-09-12') AS d_7,
       count(*) FILTER (WHERE <时间列> >= '2026-08-31' AND <时间列> < '2026-09-13') AS n_13d,
       min(<时间列>), max(<时间列>) FROM public.<表名>;
```

**权威窗口汇总实测输出**（节选，完整见附录 A）：

```
provider_usage	request_time	416066	2026-08-31 11:25:35+00	2026-09-12 21:14:27+00	13	368159	7	416066
rpc_provider_health	captured_at	102598	2026-09-02 03:40:08+00	2026-09-12 21:56:13+00	11	69321	7	102598
download_pages	created_at	165502	2026-09-03 11:10:24+00	2026-09-12 20:03:32+00	10	159096	7	165502
download_shards	created_at	28385	2026-08-31 11:25:34+00	2026-09-12 20:03:13+00	12	9677	7	28385
audit_events_v2	occurred_at	48587	2026-09-12 14:42:40+00	2026-09-12 21:50:49+00	1	0	0	48587
job_event_log	created_at	42475	2026-09-12 10:10:09+00	2026-09-12 21:56:16+00	1	0	0	42475
token_metadata_observations	created_at	233792	2026-09-01 16:03:00+00	2026-09-07 17:17:29+00	6	216150	2	233792
```

> **关键观察（决定后续外推方式）**：
> - `audit_events_v2`、`job_event_log`、`investigation_evidence`、`case_activity` 的**全部数据都产生于 2026-09-12**（`days_all = 1`），因此它们在「最近 7 个完整日」窗口内的计数为 **0**；直接按 13 天平均会把它们的速率稀释 **13 倍**。
> - `token_metadata_observations.created_at` 虽然 `days_all = 6`，但其中 **216,147 行（占 92.5%）集中在 2026-09-05 一天**，是一次性批量回填，**不是稳态速率**。

### 1.3 组合级（全部有时间列的表）逐日实测

对 §1.2 每张表的逐日 `count(*)` 乘以该表平均行宽后求和，得到「整个 PostgreSQL 逻辑库当日新增字节」：

| 日期（UTC） | 新增行数 | 折算新增字节 | 折算新增 | 备注 |
|---|---:|---:|---:|---|
| 2026-08-31 | 68,743 | 44,364,958 | 42.31 MiB |  |
| 2026-09-01 | 14,514 | 8,366,316 | 7.98 MiB |  |
| 2026-09-02 | 7,863 | 3,176,909 | 3.03 MiB |  |
| 2026-09-03 | 25,118 | 12,575,928 | 11.99 MiB |  |
| 2026-09-04 | 53,731 | 33,572,293 | 32.02 MiB |  |
| 2026-09-05 | 270,499 | 173,202,358 | 165.18 MiB | token_metadata_observations 批量回填 216,147 行 |
| 2026-09-06 | 63,777 | 43,202,265 | 41.20 MiB |  |
| 2026-09-07 | 52,029 | 33,719,877 | 32.16 MiB |  |
| 2026-09-08 | 275,907 | 199,157,470 | 189.93 MiB | provider_usage 单日 191,603 行 |
| 2026-09-09 | 22,063 | 13,188,384 | 12.58 MiB |  |
| 2026-09-10 | 196,781 | 132,943,142 | 126.78 MiB | download_pages 59,813 行 + download_volume_guard_commits 22,699 行 |
| 2026-09-11 | 11,130 | 3,769,554 | 3.59 MiB |  |
| 2026-09-12 | 120,507 | 163,053,472 | 155.50 MiB | **不完整日**（截至 21:49 UTC） |

**统计量**：

| 统计口径 | rows/day | bytes/day |
|---|---:|---:|
| 全历史 13 天均值 | 90,974 | 63.40 MiB |
| 全历史 13 天**中位数** | 53,731 | **32.16 MiB** |
| 12 个完整日均值（08-31..09-11） | 88,513 | **55.73 MiB** |
| 最近 7 个完整日均值（09-05..09-11） | 127,455 | **81.63 MiB** |
| 单日峰值（2026-09-08） | 275,907 | 189.93 MiB |

**逐日明细 SQL（可复现）**：

```sql
-- 步骤 1：每张表按天计数
SELECT '<表>' AS tbl, '<时间列>' AS col, to_char(date_trunc('day', <时间列>), 'YYYY-MM-DD') AS d, count(*)
  FROM public.<表> WHERE <时间列> IS NOT NULL GROUP BY 3;

-- 步骤 2：每张表的平均行宽（含索引）
SELECT c.relname, pg_total_relation_size(c.oid) AS total_bytes,
       (xpath('/row/c/text()', query_to_xml(format('SELECT count(*) AS c FROM public.%I', c.relname), false, true, '')))[1]::text::bigint AS exact_rows,
       pg_total_relation_size(c.oid)::numeric
         / NULLIF((xpath('/row/c/text()', query_to_xml(format('SELECT count(*) AS c FROM public.%I', c.relname), false, true, '')))[1]::text::bigint, 0) AS bytes_per_row
  FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname='public' AND c.relkind IN ('r','p');

-- 步骤 3：组合级 = Σ(当日行数 × 平均行宽)，按 d 分组求和
```

### 1.4 逐表明细（含样本充分性判定）

**判定规则**：
- **充分**：活跃天数 ≥ 7，且不存在单日占比 > 50% 的批量回填 → 直接采用实测速率。
- **样本不足**：活跃天数 < 7，或存在单日批量回填 → **必须**按下方「误差方向/上下界」解读。

| 表.时间列 | 活跃天数 | 日均行数 | 中位日行数 | 峰值日行数 | 字节/行 | bytes/day（均值） | bytes/day（中位） | bytes/day（峰值） | 样本判定 |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---|
| `audit_events_v2`.`occurred_at` | 1 | 48,586 | 48,586 | 48,586 | 1,417 | 65.66 MiB | 65.66 MiB | 65.66 MiB | **样本不足**：仅 1 天数据 |
| `job_event_log`.`created_at` | 1 | 42,036 | 42,036 | 42,036 | 1,556 | 62.37 MiB | 62.37 MiB | 62.37 MiB | **样本不足**：仅 1 天数据 |
| `token_metadata_observations`.`created_at` | 6 | 38,965 | 546 | 216,147 | 671 | 24.94 MiB | 0.35 MiB | 138.36 MiB | **样本不足**：仅 6 天数据 |
| `provider_usage`.`request_time` | 13 | 32,005 | 8,009 | 191,603 | 740 | 22.58 MiB | 5.65 MiB | 135.15 MiB | **充分**（≥7 天且无单日回填） |
| `token_metadata_observations`.`observed_at` | 8 | 28,870 | 41 | 216,147 | 671 | 18.48 MiB | 0.03 MiB | 138.36 MiB | **样本不足**：单日批量回填占比 >50% |
| `download_pages`.`created_at` | 10 | 16,550 | 2,314 | 59,858 | 771 | 12.17 MiB | 1.70 MiB | 44.00 MiB | **充分**（≥7 天且无单日回填） |
| `investigation_evidence`.`collected_at` | 1 | 7,127 | 7,127 | 7,127 | 1,087 | 7.39 MiB | 7.39 MiB | 7.39 MiB | **样本不足**：仅 1 天数据 |
| `investigation_steps`.`created_at` | 2 | 174 | 76 | 271 | 18,391 | 3.04 MiB | 1.33 MiB | 4.75 MiB | **样本不足**：仅 2 天数据 |
| `rpc_provider_health`.`captured_at` | 11 | 9,322 | 9,807 | 10,073 | 292 | 2.59 MiB | 2.73 MiB | 2.80 MiB | **充分**（≥7 天且无单日回填） |
| `download_shards`.`created_at` | 12 | 2,365 | 406 | 9,563 | 958 | 2.16 MiB | 0.37 MiB | 8.74 MiB | **充分**（≥7 天且无单日回填） |
| `login_events`.`occurred_at` | 2 | 1,638 | 109 | 3,166 | 1,226 | 1.91 MiB | 0.13 MiB | 3.70 MiB | **样本不足**：仅 2 天数据 |
| `download_volume_guard_commits`.`committed_at` | 4 | 5,744 | 107 | 22,699 | 334 | 1.83 MiB | 0.03 MiB | 7.23 MiB | **样本不足**：仅 4 天数据 |
| `token_contract_catalog`.`created_at` | 4 | 4,277 | 1,636 | 12,602 | 379 | 1.55 MiB | 0.59 MiB | 4.56 MiB | **样本不足**：仅 4 天数据 |
| `sessions`.`created_at` | 2 | 879 | 115 | 1,642 | 1,240 | 1.04 MiB | 0.14 MiB | 1.94 MiB | **样本不足**：仅 2 天数据 |
| `token_metadata`.`created_at` | 9 | 1,955 | 32 | 12,602 | 484 | 0.90 MiB | 0.01 MiB | 5.82 MiB | **样本不足**：单日批量回填占比 >50% |
| `ankr_benchmark_runs`.`created_at` | 3 | 646 | 172 | 1,754 | 1,175 | 0.72 MiB | 0.19 MiB | 1.97 MiB | **样本不足**：仅 3 天数据 |
| `investigation_findings`.`created_at` | 2 | 172 | 52 | 292 | 3,691 | 0.61 MiB | 0.18 MiB | 1.03 MiB | **样本不足**：仅 2 天数据 |
| `dataset_coverage`.`created_at` | 12 | 677 | 30 | 5,929 | 832 | 0.54 MiB | 0.02 MiB | 4.70 MiB | **样本不足**：单日批量回填占比 >50% |
| `checkpoints`.`updated_at` | 12 | 1,665 | 381 | 6,080 | 283 | 0.45 MiB | 0.10 MiB | 1.64 MiB | **充分**（≥7 天且无单日回填） |
| `download_datasets`.`created_at` | 12 | 1,250 | 172 | 11,221 | 363 | 0.43 MiB | 0.06 MiB | 3.88 MiB | **样本不足**：单日批量回填占比 >50% |
| `download_jobs`.`created_at` | 12 | 202 | 46 | 1,756 | 2,089 | 0.40 MiB | 0.09 MiB | 3.50 MiB | **样本不足**：单日批量回填占比 >50% |
| `case_activity`.`created_at` | 1 | 524 | 524 | 524 | 766 | 0.38 MiB | 0.38 MiB | 0.38 MiB | **样本不足**：仅 1 天数据 |
| `ankr_shard_profiles`.`created_at` | 10 | 1,187 | 310 | 5,448 | 332 | 0.38 MiB | 0.10 MiB | 1.72 MiB | **充分**（≥7 天且无单日回填） |
| `ankr_fetch_concurrency_runs`.`created_at` | 2 | 420 | 89 | 751 | 761 | 0.30 MiB | 0.06 MiB | 0.54 MiB | **样本不足**：仅 2 天数据 |
| `download_subjects`.`created_at` | 12 | 770 | 128 | 5,606 | 360 | 0.26 MiB | 0.04 MiB | 1.92 MiB | **样本不足**：单日批量回填占比 >50% |
| `outbox_events`.`created_at` | 2 | 82 | 57 | 106 | 1,910 | 0.15 MiB | 0.10 MiB | 0.19 MiB | **样本不足**：仅 2 天数据 |
| `audit_events`.`created_at` | 13 | 274 | 109 | 2,144 | 370 | 0.10 MiB | 0.04 MiB | 0.76 MiB | **样本不足**：单日批量回填占比 >50% |
| `session_reuse_events`.`occurred_at` | 1 | 22 | 22 | 22 | 3,724 | 0.08 MiB | 0.08 MiB | 0.08 MiB | **样本不足**：仅 1 天数据 |
| `audit_integrity_incident_events`.`occurred_at` | 1 | 4 | 4 | 4 | 16,384 | 0.06 MiB | 0.06 MiB | 0.06 MiB | **样本不足**：仅 1 天数据 |
| `feature_flag_evaluations`.`evaluated_at` | 1 | 42 | 42 | 42 | 1,560 | 0.06 MiB | 0.06 MiB | 0.06 MiB | **样本不足**：仅 1 天数据 |
| `gpt_action_request_logs`.`created_at` | 4 | 111 | 87 | 236 | 571 | 0.06 MiB | 0.05 MiB | 0.13 MiB | **样本不足**：仅 4 天数据 |
| `download_planning_decisions`.`created_at` | 2 | 48 | 1 | 95 | 1,109 | 0.05 MiB | 0.00 MiB | 0.10 MiB | **样本不足**：仅 2 天数据 |
| `count_oracle_observations`.`created_at` | 2 | 33 | 1 | 65 | 1,489 | 0.05 MiB | 0.00 MiB | 0.09 MiB | **样本不足**：仅 2 天数据 |
| `api_control_actions`.`created_at` | 10 | 78 | 58 | 176 | 549 | 0.04 MiB | 0.03 MiB | 0.09 MiB | **充分**（≥7 天且无单日回填） |
| `count_oracle_calibration_samples`.`created_at` | 2 | 26 | 1 | 51 | 1,260 | 0.03 MiB | 0.00 MiB | 0.06 MiB | **样本不足**：仅 2 天数据 |
| `count_provider_attempts`.`created_at` | 2 | 1 | 1 | 1 | 32,768 | 0.03 MiB | 0.03 MiB | 0.03 MiB | **样本不足**：仅 2 天数据 |
| `count_resolution_decisions`.`created_at` | 2 | 2 | 2 | 2 | 16,384 | 0.03 MiB | 0.03 MiB | 0.03 MiB | **样本不足**：仅 2 天数据 |
| `notification_delivery_attempts`.`created_at` | 1 | 58 | 58 | 58 | 565 | 0.03 MiB | 0.03 MiB | 0.03 MiB | **样本不足**：仅 1 天数据 |

### 1.5 「样本不足」表的显式声明（误差方向与上下界）

以下表的数据**不足以支撑线性外推**，本节按任务要求逐条写明外推方式与误差方向，**不伪装成实测**。

#### (a) 仅 2026-09-12 一天有数据的表（当日已过 21.817 h = 0.9090 天）

| 表.时间列 | 当日实测行数 | 折全日 rows/day | bytes/day | 下界 | 上界 | 误差方向 |
|---|---:|---:|---:|---:|---:|---|
| `audit_events_v2`.`occurred_at` | 48,586 | 53,450 | 72.23 MiB | **0**（若为一次性事件） | **72.23 MiB**（若为全天稳态） | 单向：真实稳态若低于当日强度则**高估**；若当天是不完整/异常日则**低估** |
| `job_event_log`.`created_at` | 42,036 | 46,244 | 68.62 MiB | **0**（若为一次性事件） | **68.62 MiB**（若为全天稳态） | 单向：真实稳态若低于当日强度则**高估**；若当天是不完整/异常日则**低估** |
| `investigation_evidence`.`collected_at` | 7,127 | 7,840 | 8.13 MiB | **0**（若为一次性事件） | **8.13 MiB**（若为全天稳态） | 单向：真实稳态若低于当日强度则**高估**；若当天是不完整/异常日则**低估** |
| `case_activity`.`created_at` | 524 | 576 | 0.42 MiB | **0**（若为一次性事件） | **0.42 MiB**（若为全天稳态） | 单向：真实稳态若低于当日强度则**高估**；若当天是不完整/异常日则**低估** |
| `session_reuse_events`.`occurred_at` | 22 | 24 | 0.09 MiB | **0**（若为一次性事件） | **0.09 MiB**（若为全天稳态） | 单向：真实稳态若低于当日强度则**高估**；若当天是不完整/异常日则**低估** |
| `audit_integrity_incident_events`.`occurred_at` | 4 | 4 | 0.07 MiB | **0**（若为一次性事件） | **0.07 MiB**（若为全天稳态） | 单向：真实稳态若低于当日强度则**高估**；若当天是不完整/异常日则**低估** |
| `feature_flag_evaluations`.`evaluated_at` | 42 | 46 | 0.07 MiB | **0**（若为一次性事件） | **0.07 MiB**（若为全天稳态） | 单向：真实稳态若低于当日强度则**高估**；若当天是不完整/异常日则**低估** |
| `notification_delivery_attempts`.`created_at` | 58 | 64 | 0.03 MiB | **0**（若为一次性事件） | **0.03 MiB**（若为全天稳态） | 单向：真实稳态若低于当日强度则**高估**；若当天是不完整/异常日则**低估** |

**其中两项必须单独说明（数量级最大）**：

**(a-1) `job_event_log`**：`min(created_at) = 2026-09-12 10:10:09 UTC`，`max = 21:56:16 UTC`，跨 **11.77 小时**，共 42,475 行。

```sql
SELECT to_char(date_trunc('hour', created_at),'MM-DD HH24') AS h, count(*)
  FROM job_event_log GROUP BY 1 ORDER BY 1;
-- 09-12 10:4512  11:5673  12:3721  13:3196  14:3178  15:3358
-- 09-12 16:3299  17:3342  18:1748  19:3585  20:3528  21:3304
```

- 小时分布**平稳**（≈3,500 行/h，无单点尖峰），说明是**稳态写入**而非一次性突发。
- 折算 1：按 11.77 小时实际活跃时长除以 0.9090 天 → **46,727 行/日 → 69.34 MiB/d**（假设一天只有 11.77 小时在跑）。
- 折算 2：按 12 小时观测到的 3,500 行/h 线性外推到 24 h → **84,000 行/日 → 124.65 MiB/d**（假设全天持续有任务在跑）。
- **推荐取上界 124.65 MiB/d**（该表由运行中任务的 SSE 事件驱动，属于"任务在跑就有"的稳态），并在 §0.3 情景表中以「补充项」方式单独列出。

**(a-2) `audit_events_v2`**：`min = 2026-09-12 14:42:40 UTC`，`max = 21:50:49 UTC`，跨 **7.14 小时**，共 48,587 行。

```sql
SELECT to_char(date_trunc('hour', occurred_at),'MM-DD HH24') AS h, count(*)
  FROM audit_events_v2 GROUP BY 1 ORDER BY 1;
-- 09-12 14:278  15:688  16:509  17:166  18:46565  19:84  20:212  21:85

SELECT action, count(*) FROM audit_events_v2 GROUP BY 2 ORDER BY 2 DESC LIMIT 4;
-- admin:login-event:read   22174
-- admin:audit:read         17898
-- authorization.denied      6709
-- admin:audit:verify         121
```

- **18:00 单小时 46,565 行占全表 95.8%**，且其中 `admin:login-event:read`(22,174) + `admin:audit:read`(17,898) = 40,072 行属于**一次审计读取操作产生的读取审计记录**（可解释为一次后台读取触发了逐行审计写入）。
- **稳态估计**（剔除 18:00 突发）：其余 7 个小时共 2,477 行 → 354 行/h → **8,493 行/日 → 11.48 MiB/d**。
- **突发上界**：若 18:00 那样的读取批量周期性发生，单次即 **62.93 MiB**；频率未知，**不可外推**。
- **误差方向**：把 48,587 行按 13 天平均（→ 5.05 MiB/d）**严重低估**；按单日折算（→ 72.23 MiB/d）**可能高估**。推荐区间：**11.48 MiB/d（稳态）～ 72.23 MiB/d（突发频发）**。

#### (b) 2–6 天数据、且含单日批量回填的表

| 表.时间列 | 活跃天数 | 各日行数 | 判定 | 处理方式 |
|---|---:|---|---|---|
| `token_metadata_observations`.`created_at` | 6 | 09-01=8771；09-02=41；09-03=8284；09-04=546；09-05=216147；09-07=3 | **单日占 92.5%** | 剔除批量日后按剩余日速率外推（本表记录为 **0.35 MiB/d 中位**，**24.94 MiB/d 均值**） |
| `investigation_steps`.`created_at` | 2 | 09-09=76；09-12=271 | **单日占 78.1%** | 剔除批量日后按剩余日速率外推（本表记录为 **1.33 MiB/d 中位**，**3.04 MiB/d 均值**） |
| `login_events`.`occurred_at` | 2 | 09-11=109；09-12=3166 | **单日占 96.7%** | 剔除批量日后按剩余日速率外推（本表记录为 **0.13 MiB/d 中位**，**1.91 MiB/d 均值**） |
| `download_volume_guard_commits`.`committed_at` | 4 | 09-09=107；09-10=22699；09-11=42；09-12=128 | **单日占 98.8%** | 剔除批量日后按剩余日速率外推（本表记录为 **0.03 MiB/d 中位**，**1.83 MiB/d 均值**） |
| `token_contract_catalog`.`created_at` | 4 | 09-01=2850；09-03=1636；09-04=20；09-05=12602 | **单日占 73.7%** | 剔除批量日后按剩余日速率外推（本表记录为 **0.59 MiB/d 中位**，**1.55 MiB/d 均值**） |
| `sessions`.`created_at` | 2 | 09-11=115；09-12=1642 | **单日占 93.5%** | 剔除批量日后按剩余日速率外推（本表记录为 **0.14 MiB/d 中位**，**1.04 MiB/d 均值**） |
| `ankr_benchmark_runs`.`created_at` | 3 | 09-03=12；09-04=1754；09-05=172 | **单日占 90.5%** | 剔除批量日后按剩余日速率外推（本表记录为 **0.19 MiB/d 中位**，**0.72 MiB/d 均值**） |
| `investigation_findings`.`created_at` | 2 | 09-09=52；09-12=292 | **单日占 84.9%** | 剔除批量日后按剩余日速率外推（本表记录为 **0.18 MiB/d 中位**，**0.61 MiB/d 均值**） |
| `ankr_fetch_concurrency_runs`.`created_at` | 2 | 09-04=751；09-05=89 | **单日占 89.4%** | 剔除批量日后按剩余日速率外推（本表记录为 **0.06 MiB/d 中位**，**0.30 MiB/d 均值**） |
| `outbox_events`.`created_at` | 2 | 09-09=57；09-12=106 | **单日占 65.0%** | 剔除批量日后按剩余日速率外推（本表记录为 **0.10 MiB/d 中位**，**0.15 MiB/d 均值**） |
| `gpt_action_request_logs`.`created_at` | 4 | 09-09=87；09-10=236；09-11=98；09-12=24 | **单日占 53.0%** | 剔除批量日后按剩余日速率外推（本表记录为 **0.05 MiB/d 中位**，**0.06 MiB/d 均值**） |
| `download_planning_decisions`.`created_at` | 2 | 09-03=95；09-06=1 | **单日占 99.0%** | 剔除批量日后按剩余日速率外推（本表记录为 **0.00 MiB/d 中位**，**0.05 MiB/d 均值**） |
| `count_oracle_observations`.`created_at` | 2 | 09-03=65；09-06=1 | **单日占 98.5%** | 剔除批量日后按剩余日速率外推（本表记录为 **0.00 MiB/d 中位**，**0.05 MiB/d 均值**） |
| `count_oracle_calibration_samples`.`created_at` | 2 | 09-03=51；09-06=1 | **单日占 98.1%** | 剔除批量日后按剩余日速率外推（本表记录为 **0.00 MiB/d 中位**，**0.03 MiB/d 均值**） |
| `count_provider_attempts`.`created_at` | 2 | 09-04=1；09-06=1 | 无单日主导 | 按 2 天线性外推，误差方向双向（**0.03 ～ 0.03 MiB/d**） |
| `count_resolution_decisions`.`created_at` | 2 | 09-04=2；09-06=2 | 无单日主导 | 按 2 天线性外推，误差方向双向（**0.03 ～ 0.03 MiB/d**） |

> **`token_metadata_observations` 特别说明**：其 `created_at` 的 6 个活跃日中，**2026-09-05 单日 216,147 行占 92.5%**。若按 6 天平均得 **25.0 MiB/d**（严重高估稳态）；剔除该批后的稳态中位仅 **${MiB(546 * 671)} MiB/d**。因此该表的 30/90/365 天预测采用**中位口径**。

---

## 2. ClickHouse 日增实测（`system.part_log`，真实写入事件）

### 2.1 为什么用 `system.part_log` 而不是 `pg_*` 式推导

ClickHouse 自带 `system.part_log`，逐条记录 part 生命周期事件。其中 `event_type = 'NewPart'` 表示**新 part 被写入**（即 INSERT 落盘），`size_in_bytes` 是该 part 的磁盘字节。

> **⚠️ 口径警告（Revision 2，复核意见 1）**：本节及 §2.2 的 `NewPart` 字节是 **gross（毛）写入量**——
> 1. **不是磁盘净增**：后续 `MergeParts`（合并去重）与 `RemovePart` 会释放空间；
> 2. **也不是净增长的下界**：净增可能比 gross 小得多，甚至在大量去重/删除时**为负**；
> 3. 因此 **禁止** 用 §2.2 的数字直接推导 `daysToExhaust`，也**不得**称之为「实测预测」。
>
> 真实净增证据见 §2.5（两点快照 + 全生命周期净增）。§2.2 的数字保留，但**降级为「写入活动强度指标」**，仅用于说明写入的突发性。

```sql
SELECT event_date, event_type, count() AS n, sum(size_in_bytes) AS bytes
  FROM system.part_log
 WHERE database='blockchain' AND event_date >= '2026-08-25'
 GROUP BY event_date, event_type ORDER BY event_date, event_type;
```

### 2.2 每日新写入字节（`NewPart`）

| 日期（UTC） | NewPart 次数 | 新写入字节 | 新写入 |
|---|---:|---:|---:|
| 2026-08-31 | 19,929 | 2,743,967,577 | 2.56 GiB |
| 2026-09-01 | 1,061 | 286,761,803 | 0.27 GiB |
| 2026-09-02 | 85 | 22,112,683 | 0.02 GiB |
| 2026-09-03 | 1,786 | 586,688,852 | 0.55 GiB |
| 2026-09-04 | 8,524 | 1,685,685,287 | 1.57 GiB |
| 2026-09-05 | 18,056 | 11,511,999,480 | 10.72 GiB |
| 2026-09-06 | 624 | 15,342,078 | 0.01 GiB |
| 2026-09-07 | 2,884 | 68,104,435 | 0.06 GiB |
| 2026-09-08 | 33,329 | 2,453,446,769 | 2.28 GiB |
| 2026-09-09 | 812 | 21,430,641 | 0.02 GiB |
| 2026-09-10 | 29,566 | 3,063,601,508 | 2.85 GiB |
| 2026-09-11 | 106 | 506,008 | 0.00 GiB |
| 2026-09-12（不完整日） | 302 | 62,796,127 | 0.06 GiB |
| **合计** | **117,064** | **22,522,443,248** | **20.98 GiB** |

**汇总**（`NewPart` 字节合计）：

| 口径 | 天数 | 合计写入 | bytes/day |
|---|---:|---:|---:|
| 全历史（08-31..09-12，含不完整日） | 13 | 22,522,443,248 | **1.61 GiB/d** |
| 12 个完整日（08-31..09-11） | 12 | 22,459,647,121 | **1.74 GiB/d** |
| 最近 7 个完整日（09-05..09-11） | 7 | 17,134,430,919 | **2.28 GiB/d** |

**校验（口径一致性）**：`NewPart` 累计写入 22.52 GB，而当前活跃 part 磁盘总量为 **16.02 GiB（17,201,082,625 B）**——差额由 `MergeParts`（合并去重）与 `RemovePart`（删除旧 part）解释，二者在同一份 `part_log` 中可见：

```
2026-08-31  MergeParts  4111  parts  11,379,413,066 bytes
2026-09-05  MergeParts  3537  parts  35,298,235,404 bytes
2026-09-08  MergeParts  6763  parts  11,475,924,169 bytes
2026-09-10  MergeParts  5974  parts  14,813,599,204 bytes
```

> 注意：`MergeParts.size_in_bytes` 远大于同日 `NewPart.size_in_bytes`，说明 **`NewPart` 只记录 INSERT 产生的新 part，不包含合并产物**，因此 `NewPart` 字节可作为「每日写入量」的下界可靠估计。

### 2.3 各表当前占用（`system.parts WHERE active`）

| 表 | 活跃 part | 磁盘字节 | 行数 |
|---|---:|---:|---:|
| `transactions` | 200 | 6,036,034,941 | 35,932,520 |
| `address_activity` | 408 | 5,833,599,689 | 74,454,629 |
| `token_transfers` | 357 | 4,845,020,906 | 56,605,708 |
| `logs` | 10 | 489,685,777 | 11,901,727 |
| `transaction_traces` | 1 | 135,534 | 3 |
| `internal_calls` | 2 | 118,259 | 2,729 |
| `address_balances` | 2 | 14,592 | 86 |
| `transaction_decoded_inputs` | 2 | 4,711 | 3 |
| **合计** | **982** | **17,204,614,409（16.02 GiB）** | **178,897,405** |

> **口径冲突（如实记录）**：`du -sh /data/blockchain-platform/clickhouse` = **39G**，而活跃 part 仅 **${GiB(chB)} GiB**。差额约 17 GiB 来自非活跃 part、`detached/`、`preprocessed/`、`tmp/`。本次为只读采集，**不做清理或合并**，故不进一步归因（已列入 §8 未闭合项）。

### 2.4 无 TTL 声明（migration grep，**佐证**，不作为唯一依据）

```
$ grep -rn -i -E "TTL|PARTITION BY" backend/migrations/clickhouse
backend/migrations/clickhouse/0001_init.sql:26:PARTITION BY (chain, toYYYYMM(timestamp))
backend/migrations/clickhouse/0001_init.sql:47:PARTITION BY (chain, toYYYYMM(timestamp))
backend/migrations/clickhouse/0001_init.sql:68:PARTITION BY (chain, toYYYYMM(timestamp))
backend/migrations/clickhouse/0001_init.sql:87:PARTITION BY (chain, toYYYYMM(timestamp))
backend/migrations/clickhouse/0001_init.sql:109:PARTITION BY (chain, toYYYYMM(timestamp))
backend/migrations/clickhouse/0002_address_activity.sql:20:PARTITION BY (chain, toYYYYMM(timestamp))
backend/migrations/clickhouse/0004_method_abi.sql:44:PARTITION BY chain
backend/migrations/clickhouse/0005_trace.sql:17:PARTITION BY chain
backend/migrations/clickhouse/0005_trace.sql:40:PARTITION BY chain
```

> **（migration grep 结论）** 迁移源码中**没有任何 `TTL` 子句**，只有 `PARTITION BY`。
>
> **但该证据不充分（复核意见 4）**：migration grep 只能证明**迁移源码**没有 TTL，**不能**排除手工 `ALTER TABLE ... MODIFY TTL`。因此必须用**在线部署 DDL** 核实 —— 见 **§2.6**。

### 2.5 真实净增实测（Revision 2 新增，复核意见 1 选项 b）

#### (a) 两点快照（真实净增，短窗口）

```sql
SELECT now(), sum(bytes_on_disk) AS bytes, sum(rows) AS rows, count() AS parts
  FROM system.parts WHERE active AND database='blockchain';
```

| 时点 | UTC | 活跃 part 字节 | 活跃 part 行数 | part 数 |
|---|---|---:|---:|---:|
| T1 | 2026-09-12 21:5x（与 §2.3 同批） | 17,204,614,409 | 178,897,405 | 982 |
| T_A | 2026-09-12 22:10:10 | 17,204,614,409 | 178,897,405 | 982 |
| T_B | 2026-09-12 22:12:04 | 17,204,614,409 | 178,897,405 | 982 |

**实测净增 = 0 B / 0 行 / 0 part**，观测窗口 T1→T_B ≈ 20 分钟。

> **正确解读（不得过度外推）**：该窗口内平台空闲、无导出/摄取写入，因此净增为 0。这**只证明该 20 分钟内净增为 0**，**不能**外推为日均 0，也**不能**证明全生命周期净增为 0。它恰好证明 **ClickHouse 增长是突发式而非连续式**。

#### (b) 全生命周期净增（N1，长窗口）

锚点：当前活跃 part 中**最早**的 `modification_time`。

```sql
SELECT table, min(modification_time), max(modification_time), count()
  FROM system.parts WHERE active AND database='blockchain' GROUP BY table ORDER BY table;
-- logs            2026-08-31 11:38:23 → 2026-09-12 19:47:35   (10 parts)
-- address_balances 2026-08-31 13:09:23 → 2026-09-02 10:58:54   (2 parts)
```

| 计算 | 值 |
|---|---|
| 观测起点 | 2026-08-31 11:38:23 UTC（证明 ClickHouse 数据存储自该时刻起存在） |
| 观测终点 | 2026-09-12 22:12:04 UTC |
| 观测时长 | **12.44 天** |
| 当前活跃 part 字节 | 17,204,614,409 B（16.02 GiB） |
| **净增速率 N1** | **1,384,112,341 B/日 ≈ 1.29 GiB/日** |

> **N1 的两个前提（必须同时成立）**：
> 1. **假定 2026-08-31 11:38:23 之前该库为空**（或该时刻之前的 part 已全部被合并/删除且不留痕）。该假设**未 VERIFIED**——本次无法读到更早的 part 记录。
> 2. **假定这 12.44 天内没有发生净减少**（即无大规模去重/删除）。`ReplacingMergeTree` 的去重只影响重复行，若摄取存在大量重复，N1 会**高估**真实净增。
>
> **误差方向**：**N1 是净增的上界估计**（因为去重/删除只会让它更小）。真实净增 **≤ 1.29 GiB/日**，**无保证**。

#### (c) 三口径对比与使用规则

| 口径 | 值 | 用途 | 禁止用途 |
|---|---:|---|---|
| G（gross，§2.2） | 1.61–2.28 GiB/d | 说明**写入活动强度**与突发性 | ❌ 不得用于 `daysToExhaust` |
| N1（全生命周期净增） | **1.29 GiB/d**（上界） | 长窗口净增的上界估计 | ⚠️ 需同时声明 §2.5(b) 的两个前提 |
| N2（两点实测） | **0 B / 20 min** | 证明净增是**突发式** | ❌ 不得外推日均 |

**本文正文（§0.3、§4.3）统一采用 N1，并标注其为上界。**

### 2.6 在线部署 DDL 核实「无 TTL」（Revision 2 新增，复核意见 4）

```sql
SELECT name, create_table_query FROM system.tables WHERE database='blockchain' ORDER BY name;
```

**实测 DDL 片段（原文，9 张表全部核对）**：

```
transactions               ENGINE = ReplacingMergeTree(source_ingested_at) PARTITION BY (chain, toYYYYMM(timestamp)) ORDER BY (chain, tx_hash) SETTINGS index_granularity = 8192
token_transfers            ENGINE = ReplacingMergeTree(source_ingested_at) PARTITION BY (chain, toYYYYMM(timestamp)) ORDER BY (chain, tx_hash, log_index, token_address) SETTINGS index_granularity = 8192
address_activity           ENGINE = ReplacingMergeTree(source_ingested_at) PARTITION BY (chain, toYYYYMM(timestamp)) ORDER BY (chain, address, timestamp, tx_hash, activity_type, counterparty, token_address) SETTINGS index_granularity = 8192
logs                       ENGINE = ReplacingMergeTree(source_ingested_at) PARTITION BY (chain, toYYYYMM(timestamp)) ORDER BY (chain, tx_hash, log_index) SETTINGS index_granularity = 8192
nft_transfers              ENGINE = ReplacingMergeTree(source_ingested_at) PARTITION BY (chain, toYYYYMM(timestamp)) ORDER BY (chain, tx_hash, log_index, contract_address, token_id) SETTINGS index_granularity = 8192
address_balances           ENGINE = ReplacingMergeTree(source_ingested_at) PARTITION BY (chain, toYYYYMM(timestamp)) ORDER BY (chain, address, token_symbol, token_address) SETTINGS index_granularity = 8192
internal_calls             ENGINE = ReplacingMergeTree(traced_at) PARTITION BY chain ORDER BY (chain, tx_hash, call_index) SETTINGS index_granularity = 8192
transaction_traces         ENGINE = ReplacingMergeTree(traced_at) PARTITION BY chain ORDER BY (chain, tx_hash) SETTINGS index_granularity = 8192
transaction_decoded_inputs ENGINE = ReplacingMergeTree(decoded_at) PARTITION BY chain ORDER BY (chain, tx_hash) SETTINGS index_granularity = 8192
```

> **在线实测结论**：**9 张表的 `create_table_query` 中均无 `TTL` 子句**，只有 `ENGINE` / `PARTITION BY` / `ORDER BY` / `SETTINGS index_granularity = 8192`（部分表另有 bloom_filter `INDEX`）。「**无 TTL**」结论由**在线部署 DDL 直接证实**；§2.4 的 migration grep 仅作佐证。

### 2.7 ClickHouse 目录的 16 GiB 一次性备份（Revision 2 新增）

Revision 1 只写了「du 39G vs 活跃 part 16.02 GiB，差额未归因」。Revision 2 完成归因：

```
$ du -sb /data/blockchain-platform/clickhouse/*   | sort -rn | head
23516769451     /data/blockchain-platform/clickhouse/store        ← 21.90 GiB，全部 part（活跃 16.02 GiB + 非活跃 ~5.9 GiB）
17215465503     /data/blockchain-platform/clickhouse/backups      ← 16.03 GiB，**一次性备份副本**
104387          /data/blockchain-platform/clickhouse/preprocessed_configs
1612            /data/blockchain-platform/clickhouse/data
434             /data/blockchain-platform/clickhouse/metadata
$ ls -la /data/blockchain-platform/clickhouse/backups
drwxr-x--- 3 messagebus lxd 4096 ... 20260912T171120Z     ← 备份时间戳 2026-09-12 17:11:20 UTC
```

> **结论**：CH 目录中 **16.03 GiB（占其 37.93 GiB 的 42%）是 `BACKUP` 产生的一次性副本**（与 PITR 演练同一窗口）。`store/` 与 `backups/` 之和 ≈ CH 目录总量，说明二者**未使用硬链接**（否则 `du` 会去重），即**真实占用了两份磁盘**。该副本**与稳态增长无关，不得计入日增**。

---

## 3. MinIO 日增实测

### 3.1 桶清单与当前占用（只读 S3 API，`mc` 不存在）

`mc` 在部署机与容器内均不存在，改用只读 S3 API（`GET /`、`GET /<bucket>?list-type=2`、`?versioning`、`?lifecycle`）：

| 桶 | 对象数 | 字节 | | 版本控制 | ILM |
|---|---:|---:|---|---|---|
| `blockchain-audit` | 4 | 1,964 | 0.00 GiB | 未启用 | **无 lifecycle 配置**（404） |
| `blockchain-backups` | 0 | 0 | 0.00 GiB | 未启用 | **无 lifecycle 配置**（404） |
| `blockchain-exports` | 28 | 5,274,869 | 0.00 GiB | 未启用 | **无 lifecycle 配置**（404） |
| `blockchain-raw` | 170,515 | 15,058,046,314 | 14.02 GiB | 未启用 | **无 lifecycle 配置**（404） |
| `blockchain-reports` | 68 | 115,322 | 0.00 GiB | 未启用 | **无 lifecycle 配置**（404） |
| `blockchain-reports-drill-20260912t175029z` | 60 | 101,724 | 0.00 GiB | 未启用 | **无 lifecycle 配置**（404） |
| **合计** | **170,675** | **15,063,540,193** | **14.03 GiB** | — | — |

### 3.2 `blockchain-raw` 逐日分布（对象 `LastModified`，**survivor / current-version 口径**）

| 日期（UTC） | 对象数 | 字节 | |
|---|---:|---:|---|
| 2026-08-31 | 35,746 | 2,738,817,223 | 2.55 GiB |
| 2026-09-01 | 81 | 1,994,096 | 0.00 GiB |
| 2026-09-02 | 420 | 542,317,788 | 0.51 GiB |
| 2026-09-03 | 654 | 195,427,714 | 0.18 GiB |
| 2026-09-04 | 3,324 | 426,076,462 | 0.40 GiB |
| 2026-09-05 | 6,319 | 7,508,775,015 | 6.99 GiB |
| 2026-09-06 | 607 | 3,530,129 | 0.00 GiB |
| 2026-09-07 | 640 | 160,560,301 | 0.15 GiB |
| 2026-09-08 | 61,321 | 2,401,351,912 | 2.24 GiB |
| 2026-09-09 | 707 | 13,830,845 | 0.01 GiB |
| 2026-09-10 | 60,493 | 1,062,677,947 | 0.99 GiB |
| 2026-09-11 | 78 | 115,650 | 0.00 GiB |
| 2026-09-12 | 125 | 2,571,232 | 0.00 GiB |
| **合计** | **170,515** | **15,058,046,314** | **14.02 GiB** |

| 口径 | 天数 | 合计 | bytes/day |
|---|---:|---:|---:|
| 全历史（08-31..09-12，含不完整日） | 13 | 15,058,046,314 | **1.08 GiB/d** |
| 12 个完整日 | 12 | 15,055,475,082 | **1.17 GiB/d** |
| 最近 7 个完整日 | 7 | 11,150,841,799 | **1.48 GiB/d** |

> **口径警告（Revision 2 强化，复核意见 3 —— 必须遵守）**：
>
> 1. **survivor 口径**：`ListObjectsV2` 只枚举**当前存活对象**；**已删除的对象完全不在统计内**（MinIO 无删除日志可查，故删除量**不可得**）。
> 2. **current-version 口径**：6 个桶**均未启用版本控制**（`GET ?versioning` 返回空配置），故**无历史版本**可统计。若历史上曾启用过版本控制，旧版本也不会出现在本表。
> 3. `LastModified` 是**最后修改时间**而非首次写入时间；被**覆盖写**的对象会归入最后一次修改日。
>
> **因此上表是「当前存活对象的近似写入分布」，不是「全部历史净增」。** 误差方向取决于删除量与覆盖写量的比例——**本次无法量化**（已列入 §7 未闭合项）。
>
> **另外**：MinIO 侧的**真实净增**（写入 − 删除 − 覆盖）**不可得**，因为 MinIO 未启用版本控制、也未开启审计/指标日志采集。因此 §4.3 的 MinIO 分量应理解为「**存活对象增长的上界**」。

---

## 4. 磁盘水位与耗尽预测

### 4.1 实测水位（`df -B1`）

```
$ df -h; df -B1 / /data
Filesystem                         Size  Used Avail Use% Mounted on
/dev/mapper/ubuntu--vg-ubuntu--lv  147G   80G   60G  58% /
/dev/mapper/ubuntu--vg-data_lv     1.1T   61G  924G   7% /data

Filesystem                            1B-blocks        Used     Available Use% Mounted on
/dev/mapper/ubuntu--vg-ubuntu--lv  157396975616 85457428480  63869706240  58% /
/dev/mapper/ubuntu--vg-data_lv    1105357205504 64576253952 991482462208   7% /data
```

| 分区 | 总容量 | 已用 | 可用 | 使用率 | 承载 |
|---|---:|---:|---:|---:|---|
| `/data` @22:12:25 | 1,105,357,205,504 B（1.01 TiB） | **86,406,115,328 B（80.47 GiB）** | **969,652,600,832 B（903.05 GiB）** | 7.82% | postgres / clickhouse / minio / nats 四个容器数据卷 |
| `/` @21:49:04 | 157,396,975,616 B（146.59 GiB） | 85,457,428,480 B（79.59 GiB） | 63,869,706,240 B（59.48 GiB） | 54.29% | `/var/lib/docker`（镜像层、容器可写层） |

> **`/data` 已用在 23 分钟内增长 20.33 GiB**（Revision 1 完全遗漏）：
> ```
> df -B1 /data @21:49:04   Used 64,576,253,952   Avail 991,482,462,208   Use% 5.84
> df -B1 /data @22:12:25   Used 86,406,115,328   Avail 969,652,600,832   Use% 7.82   ← +21,829,861,376 B
> ```
> **该 20.33 GiB 不是业务增长，而是 PITR/RPO 演练写入 base 备份所致**（见 §4.5）。Revision 1 用 21:49 的水位（60.14 GiB）作为基线做耗尽预测，**基线本身已失效**。

> 全部业务数据卷位于 **`/data`**（见 `deploy/compose.yaml`：`/data/blockchain-platform/{postgres,clickhouse,minio,nats}`），因此下文耗尽预测以 `/data` 为准。

### 4.2 组件分解（UTC 2026-09-12 22:12:25，`du -sb`）

| 组件 | 字节 | GiB | 其中**一次性/演练占用** | 其中**稳态数据** |
|---|---:|---:|---|---|
| `clickhouse/` | 40,730,444,188 | 37.93 | **`backups/20260912T171120Z/` = 17,215,465,503 B（16.03 GiB）** | `store/` 23,516,769,451 B（21.90 GiB） |
| 容器 `/var/lib/postgresql` | 28,497,407,919 | 26.54 | **8 个 `pitr-*` 目录 ≈ 24,108,650,842 B（22.45 GiB）** | 在线 PGDATA 3,113,687,297 B（2.90 GiB）+ `wal-archive` 1,275,069,780 B（1.19 GiB） |
| `minio/` | 15,137,696,227 | 14.10 | 无 | 存活对象 15,063,390,193 B（14.03 GiB） |
| `nats/` | **不可得** | — | — | `Permission denied`（未以容器 root 读取） |

> **一次性占用合计约 38.5 GiB，占 `/data` 已用（80.47 GiB）的 47.8%**。这也解释了 Revision 1 的「du 39G vs 活跃 part 16 GiB」差额——**主因是 ClickHouse 备份副本，不是非活跃 part**。

### 4.3 三组件日增汇总（口径已修正）

| 组件 | 口径 L（全历史） | 口径 M（12 完整日） | 口径 H（近 7 完整日） | 口径性质 |
|---|---:|---:|---:|---|
| PostgreSQL（**逻辑库**） | 32.16 MiB/d | 55.73 MiB/d | 81.63 MiB/d | 推导口径（§1.1），**系统性低估** |
| ClickHouse | **1.29 GiB/d（N1 净增上界，全窗口统一）** | 同左 | 同左 | **净增**；gross 口径 1.61/1.74/2.28 GiB/d **不得用于耗尽计算**（§2.1） |
| MinIO `blockchain-raw` | 1.08 GiB/d | 1.17 GiB/d | 1.48 GiB/d | **survivor 口径上界**，不含删除（§3.2） |
| **合计（稳态业务）** | **2.40 GiB/d** | **2.51 GiB/d** | **2.85 GiB/d** | — |
| 另计：**WAL 归档** | **≈21.0 – 27.0 GiB/d** | 同左 | 同左 | 实测（§4.6）；**若持续则完全主导增长** |

### 4.4 预计耗尽（Revision 2：**只给区间与前提，不给单一确定日期**）

> **为什么不给确定日期（复核意见 1、2 的直接后果）**：`/data` 的当前增长**由演练与一次性副本主导**（§4.2：一次性占用 38.5 GiB，23 分钟写入 20.33 GiB），**且 WAL 归档速率远超业务写入**（§4.6）。任何单一「耗尽日」都会**掩盖**这一事实，因此本节只给**在明确前提下的区间**。

**情景 A：仅稳态业务写入（假设演练结束、CH 备份不再新增、WAL 归档已有清理）**

| 口径 | 合计日增 | 以 903.05 GiB 可用计 | 前提 |
|---|---:|---:|---|
| L | 2.40 GiB/d | **≈ 376 天** | 见下方三条前提 |
| M | 2.51 GiB/d | **≈ 359 天** | 同上 |
| H | 2.85 GiB/d | **≈ 316 天** | 同上 |

**情景 B：稳态业务写入 + WAL 归档持续（`archive_mode=on` + `archive_timeout=60` 且无清理）**

| WAL 取值 | WAL 日增 | 合计日增（口径 M） | 以 903.05 GiB 可用计 |
|---|---:|---:|---:|
| 下界（短窗口 22:09:12→22:12:25 实测） | 20.98 GiB/d | 23.49 GiB/d | **≈ 38 天** |
| `archive_timeout=60` 理论下限（16 MiB/60 s） | 22.50 GiB/d | 25.01 GiB/d | **≈ 36 天** |
| 上界（全窗口 21:08:46→22:11:12 实测） | 27.03 GiB/d | 29.54 GiB/d | **≈ 31 天** |

> **情景 B 是当前配置下更接近真实的区间：约 31–38 天**。但 **WAL 归档速率是否长期成立 = NOT VERIFIED**（§4.6、§7），因此**不得**把「31 天」当作确定结论。

### 4.5 单组件独立预测（30 / 90 / 365 天）

> 预测公式：`占用(t) = 当前占用 + 日增 × t`。**本节的 ClickHouse 分量已改用 N1 净增（1.29 GiB/d）**，不再使用 gross 口径。

#### (a) PostgreSQL（逻辑库）

当前逻辑库 **902,608,575 B（860.80 MiB）**。

| 预测日 | 口径 L（32.16 MiB/d） | 口径 M（55.73 MiB/d） | 口径 H（81.63 MiB/d） |
|---|---:|---:|---:|
| +30 天 | 1825.53 MiB | 2532.68 MiB | 3309.76 MiB |
| +90 天 | 3754.99 MiB | 5876.45 MiB | 8207.69 MiB |
| +365 天 | 12598.38 MiB | 21202.06 MiB | 30656.55 MiB |

> ⚠️ 该表**只覆盖逻辑库**，**不含** WAL 归档与 PITR/备份目录（那两项在 §4.6、§4.7 单列）。PG 在 `/data` 上的实际占用由 §4.7 给出。

#### (b) ClickHouse（**改用 N1 净增**；gross 口径仅作对照）

当前 `blockchain` 库活跃 part **17,204,614,409 B（16.02 GiB）**，982 个 part；`store/` 总量 21.90 GiB；目录总量 37.93 GiB（含 16.03 GiB 一次性备份）。

| 预测日 | **N1 净增（1.29 GiB/d，本文采用）** | 对照：gross L（1.61） | 对照：gross M（1.74） | 对照：gross H（2.28） |
|---|---:|---:|---:|---:|
| +30 天 | **54.72 GiB** | 64.43 GiB | 68.31 GiB | 84.41 GiB |
| +90 天 | **132.12 GiB** | 161.24 GiB | 172.90 GiB | 221.19 GiB |
| +365 天 | **486.87 GiB** | 604.95 GiB | 652.25 GiB | 848.10 GiB |

> ⚠️ **净增长无保证**：N1 是**上界**（§2.5b）。若持续发生去重/删除，真实占用**会低于**本表。`+365 天 = 486.87 GiB` 意味着**仅 ClickHouse 一项就会吃掉 `/data` 余量的一半以上**（可用 903.05 GiB）——这是本预测中**最需要关注的单项**。

#### (c) MinIO（survivor 口径上界）

当前全部桶 **15,063,390,193 B（14.03 GiB）**；文件系统 `du -sb` 实测 15,137,696,227 B（14.10 GiB）。

| 预测日 | 口径 L（1.08 GiB/d） | 口径 M（1.17 GiB/d） | 口径 H（1.48 GiB/d） |
|---|---:|---:|---:|
| +30 天 | 46.39 GiB | 49.08 GiB | 58.54 GiB |
| +90 天 | 111.12 GiB | 119.19 GiB | 147.55 GiB |
| +365 天 | 407.78 GiB | 440.52 GiB | 555.53 GiB |

> ⚠️ **该表是上界**：survivor 口径不含已删除对象，真实净增**≤** 本表。误差量**不可得**（§3.2）。

### 4.6 WAL 归档：当前 `/data` 增长的**主导项**（Revision 2 新增）

**实测（两段独立窗口）**：

```
$ docker exec -u 0 deploy-postgres-1 sh -c 'ls /var/lib/postgresql/wal-archive | wc -l; du -sb /var/lib/postgresql/wal-archive;
                                          ls -la --time-style=full-iso /var/lib/postgresql/wal-archive | head -4; ... | tail -3'
77 个段  1,224,738,132 B   首段 2026-09-12 21:08:46  末段 22:09:12
79 个段  1,258,292,564 B   首段 2026-09-12 21:08:46  末段 22:11:12
（每段固定 16 MiB = 16,777,216 B）
```

| 观测窗口 | 时长 | 字节增量 | 段数增量 | 速率 | 折算日增 |
|---|---:|---:|---:|---:|---:|
| 21:08:46 → 22:09:12 | 3,626 s | 1,224,738,132 | 77 | 337,765 B/s | **27.03 GiB/d** |
| 21:08:46 → 22:11:12 | 3,746 s | 1,258,292,564 | 79 | 335,903 B/s | **26.88 GiB/d** |
| 22:09:12 → 22:12:25 | 193 s | 50,331,648 | 2 | 260,786 B/s | **20.98 GiB/d** |
| 理论下限（`archive_timeout=60`） | 60 s | 16,777,216 | 1 | 279,620 B/s | **22.50 GiB/d** |

**相关配置实测**：

```sql
SELECT name, setting FROM pg_settings WHERE name IN
  ('archive_mode','archive_command','archive_timeout','wal_level','wal_keep_size','max_wal_size','min_wal_size');
-- archive_mode     | on
-- archive_command  | if [ -f /var/lib/postgresql/wal-archive/%f ]; then cmp -s %p /var/lib/postgresql/wal-archive/%f; else cp %p /var/lib/postgresql/wal-archive/%f; fi
-- archive_timeout  | 60        ← 每 60 秒强制切换一个 WAL 段（即使空闲）
-- wal_level        | replica
-- wal_keep_size    | 0
-- max_wal_size     | 1024      (MB)
-- min_wal_size     | 80        (MB)
```

**归档清理责任方排查（全部无命中）**：

```
$ crontab -l                                  → no crontab for euripides
$ ls /etc/cron.d/                             → certbot, e2scrub_all, sysstat（无 WAL 清理任务）
$ systemctl list-timers --all | grep -iE 'pg|wal|pitr|backup|blockchain'   → 无命中
$ ls scripts/ | grep -iE 'pitr|backup|wal|restore'                          → 无命中
```

> **关键结论**：`archive_timeout=60` 使 PostgreSQL **即使空闲也每 60 秒归档一个 16 MiB 段**（下限 22.50 GiB/d）；实测 **20.98 – 27.03 GiB/d**；且**未发现任何清理机制**。这是**当前 `/data` 增长的主导项**，量级是全部业务写入（2.51 GiB/d）的 **约 10 倍**。
>
> **NOT VERIFIED**：该配置是否为 PITR 演练期间的临时设置、归档目录由谁在何时清理，**均无法从代码/迁移确认**（§7 第 10 项）。**不得**据此断言「31 天后磁盘一定耗尽」。

### 4.7 敏感性分析：备份频率 × base 体积 + WAL（Revision 2 重写，复核意见 2）

> **Revision 1 的错误**：曾用「`/data/blockchain-platform/postgres` du = 11 GiB ÷ 逻辑库 861 MiB = **13.09×**，按此放大 PG 分量」来做敏感性分析，并声称真实耗尽日「落在两个模型之间」。**该做法错误**——那 11 GiB（现测 26.54 GiB）中的绝大部分是**本次 PITR 失败演练产生的多个 base/clone 目录**（一次性），放大系数**没有任何外推意义**。本节按复核意见重写。

#### (a) 实测：`/var/lib/postgresql` 逐目录清单（UTC 2026-09-12 22:0x，容器 root）

```
$ docker exec -u 0 deploy-postgres-1 sh -c 'du -sh /var/lib/postgresql; for d in /var/lib/postgresql/*; do du -sh "$d"; done'
24G     /var/lib/postgresql                                  ← 22:12 再测为 26.54 GiB（演练仍在写）
3.0G    /var/lib/postgresql/18                                ← 在线 PGDATA 所在
4.0K    /var/lib/postgresql/pitr-base-20260912T211721Z        ← 空目录
2.7G    /var/lib/postgresql/pitr-base-20260912T214713Z
2.7G    /var/lib/postgresql/pitr-base-20260912T215037Z
2.7G    /var/lib/postgresql/pitr-base-20260912T215656Z
2.7G    /var/lib/postgresql/pitr-base-20260912T215751Z
2.9G    /var/lib/postgresql/pitr-clone-rpo1-20260912T215751Z
2.8G    /var/lib/postgresql/pitr-cloneA-20260912T215037Z
2.8G    /var/lib/postgresql/pitr-cloneA-20260912T215751Z
33M     /var/lib/postgresql/pitr-frozen-20260912T215037Z-rpo1
49M     /var/lib/postgresql/pitr-frozen-20260912T215656Z-sc
129M    /var/lib/postgresql/pitr-frozen-20260912T215751Z-rpo1
49M     /var/lib/postgresql/pitr-frozen-20260912T215751Z-target
1.2G    /var/lib/postgresql/wal-archive
```

**8 个 base/clone 目录的时间戳全部落在 2026-09-12 21:17–21:58 UTC（41 分钟内）**，即**一次演练**，合计 **≈ 22.45 GiB**（22:12 实测）；在线 PGDATA 仅 **2.90 GiB**。

#### (b) 敏感性公式（复核意见 2 要求单列）

```
每日净增(PG 卷) = 备份频率(f) × 每份 base 体积(B)
                + WAL/day(W) × (1 − 清理比例(p))
                + 恢复空间瞬时占用(R) / 恢复窗口天数
```

| 变量 | 符号 | 实测/推断值 | 状态 |
|---|---|---|---|
| 每份 base 体积 | **B** | **2.7 – 2.9 GiB**（实测 8 份：5×base 2.7G，2×cloneA 2.8G，1×clone-rpo1 2.9G） | **已实测** |
| 备份频率 | **f** | 演练期间 = 8 份 / 41 min ≈ **11.7 份/小时**（**不可持续**） | **NOT VERIFIED**（常态频率未知；未发现 cron/systemd timer/脚本调度备份） |
| WAL 日增 | **W** | **20.98 – 27.03 GiB/d**（§4.6） | **已实测**（但长期是否成立 **NOT VERIFIED**） |
| 归档清理比例 | **p** | **0**（未发现任何清理机制） | **推断**（未发现清理任务 ≠ 不存在外部清理） |
| 恢复空间 | **R** | 恢复一次需 **1 × B（2.7–2.9 GiB）** 起的可写空间 | **NOT VERIFIED**（RPO/RTO 目标值未在代码或文档中找到） |
| 备份保留窗口 | — | 未定义 | **NOT VERIFIED**（`retention_policies` 7 类资源中**无 PG 备份类**；见 §6） |
| 副本数 / 异地副本 | — | 未定义 | **NOT VERIFIED** |

#### (c) 情景表（**前提明确，未知量标注**）

| 情景 | 假设 | PG 卷日增 | 说明 |
|---|---|---:|---|
| **S0 演练已结束、归档无清理** | f = 0 | **20.98 – 27.03 GiB/d**（全部来自 WAL） | 当前最可能的稳态 |
| S1 每日 1 次全量 base | f = 1/d，B = 2.8 GiB | 23.8 – 29.8 GiB/d | **f 为 NOT VERIFIED 假设** |
| S2 每 6 小时 1 次 base | f = 4/d，B = 2.8 GiB | 32.2 – 38.2 GiB/d | 同上 |
| S3 演练持续（仅在演练期间） | f ≈ 11.7/h | ≈ 780 GiB/d | **不可持续，仅说明演练的破坏性** |

> **误差方向与结论**：**只要 WAL 归档无清理，PG 卷的日增就在 21–27 GiB/d 量级**，备份频率只会让它**更大**。Revision 1 关于「真实耗尽日落在两个模型之间」的表述**已删除**——**那 11 GiB 中 22.45 GiB 是演练一次性占用，不构成任何速率**。

### 4.8 30 / 90 / 365 天 `/data` 占用预测（Revision 2 重写）

`/data` 总容量 1,105,357,205,504 B（1029.44 GiB）；**基线取 2026-09-12 22:12:25 实测 80.47 GiB**（Revision 1 用的 60.14 GiB 基线已失效，见 §4.1）。

**情景 A（仅稳态业务：2.51 GiB/d，不含 WAL、不含演练/备份）**

| 预测日 | 占用 | 可用余量 |
|---|---:|---:|
| +30 天 | **155.83 GiB** | 873.61 GiB |
| +90 天 | **306.54 GiB** | 722.90 GiB |
| +365 天 | **997.31 GiB** | **32.13 GiB** ⚠️ |

**情景 B（稳态业务 + WAL 归档，取 §4.6 中值 25.01 GiB/d → 合计 27.52 GiB/d）**

| 预测日 | 占用 | 可用余量 |
|---|---:|---:|
| +30 天 | **906.07 GiB** | **123.37 GiB** ⚠️ |
| +90 天 | 2557.27 GiB | **−1527.83 GiB（早已耗尽）** |
| +365 天 | — | 不适用 |

> **结论**：
> - **情景 A（无 WAL 增长）下，`+365 天 997.31 GiB` 已逼近 1029.44 GiB 总容量，余量仅 32.13 GiB** —— Revision 1 写的「仍有大量余量」是**算术错误**（其表格已列出 −113.28 GiB，结论却相反），此处更正。
> - **情景 B（WAL 持续）下，约 33 天内耗尽**（903.05 GiB ÷ 27.52 GiB/d ≈ 32.8 天），即 **2026-10-15 前后**。**但该情景依赖「WAL 归档永远不清理」这一 NOT VERIFIED 前提**，因此**不得**作为确定结论。
> - **两个情景的差异（33 天 vs 365 天）完全取决于 WAL 归档是否被清理**——这是本容量预测中**唯一的决定性变量**。

---

## 5. organization / workspace / dataset 三维度占用与预测

### 5.1 维度列可用性（实测）

三个维度列**均存在**（`docs/data-dictionary.md` §8.1 有完整命中清单）。维度基数实测：

```sql
SELECT (SELECT count(*) FROM organizations) AS orgs,
       (SELECT count(*) FROM workspaces)    AS workspaces,
       (SELECT count(*) FROM download_datasets) AS datasets;
-- 2 | 3 | 14996
```

| 维度 | 基数 | `organization_id` 表数 | `workspace_id` 表数 | `dataset` 表数 | `dataset_id` 表数 |
|---|---:|---:|---:|---:|---:|
| organization / workspace / dataset | 2 / 3 / 14,996 | 33 | 27 | 24 | 3 |

### 5.2 organization 维度（实测当前占用）

> 字节口径：`Σ(该组织行数 × 该表平均行宽)`，平均行宽 = `pg_total_relation_size / count(*)`。

| organization_id | 归属行数 | 折算字节 | 折算 | 占比 | 涉及表数 |
|---|---:|---:|---:|---:|---:|
| `d3b152f2-44c0-441c-b047-be24c137a84b` | 45,673 | 66,543,330 | 63.46 MiB | 80.70% | 9 |
| `(NULL)` | 8,035 | 11,115,142 | 10.60 MiB | 13.48% | 2 |
| `627c131a-3842-4e19-bade-d20ffb8aea8f` | 2,994 | 4,803,617 | 4.58 MiB | 5.83% | 10 |
| **合计** | **56,702** | **82,462,089** | **78.64 MiB** | 100% | — |

**30 / 90 / 365 天预测**（假设各组织按当前占比分摊全库日增 55.73 MiB/d）：

| organization_id | 当前 | +30 天 | +90 天 | +365 天 |
|---|---:|---:|---:|---:|
| `d3b152f2-44c0-441c-b047-be24c137a84b` | 63.46 MiB | 1412.60 MiB | 4110.88 MiB | 16477.98 MiB |
| `(NULL)` | 10.60 MiB | 235.96 MiB | 686.67 MiB | 2752.42 MiB |
| `627c131a-3842-4e19-bade-d20ffb8aea8f` | 4.58 MiB | 101.97 MiB | 296.76 MiB | 1189.51 MiB |

> **该预测的强假设（必须声明）**：按「当前占比」分摊未来增量。若某组织未来贡献的下载/审计量占比变化，预测随之失效。**无法**从当前 13 天数据推断各组织的独立增长率——`audit_events_v2` 全部数据集中在一个小时窗口内（§1.5 a-2），不具备分组织日增的时间序列。

### 5.3 workspace 维度（实测当前占用）

| workspace_id | 归属行数 | 折算字节 | 折算 | 占比 | 涉及表数 |
|---|---:|---:|---:|---:|---:|
| `000e31bb-8c48-435d-af97-95bbc75a496c` | 45,975 | 66,127,406 | 63.06 MiB | 81.40% | 8 |
| `(NULL)` | 8,163 | 11,273,750 | 10.75 MiB | 13.88% | 2 |
| `14dfbf1b-b3d7-4326-8395-b1f7fa9f8872` | 2,859 | 3,839,336 | 3.66 MiB | 4.73% | 8 |
| `231875b4-2687-4535-aba6-cbee410c5020` | 1 | 989 | 0.00 MiB | 0.00% | 1 |
| **合计** | **56,998** | **81,241,481** | **77.48 MiB** | 100% | — |

| workspace_id | 当前 | +30 天 | +90 天 | +365 天 |
|---|---:|---:|---:|---:|
| `000e31bb-8c48-435d-af97-95bbc75a496c` | 63.06 MiB | 1423.91 MiB | 4145.61 MiB | 16620.07 MiB |
| `(NULL)` | 10.75 MiB | 242.76 MiB | 706.77 MiB | 2833.48 MiB |
| `14dfbf1b-b3d7-4326-8395-b1f7fa9f8872` | 3.66 MiB | 82.67 MiB | 240.69 MiB | 964.96 MiB |
| `231875b4-2687-4535-aba6-cbee410c5020` | 0.00 MiB | 0.02 MiB | 0.06 MiB | 0.25 MiB |

> 同样适用 §5.2 的分摊假设。另外注意 `audit_events_v2` 与 `login_events` 中存在 `workspace_id IS NULL` 的行（分别为 6,631 与 1,532 行），属**未归属工作区**的系统级事件，预测中已单列。

### 5.4 dataset 维度（实测当前占用）

> `dataset` 为文本数据集名。`download_volume_guard_commits.dataset_id` 是 **UUID 外键**（指向 `download_datasets.id`），与文本 `dataset` 语义不同；该表在 §5.4 中以 UUID 呈现。

| dataset | 归属行数 | 折算字节 | 折算 | 占比 | 涉及表数 |
|---|---:|---:|---:|---:|---:|
| `token_transfers` | 390,691 | 293,426,042 | 279.83 MiB | 60.02% | 6 |
| `transactions` | 98,751 | 74,621,459 | 71.16 MiB | 15.26% | 6 |
| `receipt` | 96,112 | 71,086,761 | 67.79 MiB | 14.54% | 1 |
| `internal_transactions` | 21,817 | 16,318,829 | 15.56 MiB | 3.34% | 5 |
| `CHAIN_HEAD` | 8,877 | 6,565,644 | 6.26 MiB | 1.34% | 1 |
| `` | 347 | 6,381,568 | 6.09 MiB | 1.31% | 1 |
| `BALANCE` | 5,558 | 4,110,831 | 3.92 MiB | 0.84% | 1 |
| `logs` | 4,468 | 3,572,695 | 3.41 MiB | 0.73% | 5 |
| `b5311edc-86ad-4a0a-ad9d-4ec2073049de` | 9,999 | 3,340,502 | 3.19 MiB | 0.68% | 1 |
| `b6c33500-589d-4ec5-aa2e-b389263551a9` | 9,999 | 3,340,502 | 3.19 MiB | 0.68% | 1 |
| `contract` | 1,814 | 1,341,678 | 1.28 MiB | 0.27% | 1 |
| `probe` | 1,616 | 1,195,233 | 1.14 MiB | 0.24% | 1 |
| `latest_block` | 1,224 | 905,300 | 0.86 MiB | 0.19% | 1 |
| `block_timestamp` | 1,215 | 898,643 | 0.86 MiB | 0.18% | 1 |
| `account` | 829 | 613,148 | 0.58 MiB | 0.13% | 1 |
| `b61d0a08-6e9b-4473-acbc-295e4b8bb28f` | 392 | 130,961 | 0.12 MiB | 0.03% | 1 |
| `2128c74c-ec71-46b0-ae4f-4e0a7b21c75a` | 352 | 117,597 | 0.11 MiB | 0.02% | 1 |
| `nft_transfers` | 100 | 83,764 | 0.08 MiB | 0.02% | 3 |
| `d20fe09b-699d-4159-a92d-58e5129a1afa` | 217 | 72,496 | 0.07 MiB | 0.01% | 1 |
| `a43e5978-2e87-4ac7-828e-3dc4812026be` | 112 | 37,417 | 0.04 MiB | 0.01% | 1 |
| `5a33a7bd-ebff-4097-91da-bce30fbe445c` | 111 | 37,083 | 0.04 MiB | 0.01% | 1 |
| `CODE` | 47 | 34,762 | 0.03 MiB | 0.01% | 1 |
| `9dfc3508-f854-4dd3-8100-586311c06ab8` | 89 | 29,733 | 0.03 MiB | 0.01% | 1 |
| `RECEIPT` | 36 | 26,626 | 0.03 MiB | 0.01% | 1 |
| `ab826a20-827a-486e-920a-6f72aa722073` | 76 | 25,390 | 0.02 MiB | 0.01% | 1 |
| `balances` | 31 | 20,553 | 0.02 MiB | 0.00% | 4 |
| `5e664f87-f402-466f-a797-84290b697df3` | 61 | 20,379 | 0.02 MiB | 0.00% | 1 |
| `fa41524b-7a3e-49ac-b42a-93b4e60d29bc` | 60 | 20,045 | 0.02 MiB | 0.00% | 1 |
| `8d77d206-0aef-4866-a7b7-5435817e3f37` | 56 | 18,709 | 0.02 MiB | 0.00% | 1 |
| `3c67fe7f-dfa1-42b4-83c9-c49583ec4e2e` | 52 | 17,372 | 0.02 MiB | 0.00% | 1 |
| …（其余 153 个 UUID 级 `dataset_id` 条目，合计 0.48 MiB） | — | — | — | — | — |
| **合计** | **656,557** | **488,914,944** | **466.27 MiB** | 100% | 183 |

**按数据集名的 30 / 90 / 365 天预测**（仅文本 `dataset` 项）：

| dataset | 当前 | +30 天 | +90 天 | +365 天 |
|---|---:|---:|---:|---:|
| `token_transfers` | 279.83 MiB | 1283.23 MiB | 3290.02 MiB | 12487.80 MiB |
| `transactions` | 71.16 MiB | 326.34 MiB | 836.69 MiB | 3175.78 MiB |
| `receipt` | 67.79 MiB | 310.88 MiB | 797.05 MiB | 3025.35 MiB |
| `internal_transactions` | 15.56 MiB | 71.37 MiB | 182.97 MiB | 694.51 MiB |
| `CHAIN_HEAD` | 6.26 MiB | 28.71 MiB | 73.62 MiB | 279.42 MiB |
| `` | 6.09 MiB | 27.91 MiB | 71.55 MiB | 271.59 MiB |
| `BALANCE` | 3.92 MiB | 17.98 MiB | 46.09 MiB | 174.95 MiB |
| `logs` | 3.41 MiB | 15.62 MiB | 40.06 MiB | 152.05 MiB |
| `contract` | 1.28 MiB | 5.87 MiB | 15.04 MiB | 57.10 MiB |
| `probe` | 1.14 MiB | 5.23 MiB | 13.40 MiB | 50.87 MiB |
| `latest_block` | 0.86 MiB | 3.96 MiB | 10.15 MiB | 38.53 MiB |
| `block_timestamp` | 0.86 MiB | 3.93 MiB | 10.08 MiB | 38.25 MiB |
| `account` | 0.58 MiB | 2.68 MiB | 6.87 MiB | 26.09 MiB |
| `nft_transfers` | 0.08 MiB | 0.37 MiB | 0.94 MiB | 3.56 MiB |
| `CODE` | 0.03 MiB | 0.15 MiB | 0.39 MiB | 1.48 MiB |
| `RECEIPT` | 0.03 MiB | 0.12 MiB | 0.30 MiB | 1.13 MiB |
| `balances` | 0.02 MiB | 0.09 MiB | 0.23 MiB | 0.87 MiB |
| `transaction_detail` | 0.01 MiB | 0.07 MiB | 0.17 MiB | 0.66 MiB |
| `control_plane` | 0.01 MiB | 0.04 MiB | 0.11 MiB | 0.41 MiB |
| `CONTRACT_READ` | 0.00 MiB | 0.02 MiB | 0.05 MiB | 0.19 MiB |
| `transaction_receipt` | 0.00 MiB | 0.02 MiB | 0.04 MiB | 0.16 MiB |
| `TRACE` | 0.00 MiB | 0.01 MiB | 0.02 MiB | 0.09 MiB |

> **数据集维度的关键限制**：PG 侧数据集占用（466.27 MiB）**远小于**该数据集在 ClickHouse 与 MinIO 的真实体量。例如 `token_transfers` 在 ClickHouse 占 **4.51 GiB**、`transactions` 占 **5.62 GiB**，而这些在 PG 的 `dataset` 列上只体现为行数。因此 **PG 的 dataset 维度预测不能代表端到端数据集容量**；端到端需 CH 的 `chain`+`partition` 维度（见 §2.3）与 MinIO 的对象前缀维度（当前 raw 桶未按 dataset 分前缀，**不可得**）。

---

## 6. 保留策略约束：仅已批准 policy 下才执行 lifecycle

### 6.1 计划原文（`docs/dual-system-development-plan.md` §30.3，第 2471 行起）

> 以下为**逐字引用**，来自 `E:\Code\远程虚拟机\harness\blockchain-platform\docs\dual-system-development-plan.md` 第 2471–2478 行（部署机同文件路径 `/home/euripides/projects/blockchain-platform/docs/dual-system-development-plan.md`，行号一致）：

```markdown
### 30.3 存储容量

- 按 organization/workspace/dataset 统计每日新增 rows/bytes 和 30/90/365 天预测。
- Login/Audit 分区建立前估算事件数：用户请求量 × 审计采样/完整记录率。
- MinIO lifecycle 仅在已批准 retention policy 下执行；Raw、Evidence、Report、Audit 使用不同 bucket/prefix policy。
- PostgreSQL 分区归档后验证查询和审计导出仍能跨在线/归档范围工作。
```

**本文档对上述第 1、3 条的落实**：第 1 条见 §1（PG 日增）与 §5（三维度 30/90/365 预测）；第 3 条见 §6.3（实测 lifecycle 全未配置、策略全未启用）。第 2 条（Login/Audit 分区前的容量估算）见 §6.4。

### 6.2 `retention_policies` 实际内容（`enabled` 全部为 `false`）

```sql
SELECT policy_key, resource_type, scope_type, retain_days, archive_after_days, action, grace_days, legal_hold, enabled
  FROM retention_policies ORDER BY policy_key;
```

| policy_key | resource_type | scope | retain_days | archive_after_days | action | grace_days | legal_hold | **enabled** |
|---|---|---|---:|---:|---|---:|---|---|
| `audit-archive-default` | `audit_archive` | global | 2555 | 365 | archive | 30 | t | **f** |
| `cache-objects-default` | `cache_object` | global | 30 | NULL | delete | 3 | f | **f** |
| `clickhouse-partition-default` | `clickhouse_partition` | global | 730 | 365 | archive | 30 | f | **f** |
| `export-objects-default` | `export_object` | global | 90 | NULL | delete | 7 | f | **f** |
| `raw-objects-default` | `raw_object` | global | 365 | 90 | archive | 7 | f | **f** |
| `user-activity-default` | `user_activity` | global | 90 | 90 | archive | 7 | f | **f** |

**基线来源**：`backend/migrations/postgres/0052_s6_data_governance.sql:185-192`，原文注释：

```sql
-- 4. 基线数据（幂等）：策略默认全部 enabled=false，删除动作默认关闭。
```

### 6.3 治理开关与 MinIO lifecycle 实测状态

```sql
SELECT key, value::text FROM configuration_settings WHERE key LIKE 'governance%';
-- governance.deletion.execution_enabled   | false
-- governance.retention.scheduler_enabled  | false
```

MinIO 侧（只读 S3 API `GET /<bucket>?lifecycle`）：

| 桶 | lifecycle 查询结果 |
|---|---|
| `blockchain-audit` | HTTP **404** `NoSuchLifecycleConfiguration` — **不存在 lifecycle 配置** |
| `blockchain-backups` | HTTP **404** `NoSuchLifecycleConfiguration` — **不存在 lifecycle 配置** |
| `blockchain-exports` | HTTP **404** `NoSuchLifecycleConfiguration` — **不存在 lifecycle 配置** |
| `blockchain-raw` | HTTP **404** `NoSuchLifecycleConfiguration` — **不存在 lifecycle 配置** |
| `blockchain-reports` | HTTP **404** `NoSuchLifecycleConfiguration` — **不存在 lifecycle 配置** |
| `blockchain-reports-drill-20260912t175029z` | HTTP **404** `NoSuchLifecycleConfiguration` — **不存在 lifecycle 配置** |

### 6.4 约束结论（对容量预测的直接影响）

1. **当前不存在任何正在执行的保留/生命周期动作**：
   - MinIO：6 个桶**全部无 lifecycle 配置**（404 实测）；
   - ClickHouse：9 张表**迁移中无任何 `TTL` 子句**（grep 实测）；
   - PostgreSQL：`retention_policies` 6 条基线 **`enabled` 全为 `false`**，`governance.retention.scheduler_enabled = false`，`governance.deletion.execution_enabled = false`。
2. **因此本文 §4 的全部预测都是「无界增长」口径**——即所有数据永久保留。任何一条策略被批准并启用后，预测需要重算。
3. **「仅已批准 retention policy 下才执行 lifecycle」的落地约束**：
   - MinIO lifecycle 配置**不得**在本环境手工添加；必须先有 `retention_policies` 中对应 `resource_type`（`raw_object` / `export_object` / `cache_object`）的**已批准且 `enabled=true`** 的策略行。
   - 计划 §30.3 明确要求 **Raw、Evidence、Report、Audit 使用不同 bucket/prefix policy**。当前 bucket 已按用途分离（`blockchain-raw` / `blockchain-exports` / `blockchain-reports` / `blockchain-audit` / `blockchain-backups`），但**均未配置 policy**，即分离只做到 bucket 层，**尚未做到 policy 层**。
   - PostgreSQL 分区归档：`backend/migrations/postgres/0057_s7_indexes_partitions.sql:123` 的 `public.s7_partition_archive_candidates(p_retention_days int DEFAULT 90)` 是**只读 `STABLE` 函数**，其注释原文为「本函数不执行任何 DETACH/DROP —— 实际删除在 S6 保留策略开关打开前保持关闭」。
   - ClickHouse：`clickhouse-partition-default`（730 天）未启用前**不得** `DROP PARTITION`。
4. **容量治理的实际缺口**：`retention_policies` 只覆盖 7 类资源（`raw_object` / `export_object` / `user_activity` / `audit_archive` / `clickhouse_partition` / `cache_object` / `job_artifact`），而 PostgreSQL 有 **149 张表**，其中 **126 张表的保留期「未定义」**（见 `docs/data-dictionary.md` §2.2）。仅靠现有策略无法约束 PG 主体增量。

### 6.5 §30.3 第 2 条：Login / Audit 分区建立前的容量估算

计划原文：「Login/Audit 分区建立前估算事件数：用户请求量 × 审计采样/完整记录率。」

**实测基础数据**：

| 项 | 实测值 | 出处 |
|---|---|---|
| `login_events` 总行数 / 活跃天数 | 3,275 行 / 2 天（2026-09-11 19:00 起） | §1.2 窗口汇总 |
| `audit_events_v2` 总行数 / 活跃时长 | 48,587 行 / 7.14 小时（2026-09-12 14:42 起） | §1.5 (a-2) |
| `audit_events_v2` 中比例最高的动作 | `admin:login-event:read` 22,174 次（45.6%）、`admin:audit:read` 17,898 次（36.8%） | `SELECT action, count(*) ... GROUP BY 2` |
| `audit_events_v2` 稳态写入率（剔除 18:00 突发） | ≈ 354 行/h → 8,493 行/日 | §1.5 (a-2) |
| `login_events` 稳态写入率 | 109 行/7 日窗口 → 约 16 行/日；09-11 单日 3,166 行 | §1.4 |

**结论**：

- **无法**按计划原文的「用户请求量 × 审计采样率」估算——缺少「用户请求量」的实测计数器（无 API 请求总量 metric 表的读取证据；`provider_usage` 只覆盖 Provider 外部调用，不含平台 API 请求）。
- **可用的替代口径**：以 `audit_events_v2` 稳态 8,493 行/日、平均行宽 1,417 B 计，**≈ 11.48 MiB/日**；99 分位突发（18:00 单小时 46,565 行）折算 **≈ 62.93 MiB**。
- **分区建议**：若按月分区，单月稳态约 **0.34 GiB**，含一次突发约 **2.2 GiB**。按当前 `/data` 余量（923.39 GiB）无压力；风险不在容量而在**突发写入造成的瞬时 IO**。
- `user_activity_events` **已建好月分区框架**（202609/202610/202611 + hist + default，共 5 个分区，全部 0 行），但 `login_events` 与 `audit_events_v2` **尚未分区**（`pg_class.relkind='r'`，见 `docs/data-dictionary.md` §5.1）。

---

## 7. 未能闭合项 / 不可得项（如实清单）

| # | 项目 | 状态 | 原因 | 已尝试的命令 |
|---:|---|---|---|---|
| 1 | PostgreSQL **历史体积快照**（逐日 `pg_total_relation_size`） | **不可得** | PostgreSQL 不保留体积历史。`stats_reset` 为 NULL **只表示当前未记录/无法判断窗口起点**，**不能**据此断言「从未重置」（Revision 2 修正）；成因本次未验证 | `SELECT stats_reset FROM pg_stat_database WHERE datname='blockchain_platform'` → **NULL**；未再尝试 `pg_stat_get_db_stat_reset_time()` 等替代接口 |
| 2 | PostgreSQL 目录**逐目录分解与总占用** | **已闭合（Revision 2）** | — | `docker exec -u 0 deploy-postgres-1 sh -c 'du -sh /var/lib/postgresql; for d in /var/lib/postgresql/*; do du -sh "$d"; done'` → 24 GiB @22:0x / 28,497,407,919 B @22:12:25，含 8 个 `pitr-*` 与 `wal-archive` 的逐项体积 |
| 2b | PostgreSQL 目录的**逐日增长序列** | **不可得** | 无历史 `du`/`df` 快照；本次仅有 21:49 与 22:12 两个时点，且该区间被演练污染 | 两个时点 `df -B1 /data`（60.14 GiB → 80.47 GiB）；`du -sb` 各组件 |
| 2c | **NATS `jetstream` 占用** | **仍不可得** | 只对 PostgreSQL 用了容器 root 读取，未对 NATS 容器做同等读取 | `du -sh /data/blockchain-platform/nats` → `Permission denied`；未尝试 `docker exec -u 0 deploy-nats-1 du -sh /data` |
| 3 | ClickHouse 目录 `du`（37.93 GiB）与活跃 part（16.02 GiB）的**差额归因** | **已闭合（Revision 2）** | — | `du -sb /data/blockchain-platform/clickhouse/*` → `store/` 21.90 GiB + **`backups/20260912T171120Z/` 16.03 GiB（一次性备份副本）** + 其他 <1 MiB。差额主因是**备份副本**，Revision 1 归因为「非活跃/detached」**不正确** |
| 3b | `store/`（21.90 GiB）与活跃 part（16.02 GiB）之差 **≈5.9 GiB** | **未闭合** | 该差额应为非活跃 part 或 `detached/`，但逐目录 `du store/*/*/detached` 实测**全部为 0**，故实际构成未查明 | `du -sb /data/blockchain-platform/clickhouse/store*`；`du -sb .../store/*/*/detached`（全为 0） |
| 3c | `du -sh` = 39G 与 `du -sb` = 37.93 GiB **读数不自洽** | **未闭合** | 同一目录两种 `du` 口径差约 1 GiB；未查明 | `du -sh` 与 `du -sb` 在同一分钟内对同一目录执行 |
| 4 | MinIO `blockchain-raw` 对象前缀 / 数据集维度 | **不可得** | 需 `mc ls --recursive` 级前缀统计；`mc` 在本机与容器内均不存在，且 raw 桶对象键前缀结构未通过 S3 API 逐前缀聚合（会显著增加读取量） | `which mc` / `ls /usr/local/bin/mc` / `ls /usr/bin/mc` / `docker exec deploy-minio-1 which mc` 全部失败；改用 `ListObjectsV2` 仅取对象数与 `Size` |
| 5 | 「用户请求量」实测计数 | **不可得** | 无平台 API 请求总数的时间序列表；`provider_usage` 仅覆盖对外 Provider 调用 | `grep` 于表清单未见 `api_requests`/`http_requests` 类表；`information_schema.tables` 全量核对 |
| 6 | 各 organization / workspace 的**独立日增时间序列** | **不可得** | 归因表（`audit_events_v2`）全部数据集中在一个小时内，无法形成分组织日序列 | §1.5 (a-2) 小时分布实测；§5.2/§5.3 采用「按当前占比分摊」的显式假设 |
| 7 | MinIO **真实净增**（写入 − 删除 − 覆盖） | **不可得（Revision 2 强化）** | 三个原因叠加：① `ListObjectsV2` 是 **survivor/current-version 口径**，不含已删除对象；② 6 个桶**全部未启用版本控制**，无历史版本；③ MinIO **未开启审计日志/指标采集**，无删除与覆盖写记录 | S3 `ListObjectsV2` 按 `LastModified` 分组；`GET ?versions`（未启用）；`GET ?versioning`（空配置）；未找到 MinIO audit webhook 配置 |
| 7b | MinIO 已删除对象的**累计体积** | **不可得** | 无删除日志；`.minio.sys` 下的 `usage-cache.bin` 为二进制缓存，未解析 | `ls /data/blockchain-platform/minio/.minio.sys/`（存在 `.usage-cache.bin`、`.bloomcycle.bin`，**未解析**） |
| 8 | 生产负载下的增长率 | **不可得** | 当前 13 天数据全部来自开发/验收负载，不能代表生产 | 无生产流量证据；本文所有预测均标注为「当前负载口径」 |
| 9 | `docs/manuals/` 与 `RELEASE-DECISION.md` | **不存在** | 部署机无此目录/文件 | `ls -la docs/`（只有 `acceptance/`、`adr/`、`implementation/` 与若干 md）；`ls -la docs/manuals` → `No such file or directory`；`ls -la RELEASE-DECISION.md` → `No such file or directory` |
| 10 | `login_events` / `audit_events_v2` 的分区方案 | **未实施** | 两表仍为普通基表（`relkind='r'`） | `pg_class.relkind` 实测；§6.5 |
| 11 | **WAL 归档速率是否长期成立**（决定 33 天 vs 365 天） | **NOT VERIFIED（Revision 2 新增，最关键）** | 实测 20.98–27.03 GiB/d（§4.6），但：① 该配置是否为 PITR 演练临时值未知；② 公告的清理责任方不存在（crontab/timer/脚本均无命中）；③ `archive_command` 是 `cp` 而不删除源，但归档目录**是否被演练脚本或人工清理**无法确认 | `pg_settings` 的 `archive_mode/archive_command/archive_timeout`；`ls -la --time-style=full-iso wal-archive`；`crontab -l`；`ls /etc/cron.d/`；`systemctl list-timers --all`；`ls scripts/ | grep -iE 'pitr|backup|wal|restore'` |
| 12 | **ClickHouse 真实净增**的长期速率 | **部分可得 / 有假设** | N2（两点快照）仅 20 分钟且为空闲期（净增 0）；N1（全生命周期 1.29 GiB/d）依赖「起始为空」假设，**该假设未 VERIFIED**（§2.5b） | `SELECT sum(bytes_on_disk), sum(rows) FROM system.parts WHERE active AND database='blockchain'`（T_A、T_B 两次）；`SELECT min(modification_time) FROM system.parts WHERE active` |
| 13 | PITR 演练目录的**清理责任方与保留期限** | **NOT VERIFIED** | 8 个 `pitr-*` 目录（≈22.45 GiB）+ 1 个 CH 备份目录（16.03 GiB）无任何调度清理机制 | `ls -la /data/blockchain-platform/postgres/`；`ls /etc/cron.d/`；`systemctl list-timers --all`；`ls scripts/ | grep -iE 'pitr|backup'` → 无命中 |
| 14 | **RPO/RTO 目标值**与备份保留窗口 | **NOT VERIFIED** | §4.7 敏感性公式中的 `f`（备份频率）、保留窗口、副本数均无出处；`retention_policies` 7 类资源中**无 PG 备份类**（`raw_object`/`export_object`/`user_activity`/`audit_archive`/`clickhouse_partition`/`cache_object`/`job_artifact`） | `SELECT resource_type FROM retention_policies GROUP BY 1`；未在代码或文档中找到 RPO/RTO 定义 |

---

## 8. 附录：完整命令与真实输出

### A. 连接方式（不记录任何口令明文）

```bash
cd /home/euripides/projects/blockchain-platform
export PGPASSWORD=$(grep -m1 '^POSTGRES_PASSWORD=' deploy/.env | cut -d= -f2-)
docker exec -e PGPASSWORD="$PGPASSWORD" deploy-postgres-1 \
  psql -U blockchain -d blockchain_platform -At -F "$(printf '\t')" -c "<SQL>"

export CH_USER=$(grep -m1 '^CLICKHOUSE_USER=' deploy/.env | cut -d= -f2-)
export CH_PASS=$(grep -m1 '^CLICKHOUSE_PASSWORD=' deploy/.env | cut -d= -f2-)
docker exec deploy-clickhouse-1 clickhouse-client --user "$CH_USER" --password "$CH_PASS" -q "<SQL>"
```

### B. 实例与时钟

```
$ psql -Atc "SELECT now()::text, pg_postmaster_start_time()::text"
2026-09-12 21:49:04.455254+00 | 2026-09-12 21:09:51.014025+00

$ clickhouse-client -q "SELECT version()"
26.7.5.10
```

### C. PostgreSQL 组合级逐日实测（§1.3 原始结果）

```
日期        | 新增行数 | 折算字节     | MiB
2026-08-31  |   68,743 |   44,364,958 |  42.31
2026-09-01  |   14,514 |    8,366,316 |   7.98
2026-09-02  |    7,863 |    3,176,909 |   3.03
2026-09-03  |   25,118 |   12,575,928 |  11.99
2026-09-04  |   53,731 |   33,572,293 |  32.02
2026-09-05  |  270,499 |  173,202,358 | 165.18
2026-09-06  |   63,777 |   43,202,265 |  41.20
2026-09-07  |   52,029 |   33,719,877 |  32.16
2026-09-08  |  275,907 |  199,157,470 | 189.93
2026-09-09  |   22,063 |   13,188,384 |  12.58
2026-09-10  |  196,781 |  132,943,142 | 126.78
2026-09-11  |   11,130 |    3,769,554 |   3.59
2026-09-12  |  120,507 |  163,053,472 | 155.50   ← 不完整日（21.817h）

12 完整日: mean = 55.73 MiB/d ; median = 32.09 MiB/d ; max = 189.93 MiB/d
近 7 完整日: mean = 81.63 MiB/d ; median = 41.20 MiB/d ; max = 189.93 MiB/d
```

### D. ClickHouse `part_log` 原始输出（§2.2）

```
event_date  event_type  n      bytes
2026-08-31  NewPart     19929   2743967577
2026-09-01  NewPart      1061    286761803
2026-09-02  NewPart        85     22112683
2026-09-03  NewPart      1786    586688852
2026-09-04  NewPart      8524   1685685287
2026-09-05  NewPart     18056  11511999480
2026-09-06  NewPart       624     15342078
2026-09-07  NewPart      2884     68104435
2026-09-08  NewPart     33329   2453446769
2026-09-09  NewPart       812     21430641
2026-09-10  NewPart     29566   3063601508
2026-09-11  NewPart       106      506008
2026-09-12  NewPart       302     62796127
```

### E. MinIO 只读 S3 API 原始输出（§3）

```
LISTBUCKETS status 200
BUCKETS: ['blockchain-audit', 'blockchain-backups', 'blockchain-exports', 'blockchain-raw',
          'blockchain-reports', 'blockchain-reports-drill-20260912t175029z']

=== blockchain-raw
  versioning[200]: <VersioningConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"></VersioningConfiguration>
  lifecycle[404]:  <Error><Code>NoSuchLifecycleConfiguration</Code>...
  encryption[404]: ServerSideEncryptionConfigurationNotFoundError
  objectlock[404]: ObjectLockConfigurationNotFoundError
  objects=170515 total_bytes=15058046314 total_pretty=14360.47 MiB
```

### F. 磁盘

```
$ df -B1 /
/dev/mapper/ubuntu--vg-ubuntu--lv  157396975616 85457428480  63869706240  58% /
$ df -B1 /data
/dev/mapper/ubuntu--vg-data_lv    1105357205504 64576253952 991482462208   7% /data
$ du -sh /data/blockchain-platform/*
39G     /data/blockchain-platform/clickhouse
16G     /data/blockchain-platform/minio
24K     /data/blockchain-platform/postgres    # 部分子目录 Permission denied
```

### G. 本文档的副作用声明

- Windows 工作树：**仅新增** `docs/data-dictionary.md` 与 `docs/capacity-forecast.md`；未 commit / reset / checkout / clean，未删除任何文件。
- 部署机：未写入任何文件、未新增或删除容器、未重启任何服务。
- 数据库：仅 `SELECT` 与系统表查询；ClickHouse 仅 `SELECT`；MinIO 仅 HTTP `GET`。

