# 调查接口与 MCP 扩展

接入现有 `blockchain-platform-mcp`，入口仍为 `https://aurore.online/mcp`。工具总数从 38 增至 77（第一轮 71，本轮再补齐 6 个工具），原工具保留。REST 新接口为鉴权后的 POST `/api/v1/analysis/{tool_name}`；文件接口为 POST `/api/v1/export/get_export_file`、`download_export`。接口返回统一的 `success/data/meta/error`。

## 余额与资产快照

`get_native_balance`、`get_token_balance`、`get_all_token_balances`、`get_balance_at`、`get_balance_history`。

```json
{"chain":"bsc","address":"0x467a4c89ff241e3bde8de50d0a8f999c4a8a4d7f","token_contract":"0x55d398326f99059ff775485246999027b3197955","timestamp":"2026-08-02T20:41:01Z"}
```

省略区块和时间时读取当前区块；`block` 接受整数、十进制/十六进制字符串或链支持的标签。`timestamp` 使用带时区的 ISO 8601，选择该时刻之前最后一个实际区块，与 `block/blockTag` 互斥。历史读取要求 archive provider，失败会明确返回错误，不会改用历史净流入。

返回 `balance`、`balance_raw` 字符串，以及 `balance_source=rpc，balance_source_detail=rpc_state`、`as_of_block`、区块哈希、区块时间、`is_current_balance`。ERC20 的余额与 decimals/symbol 在同一区块读取；元数据失败时保留原始余额，格式化余额为 null。

全资产读取从已索引流水发现代币，或接受调用者提供的 `tokens`。每页 1–100 个，游标固定首次区块；`discovery_complete=false` 明确说明未观察到的代币可能遗漏。后续页属于同一冻结快照，不应当作重新刷新后的最新余额。历史序列是按 `interval_seconds` 采样的真实状态快照，每页 1–32 个样本，游标可继续读取全部样本。

本次快照和合约/RPC 功能支持 BSC、Ethereum、Polygon、Arbitrum、Optimism、Base、Avalanche。Solana/TRON 原有工具保留；本次没有声称它们支持任意历史状态。

## RPC、ABI 与控制权

`read_rpc` / `rpc_call` 只允许以下方法，另外也有同名 MCP 工具：

`eth_call`、`eth_getCode`、`eth_getStorageAt`、`eth_getTransactionByHash`、`eth_getTransactionReceipt`、`eth_getLogs`、`eth_getBalance`、`eth_blockNumber`、`eth_getBlockByNumber`。

直接方法接受 `block` 或 `blockTag`；`read_rpc` 使用原生 `params` 顺序。所有调用经过已有 RPC gateway，不接受自定义 endpoint、密钥、写方法或状态覆盖。`eth_call` 只接受零 value，gas 上限 10,000,000；logs 必须给 blockHash 或受链配置限制的明确区块范围。

`get_contract_abi` 使用现有缓存或 Sourcify v2 的验证 ABI；可以提供 `abi`，但调用者 ABI 不会标成已验证。`decode_transaction_input` 支持 tx_hash 或 address/input；`decode_logs` 支持 tx_hash 或最多 200 个原始日志，逐个保留解码证据与未解码状态。交易/日志按原始区块选择代理 implementation。未知 ABI/碰撞不会被当成已证明的函数。

`read_contract` 接受函数名或完整签名、args、ABI/输出类型；支持嵌套数组、tuple、整数、地址和 bytes。超出 JavaScript 安全整数范围的参数必须使用字符串。

```json
{"chain":"bsc","address":"0x55d398326f99059ff775485246999027b3197955","function":"owner()","args":[],"output_types":["address"]}
```

`resolve_proxy_implementation` / `resolve_proxy` 支持 EIP1967 implementation/admin/beacon 和标准 EIP1167，返回原始存储槽或字节码证据。自定义代理可能无法识别。

`get_contract_control` 返回 owner、admin、proxy admin、implementation、可核验部署交易的创建发送者、多签 owners/threshold、可枚举角色成员及 ABI 权限候选。非枚举角色、自定义授权逻辑和特殊部署方式可能不完整；pause/withdraw/mint/upgrade 函数存在不等于已证明某地址能够调用它。

## 全量分组与逐笔核对

`aggregate_transfers` 在服务端按交易/日志/代币去重，支持 counterparty、from_address、to_address、token_contract、direction、day 组合分组及金额、笔数、首末时间排序。必须选择 token 或 token_contract；不同资产保持独立金额总额。每页最多 1000，总分组数没有 200 名上限。

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

新增 `batch_get_balances`、`batch_get_contract_control`、`batch_get_labels`、`batch_get_receipts`、`batch_get_transaction_details`。已有 `batch_get_transactions` 继续按地址查询交易；按交易哈希读取用 `batch_get_transaction_details`。专用批量接受最多 200 个地址/哈希，保留每个结果的来源、错误和游标。

全部新单项操作均可放入 `batch_call_tools`，包括 RPC、解码、分组、文件与资金追踪。模式参数先验证；上游局部失败保留其他结果，只重试失败项。分页仍按每个地址分别继续，不把任务批量查询误作地址批量。

新普通链读取、余额、ABI、标签和控制权走 `read` scope；聚合、资金和交易所路径走 `analysis`；文件走 `export`。沿用已有 Bearer/OAuth、限流和工作区隔离。

## 验证

受影响 Go 包测试及 go vet 通过；MCP 21 项测试通过。真实 ClickHouse 的隔离夹具用 1002 条重复版本验证 501 个地址完整分页、去重和字符串精度。真实链上读取验证当前/历史/时间点余额、109 条事件解码、两个归因模型、批量回执、局部失败及文件资源拼接 SHA-256。

设计依据：[Ethereum JSON-RPC](https://ethereum.org/developers/docs/apis/json-rpc/)、[Solidity ABI](https://docs.soliditylang.org/en/latest/abi-spec.html)、[EIP1967](https://eips.ethereum.org/EIPS/eip-1967)、[Sourcify v2](https://docs.sourcify.dev/blog/apiv2-lookup-endpoints/)。


## 2026-10-01 正确性补齐

`trace_fund_batch` 接受 source_address、source_tx_hash、token_contract、amount、start_time/end_time、max_depth 和 attribution_model。默认 max_depth=5、attribution_model=both；支持 proportional/fifo/both。没有哈希时必须给完整源地址、合约和开始时间；多笔候选会返回 requires_source_selection，不自动追踪地址总流水。

每条模型边提供 tx_hash/block_number/timestamp/token_contract、actual_transfer_amount 和 attributed_amount（及各自 raw）、计算前源批次余额/总余额、source_share_before_transfer 和精确 source_share_fraction、source_batch_id、批次消耗前后余额。金额为字符串；比例显示小数保留 80 位，精确分数与原始整数是复核依据。归因标记 inferred，未校准 confidence 为 null。balance_basis 解释归因账本余额不是逐笔重新 RPC 读取；缺失流水补量通过 balance_gap_injected(_raw) 和 anomalies 明示。

两模型同时返回 model_consistency、proportional_result、fifo_result、difference_amount/raw、difference_ratio 与逐边比较。差额衡量终态分配差异，不能把多跳金额重复相加。

`find_fund_paths` 明确为地址下游历史活动图，is_source_fund_attribution=false。paths 保留完整根地址序列和所有 hops，不可作为指定源批次的确定去向。

新增 `batch_get_native_balances`、`batch_get_token_balances`：1–200 地址，服务器固定一个区块/时间点，返回 snapshot_shared=true，最多 8 并发。新增 `batch_decode_transactions`：1–200 交易哈希。原有专用批量扩至 200；昂贵操作受并发上限约束，逐项错误、证据及游标保留。混合 batch_call_tools 仍为 20 项，全部先验证，禁止嵌套批量。

资产选择使用 chain+token_contract；显式合约优先。注册表内 USDT/USDC 符号解析为对应链的精确合约，未知符号需给合约，不允许同名资产合并。返回 canonical_asset/canonical_contract/verified_token/symbol_confusable/spam_suspected/identity_source。verified_token 仅表示版本化资产注册表匹配。近似符号和不同合约的 BSC-USD 只产生疑似混淆标签，不作为欺诈定论。

ABI 解码明确 abi_source、abi_origin、selector_source、verified 和 confidence。缺少 ABI 时返回本地或 4byte 候选，碰撞保留；PUSH4/EQ 字节码证据只作启发。选择器缓存有一小时有效期，远端查询有超时和大小限制。旧 decode_transaction 的 EVM 路径也使用这些能力。代理按交易/日志区块选择 implementation，因此不会为满足固定回退顺序而错误使用当前实现 ABI。

控制权证据注明 evidence_source、block_number、原始 eth_call/slot/交易；可枚举角色/多签可核验，非枚举角色或自定义控制逻辑仍可能未知。角色命名哈希和 privileged ABI 函数只作候选。

实测 200 地址全部成功且共享区块；Case B 精确接收地址共 6 笔 79163.6899，min_amount=10000 为两笔 65729.7883。分别确认源回执并执行双模型，单跳累计归因不超过各自源金额，终态金额守恒；两份独立批次不冒充联合分配。完整验收和覆盖限制见随发布归档的差异与验收.md、case-b-verification.json、extended-verification.json。


## 2026-10-03 性能与缺陷修复

批量统一执行预算为 REST 单请求超时与 50 秒的较小值，HTTP/SDK 取消会停止后续派发并取消在途读取。已成功项和逐项游标保留，未派发项 started=false；写操作没有自动重试，已发起请求在超时/断线后是否完成需先核对任务状态。

REST 实际流最多 8 MiB；批量嵌入文件累计最多 8 MiB，保留的响应 JSON 最多 16 MiB。损坏文件、无效资源或超预算项单独报错，不使其他成功项丢失。对应错误包括 REST_RESPONSE_TOO_LARGE、BATCH_RESOURCE_LIMIT_EXCEEDED、BATCH_RESPONSE_LIMIT_EXCEEDED、EXPORT_INTEGRITY_FAILED、EXPORT_RESOURCE_INVALID、BATCH_DEADLINE_EXCEEDED 和 REQUEST_CANCELLED。文件继续使用 next_offset 分块读取和整文件 SHA-256 核对。

交易状态及金额筛选在最新版本去重后执行；原生金额换算保留完整小数，同时间记录分页增加稳定唯一键。全资产代币发现游标固定入库水位并使用 token_address 键分页；旧的无水位发现游标需重新开始。单合约聚合在一次扫描中返回完整总额、笔数和截止区块，空页仍返回完整总额。

资金账本去除零金额和自转账，历史余额只读取保留账本的付款方；时间前沿排除源资金到达前的扩展，同时允许重复证据在发现更早到达路径后重新判断。源回执和区块不一致返回 SOURCE_STATE_CHANGED。单模型只计算所选模型。

归因模型每次最多 2,000,000 工作单位、50,000 活动片段及 100,000 消耗审计，超限返回 HTTP 422 ALLOCATION_BUDGET_EXCEEDED；可缩小时间窗、记录数或选择单模型。输入使用 uint256 范围，合计使用任意精度整数。相邻无标记片段会合并，不跨越有标记 FIFO 片段；比例分摊按合并后片段舍入，最小代币单位可能与旧逐片段舍入不同，已在 assumptions 中披露。两种模型与覆盖限制、逐笔证据仍同时保留。

MCP 31 项测试、6 项隔离 ClickHouse 测试、受影响 Go 包及 go vet 通过；候选完成 200 地址共享区块批量、Case A/B 双模型守恒、完整分页/金额及导出资源 SHA-256 验证。性能原始数据见 baseline-performance.json、candidate-performance.json 和 benchmark-old/new.log。
