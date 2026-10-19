# 合约运行与方法分析

本功能以固定区块、区块哈希和代码哈希为分析身份，输出运行机制、方法作用、执行条件、有序步骤、金额公式、状态读写、权限、依赖关系、选定交易和证据文件。入口地址、实现代码地址和状态地址分别保存。方法名称、注释及候选 ABI 不替代函数体和交易证据。

2026-10-19 核对：生产规则为 funds-semantics-8（镜像 sha256:466f29f2…），本工作树候选为 funds-semantics-20（镜像 sha256:4440baad…）。13 至 20 依次补充 nonce 与签名检查、源码与字节码提交入口、方法保存点和 SEVM 隔离、字节内存与堆写入、存储到内存复制、元组与聚合左值、隐式创建身份、历史前序状态与代理升级连续性。功能验收以 acceptance/contract-analysis-heap-writes 的冻结镜像、实际执行和逐项结果为准；通用业务质量仍为 NO_GO，源码侧 13 项静态恢复、42 项部分、9 项不可得，隐藏来源 64 项未恢复，盲测样本为 0。源码存在、阶段验收和生产上线分别记录，运行中的真实版本通过 get_contract_analysis_capabilities 查询。

引擎、运行时源码核对及计算型余额见 [EVM 合约分析](contract-analysis-evm-coverage.md)；资金参数和固定区块控制地址见 [资金公式与控制权限](contract-analysis-evm-semantics.md)；多返回值、循环、角色条件、跨合约上下文及协议公式见 [方法机制与资金依赖](contract-analysis-evm-mechanisms.md)。下文保留原发布基础能力的说明，各阶段验收记录保存在对应 acceptance 目录。

## 源码与字节码输入

原有 chain/address 请求继续绑定固定区块。新增 input.kind=source 接收 Solidity 具名文件、包含 commit 的完整编译器版本、设置和 file:Contract 目标；input.kind=bytecode 接收完整十六进制代码及 runtime/creation 阶段。输入总请求沿用 2 MiB 上限，源码最多 256 个文件、合计 1536 KiB，单份代码最多 49152 字节。文件路径使用安全相对路径，可保留 ./ 前缀，导入只使用提交文件，编译在无网络容器内进行。

未指定部署时，结果 identity_kind=code_only，保存 input_sha256、代码阶段及编译身份；state_granularity=symbolic_state。JSON 中省略观察链、区块、时间和地址字段，声明的 chain_id、execution_address、evm_rules 单独存入 declared_environment。全过程不申请 Provider 请求。实际角色成员、历史初始化、余额和交易不会由默认值补齐。

同时指定 chain/address 时，提交源码重编译后与实际运行代码核对；提交运行字节码必须与观察代码一致。创建代码不能直接与部署运行代码等同。源码编译失败保留原输入与完整编译诊断；同时提供运行代码时继续字节码分析。只有创建代码时，返回反汇编和运行代码尚未建立的 frontier。只有编译模板时，不将不可变值或链接库占位零值当作部署观察；对应字节码语义引擎保持未运行，并继续源码模型。

状态查询返回原输入工件 ID。该输入在排队、运行、失败和取消后都可通过原有工件工具读取，继续使用任务作用域、导出权限、片段及整体 SHA256。其他结果仍在原子发布完成后读取。

methods、claims、funds、permissions、flows 及模型结果附加 analysis_dependencies；conclusions 汇总稳定结论身份、代码/状态上下文、条件、假设、表达式、调用、证明义务和证据引用。analysis_gaps 记录未解析依赖、位置及受影响的结论 ID。静态模型不因此升级为真实执行或完整性质证明。

依赖明细超过 2 MiB 时，conclusions、analysis_gaps 和各行依赖字段改为完整明细的索引。dependency_artifacts 给出工件、编码、解压后长度及 SHA256；通过原有工件工具取得 conclusion-dependencies.json.gz 后，按 dependency_record 读取对应记录。该转换完整保留每项引用和缺口，原有语义分区继续分页读取。

引擎标准输出仍限制为 64 MiB。较大结果在该边界内使用带压缩及解压 SHA256 的内部 gzip 封装，后端最多展开 128 MiB，并再次核对版本、规则和完整 snapshot；通过后恢复原有结果结构，再进入原有保存、分页和工件链路。普通结果继续使用原 JSON 协议，单份用户工件的原有限制保持不变。

## 局部失败恢复

源码通过编译及代码身份核对后保存原文、布局、方法声明和参数。每个方法单独建立保存点，总表达式上限 50000，每方法初始上限 8000，方法分析初始上限 15 秒，源码主体初始上限 90 秒。超限或方法分析失败时，同时撤回该方法尚未完整生成的步骤、表达式、状态、调用、资金、定义和证据，保留此前成功的方法及未处理入口；缺口保存具体方法和原因。源码模型不完整时，跳过整份源码的状态及权限闭合验证，避免把未处理入口排除后声明完整证明。

反汇编和 selector 发现仍由父进程完成。SEVM 的符号构造、遍历及伪代码、IR 导出在同一无网络容器内的 Node 子进程执行，堆上限 512 MiB、最长 60 秒、传输上限 32 MiB。较大结果使用无损 gzip，展开上限 128 MiB，分别核对压缩及展开后的长度与 SHA256，再验证请求和记录引用。父进程预留 20 秒用于解释和保存，后端把原任务剩余时间传入运行器，不另起任务或重置累计预算。

子进程结果绑定请求 SHA256、完整 snapshot 和规则，逐项检查行身份、来源方法、工件 SHA256 及引用后整体合并；修改源方法参数、重复记录、串代码或悬空引用均拒绝。超时、堆耗尽、信号退出、异常退出和损坏输出分别保存原因。独立成立的源码结果及反汇编继续保存，运行代码子引擎失败不附加到构造方法的语义上。对应任务仍通过已有 MCP、分页和完整工件读取。

容器内观察器等待引擎退出并完整转发输出后，再读取 cgroup 峰值内存和 CPU。记录绑定实际传输的长度、SHA256 和引擎退出码；后端核对后保存到 metrics.container_execution，任务汇总到 engine_resources。子进程 RSS、容器峰值和 worker 服务峰值分别保留。

## 开源组件与自研部分

| 组件 | 正式版本 | 实际用途 |
|---|---|---|
| WhatsABI | 0.29.0 | 从运行字节码提取入口与候选接口 |
| sevm | 0.7.4 | 符号表达式、IR、伪代码和调用、存储证据 |
| Heimdall | 上游 release 0.9.3；二进制报告 0.9.2 | 离线反编译工件；记录两个版本及二进制 SHA256 |
| Solidity compiler | 0.4.13 至 0.8.37 中锁文件列出的 90 个稳定版本 | 按原始设置重编译、核对字节码，再提取 AST 与布局 |
| Anvil | Foundry 1.8.5 | 在没有外部网络的容器中，用交易前状态 witness 重放 |
| 自研模型 | contract-analysis-v1，规则版本由能力接口返回 | 方法身份、条件与顺序、精确金额、中文作用、跨方法依赖、实际资金与状态核对 |

编译器的实际身份与请求版本再次核对。npm 的 solc 0.8.1 包报告编译器 0.8.0，拒绝把它用于 0.8.1 源码。旧编译器缺少 storageLayout 时，只推导已实现并验收的 Solidity 存储规则；出现未知聚合类型后，后续槽位保留为未解析。

Slither、Gigahorse、crytic-compile、evm-storage、Ethpector、Mythril、Halmos、Panoramix、NeverD 已纳入研究表。本次正式组合采用 AST、sevm 和自研模型；本轮新增 Slither、crytic-compile、Gigahorse 和 Vyper；其他研究项目按实际接入与验收状态记录。项目资料见 [批准的开发计划](contract-analysis-development-plan.md)。

## 方法分析

每个方法包含完整 code/storage 身份、selector、签名依据、参数和返回值、中文作用、guard、步骤、状态、调用、金额表达式、依赖方法、交易实例和证据 ID。modifier、内部调用和参数绑定展开到调用方法；继承中被覆盖的定义与实际外部入口分别保存。constructor 保留为部署源码，不当成运行字节码入口。

金额和状态值使用整数或精确表达式。表达式保留位宽、符号、checked/unchecked、除法顺序、向零取整和显式类型转换。mapping 基槽不读取为账户余额；没有选择或观察到的 mapping key 不填写零余额。

有源码时，先核对编译器、重编译字节码、Sourcify 匹配记录和固定区块字节码。核对失败后保留原始来源文件与冲突，使用实际字节码通道。手写汇编和未完成源码路径可以补充字节码证据，仍使用同一个方法身份。

页面的“已解析”表示当前语法和数据模型的解析情况。全部可执行分支没有穷尽验证时，任务保留 `completed + partial`、覆盖维度和待分析对象。

## 实际执行与资金

选定交易保存原交易、receipt、区块及父块、callTracer、prestateTracer 与 transaction diff。历史代码来自该笔交易前状态，包含同块前序交易的影响。调用中保存实际 caller、代码地址、状态地址、完整 calldata 和 trace_path。

成功 CALL、CREATE 和 SELFDESTRUCT 的原生币支出与静态付款表达式分开。失败祖先下的付款、DELEGATECALL 的重复 value、STATICCALL 和同地址原生币 self-call 不计为独立已结算支出。Gas、可取得的 L1 fee、blob fee 与业务手续费分开。

| 资产或结构 | 当前能力及实际验证 |
|---|---|
| 直接 balanceOf mapping 的 ERC20 | 验证源码及交易前代码身份后，核对事件净额与账本净变动；保留精确差异及未观察 key |
| 原始 WETH9 运行代码 | 以代码哈希识别 Deposit/Withdrawal，连同 Transfer 核对铸造、销毁和余额变化 |
| ERC721 / ERC1155 | 严格按 receipt ABI 形状保存资产编号、数量和 batch_index；不自动推导所有权存储布局 |
| ERC1967 / beacon / minimal proxy | 保存代码和状态地址；本地 EVM 验证委托调用及代理状态 |
| Diamond | 有界 loupe 查询、每个 selector 的 facetAddress 核对和 frontier；局部 fixture 验证调用，生产 resolver 还受上游 RPC 能力限制 |
| 自定义代理、AMM、vault、staking、奖励和桥 | 通用源码、调用、状态和资金表达式可读取；协议特有换算、份额或桥结算须有对应可验证状态，未具备的 adapter 保留具体缺口 |

`transaction_net_ledger_matched` 表示该笔交易内已观察地址的账本净额相符。逐条事件的独立结算另记为未验证。转账税、重基准、兑换及其他余额变化可以产生差异记录，不自动修改事件金额使账目相符。

Ethereum 的受支持区块按执行规则选择 hardfork。精确重放核对 status、日志、Gas、账户余额/nonce/code 及取得的状态槽。未知账户、未知状态槽、缺失历史区块、环境不完整或结果差异均保留为具体状态。其他 EVM 链尚无正式重放规则时返回 `unsupported_chain_rules`。TRON、Solana 入口保持关闭；Vyper 0.4.3 的 EVM 源码通道另行核对，其他 Vyper 编译版本返回明确缺失。

## 历史范围与版本

请求范围内的 selector 调用统计来自已有索引和提交覆盖记录，不自动下载外围地址历史。完整分页返回真实总数。索引范围缺失、历史代码版本归属未证明、内部调用未覆盖时，各维度分别记录。选定交易的深入解释不替代全范围历史覆盖。

两个已完成任务可以按代码地址、状态地址、selector 和入口身份比较版本、方法、权限、资金表达式及观察到的配置状态。selector 冲突和同名方法分别保留。

## 工作台、MCP 与文件

用户端增加分析列表、新建页和工作台，地址详情提供合约分析入口。工作台覆盖方法、条件、步骤、金额、状态、真实交易、重放、资金核对、图、版本、原始证据和文件。method_id、evidence_id、分页和比较任务写入 URL。参数和条件按字段展示，完整机器字段在详情弹窗中读取。

合约分析提供 9 个 MCP 工具：`get_contract_analysis_capabilities`、`analyze_contract`、`get_contract_analysis`、`list_contract_methods`、`get_contract_method_analysis`、`get_contract_execution_flow`、`explain_contract_transaction`、`compare_contract_versions`、`get_contract_analysis_artifact`。工件还提供受作用域保护的 `contract-analysis://` resource。所有查询支持有界分页，文件按片段和整体 SHA256 校验。

导出包括 JSON、方法 CSV、Markdown、DOT、可编辑图数据和包含原始工件的 ZIP。ZIP 内保存逐文件长度与 SHA256。单个保存工件当前上限 32 MiB，读取块最大 4 MiB。DOCX、XLSX 导出、人工批注保存界面及通用有界符号求解尚未实现。

## 资源与隔离

专用 worker 使用租约、fence、按处理版本领取任务和全局重型任务锁。runner 默认 2 vCPU、2 GiB、128 PID、只读根目录、非 root、无网络、无宿主目录或 Docker socket 挂载。输入限 24 MiB，单行限 4 MiB，缓存分区限 16 MiB。正常分析的 EVM 写操作只发生在容器内的模拟节点。

RPC HTTP 请求、重试、credits、状态读取和处理时间使用持久预算，提交时固定区块的请求也纳入计数。取消与恢复不重置预算。成本来自 provider_usage 中的实际请求及配置单价，页面同时提供费用和计数。查询已保存的方法及证据不产生新链请求。

通用 MCP cancel_analysis、resume_analysis 处理合约任务时，按相同 analysis_id 写入 contract_analysis.cancel、contract_analysis.resume 审计事件，包含组织、工作空间及返回状态。原有接口参数、权限和任务控制规则继续使用。

正式样本和发布证据保存在 `acceptance/contract-analysis/`。性能数值是对应镜像与样本的实测值，各个未解析能力和来源失败仍可在结果中读取。

## 字节内存与 ABI 候选

`funds-semantics-16` 在独立 QA 中验证。实际服务使用的规则和镜像以 `get_contract_analysis_capabilities` 返回值为准。

字节模型保存写入前后的版本、非对齐读取、低八位写入和重叠复制快照。TAC 使用块内非终止语句顺序和块间 CFG 边；反编译文件的行次序、跨块 lexical next 不作为执行顺序。未完成循环、入口内存、返回长度和路径保留具体前沿。TAC 的 frame_id 表示符号方法分析上下文，实际交易帧仍由执行证据识别。

源码 bytes 按编译器声明和构造时的参数表达式绑定，支持局部别名、重绑定、内部参数、显式及命名返回、tuple 返回、条件分支和短路求值。对象地址使用相对偏移；原始编码被覆盖、空闲指针被改写或内部函数指针无法展开时，保存失效版本及原因。严格匹配的首条汇编 CALL 可使用进入该块前已建立的 bytes 对象。

ABI 模型保存标量和嵌套 tuple、数组、bytes、string 的完整整数、相对头尾、长度及范围。规范布局检查单独记录；非规范布局的目标解码器行为通过具体执行验证。未知动态长度保留已知头部和未解析部分。源码中未修改的同类型 `abi.encode`、`abi.decode` 可以关联到构造时的参数值，未知结构体值继续保留缺口。

`call_input_bindings` 关联调用及其字节快照；`memory_versions`、`memory_reads` 和 `memory_frontier` 保存 TAC 及对象内存依据；`source_memory_versions`、`source_memory_phis` 保存源码对象及条件候选；`abi_values`、`abi_decodings` 保存字段和解码。`call_argument_models`、`fund_call_models` 保存从 CALL 字节取得的候选接口及资金参数；目标实现、成功返回和祖先回滚未绑定时，其资金状态保持 pending。

使用 `get_contract_analysis(section=..., method_id=...)` 分页读取这些分区，完整记录同时进入机器工件和证据 ZIP。`list_contract_methods` 提供方法的内存与调用绑定 ID；原有方法详情、流程、版本和交易解释工具继续读取各自保存结果。字节恢复不提升完整权限、完整经济模型和所有路径证明状态。

原真实材料已经用于开发或审核，后续计作回归。历史 QA 曾继承 dRPC 管理后台同步，旧 Provider 使用表未覆盖这部分 HTTP 和费用；完整外部预算继续为 NOT_VERIFIED。后续取材请求保留为零，隔离 QA 关闭管理同步与启动探测。
