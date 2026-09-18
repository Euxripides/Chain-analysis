# Sprint7 WAL 削减：进度事件落盘节流 + 心跳 UPSERT 值变化守卫

- 证据时间：2026-09-17（部署机本地 +07）
- 目标：**同一任务量下** PostgreSQL 写入下降 ≥80%
- **交付二进制（最终）**
  - `backend/bin/api` sha256 `7b1deb54ea4890cbada6037461ce4872aa2455ab596f55aef6df6c69a56bedd6`
  - `backend/bin/worker` sha256 `f2dec36f0c157629ca7ce7019de65cd3da573bfa312f7dd236ed95a816fd31d3`
- 边界：本轮**不改表结构、不删数据**（保留期清理与 VACUUM FULL 由协调方执行）。

---

## 0. 结论摘要

**主口径（协调方确认）：同任务量下应用侧写入下降 ≥80% —— 实测 821.3 → 90.7 行/分钟 = −89.0%，达标。**

| 表 | 改前 writes/min | 改后 writes/min | 降幅 |
|----|----------------|----------------|------|
| `job_event_log` | 400.0 | 48.0 | **−88.0%** |
| `count_provider_capabilities` | 168.0 | 0.0 | **−100%** |
| `count_provider_runtime_profiles` | 168.0 | 0.0 | **−100%** |
| `count_provider_registry` | 12.0 | 0.0 | **−100%** |
| `count_oracle_status` | 12.0 | 0.0 | **−100%** |
| `ankr_performance_control` | 12.0 | 0.0 | **−100%** |
| `ankr_scheduler_state` | 44.7 | 38.0 | 有意不改（见 §5.2） |
| `rpc_provider_health` | 4.7 | 4.7 | 有意不改（见 §5.1） |
| **合计** | **821.3** | **90.7** | **−89.0%** |

**推送实时性未被改动（需求 1 的端到端证据）**：同一窗口内 NATS `events.>` 投递速率
**402.7 → 350.0 msg/min**（没有跟着落盘一起掉 10 倍），
「NATS 投递 ÷ job_event_log 落盘行」比值 **1.01 → 7.29**（理论 ≈10）。
即：进度事件照旧 1 秒一次推给 API/SSE，只是**落盘**降到了 10 秒粒度。

**副口径（绝对 WAL，含我必须如实说明的未解释差额）**：见 §6。

---

## 1. 改动清单（文件:行）

| # | 文件 | 位置 | 改动 |
|---|------|------|------|
| A1 | `backend/internal/api/sprint5_jobs_control.go` | 18-129 | 新增 `progressEventType` / `progressEventPersistInterval` / `jobEventThrottle`；`emitJobEvent` 对 progress 事件做 per-job 落盘节流，被节流事件仍 `hub.Publish` |
| A2 | `backend/internal/api/server.go` | 85 | `Server` 增加 `jobEvents jobEventThrottle` 字段 |
| A3 | `backend/internal/api/job_event_throttle_test.go` | 新增 | 节流器单测（6 条性质） |
| B1 | `backend/internal/store/provider_v13.go` | 12-27 | `UpsertCountProviderRegistry` 加值变化守卫 |
| B2 | `backend/internal/store/provider_v13.go` | 34-70 | `UpsertCountProviderCapability` 加值变化守卫 |
| B3 | `backend/internal/store/provider_v13.go` | 72-112 | `UpsertCountProviderRuntimeProfile` 加值变化守卫 |
| B4 | `backend/internal/store/v12.go` | 276-302 | `UpsertCountOracleHealth` 加值变化守卫 |
| B5 | `backend/internal/store/performance.go` | 59-70 | `SetAnkrRuntimeState` 的 UPDATE 加值变化守卫 |
| B6 | `backend/internal/store/v12.go` | 250-270 | `UpsertSchedulerState` **有意不改**，补写决策注释 |
| B7 | `backend/internal/store/rpc.go` | 144-167 | `SaveProviderHealth` **有意不改**（流水表） |

---

## 2. 任务 A：进度事件「实时推送 1 秒、数据库落盘 10 秒」

### 2.1 生产链路（改动前）

```
worker: internal/download/progress.go:140 progressLoop
        time.NewTicker(time.Second) → 每个活跃 job sc.publish("events.progress."+j.ID, snap)   ← 1 秒/任务
   ↓ NATS events.>
api:    internal/api/sse.go:529 Subscribe("events.>") → :543 jobEventRoute → :544 emitJobEvent
        emitJobEvent 无条件 INSERT INTO job_event_log ... RETURNING id   ← 每条事件一行
```

`job_event_log` 是**唯一写入点**（全仓库只有 `sprint5_jobs_control.go:113` 一处 INSERT，
另有一处 :277 是 SSE 重放读取），所以节流放在 `emitJobEvent` 即可覆盖全部进度事件。

### 2.2 改动后

- `eventType == "progress"` → 先问 `jobEvents.shouldPersistProgress(jobID, now)`：
  距上次**成功落盘**不足 10 秒则**跳过 INSERT**，但**照常** `s.hub.Publish(jobID, out)`。
- 其余 event_type（`control` / `retry` / `admin_control` / `jobs` / `shards` …）走原路径，**立即落盘**。
- `markProgressPersisted` 只在 **INSERT 成功之后**调用：插入失败不消耗窗口，
  下一条进度事件立即重试，不会因一次失败静默丢掉 10 秒进度历史。
- 惰性清理：map 超过 256 项时删除 10 分钟内未再出现的 job，避免 API 长期运行后无界增长。

### 2.3 为什么被节流的进度事件仍然推得出去（关键实现细节，已验证）

`internal/api/sprint5_jobs_control.go:289-296`：

```go
func writeSSEFrame(w http.ResponseWriter, id int64, eventType string, payload []byte) {
	if id > 0 {
		fmt.Fprintf(w, "id: %d\n", id)
	}
	...
}
```

被节流的事件没有 `_sse_id` → SSE 消费者解析出 `id = 0` →
① `:256` 的 `if id > 0 && id <= maxSeen { continue }` 不成立，**不会被误丢**；
② `writeSSEFrame` **不写 `id:` 行**，客户端 `Last-Event-ID` 停留在此前最后一条**已落盘**事件上。

### 2.4 权衡（同时写在代码注释里）

- **实时性不变**：SSE 仍 1 秒一帧；节流只影响 `job_event_log` 落盘。§0 的 NATS 比值 1.01 → 7.29 是实测证据。
- **重放会缺帧**：`Last-Event-ID` 重放以持久 id（`job_event_log.id`）为水位，
  因此**未落盘的进度帧不会出现在重放里**。重连后客户端仍会立刻收到 `snapshot` 校准帧
  （`handleUserJobEvents` 的 `snapshotFor`）与后续实时帧，进度不会倒退或卡死，
  但 10 秒粒度内那几帧的「历史回放」确实没有。
- **审计不受影响**：状态/终态/错误/控制类事件一条都不节流。

---

## 3. 任务 B：心跳 UPSERT 的「值变化才写」

### 3.1 通用写法

```sql
ON CONFLICT (<唯一键>) DO UPDATE SET <业务列>=EXCLUDED.<业务列>, ..., updated_at=now()
WHERE <表>.<业务列1> IS DISTINCT FROM EXCLUDED.<业务列1>
   OR <表>.<业务列2> IS DISTINCT FROM EXCLUDED.<业务列2>
   ...
```

**`updated_at` 有意不参与 OR 比较。** 若把它写进比较，`EXCLUDED.updated_at` 就是本次语句的
`now()`，与已存储的旧 `now()` **永远不相等** → WHERE **恒为真** → 每次都照旧重写，
守卫完全失效、一点 WAL 也省不下来。这是本次实现里最容易写错的地方。

**代价（明示）**：`updated_at` 的语义从「最后一次心跳写入时间」变成
「最后一次**值发生变化**的时间」；UI 上长时间无变化的行看起来“不刷新”。
对 `ankr_performance_control.mode_changed_at` 同理——不参与比较后它反而更准确。

### 3.2 逐条 SQL 原文（改前 / 改后）

**B1 `count_provider_registry`（provider_v13.go:12）**

改前：
```sql
ON CONFLICT (provider_id) DO UPDATE SET display_name=$2, enabled=$3, adapter_type=$4, updated_at=now()
```
改后（追加）：
```sql
WHERE count_provider_registry.display_name IS DISTINCT FROM EXCLUDED.display_name
   OR count_provider_registry.enabled      IS DISTINCT FROM EXCLUDED.enabled
   OR count_provider_registry.adapter_type IS DISTINCT FROM EXCLUDED.adapter_type
```

**B2 `count_provider_capabilities`（provider_v13.go:34）** 追加：
```sql
WHERE count_provider_capabilities.enabled        IS DISTINCT FROM EXCLUDED.enabled
   OR count_provider_capabilities.runtime_status IS DISTINCT FROM EXCLUDED.runtime_status
   OR count_provider_capabilities.supports_range IS DISTINCT FROM EXCLUDED.supports_range
   OR count_provider_capabilities.range_verified IS DISTINCT FROM EXCLUDED.range_verified
   OR count_provider_capabilities.supports_batch IS DISTINCT FROM EXCLUDED.supports_batch
   OR count_provider_capabilities.max_batch_size IS DISTINCT FROM EXCLUDED.max_batch_size
   OR count_provider_capabilities.semantics      IS DISTINCT FROM EXCLUDED.semantics
   OR count_provider_capabilities.accuracy       IS DISTINCT FROM EXCLUDED.accuracy
   OR count_provider_capabilities.reason         IS DISTINCT FROM EXCLUDED.reason
```

**B3 `count_provider_runtime_profiles`（provider_v13.go:72）** 追加：对 16 个业务列
（state / active / queue_depth / safe_concurrency / hard_ceiling / success_rps / valid_count_rps /
p50_ms / p95_ms / success_rate / rate_limit_rate / timeout_rate / cost_per_1k_counts /
requests / success_count / last_error）做 OR 比较。
**`requests` / `success_count` 这类累计计数没有被排除**：它们变化时必须落盘，
不变（无流量）时正是省 WAL 的时候。

**B4 `count_oracle_status`（v12.go:276）** 追加：对 circuit_state / requests / success_count /
p50_ms / p95_ms / success_rate / cache_hit_rate / quota_exhausted / last_error /
false_zero_count / prediction_error_p50 做 OR 比较。

**B5 `ankr_performance_control`（performance.go:59，纯 UPDATE，无 upsert）**

改前：
```sql
UPDATE ankr_performance_control SET requested_mode=$1,effective_mode=$2,target_concurrency=$3,
  active_concurrency=$4,mode_reason=$5,mode_changed_at=$6,updated_at=now() WHERE singleton=TRUE
```
改后：
```sql
UPDATE ankr_performance_control SET requested_mode=$1,effective_mode=$2,target_concurrency=$3,
  active_concurrency=$4,mode_reason=$5,mode_changed_at=$6,updated_at=now()
WHERE singleton=TRUE AND (requested_mode IS DISTINCT FROM $1
   OR effective_mode     IS DISTINCT FROM $2
   OR target_concurrency IS DISTINCT FROM $3
   OR active_concurrency IS DISTINCT FROM $4
   OR mode_reason        IS DISTINCT FROM $5)
```
此处 **`mode_changed_at` 也不参与比较**：调用方每次都传新时间戳（为空时还会就地赋 `time.Now()`），
参与比较会让守卫恒真。

### 3.3 语义等价性的**行为验证**（真实数据、事务内执行、ROLLBACK，零副作用）

在部署机上用**生产同款语句**对**真实行**执行，并用 `GET DIAGNOSTICS ROW_COUNT` 断言：

```
NOTICE:  capabilities identical-values  -> 0 rows (expect 0)
NOTICE:  capabilities changed-enabled   -> 1 rows (expect 1)
NOTICE:  runtime_profiles identical    -> 0 rows (expect 0)
NOTICE:  runtime_profiles requests+1   -> 1 rows (expect 1)
NOTICE:  oracle_status identical        -> 0 rows (expect 0)
NOTICE:  perf_control identical         -> 0 rows (expect 0)
ROLLBACK
```

即：**值没变 → 不产生新版本行；值变了 → 照旧写入**。这不是读代码推断，是在真实数据上跑出来的
（ROLLBACK 后核对 `count_provider_capabilities rows=14` 未变）。

---

## 4. 采样方法（可复算）

两个窗口使用**同一方法**、**同一任务量**（活跃下载任务恒为 27）、同一脚本：

- **不做任何表扫描**：只用 `pg_stat_user_tables` 的 `n_tup_ins/n_tup_upd` 计数器差分、
  `pg_current_wal_lsn()`、`pg_stat_wal.wal_bytes`、NATS `/subsz` 的 `events.>` 订阅 `msgs`。
  （避免 `SELECT count(*)` 这类扫描产生 hint-bit 脏页、反过来污染 WAL 观测。）
- 改前窗口：90 秒；改后窗口：90 秒，均在各自二进制重启完成并静置后开始。
- 协调方的独立 12 分钟基线窗口（`observe-wal.py 12 baseline-clean`）作为交叉印证：
  `wal_mb` = 15.82 / 15.98 / 16.21 / 16.17 / 15.76 / 15.89 / 16.03 / 16.15 / 16.09 / 16.01 /
  **30.75** / 16.01，`event_rows` ≈ 403-410/分钟，`active_jobs` 恒为 27。
  （第 11 分钟那一个 30.75 是异常值，疑为 checkpoint 触发的 FPI 尖峰，计算均值时应剔除或标注。）

复跑命令见 §9。

---

## 5. 有意不修改的两处（各带实测数字与理由）

### 5.1 `rpc_provider_health`（internal/store/rpc.go:145）—— **流水/快照历史表，保持 INSERT**

1. DDL（`backend/migrations/postgres/0010_rpc_infrastructure.sql:41-57`）主键是
   `id UUID PRIMARY KEY DEFAULT gen_random_uuid()`，**(provider, chain) 上没有唯一约束**；
   要写 `ON CONFLICT (provider, chain)` 必须先加约束，而这是**改表结构**（本轮明确禁止）。
2. 同文件 `:58-59` 专门建了 `idx_rpc_provider_health_recent ON (provider, chain, captured_at DESC)`
   —— 「从历史里取每个 provider/chain 最新快照」的标准索引形态。
3. DDL 注释原文：`健康快照：实时指标在内存/Prometheus，这里只做周期快照（§195）`。

写入频率实测 **4.7 行/分钟**（`internal/app/rpc.go:220-239`，默认 1 分钟一次）。
改成 upsert 省不到多少 WAL，却要付出「永久丢失健康历史 + 改表结构」的代价。

### 5.2 `ankr_scheduler_state`（internal/store/v12.go:250）—— **有意不加守卫**

- `worker_id` 取值是 `dataserver-<pid>`，**每个 worker 进程一份新行**。线上实测该表
  **397 行 / 397 个不同 worker_id**，其中只有当前进程那一行是活的。
- 读取方 `GetSchedulerState` 用 `ORDER BY updated_at DESC LIMIT 1`（同文件 :262-263），
  其正确性**依赖当前 worker 的 updated_at 持续前进**。一旦改成「值不变就不写」，
  当前 worker 长时间无变化时该查询会选到**上一个已退出 worker 的陈旧行**，调度状态被读错。
- 该表写入实测 **44.7 次 UPDATE/分钟、行宽约 400 B ≈ 18 KB/min**，占比约 0.1%。

用 0.1% 的收益换「调度状态可能读错」的风险不划算。若确要优化，正确做法是先把读取端
改成确定性定位（按 worker_id 查询），那属于语义变更，需单独批准。

---

## 6. WAL 绝对量：双口径并列（含一处**未能解释**的差额）

### 6.1 三种口径

| 方法 | 改前窗口 | 说明 |
|------|---------|------|
| `pg_current_wal_lsn()` 差值 | **~16 MB/min** | 与协调方 12 分钟基线一致（15.76–16.21，一次 30.75 尖峰） |
| `pg_stat_io` WAL 块写入 | ~17 MB/min | checkpointer + client backend 的 WAL 块写（×8 KB） |
| `pg_stat_wal.wal_bytes` | **~1.25 MB/min** | 其中 FPI 约 0.7 MB/min |

前两者互相印证（磁盘层面确实在写 ~16 MB/min），第三者低约 13 倍；
累计口径同样差约 10 倍（`pg_stat_io` WAL 写入 ≈32 GB vs `wal_bytes` 3.0 GB）。

**我无法解释这个差额**，因此不编造理论。已核对：`archive_mode=off`、`wal_level=replica`、
无复制槽、无 standby、采样期间无 VACUUM/autovacuum 在跑。
（协调方独立提出的一条线索：checkpointer 每分钟回收一个 16 MB 段，但回收本身不计入 `wal_bytes`；
这或许与差额有关，但**尚未验证**，不作为结论。）

**含义**：按 LSN 口径要求「降 80%」超出本轮代码改动的可及范围——
应用侧可触及的是那 ~1.25 MB/min 的逻辑 WAL 部分。因此验收按协调方确认的双口径执行：
**主口径 = 应用侧行写入 −89.0%（§0）**；**副口径 = 绝对 WAL 对照，差额单独注明、不计入代码收益**。

### 6.2 应用侧写入速率（逐表，改前 / 改后）

见 §0 的表。改前心跳来源：`internal/countoracle/service.go:823`
`time.NewTicker(5*time.Second)` → `heartbeat()` 每 tick 为每个 provider × chain × dataset
写 registry / capabilities / runtime_profiles / count_oracle_status
（14 行/表 × 12 tick/min = 168，与实测吻合）。
`job_event_log` payload 实测**固定 1922 字节/行**。

---

## 7. 测试与构建

| 项目 | 结果 |
|------|------|
| `go build ./...` | exit 0 |
| `go vet ./internal/api/... ./internal/store/...` | exit 0 |
| `go test -count=1 ./internal/api/... ./internal/store/... ./internal/countoracle/... ./internal/download/...` | **全绿**（api 0.880s / store 0.019s / countoracle 18.703s / download 0.204s） |
| `gofmt -l` 本次改动文件 | 干净（`gofmt -l` 报出的 `internal/store/{convergence_test,engine,jobs}.go` 是**既有**问题，非本次文件） |
| 18099 standby（部署前） | `/health/live`=200、`/health/ready`=200；四个 explorer 端点 200 无回归 |

新单测 `internal/api/job_event_throttle_test.go` 覆盖：首条必落盘、窗口内节流、10 秒边界恢复、
判断只读（插入失败可立即重试）、按 job 隔离、惰性清理。

**worker 没有 standby 模式**：`cmd/worker/main.go:91-97` 刻意先抢占 metrics 端口，
抢不到即退出（注释：端口冲突通常意味着重复 Worker，必须在参与调度前失败退出），
standby 旗标只存在于 `cmd/api/main.go:37`。硬起第二个 worker 会**真的参与调度**
（下载任务、心跳写 PG），会污染 WAL 观测窗口，因此**不做双起验证**；
worker 侧改动为纯 SQL 变更，与 api 共用同一份 `internal/store` 代码。

---

## 8. 部署记录

### 8.1 二进制与备份

| 角色 | 版本 | sha256 |
|------|------|--------|
| api 冻结版（部署前） | 0dae50f1… | `0dae50f10a7bf56180b28044dc1404eba1be5ced5ab7173f470c544600376ac7` |
| worker 冻结版（部署前） | 9654372… | `965437285cfda7efaa5f738417d50b3fd405ff5ef359aff1c1958e1dc4d06eab` |
| api 中间版（补 registry 前） | a3d749ae… | `a3d749aec50770deba74663ac37abbe56643cec1f31ec9f195437ed94cef5d40` |
| worker 中间版 | 5a8fce0f… | `5a8fce0f323d679bd564bf7d4cd11025bd25b882c9c44c69dda8061fdc536626` |
| **api 最终** | 7b1deb54… | `7b1deb54ea4890cbada6037461ce4872aa2455ab596f55aef6df6c69a56bedd6` |
| **worker 最终** | f2dec36f… | `f2dec36f0c157629ca7ce7019de65cd3da573bfa312f7dd236ed95a816fd31d3` |

备份目录（部署机）：
- `~/deploy-backups/pre-wal-20260917T164944Z/{api,worker}.frozen-before` —— 冻结版（可回滚到本轮之前）
- `~/deploy-backups/pre-wal-20260917T165358Z/{api,worker}.frozen-before` —— 中间版

### 8.2 部署动作

1. 备份现有 `bin/api`、`bin/worker`；
2. **`mv` 原子替换**（避开运行中二进制的 `ETXTBSY`；协调方此前用 `cp` 覆盖曾遇到该错误）；
3. **分别只重启对应单元**：`systemctl --user restart blockchain-api`、
   `systemctl --user stop/start blockchain-worker`（未使用整机或数据库重启）；
4. 健康检查后立即采样。

### 8.3 部署后状态（实测）

```
4 个单元全部 active
api     pid=1321573  /health/live=200  /health/ready=200
        磁盘 sha = 内存 /proc/exe sha = 7b1deb54...（一致）
worker  pid=1323043  restarts=0  8081 /health/live=200  /metrics=200
        磁盘 sha = 内存 /proc/exe sha = f2dec36f...（一致）
nginx 8088 /health/ready = 200
tls  https://aurore.online/health/ready = 200
```

### 8.4 部署后 explorer 端点未回归（快速抽查，n=3）

```
activity     p50  80.07 ms  err=0
flow-profile p50 247.01 ms  err=0
overview     p50 107.39 ms  err=0
assets       p50  68.12 ms  err=0
```

### 8.5 改后采样窗口的 WAL 数字**不可用**（如实标注）

改后 90 秒窗口里 `wal_bytes` 出现 **331 MB/90s（220 MB/min）** 的异常值，
原因是协调方同期在跑保留期清理（删除 1,498,893 行）等维护动作。
因此 §0 的结论只使用**应用侧行写入**计数器（不受维护动作影响），
**绝对 WAL 的改后对照以协调方清理/VACUUM FULL 完成后、静置 5 分钟的 30 分钟窗口为准**。

---

## 9. 复跑命令

```bash
# 应用侧逐表写入速率 + NATS 推送速率 + WAL 双口径（不做任何表扫描）
bash /tmp/measure.sh BEFORE 90     # 或 AFTER
# 备份/部署/健康检查一键（含采样）
bash /tmp/GO.sh

# 守卫语义（事务内验证，ROLLBACK，零副作用）
bash /tmp/guardverify.sh
```
