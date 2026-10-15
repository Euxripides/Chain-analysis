# EVM 循环、汇编与性质验证

分析规则为 funds-semantics-6，承接已批准的 EVM 覆盖扩展。实现及验收分别保存于 analysis-engines、backend/internal/api 与 acceptance/contract-analysis-deepening。

## 动态循环

Solidity 的 for、while、do-while 可保存动态退出条件、每条成功返回路径和有序计算。前置检查、局部赋值、整数范围和分支约束用于确定有限边界，包含递减计数器、局部截取上限、break 和 continue。单个循环最多展开 16 次，累计循环预算 64 步，返回路径最多 32 条。

边界未能穷尽时，已取得的返回路径继续保留，模型状态为 partial。frontier 中的 dynamic_loop_remainder 保存剩余条件、计算步骤和已展开次数；同一循环在不同分支出现时，汇总结果保留全部剩余路径。已展开次数不会作为任意输入的循环总次数。

## Yul 计算

编译器提供的 Yul AST 接入只读返回计算，支持局部字变量、赋值、if、switch、局部函数、标量多返回和有限 for 循环。支持的 EVM 字运算包括算术、带符号除法和余数、addmod、mulmod、位运算、移位、比较、byte、signextend 和指数计算。每个节点保留运算名称、参数、256 位字规则及源码证据；空数据 revert 形成失败路径。

局部变量以独立声明身份跟踪，函数参数和返回值单独绑定。Solidity 外部引用只接受能够明确解释的完整字；窄整数脏高位、未建模的内存读写、存储副作用和其他主机操作保留具体缺口。运算语义依据 [Solidity 0.8.30 Yul 文档](https://docs.soliditylang.org/en/v0.8.30/yul.html)。

## 外部返回

external_returns 保存调用点、目标表达式、参数表达式、预期 ABI 返回类型、调用方式及调用者和状态上下文。公共库的 DELEGATECALL 保留原调用者、原调用金额和调用方存储地址。

external_return_observations 保存真实交易中的逐帧返回：交易哈希、区块、交易序号、trace_path、完整输入与返回数据、代码哈希、代码地址、存储地址、调用者、调用金额和证据引用。执行前代码与已核对方法唯一对应后，使用实际方法的输出 ABI 解码标量和动态数组，整数保持十进制字符串精度。

同一方法反复调用的返回按调用帧分别保存。子调用已返回而祖先回滚时，返回记录标为 decoded_return_on_reverted_ancestor_path。调用帧与源码表达式之间缺少程序计数器证据时，源码关联保留候选，不将某笔交易的返回值写成通用公式中的常量。

CALLCODE 使用调用方存储，msg.sender 为当前调用合约；DELEGATECALL 继承原 msg.sender。两种调用的实际返回调用者地址由本地 EVM 交易和 trace 分别核对。

## 权限和金额性质

冻结基础镜像已有的 Z3 共享库用于验证三个明确命题：成功通过某项条件时是否必须满足调用地址相等、是否必须满足角色或状态条件，以及源码表示的两个返回分量是否守恒于相应输入。金额命题由“输入减去分量，并返回该分量”的实际计算关系生成，不依据方法或变量名称判断业务性质。

求解使用有类型的符号输入和状态，保存整数边界、checked/unchecked、显式截断和逐步除法。映射读取按存储身份和符号键表示，同一存储位置的相同键共享取值。证明前先检查成功条件是否可满足，防止把不可能成功的条件报告成有效证明。

property_obligations 保存命题、假设、方法、代码和存储身份、证据以及求解结果。状态包括 proved_under_declared_model、counterexample、infeasible_assumptions、unknown、unsupported 和 solver_unavailable。每个已提交求解的命题另有 property_proof 文件，保存完整 SMT 查询、输入含义和反例取值。单次分析最多 24 项命题，每次求解检查限时 300 毫秒；引擎进程另有整体超时和容器资源限制。

这些命题的范围保存在 proof_scope 和 assumptions 中。完整角色成员枚举、签名与多签有效性、可达状态转换、提现可达性及协议整体经济不变量仍需相应模型和证据。

## 查询和文件

原 API/MCP 工具、参数、路由、权限和数据库结构继续使用。get_contract_analysis 可分页读取 external_returns、external_return_observations、property_obligations、dataflow 与 frontier。方法记录和 method_details 增加 property_ids；能力查询列出实际支持范围和证明状态。

Markdown 报告新增外部返回依赖、执行返回结果与性质验证。完整 JSON、证明文件和证据 ZIP 使用同一保存结构。旧任务保留原引擎 digest，不回写历史结果。

## 验证

固定源码分别使用优化开启和关闭的编译结果。新增本地 Anvil 验证覆盖动态循环边界、整数溢出、Yul 字运算、零除和移位边界、函数与多返回、拒绝授权与开放分支、求解反例的实际金额、动态数组返回、重复调用和祖先回滚。未建模内存、窄整数引用及无界循环使用负例核对。

完整验收同时检查既有公式、机制、历史重放和金标准，以及真实隔离 API/MCP、数据库、逐文件校验的 ZIP、工作台和原服务。实际检查数量、镜像、代码哈希及费用以 acceptance-summary.json 和发布清单为准。

OpenZeppelin ERC4626 固定样本在本轮初次扩展后出现公式重复展开，耗时 294632 毫秒。增加源码、证据和表达式索引，以及按变量定义边界保存的有界展开缓存后，六引擎隔离复测为 17680 毫秒；先前已部署版本的对应记录为 31134 毫秒。金标准另设 120000 毫秒的该样本验收上限。
