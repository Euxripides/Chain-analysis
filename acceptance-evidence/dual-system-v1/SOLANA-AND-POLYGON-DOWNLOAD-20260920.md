# Polygon 下载跑通 + Solana 链支持（2026-09-20）

执行环境：dataserver（192.168.31.152），仓库 `/home/euripides/projects/blockchain-platform`。
触发：`把 Polygon 跑通，添加 Solana`。

## 一、Polygon 端到端跑通（此前只有配置、没有实际任务）

用 fixture 身份（`fixture-membership`，investigator 角色，专用 org/workspace）走真实产品接口：

```
POST /api/v2/downloads/preview  -> 200, coverage 0, missing_blocks 1, provider_work_required true
POST /api/v2/downloads          -> 201, job_id f27526b3-e525-4be3-9648-2397e5df12f1, job_no DL-20260920-0001
GET  /api/v2/jobs/{id}          -> COMPLETED（约 3.2s）
```

| 层 | 证据 |
| --- | --- |
| PostgreSQL `download_shards` | transactions / COMPLETED / [94089819,94089819] / rows 1 / 956 B / pages 1 / raw `raw/ankr/polygon/transactions/2026/09/93ba7bad…/manifest.json.gz` |
| PostgreSQL `dataset_coverage` | polygon · 0xf977814e90da44bfa03b6295a0616a897441acec · transactions · row_count 1 · provider ankr |
| ClickHouse `transactions` | chain=polygon, block_number=94089819, tx 0x31966fd6…, from 0xf977…acec, to 0xb5c0…ee17, method 0xa9059cbb, status 1, source_provider=ankr |
| MinIO `blockchain-raw` | manifest + part-000001.json.gz（2026-09-20 02:54:12） |

结论：Polygon 的"注册即可下"从配置事实升级为**实测事实**。

## 二、Solana 链支持（新增）

### 2.1 事实边界（决定了实现方式）

- Ankr **Advanced API（multichain）明确拒绝 solana**：`ankr_getTransactionsByAddress` 返回
  `invalid params: solana is not allowed`（2026-09-20 实测）。
- Ankr 的 **Solana 标准 RPC 端点可用**：`https://rpc.ankr.com/solana/<API_KEY>`，
  `getVersion` → 200 `solana-core 4.2.2`。
- 因此新增独立 Provider 走标准 JSON-RPC，而不是复用 Ankr Advanced API。

### 2.2 改动清单

后端：

| 文件 | 变更 |
| --- | --- |
| `internal/chain/registry.go` | 新增 `solana`（type=solana，SOL/9 位，确认深度 32，solscan 浏览器） |
| `internal/solana/address.go`（新） | base58btc 解码 + 32 字节校验；**大小写敏感**，全链路不折叠 |
| `internal/dataset/service.go` | Solana 地址分支（原样保留）；`solanaRangeGuard`：只接受显式 slot 区间 |
| `internal/ingest/normalizer.go` | `pickAddressForChain`：Solana 不折叠大小写，EVM 行为不变 |
| `internal/provider/solana/client.go`（新） | `getSignaturesForAddress` + `getTransaction(jsonParsed)`；输出与 Ankr EVM 行同构；并发抓取、区间过滤、分页 token、decoded 字节计量 |
| `internal/provider/router.go`（新） | 按 chain 分派的薄路由（fallback = Ankr），下载引擎不需要 chain=="solana" 分支 |
| `internal/download/{runner,rowbatch,finalize}.go` | provider 名/方法名随链决定：`solana` / `getTransaction`，raw 路径 `raw/solana/solana/transactions/…` |
| `internal/api/sprint5_meta.go` | `/api/v2/meta/datasets?chain=` 链级白名单；`/api/v2/meta/chains` 增加 `download_only` |
| `internal/api/sprint5_downloads.go` | subject_meta 键不再统一 ToLower（base58 会被破坏） |
| `internal/config/config.go` | `SolanaConfig`（默认复用 Ankr Solana RPC；无凭据时 fail closed 不注册） |
| `internal/app/app.go` | 构造 Solana Provider 并接入 Router |
| `migrations/postgres/0072_solana_chain.sql` | `chain_capabilities` + `provider_descriptors(solana-rpc)` + `routing_policies(solana:transactions)` + 自检 |

前端：

- `DownloadNewPage`：`/api/v2/meta/datasets` 改为按当前链取（路径字面量不变，链走 query，
  契约冻结检查仍与基线一致）；链切换后剔除新链不支持的已选 Dataset；Solana 下禁用 full/time
  并在强制切换时弹窗说明。
- `ExplorerPage`：过滤 `download_only` 链（Solana 没有 EVM 读接口与本地索引）。
- `download.types.ts`：`ChainMeta.download_only`。

### 2.3 线上实测（两次真实任务）

| 任务 | 地址 | slot | 结果 |
| --- | --- | --- | --- |
| DL-20260920-0002 (`44809950…`) | 5tzFkiKscXHK5ZXCGbXZxdw7gTjjD1mBwuoFbhUvuAi9 | 448604526 | COMPLETED，1 行 |
| DL-20260920-0003 (`e41c0c73…`) | 9WzDXwBbmkg8ZTbNMqUxvQRAyrZzDsGYdLVL9zYtAWWM | 448541970 | COMPLETED，1 行，downloaded_bytes 7099 |

ClickHouse 实证（`chain='solana'`，2 行，tx_hash 唯一）：

```
block_number 448604526 · tx 2VRtE8mfoJbiPqPCp3oz82uQ1bvzQRAesXjtzzs9stxShT3FLbitj9BVAP2tT24YLxXc3g6PfATjx3JPtzLwfkmK
from 5tzFkiKscXHK5ZXCGbXZxdw7gTjjD1mBwuoFbhUvuAi9 · to HKYGFLgsnAD2LtCrQKHripx55i2mBzF2KrQaVWaSmRrY
value_raw 144409250 (lamports) · status 1 · tx_type solana · source_provider solana
```

`dataset_coverage` 两条（provider=`solana`）；MinIO `raw/solana/solana/transactions/2026/09/…`（manifest + part）。

边界（诚实标注）：

- **只支持 transactions**：`/api/v2/meta/datasets?chain=solana` 只返回 transactions；
  token_transfers / nft / logs / balances 未实现（Provider 会以 `unsupported` 拒绝）。
- **只支持显式 slot 区间**：full/time 在 API 层被拒（`Solana 目前只支持显式区块（slot）区间`）。
- **语义映射**：block_number=slot，tx_hash=signature，from=fee payer，
  to=lamport 净增最大的非 fee payer 账户，value=fee payer 净流出减 fee；
  gas/nonce/input 恒 0/空（Solana 没有这些概念，绝不伪造）。
- Explorer 不展示 Solana（`download_only=true`）。

### 2.4 门禁

- `pnpm run typecheck`：通过。
- `node scripts/uc-contract-freeze-check.mjs`：路由/权限/API 字面量/Tab 权限四项全部
  与基线 25a6ee6 一致（`/api` 字面量 66 项未变）。
- 后端 `go build ./...` 与相关包 `go test`：通过（新增
  `internal/solana`、`internal/provider/solana`、`internal/provider`(router)、
  `internal/dataset`、`internal/ingest`、`internal/api` 单测）。
- 浏览器验收：见 `~/deploy-frontend.sh both --verify` 输出。

### 2.5 追加：已批准的 ADR-023 三项决策落地（2026-09-20 晚）

产品批准后按 `docs/adr/ADR-023-solana-token-transfers-semantics.md`（Accepted）实现并实测：

| 决策 | 落地 | 实测证据 |
| --- | --- | --- |
| D6 gross/net 分列 | `clickhouse/0008_solana_transfer_evidence.sql`：`token_transfers` 新增 `gross_amount_raw`/`fee_amount_raw`；`address_activity` 新增 `fee_amount_raw`；`ingest.Writer` 在写入点兜底不变量 `amount_raw = gross - fee` | 线上 USDC 转账：`amount_raw=3090000000`、`gross=3090000000`、`fee=0`（SPL Token 无费） |
| D10 证据详情 ATA + 手续费 | `token_transfers` 新增 `from_token_account`/`to_token_account`，`address_activity` 新增 `token_account`/`counterparty_token_account`；地址详情检查器在字段非空时展示 | 行内 `from_token_account=7KJjY7rArbydeLBF7gQ5LdqXRKRYyPArT99NEctsHsgU`、`to_token_account=HNhDsdRNVv4UPjxwnrLDfVay3SFjx4wQ984bcDfSfoB`（与主网指令逐字一致） |
| D11 CPI 调用树独立数据集 | `internal/provider/solana` 解析 `logMessages` 的 invoke 栈 → `internal_calls`；`chainDatasets` 提升到 `model.ChainDatasets` 作为唯一白名单（下载入参校验与 meta 接口同源） | 任务 DL-20260920-0005：4 个节点（System / ComputeBudget×2 / Tokenkeg），`call_index=outer*256`、`depth=1`、`trace_path=0..3`、`success=1` |

任务与数据：

| 任务 | 数据集 | 结果 |
| --- | --- | --- |
| DL-20260920-0004 | token_transfers | COMPLETED，1 行；ClickHouse `log_index=768`（外层 #3）、owner 对 `5tzFki…→CGw7M5mw…`、ATA 与手续费列如上 |
| DL-20260920-0005 | internal_transactions | COMPLETED，4 行 CPI 节点 |

金额口径（写入点兜底，任何调用方漏填都成立）：

```
amount_raw = gross_amount_raw - fee_amount_raw
EVM/TRON/SPL Token：fee = "0"，gross = amount_raw
Token-2022 带费：net 取 pre/postTokenBalances 差额（链上事实），fee = gross - net；
            指令显式费用与余额差额不一致时以余额为准并计数 fee_mismatch（绝不静默）
```

边界（与 ADR 一致，未变）：Solana 的 nft_transfers / logs / balances 仍不支持；
CPI 节点的 `from_address` 对 depth-1 为空（日志不记录顶层调用者），lamport 金额只在
System Program 内层转账数与节点数一致时配对赋值，否则保持 0 并计数 `cpi_value_unpaired`。

### 2.6 回滚

- 二进制：`backend/bin/api.bak-solana-20260920T030408Z`、`worker.bak-solana-…`（重启即回退）。
- DB：`0072` 只新增行（`chain_capabilities` / `provider_descriptors` / `routing_policies`），
  回滚 = 删这三行 + 重启；保留数据不影响既有链。
- 前端：`~/deploy-frontend.sh` 自动保留 `dist.bak-*`。
