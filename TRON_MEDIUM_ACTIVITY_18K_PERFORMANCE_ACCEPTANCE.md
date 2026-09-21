# TRON 全新地址冷下载性能验收（600 行/秒）

验收时间：2026-09-08（Asia/Shanghai）  
部署主机：`192.168.31.152`  
适用数据集：`token_transfers / TRC20`  
验收地址：`THS55WwRma8UoSXZDspNryqx2GRv47n2qv`  
验收任务：`DL-20260908-0037` / `7f01369e-0d09-4dc7-9d52-ff1505466528`

## 结论

**PASS。** 地址在任务创建前经本地 API 核验为 `token_transfers=0`、`transactions=0`，不存在可复用的本地地址历史。任务从创建到 128/128 分片、入库、校验和父级状态全部完成共 77.187723 秒，最终有效数据 57,733 行，完整墙钟吞吐为 **747.98 行/秒**，达到 600 行/秒硬目标的 124.66%。

该结果不是刷新/热缓存结果，也不是运行中峰值。计时包含冷地址活动边界探测、规划、获取、Ankr enrichment、ClickHouse 入库、分片校验和任务终态收敛。

## Gate

| 项目 | 目标 | 实测 | 结果 |
|---|---:|---:|---|
| 本地历史 | 必须为 0 | TRC20=0，Transactions=0 | PASS |
| 完整任务状态 | COMPLETED | COMPLETED | PASS |
| 分片完成 | 全部 | 128/128 | PASS |
| 完整墙钟吞吐 | >=600 行/秒 | 747.98 行/秒 | PASS |
| 首次 Fetch | 越短越好 | 956 ms | PASS |
| Receipt Calls / Row | <0.05（理想） | 0.01218 | PASS |
| Logs Match Rate | >=95% | 99.9861% | PASS |
| Receipt Fallback | <5% | 0.01386%（8 行） | PASS |
| 最终唯一身份 | 无重复 | 57,733 / 57,733 | PASS |
| 无效交易哈希 | 0 | 0 | PASS |
| 零区块号 | 0 | 0 | PASS |
| 非法金额 | 0 | 0 | PASS |
| 空 token/from/to | 0 | 0 | PASS |
| Receipt 内容抽样 | 0 不一致 | 100/100 一致 | PASS |

## 结构性优化结果

旧生产路径在 `TRON_TRC20_BATCH_RECEIPTS_ENABLED=true` 时先进入批量 Receipt 并直接返回，导致 Adaptive Logs 分支事实上不可达。上一轮冷地址证据为 30,741 行、30,741 次逻辑 Receipt、`Receipt/Row=1.0`、`Logs Match=0%`。

修复后执行顺序为：

1. Adaptive `eth_getLogs` 严格内容匹配；
2. 保留已匹配行的真实 `logIndex`；
3. 只对 unmatched transaction 做 Receipt fallback；
4. Logs 不适用或失败时，才进入 bounded batched Receipt correctness fallback。

本次冷地址结果：

- TRC20 enrichment 输入：57,735 行；最终三元唯一行：57,733 行；
- Logs matched：57,727 行（99.9861%）；
- Receipt fallback：8 行（0.01386%）；
- Receipt logical calls：703；
- Receipt HTTP requests（任务指标）：355；
- Saved Receipt calls：51,993；
- Logs logical requests：2,478；
- 所有 provider usage 审计分组错误数：0。

去重身份保持为 `(tx_hash, log_index, token_address)`，没有退化成只按交易哈希去重；同一交易内多条 Transfer log 可以分别保留。

## 调度与收尾优化

- 全新地址先用两个只读边界请求确定实际首次/末次活动，再把活跃区间拆成 127 个连续、无重叠时间分片，另加 1 个末端 catch-up 分片，保持全历史语义。
- CREATED/RESUMING 使用 200ms 快规划通道；暂停、取消和自愈扫描仍为 2 秒维护通道，避免高频全表扫描。
- Finalizer 保留逐分片页面完整性、入库一致性、Manifest、Coverage 和状态守卫，但同一批次只重算一次 dataset/subject/job，消除重复聚合锁竞争。
- 每个分片的首尾 Receipt 改为一个双调用 JSON-RPC batch；部分失败仍只回退失败子调用。
- 最后一个分片完成至 Job COMPLETED 的观测尾部约 0.12 秒。

## 套餐稳定甜点位

TronGrid 地址索引接口实测：

| 请求节奏 | 成功 | 结果 |
|---:|---:|---|
| 10 RPS | 100/100 | 稳定 |
| 15 RPS | 21/150 | 129 次 429 |
| 20 RPS | 0/200 | 200 次 429 |

因此保留 `TRONGRID_MIN_REQUEST_INTERVAL_MS=100`，不以超限重试制造吞吐。

Ankr 使用真实三调用 `eth_getLogs` batch 做 64/80/96 并发、各三轮压力测试。80/96 虽未出现 JSON-RPC 错误，但尾延迟和实际吞吐波动扩大：80 第三轮 P95 10.89 秒；96 第三轮最大 31.84 秒。生产完整任务在 64 并发下持续约 674–867 行/秒并以 747.98 行/秒终态完成，因此当前生产甜点位保持：

```text
ANKR_MAX_CONCURRENCY=64
ANKR_GLOBAL_MAX_INFLIGHT=64
ANKR_PROD_TURBO_CONCURRENCY=64
ANKR_PROD_HARD_CEILING=64
```

这一区间由真实 Logs workload 的成功率、P95、最大延迟和完整任务吞吐共同决定，不直接把套餐标称 1.5K RPS 当作安全并发。

## 正确性证据

ClickHouse `FINAL`：

```text
rows=57733
unique(tx_hash,log_index,token_address)=57733
invalid_tx_hash=0
zero_block=0
invalid_amount=0
empty_identity_field=0
```

从最终库按稳定散列抽样 100 条，使用 Ankr `eth_getTransactionReceipt` 批量回查并逐项比较：

```text
logIndex
token contract
from
to
raw integer amount
```

结果：`matched=100, mismatches=0`。

## V5.13-V5.15 套餐消耗优化

Ankr 24 小时面板曾出现 306,594 次请求，其中 34,100 次为 `-32062 区块范围过大`（11.12%）。根因是 Logs JSON-RPC batch 的累计区块跨度超过供应商隐藏上限；batch 子调用失败后又被单调用回放，形成“失败请求也计费 + 重试再次计费”。

已部署三层止损：

- 单个 `eth_getLogs` 固定不超过 5,000 blocks，单个 JSON-RPC batch 累计不超过 10,000 blocks；15,000 blocks 覆盖的逻辑调用由 10 次降为 6 次（-40%）。
- 对“已知存在 TRC20 行、但 Ankr Logs 返回空”的旧历史纪元做 30 分钟负向能力缓存，并用 singleflight 合并并发探测；同一百万区块纪元不再被不同分片反复付费探测。
- Adaptive selector 改按 Ankr 实际计费的 JSON-RPC 子调用数估算，不再把一个 HTTP batch 错当成一次低成本请求。Batch 只作为延迟优化，不作为额度优化口径。

旧历史任务 `DL-20260908-0038` 完成 37,082 行，最终身份唯一性 37,082/37,082，所有完整性异常为 0。V5.14 部署后的尾段共处理 13,325 行：Logs 仅探测 2 次，provider error 为 0；13,351 次 receipt 逻辑调用被压缩为约 672 次物理 HTTP，但套餐仍按逻辑子调用计费。

混合年代冷地址 `DL-20260908-0039` 在本地两张表均为 0 后创建。暂停时 enrichment 处理 38,104 行，Logs 2,260 次、Logs matched 6,385 行、receipt 31,749 次；相对全 receipt 路径净省约 4,095 次逻辑调用（10.75%）。由于该地址旧历史占比高，继续完整跑完会主要消耗 receipt 额度且不会增加优化结论，因此测试任务已安全暂停于 111/128 分片，未自动恢复。

成本边界必须明确：近期高密度且 Ankr Logs 可用的地址，已验收约 0.0551 Ankr 逻辑调用/行；旧历史中同块 `eth_getTransactionReceipt` 有日志而 `eth_getLogs` 明确返回空时，为保持真实 block-wide `logIndex`，接近 1 receipt/unique transaction 是当前供应商能力下的正确性成本，不能用错误的 TronGrid `event_index` 代替。

## 部署状态

- API SHA-256：`05dab2a3c545134490a71b657266ca2fc99b532eee334f02ae82991310c3576c`
- Worker SHA-256：`c07857c3ab205f4ac359f5ddbde85e92ba00c98a66f04a1baf98dcbb026e3086`
- `/health/ready`：PostgreSQL、ClickHouse、MinIO、NATS 全部 `ok`
- Ubuntu 单元测试：PASS
- Ubuntu race test（`internal/tron`、`internal/download`）：PASS

## 口径边界

本报告按照 V5.1-R1 文档的适用范围，只验收 `token_transfers / TRC20`。它不把 `transactions` 数据集混入 600 行/秒分母，也不声称本次 Canary 下载了普通交易；六地址生产重下任务的数据集完整性应以其独立任务和两张落表结果验收。
