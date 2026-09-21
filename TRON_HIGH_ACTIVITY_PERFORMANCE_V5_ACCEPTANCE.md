# TRON 高活跃地址性能优化 V5 验收报告

## 结论

**PASS_WITH_LIMITS**

V5 已部署到 `192.168.31.152`，生产任务 `DL-20260908-0008` 已从持久化 checkpoint 继续下载。吞吐塌缩状态（界面曾显示 `0.107 rows/s`）已解除，实测 60 秒滚动 Fetch 速率约 `13–17 rows/s`。相对本任务此前约 `10.3 rows/s` 的稳定基线，提升约 `1.3–1.7x`，尚未达到文档期望的 `2–3x`，因此不判定为无条件 GO。

## 部署内容

- Receipt 自适应并发：初始 `6`，稳定时每次 `+2`，自动上限 `12`，硬容量 `16`；错误、429、重试或高延迟时减半。
- 有界分页预取：内存预取 3 页、队列硬上限 4；仅 committed cursor 持久化，崩溃后从数据库 checkpoint 重取。
- Logs/Receipts 自适应：按预计请求成本选择 Logs；精确匹配 `tx_hash + log_index + token/from/to/value`，匹配失败回退 Receipt。
- Logs 边界优化：由页面首尾交易 Receipt 取得精确区块边界，取消高成本的时间到区块二分探测；边界 Receipt 同时进入缓存。
- 速率口径：界面速率改为真实 60 秒滚动窗口，并增加 60 秒/5 分钟 Provider 请求构成、QPS、P50/P95/P99、错误、429 和重试。
- 断点恢复修复：恢复任务不再删除未完成分片；保留分片 ID、page token、页码和累计行数。
- 超时分类修复：`context deadline exceeded` / `context canceled` 作为可重试错误，不再误判为 `FAILED_FINAL`。
- Batch RPC 5/10/20 探测均成功；生产开关保持关闭。
- Phase 4 子分片并行保持关闭。

## 生产任务恢复证据

- Job ID：`057206bb-ab4f-4516-b94f-9f5096bf5537`
- Job No：`DL-20260908-0008`
- 恢复前：两个未完成分片均恢复为 `page=40, downloaded_rows=8000`，任务累计 `127,726` 行。
- 首次安全恢复验证：原分片 ID 未变化，`page 40 / 8000` 推进到 `page 41 / 8200`，未重新规划、未归零。
- 最终观测：任务 `RUNNING`，10/12 shards 完成；两个运行分片分别推进到 `page 65 / 13,000` 与 `page 58 / 11,600`；累计 `136,326` 行。

## 5 分钟 Provider 指标

| Provider | 用途 | 请求 | QPS | P50 | P95 | P99 | 429 | 错误 | 重试 |
|---|---|---:|---:|---:|---:|---:|---:|---:|---:|
| Ankr | receipt | 2,348 | 7.827 | 1,386 ms | 2,144 ms | 2,743 ms | 0 | 0 | 0 |
| Ankr | trc20_enrichment_logs | 160 | 0.533 | 1,532 ms | 2,315 ms | 2,772 ms | 0 | 0 | 0 |
| TronGrid | trc20 | 16 | 0.053 | 719 ms | 1,485 ms | 1,773 ms | 0 | 0 | 0 |

Receipt 自适应目标已由 6 升至 12。观测期间 Logs 成功处理 16 页、节省 2,826 次 Receipt 请求；19 页未满足精确匹配条件，按设计回退 Receipt。

## 数据完整性

ClickHouse 对本轮新写入样本执行 `FINAL` 校验：

- 行数：2,200
- 唯一 `(tx_hash, log_index, token_address)`：2,200
- 重复键：0
- `block_number=0`：0
- 来源标记：2,200 行均为 `trongrid`
- 未使用 TronGrid `event_index` 冒充链上 `logIndex`

## 测试与界面验证

- 本地：`go test ./...` 通过。
- Ubuntu：`go test ./...` 通过。
- Ubuntu：`go test -race ./internal/tron -count=1` 通过。
- 前端构建：`npm run build` 通过。
- 实际浏览器：Chromium 桌面 `1440x1000` 与移动端 `390x844` 均成功加载任务详情；Provider 60 秒/5 分钟表格可见，任务状态和滚动速率实时更新。

## 限制与后续门槛

- 当前持续速率未达到文档理想的 `2–3x` 稳定提升，更未达到另行提出的 `400 rows/s` 目标。
- 高活跃页面中约半数仍因严格匹配门槛回退 Receipt；这是剩余主要优化空间，不能以放松 logIndex 正确性换取速度。
- 应在另一条隔离 canary 上继续优化部分匹配后的“仅对未匹配交易回退 Receipt”，通过缺失/重复/logIndex 校验后再进入生产。

