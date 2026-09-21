# TRON V4 最终修复：过程与结论（XLSX + Investigation）

> 日期：2026-09-08（Asia/Shanghai）
> 项目：`blockchain-platform`（本地镜像 `blockchain-platform-vm-merge-20260906` / 生产 VM `dataserver 192.168.31.152`）
> 依据：`TRON_V4_FINAL_FIX_AND_RISK_BASED_RELEASE_GATE.md`（原验收结论：NO-GO）
> 本轮结论：**XLSX PASS、Investigation Integration Bug 已修复 + E2E PASS → TRON Download Module = GO_WITH_KNOWN_LIMITATIONS**

---

## 1. 背景

上一轮 V4 验收（`TRON_TRC20_HYBRID_V4_ACCEPTANCE.md`）剩余两个 GO 阻断项：

1. **XLSX Export**：CSV 10,000 行 PASS，但 XLSX V1（export `6ebc6fa6…`，声称 10K）与 V2（`12bbe257…` 解包）经 openpyxl 实读均只有表头、0 数据行——已确认复现的软件缺陷。
2. **TRON Investigation**：`GET /api/v1/transactions/tron/0x78dd8b17…/trace?mode=receipt&investigation_id=4a0c72ed…` 返回 `Provider temporarily unavailable`，全链路未完成。

本轮范围（按风险导向 Gate 文档 §19）：只修 XLSX、只做 Investigation 快速定性（≤60 分钟）；其余 Gate 全部按文档降级归档；禁止扩大测试。

---

## 2. XLSX Export：P0 缺陷修复

### 2.1 现象

- CSV：10,000 rows PASS。
- XLSX V1/V2：产物只有 header、openpyxl 实读 0 data rows。
- 三个历史产物（10K、V2 zip 内 xlsx、v3-export.xlsx）症状一致：`Sheet1` 仅 A1=`chain`。

### 2.2 根因定位

解包产物 XML 后发现关键事实：**数据一直在文件里**——`token_transfers_10000.xlsx` 的 sheet XML 含 10,001 个 `<row>`、210,021 个 `<c>`（21 列 × 10,001 行），普通模式 openpyxl 能读全 10,001 行；但工作表根元素为：

```xml
<dimension ref="A1"></dimension>
```

即 `<dimension>` 保持 Excelize `NewFile` 默认值 A1。机制：Excelize `StreamWriter.Flush()` 会把 `NewStreamWriter` 创建时快照的 worksheet 结构整体序列化进工作表 XML，Flush 之后再调 `SetSheetDimension` 不会生效（V1 `writeXLSX` 与 V2 `streamXLSX` 都没在任何时机设置 dimension）。

后果：工作表 XML 数据完整，但依赖 dimension 的读取方（openpyxl `read_only=True`、pandas 等——即当初“实读”的同一方式）依据 `<dimension ref="A1">` 判定工作簿只有表头、0 数据行。CSV 无此机制，故 PASS。

### 2.3 修复（最小改动，不改架构）

文件：`backend/internal/api/export.go`、`backend/internal/api/export_v2.go`

- 新增共享 `writeXLSXSheet(f, name, rows, cols, start, end)`：
  - 在 `NewStreamWriter` **之前**调用 `f.SetSheetDimension(name, "A1:"+lastCell)`（必须早于 NewStreamWriter，否则 Flush 不携带）；
  - 首表（start==0）第 1 行写表头、数据自第 2 行；续表数据自第 1 行起（顺带修正原分表逻辑 >1,048,575 行时续表空首行问题）；
  - 保留全部错误检查（SetRow/Flush/维度错误不再被吞）。
- V1 `writeXLSX` 与 V2 `streamXLSX` 统一复用该 helper。
- 回归测试（`export_v2_test.go`）：`TestStreamXLSXAllDataRowsAndDimension`（130 行 → dimension `A1:D131`、GetRows 131 行、首末行校验）、`TestWriteXLSXEmptyRowsStillWritesValidWorkbook`。

### 2.4 实网验证（只测两档）

在生产 API（Linux VM）真实导出，窗口来自 ClickHouse 已有数据（无任何上游下载）：

| 档位 | 新建 Export | 数据窗口 | openpyxl read_only 实读 |
|---|---|---|---|
| XLSX-100 | `9d85cc58-ee1f-49cd-96a7-a2e8b7b6df3e` | fixture job `38221d92-a4f9-4a3c-9ee9-910d2b49f19b`（2026-09-03T15:41:39Z→16:13:48Z，CH 100 行 / 100 unique key） | dimension `A1:U101`、**100 data rows**、100 unique tx、21 列表头 |
| XLSX-10K | `bc0e13c3-53ab-4894-b6c5-6decdaba32cd` | job `0c87aa78…`（与 CSV PASS 同一窗口） | dimension `A1:U10001`、**10,000 data rows**、10,000 unique tx、21 列表头 |

首/末行与 CSV 证据（`token_transfers_10000.csv`：10,000 rows / 10,000 unique tx）完全一致：首行 `78dd8b17…`、末行 `23bf39df…`。

**要求 100→100、10K→10,000：满足。XLSX Gate 关闭（PASS）。**

---

## 3. TRON Investigation：快速定性（Integration Bug）与最小修复

### 3.1 现象

```text
GET /api/v1/transactions/tron/0x78dd8b17…/trace?mode=receipt&investigation_id=4a0c72ed-77f9-4ff8-87cc-95f060d40c6f
→ 200 {"error": "Provider temporarily unavailable", "error_class": "upstream"}   （约 0.5s）
```

### 3.2 定性过程（30 分钟内）

1. 错误文案定位：`Provider temporarily unavailable` = `internal/provider/rpc/errors.go` 中 `ClassUpstream` 的对外 Public 文案；`error_class: upstream` 表明错误经过 `rpc.AsError` 兜底归类。
2. 代码路径：`api/rpc.go handleTransactionTrace` → `chaintrace.Service.TransactionTrace` → `rpc.ParseConsistency(opts.Consistency)`。
3. **定位**：`mode=receipt` 不是合法 consistency（合法值 `local_only / auto / force_refresh`）。`ParseConsistency` 返回 generic error；`TransactionTrace` 原样返回；`writeRPCError` → `rpc.AsError` 把非 `*rpc.Error` 一律归为 `ClassUpstream` → 对外显示 “Provider temporarily unavailable” 并回 502。全程 <1s，**未发出任何 Provider 请求**。
4. 独立调用验证（同参数脱离 Investigation）：Ankr TRON 独立 `eth_getTransactionReceipt` 成功；10K 全链路 ankr receipt 0 errors / 0 timeout / 0 5xx；TronGrid receipt/TRC20 均正常。

**定性结论：Provider 无故障；属本系统 Integration Bug（参数校验 + 错误映射缺陷），不是外部依赖问题。**

### 3.3 最小修复（只动 request params / error mapping）

- `internal/chaintrace/service.go`：非法 mode 改为返回 `*rpc.Error{Class: ClassInvalidParams, Message: err.Error()}`（HTTP 400），不再落入 upstream 兜底。
- `internal/api/rpc.go` `writeRPCError`：`invalid_params` 类透出具体 Message（系统生成的参数说明，无 Provider/Secret 泄漏），避免笼统误导。
- 单测（`chaintrace/service_test.go`，新增）：`TestTransactionTraceRejectsInvalidModeAsInvalidParams`、`TestTransactionTraceUnsupportedWhenNoGateway`。

### 3.4 复测与真实 Investigation E2E（同参数、生产 API、1 次）

| 调用 | 结果 |
|---|---|
| `trace?mode=receipt`（原失败路径） | HTTP 400 `{"error":"mode must be local_only, auto or force_refresh","error_class":"invalid_params"}`（0.5ms） |
| `trace?mode=auto / force_refresh` | HTTP 503 `trace_unsupported`（TRON Provider 不提供 EVM call-trace：能力边界，诚实返回，非可用性故障） |
| Investigation E2E（investigation_id=`6e863900-9b30-4735-83f3-18caad4652b7`，tx `0x78dd8b17…`，地址 `TQguVRm3tDmZG7AeZ47Mk6qi6GTF1ZDqkZ`） | 交易 receipt（Ankr 实网，block 86041346，完整 log）PASS；地址 activity（ClickHouse/Graph，USDT/1023.192913/direction=out，与 XLSX-10K 首行一致）PASS |

**Investigation：Integration Bug 已修复 + 1 次真实 E2E PASS → 不再阻断 TRON Download。**

---

## 4. 其余 Gate 归档（按风险导向发布 Gate，不再执行）

| Gate | 归档 | 理由 |
|---|---|---|
| 真实 self-transfer 样本 | PASS_UNIT + LIVE_SAMPLE_NOT_AVAILABLE + NON_BLOCKING | 单测覆盖；禁止继续全链寻找 |
| 真实同交易多/同值事件样本 | PASS_UNIT + LIVE_SAMPLE_NOT_AVAILABLE + NON_BLOCKING | 单测已验证 logIndex 6/9 映射 |
| Multi-token 大样本 | SAMPLE_COVERAGE_INCOMPLETE + NON_BLOCKING | 10K 窗口仅 1 token，非程序错误 |
| 完整并发矩阵 | WAIVED | 10K c4=6.776 rows/s、c8=7.253 rows/s（+7%），不改变 initial=4/max=8 |
| 独立 Graph 页面 | NOT_APPLICABLE | 无独立 route；本轮禁止重写；activity/Graph API PASS |
| 剩余五链完整 EVM 下载 | DEFERRED + NON_BLOCKING | 本轮未改 EVM 路径；go test/vet + 七链 head + ETH/BSC live 已 PASS |

---

## 5. 代码与部署状态

### 变更文件（本地镜像 = 生产 VM 字节一致，均以 sha256 前 16 位核对）

| 文件 | 变更 | sha256(前16) |
|---|---|---|
| `backend/internal/api/export.go` | XLSX dimension 修复 + `writeXLSXSheet` | `9d2e9d5c463ddd45` |
| `backend/internal/api/export_v2.go` | streamXLSX 复用 helper | `0eff35ef38231457` |
| `backend/internal/api/export_v2_test.go` | XLSX 回归测试 | — |
| `backend/internal/chaintrace/service.go` | invalid mode → invalid_params | `a4786a6a45272cf5` |
| `backend/internal/api/rpc.go` | writeRPCError 透出参数错误 | `157ac92998b51abe` |
| `backend/internal/chaintrace/service_test.go` | 新增单测 | — |

### 生产部署（Linux VM `dataserver`）

- API 已用修复后源码重建（`/usr/local/go/bin/go build -o bin/api …`）并重启；旧二进制保留 `bin/api.pre-xlsx-dimension-fix-20260908`（含早前全部 V4 修复）。
- 本次重启同时修正环境偏差：此前运行进程仍为 `TRON_RECEIPT_INITIAL_CONCURRENCY=8`，现与 `deploy/.env` 一致：**initial=4 / max=8**；`TRONGRID_BASE_URL=https://api.trongrid.io`。
- Worker（下载执行）未改动、未重启，运行正常。
- 质量门：Windows 与 Linux `go test ./... -count=1`、`go vet ./...` 全 PASS（含新增回归测试）。

---

## 6. 最终结论

```text
XLSX Export：  100 → 100 data rows；10K → 10,000 data rows（openpyxl read_only 实读）  PASS
Investigation：Integration Bug 已修复（invalid_params 语义）；1 次真实 E2E PASS              PASS

TRON Download Module = GO_WITH_KNOWN_LIMITATIONS
```

### Known Limitations（允许带限制上线）

1. Ankr fallback 大范围扫描较慢 → 保留 `PAUSED_FALLBACK_TOO_EXPENSIVE`。
2. 稀有链上模式（self-transfer、同交易多事件）缺真实样本 → 单测覆盖。
3. TRON 不提供 EVM call-trace → `/trace` 诚实返回 `trace_unsupported`；Investigation 数据路径（receipt/activity/Explorer/Export）正常，属独立能力边界而非故障。
4. Graph 无独立页面 → 已有 activity/Graph data API 真实数据路径。

### 本轮明确未执行（遵守停止清单）

TRON 10K 重跑、20 地址稳定性重跑、100 条 correctness 重跑、kill/resume 重跑、HALF_OPEN 重跑、真实 self-transfer / 同交易多事件搜索、完整并发矩阵、Graph 新页面、七链长时间完整 EVM 下载。

### 证据文件

- `outputs/tron-v4-go-gate-xlsxfix/token_transfers_xlsx100.xlsx`（100 data rows）
- `outputs/tron-v4-go-gate-xlsxfix/token_transfers_xlsx10k.xlsx`（10,000 data rows）
- 导出记录：`9d85cc58-ee1f-49cd-96a7-a2e8b7b6df3e`、`bc0e13c3-53ab-4894-b6c5-6decdaba32cd`
- 对照：`outputs/tron-v4-go-gate/token_transfers_10000.csv`（10,000 rows PASS，SHA `ba7c775d…951f0bd1`）
- 验收报告更新：`TRON_TRC20_HYBRID_V4_ACCEPTANCE.md`（2026-09-08 最终修复轮章节）
