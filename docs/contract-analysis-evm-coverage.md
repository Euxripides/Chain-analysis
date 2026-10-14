# EVM 合约分析

资金公式与控制权限的后续实现见 [EVM 资金公式与控制权限](contract-analysis-evm-semantics.md)。

本轮集中扩展 EVM。分析以区块号、区块哈希、代码哈希、代码地址和状态地址为身份，保留源码、编译结果、中间表示、方法模型、资金表达式和交易证据。TRON、Solana 入口保持关闭。

## 分析引擎

| 引擎 | 用途 | 结果位置 |
|---|---|---|
| WhatsABI 0.29.0 | 发现字节码入口及候选接口 | methods、engines |
| sevm 0.7.4 | 符号执行结构、存储和调用表达式 | expressions、calls、symbolic_ir 工件 |
| Solidity 编译器 | 用准确版本重编译，核对运行代码，解析 AST 和布局 | source_verification、methods、storage |
| Heimdall | 离线伪代码、中间表示和辅助结构 | 分析工件、engines |
| Slither、crytic-compile | 已核对源码的 SlithIR、基本块与源位置 | ir、slithir 工件 |
| Gigahorse、Souffle | 字节码函数化 TAC、控制流、256 位数据流和副作用 | ir、control_flow、ir_dataflow、ir_effects |
| Vyper 0.4.3 | EVM 上 Vyper 源码的编译核对、AST、布局及方法分析 | methods、steps、funds、storage |

默认任务选择前六项；遇到 Vyper 源码时由源码适配器转到安装的 Vyper 编译器。其他 Vyper 编译版本返回版本缺失。Slither 的 Vyper 组合分别报告结果，不把 Solidity 的验收替代为 Vyper 验收。

Git 版本及授权文件见 analysis-engines/notices/coverage-dependencies.json。运行镜像固定到 digest；分析容器关闭网络、使用普通用户和只读文件系统，限制 CPU、内存、进程、输入输出和临时文件。来源取得使用平台的持久预算和请求记录。

## 源码与运行代码

源码服务给出的验证状态还需要本地重编译核对。Solidity 只允许编译器声明的 immutable 引用、库链接地址和库调用保护地址发生对应变化。每个 immutable 的重复位置必须一致。其他运行指令发生差异时，源码通道失败，保留来源文件和冲突记录。

有效的尾部 Solidity 元数据差异与运行指令差异分开。immutable 的具体地址、布尔值及整数来自快照运行字节码，参与金额和收款对象推导；源码构造函数中的初始表达式不替代这些值。

## 方法与运行机制

method_details 保存参数、返回值、全部已表示条件、步骤顺序、资金效果、状态读写、外部调用和具体未解析项。operating_flows 保存各代码路径上的步骤引用。control_effects 把配置修改关联到读取该状态的资金方法。

内部方法、modifier 和内部库调用沿参数展开。直接返回表达式保留括号；函数调用方的 unchecked 不传给被调用函数。局部变量重新赋值按出现顺序展开，分支后的多种值、复合赋值、递归、复杂返回路径及深度预算分别留下具体状态。

dataflow 分开记录金额、资产、付款方、收款方、条件和返回值的推导。保留原始表达式、展开表达式、定义引用和证据引用。金额采用整数原值和符号表达式，保留除法顺序、位宽、符号和 checked/unchecked。

SafeERC20 的常见路径沿使用者参数、abi.encodeCall 和直接汇编 CALL 识别。单独生成编码数据不生成转账记录。未解析的汇编分支及实际调用成败仍能查到原始代码和交易轨迹。

接口模型包括 ERC20、ERC721、ERC1155、ERC4626、Uniswap V2 pair 和 V3 pool。记录命中的已核对接口与实现操作，不仅凭接口套用协议价格、兑换率、收益率或项目身份。协议特有经济计算由方法表达式、状态与执行证据分别展示。

## 资产和余额

| 内容 | 本轮处理方式 |
|---|---|
| 原生币 | 保留静态支付、可接收入金、真实成功调用支出和执行 Gas |
| ERC20 | 转账、代扣、授权、增发、销毁、费用及账本操作分别表示 |
| 嵌套和打包余额 | 编译器布局推导映射键、结构体字段、位宽及偏移 |
| 计算型余额 | 对无外部调用、汇编、分支的已核对 balanceOf 表达式，使用交易前状态与差异进行受限整数求值 |
| 重基准 | 可核对份额、全局指数和比例共同构成的余额；缺少任何必要状态字时返回未观察状态 |
| ERC721 所有权 | 通过 ownerOf 的实际存储读取关联所有权写入，保留资产编号和收款对象 |
| ERC721 / ERC1155 receipt | 保留实际日志的资产编号、数量和批次序号 |
| 不确定接口参数 | transferFrom 的 uint256 可能表示金额或资产编号，未确认的类型保留 asset_quantity_or_token_id |

计算型余额求值支持已表示的无符号整数读槽、简单局部定义、内部直接返回、加减乘除、余数、移位、部分位运算及显式整数转换。检查除零、checked 溢出、未知状态和执行前后代码变化。条件分支、签名类型、数组、复杂返回和外部返回保持未支持状态。核对对象是该笔交易内已观察地址的净余额变化；逐条事件的结算另有字段。

## 代理与交易

保留 ERC1967、beacon、minimal proxy 以及有界 diamond 解析。委托调用使用实现代码身份和代理状态身份。选定交易取得 receipt、callTracer、prestateTracer 和状态差异；方法链接使用该笔交易实际代码版本。前状态包含同块先前交易的影响。

失败祖先、捕获回滚和重复委托 value 从实际资金支出中区分。Ethereum 有完整见证时按对应执行规则重放并核对 status、日志、Gas、账户和状态槽。其他 EVM 链的代码及交易解释继续可用；没有对应正式执行规则时，精确重放输出 unsupported_chain_rules。

## API、MCP 与工作台

现有 EVM 提交、查询、方法、执行流、交易解释、版本比较及证据文件接口继续保留。新增 get_contract_analysis_capabilities 返回配置镜像中观察到的引擎和执行范围。GET /api/v2/contract-analyses/capabilities 使用原有 explorer:read 权限。

MCP 默认九个合约工具，平台目录合计 120 个工具。新增结果通过 get_contract_analysis(section=...) 分页读取；methods 目录可定位方法，再通过 method_id 获取条件、步骤、资金、公式和状态。证据文件按片段 SHA256 和完整 SHA256 校验。

大型方法的详解通过 field_references 定位完整条件、步骤、资金及状态分区，full_view_artifact_id 保存原始详解文件和完整 SHA256。分区内容和原始视图都保留。大型机器结果的 contract-analysis.json 使用 complete_section_artifacts_v1 索引，列出全部分区文件、起始记录位置、记录数、字节数及 SHA256；完整文件同时进入证据 ZIP。

工作台增加方法详解、配置影响、接口模型、源码核对、公式推导、字节码公式、调用效果和控制流。分区、方法、证据和分页写入 URL，支持重新打开。只展示该结果实际存在的附属分区。

兼容发布保留旧任务中的镜像 digest。处理器只接受当前配置或明确列入 CONTRACT_ENGINE_PREVIOUS_IMAGES 的旧镜像，继续使用原输入和镜像恢复任务。保存结果不改写。

## 验收依据

acceptance/contract-analysis-coverage 保存源码和隐藏源码对照、金标准、公开源码构建样本、实际本地 EVM 执行、真实 API/MCP/数据库/工件/浏览器及故障检查。入口召回、逻辑实现数量、运行代码数量、公式与权限、实际交易及重放分别统计。

不同编译器和优化配置产生的代码数量不等同于同样数量的独立业务实现。静态模型列出的路径也不等同于全部状态下的可达路径。每个引擎和具体数据缺失保留实际结果状态；发布验收记录与分析结果自身的覆盖状态分别保存。
