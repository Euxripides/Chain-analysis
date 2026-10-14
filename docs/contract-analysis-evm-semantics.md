# EVM 资金公式与控制权限

本轮使用 funds-semantics-3，承接已批准 EVM 扩展计划第三阶段。分析结果保留原始表达式、完整地址、源码位置和逐项证据。

## 方法计算

Solidity 内部 pure/view 方法支持局部变量、赋值、复合算术赋值、条件分支、提前返回、具名返回变量及标量默认值。使用实际调用参数建立独立变量绑定；同一方法多次调用和不同分支分别处理。successful_return_paths 列出成功返回的条件和公式，条件选择公式保存在 symbolic_expression。

每个节点保留整数类型、位宽、符号、取整顺序及 checked/unchecked。内部方法使用自身的算术作用域。存在循环、汇编、存储别名、状态副作用、元组返回、修饰器、动态调用、递归或超过预算时，保存具体未解析原因。单次返回模型最多表示 32 条路径。

内部调用的提前返回条件保持在该调用内，避免把免手续费或金额上限分支误写成外层方法的必要条件。

## 账本变化

通过已核对 balanceOf 方法读取的状态识别余额账本。除了直接 += 和 -=，也支持经内部返回模型展开的余额自身加减赋值，包括旧版 SafeMath。Transfer 的金额与已表示账本增减建立联系后形成静态资金记录；单独事件和直接替换余额不生成这种记录。

账本记录保存 delta_operation、delta_expression、delta_expression_id 及原始写入表达式。余额核对仍使用实际交易前后状态。

## 配置和权限

control_effects 区分资金计算参数、收款对象、扣款对象、资产合约、执行条件和调用地址权限。affected_operations 保存受影响资金或条件的标识、读取状态及表达式；具名变量尚未完整求解时，标为 possible_dependency_in_unresolved_definitions。管理员地址变更可以通过受控配置方法关联到其资金方法，并保留中间方法和控制记录。

permissions.caller_constraints 保存 msg.sender 或 tx.origin 的地址相等条件，分别标明完整条件、同时满足的条件和复合条件中的一个分支。已有同一快照的直接地址状态字可生成 snapshot_target_address 和 snapshot_observations，记录完整状态地址、区块号、区块哈希、槽、偏移及原始状态字。交易前状态、冲突值、其他状态地址和过期区块不会使用区块末值补齐。

角色映射、签名、多签、复合地址计算及未取得状态保留待求值结果。地址相等条件与完整授权验证分别保存。

## 查询和工作台

现有方法、资金、权限、公式、配置影响和证据查询继续使用原接口。method_details 增加控制记录、配置依赖和调用地址条件的引用。工作台直接展示中文资金影响、完整调用地址及返回条件，原始记录通过详情弹窗查看。报告和机器结果包含相同保存字段。

get_contract_analysis_capabilities 增加 semantic_rules_version、source_return_analysis 和 funds_control_analysis。工具数量和原参数保持一致。

## 验证记录

验收记录位于 acceptance/contract-analysis-semantics。包含 Solidity 0.4.18 SafeMath 独立样本、两个优化设置的返回模型、本地 EVM 真实执行、公开 USDT 固定历史区块、旧金标准、重放、API、MCP、数据库、工件及桌面和手机页面检查。入口发现 corpus 的原有结果继续保存在上轮目录，本轮不重新计为新的业务样本。

算术作用域和返回变量处理依据 [Solidity 控制结构](https://docs.soliditylang.org/en/latest/control-structures.html)；项目使用已固定的编译器和开源引擎依赖。
