# ADR-024：Solana 的 logs / balances / nft_transfers 口径与实施顺序

| 项 | 值 |
|---|---|
| 状态 | Accepted（2026-09-20；产品指定顺序：logs → balances → nft） |
| 关联 | ADR-023（Solana 指令解析口径）、`model.ChainDatasets`、`internal/provider/solana` |
| 触发项 | 「nft_transfers、logs、balances 实现不了？」→ 产品确认按 logs → balances → nft 推进 |

## 背景

ADR-023 先做了 transactions / token_transfers / CPI 调用树，并把其余三类排除在范围外。
2026-09-20 用现有 Ankr Solana RPC 实测了它们各自的可行性：

| 探针 | 结果 | 含义 |
|---|---|---|
| `getBalance` | OK | SOL 余额可取 |
| `getTokenAccountsByOwner`（jsonParsed） | OK，单地址 **3835** 个代币账户，mint/amount/decimals 齐全 | balances 可实现 |
| `getAccountInfo`(mint) | OK（decimals=6、supply=8016…） | NFT 可按 decimals/supply 判定 |
| `getProgramAccounts` | **-32010** excluded from account secondary indexes | 批量 Metaplex 元数据这条路被封 |
| `getAsset`（DAS） | **-32601** Method not found | Ankr 无 DAS，nft 名称/symbol 只能另找来源 |

三类与 EVM 原语义的差距不同：balances 只差「快照而非历史」，nft 只差「没有 tokenId 与元数据」，
**logs 的差距最大**——Solana 根本没有 EVM 意义的日志，只有 `meta.logMessages`（程序日志文本
与 `Program data: <base64>` 程序事件），没有 topics、没有 indexed 参数、没有 event signature。

## 决策

### D1（第一个做）：`logs` 复用现有表，用 `event_source` 标注来源，不新开数据集名

行映射（逐字段，全部来自 `meta.logMessages` 的原始顺序，序号 1 起）：

| 列 | Solana 取值 |
|---|---|
| `address` | 产生该行日志的 program id（由 invoke 栈推断；推断不出时留空） |
| `topics` | 空数组（**没有 topics 概念，绝不塞伪 topic**） |
| `data` | `Program log:` 后的原始文本；`Program data:` 后的 base64 载荷原样保留 |
| `log_index` | 该交易内日志行序号（跨外层/内层统一递增，稳定可复算） |
| `removed` | 0（Solana 没有重组语义） |
| `event_source` | `solana_program_log` 或 `solana_program_data` |
| `event_name` | Anchor 事件名（能从 base64 判别式解出时），否则空 |
| `event_signature` / `event_verified` | 空 / 0（没有 EVM 签名，也没有 ABI 可校验——不伪装成已验证事件） |

- **为什么不新开 `program_logs` 数据集**：新增数据集名要连带新 ClickHouse 表、写入器、
  Explorer/导出/Coverage 全链路；而 `logs` 表本就有 `event_source` 作为来源字段，
  前端标签也是「事件日志」。用 `event_source` 表达来源比新表更便宜也更少改动面。
- **硬要求**：任何按 `event_signature` / `topics` 过滤或解码的读路径，必须显式排除
  `event_source LIKE 'solana_%'`；口径弹窗必须写明 Solana 行没有 topics/indexed 参数。
  验收里加一条断言，防止将来有人把 Solana 行喂进 EVM 事件解码器。
- 日志行数远大于交易数（一笔交易几十行很常见），页大小与内存预算按行计。
- 失败交易（`meta.err != null`）的日志**照常落库**——排障时日志正是证据。

### D2（第二个）：`balances` 按所有者快照，拿不到的元数据一律留空

- 来源：`getBalance`（lamports）+ `getTokenAccountsByOwner`（jsonParsed，SPL Token 与 Token-2022 各一次）。
- 行映射：`token_address`=mint、`balance_raw`=amount、`token_decimals`=decimals、
  `token_type`=`SPL` / `SPL-2022`（按账户 owner program）、`snapshot_block`=取数时的 `getSlot` 槽位。
- **分片与上限**：单地址实测 3835 个代币账户。provider 必须按响应体字节分批，
  超限时**显式失败并计数 `balance_truncated`**，绝不静默截断（截断会让 Coverage 误判完整）。
- **空值诚实**：`token_symbol` / `token_name` / `balance_usd` / `token_price` 留空，
  不用 mint 地址冒充 symbol。
- 语义边界：这是**取数时刻的快照**，不是任意历史槽位的余额；`snapshot_block` 如实记录取数槽位。

### D3（第三个）：`nft_transfers` 先做经典 NFT，cNFT 明确不做

- 判定：mint 的 `decimals == 0 && supply == 1`（`getAccountInfo`，按 mint 有界缓存 + 负缓存）。
- **不双重计数**：判定为 NFT 的 transfer 不再写入 `token_transfers`；判定失败（mint 查不到）
  保守留在 `token_transfers` 并计数 `nft_classification_unknown`，绝不两头都写。
- 字段映射：`contract_address`=mint、**`token_id` 留空**（Solana 没有 tokenId，
  NFT 的身份就是 mint 地址本身；把 mint 塞进 token_id 会与 EVM 语义冲突）。
- 元数据：name/symbol 留空。`getProgramAccounts` 被共享端禁用、无 DAS（均实测），
  要名称只能对每个 mint 做 Metaplex PDA 单账户查询——那是独立的一笔投入。
- **压缩 NFT（cNFT / Bubblegum）不在本 ADR 范围**：它不移动 SPL 代币，是 Merkle 树状态变更，
  需要独立解码 Bubblegum 指令与树账户，应另开 ADR。

## 被否决的方案

| 方案 | 否决理由 |
|---|---|
| 新开 `program_logs` 数据集 | 新表 + 写入器 + Explorer/导出/Coverage 全链路成本，而 `event_source` 已能表达来源 |
| 只落 `Program log:`、丢弃 `Program data:` | 丢掉最有价值的锚定/程序事件载荷 |
| 给 Solana 日志塞伪 topics / 伪 event_signature | 让下游以为可以做 EVM 式事件解码，是比"没有数据"更坏的错误 |
| 用 mint 地址冒充 token_symbol | 不诚实：地址不是符号，前端展示会误导 |
| Solana NFT 把 mint 写进 `token_id` | 与 EVM 的 tokenId 语义冲突，去重键与查询口径都会错 |
| 同一笔转账同时写 token_transfers 与 nft_transfers | 重复计数，资金流会翻倍 |
| balances 超限时截断并标记完成 | 会让 Coverage 误判"已完整下载"，是最危险的一种降级 |

## 实施顺序与验收（每项完成才进链级白名单，沿用 ADR-023 D9）

1. **logs（先做）**：单测 fixture（`Program log:` / `Program data:` / 失败交易 / 序号连续性）
   → 真链一笔（USDC 那笔交易）核对行数与 `logMessages` 逐行一致 → Coverage/MinIO 三层证据
   → 前端口径弹窗写明"程序日志，无 topics"。
2. **balances**：单测（3835 账户分片、截断显式失败、空元数据）→ 真链交易所钱包快照
   → 断言行数与 RPC 返回一致且未截断。
3. **nft_transfers**：单测（decimals=0/supply=1 判定、缓存、未知归类计数、不双写）
   → 真链一笔经典 NFT 转账。

## 影响与回滚

- 影响：Solana 的 `logs` 行语义与 EVM 不同（有 `event_source` 标注）；`balances` 只有快照；
  `nft_transfers` 只有经典 NFT 且无元数据。
- 回滚：从 `model.ChainDatasets["solana"]` 移除对应数据集即可；**无 ClickHouse schema 变更**
  （复用现有表与列），已写入行按 `chain='solana'` 可识别/清理。

## 开放项（需产品确认）

1. Solana mint 元数据（symbol/name/logo）与 USD 价格的来源：是否引入付费 provider（DAS/Helius 等）。
2. cNFT 是否立项（独立 ADR）。
