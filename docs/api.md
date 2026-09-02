# API V1

Base: `http://SERVER:8080/api/v1`（全部 JSON，时间 UTC）

## 下载任务

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/datasets/ensure` | 创建下载任务（Dataset Service 统一入口） |
| POST | `/datasets/coverage/preview` | 本地 Coverage 预览（复用比例/缺口） |
| GET | `/download-jobs?status=&limit=&offset=` | 任务列表（含进度/行数/请求/成本聚合） |
| GET | `/download-jobs/{id}` | 任务详情（Job + Subjects + Datasets + Shards + 进度） |
| POST | `/download-jobs/{id}/pause` | 暂停（Shard 边界收敛，Checkpoint 落库） |
| POST | `/download-jobs/{id}/resume` | 恢复（重算 Coverage 后只下缺口） |
| POST | `/download-jobs/{id}/cancel` | 取消（已完成数据保留，进入全局 Coverage） |
| GET | `/download-jobs/{id}/events` | SSE 实时事件流 |

### 创建任务示例

```json
POST /api/v1/datasets/ensure
{
  "source": "user_download",
  "priority": 100,
  "subjects": ["0x..."],
  "chain": "bsc",
  "datasets": ["transactions", "token_transfers", "nft_transfers", "logs", "balances"],
  "range": { "type": "full" },
  "reuse_policy": "prefer_local"
}
```

`range.type`：`full` | `block`（from_block/to_block）| `time`（from/to RFC3339）。

## 数据查询（统一数据面）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/datasets/{dataset}?chain=&subject=&limit=&offset=` | 查询 ClickHouse（FINAL 去重） |
| GET | `/coverage?chain=&subject=&dataset=` | 查询 Coverage 区间 |
| GET | `/coverage/stats` | 各 Dataset Coverage 汇总 |

## 导出（异步生成 MinIO 对象）

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/exports` | format: csv / xlsx / json(JSONL) / parquet；支持 addresses/datasets/include_meta |
| GET | `/exports/{id}` | 导出状态、真实行数、对象大小和 SHA-256 |
| GET | `/exports/{id}/download` | API 代理下载；支持 HTTP Range / 206 |
| POST | `/exports/{id}/download-ticket` | 创建 15 分钟下载 Ticket |
| GET | `/download/{ticket}` | 校验 Ticket 后由 API 流式下载；支持 HTTP Range / 206 |

超过 3 个地址时自动生成 ZIP，结构为 `addresses/{address}/{dataset}.{ext}` 加 `manifest.json`。当前单次导出最多读取 2,000,000 行；超大导出仍应按地址/数据集拆分，后续再接真正的分批游标写入。

## 地址活动（资金分析统一入口，§87）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/addresses/{address}/activity?chain=&limit=&offset=` | 活动表（direction/activity_type/counterparty/amount） |

## 导出补充参数

`POST /exports` 支持 `subject`（按地址导出）与 `chain`（数据浏览页导出时指定链）。

## V1.2 数据浏览、元数据与增量更新

| 方法 | 路径 | 说明 |
|---|---|---|
| GET/POST | `/cases` | 案件列表 / 新建案件 |
| DELETE | `/cases/{id}` | 删除案件并安全重选地址的主案件 |
| GET/POST | `/tags` | 标签列表 / 新建标签 |
| DELETE | `/tags/{id}` | 删除标签 |
| PATCH | `/addresses/{chain}/{address}/metadata` | 更新显示名、案件、主案件和标签 |
| GET | `/data-explorer/addresses` | 地址目录；服务端筛选和 keyset cursor |
| POST | `/data-explorer/query` | 多地址统一查询；稳定 keyset cursor |
| POST | `/updates/preview` | 增量缺口预览 |
| POST | `/updates/{id}/confirm` | 冻结预览目标并创建缺口下载任务 |

## V1.3 Transaction Method / ABI 解析

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/transactions/{chain}/{tx_hash}?decode=local_only\|auto\|force_refresh` | 交易详情 + Method + Decoded Input + Logs |
| GET | `/methods/{selector}` | Selector 查询，返回全部候选签名与碰撞标记 |
| GET | `/methods/search?q=&limit=` | Method 名称 / 签名 / Selector 搜索 |
| GET | `/methods/enrichment?chain=` | Method 解析覆盖率（与下载 Coverage 解耦） |
| POST | `/admin/methods/backfill` | 创建历史数据回填任务（admin） |
| GET | `/admin/methods/backfill` | 回填任务列表（admin） |
| GET | `/admin/methods/backfill/{id}` | 回填任务进度与 checkpoint（admin） |
| POST | `/admin/methods/backfill/{id}/{pause\|resume\|cancel}` | 回填任务控制（admin） |
| POST | `/admin/methods/signatures` | 人工覆盖签名 manual_override（admin） |
| POST | `/admin/contracts/{chain}/{address}/abi` | 上传合约 ABI（admin） |

`decode` 语义：

- `local_only`：只读本地，永远不产生 Provider 请求；
- `auto`（默认）：本地解码缓存 → 本地 ABI 解码 → 仍失败才向 Provider 请求一次并缓存；
- `force_refresh`：跳过缓存强制重新深度解码，需要 owner/admin/analyst/investigator 角色。

同一 `tx_hash` 第二次 `auto` 请求不会再访问 Provider（`decode_cache_hit=true`）；
每次真实请求都会写 `provider_usage`，`usage_type=tx_deep_decode`。

### 交易详情响应（节选）

```json
{
  "tx_hash": "0x...",
  "input": "0xa9059cbb...",
  "method": {
    "id": "0xa9059cbb", "name": "transfer", "signature": "transfer(address,uint256)",
    "source": "contract_abi", "verified": true, "confidence": "verified",
    "method_type": "contract_call", "category": "transfer", "risk": false
  },
  "decoded_input": { "to": "0x...", "value": "1000000000000000000" },
  "decode_source": "contract_abi",
  "decode_cache_hit": false
}
```

### Method Lookup 响应

```json
{
  "selector": "0xa9059cbb",
  "candidates": [
    { "name": "transfer", "signature": "transfer(address,uint256)", "source": "local_signature", "verified": true }
  ],
  "collision": false
}
```

### Data Explorer Method 筛选

`POST /data-explorer/query` 的 `filters` 新增：`method`（统一搜索：名称/签名/selector/`unknown`）、
`method_id`（可逗号分隔多个 selector）、`method_name`、`method_signature`、`method_source`、`method_verified`。

`POST /exports` 新增 `include_decoded_input`（默认 false）；transactions 导出默认包含
`method_id / method_name / method_signature / method_source / method_verified`。

## Provider / 健康 / 观测

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/providers/ankr/status` | 断路器状态/错误率/连续失败 |
| GET | `/providers/ankr/usage?hours=24` | 按 Dataset 的请求/Credits/成本/行数/字节 |
| GET | `/health/live` `/health/ready` | 存活/就绪（PG/CH/MinIO/NATS） |
| GET | `/metrics` | Prometheus 指标（API:8080；Worker 独立 :8081） |
| GET | `/api/v1/me` | 当前用户上下文（auth 接口预留，X-User-* 头） |

## V1.5 统一链 RPC

所有端点都经 `ChainRead -> RPCGateway -> Router -> Provider`，不提供公开 Raw RPC，也不会返回密钥或内部 endpoint。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/system/chains/{chain}/capabilities` | 当前链聚合能力（standard/archive/batch/trace/debug） |
| GET | `/chains/{chain}/head` | 实时链头、安全高度和缓存命中状态 |
| GET | `/addresses/{chain}/{address}/runtime` | EOA/合约候选、原生余额、链头 |
| GET | `/transactions/{chain}/{tx_hash}/receipt` | 类型化交易回执 |
| GET | `/transactions/{chain}/{tx_hash}/trace?mode=local_only\|auto\|force_refresh` | 规范化调用树；Analyst/Investigator 及以上 |
| GET | `/transactions/{chain}/{tx_hash}/internal-calls` | 已持久化的派生内部活动，不触发 Provider |

`trace` 不可用时返回 `503 + error_class=trace_unsupported`，其它交易和代币功能不受影响。`local_only` 缓存未命中返回 404，预算耗尽或限流返回 429。

## SSE 事件

```text
data: {"type":"job_status","job_id":"...","status":"RUNNING","progress":0.42,"ts":"..."}
data: {"job_id":"...","status":"RUNNING","progress":0.42,"downloaded_rows":...,"rows_per_sec":...,"bytes_per_sec":...,"req_per_sec":...,"api_cost_usd":...,"coverage_hit_ratio":...,"elapsed_seconds":...,"eta_seconds":...,"ts":"..."}
```

`events.jobs.{id}` 状态变化；`events.progress.{id}` 1s 聚合进度；`events.shards.{id}` Shard 状态变化。
