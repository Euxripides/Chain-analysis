# 保护壳快照 SQL 优化 + 超时/可观测性/EXPLAIN 证据（2026-09-14）

对象：`acceptance-evidence/dual-system-v1/tests/run-fault-drill-guarded.sh`（故障注入演练的"用户在工作"保护壳）。

一句话结论：**快照 SQL 从"每 shard 3 个相关子查询"改成"冻结 ID 预聚合 + JOIN"，并用逐字段等价性论证 + 本机执行的结构自检证明"字段与 ID 集合没变"；超时、连接标记、运行期采样、失败分类与 EXPLAIN 证据都已实现。但新 SQL 的真实耗时、真实 EXPLAIN 计划、以及"上一轮到底卡在哪里"的最终定论，仍必须在 VM 上跑一次才能给出 —— 本文件把它们列为待实测项，不写成已验证。**

---

## 0. 本轮背景

- 上一轮真实数据上 before 快照疑似过慢，被人工中止，未产出 PASS/FAIL。
- 监控者实测：中止时 PG 有一个 backend 处于 `Rs` / CPU 81.5%。
- 因此"stdin 死锁"的结论**证据不足**：一个正在 CPU 上跑的 backend 与"客户端从未发出查询"是不相容的。
- 本轮的取舍：不去猜结论，而是让脚本自己产出**能区分下列四种情形**的证据：SQL 真的在执行 / 在等锁 / 在做 IO / 客户端侧挂起（stdin/管道）。

---

## 1. 旧 SQL 的问题

### 1.1 结构：每个 shard 3 个相关子查询

旧 before/after 的 shard 列表这样取数（每个活跃/暂停 shard 各算一次）：

| 列 | 旧表达式 | 每个 shard 的成本 |
|---|---|---|
| `token_hash` | `(SELECT p.next_page_token_hash FROM download_pages p WHERE p.shard_id=sh.id ORDER BY p.page_no DESC LIMIT 1)` | 1 次索引查找（idx_pages_shard） |
| `success_pages` | `(SELECT count(*) FROM download_pages p WHERE p.shard_id=sh.id AND p.status IN ('VALIDATED','INGESTED','DURABLE'))` | 1 次索引范围扫描 |
| `provider_success_pages` | `(SELECT count(*) FROM provider_usage u WHERE u.shard_id=sh.id AND u.status IN ('success','ok'))` | **1 次 provider_usage 全表扫描**（见 1.2） |

目标列里的标量子查询在 PG 里是 SubPlan，**按外层行数逐次执行**；count(*) 这种形式不会被去相关成 hash 聚合。所以总量近似 `N_shard × (一次 provider_usage 扫描)`。

### 1.2 索引面：`provider_usage` 没有 `shard_id` 索引（仓库 DDL 证据）

仓库内所有 `provider_usage` 索引（`backend/migrations/postgres/`）：

| 位置 | 索引 |
|---|---|
| 0001_init.sql:151 | `idx_usage_time (request_time DESC)` |
| 0001_init.sql:152 | `idx_usage_provider (provider, request_time DESC)` |
| 0005_method_abi.sql:95 | `idx_provider_usage_usage_type (usage_type, request_time DESC)` |
| 0010_rpc_infrastructure.sql:104 | `idx_provider_usage_api_type (api_type, request_time DESC)` |
| 0010_rpc_infrastructure.sql:106 | `idx_provider_usage_chain_method (chain, method_class, request_time DESC)` |
| 0024_tron_v51_enrichment_metrics.sql:12 | `idx_provider_usage_trc20_summary (job_id, request_time DESC)` |

**没有任何一条以 `shard_id` 开头**。对照 `download_pages`：`UNIQUE (shard_id, page_no)`（0013_ankr_engine.sql:54）+ `idx_pages_shard (shard_id, page_no)`（:56）+ 部分索引 `idx_pages_status`（:57）。

> 这是"必要条件"级别的证据：**仓库 DDL 里没有可用索引** ⇒ 每个 shard 那次 count 只能全表扫。它**不能**证明 VM 上当时真的走了 Seq Scan（VM 可能有人手工建过索引、也可能 `shard_id` 走 bitmap/hash 计划）。真实计划以 `explain-before.txt` 与 `catalog.txt` 为准。

### 1.3 一个 `-c` 里装下了所有代价

旧 SQL 是单条 `psql -c`：shard 枚举 + 3 个子查询 + `audit_events_v2`/`outbox_events` 的全表聚合全在一个 statement 内，既不能分段计时，也没有 `statement_timeout`（旧版没有设置超时），失败时也没有任何"当时在等什么"的证据。本轮把超时与采样补上（第 5 节）。

### 1.4 旧形态与"Rs / CPU 81.5%"的关系：**假说，未证实**

| 假说 | 与观测是否相容 | 需要的判定证据 | 现在能否定论 |
|---|---|---|---|
| A. SQL 真的在跑（CPU-bound，全表扫 × N） | 相容（active + 无等待事件 + CPU 高） | `explain-before.txt` 的执行耗时/缓冲区 + 新脚本 `pg-stat-activity.txt` 的 active 样本 | 否（需 VM） |
| B. 在等锁（lock/LWLock） | 与 `Rs@@ + 高 CPU 不太相容 | `pg-stat-activity.txt` 里 `wait_event_type=Lock` | 否 |
| C. 在做 IO | 相容 | `wait_event_type=IO` / `explain` 的 `read` 计数 | 否 |
| D. stdin/管道死锁（客户端从未发出查询） | 与 `Rs@@+CPU 高**不相容**（没有后端在执行） | `pg-stat-activity.txt` 里**完全没有** `application_name='drill-guard'` 的行 + `stage-log` 只有 start 没有 end | 否（需 VM 一次完整运行） |

新脚本第 9 条要求：**任何失败都必须落盘分类结论**（`sql-failure-<label>.txt` 里的 `execution_evidence=` 字段），不允许用"大概是死锁"结案。

---

## 2. 新 SQL 的形式（before / after 同构）

骨架（before 用活跃 job 谓词冻结；after 用 before 冻结出来的两个数组 `unnest`）：

    WITH active_jobs AS (            -- before：与旧版逐字相同的 status 谓词
      SELECT id, status, chain, effective_mode FROM download_jobs WHERE status IN (...)
    ),
    frozen_jobs   AS (SELECT id FROM active_jobs),                       -- 冻结 job 集合
    frozen_shards AS (SELECT sh.id FROM download_shards sh
                      WHERE sh.job_id IN (SELECT id FROM frozen_jobs)),  -- 冻结 shard 集合
    page_latest AS (                 -- 每个 shard 只算一次：最新页 token 摘要
      SELECT DISTINCT ON (p.shard_id) p.shard_id, p.next_page_token_hash
      FROM download_pages p
      WHERE p.shard_id IN (SELECT id FROM frozen_shards)
      ORDER BY p.shard_id, p.page_no DESC, p.id DESC
    ),
    page_success  AS (SELECT p.shard_id, count(*) AS success_pages   FROM download_pages p
                      WHERE p.shard_id IN (SELECT id FROM frozen_shards)
                        AND p.status IN ('VALIDATED','INGESTED','DURABLE') GROUP BY p.shard_id),
    usage_success AS (SELECT u.shard_id, count(*) AS provider_success_pages FROM provider_usage u
                      WHERE u.shard_id IN (SELECT id FROM frozen_shards)
                        AND u.status IN ('success','ok') GROUP BY u.shard_id)
    SELECT json_build_object(
      ... 与旧版**同名同序**的字段 ...,
      'shards', (
        SELECT coalesce(json_agg(json_build_object(
          'id', sh.id, 'job_id', sh.job_id, 'status', sh.status,
          'page_no', coalesce(sh.page_no, cp.page_no, 0),
          'rows', coalesce(sh.downloaded_rows, cp.downloaded_rows, 0),
          'token_hash', pl.next_page_token_hash,
          'success_pages', coalesce(ps.success_pages, 0),
          'provider_success_pages', coalesce(us.provider_success_pages, 0)
        ) ORDER BY sh.id), '[]'::json)
        FROM frozen_shards fs
        JOIN download_shards sh ON sh.id = fs.id
        LEFT JOIN checkpoints cp ON cp.shard_id = sh.id
        LEFT JOIN page_latest pl ON pl.shard_id = sh.id
        LEFT JOIN page_success  ps ON ps.shard_id = sh.id
        LEFT JOIN usage_success us ON us.shard_id = sh.id
      )
    )::text;

要点：
- 每个聚合表**每个 shard 一行**，且都按 `shard_id IN (冻结集合)` 过滤 ⇒ 全表只扫一遍（`provider_usage` 从"每 shard 一遍"变成"整表一遍"）。
- 三个 `LEFT JOIN` 都是 1:1（`checkpoints` 主键就是 `shard_id`；三个聚合表按 shard_id 唯一），不会放大行数 —— 这一点由第 3 节逐条论证。
- before 与 after 共用同一形状，只把"冻结集合的来源"从"现网 status 谓词"换成"`unnest(冻结数组)`"。
- `schema_version` 保持 `1`：字段集合与旧版完全一致，只有取数形式变了；如果字段集合变了才应该升版本号（升了会让历史 baseline 不可比）。

---

## 3. 语义等价性论证

### 3.1 ID 集合（不缩小、不跳过任务）

| 集合 | 旧版 | 新版 | 等价理由 |
|---|---|---|---|
| job_ids | `active_jobs = download_jobs WHERE status IN ('RUNNING','QUEUED','PAUSE_REQUESTED','PAUSED','PAUSED_FALLBACK_TOO_EXPENSIVE')` | 同一谓词，逐字未改 | 字符串级相同 |
| before shard 集合 | `download_shards sh WHERE sh.job_id IN (SELECT id FROM active_jobs)` | `frozen_jobs` → `WHERE sh.job_id IN (SELECT id FROM frozen_jobs)` | `frozen_jobs` 就是 `active_jobs` 的 id 投影；`IN` 语义与行集合相同（`download_shards.id` 是主键、`job_id` 单值 NOT NULL，无重复行） |
| after shard 集合 | `unnest(__SHARDIDS__)` = before 的 shards[].id | 同 | 由 before JSON 生成，逐字节相同 |
| outbox 集合 | 全量 `outbox_events.id` | 同 | 未改 |
| **after 的 job/shard 判定** | `jobs_missing`/`shards_missing` + `id = ANY(冻结数组)` | 同 | 未改（仍不按现网 status 重新筛选） |

### 3.2 逐字段等价

| 字段 | 旧表达式 | 新表达式 | 为什么等价 |
|---|---|---|---|
| `token_hash` | `(SELECT next_page_token_hash ... ORDER BY page_no DESC LIMIT 1)` | `DISTINCT ON (shard_id) ... ORDER BY shard_id, page_no DESC, id DESC` | `download_pages` 有 `UNIQUE (shard_id, page_no)`（0013:54）⇒ 每个 shard 的"最大 page_no"**唯一**，无并列可挑；两式取同一行。额外加的 `p.id DESC` 只在约束不存在时才起作用（那时旧式本身就是非确定的），属于防御性确定性。无页 shard：两式都是 NULL |
| `success_pages` | 相关子查询 count(*)（同谓词） | `GROUP BY shard_id`（同谓词） | 同一谓词 + 同一 shard 范围 → 逐 shard 计数相同。左连接未命中时旧式得 0、新式 `coalesce(...,0)` = 0 |
| `provider_success_pages` | 同上 | 同上 | 同上；`u.shard_id IS NULL` 的行在两式里都不计入（`=` 与 `IN` 对 NULL 都不成立） |
| `page_no` / `rows` | `coalesce(sh.page_no, cp.page_no, 0)` 等 | 逐字相同 | 未改 |
| `id`/`job_id`/`status` | `sh.*` | 逐字相同 | 未改 |
| 顶层 `audit_max_seq`/`audit_count`/`outbox_total`/`outbox_pending`/`jobs`/`outbox_ids`/`job_ids` | — | 逐字相同 | 未改（仍是全表聚合，仍在同一个 60s 预算内 → 见第 7 节待实测） |
| after 专有 `outbox_baseline_present`/`jobs_missing`/`shards_missing` | — | 逐字相同 | 未改 |
| 行集合 | `FROM download_shards sh LEFT JOIN checkpoints cp` | `FROM frozen_shards fs JOIN download_shards sh ... LEFT JOIN checkpoints cp` | `checkpoints` 主键 `shard_id`（0001_init.sql:156）⇒ 左连接至多 1 行，不放大；`WHERE sh.id = ANY(...)` 的内连接语义与旧 `WHERE sh.job_id IN (...)` 的行集合一致（同一批 id） |
| 输出顺序 | `json_agg(... ORDER BY sh.id)` | 逐字相同 | 未改（比对脚本按 id 建字典，顺序本就不影响判定） |

### 3.3 本机已执行的结构自检（**已跑，34 项 ALL_OK**）

在 Windows 工作树（无 docker / 无 PG）用假 `docker` 应答器驱动保护壳全流程，把**真正发给 psql 的 SQL 文本**与"编辑前的旧 SQL 文本"做结构比对，34 项断言全部 OK、0 FAIL：

- 顶层字段（名 + 序）before/after 与旧版一致；after = before 去掉 `job_ids`/`outbox_ids` 并加上 `outbox_baseline_present`/`jobs_missing`/`shards_missing`（与旧版同一套差异）。
- shard 投影在"旧 before / 新 before / 旧 after / 新 after"四个形态里都是同一组 8 个字段（与预检 `SHARD_REQ` 一致）。
- 旧形态含 3 个 per-shard 相关子查询；**新 before/after 正文里一个都没有**；旧形态只保留在 `explain-before` 的对照 SQL 里。
- 新 after 的 `unnest` 数组 == before 冻结出来的 job_ids / shard_ids（逐字节），`jobs_missing`/`outbox_baseline_present` 用同一批数组。
- 实际发出的 before/after SQL 第 1 行就是本次 run 标记，紧接 `SET application_name`/`statement_timeout(60s)`/`lock_timeout(5s)` 且在查询之前。

> 这是**结构等价性**检查（文本层面），不是 SQL 运行时等价性证明。运行时的最终判定要靠 VM 上的双跑对照（旧形态 EXPLAIN vs 新形态 EXPLAIN + 两次快照结果逐字段 diff）。

### 3.4 尚未证明的部分（必须 VM 实测）

- 新 SQL 在真实 schema/数据上**能跑通**、真实耗时是多少；
- 新旧两种形态在**同一份真实数据**上返回的 shard 数组逐字段相等（本机只有结构等价 + 1 条夹具数据的形状检查）；
- `DISTINCT ON` 的实际计划：它要求按 `(shard_id ASC, page_no DESC)` 有序，而 `idx_pages_shard` 是 `(shard_id, page_no)` 升序 —— PG 13+ 可以用 Incremental Sort 廉价满足，也可能退化成对"冻结 shard 的页"做一次 Sort。**若 VM 上 EXPLAIN 显示是普通 Sort 且 shard 数很大，可换成 `LEFT JOIN LATERAL (... LIMIT 1)`（每 shard 一次索引反向扫描）**。这是一个**待实测**的备选，本轮没有实施，也没有把它写成已完成。

---

## 4. 索引证据

### 4.1 仓库 DDL 已确认（静态证据）

| 表 | 关键结构 |
|---|---|
| `download_pages` | `UNIQUE (shard_id, page_no)`（0013:54）、`idx_pages_shard (shard_id, page_no)`（:56）、`idx_pages_status (status) WHERE status IN ('DURABLE','FAILED')`（:57） |
| `provider_usage` | 见 1.2：**无 shard_id 索引** |
| `checkpoints` | `PRIMARY KEY (shard_id)`（0001_init.sql:156）、`idx_checkpoints_job (job_id)`（0057:25） |
| `download_shards` | `PRIMARY KEY (id)`、`idx_shards_job (job_id)`（0001_init.sql:108） |

规模量级来自仓库内已记录的一次实测（0057 迁移头注释，2026-09-12）：`provider_usage 414663 行、download_pages 165469 行、checkpoints 19962 行`。**这是历史记录，不是本轮实测**。

### 4.2 VM 实测（脚本已采集，尚未运行）

保护壳新增 `catalog` 只读阶段，把下列内容写进 `$OUT/catalog.txt`（不参与判定）：`pg_indexes` 的真实 `indexdef`、`pg_stat_user_tables` 的 `n_live_tup/seq_scan/idx_scan/last_analyze`、`pg_total_relation_size`、以及 `server_version/shared_buffers/work_mem/effective_cache_size/max_parallel_workers_per_gather/random_page_cost`。**没有这份文件之前，第 1 节的"没有可用索引"只是仓库证据。**

### 4.3 未实施的候选优化（不要当成已做）

    -- 候选（本轮未执行，需评审 + 负载窗口）：让"按 shard 取成功页计数"也能走索引
    CREATE INDEX CONCURRENTLY idx_provider_usage_shard
      ON public.provider_usage (shard_id) WHERE status IN ('success','ok');

不做它的理由：它是 DDL（改用户库结构、要占 IO 与锁窗口、要单独回滚计划），而新 SQL 已经把"每 shard 一次全表扫"压成"整表一次聚合"，先量测再决定。**本文件不主张它已存在或已创建。**

---

## 5. 超时与诊断策略

1. **连接标记**：所有 psql 调用 `docker exec -e PGAPPNAME=drill-guard ... psql -X ...`，且每条 SQL 自己以
   `/* drill-guard-run:<UTC>-<PID> */ SET application_name='drill-guard'; SET statement_timeout='60s'; SET lock_timeout='5s';` 开头。
   `psql -c` 每次都是独立会话，所以 SET 必须与查询在**同一次** `-c` 里（脚本里由 `render_sql` / python 生成器统一注入，缺了会自动补）。
   `-X` 用于忽略容器里的 `.psqlrc`，保证输出可复现。
2. **两层超时**：服务端 `statement_timeout=60s`、`lock_timeout=5s`；客户端外层 `timeout --kill-after=10s 180s`（`GUARD_PSQL_HARD_TIMEOUT_S`）。后者覆盖"服务端超时管不到"的客户端/管道挂起。
3. **阶段日志**：`$OUT/stage-log.txt`，每行 `stage=<名> event=<start|end> t=<ISO毫秒> epoch_ms=<毫秒> elapsed_ms= elapsed_s= ...`。覆盖 `preflight_db / catalog / before_sql / explain_new / suite / after_sql / compare / explain_old / verdict / guard`。
4. **运行期采样**：SQL 未返回期间每 `GUARD_SAMPLE_INTERVAL_S`（默认 2s，上限 `GUARD_SAMPLE_MAX` 次）执行一次
   `SELECT pid, application_name, state, wait_event_type, wait_event, query_start, now()-query_start AS dur, left(query,200) FROM pg_stat_activity WHERE application_name='drill-guard'`，
   **原始输出**追加到 `$OUT/pg-stat-activity.txt`（`#` 开头的行为本脚本加的标签注释）。采样器自己叫 `drill-guard-sampler`，不会自采样。
5. **失败分类**：`$OUT/sql-failure-<label>.txt` 记录 `rc`、`timeout_kind`（`statement_timeout / lock_timeout / deadlock / outer_hard_timeout(124) / outer_hard_kill(137) / connection_error / sql_error / empty_output`）、`guard_backend_observed_in_samples`、`observed_state_wait_joint`（如 `active||(4)`）与 `execution_evidence=`（`executing / lock_wait / io_wait / lwlock / bufferpin / no_guard_backend_observed`）。原始 stdout/stderr 一律保留。
6. **INVALID 语义**：before 阶段 SQL 失败/超时 ⇒ 判 INVALID 且**不运行故障套件**；after 阶段失败 ⇒ 判 INVALID（套件结果记在 rc 字段里）；**绝不把超时当成 0/0 的 PASS**，也不把空输出当成"没有任务"。
7. **取消规则（唯一允许的取消动作）**：`pg_cancel_backend(pid)`，且同时满足 ① `application_name='drill-guard'` ② `query` 含**本次 run 标记** ③ `query` 不含任何写动词（insert/update/delete/truncate/drop/alter/create/grant/revoke/copy/vacuum/refresh）。绝不 `pg_terminate_backend`、绝不按 pid 猜、绝不触碰用户作业、绝不清理任务/对象/锁。触发点：SQL 失败（含外层硬超时后可能仍在跑的后端）与 INT/TERM。
8. **不改变既有约束**：保护壳不改业务数据（只有 SET 与只读 SELECT/WITH）；不动维护持有位（既有 hold 一律沿用）；不代替套件做恢复（DATA-007 的恢复责任仍在套件自己的 trap 里）；所有 psql 调用 ` < /dev/null` 且 `docker exec` 不带 `-i`，从结构上排除 2026-09-13 的 stdin 管道死锁。
9. **配置开关**：`GUARD_STATEMENT_TIMEOUT(60s)`、`GUARD_LOCK_TIMEOUT(5s)`、`GUARD_PSQL_HARD_TIMEOUT_S(180)`、`GUARD_SAMPLE_INTERVAL_S(2)`、`GUARD_SAMPLE_MAX(120)`、`GUARD_EXPLAIN_NEW(1)`、`GUARD_EXPLAIN_NEW_GATING(1)`、`GUARD_EXPLAIN_OLD(1)`、`GUARD_EXPLAIN_TIMEOUT(60s)`、`GUARD_EXPLAIN_HARD_TIMEOUT_S(180)`。

### 5.1 EXPLAIN 证据的两个阶段

| 文件 | 形态 | 何时跑 | 是否影响判定 |
|---|---|---|---|
| `explain-after.txt` | **优化后**（冻结 ID 预聚合 + JOIN），`FROM frozen_shards fs JOIN ...` | 快照之后、**套件之前** | 默认 `GUARD_EXPLAIN_NEW_GATING=1`：跑不动即 INVALID 且不跑套件（与"SQL 失败/超时 ⇒ INVALID"一致）；可显式关掉 |
| `explain-before.txt` | **优化前**（逐 shard 相关子查询，本轮被替换掉的原始写法，行集合用冻结 ID 固定） | **verdict 之后**（诊断性质） | 永不影响 rc：旧形态很慢，绝不能让它挡住演练或改变结论 |

两份文件都带 `EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT)` 原始输出 + `rc / timeout_kind / elapsed_ms / sql_sha256` 头。若被开关关闭或冻结 shard 集合为空，会写一份 `rc=n/a reason=...` 的说明文件，**明确标注"本文件不是证据"**，不会伪造计划。

---

## 6. 退出码链路

    systemd 瞬态单元  →  scripts/s7/run-guarded-sprint5-collector.sh  →  run-guarded-fault-suite.sh  →  冻结的 guard.sh

| 环节 | 现状 | 本轮动作 |
|---|---|---|
| guard | 所有路径都写 `drill-guard-verdict.json`；`EXIT` trap 兜底（没写结论就异常退出 ⇒ 落一份 INVALID 并 rc=3）；返回结论自己的 rc：0=PASS 1=FAIL 2=REVIEW 3=INVALID | 新增 trap 兜底、`rc` 字段写入 verdict、套件失败与比较脚本 PASS 冲突时强制 FAIL(1) 并回写 json |
| run-guarded-fault-suite.sh | `RC=$?` → `exit "$RC"`（已在） | 补 rc 契约注释（禁止 `...; echo EXIT=$?` 式包装）、把 verdict 镜像进 run 目录并记入 manifest、rc 越界只告警不修正 |
| run-guarded-sprint5-collector.sh | `RC=$?` → `exit "$RC"`（已在） | `cd` 失败由 exit 2 改为 **exit 3**（2 在契约里是 REVIEW，会误导）；rc 同时打到 stdout 与日志；rc 越界只告警不修正 |

本机自测（假 docker）确认的实际 rc：正常 ⇒ 0/PASS；套件 rc=1 ⇒ 1/FAIL；after 计数下降 ⇒ 1/FAIL；before 语句超时 ⇒ 3/INVALID（套件未运行）；外层硬超时 rc=124 ⇒ 3/INVALID（套件未运行）；注入 `exit 9`（不写结论路径）⇒ EXIT trap 落 INVALID、进程 rc=3。

---

## 7. 仍需 VM 实测才能定论（逐条，别提前下结论）

1. **新 SQL 的真实耗时**：before/after 两次 `stage-log.txt` 的 `elapsed_s`。在没有这组数字之前，不要说"已经快了"。
2. **两份 EXPLAIN**：`explain-before.txt` / `explain-after.txt` 的节点形态、`Buffers`、`Execution Time`；特别要确认新形态里 `usage_success` 是"整表一次聚合"（HashAggregate/Seq Scan 一次）而不是又变成逐 shard 子计划。
3. **旧形态是否真的超过预算**：如果 `explain-before.txt` 显示旧形态其实很快（<1s），那么"快照慢"的结论必须重新表述（瓶颈就转移到 `audit_events_v2`/`outbox_events` 的全表聚合或 docker/psql 链路）。
4. **60s 是否够**：`audit_count/audit_max_seq/outbox_total/outbox_ids` 这几项仍是全表聚合，它们与 shard 部分共用同一个 60s。`catalog.txt` 里的行数与 `stage-log` 的耗时一起看；不够就调 `GUARD_STATEMENT_TIMEOUT`（但那要作为一次显式决策记录）。
5. **VM 上真实的索引集合**：`catalog.txt` 的 `pg_indexes` 输出。若 VM 上其实存在 `provider_usage(shard_id)` 索引，则第 1 节的成本模型要改。
6. **中止时那个 `Rs`/CPU 81.5%` 的归属**：用 VM 上一次带采样的运行里 `pg-stat-activity.txt` 的 `active||` 样本 + `stage-log` 的 start/end 时间戳来坐实（或者反过来，用"完全没有 guard 后端"的采样把 stdin 假说坐实）。
7. **新旧形态在同一份真实数据上的逐字段一致性**：先跑旧形态快照（本机夹具之外的正式数据）与新版快照各一次，直接 diff 两个 JSON 的 `shards` 数组。结构等价不等于数值等价，这一步不能省。
8. **`DISTINCT ON` 的计划**（第 3.4 节的 Sort / Incremental Sort 问题）与是否需要改用 `LEFT JOIN LATERAL ... LIMIT 1`。
9. **rc 链路**：systemd 单元的 `ExecMainStatus` 是否等于本次 guard 的结论 rc（用 `systemctl show -p ExecMainStatus` 或 journal 里的 `GUARDED_SUITE_RC=` 对照）。
10. **一次完整的 PASS/FAIL**：本文件与自测都不能替代它。上一轮被人工中止的演练需要重新跑出结论。

---

## 8. 本机已验证 / 未验证（对照表）

| 项 | 状态 | 依据 |
|---|---|---|
| 三个脚本的语法与 LF 行尾 | **已在本机验证** | `bash -n` 通过；`CR=0`（防 CRLF 破坏 bash） |
| 保护壳全流程与 rc/verdict 映射 | **已在本机验证**（假 docker 应答器） | 5 个场景 + trap 场景，见第 6 节 |
| 失败分类与采样落盘 | **已在本机验证** | 假应答器返回 `active||` 样本 ⇒ `timeout_kind=outer_hard_timeout`、`execution_evidence=executing`；statement_timeout 场景 ⇒ `timeout_kind=statement_timeout` |
| 新旧 SQL 的结构等价（字段/ID 集合/子查询形态） | **已在本机验证**（34 项断言 ALL_OK） | 第 3.3 节；比对对象是"自测运行里真正发出的 SQL"与"编辑前旧文本" |
| `provider_usage` 无 shard_id 索引 | **仓库证据**（静态） | 第 1.2 节的迁移清单 |
| VM 上的真实索引集合 / 表规模 | **未验证** | 等 `catalog.txt` |
| 新旧 SQL 的真实计划与耗时 | **未验证** | 等 `explain-before/after.txt` |
| 新旧 SQL 的运行时数值等价 | **未验证** | 第 7 节第 7 条 |
| "上一轮卡在 CPU-bound 全表扫" | **假说** | 第 1.4 节 |
| 结束 stdin 死锁假说 | **未验证** | 第 7 节第 6、10 条 |

---

## 9. 变更文件与新增产物

变更（本轮只改脚本与本文档）：

- `acceptance-evidence/dual-system-v1/tests/run-fault-drill-guarded.sh`（快照 SQL 重写 + 超时/观测/诊断/EXPLAIN + verdict/rc 全覆盖；保留既有教训注释并补记本轮发现）
- `acceptance-evidence/dual-system-v1/tests/run-guarded-fault-suite.sh`（rc 契约注释 + verdict 镜像 + 越界告警）
- `scripts/s7/run-guarded-sprint5-collector.sh`（cd 失败 rc 改为 3、rc 打到 stdout、越界告警）
- 本文档

一次运行会在 `$DRILL_OUT` 下新增/更新：

| 文件 | 用途 |
|---|---|
| `stage-log.txt` | 阶段时间戳与耗时（秒 + 毫秒） |
| `pg-stat-activity.txt` / `.err` | 运行期采样原始输出 |
| `catalog.txt` | VM 实测的索引定义、表规模、参数 |
| `preflight-db.txt` / `preflight.txt` | 连通性预检 / 冻结 ID 统计 |
| `baseline-before.json` / `baseline-after.json` | 两次快照（含 sha256 记入 verdict 的 `evidence`） |
| `_guard_after.sql` / `_guard_explain_new.sql` / `_guard_explain_old.sql` | 自描述的实际执行 SQL（带会话标记与 SET 前置） |
| `explain-after.txt` / `explain-before.txt` | 优化前/后的 EXPLAIN (ANALYZE, BUFFERS) 原始输出 |
| `sql-failure-*.txt` / `cancel-own-queries.txt` | 失败分类与"取消了哪些自己的只读查询" |
| `drill-guard-verdict.json` / `SUMMARY.txt` / `guard-self.txt` | 结论、汇总、脚本自证（sha256/参数/标记） |
