# TRON Transactions TronGrid V3 Provider Boundary 修复验收

## 结论

**GO**。

V3 Provider Boundary 已修正并在真实 TronGrid/Ankr 数据上完成新的 10 地址、100 地址、100 笔正确性对账、Worker kill/resume、重放幂等、可靠性、Graph/Explorer/Export/Investigation 和 EVM 回归验收。生产后台仍保持 `ISOLATED` 和暂停，等待人工批准恢复，未自动恢复原生产批次。

## 修复内容

- TronGrid 只负责 `GET /v1/accounts/{address}/transactions` 地址交易历史和 fingerprint 分页。
- 删除批量历史的逐交易 TronGrid receipt enrichment。TronGrid 页面已含 `txID`、`blockNumber`、`block_timestamp`、`raw_data.contract` 和 `ret`，直接规范化即可。
- Receipt、Tx Detail、TRC20、Internal、账户/余额/合约和 TRON Node/RPC 保持 Ankr。显式打开交易详情时才向 Ankr 请求 detail/receipt。
- 新增并校验 `TRON_RECEIPT_PROVIDER=ankr`、`TRON_TX_DETAIL_PROVIDER=ankr`、`TRONGRID_MIN_REQUEST_INTERVAL_MS=100`。
- 100ms 只定义为系统主动的软 pacing（目标启动速率不高于 10 req/s），不再声称为 TronGrid 官方固定 QPS 限额。
- 日请求配额保护、有界并发和 8→6→4→2 降级保留。EVM 路由、业务表结构和下游架构未改动。
- 任务进入终态时同步权威 shard 计数，避免最后一次进度快照停在 99/100；本次 100 地址任务已回填为 100/100。

## Provider Boundary Gate

| 能力 | 要求 | 实际结果 | 状态 |
|---|---|---|---|
| Transactions History | TronGrid | 10/100 地址任务的上游请求均为 `trongrid/transactions` | PASS |
| Receipt | Ankr | 批量任务不做 receipt；显式详情请求记录 `ankr/receipt` | PASS |
| Tx Detail | Ankr | 显式详情请求记录 `ankr/tx_detail`，`decode_source=ankr` | PASS |
| TRC20 | Ankr | 配置验证和 Dataset Router 回归通过 | PASS |
| Internal | Ankr | 配置验证和 Dataset Router 回归通过 | PASS |
| TronGrid receipt | 必须为 0 | 新 10 地址、100 地址和重放任务合计 `trongrid_receipt=0` | PASS |

## 真实任务证据

| Gate | 结果 | 证据 |
|---|---|---|
| 10 地址 | PASS | Job `923bc9e8-cd4b-44b3-a471-80d3babe286f`；10/10 completed，20 行，10 个独立 checkpoint；10 次 TronGrid transactions，receipt=0，403/429/timeout=0。 |
| 100 地址 | PASS | Job `f48e96df-a067-4f0a-a1a8-e01203560325`；100/100 subjects 和 shards completed，100 个独立 checkpoint，61,283 行，failed=0，retries=0，leftover fingerprint=0。 |
| 高活跃地址分页 | PASS | `TFFAMQLZybALaLb4uxHA9RBE7pxhUAjF3U`；306 pages，61,014 行，无 pagination loop。 |
| 100 地址可靠性 | PASS | 405 次 `trongrid/transactions`，HTTP 200 全部成功；403=0，429=0，timeout=0，provider error=0。 |
| Kill / Resume | PASS | 在高活跃 shard 第 213 页、42,869 行后 `kill -9` Worker；新 Worker 日志为 `released interrupted shard for resume` 且 `checkpoint_page=213`，最终完成到第 306 页。 |
| Correctness 100 | PASS | TronGrid history 对 Ankr `gettransactionbyid` + `gettransactioninfobyid`：`tx_hash`、`block_number`、`timestamp`、`contract_type`、`from/to/value raw`、`status` 全部 100/100，请求错误 0。 |
| Replay / Idempotency | PASS | Job `aeaaaf68-32a8-4dba-b776-e15ad88084ea` 真实重放 10 地址，10 次 TronGrid 请求返回 20 行；ClickHouse `FINAL` 前后均为 61,284 行/61,284 唯一 tx hash，行数未翻倍。 |

## 下游和回归

| Gate | 结果 | 证据 |
|---|---|---|
| Graph | PASS | 从真实 Explorer 样本构建 10 条 TRON edge，from/to/value/tx hash/chain 完整，与查询返回一致。 |
| Explorer | PASS | 高活跃地址随机 20 笔，hash/block/time/from/to/status/value/source_provider 全部完整，20/20 为 `source_provider=trongrid`。 |
| CSV Export | PASS | Export `1646feff-e416-4f0d-a6f7-99ba756e1463`；61,172 业务去重行，29 列，列错位=0，非 TRON 链=0，非 TronGrid 来源=0，`41` hex/错误 Base58 地址=0。 |
| XLSX Export | PASS | Export `533ed04d-65a6-4d9b-bcae-73e36a308b28`；Excel 实际以只读方式打开成功，1 sheet，61,173 行（含表头）、29 列，必需列无缺失，前 100 行 chain/provider 一致。 |
| Investigation / Detail | PASS | Planner→Downloader→Dataset Router→TronGrid→DB→Detail 链路完整；显式详情和 TRON receipt API 均 HTTP 200，主记录仍为 TronGrid，Ankr 承担 detail/receipt。 |
| EVM Regression | PASS | Linux `go test ./... -count=1` 和 `go vet ./...` 全部通过；ETH、BSC、Polygon、Arbitrum、Base、Avalanche、Optimism live head 接口全部 HTTP 200。 |

100 地址任务写入 61,283 行，导出按 `(chain, tx_hash)` 业务键去重后为 61,172 行；差值是多个被监控地址共享同一交易的正常重复发现，不是重放重复写入。

## 构建、部署和运行态

- 远程 Linux：`go test ./... -count=1`、`go vet ./...`、API/Worker build 均退出 0。
- API PID `1190149`，Worker PID `1190150`。`/health/live`=200，`/health/ready`=200，Worker `/metrics`=200。
- API/Worker 日志均显示 Ankr TRON Node 和 TronGrid transactions index 启用，密钥仅在运行时环境中，未写入源码、报告或日志。
- 当前 API Control 为 `ISOLATED`，`background_paused=true`，理由为“V3 修复验收已完成，等待人工批准恢复生产后台”。

## 配置

```env
TRON_DEFAULT_PROVIDER=ankr
TRON_TRANSACTIONS_PROVIDER=trongrid
TRON_TRC20_PROVIDER=ankr
TRON_INTERNAL_PROVIDER=ankr
TRON_RECEIPT_PROVIDER=ankr
TRON_TX_DETAIL_PROVIDER=ankr
TRONGRID_MIN_REQUEST_INTERVAL_MS=100
TRONGRID_TX_INITIAL_CONCURRENCY=4
TRONGRID_TX_NORMAL_CONCURRENCY=6
TRONGRID_TX_MAX_CONCURRENCY=8
TRONGRID_DAILY_REQUEST_BUDGET=80000
```

## 回滚和备份

回滚只需将 `TRON_TRANSACTIONS_PROVIDER=ankr` 并重启 API/Worker，不需要数据库回滚。本次部署前备份：

`/home/euripides/projects/blockchain-platform/.codex-backups/tron-v3-boundary-20260907-154454`
