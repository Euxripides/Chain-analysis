# 已完成导出的付款地址汇总

MCP 工具：`get_export_address_summary`。

REST：`GET /api/v1/export/address-summary`，沿用现有 Bearer API Key 和 `export` 权限，且只允许读取当前 Actions 工作区内的导出。

接口直接读取对象存储中的已完成导出，校验整个文件的 SHA-256，支持 CSV、Parquet 及其 ZIP 封装。汇总的数据集为 `token_transfers`。

| 参数 | 含义 |
| --- | --- |
| `job_id` | 必填，导出任务 UUID |
| `token` / `token_contract` | 至少一个；符号存在多个合约时必须用合约地址消除歧义 |
| `direction` | `all`、`in`、`out`，默认 `all`；相对于 `address` |
| `address` | 方向参照地址；单地址导出自动推断，多地址导出的 `in/out` 必须传入 |
| `from_address` | 可选，限制某个完整付款地址，便于核对该地址的全部明细 |
| `page_size` / `cursor` | 付款地址统计分页，每页 1–500，默认 100 |
| `detail_page_size` / `detail_cursor` | 逐笔明细独立分页，每页 1–500，默认 100 |

示例 MCP 参数：

```json
{
  "job_id": "填写已完成的导出任务 UUID",
  "token": "USDT",
  "direction": "in",
  "page_size": 100,
  "detail_page_size": 100
}
```

响应位于现有 `success/data/meta/error` 信封的 `data` 中：

- `addresses`：完整付款地址、记录数、金额总额、原始整数金额总额、首末区块。按规范化后的完整付款地址升序返回；Solana/TRON 保留地址大小写。
- `next_cursor` / `has_more`：付款地址的下一页。末页游标为 `null`。
- `details`：逐笔金额、原始整数金额、完整付款/收款地址、交易哈希、区块，以及 `fields` 中的原始导出字段。`record_id` 由 ZIP 条目名和记录序号组成。
- `next_detail_cursor` / `details_has_more`：明细的独立下一页。可以用 `from_address` 单独核对一个付款地址。
- `total_records`、`total_addresses`、`total_amount`、`total_amount_raw`：筛选后**全部记录**的统计，所有页面一致。金额始终为精确十进制字符串；缺少可验证的原始整数时 `total_amount_raw` 为 `null`。
- `dataset_rows`：导出中的 token-transfer 记录总数；`export_total_rows` 为整个导出任务的元数据记录数，多数据集导出时两者可能不同。
- `data_cutoff_block`：导出 token-transfer 数据集中最高的已观察区块，以字符串返回；空数据集为 `null`。它不代表链头，也不证明之前所有区块均已覆盖。
- `source_sha256`：服务器完整读取和核对后的文件摘要。

`meta.count_semantics=export_records`：统计文件中实际保存的记录；多地址导出若在不同条目中保存了同一笔交易，各份记录均保留并计数，原始明细可据此核对。

继续分页时保持同一导出及筛选条件，分别使用返回的两个游标；每个维度遇到 `has_more=false` 就停止该维度的遍历。可以调整页大小。游标经过签名并绑定文件摘要与筛选条件，API 重启后需从第一页重新开始。

未完成导出返回 `409 EXPORT_NOT_COMPLETED`；其他工作区或不存在的任务返回 `404 JOB_NOT_FOUND`。文件不完整、摘要不符、无 token-transfer 数据、金额精度不明或符号匹配多个合约时返回明确错误，不返回部分汇总或下载链接。
