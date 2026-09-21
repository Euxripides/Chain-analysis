# TRON TRC20 V4 Receipt Enrichment 优化与生产 GO Gate 验收报告

- 验收日期：2026-09-08（Asia/Shanghai）
- 规格：`D:\下载文件\TRON_TRC20_V4_RECEIPT_ENRICHMENT_OPTIMIZATION_AND_GO_GATE.md`
- 环境：Windows 工作区、Linux 生产 VM `192.168.31.152`
- 最终判定：**NO-GO**

## 1. 结论

Receipt Enrichment P0 优化已完成并部署：TronGrid 每页先按 `tx_hash` 分组，独立有界 Receipt worker pool 拉取 Ankr receipt；相同交易由 TTL/LRU 有界缓存与 singleflight 合并；一份 receipt 解析全部 Transfer logs，并按事件出现顺序匹配完全相同的多事件。Receipt 获取或精确匹配失败时拒绝写入，不用 `event_index` 冒充 block-wide `log_index`。

代码质量、10K 完整生产链路、100 条独立 receipt 对账、20 地址稳定性、跨提供商多窗口 kill/resume、HALF_OPEN、空结果、Explorer、CSV 和部分 EVM 实网回归已完成。但真实 self-transfer/同交易多事件样本、XLSX、TRON Investigation、独立 Graph 页面和七条 EVM 下载链路未全部通过；两阶段完整并发矩阵也未形成可比样本。文档规定全部 Gate PASS 才能 GO，故保持 **NO-GO**。

## 2. 实现结果

| 能力 | 结果 | 证据 |
|---|---|---|
| Page tx 去重 | PASS | `EnrichTRC20Identity` 按 tx 聚合，只投递 unique tx |
| 一份 receipt 全事件 | PASS | contract/from/to/amount 匹配；同值事件按 receipt 顺序消费 |
| 有界 cache | PASS | TTL/LRU，默认 50,000 entries / 30m；过期和容量淘汰有测试 |
| Singleflight | PASS | `(tron,tx_hash)` 并发调用仅一个 upstream，单测验证 |
| 有界独立并发 | PASS | Receipt semaphore 与 TronGrid semaphore 分离，无 per-row 无界 goroutine |
| 失败策略 | PASS | receipt/匹配失败返回错误；不写 sentinel，不使用 `event_index` |
| Usage 边界 | PASS | TronGrid=`trc20`；Ankr=`receipt`；fallback=`trc20_fallback` |
| Metrics | PASS | requests/hit/miss/shared/unique/amplification/latency/errors/bytes，无 tx hash label |
| 生产参数 | PASS | Receipt initial=4、max=8、cache=50,000/30m |
| Provider 架构 | PASS | 未改变 V4 Router 及 Transactions/Internal/EVM 分工 |

代码边界：`backend/internal/tron/{ankr_client.go,receipt_cache.go,hybrid_test.go}`、`backend/internal/observability/metrics.go`、`backend/internal/config/config.go`、`backend/internal/app/app.go`、`scripts/{trongrid_window_probe.py,tron_v4_verify.py}`。

## 3. 完整生产链路 Benchmark

均包含 TronGrid pagination、Ankr receipt、normalize、ClickHouse write。地址 `TQguVRm3tDmZG7AeZ47Mk6qi6GTF1ZDqkZ` 三个窗口均是一行一个 unique tx，故 `unique_tx_ratio=1.00`，理论最低 amplification 也是 1.00；跨地址复用另由 cache/singleflight 测试验证。

| Rows | Job | TG Req | Unique TX | Receipt Req | Hit | Total Req | Fetch+Ingest | Rows/s | Receipt P95 | Errors |
|---:|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 200 | `d99a5b70-c8e1-4cfd-8a77-9c9d0b044227` | 1 | 200 | 200 | 0 | 201 | 31.221s+0.411s | 6.41 | 旧 request-id 缺陷使明细未落库 | 0 |
| 1,000 | `738aaa9c-dcc7-4841-8b80-70000a15e969` | 5 | 1,000 | 800 新请求 | 200 | 805 | 107.104s+0.829s | 9.34 | warm-cache 重叠测试 | 0 |
| 10,000 | `0c87aa78-3c85-4a38-9bfa-34983f9b171a` | 50 | 10,000 | 10,000 | 0 | 10,050 | 1,378.754s+0.996s | 7.253 | 1,832ms | 0 |

10K Receipt P50/P95/P99=888/1,832/2,385ms，bytes=16,586,350；TronGrid P50/P95/P99=27,138/31,431/37,931ms，bytes=3,562,692；429/403/5xx/timeout/retry 均为 0。

首轮 10K c4 Job `c04ec28a-68da-4c58-82b8-b19dd4efdc17`：10,000 行、50 页、1,475.745s、6.776 rows/s。修复 provider usage `request_id` 超长后，c8 重跑仅提高约 7%，瓶颈仍在上游延迟，所以生产取 initial 4/max 8。

两阶段矩阵：Receipt 4/8 已有同口径 10K 证据；Receipt 1/2 与 TronGrid 1/2/4/8/12 未全部执行。单地址游标分页不能真实形成 12 路并发，改用不同地址又不具同口径事件密度。此项不能伪造，记 **FAIL/INCOMPLETE**。

## 4. Correctness

| Gate | 结果 | 证据/边界 |
|---|---|---|
| 10K 唯一性 | PASS | 10,000 rows/unique keys/unique tx；duplicate=0 |
| >=100 对账 | PASS | `tron_v4_verify.py --limit 100`：100/100 matched，failures=[] |
| 字段一致 | PASS | tx/log/block/time/contract/from/to/amount 全字段复核 |
| zero sentinel | PASS_WITH_NOTE | 229 条真实 receipt `logIndex=0`；0 是合法第一条链上日志，不是失败 sentinel，独立抽样已验证，不能删除真实数据迎合字面计数 |
| 金额精度 | PASS | precision anomaly=0；字符串/整数路径和大整数单测通过 |
| Self Transfer | FAIL | 实样本未找到 `from==to`；只有单测，不能冒充真实 Gate |
| 同 tx 多/同值事件 | FAIL_REAL/PASS_UNIT | 单测中一次 receipt 将相同事件映射为不同 logIndex 6/9；实样本未找到可信案例 |
| 多 Token | FAIL_SAMPLE | 10K 窗口只有 1 token；未构成同口径覆盖 |

## 5. 可靠性

| 场景 | 结果 | 证据 |
|---|---|---|
| 20 地址稳定性 | PASS | Job `4a050d83-d02c-4ded-b0a7-b3aaa84a405f`：20/20 subjects、20/20 shards、42 rows、43 requests、6.802s、0 retries/errors、20 个地址游标 |
| Cross-provider kill/resume | PASS | Job `458c03c3-4342-4a69-af0f-c0a6949b6389`：TronGrid 400 后 kill；Ankr 到 1,095 后再 kill；从 checkpoint 恢复并完成 10,000 rows/25 pages、fallback 9,600；missing/duplicate=0 |
| HALF_OPEN | PASS | 生产等价注入：OPEN→冷却 31s→单探针→NORMAL；Windows/Linux 通过 |
| Empty 不 fallback | PASS | Job `314869df-7f8d-42d2-a321-356563c549cc`：COMPLETED、0 rows、1 primary request、0 fallback；单测覆盖 |
| 强制 fallback | PASS_WITH_LIMIT | Job `772e206c-c0e9-47c4-9917-ad1ccce330b6`：20 rows、35 requests、391.47s；仅证明可用，保留 `PAUSED_FALLBACK_TOO_EXPENSIVE` |
| 重启语义 | PASS | cache 可丢；正确性依赖 DB/checkpoint/业务唯一键 |

## 6. 下游 Gate

| Gate | 结果 | 证据/原因 |
|---|---|---|
| Explorer | PASS | 实查 20 rows/20 keys，cursor 及必需字段正常 |
| Graph | PASS_WITH_LIMIT | activity API 返回 100 条真实 unique rows 且与 CH 一致；应用无独立 Graph 路由，规格又禁止重写 Graph，不能给视觉 Graph PASS |
| CSV | PASS | Export `4d5db51f-ecb0-4285-919d-8a1994cacfac`：10,000 rows/keys，字段齐全，SHA `ba7c775d...951f0bd1` 与 API 一致 |
| XLSX V1 | FAIL | `6ebc6fa6-eee9-4368-89f0-085c193a839e` 声称 10K，openpyxl 实读仅 `Sheet1!A1=chain`、0 数据行 |
| XLSX V2 | FAIL | `12bbe257-6b23-4dcd-a110-6ef863b60948` 解压 XLSX 仍仅表头、0 数据行 |
| Investigation | FAIL | `4a0c72ed-77f9-4ff8-87cc-95f060d40c6f`：补 `0x` 后仍 `Provider temporarily unavailable`，全链路未完成 |

XLSX 与 Investigation 在本规格允许修改范围外，本轮未越权改写；仍是 GO 阻断项。

## 7. EVM 与质量门

- Windows/Linux `go test ./... -count=1`、`go vet ./...`：PASS。
- Linux `go test -race ./internal/tron -count=1`、`go build ./cmd/api ./cmd/worker`：PASS。
- Python benchmark/verifier `py_compile`：PASS。
- 七链 live capability/head 成功：ETH 25,926,828；BSC 120,530,616；Polygon 93,396,916；Arbitrum 502,742,931；Base 51,005,529；Avalanche 94,707,362；Optimism 156,600,815。
- BSC Job `749d3ab9-26b5-4ca5-b82a-e43706fa291d` 完成 12 rows；ETH Job `be63d4a9-3d2c-4ff8-8c1a-5ac468e9a88e` 完成 1 row。
- 其余五链缺同口径真实下载任务，七链生产关键链路回归记 **FAIL/INCOMPLETE**。

生产已恢复 `TRONGRID_BASE_URL=https://api.trongrid.io` 与 Receipt 4/8 配置，API ready、Worker 正常；未恢复、取消或修改历史暂停生产任务。

## 8. 最终判定与证据文件

通过：三档完整链路执行、tx 去重、bounded cache、singleflight、100 条对账、大整数、kill/resume、HALF_OPEN、empty、20 地址、Explorer、CSV、代码质量。

未通过：完整并发矩阵、真实 self-transfer、真实同交易/同值多事件、多 Token、独立 Graph 页面、XLSX、Investigation、七链下载回归。

最终：**NO-GO**。继续保留 fallback 成本保护；上述失败项全部获得真实证据或在后续获准范围修复并复验后，方可改为 GO。

证据文件：

- `outputs/tron-v4-go-gate/token_transfers_10000.csv`
- `outputs/tron-v4-go-gate/token_transfers_v2.zip`
- `outputs/tron-v4-go-gate/v2/addresses/TQguVRm3tDmZG7AeZ47Mk6qi6GTF1ZDqkZ/token_transfers.xlsx`
- `C:\Users\Euripides\.codex\visualizations\2026\09\07\01a07bfc-10ff-78b0-9f2f-817309451137\tron-v4-desktop.png`
- `C:\Users\Euripides\.codex\visualizations\2026\09\07\01a07bfc-10ff-78b0-9f2f-817309451137\tron-v4-mobile.png`

---

# 2026-09-08 最终修复轮（依据 TRON_V4_FINAL_FIX_AND_RISK_BASED_RELEASE_GATE.md）

## 1. XLSX Export：P0 缺陷修复 → PASS

### 根因

XLSX V1/V2 产物并非没有数据：worksheet XML 内含全部数据行（10K 产物实测 10,001 个 <row>、210,021 个 <c>），但 sheet 的 <dimension> 保持 NewFile 默认 ref="A1"。Excelize StreamWriter 在 Flush() 时把 NewStreamWriter 快照的 worksheet 结构整体序列化进 XML，Flush 之后再调 SetSheetDimension 不生效；最终产物只有 <dimension ref="A1">。依赖 dimension 的读取方（openpyxl read_only、pandas）据此把工作簿判为只有表头、0 数据行——CSV 不受影响因此通过。

### 修复（backend/internal/api/export.go + export_v2.go）

- 新增共享 writeXLSXSheet：在 NewStreamWriter 之前按实际行数写 dimension ref="A1:{lastCell}"；首表写表头于第 1 行，续表数据自第 1 行起（原大表分表空首行问题一并修正）。
- V1 writeXLSX 与 V2 streamXLSX 统一走该 helper。
- 回归测试：TestStreamXLSXAllDataRowsAndDimension（130 行 → dimension A1:D131、131 行全可读）、TestWriteXLSXEmptyRowsStillWritesValidWorkbook。

### 实测（生产 API 真实导出，openpyxl read_only 实读——与失败时同一读取方式）

| 档位 | Export Job | 文件 | dimension | data rows | unique tx | 21 列表头 | 首行 tx | 末行 tx |
|---|---|---|---|---:|---:|---|---|---|---|
| XLSX-100 | 9d85cc58-ee1f-49cd-96a7-a2e8b7b6df3e（fixture job 38221d92-a4f9-4a3c-9ee9-910d2b49f19b，窗口 2026-09-03T15:41:39Z→16:13:48Z，CH 100 行/100 unique key） | outputs/tron-v4-go-gate-xlsxfix/token_transfers_xlsx100.xlsx | A1:U101 | 100 | 100 | ✓ | 59f9b7a9… | 23bf39df… |
| XLSX-10K | bc0e13c3-53ab-4894-b6c5-6decdaba32cd（job 0c87aa78…，与 CSV PASS 同窗） | outputs/tron-v4-go-gate-xlsxfix/token_transfers_xlsx10k.xlsx | A1:U10001 | 10,000 | 10,000 | ✓ | 78dd8b17…（同 CSV） | 23bf39df…（同 CSV） |

CSV 对照（token_transfers_10000.csv）：10,000 data rows / 10,000 unique tx，首行 78dd8b17…、末行 23bf39df…——与 XLSX-10K 完全一致。

**XLSX Gate：100 → 100、10K → 10,000，PASS。** 生产 API 已部署修复后二进制（bin/api，前版本备份 bin/api.pre-xlsx-dimension-fix-20260908），Windows/Linux go test ./... 与 go vet ./... 全 PASS。

## 2. TRON Investigation：Integration Bug 修复 → PASS

### 定性（30 分钟内完成）

原失败调用：GET /api/v1/transactions/tron/0x78dd8b17…/trace?mode=receipt&investigation_id=4a0c72ed… → {"error":"Provider temporarily unavailable","error_class":"upstream"}。

独立验证结论：Provider 全部正常（10K 全链 ankr receipt 0 errors/0 timeout/0 5xx；同交易独立 Ankr eth_getTransactionReceipt 成功）。失败原因是本系统 Integration Bug，不是上游：
- mode=receipt 不是合法 consistency（合法值 local_only/auto/force_refresh）；
- chaintrace.TransactionTrace 把 ParseConsistency 的 generic error 原样返回，api.writeRPCError → rpc.AsError 把非 rpc.Error 一律归类 ClassUpstream → 对外显示 “Provider temporarily unavailable” 并返回 502；
- 全程 <1s、无任何 Provider 请求发出——纯参数/错误映射缺陷。

### 最小修复（backend/internal/chaintrace/service.go + api/rpc.go）

- 非法 mode → rpc.Error{Class: ClassInvalidParams}（400）；
- writeRPCError 对 invalid_params 透出具体 Message（无 Provider/Secret 泄漏）；
- 单测 TestTransactionTraceRejectsInvalidModeAsInvalidParams、TestTransactionTraceUnsupportedWhenNoGateway。

### 复测（同参数，生产 API）

- ?mode=receipt → HTTP 400 {"error":"mode must be local_only, auto or force_refresh","error_class":"invalid_params"}（0.5ms，不再伪装成上游故障）
- ?mode=auto / force_refresh → HTTP 503 trace_unsupported（TRON Provider 不提供 EVM call-trace，属能力边界而非可用性故障，诚实返回）
- 真实 Investigation E2E（investigation_id=6e863900-9b30-4735-83f3-18caad4652b7，tx 78dd8b17…，TQguVRm3tDmZG7AeZ47Mk6qi6GTF1ZDqkZ）：
  - 交易 receipt（Ankr 实网）：PASS（block 86041346，完整 log）；
  - 地址 activity（ClickHouse/Graph 数据路径）：PASS（首行 78dd8b17…/USDT/1023.192913/direction=out，与 XLSX-10K 首行一致）；
  - Planner→Downloader→ClickHouse→Investigation 数据链路一致。

**Investigation Gate：Integration Bug 已修复 + 1 次真实 E2E PASS。**

## 3. 其余 Gate 归档（依据风险导向发布 Gate）

- Self-transfer / 同交易多/同值事件真实样本：PASS_UNIT + LIVE_SAMPLE_NOT_AVAILABLE + NON_BLOCKING
- Multi-token 样本：SAMPLE_COVERAGE_INCOMPLETE + NON_BLOCKING
- 完整并发矩阵：WAIVED（10K c4/c8 已实测，c8 仅约 +7%，不改变 initial=4/max=8）
- 独立 Graph 页面：NOT_APPLICABLE（activity/Graph API PASS，当前产品无独立 route，本轮禁止重写）
- 剩余五链完整 EVM 下载：DEFERRED + NON_BLOCKING（go test/vet、七链 head/capability、ETH/BSC live PASS）

## 4. 最终判定（更新）

- 生产配置维持：TRONGRID_BASE_URL=https://api.trongrid.io；Receipt initial=4/max=8（本次重启后进程环境即此值）；Provider 分工不变。
- XLSX PASS；Investigation = Integration bug 已修复且 E2E PASS。
- 代码质量：Windows/Linux go test ./... -count=1、go vet ./... 全 PASS（含新增 XLSX/chaintrace 回归测试）。
- 已知限制（不阻断）：Ankr fallback 大范围扫描慢（PAUSED_FALLBACK_TOO_EXPENSIVE）；稀有链上模式缺真实样本（单测覆盖）；Investigation 的 EVM call-trace 对 TRON 不可用（trace_unsupported，数据路径正常）；Graph 无独立页面（数据/API 路径存在）。

**最终：TRON Download Module = GO_WITH_KNOWN_LIMITATIONS。**

新增证据：
- outputs/tron-v4-go-gate-xlsxfix/token_transfers_xlsx100.xlsx
- outputs/tron-v4-go-gate-xlsxfix/token_transfers_xlsx10k.xlsx
- 导出 API 记录：9d85cc58-ee1f-49cd-96a7-a2e8b7b6df3e、bc0e13c3-53ab-4894-b6c5-6decdaba32cd
