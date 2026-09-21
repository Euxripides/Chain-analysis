# TRON × Ankr V2 Precheck

执行时间：2026-09-06  
环境：`dataserver` / `/home/euripides/projects/blockchain-platform`  
密钥：复用现有 `ANKR_API_KEY`，已配置；本报告和日志不记录密钥内容。

## 官方能力核验

- Ankr Chain RPC 支持 TRON JSON-RPC 与 Native REST。
- TRON Advanced API 地址历史索引不在支持链列表中；V2 禁止对 TRON 调用 `ankr_getTransactionsByAddress` 或 `ankr_getTokenTransfers`。
- 参考：
  - https://www.ankr.com/docs/rpc-service/chains/chains-api/tron/
  - https://www.ankr.com/docs/advanced-api/overview/
  - https://www.ankr.com/docs/rpc-service/chains/chains-api/tron/soliditynode-api-p3/
  - https://www.ankr.com/docs/rpc-service/chains/chains-api/tron/soliditynode-api-p4/

## 真实 Key 权限 Probe

| Endpoint | 结果 | HTTP | 响应字节 |
|---|---:|---:|---:|
| `eth_blockNumber` | SUPPORTED | 200 | 45 |
| `eth_getLogs` | SUPPORTED | 200 | 69,928 |
| `wallet/getblockbylimitnext` | SUPPORTED | 200 | 595,342 |
| `wallet/gettransactioninfobyblocknum` | SUPPORTED | 200 | 151,035 |
| `wallet/gettransactioninfobyid` | SUPPORTED | 200 | 3 |
| `wallet/getaccount` | SUPPORTED | 200 | 5,071 |
| `wallet/getcontract` | SUPPORTED | 200 | 37,406 |

说明：`gettransactioninfobyid` 对测试 txid 返回空对象，但 endpoint 和权限有效；数据存在性在集成测试使用当前区块内 txid 再验证。

## 现状差异

- `TRON_ENABLED=true`。
- `TRON_PROVIDER` 尚未配置。
- `TRONGRID_ENABLED` 尚未配置。
- 生产 TRON 当前仍路由到 TronGrid。

## 阶段结论

**PASS — 允许进入 V2 编码阶段。**

开发期间必须继续满足：

1. TronGrid 代码保留，默认禁用。
2. TRON 正式路由仅走 Ankr Node API。
3. TRON Node API 与 EVM Advanced API 的限流、指标和用量分开。
4. 生产前完成 block span / response-size / P50/P95 / 429/5xx 实测定标。
