# 数据字典（Data Dictionary）— 区块链取证平台

> **生成方式**：本文件由工程代理在部署机上以**只读**方式实时 introspect 生成，非凭记忆撰写。
> 所有行数、体积、约束、索引、分区、ClickHouse/MinIO 清单均来自实际执行的命令输出（见 §11 附录）。
> 生成时间（数据库时钟，UTC）：**2026-09-12 21:49:04 UTC**；PostgreSQL 实例启动时间：2026-09-12 21:09:51 UTC。

## 修订说明（Revision 2，独立复核后修正）

本文件为 **Revision 1**（首版，sha256 `5372a200a4fc16205f4626afa143abc0a1e434dee584590b7f89ab3ecdfa0df8`）的修订版。独立复核提出 6 项口径问题，逐条修正如下；**原始采样与附录全部保留**，仅在被指出的位置修正并补充新证据。

| # | 复核意见 | 本文件中的修正位置 | 修正内容 |
|---:|---|---|---|
| 1 | CH `part_log` NewPart 是 gross 写入量，不能当"净增长" | §5.5、§6 | `NewPart` 明确标注为 **gross INSERT 写入量**；净增长另列两点实测（§6.5）；**不再由该量推导任何耗尽日** |
| 2 | PG 13.09× 含 PITR 演练一次性占用，不可外推 | §0.1、§1.3 | 删除 11 GiB / 13.09× 表述，改为实测 **24 GiB @22:0x / 26.54 GiB @22:12** 并给出**逐目录分解**，区分「在线 PGDATA 2.90 GiB」与「PITR 演练一次性 ≈20–22 GiB（随演练增长）+ WAL 归档 1.14–1.19 GiB」 |
| 3 | MinIO `LastModified` 是 survivor/current-version 口径 | §1.5、§7.1 | 明确标注为 **survivor / current-version 口径**，不含已删除对象与历史版本 |
| 4 | "CH 无 TTL" 不能只靠 grep migrations | §5.5、§6 | 补 **在线部署 DDL 实测**（`system.tables.create_table_query`），原文片段贴入；migration grep 降级为佐证 |
| 5 | `stats_reset=NULL` 不证明从未重置 | §10 第 7 项 | 改为「当前未记录 / 不可判断」，不再断言"从未重置" |
| 6 | 「保留期」未定义部分不算治理已落地 | §2.2、§3 | 新增「**文档交付** vs **治理能力 NV**」分离陈述，引用 `data_asset_registry` 仅 17/149 表的实测事实 |

**修订引入的新实测证据（UTC 2026-09-12 22:04–22:10）**：

```
$ docker exec -u 0 deploy-postgres-1 sh -c 'du -sh /var/lib/postgresql; for d in /var/lib/postgresql/*; do du -sh "$d"; done'
24G     /var/lib/postgresql
3.0G    /var/lib/postgresql/18
4.0K    /var/lib/postgresql/pitr-base-20260912T211721Z
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

> 复核意见 2 成立：该体积中 **约 20–22 GiB 是 2026-09-12 当天 PITR 演练产生的 base/clone/frozen 目录**（时间戳 21:17–22:04 UTC 连续出现 8 个，22:12 实测已达 **22.45 GiB**），属一次性历史占用，**不得外推为日增长率**。原始 24 GiB 采样保留于上方。

---

## 0. 元信息与方法

### 0.1 采集环境

| 项 | 值 | 证据 |
|---|---|---|
| Windows 工作树 | `E:\Code\远程虚拟机\harness\blockchain-platform`（git dirty，本次不改动仓库状态） | — |
| 部署机 | `euripides@192.168.31.152`，`hostname=dataserver`，`Linux 6.8.0-139-generic x86_64` | `hostname; uname -a` |
| 部署目录 | `/home/euripides/projects/blockchain-platform`（无 `.git`） | `ls -la` |
| PostgreSQL | `postgres:18` → `PostgreSQL 18.6 (Debian 18.6-1.pgdg13+2)`，容器 `deploy-postgres-1` | `SELECT version()` |
| ClickHouse | `clickhouse/clickhouse-server:26.7` → `26.7.5.10`，容器 `deploy-clickhouse-1` | `SELECT version()` |
| MinIO | 容器 `deploy-minio-1`（`minio/minio:latest`） | `docker ps` |

### 0.2 口径定义（重要）

- **行数（exact）**：对 `public` schema 下全部 149 个基表/分区执行 `SELECT count(*)` 的**精确值**，通过单条 SQL 的 `query_to_xml` 逐表生成，非估算。
- **行数（reltuples）**：`pg_class.reltuples` 的**规划器估算值**，仅在 `ANALYZE` 后准确；`-1` 表示该关系从未被分析。两列并列给出，便于识别估算偏差。
- **大小**：`pg_total_relation_size(oid)`（堆 + TOAST + 索引）/ `pg_relation_size`（堆）/ `pg_indexes_size`（索引），单位字节，展示为 IEC 单位。
- **保留期**：优先取治理登记表 `data_asset_registry.retention`；否则取 `retention_policies` 基线；两者皆无则记为 **未定义**。
- **敏感级别**：优先取 `data_asset_registry.sensitivity`（枚举 `internal` / `confidential` / `restricted`）；否则按列名规则推断并**显式标注推断依据**。
- **数据所有者领域**：优先取 `data_asset_registry.owner_module`；否则对 `backend/internal/<domain>` 做 `grep -rl -w <表名>` 取非通用包的最高命中域，并**原样附上命中统计**。
- 通用包（`store` / `api` / `model` / `config`）不作为优先领域，仅在无业务包命中时使用。

### 0.3 只读与合规声明

本次采集**仅执行** `SELECT` / `information_schema` / `pg_catalog` / `clickhouse-client` 只读查询 / `mc` 等价只读 S3 API（`GET ListObjectsV2`、`GET ?versioning`、`GET ?lifecycle`）/ `docker ps|exec`（只读命令）/ `df` / `du` / `ls` / `cat` / `grep`。
**未执行**任何 `INSERT`/`UPDATE`/`DELETE`/DDL、未重启任何服务或容器、未执行 `docker stop|rm`、未写入生产库、未删除任何文件。

---

## 1. 存储全景

### 1.1 PostgreSQL

```sql
SELECT pg_size_pretty(pg_database_size('blockchain_platform')) AS db_size,
       pg_database_size('blockchain_platform') AS bytes;
```

实测结果：`861 MB | 902608575`（即 **902,608,575 字节 / 860.80 MiB**）。

| 指标 | 实测值 |
|---|---|
| public schema 基表数量（relkind='r'） | **148** |
| 分区父表数量（relkind='p'） | **1**（`user_activity_events`） |
| 合计统计对象（含分区） | **149** |
| 149 张表 `pg_total_relation_size` 之和 | **888,438,784 字节 / 847.28 MiB** |
| 149 张表精确行数之和 | **1,195,688** |
| public 索引数量 | **377**（其中 UNIQUE 索引 208） |
| 约束数量 | **2200**：PRIMARY KEY 149、UNIQUE 47、FOREIGN KEY 172、CHECK 97、NOT NULL 1735 |
| 数据库角色 | `blockchain`（超级用户态拥有者）、`blockchain_api`、`blockchain_app` |
| 容器内数据目录 | `/var/lib/postgresql` → **24 GiB**（`docker exec -u 0 deploy-postgres-1 sh -c 'du -sh /var/lib/postgresql'`，UTC 2026-09-12 22:0x） |
| ├ 在线 PGDATA `18/docker` | **3,113,433,345 B（2.90 GiB）**（含 `pg_wal` 225 MiB / 17 段） |
| ├ PITR 演练一次性目录 | **约 20.7 GiB**：`pitr-base-*` × 5（2.7G×4 + 4.0K）、`pitr-cloneA-*` × 2（2.8G×2）、`pitr-clone-rpo1-*` × 1（2.9G）、`pitr-frozen-*` × 4（33M+49M+129M+49M） |
| └ WAL 归档 `wal-archive` | **1,224,738,132 B（1.14 GiB）**，77 个 16 MiB 段 |

> 说明：`pg_total_relation_size` 之和（847.3 MiB）小于数据库总大小（860.80 MiB），差额为系统目录、TOAST 共享、空闲空间与 `schema_migrations` 之外的元数据开销。

### 1.2 磁盘水位（VM `/data` 分区）

```
$ df -h; df -B1 / /data
Filesystem                         Size  Used Avail Use% Mounted on
/dev/mapper/ubuntu--vg-ubuntu--lv  147G   80G   60G  58% /
/dev/mapper/ubuntu--vg-data_lv     1.1T   61G  924G   7% /data

Filesystem                            1B-blocks        Used     Available Use% Mounted on
/dev/mapper/ubuntu--vg-ubuntu--lv  157396975616 85457428480  63869706240  58% /
/dev/mapper/ubuntu--vg-data_lv    1105357205504 64576253952 991482462208   7% /data
```

> **注意**：`/data` 的 `df -h` 显示 `924G` 可用为 **GiB**（991,482,462,208 字节 ÷ 1024³ = 923.4 GiB）。容器数据卷均落在 `/data`（`compose.yaml` 中 postgres/clickhouse/minio/nats 均挂载 `/data/blockchain-platform/*`），`/` 分区另承载 `/var/lib/docker`。

| 分区 | 总容量 | 已用 | 可用 | 使用率 |
|---|---|---|---|---|
| `/data` | 1,105,357,205,504 B（1.01 TiB） | 64,576,253,952 B（60.14 GiB） | 991,482,462,208 B（923.39 GiB） | 5.84% |
| `/` | 157,396,975,616 B（146.59 GiB） | 85,457,428,480 B（79.59 GiB） | 63,869,706,240 B（59.48 GiB） | 54.29% |

ClickHouse `system.disks` 复核（同一文件系统视角）：

```
SELECT name, path, formatReadableSize(free_space), formatReadableSize(total_space) FROM system.disks
default  /var/lib/clickhouse/  923.39 GiB  1.01 TiB
```

### 1.3 `/data` 各组件磁盘占用

```
$ du -sh /data/blockchain-platform/*          # 以 euripides 身份：postgres/nats 子目录 Permission denied
39G     /data/blockchain-platform/clickhouse
16G     /data/blockchain-platform/minio
24K     /data/blockchain-platform/postgres
8.0K    /data/blockchain-platform/nats
4.0K    /data/blockchain-platform/backups
4.0K    /data/blockchain-platform/exports
4.0K    /data/blockchain-platform/pitr

$ du -sb /data/blockchain-platform/clickhouse   # 精确字节，UTC 2026-09-12 22:10:10
40716354145     (37.92 GiB)

$ docker exec -u 0 deploy-postgres-1 sh -c 'du -sh /var/lib/postgresql'   # 容器 root 身份：权限问题消除
24G     /var/lib/postgresql
```

> **口径修正（Revision 2）**：Revision 1 曾写「PostgreSQL 与 NATS 的真实磁盘占用**不可得**」。该结论**部分错误**——以 `euripides` 身份 `du` 确实触发 `Permission denied`，但改用 `docker exec -u 0`（容器内 root）即可完整读取。实测 PostgreSQL 卷 = **24 GiB**，其中**约 22.45 GiB 为 2026-09-12 当天 PITR 演练产生的一次性 base/clone 目录**（详见 §0.1 表与 `capacity-forecast.md` §4.2、§4.7）。**NATS 的 jetstream 目录仍不可得**（未以容器 root 读取）。

> **`du -sh` 与 `du -sb` 读数不一致（如实记录）**：`du -sh clickhouse` 显示 **39G**，而同一目录 `du -sb` = **40,716,354,145 B = 37.92 GiB**，二者不自洽（37.92 GiB 应为 38G）。本文件与 `capacity-forecast.md` 一律采用 **`du -sb` 精确字节值**；该显示差异**未闭合**（已列入 §10）。

> **口径说明（Revision 2 改写）**：ClickHouse 的 `du` 值包含**非活跃 part、`detached/`、`preprocessed/`、`tmp/`**，而 `system.parts WHERE active` 只统计**当前活跃 part**。二者**不是同一口径**，差额约 17 GiB **未进一步归因**。Revision 1 曾把该差额写成「约 17 GiB 来自非活跃/detached/preprocessed/tmp」，属**未经验证的归因推测**，此处改为如实表述。

### 1.4 ClickHouse 库级用量

```sql
SELECT database, formatReadableSize(sum(bytes_on_disk)) FROM system.parts WHERE active GROUP BY 1
```

| 库 | 活跃 part 磁盘字节 |
|---|---|
| `blockchain` | **16.02 GiB** |
| `system` | 5.75 GiB |

### 1.5 MinIO 总量

| 指标 | 实测值 |
|---|---|
| 桶数量 | **6** |
| 对象总数 | **170,675** |
| 对象字节总数（S3 `Size` 求和） | **15,063,390,193 B（14.03 GiB）** |
| 文件系统占用（`du -sh minio`） | 16 GiB |
| **口径（Revision 2 补充）** | **survivor / current-version 口径**：`ListObjectsV2` 只枚举**当前存活对象**，**不含已删除对象、不含历史版本**（6 个桶均未启用版本控制，故无版本条目）。因此 14.03 GiB 是**当前存量**，**不是全部历史写入净增** |
| 版本控制 | **全部 6 个桶均未启用**（`GET ?versioning` 返回空 `<VersioningConfiguration/>`） |
| ILM（lifecycle） | **全部 6 个桶均无 lifecycle 配置**（`GET ?lifecycle` → 404 `NoSuchLifecycleConfiguration`） |
| 服务端加密 / Object Lock | **全部未配置**（404 `ServerSideEncryptionConfigurationNotFoundError` / `ObjectLockConfigurationNotFoundError`） |

---

## 2. 数据所有者领域分布（149 张表）

| 领域 | 表数 |
|---|---|
| `store` | 30 |
| `api` | 24 |
| `investigation` | 13 |
| `policy` | 8 |
| `audit` | 8 |
| `provider` | 7 |
| `download` | 7 |
| `tokenmeta` | 7 |
| `report` | 5 |
| `case` | 5 |
| `iam` | 5 |
| `api(父表 user_activity_events 的分区)` | 5 |
| `method` | 3 |
| `countoracle` | 3 |
| `casework` | 3 |
| `subjectintel` | 2 |
| `notification` | 2 |
| `platform(企业预留)` | 2 |
| `semantic` | 2 |
| `dataset` | 2 |
| `governance(迁移内登记表)` | 1 |
| `export` | 1 |
| `canvas` | 1 |
| `platform(迁移/运维)` | 1 |
| `review` | 1 |
| `platform(迁移框架 `backend/migrations/migrations.go`)` | 1 |

### 2.1 敏感级别分布

| 敏感级别 | 表数 | 含义 |
|---|---|---|
| `restricted` | 18 | 审计链、认证事件、用户行为遥测：append-only，仅 SELECT/INSERT 授权 |
| `confidential` | 29 | 含 PII（email/remote_ip）或凭证/密钥字段（hash/secret_ref/ciphertext） |
| `internal` | 102 | 业务数据（含链上标识：address/tx_hash），不含直接 PII |

### 2.2 保留期覆盖情况

#### 2.2.1 文档交付（本数据字典的「保留期」列写的是什么）

本文件 §3 总览表与 §4 逐表章节中的「保留期」列，是**文档层面的登记/推断值**，来源为：

- **23** 张表有明确值，其中 **17** 张来自治理登记表 `data_asset_registry.retention`，**9** 张来自 `retention_policies` 基线映射（`raw_object` / `export_object` / `user_activity` / `audit_archive` / `clickhouse_partition` / `cache_object` 六类资源）。表数之和大于 23 是因为部分表同时命中两条来源。
- **126** 张表为「**未定义**」——迁移与代码中均无 retention / TTL / 分区定义。已对 `backend/migrations/postgres` 执行：

  ```
  $ grep -rn -i -E "retention|ttl|partition by|older than|interval '" backend/migrations/postgres
  ```

> **「未定义」不等于「无保留义务」，也不等于「已落地治理」。** 它只表示**文档与代码中都找不到该表的保留期定义**。

#### 2.2.2 治理能力（Revision 2 新增：与本数据字典的交付物分开陈述）

| 治理能力维度 | 实测状态 | 证据 |
|---|---|---|
| 保留策略**已登记** | 6 条（`retention_policies`） | `SELECT policy_key, enabled FROM retention_policies` |
| 保留策略**已启用** | **0 条**（6 条 `enabled` 全为 `false`） | 同上 |
| 保留调度器 | **关闭** | `configuration_settings.governance.retention.scheduler_enabled = false` |
| 删除执行 | **关闭** | `configuration_settings.governance.deletion.execution_enabled = false` |
| MinIO lifecycle | **6 个桶全部未配置**（HTTP 404 `NoSuchLifecycleConfiguration`） | `GET /<bucket>?lifecycle` |
| ClickHouse TTL | **9 张表全部无 TTL**（在线 DDL 实测，见 §5.5） | `system.tables.create_table_query` |
| 表级治理登记覆盖率 | **17 / 149 = 11.4%** | `SELECT count(*) FROM data_asset_registry` = 17 |

> **结论（治理能力 NV）**：**治理能力当前处于 NOT VERIFIED / 未生效状态**。本数据字典**交付的是「保留期文档」**，不是「保留期已落地」。
>
> 具体而言：本文件写「保留期 = 365 天」**仅表示文档有此登记**；该 365 天**没有**任何调度器/任务在执行，**没有任何数据因该登记而被归档或删除**。132 张表在治理层面**完全无登记**（`data_asset_registry` 仅 17 行），其"未定义"是**治理缺口**，不是数据字典的遗漏。

> **关键约束（与容量预测直接相关）**：`retention_policies` 表内 6 条基线策略的 `enabled` **全部为 `false`**；`configuration_settings` 中 `governance.retention.scheduler_enabled = false`、`governance.deletion.execution_enabled = false`。即**当前没有任何保留策略在实际执行**，所有表都在无界累积（详见 `docs/capacity-forecast.md` §0.4、§6）。**注意**：本数据字典写出的保留期是**文档交付**，不等于**治理能力已落地**——见本文件 §2.2.2。

---

## 3. 表总览（149 张，按总大小降序）

> 排序：`pg_total_relation_size` 降序。行数为 `count(*)` 精确值。

| # | 表名 | 中文用途 | 领域 | 敏感级 | 保留期 | 精确行数 | 总大小 |
|---:|---|---|---|---|---|---:|---:|
| 1 | [`provider_usage`](#provider-usage) | Provider 调用用量明细：每次 API 调用的字节数、TTFB/body 耗时、错误与… | `download` | internal | 未定义 | 416,066 | 293.48 MiB |
| 2 | [`token_metadata_observations`](#token-metadata-observations) | Token 元数据原始观察：每次来源抓取的原始载荷哈希与观察时间。 | `tokenmeta` | internal | 未定义 | 233,792 | 149.66 MiB |
| 3 | [`download_pages`](#download-pages) | 下载页：单页请求/响应对象键、query_hash、翻页令牌哈希与各阶段时间戳。 | `download` | confidential | 未定义 | 165,502 | 121.66 MiB |
| 4 | [`audit_events_v2`](#audit-events-v2) | 审计事件 v2（append-only 哈希链）：occurred_at/actor/act… | `audit` | restricted | 已定义 | 48,586 | 65.66 MiB |
| 5 | [`job_event_log`](#job-event-log) | 任务事件重放日志：SSE Last-Event-ID 重放（§21.5），数据本体在 Min… | `api` | internal | 未定义 | 42,007 | 62.33 MiB |
| 6 | [`rpc_provider_health`](#rpc-provider-health) | RPC Provider 健康采样：延迟、成功率与采样时间序列。 | `store` | internal | 未定义 | 102,542 | 28.52 MiB |
| 7 | [`download_shards`](#download-shards) | 下载分片：Range 分片、租约（lease_until/lease_epoch）、工作量排… | `download` | internal | 未定义 | 28,385 | 25.93 MiB |
| 8 | [`token_metadata_resolve_queue`](#token-metadata-resolve-queue) | Token 元数据解析队列：重试次数、下次重试时间与去重键。 | `tokenmeta` | internal | 未定义 | 2,854 | 10.80 MiB |
| 9 | [`token_metadata`](#token-metadata) | Token 元数据主档：名称/符号/精度/logo 对象键/置信度与解析状态。 | `tokenmeta` | internal | 未定义 | 17,594 | 8.13 MiB |
| 10 | [`investigation_evidence`](#investigation-evidence) | 调查证据：来源引用、content_hash、采集与核验时间。 | `investigation` | internal | 未定义 | 7,127 | 7.39 MiB |
| 11 | [`download_volume_guard_commits`](#download-volume-guard-commits) | 体量护栏提交记录：护栏触发/放行的不可变提交。 | `store` | internal | 未定义 | 22,976 | 7.32 MiB |
| 12 | [`dataset_coverage`](#dataset-coverage) | 数据集覆盖度：每个数据集已完成的区间与提交时间（覆盖度收尾依据）。 | `api` | internal | 未定义 | 8,124 | 6.45 MiB |
| 13 | [`token_contract_catalog`](#token-contract-catalog) | Token 合约目录：首次/最近出现时间与来源。 | `tokenmeta` | internal | 未定义 | 17,108 | 6.19 MiB |
| 14 | [`investigation_steps`](#investigation-steps) | 调查步骤：单步子任务、输入输出哈希、耗时与数据集。 | `investigation` | internal | 未定义 | 347 | 6.09 MiB |
| 15 | [`checkpoints`](#checkpoints) | 断点/水位表：任务或数据集的处理水位与最后成功时间。 | `api` | internal | 未定义 | 19,975 | 5.39 MiB |
| 16 | [`download_datasets`](#download-datasets) | 下载数据集：数据集名（transactions/logs/token_transfers 等… | `download` | internal | 未定义 | 14,996 | 5.19 MiB |
| 17 | [`download_jobs`](#download-jobs) | 下载任务：目标地址/区间/模式（safe/fast/turbo）与抓取-入库各阶段时间。 | `download` | internal | 已定义 | 2,419 | 4.82 MiB |
| 18 | [`login_events`](#login-events) | 登录/认证事件（append-only）：成功/失败、MFA 方法与结果、哈希链字段。 | `audit` | restricted | 已定义 | 3,275 | 3.83 MiB |
| 19 | [`ankr_shard_profiles`](#ankr-shard-profiles) | Ankr 分片画像：数据集/链的分片耗时与体量画像。 | `store` | internal | 未定义 | 11,869 | 3.76 MiB |
| 20 | [`download_subjects`](#download-subjects) | 下载对象：任务下的地址/主体维度。 | `download` | internal | 未定义 | 9,245 | 3.17 MiB |
| 21 | [`ankr_benchmark_runs`](#ankr-benchmark-runs) | Ankr 基准运行：固定 target_block 的 1/2/4/8 Range 极限性能… | `store` | internal | 未定义 | 1,938 | 2.17 MiB |
| 22 | [`sessions`](#sessions) | 会话：令牌哈希、CSRF 哈希、MFA 满足标记、轮换链与吊销信息。 | `iam` | confidential | 未定义 | 1,757 | 2.08 MiB |
| 23 | [`graph_patches`](#graph-patches) | 图谱补丁：对画布/图谱的增量修改补丁。 | `investigation` | internal | 未定义 | 69 | 1.45 MiB |
| 24 | [`investigation_canvases`](#investigation-canvases) | 调查画布：图谱画布的元数据与归属。 | `canvas` | confidential | 已定义 | 90 | 1.37 MiB |
| 25 | [`audit_events`](#audit-events) | V1 审计事件表（0001 初版）：早期审计记录，V2 起由 audit_events_v2… | `report` | restricted | 未定义 | 3,565 | 1.26 MiB |
| 26 | [`investigation_findings`](#investigation-findings) | 调查发现（Finding）：结论、置信度、复核状态。 | `casework` | internal | 未定义 | 344 | 1.21 MiB |
| 27 | [`finding_evidence_links`](#finding-evidence-links) | 发现—证据关联：Finding 与 Evidence 的多对多引用。 | `investigation` | internal | 未定义 | 5,486 | 848.0 KiB |
| 28 | [`canvas_snapshots`](#canvas-snapshots) | 画布快照：画布状态序列化与版本。 | `report` | internal | 未定义 | 21 | 744.0 KiB |
| 29 | [`ankr_fetch_concurrency_runs`](#ankr-fetch-concurrency-runs) | Ankr 抓取并发运行：并发档位压测记录。 | `store` | internal | 未定义 | 840 | 624.0 KiB |
| 30 | [`api_control_actions`](#api-control-actions) | API 控制动作审计：人工/自动控制动作记录。 | `api` | internal | 未定义 | 776 | 416.0 KiB |
| 31 | [`case_activity`](#case-activity) | 案件业务时间线：用户可理解的案件活动流水。 | `case` | internal | 已定义 | 524 | 392.0 KiB |
| 32 | [`workspace_addresses`](#workspace-addresses) | 工作区地址目录：地址主档（首末数据时间、同步时间、备注）。 | `store` | internal | 未定义 | 414 | 336.0 KiB |
| 33 | [`exports`](#exports) | 导出作业：导出格式、对象键、SHA-256、过期与数据集归属。 | `export` | confidential | 已定义 | 243 | 328.0 KiB |
| 34 | [`outbox_events`](#outbox-events) | 事务性发件箱：与业务同事务落库、异步发布的领域事件（available_at/锁定/已发布）… | `investigation` | internal | 未定义 | 163 | 304.0 KiB |
| 35 | [`advanced_analysis_results`](#advanced-analysis-results) | 高级分析结果：聚类/路径分析等计算结果缓存。 | `investigation` | internal | 未定义 | 2 | 264.0 KiB |
| 36 | [`gpt_action_request_logs`](#gpt-action-request-logs) | GPT Action 请求日志：调用方、remote_ip、耗时与结果。 | `api` | confidential | 未定义 | 445 | 248.0 KiB |
| 37 | [`investigations`](#investigations) | 调查主表：调查目标（subject JSONB）、预算、状态，挂案件与工作区。 | `investigation` | confidential | 已定义 | 129 | 240.0 KiB |
| 38 | [`ankr_scheduler_state`](#ankr-scheduler-state) | Ankr 调度器状态：全局 Ready Queue 调度水位。 | `store` | internal | 未定义 | 353 | 208.0 KiB |
| 39 | [`count_provider_capabilities`](#count-provider-capabilities) | 计数 Provider 能力：支持的链/数据集与限制。 | `store` | internal | 未定义 | 14 | 200.0 KiB |
| 40 | [`ankr_management_connection`](#ankr-management-connection) | Ankr 管理端 MCP OAuth 连接：令牌密文/Nonce（加密后落库）与连接状态。 | `provider` | confidential | 未定义 | 1 | 192.0 KiB |
| 41 | [`investigation_reports`](#investigation-reports) | 调查报告：版本 revision、内容哈希、签名哈希与提交/审批状态。 | `report` | restricted | 已定义 | 68 | 192.0 KiB |
| 42 | [`method_resolve_queue`](#method-resolve-queue) | 方法解析队列：待解析 selector 与样本交易哈希。 | `store` | internal | 未定义 | 177 | 168.0 KiB |
| 43 | [`method_signatures`](#method-signatures) | 方法签名库：selector 与候选 signature（唯一键为 selector+sig… | `method` | internal | 未定义 | 107 | 168.0 KiB |
| 44 | [`count_provider_runtime_profiles`](#count-provider-runtime-profiles) | 计数 Provider 运行时画像：超时/并发等运行时参数。 | `store` | internal | 未定义 | 14 | 160.0 KiB |
| 45 | [`download_volume_guards`](#download-volume-guards) | TRON 地址体量护栏：数据集级地址体量守卫状态与审批。 | `store` | internal | 未定义 | 161 | 160.0 KiB |
| 46 | [`api_control_alerts`](#api-control-alerts) | API 告警：首次/最后出现、确认、静默与解决时间。 | `api` | confidential | 未定义 | 31 | 152.0 KiB |
| 47 | [`investigation_stages`](#investigation-stages) | 调查阶段：运行内的阶段划分与时间。 | `investigation` | internal | 未定义 | 347 | 152.0 KiB |
| 48 | [`ankr_fetch_queue_diffs`](#ankr-fetch-queue-diffs) | Ankr 抓取队列差异：队列前后快照差异。 | `store` | internal | 未定义 | 132 | 144.0 KiB |
| 49 | [`kill_switch_engagements`](#kill-switch-engagements) | Kill Switch 启用记录：启用/恢复、升级认证与复核期限。 | `api` | internal | 未定义 | 27 | 144.0 KiB |
| 50 | [`role_permissions`](#role-permissions) | 角色—权限绑定：授予时间与授予人。 | `policy` | internal | 未定义 | 563 | 136.0 KiB |
| 51 | [`approval_requests`](#approval-requests) | 双人审批请求：对象、动作、请求人、过期与执行状态。 | `api` | internal | 未定义 | 28 | 128.0 KiB |
| 52 | [`case_members`](#case-members) | 案件成员与角色。 | `case` | internal | 已定义 | 105 | 120.0 KiB |
| 53 | [`investigation_runs`](#investigation-runs) | 调查运行实例：一次执行的状态、目标、计划与时间。 | `investigation` | internal | 未定义 | 79 | 120.0 KiB |
| 54 | [`cases`](#cases) | 案件主表：案件编号/名称/密级/状态，按组织与工作区隔离。 | `case` | confidential | 已定义 | 116 | 112.0 KiB |
| 55 | [`export_tickets`](#export-tickets) | 导出下载票据：票据令牌、过期与使用状态。 | `store` | internal | 未定义 | 182 | 112.0 KiB |
| 56 | [`download_planning_decisions`](#download-planning-decisions) | 下载规划决策：分片规划依据记录（count oracle 结果等）。 | `store` | internal | 未定义 | 96 | 104.0 KiB |
| 57 | [`notifications`](#notifications) | 站内通知：收件主体、标题正文引用、已读时间。 | `notification` | internal | 已定义 | 58 | 104.0 KiB |
| 58 | [`audit_exports`](#audit-exports) | 审计导出作业：导出范围、文件对象键、哈希与过期时间。 | `audit` | restricted | 未定义 | 14 | 96.0 KiB |
| 59 | [`count_oracle_observations`](#count-oracle-observations) | Count Oracle 观测：对 (chain,dataset,range) 的计数观测与… | `store` | internal | 未定义 | 66 | 96.0 KiB |
| 60 | [`credentials`](#credentials) | 凭证（WebAuthn/TOTP 等）：仅存 secret_hash 与元数据，不存明文。 | `iam` | confidential | 未定义 | 42 | 96.0 KiB |
| 61 | [`member_role_bindings`](#member-role-bindings) | 成员角色绑定：主体在组织/工作区内的角色授予与撤销。 | `policy` | internal | 未定义 | 78 | 96.0 KiB |
| 62 | [`rpc_provider_capabilities`](#rpc-provider-capabilities) | RPC Provider 能力探测：能力项、检查时间与过期时间。 | `store` | internal | 未定义 | 98 | 96.0 KiB |
| 63 | [`approval_request_events`](#approval-request-events) | 审批请求事件流：请求/批准/拒绝/执行的时间线。 | `api` | confidential | 未定义 | 56 | 80.0 KiB |
| 64 | [`audit_integrity_incidents`](#audit-integrity-incidents) | 审计完整性事件：链断裂/锚点不匹配的封存记录与封印哈希。 | `audit` | restricted | 未定义 | 1 | 80.0 KiB |
| 65 | [`identities`](#identities) | 身份主体：登录账号（email/密码哈希/MFA/锁定）与平台内所有主体引用目标。 | `casework` | confidential | 未定义 | 27 | 80.0 KiB |
| 66 | [`review_requests`](#review-requests) | 复核请求：Finding/Evidence/报告提交复核的状态机与决策人。 | `review` | confidential | 已定义 | 21 | 80.0 KiB |
| 67 | [`semantic_projection_state`](#semantic-projection-state) | 语义投影状态：投影器水位与重算时间。 | `semantic` | internal | 未定义 | 2 | 80.0 KiB |
| 68 | [`session_reuse_events`](#session-reuse-events) | 会话令牌复用检测事件（令牌家族被盗用的取证线索）。 | `iam` | confidential | 未定义 | 22 | 80.0 KiB |
| 69 | [`token_metadata_backfill_jobs`](#token-metadata-backfill-jobs) | Token 元数据回填作业：进度、检查点与完成时间。 | `tokenmeta` | internal | 未定义 | 6 | 80.0 KiB |
| 70 | [`trusted_devices`](#trusted-devices) | 受信任设备：设备指纹、信任有效期与吊销状态。 | `iam` | internal | 未定义 | 33 | 80.0 KiB |
| 71 | [`permissions`](#permissions) | 权限点目录：permission_key、分组与风险级别。 | `policy` | internal | 未定义 | 177 | 72.0 KiB |
| 72 | [`address_dataset_sync_state`](#address-dataset-sync-state) | 地址级数据集同步水位：每个地址每个数据集已完整拉取到的区间。 | `store` | internal | 未定义 | 80 | 64.0 KiB |
| 73 | [`ankr_page_profiles`](#ankr-page-profiles) | Ankr 页画像：页面级耗时与用量画像。 | `store` | internal | 未定义 | 7 | 64.0 KiB |
| 74 | [`ankr_performance_control`](#ankr-performance-control) | Ankr 性能控制：当前 SAFE/FAST/TURBO 模式与切换时间。 | `store` | internal | 未定义 | 1 | 64.0 KiB |
| 75 | [`ankr_range_certifications`](#ankr-range-certifications) | Ankr Range 认证：Transactions 多 Range 认证结果。 | `download` | internal | 未定义 | 1 | 64.0 KiB |
| 76 | [`api_control_settings`](#api-control-settings) | API 控制中心单例设置：自动保护、Kill Switch、后台暂停与状态机。 | `tokenmeta` | internal | 未定义 | 1 | 64.0 KiB |
| 77 | [`audit_chain_heads`](#audit-chain-heads) | 审计链头：每个审计流（stream）的最新哈希与序号，用于链断裂检测。 | `audit` | restricted | 未定义 | 2 | 64.0 KiB |
| 78 | [`audit_integrity_incident_events`](#audit-integrity-incident-events) | 审计完整性事件明细（append-only 哈希链）。 | `audit` | restricted | 未定义 | 4 | 64.0 KiB |
| 79 | [`case_subjects`](#case-subjects) | 案件调查对象（地址/实体）与对象类型。 | `case` | confidential | 已定义 | 31 | 64.0 KiB |
| 80 | [`configuration_revisions`](#configuration-revisions) | 配置版本历史：前后哈希、操作者与是否密文。 | `api` | confidential | 未定义 | 21 | 64.0 KiB |
| 81 | [`contract_abis`](#contract-abis) | 合约 ABI 缓存：按地址缓存的 ABI 与抓取时间（当前无数据）。 | `method` | internal | 未定义 | 0 | 64.0 KiB |
| 82 | [`count_oracle_calibration_samples`](#count-oracle-calibration-samples) | 校准样本：单次观测的预测/实测对比与 Provider 维度。 | `store` | internal | 未定义 | 52 | 64.0 KiB |
| 83 | [`count_oracle_status`](#count-oracle-status) | Count Oracle 状态：各链/数据集的最新可用性与降级状态。 | `provider` | internal | 未定义 | 1 | 64.0 KiB |
| 84 | [`count_provider_attempts`](#count-provider-attempts) | 计数 Provider 尝试：请求哈希、凭证槽位、结果与耗时。 | `store` | confidential | 未定义 | 2 | 64.0 KiB |
| 85 | [`count_provider_registry`](#count-provider-registry) | 计数 Provider 注册表：名称、优先级、启停。 | `countoracle` | internal | 未定义 | 1 | 64.0 KiB |
| 86 | [`count_resolution_decisions`](#count-resolution-decisions) | 计数解析决策：最终选用的 Provider 与请求/主体哈希。 | `countoracle` | confidential | 未定义 | 4 | 64.0 KiB |
| 87 | [`entity_relationships`](#entity-relationships) | 实体关系：多主体 OSINT 下实体之间的边（关系类型与置信度）。 | `subjectintel` | internal | 未定义 | 1 | 64.0 KiB |
| 88 | [`event_signatures`](#event-signatures) | 事件签名库：topic0 与事件签名。 | `method` | internal | 未定义 | 19 | 64.0 KiB |
| 89 | [`feature_flag_evaluations`](#feature-flag-evaluations) | 功能开关评估记录：主体、结果与评估时间。 | `api` | internal | 未定义 | 42 | 64.0 KiB |
| 90 | [`gpt_action_api_keys`](#gpt-action-api-keys) | GPT Action API 密钥：仅存 key_hash，含过期/轮换/吊销。 | `api` | confidential | 未定义 | 6 | 64.0 KiB |
| 91 | [`investigation_subjects`](#investigation-subjects) | 调查对象（多主体）：一次调查可含多个被调查实体。 | `subjectintel` | internal | 未定义 | 12 | 64.0 KiB |
| 92 | [`report_approvals`](#report-approvals) | 报告审批记录：revision、审批人、内容哈希（证据级留存）。 | `report` | restricted | 已定义 | 21 | 64.0 KiB |
| 93 | [`retention_policies`](#retention-policies) | 保留策略：资源类型、retain_days/archive_after_days/actio… | `api` | internal | 未定义 | 6 | 64.0 KiB |
| 94 | [`address_tag_links`](#address-tag-links) | 地址—标签关联：地址打标（交易所/风险标签等）。 | `store` | internal | 未定义 | 86 | 56.0 KiB |
| 95 | [`report_download_tickets`](#report-download-tickets) | 报告下载票据：仅存 token 摘要，带过期与使用状态。 | `report` | restricted | 已定义 | 14 | 56.0 KiB |
| 96 | [`roles`](#roles) | 角色定义：按组织隔离的角色及内置标记。 | `iam` | internal | 未定义 | 9 | 56.0 KiB |
| 97 | [`ankr_management_oauth_state`](#ankr-management-oauth-state) | Ankr OAuth 状态：PKCE verifier 密文与 state_hash。 | `provider` | confidential | 未定义 | 0 | 48.0 KiB |
| 98 | [`ankr_production_profiles`](#ankr-production-profiles) | Ankr 生产画像：经认证的生产运行参数。 | `store` | internal | 未定义 | 1 | 48.0 KiB |
| 99 | [`audit_chain_anchors`](#audit-chain-anchors) | 审计链锚点：按日锚定链头哈希并落 content_hash 防篡改。 | `audit` | restricted | 未定义 | 4 | 48.0 KiB |
| 100 | [`audit_integrity_incident_anchors`](#audit-integrity-incident-anchors) | 完整性事件锚点：按日锚定事件链哈希。 | `audit` | restricted | 未定义 | 1 | 48.0 KiB |
| 101 | [`case_comments`](#case-comments) | 案件协作评论：正文与 body_hash。 | `case` | confidential | 已定义 | 16 | 48.0 KiB |
| 102 | [`configuration_settings`](#configuration-settings) | 配置项：值、是否密文、secret_ref 与版本。 | `api` | confidential | 未定义 | 8 | 48.0 KiB |
| 103 | [`feature_flags`](#feature-flags) | 功能开关：键、默认值与启停。 | `api` | internal | 未定义 | 6 | 48.0 KiB |
| 104 | [`method_backfill_jobs`](#method-backfill-jobs) | 方法回填作业：检查点交易哈希与进度。 | `store` | internal | 未定义 | 4 | 48.0 KiB |
| 105 | [`organizations`](#organizations) | 租户（组织）主表：平台最上层隔离单元，所有 workspace/case/audit 均挂在… | `investigation` | internal | 未定义 | 2 | 48.0 KiB |
| 106 | [`provider_descriptors`](#provider-descriptors) | Provider 描述符：能力、端点与最近测试结果。 | `api` | internal | 未定义 | 4 | 48.0 KiB |
| 107 | [`provider_secrets`](#provider-secrets) | Provider 密钥引用：只存 secret_ref 与轮换元数据，不存明文。 | `api` | confidential | 未定义 | 14 | 48.0 KiB |
| 108 | [`routing_policies`](#routing-policies) | 路由策略：Provider 路由规则（按链/数据集/成本权重）。 | `policy` | internal | 未定义 | 4 | 48.0 KiB |
| 109 | [`tags`](#tags) | 标签字典：标签键值、类型、颜色与组织/工作区归属。 | `dataset` | internal | 未定义 | 4 | 48.0 KiB |
| 110 | [`users`](#users) | 用户表（V1 单用户期遗留）：用户账号与所属组织；V2 起账号主体迁至 identities… | `dataset` | confidential | 未定义 | 1 | 48.0 KiB |
| 111 | [`workspace_memberships`](#workspace-memberships) | 工作区成员关系：身份在工作区中的成员状态与角色。 | `casework` | internal | 未定义 | 33 | 48.0 KiB |
| 112 | [`workspaces`](#workspaces) | 工作区：组织内的协作与数据隔离单元，地址/案件/导出按工作区归属。 | `policy` | internal | 未定义 | 3 | 48.0 KiB |
| 113 | [`provider_pricing`](#provider-pricing) | Provider 定价：按资源类型的单价与生效区间。 | `store` | internal | 未定义 | 2 | 40.0 KiB |
| 114 | [`rpc_providers`](#rpc-providers) | RPC Provider 控制面：名称、类型、启停与凭证引用（credential_ref）… | `provider` | confidential | 未定义 | 1 | 40.0 KiB |
| 115 | [`user_activity_events_202609`](#user-activity-events-202609) | 用户行为事件 2026-09 月分区。 | `api(父表 user_activity_events 的分区)` | restricted | 已定义 | 0 | 40.0 KiB |
| 116 | [`user_activity_events_202610`](#user-activity-events-202610) | 用户行为事件 2026-10 月分区。 | `api(父表 user_activity_events 的分区)` | restricted | 已定义 | 0 | 40.0 KiB |
| 117 | [`user_activity_events_202611`](#user-activity-events-202611) | 用户行为事件 2026-11 月分区。 | `api(父表 user_activity_events 的分区)` | restricted | 已定义 | 0 | 40.0 KiB |
| 118 | [`user_activity_events_default`](#user-activity-events-default) | 用户行为事件 DEFAULT 兜底分区（防止漏建分区导致写入失败）。 | `api(父表 user_activity_events 的分区)` | restricted | 已定义 | 0 | 40.0 KiB |
| 119 | [`user_activity_events_hist`](#user-activity-events-hist) | 用户行为事件历史分区（MINVALUE 至 2026-09-01）。 | `api(父表 user_activity_events 的分区)` | restricted | 已定义 | 0 | 40.0 KiB |
| 120 | [`ankr_sweet_spot_profiles`](#ankr-sweet-spot-profiles) | Ankr Sweet Spot 画像：最优并发/Range 档位记录。 | `store` | internal | 未定义 | 5 | 32.0 KiB |
| 121 | [`api_control_budgets`](#api-control-budgets) | API 预算：配额上限与窗口。 | `api` | internal | 未定义 | 16 | 32.0 KiB |
| 122 | [`chain_capabilities`](#chain-capabilities) | 链能力矩阵：各链支持的数据集与能力项。 | `provider` | internal | 未定义 | 8 | 32.0 KiB |
| 123 | [`count_oracle_calibration`](#count-oracle-calibration) | Count Oracle 校准参数：按链/数据集的校准系数。 | `countoracle` | internal | 未定义 | 3 | 32.0 KiB |
| 124 | [`data_asset_registry`](#data-asset-registry) | 数据资产登记表（治理元数据）：表名、所有者模块、敏感级别、保留期与备注。 | `governance(迁移内登记表)` | internal | 未定义 | 17 | 32.0 KiB |
| 125 | [`notification_delivery_attempts`](#notification-delivery-attempts) | 通知投递尝试：渠道、脱敏错误与重试序号。 | `notification` | internal | 已定义 | 58 | 32.0 KiB |
| 126 | [`organization_memberships`](#organization-memberships) | 组织成员关系：身份在组织中的成员状态与加入时间。 | `policy` | internal | 未定义 | 27 | 32.0 KiB |
| 127 | [`partition_maintenance_log`](#partition-maintenance-log) | 分区维护日志：分区创建/归档候选计算的审计轨迹（不含删除动作）。 | `platform(迁移/运维)` | internal | 未定义 | 3 | 32.0 KiB |
| 128 | [`projection_registry`](#projection-registry) | 投影注册表：投影器元数据与重建时间。 | `investigation` | internal | 未定义 | 5 | 32.0 KiB |
| 129 | [`projects`](#projects) | 项目预留表：企业化预留的项目分组（0002 企业预留，当前无数据）。 | `platform(企业预留)` | internal | 未定义 | 1 | 32.0 KiB |
| 130 | [`schema_migrations`](#schema-migrations) | 迁移版本表：已应用的迁移文件与时间（由迁移框架写入）。 | `platform(迁移框架 `backend/migrations/migrations.go`)` | internal | 未定义 | 57 | 32.0 KiB |
| 131 | [`schema_registry`](#schema-registry) | Schema 版本注册表：投影/事件 schema 的版本与描述。 | `investigation` | internal | 未定义 | 10 | 32.0 KiB |
| 132 | [`semantic_events`](#semantic-events) | 语义事件：从链上/调查数据投影出的语义层事件。 | `semantic` | internal | 未定义 | 0 | 32.0 KiB |
| 133 | [`update_previews`](#update-previews) | 地址更新预览：增量更新前的差异预览。 | `store` | internal | 未定义 | 4 | 32.0 KiB |
| 134 | [`access_policies`](#access-policies) | 访问策略：ABAC 条件表达式（allow/deny）、启停与优先级。 | `policy` | internal | 未定义 | 0 | 24.0 KiB |
| 135 | [`address_case_links`](#address-case-links) | 地址—案件关联：把链上地址挂到案件下。 | `store` | internal | 未定义 | 44 | 24.0 KiB |
| 136 | [`api_control_policies`](#api-control-policies) | API 控制策略：限流/降级规则。 | `api` | internal | 未定义 | 1 | 24.0 KiB |
| 137 | [`backup_jobs`](#backup-jobs) | 备份作业：范围、对象键、状态与请求人。 | `api` | confidential | 未定义 | 0 | 24.0 KiB |
| 138 | [`deletion_requests`](#deletion-requests) | 删除请求：双人审批 + 可恢复窗口；实际删除默认关闭。 | `api` | confidential | 未定义 | 0 | 24.0 KiB |
| 139 | [`platform_jobs`](#platform-jobs) | 统一任务表：平台级任务（调查/下载/导出）的租约与状态（Phase 0 统一 Job）。 | `investigation` | internal | 未定义 | 0 | 24.0 KiB |
| 140 | [`quotas`](#quotas) | 配额预留表：企业化预留的组织级资源配额（当前无数据）。 | `platform(企业预留)` | internal | 未定义 | 0 | 24.0 KiB |
| 141 | [`restore_previews`](#restore-previews) | 恢复预览：恢复演练的影响评估（只读）。 | `api` | internal | 未定义 | 0 | 24.0 KiB |
| 142 | [`retention_runs`](#retention-runs) | 保留策略执行记录：dry-run 统计、匹配对象数与审批引用。 | `api` | confidential | 未定义 | 0 | 24.0 KiB |
| 143 | [`routing_policy_revisions`](#routing-policy-revisions) | 路由策略版本历史：变更前后快照与操作者。 | `policy` | confidential | 未定义 | 0 | 24.0 KiB |
| 144 | [`token_metadata_overrides`](#token-metadata-overrides) | Token 元数据人工覆盖：管理员修正值与理由。 | `tokenmeta` | internal | 未定义 | 0 | 24.0 KiB |
| 145 | [`ankr_global_concurrency_profiles`](#ankr-global-concurrency-profiles) | Ankr 全局并发画像（全局单例）。 | `store` | internal | 未定义 | 0 | 16.0 KiB |
| 146 | [`ankr_global_fetch_profiles`](#ankr-global-fetch-profiles) | Ankr 全局抓取画像（全局单例）。 | `store` | internal | 未定义 | 0 | 16.0 KiB |
| 147 | [`rpc_provider_chains`](#rpc-provider-chains) | RPC Provider—链绑定关系。 | `provider` | internal | 未定义 | 0 | 16.0 KiB |
| 148 | [`rpc_usage_rollups`](#rpc-usage-rollups) | RPC 用量按日汇总（day 维度）。 | `provider` | internal | 未定义 | 0 | 8.0 KiB |
| 149 | [`user_activity_events`](#user-activity-events) | 用户高频行为事件（按 occurred_at 月分区父表）：用户操作遥测，90 天归档策略目… | `api` | restricted | 已定义 | 0 | 0 B |

---

## 4. 逐表明细（149 张关系，按表名升序）

> 每张表包含：中文用途、数据所有者领域、敏感级别、保留期、建表迁移、行数、大小、分区情况、列定义、约束、索引。
> 所有「依据」列均为**实际文件路径:行号**或**实际执行的 grep 结果**。

<a id="access-policies"></a>

### `access_policies`

| 属性 | 值 |
|---|---|
| 中文用途 | 访问策略：ABAC 条件表达式（allow/deny）、启停与优先级。 |
| 数据所有者领域 | `policy` — 依据：`grep -rl -w access_policies backend` 仅命中 `backend/migrations/postgres/0041_rbac.sql`（无 Go 引用） |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0041_rbac.sql:79` |
| 行数（精确 `count(*)`） | **0** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **24.0 KiB**（heap 0 B / index 16.0 KiB / 共 24,576 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `organization_id` | uuid | 可空 | `—` |
| 3 | `key` | text | NOT NULL | `—` |
| 4 | `effect` | text | NOT NULL | `'allow'::text` |
| 5 | `permission_key` | text | NOT NULL | `—` |
| 6 | `condition` | jsonb | NOT NULL | `'{}'::jsonb` |
| 7 | `priority` | integer(32) | NOT NULL | `100` |
| 8 | `enabled` | boolean | NOT NULL | `true` |
| 9 | `description` | text | NOT NULL | `''::text` |
| 10 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 11 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`access_policies_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（1）：`access_policies_organization_id_fkey` `FOREIGN KEY (organization_id) REFERENCES organizations(id) ON DELETE CASCADE`
- CHECK（1）：`access_policies_effect_check` `CHECK ((effect = ANY (ARRAY['allow'::text, 'deny'::text])))`
- NOT NULL 约束对象：10 个（详见上表「可空」列）

**索引（2）**

- `access_policies_key_uidx` — `CREATE UNIQUE INDEX access_policies_key_uidx ON public.access_policies USING btree (key)`
- `access_policies_pkey` — `CREATE UNIQUE INDEX access_policies_pkey ON public.access_policies USING btree (id)`

<a id="address-case-links"></a>

### `address_case_links`

| 属性 | 值 |
|---|---|
| 中文用途 | 地址—案件关联：把链上地址挂到案件下。 |
| 数据所有者领域 | `store` — 依据：仅命中通用包 → store(2) |
| 敏感级别 | **internal** — 依据：业务数据（含链上标识） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0004_v12_metadata.sql:58` |
| 行数（精确 `count(*)`） | **44** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **24.0 KiB**（heap 8.0 KiB / index 16.0 KiB / 共 24,576 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `workspace_id` | uuid | NOT NULL | `—` |
| 2 | `chain` | character varying(32) | NOT NULL | `—` |
| 3 | `normalized_address` | character varying(128) | NOT NULL | `—` |
| 4 | `case_id` | uuid | NOT NULL | `—` |
| 5 | `assigned_by` | uuid | 可空 | `—` |
| 6 | `assigned_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`address_case_links_pkey` `PRIMARY KEY (workspace_id, chain, normalized_address, case_id)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：5 个（详见上表「可空」列）

**索引（1）**

- `address_case_links_pkey` — `CREATE UNIQUE INDEX address_case_links_pkey ON public.address_case_links USING btree (workspace_id, chain, normalized_address, case_id)`

<a id="address-dataset-sync-state"></a>

### `address_dataset_sync_state`

| 属性 | 值 |
|---|---|
| 中文用途 | 地址级数据集同步水位：每个地址每个数据集已完整拉取到的区间。 |
| 数据所有者领域 | `store` — 依据：仅命中通用包 → store(2) api(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0038_address_dataset_sync_state.sql:1` |
| 行数（精确 `count(*)`） | **80** |
| 行数（`pg_class.reltuples` 估算） | 53 |
| 大小 | **64.0 KiB**（heap 16.0 KiB / index 16.0 KiB / 共 65,536 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `chain` | text | NOT NULL | `—` |
| 2 | `subject` | text | NOT NULL | `—` |
| 3 | `dataset` | text | NOT NULL | `—` |
| 4 | `complete_from` | timestamp with time zone | NOT NULL | `—` |
| 5 | `complete_through` | timestamp with time zone | NOT NULL | `—` |
| 6 | `source_job_id` | uuid | 可空 | `—` |
| 7 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`address_dataset_sync_state_pkey` `PRIMARY KEY (chain, subject, dataset)`
- UNIQUE（0）：无
- FOREIGN KEY（1）：`address_dataset_sync_state_source_job_id_fkey` `FOREIGN KEY (source_job_id) REFERENCES download_jobs(id) ON DELETE SET NULL`
- CHECK（1）：`address_dataset_sync_state_check` `CHECK ((complete_through >= complete_from))`
- NOT NULL 约束对象：6 个（详见上表「可空」列）

**索引（1）**

- `address_dataset_sync_state_pkey` — `CREATE UNIQUE INDEX address_dataset_sync_state_pkey ON public.address_dataset_sync_state USING btree (chain, subject, dataset)`

<a id="address-tag-links"></a>

### `address_tag_links`

| 属性 | 值 |
|---|---|
| 中文用途 | 地址—标签关联：地址打标（交易所/风险标签等）。 |
| 数据所有者领域 | `store` — 依据：仅命中通用包 → store(2) |
| 敏感级别 | **internal** — 依据：业务数据（含链上标识） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0004_v12_metadata.sql:68` |
| 行数（精确 `count(*)`） | **86** |
| 行数（`pg_class.reltuples` 估算） | 56 |
| 大小 | **56.0 KiB**（heap 16.0 KiB / index 16.0 KiB / 共 57,344 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `workspace_id` | uuid | NOT NULL | `—` |
| 2 | `chain` | character varying(32) | NOT NULL | `—` |
| 3 | `normalized_address` | character varying(128) | NOT NULL | `—` |
| 4 | `tag_id` | uuid | NOT NULL | `—` |
| 5 | `assigned_by` | uuid | 可空 | `—` |
| 6 | `assigned_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`address_tag_links_pkey` `PRIMARY KEY (workspace_id, chain, normalized_address, tag_id)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：5 个（详见上表「可空」列）

**索引（1）**

- `address_tag_links_pkey` — `CREATE UNIQUE INDEX address_tag_links_pkey ON public.address_tag_links USING btree (workspace_id, chain, normalized_address, tag_id)`

<a id="advanced-analysis-results"></a>

### `advanced_analysis_results`

| 属性 | 值 |
|---|---|
| 中文用途 | 高级分析结果：聚类/路径分析等计算结果缓存。 |
| 数据所有者领域 | `investigation` — 依据：`grep -rl -w advanced_analysis_results backend/internal` → investigation(1) advanced(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0033_advanced_investigation.sql:1` |
| 行数（精确 `count(*)`） | **2** |
| 行数（`pg_class.reltuples` 估算） | 2 |
| 大小 | **264.0 KiB**（heap 8.0 KiB / index 32.0 KiB / 共 270,336 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `chain` | text | NOT NULL | `—` |
| 2 | `subject` | text | NOT NULL | `—` |
| 3 | `projection_version` | text | NOT NULL | `—` |
| 4 | `algorithm_version` | text | NOT NULL | `—` |
| 5 | `data_version` | text | NOT NULL | `—` |
| 6 | `result` | jsonb | NOT NULL | `'{}'::jsonb` |
| 7 | `computed_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`advanced_analysis_results_pkey` `PRIMARY KEY (chain, subject, projection_version)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：7 个（详见上表「可空」列）

**索引（2）**

- `advanced_analysis_results_pkey` — `CREATE UNIQUE INDEX advanced_analysis_results_pkey ON public.advanced_analysis_results USING btree (chain, subject, projection_version)`
- `idx_advanced_analysis_results_subject` — `CREATE INDEX idx_advanced_analysis_results_subject ON public.advanced_analysis_results USING btree (chain, subject, computed_at DESC)`

<a id="ankr-benchmark-runs"></a>

### `ankr_benchmark_runs`

| 属性 | 值 |
|---|---|
| 中文用途 | Ankr 基准运行：固定 target_block 的 1/2/4/8 Range 极限性能实测。 |
| 数据所有者领域 | `store` — 依据：仅命中通用包 → store(1) api(1) |
| 敏感级别 | **internal** — 依据：业务数据（含链上标识） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0014_v11_benchmark.sql:7` |
| 行数（精确 `count(*)`） | **1,938** |
| 行数（`pg_class.reltuples` 估算） | 1,911 |
| 大小 | **2.17 MiB**（heap 1.74 MiB / index 264.0 KiB / 共 2,277,376 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `—` |
| 2 | `chain` | text | NOT NULL | `—` |
| 3 | `dataset` | text | NOT NULL | `—` |
| 4 | `address_hash` | text | NOT NULL | `''::text` |
| 5 | `target_block` | bigint(64) | NOT NULL | `0` |
| 6 | `shard_count` | integer(32) | NOT NULL | `1` |
| 7 | `page_size` | integer(32) | NOT NULL | `10000` |
| 8 | `job_id` | uuid | 可空 | `—` |
| 9 | `status` | text | NOT NULL | `'RUNNING'::text` |
| 10 | `wall_clock_ms` | bigint(64) | NOT NULL | `0` |
| 11 | `rows` | bigint(64) | NOT NULL | `0` |
| 12 | `unique_rows` | bigint(64) | NOT NULL | `0` |
| 13 | `requests` | integer(32) | NOT NULL | `0` |
| 14 | `attempts` | integer(32) | NOT NULL | `0` |
| 15 | `credits` | real(24) | NOT NULL | `0` |
| 16 | `cost` | real(24) | NOT NULL | `0` |
| 17 | `p50_ms` | integer(32) | NOT NULL | `0` |
| 18 | `p95_ms` | integer(32) | NOT NULL | `0` |
| 19 | `p99_ms` | integer(32) | NOT NULL | `0` |
| 20 | `page_gap_avg_ms` | bigint(64) | NOT NULL | `0` |
| 21 | `page_gap_p95_ms` | bigint(64) | NOT NULL | `0` |
| 22 | `peak_active_shards` | integer(32) | NOT NULL | `0` |
| 23 | `retry_rate` | double precision(53) | NOT NULL | `0` |
| 24 | `timeout_rate` | double precision(53) | NOT NULL | `0` |
| 25 | `rate_limit_rate` | double precision(53) | NOT NULL | `0` |
| 26 | `error_429` | integer(32) | NOT NULL | `0` |
| 27 | `error_conn_limit` | integer(32) | NOT NULL | `0` |
| 28 | `error_timeout` | integer(32) | NOT NULL | `0` |
| 29 | `error_oversized` | integer(32) | NOT NULL | `0` |
| 30 | `peak_rss` | bigint(64) | NOT NULL | `0` |
| 31 | `spool_peak_bytes` | bigint(64) | NOT NULL | `0` |
| 32 | `ch_p95_ms` | bigint(64) | NOT NULL | `0` |
| 33 | `raw_overlap_rows` | bigint(64) | NOT NULL | `0` |
| 34 | `boundary_missing` | bigint(64) | NOT NULL | `0` |
| 35 | `boundary_evidence` | jsonb | NOT NULL | `'{}'::jsonb` |
| 36 | `integrity_status` | text | NOT NULL | `'PENDING'::text` |
| 37 | `excluded_from_learning` | boolean | NOT NULL | `false` |
| 38 | `error` | text | NOT NULL | `''::text` |
| 39 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 40 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |
| 41 | `provider_latency_p50_ms` | integer(32) | NOT NULL | `0` |
| 42 | `provider_latency_p95_ms` | integer(32) | NOT NULL | `0` |
| 43 | `provider_ttfb_p50_ms` | integer(32) | NOT NULL | `0` |
| 44 | `provider_ttfb_p95_ms` | integer(32) | NOT NULL | `0` |
| 45 | `provider_body_p50_ms` | integer(32) | NOT NULL | `0` |
| 46 | `provider_body_p95_ms` | integer(32) | NOT NULL | `0` |
| 47 | `normalized_wall_clock_ms` | bigint(64) | NOT NULL | `0` |
| 48 | `provider_window_ratio` | double precision(53) | NOT NULL | `0` |
| 49 | `excluded_reason` | text | NOT NULL | `''::text` |
| 50 | `exclude_reason` | text | NOT NULL | `''::text` |

**约束**

- PRIMARY KEY（1）：`ankr_benchmark_runs_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：49 个（详见上表「可空」列）

**索引（2）**

- `ankr_benchmark_runs_pkey` — `CREATE UNIQUE INDEX ankr_benchmark_runs_pkey ON public.ankr_benchmark_runs USING btree (id)`
- `idx_benchmark_lookup` — `CREATE INDEX idx_benchmark_lookup ON public.ankr_benchmark_runs USING btree (chain, dataset, created_at DESC)`

<a id="ankr-fetch-concurrency-runs"></a>

### `ankr_fetch_concurrency_runs`

| 属性 | 值 |
|---|---|
| 中文用途 | Ankr 抓取并发运行：并发档位压测记录。 |
| 数据所有者领域 | `store` — 依据：仅命中通用包 → store(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0017_ankr_fetch_v14.sql:46` |
| 行数（精确 `count(*)`） | **840** |
| 行数（`pg_class.reltuples` 估算） | 725 |
| 大小 | **624.0 KiB**（heap 488.0 KiB / index 104.0 KiB / 共 638,976 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `—` |
| 2 | `run_id` | text | NOT NULL | `''::text` |
| 3 | `chain` | text | NOT NULL | `''::text` |
| 4 | `dataset_mix` | text | NOT NULL | `''::text` |
| 5 | `concurrency_target` | integer(32) | NOT NULL | `0` |
| 6 | `wall_ms` | bigint(64) | NOT NULL | `0` |
| 7 | `rows` | bigint(64) | NOT NULL | `0` |
| 8 | `unique_rows` | bigint(64) | NOT NULL | `0` |
| 9 | `requests` | integer(32) | NOT NULL | `0` |
| 10 | `credits` | double precision(53) | NOT NULL | `0` |
| 11 | `ttfb_p50_ms` | integer(32) | NOT NULL | `0` |
| 12 | `ttfb_p95_ms` | integer(32) | NOT NULL | `0` |
| 13 | `body_p50_ms` | integer(32) | NOT NULL | `0` |
| 14 | `body_p95_ms` | integer(32) | NOT NULL | `0` |
| 15 | `retry_amp` | double precision(53) | NOT NULL | `0` |
| 16 | `r429_count` | bigint(64) | NOT NULL | `0` |
| 17 | `timeout_count` | bigint(64) | NOT NULL | `0` |
| 18 | `wire_bytes` | bigint(64) | NOT NULL | `0` |
| 19 | `decoded_bytes` | bigint(64) | NOT NULL | `0` |
| 20 | `max_memory` | bigint(64) | NOT NULL | `0` |
| 21 | `max_spool` | bigint(64) | NOT NULL | `0` |
| 22 | `pass` | boolean | NOT NULL | `false` |
| 23 | `exclude_reason` | text | NOT NULL | `''::text` |
| 24 | `created_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`ankr_fetch_concurrency_runs_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：24 个（详见上表「可空」列）

**索引（2）**

- `ankr_fetch_concurrency_runs_pkey` — `CREATE UNIQUE INDEX ankr_fetch_concurrency_runs_pkey ON public.ankr_fetch_concurrency_runs USING btree (id)`
- `idx_fetch_concurrency_runs_time` — `CREATE INDEX idx_fetch_concurrency_runs_time ON public.ankr_fetch_concurrency_runs USING btree (created_at DESC)`

<a id="ankr-fetch-queue-diffs"></a>

### `ankr_fetch_queue_diffs`

| 属性 | 值 |
|---|---|
| 中文用途 | Ankr 抓取队列差异：队列前后快照差异。 |
| 数据所有者领域 | `store` — 依据：仅命中通用包 → store(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0017_ankr_fetch_v14.sql:75` |
| 行数（精确 `count(*)`） | **132** |
| 行数（`pg_class.reltuples` 估算） | 132 |
| 大小 | **144.0 KiB**（heap 80.0 KiB / index 32.0 KiB / 共 147,456 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `—` |
| 2 | `sampled_ready` | integer(32) | NOT NULL | `0` |
| 3 | `actual_first_shard` | text | NOT NULL | `''::text` |
| 4 | `v14_first_shard` | text | NOT NULL | `''::text` |
| 5 | `order_mismatch` | boolean | NOT NULL | `false` |
| 6 | `mismatch_rank` | integer(32) | NOT NULL | `'-1'::integer` |
| 7 | `created_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`ankr_fetch_queue_diffs_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：7 个（详见上表「可空」列）

**索引（2）**

- `ankr_fetch_queue_diffs_pkey` — `CREATE UNIQUE INDEX ankr_fetch_queue_diffs_pkey ON public.ankr_fetch_queue_diffs USING btree (id)`
- `idx_fetch_queue_diffs_time` — `CREATE INDEX idx_fetch_queue_diffs_time ON public.ankr_fetch_queue_diffs USING btree (created_at DESC)`

<a id="ankr-global-concurrency-profiles"></a>

### `ankr_global_concurrency_profiles`

| 属性 | 值 |
|---|---|
| 中文用途 | Ankr 全局并发画像（全局单例）。 |
| 数据所有者领域 | `store` — 依据：仅命中通用包 → store(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0015_v12_count_oracle.sql:88` |
| 行数（精确 `count(*)`） | **0** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **16.0 KiB**（heap 0 B / index 8.0 KiB / 共 16,384 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `chain_mix` | text | NOT NULL | `'all'::text` |
| 2 | `dataset_mix` | text | NOT NULL | `'all'::text` |
| 3 | `preferred_global_concurrency` | integer(32) | NOT NULL | `0` |
| 4 | `p50_ms` | integer(32) | NOT NULL | `0` |
| 5 | `p95_ms` | integer(32) | NOT NULL | `0` |
| 6 | `goodput` | double precision(53) | NOT NULL | `0` |
| 7 | `retry_rate` | double precision(53) | NOT NULL | `0` |
| 8 | `timeout_rate` | double precision(53) | NOT NULL | `0` |
| 9 | `rate_limit_rate` | double precision(53) | NOT NULL | `0` |
| 10 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`ankr_global_concurrency_profiles_pkey` `PRIMARY KEY (chain_mix, dataset_mix)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：10 个（详见上表「可空」列）

**索引（1）**

- `ankr_global_concurrency_profiles_pkey` — `CREATE UNIQUE INDEX ankr_global_concurrency_profiles_pkey ON public.ankr_global_concurrency_profiles USING btree (chain_mix, dataset_mix)`

<a id="ankr-global-fetch-profiles"></a>

### `ankr_global_fetch_profiles`

| 属性 | 值 |
|---|---|
| 中文用途 | Ankr 全局抓取画像（全局单例）。 |
| 数据所有者领域 | `store` — 依据：仅命中通用包 → store(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0017_ankr_fetch_v14.sql:27` |
| 行数（精确 `count(*)`） | **0** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **16.0 KiB**（heap 0 B / index 8.0 KiB / 共 16,384 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `chain` | text | NOT NULL | `'all'::text` |
| 2 | `dataset_mix` | text | NOT NULL | `'all'::text` |
| 3 | `page_size_class` | integer(32) | NOT NULL | `10000` |
| 4 | `safe_concurrency` | integer(32) | NOT NULL | `0` |
| 5 | `best_goodput` | double precision(53) | NOT NULL | `0` |
| 6 | `p50_ms` | integer(32) | NOT NULL | `0` |
| 7 | `p95_ms` | integer(32) | NOT NULL | `0` |
| 8 | `rate_limit_rate` | double precision(53) | NOT NULL | `0` |
| 9 | `timeout_rate` | double precision(53) | NOT NULL | `0` |
| 10 | `retry_amp` | double precision(53) | NOT NULL | `0` |
| 11 | `avg_wire_bytes` | bigint(64) | NOT NULL | `0` |
| 12 | `avg_decoded_bytes` | bigint(64) | NOT NULL | `0` |
| 13 | `samples` | integer(32) | NOT NULL | `0` |
| 14 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`ankr_global_fetch_profiles_pkey` `PRIMARY KEY (chain, dataset_mix, page_size_class)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：14 个（详见上表「可空」列）

**索引（1）**

- `ankr_global_fetch_profiles_pkey` — `CREATE UNIQUE INDEX ankr_global_fetch_profiles_pkey ON public.ankr_global_fetch_profiles USING btree (chain, dataset_mix, page_size_class)`

<a id="ankr-management-connection"></a>

### `ankr_management_connection`

| 属性 | 值 |
|---|---|
| 中文用途 | Ankr 管理端 MCP OAuth 连接：令牌密文/Nonce（加密后落库）与连接状态。 |
| 数据所有者领域 | `provider` — 依据：`grep -rl -w ankr_management_connection backend/internal` → provider(2) |
| 敏感级别 | **confidential** — 依据：含凭证/密钥类字段：token_ciphertext, token_nonce |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0012_ankr_management.sql:3` |
| 行数（精确 `count(*)`） | **1** |
| 行数（`pg_class.reltuples` 估算） | 1 |
| 大小 | **192.0 KiB**（heap 144.0 KiB / index 16.0 KiB / 共 196,608 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | smallint(16) | NOT NULL | `1` |
| 2 | `client_id` | text | NOT NULL | `''::text` |
| 3 | `redirect_uri` | text | NOT NULL | `''::text` |
| 4 | `token_ciphertext` | bytea | 可空 | `—` |
| 5 | `token_nonce` | bytea | 可空 | `—` |
| 6 | `token_expires_at` | timestamp with time zone | 可空 | `—` |
| 7 | `account_address` | text | NOT NULL | `''::text` |
| 8 | `remaining_credits` | numeric | 可空 | `—` |
| 9 | `balance_usd` | numeric | 可空 | `—` |
| 10 | `plan` | text | NOT NULL | `''::text` |
| 11 | `raw_balance` | jsonb | 可空 | `—` |
| 12 | `connected_at` | timestamp with time zone | 可空 | `—` |
| 13 | `last_synced_at` | timestamp with time zone | 可空 | `—` |
| 14 | `last_error` | text | NOT NULL | `''::text` |
| 15 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`ankr_management_connection_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（1）：`ankr_management_connection_id_check` `CHECK ((id = 1))`
- NOT NULL 约束对象：7 个（详见上表「可空」列）

**索引（1）**

- `ankr_management_connection_pkey` — `CREATE UNIQUE INDEX ankr_management_connection_pkey ON public.ankr_management_connection USING btree (id)`

<a id="ankr-management-oauth-state"></a>

### `ankr_management_oauth_state`

| 属性 | 值 |
|---|---|
| 中文用途 | Ankr OAuth 状态：PKCE verifier 密文与 state_hash。 |
| 数据所有者领域 | `provider` — 依据：`grep -rl -w ankr_management_oauth_state backend/internal` → provider(2) api(1) |
| 敏感级别 | **confidential** — 依据：含凭证/密钥类字段：state_hash, verifier_ciphertext, verifier_nonce |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0012_ankr_management.sql:22` |
| 行数（精确 `count(*)`） | **0** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **48.0 KiB**（heap 8.0 KiB / index 32.0 KiB / 共 49,152 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `state_hash` | text | NOT NULL | `—` |
| 2 | `client_id` | text | NOT NULL | `—` |
| 3 | `redirect_uri` | text | NOT NULL | `—` |
| 4 | `verifier_ciphertext` | bytea | NOT NULL | `—` |
| 5 | `verifier_nonce` | bytea | NOT NULL | `—` |
| 6 | `expires_at` | timestamp with time zone | NOT NULL | `—` |
| 7 | `created_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`ankr_management_oauth_state_pkey` `PRIMARY KEY (state_hash)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：7 个（详见上表「可空」列）

**索引（2）**

- `ankr_management_oauth_state_pkey` — `CREATE UNIQUE INDEX ankr_management_oauth_state_pkey ON public.ankr_management_oauth_state USING btree (state_hash)`
- `idx_ankr_management_oauth_state_expiry` — `CREATE INDEX idx_ankr_management_oauth_state_expiry ON public.ankr_management_oauth_state USING btree (expires_at)`

<a id="ankr-page-profiles"></a>

### `ankr_page_profiles`

| 属性 | 值 |
|---|---|
| 中文用途 | Ankr 页画像：页面级耗时与用量画像。 |
| 数据所有者领域 | `store` — 依据：仅命中通用包 → store(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0013_ankr_engine.sql:83` |
| 行数（精确 `count(*)`） | **7** |
| 行数（`pg_class.reltuples` 估算） | 7 |
| 大小 | **64.0 KiB**（heap 8.0 KiB / index 16.0 KiB / 共 65,536 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `chain` | text | NOT NULL | `—` |
| 2 | `dataset` | text | NOT NULL | `—` |
| 3 | `query_shape` | text | NOT NULL | `—` |
| 4 | `preferred_page_size` | integer(32) | NOT NULL | `10000` |
| 5 | `avg_rows` | integer(32) | NOT NULL | `0` |
| 6 | `avg_wire_bytes` | bigint(64) | NOT NULL | `0` |
| 7 | `avg_decoded_bytes` | bigint(64) | NOT NULL | `0` |
| 8 | `avg_latency_ms` | bigint(64) | NOT NULL | `0` |
| 9 | `p95_latency_ms` | bigint(64) | NOT NULL | `0` |
| 10 | `timeout_rate` | double precision(53) | NOT NULL | `0` |
| 11 | `oversized_rate` | double precision(53) | NOT NULL | `0` |
| 12 | `pages_observed` | bigint(64) | NOT NULL | `0` |
| 13 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`ankr_page_profiles_pkey` `PRIMARY KEY (chain, dataset, query_shape)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：13 个（详见上表「可空」列）

**索引（1）**

- `ankr_page_profiles_pkey` — `CREATE UNIQUE INDEX ankr_page_profiles_pkey ON public.ankr_page_profiles USING btree (chain, dataset, query_shape)`

<a id="ankr-performance-control"></a>

### `ankr_performance_control`

| 属性 | 值 |
|---|---|
| 中文用途 | Ankr 性能控制：当前 SAFE/FAST/TURBO 模式与切换时间。 |
| 数据所有者领域 | `store` — 依据：仅命中通用包 → store(1) api(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0020_ankr_performance_modes.sql:29` |
| 行数（精确 `count(*)`） | **1** |
| 行数（`pg_class.reltuples` 估算） | 1 |
| 大小 | **64.0 KiB**（heap 8.0 KiB / index 16.0 KiB / 共 65,536 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `singleton` | boolean | NOT NULL | `true` |
| 2 | `configured_mode` | text | NOT NULL | `'fast'::text` |
| 3 | `requested_mode` | text | NOT NULL | `'fast'::text` |
| 4 | `effective_mode` | text | NOT NULL | `'fast'::text` |
| 5 | `target_concurrency` | integer(32) | NOT NULL | `32` |
| 6 | `active_concurrency` | integer(32) | NOT NULL | `0` |
| 7 | `mode_reason` | text | NOT NULL | `'default_profile'::text` |
| 8 | `mode_changed_at` | timestamp with time zone | NOT NULL | `now()` |
| 9 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`ankr_performance_control_pkey` `PRIMARY KEY (singleton)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（4）：`ankr_performance_control_configured_mode_check` `CHECK ((configured_mode = ANY (ARRAY['safe'::text, 'fast'::text, 'turbo'::text])))`；`ankr_performance_control_effective_mode_check` `CHECK ((effective_mode = ANY (ARRAY['safe'::text, 'fast'::text, 'turbo'::text])))`；`ankr_performance_control_requested_mode_check` `CHECK ((requested_mode = ANY (ARRAY['safe'::text, 'fast'::text, 'turbo'::text])))`；`ankr_performance_control_singleton_check` `CHECK (singleton)`
- NOT NULL 约束对象：9 个（详见上表「可空」列）

**索引（1）**

- `ankr_performance_control_pkey` — `CREATE UNIQUE INDEX ankr_performance_control_pkey ON public.ankr_performance_control USING btree (singleton)`

<a id="ankr-production-profiles"></a>

### `ankr_production_profiles`

| 属性 | 值 |
|---|---|
| 中文用途 | Ankr 生产画像：经认证的生产运行参数。 |
| 数据所有者领域 | `store` — 依据：仅命中通用包 → store(1) api(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0013_ankr_performance_modes.sql:17` |
| 行数（精确 `count(*)`） | **1** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **48.0 KiB**（heap 8.0 KiB / index 32.0 KiB / 共 49,152 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `scope` | text | NOT NULL | `—` |
| 3 | `chain` | text | NOT NULL | `''::text` |
| 4 | `dataset` | text | NOT NULL | `''::text` |
| 5 | `default_mode` | text | NOT NULL | `—` |
| 6 | `safe_concurrency` | integer(32) | NOT NULL | `—` |
| 7 | `fast_concurrency` | integer(32) | NOT NULL | `—` |
| 8 | `turbo_concurrency` | integer(32) | NOT NULL | `—` |
| 9 | `hard_ceiling` | integer(32) | NOT NULL | `—` |
| 10 | `reject_boundary` | integer(32) | NOT NULL | `—` |
| 11 | `memory_budget_bytes` | bigint(64) | NOT NULL | `—` |
| 12 | `absolute_rss_limit_bytes` | bigint(64) | NOT NULL | `—` |
| 13 | `page_size_default` | integer(32) | NOT NULL | `—` |
| 14 | `request_timeout_seconds` | integer(32) | NOT NULL | `—` |
| 15 | `range_sharding_enabled` | boolean | NOT NULL | `—` |
| 16 | `version` | text | NOT NULL | `—` |
| 17 | `verified_at` | timestamp with time zone | NOT NULL | `—` |
| 18 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |
| 19 | `updated_by` | text | NOT NULL | `—` |

**约束**

- PRIMARY KEY（1）：`ankr_production_profiles_pkey` `PRIMARY KEY (id)`
- UNIQUE（1）：`ankr_production_profiles_scope_chain_dataset_key` `UNIQUE (scope, chain, dataset)`
- FOREIGN KEY（0）：无
- CHECK（2）：`ankr_production_profiles_default_mode_check` `CHECK ((default_mode = ANY (ARRAY['safe'::text, 'fast'::text, 'turbo'::text])))`；`ankr_production_profiles_hard_ceiling_check` `CHECK (((hard_ceiling >= 1) AND (hard_ceiling <= 64)))`
- NOT NULL 约束对象：19 个（详见上表「可空」列）

**索引（2）**

- `ankr_production_profiles_pkey` — `CREATE UNIQUE INDEX ankr_production_profiles_pkey ON public.ankr_production_profiles USING btree (id)`
- `ankr_production_profiles_scope_chain_dataset_key` — `CREATE UNIQUE INDEX ankr_production_profiles_scope_chain_dataset_key ON public.ankr_production_profiles USING btree (scope, chain, dataset)`

<a id="ankr-range-certifications"></a>

### `ankr_range_certifications`

| 属性 | 值 |
|---|---|
| 中文用途 | Ankr Range 认证：Transactions 多 Range 认证结果。 |
| 数据所有者领域 | `download` — 依据：`grep -rl -w ankr_range_certifications backend/internal` → store(1) download(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0017_ankr_fetch_v14.sql:10` |
| 行数（精确 `count(*)`） | **1** |
| 行数（`pg_class.reltuples` 估算） | 1 |
| 大小 | **64.0 KiB**（heap 8.0 KiB / index 16.0 KiB / 共 65,536 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `chain` | text | NOT NULL | `—` |
| 2 | `dataset` | text | NOT NULL | `—` |
| 3 | `range_supported` | boolean | NOT NULL | `false` |
| 4 | `range_verified` | boolean | NOT NULL | `false` |
| 5 | `reference_run` | text | NOT NULL | `''::text` |
| 6 | `missing` | bigint(64) | NOT NULL | `0` |
| 7 | `real_duplicates` | bigint(64) | NOT NULL | `0` |
| 8 | `boundary_violations` | bigint(64) | NOT NULL | `0` |
| 9 | `union_rows` | bigint(64) | NOT NULL | `'-1'::integer` |
| 10 | `verification_version` | integer(32) | NOT NULL | `1` |
| 11 | `verified_at` | timestamp with time zone | 可空 | `—` |
| 12 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`ankr_range_certifications_pkey` `PRIMARY KEY (chain, dataset)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：11 个（详见上表「可空」列）

**索引（1）**

- `ankr_range_certifications_pkey` — `CREATE UNIQUE INDEX ankr_range_certifications_pkey ON public.ankr_range_certifications USING btree (chain, dataset)`

<a id="ankr-scheduler-state"></a>

### `ankr_scheduler_state`

| 属性 | 值 |
|---|---|
| 中文用途 | Ankr 调度器状态：全局 Ready Queue 调度水位。 |
| 数据所有者领域 | `store` — 依据：仅命中通用包 → store(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0015_v12_count_oracle.sql:103` |
| 行数（精确 `count(*)`） | **353** |
| 行数（`pg_class.reltuples` 估算） | 353 |
| 大小 | **208.0 KiB**（heap 128.0 KiB / index 40.0 KiB / 共 212,992 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `worker_id` | text | NOT NULL | `—` |
| 2 | `active_slots` | integer(32) | NOT NULL | `0` |
| 3 | `weighted_slots` | integer(32) | NOT NULL | `0` |
| 4 | `concurrency_target` | integer(32) | NOT NULL | `0` |
| 5 | `hard_ceiling` | integer(32) | NOT NULL | `600` |
| 6 | `ramp_level` | integer(32) | NOT NULL | `0` |
| 7 | `ramp_phase` | text | NOT NULL | `''::text` |
| 8 | `control_state` | text | NOT NULL | `'NORMAL'::text` |
| 9 | `control_reason` | text | NOT NULL | `''::text` |
| 10 | `goodput` | double precision(53) | NOT NULL | `0` |
| 11 | `ttfb_p50_ms` | integer(32) | NOT NULL | `0` |
| 12 | `mem_used_bytes` | bigint(64) | NOT NULL | `0` |
| 13 | `spool_used_bytes` | bigint(64) | NOT NULL | `0` |
| 14 | `time_to_full_safe_concurrency_ms` | bigint(64) | NOT NULL | `0` |
| 15 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |
| 16 | `configured_initial_concurrency` | integer(32) | NOT NULL | `0` |
| 17 | `configured_max_concurrency` | integer(32) | NOT NULL | `0` |
| 18 | `global_hard_ceiling` | integer(32) | NOT NULL | `0` |
| 19 | `ready_queue_enabled` | boolean | NOT NULL | `false` |
| 20 | `ready_queue_shadow` | boolean | NOT NULL | `false` |
| 21 | `ready_queue_mode` | text | NOT NULL | `''::text` |
| 22 | `auto_tune_enabled` | boolean | NOT NULL | `false` |
| 23 | `controller_mode` | text | NOT NULL | `''::text` |

**约束**

- PRIMARY KEY（1）：`ankr_scheduler_state_pkey` `PRIMARY KEY (worker_id)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：23 个（详见上表「可空」列）

**索引（1）**

- `ankr_scheduler_state_pkey` — `CREATE UNIQUE INDEX ankr_scheduler_state_pkey ON public.ankr_scheduler_state USING btree (worker_id)`

<a id="ankr-shard-profiles"></a>

### `ankr_shard_profiles`

| 属性 | 值 |
|---|---|
| 中文用途 | Ankr 分片画像：数据集/链的分片耗时与体量画像。 |
| 数据所有者领域 | `store` — 依据：仅命中通用包 → store(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0013_ankr_engine.sql:60` |
| 行数（精确 `count(*)`） | **11,869** |
| 行数（`pg_class.reltuples` 估算） | 11,275 |
| 大小 | **3.76 MiB**（heap 2.93 MiB / index 808.0 KiB / 共 3,940,352 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `—` |
| 2 | `chain` | text | NOT NULL | `—` |
| 3 | `dataset` | text | NOT NULL | `—` |
| 4 | `subject_class` | text | NOT NULL | `'address'::text` |
| 5 | `block_from` | bigint(64) | NOT NULL | `0` |
| 6 | `block_to` | bigint(64) | NOT NULL | `0` |
| 7 | `rows` | bigint(64) | NOT NULL | `0` |
| 8 | `pages` | integer(32) | NOT NULL | `0` |
| 9 | `wire_bytes` | bigint(64) | NOT NULL | `0` |
| 10 | `decoded_bytes` | bigint(64) | NOT NULL | `0` |
| 11 | `fetch_duration_ms` | bigint(64) | NOT NULL | `0` |
| 12 | `ingest_duration_ms` | bigint(64) | NOT NULL | `0` |
| 13 | `rows_per_block` | double precision(53) | NOT NULL | `0` |
| 14 | `bytes_per_row` | double precision(53) | NOT NULL | `0` |
| 15 | `page_latency_p50_ms` | bigint(64) | NOT NULL | `0` |
| 16 | `page_latency_p95_ms` | bigint(64) | NOT NULL | `0` |
| 17 | `page_gap_avg_ms` | bigint(64) | NOT NULL | `0` |
| 18 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 19 | `query_shape` | text | NOT NULL | `''::text` |
| 20 | `density_class` | text | NOT NULL | `''::text` |
| 21 | `preferred_shard_count` | integer(32) | NOT NULL | `0` |
| 22 | `avg_wall_clock_ms` | bigint(64) | NOT NULL | `0` |
| 23 | `provider_latency_avg_ms` | bigint(64) | NOT NULL | `0` |
| 24 | `provider_latency_p95_ms` | bigint(64) | NOT NULL | `0` |
| 25 | `retry_rate` | double precision(53) | NOT NULL | `0` |
| 26 | `timeout_rate` | double precision(53) | NOT NULL | `0` |
| 27 | `rate_limit_rate` | double precision(53) | NOT NULL | `0` |
| 28 | `sample_count` | integer(32) | NOT NULL | `0` |

**约束**

- PRIMARY KEY（1）：`ankr_shard_profiles_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：28 个（详见上表「可空」列）

**索引（2）**

- `ankr_shard_profiles_pkey` — `CREATE UNIQUE INDEX ankr_shard_profiles_pkey ON public.ankr_shard_profiles USING btree (id)`
- `idx_shard_profiles_lookup` — `CREATE INDEX idx_shard_profiles_lookup ON public.ankr_shard_profiles USING btree (chain, dataset, subject_class, block_from)`

<a id="ankr-sweet-spot-profiles"></a>

### `ankr_sweet_spot_profiles`

| 属性 | 值 |
|---|---|
| 中文用途 | Ankr Sweet Spot 画像：最优并发/Range 档位记录。 |
| 数据所有者领域 | `store` — 依据：仅命中通用包 → store(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0014_v11_benchmark.sql:64` |
| 行数（精确 `count(*)`） | **5** |
| 行数（`pg_class.reltuples` 估算） | 5 |
| 大小 | **32.0 KiB**（heap 8.0 KiB / index 16.0 KiB / 共 32,768 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `chain` | text | NOT NULL | `—` |
| 2 | `dataset` | text | NOT NULL | `—` |
| 3 | `query_shape` | text | NOT NULL | `''::text` |
| 4 | `density_class` | text | NOT NULL | `''::text` |
| 5 | `preferred_shard_count` | integer(32) | NOT NULL | `0` |
| 6 | `avg_wall_clock_ms` | bigint(64) | NOT NULL | `0` |
| 7 | `avg_rows` | bigint(64) | NOT NULL | `0` |
| 8 | `avg_pages` | bigint(64) | NOT NULL | `0` |
| 9 | `provider_latency_avg_ms` | bigint(64) | NOT NULL | `0` |
| 10 | `provider_latency_p95_ms` | bigint(64) | NOT NULL | `0` |
| 11 | `retry_rate` | double precision(53) | NOT NULL | `0` |
| 12 | `timeout_rate` | double precision(53) | NOT NULL | `0` |
| 13 | `rate_limit_rate` | double precision(53) | NOT NULL | `0` |
| 14 | `sample_count` | integer(32) | NOT NULL | `0` |
| 15 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`ankr_sweet_spot_profiles_pkey` `PRIMARY KEY (chain, dataset, query_shape, density_class)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：15 个（详见上表「可空」列）

**索引（1）**

- `ankr_sweet_spot_profiles_pkey` — `CREATE UNIQUE INDEX ankr_sweet_spot_profiles_pkey ON public.ankr_sweet_spot_profiles USING btree (chain, dataset, query_shape, density_class)`

<a id="api-control-actions"></a>

### `api_control_actions`

| 属性 | 值 |
|---|---|
| 中文用途 | API 控制动作审计：人工/自动控制动作记录。 |
| 数据所有者领域 | `api` — 依据：仅命中通用包 → api(3) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0011_api_control.sql:33` |
| 行数（精确 `count(*)`） | **776** |
| 行数（`pg_class.reltuples` 估算） | 776 |
| 大小 | **416.0 KiB**（heap 280.0 KiB / index 104.0 KiB / 共 425,984 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `action` | character varying(64) | NOT NULL | `—` |
| 3 | `source` | character varying(24) | NOT NULL | `'automatic'::character varying` |
| 4 | `from_state` | character varying(24) | NOT NULL | `''::character varying` |
| 5 | `to_state` | character varying(24) | NOT NULL | `''::character varying` |
| 6 | `reason` | text | NOT NULL | `''::text` |
| 7 | `detail` | jsonb | NOT NULL | `'{}'::jsonb` |
| 8 | `actor` | character varying(128) | NOT NULL | `'system'::character varying` |
| 9 | `created_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`api_control_actions_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：9 个（详见上表「可空」列）

**索引（2）**

- `api_control_actions_pkey` — `CREATE UNIQUE INDEX api_control_actions_pkey ON public.api_control_actions USING btree (id)`
- `idx_api_control_actions_time` — `CREATE INDEX idx_api_control_actions_time ON public.api_control_actions USING btree (created_at DESC)`

<a id="api-control-alerts"></a>

### `api_control_alerts`

| 属性 | 值 |
|---|---|
| 中文用途 | API 告警：首次/最后出现、确认、静默与解决时间。 |
| 数据所有者领域 | `api` — 依据：仅命中通用包 → api(5) |
| 敏感级别 | **confidential** — 依据：含 PII 字段：fingerprint |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0011_api_control.sql:58` |
| 行数（精确 `count(*)`） | **31** |
| 行数（`pg_class.reltuples` 估算） | 31 |
| 大小 | **152.0 KiB**（heap 16.0 KiB / index 96.0 KiB / 共 155,648 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `severity` | character varying(16) | NOT NULL | `—` |
| 3 | `title` | character varying(160) | NOT NULL | `—` |
| 4 | `message` | text | NOT NULL | `''::text` |
| 5 | `status` | character varying(16) | NOT NULL | `'OPEN'::character varying` |
| 6 | `fingerprint` | character varying(160) | 可空 | `—` |
| 7 | `first_seen_at` | timestamp with time zone | NOT NULL | `now()` |
| 8 | `last_seen_at` | timestamp with time zone | NOT NULL | `now()` |
| 9 | `muted_until` | timestamp with time zone | 可空 | `—` |
| 10 | `resolved_at` | timestamp with time zone | 可空 | `—` |
| 11 | `acknowledged_at` | timestamp with time zone | 可空 | `—` |
| 12 | `acknowledged_by` | uuid | 可空 | `—` |
| 13 | `acknowledge_note` | text | NOT NULL | `''::text` |
| 14 | `resolved_by` | uuid | 可空 | `—` |
| 15 | `resolution_note` | text | NOT NULL | `''::text` |
| 16 | `muted_by` | uuid | 可空 | `—` |
| 17 | `mute_reason` | text | NOT NULL | `''::text` |
| 18 | `occurrence_count` | bigint(64) | NOT NULL | `1` |
| 19 | `source` | text | NOT NULL | `'api-control'::text` |
| 20 | `slo_key` | text | NOT NULL | `''::text` |
| 21 | `trace_id` | text | NOT NULL | `''::text` |
| 22 | `links` | jsonb | NOT NULL | `'{}'::jsonb` |
| 23 | `labels` | jsonb | NOT NULL | `'{}'::jsonb` |
| 24 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |
| 25 | `priority` | text | NOT NULL | `''::text` |

**约束**

- PRIMARY KEY（1）：`api_control_alerts_pkey` `PRIMARY KEY (id)`
- UNIQUE（1）：`api_control_alerts_fingerprint_key` `UNIQUE (fingerprint)`
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：18 个（详见上表「可空」列）

**索引（6）**

- `api_control_alerts_fingerprint_key` — `CREATE UNIQUE INDEX api_control_alerts_fingerprint_key ON public.api_control_alerts USING btree (fingerprint)`
- `api_control_alerts_pkey` — `CREATE UNIQUE INDEX api_control_alerts_pkey ON public.api_control_alerts USING btree (id)`
- `api_control_alerts_priority_idx` — `CREATE INDEX api_control_alerts_priority_idx ON public.api_control_alerts USING btree (priority, last_seen_at DESC)`
- `api_control_alerts_severity_idx` — `CREATE INDEX api_control_alerts_severity_idx ON public.api_control_alerts USING btree (severity, last_seen_at DESC)`
- `api_control_alerts_status_idx` — `CREATE INDEX api_control_alerts_status_idx ON public.api_control_alerts USING btree (status, last_seen_at DESC)`
- `idx_api_control_alerts_status` — `CREATE INDEX idx_api_control_alerts_status ON public.api_control_alerts USING btree (status, last_seen_at DESC)`

<a id="api-control-budgets"></a>

### `api_control_budgets`

| 属性 | 值 |
|---|---|
| 中文用途 | API 预算：配额上限与窗口。 |
| 数据所有者领域 | `api` — 依据：仅命中通用包 → api(3) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0011_api_control.sql:46` |
| 行数（精确 `count(*)`） | **16** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **32.0 KiB**（heap 8.0 KiB / index 16.0 KiB / 共 32,768 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `name` | character varying(96) | NOT NULL | `—` |
| 3 | `provider` | character varying(64) | NOT NULL | `'all'::character varying` |
| 4 | `period` | character varying(16) | NOT NULL | `'monthly'::character varying` |
| 5 | `limit_usd` | double precision(53) | NOT NULL | `0` |
| 6 | `warn_percent` | integer(32) | NOT NULL | `80` |
| 7 | `enabled` | boolean | NOT NULL | `true` |
| 8 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 9 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |
| 10 | `dimension` | text | NOT NULL | `'provider'::text` |
| 11 | `scope_key` | text | NOT NULL | `''::text` |
| 12 | `action` | text | NOT NULL | `'alert'::text` |
| 13 | `action_threshold` | integer(32) | NOT NULL | `100` |
| 14 | `currency` | text | NOT NULL | `'USD'::text` |
| 15 | `notes` | text | NOT NULL | `''::text` |
| 16 | `revision` | bigint(64) | NOT NULL | `1` |
| 17 | `updated_by` | uuid | 可空 | `—` |

**约束**

- PRIMARY KEY（1）：`api_control_budgets_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（1）：`api_control_budgets_updated_by_fkey` `FOREIGN KEY (updated_by) REFERENCES identities(id)`
- CHECK（0）：无
- NOT NULL 约束对象：16 个（详见上表「可空」列）

**索引（1）**

- `api_control_budgets_pkey` — `CREATE UNIQUE INDEX api_control_budgets_pkey ON public.api_control_budgets USING btree (id)`

<a id="api-control-policies"></a>

### `api_control_policies`

| 属性 | 值 |
|---|---|
| 中文用途 | API 控制策略：限流/降级规则。 |
| 数据所有者领域 | `api` — 依据：仅命中通用包 → api(2) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0011_api_control.sql:18` |
| 行数（精确 `count(*)`） | **1** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **24.0 KiB**（heap 8.0 KiB / index 16.0 KiB / 共 24,576 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `name` | character varying(96) | NOT NULL | `—` |
| 3 | `enabled` | boolean | NOT NULL | `true` |
| 4 | `warning_error_rate` | double precision(53) | NOT NULL | `0.03` |
| 5 | `critical_error_rate` | double precision(53) | NOT NULL | `0.12` |
| 6 | `warning_p95_ms` | integer(32) | NOT NULL | `5000` |
| 7 | `critical_p95_ms` | integer(32) | NOT NULL | `15000` |
| 8 | `cooldown_seconds` | integer(32) | NOT NULL | `300` |
| 9 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`api_control_policies_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：9 个（详见上表「可空」列）

**索引（1）**

- `api_control_policies_pkey` — `CREATE UNIQUE INDEX api_control_policies_pkey ON public.api_control_policies USING btree (id)`

<a id="api-control-settings"></a>

### `api_control_settings`

| 属性 | 值 |
|---|---|
| 中文用途 | API 控制中心单例设置：自动保护、Kill Switch、后台暂停与状态机。 |
| 数据所有者领域 | `tokenmeta` — 依据：`grep -rl -w api_control_settings backend/internal` → api(2) tokenmeta(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0011_api_control.sql:3` |
| 行数（精确 `count(*)`） | **1** |
| 行数（`pg_class.reltuples` 估算） | 1 |
| 大小 | **64.0 KiB**（heap 8.0 KiB / index 16.0 KiB / 共 65,536 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | smallint(16) | NOT NULL | `1` |
| 2 | `automatic_protection` | boolean | NOT NULL | `true` |
| 3 | `kill_switch` | boolean | NOT NULL | `false` |
| 4 | `background_paused` | boolean | NOT NULL | `false` |
| 5 | `state` | character varying(24) | NOT NULL | `'NORMAL'::character varying` |
| 6 | `state_reason` | text | NOT NULL | `'指标正常'::text` |
| 7 | `state_since` | timestamp with time zone | NOT NULL | `now()` |
| 8 | `manual_until` | timestamp with time zone | 可空 | `—` |
| 9 | `manual_mode` | character varying(24) | 可空 | `—` |
| 10 | `manual_reason` | text | 可空 | `—` |
| 11 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`api_control_settings_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（1）：`api_control_settings_id_check` `CHECK ((id = 1))`
- NOT NULL 约束对象：8 个（详见上表「可空」列）

**索引（1）**

- `api_control_settings_pkey` — `CREATE UNIQUE INDEX api_control_settings_pkey ON public.api_control_settings USING btree (id)`

<a id="approval-request-events"></a>

### `approval_request_events`

| 属性 | 值 |
|---|---|
| 中文用途 | 审批请求事件流：请求/批准/拒绝/执行的时间线。 |
| 数据所有者领域 | `api` — 依据：仅命中通用包 → api(1) |
| 敏感级别 | **confidential** — 依据：含 PII 字段：actor_email |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0051_s6_control_governance.sql:157` |
| 行数（精确 `count(*)`） | **56** |
| 行数（`pg_class.reltuples` 估算） | 52 |
| 大小 | **80.0 KiB**（heap 16.0 KiB / index 32.0 KiB / 共 81,920 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `approval_request_id` | uuid | NOT NULL | `—` |
| 3 | `event` | text | NOT NULL | `—` |
| 4 | `actor_id` | uuid | 可空 | `—` |
| 5 | `actor_email` | text | NOT NULL | `''::text` |
| 6 | `note` | text | NOT NULL | `''::text` |
| 7 | `detail` | jsonb | NOT NULL | `'{}'::jsonb` |
| 8 | `created_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`approval_request_events_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（2）：`approval_request_events_actor_id_fkey` `FOREIGN KEY (actor_id) REFERENCES identities(id)`；`approval_request_events_approval_request_id_fkey` `FOREIGN KEY (approval_request_id) REFERENCES approval_requests(id) ON DELETE CASCADE`
- CHECK（0）：无
- NOT NULL 约束对象：7 个（详见上表「可空」列）

**索引（2）**

- `approval_request_events_pkey` — `CREATE UNIQUE INDEX approval_request_events_pkey ON public.approval_request_events USING btree (id)`
- `approval_request_events_request_idx` — `CREATE INDEX approval_request_events_request_idx ON public.approval_request_events USING btree (approval_request_id, created_at DESC)`

<a id="approval-requests"></a>

### `approval_requests`

| 属性 | 值 |
|---|---|
| 中文用途 | 双人审批请求：对象、动作、请求人、过期与执行状态。 |
| 数据所有者领域 | `api` — 依据：仅命中通用包 → api(3) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0042_audit_v2.sql:143` |
| 行数（精确 `count(*)`） | **28** |
| 行数（`pg_class.reltuples` 估算） | 22 |
| 大小 | **128.0 KiB**（heap 48.0 KiB / index 48.0 KiB / 共 131,072 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `organization_id` | uuid | 可空 | `—` |
| 3 | `operation` | text | NOT NULL | `—` |
| 4 | `target_type` | text | NOT NULL | `''::text` |
| 5 | `target_id` | text | NOT NULL | `''::text` |
| 6 | `payload` | jsonb | NOT NULL | `'{}'::jsonb` |
| 7 | `requested_by` | uuid | NOT NULL | `—` |
| 8 | `requested_at` | timestamp with time zone | NOT NULL | `now()` |
| 9 | `status` | text | NOT NULL | `'pending'::text` |
| 10 | `decided_by` | uuid | 可空 | `—` |
| 11 | `decided_at` | timestamp with time zone | 可空 | `—` |
| 12 | `decision_note` | text | NOT NULL | `''::text` |
| 13 | `executed_at` | timestamp with time zone | 可空 | `—` |
| 14 | `expires_at` | timestamp with time zone | 可空 | `—` |
| 15 | `result` | jsonb | 可空 | `—` |

**约束**

- PRIMARY KEY（1）：`approval_requests_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（3）：`approval_requests_decided_by_fkey` `FOREIGN KEY (decided_by) REFERENCES identities(id)`；`approval_requests_organization_id_fkey` `FOREIGN KEY (organization_id) REFERENCES organizations(id)`；`approval_requests_requested_by_fkey` `FOREIGN KEY (requested_by) REFERENCES identities(id)`
- CHECK（2）：`approval_requests_status_check` `CHECK ((status = ANY (ARRAY['pending'::text, 'approved'::text, 'rejected'::text, 'expired'::text, 'executed'::text, 'failed'::text, 'cancelled'::text])))`；`approval_requests_two_person_chk` `CHECK (((decided_by IS NULL) OR (decided_by <> requested_by)))`
- NOT NULL 约束对象：9 个（详见上表「可空」列）

**索引（3）**

- `approval_requests_pkey` — `CREATE UNIQUE INDEX approval_requests_pkey ON public.approval_requests USING btree (id)`
- `approval_requests_requester_idx` — `CREATE INDEX approval_requests_requester_idx ON public.approval_requests USING btree (requested_by, requested_at DESC)`
- `approval_requests_status_idx` — `CREATE INDEX approval_requests_status_idx ON public.approval_requests USING btree (status, requested_at DESC)`

<a id="audit-chain-anchors"></a>

### `audit_chain_anchors`

| 属性 | 值 |
|---|---|
| 中文用途 | 审计链锚点：按日锚定链头哈希并落 content_hash 防篡改。 |
| 数据所有者领域 | `audit` — 依据：`grep -rl -w audit_chain_anchors backend/internal` → audit(3) api(2) |
| 敏感级别 | **restricted** — 依据：审计链/认证事件（append-only；哈希链字段：head_hash, content_hash） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0042_audit_v2.sql:128` |
| 行数（精确 `count(*)`） | **4** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **48.0 KiB**（heap 8.0 KiB / index 32.0 KiB / 共 49,152 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `chain` | text | NOT NULL | `—` |
| 3 | `anchor_date` | date | NOT NULL | `—` |
| 4 | `from_seq` | bigint(64) | NOT NULL | `—` |
| 5 | `to_seq` | bigint(64) | NOT NULL | `—` |
| 6 | `head_hash` | text | NOT NULL | `—` |
| 7 | `event_count` | bigint(64) | NOT NULL | `—` |
| 8 | `object_ref` | text | NOT NULL | `''::text` |
| 9 | `content_hash` | text | NOT NULL | `''::text` |
| 10 | `created_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`audit_chain_anchors_pkey` `PRIMARY KEY (id)`
- UNIQUE（1）：`audit_chain_anchors_chain_anchor_date_key` `UNIQUE (chain, anchor_date)`
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：10 个（详见上表「可空」列）

**索引（2）**

- `audit_chain_anchors_chain_anchor_date_key` — `CREATE UNIQUE INDEX audit_chain_anchors_chain_anchor_date_key ON public.audit_chain_anchors USING btree (chain, anchor_date)`
- `audit_chain_anchors_pkey` — `CREATE UNIQUE INDEX audit_chain_anchors_pkey ON public.audit_chain_anchors USING btree (id)`

<a id="audit-chain-heads"></a>

### `audit_chain_heads`

| 属性 | 值 |
|---|---|
| 中文用途 | 审计链头：每个审计流（stream）的最新哈希与序号，用于链断裂检测。 |
| 数据所有者领域 | `audit` — 依据：`grep -rl -w audit_chain_heads backend/internal` → audit(1) |
| 敏感级别 | **restricted** — 依据：审计链/认证事件（append-only；哈希链字段：last_hash） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0040_iam.sql:161` |
| 行数（精确 `count(*)`） | **2** |
| 行数（`pg_class.reltuples` 估算） | 2 |
| 大小 | **64.0 KiB**（heap 8.0 KiB / index 16.0 KiB / 共 65,536 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `chain` | text | NOT NULL | `—` |
| 2 | `last_seq` | bigint(64) | NOT NULL | `0` |
| 3 | `last_hash` | text | NOT NULL | `''::text` |
| 4 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`audit_chain_heads_pkey` `PRIMARY KEY (chain)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：4 个（详见上表「可空」列）

**索引（1）**

- `audit_chain_heads_pkey` — `CREATE UNIQUE INDEX audit_chain_heads_pkey ON public.audit_chain_heads USING btree (chain)`

<a id="audit-events"></a>

### `audit_events`

| 属性 | 值 |
|---|---|
| 中文用途 | V1 审计事件表（0001 初版）：早期审计记录，V2 起由 audit_events_v2 承担。 |
| 数据所有者领域 | `report` — 依据：`grep -rl -w audit_events backend/internal` → store(1) report(1) investigation(1) |
| 敏感级别 | **restricted** — 依据：审计链/认证事件（append-only；哈希链字段：无） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0001_init.sql:169` |
| 行数（精确 `count(*)`） | **3,565** |
| 行数（`pg_class.reltuples` 估算） | 3,502 |
| 大小 | **1.26 MiB**（heap 928.0 KiB / index 320.0 KiB / 共 1,318,912 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `—` |
| 2 | `user_id` | uuid | 可空 | `—` |
| 3 | `action` | text | NOT NULL | `—` |
| 4 | `resource_type` | text | NOT NULL | `''::text` |
| 5 | `resource_id` | text | NOT NULL | `''::text` |
| 6 | `detail` | jsonb | NOT NULL | `'{}'::jsonb` |
| 7 | `created_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`audit_events_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：6 个（详见上表「可空」列）

**索引（2）**

- `audit_events_pkey` — `CREATE UNIQUE INDEX audit_events_pkey ON public.audit_events USING btree (id)`
- `idx_audit_time` — `CREATE INDEX idx_audit_time ON public.audit_events USING btree (created_at DESC)`

<a id="audit-events-v2"></a>

### `audit_events_v2`

| 属性 | 值 |
|---|---|
| 中文用途 | 审计事件 v2（append-only 哈希链）：occurred_at/actor/action/resource/前后哈希，合规留存主体。 |
| 数据所有者领域 | `audit` — 依据：`data_asset_registry.owner_module` |
| 敏感级别 | **restricted** — 依据：`data_asset_registry.sensitivity` |
| 保留期 | 在线 365 天 / 归档 3 年 — 依据：`data_asset_registry.retention`（写入见 `backend/migrations/postgres/0044_tenant_scope.sql:155-176`、`0047_case_collaboration.sql:267-279`） |
| 建表迁移 | `backend/migrations/postgres/0042_audit_v2.sql:10` |
| 行数（精确 `count(*)`） | **48,586** |
| 行数（`pg_class.reltuples` 估算） | 47,427 |
| 大小 | **65.66 MiB**（heap 34.14 MiB / index 31.47 MiB / 共 68,845,568 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `chain` | text | NOT NULL | `'audit'::text` |
| 3 | `seq` | bigint(64) | NOT NULL | `—` |
| 4 | `occurred_at` | timestamp with time zone | NOT NULL | `now()` |
| 5 | `event_version` | integer(32) | NOT NULL | `1` |
| 6 | `environment` | text | NOT NULL | `''::text` |
| 7 | `actor_type` | text | NOT NULL | `'user'::text` |
| 8 | `actor_id` | uuid | 可空 | `—` |
| 9 | `actor_email` | text | NOT NULL | `''::text` |
| 10 | `actor_display_name` | text | NOT NULL | `''::text` |
| 11 | `actor_roles` | ARRAY | NOT NULL | `'{}'::text[]` |
| 12 | `organization_id` | uuid | 可空 | `—` |
| 13 | `workspace_id` | uuid | 可空 | `—` |
| 14 | `session_id` | uuid | 可空 | `—` |
| 15 | `device_id` | uuid | 可空 | `—` |
| 16 | `action` | text | NOT NULL | `—` |
| 17 | `category` | text | NOT NULL | `—` |
| 18 | `resource_type` | text | NOT NULL | `''::text` |
| 19 | `resource_id` | text | NOT NULL | `''::text` |
| 20 | `case_id` | uuid | 可空 | `—` |
| 21 | `investigation_id` | uuid | 可空 | `—` |
| 22 | `job_id` | uuid | 可空 | `—` |
| 23 | `report_id` | uuid | 可空 | `—` |
| 24 | `http_method` | text | NOT NULL | `''::text` |
| 25 | `route_template` | text | NOT NULL | `''::text` |
| 26 | `request_id` | text | NOT NULL | `''::text` |
| 27 | `trace_id` | text | NOT NULL | `''::text` |
| 28 | `idempotency_key_hash` | text | NOT NULL | `''::text` |
| 29 | `client_version` | text | NOT NULL | `''::text` |
| 30 | `result` | text | NOT NULL | `—` |
| 31 | `http_status` | integer(32) | NOT NULL | `0` |
| 32 | `error_code` | text | NOT NULL | `''::text` |
| 33 | `duration_ms` | integer(32) | NOT NULL | `0` |
| 34 | `result_count` | bigint(64) | NOT NULL | `0` |
| 35 | `affected_rows` | integer(32) | NOT NULL | `0` |
| 36 | `permission_key` | text | 可空 | `—` |
| 37 | `policy_decision` | text | NOT NULL | `''::text` |
| 38 | `deny_reason` | text | NOT NULL | `''::text` |
| 39 | `sensitive` | boolean | NOT NULL | `false` |
| 40 | `risk_level` | text | NOT NULL | `'low'::text` |
| 41 | `approval_id` | uuid | 可空 | `—` |
| 42 | `second_confirm` | boolean | NOT NULL | `false` |
| 43 | `requires_review` | boolean | NOT NULL | `false` |
| 44 | `before` | jsonb | 可空 | `—` |
| 45 | `after` | jsonb | 可空 | `—` |
| 46 | `changes` | jsonb | 可空 | `—` |
| 47 | `detail` | jsonb | NOT NULL | `'{}'::jsonb` |
| 48 | `peer_ip` | inet | 可空 | `—` |
| 49 | `client_ip` | inet | 可空 | `—` |
| 50 | `proxy_chain` | ARRAY | NOT NULL | `'{}'::text[]` |
| 51 | `user_agent` | text | NOT NULL | `''::text` |
| 52 | `data_source` | text | NOT NULL | `''::text` |
| 53 | `content_hash` | text | NOT NULL | `''::text` |
| 54 | `object_ref` | text | NOT NULL | `''::text` |
| 55 | `prev_hash` | text | NOT NULL | `—` |
| 56 | `event_hash` | text | NOT NULL | `—` |

**约束**

- PRIMARY KEY（1）：`audit_events_v2_pkey` `PRIMARY KEY (id)`
- UNIQUE（1）：`audit_events_v2_chain_seq_key` `UNIQUE (chain, seq)`
- FOREIGN KEY（0）：无
- CHECK（4）：`audit_events_v2_actor_type_check` `CHECK ((actor_type = ANY (ARRAY['user'::text, 'system'::text, 'api_client'::text, 'anonymous'::text])))`；`audit_events_v2_result_check` `CHECK ((result = ANY (ARRAY['allow'::text, 'deny'::text, 'success'::text, 'failure'::text, 'error'::text])))`；`audit_events_v2_risk_level_check` `CHECK ((risk_level = ANY (ARRAY['low'::text, 'medium'::text, 'high'::text, 'critical'::text])))`；`audit_events_v2_s7_scope_coherence_check` `CHECK (((workspace_id IS NULL) OR (organization_id IS NOT NULL)))`
- NOT NULL 约束对象：40 个（详见上表「可空」列）

**索引（12）**

- `audit_events_v2_chain_seq_key` — `CREATE UNIQUE INDEX audit_events_v2_chain_seq_key ON public.audit_events_v2 USING btree (chain, seq)`
- `audit_events_v2_pkey` — `CREATE UNIQUE INDEX audit_events_v2_pkey ON public.audit_events_v2 USING btree (id)`
- `audit_v2_action_idx` — `CREATE INDEX audit_v2_action_idx ON public.audit_events_v2 USING btree (action, occurred_at DESC)`
- `audit_v2_actor_idx` — `CREATE INDEX audit_v2_actor_idx ON public.audit_events_v2 USING btree (actor_id, occurred_at DESC)`
- `audit_v2_category_idx` — `CREATE INDEX audit_v2_category_idx ON public.audit_events_v2 USING btree (category, occurred_at DESC)`
- `audit_v2_org_idx` — `CREATE INDEX audit_v2_org_idx ON public.audit_events_v2 USING btree (organization_id, occurred_at DESC)`
- `audit_v2_resource_idx` — `CREATE INDEX audit_v2_resource_idx ON public.audit_events_v2 USING btree (resource_type, resource_id, occurred_at DESC)`
- `audit_v2_session_idx` — `CREATE INDEX audit_v2_session_idx ON public.audit_events_v2 USING btree (session_id) WHERE (session_id IS NOT NULL)`
- `audit_v2_time_idx` — `CREATE INDEX audit_v2_time_idx ON public.audit_events_v2 USING btree (occurred_at DESC)`
- `audit_v2_trace_idx` — `CREATE INDEX audit_v2_trace_idx ON public.audit_events_v2 USING btree (trace_id) WHERE (trace_id <> ''::text)`
- `idx_audit_v2_result_seq` — `CREATE INDEX idx_audit_v2_result_seq ON public.audit_events_v2 USING btree (result, seq DESC)`
- `idx_audit_v2_seq_desc` — `CREATE INDEX idx_audit_v2_seq_desc ON public.audit_events_v2 USING btree (seq DESC)`

<a id="audit-exports"></a>

### `audit_exports`

| 属性 | 值 |
|---|---|
| 中文用途 | 审计导出作业：导出范围、文件对象键、哈希与过期时间。 |
| 数据所有者领域 | `audit` — 依据：`grep -rl -w audit_exports backend/internal` → api(3) audit(1) |
| 敏感级别 | **restricted** — 依据：审计链/认证事件（append-only；哈希链字段：无） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0052_s6_data_governance.sql:154` |
| 行数（精确 `count(*)`） | **14** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **96.0 KiB**（heap 32.0 KiB / index 32.0 KiB / 共 98,304 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `format` | text | NOT NULL | `'jsonl'::text` |
| 3 | `chain` | text | NOT NULL | `'audit'::text` |
| 4 | `filters` | jsonb | NOT NULL | `'{}'::jsonb` |
| 5 | `status` | text | NOT NULL | `'queued'::text` |
| 6 | `requested_by` | uuid | 可空 | `—` |
| 7 | `requested_by_email` | text | NOT NULL | `''::text` |
| 8 | `approval_request_id` | uuid | 可空 | `—` |
| 9 | `event_count` | bigint(64) | NOT NULL | `0` |
| 10 | `from_seq` | bigint(64) | 可空 | `—` |
| 11 | `to_seq` | bigint(64) | 可空 | `—` |
| 12 | `first_event_at` | timestamp with time zone | 可空 | `—` |
| 13 | `last_event_at` | timestamp with time zone | 可空 | `—` |
| 14 | `object_ref` | text | NOT NULL | `''::text` |
| 15 | `manifest_ref` | text | NOT NULL | `''::text` |
| 16 | `size_bytes` | bigint(64) | NOT NULL | `0` |
| 17 | `sha256` | text | NOT NULL | `''::text` |
| 18 | `manifest` | jsonb | NOT NULL | `'{}'::jsonb` |
| 19 | `error` | text | NOT NULL | `''::text` |
| 20 | `started_at` | timestamp with time zone | 可空 | `—` |
| 21 | `completed_at` | timestamp with time zone | 可空 | `—` |
| 22 | `expires_at` | timestamp with time zone | 可空 | `—` |
| 23 | `created_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`audit_exports_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（2）：`audit_exports_approval_request_id_fkey` `FOREIGN KEY (approval_request_id) REFERENCES approval_requests(id)`；`audit_exports_requested_by_fkey` `FOREIGN KEY (requested_by) REFERENCES identities(id)`
- CHECK（2）：`audit_exports_format_check` `CHECK ((format = ANY (ARRAY['jsonl'::text, 'csv'::text])))`；`audit_exports_status_check` `CHECK ((status = ANY (ARRAY['queued'::text, 'running'::text, 'completed'::text, 'failed'::text, 'expired'::text])))`
- NOT NULL 约束对象：14 个（详见上表「可空」列）

**索引（2）**

- `audit_exports_created_idx` — `CREATE INDEX audit_exports_created_idx ON public.audit_exports USING btree (created_at DESC)`
- `audit_exports_pkey` — `CREATE UNIQUE INDEX audit_exports_pkey ON public.audit_exports USING btree (id)`

<a id="audit-integrity-incident-anchors"></a>

### `audit_integrity_incident_anchors`

| 属性 | 值 |
|---|---|
| 中文用途 | 完整性事件锚点：按日锚定事件链哈希。 |
| 数据所有者领域 | `audit` — 依据：`grep -rl -w audit_integrity_incident_anchors backend/internal` → audit(1) |
| 敏感级别 | **restricted** — 依据：审计链/认证事件（append-only；哈希链字段：head_hash, content_hash, archive_hash） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0055_s6_audit_integrity_boundary.sql:86` |
| 行数（精确 `count(*)`） | **1** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **48.0 KiB**（heap 8.0 KiB / index 32.0 KiB / 共 49,152 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `incident_id` | uuid | NOT NULL | `—` |
| 3 | `chain` | text | NOT NULL | `—` |
| 4 | `anchor_date` | date | NOT NULL | `—` |
| 5 | `from_seq` | bigint(64) | NOT NULL | `—` |
| 6 | `to_seq` | bigint(64) | NOT NULL | `—` |
| 7 | `head_hash` | text | NOT NULL | `—` |
| 8 | `event_count` | bigint(64) | NOT NULL | `—` |
| 9 | `object_ref` | text | NOT NULL | `''::text` |
| 10 | `content_hash` | text | NOT NULL | `''::text` |
| 11 | `archive_hash` | text | NOT NULL | `''::text` |
| 12 | `archive_intact` | boolean | NOT NULL | `false` |
| 13 | `disposition` | text | NOT NULL | `'sealed_unrecoverable'::text` |
| 14 | `created_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`audit_integrity_incident_anchors_pkey` `PRIMARY KEY (id)`
- UNIQUE（1）：`audit_integrity_incident_anch_incident_id_chain_anchor_date_key` `UNIQUE (incident_id, chain, anchor_date)`
- FOREIGN KEY（1）：`audit_integrity_incident_anchors_incident_id_fkey` `FOREIGN KEY (incident_id) REFERENCES audit_integrity_incidents(id)`
- CHECK（1）：`audit_integrity_incident_anchors_disposition_check` `CHECK ((disposition = 'sealed_unrecoverable'::text))`
- NOT NULL 约束对象：14 个（详见上表「可空」列）

**索引（2）**

- `audit_integrity_incident_anch_incident_id_chain_anchor_date_key` — `CREATE UNIQUE INDEX audit_integrity_incident_anch_incident_id_chain_anchor_date_key ON public.audit_integrity_incident_anchors USING btree (incident_id, chain, anchor_date)`
- `audit_integrity_incident_anchors_pkey` — `CREATE UNIQUE INDEX audit_integrity_incident_anchors_pkey ON public.audit_integrity_incident_anchors USING btree (id)`

<a id="audit-integrity-incident-events"></a>

### `audit_integrity_incident_events`

| 属性 | 值 |
|---|---|
| 中文用途 | 审计完整性事件明细（append-only 哈希链）。 |
| 数据所有者领域 | `audit` — 依据：`grep -rl -w audit_integrity_incident_events backend/internal` → audit(1) |
| 敏感级别 | **restricted** — 依据：审计链/认证事件（append-only；哈希链字段：prev_hash, event_hash） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0055_s6_audit_integrity_boundary.sql:114` |
| 行数（精确 `count(*)`） | **4** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **64.0 KiB**（heap 8.0 KiB / index 48.0 KiB / 共 65,536 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `incident_ref` | text | NOT NULL | `—` |
| 3 | `incident_id` | uuid | 可空 | `—` |
| 4 | `approval_request_id` | uuid | 可空 | `—` |
| 5 | `chain` | text | NOT NULL | `—` |
| 6 | `seq` | bigint(64) | NOT NULL | `—` |
| 7 | `event` | text | NOT NULL | `—` |
| 8 | `actor_id` | uuid | 可空 | `—` |
| 9 | `actor_email` | text | NOT NULL | `''::text` |
| 10 | `note` | text | NOT NULL | `''::text` |
| 11 | `detail` | jsonb | NOT NULL | `'{}'::jsonb` |
| 12 | `prev_hash` | text | NOT NULL | `—` |
| 13 | `event_hash` | text | NOT NULL | `—` |
| 14 | `occurred_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`audit_integrity_incident_events_pkey` `PRIMARY KEY (id)`
- UNIQUE（1）：`audit_integrity_incident_events_incident_ref_seq_key` `UNIQUE (incident_ref, seq)`
- FOREIGN KEY（2）：`audit_integrity_incident_events_approval_request_id_fkey` `FOREIGN KEY (approval_request_id) REFERENCES approval_requests(id)`；`audit_integrity_incident_events_incident_id_fkey` `FOREIGN KEY (incident_id) REFERENCES audit_integrity_incidents(id)`
- CHECK（2）：`audit_integrity_incident_events_event_check` `CHECK ((event = ANY (ARRAY['detected'::text, 'requested'::text, 'approved'::text, 'rejected'::text, 'sealed'::text, 'failed'::text])))`；`audit_integrity_incident_events_hash_len` `CHECK ((length(event_hash) = 64))`
- NOT NULL 约束对象：11 个（详见上表「可空」列）

**索引（3）**

- `audit_integrity_incident_events_incident_ref_seq_key` — `CREATE UNIQUE INDEX audit_integrity_incident_events_incident_ref_seq_key ON public.audit_integrity_incident_events USING btree (incident_ref, seq)`
- `audit_integrity_incident_events_pkey` — `CREATE UNIQUE INDEX audit_integrity_incident_events_pkey ON public.audit_integrity_incident_events USING btree (id)`
- `audit_integrity_incident_events_ref_idx` — `CREATE INDEX audit_integrity_incident_events_ref_idx ON public.audit_integrity_incident_events USING btree (incident_ref, seq)`

<a id="audit-integrity-incidents"></a>

### `audit_integrity_incidents`

| 属性 | 值 |
|---|---|
| 中文用途 | 审计完整性事件：链断裂/锚点不匹配的封存记录与封印哈希。 |
| 数据所有者领域 | `audit` — 依据：`grep -rl -w audit_integrity_incidents backend/internal` → audit(1) |
| 敏感级别 | **restricted** — 依据：审计链/认证事件（append-only；哈希链字段：sealed_tail_hash, evidence_hash） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0055_s6_audit_integrity_boundary.sql:41` |
| 行数（精确 `count(*)`） | **1** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **80.0 KiB**（heap 8.0 KiB / index 64.0 KiB / 共 81,920 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `incident_ref` | text | NOT NULL | `—` |
| 3 | `chain` | text | NOT NULL | `—` |
| 4 | `kind` | text | NOT NULL | `'unrecoverable_gap'::text` |
| 5 | `gap_from_seq` | bigint(64) | NOT NULL | `—` |
| 6 | `gap_to_seq` | bigint(64) | NOT NULL | `—` |
| 7 | `gap_event_count` | bigint(64) | NOT NULL | `—` |
| 8 | `trusted_from_seq` | bigint(64) | NOT NULL | `—` |
| 9 | `sealed_tail_hash` | text | NOT NULL | `—` |
| 10 | `sealed_tail_source` | text | NOT NULL | `''::text` |
| 11 | `detection_method` | text | NOT NULL | `''::text` |
| 12 | `detected_at` | timestamp with time zone | NOT NULL | `now()` |
| 13 | `detected_by_email` | text | NOT NULL | `''::text` |
| 14 | `sealed_at` | timestamp with time zone | NOT NULL | `now()` |
| 15 | `sealed_by` | uuid | 可空 | `—` |
| 16 | `sealed_by_email` | text | NOT NULL | `''::text` |
| 17 | `reason` | text | NOT NULL | `—` |
| 18 | `approval_request_id` | uuid | 可空 | `—` |
| 19 | `evidence` | jsonb | NOT NULL | `'{}'::jsonb` |
| 20 | `evidence_hash` | text | NOT NULL | `—` |
| 21 | `created_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`audit_integrity_incidents_pkey` `PRIMARY KEY (id)`
- UNIQUE（2）：`audit_integrity_incidents_chain_gap_from_seq_gap_to_seq_key` `UNIQUE (chain, gap_from_seq, gap_to_seq)`；`audit_integrity_incidents_incident_ref_key` `UNIQUE (incident_ref)`
- FOREIGN KEY（2）：`audit_integrity_incidents_approval_request_id_fkey` `FOREIGN KEY (approval_request_id) REFERENCES approval_requests(id)`；`audit_integrity_incidents_sealed_by_fkey` `FOREIGN KEY (sealed_by) REFERENCES identities(id)`
- CHECK（5）：`audit_integrity_incidents_evidence_hash_len` `CHECK ((length(evidence_hash) = 64))`；`audit_integrity_incidents_gap_order` `CHECK ((gap_to_seq >= gap_from_seq))`；`audit_integrity_incidents_head_hash_len` `CHECK ((length(sealed_tail_hash) = 64))`；`audit_integrity_incidents_kind_check` `CHECK ((kind = 'unrecoverable_gap'::text))`；`audit_integrity_incidents_trusted_after_gap` `CHECK ((trusted_from_seq = (gap_to_seq + 1)))`
- NOT NULL 约束对象：19 个（详见上表「可空」列）

**索引（4）**

- `audit_integrity_incidents_chain_gap_from_seq_gap_to_seq_key` — `CREATE UNIQUE INDEX audit_integrity_incidents_chain_gap_from_seq_gap_to_seq_key ON public.audit_integrity_incidents USING btree (chain, gap_from_seq, gap_to_seq)`
- `audit_integrity_incidents_chain_idx` — `CREATE INDEX audit_integrity_incidents_chain_idx ON public.audit_integrity_incidents USING btree (chain, sealed_at DESC)`
- `audit_integrity_incidents_incident_ref_key` — `CREATE UNIQUE INDEX audit_integrity_incidents_incident_ref_key ON public.audit_integrity_incidents USING btree (incident_ref)`
- `audit_integrity_incidents_pkey` — `CREATE UNIQUE INDEX audit_integrity_incidents_pkey ON public.audit_integrity_incidents USING btree (id)`

<a id="backup-jobs"></a>

### `backup_jobs`

| 属性 | 值 |
|---|---|
| 中文用途 | 备份作业：范围、对象键、状态与请求人。 |
| 数据所有者领域 | `api` — 依据：仅命中通用包 → api(2) |
| 敏感级别 | **confidential** — 依据：含 PII 字段：requested_by_email |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0052_s6_data_governance.sql:100` |
| 行数（精确 `count(*)`） | **0** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **24.0 KiB**（heap 0 B / index 16.0 KiB / 共 24,576 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `kind` | text | NOT NULL | `—` |
| 3 | `target` | text | NOT NULL | `—` |
| 4 | `status` | text | NOT NULL | `'queued'::text` |
| 5 | `drill` | boolean | NOT NULL | `false` |
| 6 | `requested_by` | uuid | 可空 | `—` |
| 7 | `requested_by_email` | text | NOT NULL | `''::text` |
| 8 | `approval_request_id` | uuid | 可空 | `—` |
| 9 | `backup_id` | uuid | 可空 | `—` |
| 10 | `started_at` | timestamp with time zone | 可空 | `—` |
| 11 | `finished_at` | timestamp with time zone | 可空 | `—` |
| 12 | `size_bytes` | bigint(64) | NOT NULL | `0` |
| 13 | `object_ref` | text | NOT NULL | `''::text` |
| 14 | `checksum` | text | NOT NULL | `''::text` |
| 15 | `schema_version` | text | NOT NULL | `''::text` |
| 16 | `build_sha` | text | NOT NULL | `''::text` |
| 17 | `verify_status` | text | NOT NULL | `''::text` |
| 18 | `verify_detail` | jsonb | NOT NULL | `'{}'::jsonb` |
| 19 | `rpo_seconds` | bigint(64) | 可空 | `—` |
| 20 | `rto_seconds` | bigint(64) | 可空 | `—` |
| 21 | `maintenance_window` | text | NOT NULL | `''::text` |
| 22 | `notes` | text | NOT NULL | `''::text` |
| 23 | `result` | jsonb | 可空 | `—` |
| 24 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 25 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`backup_jobs_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（3）：`backup_jobs_approval_request_id_fkey` `FOREIGN KEY (approval_request_id) REFERENCES approval_requests(id)`；`backup_jobs_backup_id_fkey` `FOREIGN KEY (backup_id) REFERENCES backup_jobs(id) ON DELETE SET NULL`；`backup_jobs_requested_by_fkey` `FOREIGN KEY (requested_by) REFERENCES identities(id)`
- CHECK（3）：`backup_jobs_kind_check` `CHECK ((kind = ANY (ARRAY['backup'::text, 'restore'::text, 'verify'::text])))`；`backup_jobs_status_check` `CHECK ((status = ANY (ARRAY['queued'::text, 'running'::text, 'completed'::text, 'failed'::text, 'verified'::text, 'blocked'::text, 'rejected'::text, 'previewed'::text])))`；`backup_jobs_target_check` `CHECK ((target = ANY (ARRAY['postgres'::text, 'clickhouse'::text, 'minio'::text, 'config'::text, 'all'::text])))`
- NOT NULL 约束对象：17 个（详见上表「可空」列）

**索引（2）**

- `backup_jobs_kind_idx` — `CREATE INDEX backup_jobs_kind_idx ON public.backup_jobs USING btree (kind, created_at DESC)`
- `backup_jobs_pkey` — `CREATE UNIQUE INDEX backup_jobs_pkey ON public.backup_jobs USING btree (id)`

<a id="canvas-snapshots"></a>

### `canvas_snapshots`

| 属性 | 值 |
|---|---|
| 中文用途 | 画布快照：画布状态序列化与版本。 |
| 数据所有者领域 | `report` — 依据：`grep -rl -w canvas_snapshots backend/internal` → report(1) investigation(1) canvas(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0030_canvas_phase3.sql:29` |
| 行数（精确 `count(*)`） | **21** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **744.0 KiB**（heap 8.0 KiB / index 32.0 KiB / 共 761,856 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `canvas_id` | uuid | NOT NULL | `—` |
| 3 | `canvas_revision` | bigint(64) | NOT NULL | `—` |
| 4 | `name` | text | NOT NULL | `—` |
| 5 | `state` | jsonb | NOT NULL | `—` |
| 6 | `created_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`canvas_snapshots_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（1）：`canvas_snapshots_canvas_id_fkey` `FOREIGN KEY (canvas_id) REFERENCES investigation_canvases(id) ON DELETE CASCADE`
- CHECK（0）：无
- NOT NULL 约束对象：6 个（详见上表「可空」列）

**索引（2）**

- `canvas_snapshots_pkey` — `CREATE UNIQUE INDEX canvas_snapshots_pkey ON public.canvas_snapshots USING btree (id)`
- `idx_canvas_snapshots_canvas_created` — `CREATE INDEX idx_canvas_snapshots_canvas_created ON public.canvas_snapshots USING btree (canvas_id, created_at DESC)`

<a id="case-activity"></a>

### `case_activity`

| 属性 | 值 |
|---|---|
| 中文用途 | 案件业务时间线：用户可理解的案件活动流水。 |
| 数据所有者领域 | `case` — 依据：`data_asset_registry.owner_module` |
| 敏感级别 | **internal** — 依据：`data_asset_registry.sensitivity` |
| 保留期 | 在线 365 天 — 依据：`data_asset_registry.retention`（写入见 `backend/migrations/postgres/0044_tenant_scope.sql:155-176`、`0047_case_collaboration.sql:267-279`） |
| 建表迁移 | `backend/migrations/postgres/0047_case_collaboration.sql:66` |
| 行数（精确 `count(*)`） | **524** |
| 行数（`pg_class.reltuples` 估算） | 476 |
| 大小 | **392.0 KiB**（heap 216.0 KiB / index 144.0 KiB / 共 401,408 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `case_id` | uuid | NOT NULL | `—` |
| 3 | `event_type` | character varying(64) | NOT NULL | `—` |
| 4 | `actor_identity_id` | uuid | 可空 | `—` |
| 5 | `actor_display` | character varying(255) | NOT NULL | `''::character varying` |
| 6 | `summary` | text | NOT NULL | `''::text` |
| 7 | `object_type` | character varying(48) | NOT NULL | `''::character varying` |
| 8 | `object_id` | text | NOT NULL | `''::text` |
| 9 | `detail` | jsonb | NOT NULL | `'{}'::jsonb` |
| 10 | `trace_id` | character varying(64) | NOT NULL | `''::character varying` |
| 11 | `created_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`case_activity_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（2）：`case_activity_actor_identity_id_fkey` `FOREIGN KEY (actor_identity_id) REFERENCES identities(id)`；`case_activity_case_id_fkey` `FOREIGN KEY (case_id) REFERENCES cases(id) ON DELETE CASCADE`
- CHECK（0）：无
- NOT NULL 约束对象：10 个（详见上表「可空」列）

**索引（3）**

- `case_activity_pkey` — `CREATE UNIQUE INDEX case_activity_pkey ON public.case_activity USING btree (id)`
- `idx_case_activity_actor` — `CREATE INDEX idx_case_activity_actor ON public.case_activity USING btree (actor_identity_id, created_at DESC)`
- `idx_case_activity_case` — `CREATE INDEX idx_case_activity_case ON public.case_activity USING btree (case_id, created_at DESC)`

<a id="case-comments"></a>

### `case_comments`

| 属性 | 值 |
|---|---|
| 中文用途 | 案件协作评论：正文与 body_hash。 |
| 数据所有者领域 | `case` — 依据：`data_asset_registry.owner_module` |
| 敏感级别 | **confidential** — 依据：`data_asset_registry.sensitivity` |
| 保留期 | 随案件归档 — 依据：`data_asset_registry.retention`（写入见 `backend/migrations/postgres/0044_tenant_scope.sql:155-176`、`0047_case_collaboration.sql:267-279`） |
| 建表迁移 | `backend/migrations/postgres/0047_case_collaboration.sql:34` |
| 行数（精确 `count(*)`） | **16** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **48.0 KiB**（heap 8.0 KiB / index 32.0 KiB / 共 49,152 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `case_id` | uuid | NOT NULL | `—` |
| 3 | `thread_id` | uuid | 可空 | `—` |
| 4 | `parent_id` | uuid | 可空 | `—` |
| 5 | `body_markdown` | text | NOT NULL | `—` |
| 6 | `body_hash` | character varying(64) | NOT NULL | `—` |
| 7 | `created_by` | uuid | 可空 | `—` |
| 8 | `edited_revision` | bigint(64) | NOT NULL | `0` |
| 9 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 10 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`case_comments_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（3）：`case_comments_case_id_fkey` `FOREIGN KEY (case_id) REFERENCES cases(id) ON DELETE CASCADE`；`case_comments_created_by_fkey` `FOREIGN KEY (created_by) REFERENCES identities(id)`；`case_comments_parent_id_fkey` `FOREIGN KEY (parent_id) REFERENCES case_comments(id) ON DELETE CASCADE`
- CHECK（0）：无
- NOT NULL 约束对象：7 个（详见上表「可空」列）

**索引（2）**

- `case_comments_pkey` — `CREATE UNIQUE INDEX case_comments_pkey ON public.case_comments USING btree (id)`
- `idx_case_comments_case` — `CREATE INDEX idx_case_comments_case ON public.case_comments USING btree (case_id, created_at DESC)`

<a id="case-members"></a>

### `case_members`

| 属性 | 值 |
|---|---|
| 中文用途 | 案件成员与角色。 |
| 数据所有者领域 | `case` — 依据：`data_asset_registry.owner_module` |
| 敏感级别 | **internal** — 依据：`data_asset_registry.sensitivity` |
| 保留期 | 随案件归档 — 依据：`data_asset_registry.retention`（写入见 `backend/migrations/postgres/0044_tenant_scope.sql:155-176`、`0047_case_collaboration.sql:267-279`） |
| 建表迁移 | `backend/migrations/postgres/0047_case_collaboration.sql:20` |
| 行数（精确 `count(*)`） | **105** |
| 行数（`pg_class.reltuples` 估算） | 94 |
| 大小 | **120.0 KiB**（heap 32.0 KiB / index 64.0 KiB / 共 122,880 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `case_id` | uuid | NOT NULL | `—` |
| 3 | `identity_id` | uuid | NOT NULL | `—` |
| 4 | `role` | character varying(24) | NOT NULL | `'investigator'::character varying` |
| 5 | `added_by` | uuid | 可空 | `—` |
| 6 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 7 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`case_members_pkey` `PRIMARY KEY (id)`
- UNIQUE（1）：`case_members_case_id_identity_id_key` `UNIQUE (case_id, identity_id)`
- FOREIGN KEY（3）：`case_members_added_by_fkey` `FOREIGN KEY (added_by) REFERENCES identities(id)`；`case_members_case_id_fkey` `FOREIGN KEY (case_id) REFERENCES cases(id) ON DELETE CASCADE`；`case_members_identity_id_fkey` `FOREIGN KEY (identity_id) REFERENCES identities(id) ON DELETE CASCADE`
- CHECK（1）：`case_members_role_check` `CHECK (((role)::text = ANY ((ARRAY['lead'::character varying, 'investigator'::character varying, 'reviewer'::character varying, 'viewer'::character varying])::text[])))`
- NOT NULL 约束对象：6 个（详见上表「可空」列）

**索引（4）**

- `case_members_case_id_identity_id_key` — `CREATE UNIQUE INDEX case_members_case_id_identity_id_key ON public.case_members USING btree (case_id, identity_id)`
- `case_members_pkey` — `CREATE UNIQUE INDEX case_members_pkey ON public.case_members USING btree (id)`
- `idx_case_members_case` — `CREATE INDEX idx_case_members_case ON public.case_members USING btree (case_id, created_at)`
- `idx_case_members_identity` — `CREATE INDEX idx_case_members_identity ON public.case_members USING btree (identity_id, case_id)`

<a id="case-subjects"></a>

### `case_subjects`

| 属性 | 值 |
|---|---|
| 中文用途 | 案件调查对象（地址/实体）与对象类型。 |
| 数据所有者领域 | `case` — 依据：`data_asset_registry.owner_module` |
| 敏感级别 | **confidential** — 依据：`data_asset_registry.sensitivity` |
| 保留期 | 随案件归档 — 依据：`data_asset_registry.retention`（写入见 `backend/migrations/postgres/0044_tenant_scope.sql:155-176`、`0047_case_collaboration.sql:267-279`） |
| 建表迁移 | `backend/migrations/postgres/0047_case_collaboration.sql:48` |
| 行数（精确 `count(*)`） | **31** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **64.0 KiB**（heap 8.0 KiB / index 48.0 KiB / 共 65,536 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `case_id` | uuid | NOT NULL | `—` |
| 3 | `subject_type` | character varying(24) | NOT NULL | `—` |
| 4 | `chain` | character varying(32) | 可空 | `—` |
| 5 | `value` | text | NOT NULL | `—` |
| 6 | `normalized_value` | text | NOT NULL | `—` |
| 7 | `label` | character varying(255) | NOT NULL | `''::character varying` |
| 8 | `properties` | jsonb | NOT NULL | `'{}'::jsonb` |
| 9 | `revision` | bigint(64) | NOT NULL | `1` |
| 10 | `created_by` | uuid | 可空 | `—` |
| 11 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 12 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`case_subjects_pkey` `PRIMARY KEY (id)`
- UNIQUE（1）：`case_subjects_case_id_subject_type_chain_normalized_value_key` `UNIQUE (case_id, subject_type, chain, normalized_value)`
- FOREIGN KEY（2）：`case_subjects_case_id_fkey` `FOREIGN KEY (case_id) REFERENCES cases(id) ON DELETE CASCADE`；`case_subjects_created_by_fkey` `FOREIGN KEY (created_by) REFERENCES identities(id)`
- CHECK（1）：`case_subjects_subject_type_check` `CHECK (((subject_type)::text = ANY ((ARRAY['ADDRESS'::character varying, 'TRANSACTION'::character varying, 'DOMAIN'::character varying, 'IP'::character varying, 'APK'::character varying, 'ENTITY'::character varying, 'OTHER'::character varying])::text[])))`
- NOT NULL 约束对象：10 个（详见上表「可空」列）

**索引（3）**

- `case_subjects_case_id_subject_type_chain_normalized_value_key` — `CREATE UNIQUE INDEX case_subjects_case_id_subject_type_chain_normalized_value_key ON public.case_subjects USING btree (case_id, subject_type, chain, normalized_value)`
- `case_subjects_pkey` — `CREATE UNIQUE INDEX case_subjects_pkey ON public.case_subjects USING btree (id)`
- `idx_case_subjects_case` — `CREATE INDEX idx_case_subjects_case ON public.case_subjects USING btree (case_id, created_at)`

<a id="cases"></a>

### `cases`

| 属性 | 值 |
|---|---|
| 中文用途 | 案件主表：案件编号/名称/密级/状态，按组织与工作区隔离。 |
| 数据所有者领域 | `case` — 依据：`data_asset_registry.owner_module` |
| 敏感级别 | **confidential** — 依据：`data_asset_registry.sensitivity` |
| 保留期 | 案件归档后 3 年 — 依据：`data_asset_registry.retention`（写入见 `backend/migrations/postgres/0044_tenant_scope.sql:155-176`、`0047_case_collaboration.sql:267-279`） |
| 建表迁移 | `backend/migrations/postgres/0004_v12_metadata.sql:2` |
| 行数（精确 `count(*)`） | **116** |
| 行数（`pg_class.reltuples` 估算） | 105 |
| 大小 | **112.0 KiB**（heap 32.0 KiB / index 48.0 KiB / 共 114,688 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `organization_id` | uuid | NOT NULL | `—` |
| 3 | `workspace_id` | uuid | NOT NULL | `—` |
| 4 | `case_no` | character varying(128) | 可空 | `—` |
| 5 | `name` | character varying(255) | NOT NULL | `—` |
| 6 | `description` | text | 可空 | `—` |
| 7 | `status` | character varying(32) | NOT NULL | `'active'::character varying` |
| 8 | `color` | character varying(32) | 可空 | `—` |
| 9 | `created_by` | uuid | 可空 | `—` |
| 10 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 11 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |
| 12 | `revision` | bigint(64) | NOT NULL | `1` |
| 13 | `classification` | character varying(24) | NOT NULL | `'internal'::character varying` |
| 14 | `priority` | character varying(16) | NOT NULL | `'medium'::character varying` |
| 15 | `archived_at` | timestamp with time zone | 可空 | `—` |
| 16 | `closed_at` | timestamp with time zone | 可空 | `—` |

**约束**

- PRIMARY KEY（1）：`cases_pkey` `PRIMARY KEY (id)`
- UNIQUE（1）：`cases_workspace_id_name_key` `UNIQUE (workspace_id, name)`
- FOREIGN KEY（2）：`cases_organization_id_fkey` `FOREIGN KEY (organization_id) REFERENCES organizations(id)`；`cases_workspace_id_fkey` `FOREIGN KEY (workspace_id) REFERENCES workspaces(id)`
- CHECK（2）：`cases_classification_check` `CHECK (((classification)::text = ANY ((ARRAY['public'::character varying, 'internal'::character varying, 'confidential'::character varying, 'restricted'::character varying])::text[])))`；`cases_priority_check` `CHECK (((priority)::text = ANY ((ARRAY['low'::character varying, 'medium'::character varying, 'high'::character varying, 'critical'::character varying])::text[])))`
- NOT NULL 约束对象：10 个（详见上表「可空」列）

**索引（3）**

- `cases_pkey` — `CREATE UNIQUE INDEX cases_pkey ON public.cases USING btree (id)`
- `cases_scope_time_idx` — `CREATE INDEX cases_scope_time_idx ON public.cases USING btree (organization_id, workspace_id, updated_at DESC)`
- `cases_workspace_id_name_key` — `CREATE UNIQUE INDEX cases_workspace_id_name_key ON public.cases USING btree (workspace_id, name)`

<a id="chain-capabilities"></a>

### `chain_capabilities`

| 属性 | 值 |
|---|---|
| 中文用途 | 链能力矩阵：各链支持的数据集与能力项。 |
| 数据所有者领域 | `provider` — 依据：`grep -rl -w chain_capabilities backend/internal` → provider(2) store(1) countoracle(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0015_v12_count_oracle.sql:139` |
| 行数（精确 `count(*)`） | **8** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **32.0 KiB**（heap 8.0 KiB / index 16.0 KiB / 共 32,768 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `chain` | text | NOT NULL | `—` |
| 2 | `evm_chain_id` | bigint(64) | 可空 | `0` |
| 3 | `ankr_alias` | text | NOT NULL | `''::text` |
| 4 | `chainbase_tx_count` | boolean | NOT NULL | `false` |
| 5 | `chainbase_token_count` | boolean | NOT NULL | `false` |
| 6 | `ankr_transactions` | boolean | NOT NULL | `false` |
| 7 | `ankr_token_transfers` | boolean | NOT NULL | `false` |
| 8 | `range_supported` | boolean | NOT NULL | `false` |
| 9 | `enabled` | boolean | NOT NULL | `true` |
| 10 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |
| 11 | `chain_type` | text | NOT NULL | `'evm'::text` |
| 12 | `native_symbol` | text | NOT NULL | `''::text` |

**约束**

- PRIMARY KEY（1）：`chain_capabilities_pkey` `PRIMARY KEY (chain)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：11 个（详见上表「可空」列）

**索引（1）**

- `chain_capabilities_pkey` — `CREATE UNIQUE INDEX chain_capabilities_pkey ON public.chain_capabilities USING btree (chain)`

<a id="checkpoints"></a>

### `checkpoints`

| 属性 | 值 |
|---|---|
| 中文用途 | 断点/水位表：任务或数据集的处理水位与最后成功时间。 |
| 数据所有者领域 | `api` — 依据：仅命中通用包 → api(4) store(3) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0001_init.sql:155` |
| 行数（精确 `count(*)`） | **19,975** |
| 行数（`pg_class.reltuples` 估算） | 19,962 |
| 大小 | **5.39 MiB**（heap 4.33 MiB / index 1.02 MiB / 共 5,652,480 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `shard_id` | uuid | NOT NULL | `—` |
| 2 | `job_id` | uuid | NOT NULL | `—` |
| 3 | `range_start` | bigint(64) | NOT NULL | `—` |
| 4 | `range_end` | bigint(64) | NOT NULL | `—` |
| 5 | `next_page_token` | text | NOT NULL | `''::text` |
| 6 | `page_no` | integer(32) | NOT NULL | `0` |
| 7 | `downloaded_rows` | bigint(64) | NOT NULL | `0` |
| 8 | `downloaded_bytes` | bigint(64) | NOT NULL | `0` |
| 9 | `last_success_at` | timestamp with time zone | 可空 | `—` |
| 10 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`checkpoints_pkey` `PRIMARY KEY (shard_id)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：9 个（详见上表「可空」列）

**索引（2）**

- `checkpoints_pkey` — `CREATE UNIQUE INDEX checkpoints_pkey ON public.checkpoints USING btree (shard_id)`
- `idx_checkpoints_job` — `CREATE INDEX idx_checkpoints_job ON public.checkpoints USING btree (job_id)`

<a id="configuration-revisions"></a>

### `configuration_revisions`

| 属性 | 值 |
|---|---|
| 中文用途 | 配置版本历史：前后哈希、操作者与是否密文。 |
| 数据所有者领域 | `api` — 依据：仅命中通用包 → api(2) |
| 敏感级别 | **confidential** — 依据：含凭证/密钥类字段：secret_ref, is_secret, before_hash, after_hash |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0051_s6_control_governance.sql:28` |
| 行数（精确 `count(*)`） | **21** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **64.0 KiB**（heap 8.0 KiB / index 48.0 KiB / 共 65,536 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `key` | text | NOT NULL | `—` |
| 3 | `revision` | bigint(64) | NOT NULL | `—` |
| 4 | `value` | jsonb | 可空 | `—` |
| 5 | `secret_ref` | text | NOT NULL | `''::text` |
| 6 | `is_secret` | boolean | NOT NULL | `false` |
| 7 | `before_hash` | text | NOT NULL | `''::text` |
| 8 | `after_hash` | text | NOT NULL | `''::text` |
| 9 | `change_note` | text | NOT NULL | `''::text` |
| 10 | `actor_id` | uuid | 可空 | `—` |
| 11 | `actor_email` | text | NOT NULL | `''::text` |
| 12 | `request_id` | text | NOT NULL | `''::text` |
| 13 | `trace_id` | text | NOT NULL | `''::text` |
| 14 | `approval_request_id` | uuid | 可空 | `—` |
| 15 | `created_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`configuration_revisions_pkey` `PRIMARY KEY (id)`
- UNIQUE（1）：`configuration_revisions_key_revision_key` `UNIQUE (key, revision)`
- FOREIGN KEY（2）：`configuration_revisions_actor_id_fkey` `FOREIGN KEY (actor_id) REFERENCES identities(id)`；`configuration_revisions_approval_request_id_fkey` `FOREIGN KEY (approval_request_id) REFERENCES approval_requests(id)`
- CHECK（0）：无
- NOT NULL 约束对象：12 个（详见上表「可空」列）

**索引（3）**

- `configuration_revisions_key_idx` — `CREATE INDEX configuration_revisions_key_idx ON public.configuration_revisions USING btree (key, revision DESC)`
- `configuration_revisions_key_revision_key` — `CREATE UNIQUE INDEX configuration_revisions_key_revision_key ON public.configuration_revisions USING btree (key, revision)`
- `configuration_revisions_pkey` — `CREATE UNIQUE INDEX configuration_revisions_pkey ON public.configuration_revisions USING btree (id)`

<a id="configuration-settings"></a>

### `configuration_settings`

| 属性 | 值 |
|---|---|
| 中文用途 | 配置项：值、是否密文、secret_ref 与版本。 |
| 数据所有者领域 | `api` — 依据：仅命中通用包 → api(3) |
| 敏感级别 | **confidential** — 依据：含凭证/密钥类字段：secret_ref, is_secret |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0051_s6_control_governance.sql:11` |
| 行数（精确 `count(*)`） | **8** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **48.0 KiB**（heap 8.0 KiB / index 32.0 KiB / 共 49,152 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `key` | text | NOT NULL | `—` |
| 2 | `category` | text | NOT NULL | `'general'::text` |
| 3 | `value_type` | text | NOT NULL | `'string'::text` |
| 4 | `value` | jsonb | NOT NULL | `'null'::jsonb` |
| 5 | `secret_ref` | text | NOT NULL | `''::text` |
| 6 | `is_secret` | boolean | NOT NULL | `false` |
| 7 | `description` | text | NOT NULL | `''::text` |
| 8 | `revision` | bigint(64) | NOT NULL | `1` |
| 9 | `updated_by` | uuid | 可空 | `—` |
| 10 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |
| 11 | `created_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`configuration_settings_pkey` `PRIMARY KEY (key)`
- UNIQUE（0）：无
- FOREIGN KEY（1）：`configuration_settings_updated_by_fkey` `FOREIGN KEY (updated_by) REFERENCES identities(id)`
- CHECK（1）：`configuration_settings_value_type_check` `CHECK ((value_type = ANY (ARRAY['string'::text, 'int'::text, 'bool'::text, 'json'::text, 'secret-ref'::text])))`
- NOT NULL 约束对象：10 个（详见上表「可空」列）

**索引（2）**

- `configuration_settings_category_idx` — `CREATE INDEX configuration_settings_category_idx ON public.configuration_settings USING btree (category, key)`
- `configuration_settings_pkey` — `CREATE UNIQUE INDEX configuration_settings_pkey ON public.configuration_settings USING btree (key)`

<a id="contract-abis"></a>

### `contract_abis`

| 属性 | 值 |
|---|---|
| 中文用途 | 合约 ABI 缓存：按地址缓存的 ABI 与抓取时间（当前无数据）。 |
| 数据所有者领域 | `method` — 依据：`grep -rl -w contract_abis backend/internal` → method(2) store(1) |
| 敏感级别 | **internal** — 依据：业务数据（含链上标识） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0005_method_abi.sql:19` |
| 行数（精确 `count(*)`） | **0** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **64.0 KiB**（heap 8.0 KiB / index 48.0 KiB / 共 65,536 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `chain` | character varying(32) | NOT NULL | `—` |
| 3 | `contract_address` | character varying(128) | NOT NULL | `—` |
| 4 | `abi_json` | jsonb | NOT NULL | `—` |
| 5 | `source` | character varying(32) | NOT NULL | `—` |
| 6 | `verified` | boolean | NOT NULL | `false` |
| 7 | `implementation_address` | character varying(128) | 可空 | `—` |
| 8 | `fetched_at` | timestamp with time zone | 可空 | `—` |
| 9 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`contract_abis_pkey` `PRIMARY KEY (id)`
- UNIQUE（1）：`contract_abis_chain_contract_address_source_key` `UNIQUE (chain, contract_address, source)`
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：7 个（详见上表「可空」列）

**索引（3）**

- `contract_abis_chain_contract_address_source_key` — `CREATE UNIQUE INDEX contract_abis_chain_contract_address_source_key ON public.contract_abis USING btree (chain, contract_address, source)`
- `contract_abis_pkey` — `CREATE UNIQUE INDEX contract_abis_pkey ON public.contract_abis USING btree (id)`
- `idx_contract_abis_lookup` — `CREATE INDEX idx_contract_abis_lookup ON public.contract_abis USING btree (chain, contract_address)`

<a id="count-oracle-calibration"></a>

### `count_oracle_calibration`

| 属性 | 值 |
|---|---|
| 中文用途 | Count Oracle 校准参数：按链/数据集的校准系数。 |
| 数据所有者领域 | `countoracle` — 依据：`grep -rl -w count_oracle_calibration backend/internal` → store(1) countoracle(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0015_v12_count_oracle.sql:54` |
| 行数（精确 `count(*)`） | **3** |
| 行数（`pg_class.reltuples` 估算） | 3 |
| 大小 | **32.0 KiB**（heap 8.0 KiB / index 16.0 KiB / 共 32,768 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `chain` | text | NOT NULL | `—` |
| 2 | `dataset` | text | NOT NULL | `—` |
| 3 | `sample_count` | integer(32) | NOT NULL | `0` |
| 4 | `ratio_p50` | double precision(53) | NOT NULL | `1` |
| 5 | `ratio_p95` | double precision(53) | NOT NULL | `1` |
| 6 | `absolute_error_p50` | double precision(53) | NOT NULL | `0` |
| 7 | `absolute_error_p95` | double precision(53) | NOT NULL | `0` |
| 8 | `false_zero_count` | integer(32) | NOT NULL | `0` |
| 9 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |
| 10 | `provider` | text | NOT NULL | `'chainbase'::text` |

**约束**

- PRIMARY KEY（1）：`count_oracle_calibration_pkey` `PRIMARY KEY (provider, chain, dataset)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：10 个（详见上表「可空」列）

**索引（1）**

- `count_oracle_calibration_pkey` — `CREATE UNIQUE INDEX count_oracle_calibration_pkey ON public.count_oracle_calibration USING btree (provider, chain, dataset)`

<a id="count-oracle-calibration-samples"></a>

### `count_oracle_calibration_samples`

| 属性 | 值 |
|---|---|
| 中文用途 | 校准样本：单次观测的预测/实测对比与 Provider 维度。 |
| 数据所有者领域 | `store` — 依据：仅命中通用包 → store(1) |
| 敏感级别 | **internal** — 依据：业务数据（含链上标识） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0015_v12_count_oracle.sql:42` |
| 行数（精确 `count(*)`） | **52** |
| 行数（`pg_class.reltuples` 估算） | 51 |
| 大小 | **64.0 KiB**（heap 8.0 KiB / index 48.0 KiB / 共 65,536 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `—` |
| 2 | `chain` | text | NOT NULL | `—` |
| 3 | `dataset` | text | NOT NULL | `—` |
| 4 | `address_hash` | text | NOT NULL | `''::text` |
| 5 | `predicted_count` | bigint(64) | NOT NULL | `0` |
| 6 | `actual_count` | bigint(64) | NOT NULL | `0` |
| 7 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 8 | `provider` | text | NOT NULL | `'chainbase'::text` |

**约束**

- PRIMARY KEY（1）：`count_oracle_calibration_samples_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：8 个（详见上表「可空」列）

**索引（3）**

- `count_oracle_calibration_samples_pkey` — `CREATE UNIQUE INDEX count_oracle_calibration_samples_pkey ON public.count_oracle_calibration_samples USING btree (id)`
- `idx_calib_samples_lookup` — `CREATE INDEX idx_calib_samples_lookup ON public.count_oracle_calibration_samples USING btree (chain, dataset, created_at DESC)`
- `idx_calib_samples_provider` — `CREATE INDEX idx_calib_samples_provider ON public.count_oracle_calibration_samples USING btree (provider, chain, dataset, created_at DESC)`

<a id="count-oracle-observations"></a>

### `count_oracle_observations`

| 属性 | 值 |
|---|---|
| 中文用途 | Count Oracle 观测：对 (chain,dataset,range) 的计数观测与 address_hash。 |
| 数据所有者领域 | `store` — 依据：仅命中通用包 → store(1) |
| 敏感级别 | **internal** — 依据：业务数据（含链上标识） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0015_v12_count_oracle.sql:20` |
| 行数（精确 `count(*)`） | **66** |
| 行数（`pg_class.reltuples` 估算） | 52 |
| 大小 | **96.0 KiB**（heap 16.0 KiB / index 48.0 KiB / 共 98,304 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `—` |
| 2 | `provider` | text | NOT NULL | `'chainbase'::text` |
| 3 | `chain` | text | NOT NULL | `—` |
| 4 | `dataset` | text | NOT NULL | `—` |
| 5 | `address_hash` | text | NOT NULL | `''::text` |
| 6 | `from_block` | bigint(64) | NOT NULL | `0` |
| 7 | `to_block` | bigint(64) | NOT NULL | `0` |
| 8 | `raw_count` | bigint(64) | NOT NULL | `'-1'::integer` |
| 9 | `adjusted_count` | bigint(64) | NOT NULL | `'-1'::integer` |
| 10 | `latency_ms` | integer(32) | NOT NULL | `0` |
| 11 | `cache_hit` | boolean | NOT NULL | `false` |
| 12 | `confidence` | double precision(53) | NOT NULL | `0` |
| 13 | `status` | text | NOT NULL | `''::text` |
| 14 | `error_class` | text | NOT NULL | `''::text` |
| 15 | `job_id` | uuid | 可空 | `—` |
| 16 | `created_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`count_oracle_observations_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：15 个（详见上表「可空」列）

**索引（3）**

- `count_oracle_observations_pkey` — `CREATE UNIQUE INDEX count_oracle_observations_pkey ON public.count_oracle_observations USING btree (id)`
- `idx_count_obs_job` — `CREATE INDEX idx_count_obs_job ON public.count_oracle_observations USING btree (job_id)`
- `idx_count_obs_lookup` — `CREATE INDEX idx_count_obs_lookup ON public.count_oracle_observations USING btree (chain, dataset, created_at DESC)`

<a id="count-oracle-status"></a>

### `count_oracle_status`

| 属性 | 值 |
|---|---|
| 中文用途 | Count Oracle 状态：各链/数据集的最新可用性与降级状态。 |
| 数据所有者领域 | `provider` — 依据：`grep -rl -w count_oracle_status backend/internal` → store(1) provider(1) countoracle(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0015_v12_count_oracle.sql:122` |
| 行数（精确 `count(*)`） | **1** |
| 行数（`pg_class.reltuples` 估算） | 1 |
| 大小 | **64.0 KiB**（heap 8.0 KiB / index 16.0 KiB / 共 65,536 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `provider` | text | NOT NULL | `—` |
| 2 | `circuit_state` | text | NOT NULL | `'CLOSED'::text` |
| 3 | `requests` | bigint(64) | NOT NULL | `0` |
| 4 | `success_count` | bigint(64) | NOT NULL | `0` |
| 5 | `p50_ms` | integer(32) | NOT NULL | `0` |
| 6 | `p95_ms` | integer(32) | NOT NULL | `0` |
| 7 | `success_rate` | double precision(53) | NOT NULL | `0` |
| 8 | `cache_hit_rate` | double precision(53) | NOT NULL | `0` |
| 9 | `quota_exhausted` | boolean | NOT NULL | `false` |
| 10 | `last_error` | text | NOT NULL | `''::text` |
| 11 | `false_zero_count` | integer(32) | NOT NULL | `0` |
| 12 | `prediction_error_p50` | double precision(53) | NOT NULL | `0` |
| 13 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`count_oracle_status_pkey` `PRIMARY KEY (provider)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：13 个（详见上表「可空」列）

**索引（1）**

- `count_oracle_status_pkey` — `CREATE UNIQUE INDEX count_oracle_status_pkey ON public.count_oracle_status USING btree (provider)`

<a id="count-provider-attempts"></a>

### `count_provider_attempts`

| 属性 | 值 |
|---|---|
| 中文用途 | 计数 Provider 尝试：请求哈希、凭证槽位、结果与耗时。 |
| 数据所有者领域 | `store` — 依据：仅命中通用包 → store(1) model(1) config(1) |
| 敏感级别 | **confidential** — 依据：含凭证/密钥类字段：request_hash, credential_slot_id |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0016_count_provider_port.sql:66` |
| 行数（精确 `count(*)`） | **2** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **64.0 KiB**（heap 8.0 KiB / index 48.0 KiB / 共 65,536 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `—` |
| 2 | `request_hash` | text | NOT NULL | `''::text` |
| 3 | `provider_id` | text | NOT NULL | `—` |
| 4 | `credential_slot_id` | text | NOT NULL | `''::text` |
| 5 | `chain_id` | bigint(64) | NOT NULL | `0` |
| 6 | `dataset` | text | NOT NULL | `''::text` |
| 7 | `endpoint` | text | NOT NULL | `''::text` |
| 8 | `status` | text | NOT NULL | `''::text` |
| 9 | `error_class` | text | NOT NULL | `''::text` |
| 10 | `latency_ms` | integer(32) | NOT NULL | `0` |
| 11 | `count` | bigint(64) | NOT NULL | `'-1'::integer` |
| 12 | `requests_cost` | bigint(64) | NOT NULL | `0` |
| 13 | `credits_cost` | double precision(53) | NOT NULL | `0` |
| 14 | `usd_cost` | double precision(53) | NOT NULL | `0` |
| 15 | `created_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`count_provider_attempts_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：15 个（详见上表「可空」列）

**索引（3）**

- `count_provider_attempts_pkey` — `CREATE UNIQUE INDEX count_provider_attempts_pkey ON public.count_provider_attempts USING btree (id)`
- `idx_count_provider_attempts_hash` — `CREATE INDEX idx_count_provider_attempts_hash ON public.count_provider_attempts USING btree (request_hash)`
- `idx_count_provider_attempts_lookup` — `CREATE INDEX idx_count_provider_attempts_lookup ON public.count_provider_attempts USING btree (provider_id, dataset, created_at DESC)`

<a id="count-provider-capabilities"></a>

### `count_provider_capabilities`

| 属性 | 值 |
|---|---|
| 中文用途 | 计数 Provider 能力：支持的链/数据集与限制。 |
| 数据所有者领域 | `store` — 依据：仅命中通用包 → store(1) model(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0016_count_provider_port.sql:23` |
| 行数（精确 `count(*)`） | **14** |
| 行数（`pg_class.reltuples` 估算） | 14 |
| 大小 | **200.0 KiB**（heap 144.0 KiB / index 16.0 KiB / 共 204,800 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `provider_id` | text | NOT NULL | `—` |
| 2 | `chain_id` | bigint(64) | NOT NULL | `0` |
| 3 | `dataset` | text | NOT NULL | `—` |
| 4 | `enabled` | boolean | NOT NULL | `true` |
| 5 | `runtime_status` | text | NOT NULL | `'AVAILABLE'::text` |
| 6 | `supports_range` | boolean | NOT NULL | `false` |
| 7 | `range_verified` | boolean | NOT NULL | `false` |
| 8 | `supports_batch` | boolean | NOT NULL | `false` |
| 9 | `max_batch_size` | integer(32) | NOT NULL | `0` |
| 10 | `semantics` | text | NOT NULL | `'TOTAL_ROWS'::text` |
| 11 | `accuracy` | text | NOT NULL | `'EXACT'::text` |
| 12 | `reason` | text | NOT NULL | `''::text` |
| 13 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`count_provider_capabilities_pkey` `PRIMARY KEY (provider_id, chain_id, dataset)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：13 个（详见上表「可空」列）

**索引（1）**

- `count_provider_capabilities_pkey` — `CREATE UNIQUE INDEX count_provider_capabilities_pkey ON public.count_provider_capabilities USING btree (provider_id, chain_id, dataset)`

<a id="count-provider-registry"></a>

### `count_provider_registry`

| 属性 | 值 |
|---|---|
| 中文用途 | 计数 Provider 注册表：名称、优先级、启停。 |
| 数据所有者领域 | `countoracle` — 依据：`grep -rl -w count_provider_registry backend/internal` → store(1) model(1) countoracle(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0016_count_provider_port.sql:13` |
| 行数（精确 `count(*)`） | **1** |
| 行数（`pg_class.reltuples` 估算） | 1 |
| 大小 | **64.0 KiB**（heap 8.0 KiB / index 16.0 KiB / 共 65,536 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `provider_id` | text | NOT NULL | `—` |
| 2 | `display_name` | text | NOT NULL | `''::text` |
| 3 | `enabled` | boolean | NOT NULL | `true` |
| 4 | `adapter_type` | text | NOT NULL | `''::text` |
| 5 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 6 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`count_provider_registry_pkey` `PRIMARY KEY (provider_id)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：6 个（详见上表「可空」列）

**索引（1）**

- `count_provider_registry_pkey` — `CREATE UNIQUE INDEX count_provider_registry_pkey ON public.count_provider_registry USING btree (provider_id)`

<a id="count-provider-runtime-profiles"></a>

### `count_provider_runtime_profiles`

| 属性 | 值 |
|---|---|
| 中文用途 | 计数 Provider 运行时画像：超时/并发等运行时参数。 |
| 数据所有者领域 | `store` — 依据：仅命中通用包 → store(1) model(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0016_count_provider_port.sql:41` |
| 行数（精确 `count(*)`） | **14** |
| 行数（`pg_class.reltuples` 估算） | 14 |
| 大小 | **160.0 KiB**（heap 104.0 KiB / index 16.0 KiB / 共 163,840 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `provider_id` | text | NOT NULL | `—` |
| 2 | `chain_id` | bigint(64) | NOT NULL | `0` |
| 3 | `dataset` | text | NOT NULL | `'_all'::text` |
| 4 | `state` | text | NOT NULL | `'UNKNOWN'::text` |
| 5 | `active` | integer(32) | NOT NULL | `0` |
| 6 | `queue_depth` | integer(32) | NOT NULL | `0` |
| 7 | `safe_concurrency` | integer(32) | NOT NULL | `0` |
| 8 | `hard_ceiling` | integer(32) | NOT NULL | `0` |
| 9 | `success_rps` | double precision(53) | NOT NULL | `0` |
| 10 | `valid_count_rps` | double precision(53) | NOT NULL | `0` |
| 11 | `p50_ms` | integer(32) | NOT NULL | `0` |
| 12 | `p95_ms` | integer(32) | NOT NULL | `0` |
| 13 | `success_rate` | double precision(53) | NOT NULL | `0` |
| 14 | `rate_limit_rate` | double precision(53) | NOT NULL | `0` |
| 15 | `timeout_rate` | double precision(53) | NOT NULL | `0` |
| 16 | `cost_per_1k_counts` | double precision(53) | NOT NULL | `0` |
| 17 | `requests` | bigint(64) | NOT NULL | `0` |
| 18 | `success_count` | bigint(64) | NOT NULL | `0` |
| 19 | `last_error` | text | NOT NULL | `''::text` |
| 20 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`count_provider_runtime_profiles_pkey` `PRIMARY KEY (provider_id, chain_id, dataset)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：20 个（详见上表「可空」列）

**索引（1）**

- `count_provider_runtime_profiles_pkey` — `CREATE UNIQUE INDEX count_provider_runtime_profiles_pkey ON public.count_provider_runtime_profiles USING btree (provider_id, chain_id, dataset)`

<a id="count-resolution-decisions"></a>

### `count_resolution_decisions`

| 属性 | 值 |
|---|---|
| 中文用途 | 计数解析决策：最终选用的 Provider 与请求/主体哈希。 |
| 数据所有者领域 | `countoracle` — 依据：`grep -rl -w count_resolution_decisions backend/internal` → store(1) countoracle(1) |
| 敏感级别 | **confidential** — 依据：含哈希/密钥类字段：request_hash, subject_hash |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0016_count_provider_port.sql:87` |
| 行数（精确 `count(*)`） | **4** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **64.0 KiB**（heap 8.0 KiB / index 48.0 KiB / 共 65,536 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `—` |
| 2 | `request_hash` | text | NOT NULL | `''::text` |
| 3 | `chain_id` | bigint(64) | NOT NULL | `0` |
| 4 | `dataset` | text | NOT NULL | `''::text` |
| 5 | `subject_hash` | text | NOT NULL | `''::text` |
| 6 | `range_start` | bigint(64) | NOT NULL | `0` |
| 7 | `range_end` | bigint(64) | NOT NULL | `0` |
| 8 | `status` | text | NOT NULL | `''::text` |
| 9 | `reason` | text | NOT NULL | `''::text` |
| 10 | `provider_id` | text | NOT NULL | `''::text` |
| 11 | `count` | bigint(64) | NOT NULL | `'-1'::integer` |
| 12 | `latency_ms` | integer(32) | NOT NULL | `0` |
| 13 | `fallback_recommended` | boolean | NOT NULL | `false` |
| 14 | `old_source` | text | NOT NULL | `''::text` |
| 15 | `new_source` | text | NOT NULL | `''::text` |
| 16 | `old_count` | bigint(64) | NOT NULL | `'-1'::integer` |
| 17 | `new_count` | bigint(64) | NOT NULL | `'-1'::integer` |
| 18 | `old_initial` | integer(32) | NOT NULL | `0` |
| 19 | `new_initial` | integer(32) | NOT NULL | `0` |
| 20 | `decision_diff_reason` | text | NOT NULL | `''::text` |
| 21 | `created_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`count_resolution_decisions_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：21 个（详见上表「可空」列）

**索引（3）**

- `count_resolution_decisions_pkey` — `CREATE UNIQUE INDEX count_resolution_decisions_pkey ON public.count_resolution_decisions USING btree (id)`
- `idx_count_resolution_decisions_hash` — `CREATE INDEX idx_count_resolution_decisions_hash ON public.count_resolution_decisions USING btree (request_hash)`
- `idx_count_resolution_decisions_time` — `CREATE INDEX idx_count_resolution_decisions_time ON public.count_resolution_decisions USING btree (created_at DESC)`

<a id="credentials"></a>

### `credentials`

| 属性 | 值 |
|---|---|
| 中文用途 | 凭证（WebAuthn/TOTP 等）：仅存 secret_hash 与元数据，不存明文。 |
| 数据所有者领域 | `iam` — 依据：`grep -rl -w credentials backend/internal` → iam(3) rawstore(1) api(1) |
| 敏感级别 | **confidential** — 依据：含凭证/密钥类字段：secret_hash |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0040_iam.sql:37` |
| 行数（精确 `count(*)`） | **42** |
| 行数（`pg_class.reltuples` 估算） | 36 |
| 大小 | **96.0 KiB**（heap 16.0 KiB / index 48.0 KiB / 共 98,304 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `identity_id` | uuid | NOT NULL | `—` |
| 3 | `kind` | text | NOT NULL | `—` |
| 4 | `algorithm` | text | NOT NULL | `'argon2id'::text` |
| 5 | `secret_hash` | text | NOT NULL | `—` |
| 6 | `params` | jsonb | NOT NULL | `'{}'::jsonb` |
| 7 | `label` | text | NOT NULL | `''::text` |
| 8 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 9 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |
| 10 | `last_used_at` | timestamp with time zone | 可空 | `—` |
| 11 | `expires_at` | timestamp with time zone | 可空 | `—` |
| 12 | `revoked_at` | timestamp with time zone | 可空 | `—` |

**约束**

- PRIMARY KEY（1）：`credentials_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（1）：`credentials_identity_id_fkey` `FOREIGN KEY (identity_id) REFERENCES identities(id) ON DELETE CASCADE`
- CHECK（1）：`credentials_kind_check` `CHECK ((kind = ANY (ARRAY['password'::text, 'totp'::text, 'recovery_code'::text, 'oidc'::text])))`
- NOT NULL 约束对象：9 个（详见上表「可空」列）

**索引（3）**

- `credentials_identity_idx` — `CREATE INDEX credentials_identity_idx ON public.credentials USING btree (identity_id, kind)`
- `credentials_password_uidx` — `CREATE UNIQUE INDEX credentials_password_uidx ON public.credentials USING btree (identity_id) WHERE ((kind = 'password'::text) AND (revoked_at IS NULL))`
- `credentials_pkey` — `CREATE UNIQUE INDEX credentials_pkey ON public.credentials USING btree (id)`

<a id="data-asset-registry"></a>

### `data_asset_registry`

| 属性 | 值 |
|---|---|
| 中文用途 | 数据资产登记表（治理元数据）：表名、所有者模块、敏感级别、保留期与备注。 |
| 数据所有者领域 | `governance(迁移内登记表)` — 依据：仅命中 `backend/migrations/postgres/0044_tenant_scope.sql`、`0047_case_collaboration.sql` |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0044_tenant_scope.sql:155` |
| 行数（精确 `count(*)`） | **17** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **32.0 KiB**（heap 8.0 KiB / index 16.0 KiB / 共 32,768 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `table_name` | text | NOT NULL | `—` |
| 2 | `owner_module` | text | NOT NULL | `—` |
| 3 | `scope_column` | text | NOT NULL | `'organization_id'::text` |
| 4 | `sensitivity` | text | NOT NULL | `'internal'::text` |
| 5 | `retention` | text | NOT NULL | `''::text` |
| 6 | `notes` | text | NOT NULL | `''::text` |
| 7 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`data_asset_registry_pkey` `PRIMARY KEY (table_name)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（1）：`data_asset_registry_sensitivity_check` `CHECK ((sensitivity = ANY (ARRAY['public'::text, 'internal'::text, 'confidential'::text, 'restricted'::text])))`
- NOT NULL 约束对象：7 个（详见上表「可空」列）

**索引（1）**

- `data_asset_registry_pkey` — `CREATE UNIQUE INDEX data_asset_registry_pkey ON public.data_asset_registry USING btree (table_name)`

<a id="dataset-coverage"></a>

### `dataset_coverage`

| 属性 | 值 |
|---|---|
| 中文用途 | 数据集覆盖度：每个数据集已完成的区间与提交时间（覆盖度收尾依据）。 |
| 数据所有者领域 | `api` — 依据：仅命中通用包 → api(6) store(2) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0001_init.sql:112` |
| 行数（精确 `count(*)`） | **8,124** |
| 行数（`pg_class.reltuples` 估算） | 7,542 |
| 大小 | **6.45 MiB**（heap 2.79 MiB / index 3.62 MiB / 共 6,758,400 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `—` |
| 2 | `chain` | text | NOT NULL | `—` |
| 3 | `subject_type` | text | NOT NULL | `'address'::text` |
| 4 | `subject` | text | NOT NULL | `—` |
| 5 | `dataset` | text | NOT NULL | `—` |
| 6 | `from_block` | bigint(64) | NOT NULL | `—` |
| 7 | `to_block` | bigint(64) | NOT NULL | `—` |
| 8 | `status` | text | NOT NULL | `'COMPLETE'::text` |
| 9 | `provider` | text | NOT NULL | `'ankr'::text` |
| 10 | `row_count` | bigint(64) | NOT NULL | `0` |
| 11 | `raw_bytes` | bigint(64) | NOT NULL | `0` |
| 12 | `head_block` | bigint(64) | NOT NULL | `0` |
| 13 | `committed_at` | timestamp with time zone | NOT NULL | `now()` |
| 14 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 15 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`dataset_coverage_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：15 个（详见上表「可空」列）

**索引（3）**

- `dataset_coverage_pkey` — `CREATE UNIQUE INDEX dataset_coverage_pkey ON public.dataset_coverage USING btree (id)`
- `idx_coverage_blocks` — `CREATE INDEX idx_coverage_blocks ON public.dataset_coverage USING btree (chain, subject_type, subject, dataset, from_block, to_block)`
- `idx_coverage_lookup` — `CREATE INDEX idx_coverage_lookup ON public.dataset_coverage USING btree (chain, subject_type, subject, dataset, status)`

<a id="deletion-requests"></a>

### `deletion_requests`

| 属性 | 值 |
|---|---|
| 中文用途 | 删除请求：双人审批 + 可恢复窗口；实际删除默认关闭。 |
| 数据所有者领域 | `api` — 依据：仅命中通用包 → api(1) |
| 敏感级别 | **confidential** — 依据：含 PII 字段：requested_by_email |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0052_s6_data_governance.sql:70` |
| 行数（精确 `count(*)`） | **0** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **24.0 KiB**（heap 0 B / index 16.0 KiB / 共 24,576 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `resource_type` | text | NOT NULL | `—` |
| 3 | `policy_id` | uuid | 可空 | `—` |
| 4 | `target_scope` | jsonb | NOT NULL | `'{}'::jsonb` |
| 5 | `reason` | text | NOT NULL | `''::text` |
| 6 | `status` | text | NOT NULL | `'previewed'::text` |
| 7 | `dry_run` | boolean | NOT NULL | `true` |
| 8 | `execution_enabled` | boolean | NOT NULL | `false` |
| 9 | `matched_objects` | bigint(64) | NOT NULL | `0` |
| 10 | `matched_bytes` | bigint(64) | NOT NULL | `0` |
| 11 | `preview` | jsonb | NOT NULL | `'{}'::jsonb` |
| 12 | `approval_request_id` | uuid | 可空 | `—` |
| 13 | `requested_by` | uuid | 可空 | `—` |
| 14 | `requested_by_email` | text | NOT NULL | `''::text` |
| 15 | `approved_by` | uuid | 可空 | `—` |
| 16 | `restore_window_until` | timestamp with time zone | 可空 | `—` |
| 17 | `executed_at` | timestamp with time zone | 可空 | `—` |
| 18 | `result` | jsonb | 可空 | `—` |
| 19 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 20 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`deletion_requests_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（4）：`deletion_requests_approval_request_id_fkey` `FOREIGN KEY (approval_request_id) REFERENCES approval_requests(id)`；`deletion_requests_approved_by_fkey` `FOREIGN KEY (approved_by) REFERENCES identities(id)`；`deletion_requests_policy_id_fkey` `FOREIGN KEY (policy_id) REFERENCES retention_policies(id) ON DELETE SET NULL`；`deletion_requests_requested_by_fkey` `FOREIGN KEY (requested_by) REFERENCES identities(id)`
- CHECK（1）：`deletion_requests_status_check` `CHECK ((status = ANY (ARRAY['previewed'::text, 'pending_approval'::text, 'approved'::text, 'rejected'::text, 'executing'::text, 'completed'::text, 'failed'::text, 'cancelled'::text, 'blocked_disabled'::text])))`
- NOT NULL 约束对象：13 个（详见上表「可空」列）

**索引（2）**

- `deletion_requests_pkey` — `CREATE UNIQUE INDEX deletion_requests_pkey ON public.deletion_requests USING btree (id)`
- `deletion_requests_status_idx` — `CREATE INDEX deletion_requests_status_idx ON public.deletion_requests USING btree (status, created_at DESC)`

<a id="download-datasets"></a>

### `download_datasets`

| 属性 | 值 |
|---|---|
| 中文用途 | 下载数据集：数据集名（transactions/logs/token_transfers 等）与区间。 |
| 数据所有者领域 | `download` — 依据：`grep -rl -w download_datasets backend/internal` → store(4) download(3) api(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0001_init.sql:66` |
| 行数（精确 `count(*)`） | **14,996** |
| 行数（`pg_class.reltuples` 估算） | 14,772 |
| 大小 | **5.19 MiB**（heap 3.02 MiB / index 2.13 MiB / 共 5,439,488 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `—` |
| 2 | `job_id` | uuid | NOT NULL | `—` |
| 3 | `subject_id` | uuid | NOT NULL | `—` |
| 4 | `dataset` | text | NOT NULL | `—` |
| 5 | `status` | text | NOT NULL | `'PENDING'::text` |
| 6 | `progress` | real(24) | NOT NULL | `0` |
| 7 | `weight` | real(24) | NOT NULL | `1` |
| 8 | `stats` | jsonb | NOT NULL | `'{}'::jsonb` |
| 9 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 10 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`download_datasets_pkey` `PRIMARY KEY (id)`
- UNIQUE（1）：`download_datasets_subject_id_dataset_key` `UNIQUE (subject_id, dataset)`
- FOREIGN KEY（2）：`download_datasets_job_id_fkey` `FOREIGN KEY (job_id) REFERENCES download_jobs(id) ON DELETE CASCADE`；`download_datasets_subject_id_fkey` `FOREIGN KEY (subject_id) REFERENCES download_subjects(id) ON DELETE CASCADE`
- CHECK（0）：无
- NOT NULL 约束对象：10 个（详见上表「可空」列）

**索引（3）**

- `download_datasets_pkey` — `CREATE UNIQUE INDEX download_datasets_pkey ON public.download_datasets USING btree (id)`
- `download_datasets_subject_id_dataset_key` — `CREATE UNIQUE INDEX download_datasets_subject_id_dataset_key ON public.download_datasets USING btree (subject_id, dataset)`
- `idx_datasets_subject` — `CREATE INDEX idx_datasets_subject ON public.download_datasets USING btree (subject_id)`

<a id="download-jobs"></a>

### `download_jobs`

| 属性 | 值 |
|---|---|
| 中文用途 | 下载任务：目标地址/区间/模式（safe/fast/turbo）与抓取-入库各阶段时间。 |
| 数据所有者领域 | `download` — 依据：`data_asset_registry.owner_module` |
| 敏感级别 | **internal** — 依据：`data_asset_registry.sensitivity` |
| 保留期 | 365 天 — 依据：`data_asset_registry.retention`（写入见 `backend/migrations/postgres/0044_tenant_scope.sql:155-176`、`0047_case_collaboration.sql:267-279`） |
| 建表迁移 | `backend/migrations/postgres/0001_init.sql:28` |
| 行数（精确 `count(*)`） | **2,419** |
| 行数（`pg_class.reltuples` 估算） | 2,411 |
| 大小 | **4.82 MiB**（heap 3.90 MiB / index 904.0 KiB / 共 5,054,464 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `—` |
| 2 | `job_no` | text | NOT NULL | `—` |
| 3 | `status` | text | NOT NULL | `—` |
| 4 | `priority` | integer(32) | NOT NULL | `100` |
| 5 | `source` | text | NOT NULL | `'user_download'::text` |
| 6 | `chain` | text | NOT NULL | `—` |
| 7 | `range_type` | text | NOT NULL | `—` |
| 8 | `from_block` | bigint(64) | 可空 | `—` |
| 9 | `to_block` | bigint(64) | 可空 | `—` |
| 10 | `from_timestamp` | timestamp with time zone | 可空 | `—` |
| 11 | `to_timestamp` | timestamp with time zone | 可空 | `—` |
| 12 | `datasets` | ARRAY | NOT NULL | `—` |
| 13 | `reuse_policy` | text | NOT NULL | `'prefer_local'::text` |
| 14 | `head_block` | bigint(64) | NOT NULL | `0` |
| 15 | `created_by` | uuid | 可空 | `—` |
| 16 | `error` | text | NOT NULL | `''::text` |
| 17 | `stats` | jsonb | NOT NULL | `'{}'::jsonb` |
| 18 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 19 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |
| 20 | `organization_id` | uuid | NOT NULL | `—` |
| 21 | `workspace_id` | uuid | NOT NULL | `—` |
| 22 | `project_id` | uuid | 可空 | `—` |
| 23 | `resolved_at` | timestamp with time zone | 可空 | `—` |
| 24 | `fetch_started_at` | timestamp with time zone | 可空 | `—` |
| 25 | `fetch_completed_at` | timestamp with time zone | 可空 | `—` |
| 26 | `ingest_started_at` | timestamp with time zone | 可空 | `—` |
| 27 | `ingest_completed_at` | timestamp with time zone | 可空 | `—` |
| 28 | `validation_completed_at` | timestamp with time zone | 可空 | `—` |
| 29 | `mode` | text | NOT NULL | `'balanced'::text` |
| 30 | `requested_mode` | text | NOT NULL | `'fast'::text` |
| 31 | `effective_mode` | text | NOT NULL | `'fast'::text` |
| 32 | `mode_reason` | text | NOT NULL | `'default_profile'::text` |
| 33 | `mode_changed_at` | timestamp with time zone | NOT NULL | `now()` |
| 34 | `request_fingerprint` | text | 可空 | `—` |
| 35 | `created_by_identity` | uuid | 可空 | `—` |

**约束**

- PRIMARY KEY（1）：`download_jobs_pkey` `PRIMARY KEY (id)`
- UNIQUE（1）：`download_jobs_job_no_key` `UNIQUE (job_no)`
- FOREIGN KEY（5）：`download_jobs_created_by_fkey` `FOREIGN KEY (created_by) REFERENCES users(id)`；`download_jobs_created_by_identity_fkey` `FOREIGN KEY (created_by_identity) REFERENCES identities(id)`；`download_jobs_organization_id_fkey` `FOREIGN KEY (organization_id) REFERENCES organizations(id)`；`download_jobs_project_id_fkey` `FOREIGN KEY (project_id) REFERENCES projects(id)`；`download_jobs_workspace_id_fkey` `FOREIGN KEY (workspace_id) REFERENCES workspaces(id)`
- CHECK（2）：`download_jobs_effective_mode_check` `CHECK ((effective_mode = ANY (ARRAY['safe'::text, 'fast'::text, 'turbo'::text])))`；`download_jobs_requested_mode_check` `CHECK ((requested_mode = ANY (ARRAY['safe'::text, 'fast'::text, 'turbo'::text])))`
- NOT NULL 约束对象：21 个（详见上表「可空」列）

**索引（5）**

- `download_jobs_identity_scope_idx` — `CREATE INDEX download_jobs_identity_scope_idx ON public.download_jobs USING btree (organization_id, workspace_id, created_by_identity, updated_at DESC)`
- `download_jobs_job_no_key` — `CREATE UNIQUE INDEX download_jobs_job_no_key ON public.download_jobs USING btree (job_no)`
- `download_jobs_pkey` — `CREATE UNIQUE INDEX download_jobs_pkey ON public.download_jobs USING btree (id)`
- `download_jobs_reusable_fingerprint_key` — `CREATE UNIQUE INDEX download_jobs_reusable_fingerprint_key ON public.download_jobs USING btree (request_fingerprint) WHERE ((request_fingerprint IS NOT NULL) AND (status = ANY (ARRAY['CREATED'::text, 'PLANNING'::text, 'QUEUED'::text, 'RUNNING'::text, 'PAUSE_REQUESTED'::text, 'PAUSED'::text, 'RESUMING'::text, 'WAITING_USER_CONFIRMATION'::text])))`
- `download_jobs_scope_time_idx` — `CREATE INDEX download_jobs_scope_time_idx ON public.download_jobs USING btree (organization_id, workspace_id, updated_at DESC)`

<a id="download-pages"></a>

### `download_pages`

| 属性 | 值 |
|---|---|
| 中文用途 | 下载页：单页请求/响应对象键、query_hash、翻页令牌哈希与各阶段时间戳。 |
| 数据所有者领域 | `download` — 依据：`grep -rl -w download_pages backend/internal` → store(3) download(1) |
| 敏感级别 | **confidential** — 依据：含凭证/密钥类字段：query_hash, next_page_token_hash |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0013_ankr_engine.sql:25` |
| 行数（精确 `count(*)`） | **165,502** |
| 行数（`pg_class.reltuples` 估算） | 165,475 |
| 大小 | **121.66 MiB**（heap 82.16 MiB / index 39.45 MiB / 共 127,574,016 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `—` |
| 2 | `job_id` | uuid | NOT NULL | `—` |
| 3 | `shard_id` | uuid | NOT NULL | `—` |
| 4 | `dataset` | text | NOT NULL | `—` |
| 5 | `page_no` | integer(32) | NOT NULL | `—` |
| 6 | `query_hash` | text | NOT NULL | `''::text` |
| 7 | `status` | text | NOT NULL | `'DURABLE'::text` |
| 8 | `page_size` | integer(32) | NOT NULL | `0` |
| 9 | `wire_bytes` | bigint(64) | NOT NULL | `0` |
| 10 | `decoded_bytes` | bigint(64) | NOT NULL | `0` |
| 11 | `raw_path` | text | NOT NULL | `''::text` |
| 12 | `raw_sha256` | text | NOT NULL | `''::text` |
| 13 | `rows_fetched` | integer(32) | NOT NULL | `0` |
| 14 | `rows_parsed` | integer(32) | NOT NULL | `0` |
| 15 | `rows_inserted` | integer(32) | NOT NULL | `0` |
| 16 | `unique_rows` | integer(32) | NOT NULL | `0` |
| 17 | `next_page_token_encrypted` | text | NOT NULL | `''::text` |
| 18 | `next_page_token_hash` | text | NOT NULL | `''::text` |
| 19 | `from_block` | bigint(64) | NOT NULL | `0` |
| 20 | `to_block` | bigint(64) | NOT NULL | `0` |
| 21 | `fetch_started_at` | timestamp with time zone | 可空 | `—` |
| 22 | `body_completed_at` | timestamp with time zone | 可空 | `—` |
| 23 | `durable_at` | timestamp with time zone | 可空 | `—` |
| 24 | `ingested_at` | timestamp with time zone | 可空 | `—` |
| 25 | `validated_at` | timestamp with time zone | 可空 | `—` |
| 26 | `error` | text | NOT NULL | `''::text` |
| 27 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 28 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`download_pages_pkey` `PRIMARY KEY (id)`
- UNIQUE（1）：`download_pages_shard_id_page_no_key` `UNIQUE (shard_id, page_no)`
- FOREIGN KEY（1）：`download_pages_shard_id_fkey` `FOREIGN KEY (shard_id) REFERENCES download_shards(id) ON DELETE CASCADE`
- CHECK（0）：无
- NOT NULL 约束对象：23 个（详见上表「可空」列）

**索引（5）**

- `download_pages_pkey` — `CREATE UNIQUE INDEX download_pages_pkey ON public.download_pages USING btree (id)`
- `download_pages_shard_id_page_no_key` — `CREATE UNIQUE INDEX download_pages_shard_id_page_no_key ON public.download_pages USING btree (shard_id, page_no)`
- `idx_download_pages_job_status` — `CREATE INDEX idx_download_pages_job_status ON public.download_pages USING btree (job_id, status)`
- `idx_pages_shard` — `CREATE INDEX idx_pages_shard ON public.download_pages USING btree (shard_id, page_no)`
- `idx_pages_status` — `CREATE INDEX idx_pages_status ON public.download_pages USING btree (status) WHERE (status = ANY (ARRAY['DURABLE'::text, 'FAILED'::text]))`

<a id="download-planning-decisions"></a>

### `download_planning_decisions`

| 属性 | 值 |
|---|---|
| 中文用途 | 下载规划决策：分片规划依据记录（count oracle 结果等）。 |
| 数据所有者领域 | `store` — 依据：仅命中通用包 → store(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0015_v12_count_oracle.sql:68` |
| 行数（精确 `count(*)`） | **96** |
| 行数（`pg_class.reltuples` 估算） | 67 |
| 大小 | **104.0 KiB**（heap 24.0 KiB / index 48.0 KiB / 共 106,496 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `—` |
| 2 | `job_id` | uuid | NOT NULL | `—` |
| 3 | `subject_id` | uuid | NOT NULL | `—` |
| 4 | `dataset` | text | NOT NULL | `—` |
| 5 | `count_source` | text | NOT NULL | `''::text` |
| 6 | `predicted_rows` | bigint(64) | NOT NULL | `'-1'::integer` |
| 7 | `predicted_pages` | bigint(64) | NOT NULL | `'-1'::integer` |
| 8 | `profile_hit` | boolean | NOT NULL | `false` |
| 9 | `preferred_shards` | integer(32) | NOT NULL | `0` |
| 10 | `initial_shards` | integer(32) | NOT NULL | `0` |
| 11 | `provider_safe_slots` | integer(32) | NOT NULL | `0` |
| 12 | `global_active_slots` | integer(32) | NOT NULL | `0` |
| 13 | `decision_reason` | text | NOT NULL | `''::text` |
| 14 | `created_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`download_planning_decisions_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：14 个（详见上表「可空」列）

**索引（3）**

- `download_planning_decisions_pkey` — `CREATE UNIQUE INDEX download_planning_decisions_pkey ON public.download_planning_decisions USING btree (id)`
- `idx_planning_decisions_job` — `CREATE INDEX idx_planning_decisions_job ON public.download_planning_decisions USING btree (job_id)`
- `idx_planning_decisions_time` — `CREATE INDEX idx_planning_decisions_time ON public.download_planning_decisions USING btree (created_at DESC)`

<a id="download-shards"></a>

### `download_shards`

| 属性 | 值 |
|---|---|
| 中文用途 | 下载分片：Range 分片、租约（lease_until/lease_epoch）、工作量排序与校验时间。 |
| 数据所有者领域 | `download` — 依据：`grep -rl -w download_shards backend/internal` → store(7) download(7) api(3) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0001_init.sql:82` |
| 行数（精确 `count(*)`） | **28,385** |
| 行数（`pg_class.reltuples` 估算） | 28,346 |
| 大小 | **25.93 MiB**（heap 23.11 MiB / index 2.73 MiB / 共 27,189,248 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `—` |
| 2 | `job_id` | uuid | NOT NULL | `—` |
| 3 | `subject_id` | uuid | NOT NULL | `—` |
| 4 | `dataset_id` | uuid | NOT NULL | `—` |
| 5 | `dataset` | text | NOT NULL | `—` |
| 6 | `from_block` | bigint(64) | NOT NULL | `—` |
| 7 | `to_block` | bigint(64) | NOT NULL | `—` |
| 8 | `from_timestamp` | timestamp with time zone | 可空 | `—` |
| 9 | `to_timestamp` | timestamp with time zone | 可空 | `—` |
| 10 | `page_token` | text | NOT NULL | `''::text` |
| 11 | `page_no` | integer(32) | NOT NULL | `0` |
| 12 | `status` | text | NOT NULL | `'PENDING'::text` |
| 13 | `priority` | integer(32) | NOT NULL | `100` |
| 14 | `retries` | integer(32) | NOT NULL | `0` |
| 15 | `max_retries` | integer(32) | NOT NULL | `5` |
| 16 | `lease_owner` | text | NOT NULL | `''::text` |
| 17 | `leased_until` | timestamp with time zone | 可空 | `—` |
| 18 | `downloaded_rows` | bigint(64) | NOT NULL | `0` |
| 19 | `downloaded_bytes` | bigint(64) | NOT NULL | `0` |
| 20 | `raw_object` | text | NOT NULL | `''::text` |
| 21 | `error` | text | NOT NULL | `''::text` |
| 22 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 23 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |
| 24 | `page_size` | integer(32) | NOT NULL | `0` |
| 25 | `page_state` | jsonb | NOT NULL | `'{}'::jsonb` |
| 26 | `fetched_at` | timestamp with time zone | 可空 | `—` |
| 27 | `ingested_at` | timestamp with time zone | 可空 | `—` |
| 28 | `validated_at` | timestamp with time zone | 可空 | `—` |
| 29 | `fetch_duration_ms` | bigint(64) | NOT NULL | `0` |
| 30 | `ingest_duration_ms` | bigint(64) | NOT NULL | `0` |
| 31 | `wire_bytes` | bigint(64) | NOT NULL | `0` |
| 32 | `decoded_bytes` | bigint(64) | NOT NULL | `0` |
| 33 | `page_count` | integer(32) | NOT NULL | `0` |
| 34 | `page_gap_avg_ms` | bigint(64) | NOT NULL | `0` |
| 35 | `lease_epoch` | bigint(64) | NOT NULL | `0` |
| 36 | `work_score` | double precision(53) | NOT NULL | `0` |
| 37 | `tail_boost` | boolean | NOT NULL | `false` |

**约束**

- PRIMARY KEY（1）：`download_shards_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（3）：`download_shards_dataset_id_fkey` `FOREIGN KEY (dataset_id) REFERENCES download_datasets(id) ON DELETE CASCADE`；`download_shards_job_id_fkey` `FOREIGN KEY (job_id) REFERENCES download_jobs(id) ON DELETE CASCADE`；`download_shards_subject_id_fkey` `FOREIGN KEY (subject_id) REFERENCES download_subjects(id) ON DELETE CASCADE`
- CHECK（0）：无
- NOT NULL 约束对象：31 个（详见上表「可空」列）

**索引（4）**

- `download_shards_pkey` — `CREATE UNIQUE INDEX download_shards_pkey ON public.download_shards USING btree (id)`
- `idx_shards_dataset` — `CREATE INDEX idx_shards_dataset ON public.download_shards USING btree (dataset_id)`
- `idx_shards_dispatch` — `CREATE INDEX idx_shards_dispatch ON public.download_shards USING btree (priority DESC, created_at) WHERE (status = ANY (ARRAY['PENDING'::text, 'FAILED_RETRYABLE'::text]))`
- `idx_shards_job` — `CREATE INDEX idx_shards_job ON public.download_shards USING btree (job_id)`

<a id="download-subjects"></a>

### `download_subjects`

| 属性 | 值 |
|---|---|
| 中文用途 | 下载对象：任务下的地址/主体维度。 |
| 数据所有者领域 | `download` — 依据：`grep -rl -w download_subjects backend/internal` → store(4) download(2) api(2) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0001_init.sql:51` |
| 行数（精确 `count(*)`） | **9,245** |
| 行数（`pg_class.reltuples` 估算） | 9,222 |
| 大小 | **3.17 MiB**（heap 1.46 MiB / index 1.67 MiB / 共 3,325,952 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `—` |
| 2 | `job_id` | uuid | NOT NULL | `—` |
| 3 | `subject_type` | text | NOT NULL | `'address'::text` |
| 4 | `subject` | text | NOT NULL | `—` |
| 5 | `status` | text | NOT NULL | `'PENDING'::text` |
| 6 | `progress` | real(24) | NOT NULL | `0` |
| 7 | `stats` | jsonb | NOT NULL | `'{}'::jsonb` |
| 8 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 9 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`download_subjects_pkey` `PRIMARY KEY (id)`
- UNIQUE（1）：`download_subjects_job_id_subject_key` `UNIQUE (job_id, subject)`
- FOREIGN KEY（1）：`download_subjects_job_id_fkey` `FOREIGN KEY (job_id) REFERENCES download_jobs(id) ON DELETE CASCADE`
- CHECK（0）：无
- NOT NULL 约束对象：9 个（详见上表「可空」列）

**索引（3）**

- `download_subjects_job_id_subject_key` — `CREATE UNIQUE INDEX download_subjects_job_id_subject_key ON public.download_subjects USING btree (job_id, subject)`
- `download_subjects_pkey` — `CREATE UNIQUE INDEX download_subjects_pkey ON public.download_subjects USING btree (id)`
- `idx_subjects_job` — `CREATE INDEX idx_subjects_job ON public.download_subjects USING btree (job_id)`

<a id="download-volume-guard-commits"></a>

### `download_volume_guard_commits`

| 属性 | 值 |
|---|---|
| 中文用途 | 体量护栏提交记录：护栏触发/放行的不可变提交。 |
| 数据所有者领域 | `store` — 依据：仅命中通用包 → store(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0027_tron_volume_guard.sql:31` |
| 行数（精确 `count(*)`） | **22,976** |
| 行数（`pg_class.reltuples` 估算） | 15,450 |
| 大小 | **7.32 MiB**（heap 3.20 MiB / index 4.09 MiB / 共 7,675,904 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `shard_id` | uuid | NOT NULL | `—` |
| 2 | `page_no` | integer(32) | NOT NULL | `—` |
| 3 | `dataset_id` | uuid | NOT NULL | `—` |
| 4 | `rows_committed` | bigint(64) | NOT NULL | `—` |
| 5 | `provider_calls` | bigint(64) | NOT NULL | `1` |
| 6 | `estimated_cost_usd` | double precision(53) | NOT NULL | `0` |
| 7 | `min_block` | bigint(64) | NOT NULL | `0` |
| 8 | `max_block` | bigint(64) | NOT NULL | `0` |
| 9 | `full_page` | boolean | NOT NULL | `false` |
| 10 | `committed_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`download_volume_guard_commits_pkey` `PRIMARY KEY (shard_id, page_no)`
- UNIQUE（0）：无
- FOREIGN KEY（2）：`download_volume_guard_commits_dataset_id_fkey` `FOREIGN KEY (dataset_id) REFERENCES download_datasets(id) ON DELETE CASCADE`；`download_volume_guard_commits_shard_id_fkey` `FOREIGN KEY (shard_id) REFERENCES download_shards(id) ON DELETE CASCADE`
- CHECK（0）：无
- NOT NULL 约束对象：10 个（详见上表「可空」列）

**索引（2）**

- `download_volume_guard_commits_pkey` — `CREATE UNIQUE INDEX download_volume_guard_commits_pkey ON public.download_volume_guard_commits USING btree (shard_id, page_no)`
- `idx_volume_guard_commits_dataset` — `CREATE INDEX idx_volume_guard_commits_dataset ON public.download_volume_guard_commits USING btree (dataset_id, committed_at)`

<a id="download-volume-guards"></a>

### `download_volume_guards`

| 属性 | 值 |
|---|---|
| 中文用途 | TRON 地址体量护栏：数据集级地址体量守卫状态与审批。 |
| 数据所有者领域 | `store` — 依据：仅命中通用包 → store(2) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0027_tron_volume_guard.sql:2` |
| 行数（精确 `count(*)`） | **161** |
| 行数（`pg_class.reltuples` 估算） | 148 |
| 大小 | **160.0 KiB**（heap 48.0 KiB / index 72.0 KiB / 共 163,840 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `job_id` | uuid | NOT NULL | `—` |
| 2 | `subject_id` | uuid | NOT NULL | `—` |
| 3 | `dataset_id` | uuid | NOT NULL | `—` |
| 4 | `dataset` | text | NOT NULL | `—` |
| 5 | `state` | text | NOT NULL | `'RUNNING'::text` |
| 6 | `reason` | text | NOT NULL | `''::text` |
| 7 | `risk_level` | text | NOT NULL | `'NORMAL'::text` |
| 8 | `actual_rows` | bigint(64) | NOT NULL | `0` |
| 9 | `projected_rows_low` | bigint(64) | NOT NULL | `0` |
| 10 | `projected_rows_mid` | bigint(64) | NOT NULL | `0` |
| 11 | `projected_rows_high` | bigint(64) | NOT NULL | `0` |
| 12 | `confidence` | double precision(53) | NOT NULL | `0` |
| 13 | `actual_provider_calls` | bigint(64) | NOT NULL | `0` |
| 14 | `projected_provider_calls` | bigint(64) | NOT NULL | `0` |
| 15 | `actual_cost_usd` | double precision(53) | NOT NULL | `0` |
| 16 | `projected_cost_usd` | double precision(53) | NOT NULL | `0` |
| 17 | `elapsed_seconds` | double precision(53) | NOT NULL | `0` |
| 18 | `projected_duration_seconds` | double precision(53) | NOT NULL | `0` |
| 19 | `guard_override` | boolean | NOT NULL | `false` |
| 20 | `guard_triggered_at` | timestamp with time zone | 可空 | `—` |
| 21 | `guard_approved_at` | timestamp with time zone | 可空 | `—` |
| 22 | `guard_approved_by` | text | NOT NULL | `''::text` |
| 23 | `guard_approved_projection` | bigint(64) | NOT NULL | `0` |
| 24 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 25 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`download_volume_guards_pkey` `PRIMARY KEY (dataset_id)`
- UNIQUE（1）：`download_volume_guards_job_id_subject_id_dataset_key` `UNIQUE (job_id, subject_id, dataset)`
- FOREIGN KEY（3）：`download_volume_guards_dataset_id_fkey` `FOREIGN KEY (dataset_id) REFERENCES download_datasets(id) ON DELETE CASCADE`；`download_volume_guards_job_id_fkey` `FOREIGN KEY (job_id) REFERENCES download_jobs(id) ON DELETE CASCADE`；`download_volume_guards_subject_id_fkey` `FOREIGN KEY (subject_id) REFERENCES download_subjects(id) ON DELETE CASCADE`
- CHECK（0）：无
- NOT NULL 约束对象：23 个（详见上表「可空」列）

**索引（3）**

- `download_volume_guards_job_id_subject_id_dataset_key` — `CREATE UNIQUE INDEX download_volume_guards_job_id_subject_id_dataset_key ON public.download_volume_guards USING btree (job_id, subject_id, dataset)`
- `download_volume_guards_pkey` — `CREATE UNIQUE INDEX download_volume_guards_pkey ON public.download_volume_guards USING btree (dataset_id)`
- `idx_volume_guards_job_state` — `CREATE INDEX idx_volume_guards_job_state ON public.download_volume_guards USING btree (job_id, state)`

<a id="entity-relationships"></a>

### `entity_relationships`

| 属性 | 值 |
|---|---|
| 中文用途 | 实体关系：多主体 OSINT 下实体之间的边（关系类型与置信度）。 |
| 数据所有者领域 | `subjectintel` — 依据：`grep -rl -w entity_relationships backend/internal` → subjectintel(1) investigation(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0035_multi_subject_osint.sql:18` |
| 行数（精确 `count(*)`） | **1** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **64.0 KiB**（heap 8.0 KiB / index 48.0 KiB / 共 65,536 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `investigation_id` | uuid | NOT NULL | `—` |
| 3 | `source_subject_id` | uuid | NOT NULL | `—` |
| 4 | `target_subject_id` | uuid | NOT NULL | `—` |
| 5 | `relation_type` | text | NOT NULL | `—` |
| 6 | `source` | text | NOT NULL | `—` |
| 7 | `confidence` | double precision(53) | NOT NULL | `—` |
| 8 | `evidence_refs` | jsonb | NOT NULL | `'[]'::jsonb` |
| 9 | `provenance` | jsonb | NOT NULL | `'{}'::jsonb` |
| 10 | `created_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`entity_relationships_pkey` `PRIMARY KEY (id)`
- UNIQUE（1）：`entity_relationships_investigation_id_source_subject_id_tar_key` `UNIQUE (investigation_id, source_subject_id, target_subject_id, relation_type)`
- FOREIGN KEY（3）：`entity_relationships_investigation_id_fkey` `FOREIGN KEY (investigation_id) REFERENCES investigations(id) ON DELETE CASCADE`；`entity_relationships_source_subject_id_fkey` `FOREIGN KEY (source_subject_id) REFERENCES investigation_subjects(id) ON DELETE CASCADE`；`entity_relationships_target_subject_id_fkey` `FOREIGN KEY (target_subject_id) REFERENCES investigation_subjects(id) ON DELETE CASCADE`
- CHECK（3）：`entity_relationships_check` `CHECK ((source_subject_id <> target_subject_id))`；`entity_relationships_confidence_check` `CHECK (((confidence >= (0)::double precision) AND (confidence <= (1)::double precision)))`；`entity_relationships_source_check` `CHECK ((source = ANY (ARRAY['SYSTEM'::text, 'USER'::text, 'PROVIDER'::text, 'OSINT'::text, 'HEURISTIC'::text, 'AGENT'::text, 'CASE'::text])))`
- NOT NULL 约束对象：10 个（详见上表「可空」列）

**索引（3）**

- `entity_relationships_investigation_id_source_subject_id_tar_key` — `CREATE UNIQUE INDEX entity_relationships_investigation_id_source_subject_id_tar_key ON public.entity_relationships USING btree (investigation_id, source_subject_id, target_subject_id, relation_type)`
- `entity_relationships_pkey` — `CREATE UNIQUE INDEX entity_relationships_pkey ON public.entity_relationships USING btree (id)`
- `idx_entity_relationships_case` — `CREATE INDEX idx_entity_relationships_case ON public.entity_relationships USING btree (investigation_id, created_at)`

<a id="event-signatures"></a>

### `event_signatures`

| 属性 | 值 |
|---|---|
| 中文用途 | 事件签名库：topic0 与事件签名。 |
| 数据所有者领域 | `method` — 依据：`grep -rl -w event_signatures backend/internal` → method(2) store(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0005_method_abi.sql:34` |
| 行数（精确 `count(*)`） | **19** |
| 行数（`pg_class.reltuples` 估算） | 19 |
| 大小 | **64.0 KiB**（heap 8.0 KiB / index 48.0 KiB / 共 65,536 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `topic0` | character varying(66) | NOT NULL | `—` |
| 3 | `event_name` | character varying(255) | NOT NULL | `—` |
| 4 | `signature` | text | NOT NULL | `—` |
| 5 | `source` | character varying(32) | NOT NULL | `—` |
| 6 | `verified` | boolean | NOT NULL | `false` |
| 7 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 8 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`event_signatures_pkey` `PRIMARY KEY (id)`
- UNIQUE（1）：`event_signatures_topic0_signature_key` `UNIQUE (topic0, signature)`
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：8 个（详见上表「可空」列）

**索引（3）**

- `event_signatures_pkey` — `CREATE UNIQUE INDEX event_signatures_pkey ON public.event_signatures USING btree (id)`
- `event_signatures_topic0_signature_key` — `CREATE UNIQUE INDEX event_signatures_topic0_signature_key ON public.event_signatures USING btree (topic0, signature)`
- `idx_event_signatures_topic0` — `CREATE INDEX idx_event_signatures_topic0 ON public.event_signatures USING btree (topic0)`

<a id="export-tickets"></a>

### `export_tickets`

| 属性 | 值 |
|---|---|
| 中文用途 | 导出下载票据：票据令牌、过期与使用状态。 |
| 数据所有者领域 | `store` — 依据：仅命中通用包 → store(1) api(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0004_v12_metadata.sql:119` |
| 行数（精确 `count(*)`） | **182** |
| 行数（`pg_class.reltuples` 估算） | 178 |
| 大小 | **112.0 KiB**（heap 32.0 KiB / index 48.0 KiB / 共 114,688 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `export_id` | uuid | NOT NULL | `—` |
| 3 | `user_id` | uuid | 可空 | `—` |
| 4 | `organization_id` | uuid | NOT NULL | `—` |
| 5 | `workspace_id` | uuid | 可空 | `—` |
| 6 | `expires_at` | timestamp with time zone | NOT NULL | `—` |
| 7 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 8 | `used_at` | timestamp with time zone | 可空 | `—` |
| 9 | `used_by` | uuid | 可空 | `—` |

**约束**

- PRIMARY KEY（1）：`export_tickets_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（1）：`export_tickets_export_id_fkey` `FOREIGN KEY (export_id) REFERENCES exports(id)`
- CHECK（1）：`export_tickets_s7_scope_coherence_check` `CHECK (((workspace_id IS NULL) OR (organization_id IS NOT NULL)))`
- NOT NULL 约束对象：5 个（详见上表「可空」列）

**索引（3）**

- `export_tickets_pkey` — `CREATE UNIQUE INDEX export_tickets_pkey ON public.export_tickets USING btree (id)`
- `idx_export_tickets_active` — `CREATE INDEX idx_export_tickets_active ON public.export_tickets USING btree (export_id) WHERE (used_at IS NULL)`
- `idx_export_tickets_expiry` — `CREATE INDEX idx_export_tickets_expiry ON public.export_tickets USING btree (expires_at)`

<a id="exports"></a>

### `exports`

| 属性 | 值 |
|---|---|
| 中文用途 | 导出作业：导出格式、对象键、SHA-256、过期与数据集归属。 |
| 数据所有者领域 | `export` — 依据：`data_asset_registry.owner_module` |
| 敏感级别 | **confidential** — 依据：`data_asset_registry.sensitivity` |
| 保留期 | 按导出策略 — 依据：`data_asset_registry.retention`（写入见 `backend/migrations/postgres/0044_tenant_scope.sql:155-176`、`0047_case_collaboration.sql:267-279`） |
| 建表迁移 | `backend/migrations/postgres/0001_init.sql:181` |
| 行数（精确 `count(*)`） | **243** |
| 行数（`pg_class.reltuples` 估算） | 228 |
| 大小 | **328.0 KiB**（heap 184.0 KiB / index 104.0 KiB / 共 335,872 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `—` |
| 2 | `job_id` | uuid | 可空 | `—` |
| 3 | `dataset` | text | NOT NULL | `''::text` |
| 4 | `format` | text | NOT NULL | `—` |
| 5 | `status` | text | NOT NULL | `'PENDING'::text` |
| 6 | `object_ref` | text | NOT NULL | `''::text` |
| 7 | `rows` | bigint(64) | NOT NULL | `0` |
| 8 | `error` | text | NOT NULL | `''::text` |
| 9 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 10 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |
| 11 | `organization_id` | uuid | NOT NULL | `—` |
| 12 | `workspace_id` | uuid | NOT NULL | `—` |
| 13 | `created_by` | uuid | 可空 | `—` |
| 14 | `zip` | boolean | NOT NULL | `false` |
| 15 | `query_snapshot` | jsonb | NOT NULL | `'{}'::jsonb` |
| 16 | `sha256` | character varying(64) | NOT NULL | `''::character varying` |
| 17 | `expires_at` | timestamp with time zone | 可空 | `—` |
| 18 | `started_at` | timestamp with time zone | 可空 | `—` |
| 19 | `finished_at` | timestamp with time zone | 可空 | `—` |
| 20 | `format_old` | character varying(16) | 可空 | `—` |

**约束**

- PRIMARY KEY（1）：`exports_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（2）：`exports_organization_id_fkey` `FOREIGN KEY (organization_id) REFERENCES organizations(id)`；`exports_workspace_id_fkey` `FOREIGN KEY (workspace_id) REFERENCES workspaces(id)`
- CHECK（0）：无
- NOT NULL 约束对象：14 个（详见上表「可空」列）

**索引（3）**

- `exports_pkey` — `CREATE UNIQUE INDEX exports_pkey ON public.exports USING btree (id)`
- `exports_scope_time_idx` — `CREATE INDEX exports_scope_time_idx ON public.exports USING btree (organization_id, workspace_id, created_at DESC)`
- `idx_exports_job_org_created` — `CREATE INDEX idx_exports_job_org_created ON public.exports USING btree (job_id, organization_id, created_at DESC)`

<a id="feature-flag-evaluations"></a>

### `feature_flag_evaluations`

| 属性 | 值 |
|---|---|
| 中文用途 | 功能开关评估记录：主体、结果与评估时间。 |
| 数据所有者领域 | `api` — 依据：仅命中通用包 → api(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0051_s6_control_governance.sql:73` |
| 行数（精确 `count(*)`） | **42** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **64.0 KiB**（heap 8.0 KiB / index 48.0 KiB / 共 65,536 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `flag_key` | text | NOT NULL | `—` |
| 3 | `flag_revision` | bigint(64) | NOT NULL | `—` |
| 4 | `environment` | text | NOT NULL | `''::text` |
| 5 | `subject_type` | text | NOT NULL | `'user'::text` |
| 6 | `subject_id` | text | NOT NULL | `''::text` |
| 7 | `organization_id` | uuid | 可空 | `—` |
| 8 | `workspace_id` | uuid | 可空 | `—` |
| 9 | `result` | boolean | NOT NULL | `—` |
| 10 | `reason` | text | NOT NULL | `''::text` |
| 11 | `bucket` | integer(32) | NOT NULL | `0` |
| 12 | `evaluated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`feature_flag_evaluations_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（1）：`feature_flag_evaluations_s7_scope_coherence_check` `CHECK (((workspace_id IS NULL) OR (organization_id IS NOT NULL)))`
- NOT NULL 约束对象：10 个（详见上表「可空」列）

**索引（3）**

- `feature_flag_evaluations_key_idx` — `CREATE INDEX feature_flag_evaluations_key_idx ON public.feature_flag_evaluations USING btree (flag_key, evaluated_at DESC)`
- `feature_flag_evaluations_pkey` — `CREATE UNIQUE INDEX feature_flag_evaluations_pkey ON public.feature_flag_evaluations USING btree (id)`
- `feature_flag_evaluations_subject_idx` — `CREATE INDEX feature_flag_evaluations_subject_idx ON public.feature_flag_evaluations USING btree (subject_id, flag_key, evaluated_at DESC)`

<a id="feature-flags"></a>

### `feature_flags`

| 属性 | 值 |
|---|---|
| 中文用途 | 功能开关：键、默认值与启停。 |
| 数据所有者领域 | `api` — 依据：仅命中通用包 → api(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0051_s6_control_governance.sql:52` |
| 行数（精确 `count(*)`） | **6** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **48.0 KiB**（heap 8.0 KiB / index 32.0 KiB / 共 49,152 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `key` | text | NOT NULL | `—` |
| 2 | `description` | text | NOT NULL | `''::text` |
| 3 | `environment` | text | NOT NULL | `'all'::text` |
| 4 | `enabled` | boolean | NOT NULL | `false` |
| 5 | `default_value` | boolean | NOT NULL | `false` |
| 6 | `percentage` | integer(32) | NOT NULL | `0` |
| 7 | `scope_type` | text | NOT NULL | `'global'::text` |
| 8 | `scope_org_id` | uuid | 可空 | `—` |
| 9 | `scope_workspace_id` | uuid | 可空 | `—` |
| 10 | `rules` | jsonb | NOT NULL | `'[]'::jsonb` |
| 11 | `revision` | bigint(64) | NOT NULL | `1` |
| 12 | `updated_by` | uuid | 可空 | `—` |
| 13 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 14 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |
| 15 | `scope_user_id` | uuid | 可空 | `—` |

**约束**

- PRIMARY KEY（1）：`feature_flags_pkey` `PRIMARY KEY (key)`
- UNIQUE（0）：无
- FOREIGN KEY（4）：`feature_flags_scope_org_id_fkey` `FOREIGN KEY (scope_org_id) REFERENCES organizations(id)`；`feature_flags_scope_user_id_fkey` `FOREIGN KEY (scope_user_id) REFERENCES users(id)`；`feature_flags_scope_workspace_id_fkey` `FOREIGN KEY (scope_workspace_id) REFERENCES workspaces(id)`；`feature_flags_updated_by_fkey` `FOREIGN KEY (updated_by) REFERENCES identities(id)`
- CHECK（4）：`feature_flags_percentage_check` `CHECK (((percentage >= 0) AND (percentage <= 100)))`；`feature_flags_scope_target_check` `CHECK (((scope_type = 'global'::text) OR ((scope_type = 'organization'::text) AND (scope_org_id IS NOT NULL)) OR ((scope_type = 'workspace'::text) AND (scope_org_id IS NOT NULL) AND (scope_workspace_id IS NOT NULL)) OR ((scope_type = 'user'::text) AND (scope_user_id IS NOT NULL))))`；`feature_flags_scope_type_check` `CHECK ((scope_type = ANY (ARRAY['global'::text, 'organization'::text, 'workspace'::text, 'user'::text])))`；`feature_flags_ws_implies_org_check` `CHECK (((scope_workspace_id IS NULL) OR (scope_org_id IS NOT NULL)))`
- NOT NULL 约束对象：11 个（详见上表「可空」列）

**索引（2）**

- `feature_flags_pkey` — `CREATE UNIQUE INDEX feature_flags_pkey ON public.feature_flags USING btree (key)`
- `feature_flags_scope_idx` — `CREATE INDEX feature_flags_scope_idx ON public.feature_flags USING btree (environment, scope_type)`

<a id="finding-evidence-links"></a>

### `finding_evidence_links`

| 属性 | 值 |
|---|---|
| 中文用途 | 发现—证据关联：Finding 与 Evidence 的多对多引用。 |
| 数据所有者领域 | `investigation` — 依据：`grep -rl -w finding_evidence_links backend/internal` → investigation(1) casework(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0028_investigation_phase0.sql:102` |
| 行数（精确 `count(*)`） | **5,486** |
| 行数（`pg_class.reltuples` 估算） | 4,968 |
| 大小 | **848.0 KiB**（heap 368.0 KiB / index 448.0 KiB / 共 868,352 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `finding_id` | uuid | NOT NULL | `—` |
| 2 | `evidence_id` | uuid | NOT NULL | `—` |
| 3 | `created_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`finding_evidence_links_pkey` `PRIMARY KEY (finding_id, evidence_id)`
- UNIQUE（0）：无
- FOREIGN KEY（2）：`finding_evidence_links_evidence_id_fkey` `FOREIGN KEY (evidence_id) REFERENCES investigation_evidence(id) ON DELETE CASCADE`；`finding_evidence_links_finding_id_fkey` `FOREIGN KEY (finding_id) REFERENCES investigation_findings(id) ON DELETE CASCADE`
- CHECK（0）：无
- NOT NULL 约束对象：3 个（详见上表「可空」列）

**索引（1）**

- `finding_evidence_links_pkey` — `CREATE UNIQUE INDEX finding_evidence_links_pkey ON public.finding_evidence_links USING btree (finding_id, evidence_id)`

<a id="gpt-action-api-keys"></a>

### `gpt_action_api_keys`

| 属性 | 值 |
|---|---|
| 中文用途 | GPT Action API 密钥：仅存 key_hash，含过期/轮换/吊销。 |
| 数据所有者领域 | `api` — 依据：仅命中通用包 → api(4) |
| 敏感级别 | **confidential** — 依据：含凭证/密钥类字段：key_hash |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0036_gpt_actions_api_keys.sql:1` |
| 行数（精确 `count(*)`） | **6** |
| 行数（`pg_class.reltuples` 估算） | 4 |
| 大小 | **64.0 KiB**（heap 8.0 KiB / index 48.0 KiB / 共 65,536 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `name` | text | NOT NULL | `—` |
| 3 | `key_prefix` | text | NOT NULL | `—` |
| 4 | `key_hash` | bytea | NOT NULL | `—` |
| 5 | `scopes` | ARRAY | NOT NULL | `ARRAY['read'::text, 'analysis'::text, 'export'::text]` |
| 6 | `rate_limit_per_minute` | integer(32) | NOT NULL | `300` |
| 7 | `allowed_ips` | ARRAY | NOT NULL | `ARRAY[]::text[]` |
| 8 | `expires_at` | timestamp with time zone | 可空 | `—` |
| 9 | `disabled_at` | timestamp with time zone | 可空 | `—` |
| 10 | `last_used_at` | timestamp with time zone | 可空 | `—` |
| 11 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 12 | `created_by` | text | NOT NULL | `'admin@local'::text` |
| 13 | `rotated_from` | uuid | 可空 | `—` |
| 14 | `rotated_at` | timestamp with time zone | 可空 | `—` |
| 15 | `revoked_at` | timestamp with time zone | 可空 | `—` |
| 16 | `revoked_by` | uuid | 可空 | `—` |
| 17 | `revoke_reason` | text | NOT NULL | `''::text` |
| 18 | `created_by_identity` | uuid | 可空 | `—` |
| 19 | `revision` | bigint(64) | NOT NULL | `1` |
| 20 | `description` | text | NOT NULL | `''::text` |

**约束**

- PRIMARY KEY（1）：`gpt_action_api_keys_pkey` `PRIMARY KEY (id)`
- UNIQUE（1）：`gpt_action_api_keys_key_hash_key` `UNIQUE (key_hash)`
- FOREIGN KEY（0）：无
- CHECK（1）：`gpt_action_api_keys_rate_limit_per_minute_check` `CHECK ((rate_limit_per_minute > 0))`
- NOT NULL 约束对象：12 个（详见上表「可空」列）

**索引（3）**

- `gpt_action_api_keys_key_hash_key` — `CREATE UNIQUE INDEX gpt_action_api_keys_key_hash_key ON public.gpt_action_api_keys USING btree (key_hash)`
- `gpt_action_api_keys_pkey` — `CREATE UNIQUE INDEX gpt_action_api_keys_pkey ON public.gpt_action_api_keys USING btree (id)`
- `gpt_action_api_keys_prefix_idx` — `CREATE INDEX gpt_action_api_keys_prefix_idx ON public.gpt_action_api_keys USING btree (key_prefix)`

<a id="gpt-action-request-logs"></a>

### `gpt_action_request_logs`

| 属性 | 值 |
|---|---|
| 中文用途 | GPT Action 请求日志：调用方、remote_ip、耗时与结果。 |
| 数据所有者领域 | `api` — 依据：仅命中通用包 → api(1) |
| 敏感级别 | **confidential** — 依据：含 PII 字段：remote_ip |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0036_gpt_actions_api_keys.sql:16` |
| 行数（精确 `count(*)`） | **445** |
| 行数（`pg_class.reltuples` 估算） | 414 |
| 大小 | **248.0 KiB**（heap 104.0 KiB / index 112.0 KiB / 共 253,952 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | bigint(64) | NOT NULL | `nextval('gpt_action_request_logs_id_seq'::regclass)` |
| 2 | `api_key_id` | uuid | 可空 | `—` |
| 3 | `request_id` | text | NOT NULL | `—` |
| 4 | `method` | text | NOT NULL | `—` |
| 5 | `path` | text | NOT NULL | `—` |
| 6 | `status` | integer(32) | NOT NULL | `—` |
| 7 | `duration_ms` | bigint(64) | NOT NULL | `—` |
| 8 | `remote_ip` | text | NOT NULL | `—` |
| 9 | `created_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`gpt_action_request_logs_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（1）：`gpt_action_request_logs_api_key_id_fkey` `FOREIGN KEY (api_key_id) REFERENCES gpt_action_api_keys(id) ON DELETE SET NULL`
- CHECK（0）：无
- NOT NULL 约束对象：8 个（详见上表「可空」列）

**索引（3）**

- `gpt_action_request_logs_pkey` — `CREATE UNIQUE INDEX gpt_action_request_logs_pkey ON public.gpt_action_request_logs USING btree (id)`
- `idx_gpt_action_request_logs_key_time` — `CREATE INDEX idx_gpt_action_request_logs_key_time ON public.gpt_action_request_logs USING btree (api_key_id, created_at DESC)`
- `idx_gpt_action_request_logs_time` — `CREATE INDEX idx_gpt_action_request_logs_time ON public.gpt_action_request_logs USING btree (created_at DESC)`

<a id="graph-patches"></a>

### `graph_patches`

| 属性 | 值 |
|---|---|
| 中文用途 | 图谱补丁：对画布/图谱的增量修改补丁。 |
| 数据所有者领域 | `investigation` — 依据：`grep -rl -w graph_patches backend/internal` → investigation(2) report(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0028_investigation_phase0.sql:109` |
| 行数（精确 `count(*)`） | **69** |
| 行数（`pg_class.reltuples` 估算） | 51 |
| 大小 | **1.45 MiB**（heap 8.0 KiB / index 32.0 KiB / 共 1,515,520 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `investigation_id` | uuid | NOT NULL | `—` |
| 3 | `canvas_id` | uuid | 可空 | `—` |
| 4 | `base_revision` | bigint(64) | NOT NULL | `—` |
| 5 | `revision` | bigint(64) | NOT NULL | `—` |
| 6 | `patch` | jsonb | NOT NULL | `—` |
| 7 | `contract_version` | character varying(16) | NOT NULL | `'1.0'::character varying` |
| 8 | `created_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`graph_patches_pkey` `PRIMARY KEY (id)`
- UNIQUE（1）：`graph_patches_investigation_id_canvas_id_revision_key` `UNIQUE (investigation_id, canvas_id, revision)`
- FOREIGN KEY（1）：`graph_patches_investigation_id_fkey` `FOREIGN KEY (investigation_id) REFERENCES investigations(id) ON DELETE CASCADE`
- CHECK（0）：无
- NOT NULL 约束对象：7 个（详见上表「可空」列）

**索引（2）**

- `graph_patches_investigation_id_canvas_id_revision_key` — `CREATE UNIQUE INDEX graph_patches_investigation_id_canvas_id_revision_key ON public.graph_patches USING btree (investigation_id, canvas_id, revision)`
- `graph_patches_pkey` — `CREATE UNIQUE INDEX graph_patches_pkey ON public.graph_patches USING btree (id)`

<a id="identities"></a>

### `identities`

| 属性 | 值 |
|---|---|
| 中文用途 | 身份主体：登录账号（email/密码哈希/MFA/锁定）与平台内所有主体引用目标。 |
| 数据所有者领域 | `casework` — 依据：`grep -rl -w identities backend/internal` → api(10) casework(4) iam(2) |
| 敏感级别 | **confidential** — 依据：含凭证/密钥类字段：must_change_password, password_changed_at |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0040_iam.sql:11` |
| 行数（精确 `count(*)`） | **27** |
| 行数（`pg_class.reltuples` 估算） | 27 |
| 大小 | **80.0 KiB**（heap 8.0 KiB / index 64.0 KiB / 共 81,920 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `organization_id` | uuid | NOT NULL | `—` |
| 3 | `email` | text | NOT NULL | `—` |
| 4 | `display_name` | text | NOT NULL | `''::text` |
| 5 | `status` | text | NOT NULL | `'active'::text` |
| 6 | `is_platform_admin` | boolean | NOT NULL | `false` |
| 7 | `mfa_required` | boolean | NOT NULL | `false` |
| 8 | `must_change_password` | boolean | NOT NULL | `false` |
| 9 | `failed_login_count` | integer(32) | NOT NULL | `0` |
| 10 | `locked_until` | timestamp with time zone | 可空 | `—` |
| 11 | `last_login_at` | timestamp with time zone | 可空 | `—` |
| 12 | `last_login_ip` | inet | 可空 | `—` |
| 13 | `password_changed_at` | timestamp with time zone | 可空 | `—` |
| 14 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 15 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |
| 16 | `legacy_user_id` | uuid | 可空 | `—` |

**约束**

- PRIMARY KEY（1）：`identities_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（2）：`identities_legacy_user_id_fkey` `FOREIGN KEY (legacy_user_id) REFERENCES users(id)`；`identities_organization_id_fkey` `FOREIGN KEY (organization_id) REFERENCES organizations(id)`
- CHECK（1）：`identities_status_check` `CHECK ((status = ANY (ARRAY['invited'::text, 'active'::text, 'disabled'::text, 'locked'::text])))`
- NOT NULL 约束对象：11 个（详见上表「可空」列）

**索引（4）**

- `identities_email_lower_uidx` — `CREATE UNIQUE INDEX identities_email_lower_uidx ON public.identities USING btree (lower(email))`
- `identities_legacy_user_uidx` — `CREATE UNIQUE INDEX identities_legacy_user_uidx ON public.identities USING btree (legacy_user_id) WHERE (legacy_user_id IS NOT NULL)`
- `identities_org_status_idx` — `CREATE INDEX identities_org_status_idx ON public.identities USING btree (organization_id, status)`
- `identities_pkey` — `CREATE UNIQUE INDEX identities_pkey ON public.identities USING btree (id)`

<a id="investigation-canvases"></a>

### `investigation_canvases`

| 属性 | 值 |
|---|---|
| 中文用途 | 调查画布：图谱画布的元数据与归属。 |
| 数据所有者领域 | `canvas` — 依据：`data_asset_registry.owner_module` |
| 敏感级别 | **confidential** — 依据：`data_asset_registry.sensitivity` |
| 保留期 | 随调查归档 — 依据：`data_asset_registry.retention`（写入见 `backend/migrations/postgres/0044_tenant_scope.sql:155-176`、`0047_case_collaboration.sql:267-279`） |
| 建表迁移 | `backend/migrations/postgres/0030_canvas_phase3.sql:2` |
| 行数（精确 `count(*)`） | **90** |
| 行数（`pg_class.reltuples` 估算） | 82 |
| 大小 | **1.37 MiB**（heap 88.0 KiB / index 96.0 KiB / 共 1,433,600 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `organization_id` | uuid | NOT NULL | `—` |
| 3 | `workspace_id` | uuid | NOT NULL | `—` |
| 4 | `case_id` | uuid | 可空 | `—` |
| 5 | `investigation_id` | uuid | 可空 | `—` |
| 6 | `parent_canvas_id` | uuid | 可空 | `—` |
| 7 | `name` | text | NOT NULL | `—` |
| 8 | `node_refs` | jsonb | NOT NULL | `'[]'::jsonb` |
| 9 | `edge_refs` | jsonb | NOT NULL | `'[]'::jsonb` |
| 10 | `groups` | jsonb | NOT NULL | `'[]'::jsonb` |
| 11 | `annotations` | jsonb | NOT NULL | `'[]'::jsonb` |
| 12 | `filters` | jsonb | NOT NULL | `'{}'::jsonb` |
| 13 | `layout` | jsonb | NOT NULL | `'{}'::jsonb` |
| 14 | `camera` | jsonb | NOT NULL | `'{}'::jsonb` |
| 15 | `view_state` | jsonb | NOT NULL | `'{}'::jsonb` |
| 16 | `revision` | bigint(64) | NOT NULL | `1` |
| 17 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 18 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`investigation_canvases_pkey` `PRIMARY KEY (id)`
- UNIQUE（1）：`investigation_canvases_case_id_name_key` `UNIQUE (case_id, name)`
- FOREIGN KEY（5）：`investigation_canvases_case_id_fkey` `FOREIGN KEY (case_id) REFERENCES cases(id) ON DELETE CASCADE`；`investigation_canvases_investigation_id_fkey` `FOREIGN KEY (investigation_id) REFERENCES investigations(id) ON DELETE SET NULL`；`investigation_canvases_organization_id_fkey` `FOREIGN KEY (organization_id) REFERENCES organizations(id)`；`investigation_canvases_parent_canvas_id_fkey` `FOREIGN KEY (parent_canvas_id) REFERENCES investigation_canvases(id) ON DELETE SET NULL`；`investigation_canvases_workspace_id_fkey` `FOREIGN KEY (workspace_id) REFERENCES workspaces(id)`
- CHECK（0）：无
- NOT NULL 约束对象：15 个（详见上表「可空」列）

**索引（5）**

- `idx_investigation_canvases_case_updated` — `CREATE INDEX idx_investigation_canvases_case_updated ON public.investigation_canvases USING btree (case_id, updated_at DESC)`
- `idx_investigation_canvases_investigation` — `CREATE INDEX idx_investigation_canvases_investigation ON public.investigation_canvases USING btree (investigation_id, updated_at DESC)`
- `investigation_canvases_case_id_name_key` — `CREATE UNIQUE INDEX investigation_canvases_case_id_name_key ON public.investigation_canvases USING btree (case_id, name)`
- `investigation_canvases_pkey` — `CREATE UNIQUE INDEX investigation_canvases_pkey ON public.investigation_canvases USING btree (id)`
- `investigation_canvases_scope_time_idx` — `CREATE INDEX investigation_canvases_scope_time_idx ON public.investigation_canvases USING btree (organization_id, workspace_id, updated_at DESC)`

<a id="investigation-evidence"></a>

### `investigation_evidence`

| 属性 | 值 |
|---|---|
| 中文用途 | 调查证据：来源引用、content_hash、采集与核验时间。 |
| 数据所有者领域 | `investigation` — 依据：`grep -rl -w investigation_evidence backend/internal` → investigation(2) casework(2) report(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0028_investigation_phase0.sql:89` |
| 行数（精确 `count(*)`） | **7,127** |
| 行数（`pg_class.reltuples` 估算） | 6,712 |
| 大小 | **7.39 MiB**（heap 6.94 MiB / index 424.0 KiB / 共 7,749,632 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `investigation_id` | uuid | NOT NULL | `—` |
| 3 | `evidence_type` | character varying(64) | NOT NULL | `—` |
| 4 | `title` | text | NOT NULL | `—` |
| 5 | `source_refs` | jsonb | NOT NULL | `'[]'::jsonb` |
| 6 | `content_hash` | character varying(128) | NOT NULL | `''::character varying` |
| 7 | `object_ref` | text | NOT NULL | `''::text` |
| 8 | `provenance` | jsonb | NOT NULL | `—` |
| 9 | `contract_version` | character varying(16) | NOT NULL | `'1.0'::character varying` |
| 10 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 11 | `status` | character varying(24) | NOT NULL | `'collected'::character varying` |
| 12 | `version` | integer(32) | NOT NULL | `1` |
| 13 | `revision` | bigint(64) | NOT NULL | `1` |
| 14 | `nature` | character varying(16) | NOT NULL | `'DETERMINISTIC'::character varying` |
| 15 | `supersedes_id` | uuid | 可空 | `—` |
| 16 | `superseded_by` | uuid | 可空 | `—` |
| 17 | `created_by` | uuid | 可空 | `—` |
| 18 | `verified_by` | uuid | 可空 | `—` |
| 19 | `verified_at` | timestamp with time zone | 可空 | `—` |
| 20 | `collected_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`investigation_evidence_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（5）：`investigation_evidence_created_by_fkey` `FOREIGN KEY (created_by) REFERENCES identities(id)`；`investigation_evidence_investigation_id_fkey` `FOREIGN KEY (investigation_id) REFERENCES investigations(id) ON DELETE CASCADE`；`investigation_evidence_superseded_by_fkey` `FOREIGN KEY (superseded_by) REFERENCES investigation_evidence(id)`；`investigation_evidence_supersedes_id_fkey` `FOREIGN KEY (supersedes_id) REFERENCES investigation_evidence(id)`；`investigation_evidence_verified_by_fkey` `FOREIGN KEY (verified_by) REFERENCES identities(id)`
- CHECK（2）：`investigation_evidence_nature_check` `CHECK (((nature)::text = ANY ((ARRAY['CONFIRMED'::character varying, 'INFERRED'::character varying, 'HEURISTIC'::character varying, 'DETERMINISTIC'::character varying])::text[])))`；`investigation_evidence_status_check` `CHECK (((status)::text = ANY ((ARRAY['collected'::character varying, 'verified'::character varying, 'disputed'::character varying, 'superseded'::character varying])::text[])))`
- NOT NULL 约束对象：15 个（详见上表「可空」列）

**索引（3）**

- `idx_evidence_investigation_status` — `CREATE INDEX idx_evidence_investigation_status ON public.investigation_evidence USING btree (investigation_id, status, created_at DESC)`
- `idx_evidence_supersedes` — `CREATE INDEX idx_evidence_supersedes ON public.investigation_evidence USING btree (supersedes_id) WHERE (supersedes_id IS NOT NULL)`
- `investigation_evidence_pkey` — `CREATE UNIQUE INDEX investigation_evidence_pkey ON public.investigation_evidence USING btree (id)`

<a id="investigation-findings"></a>

### `investigation_findings`

| 属性 | 值 |
|---|---|
| 中文用途 | 调查发现（Finding）：结论、置信度、复核状态。 |
| 数据所有者领域 | `casework` — 依据：`grep -rl -w investigation_findings backend/internal` → casework(4) investigation(2) report(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0028_investigation_phase0.sql:72` |
| 行数（精确 `count(*)`） | **344** |
| 行数（`pg_class.reltuples` 估算） | 311 |
| 大小 | **1.21 MiB**（heap 344.0 KiB / index 88.0 KiB / 共 1,269,760 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `investigation_id` | uuid | NOT NULL | `—` |
| 3 | `finding_type` | character varying(64) | NOT NULL | `—` |
| 4 | `title` | text | NOT NULL | `—` |
| 5 | `summary` | text | NOT NULL | `''::text` |
| 6 | `confidence` | double precision(53) | NOT NULL | `0` |
| 7 | `status` | character varying(24) | NOT NULL | `'candidate'::character varying` |
| 8 | `properties` | jsonb | NOT NULL | `'{}'::jsonb` |
| 9 | `algorithm` | character varying(128) | NOT NULL | `''::character varying` |
| 10 | `algorithm_version` | character varying(32) | NOT NULL | `''::character varying` |
| 11 | `contract_version` | character varying(16) | NOT NULL | `'1.0'::character varying` |
| 12 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 13 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |
| 14 | `revision` | bigint(64) | NOT NULL | `1` |
| 15 | `version` | integer(32) | NOT NULL | `1` |
| 16 | `nature` | character varying(16) | NOT NULL | `'DETERMINISTIC'::character varying` |
| 17 | `reviewed_by` | uuid | 可空 | `—` |
| 18 | `reviewed_at` | timestamp with time zone | 可空 | `—` |
| 19 | `review_note` | text | NOT NULL | `''::text` |
| 20 | `supersedes_id` | uuid | 可空 | `—` |
| 21 | `superseded_by` | uuid | 可空 | `—` |
| 22 | `created_by` | uuid | 可空 | `—` |

**约束**

- PRIMARY KEY（1）：`investigation_findings_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（5）：`investigation_findings_created_by_fkey` `FOREIGN KEY (created_by) REFERENCES identities(id)`；`investigation_findings_investigation_id_fkey` `FOREIGN KEY (investigation_id) REFERENCES investigations(id) ON DELETE CASCADE`；`investigation_findings_reviewed_by_fkey` `FOREIGN KEY (reviewed_by) REFERENCES identities(id)`；`investigation_findings_superseded_by_fkey` `FOREIGN KEY (superseded_by) REFERENCES investigation_findings(id)`；`investigation_findings_supersedes_id_fkey` `FOREIGN KEY (supersedes_id) REFERENCES investigation_findings(id)`
- CHECK（2）：`investigation_findings_confidence_check` `CHECK (((confidence >= (0)::double precision) AND (confidence <= (1)::double precision)))`；`investigation_findings_nature_check` `CHECK (((nature)::text = ANY ((ARRAY['CONFIRMED'::character varying, 'INFERRED'::character varying, 'HEURISTIC'::character varying, 'DETERMINISTIC'::character varying])::text[])))`
- NOT NULL 约束对象：17 个（详见上表「可空」列）

**索引（3）**

- `idx_findings_investigation_status` — `CREATE INDEX idx_findings_investigation_status ON public.investigation_findings USING btree (investigation_id, status, created_at DESC)`
- `idx_findings_supersedes` — `CREATE INDEX idx_findings_supersedes ON public.investigation_findings USING btree (supersedes_id) WHERE (supersedes_id IS NOT NULL)`
- `investigation_findings_pkey` — `CREATE UNIQUE INDEX investigation_findings_pkey ON public.investigation_findings USING btree (id)`

<a id="investigation-reports"></a>

### `investigation_reports`

| 属性 | 值 |
|---|---|
| 中文用途 | 调查报告：版本 revision、内容哈希、签名哈希与提交/审批状态。 |
| 数据所有者领域 | `report` — 依据：`data_asset_registry.owner_module` |
| 敏感级别 | **restricted** — 依据：`data_asset_registry.sensitivity` |
| 保留期 | 长期保留（证据） — 依据：`data_asset_registry.retention`（写入见 `backend/migrations/postgres/0044_tenant_scope.sql:155-176`、`0047_case_collaboration.sql:267-279`） |
| 建表迁移 | `backend/migrations/postgres/0034_investigation_reports.sql:1` |
| 行数（精确 `count(*)`） | **68** |
| 行数（`pg_class.reltuples` 估算） | 65 |
| 大小 | **192.0 KiB**（heap 56.0 KiB / index 96.0 KiB / 共 196,608 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `investigation_id` | uuid | NOT NULL | `—` |
| 3 | `revision` | bigint(64) | NOT NULL | `—` |
| 4 | `format` | text | NOT NULL | `'markdown'::text` |
| 5 | `object_key` | text | NOT NULL | `—` |
| 6 | `content_hash` | text | NOT NULL | `—` |
| 7 | `size_bytes` | bigint(64) | NOT NULL | `—` |
| 8 | `metadata` | jsonb | NOT NULL | `'{}'::jsonb` |
| 9 | `generated_at` | timestamp with time zone | NOT NULL | `now()` |
| 10 | `organization_id` | uuid | NOT NULL | `—` |
| 11 | `workspace_id` | uuid | NOT NULL | `—` |
| 12 | `case_id` | uuid | 可空 | `—` |
| 13 | `generated_by` | uuid | 可空 | `—` |
| 14 | `status` | character varying(24) | NOT NULL | `'generated'::character varying` |
| 15 | `status_revision` | bigint(64) | NOT NULL | `1` |
| 16 | `submitted_at` | timestamp with time zone | 可空 | `—` |
| 17 | `submitted_by` | uuid | 可空 | `—` |
| 18 | `approved_at` | timestamp with time zone | 可空 | `—` |
| 19 | `approved_by` | uuid | 可空 | `—` |
| 20 | `rejected_at` | timestamp with time zone | 可空 | `—` |
| 21 | `rejected_by` | uuid | 可空 | `—` |
| 22 | `reject_reason` | text | NOT NULL | `''::text` |
| 23 | `signed_at` | timestamp with time zone | 可空 | `—` |
| 24 | `signed_by` | uuid | 可空 | `—` |
| 25 | `signature_hash` | character varying(64) | NOT NULL | `''::character varying` |
| 26 | `archived_at` | timestamp with time zone | 可空 | `—` |

**约束**

- PRIMARY KEY（1）：`investigation_reports_pkey` `PRIMARY KEY (id)`
- UNIQUE（1）：`investigation_reports_investigation_id_revision_key` `UNIQUE (investigation_id, revision)`
- FOREIGN KEY（7）：`investigation_reports_approved_by_fkey` `FOREIGN KEY (approved_by) REFERENCES identities(id)`；`investigation_reports_investigation_id_fkey` `FOREIGN KEY (investigation_id) REFERENCES investigations(id) ON DELETE CASCADE`；`investigation_reports_organization_id_fkey` `FOREIGN KEY (organization_id) REFERENCES organizations(id)`；`investigation_reports_rejected_by_fkey` `FOREIGN KEY (rejected_by) REFERENCES identities(id)`；`investigation_reports_signed_by_fkey` `FOREIGN KEY (signed_by) REFERENCES identities(id)`；`investigation_reports_submitted_by_fkey` `FOREIGN KEY (submitted_by) REFERENCES identities(id)`；`investigation_reports_workspace_id_fkey` `FOREIGN KEY (workspace_id) REFERENCES workspaces(id)`
- CHECK（1）：`investigation_reports_status_check` `CHECK (((status)::text = ANY ((ARRAY['generated'::character varying, 'in_review'::character varying, 'approved'::character varying, 'rejected'::character varying, 'signed'::character varying, 'archived'::character varying])::text[])))`
- NOT NULL 约束对象：15 个（详见上表「可空」列）

**索引（6）**

- `idx_investigation_reports_latest` — `CREATE INDEX idx_investigation_reports_latest ON public.investigation_reports USING btree (investigation_id, revision DESC)`
- `idx_reports_case_status` — `CREATE INDEX idx_reports_case_status ON public.investigation_reports USING btree (case_id, status, generated_at DESC)`
- `investigation_reports_investigation_id_revision_key` — `CREATE UNIQUE INDEX investigation_reports_investigation_id_revision_key ON public.investigation_reports USING btree (investigation_id, revision)`
- `investigation_reports_investigation_idx` — `CREATE INDEX investigation_reports_investigation_idx ON public.investigation_reports USING btree (investigation_id, revision DESC)`
- `investigation_reports_pkey` — `CREATE UNIQUE INDEX investigation_reports_pkey ON public.investigation_reports USING btree (id)`
- `investigation_reports_scope_time_idx` — `CREATE INDEX investigation_reports_scope_time_idx ON public.investigation_reports USING btree (organization_id, workspace_id, generated_at DESC)`

<a id="investigation-runs"></a>

### `investigation_runs`

| 属性 | 值 |
|---|---|
| 中文用途 | 调查运行实例：一次执行的状态、目标、计划与时间。 |
| 数据所有者领域 | `investigation` — 依据：`grep -rl -w investigation_runs backend/internal` → investigation(2) casework(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0029_investigation_runtime.sql:2` |
| 行数（精确 `count(*)`） | **79** |
| 行数（`pg_class.reltuples` 估算） | 66 |
| 大小 | **120.0 KiB**（heap 48.0 KiB / index 32.0 KiB / 共 122,880 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `investigation_id` | uuid | NOT NULL | `—` |
| 3 | `status` | text | NOT NULL | `'pending'::text` |
| 4 | `objective` | text | NOT NULL | `—` |
| 5 | `plan` | jsonb | NOT NULL | `'[]'::jsonb` |
| 6 | `frontier` | jsonb | NOT NULL | `'[]'::jsonb` |
| 7 | `visited_subjects` | jsonb | NOT NULL | `'[]'::jsonb` |
| 8 | `pending_data_jobs` | jsonb | NOT NULL | `'[]'::jsonb` |
| 9 | `pending_analysis_jobs` | jsonb | NOT NULL | `'[]'::jsonb` |
| 10 | `remaining_budget` | jsonb | NOT NULL | `'{}'::jsonb` |
| 11 | `last_event_id` | text | NOT NULL | `''::text` |
| 12 | `checkpoint` | jsonb | NOT NULL | `'{}'::jsonb` |
| 13 | `attempt` | integer(32) | NOT NULL | `1` |
| 14 | `started_at` | timestamp with time zone | 可空 | `—` |
| 15 | `finished_at` | timestamp with time zone | 可空 | `—` |
| 16 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 17 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`investigation_runs_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（1）：`investigation_runs_investigation_id_fkey` `FOREIGN KEY (investigation_id) REFERENCES investigations(id) ON DELETE CASCADE`
- CHECK（0）：无
- NOT NULL 约束对象：15 个（详见上表「可空」列）

**索引（2）**

- `idx_investigation_runs_active` — `CREATE INDEX idx_investigation_runs_active ON public.investigation_runs USING btree (status, updated_at) WHERE (status = ANY (ARRAY['pending'::text, 'running'::text, 'waiting'::text, 'paused'::text]))`
- `investigation_runs_pkey` — `CREATE UNIQUE INDEX investigation_runs_pkey ON public.investigation_runs USING btree (id)`

<a id="investigation-stages"></a>

### `investigation_stages`

| 属性 | 值 |
|---|---|
| 中文用途 | 调查阶段：运行内的阶段划分与时间。 |
| 数据所有者领域 | `investigation` — 依据：`grep -rl -w investigation_stages backend/internal` → investigation(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0029_investigation_runtime.sql:10` |
| 行数（精确 `count(*)`） | **347** |
| 行数（`pg_class.reltuples` 估算） | 327 |
| 大小 | **152.0 KiB**（heap 40.0 KiB / index 80.0 KiB / 共 155,648 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `run_id` | uuid | NOT NULL | `—` |
| 3 | `name` | text | NOT NULL | `—` |
| 4 | `ordinal` | integer(32) | NOT NULL | `—` |
| 5 | `status` | text | NOT NULL | `'pending'::text` |
| 6 | `started_at` | timestamp with time zone | 可空 | `—` |
| 7 | `finished_at` | timestamp with time zone | 可空 | `—` |

**约束**

- PRIMARY KEY（1）：`investigation_stages_pkey` `PRIMARY KEY (id)`
- UNIQUE（1）：`investigation_stages_run_id_ordinal_key` `UNIQUE (run_id, ordinal)`
- FOREIGN KEY（1）：`investigation_stages_run_id_fkey` `FOREIGN KEY (run_id) REFERENCES investigation_runs(id) ON DELETE CASCADE`
- CHECK（0）：无
- NOT NULL 约束对象：5 个（详见上表「可空」列）

**索引（2）**

- `investigation_stages_pkey` — `CREATE UNIQUE INDEX investigation_stages_pkey ON public.investigation_stages USING btree (id)`
- `investigation_stages_run_id_ordinal_key` — `CREATE UNIQUE INDEX investigation_stages_run_id_ordinal_key ON public.investigation_stages USING btree (run_id, ordinal)`

<a id="investigation-steps"></a>

### `investigation_steps`

| 属性 | 值 |
|---|---|
| 中文用途 | 调查步骤：单步子任务、输入输出哈希、耗时与数据集。 |
| 数据所有者领域 | `investigation` — 依据：`grep -rl -w investigation_steps backend/internal` → investigation(2) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0029_investigation_runtime.sql:14` |
| 行数（精确 `count(*)`） | **347** |
| 行数（`pg_class.reltuples` 估算） | 292 |
| 大小 | **6.09 MiB**（heap 184.0 KiB / index 80.0 KiB / 共 6,381,568 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `run_id` | uuid | NOT NULL | `—` |
| 3 | `stage_id` | uuid | NOT NULL | `—` |
| 4 | `ordinal` | integer(32) | NOT NULL | `—` |
| 5 | `action` | text | NOT NULL | `—` |
| 6 | `capability` | text | NOT NULL | `—` |
| 7 | `capability_version` | text | NOT NULL | `—` |
| 8 | `status` | text | NOT NULL | `'pending'::text` |
| 9 | `input` | jsonb | NOT NULL | `'{}'::jsonb` |
| 10 | `output` | jsonb | NOT NULL | `'{}'::jsonb` |
| 11 | `output_hash` | text | NOT NULL | `''::text` |
| 12 | `data_version` | text | NOT NULL | `''::text` |
| 13 | `algorithm_version` | text | NOT NULL | `''::text` |
| 14 | `provider` | text | NOT NULL | `''::text` |
| 15 | `dataset` | text | NOT NULL | `''::text` |
| 16 | `external_job_id` | uuid | 可空 | `—` |
| 17 | `error` | text | NOT NULL | `''::text` |
| 18 | `started_at` | timestamp with time zone | 可空 | `—` |
| 19 | `finished_at` | timestamp with time zone | 可空 | `—` |
| 20 | `created_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`investigation_steps_pkey` `PRIMARY KEY (id)`
- UNIQUE（1）：`investigation_steps_run_id_ordinal_key` `UNIQUE (run_id, ordinal)`
- FOREIGN KEY（2）：`investigation_steps_run_id_fkey` `FOREIGN KEY (run_id) REFERENCES investigation_runs(id) ON DELETE CASCADE`；`investigation_steps_stage_id_fkey` `FOREIGN KEY (stage_id) REFERENCES investigation_stages(id) ON DELETE CASCADE`
- CHECK（0）：无
- NOT NULL 约束对象：17 个（详见上表「可空」列）

**索引（2）**

- `investigation_steps_pkey` — `CREATE UNIQUE INDEX investigation_steps_pkey ON public.investigation_steps USING btree (id)`
- `investigation_steps_run_id_ordinal_key` — `CREATE UNIQUE INDEX investigation_steps_run_id_ordinal_key ON public.investigation_steps USING btree (run_id, ordinal)`

<a id="investigation-subjects"></a>

### `investigation_subjects`

| 属性 | 值 |
|---|---|
| 中文用途 | 调查对象（多主体）：一次调查可含多个被调查实体。 |
| 数据所有者领域 | `subjectintel` — 依据：`grep -rl -w investigation_subjects backend/internal` → subjectintel(1) investigation(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0035_multi_subject_osint.sql:1` |
| 行数（精确 `count(*)`） | **12** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **64.0 KiB**（heap 8.0 KiB / index 48.0 KiB / 共 65,536 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `investigation_id` | uuid | NOT NULL | `—` |
| 3 | `subject_type` | text | NOT NULL | `—` |
| 4 | `subject_value` | text | NOT NULL | `—` |
| 5 | `label` | text | NOT NULL | `''::text` |
| 6 | `source` | text | NOT NULL | `—` |
| 7 | `confidence` | double precision(53) | NOT NULL | `—` |
| 8 | `evidence_refs` | jsonb | NOT NULL | `'[]'::jsonb` |
| 9 | `provenance` | jsonb | NOT NULL | `'{}'::jsonb` |
| 10 | `created_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`investigation_subjects_pkey` `PRIMARY KEY (id)`
- UNIQUE（1）：`investigation_subjects_investigation_id_subject_type_subjec_key` `UNIQUE (investigation_id, subject_type, subject_value)`
- FOREIGN KEY（1）：`investigation_subjects_investigation_id_fkey` `FOREIGN KEY (investigation_id) REFERENCES investigations(id) ON DELETE CASCADE`
- CHECK（3）：`investigation_subjects_confidence_check` `CHECK (((confidence >= (0)::double precision) AND (confidence <= (1)::double precision)))`；`investigation_subjects_source_check` `CHECK ((source = ANY (ARRAY['SYSTEM'::text, 'USER'::text, 'PROVIDER'::text, 'OSINT'::text, 'HEURISTIC'::text, 'AGENT'::text, 'CASE'::text])))`；`investigation_subjects_subject_type_check` `CHECK ((subject_type = ANY (ARRAY['ADDRESS'::text, 'DOMAIN'::text, 'IP'::text, 'SOCIAL_ACCOUNT'::text, 'APK'::text, 'ORGANIZATION'::text, 'FILE'::text, 'CONTRACT'::text, 'TRANSACTION'::text])))`
- NOT NULL 约束对象：10 个（详见上表「可空」列）

**索引（3）**

- `idx_investigation_subjects_case` — `CREATE INDEX idx_investigation_subjects_case ON public.investigation_subjects USING btree (investigation_id, created_at)`
- `investigation_subjects_investigation_id_subject_type_subjec_key` — `CREATE UNIQUE INDEX investigation_subjects_investigation_id_subject_type_subjec_key ON public.investigation_subjects USING btree (investigation_id, subject_type, subject_value)`
- `investigation_subjects_pkey` — `CREATE UNIQUE INDEX investigation_subjects_pkey ON public.investigation_subjects USING btree (id)`

<a id="investigations"></a>

### `investigations`

| 属性 | 值 |
|---|---|
| 中文用途 | 调查主表：调查目标（subject JSONB）、预算、状态，挂案件与工作区。 |
| 数据所有者领域 | `investigation` — 依据：`data_asset_registry.owner_module` |
| 敏感级别 | **confidential** — 依据：`data_asset_registry.sensitivity` |
| 保留期 | 随案件归档 — 依据：`data_asset_registry.retention`（写入见 `backend/migrations/postgres/0044_tenant_scope.sql:155-176`、`0047_case_collaboration.sql:267-279`） |
| 建表迁移 | `backend/migrations/postgres/0028_investigation_phase0.sql:3` |
| 行数（精确 `count(*)`） | **129** |
| 行数（`pg_class.reltuples` 估算） | 128 |
| 大小 | **240.0 KiB**（heap 96.0 KiB / index 104.0 KiB / 共 245,760 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `organization_id` | uuid | NOT NULL | `—` |
| 3 | `workspace_id` | uuid | NOT NULL | `—` |
| 4 | `case_id` | uuid | 可空 | `—` |
| 5 | `subject` | jsonb | NOT NULL | `—` |
| 6 | `goal` | text | NOT NULL | `—` |
| 7 | `mode` | character varying(16) | NOT NULL | `'assisted'::character varying` |
| 8 | `status` | character varying(32) | NOT NULL | `'created'::character varying` |
| 9 | `budget` | jsonb | NOT NULL | `'{}'::jsonb` |
| 10 | `stop_conditions` | jsonb | NOT NULL | `'{}'::jsonb` |
| 11 | `state` | jsonb | NOT NULL | `'{}'::jsonb` |
| 12 | `contract_version` | character varying(16) | NOT NULL | `'1.0'::character varying` |
| 13 | `revision` | bigint(64) | NOT NULL | `1` |
| 14 | `idempotency_key` | text | 可空 | `—` |
| 15 | `created_by` | uuid | 可空 | `—` |
| 16 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 17 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |
| 18 | `active_run_id` | uuid | 可空 | `—` |
| 19 | `data_requirement` | jsonb | NOT NULL | `'{}'::jsonb` |

**约束**

- PRIMARY KEY（1）：`investigations_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（4）：`investigations_case_id_fkey` `FOREIGN KEY (case_id) REFERENCES cases(id)`；`investigations_created_by_fkey` `FOREIGN KEY (created_by) REFERENCES users(id)`；`investigations_organization_id_fkey` `FOREIGN KEY (organization_id) REFERENCES organizations(id)`；`investigations_workspace_id_fkey` `FOREIGN KEY (workspace_id) REFERENCES workspaces(id)`
- CHECK（2）：`investigations_mode_check` `CHECK (((mode)::text = ANY ((ARRAY['manual'::character varying, 'assisted'::character varying, 'auto'::character varying])::text[])))`；`investigations_status_check` `CHECK (((status)::text = ANY ((ARRAY['created'::character varying, 'running'::character varying, 'waiting_data'::character varying, 'waiting_review'::character varying, 'paused'::character varying, 'completed'::character varying, 'failed'::character varying, 'cancelled'::character varying])::text[])))`
- NOT NULL 约束对象：15 个（详见上表「可空」列）

**索引（5）**

- `idx_investigations_case_status` — `CREATE INDEX idx_investigations_case_status ON public.investigations USING btree (case_id, status)`
- `idx_investigations_scope` — `CREATE INDEX idx_investigations_scope ON public.investigations USING btree (organization_id, workspace_id, updated_at DESC)`
- `investigations_pkey` — `CREATE UNIQUE INDEX investigations_pkey ON public.investigations USING btree (id)`
- `investigations_scope_time_idx` — `CREATE INDEX investigations_scope_time_idx ON public.investigations USING btree (organization_id, workspace_id, updated_at DESC)`
- `uq_investigations_idempotency` — `CREATE UNIQUE INDEX uq_investigations_idempotency ON public.investigations USING btree (organization_id, idempotency_key) WHERE (idempotency_key IS NOT NULL)`

<a id="job-event-log"></a>

### `job_event_log`

| 属性 | 值 |
|---|---|
| 中文用途 | 任务事件重放日志：SSE Last-Event-ID 重放（§21.5），数据本体在 MinIO/ClickHouse。 |
| 数据所有者领域 | `api` — 依据：仅命中通用包 → api(2) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0049_s5_explorer_download_jobs.sql:46` |
| 行数（精确 `count(*)`） | **42,007** |
| 行数（`pg_class.reltuples` 估算） | 42,064 |
| 大小 | **62.33 MiB**（heap 58.56 MiB / index 3.72 MiB / 共 65,355,776 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | bigint(64) | NOT NULL | `nextval('job_event_log_id_seq'::regclass)` |
| 2 | `job_id` | uuid | NOT NULL | `—` |
| 3 | `event_type` | character varying(64) | NOT NULL | `'update'::character varying` |
| 4 | `payload` | jsonb | NOT NULL | `'{}'::jsonb` |
| 5 | `created_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`job_event_log_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（1）：`job_event_log_job_id_fkey` `FOREIGN KEY (job_id) REFERENCES download_jobs(id) ON DELETE CASCADE`
- CHECK（0）：无
- NOT NULL 约束对象：5 个（详见上表「可空」列）

**索引（2）**

- `job_event_log_job_id_id_idx` — `CREATE INDEX job_event_log_job_id_id_idx ON public.job_event_log USING btree (job_id, id)`
- `job_event_log_pkey` — `CREATE UNIQUE INDEX job_event_log_pkey ON public.job_event_log USING btree (id)`

<a id="kill-switch-engagements"></a>

### `kill_switch_engagements`

| 属性 | 值 |
|---|---|
| 中文用途 | Kill Switch 启用记录：启用/恢复、升级认证与复核期限。 |
| 数据所有者领域 | `api` — 依据：仅命中通用包 → api(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0051_s6_control_governance.sql:95` |
| 行数（精确 `count(*)`） | **27** |
| 行数（`pg_class.reltuples` 估算） | 23 |
| 大小 | **144.0 KiB**（heap 72.0 KiB / index 32.0 KiB / 共 147,456 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `mode` | text | NOT NULL | `—` |
| 3 | `status` | text | NOT NULL | `'pending_approval'::text` |
| 4 | `reason` | text | NOT NULL | `—` |
| 5 | `scope` | jsonb | NOT NULL | `'{}'::jsonb` |
| 6 | `impact_preview` | jsonb | NOT NULL | `'{}'::jsonb` |
| 7 | `approval_request_id` | uuid | 可空 | `—` |
| 8 | `step_up_at` | timestamp with time zone | 可空 | `—` |
| 9 | `step_up_method` | text | NOT NULL | `'password'::text` |
| 10 | `requested_by` | uuid | 可空 | `—` |
| 11 | `approved_by` | uuid | 可空 | `—` |
| 12 | `engaged_at` | timestamp with time zone | 可空 | `—` |
| 13 | `restored_at` | timestamp with time zone | 可空 | `—` |
| 14 | `health_check` | jsonb | 可空 | `—` |
| 15 | `break_glass` | boolean | NOT NULL | `false` |
| 16 | `review_required_by` | timestamp with time zone | 可空 | `—` |
| 17 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 18 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`kill_switch_engagements_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（3）：`kill_switch_engagements_approval_request_id_fkey` `FOREIGN KEY (approval_request_id) REFERENCES approval_requests(id)`；`kill_switch_engagements_approved_by_fkey` `FOREIGN KEY (approved_by) REFERENCES identities(id)`；`kill_switch_engagements_requested_by_fkey` `FOREIGN KEY (requested_by) REFERENCES identities(id)`
- CHECK（2）：`kill_switch_engagements_mode_check` `CHECK ((mode = ANY (ARRAY['engage'::text, 'restore'::text])))`；`kill_switch_engagements_status_check` `CHECK ((status = ANY (ARRAY['pending_approval'::text, 'approved'::text, 'rejected'::text, 'executing'::text, 'active'::text, 'restored'::text, 'failed'::text, 'cancelled'::text])))`
- NOT NULL 约束对象：10 个（详见上表「可空」列）

**索引（2）**

- `kill_switch_engagements_pkey` — `CREATE UNIQUE INDEX kill_switch_engagements_pkey ON public.kill_switch_engagements USING btree (id)`
- `kill_switch_engagements_status_idx` — `CREATE INDEX kill_switch_engagements_status_idx ON public.kill_switch_engagements USING btree (status, created_at DESC)`

<a id="login-events"></a>

### `login_events`

| 属性 | 值 |
|---|---|
| 中文用途 | 登录/认证事件（append-only）：成功/失败、MFA 方法与结果、哈希链字段。 |
| 数据所有者领域 | `audit` — 依据：`data_asset_registry.owner_module` |
| 敏感级别 | **restricted** — 依据：`data_asset_registry.sensitivity` |
| 保留期 | 在线 365 天 / 归档 3 年 — 依据：`data_asset_registry.retention`（写入见 `backend/migrations/postgres/0044_tenant_scope.sql:155-176`、`0047_case_collaboration.sql:267-279`） |
| 建表迁移 | `backend/migrations/postgres/0040_iam.sql:102` |
| 行数（精确 `count(*)`） | **3,275** |
| 行数（`pg_class.reltuples` 估算） | 2,996 |
| 大小 | **3.83 MiB**（heap 2.21 MiB / index 1.58 MiB / 共 4,014,080 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `seq` | bigint(64) | NOT NULL | `—` |
| 3 | `chain` | text | NOT NULL | `'login'::text` |
| 4 | `occurred_at` | timestamp with time zone | NOT NULL | `now()` |
| 5 | `event_version` | integer(32) | NOT NULL | `1` |
| 6 | `environment` | text | NOT NULL | `''::text` |
| 7 | `event_type` | text | NOT NULL | `—` |
| 8 | `result` | text | NOT NULL | `—` |
| 9 | `reason_code` | text | NOT NULL | `''::text` |
| 10 | `risk_level` | text | NOT NULL | `'low'::text` |
| 11 | `risk_action` | text | NOT NULL | `''::text` |
| 12 | `identity_id` | uuid | 可空 | `—` |
| 13 | `subject_digest` | text | NOT NULL | `''::text` |
| 14 | `subject_masked` | text | NOT NULL | `''::text` |
| 15 | `organization_id` | uuid | 可空 | `—` |
| 16 | `workspace_id` | uuid | 可空 | `—` |
| 17 | `audience` | text | NOT NULL | `'user'::text` |
| 18 | `auth_method` | text | NOT NULL | `'password'::text` |
| 19 | `identity_provider` | text | NOT NULL | `'local'::text` |
| 20 | `mfa_method` | text | NOT NULL | `''::text` |
| 21 | `mfa_result` | text | NOT NULL | `''::text` |
| 22 | `session_id` | uuid | 可空 | `—` |
| 23 | `device_id` | uuid | 可空 | `—` |
| 24 | `is_new_device` | boolean | NOT NULL | `false` |
| 25 | `device_trusted` | boolean | NOT NULL | `false` |
| 26 | `device_label` | text | NOT NULL | `''::text` |
| 27 | `device_type` | text | NOT NULL | `''::text` |
| 28 | `device_vendor` | text | NOT NULL | `''::text` |
| 29 | `device_model` | text | NOT NULL | `''::text` |
| 30 | `os_name` | text | NOT NULL | `''::text` |
| 31 | `os_version` | text | NOT NULL | `''::text` |
| 32 | `browser_name` | text | NOT NULL | `''::text` |
| 33 | `browser_version` | text | NOT NULL | `''::text` |
| 34 | `user_agent` | text | NOT NULL | `''::text` |
| 35 | `client_hints` | jsonb | NOT NULL | `'{}'::jsonb` |
| 36 | `peer_ip` | inet | 可空 | `—` |
| 37 | `client_ip` | inet | 可空 | `—` |
| 38 | `proxy_chain` | ARRAY | NOT NULL | `'{}'::text[]` |
| 39 | `entry_host` | text | NOT NULL | `''::text` |
| 40 | `scheme` | text | NOT NULL | `''::text` |
| 41 | `request_id` | text | NOT NULL | `''::text` |
| 42 | `trace_id` | text | NOT NULL | `''::text` |
| 43 | `duration_ms` | integer(32) | NOT NULL | `0` |
| 44 | `rate_limited` | boolean | NOT NULL | `false` |
| 45 | `alert_id` | uuid | 可空 | `—` |
| 46 | `ip_intel` | jsonb | NOT NULL | `'{}'::jsonb` |
| 47 | `detail` | jsonb | NOT NULL | `'{}'::jsonb` |
| 48 | `prev_hash` | text | NOT NULL | `—` |
| 49 | `event_hash` | text | NOT NULL | `—` |

**约束**

- PRIMARY KEY（1）：`login_events_pkey` `PRIMARY KEY (id)`
- UNIQUE（1）：`login_events_chain_seq_key` `UNIQUE (chain, seq)`
- FOREIGN KEY（1）：`login_events_identity_id_fkey` `FOREIGN KEY (identity_id) REFERENCES identities(id)`
- CHECK（2）：`login_events_result_check` `CHECK ((result = ANY (ARRAY['success'::text, 'failure'::text, 'denied'::text, 'blocked'::text])))`；`login_events_s7_scope_coherence_check` `CHECK (((workspace_id IS NULL) OR (organization_id IS NOT NULL)))`
- NOT NULL 约束对象：41 个（详见上表「可空」列）

**索引（10）**

- `idx_login_events_org_seq` — `CREATE INDEX idx_login_events_org_seq ON public.login_events USING btree (organization_id, seq DESC)`
- `idx_login_events_seq_desc` — `CREATE INDEX idx_login_events_seq_desc ON public.login_events USING btree (seq DESC)`
- `idx_login_events_type_seq` — `CREATE INDEX idx_login_events_type_seq ON public.login_events USING btree (event_type, seq DESC)`
- `login_events_chain_seq_key` — `CREATE UNIQUE INDEX login_events_chain_seq_key ON public.login_events USING btree (chain, seq)`
- `login_events_identity_idx` — `CREATE INDEX login_events_identity_idx ON public.login_events USING btree (identity_id, occurred_at DESC)`
- `login_events_ip_idx` — `CREATE INDEX login_events_ip_idx ON public.login_events USING btree (client_ip, occurred_at DESC)`
- `login_events_pkey` — `CREATE UNIQUE INDEX login_events_pkey ON public.login_events USING btree (id)`
- `login_events_result_idx` — `CREATE INDEX login_events_result_idx ON public.login_events USING btree (result, occurred_at DESC)`
- `login_events_session_idx` — `CREATE INDEX login_events_session_idx ON public.login_events USING btree (session_id)`
- `login_events_time_idx` — `CREATE INDEX login_events_time_idx ON public.login_events USING btree (occurred_at DESC)`

<a id="member-role-bindings"></a>

### `member_role_bindings`

| 属性 | 值 |
|---|---|
| 中文用途 | 成员角色绑定：主体在组织/工作区内的角色授予与撤销。 |
| 数据所有者领域 | `policy` — 依据：`grep -rl -w member_role_bindings backend/internal` → api(2) policy(1) iam(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0041_rbac.sql:61` |
| 行数（精确 `count(*)`） | **78** |
| 行数（`pg_class.reltuples` 估算） | 57 |
| 大小 | **96.0 KiB**（heap 16.0 KiB / index 48.0 KiB / 共 98,304 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `identity_id` | uuid | NOT NULL | `—` |
| 3 | `role_id` | uuid | NOT NULL | `—` |
| 4 | `scope_type` | text | NOT NULL | `—` |
| 5 | `scope_id` | uuid | 可空 | `—` |
| 6 | `granted_by` | uuid | 可空 | `—` |
| 7 | `granted_at` | timestamp with time zone | NOT NULL | `now()` |
| 8 | `expires_at` | timestamp with time zone | 可空 | `—` |
| 9 | `revoked_at` | timestamp with time zone | 可空 | `—` |
| 10 | `reason` | text | NOT NULL | `''::text` |

**约束**

- PRIMARY KEY（1）：`member_role_bindings_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（3）：`member_role_bindings_granted_by_fkey` `FOREIGN KEY (granted_by) REFERENCES identities(id)`；`member_role_bindings_identity_id_fkey` `FOREIGN KEY (identity_id) REFERENCES identities(id) ON DELETE CASCADE`；`member_role_bindings_role_id_fkey` `FOREIGN KEY (role_id) REFERENCES roles(id) ON DELETE CASCADE`
- CHECK（1）：`member_role_bindings_scope_type_check` `CHECK ((scope_type = ANY (ARRAY['organization'::text, 'workspace'::text, 'case'::text, 'self'::text])))`
- NOT NULL 约束对象：6 个（详见上表「可空」列）

**索引（3）**

- `member_role_bindings_identity_idx` — `CREATE INDEX member_role_bindings_identity_idx ON public.member_role_bindings USING btree (identity_id) WHERE (revoked_at IS NULL)`
- `member_role_bindings_pkey` — `CREATE UNIQUE INDEX member_role_bindings_pkey ON public.member_role_bindings USING btree (id)`
- `member_role_bindings_uidx` — `CREATE UNIQUE INDEX member_role_bindings_uidx ON public.member_role_bindings USING btree (identity_id, role_id, scope_type, COALESCE(scope_id, '00000000-0000-0000-0000-000000000000'::uuid)) WHERE (revoked_at IS NULL)`

<a id="method-backfill-jobs"></a>

### `method_backfill_jobs`

| 属性 | 值 |
|---|---|
| 中文用途 | 方法回填作业：检查点交易哈希与进度。 |
| 数据所有者领域 | `store` — 依据：仅命中通用包 → store(1) |
| 敏感级别 | **internal** — 依据：业务数据（含链上标识） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0005_method_abi.sql:67` |
| 行数（精确 `count(*)`） | **4** |
| 行数（`pg_class.reltuples` 估算） | 4 |
| 大小 | **48.0 KiB**（heap 8.0 KiB / index 32.0 KiB / 共 49,152 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `chain` | character varying(32) | NOT NULL | `—` |
| 3 | `status` | character varying(24) | NOT NULL | `'PENDING'::character varying` |
| 4 | `priority` | character varying(16) | NOT NULL | `'background'::character varying` |
| 5 | `batch_size` | integer(32) | NOT NULL | `20000` |
| 6 | `checkpoint_timestamp` | timestamp with time zone | 可空 | `—` |
| 7 | `checkpoint_tx_hash` | character varying(80) | NOT NULL | `''::character varying` |
| 8 | `total_rows` | bigint(64) | NOT NULL | `0` |
| 9 | `scanned_rows` | bigint(64) | NOT NULL | `0` |
| 10 | `rewritten_rows` | bigint(64) | NOT NULL | `0` |
| 11 | `resolved_rows` | bigint(64) | NOT NULL | `0` |
| 12 | `unknown_rows` | bigint(64) | NOT NULL | `0` |
| 13 | `error` | text | NOT NULL | `''::text` |
| 14 | `created_by` | uuid | 可空 | `—` |
| 15 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 16 | `started_at` | timestamp with time zone | 可空 | `—` |
| 17 | `finished_at` | timestamp with time zone | 可空 | `—` |
| 18 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`method_backfill_jobs_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：14 个（详见上表「可空」列）

**索引（2）**

- `idx_method_backfill_status` — `CREATE INDEX idx_method_backfill_status ON public.method_backfill_jobs USING btree (status, created_at)`
- `method_backfill_jobs_pkey` — `CREATE UNIQUE INDEX method_backfill_jobs_pkey ON public.method_backfill_jobs USING btree (id)`

<a id="method-resolve-queue"></a>

### `method_resolve_queue`

| 属性 | 值 |
|---|---|
| 中文用途 | 方法解析队列：待解析 selector 与样本交易哈希。 |
| 数据所有者领域 | `store` — 依据：仅命中通用包 → store(1) |
| 敏感级别 | **internal** — 依据：业务数据（含链上标识） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0005_method_abi.sql:49` |
| 行数（精确 `count(*)`） | **177** |
| 行数（`pg_class.reltuples` 估算） | 177 |
| 大小 | **168.0 KiB**（heap 56.0 KiB / index 72.0 KiB / 共 172,032 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `chain` | character varying(32) | NOT NULL | `—` |
| 3 | `contract_address` | character varying(128) | NOT NULL | `''::character varying` |
| 4 | `method_id` | character varying(10) | NOT NULL | `—` |
| 5 | `sample_tx_hash` | character varying(80) | NOT NULL | `''::character varying` |
| 6 | `input_length` | integer(32) | NOT NULL | `0` |
| 7 | `occurrence_count` | bigint(64) | NOT NULL | `1` |
| 8 | `status` | character varying(24) | NOT NULL | `'PENDING'::character varying` |
| 9 | `attempts` | integer(32) | NOT NULL | `0` |
| 10 | `last_error` | text | NOT NULL | `''::text` |
| 11 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 12 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`method_resolve_queue_pkey` `PRIMARY KEY (id)`
- UNIQUE（1）：`method_resolve_queue_chain_contract_address_method_id_key` `UNIQUE (chain, contract_address, method_id)`
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：12 个（详见上表「可空」列）

**索引（3）**

- `idx_method_resolve_queue_status` — `CREATE INDEX idx_method_resolve_queue_status ON public.method_resolve_queue USING btree (status, updated_at)`
- `method_resolve_queue_chain_contract_address_method_id_key` — `CREATE UNIQUE INDEX method_resolve_queue_chain_contract_address_method_id_key ON public.method_resolve_queue USING btree (chain, contract_address, method_id)`
- `method_resolve_queue_pkey` — `CREATE UNIQUE INDEX method_resolve_queue_pkey ON public.method_resolve_queue USING btree (id)`

<a id="method-signatures"></a>

### `method_signatures`

| 属性 | 值 |
|---|---|
| 中文用途 | 方法签名库：selector 与候选 signature（唯一键为 selector+signature）。 |
| 数据所有者领域 | `method` — 依据：`grep -rl -w method_signatures backend/internal` → store(1) method(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0005_method_abi.sql:3` |
| 行数（精确 `count(*)`） | **107** |
| 行数（`pg_class.reltuples` 估算） | 107 |
| 大小 | **168.0 KiB**（heap 48.0 KiB / index 80.0 KiB / 共 172,032 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `selector` | character varying(10) | NOT NULL | `—` |
| 3 | `method_name` | character varying(255) | NOT NULL | `—` |
| 4 | `signature` | text | NOT NULL | `—` |
| 5 | `source` | character varying(32) | NOT NULL | `—` |
| 6 | `verified` | boolean | NOT NULL | `false` |
| 7 | `occurrence_count` | bigint(64) | NOT NULL | `0` |
| 8 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 9 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`method_signatures_pkey` `PRIMARY KEY (id)`
- UNIQUE（1）：`method_signatures_selector_signature_key` `UNIQUE (selector, signature)`
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：9 个（详见上表「可空」列）

**索引（4）**

- `idx_method_signatures_name` — `CREATE INDEX idx_method_signatures_name ON public.method_signatures USING btree (lower((method_name)::text))`
- `idx_method_signatures_selector` — `CREATE INDEX idx_method_signatures_selector ON public.method_signatures USING btree (selector)`
- `method_signatures_pkey` — `CREATE UNIQUE INDEX method_signatures_pkey ON public.method_signatures USING btree (id)`
- `method_signatures_selector_signature_key` — `CREATE UNIQUE INDEX method_signatures_selector_signature_key ON public.method_signatures USING btree (selector, signature)`

<a id="notification-delivery-attempts"></a>

### `notification_delivery_attempts`

| 属性 | 值 |
|---|---|
| 中文用途 | 通知投递尝试：渠道、脱敏错误与重试序号。 |
| 数据所有者领域 | `notification` — 依据：`data_asset_registry.owner_module` |
| 敏感级别 | **internal** — 依据：`data_asset_registry.sensitivity` |
| 保留期 | 90 天 — 依据：`data_asset_registry.retention`（写入见 `backend/migrations/postgres/0044_tenant_scope.sql:155-176`、`0047_case_collaboration.sql:267-279`） |
| 建表迁移 | `backend/migrations/postgres/0047_case_collaboration.sql:205` |
| 行数（精确 `count(*)`） | **58** |
| 行数（`pg_class.reltuples` 估算） | 51 |
| 大小 | **32.0 KiB**（heap 8.0 KiB / index 16.0 KiB / 共 32,768 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `notification_id` | uuid | NOT NULL | `—` |
| 3 | `channel` | character varying(24) | NOT NULL | `'inbox'::character varying` |
| 4 | `status` | character varying(24) | NOT NULL | `'delivered'::character varying` |
| 5 | `attempts` | integer(32) | NOT NULL | `1` |
| 6 | `provider_message_id` | text | NOT NULL | `''::text` |
| 7 | `error_redacted` | text | NOT NULL | `''::text` |
| 8 | `created_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`notification_delivery_attempts_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（1）：`notification_delivery_attempts_notification_id_fkey` `FOREIGN KEY (notification_id) REFERENCES notifications(id) ON DELETE CASCADE`
- CHECK（0）：无
- NOT NULL 约束对象：8 个（详见上表「可空」列）

**索引（1）**

- `notification_delivery_attempts_pkey` — `CREATE UNIQUE INDEX notification_delivery_attempts_pkey ON public.notification_delivery_attempts USING btree (id)`

<a id="notifications"></a>

### `notifications`

| 属性 | 值 |
|---|---|
| 中文用途 | 站内通知：收件主体、标题正文引用、已读时间。 |
| 数据所有者领域 | `notification` — 依据：`data_asset_registry.owner_module` |
| 敏感级别 | **internal** — 依据：`data_asset_registry.sensitivity` |
| 保留期 | 90 天 — 依据：`data_asset_registry.retention`（写入见 `backend/migrations/postgres/0044_tenant_scope.sql:155-176`、`0047_case_collaboration.sql:267-279`） |
| 建表迁移 | `backend/migrations/postgres/0047_case_collaboration.sql:187` |
| 行数（精确 `count(*)`） | **58** |
| 行数（`pg_class.reltuples` 估算） | 39 |
| 大小 | **104.0 KiB**（heap 24.0 KiB / index 48.0 KiB / 共 106,496 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `user_id` | uuid | NOT NULL | `—` |
| 3 | `organization_id` | uuid | 可空 | `—` |
| 4 | `workspace_id` | uuid | 可空 | `—` |
| 5 | `type` | character varying(48) | NOT NULL | `—` |
| 6 | `title_key` | character varying(128) | NOT NULL | `—` |
| 7 | `title` | text | NOT NULL | `''::text` |
| 8 | `body` | text | NOT NULL | `''::text` |
| 9 | `object_type` | character varying(48) | NOT NULL | `''::character varying` |
| 10 | `object_id` | text | NOT NULL | `''::text` |
| 11 | `payload` | jsonb | NOT NULL | `'{}'::jsonb` |
| 12 | `read_at` | timestamp with time zone | 可空 | `—` |
| 13 | `created_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`notifications_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（3）：`notifications_organization_id_fkey` `FOREIGN KEY (organization_id) REFERENCES organizations(id)`；`notifications_user_id_fkey` `FOREIGN KEY (user_id) REFERENCES identities(id) ON DELETE CASCADE`；`notifications_workspace_id_fkey` `FOREIGN KEY (workspace_id) REFERENCES workspaces(id)`
- CHECK（1）：`notifications_s7_scope_coherence_check` `CHECK (((workspace_id IS NULL) OR (organization_id IS NOT NULL)))`
- NOT NULL 约束对象：10 个（详见上表「可空」列）

**索引（3）**

- `idx_notifications_unread` — `CREATE INDEX idx_notifications_unread ON public.notifications USING btree (user_id, read_at, created_at DESC)`
- `idx_notifications_user` — `CREATE INDEX idx_notifications_user ON public.notifications USING btree (user_id, created_at DESC)`
- `notifications_pkey` — `CREATE UNIQUE INDEX notifications_pkey ON public.notifications USING btree (id)`

<a id="organization-memberships"></a>

### `organization_memberships`

| 属性 | 值 |
|---|---|
| 中文用途 | 组织成员关系：身份在组织中的成员状态与加入时间。 |
| 数据所有者领域 | `policy` — 依据：`grep -rl -w organization_memberships backend/internal` → api(3) policy(1) iam(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0041_rbac.sql:40` |
| 行数（精确 `count(*)`） | **27** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **32.0 KiB**（heap 8.0 KiB / index 16.0 KiB / 共 32,768 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `organization_id` | uuid | NOT NULL | `—` |
| 2 | `identity_id` | uuid | NOT NULL | `—` |
| 3 | `status` | text | NOT NULL | `'active'::text` |
| 4 | `title` | text | NOT NULL | `''::text` |
| 5 | `joined_at` | timestamp with time zone | NOT NULL | `now()` |
| 6 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`organization_memberships_pkey` `PRIMARY KEY (organization_id, identity_id)`
- UNIQUE（0）：无
- FOREIGN KEY（2）：`organization_memberships_identity_id_fkey` `FOREIGN KEY (identity_id) REFERENCES identities(id) ON DELETE CASCADE`；`organization_memberships_organization_id_fkey` `FOREIGN KEY (organization_id) REFERENCES organizations(id) ON DELETE CASCADE`
- CHECK（1）：`organization_memberships_status_check` `CHECK ((status = ANY (ARRAY['invited'::text, 'active'::text, 'suspended'::text, 'removed'::text])))`
- NOT NULL 约束对象：6 个（详见上表「可空」列）

**索引（1）**

- `organization_memberships_pkey` — `CREATE UNIQUE INDEX organization_memberships_pkey ON public.organization_memberships USING btree (organization_id, identity_id)`

<a id="organizations"></a>

### `organizations`

| 属性 | 值 |
|---|---|
| 中文用途 | 租户（组织）主表：平台最上层隔离单元，所有 workspace/case/audit 均挂在组织下。 |
| 数据所有者领域 | `investigation` — 依据：`grep -rl -w organizations backend/internal` → api(2) store(1) investigation(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0001_init.sql:5` |
| 行数（精确 `count(*)`） | **2** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **48.0 KiB**（heap 8.0 KiB / index 32.0 KiB / 共 49,152 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `name` | text | NOT NULL | `—` |
| 3 | `slug` | text | NOT NULL | `—` |
| 4 | `created_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`organizations_pkey` `PRIMARY KEY (id)`
- UNIQUE（1）：`organizations_slug_key` `UNIQUE (slug)`
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：4 个（详见上表「可空」列）

**索引（2）**

- `organizations_pkey` — `CREATE UNIQUE INDEX organizations_pkey ON public.organizations USING btree (id)`
- `organizations_slug_key` — `CREATE UNIQUE INDEX organizations_slug_key ON public.organizations USING btree (slug)`

<a id="outbox-events"></a>

### `outbox_events`

| 属性 | 值 |
|---|---|
| 中文用途 | 事务性发件箱：与业务同事务落库、异步发布的领域事件（available_at/锁定/已发布）。 |
| 数据所有者领域 | `investigation` — 依据：`grep -rl -w outbox_events backend/internal` → investigation(1) events(1) audit(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0028_investigation_phase0.sql:121` |
| 行数（精确 `count(*)`） | **163** |
| 行数（`pg_class.reltuples` 估算） | 147 |
| 大小 | **304.0 KiB**（heap 152.0 KiB / index 112.0 KiB / 共 311,296 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `—` |
| 2 | `event_type` | character varying(128) | NOT NULL | `—` |
| 3 | `event_version` | character varying(32) | NOT NULL | `—` |
| 4 | `aggregate_type` | character varying(64) | NOT NULL | `—` |
| 5 | `aggregate_id` | text | NOT NULL | `—` |
| 6 | `sequence` | bigint(64) | NOT NULL | `—` |
| 7 | `subject` | character varying(255) | NOT NULL | `—` |
| 8 | `envelope` | jsonb | NOT NULL | `—` |
| 9 | `attempts` | integer(32) | NOT NULL | `0` |
| 10 | `available_at` | timestamp with time zone | NOT NULL | `now()` |
| 11 | `published_at` | timestamp with time zone | 可空 | `—` |
| 12 | `last_error` | text | NOT NULL | `''::text` |
| 13 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 14 | `dedupe_key` | text | NOT NULL | `''::text` |
| 15 | `locked_until` | timestamp with time zone | 可空 | `—` |

**约束**

- PRIMARY KEY（1）：`outbox_events_pkey` `PRIMARY KEY (id)`
- UNIQUE（1）：`outbox_events_aggregate_type_aggregate_id_sequence_key` `UNIQUE (aggregate_type, aggregate_id, sequence)`
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：13 个（详见上表「可空」列）

**索引（5）**

- `idx_outbox_pending` — `CREATE INDEX idx_outbox_pending ON public.outbox_events USING btree (available_at, created_at) WHERE (published_at IS NULL)`
- `outbox_events_aggregate_type_aggregate_id_sequence_key` — `CREATE UNIQUE INDEX outbox_events_aggregate_type_aggregate_id_sequence_key ON public.outbox_events USING btree (aggregate_type, aggregate_id, sequence)`
- `outbox_events_dedupe_uidx` — `CREATE UNIQUE INDEX outbox_events_dedupe_uidx ON public.outbox_events USING btree (dedupe_key) WHERE (dedupe_key <> ''::text)`
- `outbox_events_pkey` — `CREATE UNIQUE INDEX outbox_events_pkey ON public.outbox_events USING btree (id)`
- `outbox_events_topic_idx` — `CREATE INDEX outbox_events_topic_idx ON public.outbox_events USING btree (subject, created_at DESC)`

<a id="partition-maintenance-log"></a>

### `partition_maintenance_log`

| 属性 | 值 |
|---|---|
| 中文用途 | 分区维护日志：分区创建/归档候选计算的审计轨迹（不含删除动作）。 |
| 数据所有者领域 | `platform(迁移/运维)` — 依据：仅命中 `backend/migrations/postgres/0057_s7_indexes_partitions.sql` |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0057_s7_indexes_partitions.sql:63` |
| 行数（精确 `count(*)`） | **3** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **32.0 KiB**（heap 8.0 KiB / index 16.0 KiB / 共 32,768 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `table_name` | text | NOT NULL | `—` |
| 3 | `action` | text | NOT NULL | `—` |
| 4 | `partition_name` | text | NOT NULL | `''::text` |
| 5 | `detail` | jsonb | NOT NULL | `'{}'::jsonb` |
| 6 | `created_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`partition_maintenance_log_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：6 个（详见上表「可空」列）

**索引（1）**

- `partition_maintenance_log_pkey` — `CREATE UNIQUE INDEX partition_maintenance_log_pkey ON public.partition_maintenance_log USING btree (id)`

<a id="permissions"></a>

### `permissions`

| 属性 | 值 |
|---|---|
| 中文用途 | 权限点目录：permission_key、分组与风险级别。 |
| 数据所有者领域 | `policy` — 依据：`grep -rl -w permissions backend/internal` → policy(4) api(4) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0041_rbac.sql:10` |
| 行数（精确 `count(*)`） | **177** |
| 行数（`pg_class.reltuples` 估算） | 163 |
| 大小 | **72.0 KiB**（heap 24.0 KiB / index 16.0 KiB / 共 73,728 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `key` | text | NOT NULL | `—` |
| 2 | `category` | text | NOT NULL | `—` |
| 3 | `description` | text | NOT NULL | `''::text` |
| 4 | `risk_level` | text | NOT NULL | `'low'::text` |
| 5 | `sensitive` | boolean | NOT NULL | `false` |
| 6 | `created_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`permissions_pkey` `PRIMARY KEY (key)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（1）：`permissions_risk_level_check` `CHECK ((risk_level = ANY (ARRAY['low'::text, 'medium'::text, 'high'::text, 'critical'::text])))`
- NOT NULL 约束对象：6 个（详见上表「可空」列）

**索引（1）**

- `permissions_pkey` — `CREATE UNIQUE INDEX permissions_pkey ON public.permissions USING btree (key)`

<a id="platform-jobs"></a>

### `platform_jobs`

| 属性 | 值 |
|---|---|
| 中文用途 | 统一任务表：平台级任务（调查/下载/导出）的租约与状态（Phase 0 统一 Job）。 |
| 数据所有者领域 | `investigation` — 依据：`grep -rl -w platform_jobs backend/internal` → investigation(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0028_investigation_phase0.sql:27` |
| 行数（精确 `count(*)`） | **0** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **24.0 KiB**（heap 0 B / index 16.0 KiB / 共 24,576 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `job_type` | character varying(64) | NOT NULL | `—` |
| 3 | `status` | character varying(32) | NOT NULL | `'pending'::character varying` |
| 4 | `priority` | integer(32) | NOT NULL | `100` |
| 5 | `resource_class` | character varying(32) | NOT NULL | `'background'::character varying` |
| 6 | `aggregate_type` | character varying(64) | 可空 | `—` |
| 7 | `aggregate_id` | uuid | 可空 | `—` |
| 8 | `payload` | jsonb | NOT NULL | `'{}'::jsonb` |
| 9 | `checkpoint` | jsonb | NOT NULL | `'{}'::jsonb` |
| 10 | `request_context` | jsonb | NOT NULL | `'{}'::jsonb` |
| 11 | `attempt` | integer(32) | NOT NULL | `0` |
| 12 | `max_attempts` | integer(32) | NOT NULL | `3` |
| 13 | `lease_owner` | text | 可空 | `—` |
| 14 | `leased_until` | timestamp with time zone | 可空 | `—` |
| 15 | `error` | text | NOT NULL | `''::text` |
| 16 | `contract_version` | character varying(16) | NOT NULL | `'1.0'::character varying` |
| 17 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 18 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`platform_jobs_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（1）：`platform_jobs_resource_class_check` `CHECK (((resource_class)::text = ANY ((ARRAY['interactive'::character varying, 'background'::character varying, 'backfill'::character varying, 'maintenance'::character varying])::text[])))`
- NOT NULL 约束对象：14 个（详见上表「可空」列）

**索引（2）**

- `idx_platform_jobs_ready` — `CREATE INDEX idx_platform_jobs_ready ON public.platform_jobs USING btree (resource_class, priority, created_at) WHERE ((status)::text = ANY ((ARRAY['pending'::character varying, 'retry'::character varying])::text[]))`
- `platform_jobs_pkey` — `CREATE UNIQUE INDEX platform_jobs_pkey ON public.platform_jobs USING btree (id)`

<a id="projection-registry"></a>

### `projection_registry`

| 属性 | 值 |
|---|---|
| 中文用途 | 投影注册表：投影器元数据与重建时间。 |
| 数据所有者领域 | `investigation` — 依据：`grep -rl -w projection_registry backend/internal` → investigation(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0028_investigation_phase0.sql:60` |
| 行数（精确 `count(*)`） | **5** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **32.0 KiB**（heap 8.0 KiB / index 16.0 KiB / 共 32,768 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `projection_name` | character varying(128) | NOT NULL | `—` |
| 2 | `projection_version` | character varying(32) | NOT NULL | `—` |
| 3 | `source_schema` | character varying(128) | NOT NULL | `—` |
| 4 | `source_version` | character varying(32) | NOT NULL | `—` |
| 5 | `status` | character varying(24) | NOT NULL | `'active'::character varying` |
| 6 | `checkpoint` | jsonb | NOT NULL | `'{}'::jsonb` |
| 7 | `rebuilt_at` | timestamp with time zone | 可空 | `—` |
| 8 | `created_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`projection_registry_pkey` `PRIMARY KEY (projection_name, projection_version)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：7 个（详见上表「可空」列）

**索引（1）**

- `projection_registry_pkey` — `CREATE UNIQUE INDEX projection_registry_pkey ON public.projection_registry USING btree (projection_name, projection_version)`

<a id="projects"></a>

### `projects`

| 属性 | 值 |
|---|---|
| 中文用途 | 项目预留表：企业化预留的项目分组（0002 企业预留，当前无数据）。 |
| 数据所有者领域 | `platform(企业预留)` — 依据：仅命中 `backend/migrations/postgres/0002_enterprise_reserved.sql` |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0002_enterprise_reserved.sql:11` |
| 行数（精确 `count(*)`） | **1** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **32.0 KiB**（heap 8.0 KiB / index 16.0 KiB / 共 32,768 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `workspace_id` | uuid | NOT NULL | `—` |
| 3 | `name` | text | NOT NULL | `—` |
| 4 | `created_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`projects_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（1）：`projects_workspace_id_fkey` `FOREIGN KEY (workspace_id) REFERENCES workspaces(id)`
- CHECK（0）：无
- NOT NULL 约束对象：4 个（详见上表「可空」列）

**索引（1）**

- `projects_pkey` — `CREATE UNIQUE INDEX projects_pkey ON public.projects USING btree (id)`

<a id="provider-descriptors"></a>

### `provider_descriptors`

| 属性 | 值 |
|---|---|
| 中文用途 | Provider 描述符：能力、端点与最近测试结果。 |
| 数据所有者领域 | `api` — 依据：仅命中通用包 → api(3) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0051_s6_control_governance.sql:173` |
| 行数（精确 `count(*)`） | **4** |
| 行数（`pg_class.reltuples` 估算） | 4 |
| 大小 | **48.0 KiB**（heap 8.0 KiB / index 32.0 KiB / 共 49,152 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `provider_key` | text | NOT NULL | `—` |
| 3 | `display_name` | text | NOT NULL | `—` |
| 4 | `kind` | text | NOT NULL | `'rpc'::text` |
| 5 | `chains` | ARRAY | NOT NULL | `'{}'::text[]` |
| 6 | `datasets` | ARRAY | NOT NULL | `'{}'::text[]` |
| 7 | `capabilities` | jsonb | NOT NULL | `'[]'::jsonb` |
| 8 | `endpoint` | text | NOT NULL | `''::text` |
| 9 | `status` | text | NOT NULL | `'disconnected'::text` |
| 10 | `config` | jsonb | NOT NULL | `'{}'::jsonb` |
| 11 | `concurrency` | integer(32) | NOT NULL | `0` |
| 12 | `rate_limit_per_minute` | integer(32) | NOT NULL | `0` |
| 13 | `priority` | integer(32) | NOT NULL | `100` |
| 14 | `revision` | bigint(64) | NOT NULL | `1` |
| 15 | `capability_drift` | jsonb | 可空 | `—` |
| 16 | `last_test_at` | timestamp with time zone | 可空 | `—` |
| 17 | `last_test_result` | jsonb | 可空 | `—` |
| 18 | `updated_by` | uuid | 可空 | `—` |
| 19 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 20 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`provider_descriptors_pkey` `PRIMARY KEY (id)`
- UNIQUE（1）：`provider_descriptors_provider_key_key` `UNIQUE (provider_key)`
- FOREIGN KEY（1）：`provider_descriptors_updated_by_fkey` `FOREIGN KEY (updated_by) REFERENCES identities(id)`
- CHECK（2）：`provider_descriptors_kind_check` `CHECK ((kind = ANY (ARRAY['rpc'::text, 'count'::text, 'management'::text, 'mock'::text, 'local'::text])))`；`provider_descriptors_status_check` `CHECK ((status = ANY (ARRAY['connected'::text, 'disconnected'::text, 'degraded'::text, 'disabled'::text])))`
- NOT NULL 约束对象：16 个（详见上表「可空」列）

**索引（2）**

- `provider_descriptors_pkey` — `CREATE UNIQUE INDEX provider_descriptors_pkey ON public.provider_descriptors USING btree (id)`
- `provider_descriptors_provider_key_key` — `CREATE UNIQUE INDEX provider_descriptors_provider_key_key ON public.provider_descriptors USING btree (provider_key)`

<a id="provider-pricing"></a>

### `provider_pricing`

| 属性 | 值 |
|---|---|
| 中文用途 | Provider 定价：按资源类型的单价与生效区间。 |
| 数据所有者领域 | `store` — 依据：仅命中通用包 → store(1) api(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0010_rpc_infrastructure.sql:62` |
| 行数（精确 `count(*)`） | **2** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **40.0 KiB**（heap 8.0 KiB / index 32.0 KiB / 共 40,960 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `provider` | character varying(64) | NOT NULL | `—` |
| 3 | `api_type` | character varying(16) | NOT NULL | `—` |
| 4 | `plan` | character varying(32) | NOT NULL | `'payg'::character varying` |
| 5 | `credits_per_call` | double precision(53) | NOT NULL | `—` |
| 6 | `cost_per_call_usd` | double precision(53) | NOT NULL | `—` |
| 7 | `effective_from` | timestamp with time zone | NOT NULL | `now()` |
| 8 | `effective_to` | timestamp with time zone | 可空 | `—` |
| 9 | `created_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`provider_pricing_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：8 个（详见上表「可空」列）

**索引（2）**

- `idx_provider_pricing_window` — `CREATE INDEX idx_provider_pricing_window ON public.provider_pricing USING btree (provider, api_type, effective_from DESC)`
- `provider_pricing_pkey` — `CREATE UNIQUE INDEX provider_pricing_pkey ON public.provider_pricing USING btree (id)`

<a id="provider-secrets"></a>

### `provider_secrets`

| 属性 | 值 |
|---|---|
| 中文用途 | Provider 密钥引用：只存 secret_ref 与轮换元数据，不存明文。 |
| 数据所有者领域 | `api` — 依据：仅命中通用包 → api(1) |
| 敏感级别 | **confidential** — 依据：含凭证/密钥类字段：secret_ref |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0051_s6_control_governance.sql:199` |
| 行数（精确 `count(*)`） | **14** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **48.0 KiB**（heap 8.0 KiB / index 32.0 KiB / 共 49,152 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `provider_key` | text | NOT NULL | `—` |
| 3 | `secret_ref` | text | NOT NULL | `—` |
| 4 | `prefix` | text | NOT NULL | `''::text` |
| 5 | `fingerprint` | text | NOT NULL | `—` |
| 6 | `status` | text | NOT NULL | `'active'::text` |
| 7 | `note` | text | NOT NULL | `''::text` |
| 8 | `created_by` | uuid | 可空 | `—` |
| 9 | `rotated_by` | uuid | 可空 | `—` |
| 10 | `rotated_at` | timestamp with time zone | NOT NULL | `now()` |
| 11 | `last_used_at` | timestamp with time zone | 可空 | `—` |
| 12 | `expires_at` | timestamp with time zone | 可空 | `—` |
| 13 | `approval_request_id` | uuid | 可空 | `—` |

**约束**

- PRIMARY KEY（1）：`provider_secrets_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（4）：`provider_secrets_approval_request_id_fkey` `FOREIGN KEY (approval_request_id) REFERENCES approval_requests(id)`；`provider_secrets_created_by_fkey` `FOREIGN KEY (created_by) REFERENCES identities(id)`；`provider_secrets_provider_key_fkey` `FOREIGN KEY (provider_key) REFERENCES provider_descriptors(provider_key) ON DELETE CASCADE`；`provider_secrets_rotated_by_fkey` `FOREIGN KEY (rotated_by) REFERENCES identities(id)`
- CHECK（1）：`provider_secrets_status_check` `CHECK ((status = ANY (ARRAY['active'::text, 'retiring'::text, 'revoked'::text])))`
- NOT NULL 约束对象：8 个（详见上表「可空」列）

**索引（2）**

- `provider_secrets_pkey` — `CREATE UNIQUE INDEX provider_secrets_pkey ON public.provider_secrets USING btree (id)`
- `provider_secrets_provider_idx` — `CREATE INDEX provider_secrets_provider_idx ON public.provider_secrets USING btree (provider_key, rotated_at DESC)`

<a id="provider-usage"></a>

### `provider_usage`

| 属性 | 值 |
|---|---|
| 中文用途 | Provider 调用用量明细：每次 API 调用的字节数、TTFB/body 耗时、错误与数据集归属。 |
| 数据所有者领域 | `download` — 依据：`grep -rl -w provider_usage backend/internal` → api(9) store(4) download(3) |
| 敏感级别 | **internal** — 依据：业务数据（含链上标识） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0001_init.sql:133` |
| 行数（精确 `count(*)`） | **416,066** |
| 行数（`pg_class.reltuples` 估算） | 408,316 |
| 大小 | **293.48 MiB**（heap 152.45 MiB / index 140.95 MiB / 共 307,732,480 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `—` |
| 2 | `provider` | text | NOT NULL | `—` |
| 3 | `method` | text | NOT NULL | `—` |
| 4 | `dataset` | text | NOT NULL | `''::text` |
| 5 | `job_id` | uuid | 可空 | `—` |
| 6 | `shard_id` | uuid | 可空 | `—` |
| 7 | `tenant_id` | text | NOT NULL | `''::text` |
| 8 | `request_time` | timestamp with time zone | NOT NULL | `now()` |
| 9 | `duration_ms` | integer(32) | NOT NULL | `0` |
| 10 | `status` | text | NOT NULL | `—` |
| 11 | `http_status` | integer(32) | NOT NULL | `0` |
| 12 | `api_error` | text | NOT NULL | `''::text` |
| 13 | `rows` | integer(32) | NOT NULL | `0` |
| 14 | `bytes` | integer(32) | NOT NULL | `0` |
| 15 | `credits` | real(24) | NOT NULL | `0` |
| 16 | `estimated_cost_usd` | real(24) | NOT NULL | `0` |
| 17 | `error_class` | character varying(64) | NOT NULL | `''::character varying` |
| 18 | `rpc_code` | integer(32) | NOT NULL | `0` |
| 19 | `trace_id` | character varying(128) | NOT NULL | `''::character varying` |
| 20 | `page_size` | integer(32) | NOT NULL | `0` |
| 21 | `returned_rows` | bigint(64) | NOT NULL | `0` |
| 22 | `response_bytes` | bigint(64) | NOT NULL | `0` |
| 23 | `retry_attempt` | integer(32) | NOT NULL | `0` |
| 24 | `retry_reason` | character varying(256) | NOT NULL | `''::character varying` |
| 25 | `request_id` | character varying(64) | NOT NULL | `''::character varying` |
| 26 | `usage_type` | character varying(32) | NOT NULL | `'dataset_download'::character varying` |
| 27 | `tx_hash` | character varying(80) | NOT NULL | `''::character varying` |
| 28 | `cache_hit` | boolean | NOT NULL | `false` |
| 29 | `decode_priority` | character varying(16) | NOT NULL | `'normal'::character varying` |
| 30 | `decode_budget` | integer(32) | 可空 | `—` |
| 31 | `api_type` | character varying(16) | NOT NULL | `'advanced'::character varying` |
| 32 | `chain` | character varying(32) | NOT NULL | `''::character varying` |
| 33 | `method_class` | character varying(32) | NOT NULL | `''::character varying` |
| 34 | `method_calls` | integer(32) | NOT NULL | `1` |
| 35 | `batch_size` | integer(32) | NOT NULL | `0` |
| 36 | `singleflight_hit` | boolean | NOT NULL | `false` |
| 37 | `investigation_id` | uuid | 可空 | `—` |
| 38 | `priority` | character varying(16) | NOT NULL | `'normal'::character varying` |
| 39 | `wire_bytes` | bigint(64) | NOT NULL | `0` |
| 40 | `decoded_bytes` | bigint(64) | NOT NULL | `0` |
| 41 | `ttfb_ms` | integer(32) | NOT NULL | `0` |
| 42 | `body_ms` | integer(32) | NOT NULL | `0` |
| 43 | `logical_calls` | integer(32) | NOT NULL | `0` |
| 44 | `http_requests` | integer(32) | NOT NULL | `0` |
| 45 | `logs_requests` | integer(32) | NOT NULL | `0` |
| 46 | `matched_rows` | integer(32) | NOT NULL | `0` |
| 47 | `fallback_rows` | integer(32) | NOT NULL | `0` |
| 48 | `saved_calls` | integer(32) | NOT NULL | `0` |
| 49 | `failure_reasons` | jsonb | NOT NULL | `'{}'::jsonb` |

**约束**

- PRIMARY KEY（1）：`provider_usage_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：45 个（详见上表「可空」列）

**索引（7）**

- `idx_provider_usage_api_type` — `CREATE INDEX idx_provider_usage_api_type ON public.provider_usage USING btree (api_type, request_time DESC)`
- `idx_provider_usage_chain_method` — `CREATE INDEX idx_provider_usage_chain_method ON public.provider_usage USING btree (chain, method_class, request_time DESC)`
- `idx_provider_usage_trc20_summary` — `CREATE INDEX idx_provider_usage_trc20_summary ON public.provider_usage USING btree (job_id, request_time DESC) WHERE ((usage_type)::text = 'trc20_enrichment_summary'::text)`
- `idx_provider_usage_usage_type` — `CREATE INDEX idx_provider_usage_usage_type ON public.provider_usage USING btree (usage_type, request_time DESC)`
- `idx_usage_provider` — `CREATE INDEX idx_usage_provider ON public.provider_usage USING btree (provider, request_time DESC)`
- `idx_usage_time` — `CREATE INDEX idx_usage_time ON public.provider_usage USING btree (request_time DESC)`
- `provider_usage_pkey` — `CREATE UNIQUE INDEX provider_usage_pkey ON public.provider_usage USING btree (id)`

<a id="quotas"></a>

### `quotas`

| 属性 | 值 |
|---|---|
| 中文用途 | 配额预留表：企业化预留的组织级资源配额（当前无数据）。 |
| 数据所有者领域 | `platform(企业预留)` — 依据：仅命中 `backend/migrations/postgres/0002_enterprise_reserved.sql` |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0002_enterprise_reserved.sql:19` |
| 行数（精确 `count(*)`） | **0** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **24.0 KiB**（heap 0 B / index 16.0 KiB / 共 24,576 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `organization_id` | uuid | NOT NULL | `—` |
| 3 | `provider` | text | NOT NULL | `'ankr'::text` |
| 4 | `requests_per_day` | bigint(64) | NOT NULL | `0` |
| 5 | `credits_per_day` | bigint(64) | NOT NULL | `0` |
| 6 | `cost_per_day_usd` | numeric(14) | NOT NULL | `0` |
| 7 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`quotas_pkey` `PRIMARY KEY (id)`
- UNIQUE（1）：`quotas_organization_id_provider_key` `UNIQUE (organization_id, provider)`
- FOREIGN KEY（1）：`quotas_organization_id_fkey` `FOREIGN KEY (organization_id) REFERENCES organizations(id)`
- CHECK（0）：无
- NOT NULL 约束对象：7 个（详见上表「可空」列）

**索引（2）**

- `quotas_organization_id_provider_key` — `CREATE UNIQUE INDEX quotas_organization_id_provider_key ON public.quotas USING btree (organization_id, provider)`
- `quotas_pkey` — `CREATE UNIQUE INDEX quotas_pkey ON public.quotas USING btree (id)`

<a id="report-approvals"></a>

### `report_approvals`

| 属性 | 值 |
|---|---|
| 中文用途 | 报告审批记录：revision、审批人、内容哈希（证据级留存）。 |
| 数据所有者领域 | `report` — 依据：`data_asset_registry.owner_module` |
| 敏感级别 | **restricted** — 依据：`data_asset_registry.sensitivity` |
| 保留期 | 长期保留（证据） — 依据：`data_asset_registry.retention`（写入见 `backend/migrations/postgres/0044_tenant_scope.sql:155-176`、`0047_case_collaboration.sql:267-279`） |
| 建表迁移 | `backend/migrations/postgres/0047_case_collaboration.sql:159` |
| 行数（精确 `count(*)`） | **21** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **64.0 KiB**（heap 8.0 KiB / index 48.0 KiB / 共 65,536 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `report_id` | uuid | NOT NULL | `—` |
| 3 | `report_revision` | bigint(64) | NOT NULL | `—` |
| 4 | `reviewer_id` | uuid | 可空 | `—` |
| 5 | `decision` | character varying(16) | NOT NULL | `—` |
| 6 | `content_hash` | character varying(64) | NOT NULL | `''::character varying` |
| 7 | `note` | text | NOT NULL | `''::text` |
| 8 | `created_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`report_approvals_pkey` `PRIMARY KEY (id)`
- UNIQUE（1）：`report_approvals_report_id_report_revision_reviewer_id_deci_key` `UNIQUE (report_id, report_revision, reviewer_id, decision)`
- FOREIGN KEY（2）：`report_approvals_report_id_fkey` `FOREIGN KEY (report_id) REFERENCES investigation_reports(id) ON DELETE CASCADE`；`report_approvals_reviewer_id_fkey` `FOREIGN KEY (reviewer_id) REFERENCES identities(id)`
- CHECK（1）：`report_approvals_decision_check` `CHECK (((decision)::text = ANY ((ARRAY['approve'::character varying, 'reject'::character varying])::text[])))`
- NOT NULL 约束对象：7 个（详见上表「可空」列）

**索引（3）**

- `idx_report_approvals_report` — `CREATE INDEX idx_report_approvals_report ON public.report_approvals USING btree (report_id, report_revision, created_at DESC)`
- `report_approvals_pkey` — `CREATE UNIQUE INDEX report_approvals_pkey ON public.report_approvals USING btree (id)`
- `report_approvals_report_id_report_revision_reviewer_id_deci_key` — `CREATE UNIQUE INDEX report_approvals_report_id_report_revision_reviewer_id_deci_key ON public.report_approvals USING btree (report_id, report_revision, reviewer_id, decision)`

<a id="report-download-tickets"></a>

### `report_download_tickets`

| 属性 | 值 |
|---|---|
| 中文用途 | 报告下载票据：仅存 token 摘要，带过期与使用状态。 |
| 数据所有者领域 | `report` — 依据：`data_asset_registry.owner_module` |
| 敏感级别 | **restricted** — 依据：`data_asset_registry.sensitivity` |
| 保留期 | 票据过期后 30 天 — 依据：`data_asset_registry.retention`（写入见 `backend/migrations/postgres/0044_tenant_scope.sql:155-176`、`0047_case_collaboration.sql:267-279`） |
| 建表迁移 | `backend/migrations/postgres/0047_case_collaboration.sql:172` |
| 行数（精确 `count(*)`） | **14** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **56.0 KiB**（heap 8.0 KiB / index 48.0 KiB / 共 57,344 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `report_id` | uuid | NOT NULL | `—` |
| 3 | `token_hash` | character varying(64) | NOT NULL | `—` |
| 4 | `user_id` | uuid | 可空 | `—` |
| 5 | `organization_id` | uuid | NOT NULL | `—` |
| 6 | `workspace_id` | uuid | 可空 | `—` |
| 7 | `expires_at` | timestamp with time zone | NOT NULL | `—` |
| 8 | `used_at` | timestamp with time zone | 可空 | `—` |
| 9 | `created_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`report_download_tickets_pkey` `PRIMARY KEY (id)`
- UNIQUE（1）：`report_download_tickets_token_hash_key` `UNIQUE (token_hash)`
- FOREIGN KEY（4）：`report_download_tickets_organization_id_fkey` `FOREIGN KEY (organization_id) REFERENCES organizations(id)`；`report_download_tickets_report_id_fkey` `FOREIGN KEY (report_id) REFERENCES investigation_reports(id) ON DELETE CASCADE`；`report_download_tickets_user_id_fkey` `FOREIGN KEY (user_id) REFERENCES identities(id)`；`report_download_tickets_workspace_id_fkey` `FOREIGN KEY (workspace_id) REFERENCES workspaces(id)`
- CHECK（1）：`report_download_tickets_s7_scope_coherence_check` `CHECK (((workspace_id IS NULL) OR (organization_id IS NOT NULL)))`
- NOT NULL 约束对象：6 个（详见上表「可空」列）

**索引（3）**

- `idx_report_tickets_expiry` — `CREATE INDEX idx_report_tickets_expiry ON public.report_download_tickets USING btree (expires_at) WHERE (used_at IS NULL)`
- `report_download_tickets_pkey` — `CREATE UNIQUE INDEX report_download_tickets_pkey ON public.report_download_tickets USING btree (id)`
- `report_download_tickets_token_hash_key` — `CREATE UNIQUE INDEX report_download_tickets_token_hash_key ON public.report_download_tickets USING btree (token_hash)`

<a id="restore-previews"></a>

### `restore_previews`

| 属性 | 值 |
|---|---|
| 中文用途 | 恢复预览：恢复演练的影响评估（只读）。 |
| 数据所有者领域 | `api` — 依据：仅命中通用包 → api(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0052_s6_data_governance.sql:132` |
| 行数（精确 `count(*)`） | **0** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **24.0 KiB**（heap 0 B / index 16.0 KiB / 共 24,576 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `backup_id` | uuid | 可空 | `—` |
| 3 | `target` | text | NOT NULL | `'all'::text` |
| 4 | `scope` | jsonb | NOT NULL | `'{}'::jsonb` |
| 5 | `impact` | jsonb | NOT NULL | `'{}'::jsonb` |
| 6 | `conflict_count` | bigint(64) | NOT NULL | `0` |
| 7 | `verified` | boolean | NOT NULL | `false` |
| 8 | `verify_detail` | jsonb | NOT NULL | `'{}'::jsonb` |
| 9 | `maintenance_window` | text | NOT NULL | `''::text` |
| 10 | `approval_request_id` | uuid | 可空 | `—` |
| 11 | `requested_by` | uuid | 可空 | `—` |
| 12 | `created_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`restore_previews_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（3）：`restore_previews_approval_request_id_fkey` `FOREIGN KEY (approval_request_id) REFERENCES approval_requests(id)`；`restore_previews_backup_id_fkey` `FOREIGN KEY (backup_id) REFERENCES backup_jobs(id) ON DELETE SET NULL`；`restore_previews_requested_by_fkey` `FOREIGN KEY (requested_by) REFERENCES identities(id)`
- CHECK（0）：无
- NOT NULL 约束对象：9 个（详见上表「可空」列）

**索引（2）**

- `restore_previews_backup_idx` — `CREATE INDEX restore_previews_backup_idx ON public.restore_previews USING btree (backup_id, created_at DESC)`
- `restore_previews_pkey` — `CREATE UNIQUE INDEX restore_previews_pkey ON public.restore_previews USING btree (id)`

<a id="retention-policies"></a>

### `retention_policies`

| 属性 | 值 |
|---|---|
| 中文用途 | 保留策略：资源类型、retain_days/archive_after_days/action/grace_days/legal_hold/enabled。 |
| 数据所有者领域 | `api` — 依据：仅命中通用包 → api(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0052_s6_data_governance.sql:10` |
| 行数（精确 `count(*)`） | **6** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **64.0 KiB**（heap 8.0 KiB / index 48.0 KiB / 共 65,536 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `policy_key` | text | NOT NULL | `—` |
| 3 | `description` | text | NOT NULL | `''::text` |
| 4 | `resource_type` | text | NOT NULL | `—` |
| 5 | `scope_type` | text | NOT NULL | `'global'::text` |
| 6 | `scope_org_id` | uuid | 可空 | `—` |
| 7 | `scope_workspace_id` | uuid | 可空 | `—` |
| 8 | `retain_days` | integer(32) | NOT NULL | `365` |
| 9 | `archive_after_days` | integer(32) | 可空 | `—` |
| 10 | `action` | text | NOT NULL | `'archive'::text` |
| 11 | `grace_days` | integer(32) | NOT NULL | `7` |
| 12 | `legal_hold` | boolean | NOT NULL | `false` |
| 13 | `enabled` | boolean | NOT NULL | `false` |
| 14 | `last_run_at` | timestamp with time zone | 可空 | `—` |
| 15 | `next_run_at` | timestamp with time zone | 可空 | `—` |
| 16 | `revision` | bigint(64) | NOT NULL | `1` |
| 17 | `updated_by` | uuid | 可空 | `—` |
| 18 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 19 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`retention_policies_pkey` `PRIMARY KEY (id)`
- UNIQUE（1）：`retention_policies_policy_key_key` `UNIQUE (policy_key)`
- FOREIGN KEY（3）：`retention_policies_scope_org_id_fkey` `FOREIGN KEY (scope_org_id) REFERENCES organizations(id)`；`retention_policies_scope_workspace_id_fkey` `FOREIGN KEY (scope_workspace_id) REFERENCES workspaces(id)`；`retention_policies_updated_by_fkey` `FOREIGN KEY (updated_by) REFERENCES identities(id)`
- CHECK（6）：`retention_policies_action_check` `CHECK ((action = ANY (ARRAY['archive'::text, 'delete'::text, 'none'::text])))`；`retention_policies_archive_after_days_check` `CHECK (((archive_after_days IS NULL) OR (archive_after_days >= 0)))`；`retention_policies_grace_days_check` `CHECK ((grace_days >= 0))`；`retention_policies_resource_type_check` `CHECK ((resource_type = ANY (ARRAY['raw_object'::text, 'export_object'::text, 'user_activity'::text, 'audit_archive'::text, 'clickhouse_partition'::text, 'cache_object'::text, 'job_artifact'::text])))`；`retention_policies_retain_days_check` `CHECK ((retain_days >= 0))`；`retention_policies_scope_type_check` `CHECK ((scope_type = ANY (ARRAY['global'::text, 'organization'::text, 'workspace'::text])))`
- NOT NULL 约束对象：13 个（详见上表「可空」列）

**索引（3）**

- `retention_policies_pkey` — `CREATE UNIQUE INDEX retention_policies_pkey ON public.retention_policies USING btree (id)`
- `retention_policies_policy_key_key` — `CREATE UNIQUE INDEX retention_policies_policy_key_key ON public.retention_policies USING btree (policy_key)`
- `retention_policies_resource_idx` — `CREATE INDEX retention_policies_resource_idx ON public.retention_policies USING btree (resource_type, enabled)`

<a id="retention-runs"></a>

### `retention_runs`

| 属性 | 值 |
|---|---|
| 中文用途 | 保留策略执行记录：dry-run 统计、匹配对象数与审批引用。 |
| 数据所有者领域 | `api` — 依据：仅命中通用包 → api(1) |
| 敏感级别 | **confidential** — 依据：含 PII 字段：requested_by_email |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0052_s6_data_governance.sql:40` |
| 行数（精确 `count(*)`） | **0** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **24.0 KiB**（heap 0 B / index 16.0 KiB / 共 24,576 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `policy_id` | uuid | 可空 | `—` |
| 3 | `policy_key` | text | NOT NULL | `''::text` |
| 4 | `mode` | text | NOT NULL | `'preview'::text` |
| 5 | `status` | text | NOT NULL | `'preview'::text` |
| 6 | `dry_run` | boolean | NOT NULL | `true` |
| 7 | `scanned_objects` | bigint(64) | NOT NULL | `0` |
| 8 | `matched_objects` | bigint(64) | NOT NULL | `0` |
| 9 | `matched_bytes` | bigint(64) | NOT NULL | `0` |
| 10 | `sample` | jsonb | NOT NULL | `'[]'::jsonb` |
| 11 | `impact` | jsonb | NOT NULL | `'{}'::jsonb` |
| 12 | `block_reason` | text | NOT NULL | `''::text` |
| 13 | `approval_request_id` | uuid | 可空 | `—` |
| 14 | `requested_by` | uuid | 可空 | `—` |
| 15 | `requested_by_email` | text | NOT NULL | `''::text` |
| 16 | `started_at` | timestamp with time zone | 可空 | `—` |
| 17 | `finished_at` | timestamp with time zone | 可空 | `—` |
| 18 | `result` | jsonb | 可空 | `—` |
| 19 | `created_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`retention_runs_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（3）：`retention_runs_approval_request_id_fkey` `FOREIGN KEY (approval_request_id) REFERENCES approval_requests(id)`；`retention_runs_policy_id_fkey` `FOREIGN KEY (policy_id) REFERENCES retention_policies(id) ON DELETE SET NULL`；`retention_runs_requested_by_fkey` `FOREIGN KEY (requested_by) REFERENCES identities(id)`
- CHECK（2）：`retention_runs_mode_check` `CHECK ((mode = ANY (ARRAY['preview'::text, 'execute'::text])))`；`retention_runs_status_check` `CHECK ((status = ANY (ARRAY['preview'::text, 'pending_approval'::text, 'approved'::text, 'executing'::text, 'completed'::text, 'failed'::text, 'blocked_disabled'::text, 'rejected'::text])))`
- NOT NULL 约束对象：13 个（详见上表「可空」列）

**索引（2）**

- `retention_runs_pkey` — `CREATE UNIQUE INDEX retention_runs_pkey ON public.retention_runs USING btree (id)`
- `retention_runs_policy_idx` — `CREATE INDEX retention_runs_policy_idx ON public.retention_runs USING btree (policy_key, created_at DESC)`

<a id="review-requests"></a>

### `review_requests`

| 属性 | 值 |
|---|---|
| 中文用途 | 复核请求：Finding/Evidence/报告提交复核的状态机与决策人。 |
| 数据所有者领域 | `review` — 依据：`data_asset_registry.owner_module` |
| 敏感级别 | **confidential** — 依据：`data_asset_registry.sensitivity` |
| 保留期 | 随对象归档 — 依据：`data_asset_registry.retention`（写入见 `backend/migrations/postgres/0044_tenant_scope.sql:155-176`、`0047_case_collaboration.sql:267-279`） |
| 建表迁移 | `backend/migrations/postgres/0047_case_collaboration.sql:84` |
| 行数（精确 `count(*)`） | **21** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **80.0 KiB**（heap 8.0 KiB / index 64.0 KiB / 共 81,920 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `organization_id` | uuid | NOT NULL | `—` |
| 3 | `workspace_id` | uuid | NOT NULL | `—` |
| 4 | `case_id` | uuid | 可空 | `—` |
| 5 | `investigation_id` | uuid | 可空 | `—` |
| 6 | `resource_type` | character varying(32) | NOT NULL | `—` |
| 7 | `resource_id` | uuid | NOT NULL | `—` |
| 8 | `requested_by` | uuid | 可空 | `—` |
| 9 | `reviewer_id` | uuid | 可空 | `—` |
| 10 | `status` | character varying(16) | NOT NULL | `'pending'::character varying` |
| 11 | `reason` | text | NOT NULL | `''::text` |
| 12 | `decision_note` | text | NOT NULL | `''::text` |
| 13 | `revision` | bigint(64) | NOT NULL | `1` |
| 14 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 15 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |
| 16 | `decided_at` | timestamp with time zone | 可空 | `—` |

**约束**

- PRIMARY KEY（1）：`review_requests_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（6）：`review_requests_case_id_fkey` `FOREIGN KEY (case_id) REFERENCES cases(id) ON DELETE SET NULL`；`review_requests_investigation_id_fkey` `FOREIGN KEY (investigation_id) REFERENCES investigations(id) ON DELETE SET NULL`；`review_requests_organization_id_fkey` `FOREIGN KEY (organization_id) REFERENCES organizations(id)`；`review_requests_requested_by_fkey` `FOREIGN KEY (requested_by) REFERENCES identities(id)`；`review_requests_reviewer_id_fkey` `FOREIGN KEY (reviewer_id) REFERENCES identities(id)`；`review_requests_workspace_id_fkey` `FOREIGN KEY (workspace_id) REFERENCES workspaces(id)`
- CHECK（2）：`review_requests_resource_type_check` `CHECK (((resource_type)::text = ANY ((ARRAY['investigation'::character varying, 'finding'::character varying, 'evidence'::character varying, 'report'::character varying])::text[])))`；`review_requests_status_check` `CHECK (((status)::text = ANY ((ARRAY['pending'::character varying, 'approved'::character varying, 'rejected'::character varying, 'cancelled'::character varying])::text[])))`
- NOT NULL 约束对象：11 个（详见上表「可空」列）

**索引（4）**

- `idx_review_requests_resource` — `CREATE INDEX idx_review_requests_resource ON public.review_requests USING btree (resource_type, resource_id, status)`
- `idx_review_requests_reviewer` — `CREATE INDEX idx_review_requests_reviewer ON public.review_requests USING btree (reviewer_id, status, created_at DESC)`
- `idx_review_requests_scope` — `CREATE INDEX idx_review_requests_scope ON public.review_requests USING btree (organization_id, workspace_id, status, created_at DESC)`
- `review_requests_pkey` — `CREATE UNIQUE INDEX review_requests_pkey ON public.review_requests USING btree (id)`

<a id="role-permissions"></a>

### `role_permissions`

| 属性 | 值 |
|---|---|
| 中文用途 | 角色—权限绑定：授予时间与授予人。 |
| 数据所有者领域 | `policy` — 依据：`grep -rl -w role_permissions backend/internal` → api(2) policy(1) casework(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0041_rbac.sql:32` |
| 行数（精确 `count(*)`） | **563** |
| 行数（`pg_class.reltuples` 估算） | 496 |
| 大小 | **136.0 KiB**（heap 40.0 KiB / index 64.0 KiB / 共 139,264 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `role_id` | uuid | NOT NULL | `—` |
| 2 | `permission_key` | text | NOT NULL | `—` |
| 3 | `granted_at` | timestamp with time zone | NOT NULL | `now()` |
| 4 | `granted_by` | uuid | 可空 | `—` |

**约束**

- PRIMARY KEY（1）：`role_permissions_pkey` `PRIMARY KEY (role_id, permission_key)`
- UNIQUE（0）：无
- FOREIGN KEY（3）：`role_permissions_granted_by_fkey` `FOREIGN KEY (granted_by) REFERENCES identities(id)`；`role_permissions_permission_key_fkey` `FOREIGN KEY (permission_key) REFERENCES permissions(key) ON DELETE CASCADE`；`role_permissions_role_id_fkey` `FOREIGN KEY (role_id) REFERENCES roles(id) ON DELETE CASCADE`
- CHECK（0）：无
- NOT NULL 约束对象：3 个（详见上表「可空」列）

**索引（1）**

- `role_permissions_pkey` — `CREATE UNIQUE INDEX role_permissions_pkey ON public.role_permissions USING btree (role_id, permission_key)`

<a id="roles"></a>

### `roles`

| 属性 | 值 |
|---|---|
| 中文用途 | 角色定义：按组织隔离的角色及内置标记。 |
| 数据所有者领域 | `iam` — 依据：`grep -rl -w roles backend/internal` → api(7) iam(2) casework(2) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0041_rbac.sql:19` |
| 行数（精确 `count(*)`） | **9** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **56.0 KiB**（heap 8.0 KiB / index 40.0 KiB / 共 57,344 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `organization_id` | uuid | 可空 | `—` |
| 3 | `key` | text | NOT NULL | `—` |
| 4 | `name` | text | NOT NULL | `—` |
| 5 | `description` | text | NOT NULL | `''::text` |
| 6 | `is_system` | boolean | NOT NULL | `false` |
| 7 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 8 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`roles_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（1）：`roles_organization_id_fkey` `FOREIGN KEY (organization_id) REFERENCES organizations(id) ON DELETE CASCADE`
- CHECK（0）：无
- NOT NULL 约束对象：7 个（详见上表「可空」列）

**索引（3）**

- `roles_org_key_uidx` — `CREATE UNIQUE INDEX roles_org_key_uidx ON public.roles USING btree (organization_id, key) WHERE (organization_id IS NOT NULL)`
- `roles_pkey` — `CREATE UNIQUE INDEX roles_pkey ON public.roles USING btree (id)`
- `roles_system_key_uidx` — `CREATE UNIQUE INDEX roles_system_key_uidx ON public.roles USING btree (key) WHERE is_system`

<a id="routing-policies"></a>

### `routing_policies`

| 属性 | 值 |
|---|---|
| 中文用途 | 路由策略：Provider 路由规则（按链/数据集/成本权重）。 |
| 数据所有者领域 | `policy` — 依据：命中 `backend/internal/api/sprint6_governance.go`、`sprint6_provider.go` |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0051_s6_control_governance.sql:218` |
| 行数（精确 `count(*)`） | **4** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **48.0 KiB**（heap 8.0 KiB / index 32.0 KiB / 共 49,152 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `policy_key` | text | NOT NULL | `—` |
| 3 | `description` | text | NOT NULL | `''::text` |
| 4 | `chain` | text | NOT NULL | `''::text` |
| 5 | `dataset` | text | NOT NULL | `''::text` |
| 6 | `enabled` | boolean | NOT NULL | `true` |
| 7 | `primary_provider` | text | NOT NULL | `''::text` |
| 8 | `fallback_providers` | ARRAY | NOT NULL | `'{}'::text[]` |
| 9 | `max_concurrency` | integer(32) | NOT NULL | `0` |
| 10 | `rate_limit_per_minute` | integer(32) | NOT NULL | `0` |
| 11 | `circuit_error_rate` | double precision(53) | NOT NULL | `0` |
| 12 | `circuit_p95_ms` | integer(32) | NOT NULL | `0` |
| 13 | `circuit_cooldown_seconds` | integer(32) | NOT NULL | `60` |
| 14 | `rollout_percent` | integer(32) | NOT NULL | `100` |
| 15 | `revision` | bigint(64) | NOT NULL | `1` |
| 16 | `updated_by` | uuid | 可空 | `—` |
| 17 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 18 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`routing_policies_pkey` `PRIMARY KEY (id)`
- UNIQUE（1）：`routing_policies_policy_key_key` `UNIQUE (policy_key)`
- FOREIGN KEY（1）：`routing_policies_updated_by_fkey` `FOREIGN KEY (updated_by) REFERENCES identities(id)`
- CHECK（1）：`routing_policies_rollout_percent_check` `CHECK (((rollout_percent >= 0) AND (rollout_percent <= 100)))`
- NOT NULL 约束对象：17 个（详见上表「可空」列）

**索引（2）**

- `routing_policies_pkey` — `CREATE UNIQUE INDEX routing_policies_pkey ON public.routing_policies USING btree (id)`
- `routing_policies_policy_key_key` — `CREATE UNIQUE INDEX routing_policies_policy_key_key ON public.routing_policies USING btree (policy_key)`

<a id="routing-policy-revisions"></a>

### `routing_policy_revisions`

| 属性 | 值 |
|---|---|
| 中文用途 | 路由策略版本历史：变更前后快照与操作者。 |
| 数据所有者领域 | `policy` — 依据：命中 `backend/internal/api/sprint6_provider.go` |
| 敏感级别 | **confidential** — 依据：含 PII 字段：actor_email |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0051_s6_control_governance.sql:239` |
| 行数（精确 `count(*)`） | **0** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **24.0 KiB**（heap 0 B / index 16.0 KiB / 共 24,576 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `policy_key` | text | NOT NULL | `—` |
| 3 | `revision` | bigint(64) | NOT NULL | `—` |
| 4 | `before` | jsonb | NOT NULL | `'{}'::jsonb` |
| 5 | `after` | jsonb | NOT NULL | `'{}'::jsonb` |
| 6 | `change_note` | text | NOT NULL | `''::text` |
| 7 | `actor_id` | uuid | 可空 | `—` |
| 8 | `actor_email` | text | NOT NULL | `''::text` |
| 9 | `approval_request_id` | uuid | 可空 | `—` |
| 10 | `created_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`routing_policy_revisions_pkey` `PRIMARY KEY (id)`
- UNIQUE（1）：`routing_policy_revisions_policy_key_revision_key` `UNIQUE (policy_key, revision)`
- FOREIGN KEY（2）：`routing_policy_revisions_actor_id_fkey` `FOREIGN KEY (actor_id) REFERENCES identities(id)`；`routing_policy_revisions_approval_request_id_fkey` `FOREIGN KEY (approval_request_id) REFERENCES approval_requests(id)`
- CHECK（0）：无
- NOT NULL 约束对象：8 个（详见上表「可空」列）

**索引（2）**

- `routing_policy_revisions_pkey` — `CREATE UNIQUE INDEX routing_policy_revisions_pkey ON public.routing_policy_revisions USING btree (id)`
- `routing_policy_revisions_policy_key_revision_key` — `CREATE UNIQUE INDEX routing_policy_revisions_policy_key_revision_key ON public.routing_policy_revisions USING btree (policy_key, revision)`

<a id="rpc-provider-capabilities"></a>

### `rpc_provider_capabilities`

| 属性 | 值 |
|---|---|
| 中文用途 | RPC Provider 能力探测：能力项、检查时间与过期时间。 |
| 数据所有者领域 | `store` — 依据：仅命中通用包 → store(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0010_rpc_infrastructure.sql:28` |
| 行数（精确 `count(*)`） | **98** |
| 行数（`pg_class.reltuples` 估算） | 98 |
| 大小 | **96.0 KiB**（heap 40.0 KiB / index 16.0 KiB / 共 98,304 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `provider` | character varying(64) | NOT NULL | `—` |
| 2 | `chain` | character varying(32) | NOT NULL | `—` |
| 3 | `capability` | character varying(32) | NOT NULL | `—` |
| 4 | `supported` | boolean | NOT NULL | `—` |
| 5 | `source` | character varying(16) | NOT NULL | `'static'::character varying` |
| 6 | `detail` | jsonb | NOT NULL | `'{}'::jsonb` |
| 7 | `checked_at` | timestamp with time zone | NOT NULL | `now()` |
| 8 | `expires_at` | timestamp with time zone | 可空 | `—` |

**约束**

- PRIMARY KEY（1）：`rpc_provider_capabilities_pkey` `PRIMARY KEY (provider, chain, capability)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：7 个（详见上表「可空」列）

**索引（1）**

- `rpc_provider_capabilities_pkey` — `CREATE UNIQUE INDEX rpc_provider_capabilities_pkey ON public.rpc_provider_capabilities USING btree (provider, chain, capability)`

<a id="rpc-provider-chains"></a>

### `rpc_provider_chains`

| 属性 | 值 |
|---|---|
| 中文用途 | RPC Provider—链绑定关系。 |
| 数据所有者领域 | `provider` — 依据：仅命中 `backend/migrations/postgres/0010_rpc_infrastructure.sql` |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0010_rpc_infrastructure.sql:15` |
| 行数（精确 `count(*)`） | **0** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **16.0 KiB**（heap 0 B / index 8.0 KiB / 共 16,384 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `provider` | character varying(64) | NOT NULL | `—` |
| 2 | `chain` | character varying(32) | NOT NULL | `—` |
| 3 | `enabled` | boolean | NOT NULL | `true` |
| 4 | `endpoint_template` | text | 可空 | `—` |
| 5 | `archive_enabled` | boolean | NOT NULL | `false` |
| 6 | `weight` | integer(32) | NOT NULL | `100` |
| 7 | `priority` | integer(32) | NOT NULL | `100` |
| 8 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`rpc_provider_chains_pkey` `PRIMARY KEY (provider, chain)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：7 个（详见上表「可空」列）

**索引（1）**

- `rpc_provider_chains_pkey` — `CREATE UNIQUE INDEX rpc_provider_chains_pkey ON public.rpc_provider_chains USING btree (provider, chain)`

<a id="rpc-provider-health"></a>

### `rpc_provider_health`

| 属性 | 值 |
|---|---|
| 中文用途 | RPC Provider 健康采样：延迟、成功率与采样时间序列。 |
| 数据所有者领域 | `store` — 依据：仅命中通用包 → store(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0010_rpc_infrastructure.sql:41` |
| 行数（精确 `count(*)`） | **102,542** |
| 行数（`pg_class.reltuples` 估算） | 93,344 |
| 大小 | **28.52 MiB**（heap 17.02 MiB / index 11.45 MiB / 共 29,900,800 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `provider` | character varying(64) | NOT NULL | `—` |
| 3 | `chain` | character varying(32) | NOT NULL | `—` |
| 4 | `state` | character varying(16) | NOT NULL | `—` |
| 5 | `success_rate` | double precision(53) | NOT NULL | `0` |
| 6 | `p50_ms` | double precision(53) | NOT NULL | `0` |
| 7 | `p95_ms` | double precision(53) | NOT NULL | `0` |
| 8 | `p99_ms` | double precision(53) | NOT NULL | `0` |
| 9 | `timeout_rate` | double precision(53) | NOT NULL | `0` |
| 10 | `head_block` | bigint(64) | NOT NULL | `0` |
| 11 | `head_lag` | bigint(64) | NOT NULL | `0` |
| 12 | `in_flight` | integer(32) | NOT NULL | `0` |
| 13 | `requests` | bigint(64) | NOT NULL | `0` |
| 14 | `last_error` | text | 可空 | `—` |
| 15 | `captured_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`rpc_provider_health_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：14 个（详见上表「可空」列）

**索引（2）**

- `idx_rpc_provider_health_recent` — `CREATE INDEX idx_rpc_provider_health_recent ON public.rpc_provider_health USING btree (provider, chain, captured_at DESC)`
- `rpc_provider_health_pkey` — `CREATE UNIQUE INDEX rpc_provider_health_pkey ON public.rpc_provider_health USING btree (id)`

<a id="rpc-providers"></a>

### `rpc_providers`

| 属性 | 值 |
|---|---|
| 中文用途 | RPC Provider 控制面：名称、类型、启停与凭证引用（credential_ref）。 |
| 数据所有者领域 | `provider` — 依据：仅命中 `backend/migrations/postgres/0010_rpc_infrastructure.sql` |
| 敏感级别 | **confidential** — 依据：含凭证/密钥类字段：credential_ref |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0010_rpc_infrastructure.sql:4` |
| 行数（精确 `count(*)`） | **1** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **40.0 KiB**（heap 8.0 KiB / index 32.0 KiB / 共 40,960 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `name` | character varying(64) | NOT NULL | `—` |
| 3 | `provider_type` | character varying(32) | NOT NULL | `'hosted'::character varying` |
| 4 | `enabled` | boolean | NOT NULL | `true` |
| 5 | `priority` | integer(32) | NOT NULL | `100` |
| 6 | `credential_ref` | character varying(128) | 可空 | `—` |
| 7 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 8 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`rpc_providers_pkey` `PRIMARY KEY (id)`
- UNIQUE（1）：`rpc_providers_name_key` `UNIQUE (name)`
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：7 个（详见上表「可空」列）

**索引（2）**

- `rpc_providers_name_key` — `CREATE UNIQUE INDEX rpc_providers_name_key ON public.rpc_providers USING btree (name)`
- `rpc_providers_pkey` — `CREATE UNIQUE INDEX rpc_providers_pkey ON public.rpc_providers USING btree (id)`

<a id="rpc-usage-rollups"></a>

### `rpc_usage_rollups`

| 属性 | 值 |
|---|---|
| 中文用途 | RPC 用量按日汇总（day 维度）。 |
| 数据所有者领域 | `provider` — 依据：仅命中 `backend/migrations/postgres/0010_rpc_infrastructure.sql` |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0010_rpc_infrastructure.sql:77` |
| 行数（精确 `count(*)`） | **0** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **8.0 KiB**（heap 0 B / index 8.0 KiB / 共 8,192 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `day` | date | NOT NULL | `—` |
| 2 | `provider` | character varying(64) | NOT NULL | `—` |
| 3 | `chain` | character varying(32) | NOT NULL | `—` |
| 4 | `method_class` | character varying(32) | NOT NULL | `—` |
| 5 | `usage_type` | character varying(32) | NOT NULL | `—` |
| 6 | `http_requests` | bigint(64) | NOT NULL | `0` |
| 7 | `method_calls` | bigint(64) | NOT NULL | `0` |
| 8 | `cache_hits` | bigint(64) | NOT NULL | `0` |
| 9 | `errors` | bigint(64) | NOT NULL | `0` |
| 10 | `credits` | double precision(53) | NOT NULL | `0` |
| 11 | `cost_usd` | double precision(53) | NOT NULL | `0` |
| 12 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`rpc_usage_rollups_pkey` `PRIMARY KEY (day, provider, chain, method_class, usage_type)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：12 个（详见上表「可空」列）

**索引（1）**

- `rpc_usage_rollups_pkey` — `CREATE UNIQUE INDEX rpc_usage_rollups_pkey ON public.rpc_usage_rollups USING btree (day, provider, chain, method_class, usage_type)`

<a id="schema-migrations"></a>

### `schema_migrations`

| 属性 | 值 |
|---|---|
| 中文用途 | 迁移版本表：已应用的迁移文件与时间（由迁移框架写入）。 |
| 数据所有者领域 | `platform(迁移框架)` — 依据：命中 `backend/migrations/migrations.go`、`backend/internal/api/sprint6_governance.go` |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `未在 backend/migrations/postgres 找到 CREATE TABLE（由迁移框架/扩展或 ALTER 后续创建）` |
| 行数（精确 `count(*)`） | **57** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **32.0 KiB**（heap 8.0 KiB / index 16.0 KiB / 共 32,768 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `name` | text | NOT NULL | `—` |
| 2 | `applied_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`schema_migrations_pkey` `PRIMARY KEY (name)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：2 个（详见上表「可空」列）

**索引（1）**

- `schema_migrations_pkey` — `CREATE UNIQUE INDEX schema_migrations_pkey ON public.schema_migrations USING btree (name)`

<a id="schema-registry"></a>

### `schema_registry`

| 属性 | 值 |
|---|---|
| 中文用途 | Schema 版本注册表：投影/事件 schema 的版本与描述。 |
| 数据所有者领域 | `investigation` — 依据：`grep -rl -w schema_registry backend/internal` → investigation(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0028_investigation_phase0.sql:50` |
| 行数（精确 `count(*)`） | **10** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **32.0 KiB**（heap 8.0 KiB / index 16.0 KiB / 共 32,768 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `schema_name` | character varying(128) | NOT NULL | `—` |
| 2 | `schema_version` | character varying(32) | NOT NULL | `—` |
| 3 | `compatibility` | character varying(16) | NOT NULL | `'backward'::character varying` |
| 4 | `definition` | jsonb | NOT NULL | `'{}'::jsonb` |
| 5 | `active` | boolean | NOT NULL | `true` |
| 6 | `created_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`schema_registry_pkey` `PRIMARY KEY (schema_name, schema_version)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：6 个（详见上表「可空」列）

**索引（1）**

- `schema_registry_pkey` — `CREATE UNIQUE INDEX schema_registry_pkey ON public.schema_registry USING btree (schema_name, schema_version)`

<a id="semantic-events"></a>

### `semantic_events`

| 属性 | 值 |
|---|---|
| 中文用途 | 语义事件：从链上/调查数据投影出的语义层事件。 |
| 数据所有者领域 | `semantic` — 依据：`grep -rl -w semantic_events backend/internal` → semantic(1) investigation(1) api(1) |
| 敏感级别 | **internal** — 依据：业务数据（含链上标识） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0031_semantic_engine.sql:1` |
| 行数（精确 `count(*)`） | **0** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **32.0 KiB**（heap 0 B / index 24.0 KiB / 共 32,768 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `chain` | text | NOT NULL | `—` |
| 3 | `subject` | text | NOT NULL | `—` |
| 4 | `tx_hash` | text | NOT NULL | `—` |
| 5 | `event_type` | text | NOT NULL | `—` |
| 6 | `confidence` | double precision(53) | NOT NULL | `—` |
| 7 | `payload` | jsonb | NOT NULL | `'{}'::jsonb` |
| 8 | `provenance` | jsonb | NOT NULL | `'{}'::jsonb` |
| 9 | `projection_version` | text | NOT NULL | `—` |
| 10 | `algorithm_version` | text | NOT NULL | `—` |
| 11 | `data_version` | text | NOT NULL | `—` |
| 12 | `occurred_at` | timestamp with time zone | 可空 | `—` |
| 13 | `computed_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`semantic_events_pkey` `PRIMARY KEY (id)`
- UNIQUE（1）：`semantic_events_chain_subject_tx_hash_event_type_projection_key` `UNIQUE (chain, subject, tx_hash, event_type, projection_version)`
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：12 个（详见上表「可空」列）

**索引（3）**

- `idx_semantic_events_subject_time` — `CREATE INDEX idx_semantic_events_subject_time ON public.semantic_events USING btree (chain, subject, occurred_at DESC)`
- `semantic_events_chain_subject_tx_hash_event_type_projection_key` — `CREATE UNIQUE INDEX semantic_events_chain_subject_tx_hash_event_type_projection_key ON public.semantic_events USING btree (chain, subject, tx_hash, event_type, projection_version)`
- `semantic_events_pkey` — `CREATE UNIQUE INDEX semantic_events_pkey ON public.semantic_events USING btree (id)`

<a id="semantic-projection-state"></a>

### `semantic_projection_state`

| 属性 | 值 |
|---|---|
| 中文用途 | 语义投影状态：投影器水位与重算时间。 |
| 数据所有者领域 | `semantic` — 依据：`grep -rl -w semantic_projection_state backend/internal` → semantic(1) investigation(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0032_semantic_projection_state.sql:1` |
| 行数（精确 `count(*)`） | **2** |
| 行数（`pg_class.reltuples` 估算） | 2 |
| 大小 | **80.0 KiB**（heap 8.0 KiB / index 32.0 KiB / 共 81,920 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `chain` | text | NOT NULL | `—` |
| 2 | `subject` | text | NOT NULL | `—` |
| 3 | `projection_version` | text | NOT NULL | `—` |
| 4 | `algorithm_version` | text | NOT NULL | `—` |
| 5 | `data_version` | text | NOT NULL | `—` |
| 6 | `event_count` | bigint(64) | NOT NULL | `0` |
| 7 | `computed_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`semantic_projection_state_pkey` `PRIMARY KEY (chain, subject, projection_version)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：7 个（详见上表「可空」列）

**索引（2）**

- `idx_semantic_projection_state_subject` — `CREATE INDEX idx_semantic_projection_state_subject ON public.semantic_projection_state USING btree (chain, subject, computed_at DESC)`
- `semantic_projection_state_pkey` — `CREATE UNIQUE INDEX semantic_projection_state_pkey ON public.semantic_projection_state USING btree (chain, subject, projection_version)`

<a id="session-reuse-events"></a>

### `session_reuse_events`

| 属性 | 值 |
|---|---|
| 中文用途 | 会话令牌复用检测事件（令牌家族被盗用的取证线索）。 |
| 数据所有者领域 | `iam` — 依据：`grep -rl -w session_reuse_events backend/internal` → iam(1) |
| 敏感级别 | **confidential** — 依据：含 PII 字段：user_agent |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0045_session_family.sql:34` |
| 行数（精确 `count(*)`） | **22** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **80.0 KiB**（heap 8.0 KiB / index 64.0 KiB / 共 81,920 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `occurred_at` | timestamp with time zone | NOT NULL | `now()` |
| 3 | `family_id` | uuid | NOT NULL | `—` |
| 4 | `identity_id` | uuid | NOT NULL | `—` |
| 5 | `organization_id` | uuid | 可空 | `—` |
| 6 | `presented_session_id` | uuid | NOT NULL | `—` |
| 7 | `revoked_at` | timestamp with time zone | 可空 | `—` |
| 8 | `revoked_reason` | text | NOT NULL | `''::text` |
| 9 | `replaced_by` | uuid | 可空 | `—` |
| 10 | `sessions_revoked` | integer(32) | NOT NULL | `0` |
| 11 | `client_ip` | inet | 可空 | `—` |
| 12 | `peer_ip` | inet | 可空 | `—` |
| 13 | `user_agent` | text | NOT NULL | `''::text` |
| 14 | `request_id` | text | NOT NULL | `''::text` |
| 15 | `trace_id` | text | NOT NULL | `''::text` |
| 16 | `detail` | jsonb | NOT NULL | `'{}'::jsonb` |

**约束**

- PRIMARY KEY（1）：`session_reuse_events_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：11 个（详见上表「可空」列）

**索引（4）**

- `session_reuse_events_family_idx` — `CREATE INDEX session_reuse_events_family_idx ON public.session_reuse_events USING btree (family_id, occurred_at DESC)`
- `session_reuse_events_identity_idx` — `CREATE INDEX session_reuse_events_identity_idx ON public.session_reuse_events USING btree (identity_id, occurred_at DESC)`
- `session_reuse_events_pkey` — `CREATE UNIQUE INDEX session_reuse_events_pkey ON public.session_reuse_events USING btree (id)`
- `session_reuse_events_time_idx` — `CREATE INDEX session_reuse_events_time_idx ON public.session_reuse_events USING btree (occurred_at DESC)`

<a id="sessions"></a>

### `sessions`

| 属性 | 值 |
|---|---|
| 中文用途 | 会话：令牌哈希、CSRF 哈希、MFA 满足标记、轮换链与吊销信息。 |
| 数据所有者领域 | `iam` — 依据：`grep -rl -w sessions backend/internal` → api(4) iam(3) |
| 敏感级别 | **confidential** — 依据：含凭证/密钥类字段：token_hash, csrf_hash |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0040_iam.sql:72` |
| 行数（精确 `count(*)`） | **1,757** |
| 行数（`pg_class.reltuples` 估算） | 1,706 |
| 大小 | **2.08 MiB**（heap 1.28 MiB / index 776.0 KiB / 共 2,179,072 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `identity_id` | uuid | NOT NULL | `—` |
| 3 | `organization_id` | uuid | NOT NULL | `—` |
| 4 | `workspace_id` | uuid | 可空 | `—` |
| 5 | `audience` | text | NOT NULL | `—` |
| 6 | `token_hash` | text | NOT NULL | `—` |
| 7 | `csrf_hash` | text | NOT NULL | `—` |
| 8 | `auth_method` | text | NOT NULL | `'password'::text` |
| 9 | `mfa_satisfied` | boolean | NOT NULL | `false` |
| 10 | `device_id` | uuid | 可空 | `—` |
| 11 | `user_agent` | text | NOT NULL | `''::text` |
| 12 | `client_hints` | jsonb | NOT NULL | `'{}'::jsonb` |
| 13 | `device` | jsonb | NOT NULL | `'{}'::jsonb` |
| 14 | `peer_ip` | inet | 可空 | `—` |
| 15 | `client_ip` | inet | 可空 | `—` |
| 16 | `proxy_chain` | ARRAY | NOT NULL | `'{}'::text[]` |
| 17 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 18 | `last_seen_at` | timestamp with time zone | NOT NULL | `now()` |
| 19 | `expires_at` | timestamp with time zone | NOT NULL | `—` |
| 20 | `absolute_expires_at` | timestamp with time zone | NOT NULL | `—` |
| 21 | `revoked_at` | timestamp with time zone | 可空 | `—` |
| 22 | `revoked_reason` | text | NOT NULL | `''::text` |
| 23 | `revoked_by` | uuid | 可空 | `—` |
| 24 | `family_id` | uuid | NOT NULL | `—` |
| 25 | `parent_id` | uuid | 可空 | `—` |
| 26 | `replaced_by` | uuid | 可空 | `—` |
| 27 | `rotated_at` | timestamp with time zone | 可空 | `—` |
| 28 | `rotation_count` | integer(32) | NOT NULL | `0` |

**约束**

- PRIMARY KEY（1）：`sessions_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（7）：`sessions_device_id_fkey` `FOREIGN KEY (device_id) REFERENCES trusted_devices(id) ON DELETE SET NULL`；`sessions_identity_id_fkey` `FOREIGN KEY (identity_id) REFERENCES identities(id) ON DELETE CASCADE`；`sessions_organization_id_fkey` `FOREIGN KEY (organization_id) REFERENCES organizations(id)`；`sessions_parent_id_fkey` `FOREIGN KEY (parent_id) REFERENCES sessions(id) ON DELETE SET NULL`；`sessions_replaced_by_fkey` `FOREIGN KEY (replaced_by) REFERENCES sessions(id) ON DELETE SET NULL`；`sessions_revoked_by_fkey` `FOREIGN KEY (revoked_by) REFERENCES identities(id) ON DELETE SET NULL`；`sessions_workspace_id_fkey` `FOREIGN KEY (workspace_id) REFERENCES workspaces(id)`
- CHECK（1）：`sessions_audience_check` `CHECK ((audience = ANY (ARRAY['user'::text, 'admin'::text])))`
- NOT NULL 约束对象：19 个（详见上表「可空」列）

**索引（7）**

- `idx_sessions_org_last_seen` — `CREATE INDEX idx_sessions_org_last_seen ON public.sessions USING btree (organization_id, last_seen_at DESC)`
- `sessions_active_idx` — `CREATE INDEX sessions_active_idx ON public.sessions USING btree (expires_at) WHERE (revoked_at IS NULL)`
- `sessions_family_idx` — `CREATE INDEX sessions_family_idx ON public.sessions USING btree (family_id, created_at DESC)`
- `sessions_identity_idx` — `CREATE INDEX sessions_identity_idx ON public.sessions USING btree (identity_id, audience, last_seen_at DESC)`
- `sessions_parent_idx` — `CREATE INDEX sessions_parent_idx ON public.sessions USING btree (parent_id) WHERE (parent_id IS NOT NULL)`
- `sessions_pkey` — `CREATE UNIQUE INDEX sessions_pkey ON public.sessions USING btree (id)`
- `sessions_token_uidx` — `CREATE UNIQUE INDEX sessions_token_uidx ON public.sessions USING btree (token_hash)`

<a id="tags"></a>

### `tags`

| 属性 | 值 |
|---|---|
| 中文用途 | 标签字典：标签键值、类型、颜色与组织/工作区归属。 |
| 数据所有者领域 | `dataset` — 依据：`grep -rl -w tags backend/internal` → api(4) dataset(2) store(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0004_v12_metadata.sql:17` |
| 行数（精确 `count(*)`） | **4** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **48.0 KiB**（heap 8.0 KiB / index 32.0 KiB / 共 49,152 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `organization_id` | uuid | NOT NULL | `—` |
| 3 | `workspace_id` | uuid | NOT NULL | `—` |
| 4 | `name` | character varying(128) | NOT NULL | `—` |
| 5 | `description` | text | 可空 | `—` |
| 6 | `color` | character varying(32) | 可空 | `—` |
| 7 | `source` | character varying(32) | NOT NULL | `'user'::character varying` |
| 8 | `created_by` | uuid | 可空 | `—` |
| 9 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 10 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`tags_pkey` `PRIMARY KEY (id)`
- UNIQUE（1）：`tags_workspace_id_name_key` `UNIQUE (workspace_id, name)`
- FOREIGN KEY（2）：`tags_organization_id_fkey` `FOREIGN KEY (organization_id) REFERENCES organizations(id)`；`tags_workspace_id_fkey` `FOREIGN KEY (workspace_id) REFERENCES workspaces(id)`
- CHECK（0）：无
- NOT NULL 约束对象：7 个（详见上表「可空」列）

**索引（2）**

- `tags_pkey` — `CREATE UNIQUE INDEX tags_pkey ON public.tags USING btree (id)`
- `tags_workspace_id_name_key` — `CREATE UNIQUE INDEX tags_workspace_id_name_key ON public.tags USING btree (workspace_id, name)`

<a id="token-contract-catalog"></a>

### `token_contract_catalog`

| 属性 | 值 |
|---|---|
| 中文用途 | Token 合约目录：首次/最近出现时间与来源。 |
| 数据所有者领域 | `tokenmeta` — 依据：`grep -rl -w token_contract_catalog backend/internal` → tokenmeta(2) |
| 敏感级别 | **internal** — 依据：业务数据（含链上标识） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0006_token_metadata.sql:57` |
| 行数（精确 `count(*)`） | **17,108** |
| 行数（`pg_class.reltuples` 估算） | 17,108 |
| 大小 | **6.19 MiB**（heap 2.66 MiB / index 3.50 MiB / 共 6,488,064 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `chain` | character varying(32) | NOT NULL | `—` |
| 2 | `normalized_address` | character varying(128) | NOT NULL | `—` |
| 3 | `first_seen_block` | bigint(64) | 可空 | `—` |
| 4 | `last_seen_block` | bigint(64) | 可空 | `—` |
| 5 | `first_seen_at` | timestamp with time zone | 可空 | `—` |
| 6 | `last_seen_at` | timestamp with time zone | 可空 | `—` |
| 7 | `transfer_count` | bigint(64) | NOT NULL | `0` |
| 8 | `metadata_status` | character varying(32) | NOT NULL | `'unknown'::character varying` |
| 9 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 10 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`token_contract_catalog_pkey` `PRIMARY KEY (chain, normalized_address)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（1）：`token_contract_catalog_valid_address` `CHECK (((normalized_address)::text ~ '^0x[0-9a-f]{40}$'::text))`
- NOT NULL 约束对象：6 个（详见上表「可空」列）

**索引（2）**

- `idx_token_catalog_status` — `CREATE INDEX idx_token_catalog_status ON public.token_contract_catalog USING btree (metadata_status, chain, normalized_address)`
- `token_contract_catalog_pkey` — `CREATE UNIQUE INDEX token_contract_catalog_pkey ON public.token_contract_catalog USING btree (chain, normalized_address)`

<a id="token-metadata"></a>

### `token_metadata`

| 属性 | 值 |
|---|---|
| 中文用途 | Token 元数据主档：名称/符号/精度/logo 对象键/置信度与解析状态。 |
| 数据所有者领域 | `tokenmeta` — 依据：`grep -rl -w token_metadata backend/internal` → tokenmeta(2) store(1) provider(1) |
| 敏感级别 | **internal** — 依据：业务数据（含链上标识） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0004_v12_metadata.sql:78` |
| 行数（精确 `count(*)`） | **17,594** |
| 行数（`pg_class.reltuples` 估算） | 17,574 |
| 大小 | **8.13 MiB**（heap 4.80 MiB / index 3.29 MiB / 共 8,519,680 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `chain` | character varying(32) | NOT NULL | `—` |
| 2 | `contract_address` | character varying(128) | NOT NULL | `—` |
| 3 | `name` | character varying(255) | 可空 | `—` |
| 4 | `symbol` | character varying(128) | 可空 | `—` |
| 5 | `decimals` | integer(32) | 可空 | `—` |
| 6 | `logo_url` | text | 可空 | `—` |
| 7 | `token_type` | character varying(32) | 可空 | `—` |
| 8 | `verified` | boolean | NOT NULL | `false` |
| 9 | `metadata_source` | character varying(32) | 可空 | `'ankr'::character varying` |
| 10 | `last_verified_at` | timestamp with time zone | 可空 | `—` |
| 11 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |
| 12 | `normalized_address` | character varying(128) | NOT NULL | `—` |
| 13 | `logo_object_key` | text | 可空 | `—` |
| 14 | `logo_mime_type` | character varying(128) | 可空 | `—` |
| 15 | `confidence` | character varying(32) | NOT NULL | `'unknown'::character varying` |
| 16 | `metadata_status` | character varying(32) | NOT NULL | `'unknown'::character varying` |
| 17 | `first_seen_block` | bigint(64) | 可空 | `—` |
| 18 | `last_seen_block` | bigint(64) | 可空 | `—` |
| 19 | `first_seen_at` | timestamp with time zone | 可空 | `—` |
| 20 | `last_seen_at` | timestamp with time zone | 可空 | `—` |
| 21 | `last_resolved_at` | timestamp with time zone | 可空 | `—` |
| 22 | `created_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`token_metadata_pkey` `PRIMARY KEY (chain, contract_address)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（1）：`token_metadata_valid_address` `CHECK (((((chain)::text = 'tron'::text) AND ((normalized_address)::text ~ '^T[1-9A-HJ-NP-Za-km-z]{33}$'::text)) OR (((chain)::text <> 'tron'::text) AND ((normalized_address)::text ~ '^0x[0-9a-f]{40}$'::text))))`
- NOT NULL 约束对象：8 个（详见上表「可空」列）

**索引（2）**

- `token_metadata_pkey` — `CREATE UNIQUE INDEX token_metadata_pkey ON public.token_metadata USING btree (chain, contract_address)`
- `uq_token_metadata_normalized` — `CREATE UNIQUE INDEX uq_token_metadata_normalized ON public.token_metadata USING btree (chain, normalized_address)`

<a id="token-metadata-backfill-jobs"></a>

### `token_metadata_backfill_jobs`

| 属性 | 值 |
|---|---|
| 中文用途 | Token 元数据回填作业：进度、检查点与完成时间。 |
| 数据所有者领域 | `tokenmeta` — 依据：`grep -rl -w token_metadata_backfill_jobs backend/internal` → tokenmeta(1) api(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0006_token_metadata.sql:86` |
| 行数（精确 `count(*)`） | **6** |
| 行数（`pg_class.reltuples` 估算） | 6 |
| 大小 | **80.0 KiB**（heap 8.0 KiB / index 32.0 KiB / 共 81,920 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `chain` | character varying(32) | 可空 | `—` |
| 3 | `status` | character varying(32) | NOT NULL | `—` |
| 4 | `cursor_json` | jsonb | NOT NULL | `'{}'::jsonb` |
| 5 | `scanned_tokens` | bigint(64) | NOT NULL | `0` |
| 6 | `completed_tokens` | bigint(64) | NOT NULL | `0` |
| 7 | `partial_tokens` | bigint(64) | NOT NULL | `0` |
| 8 | `failed_tokens` | bigint(64) | NOT NULL | `0` |
| 9 | `started_at` | timestamp with time zone | 可空 | `—` |
| 10 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |
| 11 | `completed_at` | timestamp with time zone | 可空 | `—` |
| 12 | `last_error` | text | 可空 | `—` |
| 13 | `raw_objects` | bigint(64) | NOT NULL | `0` |
| 14 | `raw_observations` | bigint(64) | NOT NULL | `0` |
| 15 | `raw_failed_objects` | bigint(64) | NOT NULL | `0` |
| 16 | `raw_failed_parts` | bigint(64) | NOT NULL | `0` |

**约束**

- PRIMARY KEY（1）：`token_metadata_backfill_jobs_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：12 个（详见上表「可空」列）

**索引（2）**

- `idx_token_backfill_status` — `CREATE INDEX idx_token_backfill_status ON public.token_metadata_backfill_jobs USING btree (status, updated_at)`
- `token_metadata_backfill_jobs_pkey` — `CREATE UNIQUE INDEX token_metadata_backfill_jobs_pkey ON public.token_metadata_backfill_jobs USING btree (id)`

<a id="token-metadata-observations"></a>

### `token_metadata_observations`

| 属性 | 值 |
|---|---|
| 中文用途 | Token 元数据原始观察：每次来源抓取的原始载荷哈希与观察时间。 |
| 数据所有者领域 | `tokenmeta` — 依据：`grep -rl -w token_metadata_observations backend/internal` → tokenmeta(3) |
| 敏感级别 | **internal** — 依据：业务数据（含链上标识） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0006_token_metadata.sql:18` |
| 行数（精确 `count(*)`） | **233,792** |
| 行数（`pg_class.reltuples` 估算） | 231,152 |
| 大小 | **149.66 MiB**（heap 66.05 MiB / index 83.55 MiB / 共 156,925,952 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `chain` | character varying(32) | NOT NULL | `—` |
| 3 | `contract_address` | character varying(128) | NOT NULL | `—` |
| 4 | `normalized_address` | character varying(128) | NOT NULL | `—` |
| 5 | `name` | character varying(255) | 可空 | `—` |
| 6 | `symbol` | character varying(128) | 可空 | `—` |
| 7 | `decimals` | integer(32) | 可空 | `—` |
| 8 | `token_type` | character varying(32) | 可空 | `—` |
| 9 | `logo_url` | text | 可空 | `—` |
| 10 | `source` | character varying(32) | NOT NULL | `—` |
| 11 | `source_ref` | character varying(255) | 可空 | `—` |
| 12 | `block_number` | bigint(64) | 可空 | `—` |
| 13 | `observed_at` | timestamp with time zone | NOT NULL | `now()` |
| 14 | `raw_hash` | character varying(128) | 可空 | `—` |
| 15 | `valid` | boolean | NOT NULL | `true` |
| 16 | `validation_error` | text | 可空 | `—` |
| 17 | `created_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`token_metadata_observations_pkey` `PRIMARY KEY (id)`
- UNIQUE（1）：`token_metadata_observations_chain_normalized_address_source_key` `UNIQUE (chain, normalized_address, source, raw_hash)`
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：8 个（详见上表「可空」列）

**索引（3）**

- `idx_token_metadata_obs_lookup` — `CREATE INDEX idx_token_metadata_obs_lookup ON public.token_metadata_observations USING btree (chain, normalized_address, observed_at DESC)`
- `token_metadata_observations_chain_normalized_address_source_key` — `CREATE UNIQUE INDEX token_metadata_observations_chain_normalized_address_source_key ON public.token_metadata_observations USING btree (chain, normalized_address, source, raw_hash)`
- `token_metadata_observations_pkey` — `CREATE UNIQUE INDEX token_metadata_observations_pkey ON public.token_metadata_observations USING btree (id)`

<a id="token-metadata-overrides"></a>

### `token_metadata_overrides`

| 属性 | 值 |
|---|---|
| 中文用途 | Token 元数据人工覆盖：管理员修正值与理由。 |
| 数据所有者领域 | `tokenmeta` — 依据：仅命中 `backend/migrations/postgres/0006_token_metadata.sql`、`0010_token_metadata_rpc_safety.sql` |
| 敏感级别 | **internal** — 依据：业务数据（含链上标识） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0006_token_metadata.sql:41` |
| 行数（精确 `count(*)`） | **0** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **24.0 KiB**（heap 0 B / index 16.0 KiB / 共 24,576 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `chain` | character varying(32) | NOT NULL | `—` |
| 3 | `normalized_address` | character varying(128) | NOT NULL | `—` |
| 4 | `name` | character varying(255) | 可空 | `—` |
| 5 | `symbol` | character varying(128) | 可空 | `—` |
| 6 | `decimals` | integer(32) | 可空 | `—` |
| 7 | `token_type` | character varying(32) | 可空 | `—` |
| 8 | `logo_object_key` | text | 可空 | `—` |
| 9 | `reason` | text | 可空 | `—` |
| 10 | `created_by` | uuid | 可空 | `—` |
| 11 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 12 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`token_metadata_overrides_pkey` `PRIMARY KEY (id)`
- UNIQUE（1）：`token_metadata_overrides_chain_normalized_address_key` `UNIQUE (chain, normalized_address)`
- FOREIGN KEY（0）：无
- CHECK（0）：无
- NOT NULL 约束对象：5 个（详见上表「可空」列）

**索引（2）**

- `token_metadata_overrides_chain_normalized_address_key` — `CREATE UNIQUE INDEX token_metadata_overrides_chain_normalized_address_key ON public.token_metadata_overrides USING btree (chain, normalized_address)`
- `token_metadata_overrides_pkey` — `CREATE UNIQUE INDEX token_metadata_overrides_pkey ON public.token_metadata_overrides USING btree (id)`

<a id="token-metadata-resolve-queue"></a>

### `token_metadata_resolve_queue`

| 属性 | 值 |
|---|---|
| 中文用途 | Token 元数据解析队列：重试次数、下次重试时间与去重键。 |
| 数据所有者领域 | `tokenmeta` — 依据：`grep -rl -w token_metadata_resolve_queue backend/internal` → tokenmeta(2) |
| 敏感级别 | **internal** — 依据：业务数据（含链上标识） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0006_token_metadata.sql:72` |
| 行数（精确 `count(*)`） | **2,854** |
| 行数（`pg_class.reltuples` 估算） | 2,850 |
| 大小 | **10.80 MiB**（heap 4.36 MiB / index 6.40 MiB / 共 11,321,344 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `chain` | character varying(32) | NOT NULL | `—` |
| 2 | `normalized_address` | character varying(128) | NOT NULL | `—` |
| 3 | `status` | character varying(32) | NOT NULL | `'pending'::character varying` |
| 4 | `attempts` | integer(32) | NOT NULL | `0` |
| 5 | `next_retry_at` | timestamp with time zone | 可空 | `—` |
| 6 | `last_error` | text | 可空 | `—` |
| 7 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 8 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |
| 9 | `priority` | smallint(16) | NOT NULL | `0` |

**约束**

- PRIMARY KEY（1）：`token_metadata_resolve_queue_pkey` `PRIMARY KEY (chain, normalized_address)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（1）：`token_metadata_resolve_queue_valid_address` `CHECK (((normalized_address)::text ~ '^0x[0-9a-f]{40}$'::text))`
- NOT NULL 约束对象：7 个（详见上表「可空」列）

**索引（3）**

- `idx_token_resolve_priority` — `CREATE INDEX idx_token_resolve_priority ON public.token_metadata_resolve_queue USING btree (status, priority DESC, next_retry_at, updated_at)`
- `idx_token_resolve_runnable` — `CREATE INDEX idx_token_resolve_runnable ON public.token_metadata_resolve_queue USING btree (status, next_retry_at, updated_at)`
- `token_metadata_resolve_queue_pkey` — `CREATE UNIQUE INDEX token_metadata_resolve_queue_pkey ON public.token_metadata_resolve_queue USING btree (chain, normalized_address)`

<a id="trusted-devices"></a>

### `trusted_devices`

| 属性 | 值 |
|---|---|
| 中文用途 | 受信任设备：设备指纹、信任有效期与吊销状态。 |
| 数据所有者领域 | `iam` — 依据：`grep -rl -w trusted_devices backend/internal` → iam(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0040_iam.sql:56` |
| 行数（精确 `count(*)`） | **33** |
| 行数（`pg_class.reltuples` 估算） | 33 |
| 大小 | **80.0 KiB**（heap 8.0 KiB / index 32.0 KiB / 共 81,920 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `identity_id` | uuid | NOT NULL | `—` |
| 3 | `device_label` | text | NOT NULL | `''::text` |
| 4 | `platform` | text | NOT NULL | `''::text` |
| 5 | `browser` | text | NOT NULL | `''::text` |
| 6 | `first_seen_at` | timestamp with time zone | NOT NULL | `now()` |
| 7 | `last_seen_at` | timestamp with time zone | NOT NULL | `now()` |
| 8 | `trusted_until` | timestamp with time zone | 可空 | `—` |
| 9 | `revoked_at` | timestamp with time zone | 可空 | `—` |
| 10 | `created_ip` | inet | 可空 | `—` |
| 11 | `last_ip` | inet | 可空 | `—` |

**约束**

- PRIMARY KEY（1）：`trusted_devices_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（1）：`trusted_devices_identity_id_fkey` `FOREIGN KEY (identity_id) REFERENCES identities(id) ON DELETE CASCADE`
- CHECK（0）：无
- NOT NULL 约束对象：7 个（详见上表「可空」列）

**索引（2）**

- `trusted_devices_identity_idx` — `CREATE INDEX trusted_devices_identity_idx ON public.trusted_devices USING btree (identity_id, last_seen_at DESC)`
- `trusted_devices_pkey` — `CREATE UNIQUE INDEX trusted_devices_pkey ON public.trusted_devices USING btree (id)`

<a id="update-previews"></a>

### `update_previews`

| 属性 | 值 |
|---|---|
| 中文用途 | 地址更新预览：增量更新前的差异预览。 |
| 数据所有者领域 | `store` — 依据：仅命中通用包 → store(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0004_v12_metadata.sql:93` |
| 行数（精确 `count(*)`） | **4** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **32.0 KiB**（heap 8.0 KiB / index 16.0 KiB / 共 32,768 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `organization_id` | uuid | NOT NULL | `—` |
| 3 | `workspace_id` | uuid | NOT NULL | `—` |
| 4 | `created_by` | uuid | 可空 | `—` |
| 5 | `chain` | character varying(32) | NOT NULL | `—` |
| 6 | `target_block` | bigint(64) | NOT NULL | `—` |
| 7 | `request_json` | jsonb | NOT NULL | `—` |
| 8 | `result_json` | jsonb | NOT NULL | `—` |
| 9 | `expires_at` | timestamp with time zone | NOT NULL | `—` |
| 10 | `created_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`update_previews_pkey` `PRIMARY KEY (id)`
- UNIQUE（0）：无
- FOREIGN KEY（2）：`update_previews_organization_id_fkey` `FOREIGN KEY (organization_id) REFERENCES organizations(id)`；`update_previews_workspace_id_fkey` `FOREIGN KEY (workspace_id) REFERENCES workspaces(id)`
- CHECK（0）：无
- NOT NULL 约束对象：9 个（详见上表「可空」列）

**索引（1）**

- `update_previews_pkey` — `CREATE UNIQUE INDEX update_previews_pkey ON public.update_previews USING btree (id)`

<a id="user-activity-events"></a>

### `user_activity_events`

| 属性 | 值 |
|---|---|
| 中文用途 | 用户高频行为事件（按 occurred_at 月分区父表）：用户操作遥测，90 天归档策略目标。 |
| 数据所有者领域 | `api` — 依据：仅命中通用包 → api(1) |
| 敏感级别 | **restricted** — 依据：用户行为遥测（append-only 授权，仅 SELECT/INSERT） |
| 保留期 | 见 `retention_policies.user-activity-default`（基线默认，当前 enabled=false） — 依据：`backend/migrations/postgres/0052_s6_data_governance.sql:185-192` |
| 建表迁移 | `backend/migrations/postgres/0057_s7_indexes_partitions.sql:269` |
| 行数（精确 `count(*)`） | **0** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **0 B**（heap 0 B / index 0 B / 共 0 B） |
| 分区 | **是（分区父表，`PARTITION BY RANGE`）** |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `occurred_at` | timestamp with time zone | NOT NULL | `now()` |
| 3 | `identity_id` | uuid | 可空 | `—` |
| 4 | `organization_id` | uuid | 可空 | `—` |
| 5 | `workspace_id` | uuid | 可空 | `—` |
| 6 | `session_id` | uuid | 可空 | `—` |
| 7 | `category` | text | NOT NULL | `—` |
| 8 | `action` | text | NOT NULL | `—` |
| 9 | `route` | text | NOT NULL | `''::text` |
| 10 | `object_type` | text | NOT NULL | `''::text` |
| 11 | `object_id` | text | NOT NULL | `''::text` |
| 12 | `params_digest` | text | NOT NULL | `''::text` |
| 13 | `result_count` | bigint(64) | NOT NULL | `0` |
| 14 | `duration_ms` | integer(32) | NOT NULL | `0` |
| 15 | `data_source` | text | NOT NULL | `''::text` |
| 16 | `detail` | jsonb | NOT NULL | `'{}'::jsonb` |

**约束**

- PRIMARY KEY（1）：`user_activity_events_pkey` `PRIMARY KEY (id, occurred_at)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（1）：`user_activity_events_s7_scope_coherence_check` `CHECK (((workspace_id IS NULL) OR (organization_id IS NOT NULL)))`
- NOT NULL 约束对象：12 个（详见上表「可空」列）

**索引（4）**

- `user_activity_events_pkey` — `CREATE UNIQUE INDEX user_activity_events_pkey ON ONLY public.user_activity_events USING btree (id, occurred_at)`
- `user_activity_identity_idx` — `CREATE INDEX user_activity_identity_idx ON ONLY public.user_activity_events USING btree (identity_id, occurred_at DESC)`
- `user_activity_org_time_idx` — `CREATE INDEX user_activity_org_time_idx ON ONLY public.user_activity_events USING btree (organization_id, occurred_at DESC)`
- `user_activity_time_idx` — `CREATE INDEX user_activity_time_idx ON ONLY public.user_activity_events USING btree (occurred_at DESC)`

<a id="user-activity-events-202609"></a>

### `user_activity_events_202609`

| 属性 | 值 |
|---|---|
| 中文用途 | 用户行为事件 2026-09 月分区。 |
| 数据所有者领域 | `api(父表分区)` — 依据：分区表：无独立 Go 引用，归属父表 `user_activity_events` 的领域 |
| 敏感级别 | **restricted** — 依据：用户行为遥测（append-only 授权，仅 SELECT/INSERT） |
| 保留期 | 见 `retention_policies.user-activity-default`（基线默认，当前 enabled=false） — 依据：`backend/migrations/postgres/0052_s6_data_governance.sql:185-192` |
| 建表迁移 | `未在 backend/migrations/postgres 找到 CREATE TABLE（由迁移框架/扩展或 ALTER 后续创建）` |
| 行数（精确 `count(*)`） | **0** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **40.0 KiB**（heap 0 B / index 32.0 KiB / 共 40,960 B） |
| 分区 | **是（分区子表，父表 `user_activity_events`，边界 `FOR VALUES FROM ('2026-09-01 00:00:00+00') TO ('2026-10-01 00:00:00+00')`）** |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `occurred_at` | timestamp with time zone | NOT NULL | `now()` |
| 3 | `identity_id` | uuid | 可空 | `—` |
| 4 | `organization_id` | uuid | 可空 | `—` |
| 5 | `workspace_id` | uuid | 可空 | `—` |
| 6 | `session_id` | uuid | 可空 | `—` |
| 7 | `category` | text | NOT NULL | `—` |
| 8 | `action` | text | NOT NULL | `—` |
| 9 | `route` | text | NOT NULL | `''::text` |
| 10 | `object_type` | text | NOT NULL | `''::text` |
| 11 | `object_id` | text | NOT NULL | `''::text` |
| 12 | `params_digest` | text | NOT NULL | `''::text` |
| 13 | `result_count` | bigint(64) | NOT NULL | `0` |
| 14 | `duration_ms` | integer(32) | NOT NULL | `0` |
| 15 | `data_source` | text | NOT NULL | `''::text` |
| 16 | `detail` | jsonb | NOT NULL | `'{}'::jsonb` |

**约束**

- PRIMARY KEY（1）：`user_activity_events_202609_pkey` `PRIMARY KEY (id, occurred_at)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（1）：`user_activity_events_s7_scope_coherence_check` `CHECK (((workspace_id IS NULL) OR (organization_id IS NOT NULL)))`
- NOT NULL 约束对象：12 个（详见上表「可空」列）

**索引（4）**

- `user_activity_events_202609_identity_id_occurred_at_idx` — `CREATE INDEX user_activity_events_202609_identity_id_occurred_at_idx ON public.user_activity_events_202609 USING btree (identity_id, occurred_at DESC)`
- `user_activity_events_202609_occurred_at_idx` — `CREATE INDEX user_activity_events_202609_occurred_at_idx ON public.user_activity_events_202609 USING btree (occurred_at DESC)`
- `user_activity_events_202609_organization_id_occurred_at_idx` — `CREATE INDEX user_activity_events_202609_organization_id_occurred_at_idx ON public.user_activity_events_202609 USING btree (organization_id, occurred_at DESC)`
- `user_activity_events_202609_pkey` — `CREATE UNIQUE INDEX user_activity_events_202609_pkey ON public.user_activity_events_202609 USING btree (id, occurred_at)`

<a id="user-activity-events-202610"></a>

### `user_activity_events_202610`

| 属性 | 值 |
|---|---|
| 中文用途 | 用户行为事件 2026-10 月分区。 |
| 数据所有者领域 | `api(父表分区)` — 依据：分区表：无独立 Go 引用，归属父表 `user_activity_events` 的领域 |
| 敏感级别 | **restricted** — 依据：用户行为遥测（append-only 授权，仅 SELECT/INSERT） |
| 保留期 | 见 `retention_policies.user-activity-default`（基线默认，当前 enabled=false） — 依据：`backend/migrations/postgres/0052_s6_data_governance.sql:185-192` |
| 建表迁移 | `未在 backend/migrations/postgres 找到 CREATE TABLE（由迁移框架/扩展或 ALTER 后续创建）` |
| 行数（精确 `count(*)`） | **0** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **40.0 KiB**（heap 0 B / index 32.0 KiB / 共 40,960 B） |
| 分区 | **是（分区子表，父表 `user_activity_events`，边界 `FOR VALUES FROM ('2026-10-01 00:00:00+00') TO ('2026-11-01 00:00:00+00')`）** |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `occurred_at` | timestamp with time zone | NOT NULL | `now()` |
| 3 | `identity_id` | uuid | 可空 | `—` |
| 4 | `organization_id` | uuid | 可空 | `—` |
| 5 | `workspace_id` | uuid | 可空 | `—` |
| 6 | `session_id` | uuid | 可空 | `—` |
| 7 | `category` | text | NOT NULL | `—` |
| 8 | `action` | text | NOT NULL | `—` |
| 9 | `route` | text | NOT NULL | `''::text` |
| 10 | `object_type` | text | NOT NULL | `''::text` |
| 11 | `object_id` | text | NOT NULL | `''::text` |
| 12 | `params_digest` | text | NOT NULL | `''::text` |
| 13 | `result_count` | bigint(64) | NOT NULL | `0` |
| 14 | `duration_ms` | integer(32) | NOT NULL | `0` |
| 15 | `data_source` | text | NOT NULL | `''::text` |
| 16 | `detail` | jsonb | NOT NULL | `'{}'::jsonb` |

**约束**

- PRIMARY KEY（1）：`user_activity_events_202610_pkey` `PRIMARY KEY (id, occurred_at)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（1）：`user_activity_events_s7_scope_coherence_check` `CHECK (((workspace_id IS NULL) OR (organization_id IS NOT NULL)))`
- NOT NULL 约束对象：12 个（详见上表「可空」列）

**索引（4）**

- `user_activity_events_202610_identity_id_occurred_at_idx` — `CREATE INDEX user_activity_events_202610_identity_id_occurred_at_idx ON public.user_activity_events_202610 USING btree (identity_id, occurred_at DESC)`
- `user_activity_events_202610_occurred_at_idx` — `CREATE INDEX user_activity_events_202610_occurred_at_idx ON public.user_activity_events_202610 USING btree (occurred_at DESC)`
- `user_activity_events_202610_organization_id_occurred_at_idx` — `CREATE INDEX user_activity_events_202610_organization_id_occurred_at_idx ON public.user_activity_events_202610 USING btree (organization_id, occurred_at DESC)`
- `user_activity_events_202610_pkey` — `CREATE UNIQUE INDEX user_activity_events_202610_pkey ON public.user_activity_events_202610 USING btree (id, occurred_at)`

<a id="user-activity-events-202611"></a>

### `user_activity_events_202611`

| 属性 | 值 |
|---|---|
| 中文用途 | 用户行为事件 2026-11 月分区。 |
| 数据所有者领域 | `api(父表分区)` — 依据：分区表：无独立 Go 引用，归属父表 `user_activity_events` 的领域 |
| 敏感级别 | **restricted** — 依据：用户行为遥测（append-only 授权，仅 SELECT/INSERT） |
| 保留期 | 见 `retention_policies.user-activity-default`（基线默认，当前 enabled=false） — 依据：`backend/migrations/postgres/0052_s6_data_governance.sql:185-192` |
| 建表迁移 | `未在 backend/migrations/postgres 找到 CREATE TABLE（由迁移框架/扩展或 ALTER 后续创建）` |
| 行数（精确 `count(*)`） | **0** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **40.0 KiB**（heap 0 B / index 32.0 KiB / 共 40,960 B） |
| 分区 | **是（分区子表，父表 `user_activity_events`，边界 `FOR VALUES FROM ('2026-11-01 00:00:00+00') TO ('2026-12-01 00:00:00+00')`）** |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `occurred_at` | timestamp with time zone | NOT NULL | `now()` |
| 3 | `identity_id` | uuid | 可空 | `—` |
| 4 | `organization_id` | uuid | 可空 | `—` |
| 5 | `workspace_id` | uuid | 可空 | `—` |
| 6 | `session_id` | uuid | 可空 | `—` |
| 7 | `category` | text | NOT NULL | `—` |
| 8 | `action` | text | NOT NULL | `—` |
| 9 | `route` | text | NOT NULL | `''::text` |
| 10 | `object_type` | text | NOT NULL | `''::text` |
| 11 | `object_id` | text | NOT NULL | `''::text` |
| 12 | `params_digest` | text | NOT NULL | `''::text` |
| 13 | `result_count` | bigint(64) | NOT NULL | `0` |
| 14 | `duration_ms` | integer(32) | NOT NULL | `0` |
| 15 | `data_source` | text | NOT NULL | `''::text` |
| 16 | `detail` | jsonb | NOT NULL | `'{}'::jsonb` |

**约束**

- PRIMARY KEY（1）：`user_activity_events_202611_pkey` `PRIMARY KEY (id, occurred_at)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（1）：`user_activity_events_s7_scope_coherence_check` `CHECK (((workspace_id IS NULL) OR (organization_id IS NOT NULL)))`
- NOT NULL 约束对象：12 个（详见上表「可空」列）

**索引（4）**

- `user_activity_events_202611_identity_id_occurred_at_idx` — `CREATE INDEX user_activity_events_202611_identity_id_occurred_at_idx ON public.user_activity_events_202611 USING btree (identity_id, occurred_at DESC)`
- `user_activity_events_202611_occurred_at_idx` — `CREATE INDEX user_activity_events_202611_occurred_at_idx ON public.user_activity_events_202611 USING btree (occurred_at DESC)`
- `user_activity_events_202611_organization_id_occurred_at_idx` — `CREATE INDEX user_activity_events_202611_organization_id_occurred_at_idx ON public.user_activity_events_202611 USING btree (organization_id, occurred_at DESC)`
- `user_activity_events_202611_pkey` — `CREATE UNIQUE INDEX user_activity_events_202611_pkey ON public.user_activity_events_202611 USING btree (id, occurred_at)`

<a id="user-activity-events-default"></a>

### `user_activity_events_default`

| 属性 | 值 |
|---|---|
| 中文用途 | 用户行为事件 DEFAULT 兜底分区（防止漏建分区导致写入失败）。 |
| 数据所有者领域 | `api(父表分区)` — 依据：分区表：无独立 Go 引用，归属父表 `user_activity_events` 的领域 |
| 敏感级别 | **restricted** — 依据：用户行为遥测（append-only 授权，仅 SELECT/INSERT） |
| 保留期 | 见 `retention_policies.user-activity-default`（基线默认，当前 enabled=false） — 依据：`backend/migrations/postgres/0052_s6_data_governance.sql:185-192` |
| 建表迁移 | `backend/migrations/postgres/0057_s7_indexes_partitions.sql:283` |
| 行数（精确 `count(*)`） | **0** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **40.0 KiB**（heap 0 B / index 32.0 KiB / 共 40,960 B） |
| 分区 | **是（分区子表，父表 `user_activity_events`，边界 `DEFAULT`）** |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `occurred_at` | timestamp with time zone | NOT NULL | `now()` |
| 3 | `identity_id` | uuid | 可空 | `—` |
| 4 | `organization_id` | uuid | 可空 | `—` |
| 5 | `workspace_id` | uuid | 可空 | `—` |
| 6 | `session_id` | uuid | 可空 | `—` |
| 7 | `category` | text | NOT NULL | `—` |
| 8 | `action` | text | NOT NULL | `—` |
| 9 | `route` | text | NOT NULL | `''::text` |
| 10 | `object_type` | text | NOT NULL | `''::text` |
| 11 | `object_id` | text | NOT NULL | `''::text` |
| 12 | `params_digest` | text | NOT NULL | `''::text` |
| 13 | `result_count` | bigint(64) | NOT NULL | `0` |
| 14 | `duration_ms` | integer(32) | NOT NULL | `0` |
| 15 | `data_source` | text | NOT NULL | `''::text` |
| 16 | `detail` | jsonb | NOT NULL | `'{}'::jsonb` |

**约束**

- PRIMARY KEY（1）：`user_activity_events_default_pkey` `PRIMARY KEY (id, occurred_at)`
- UNIQUE（0）：无
- FOREIGN KEY（0）：无
- CHECK（1）：`user_activity_events_s7_scope_coherence_check` `CHECK (((workspace_id IS NULL) OR (organization_id IS NOT NULL)))`
- NOT NULL 约束对象：12 个（详见上表「可空」列）

**索引（4）**

- `user_activity_events_default_identity_id_occurred_at_idx` — `CREATE INDEX user_activity_events_default_identity_id_occurred_at_idx ON public.user_activity_events_default USING btree (identity_id, occurred_at DESC)`
- `user_activity_events_default_occurred_at_idx` — `CREATE INDEX user_activity_events_default_occurred_at_idx ON public.user_activity_events_default USING btree (occurred_at DESC)`
- `user_activity_events_default_organization_id_occurred_at_idx` — `CREATE INDEX user_activity_events_default_organization_id_occurred_at_idx ON public.user_activity_events_default USING btree (organization_id, occurred_at DESC)`
- `user_activity_events_default_pkey` — `CREATE UNIQUE INDEX user_activity_events_default_pkey ON public.user_activity_events_default USING btree (id, occurred_at)`

<a id="user-activity-events-hist"></a>

### `user_activity_events_hist`

| 属性 | 值 |
|---|---|
| 中文用途 | 用户行为事件历史分区（MINVALUE 至 2026-09-01）。 |
| 数据所有者领域 | `api(父表分区)` — 依据：分区表：无独立 Go 引用，归属父表 `user_activity_events` 的领域 |
| 敏感级别 | **restricted** — 依据：用户行为遥测（append-only 授权，仅 SELECT/INSERT） |
| 保留期 | 见 `retention_policies.user-activity-default`（基线默认，当前 enabled=false） — 依据：`backend/migrations/postgres/0052_s6_data_governance.sql:185-192` |
| 建表迁移 | `未在 backend/migrations/postgres 找到 CREATE TABLE（由迁移框架/扩展或 ALTER 后续创建）` |
| 行数（精确 `count(*)`） | **0** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **40.0 KiB**（heap 0 B / index 32.0 KiB / 共 40,960 B） |
| 分区 | **是（分区子表，父表 `user_activity_events`，边界 `FOR VALUES FROM (MINVALUE) TO ('2026-09-01 00:00:00+00')`）** |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `occurred_at` | timestamp with time zone | NOT NULL | `now()` |
| 3 | `identity_id` | uuid | 可空 | `—` |
| 4 | `organization_id` | uuid | 可空 | `—` |
| 5 | `workspace_id` | uuid | 可空 | `—` |
| 6 | `session_id` | uuid | 可空 | `—` |
| 7 | `category` | text | NOT NULL | `—` |
| 8 | `action` | text | NOT NULL | `—` |
| 9 | `route` | text | NOT NULL | `''::text` |
| 10 | `object_type` | text | NOT NULL | `''::text` |
| 11 | `object_id` | text | NOT NULL | `''::text` |
| 12 | `params_digest` | text | NOT NULL | `''::text` |
| 13 | `result_count` | bigint(64) | NOT NULL | `0` |
| 14 | `duration_ms` | integer(32) | NOT NULL | `0` |
| 15 | `data_source` | text | NOT NULL | `''::text` |
| 16 | `detail` | jsonb | NOT NULL | `'{}'::jsonb` |

**约束**

- PRIMARY KEY（1）：`user_activity_events_hist_pkey` `PRIMARY KEY (id, occurred_at)`
- UNIQUE（0）：无
- FOREIGN KEY（1）：`user_activity_events_identity_id_fkey` `FOREIGN KEY (identity_id) REFERENCES identities(id) ON DELETE SET NULL`
- CHECK（2）：`user_activity_events_hist_bounds` `CHECK ((occurred_at < '2026-09-01 00:00:00+00'::timestamp with time zone))`；`user_activity_events_s7_scope_coherence_check` `CHECK (((workspace_id IS NULL) OR (organization_id IS NOT NULL)))`
- NOT NULL 约束对象：12 个（详见上表「可空」列）

**索引（4）**

- `user_activity_events_hist_identity_id_occurred_at_idx` — `CREATE INDEX user_activity_events_hist_identity_id_occurred_at_idx ON public.user_activity_events_hist USING btree (identity_id, occurred_at DESC)`
- `user_activity_events_hist_occurred_at_idx` — `CREATE INDEX user_activity_events_hist_occurred_at_idx ON public.user_activity_events_hist USING btree (occurred_at DESC)`
- `user_activity_events_hist_organization_id_occurred_at_idx` — `CREATE INDEX user_activity_events_hist_organization_id_occurred_at_idx ON public.user_activity_events_hist USING btree (organization_id, occurred_at DESC)`
- `user_activity_events_hist_pkey` — `CREATE UNIQUE INDEX user_activity_events_hist_pkey ON public.user_activity_events_hist USING btree (id, occurred_at)`

<a id="users"></a>

### `users`

| 属性 | 值 |
|---|---|
| 中文用途 | 用户表（V1 单用户期遗留）：用户账号与所属组织；V2 起账号主体迁至 identities，本表保留兼容。 |
| 数据所有者领域 | `dataset` — 依据：`grep -rl -w users backend/internal` → api(5) dataset(1) |
| 敏感级别 | **confidential** — 依据：含 PII 字段：email |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0001_init.sql:12` |
| 行数（精确 `count(*)`） | **1** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **48.0 KiB**（heap 8.0 KiB / index 32.0 KiB / 共 49,152 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `organization_id` | uuid | 可空 | `—` |
| 3 | `email` | text | NOT NULL | `—` |
| 4 | `display_name` | text | NOT NULL | `''::text` |
| 5 | `role` | text | NOT NULL | `'owner'::text` |
| 6 | `created_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`users_pkey` `PRIMARY KEY (id)`
- UNIQUE（1）：`users_email_key` `UNIQUE (email)`
- FOREIGN KEY（1）：`users_organization_id_fkey` `FOREIGN KEY (organization_id) REFERENCES organizations(id)`
- CHECK（0）：无
- NOT NULL 约束对象：5 个（详见上表「可空」列）

**索引（2）**

- `users_email_key` — `CREATE UNIQUE INDEX users_email_key ON public.users USING btree (email)`
- `users_pkey` — `CREATE UNIQUE INDEX users_pkey ON public.users USING btree (id)`

<a id="workspace-addresses"></a>

### `workspace_addresses`

| 属性 | 值 |
|---|---|
| 中文用途 | 工作区地址目录：地址主档（首末数据时间、同步时间、备注）。 |
| 数据所有者领域 | `store` — 依据：仅命中通用包 → store(2) api(1) |
| 敏感级别 | **internal** — 依据：业务数据（含链上标识） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0004_v12_metadata.sql:31` |
| 行数（精确 `count(*)`） | **414** |
| 行数（`pg_class.reltuples` 估算） | 414 |
| 大小 | **336.0 KiB**（heap 184.0 KiB / index 112.0 KiB / 共 344,064 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `organization_id` | uuid | NOT NULL | `—` |
| 3 | `workspace_id` | uuid | NOT NULL | `—` |
| 4 | `chain` | character varying(32) | NOT NULL | `—` |
| 5 | `address` | character varying(128) | NOT NULL | `—` |
| 6 | `normalized_address` | character varying(128) | NOT NULL | `—` |
| 7 | `display_name` | character varying(255) | 可空 | `—` |
| 8 | `primary_case_id` | uuid | 可空 | `—` |
| 9 | `transaction_count` | bigint(64) | NOT NULL | `0` |
| 10 | `token_transfer_count` | bigint(64) | NOT NULL | `0` |
| 11 | `nft_transfer_count` | bigint(64) | NOT NULL | `0` |
| 12 | `log_count` | bigint(64) | NOT NULL | `0` |
| 13 | `total_activity_count` | bigint(64) | NOT NULL | `0` |
| 14 | `first_data_block` | bigint(64) | 可空 | `—` |
| 15 | `last_data_block` | bigint(64) | 可空 | `—` |
| 16 | `first_data_at` | timestamp with time zone | 可空 | `—` |
| 17 | `last_data_at` | timestamp with time zone | 可空 | `—` |
| 18 | `last_sync_at` | timestamp with time zone | 可空 | `—` |
| 19 | `sync_status` | character varying(32) | NOT NULL | `'unknown'::character varying` |
| 20 | `created_by` | uuid | 可空 | `—` |
| 21 | `created_at` | timestamp with time zone | NOT NULL | `now()` |
| 22 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`workspace_addresses_pkey` `PRIMARY KEY (id)`
- UNIQUE（1）：`workspace_addresses_workspace_id_chain_normalized_address_key` `UNIQUE (workspace_id, chain, normalized_address)`
- FOREIGN KEY（2）：`workspace_addresses_organization_id_fkey` `FOREIGN KEY (organization_id) REFERENCES organizations(id)`；`workspace_addresses_workspace_id_fkey` `FOREIGN KEY (workspace_id) REFERENCES workspaces(id)`
- CHECK（0）：无
- NOT NULL 约束对象：14 个（详见上表「可空」列）

**索引（3）**

- `idx_workspace_addresses_updated` — `CREATE INDEX idx_workspace_addresses_updated ON public.workspace_addresses USING btree (workspace_id, last_sync_at DESC)`
- `workspace_addresses_pkey` — `CREATE UNIQUE INDEX workspace_addresses_pkey ON public.workspace_addresses USING btree (id)`
- `workspace_addresses_workspace_id_chain_normalized_address_key` — `CREATE UNIQUE INDEX workspace_addresses_workspace_id_chain_normalized_address_key ON public.workspace_addresses USING btree (workspace_id, chain, normalized_address)`

<a id="workspace-memberships"></a>

### `workspace_memberships`

| 属性 | 值 |
|---|---|
| 中文用途 | 工作区成员关系：身份在工作区中的成员状态与角色。 |
| 数据所有者领域 | `casework` — 依据：`grep -rl -w workspace_memberships backend/internal` → api(3) casework(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0041_rbac.sql:50` |
| 行数（精确 `count(*)`） | **33** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **48.0 KiB**（heap 8.0 KiB / index 32.0 KiB / 共 49,152 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `workspace_id` | uuid | NOT NULL | `—` |
| 2 | `organization_id` | uuid | NOT NULL | `—` |
| 3 | `identity_id` | uuid | NOT NULL | `—` |
| 4 | `status` | text | NOT NULL | `'active'::text` |
| 5 | `joined_at` | timestamp with time zone | NOT NULL | `now()` |
| 6 | `updated_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`workspace_memberships_pkey` `PRIMARY KEY (workspace_id, identity_id)`
- UNIQUE（0）：无
- FOREIGN KEY（3）：`workspace_memberships_identity_id_fkey` `FOREIGN KEY (identity_id) REFERENCES identities(id) ON DELETE CASCADE`；`workspace_memberships_organization_id_fkey` `FOREIGN KEY (organization_id) REFERENCES organizations(id) ON DELETE CASCADE`；`workspace_memberships_workspace_id_fkey` `FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE`
- CHECK（1）：`workspace_memberships_status_check` `CHECK ((status = ANY (ARRAY['invited'::text, 'active'::text, 'suspended'::text, 'removed'::text])))`
- NOT NULL 约束对象：6 个（详见上表「可空」列）

**索引（2）**

- `workspace_memberships_identity_idx` — `CREATE INDEX workspace_memberships_identity_idx ON public.workspace_memberships USING btree (identity_id, status)`
- `workspace_memberships_pkey` — `CREATE UNIQUE INDEX workspace_memberships_pkey ON public.workspace_memberships USING btree (workspace_id, identity_id)`

<a id="workspaces"></a>

### `workspaces`

| 属性 | 值 |
|---|---|
| 中文用途 | 工作区：组织内的协作与数据隔离单元，地址/案件/导出按工作区归属。 |
| 数据所有者领域 | `policy` — 依据：`grep -rl -w workspaces backend/internal` → api(4) store(2) policy(1) |
| 敏感级别 | **internal** — 依据：推断（无密钥/PII/审计字段） |
| 保留期 | 未定义 — 依据：迁移与代码中未找到该表的 retention/分区定义 |
| 建表迁移 | `backend/migrations/postgres/0002_enterprise_reserved.sql:2` |
| 行数（精确 `count(*)`） | **3** |
| 行数（`pg_class.reltuples` 估算） | -1（从未 ANALYZE） |
| 大小 | **48.0 KiB**（heap 8.0 KiB / index 32.0 KiB / 共 49,152 B） |
| 分区 | 否（普通基表） |

**列定义**

| # | 列名 | 类型 | 可空 | 默认值 |
|---:|---|---|---|---|
| 1 | `id` | uuid | NOT NULL | `gen_random_uuid()` |
| 2 | `organization_id` | uuid | NOT NULL | `—` |
| 3 | `name` | text | NOT NULL | `—` |
| 4 | `slug` | text | NOT NULL | `—` |
| 5 | `created_at` | timestamp with time zone | NOT NULL | `now()` |

**约束**

- PRIMARY KEY（1）：`workspaces_pkey` `PRIMARY KEY (id)`
- UNIQUE（1）：`workspaces_organization_id_slug_key` `UNIQUE (organization_id, slug)`
- FOREIGN KEY（1）：`workspaces_organization_id_fkey` `FOREIGN KEY (organization_id) REFERENCES organizations(id)`
- CHECK（0）：无
- NOT NULL 约束对象：5 个（详见上表「可空」列）

**索引（2）**

- `workspaces_organization_id_slug_key` — `CREATE UNIQUE INDEX workspaces_organization_id_slug_key ON public.workspaces USING btree (organization_id, slug)`
- `workspaces_pkey` — `CREATE UNIQUE INDEX workspaces_pkey ON public.workspaces USING btree (id)`

---

## 5. 分区与保留期

### 5.1 唯一的分区表：`user_activity_events`

实测分区清单（`pg_inherits` + `pg_class.relpartbound`）：

| 分区名 | 边界 | 精确行数 | 总大小 |
|---|---|---:|---:|
| `user_activity_events_202610` | `FOR VALUES FROM ('2026-10-01 00:00:00+00') TO ('2026-11-01 00:00:00+00')` | 0 | 40960 B |
| `user_activity_events_hist` | `FOR VALUES FROM (MINVALUE) TO ('2026-09-01 00:00:00+00')` | 0 | 40960 B |
| `user_activity_events_default` | `DEFAULT` | 0 | 40960 B |
| `user_activity_events_202611` | `FOR VALUES FROM ('2026-11-01 00:00:00+00') TO ('2026-12-01 00:00:00+00')` | 0 | 40960 B |
| `user_activity_events_202609` | `FOR VALUES FROM ('2026-09-01 00:00:00+00') TO ('2026-10-01 00:00:00+00')` | 0 | 40960 B |
| `user_activity_events`（父表，relkind='p'） | — | 0（父表不存数据） | 0 B |

**DDL 依据**：`backend/migrations/postgres/0057_s7_indexes_partitions.sql:269-283`

```sql
-- backend/migrations/postgres/0057_s7_indexes_partitions.sql:269
CREATE TABLE public.user_activity_events (
    LIKE public.user_activity_events_hist INCLUDING DEFAULTS INCLUDING CONSTRAINTS INCLUDING STORAGE
) PARTITION BY RANGE (occurred_at);
ALTER TABLE public.user_activity_events ADD PRIMARY KEY (id, occurred_at);
-- :283 DEFAULT 分区兜底：运维漏建分区时写入不失败、不静默丢弃
CREATE TABLE public.user_activity_events_default PARTITION OF public.user_activity_events DEFAULT;
```

**分区维护/归档候选函数**（`backend/migrations/postgres/0057_s7_indexes_partitions.sql:123`）：

```sql
CREATE OR REPLACE FUNCTION public.s7_partition_archive_candidates(p_retention_days int DEFAULT 90)
RETURNS TABLE(partition_name text, bound_expr text, rows bigint, size_bytes bigint, max_occurred_at timestamptz)
-- 注释原文：「归档候选（只读）：整体早于保留期的分区，可进入「导出 → 双人审批 → DETACH」流程。
--            本函数不执行任何 DETACH/DROP —— 实际删除在 S6 保留策略开关打开前保持关闭。」
```

> **重要**：该函数声明为 `STABLE`（只读）。当前**没有任何分区超过 90 天**——`user_activity_events` 父表与全部 5 个子表精确行数均为 **0**，历史分区上界为 `2026-09-01`。

### 5.2 保留策略表实际内容

```sql
SELECT policy_key, resource_type, scope_type, retain_days, archive_after_days, action, grace_days, legal_hold, enabled FROM retention_policies ORDER BY policy_key;
```

| policy_key | resource_type | scope | retain_days | archive_after_days | action | grace_days | legal_hold | **enabled** |
|---|---|---|---:|---:|---|---:|---|---|
| `audit-archive-default` | `audit_archive` | global | 2555 | 365 | archive | 30 | t | **f** |
| `cache-objects-default` | `cache_object` | global | 30 |  | delete | 3 | f | **f** |
| `clickhouse-partition-default` | `clickhouse_partition` | global | 730 | 365 | archive | 30 | f | **f** |
| `export-objects-default` | `export_object` | global | 90 |  | delete | 7 | f | **f** |
| `raw-objects-default` | `raw_object` | global | 365 | 90 | archive | 7 | f | **f** |
| `user-activity-default` | `user_activity` | global | 90 | 90 | archive | 7 | f | **f** |

**基线数据来源**：`backend/migrations/postgres/0052_s6_data_governance.sql:185-192`，原文注释：「基线数据（幂等）：策略默认全部 enabled=false，删除动作默认关闭。」

```sql
INSERT INTO retention_policies (policy_key, description, resource_type, retain_days, archive_after_days, action, grace_days, legal_hold, enabled) VALUES
    ('raw-objects-default',          '原始 Provider 响应对象（MinIO raw bucket）', 'raw_object',            365,  90,   'archive',  7, false, false),
    ('export-objects-default',       '用户导出产物（MinIO export bucket）',        'export_object',          90,  NULL, 'delete',   7, false, false),
    ('user-activity-default',        '用户高频行为事件（§22.4，90 天归档）',       'user_activity',          90,  90,   'archive',  7, false, false),
    ('audit-archive-default',        '审计归档（合规留存，禁止自动删除）',         'audit_archive',        2555, 365,  'archive', 30, true,  false),
    ('clickhouse-partition-default', 'ClickHouse 明细分区（超期转冷）',            'clickhouse_partition',  730, 365,  'archive', 30, false, false),
    ('cache-objects-default',        '可重建缓存对象',                             'cache_object',           30,  NULL, 'delete',   3, false, false)
ON CONFLICT (policy_key) DO NOTHING;
```

### 5.3 治理开关实际状态

```sql
SELECT key, value::text FROM configuration_settings WHERE key LIKE 'governance%';
```

| 配置项 | 实际值 | 含义 |
|---|---|---|
| `governance.deletion.execution_enabled` | `false` | 删除执行：**关闭** |
| `governance.retention.scheduler_enabled` | `false` | 保留策略调度器：**关闭** |

> 结论：**当前不存在任何正在执行的保留/生命周期动作**。MinIO lifecycle、ClickHouse TTL、PostgreSQL 分区 DETACH/DROP 均未启用。此事实直接决定 `docs/capacity-forecast.md` 的全部预测都是**无界增长**口径。

### 5.4 保留期登记（`data_asset_registry`）

| 表名 | owner_module | sensitivity | retention | notes |
|---|---|---|---|---|
| `audit_events_v2` | `audit` | restricted | 在线 365 天 / 归档 3 年 | append-only，触发器保护 |
| `case_activity` | `case` | internal | 在线 365 天 | 用户可理解的业务时间线 |
| `case_comments` | `case` | confidential | 随案件归档 | 协作评论，正文不写秘密凭证 |
| `case_members` | `case` | internal | 随案件归档 | 案件成员与角色 |
| `cases` | `case` | confidential | 案件归档后 3 年 | 案件元数据，含密级与成员 |
| `case_subjects` | `case` | confidential | 随案件归档 | 案件调查对象 |
| `download_jobs` | `download` | internal | 365 天 | 下载任务与游标 |
| `exports` | `export` | confidential | 按导出策略 | 导出文件引用与 SHA-256 |
| `investigation_canvases` | `canvas` | confidential | 随调查归档 | 图谱画布与快照引用 |
| `investigation_reports` | `report` | restricted | 长期保留（证据） | 报告对象引用与内容哈希 |
| `investigations` | `investigation` | confidential | 随案件归档 | 调查目标、预算与状态 |
| `login_events` | `audit` | restricted | 在线 365 天 / 归档 3 年 | append-only，触发器保护 |
| `notification_delivery_attempts` | `notification` | internal | 90 天 | 送达尝试（脱敏错误） |
| `notifications` | `notification` | internal | 90 天 | 站内通知 |
| `report_approvals` | `report` | restricted | 长期保留（证据） | 报告 revision 审批记录 |
| `report_download_tickets` | `report` | restricted | 票据过期后 30 天 | 只保存 token 摘要 |
| `review_requests` | `review` | confidential | 随对象归档 | Finding/Evidence/报告复核请求 |

**写入来源**：`backend/migrations/postgres/0044_tenant_scope.sql:155-176`（列定义 `:161 retention text NOT NULL DEFAULT ''`，INSERT `:166`，UPSERT `:178`）与 `backend/migrations/postgres/0047_case_collaboration.sql:267-279`。

**注意**：`data_asset_registry` 仅登记 **17** 张表，占 149 张关系的 11.4%；其余 132 张在治理层面**无登记**。

### 5.5 ClickHouse 侧无 TTL

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

> **修正（Revision 2）**：以上为 **migration grep**，只能证明**迁移源码**中无 `TTL`，**不足以证明在线部署无 TTL**（可能存在手工 `ALTER TABLE ... MODIFY TTL`）。因此补充**在线部署 DDL 实测**：

```sql
SELECT name, create_table_query FROM system.tables WHERE database='blockchain' ORDER BY name;
```

实测输出（`blockchain` 库全部 9 张表的完整 DDL 已逐表核对，以下为代表性片段，保留原文）：

```
transactions              ENGINE = ReplacingMergeTree(source_ingested_at) PARTITION BY (chain, toYYYYMM(timestamp)) ORDER BY (chain, tx_hash) SETTINGS index_granularity = 8192
token_transfers           ENGINE = ReplacingMergeTree(source_ingested_at) PARTITION BY (chain, toYYYYMM(timestamp)) ORDER BY (chain, tx_hash, log_index, token_address) SETTINGS index_granularity = 8192
address_activity          ENGINE = ReplacingMergeTree(source_ingested_at) PARTITION BY (chain, toYYYYMM(timestamp)) ORDER BY (chain, address, timestamp, tx_hash, activity_type, counterparty, token_address) SETTINGS index_granularity = 8192
logs                      ENGINE = ReplacingMergeTree(source_ingested_at) PARTITION BY (chain, toYYYYMM(timestamp)) ORDER BY (chain, tx_hash, log_index) SETTINGS index_granularity = 8192
nft_transfers             ENGINE = ReplacingMergeTree(source_ingested_at) PARTITION BY (chain, toYYYYMM(timestamp)) ORDER BY (chain, tx_hash, log_index, contract_address, token_id) SETTINGS index_granularity = 8192
address_balances          ENGINE = ReplacingMergeTree(source_ingested_at) PARTITION BY (chain, toYYYYMM(timestamp)) ORDER BY (chain, address, token_symbol, token_address) SETTINGS index_granularity = 8192
internal_calls            ENGINE = ReplacingMergeTree(traced_at) PARTITION BY chain ORDER BY (chain, tx_hash, call_index) SETTINGS index_granularity = 8192
transaction_traces        ENGINE = ReplacingMergeTree(traced_at) PARTITION BY chain ORDER BY (chain, tx_hash) SETTINGS index_granularity = 8192
transaction_decoded_inputs ENGINE = ReplacingMergeTree(decoded_at) PARTITION BY chain ORDER BY (chain, tx_hash) SETTINGS index_granularity = 8192
```

> **在线实测结论（Revision 2）**：**9 张表的 `create_table_query` 中均无 `TTL` 子句**，只有 `ENGINE` / `PARTITION BY` / `ORDER BY` / `SETTINGS index_granularity = 8192`（部分表另有 bloom_filter `INDEX`）。引擎均为 `ReplacingMergeTree(<version 列>)`。因此「**无 TTL**」这一结论由**在线 DDL 直接证实**，migration grep 仅作佐证。ClickHouse 明细数据无界增长，只能依靠 `retention_policies.clickhouse-partition-default`（730 天，**enabled=false**）或人工 `DROP PARTITION` 控制。

---

## 6. ClickHouse 表清单（库 `blockchain`）

```
$ docker exec deploy-clickhouse-1 clickhouse-client --user $CLICKHOUSE_USER --password $CLICKHOUSE_PASSWORD \
    -q "SELECT database, name, engine, sorting_key, partition_key, total_rows, formatReadableSize(total_bytes) FROM system.tables WHERE database='blockchain' FORMAT TSVWithNames"
```

| 表名 | 引擎 | 排序键（sorting_key） | 分区键（partition_key） | 行数（total_rows） | 大小（total_bytes） |
|---|---|---|---|---:|---:|
| `address_activity` | ReplacingMergeTree | `chain, address, timestamp, tx_hash, activity_type, counterparty, token_address` | `(chain, toYYYYMM(timestamp))` | 74,454,629 | 5.43 GiB |
| `address_balances` | ReplacingMergeTree | `chain, address, token_symbol, token_address` | `(chain, toYYYYMM(timestamp))` | 86 | 14.25 KiB |
| `internal_calls` | ReplacingMergeTree | `chain, tx_hash, call_index` | `chain` | 2,729 | 115.49 KiB |
| `logs` | ReplacingMergeTree | `chain, tx_hash, log_index` | `(chain, toYYYYMM(timestamp))` | 11,901,727 | 467.00 MiB |
| `nft_transfers` | ReplacingMergeTree | `chain, tx_hash, log_index, contract_address, token_id` | `(chain, toYYYYMM(timestamp))` | 0 | 0.00 B |
| `token_transfers` | ReplacingMergeTree | `chain, tx_hash, log_index, token_address` | `(chain, toYYYYMM(timestamp))` | 56,605,708 | 4.51 GiB |
| `transaction_decoded_inputs` | ReplacingMergeTree | `chain, tx_hash` | `chain` | 3 | 4.60 KiB |
| `transaction_traces` | ReplacingMergeTree | `chain, tx_hash` | `chain` | 3 | 132.36 KiB |
| `transactions` | ReplacingMergeTree | `chain, tx_hash` | `(chain, toYYYYMM(timestamp))` | 35,932,520 | 5.62 GiB |

> 全部 9 张表的引擎均为 **`ReplacingMergeTree`**（实测 `system.tables.engine`）。

**磁盘占用与 part 数**（`SELECT table, sum(bytes_on_disk), sum(rows), count() FROM system.parts WHERE active AND database='blockchain' GROUP BY table`）：

| 表名 | 活跃 part 数 | 磁盘字节（bytes_on_disk） | 行数 |
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

**库级汇总**：`blockchain` = 16.02 GiB；`system` = 5.75 GiB（`SELECT database, formatReadableSize(sum(bytes_on_disk)) FROM system.parts WHERE active GROUP BY 1`）。

> **保留期**：9 张 CH 表**均无 TTL**。唯一登记的 `retention_policies.clickhouse-partition-default`（`retain_days=730`, `archive_after_days=365`, `action=archive`, `enabled=false`）未启用 → 保留期记为「**策略已登记但未启用（等效无限期）**」。

### 6.5 `system.part_log` 的口径（Revision 2 修正）

**复核意见 1 成立**：`system.part_log` 的 `NewPart` 事件记录的是 **gross（毛）INSERT 写入量**，即新 part 落盘字节，**不是磁盘净增**，也**不是净增长的下界**——后台 `merge`（去重）、`mutate`、`RemovePart` 都会让净增**小于**该值。

实测全历史事件聚合（`SELECT event_type, count(), sum(size_in_bytes) FROM system.part_log WHERE database='blockchain' GROUP BY event_type`）：

| event_type | 事件数 | 字节 | 口径 |
|---|---:|---:|---|
| `NewPart` | 117,064 | **22,524,443,248** | **gross INSERT 写入量**（不含合并产物，见下） |
| `RemovePart` | 143,583 | 138,789,151,601 | 被移除 part 的体积（**含被合并吸收的源 part**，故远大于 NewPart） |
| `MergeParts` | 24,061 | 79,114,067,240 | 参与合并的**源** part 体积 |
| `MutatePart` | 3,503 | 54,357,255,522 | 参与 mutation 的 part 体积 |

> 注意：`MergeParts` 与 `RemovePart` 的体积**远大于** `NewPart`，说明这两类事件统计的是**源 part**而非产物，因此 **`NewPart − RemovePart` 不是有效净增公式**（其值为负 −116 GB，无物理意义）。**真实净增只能由两点快照测得**。

### 6.6 两点快照实测净增（Revision 2 新增）

在两个时点分别采集 `system.parts WHERE active` 的 `sum(bytes_on_disk)` 与 `sum(rows)`：

| 时点 | UTC（`system.parts` 的 `now()`） | 活跃 part 字节 | 活跃 part 行数 | part 数 |
|---|---|---:|---:|---:|
| T1 | 2026-09-12 21:5x（§2.3 同批采集） | 17,204,614,409 | 178,897,405 | 982 |
| T2 | 2026-09-12 22:10:10 | 17,204,614,409 | 178,897,405 | 982 |

```sql
SELECT now(), sum(bytes_on_disk) AS bytes, sum(rows) AS rows, count() AS parts
  FROM system.parts WHERE active AND database='blockchain';
```

**实测结果**：观测窗口内**净增 = 0 B / 0 行 / 0 part**。该窗口内 ClickHouse 无写入（平台空闲）。

> **该结果的正确解读（不得过度外推）**：这是一次**短窗口、空闲时刻**的实测，**只能证明"该窗口内净增为 0"**，**不能**用 0 去外推日均，也**不能**证明全生命周期净增为 0。它恰好证明 CH 增长是**突发式**而非连续式。长时间尺度的净增证据见 `docs/capacity-forecast.md` §2.5。

---

## 7. MinIO 桶清单

> `mc` 在部署机与 MinIO 容器内**均不存在**（`which mc`、`ls /usr/local/bin/mc`、`ls /usr/bin/mc`、`docker exec deploy-minio-1 which mc` 全部失败）。因此改用等价的**只读 S3 API**（AWS SigV4：`GET /`、`GET /<bucket>?list-type=2`、`GET /<bucket>?versioning`、`GET /<bucket>?lifecycle`、`GET /<bucket>?encryption`、`GET /<bucket>?object-lock`），以 `python3` 标准库 `hmac/hashlib/urllib` 实现，**只发 GET，不写任何对象**。

| 桶名 | 对象数 | 总大小（字节） | 总大小 | 版本控制 | ILM（lifecycle） | 服务端加密 | Object Lock |
|---|---:|---:|---|---|---|---|---|
| `blockchain-audit` | 4 | 1,964 | 1.92 KiB | 未启用（空 `<VersioningConfiguration/>`） | **无**（404 `NoSuchLifecycleConfiguration`） | **无**（404） | **无**（404） |
| `blockchain-backups` | 0 | 0 | 0 B | 未启用（空 `<VersioningConfiguration/>`） | **无**（404 `NoSuchLifecycleConfiguration`） | **无**（404） | **无**（404） |
| `blockchain-exports` | 28 | 5,274,869 | 5.03 MiB | 未启用（空 `<VersioningConfiguration/>`） | **无**（404 `NoSuchLifecycleConfiguration`） | **无**（404） | **无**（404） |
| `blockchain-raw` | 170,515 | 15,058,046,314 | 14.03 GiB | 未启用（空 `<VersioningConfiguration/>`） | **无**（404 `NoSuchLifecycleConfiguration`） | **无**（404） | **无**（404） |
| `blockchain-reports` | 68 | 115,322 | 0.11 MiB | 未启用（空 `<VersioningConfiguration/>`） | **无**（404 `NoSuchLifecycleConfiguration`） | **无**（404） | **无**（404） |
| `blockchain-reports-drill-20260912t175029z` | 60 | 101,724 | 0.10 MiB | 未启用（空 `<VersioningConfiguration/>`） | **无**（404 `NoSuchLifecycleConfiguration`） | **无**（404） | **无**（404） |
| **合计** | **170,675** | **15,063,540,193（14.03 GiB）** | **14.03 GiB** | — | — | — | — |

### 7.1 `blockchain-raw` 按对象 `LastModified` 的日分布（唯一体量桶）

> **口径（Revision 2 强化标注）**：本节的逐日分布是 **survivor / current-version 口径**——
> 1. 只覆盖**当前存活对象**，**已删除对象完全不在统计内**；
> 2. 6 个桶均**未启用版本控制**，故**无历史版本**；若曾启用过版本控制，旧版本也不在 `ListObjectsV2` 内；
> 3. `LastModified` 是**最后修改时间**，被覆盖写的对象会归入最后一次修改日。
>
> 因此该分布是**近似写入分布**，**不是全部历史净增**。误差方向取决于删除与覆盖写的比例——**当前不可量化**（已列入 §10）。

| 日期（UTC） | 对象数 | 字节 |
|---|---:|---:|
| 2026-08-31 | 35,746 | 2,738,817,223 |
| 2026-09-01 | 81 | 1,994,096 |
| 2026-09-02 | 420 | 542,317,788 |
| 2026-09-03 | 654 | 195,427,714 |
| 2026-09-04 | 3,324 | 426,076,462 |
| 2026-09-05 | 6,319 | 7,508,775,015 |
| 2026-09-06 | 607 | 3,530,129 |
| 2026-09-07 | 640 | 160,560,301 |
| 2026-09-08 | 61,321 | 2,401,351,912 |
| 2026-09-09 | 707 | 13,830,845 |
| 2026-09-10 | 60,493 | 1,062,677,947 |
| 2026-09-11 | 78 | 115,650 |
| 2026-09-12 | 125 | 2,571,232 |
| **合计** | **170,515** | **15,058,046,314** |

> **口径说明**：`LastModified` 是对象的**最后修改时间**，非首次写入时间；MinIO 未启用版本控制，故该分布近似等于「当日写入对象」体积，但**被覆盖写过的对象会归到最后一次修改日**。另有 2,491 个对象（15,063,390,193 − 15,058,046,314 = 5,343,879 字节）分布在其余 4 个桶。

---

## 8. 三维度归属（organization / workspace / dataset）

### 8.1 维度列可用性

```sql
SELECT table_name, column_name FROM information_schema.columns
 WHERE table_schema='public' AND column_name IN ('organization_id','workspace_id','dataset_id','dataset');
```

实测：**三个维度列均存在**（查询返回 **87** 行「表-列」组合）。命中表数（含 `user_activity_events` 的 5 个分区子表）：

| 维度列 | 命中表数 | 语义 |
|---|---:|---|
| `organization_id` | 33 | UUID，指向 `organizations.id` |
| `workspace_id` | 27 | UUID，指向 `workspaces.id` |
| `dataset` | 24 | **文本数据集名**（`transactions`/`token_transfers`/`logs`/`internal_transactions`/`balances`/`nft_transfers`） |
| `dataset_id` | 3 | **UUID 外键**，指向 `download_datasets.id` |

> 二者语义不同，不可混用：`download_shards` 同时拥有 `dataset`（文本名）与 `dataset_id`（UUID）。

环境基数：`SELECT count(*) FROM organizations` = **2**；`FROM workspaces` = **3**；`FROM download_datasets` = **14,996**。

### 8.2 organization 维度实测占用（按 `count(*)`）

| 表 | organization_id | 行数 |
|---|---|---:|
| `approval_requests` | `627c131a-3842-4e19-bade-d20ffb8aea8f` | 27 |
| `approval_requests` | `d3b152f2-44c0-441c-b047-be24c137a84b` | 1 |
| `audit_events_v2` | `627c131a-3842-4e19-bade-d20ffb8aea8f` | 825 |
| `audit_events_v2` | `(NULL)` | 6,622 |
| `audit_events_v2` | `d3b152f2-44c0-441c-b047-be24c137a84b` | 41,140 |
| `cases` | `627c131a-3842-4e19-bade-d20ffb8aea8f` | 90 |
| `cases` | `d3b152f2-44c0-441c-b047-be24c137a84b` | 26 |
| `download_jobs` | `627c131a-3842-4e19-bade-d20ffb8aea8f` | 72 |
| `download_jobs` | `d3b152f2-44c0-441c-b047-be24c137a84b` | 2,347 |
| `exports` | `d3b152f2-44c0-441c-b047-be24c137a84b` | 129 |
| `exports` | `627c131a-3842-4e19-bade-d20ffb8aea8f` | 114 |
| `investigation_canvases` | `627c131a-3842-4e19-bade-d20ffb8aea8f` | 48 |
| `investigation_canvases` | `d3b152f2-44c0-441c-b047-be24c137a84b` | 42 |
| `investigations` | `d3b152f2-44c0-441c-b047-be24c137a84b` | 24 |
| `investigations` | `627c131a-3842-4e19-bade-d20ffb8aea8f` | 105 |
| `login_events` | `627c131a-3842-4e19-bade-d20ffb8aea8f` | 857 |
| `login_events` | `(NULL)` | 1,413 |
| `login_events` | `d3b152f2-44c0-441c-b047-be24c137a84b` | 1,005 |
| `notifications` | `627c131a-3842-4e19-bade-d20ffb8aea8f` | 58 |
| `sessions` | `d3b152f2-44c0-441c-b047-be24c137a84b` | 959 |
| `sessions` | `627c131a-3842-4e19-bade-d20ffb8aea8f` | 798 |

### 8.3 workspace 维度实测占用

| 表 | workspace_id | 行数 |
|---|---|---:|
| `audit_events_v2` | `14dfbf1b-b3d7-4326-8395-b1f7fa9f8872` | 825 |
| `audit_events_v2` | `(NULL)` | 6,631 |
| `audit_events_v2` | `000e31bb-8c48-435d-af97-95bbc75a496c` | 41,131 |
| `cases` | `14dfbf1b-b3d7-4326-8395-b1f7fa9f8872` | 89 |
| `cases` | `000e31bb-8c48-435d-af97-95bbc75a496c` | 26 |
| `cases` | `231875b4-2687-4535-aba6-cbee410c5020` | 1 |
| `download_jobs` | `14dfbf1b-b3d7-4326-8395-b1f7fa9f8872` | 72 |
| `download_jobs` | `000e31bb-8c48-435d-af97-95bbc75a496c` | 2,347 |
| `exports` | `000e31bb-8c48-435d-af97-95bbc75a496c` | 129 |
| `exports` | `14dfbf1b-b3d7-4326-8395-b1f7fa9f8872` | 114 |
| `investigations` | `000e31bb-8c48-435d-af97-95bbc75a496c` | 24 |
| `investigations` | `14dfbf1b-b3d7-4326-8395-b1f7fa9f8872` | 105 |
| `login_events` | `000e31bb-8c48-435d-af97-95bbc75a496c` | 945 |
| `login_events` | `14dfbf1b-b3d7-4326-8395-b1f7fa9f8872` | 798 |
| `login_events` | `(NULL)` | 1,532 |
| `notifications` | `14dfbf1b-b3d7-4326-8395-b1f7fa9f8872` | 58 |
| `sessions` | `000e31bb-8c48-435d-af97-95bbc75a496c` | 959 |
| `sessions` | `14dfbf1b-b3d7-4326-8395-b1f7fa9f8872` | 798 |
| `workspace_addresses` | `000e31bb-8c48-435d-af97-95bbc75a496c` | 414 |

### 8.4 dataset 维度实测占用

| 表 | dataset | 行数 |
|---|---|---:|
| `dataset_coverage` | `balances` | 5 |
| `dataset_coverage` | `internal_transactions` | 5 |
| `dataset_coverage` | `token_transfers` | 4,909 |
| `dataset_coverage` | `transactions` | 3,190 |
| `dataset_coverage` | `logs` | 15 |
| `download_datasets` | `transactions` | 7,464 |
| `download_datasets` | `nft_transfers` | 7 |
| `download_datasets` | `token_transfers` | 6,691 |
| `download_datasets` | `logs` | 664 |
| `download_datasets` | `internal_transactions` | 159 |
| `download_datasets` | `balances` | 11 |
| `download_pages` | `logs` | 696 |
| `download_pages` | `internal_transactions` | 6,647 |
| `download_pages` | `transactions` | 38,224 |
| `download_pages` | `token_transfers` | 119,935 |
| `download_shards` | `balances` | 6 |
| `download_shards` | `nft_transfers` | 57 |
| `download_shards` | `logs` | 2,269 |
| `download_shards` | `internal_transactions` | 158 |
| `download_shards` | `transactions` | 13,278 |
| `download_shards` | `token_transfers` | 12,617 |
| `download_volume_guard_commits` | `b5311edc-86ad-4a0a-ad9d-4ec2073049de` | 9,999 |
| `download_volume_guard_commits` | `b6c33500-589d-4ec5-aa2e-b389263551a9` | 9,999 |
| `download_volume_guard_commits` | `35f61062-5823-4cf1-815e-55baa83bac2d` | 9 |
| `download_volume_guard_commits` | `9dfc3508-f854-4dd3-8100-586311c06ab8` | 89 |
| `download_volume_guard_commits` | `ec469192-0dad-437b-8846-2b68f240522f` | 8 |
| `download_volume_guard_commits` | `11556905-40c1-475e-810b-0d96ee0a08ab` | 8 |
| `download_volume_guard_commits` | `33697a0b-b36c-49e1-bbbf-aa203b4b6c3c` | 8 |
| `download_volume_guard_commits` | `ab826a20-827a-486e-920a-6f72aa722073` | 76 |
| `download_volume_guard_commits` | `5e664f87-f402-466f-a797-84290b697df3` | 61 |
| `download_volume_guard_commits` | `fa41524b-7a3e-49ac-b42a-93b4e60d29bc` | 60 |
| `download_volume_guard_commits` | `8d77d206-0aef-4866-a7b7-5435817e3f37` | 56 |
| `download_volume_guard_commits` | `3c67fe7f-dfa1-42b4-83c9-c49583ec4e2e` | 52 |
| `download_volume_guard_commits` | `437b5194-51f8-4bb3-beb9-45e6f16699cc` | 51 |
| `download_volume_guard_commits` | `f86f69b3-9752-42c2-8359-6ee8ea0d93c0` | 51 |
| `download_volume_guard_commits` | `f81135e4-e7b9-408c-8e0b-42b0bba08063` | 50 |
| `download_volume_guard_commits` | `d4a98f28-1b81-4419-a853-4d624acc69e8` | 50 |
| `download_volume_guard_commits` | `ed3bcc69-0d09-4c11-a9e4-912b8f2f1f33` | 5 |
| `download_volume_guard_commits` | `b488111b-a517-42c8-ad6f-358f9eb987ac` | 48 |
| `download_volume_guard_commits` | `b33f9fc0-ec81-49d8-b4df-af9f6b2208b1` | 48 |
| `download_volume_guard_commits` | `d71b4005-05d4-4159-aa23-9d188bf82ec0` | 44 |
| `download_volume_guard_commits` | `fe330946-aafc-4ed6-ac30-c3710f49f95d` | 42 |
| `download_volume_guard_commits` | `c3bc26f8-e139-4620-9117-ac9b0e7f2696` | 4 |
| `download_volume_guard_commits` | `f11b1f70-28be-48d4-ac06-68533aa5152c` | 4 |
| `download_volume_guard_commits` | `4591fe97-2816-4077-be10-eb73814684e2` | 4 |
| `download_volume_guard_commits` | `c409c405-cb7d-4498-b5d1-c1f6fe782aa0` | 4 |
| `download_volume_guard_commits` | `ffb2f757-1747-41cf-8d7e-9276424d418a` | 4 |
| `download_volume_guard_commits` | `e08c4485-9263-4b5c-8de4-329f1c61bac3` | 4 |
| `download_volume_guard_commits` | `03658292-cedd-4caf-8148-db415767f281` | 4 |
| `download_volume_guard_commits` | `42628254-d919-452e-8f26-76ac9d84cf82` | 4 |
| `download_volume_guard_commits` | `06c7db65-f13d-41af-878b-464a79dd3914` | 4 |
| `download_volume_guard_commits` | `f4e40fef-26a6-4e6f-89a0-0865d817f94d` | 4 |
| `download_volume_guard_commits` | `2e652593-c62b-47db-ae36-fc045e355f46` | 4 |
| `download_volume_guard_commits` | `62d6765b-3a02-45c9-8cce-a988e1b35475` | 4 |
| `download_volume_guard_commits` | `96b99dfe-1d2c-4778-97d0-5770aabd188a` | 4 |
| `download_volume_guard_commits` | `476d30a5-f861-4f47-bdde-68d93fcc90db` | 4 |
| `download_volume_guard_commits` | `f2cd938b-b71f-408d-a9c8-b850ed729c27` | 4 |
| `download_volume_guard_commits` | `8a31ece0-c721-4cd7-830f-7fa7d53d3f71` | 4 |
| `download_volume_guard_commits` | `3fd3ffda-d469-4f37-a972-9ea958c43af5` | 4 |
| `download_volume_guard_commits` | `aaa69ad5-8611-43ed-a875-8193a7cc87d5` | 4 |
| `download_volume_guard_commits` | `7557d636-89e5-4848-acc3-339275575129` | 4 |
| `download_volume_guard_commits` | `d289b338-9071-4ecc-a322-5e97e97f6344` | 4 |
| `download_volume_guard_commits` | `cf3daf2b-87ff-4a94-927c-739555fac0b5` | 4 |
| `download_volume_guard_commits` | `664c3130-0d21-4cf6-b8e8-51e6b77316ab` | 4 |
| `download_volume_guard_commits` | `6e6e3f57-744f-448b-82cb-928e8831ada3` | 4 |
| `download_volume_guard_commits` | `66da112f-f93a-4166-bdec-cbe5b83edb89` | 4 |
| `download_volume_guard_commits` | `a0224712-a5c0-43fc-b656-0c9e84a8d006` | 4 |
| `download_volume_guard_commits` | `11a0d947-0056-4512-a163-78339588deb1` | 4 |
| `download_volume_guard_commits` | `c594ba93-5b15-40e0-8534-d2fc5abc6b0a` | 4 |
| `download_volume_guard_commits` | `c806983b-60f4-4922-9b25-8a9753237ace` | 4 |
| `download_volume_guard_commits` | `ccd6f8b7-d5fc-4f91-9a40-5eeb4e611218` | 4 |
| `download_volume_guard_commits` | `36d40e80-852a-4d58-b48a-39d19836e8e2` | 4 |
| `download_volume_guard_commits` | `3ef29e82-eb17-4e4c-b58d-a5f9e75f2836` | 4 |
| `download_volume_guard_commits` | `ed635a9e-5eb9-4901-8cb4-44bb4bc99e8e` | 4 |
| `download_volume_guard_commits` | `72c94adf-efda-48a0-87b9-1c38e776a5a0` | 4 |
| `download_volume_guard_commits` | `b43032d7-2b05-4dc2-91a3-8caf347ec784` | 4 |
| `download_volume_guard_commits` | `71d5c1e0-9e2f-4ea5-9625-ab9e553f47a6` | 4 |
| `download_volume_guard_commits` | `c3889796-d484-4abd-9c72-3e8f7cdd2a66` | 4 |
| `download_volume_guard_commits` | `2881fc50-2b9c-46c3-9ec0-a629b06b9f95` | 4 |
| `download_volume_guard_commits` | `60bd630e-1f80-4e2d-800b-94899167e58c` | 4 |
| `download_volume_guard_commits` | `b589cfa0-209d-4ed4-b74b-dc2f59278d59` | 4 |
| `download_volume_guard_commits` | `b61d0a08-6e9b-4473-acbc-295e4b8bb28f` | 392 |
| `download_volume_guard_commits` | `2128c74c-ec71-46b0-ae4f-4e0a7b21c75a` | 352 |
| `download_volume_guard_commits` | `54779b3d-d4c6-4e71-aa2e-3bc097691d0c` | 35 |
| `download_volume_guard_commits` | `97ba9bfa-9704-4ff9-877e-b2ddfda2bf08` | 35 |
| `download_volume_guard_commits` | `cfa75dca-8ffa-472c-b191-d0046f6186c0` | 31 |
| `download_volume_guard_commits` | `4afb5baa-f7ff-440f-9091-55060620e30f` | 31 |
| `download_volume_guard_commits` | `f235de1d-2d30-4b67-ab2a-edb69e2293f7` | 24 |
| `download_volume_guard_commits` | `7ca6ca23-ffe6-4101-87a5-9cfeb0de8c26` | 23 |
| `download_volume_guard_commits` | `2064d0f3-1cde-4e08-992b-607c4bcfc635` | 23 |
| `download_volume_guard_commits` | `ed24b00c-f3a0-4cc6-922f-05ef5c2ffd91` | 22 |
| `download_volume_guard_commits` | `d20fe09b-699d-4159-a92d-58e5129a1afa` | 217 |
| `download_volume_guard_commits` | `aac8cffa-0ac0-498c-a778-99958628ee73` | 20 |
| `download_volume_guard_commits` | `a3d99efa-6656-45fd-a6e6-7068797034a4` | 20 |
| `download_volume_guard_commits` | `51ec3bbc-6cf0-4931-b5fc-0b1a2fdff08f` | 2 |
| `download_volume_guard_commits` | `c4109646-d307-4906-8d37-5e7a61bb5775` | 2 |
| `download_volume_guard_commits` | `639d8b26-c839-49e6-bd24-8e052abc78ed` | 2 |
| `download_volume_guard_commits` | `7b2f68aa-7a9c-437f-b682-9af0e5213a79` | 2 |
| `download_volume_guard_commits` | `775d33d9-659b-4409-8444-55a1955f6a16` | 2 |
| `download_volume_guard_commits` | `fb855eb1-1eb6-4cd3-9424-4c00ac8e0b2a` | 2 |
| `download_volume_guard_commits` | `fca31dd5-164b-4beb-aad1-a0dd08d66af8` | 2 |
| `download_volume_guard_commits` | `cb50c1fd-b057-4514-88fa-13731fa8b4eb` | 2 |
| `download_volume_guard_commits` | `1f73f1bf-e8d4-4512-a6a5-a513778b4ab3` | 2 |
| `download_volume_guard_commits` | `8fa11ea8-78d0-474f-ac80-390104dde160` | 2 |
| `download_volume_guard_commits` | `361c0902-e44f-4f88-97a8-e72604ea3a8a` | 2 |
| `download_volume_guard_commits` | `9b8e9b30-5442-4145-aa40-1e903e628e83` | 2 |
| `download_volume_guard_commits` | `985334b4-4f0b-4e54-8cfc-0d79fda1d9a0` | 2 |
| `download_volume_guard_commits` | `84e6f2f4-450b-4ad3-a1fd-1a4a13b06f38` | 2 |
| `download_volume_guard_commits` | `e7cbc027-f6e1-450f-a07b-44cd174a8871` | 2 |
| `download_volume_guard_commits` | `ea0c788e-52c7-4576-8715-09ae9a4e8920` | 2 |
| `download_volume_guard_commits` | `e490578f-b1cc-4379-ab16-73f5aad7e50d` | 2 |
| `download_volume_guard_commits` | `996323f7-8a45-4858-823d-fcc174cd955e` | 2 |
| `download_volume_guard_commits` | `e9513ae6-f1c0-4dd6-a409-9f81d55a3286` | 2 |
| `download_volume_guard_commits` | `bee61e28-cfe6-4328-8c6c-391953ff1845` | 2 |
| `download_volume_guard_commits` | `32d97c31-ad87-4103-9be5-25f2385fe6fb` | 2 |
| `download_volume_guard_commits` | `3adcbfbd-f91a-4f97-8bda-adf74b92ff44` | 2 |
| `download_volume_guard_commits` | `378e540a-00f2-49d3-bbed-61913fc249c1` | 2 |
| `download_volume_guard_commits` | `2e554a8f-dd1b-4170-9984-71930b17c0f0` | 17 |
| `download_volume_guard_commits` | `fbed0e1f-72ff-48b9-bc65-dff6775a66ff` | 17 |
| `download_volume_guard_commits` | `86addec2-3534-4266-b6c9-3bdc53ca5b2f` | 17 |
| `download_volume_guard_commits` | `45704961-c7a5-4e04-98ab-a353fd6a60e3` | 16 |
| `download_volume_guard_commits` | `80035265-0ead-4265-9948-903d26be16d8` | 16 |
| `download_volume_guard_commits` | `afa94015-7021-47bc-841f-cba3c2cef8e2` | 16 |
| `download_volume_guard_commits` | `f6584037-d561-4e60-a9bb-c1807b0001ae` | 16 |
| `download_volume_guard_commits` | `e63c34e2-fb78-409e-ab35-c828782abd53` | 16 |
| `download_volume_guard_commits` | `4764b8d1-ba61-4a75-899b-f3d7d236bbb3` | 16 |
| `download_volume_guard_commits` | `986ce9f7-85f2-473d-b6a3-a59c6978bfaf` | 16 |
| `download_volume_guard_commits` | `285220ca-39f5-4e09-b01d-4ff7a55b5d0c` | 16 |
| `download_volume_guard_commits` | `da8418de-a825-4230-8417-922281d88aa3` | 16 |
| `download_volume_guard_commits` | `f61603a4-9c39-4ffd-b252-b4376bf7deed` | 16 |
| `download_volume_guard_commits` | `ca7d302b-2115-4978-a6b5-c923b815a91d` | 16 |
| `download_volume_guard_commits` | `dab1a1bb-272b-41a7-99bc-3dab562671db` | 16 |
| `download_volume_guard_commits` | `5063acc5-3af4-47e0-8802-6629e121047c` | 16 |
| `download_volume_guard_commits` | `8420d439-5d0d-451d-842c-8b7a50468316` | 16 |
| `download_volume_guard_commits` | `fbcf8c26-271f-4ca7-8103-2f1dd8d230f6` | 16 |
| `download_volume_guard_commits` | `d8edb8ec-9644-4bb1-b6ab-640d2547c08a` | 16 |
| `download_volume_guard_commits` | `548feaf7-cf1f-4a03-b986-4c7fae66ac85` | 16 |
| `download_volume_guard_commits` | `a4c9801c-c3ad-473f-8e02-bedac77767ff` | 16 |
| `download_volume_guard_commits` | `74740289-a773-4410-b06d-a74ebd420fea` | 16 |
| `download_volume_guard_commits` | `b745368f-c8b3-4c34-b5a9-62e6ffac2af5` | 16 |
| `download_volume_guard_commits` | `e280e9f3-ff92-489b-9a55-3792222d0b38` | 16 |
| `download_volume_guard_commits` | `8561b5f6-2321-42cf-bca2-56e5f18e9045` | 16 |
| `download_volume_guard_commits` | `d5a991da-51ff-4bca-a21c-9e94b12f52a7` | 16 |
| `download_volume_guard_commits` | `fea5fc78-35c5-4ca2-a759-68f56da74292` | 16 |
| `download_volume_guard_commits` | `bca8a21f-973b-4b12-8a95-c8392874b6a0` | 16 |
| `download_volume_guard_commits` | `e6338f76-5867-4e52-bf33-d8b448854d8a` | 16 |
| `download_volume_guard_commits` | `7e9c5330-4c73-48e9-aa45-991e62902d31` | 12 |
| `download_volume_guard_commits` | `a43e5978-2e87-4ac7-828e-3dc4812026be` | 112 |
| `download_volume_guard_commits` | `5a33a7bd-ebff-4097-91da-bce30fbe445c` | 111 |
| `download_volume_guard_commits` | `4b52b6d7-f651-464a-bb15-97ce9c8bf813` | 1 |
| `download_volume_guard_commits` | `305b10ec-e427-4917-beeb-32da188cdc4a` | 1 |
| `download_volume_guard_commits` | `dedb3cc4-2282-4e9a-ab65-039b0e63467a` | 1 |
| `download_volume_guard_commits` | `a4f7c7f1-cc1b-46bc-88b9-639a1c48e6f5` | 1 |
| `download_volume_guard_commits` | `bb9f7ab5-8bdd-415c-9c36-3e908e86b429` | 1 |
| `download_volume_guard_commits` | `f57f539f-7227-4218-ac65-1ad0ea7df7bb` | 1 |
| `download_volume_guard_commits` | `958b8434-be02-4de1-8911-da77ee83c583` | 1 |
| `download_volume_guard_commits` | `e9e21cd5-28e4-4b87-97fa-40c8b0685f1e` | 1 |
| `download_volume_guard_commits` | `8dce26f3-fbdc-4397-bcb1-91faf726d175` | 1 |
| `download_volume_guard_commits` | `6e8179ab-64b5-42ef-b2e0-1b6411a76388` | 1 |
| `download_volume_guard_commits` | `02ad299e-fb8a-4653-aa54-54d81b2b2cfa` | 1 |
| `download_volume_guard_commits` | `60bf140e-24c3-42b2-93b3-51e67164bd0b` | 1 |
| `download_volume_guard_commits` | `215486e5-5a00-444e-add8-2066a5760e76` | 1 |
| `download_volume_guard_commits` | `ab492e5d-52dd-4c74-94ea-e92996bfcc00` | 1 |
| `download_volume_guard_commits` | `96c9249e-addf-40ca-bdd6-aa916e9cf17d` | 1 |
| `download_volume_guard_commits` | `ef505305-a6fb-4554-8ad9-c2d456cbdc66` | 1 |
| `download_volume_guard_commits` | `173f861f-8c5e-4cd6-b6de-b128b8dd2547` | 1 |
| `download_volume_guard_commits` | `a1ebb4aa-f624-4541-a03e-60ccf6ac03e6` | 1 |
| `download_volume_guard_commits` | `4933de9d-f711-45e5-9fec-4b07313b3cda` | 1 |
| `download_volume_guard_commits` | `e8d3a84a-bb50-48fc-9e47-5948e5afd861` | 1 |
| `download_volume_guard_commits` | `6ab70d40-1737-4b75-807f-8ba231f49312` | 1 |
| `download_volume_guard_commits` | `3fd82648-1856-4752-8846-80f41de5e423` | 1 |
| `download_volume_guard_commits` | `570c8078-53f6-42d3-a3a1-08a852a8ff78` | 1 |
| `download_volume_guard_commits` | `7bedecc8-ce36-44e2-991f-123d3956020a` | 1 |
| `download_volume_guard_commits` | `b59a0362-b07e-4bd4-967e-fed96df79498` | 1 |
| `download_volume_guard_commits` | `142b7aa7-fbe8-44ad-aa76-b8a41f380642` | 1 |
| `download_volume_guard_commits` | `df46709b-87c4-4157-9674-0c42587646fe` | 1 |
| `download_volume_guard_commits` | `08005930-ea04-485f-afbe-ca3bb919946b` | 1 |
| `download_volume_guard_commits` | `d6943346-5fbb-4237-b923-31b5b89f3257` | 1 |
| `download_volume_guard_commits` | `8c075c1c-3b75-40c3-b65c-9ed8ca4b69f0` | 1 |
| `download_volume_guard_commits` | `a1bc5015-784e-4d39-92de-767ab17822e2` | 1 |
| `download_volume_guard_commits` | `04107ed0-a23c-4d00-be2d-eb79fc137198` | 1 |
| `download_volume_guard_commits` | `9be9ba1a-5622-405b-83f9-ba66f067d805` | 1 |
| `download_volume_guards` | `transactions` | 39 |
| `download_volume_guards` | `token_transfers` | 122 |
| `investigation_steps` | `` | 347 |
| `provider_usage` | `receipt` | 96,112 |
| `provider_usage` | `balances` | 9 |
| `provider_usage` | `CHAIN_HEAD` | 8,877 |
| `provider_usage` | `account` | 829 |
| `provider_usage` | `logs` | 824 |
| `provider_usage` | `CONTRACT_READ` | 6 |
| `provider_usage` | `BALANCE` | 5,558 |
| `provider_usage` | `transaction_receipt` | 5 |
| `provider_usage` | `CODE` | 47 |
| `provider_usage` | `transactions` | 36,556 |
| `provider_usage` | `RECEIPT` | 36 |
| `provider_usage` | `nft_transfers` | 36 |
| `provider_usage` | `TRACE` | 3 |
| `provider_usage` | `token_transfers` | 246,417 |
| `provider_usage` | `transaction_detail` | 21 |
| `provider_usage` | `contract` | 1,814 |
| `provider_usage` | `probe` | 1,616 |
| `provider_usage` | `internal_transactions` | 14,848 |
| `provider_usage` | `control_plane` | 13 |
| `provider_usage` | `latest_block` | 1,224 |
| `provider_usage` | `block_timestamp` | 1,215 |

---
## 9. 权限模型（append-only 边界）

```sql
SELECT grantee, table_name, string_agg(DISTINCT privilege_type, ',' ORDER BY privilege_type)
  FROM information_schema.role_table_grants
 WHERE table_schema='public' AND grantee <> 'blockchain' GROUP BY 1,2 ORDER BY 1,2;
```

数据库角色（`SELECT rolname FROM pg_roles WHERE rolname NOT LIKE 'pg_%'`）：`blockchain`（库拥有者）、`blockchain_api`、`blockchain_app`。

| 角色 | 被授权表数 | 其中受限（仅 `SELECT` / `INSERT,SELECT`） | 具备 `DELETE` 的表数 |
|---|---:|---:|---:|
| `blockchain_app` | 149 | 14 | 134 |

**仅 `SELECT` / `INSERT,SELECT` 授权的表（append-only 边界，共 14 张）**：

- `blockchain_app.approval_request_events`
- `blockchain_app.audit_chain_anchors`
- `blockchain_app.audit_events_v2`
- `blockchain_app.audit_integrity_incident_anchors`
- `blockchain_app.audit_integrity_incident_events`
- `blockchain_app.audit_integrity_incidents`
- `blockchain_app.login_events`
- `blockchain_app.partition_maintenance_log`
- `blockchain_app.user_activity_events`
- `blockchain_app.user_activity_events_202609`
- `blockchain_app.user_activity_events_202610`
- `blockchain_app.user_activity_events_202611`
- `blockchain_app.user_activity_events_default`
- `blockchain_app.user_activity_events_hist`

> 依据：`backend/migrations/postgres/0043_audit_append_only.sql`（触发器保护，`:24` 抛错文案「audit table % is append-only; % is not permitted (set app.audit_maintenance=on for an approved retention job)」）与 `backend/migrations/postgres/0057_s7_indexes_partitions.sql:290-298`（对 `user_activity_events*` `REVOKE UPDATE, DELETE, TRUNCATE`）。

---

## 10. 未闭合项 / 不可得项（如实清单）

| # | 项目 | 状态 | 原因 | 已尝试的命令 |
|---:|---|---|---|---|
| 1 | **PostgreSQL 磁盘占用** | **已闭合（Revision 2）** | — | `docker exec -u 0 deploy-postgres-1 sh -c 'du -sh /var/lib/postgresql; for d in /var/lib/postgresql/*; do du -sh "$d"; done'` → **24 GiB**，含**逐目录分解**（在线 PGDATA 3.0G / 5×pitr-base 2.7G / 2×pitr-cloneA 2.8G / pitr-clone-rpo1 2.9G / 4×pitr-frozen 33–129M / wal-archive 1.2G） |
| 1b | **NATS `jetstream` 目录占用** | **仍不可得** | 仅改用容器 root 读取了 PostgreSQL；未对 NATS 容器做同等读取 | `du -sh /data/blockchain-platform/nats` → `Permission denied`（未尝试 `docker exec -u 0 deploy-nats-1 du -sh /data`） |
| 2 | ClickHouse `du` 与 `system.parts` 活跃字节差额**归因** | **未闭合**（Revision 2 修正措辞） | Revision 1 曾断言「差额约 17 GiB 来自非活跃/detached/preprocessed/tmp」，属**未经验证的推测**。正确表述：二者**口径不同**（`du` 含非活跃 part、`detached/`、`preprocessed/`、`tmp/` 等），差额**确实存在但未逐项归因**；只读采集不清理、不合并 | `du -sb /data/blockchain-platform/clickhouse` → 40,716,354,145 B（37.92 GiB）；`SELECT database, sum(bytes_on_disk) FROM system.parts WHERE active GROUP BY 1` |
| 2b | `du -sh clickhouse` = 39G 与 `du -sb` = 37.92 GiB **读数不自洽** | **未闭合** | 同一目录两种 `du` 口径差约 1 GiB，`du -h` 的进位/块计算与 `-b` 不一致的原因未查明 | `du -sh /data/blockchain-platform/clickhouse`；`du -sb /data/blockchain-platform/clickhouse`（同一分钟内执行） |
| 3 | MinIO 桶内**历史**版本与删除标记 | **不适用** | 6 个桶全部未启用版本控制（`GET ?versioning` 返回空配置），故无版本条目 | S3 `GET /<bucket>?versions` |
| 4 | 132 张表的保留期定义 | **未定义（不是采集失败）** | 迁移与代码中确实没有这些表的 retention/TTL/partition 定义 | `grep -rn -i -E "retention\|ttl\|partition by\|older than\|interval '" backend/migrations/postgres`（全部命中已列入 §5） |
| 5 | 132 张表的 `data_asset_registry` 登记 | **未登记** | 迁移 `0044`/`0047` 只 UPSERT 了 17 张表 | `SELECT count(*) FROM data_asset_registry` = 17 |
| 6 | `docs/manuals/` 与 `RELEASE-DECISION.md` | **不存在** | 部署机 `/home/euripides/projects/blockchain-platform` 下无此目录/文件 | `ls -la docs/`；`ls -la docs/manuals`；`ls -la RELEASE-DECISION.md` → `No such file or directory`；`find . -maxdepth 3 -iname 'RELEASE-DECISION*'` → 无输出 |
| 7 | PostgreSQL 逐日 `pg_total_relation_size` 历史快照 | **不可得** | PG 不保留体积历史快照。`pg_stat_database.stats_reset` 返回 **NULL**，这只表示**当前未记录该时间戳 / 无法据其判断计数器窗口起点**（Revision 2 修正：**不能**据此断言「从未重置」——NULL 也可能由统计系统初始化方式、版本差异或写入缺失造成，本次未进一步验证成因） | `SELECT stats_reset FROM pg_stat_database WHERE datname='blockchain_platform'` → 空值；未再尝试 `pg_stat_get_db_stat_reset_time()` 等替代接口（列为后续可验证项） |
| 7b | 各表 `n_tup_ins` 计数器的**起点时间** | **不可判断** | 同上：无 `stats_reset` 时间戳即无法界定计数器覆盖的窗口，故本文**未**使用 `n_tup_ins` 推算速率 | `SELECT relname, n_tup_ins FROM pg_stat_user_tables`（仅参考，未用于任何速率计算） |
| 8 | `user_activity_events` 的实际数据量 | **为 0** | 父表与 5 个分区精确 `count(*)` 全为 0；分区框架已建但尚无写入 | `SELECT count(*) FROM user_activity_events` 及各分区 |
| 9 | 22 张空表 | **为 0 行** | 精确 `count(*)` = 0，属正常（预留表/未启用功能） | §3 总览表 |
| 10 | **WAL 归档的持续增长率是否长期成立** | **NOT VERIFIED**（Revision 2 新增） | 实测 `wal-archive` 77 段 / 1,224,738,132 B，跨越 2026-09-12 21:08:46 → 22:09:12（60.4 min）；PG 参数 `archive_mode=on`、`archive_timeout=60`、`wal_keep_size=0`、`wal_level=replica`。**但**该配置是否为 PITR 演练期间的临时值、归档目录由谁在何时清理，**均无法从代码或迁移确认** | `docker exec deploy-postgres-1 psql -c "SELECT name,setting FROM pg_settings WHERE name IN ('archive_mode','archive_command','archive_timeout','wal_level','wal_keep_size')"`；`ls -la --time-style=full-iso /var/lib/postgresql/wal-archive`；`crontab -l`（no crontab）；`ls /etc/cron.d/`（certbot/e2scrub_all/sysstat，**无 WAL 清理任务**）；`systemctl list-timers --all` 过滤 pg/wal/pitr/backup → **无命中** |
| 11 | PITR 演练目录的**保留期限与清理责任方** | **NOT VERIFIED** | 8 个 `pitr-*` 目录（约 20.7 GiB）由 2026-09-12 21:17–22:04 的一次演练产生；无任何 cron/timer/脚本文件被发现负责清理 | `ls -la /data/blockchain-platform/postgres/`；`ls -la scripts/ | grep -iE 'pitr|backup|wal|restore'` → **无命中** |

---

## 11. 附录：完整命令与真实输出摘要

### A. 连接与凭据获取方式（不在本文档记录任何口令明文）

```bash
cd /home/euripides/projects/blockchain-platform
export PGPASSWORD=$(grep -m1 '^POSTGRES_PASSWORD=' deploy/.env | cut -d= -f2-)
docker exec -e PGPASSWORD="$PGPASSWORD" deploy-postgres-1 \
  psql -U blockchain -d blockchain_platform -At -F "$(printf '\t')" -c "<SQL>"
```

ClickHouse：

```bash
export CH_USER=$(grep -m1 '^CLICKHOUSE_USER=' deploy/.env | cut -d= -f2-)
export CH_PASS=$(grep -m1 '^CLICKHOUSE_PASSWORD=' deploy/.env | cut -d= -f2-)
docker exec deploy-clickhouse-1 clickhouse-client --user "$CH_USER" --password "$CH_PASS" -q "<SQL>"
```

MinIO 凭据变量名：`MINIO_ROOT_USER` / `MINIO_ROOT_PASSWORD`，端点 `127.0.0.1:$MINIO_API_PORT`（`deploy/.env`）。

### B. 关键命令与输出

**B1. 版本与库大小**

```
$ psql -Atc "SELECT version();"
PostgreSQL 18.6 (Debian 18.6-1.pgdg13+2) on x86_64-pc-linux-gnu, compiled by gcc (Debian 14.2.0-19) 14.2.0, 64-bit

$ psql -Atc "SELECT pg_size_pretty(pg_database_size('blockchain_platform')) AS db_size, pg_database_size('blockchain_platform') AS bytes;"
861 MB|902608575

$ psql -Atc "SELECT count(*) AS base_tables FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relkind='r';"
148

$ psql -Atc "SELECT c.relkind, count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' GROUP BY 1 ORDER BY 1;"
I	4      -- partitioned index
S	2      -- sequence
i	373    -- index
p	1      -- partitioned table (user_activity_events)
r	148    -- base table
```

**B2. 精确行数（单条 SQL 生成，覆盖全部 149 个关系）**

```sql
SELECT c.relname,
       (xpath('/row/c/text()',
              query_to_xml(format('SELECT count(*) AS c FROM public.%I', c.relname), false, true, '')))[1]::text::bigint
         AS exact_rows
  FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname='public' AND c.relkind IN ('r','p')
 ORDER BY c.relname;
```

结果示例（前 5 行）：

```
access_policies	0
address_case_links	44
address_dataset_sync_state	80
address_tag_links	86
advanced_analysis_results	2
```

**B3. 体积与分区**

```sql
SELECT c.relname, c.relkind, c.reltuples::bigint, pg_total_relation_size(c.oid),
       pg_relation_size(c.oid), pg_indexes_size(c.oid), c.relispartition,
       COALESCE(pg_get_expr(c.relpartbound, c.oid),''), COALESCE(pc.relname,'')
  FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
  LEFT JOIN pg_inherits i ON i.inhrelid=c.oid
  LEFT JOIN pg_class pc ON pc.oid=i.inhparent
 WHERE n.nspname='public' AND c.relkind IN ('r','p')
 ORDER BY pg_total_relation_size(c.oid) DESC;
```

前 5 行真实输出：

```
provider_usage	                r	408316	307732480	159858688	147800064	f
token_metadata_observations	r	231152	156925952	69263360	87605248	f
download_pages	            r	165475	127574016	86155264	41361408	f
audit_events_v2	            r	47427	68845568	35799040	32997376	f
job_event_log	              r	42064	65355776	61407232	3899392	f
```

**B4. 约束与索引**

```sql
SELECT c.relname, con.conname, con.contype, pg_get_constraintdef(con.oid)
  FROM pg_constraint con JOIN pg_class c ON c.oid=con.conrelid
  JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public'
 ORDER BY c.relname, con.contype, con.conname;   -- 2200 行

SELECT tablename, indexname, indexdef FROM pg_indexes
 WHERE schemaname='public' ORDER BY tablename, indexname;   -- 377 行
```

**B5. ClickHouse**

```
$ clickhouse-client -q "SELECT version()"
26.7.5.10

$ clickhouse-client -q "SELECT database, name, engine, sorting_key, partition_key, total_rows, formatReadableSize(total_bytes) FROM system.tables WHERE database='blockchain' FORMAT TSVWithNames"
blockchain  transactions  ReplacingMergeTree  chain, tx_hash  (chain, toYYYYMM(timestamp))  35932520  5.62 GiB
blockchain  token_transfers  ReplacingMergeTree  chain, tx_hash, log_index, token_address  (chain, toYYYYMM(timestamp))  56605708  4.51 GiB
blockchain  address_activity  ReplacingMergeTree  chain, address, timestamp, tx_hash, activity_type, counterparty, token_address  (chain, toYYYYMM(timestamp))  74454629  5.43 GiB
blockchain  logs  ReplacingMergeTree  chain, tx_hash, log_index  (chain, toYYYYMM(timestamp))  11901727  467.00 MiB
blockchain  nft_transfers  ReplacingMergeTree  ...  0  0.00 B        （其余 4 张小表略，见 §6）

$ clickhouse-client -q "SELECT name, path, formatReadableSize(free_space), formatReadableSize(total_space) FROM system.disks"
default  /var/lib/clickhouse/  923.39 GiB  1.01 TiB
```

**B6. MinIO（只读 S3 API 替代 `mc`）**

```
LISTBUCKETS status 200
BUCKETS: ['blockchain-audit', 'blockchain-backups', 'blockchain-exports', 'blockchain-raw', 'blockchain-reports', 'blockchain-reports-drill-20260912t175029z']

=== blockchain-raw
  versioning[200]: <VersioningConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"></VersioningConfiguration>
  lifecycle[404]: <Error><Code>NoSuchLifecycleConfiguration</Code><Message>The lifecycle configuration does not exist</Message>...
  encryption[404]: ServerSideEncryptionConfigurationNotFoundError
  objectlock[404]: ObjectLockConfigurationNotFoundError
  objects=170515 total_bytes=15058046314 total_pretty=14360.47 MiB
  by_day: 2026-08-31:35746/2738817223  2026-09-05:6319/7508775015  2026-09-08:61321/2401351912  2026-09-10:60493/1062677947 ...
```

**B7. 磁盘**

```
$ df -B1 /data
/dev/mapper/ubuntu--vg-data_lv  1105357205504 64576253952 991482462208  7% /data
```

### C. 本次采集对仓库与系统的副作用

- 仓库工作树 `E:\Code\远程虚拟机\harness\blockchain-platform`：**仅新增两个文档文件**（`docs/data-dictionary.md`、`docs/capacity-forecast.md`），未 commit / reset / checkout / clean，未删除任何文件。
- 部署机：未写入任何文件、未新增/删除容器、未重启任何服务。中间证据文件仅落在 Windows 侧 `.work/s31-evidence/` 临时目录。
- 数据库：仅 `SELECT` 与系统表查询，未执行任何 DDL/DML。

