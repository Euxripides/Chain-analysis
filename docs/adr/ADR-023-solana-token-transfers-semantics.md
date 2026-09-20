# ADR-023：Solana token_transfers 的指令解析口径

| 项 | 值 |
|---|---|
| 状态 | Accepted（2026-09-20，产品已批准三项口径：gross/net 分列、证据详情展示 ATA 与手续费、CPI 调用树独立数据集） |
| 关联 | AGENTS.md、ADR-003 权限声明、`internal/provider/solana`（commit 835b6ca）、`acceptance-evidence/dual-system-v1/SOLANA-AND-POLYGON-DOWNLOAD-20260920.md` |
| 触发项 | 「Solana 能不能下 token_transfers」；当前 `chainDatasetsByChain["solana"]` 只有 `transactions` |

## 背景

2026-09-20 已上线 Solana 的 `transactions` 下载（标准 JSON-RPC：`getSignaturesForAddress` + `getTransaction`，commit 835b6ca）。
`token_transfers` 没做，根因是 Solana 与 EVM 的代币转账**来源不同**：

- EVM：代币合约 emit `Transfer(from,to,value)` 日志，`ankr_getTokenTransfers` 直接给结构化行（现有 5 条 EVM 链都是这样）。
- Solana：没有日志。转账是交易里的 **instruction**，由 SPL Token 程序（`Tokenkeg…`）或 Token-2022（`TokenzQd…`）执行，必须自己解码。

实测样本（Ankr Solana RPC，2026-09-20）：

```
交易 fQyniXxUj5Ytxcmw7H1VRXcEB9bwfFJiMN1XEj6F2YhKBCX1KbmtfkSwexZDjQ91y3ifwyQEYCUqS48gNgUNp6v
slot 448611120，外层指令 #3，programId = Tokenkeg…（spl-token）
parsed.type = transferChecked
  source      = 7KJjY7rArbydeLBF7gQ5LdqXRKRYyPArT99NEctsHsgU   ← 代币账户（ATA）
  destination = HNhDsdRNVv4UPjxwnrLDfVay3SFjx4wQ984bcDfSfoB   ← 代币账户（ATA）
  authority   = 5tzFkiKscXHK5ZXCGbXZxdw7gTjjD1mBwuoFbhUvuAi9   ← 转出方钱包
  mint        = EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v（USDC）
  amount      = 3090000000，decimals = 6

原始 data（base58 解出）：0c 80a82db800000000 06
  0x0C = 12 = TransferChecked；随后 8 字节小端 = 3090000000；再 1 字节 = decimals

meta.preTokenBalances：
  accountIndex 2 → owner 5tzFki…（转出钱包）
  accountIndex 3 → owner CGw7M5mw9KnnzF7kgBku7psEHRCuaUjmUHJM33atrhqs（**转入钱包**）
```

由此产生三个必须先定口径、再写代码的问题：

1. `from/to` 记代币账户还是钱包 owner？指令里只有代币账户，**转入方钱包不在指令里**（上例的 `CGw7M5mw…` 只能从 `preTokenBalances` 得到）。
2. Token-2022 的转账手续费（transfer fee）：指令金额 ≠ 收款人实收，`amount_raw` 记哪个？
3. 一笔交易可能含几十条指令（外层 + CPI 内层），ClickHouse 的 `log_index UInt32` 如何为 Solana 提供排序键？

## 决策

### D1：`from_address` / `to_address` 记**钱包 owner**，不记代币账户

与 EVM/TRON 语义对齐（地址维度是钱包），否则同一案件里跨链聚合、地址详情、图谱都会对不上。
代币账户只是解析中间量；若将来需要展示 ATA，另加字段，不改变本列语义。
**解析不出 owner 时不得写猜测值**：该条记为 `owner_unresolved` 并跳过，同时计数上报。

### D2：owner 解析优先用零额外 RPC 的 `preTokenBalances` / `postTokenBalances`

`meta.preTokenBalances[].accountIndex` 指向 `message.accountKeys` 的下标，条目自带 `owner` 与 `mint`，正好把 ATA 映射成钱包。
仅在该映射缺失时才回退 `getAccountInfo`（每次解析有界、带缓存、并发受 provider 限额约束）。
**禁止**「每条 transfer 一次 getAccountInfo」——N+1 外部调用，慢且烧额度。

### D3：`log_index = outer_index * 256 + inner_index`（外层 inner=0，CPI 内层 1..255）

- 外层 #3 → `3*256+0 = 768`；外层 #3 的第 2 条内层 → `3*256+2 = 770`。
- 单笔交易外层指令数 < 2^24、单外层内层数 < 256；越界即拒绝该条并记录，**不静默截断**。
- Solana 没有日志序号，这是**合成排序键**，必须在文档与前端口径里写明「不是链上原生字段」。

### D4：外层与 CPI 内层都扫，按 `(signature, outer, inner)` 去重

只扫外层会系统性漏掉聚合器（Jupiter 等）内部执行的转账；两层都收时用上述三元组做唯一键。
**不得**按 `(from,to,mint,amount)` 去重——同一交易里相同金额的重复转账是合法的。

### D5：Token-2022 单独维护判别表，带费转账按扩展处理

- program id 分两类：SPL Token 与 Token-2022；两者的指令判别值表**分开维护**并以单测冻结
  （Token-2022 的 `TransferCheckedWithFee` 属于 TransferFee 扩展的子指令，判别值与 SPL Token 不同，实现时以官方 program 可见判别表为准）。
- 未识别的 Token-2022 扩展指令：记 `unsupported_extension`（原始 data 进 raw），**不静默丢弃、不猜金额**。

### D6：`amount_raw` = **净额（net，实际移动/到账）**；新增 `gross_amount_raw` / `fee_amount_raw` 分列

产品决策（2026-09-20）：必须能区分 gross / net，因此扩 ClickHouse 列，而不是把费用只塞进 raw。

- `blockchain.token_transfers` 新增 `gross_amount_raw String`、`fee_amount_raw String`（既有 `amount_raw` / `amount` 语义统一为**净额**）。
- 不变量（跨链一致，单测冻结）：`amount_raw = gross_amount_raw - fee_amount_raw`；EVM/TRON 无费用概念时 `gross_amount_raw = amount_raw`、`fee_amount_raw = "0"`；Solana 的 SPL Token（非 Token-2022）同理。
- Token-2022 带费转账：`gross_amount_raw` = 指令声明金额，`fee_amount_raw` = 代扣手续费，`amount_raw` = 收款人实收（与 `pre/postTokenBalances` 差额自洽）。
- `amount Decimal(76,38)` 仍由 `amount_raw` + `token_decimals` 换算，即展示净额。

### D7：失败交易不产出 token_transfers 行

`meta.err != null` 的交易整体跳过（与 EVM 失败交易没有 Transfer 日志一致），并计入 usage 的 `skipped_failed_tx`。

### D8：编码用 `jsonParsed`，兜底 `json`；**不使用 `base64`**

实测 Ankr 的 `getTransaction(encoding=base64)` 返回 `transaction = [wire_tx_base64, "base64"]`——
**没有解析好的 message**，要自己反序列化 Solana wire 格式。因此：
首选 `jsonParsed`（节点已给 `parsed.type/info`）；节点未返回 parsed 时回退 `json`（结构已解、指令 data 是 base58，按 D5 判别表自己解）。
自研 wire 反序列化被明确否决（见下表）。

### D9：只有实现 + 验收通过后，才把新数据集加进 `chainDatasetsByChain["solana"]`

口径落地前，`/api/v2/meta/datasets?chain=solana` 必须继续只返回已实现的数据集——前端不暴露做不到的能力。

### D10：证据详情展示代币账户（ATA）与手续费明细

产品决策（2026-09-20）：`blockchain.token_transfers` 新增 `from_token_account` / `to_token_account`，
`blockchain.address_activity` 同步新增 `token_account`（主体在该笔转账中的 ATA）、`counterparty_token_account`、`fee_amount_raw`。
地址详情（Token Transfer 行）的详情视图必须能展开看到：主体 ATA、对手方 ATA、gross / fee / net 三个金额。
非 Solana 链这些列写空串，前端只在非空时展示（绝不显示伪造的 ATA）。

### D11：CPI 调用树作为独立数据集 `internal_transactions`

产品决策（2026-09-20）：Solana 的 CPI 调用树单独成数据集，复用既有 `blockchain.internal_calls` 表：

- 每个 CPI 节点一行：`from_address` = 发起方 program，`to_address` = 被调用 program，
  `call_type` = `cpi`，`depth` = 嵌套深度，`trace_path` = 稳定的路径串（如 `3.0.1`），
  `method_id` = 指令判别值或 `parsed.type`（取得到时），`success` 取自交易 `meta.err`，
  `call_index` 用与 D3 相同的 `outer*256+inner` 合成键。
- `value_raw` 只在节点确实是系统转账（System Program `transfer`）时填 lamports，其余为 `0`——
  不把代币金额塞进 `value_raw` 冒充原生转账。
- 该数据集同样受 D9 约束：实现 + 验收通过后才出现在链级白名单。

## 被否决的方案

| 方案 | 否决理由 |
|---|---|
| from/to 记代币账户（ATA） | 与 EVM/TRON 语义不一致，跨链地址聚合、案件关联、地址详情都会错 |
| 每条 transfer 调 `getAccountInfo` 解 owner | N+1 外部调用，烧额度且拖慢下载；`preTokenBalances` 已免费给出 owner |
| 只扫外层指令 | 漏掉 CPI（程序内转账），系统性少数据且难以察觉 |
| base64 + 自研 wire 反序列化 | 重复实现 Solana 序列化格式，回归风险高；json/jsonParsed 已足够 |
| `amount_raw` 记收款人实收（net） | 与 EVM 日志口径分裂；费用信息可以放 raw，等产品需要再加列 |
| 把 nft_transfers / logs / balances 一起做 | 范围蔓延：SPL NFT 与 Token-2022 元数据是另一套语义，应各自决策 |

## 影响

- **正向**：Solana 与 EVM/TRON 在 `token_transfers` 上语义一致；gross/net/手续费可查询、可核对；ATA 可追溯到具体代币账户；CPI 调用树补齐程序间调用视角。
- **代价**：provider 增加指令解码器与 CPI 提取（约 1.5 天，含判别表常量与 fixture 单测）；owner 映射缺失时有有界回退调用；
  ClickHouse 与 `address_activity` 各加少量列（String，默认空串，向后兼容）。
- **边界**：`nft_transfers` / `logs` / `balances` 仍不支持；Solana 仍只接受显式 slot 区间（见 `solanaRangeGuard`）。

## 验证计划（实现完成时必须全部通过）

1. 单测 fixture：`transferChecked` 正常样本、CPI 内层样本、Token-2022 带费样本、失败交易样本、owner 映射缺失样本、CPI 树样本。
2. 真链对账：同一笔交易解出的 transfer 集合必须与 `pre/postTokenBalances` 的账户差额一致；不一致即 `reconciliation_mismatch`，必须可见，绝不静默。
3. 金额不变量：每一行都满足 `amount_raw = gross_amount_raw - fee_amount_raw`（EVM/TRON 与无费的 Solana 行为 `fee="0"`），由单测与一次线上抽样断言。
4. 一次真实下载任务：ClickHouse 行的 `from/to` 是钱包、`token_address` = mint、`log_index` 可按 (outer,inner) 复算、ATA 与 fee 列有值；Coverage / MinIO raw 三层证据齐全。
5. CPI 树：`internal_calls` 行的 `from/to` 是 program、`depth/trace_path` 与交易内嵌套一致、`success` 与 `meta.err` 一致。
6. 前端：`/api/v2/meta/datasets?chain=solana` 返回三类；Dataset 选择与创建流程在桌面端与移动端各验一次；
   地址详情的 Token Transfer 行详情能看到主体 ATA、对手方 ATA 与 gross/fee/net。

## 回滚方式

1. **代码**：从 `chainDatasetsByChain["solana"]` 移除 `token_transfers`，并把 provider 该 dataset 改回 `unsupported`（一次提交即可收回能力面）；
2. **数据**：已写入的 Solana `token_transfers` 行可按 `chain='solana'` 识别；无需迁移（历史保留，Coverage 行按链/ provider 区分）；
3. 本决策无 schema 变更、无权限变更，因此不涉及 expand-compatible 发布流程。

## 开放项

无。三项产品决策已在本 ADR 内落实（D6 / D10 / D11）；后续若要把 `nft_transfers` / `logs` / `balances` 也纳入 Solana，
按 README 规则 2 另开 ADR，不修改本文正文。
