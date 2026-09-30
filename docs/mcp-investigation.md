# 调查接口与 MCP 扩展

接入现有 `blockchain-platform-mcp`，入口仍为 `https://aurore.online/mcp`。工具总数从 38 增至 71，原工具保留。REST 新接口为鉴权后的 POST `/api/v1/analysis/{tool_name}`；文件接口为 POST `/api/v1/export/get_export_file`、`download_export`。接口返回统一的 `success/data/meta/error`。

## 余额与资产快照

`get_native_balance`、`get_token_balance`、`get_all_token_balances`、`get_balance_at`、`get_balance_history`。

```json
{"chain":"bsc","address":"0x467a4c89ff241e3bde8de50d0a8f999c4a8a4d7f","token_contract":"0x55d398326f99059ff775485246999027b3197955","timestamp":"2026-08-02T20:41:01Z"}
```

省略区块和时间时读取当前区块；`block` 接受整数、十进制/十六进制字符串或链支持的标签。`timestamp` 使用带时区的 ISO 8601，选择该时刻之前最后一个实际区块，与 `block/blockTag` 互斥。历史读取要求 archive provider，失败会明确返回错误，不会改用历史净流入。

返回 `balance`、`balance_raw` 字符串，以及 `balance_source=rpc_state`、`as_of_block`、区块哈希、区块时间、`is_current_balance`。ERC20 的余额与 decimals/symbol 在同一区块读取；元数据失败时保留原始余额，格式化余额为 null。

全资产读取从已索引流水发现代币，或接受调用者提供的 `tokens`。每页 1–100 个，游标固定首次区块；`discovery_complete=false` 明确说明未观察到的代币可能遗漏。后续页属于同一冻结快照，不应当作重新刷新后的最新余额。历史序列是按 `interval_seconds` 采样的真实状态快照，每页 1–32 个样本，游标可继续读取全部样本。

本次快照和合约/RPC 功能支持 BSC、Ethereum、Polygon、Arbitrum、Optimism、Base、Avalanche。Solana/TRON 原有工具保留；本次没有声称它们支持任意历史状态。

## RPC、ABI 与控制权

`read_rpc` 只允许以下方法，另外也有同名 MCP 工具：

`eth_call`、`eth_getCode`、`eth_getStorageAt`、`eth_getTransactionByHash`、`eth_getTransactionReceipt`、`eth_getLogs`、`eth_getBalance`、`eth_blockNumber`、`eth_getBlockByNumber`。

直接方法接受 `block` 或 `blockTag`；`read_rpc` 使用原生 `params` 顺序。所有调用经过已有 RPC gateway，不接受自定义 endpoint、密钥、写方法或状态覆盖。`eth_call` 只接受零 value，gas 上限 10,000,000；logs 必须给 blockHash 或受链配置限制的明确区块范围。

`get_contract_abi` 使用现有缓存或 Sourcify v2 的验证 ABI；可以提供 `abi`，但调用者 ABI 不会标成已验证。`decode_transaction_input` 支持 tx_hash 或 address/input；`decode_logs` 支持 tx_hash 或最多 200 个原始日志，逐个保留解码证据与未解码状态。交易/日志按原始区块选择代理 implementation。未知 ABI/碰撞不会被当成已证明的函数。

`read_contract` 接受函数名或完整签名、args、ABI/输出类型；支持嵌套数组、tuple、整数、地址和 bytes。超出 JavaScript 安全整数范围的参数必须使用字符串。

```json
{"chain":"bsc","address":"0x55d398326f99059ff775485246999027b3197955","function":"owner()","args":[],"output_types":["address"]}
```

`resolve_proxy_implementation` 支持 EIP1967 implementation/admin/beacon 和标准 EIP1167，返回原始存储槽或字节码证据。自定义代理可能无法识别。

`get_contract_control` 返回 owner、admin、proxy admin、implementation、可核验部署交易的创建发送者、多签 owners/threshold、可枚举角色成员及 ABI 权限候选。非枚举角色、自定义授权逻辑和特殊部署方式可能不完整；pause/withdraw/mint/upgrade 函数存在不等于已证明某地址能够调用它。

## 全量分组与逐笔核对

`aggregate_transfers` 在服务端按交易/日志/代币去重，支持 counterparty、from_address、to_address、token_contract、direction、day 组合分组及金额、笔数、首末时间排序。必须选择 token 或 token_contract；不同资产保持独立金额总额。每页最多 500，总分组数没有 200 名上限。

```json
{"chain":"bsc","address":"0x467a4c89ff241e3bde8de50d0a8f999c4a8a4d7f","token_contract":"0x55d398326f99059ff775485246999027b3197955","direction":"in","group_by":["counterparty"],"order_by":"sum_amount","page_size":100}
```

返回完整匹配记录数、分组数、`totals_by_asset`、金额字符串及下一页游标。`data_cutoff_block` 是匹配流水中最大观察区块；`snapshot_upper_block` 是查询区块上限，`meta.coverage` 单独描述索引覆盖。总额使用 ClickHouse 已保存的 Decimal256(38) 精度，原始整数金额仍可通过流水或导出明细核对。游标绑定过滤条件、区块与入库时间，API 重启后需重新开始分页；持续入库或替换旧记录时应重新核对总额。

逐笔证据使用原有 `get_token_transfers` 或 `get_export_address_summary` 的独立明细游标，原始导出字段保持可读。

## MCP 文件资源

`get_export_file`、`download_export` 直接返回文件对象与嵌入的 MCP blob resource，无须公网下载链接。默认 1 MiB 分块，最多 4 MiB。`offset/next_offset` 分页；每次读取检查工作区归属。

资源 URI：`export://{job_id}/{offset}/{chunk_size}`。MCP `resources/read` 可直接读取该资源。每块包含 `chunk_sha256`，整文件包含原导出的 `sha256`；完整文件一次读出时校验整文件 SHA-256。分块文件需拼接后校验整文件 SHA-256，`full_integrity_verified=false` 不会被伪装成整文件已验证。

## 资金批次、标签与交易所路径

`trace_funds` 选择确定的 ERC20 `source_tx`，必要时加 `log_index`；`amount` 为人类单位的十进制字符串，省略时取该笔完整转账。自动同时返回 proportional 和 fifo 两种模型、区块前真实余额基线、原始流水证据、归因整数、残留金额和守恒校验。

默认 max_hops=3、time_window=604800 秒、max_nodes=50、max_records=5000；上限分别为 5、366 天、100、20000。达到预算或覆盖不足时明确标记不完整。两种模型是可比较的会计归因假设；缺失流水、重基准/代扣等特殊代币行为可能增加不确定性，不能证明混币地址中可替代单位的真实身份。

`get_labels` 返回结构化 `label_source/confidence/first_seen`，来源包括版本化的公开交易所标签、当前工作区的用户标签及链上分类探针。初始公开库覆盖部分 Binance、OKX、LBank 地址，不是完整交易所数据库；未知首次时间为 null。Pair、Router、Token、Multisig、Proxy 与 Bridge 候选均保留来源和置信度。

`detect_exchange_deposit`、`trace_to_exchange` 区分直接被标记的交易所钱包和推测的用户充值/中转路径，附逐跳汇总及原始流水查询参数。同一资产与时间顺序参与路径筛选。通向热钱包的流水不足以证明充值账户或用户身份；节点/页预算和标签覆盖不足会显式说明。

## 批量与权限

新增 `batch_get_balances`、`batch_get_contract_control`、`batch_get_labels`、`batch_get_receipts`、`batch_get_transaction_details`。已有 `batch_get_transactions` 继续按地址查询交易；按交易哈希读取用 `batch_get_transaction_details`。专用批量接受最多 20 个地址/哈希，保留每个结果的来源、错误和游标。

全部新单项操作均可放入 `batch_call_tools`，包括 RPC、解码、分组、文件与资金追踪。模式参数先验证；上游局部失败保留其他结果，只重试失败项。分页仍按每个地址分别继续，不把任务批量查询误作地址批量。

新普通链读取、余额、ABI、标签和控制权走 `read` scope；聚合、资金和交易所路径走 `analysis`；文件走 `export`。沿用已有 Bearer/OAuth、限流和工作区隔离。

## 验证

受影响 Go 包测试及 go vet 通过；MCP 20 项测试通过。真实 ClickHouse 的隔离夹具用 1002 条重复版本验证 501 个地址完整分页、去重和字符串精度。真实链上读取验证当前/历史/时间点余额、109 条事件解码、两个归因模型、批量回执、局部失败及文件资源拼接 SHA-256。

设计依据：[Ethereum JSON-RPC](https://ethereum.org/developers/docs/apis/json-rpc/)、[Solidity ABI](https://docs.soliditylang.org/en/latest/abi-spec.html)、[EIP1967](https://eips.ethereum.org/EIPS/eip-1967)、[Sourcify v2](https://docs.sourcify.dev/blog/apiv2-lookup-endpoints/)。
