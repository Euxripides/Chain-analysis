# TRON Ankr V1 修复与生产验收报告

日期：2026-09-07（远端证据时间为 UTC+07）  
目标：`dataserver:/home/euripides/projects/blockchain-platform`

## 结论

本轮已修复并部署 P0 多地址共享扫描竞态、Time→Block 冷解析容错和 TRON 生产扫描边界。当前结论为 **PASS_WITH_LIMITS**：TRC20 与多地址共享扫描可上线；Transactions/Internal 可用但必须遵循短窗口、Coverage 优先、共享扫描和 checkpoint 策略，不能作为无界全历史扫描。

## 修复内容

- `tokenRegistry` 页 token 绑定改为相同 `[start,end)` range 可被多个 shard 复用，释放采用引用计数；跨 range 仍拒绝，避免 token ownership race 造成 `FAILED_FINAL`。
- TRON `eth_getLogs` span 强制不超过 5000 blocks，并在配置校验阶段拒绝更大值。
- Transactions/Internal 增加生产 auto-scan admission guard：Transactions 1000 blocks、Internal 5000 blocks；API 创建前直接返回 400，无效任务不落库；Ankr full-history 继续拒绝。
- Time→Block 增加 per-probe retry/backoff、timestamp/block TTL cache 和 singleflight；TTL 86400 秒，probe retry=2。
- block/receipt 原始响应缓存保持有界并扩至 256 项，支持相同区间跨调度 wave 复用，不使用无界队列。
- TRON 下载页默认时间窗口改为约 5 分钟，避免全选 dataset 时隐式发起大范围扫描；Vite 构建与桌面/移动端 UI 通过。
- 并发计数隔离测试 fixture 改为直接投递 slow executor，修复测试自身未隔离导致的假失败。

## 生产配置

```text
TRON_ENABLED=true
TRON_PROVIDER=ankr
TRONGRID_ENABLED=false
TRON_ANKR_LOG_MAX_SPAN=5000
TRON_ANKR_INITIAL_CONCURRENCY=2
TRON_ANKR_MAX_CONCURRENCY=4
TRON_ANKR_CONCURRENCY=2
TRON_ANKR_BLOCK_SPAN=1
TRON_ANKR_RECEIPT_SPAN=4
TRON_REQUEST_TIMEOUT_SECONDS=180
DEFAULT_TRON_TX_LOOKBACK=100
MAX_TRON_TX_AUTO_SCAN=1000
DEFAULT_TRON_INTERNAL_LOOKBACK=500
MAX_TRON_INTERNAL_AUTO_SCAN=5000
TIME_BLOCK_CACHE_TTL_SECONDS=86400
TIME_BLOCK_PROBE_RETRY=2
```

## Gate 证据

### TRC20

既有 10K 阶梯为 ClickHouse 唯一 12,165 行、重复键 0、真实 `logIndex` 通过；`eth_getLogs` 最大跨度 5000 已硬限制。此前已完成的 retry/resume、响应大小与 Provider usage 证据保留在 `artifacts/tron-ankr-performance-acceptance`。

### 多地址共享扫描（修复后）

3、10、100 地址 Transactions 与 Internal 均 `COMPLETED`，每个 job 101 个 block pages（Internal 26 receipt pages），失败数 0；同一区间上游 block/receipt 请求不随地址数线性增长。100 地址 Transactions 在有界 256 缓存后仅 101 个 `tron_getblockbylimitnext` 请求，约 45.3 MB。

高/低活跃地址对照（同一区间、两 shard）均 `COMPLETED`；高活跃地址 12 rows、低活跃地址 0 rows，两个 shard 共享同一份约 33.0 MB block 响应缓存，详见 `postfix-high-low2-result.json`。

### 并发

Logs 在 1/2/4/8 均完成并保持约 10–11 秒；Receipts 1/2/4/8 完成，约 287/182/131/152 秒；Blocks 1/2/4/8 约 40/25/25/66 秒。Blocks 在 8 退化，因此全局生产值保持 initial=2、max=4；不以单一 logs 结果把全局并发升到 8。

### 成本标定（真实运行）

| Dataset | 区间 | Provider 请求 | 响应量 | P50/P95 | 错误 |
|---|---:|---:|---:|---:|---:|
| Transactions | 100 | 101 | 36.8 MB | 404/1166 ms | 0 |
| Transactions | 500 | 501 | 261.3 MB | 420/1301 ms | 0 |
| Transactions | 1000 | 1001 | 486.1 MB | 414/1318 ms | 0 |
| Internal | 100 | 101 | 15.7 MB | 371/403 ms | 0 |
| Internal | 500 | 501 | 82.2 MB | 375/555 ms | 0 |
| Internal | 1000 | 1001 | 148.1 MB | 377/991 ms | 0 |
| Internal | 5000 | 5002 | 413.5 MB | 366/808 ms | 1 transient timeout |

Internal 5000 job 最终 `COMPLETED`，唯一 timeout 为 5 秒级单请求，未形成 retry storm。1d/7d/30d/90d/1y 仅按上述 block-level 实测模型外推，不是完整历史实跑；Transactions 大历史仍受 block-driven 固有成本约束。

### Time→Block

三组真实 cold/warm：cold 约 60/70/72 秒，warm 各约 7 秒；三组错误数均 0。冷启动仍明显慢，生产应优先 warm cache。

### 数据安全与完整性

全库核验：failed transaction activity leak=0；rejected internal activity leak=0；transactions/internal_calls/address_activity 重复键均为 0。导出回归：job `2bf9ef75-ad07-4f8e-862c-4c13c5d72c2c`，export `be026ec9-2ea8-498d-8c68-ba7328131ff8`，CSV 12 行、5220 bytes，下载 SHA-256 与 API 记录一致，地址 34 字符、交易哈希 64 字符。

## 恢复与回归状态

- 强杀/重启续跑：job `bfae97ca-17c7-4052-89e8-dadb5167888b` 在 page 50 时记录 checkpoint（`tron_next_block=85977975`，`tron_last_success_block=85977974`），worker 强杀后重启；最终 shard 为 `COMPLETED`，400/400 pages、400 个 block 请求、0 errors，结果见 `out/postfix-kill-resume/result.json`。
- 幂等 replay：Transactions 两次均为 `COMPLETED`、91 pages、12 rows；Internal 两次均为 `COMPLETED`、76 pages、1297 rows。两类业务行、Activity 行和金额快照完全一致（Transactions Activity 12 rows / 95.559127；Internal Activity 960 rows / 0），没有翻倍，结果见 `out/postfix-idempotency/result.json`。
- Linux `go vet ./...` 与 `go test ./...` 已通过（包含 Count Oracle fixture 修复）；Windows 本地 spool fsync 目录测试受 NTFS `sync` 不支持影响，不代表 Linux 部署失败。
- Playwright live：TRON 下载/Explorer/Activity Flow 桌面+移动 4/4；EVM performance-mode 桌面+移动 2/2；`npm run build` 通过。
- EVM capability API：ETH、BSC、Base、Arbitrum、Avalanche、Optimism 200；Polygon 200 且 archive=false，按实测报告，不伪报 archive 能力。

## Provider / 系统边界

Ankr TRON Node API 可返回数据；TRC20 性能可接受。Transactions 使用 `getblockbylimitnext`，Internal 使用 receipt-by-block，二者都是 block/receipt-driven scan，缺少 address-history index，因此是高成本但可控，不应描述为“Ankr API 没数据”。本轮验收窗口（2026-09-07 04:20+07 起）provider_usage 仅有 Ankr 1,215 请求、0 errors、无 TronGrid 请求；`TRONGRID_ENABLED=false`。24 小时历史聚合仍包含更早遗留的 TronGrid 记录，不能误读为本轮调用。

## 证据目录

- [工作记录](../artifacts/tron-ankr-performance-acceptance/WORKING_NOTES.md)
- [证据日志](../artifacts/tron-ankr-performance-acceptance/EVIDENCE_LOG.md)
- [标定与修复脚本](../artifacts/tron-ankr-performance-acceptance/driver/)
- [最终远端聚合证据](../artifacts/tron-ankr-performance-acceptance/out/postfix-final/evidence.json)
- [幂等结果](../artifacts/tron-ankr-performance-acceptance/out/postfix-idempotency/result.json)
- [恢复结果](../artifacts/tron-ankr-performance-acceptance/out/postfix-kill-resume/result.json)
- [导出结果](../artifacts/tron-ankr-performance-acceptance/out/postfix-export/result.json)

若强杀或幂等结果出现漏块、重复金额、rejected/failed 泄漏、TronGrid 请求或持续 429/timeout，结论必须降为 NO-GO；在这些门槛通过前不得恢复已暂停的生产批次。
