# 合约运行机制与方法作用分析开发计划

本功能面向资金分析师。输入链、合约地址、分析区块或时间范围，以及可选的交易哈希，输出合约的运行机制、逐方法作用、权限条件、状态变化、资金计算、外部依赖、业务流程和实际交易执行情况。每项解释关联可读取的源码、字节码位置、状态或交易证据。

交付目标包括后端分析服务、隔离分析引擎、持久化任务、MCP 工具、网页分析工作台、结果和证据导出。开源组件负责已有的程序分析能力；项目自研负责方法语义、业务流程、资金公式和证据关联。本计划覆盖完整分析闭环，按依赖顺序交付各层。

## 1. 当前基础与实施边界

2026 年 10 月 14 日只读检查确认：当前直连 MCP 发布 111 个工具；已有 ABI 获取、EIP-1967 / beacon / EIP-1167 代理解析、交易解码、合约权限、实际余额、FIFO / 比例法、多来源联合追踪、持久化分析任务、结果分页及证据包。尚无合约反编译工具，也未发现现有源码中接入 Heimdall、Gigahorse 或 Slither 的实现。

虚拟机的 RPC 方法登记表包含 trace / debug trace 方法；是否能取得具体链、提供商、区块和 tracer 的结果，仍需在引擎验证阶段逐项检查。当前公开 read_rpc 工具只有只读方法白名单，不能据此认为任意 debug 方法已经对 MCP 开放。

已有任务位于 `backend/internal/api/investigation_next_jobs.go`，使用 PostgreSQL lease、fence、输入冻结、MinIO 对象、发布校验和结果分区。现有状态为 queued / running / completed / failed / cancelled；现有计算 worker 时间预算为 300 秒，单个保存对象上限为 32 MiB。新功能不能简单通过提高这些全局限制取得运行空间。

本地仓库和虚拟机源码均有大量未提交内容，本地与虚拟机的功能文件也不完全一致。实施前冻结部署基线，建立隔离候选目录，保存源码清单、提交、未提交差异、运行二进制和前端资源哈希。保留其他工作的文件、任务和数据。

首期实现 EVM 分析闭环。链是否进入正式支持表，取决于其执行规则、历史状态、trace 能力与样本验收。TRON 的 TVM 和 Solana 的 sBPF 使用独立能力适配器；不得把链枚举增加到接口就当成引擎支持。付费外部反编译服务、真实链上写交易、自动开展全部外围地址历史下载不在本次执行范围。

## 2. 开源项目评估

下表区分项目提供的能力与本项目拟采用的职责。项目资料和许可证已经检查，分析准确率与运行开销尚未通过本项目统一样本实测。源码 SHA 是研究参考点，正式引擎版本在样本测试通过后固定。

| 项目 | 项目能力 | 拟采用方式 | 许可证与参考源码 |
|---|---|---|---|
| [Heimdall-rs](https://github.com/Jon-Becker/heimdall-rs) | EVM 反汇编、重建 Solidity / Yul、候选 ABI、控制流图 | 人工阅读代码的主要候选引擎；以离线字节码输入运行 | MIT；`9de15de029ac1128c7c7566672d472f104a9747a` |
| [Gigahorse](https://github.com/nevillegrech/gigahorse-toolchain) | 按函数组织的三地址 IR、关系输出、数据流和内存建模库 | 方法结构、跨函数依赖和资金表达式分析的主要候选基础 | [BSD-3-Clause](https://github.com/nevillegrech/gigahorse-toolchain/blob/master/LICENSE)；`1b06cef82f0a29790e87f576eba6d6d793170d70` |
| [WhatsABI](https://github.com/shazow/whatsabi) | 从字节码提取 selectors、候选接口及代理信息；有 ABI / 签名加载器 | 快速入口清单与已有代理解析交叉检查 | MIT；`9a4eb4174993be4ed6f06d79d0bdcc4c3b43b6b3` |
| [sevm](https://github.com/acuarica/evm) | 可编程符号 EVM、指令和表达式结构、函数 / 事件提取、Solidity / Yul 风格伪代码 | 表达式和指令级适配候选；作为交叉检查或限定场景 fallback | MIT；`0dc035022ee32d765699a99a2f4ee601941b7fec` |
| [Slither](https://github.com/crytic/slither) | Solidity / Vyper 源码分析、调用图、变量依赖、授权条件、SlithIR | 已验证源码的方法、存储和权限分析 | AGPL-3.0；`eef5df948b8992eff85786b6b0bb4a193933e2c9` |
| [crytic-compile](https://github.com/crytic/crytic-compile) | 不同合约构建系统的编译抽象 | 固定编译设置和构建工件适配 | AGPL-3.0；`3d27afa70458b95c8660ba53b621ec6e72af08d4` |
| [Sourcify](https://github.com/argotorg/sourcify) | 验证源码、编译信息及匹配记录 | 复用公共查询与本项目已有缓存；增加源码、layout、source map 工件 | MIT；`665a64de66b278177b954ce54b9eebb5d0124cb4` |
| [evm-storage](https://github.com/banteg/evm-storage) | 按编译 layout 与 trace 解释存储变化，保留 code / storage 地址 | 独立评估和复用类型化 state diff；限制网络和响应预算 | MIT；`f0c1bc3955ec2c91a800fec91f4bbb720231f1f8` |
| [Foundry](https://github.com/foundry-rs/foundry) | 本地 fork、交易重放、调试、断言 | 实际交易重放与受控情景验证 | MIT / Apache-2.0；`19f92446d03b89af26703d592a681180741d704d` |
| [REVM](https://github.com/bluealloy/revm) | 可嵌入的 Rust EVM 执行实现 | 需要自定义 inspector 时评估；与 Foundry 同源依赖避免重复执行器 | MIT；`7e43a49fd9fe57a0f6b8bf9779d62664e92e2418` |
| [Ethpector](https://github.com/uibk-ethpector/ethpector) | 入口、调用、存储、日志和部分 sender 约束提取 | 参考权限恢复与离线分析方法；研究候选 | MIT；`467428db2254c5baf027374316f49c273bc94a85` |
| [Mythril](https://github.com/ConsenSysDiligence/mythril) | EVM 符号执行与安全检查 | 指定函数和条件的有界验证候选 | MIT；`125914a6eae81a79a089696e1f47ac8cc4b3a180` |
| [Halmos](https://github.com/a16z/halmos) | EVM 符号测试 | 对已定义金额、权限和状态性质编写断言；不当作历史交易事实查询器 | AGPL-3.0；`079bb4241d1b460baf986257d56ea86977d73451` |
| [Panoramix](https://github.com/palkeo/panoramix) | 字节码反编译，超时保留部分结果 | 复核备选；默认分支最近提交为 2023 年 6 月 | MIT；`23edd11058abafcba9340afc768d2aa9274c0b62` |
| [NeverD](https://github.com/NeverSight/NeverD) | 项目声明具备 EVM、Solana SBF 等分析与重建输出，仍标记 WIP | 实验候选；独立测量后决定是否进入 chain adapter | AGPL-3.0-only；`9c39d0a94ac66e0eb4751ecc48526a92b12f50cd` |

建议验证组合为：Sourcify / crytic-compile / Slither 的源码通道，WhatsABI 的入口通道，Heimdall 的代码阅读通道，Gigahorse 的结构化通道，trace / evm-storage / Foundry 的执行通道。sevm 保留编程扩展位置。不是每个任务都运行全部引擎：按数据来源、目标、引擎支持与前序缺口选择，并保存每个引擎自己的结果和失败原因。

维护日期与 README 不是准确率证明。不得使用其中一个引擎的函数名字去覆盖另一个引擎的原始证据。相互矛盾的输出进入 conflict 记录，保留原始值和各自位置，等待执行验证或人工核对。

## 3. 分析对象和时间

一次分析建立不可变 snapshot，至少包含 chain_id、地址、区块号与区块哈希、字节码哈希、取数时间、源码和编译信息来源、代理和实现关系、执行规则版本、引擎版本、规则版本及分析预算。

`latest` 在任务提交时解析成固定区块；后续任务恢复和结果分页复用该 snapshot。历史交易按其执行时的代码和状态分析；只读的区块末状态须注明其粒度。同一块内升级或改参数时，优先使用 transaction trace 和正确的块内重放，不用整块结束状态代替每笔交易之前的状态。

分别记录入口地址、执行代码地址、storage 地址、资产地址、CALL 接收地址、实际收款地址和原始调用者。DELEGATECALL 的实现代码与代理状态分开表示。EIP-7702、CREATE / CREATE2、beacon、minimal proxy、diamond 和自定义路由都经独立 resolver；未解析依赖保留在图中。

## 4. 合约整体运行方式

整体分析输出从入口到结算的完整结构：部署与初始化、外部入口、内部函数、状态变量、角色控制、关联 token / router / pool / vault / staking / rewards / bridge 合约，以及资金实际结算地址。

业务流程不能由函数名称拼接。用调用边、状态变量依赖、资金边和真实交易建立流程。例如注册 → 入金 → 记录份额 → 计算收益 → 领取 / 提现 → 手续费分配，必须逐步指向方法、状态和证据。缺少某一环节时，保留该环节的具体待解析条件。

输出运行流程图、方法调用图、状态依赖图、权限关系图、资金流图和版本时间线。所有图边均有 edge_type 和 evidence_ids，避免把调用关系画成转账。

## 5. 每个方法的交付字段

| 分组 | 必须包含的内容 |
|---|---|
| 身份 | method_id、selector、签名候选、已验证签名、入口位置、ABI 来源、所属代码版本 |
| 用途 | 中文作用、业务分类、具体依据；允许一个方法拥有多项行为 |
| 入口 | external / public / internal 或 bytecode entry、fallback / receive / constructor；内联函数与真实入口区分 |
| 参数 | 类型、单位、是否地址 / token / 金额 / 份额 / 时间 / ID、范围限制、零值和边界值、证据 |
| 输出 | 返回类型、返回表达式、状态返回或份额 / 收益含义、解码是否确定 |
| 条件 | caller、角色、签名、白名单、余额、额度、时间、暂停、初始化、交易状态、外部条件 |
| 步骤 | 按执行顺序列出的检查、计算、状态更新、外部调用、事件和返回；保留分支和循环 |
| 状态读写 | slot、offset、类型、mapping key 表达式、变量名来源、读写方式、同方法和跨方法关联 |
| 资金作用 | asset、from / to 表达式、amount_raw / 表达式、手续费、销毁、增发、兑换、份额增减 |
| 外部依赖 | 目标表达式、实际目标、CALL / STATICCALL / DELEGATECALL、selector、返回值依赖和失败传播 |
| 失败 | revert / panic / return false / 被捕获失败、触发条件、子调用回滚影响 |
| 历史调用 | 请求范围内次数、成功 / 失败、调用者、时间、按资产汇总金额和逐笔入口 |
| 实际执行 | 选定交易的参数、执行分支、state diff、资产变化、手续费、日志和完整调用轨迹 |
| 证据 | 源码位置、PC / 基本块、IR 节点、状态 snapshot、完整 TxHash、log_index、trace_path |
| 完整性 | 已发现和已解析的部分、未解析 jump / call / layout、引擎冲突、未执行的分支 |

method_id 由代码身份和入口身份生成，不能仅依赖方法名；selector 冲突、ABI overload、proxy admin 与 implementation 同 selector 必须能同时存在。标准 ERC 方法保留原始名称，不能因中文解释替换原始标识。

## 6. 自研方法语义层

建立版本化 FunctionModel 和 ExpressionModel。将 SlithIR、Gigahorse TAC / 关系和可用的字节码表达式映射到统一节点：参数、环境、常量、状态读写、算术、比较、分支、调用、事件、资产操作、返回和异常。

表达式保留位宽、signed / unsigned、wrap / checked、除法顺序、截断、单位及原始节点；金额和 uint256 不经过 float64。未知表达式用 unresolved 节点记录真实缺口，不以零值或伪造参数补齐。

开发以下领域规则，均以表达式与控制关系识别，不以函数名命中作为成立条件：

1. 入金、认购、质押、份额铸造和账内余额登记。
2. 本金退出、收益领取、份额赎回、紧急提现和第三方代领。
3. 手续费、税率、折扣、最小值、上限、取整和多地址分配。
4. 推荐绑定、层级关系、级差奖励、团队条件、权重分红和资格变化。
5. 锁仓、解锁、周期、到期、复投、领取间隔和时间依赖。
6. 买卖限制、黑白名单、router / pool 例外、最大交易和最大持仓。
7. token 增发、销毁、重基准、转账税、份额与底层资产转换。
8. 管理员改变费率、收款人、收益规则、提现条件、实施代码及资金提取。
9. 路由兑换、加减流动性、LP / NFT 仓位、金库和桥接交互的协议 adapter。

规则输出 behavior、preconditions、effects、dependency_methods 和 evidence_ids。金额公式与当前参数值分开保存，支持在所选历史 snapshot 求值。转账目标从 calldata、slot、常量、调用返回值或外部模块得出，须保留数据来源。

## 7. 跨方法与跨合约流程

建立 reads / writes / guards / calls / transfers / delegates / creates 图。计算一个配置方法能影响哪些资金方法，例如 setFee 写入的状态被 withdraw 读取；admin 提现是否与用户提现使用不同条件；初始化之后由什么方法改变接收地址。

相关外部合约按可见调用和配置状态扩展。维护待分析 frontier、已完成依赖、代码哈希和循环去重，节点额度用完时保存 frontier，后续可继续分析。不要把默认展示节点数当成项目真实网络规模。

协议模块逐类实现并验收：ERC20 / ERC721 / ERC1155、常见代理、多签、AMM、vault / share、staking / reward、跨链。通用模型可显示未知协议的调用和资金变化；协议特有含义需匹配代码 / ABI / 状态后才能使用。

## 8. 实际执行与情景验证

优先取得原交易、receipt、完整 call trace 和 transaction state diff。Geth [callTracer / prestateTracer](https://geth.ethereum.org/docs/developers/evm-tracing/built-in-tracers) 分别提供调用与交易前后状态能力；trace 来源、完整性和错误须在每次结果中保存。

对重要入金、领取、提现、改费率、改接收地址和升级交易，核对具体调用路径、执行代码、storage 变化、实际转账、返回值与回滚情况。失败祖先下的子调用不计入已结算资金；DELEGATECALL 的 value 不当作独立付款；STATICCALL 无资产支出；事件中的金额与真实 token 状态变化分别记录。

本地重放使用历史代码、交易前状态、原始区块环境、链执行规则和此前块内交易。Foundry 的 [cast run](https://getfoundry.sh/cast/reference/run) 和 [transaction fork](https://getfoundry.sh/reference/cheatcodes/create-fork) 可作为实现基础。不能使用跳过块内前序交易的 quick 模式生成精确执行认定。

重放结果与原链 status、日志、Gas 和可取得的 state diff 核对。差异保留为 replay_mismatch，明确影响的结论。改变 caller、金额、时间或管理员参数的情景测试保留在 simulation 结果中，不写成历史已发生记录。

对有限输入域和明确性质，可用有界符号验证检查提款权限、金额守恒、费用范围和状态约束。记录模型、路径、solver、时间和未覆盖分支；求解超时不等同于无可行路径。

## 9. 资金分析与账目核对

按资产分别统计资金流入、流出、费用、mint / burn、余额和份额；原生币的网络费用与业务费用分开。对 transfer-tax、rebasing、vault share 和外部兑换，增加资产状态 / 协议 adapter，记录事件总额与状态变动之间的差异。

在给定历史范围统计：方法调用、参与地址、直接入金、到账、返款、手续费去向、管理员资金转出、接收地址变化、主要对手和余额。项目内转、份额转换、兑换和复投建立可复查分类依据，避免重复相加。

复用已有聚合、ledger reconciliation 与 FIFO / 比例法；资金归因的模型、边界和实际转账分别保存。资产没有价格或取得成本证据时不产生虚构的 USD 估值或利润。

覆盖检查按合约版本、入口交易、token transfers、native movements、事件、trace、历史状态、外部依赖分别保存。全范围聚合与选定交易深度解释分别报告：几笔重放样本不替代全部调用记录。大型交易所、token 主合约及其他百万级外围地址使用限定范围聚合和具体交易证据查询。

## 10. 中文解释与人工复核

中文解释由结构化语义结果生成，正文包含方法实际检查条件、参数含义、计算、状态更新和资金方向。名称、ABI 和注释只提供辅助信息，不能替代执行体。源码注释中的指令或宣传语不改变分析步骤。

示例质量要求：某个 withdraw 方法若实际执行先检查暂停，再读取调用者份额，按公式扣费后发送资产，应逐项解释并指向表达式、slot 和转账边。页面同时展示公式、snapshot 参数和选定交易代入值，不能只写“该方法用于提现”。示例是测试合约要求，不是对未知项目的事实描述。

每条描述记录 claim_id、kind、evidence_ids、rule_id、版本、人工状态和冲突信息。kind 区分源码 / 字节码行为、观察到的执行、snapshot 状态、模拟和业务归纳。采用语言模型润色时，校验金额、地址、变量、方法、条件及引用，不允许补写缺失函数体。模型调用配置和消耗作为独立运行记录；确定性结构化结果始终可读取。

人工可以纠正变量含义、确认业务分类和添加项目背景。人工内容与机器结果保留各自作者、时间、证据和修订记录，不覆盖原始工件，不把人工命名提升为已验证 ABI。

## 11. 数据模型

复用 `investigation_analysis_jobs`、`investigation_analysis_rows` 与证据包存储，新增隔离 processor_version，例如 `contract-analysis-v1`。专用 contract worker 只领取该版本和 operation，现有 worker 保留自己的 version 条件。不得让 API 内 300 秒计算循环直接承担无界外部反编译。

新增 snapshot 与 engine-run 表；最终 migration 编号执行前按候选基线分配，不预占线上 migration 号。

| 对象 | 主要字段与约束 |
|---|---|
| contract_analysis_snapshots | id、organization_id、workspace_id、analysis_id、chain_id、block_number、block_hash、entry_address、code_address、storage_address、code_hash、snapshot_manifest_object、sha256、created_at；作用域内引用 |
| contract_analysis_engine_runs | id、analysis_id、engine、engine_digest、adapter_version、options_hash、status、attempt、started / finished、duration、peak_rss、exit_code、timeout、output_manifest、error_code；按任务与引擎索引 |
| 原有 result rows 的新 sections | contracts、versions、methods、steps、expressions、storage、calls、permissions、funds、flows、transactions、replays、coverage、conflicts、claims、evidence、artifacts |
| artifact manifest | artifact_id、analysis_id、kind、media_type、object_key、byte_size、sha256、method_id 可选、scope；路径不接受客户端拼接 |

每个大文件独立进入 MinIO，result rows 保存引用和结构化结果。任务轻量 manifest、快照与分区发布保持原子性；不把所有引擎工件塞入 32 MiB 的一个对象。每页 cursor 绑定作用域、snapshot、section 和 filters。

static cache key 包含 code_hash、engine_digest、adapter_version、执行规则版本和 options_hash；context cache 另包含 chain_id、block_hash、code / storage 地址、选定交易集与数据范围。内部备注和案件数据不进入公共缓存。

## 12. API 与 MCP

后端建立独立 typed ContractAnalysisRequest、MethodAnalysis、ExecutionFlow 和 AnalysisArtifact，避免把全部参数散入已有通用请求结构。现有 start_analysis 增加 contract_analysis 和 contract_transaction_analysis 的独立 schema / adapter，旧 operation 的验证和结果不改变。

| 拟新增 MCP 工具 | 输入 | 输出 |
|---|---|---|
| analyze_contract | chain、address、block 或 timestamp、可选 range / tx_hashes、分析预算、idempotency_key | analysis_id、snapshot、phase、已知能力、预算 |
| get_contract_analysis | analysis_id、section、cursor、page_size | 合约摘要或 section 完整分页、coverage、available_sections |
| list_contract_methods | analysis_id、代码身份 / 行为过滤、cursor | 方法目录、解析状态、索引和来源 |
| get_contract_method_analysis | analysis_id、method_id、子 section / cursor | 方法作用、条件、步骤、表达式、状态、资金和证据 |
| get_contract_execution_flow | analysis_id、flow_id 或 method_id、cursor | 带类型和证据的执行 / 业务流程图 |
| explain_contract_transaction | chain、完整 tx_hash、关联 analysis_id 可选 | 逐步骤执行、资金、状态、费用、失败和代码版本 |
| compare_contract_versions | chain、address、两个 snapshot 或区块、预算 | 方法、权限、表达式、收款方、实现与存储差异 |
| get_contract_analysis_artifact | analysis_id、artifact_id、offset、chunk_size | 鉴权文件块、whole / chunk SHA256、next_offset |

API 可采用 `/api/v1/analysis/contracts/*` 对接 MCP，并通过受保护的 `/api/v2/contract-analyses` 与方法 / artifact 子资源供网页使用。共享服务实现和鉴权，OpenAPI 为 SDK 类型来源。新工具在服务端 tools/list、直连客户端及网页插件连接分别核对；不能以服务端新增工具代替客户端加载验证。

异步任务保留 queued / running / completed / failed / cancelled 生命周期，增加 analysis_completeness 和分阶段状态；计算结束且有 unresolved 输出可为 completed + partial，重要缺口不被 completed 文案遮蔽。取消杀死归属该任务的进程组，fence 阻止旧 worker 发布。恢复使用原始 snapshot 和 processor 版本，输入变化返回 conflict。

## 13. 网页工作台

从已有地址详情页进入“合约分析”；保留地址 / 交易原有路线、参数、权限与深链接。独立工作台以 analysis_id 为身份，持有 chain、snapshot、code / storage 地址、运行阶段和各分区计数。

工作台包含：运行机制、方法目录、方法详情、资金机制、权限与配置、执行交易、关联合约、版本比较、证据和导出。方法可按名称 / selector / 行为 / 是否资金变更 / 解析状态筛选。每个方法详情可查看条件、执行步骤、金额公式、变量读写、外部调用、交易实例和原始代码位置。

源码 / 伪代码与解释双向定位；点击步骤关联对应 source span / PC 和 evidence。大源码、长方法列表和图采用分页或虚拟化；图可按合约 / 方法 / 资产聚合，并能展开实际边。移动端切换详情视图，保留同一 method_id 和 evidence_id。

方法解释、条件、金额公式和证据属于业务数据，可正常呈现；错误、运行失败说明、操作成功、预算调整和需要确认的动作使用既有 Dialog。遵守 `AGENTS.md` 与 `docs/code-standards.md` 的焦点、Escape、遮罩和移动端滚动要求。

RBAC 使用现有 permission registry 的真实定义：读取关联 explorer / investigation / evidence 范围，提交和取消关联分析执行范围，导出关联 export 范围。实施阶段登记映射并验收服务端拒绝路径，不仅依赖前端按钮。

## 14. 引擎与资源预算

引擎镜像与 adapter 均固定版本 / digest；无在线拉取 latest。反编译 runner 默认离线，只输入已经取得的字节码 / 工件；签名解析由平台缓存控制，Heimdall 默认关闭在线 signature resolution 和额外模型后处理。

源码编译、Python / Rust 分析与 EVM 重放在独立容器执行。只读根目录、任务专用工作目录、非 root、禁用宿主目录和 Docker socket 挂载。链取数只走平台网关与 provider pool；runner 的本地 fork 只能获得所需只读上游能力，写操作限定本地模拟节点。

初始验证预算建议：一个重型任务并发、2 vCPU / 2 GiB runner、单引擎 300 秒、总分析 1,800 秒、关联代码默认 20 个可继续扩展、图页默认 200 个节点、重放默认不超过 20 笔可显式扩展。以上是测量起点；基准结果决定正式值，不承诺所有合约在该预算内完成。

保存 wall time、peak RSS、CPU time、分析代码数量、函数和未解析数量、读取字节数、RPC HTTP 请求、重试、credits / CU 和可计算成本。达到预算保留完整已完成部分及 frontier。HTTP 超大响应显式失败，不能静默截断后发布为完整 trace。

查询静态方法目录复用缓存；只有需要历史状态、外部合约或执行实例时才取数。相同请求合并，失败部分有界重试，普通用户下载任务与合约任务互不占用无限资源。百万级外围地址不触发自动全量下载。

## 15. 自研 fallback 的实施方式

基准中开源引擎失败时，按失败类型补齐 adapter 或局部分析，不立即重写整个 EVM。复用现有解释器，开发我们需要的程序分析与业务分析。

可自研的部分包括：受支持指令反汇编与 PC 保留、入口与动态分派分析、stack / memory 表达式、CFG、数据依赖、guard 恢复、storage key、call 参数提取、资金表达式、方法作用规则、业务流程归纳及证据校验。

对已知缺口先增加最小 fixture，例如某个 packed storage、mapping key、dynamic bytes、同块升级、跨方法初始化或 multicall；用修复前失败、修复后成功的证据验收。非标准 dispatcher、动态 jump、手写汇编、复杂外部状态和循环保持真实 unresolved 表达，不把猜测包装为成功恢复。

所有自研结论与原始 opcode / 源码对应；涉及计算与控制条件时，提供 concrete witness 或适用范围明确的验证性质。引擎间一致只是交叉检查，不是全部路径的证明。

## 16. 文件级工作

以下路径相对于隔离候选项目根目录。已有文件在虚拟机已核实；新增路径是实现职责，不能误认为已经存在。

| 模块 | 主要文件 | 工作内容 |
|---|---|---|
| 快照与结构模型 | 新增 `backend/internal/contractanalysis/model.go`、`snapshot.go`、`expression.go`、`evidence.go` | 稳定 schema、完整身份、精度、证据、coverage |
| 引擎编排 | 新增 `backend/internal/contractanalysis/engine.go`、`runner.go`、`cache.go` | adapter、隔离调用、预算、进程组、静态缓存 |
| 语义与流程 | 新增 `backend/internal/contractanalysis/method.go`、`permissions.go`、`storage.go`、`funds.go`、`workflow.go`、`rules/` | 逐方法语义、领域规则、跨方法 / 合约依赖 |
| 执行验证 | 新增 `backend/internal/contractanalysis/replay.go`、`reconcile.go`；复用 `chainread`、provider RPC 和 fundtrace | trace、state diff、重放比对、资产核对 |
| adapter worker | 新增 `analysis-engines/` 下各引擎 adapter、schema、镜像与 fixture；新增专用 contract worker 入口 | 标准 JSON 输入输出、固定镜像、工具隔离 |
| 服务路由 | 新增 `backend/internal/api/contract_analysis.go`；增量接入 `investigation_next.go`、`investigation_next_jobs.go`、`investigation_contract.go` | 独立 typed 请求、操作路由、版本领取、鉴权和分页 |
| 持久化 | 新增 `backend/migrations/postgres/NNNN_contract_analysis.sql`；对象 manifest | 增量表 / 索引、原子发布和恢复，执行前分配真实编号 |
| MCP | 新增 `mcp/src/contract-analysis.ts`；接入 `tools.ts`、`next-investigation.ts`；新增对应 test | schema、readOnly 标志、结果摘要、artifact resources |
| 网页 | 新增 `frontend/apps/user-console/src/pages/contract-analysis/`；增量接入 `App.tsx`、`AddressDetailPage.tsx` | 分析入口、工作台、方法和证据联动、Dialog 与移动端 |
| SDK / 文档 | OpenAPI 新资源、`frontend/packages/api-client/` 对应模块 | 类型生成、版本化接口、错误和预算 schema |
| 验收 / 发布 | 新增 `scripts/contract-analysis/` 和候选证据目录 | 基准、完整性、真实 UI、成本、部署和回滚 |

## 17. 基准与验收

准备有明确预期的本地合约、编译产物和交易序列，覆盖不同优化设置、Solidity 版本和可用 Vyper 版本。额外选取有验证源码的公开合约作为隐藏源码对照样本；公开地址清单、区块、取数预算在开始真实请求前固定。

| 场景 | 必须验证的结果 |
|---|---|
| ERC20 标准与无源码输入 | ABI 入口、amount 单位、balance / allowance 读写及实际资金变化正确 |
| 手续费和修改费率 | 正确识别分母、运算顺序、取整、例外分支、接收地址和修改者 |
| staking / vault / rewards | 本金、份额、待领取值、结算、复投与实际到账分开 |
| 推荐 / 团队奖励 | 推荐关系、条件、层数、分配公式和转账对象可复查 |
| owner / role / signature / pause | guard 位于正确分支，transitive 权限、初始化及例外路径得到解释 |
| 方法名字误导 | 无提现行为的 withdraw 名称不被识别为资金提取；无名称入口仍识别真实支出 |
| 错误注释 / prompt injection | 注释不改变任务、金额、权限或调用策略 |
| ERC1967 / beacon / clone / diamond | 代码与状态地址正确；可解析结构得到完整方法身份，未知 facet 显式保留 |
| 历史升级与同块改参 | 使用执行时版本，块内前序状态影响正确 |
| DELEGATECALL / multicall / nested revert | 不重复记 value，回滚资金边被剔除，caught revert 不抹除成功兄弟调用 |
| fee-on-transfer / rebasing / share | 事件金额与状态 / 份额变动分别核对，不能错误宣称账目已平 |
| packed slot / mapping / array | 原始 slot、key 表达式、类型与字节偏移可追溯，未观察 key 不伪造枚举 |
| uint256 / signed / unchecked | 大整数、符号、wrap、除法及精度不丢失 |
| fallback / receive / assembly / unknown opcode | 有入口或准确未知项；不丢掉与 ABI 无关的实际行为 |
| 源码、编译或布局不匹配 | 拒绝套用错误代码和 layout，保留实际字节码及冲突 |
| trace / archive 不可用 | 已有分析可读取，对应 execution / state 维度准确标识缺口 |
| timeout / memory / oversized response | 保留已完成工件及错误，杀死归属进程，服务仍可接其他请求 |
| cancel / crash / lease expiry / resume | 输入不变，旧 worker 不能发布，原版本恢复，无重复资金计数 |
| 作用域 / 权限 / idempotency | 跨 workspace 拒绝；同 key 不同参数冲突；敏感读取及导出有审计 |
| 网页和 MCP | UI / MCP 数值与工件一致；所有分页可遍历；证据跳转、Dialog、1440px / 390px 正常 |

每个 supported fixture 的关键方法、guard、状态、资金边和公式必须与预期一致；错误的高确定性结论数为零。对公开样本按方法与能力报告结果，不预设或宣传未经实测的 100% 恢复率。索引、执行、状态和外部依赖分别检查完整性。

每个正式引擎须提交基准矩阵：样本类型、code hash、版本、配置、识别入口、解析 / 未解析方法、时间、内存、失败原因、费用及可重复证据。样本中不支持的能力必须映射到支持表和页面状态。

## 18. 实施与发布顺序

1. 冻结当前源码 / 运行基线，建立隔离候选目录；记录必要的既有测试结果，区分旧失败。
2. 固定研究版本并完成引擎 adapter 基准，决定正式组合；开源缺口按第 15 节自研补齐。
3. 完成统一模型、snapshot、方法目录、源码 / 字节码证据及语义规则；逐个 fixture 验收。
4. 完成 storage、permissions、funds、跨方法和外部依赖流程；加入实际交易和历史版本验证。
5. 完成独立 worker、任务恢复、MinIO 分区工件、MCP、API、权限和成本记录。
6. 完成网页工作台、分页、证据联动、导出和移动端；保留原接口与业务流程。
7. 跑完整场景和资源门禁，形成源码 / 二进制 / 资源 / migration / engine digest / 证据 manifest。
8. 在必需场景通过后进行受控发布；保留旧 API / MCP 二进制与前端资源。关闭新任务入口可回退，已完成任务和证据保持可读取，migration 仅增量兼容，不删除数据。

部署验收检查真实工具发现与调用、页面交互、数据库任务和 lease、对象实际字节与 SHA256、进程 / 日志、provider usage 和审计。不能用 HTTP 200 或构建成功代替上述结果。

## 19. 最终交付

交付合约运行说明、逐方法分析、资金机制与公式、实际交易解释、权限和配置变化、关联合约及版本时间线、可编辑关系图、完整机器结果、原始工件和证据包。

如导出 DOCX，标题使用真实项目或分析对象名称，只有一个主标题；正文仿宋小四、文字黑色、普通无底纹表格、无页眉和附加小字。Excel / CSV 从表头开始，基础默认格式、无颜色和装饰合并，XLSX 默认筛选。完整地址、哈希和金额精度保持原样。

正式交付附上实际实现范围、测试证据、引擎支持表、资源与 API 消耗。未实现或未验证部分逐项列在具体能力中，不以笼统“全面分析完成”掩盖缺口。
