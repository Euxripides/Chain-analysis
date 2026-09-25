# dRPC Solana 索引能力缺失 —— 支持工单（可直接发送）

提交日期：2026-09-21
账号：Key 前缀 `Ahm0HP`（Key 1 / Key 2 两把都测过，行为一致）
套餐：付费（账户余额 33,150,201 CU；公开端点提示 "chain is not available on free plan"，说明本账号不是免费层）

## 问题一句话

同一把 Key 下，Data API 对 EVM 与 Tron 正常，**唯独 Solana 的交易历史端点返回 500**；
同时 Solana JSON-RPC 的 **getSignaturesForAddress 返回 -32601**，导致无法按地址枚举历史交易。

## 复现与证据（2026-09-21 实测，同机同时段）

### 1) Data API 交易历史

```
GET https://lb.drpc.live/ethereum/{KEY}/lambda/v1/transactions/0xd8dA...6045/history?limit=1
  -> 200  {"next_page_token":"...","data":[{...}]}

GET https://lb.drpc.live/tron/{KEY}/lambda/v1/transactions/TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t/history?limit=1
  -> 200  {"next_page_token":"...","data":[...]}

GET https://lb.drpc.live/solana/{KEY}/lambda/v1/transactions/5tzFkiKscXHK5ZXCGbXZxdw7gTjjD1mBwuoFbhUvuAi9/history?limit=1
  -> 500  {"message":"We're currently experiencing technical difficulties. Please try again later or contact support if the problem persists."}
            或 {"message":"Temporary internal error. Please retry, trace-id: 3cb0aef53641bbfd44d7425c4d853fb4","code":19}
```

已排除的变量（全部得到同样结果）：

- 域名：`lb.drpc.live`、`lb.drpc.org`、`api.drpc.org`、`solana.drpc.org`
- URL 形态：单链式 `/solana/{key}/lambda/...` 与 multichain 式 `/lambda/{key}/v1/transactions/.../history`
- 参数：不带参数、`?limit=1`、`?limit=1&start=...&end=...`
- 地址：多个 Solana 地址（交易所热钱包、USDC mint）
- 两把不同的 Key（Key 1 与 Key 2）
- `/v1/chains` 返回的受支持链列表**包含** `{"id":"solana","name":"Solana"}`

### 2) Solana JSON-RPC 方法可用性

```
POST https://lb.drpc.live/solana/{KEY}
  getHealth                  -> 200 "ok"
  getSlot (finalized)        -> 200 449084890
  getTransaction             -> 200 (完整 meta / logMessages / innerInstructions)
  getBalance / getBlock / getBlockTime / getEpochInfo / getLatestBlockhash -> 200
  getSignaturesForAddress    -> 400 {"error":{"code":-32601,
                                      "message":"the method getSignaturesForAddress does not exist/is not available"}}
```

`getSignaturesForAddress` 连续请求 20 次全部 -32601；官方文档示例（`Vote111111111111111111111111111111111111111` + `limit:1`）
照抄也是同样结果。

### 3) Key 配置（由 Keys API 读回，确认不是 Key 限制）

```
GET https://drpc.org/api/team/keys/list   (Bearer <Keys API Token>)
  Key 1 / Key 2: status=true, cu_day_limit=0, rate_limit=0,
                 methods_whitelist=null, methods_blacklist=null,
                 providers=null, ip_whitelist=null, jwt_enabled=false
```

两把 Key 都没有方法白/黑名单、没有 Provider 限制、没有 IP 白名单，且 `status=true`。

### 4) 账户用量（Statistics API，证明请求确实到达并计费）

```
GET https://drpc.org/api/metrics/json?range=hour   (Bearer <Statistics API Token>)
  network=solana, provider_id=chainstack  -> getVersion 2 req / 40 CU
  network=solana, provider_id=zero1node   -> getSlot    1 req / 20 CU
  network=solana, provider_id=nodefleet   -> getTransaction 1 req / 20 CU
```

说明 Solana 请求会被分流到 chainstack / zero1node / nodefleet，但没有任何一个能服务
`getSignaturesForAddress`，且 Data API 的 Solana 交易历史在服务端直接 500。

## 我们的判断

按 dRPC 负载均衡文档（`/docs/specs/balancing/overview`），请求只会被路由到"有能力完成该请求"的 Provider。
因此 -32601 说明**本账号当前可用的 Solana Provider 集合里没有启用交易历史索引的节点**，
这不是我们的请求格式或 Key 权限问题。

## 请协助确认

1. 本账号的 **Solana 交易历史索引能力**（`getSignaturesForAddress`）是否已开通？若未开通，如何开通？
2. Data API 的 `GET /solana/{key}/lambda/v1/transactions/{address}/history` 是否已对本账号开放？
   目前稳定返回 HTTP 500（trace-id 可查），而同形态的 ethereum / tron 均 200。
3. 如果本账号的 Solana 需要指定 Provider 或使用专属端点，请给出正确的 Endpoint URL 与配置方式。

## 影响

平台侧已实现 dRPC 双通道（Data API + Solana JSON-RPC）接入，并在历史通道不可用时自动回退到现有通道。
上述能力开通后无需改代码即可自动切换到 dRPC（路由闸门每 10 秒复评一次）。


---

## 附：2026-09-22 复测更新（重要修正）

### 1) Solana Data API 不是「完全不可用」，而是**时好时坏**

同一天多次复测得到三种结果，全部出现在分钟级间隔内：

```
200  {"next_page_token":"...","data":[{...真实交易与 transfers...}]}
500  {"message":"We're currently experiencing technical difficulties..."}
400  {"error":{"code":-32601,"message":"the method GET#/lambda/v1/transactions/*/history does not exist/is not available"}}
```

因此更准确的描述是：**本账号的 Solana 交易历史端点间歇性不可用**（约一半请求失败），
而不是「未开通」。EVM / Tron 在同一时段全部 200。

### 2) 该端点**不返回 slot/block 字段**

真实响应行只有：`hash / timestamp(ms) / type / status / success / owner / fee / is_signer /
tags / defi / attributes / transfers[]`，其中 `transfers[]` 含 `direction / sender_address /
recipient_address / token{name,symbol,address} / decimals / amount_string / amount / price / value_usd`。

对以 slot 区间为准的下载来说这有两个后果：

- 行只能落到 `block_number=0`，地址活动与覆盖率失去区块维度；
- 要拿到 slot 只能对每笔交易再调一次 `getTransaction`（即开发文档 §3.2 明确要避免的 N+1）。

另外 `fee` 是**数字**（单位 SOL，如 `7e-06`），不是对象；原生 SOL 转账的 `token.address` 是
`11111111111111111111111111111111` 而不是空串 —— 这两点都已按真实响应修正实现（含回归测试）。

### 3) 因此当前采用按数据集分流

| 数据集 | 通道 | 原因 |
|---|---|---|
| balances | **dRPC**（Solana JSON-RPC）| `getBalance`/`getTokenAccountsByOwner`/`getSlot` 稳定可用，且这是付费 Key 的真实收益 |
| transactions / token_transfers / nft_transfers / logs | 既有 Solana JSON-RPC | slot 精确、稳定；Data API 间歇失败且没有 slot |
| internal_transactions | 既有 Solana JSON-RPC | dRPC 不提供该数据集 |

切换开关：`DRPC_SOLANA_HISTORY_ENABLED`（默认 false）。dRPC 侧稳定并补上 slot 字段后置 true，
路由会在约 20 秒内（连续 2 次探针成功）自动把历史切到 Data API，无需改代码。

