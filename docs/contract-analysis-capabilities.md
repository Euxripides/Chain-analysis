# 合约运行与方法分析

本功能以固定区块、区块哈希和代码哈希为分析身份，输出运行机制、方法作用、执行条件、有序步骤、金额公式、状态读写、权限、依赖关系、选定交易和证据文件。入口地址、实现代码地址和状态地址分别保存。方法名称、注释及候选 ABI 不替代函数体和交易证据。

当前 EVM 规则为 funds-semantics-4。引擎、运行时源码核对及计算型余额见 [EVM 合约分析](contract-analysis-evm-coverage.md)；资金参数和固定区块控制地址见 [资金公式与控制权限](contract-analysis-evm-semantics.md)；多返回值、循环、角色条件、跨合约上下文及协议公式见 [方法机制与资金依赖](contract-analysis-evm-mechanisms.md)。下文保留原发布基础能力的说明，各阶段验收记录保存在对应 acceptance 目录。

## 开源组件与自研部分

| 组件 | 正式版本 | 实际用途 |
|---|---|---|
| WhatsABI | 0.29.0 | 从运行字节码提取入口与候选接口 |
| sevm | 0.7.4 | 符号表达式、IR、伪代码和调用、存储证据 |
| Heimdall | 上游 release 0.9.3；二进制报告 0.9.2 | 离线反编译工件；记录两个版本及二进制 SHA256 |
| Solidity compiler | 0.4.13 至 0.8.37 中锁文件列出的 90 个稳定版本 | 按原始设置重编译、核对字节码，再提取 AST 与布局 |
| Anvil | Foundry 1.8.5 | 在没有外部网络的容器中，用交易前状态 witness 重放 |
| 自研模型 | contract-analysis-v1 / funds-semantics-1 | 方法身份、条件与顺序、精确金额、中文作用、跨方法依赖、实际资金与状态核对 |

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

正式样本和发布证据保存在 `acceptance/contract-analysis/`。性能数值是对应镜像与样本的实测值，各个未解析能力和来源失败仍可在结果中读取。
