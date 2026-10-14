# 合约分析覆盖扩展实施方案

编写日期：2026-10-14。状态：用户于 2026-10-14 确认，进入分阶段实施。

用户随后明确选择“先把 EVM 合约分析做深做全”。本轮实现和上线验收以 EVM 为范围；TRON、Solana 实验成果保留在隔离候选中，入口默认关闭，不加入本轮正式支持范围。

## 1. 目标与现有基础

用户需要分析基本所有常见合约，重点是运行机制、每个方法的实际作用、执行条件、资金计算、权限、跨合约依赖和交易证据。扩展目标同时包括通用代码分析和业务解释；只有方法名称、伪代码或漏洞列表不满足本次要求。

本次只读核对 docs/contract-analysis-capabilities.md、现有处理器和上一轮验收记录。当前正式组合是 WhatsABI、sevm、精确 Solidity 编译器、Heimdall、自研资金模型和 Anvil。源码入口、IR、方法、权限、资金表达式、代理、选定交易重放和证据工作台已有基础。Slither、Gigahorse、TRON TVM、Solana sBPF 尚未接入。真实余额核对主要限于直接 ERC20 余额映射及代码哈希确认的 WETH9。EthereumHardfork 当前只为 chain_id=1 返回正式规则。

当前请求和输入校验直接使用 EVM 地址、32 字节交易哈希及 EVM 字节码，并要求 sevm/WhatsABI。加入 Solana 不能仅修改链枚举；需要按执行环境拆分身份、请求、快照、输入与方法模型。现有 v1 请求和结果继续可读，不改写历史证据。

此前 24 个引擎样本、53 项本地 EVM 检查和一个复杂交易适合验证基础功能，不能据此计算全体合约的业务分析准确率。

本方案暂按主流 EVM 链、TRON、Solana 优先安排。Sui/Aptos Move、CosmWasm、Starknet Cairo 和 TON 列入后续执行环境；若用户选择全部同时开展，需要调整阶段和资源安排。

## 2. 覆盖目标的具体含义

| 层次 | 应提供的结果 | 检查依据 |
|---|---|---|
| 接入 | 合约/程序身份、代码版本、固定区块或 slot、来源 | 链上代码、部署或升级记录、取得的数据 |
| 结构 | 入口、分支、内部调用、状态读写、外部依赖 | 已核对源码或字节码 IR |
| 方法 | 参数、条件、顺序、精确表达式、影响及中文解释 | 表达式与对应代码位置 |
| 业务 | 入金、份额、收益、费用、提现、管理员能力 | 通用资金数据流、协议模型、取得的配置状态 |
| 执行 | 选定交易中的真实路径、资金、账户与状态变化 | receipt、trace、账户/状态前后值和可重放环境 |
| 历史 | 指定范围的调用、资金、实现版本和升级影响 | 交易覆盖、去重身份、版本区间及缺失范围 |

每项分别保存已核实、代码推导、候选解释和未解析结果。引擎成功退出、多个引擎相同输出和大模型置信分数都不能替代具体证据。

原始变量名、注释和项目宣传无法从任意字节码完整恢复。动态外部依赖、缺失历史状态和无法穷尽的路径保留明确缺口。目标是尽可能广地分析代码和资金机制，并让每项结论可查证；不对任意链、任意版本的所有执行路径作无条件完整承诺。

## 3. 分析路由与开源组合

先识别执行环境、代码版本、源码及 ABI/IDL 来源。优先重编译核对源码；源码缺失或部分解析失败时采用字节码分析。按缺口选择额外引擎，避免每个任务重复执行所有工具。每个引擎保留独立结果、耗时、版本、原始工件、错误和覆盖。

| 工具 | 计划用途 | 接入条件 |
|---|---|---|
| Sourcify / 自有已验证源码档案 | 扩大源码取得和核对，加入 Vyper 来源 | 快照字节码、编译身份和匹配类型核对 |
| crytic-compile + Slither | Solidity/Vyper、调用图、SlithIR、状态与权限数据流 | 源码核验后分析；具体语言/编译版本单独验收 |
| Gigahorse | 函数化三地址 IR、控制流、内存和跨函数数据流 | 版本固定、Souffle 环境封装、标准输出限制、离线基准 |
| sevm + WhatsABI + Heimdall | 保留现有通道，补充入口、表达式及可阅读伪代码 | 对同一代码身份对照，冲突进入核验队列 |
| Halmos + Foundry 属性测试 | 提现可达性、调用者条件、金额边界、资产守恒等已定义性质 | 明确初始状态、假设、属性和边界；限定范围内验证 |
| java-tron | TRON 执行规则、合约状态及本地 TVM 执行基础 | 独立 TRON 节点/快照适配，取得实际需要的历史状态 |
| solana-verifiable-build + Anchor IDL | Solana 可验证源码、指令及账户模型 | 构建工具链/依赖固定，IDL 不能单独证明代码内容 |
| anza-xyz/sbpf | Solana ELF、反汇编、指令/调用结构、解释器基础 | sBPF 版本、loader、syscall 和链 feature set 核对 |
| LiteSVM / Mollusk | Solana 指令模拟、账户变化和本地验证 | 账户、sysvar、依赖程序及 feature set 完整；不是历史状态获取工具 |
| Ghidra eBPF / Soleker | Solana 无源码分析的实验候选 | Ghidra eBPF 不等于完整 Solana sBPF 支持；先测指令、ABI、syscall 与数据流 |

Halmos 需要定义假设和断言；不直接提供任意历史合约的全路径证明。Mollusk/LiteSVM 可以执行给定程序与账户环境；历史账户前状态不足时，只输出模拟结果或缺口，不能标成已重放历史交易。

本次查验的上游 HEAD 如下。它们是候选实验锁定点，不代表已经生产验证；正式镜像还要固定完整依赖、编译器与镜像 digest。

| 项目 | 2026-10-14 查验的提交 |
|---|---|
| nevillegrech/gigahorse-toolchain | 1b06cef82f0a29790e87f576eba6d6d793170d70 |
| crytic/slither | eef5df948b8992eff85786b6b0bb4a193933e2c9 |
| crytic/crytic-compile | 3d27afa70458b95c8660ba53b621ec6e72af08d4 |
| sourcifyeth/sourcify | d9fc09b1fa81be715b9b76634e77b612f9506689 |
| a16z/halmos | 079bb4241d1b460baf986257d56ea86977d73451 |
| tronprotocol/java-tron | b33eed89a6a424c498d4fb1b03ca2c86eddf4840 |
| anza-xyz/sbpf | c2b66f1080eeec68a17747740d2d0ebee208a0a8 |
| LiteSVM/litesvm | 1dda337d22521a4310ec959ebabce0aa4910c497 |
| anza-xyz/mollusk | bd75dd7937a8e51fa3b655a39b76a9c6170f9d2c |
| solana-foundation/solana-verifiable-build | 26ba8211eddbfab49d30826d15a2408332b89543 |

候选进入正式引擎前更新依赖许可证和分发记录。现有引擎保留原版本供对照和回退。

## 4. 需要自研的通用分析

建立执行环境独立的 MethodModel、StateAccess、CallContext、AmountExpression、AssetEffect 和 Claim。EVM 使用 selector/代码/状态上下文，Solana 使用指令编码/程序/账户上下文；不能假设所有 Solana 指令使用固定长度的 Anchor discriminator。

将 SlithIR、Gigahorse 关系和现有表达式映射到同一有类型模型。保留位宽、符号、算术检查、精度、取整顺序、条件及代码位置。源码和字节码发生冲突时保存双方依据，选择结果需经过代码或实际执行核对。

需要补齐的通用能力：

1. 从参数、状态和环境值跟踪到付款金额、收款地址、资产以及账本变化，形成跨方法、跨合约的资金数据流。
2. 分解入金、发行份额、收益累计、分佣、复投、赎回、扣费及提现路径；列出各路径的条件与可修改参数。
3. 识别循环付款、奖励索引、推荐关系、名单、额度、冻结、交易开关及异常/回滚路径。
4. 把关键方法之间的状态依赖整理成运行流程；分析初始化、授权、存入、领取、赎回和配置修改的先后关系。
5. 将 owner、角色、签名、多签阈值、timelock、升级权、mint/burn、资金提取权落实到代码条件和取得的状态。未能完整枚举的角色成员单独记录。
6. 通过确定的地址表达式、代码、调用和实际 trace 扩展依赖图；动态或未取得的依赖保留待分析节点。
7. 对关键性质生成局部可执行验证：普通参与者能否提现、管理员能否改变收款或费率、金额边界和资产变化是否符合模型。记录输入与状态假设、覆盖和反例。
8. 中文方法解释由结构化结果生成；模型可以参与表达与候选业务归纳，确定性校验负责完整地址、金额、条件和证据引用。找不到依据的句子不进入已核实事实。

自定义收益、分红或推荐结构优先使用通用数据流解析，不能要求先匹配某个知名项目名称才输出。协议 adapter 用于补充特有状态与经济含义。

## 5. 业务模型与资产核对

| 合约类型 | 需要分析的内容 |
|---|---|
| ERC20 / TRC20、税费币、rebase | 真实扣款/到账、税费去向、余额/供应变化、黑白名单、增发和费率权限 |
| ERC721 / ERC1155 | mint/burn/transfer、资产编号、数量、授权、付款和版税、可验证的所有权状态 |
| AMM、集中流动性、聚合路由 | 实际交换路径、输入/输出、池费用、滑点条件、LP/仓位变化、闪电调用与偿还 |
| vault、staking、借贷、清算 | 资产/份额关系、奖励累计、利息、质押/赎回、抵押/债务和清算条件 |
| 分红、推荐奖励、复投、锁仓 | 分配层级、各级金额和取整、领取条件、收益来源、期限和可变配置 |
| 多签、智能账户、账户抽象 | 调用和授权对象、签名条件、阈值、批处理、费用承担及实际执行上下文 |
| 代理、Diamond、自定义 dispatcher | 实现与状态地址、路由、初始化、升级历史及同块变更 |
| 跨链桥、治理、timelock | 锁定/销毁/铸造、消息身份、费率、执行权限和延迟条件 |

先取得代码、布局或经过验证的资产规则，再核对账户/状态净变动。多资产交易按每种资产分别报告。新增 packed/nested mapping、计算余额、rebase、份额和 NFT 所有权适配，不把 Transfer 事件直接当作已核对账本。

协议识别要核对代码、实现版本、入口及所需状态。克隆、升级和仿冒接口不能仅通过函数名或项目标签套用经济模型。升级和多版本场景按交易实际执行版本归属。

跨链模块接入已有追踪能力，但只有取得能够对应的两端证据才形成已核实路径；单链反编译负责说明这一端代码做了什么。

## 6. 执行环境与数据适配

EVM：逐链登记 fork、opcode、预编译、网络费组成、历史状态与 tracer 能力。先支持静态分析，再凭样本决定某条链是否开放精确重放；兼容 JSON-RPC 不能替代执行规则一致性。复杂代理、multicall、CREATE/CREATE2、账户委托和新指令需要专门回归样本。

TRON：使用 Base58/hex 地址规范化且保留原始值，核对 TVM 版本、TRON Solidity、资源和提案激活状态。独立保存交易/receipt、内部调用、Energy、Bandwidth、TRX/TRC10/TRC20 和实际账户变化。取得完整执行前环境后运行 TVM；未取得历史前状态时不以当前状态代替。

Solana：以 program、ELF hash、loader、升级记录、slot/block 身份分析，保存指令编码、账户元信息、signer/writable、PDA、CPI、syscall、sysvar、Token/Token-2022 和 lamports/token/account-data 变化。历史前状态需要归档来源或从已知快照重建。交易记录提供的账户余额和 inner instructions 可以先用于执行解释，不能替代缺失的全部账户内容。

后续执行环境：Sui 和 Aptos 分别适配 Move 版本、对象/资源和交易模型；CosmWasm 适配 Wasm 与消息/回复；Starknet 适配 Cairo 类和状态；TON 适配 cell、消息链和自己的 TVM。TON TVM 与 TRON TVM 是不同执行环境，分别登记。后续阶段不会仅靠增加枚举宣称已支持。

## 7. 历史范围与成本

按区块或 slot 将历史任务拆成可恢复的区间，保存实际覆盖、分页完成、版本区间和去重结果。超出已有索引的范围通过受预算约束的下载任务补齐；大规模外围地址优先聚合，不自动全量下载所有交互地址。

缓存按代码和引擎版本保存静态结果，快照/交易/状态结果按链、区块或 slot、代码和状态身份隔离。重复克隆只复用静态代码结果，地址角色、权限、配置和历史资金单独核对。

沿用持久 RPC 请求、credits、状态读取、时间预算和取消/恢复规则。首轮新增真实链验收总上限为 2000 次 RPC HTTP 请求、1000000 credits、配置单价估算 10 美元，任一达到即停止新增请求。普通离线代码样本先使用已冻结数据；批量源代码取得和归档下载单独记录字节、时间及费用。外部付费反编译或模型服务不在默认范围。

## 8. 文件与接口安排

| 模块 | 现有文件或计划新增位置 | 工作 |
|---|---|---|
| 身份/模型/能力 | backend/internal/contractanalysis/model.go、rules.go；新增 capabilities.go、vm.go | 执行环境注册、分类型输入/身份、结果版本和兼容校验 |
| 编排与源码 | analysis-engines/analyze.mjs、source.mjs、bytecode.mjs、external.mjs；新增 adapters/slither、adapters/gigahorse、source-vyper | 真实引擎接入、重编译核对、隔离输出 |
| 统一 IR 与业务 | analysis-engines/model.mjs、rules/funds.mjs、rules/ledger.mjs；新增 ir、protocols、properties | 类型/数据流、业务流程、账本和性质验证 |
| 状态/交易 | backend/internal/api/contract_analysis_inputs.go、contract_analysis_reconcile.go、contract_analysis_proxy.go；新增 contract_analysis_tvm.go、contract_analysis_sbf.go | 分链取数、代理/版本、交易前环境和资产核对 |
| 历史/预算/工件 | backend/internal/api/contract_analysis_history.go、contract_analysis_budget.go、contract_analysis_usage.go、contract_analysis_artifacts.go | 分段历史、持久消耗、新证据分区 |
| 多环境 runner | backend/internal/contractanalysis/runner.go；analysis-engines 新分环境镜像及 replay adapter | 受控程序执行、版本化镜像、资源上限 |
| API / MCP / SDK | backend/internal/api/contract_analysis.go、mcp/src/contract-analysis.ts、frontend/packages/api-client/contract-analysis.ts | 能力发现、扩展请求及结果字段、旧任务兼容 |
| 工作台 | frontend/apps/user-console/src/pages/contract-analysis/ | 方法与指令、账户状态、业务关系、历史区间和证据详情 |
| 验收 | scripts/contract-analysis/；新增 acceptance/contract-analysis-coverage/ | 基准清单、金标准、隐藏源码对照、真实链和页面证据 |

新增字段采用明确类型。非 EVM 身份先使用独立 schema/version；不放宽 v1 校验以接受不合法 EVM 地址，也不破坏已有 8 个工具的调用参数。能力查询决定客户端可提交的执行环境与分析深度。

确需新增数据表或索引时另加增量 migration。既有生产迁移 0083 和已经发布的任务/工件不回写。阶段一先用现有引擎记录和结果分区实现；执行环境扩展时提交具体 schema 再验收。

页面使用结构化业务字段；额外解释、错误和确认按仓库要求使用弹窗。保留 URL 方法/证据定位、权限、分页、精确值和现有路由/API 契约。

## 9. 基准和发布验收

建立逐代码身份去重的固定 corpus，保留来源、代码 SHA、编译器、优化设置、代理身份和明确预期。至少 1000 个 EVM 代码样本；TRON/Solana 各安排至少 100 个不同代码或本地构建样本。这些数量是拟定验收规模，不是已经完成的结果，也不代表生态占比。

源码通道和隐藏源码通道分别测试。公开样本与自建样本分开，保留独立评测集，不用重复克隆扩充样本数量。资金及权限预期来自源码、部署状态与执行测试的可查证标注；抽样人工复核争议结论。

分别测量：入口召回和误报、IR 未解析节点/路径、参数与条件、金额/收款对象/资产、状态布局、权限、业务解释、实际交易资金和重放一致性。分语言、编译版本、执行环境、协议、源码可用性和复杂度报告分子/分母。不给所有指标合并一个看似准确的总百分比。

必要场景包含：

1. 不同 Solidity/Vyper 版本、优化设置、手写汇编、动态跳转及非标准入口。
2. packed/nested mapping、动态数组、bytes、proxy 状态、自定义余额及 rebase。
3. 税费转账、份额换算、整数边界/取整、推荐分佣和奖励索引。
4. 未授权调用、角色/多签/timelock、升级前后及同块前序交易改变参数。
5. 多层调用、multicall、失败祖先、捕获回滚、原生币支付与费用、mint/burn、跨资产净额。
6. TRON 地址、执行版本、Energy/Bandwidth、TRC10/TRC20、历史环境缺失。
7. Solana 非 Anchor 指令、CPI、账户权限/PDA、Token-2022、升级及缺失账户前状态。
8. 引擎冲突、候选签名错误、假源码、缺失依赖、预算耗尽和不支持的指令。

发布门槛：必要金标准中的资金、权限、上下文和精确表达式全部正确；错误证据关联为零；所有故障和缺失样本输出实际状态。对未达到该门槛的引擎/协议/执行环境，保留实验状态，不宣称正式支持。

广覆盖目标另用独立评测集实测并列明覆盖；可先把标准 EVM 外部入口发现率 98% 作为拟定目标，特殊 dispatcher 单独评测。这个目标不等于语义正确率、全路径覆盖率或全体链上合约恢复率。

记录各阶段 wall time、CPU、cgroup 内存峰值、超时、IR/工件大小、RPC/credits、重试和估算费用。依赖或重型引擎可能超过现有 2 CPU/2 GiB；先实测再配置独立重型队列，默认线上资源不自动提高。不得为追求样本数量降低代码身份和证据验证。

真实接口、MCP、数据库和导出逐项一致；ZIP 全部文件校验；桌面和移动业务流使用真实隔离后端测试；生产验证冻结资产、作用域、费用、审计、旧任务和既有服务。沿用仓库 typecheck、契约冻结及真实浏览器门禁。

## 10. 分阶段执行与回退

1. 固定现有发布基线和接口，建立 corpus/金标准及能力表，运行 v1 对照。完成标准：能够解释每个指标和已有缺口。
2. 接入 Slither、Vyper 和 Gigahorse，统一 IR、跨方法/跨合约数据流及精确资金解释；按具体失败样本自研补齐。完成标准：新增引擎实际运行并达到必要金标准，公布隐藏源码评测。
3. 逐类增加业务/余额/权限模型，先通用资金流程，再常见协议；同时扩大历史版本和分段覆盖。完成标准：方法机制与实际资金都能查到代码/状态/交易依据。
4. TRON 和 Solana 分别接入来源、身份、静态结构、交易解释与本地执行；历史精确重放仅在前状态与规则齐全时开放。完成标准：每一项支持能力有独立样本和证据。
5. 按选择扩展 Sui/Aptos、CosmWasm、Cairo 和 TON。各自完成引擎、数据、业务模型和验收后加入正式支持表。
6. 每个正式阶段独立候选发布，保留原引擎/镜像和 v1 查询；服务、预算和审计复核后开放新任务。

引擎、协议和执行环境分别受 capability 开关控制。故障时停止新的相关任务并保留已保存查询；可回退处理器与服务路由，旧任务结果及增量数据不删除。保持原工作区修改边界，使用新隔离候选；不直接在当前运行目录覆盖源码、资产或二进制。

本方案已获用户确认。各阶段实际实现与验收证据记录在 acceptance/contract-analysis-coverage/，未通过验证的能力保持具体状态。

## 11. 查验资料

- [Gigahorse](https://github.com/nevillegrech/gigahorse-toolchain)：函数化 IR、可扩展数据流与内存模型，支持有超时和回退的分析。
- [Slither](https://github.com/crytic/slither)、[crytic-compile](https://github.com/crytic/crytic-compile)、[Sourcify](https://github.com/sourcifyeth/sourcify)：源码获取/核对与 Solidity/Vyper 结构分析。
- [Halmos](https://github.com/a16z/halmos)、[符号测试说明](https://github.com/a16z/halmos/blob/main/docs/getting-started.md)：假设、断言与输入范围的测试。
- [java-tron](https://github.com/tronprotocol/java-tron)、[TRON 开发说明](https://developers.tron.network/docs/smart-contract-development)：TVM 与资源/代币扩展。
- [Solana verified builds](https://github.com/solana-foundation/solana-verifiable-build)、[sBPF](https://github.com/anza-xyz/sbpf)：代码核对、执行和反汇编。
- [LiteSVM](https://github.com/LiteSVM/litesvm)、[Mollusk](https://github.com/anza-xyz/mollusk)：给定程序和账户环境的测试。
- [Ghidra eBPF](https://github.com/Nalen98/eBPF-for-Ghidra)、[eBPF 测试](https://github.com/Ledger-Donjon/ghidra-ebpf-tests)、[Soleker](https://github.com/sepine/Soleker)：实验参考，未列为正式 Solana 反编译引擎。
- [Sui](https://github.com/MystenLabs/sui)、[CosmWasm VM](https://github.com/CosmWasm/wasmvm)、[TON](https://github.com/ton-blockchain/ton)：后续独立执行环境参考。
