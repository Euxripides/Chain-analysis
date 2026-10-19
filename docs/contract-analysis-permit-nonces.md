# 合约签名随机数分析

本阶段在现有签名和状态模型中增加命名空间 nonce、allowance 字段识别，以及覆盖全部变更入口的 nonce 最终状态检查。候选规则为 funds-semantics-13，生产服务继续使用 funds-semantics-8。候选是否通过验收，以 acceptance/contract-analysis-permit-nonces 中的冻结清单和实际检查结果为准。

## 字段和账户身份

普通公开映射及已经验证的 ERC7201 命名空间字段使用原始编译器声明身份。自定义 getter 的映射键需要逐一对应函数参数声明；固定读取其他账户、交换 owner 与 spender、无法核对命名空间根或字段的情况保留具体缺口。

继承接口中的无函数体声明不作为 getter 实现。实际 getter 使用已经建立运行方法身份的声明；公开状态变量使用其自身的声明和布局，避免接口声明遮住具体映射。

命名空间内部的局部存储别名沿用已验证的指针与字段来源。其他命名空间中同名的 nonces 字段使用独立的声明和物理存储身份。

## 两类性质

erc2612_permit_integrity 检查所识别 permit 入口的成功路径：签名主体、域与授权动作、期限、nonce 消耗及 allowance 写入。

permit_nonce_monotonicity 检查所有变更入口的完整成功路径。对路径实际写入的每个 nonce 账户，读取整个入口前后的 nonce，验证最终值不递减。同一入口先清零再恢复时比较最终存储。失败路径按原子回滚处理。

两类模型分别保存结果。某个 permit 入口通过检查时，其他入口仍可能重置 nonce；nonce 不递减也不替代 permit 对 nonce 恰好消耗一次的检查。缓存域历史、域切换、多签、合约签名和完整权限证明继续单独推进。

未知原始槽写入、无法关联的账户键、不完整入口、求解超时及证明预算耗尽都保留相应状态，不发布整个 nonce 模型已经闭合的结论。

## 状态退出

状态模型按编译器声明身份识别 Solidity 内建 revert() 和字面量错误消息，恢复持久存储及临时存储的入口状态。未建模的错误消息表达式保留缺口。测试同时保留临时清零后恢复、清零后回滚、checked 溢出回滚和 unchecked 环绕的区别。

## MCP 和证据

现有 get_contract_analysis_capabilities 返回 nonce_state_analysis 和新增模型种类。授权模型继续通过 get_contract_analysis 的 authorization_models 分区读取；模型保留 permit_model_id、nonce_state_ast_id、entry_coverage、obligations、assumptions、evidence_ids、unresolved、status 和 proof_counts。

verification_obligations 中每项义务保留具体方法、模型、性质、状态及 proof_artifact_id。原有 get_contract_analysis_artifact 读取完整 SMT 约束、求解结果和反例，继续验证分块与整体 SHA256。现有 JSON、Markdown 报告和工作台模型分区读取同一批保存结果。

## 执行验证

开发样例包含普通存储、命名空间直接访问、局部别名、无关命名空间、跨入口重置、临时恢复、回滚、nonce 递增、unchecked 环绕及未知原始槽写入。优化开关和两种代码生成方式分别记录，共享实现不重复计入真实业务样本数量。

真实 EVM 保存首次签名、nonce 重置及同一签名再次成功的交易，连同 receipt、调用树、交易前状态、完整状态变化及隔离重放结果核对。失败尝试和环境差异保留在 prior-attempt 与 gas-environment-probe 中。

新增执行样例使用已核对空状态根的本地创世 fork，并先运行 Shanghai Gas 校验交易。原始 Anvil 非 fork 模式与隔离 fork 模式在特定 calldata 清零交易上存在已复现的 Gas 差异；保留具体二进制、启动参数、交易及观测结果，不修改 receipt 使其相符。

阶段记录区分源码测试、冻结候选执行、后端解析与证明重写、实际 MCP 提交与读取、生产部署。第一项通过不自动替代后续验收。
