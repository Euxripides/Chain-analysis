# TRON Ankr Minimal Integration V2 开发与上线验收记录

日期：2026-09-06  
部署目标：`dataserver:/home/euripides/projects/blockchain-platform`

## 结论

V2 功能已开发并部署。生产 TRON 数据入口为 Ankr TRON Node API，旧 TronGrid 实现保留但默认禁用。功能、数据落库、导出、桌面/移动端界面和 EVM 回归均通过。

完整的 100/1,000/10,000 logs 与 1/7/30 天扫描性能矩阵不属于本次短区间上线验证结果，不能据此宣称多年全历史扫描性能已验收。

## 生产配置

```text
TRON_ENABLED=true
TRON_PROVIDER=ankr
TRONGRID_ENABLED=false
```

- 复用既有 `ANKR_API_KEY`，没有新增 TRON 专用 Key。
- 启动日志仅输出脱敏端点 `https://rpc.ankr.com/tron_jsonrpc/***`。
- 切换后的 TronGrid 请求数、日志 URL 命中数和 API Key 日志命中数均为 0。

## 实现范围

- TRC20：双向 `eth_getLogs`、self-transfer 去重、真实 `logIndex`、大整数、动态 span、`eth_call` 元数据缓存。
- Transactions：`wallet/getblockbylimitnext` 区块扫描，支持 TransferContract、TransferAssetContract、TriggerSmartContract 和多 contract 选择。
- Internal：`wallet/gettransactioninfobyblocknum`，支持 caller/recipient、多个 callValueInfo、rejected/failed 标记。
- 范围：时间到区块解析、显式区块范围、最大扫描保护；Ankr 模式拒绝无边界 full-history。
- 恢复：可校验区块 cursor、区块 overlap 恢复、`tron_last_success_block` 持久化。
- 复用：同一区块并发地址通过 singleflight + 有界缓存共享 block/receipt 原始响应。
- 产品层：复用 ClickHouse、Activity/Graph 数据、Explorer、Export 和 provider_usage。

## 真实任务证据

| 场景 | Job ID | 结果 |
|---|---|---|
| TRC20 单区块 | `22cb8b2a-3fc9-4b0a-83dd-daa00892823d` | COMPLETED，1 行，真实 logIndex=30，USDT/6，provider=ankr |
| Transactions 单区块 | `4af2b4bc-2669-4815-ab06-6a86f438ec7e` | COMPLETED，1 行，TriggerSmartContract |
| Internal 单区块 | `91b10e46-ce70-47f1-801a-2338eb0b2480` | COMPLETED，1 行 |
| 两地址共享 Transaction 扫描 | `22de49b3-3628-42b1-abc0-2aa742d86605` | COMPLETED，2 shards，各 1 行；上游 block-range 请求增量=1 |
| 两地址共享 Internal 扫描 | `83358e38-fde8-4447-9d98-4fd330203bc3` | COMPLETED，5/11 行；上游 receipt 请求增量=1 |
| 合约作为 TriggerSmartContract 接收方 | `25d57b8b-ad19-4fc3-8812-6de64aeccccb` | COMPLETED，94 行 |
| 时间到区块冷启动 | `1fc29623-79d8-4e70-beb3-be18e7258d74` | COMPLETED，约 139 秒，0 行，检查点到 84912056 |

## 数据核验

区块 `84912055` 的 Ankr TRC20 行：

```text
tx_hash=9e9a6aae39bd653da37988c5fe11332dc2a130acf6588d7021785d767655e621
log_index=30
token=TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t
symbol=USDT
decimals=6
from=TQguVRm3tDmZG7AeZ47Mk6qi6GTF1ZDqkZ
to=TJmmqjb1DK9TTZbQXzRQ2AuA94z4gKAPFh
amount_raw=1000000
source_provider=ankr
```

导出 `91d142bc-974a-4039-9ba8-2d7d5115b3f4` 已完成，CSV 下载 HTTP 200、963 bytes，首条数据含完整 TRON 地址、tx hash、block number、log index 与 token metadata。

## 自动化验证

- Linux：`go test ./...` 通过。
- Linux：`go vet ./...` 通过。
- Linux：`go test -race ./internal/tron` 通过。
- 前端：`npm run build` 通过。
- Playwright TRON：desktop/mobile 2/2 通过，覆盖弹窗、范围控件、Explorer、完整地址链接和横向溢出。
- Playwright EVM performance-mode：desktop/mobile 2/2 通过。
- ETH/BSC/Polygon/Base/Arbitrum/Avalanche/Optimism capability API 均返回 HTTP 200；Polygon 的 archive=false 是当前实测能力，不伪报为 true。

## 性能边界

- 单区块 TRC20：1 行，约 5.6 秒（包含两次 logs 和首次三次 metadata eth_call）。
- 单区块 Transactions：1 行，约 4.2 秒，响应约 595 KB。
- 单区块 Internal：1 行，约 2.1 秒，响应约 151 KB。
- 两地址共享 block scan：约 11 秒，上游仅 1 请求。
- 两地址共享 receipt scan：约 7 秒，上游仅 1 请求。
- 冷启动时间定位仍受 Ankr `getblockbynum` 延迟影响；短时间窗约 139 秒。多年 Transactions/Internal 的固有 block/receipt 扫描成本仍需用文档规定的完整梯度单独压测，不应通过提高并发或重新启用 TronGrid 掩盖。

## 回滚点

- 源代码备份：`/home/euripides/backups/tron-before-ankr-v2-20260906-192423.tar.gz`
- 环境配置备份：`/home/euripides/backups/deploy.env.before-tron-ankr-v2-20260906-1928`
