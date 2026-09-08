# Ankr V1.4.2 6G-12G Extreme Concurrency Test Report

测试日期：2026-09-05  
测试目标：`dataserver`（约 24 GiB RAM）  
测试依据：`D:\下载文件\Ankr_V1.4.2_6G起步_12G封顶_极限并发专项测试方案.md`  
说明：上述 Markdown 仅作为本轮测试的门禁、步骤和验收标准；本报告中的 PASS/FAIL 只依据实际运行证据。

## 结论

Worker 的运行配置已经调整并生效：Fetch memory budget 从默认 `1 GiB` 调整为 `6 GiB`，全局并发目标上限为 `256`，初始并发为 `32`，PageSize 固定为 `10000`。同样的应用配置已写入远端 `deploy/.env`，当前进程额外使用了 `GOMEMLIMIT=12GiB` 和 `GOMAXPROCS=16`。

本轮完成了一个 canary 和一批 20 个并行 benchmark 任务，抓取错误门禁通过，任务最终均完成；但不能签署专项方案的 PASS。原因是：

1. 控制器目标升到了 `256`，但正式批次的实际抓取活动槽位只在单任务 `8–9` 个分片范围内短暂出现，随后为 `0`；没有满足 `ready_shards ≥ 4 × target_concurrency` 和持续有效并发的条件。
2. 20 个任务的全链路完成时间被异步入库/最终化阶段拉长，不能把这批任务的全壁钟时间当作 C256 稳态 Fetch Goodput。
3. 源码仍没有独立的 RSS/VM `12 GiB` hard-max 执行门禁；`GOMEMLIMIT=12GiB` 是 Go heap runtime limit，不等价于进程 RSS 或整机内存硬上限。
4. 因此没有继续执行 C32–C256 的合格阶梯测量、8/10/12 GiB 升档、2 小时 Soak 或 Kill-9 恢复验收。

最终状态：**配置修正 PASS；canary PASS；正式极限并发专项验收 NO-GO / NOT QUALIFIED。**

## 配置证据

当前 Worker（PID `245035`）实际环境：

```text
ANKR_MEMORY_BUDGET_MB=6144
ANKR_INITIAL_CONCURRENCY=32
ANKR_MAX_CONCURRENCY=256
ANKR_GLOBAL_MAX_INFLIGHT=256
ANKR_CONCURRENCY_RAMP_STEPS=32,64,96,128,192,256
ANKR_GLOBAL_AUTO_TUNE_ENABLED=true
ANKR_GLOBAL_READY_QUEUE_ENABLED=true
ANKR_GLOBAL_READY_QUEUE_SHADOW=false
ANKR_READY_QUEUE_MODE=weighted
ANKR_V14_ENABLED=true
ANKR_RANGE_SHARDING_ENABLED=true
ANKR_TRANSACTIONS_RANGE_SHARDING_ENABLED=true
ANKR_PAGE_SIZE_TRANSACTIONS=10000
ANKR_PAGE_SIZE_TOKEN_TRANSFERS=10000
ANKR_PAGE_SIZE_NFT_TRANSFERS=10000
ANKR_PAGE_SIZE_LOGS=10000
ANKR_RAMP_GOODPUT_MIN_GAIN_PCT=5
ANKR_RAMP_SETTLE_SAMPLES=9
ANKR_RAMP_ROLLBACK_COOLDOWN_SECONDS=600
GOMEMLIMIT=12GiB
GOMAXPROCS=16
```

启动日志确认：

```text
worker started ... initial_concurrency=32
global concurrency ramp started ceiling=256 steps=[32,64,96,128,192,256] start_level=32 auto_tune=true
```

远端 `deploy/.env` 已持久化 `ANKR_MEMORY_BUDGET_MB=6144`、`ANKR_MAX_CONCURRENCY=256`、`ANKR_GLOBAL_MAX_INFLIGHT=256`、`32,64,96,128,192,256` Ramp、ready queue、V1.4、range sharding 和各数据集 PageSize 配置。当前 Worker 没有因该持久化动作重启。

## 门禁结果

| ID | 项目 | 结果 | 实际证据 |
|---|---|---|---|
| PRE-001 | VM 内存/Swap 基线 | PASS（运行窗口） | 测试后 `MemAvailable≈20 GiB`，Swap 已用约 `2.3 MiB`，Worker `VmSwap=0`；未观察 OOM。 |
| PRE-002 | API/Worker 存活 | PASS | API `:8080/health/live=200`，Worker `:8081/metrics=200`。 |
| PRE-003 | 后端代码回归 | PASS | 远端源码 `/usr/local/go/bin/go test ./...` 全包通过。 |
| CFG-001 | 6 GiB Fetch budget | PASS | 当前 Worker 环境为 `ANKR_MEMORY_BUDGET_MB=6144`；此前默认值为 `1024`。 |
| CFG-002 | 并发/PageSize 配置 | PASS | 当前进程和 `deploy/.env` 均已回读到 `32 → 256`、PageSize `10000`。 |
| CFG-003 | 12 GiB hard max 与 VM 保护 | FAIL / BLOCKED | 没有独立 RSS/VM hard-max gate；`GOMEMLIMIT` 只限制 Go heap 行为，不能替代 RSS/Swap/MemAvailable 门禁。 |
| RNG-001 | Canary Balanced Range | PASS（canary） | 9 个范围、8 个非空范围；非空最大/最小行数 `98/77=1.273`，低于方案硬阈值 `1.5`，高于推荐值 `1.25`。正式阶梯仍未获得合格样本量。 |
| RAMP-001 | Ready Depth / 有效并发 | FAIL | 控制器 `global_concurrency_target=256`，但正式批次抓取完成后 `global_active_slots=0`；单任务记录的 `peak_active_shards` 仅 `8–9`，没有 `4×target` Ready Shard 深度。 |
| RAMP-002 | C32–C256 正式阶梯 | NOT QUALIFIED | 20 个任务完成，但没有满足 warmup、120 秒测量、100/200 成功页和持续有效并发条件，不能签署任何 C 档通过。 |
| BUDGET-001 | 8/10/12 GiB 升档 | NOT RUN | 6 GiB 首档尚未成为合格有效并发基线。 |
| SOAK-001 | 2 小时 Soak | NOT RUN | 没有合格 FAST candidate。 |
| REC-001 | Kill-9 recovery | NOT RUN | 未执行中断操作。 |

## 实际测试结果

### Canary

Benchmark ID：`fe6296b7-5f68-41c1-9254-8a7b35400400`。

| 指标 | 结果 |
|---|---:|
| wall clock | 37,173 ms |
| rows / unique rows | 658 / 658 |
| requests / attempts | 9 / 9 |
| retry / timeout / 429 | 0 / 0 / 0 |
| peak active shards | 8 |
| peak RSS | 71,766,016 bytes（约 68.4 MiB） |
| spool peak | 397,413 bytes |
| ClickHouse p95 | 128 ms |
| canonical integrity | PASS |

### 20 任务正式批次

测试使用 transactions、`target_block=119770000`、`shard_count=8`、`page_size=10000`，轮换 3 个地址，共 20 个 benchmark、180 个分片；最终数据库状态为 20/20 benchmark 和 180/180 分片完成。

| 指标 | 实际结果 |
|---|---:|
| integrity PASS | 14/20 |
| integrity FAIL / excluded | 6/20 |
| raw rows / unique rows | 31,417 / 31,411 |
| requests / attempts | 180 / 180 |
| retry / timeout / 429 | 0 / 0 / 0 |
| 每任务 peak active shards | 8–9 |
| benchmark peak RSS | 119,705,600 bytes（约 114.2 MiB） |
| benchmark max spool | 2,911,792 bytes（约 2.78 MiB） |
| ClickHouse p95 | 285 ms |
| 单任务 wall clock 范围 | 87.115–756.929 s |

6 个 integrity FAIL 均为同一类可审计数据问题：`raw_rows=4444`、`canonical_unique_rows=4443`、`source_duplicate_rows=1`，系统按完整性规则排除，未把它们用于学习。它们不是 retry、429 或 timeout 失败。

本批次按任务完整 wall clock 计算的 unique rows/s 仅作为并发排队现象证据，不作为 Fetch Goodput：

| 任务行数 | 任务数 | unique rows/s 平均值 | 说明 |
|---:|---:|---:|---|
| 21 | 7 | 0.06 | 低数据量任务被最终化排队放大 |
| 658 | 7 | 0.93 | 含完整任务壁钟时间 |
| 4,444 | 6 | 7.52 | 其中 6 个有 1 条源重复行 |

Worker 运行时回读：`global_concurrency_target=256`、`global_active_slots=0`、`ankr_fetch_goodput_rows_per_second=0`（采样时任务已完成）；进程 `VmRSS=79,332 kB`、`VmHWM=116,900 kB`、`VmSwap=0`。这证明配置和内存遥测没有触发保护，但不证明 C256 有效吞吐。

## 阻塞项与下一步

1. 增加可执行的独立内存保护：Worker RSS hard max、VM `MemAvailable` 下限、Swap 持续增长门禁；当前 `GOMEMLIMIT` 不能承担这三个职责。
2. 增加 `active_nonzero_fetch`、`effective_concurrency_ratio`、ready depth 的持久化采样，并把它们作为 C 档合格条件。
3. 准备至少 `4 × target` 个可持续调度的非空 Ready Shard；当前 benchmark 每个任务只有 9 个 Range，不能支撑 C32 以上的有效并发测量。
4. 对满足页数和 Ready Depth 的数据集，按 `C32 → C64 → C96 → C128 → C192 → C256` 逐档执行 30 秒 warmup、至少 120 秒测量和至少 100 个成功页，保存 unique rows/s、body p95、retry amp、RSS、MemAvailable、Swap、spool 和 ClickHouse pending 曲线。
5. 只有首档满足方案门禁后，才继续 8/10/12 GiB、2 小时 Soak 和 Kill-9 recovery。

## 最终门禁状态

```text
Runtime Fetch Memory Budget = 6 GiB (APPLIED)
Configured Ramp = 32,64,96,128,192,256 (APPLIED)
Configured PageSize = 10000 (APPLIED)
12 GiB RSS/VM Hard Ceiling = NOT IMPLEMENTED
C32-C256 Qualified Goodput = NOT MEASURED
Maximum Goodput = NOT MEASURED FOR THIS PLAN
95%-Max Threshold = NOT MEASURED
Retry Amp = 1.00 ON OBSERVED BATCH
Body P95 = NOT USED AS QUALIFIED RAMP GATE
Worker RSS Peak = 119,705,600 bytes IN BENCHMARK RECORD
VM MemAvailable Min = NOT SAMPLED AS A TIME SERIES
Credits / 1M Rows = NOT SIGNED FOR THIS PLAN
Canonical Integrity = CANARY PASS; FORMAL 14/20 PASS, 6 EXCLUDED
2h Soak = NOT RUN
Kill-9 Recovery = NOT RUN
Overall Plan Acceptance = NO-GO / NOT QUALIFIED
```
