# S7-PERF-01：分层性能实测与缺陷记录

> 执行时间：2026-09-12（UTC+7）· 工具：`backend/cmd/loadprobe`（标准库，逐请求采样后复算分位数）
> 脚本：`scripts/perf/run-perf-suite.sh` · 证据：`acceptance-evidence/dual-system-v1/tests/sprint7/perf/`
> 预算依据：计划 §30.2「PostgreSQL API 查询 P95 < 200ms」「常规响应体 < 1MB」

## 1. 内网 API（直连 127.0.0.1:8080，并发 8，每场景 20s）

| 场景 | 样本 | RPS | P50 | P95 | P99 | 状态码 | 预算判定 |
|---|---:|---:|---:|---:|---:|---|---|
| /api/v2/dashboard | 24,421 | 1,220.6 | 4.99ms | **15.14ms** | 22.51ms | 200 | 达标 |
| /api/v2/cases?limit=20 | 29,517 | 1,475.8 | 4.19ms | **12.13ms** | 17.30ms | 200 | 达标 |
| /api/v2/notifications?limit=20 | 34,017 | 1,700.7 | 3.54ms | **10.75ms** | 16.74ms | 200 | 达标 |
| /api/v2/jobs?limit=20 | 2,694 | 134.7 | 49.15ms | **112.42ms** | 138.34ms | 200 | 达标 |
| /api/admin/v1/overview | 14,418 | 720.9 | 8.92ms | **23.65ms** | 33.28ms | 200 | 达标 |
| /api/admin/v1/audit/events?limit=50 | 9,517 | 475.8 | 12.57ms | **41.16ms** | 68.74ms | 200 | 达标 |
| /api/admin/v1/login-events?limit=50 | 11,117 | 555.8 | 9.96ms | **37.46ms** | 63.71ms | 200 | 达标 |
| /api/admin/v1/jobs?limit=20 | 1,854 | 92.7 | 69.38ms | **167.95ms** | 201.38ms | 200 | 达标（接近上限） |

结论：**8/8 达标**（P95 全部 < 200ms）。响应体平均 216–847 B，远低于 1MB。

> 每条场景末尾的 `err` 计数为压测窗口结束时的 context deadline（客户端侧收尾），
> 状态码分布显示全部为 200。

## 2. 长表 / 图谱（真实链上对象，内网）

| 场景 | 单请求实测 | 并发 8 下 P95 | 判定 |
|---|---:|---:|---|
| `/explorer/addresses/tron/{addr}/activity?limit=200` | **8.09 s** | > 200ms（远超预算） | **未达标** |
| 同上 `limit=50` | 4.73 s | — | **未达标** |
| `/explorer/addresses/tron/{addr}/flow-profile` | **9.88 s** | > 预算 | **未达标** |
| `/explorer/addresses/tron/{addr}/overview` | 0.14 s | 达标 | 达标 |
| `/explorer/addresses/tron/{addr}/assets` | 0.12 s | 达标 | 达标 |
| `/explorer/addresses/tron/{addr}/counterparties` | **修复前 503（12.24 s 查询失败）→ 修复后 0.022 s** | 达标 | **已修复（见 §4）** |

样本地址：`TCFNp179Lg46D16zKoumd4Poa2WFFdtqYj`（28979 行活动）、`TU585SoBMd3hp9WaLc7yctSbR9fSmXsm32`（Coverage HIT 地址）。

**遗留 P1（性能）**：`activity` 与 `flow-profile` 在数万行活动量级上为秒级响应，
与 §30.2 的交互查询预算不符。计划要求「ClickHouse 交互查询默认最大扫描 bytes/rows/时间，超限转异步任务」，
当前两项均无预算守卫。处置方向（需专项）：
1. 为活动查询加 `max_execution_time` / `max_rows_to_read` 预算，超限返回可操作的 4xx（而非静默长耗时）；
2. 为 flow-profile 预聚合或限制跳数/时间窗；
3. 前端对长表默认收窄时间窗（当前默认全量）。

## 3. 公网 HTTPS 层（Cloudflare → cloudflared → Nginx → API，从 VM 出网回环）

| 场景 | 样本 | RPS | P50 | P95 | P99 | 状态码 |
|---|---:|---:|---:|---:|---:|---|
| /api/admin/v1/overview | 113 | 5.6 | **1,226.73ms** | 2,872.66ms | 3,381.11ms | 200（8 次客户端超时） |
| /api/v2/dashboard | 206 | 10.3 | 640.83ms | 1,236.64ms | 1,453.42ms | 401 → 见下 |

- 公网层 P50 约 **0.6–1.2 s**，是内网同端点（4.99–8.92 ms）的 **100–200 倍**，差异来自
  Cloudflare 边缘 + 隧道 + 跨区域 RTT，而非应用处理时间。
- 本机 Nginx 层（8088）只测了免鉴权路径（`/health/ready`：P95 20.15ms）；
  带鉴权路径在 **HTTPS 语义**下会话 Cookie 带 `Secure` + `__Host-`（S7-SEC-01 R-05），
  curl 在 `http://` 上不会回送，因此**内网 HTTP 经 Nginx 的带鉴权测量在物理上不可得**，
  统一以公网 HTTPS 层（同样经过 Nginx）为代理层代表。

## 4. 修复的真实缺陷：counterparties 端点 100% 失败（P1）

**现象**：`GET /api/v2/explorer/addresses/{chain}/{address}/counterparties` 恒返回
`503 DATA_UNAVAILABLE 本地索引暂不可用`，响应耗时 5ms。

**根因（两层，均由实测定位）**：
1. ClickHouse 层：原实现把 `transactions`（金额经 `toInt256OrZero(value_raw)` → `Int256`）与
   `token_transfers`（`amount` 为 `Decimal(76,38)`）UNION ALL 后再 `sum()`，
   UNION 结果是 `Variant(Int256, Decimal(76,38))`，外层 `sum` 报
   `Code: 43 Illegal type Variant(...) of argument for aggregate function sum`
   （`system.query_log` 中 `ExceptionBeforeStart`，耗时 2–8ms，`query_duration_ms=3`）。
2. Go 层（隐藏第二层）：修好 SQL 后接口仍返回 0 条，因为
   `rows.Scan(&txCount)` 报 `clickhouse [ScanRow]: (tx_count) converting UInt64 to *int64 is unsupported`，
   而代码用 `continue` **静默吞掉**了扫描错误（"CH 返回 50 行、接口返回 0 条"）。

**修复**：
- SQL 改为对已物化的 `blockchain.address_activity`（同一 address × direction × counterparty × amount 口径）
  单表聚合，**语义等价**（对手方与笔数与原 UNION 查询逐行一致，已用同一地址实测比对），
  且排序键以 `(chain,address)` 开头，实测 **0.17s**（原查询 12.24s 且失败）；
- `count()` 改为 `toInt64(count())` 以匹配驱动的扫描类型（与同文件既有写法一致）；
- 扫描失败不再 `continue`，改为记录 ERROR 并返回 500（避免同类缺陷再次静默）。

**修复验证**：两个真实地址均返回 `HTTP 200`、`items=50`、耗时 0.022s / 0.036s，
数值与手工核对一致（例：`TSUUVjysXV8YqHytSNjfkNXnnB49QDvZpx 15538 笔`）。

## 5. 百万行导出与 Provider/全链路

见 `export-pipeline.md` 与 `provider-pipeline.md`（同目录）。

## 6. 证据文件

| 文件 | 说明 |
|---|---|
| `lan_*.json` / `nginx_*.json` / `wan_*.json` | 每条场景的原始采样统计（含状态码分布与错误样本） |
| `summary.json` | 汇总 + 预算违规清单（`violations`） |
| 本文件 | 判定与缺陷记录 |
