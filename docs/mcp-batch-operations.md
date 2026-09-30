# 当前 MCP 的批量操作

接入已有的 `https://aurore.online/mcp`，共 38 个工具。沿用现有 Bearer 鉴权、权限和租户范围。

## 地址批量查询

| 内容 | 工具 | 单次上限 |
| --- | --- | --- |
| 地址概况 | `batch_get_address_overview` | 200 个地址 |
| 地址资产 | `batch_get_address_assets` | 200 个地址 |
| 代币转账明细 | `batch_get_token_transfers` | 20 个地址 |
| 交易明细 | `batch_get_transactions` | 20 个地址 |
| 内部交易 | `batch_get_internal_transactions` | 20 个地址 |
| 交易对手 | `batch_get_counterparties` | 20 个地址 |
| 资金流汇总 | `batch_get_flow_summary` | 20 个地址 |
| 主要资金流 | `batch_get_top_flows` | 20 个地址 |
| 首次资金来源 | `batch_get_first_funding` | 20 个地址 |
| Solana 签名列表 | `batch_get_solana_address_signatures` | 20 个地址 |
| 已完成导出的地址统计及原始明细 | `batch_get_export_address_summary` | 20 个地址 |
| 资金路径 | `batch_find_fund_paths` | 20 个起点地址 |

新增地址批量工具将原接口的 `address`（资金路径为 `source`）替换为 `addresses`。其余筛选参数保持一致。结果按输入顺序返回；已知 EVM 链按规范化地址去重，Solana/TRON 保留大小写。

```json
{
  "chain": "bsc",
  "addresses": ["0x1111111111111111111111111111111111111111", "0x2222222222222222222222222222222222222222"],
  "token": "USDT",
  "direction": "in",
  "page_size": 25,
  "pagination": [
    {"address": "0x1111111111111111111111111111111111111111", "page": 2},
    {"address": "0x2222222222222222222222222222222222222222", "page": 4}
  ],
  "max_concurrent": 4
}
```

以上参数适用于 `batch_get_token_transfers`。每个地址独立翻页，`pagination` 仅接受原接口支持的分页字段：`page`、`page_size`、`cursor`、`detail_cursor`、`detail_page_size`、Solana 的 `before`。游标应取自对应地址上一页的返回，并保持筛选条件一致。新增交易列表和汇总工具默认每地址 25 条，上限 500 条；Solana 签名仍遵守原接口的 `limit`（默认 20，上限 100）。

`batch_get_export_address_summary` 的公共参数为 `job_id`、`token` 或 `token_contract`、`direction`、可选 `from_address`、分页参数，以及 `addresses`。每项保留原汇总响应：完整付款地址统计、逐笔 `details` 和原始 `fields`、记录总数、金额总额、数据截止区块、地址及明细的各自下一页游标。它直接读取服务器上的已完成导出文件并校验校验和。金额为精确字符串，结果无需下载公网文件才能读取。

普通新增地址批量工具默认并发 4，可设 1–8；导出汇总并发 1–2。每个结果的 `response.meta` 保留原接口的数据来源、覆盖范围等信息；普通查询的范围仍以原接口为准。

## 多地址合并导出

已有 `create_large_export` 和 `POST /api/v1/export/create` 新增 `addresses` 数组，上限 100 个输入地址。`address` 与 `addresses` 必须选一种，单地址调用保持兼容。

```json
{
  "chain": "bsc",
  "addresses": ["0x1111111111111111111111111111111111111111", "0x2222222222222222222222222222222222222222"],
  "data_types": ["token_transfers", "transactions"],
  "token_contract": "0x55d398326f99059ff775485246999027b3197955",
  "format": "parquet",
  "compression": "zstd"
}
```

返回一个 `job_id`、规范化去重后的 `addresses`、`address_count`。每个地址和数据类型在压缩包中有独立文件。CSV 使用 ZIP；Parquet 文件使用 Zstd，多个文件再放入 ZIP。时间和代币筛选应用于各地址；原始记录保留用于核对。

地址之间的同一笔转账可能出现在多个地址的导出文件中。汇总的 `total_records` 按导出记录计数（`meta.count_semantics=export_records`），不会静默按交易哈希去重；同一交易也可能包含多条转账。`data_cutoff_block` 是导出代币转账中实际观察到的最高区块，不代表导出包含该区块之前的所有链上历史。

## 所有单项操作的通用批量入口

`batch_call_tools` 支持一次 1–20 个独立操作。覆盖全部现有单项工具，包括交易详情/日志/解码、Solana 或桥接源链读取、网络与路径分析、创建多个独立导出、导出状态和汇总、数据补齐、下载状态和失败重试。多地址网络分析仍可直接使用 `build_address_network.seed_addresses`；原有 `ensure_chain_data_batch` 也继续可用。通用入口不接受嵌套批量工具。

```json
{
  "requests": [
    {"tool": "get_address_assets", "arguments": {"chain": "bsc", "address": "0x1111111111111111111111111111111111111111"}},
    {"tool": "get_transactions", "arguments": {"chain": "bsc", "address": "0x2222222222222222222222222222222222222222", "page": 3}},
    {"tool": "get_export_status", "arguments": {"job_id": "00000000-0000-4000-8000-000000000001"}}
  ],
  "max_concurrent": 4
}
```

每项 `arguments` 按对应原工具的 schema 校验；缺少必要代币、导出地址形式冲突、格式压缩组合错误等会在开始任何请求前拒绝。服务器端的数据、地址有效性及权限错误作为对应项的独立失败返回。不同操作可使用不同链、地址、筛选和分页。包含导出汇总时实际并发最多 2，否则最多 8。

## 部分失败与重试

新增批量工具返回 `data.results[]`，每项有 `index`、`tool`、`input`、`success`，以及原始 `response` 或 `{code,message}` 错误；专用地址批量工具另有 `address`。`data.requested/succeeded/failed` 给出数量。全部成功时顶层 `success=true`；部分失败为 `success=false`、`error.code=BATCH_PARTIAL_FAILURE`，成功结果仍完整返回，不设 MCP `isError`。

批量操作不是事务，执行中失败不会撤销其他已成功项。检查所有结果，只重试失败项；涉及创建导出或下载任务时，避免整批重放造成重复任务。通用批量工具因此标记为可写工具。
