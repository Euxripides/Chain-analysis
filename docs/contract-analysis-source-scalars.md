# 源码标量值与账本数量关联

本单元属于 `funds-semantics-18` 隔离候选，标量模型版本为 `source-scalar-v1`。生产版本与候选版本分别管理，实际可用能力通过 `get_contract_analysis_capabilities` 的 `engine_digest` 和 `semantic_rules_version` 核对。

## 读取时捕获值

源码分析按编译器声明 ID 和源码调用范围保存整数、布尔、地址、合约类型、枚举及定长 bytes 的值版本。数组、struct、动态 bytes/string 不属于这个标量模型；已有字节内存、ABI 和存储引用模型分别处理其已支持部分。

读取表达式绑定当时的值版本。后续重新赋值建立新版本，不改写已有读取。内部调用和重复 modifier 的参数各有独立范围，分支合流保存条件及两个正常后继值。已经 return 或 revert 的分支不提供正常后继值。

`frame_id` 是源码调用范围的稳定标识，`frame_basis` 为 `source_invocation_scope_not_observed_EVM_frame`。它不是实际交易调用树中的 EVM 帧编号。状态读取记录对应的源码位置和表达式身份，不填入未经读取的历史余额或槽值。

## 结构化结果

| 分区或字段 | 内容 |
|---|---|
| `source_scalar_versions` | 声明、类型、范围、求值点表达式、条件、次序和来源 |
| `source_scalar_phis` | 条件表达式、两个候选版本及合流值 |
| `value_derivation_id` / `value_derivation_status` | 值展开及未解析依赖 |
| `previous_source_scalar_version_id` | 历史赋值关系；普通完整覆盖不把旧值当成新值输入 |
| `parent_source_scalar_version_ids` | 当前值实际使用的父版本 |
| `write_target_expression_id` | 赋值或自增表达式的编译器写入目标 |
| `write_target_role` | `write_only` 或 `read_modify_write` |
| `write_value_expression_id` | 赋值右侧的读取表达式 |

显示写表达式时保留左值，继续按实际调用参数展开右侧值。例如，内部方法的 `z = x + y` 可以显示为 `z = left + right`；不会把 `z` 的旧零值放到等号左侧。尚未完成有序返回效果建模的表达式继续携带 `LOCAL_EXPRESSION_EFFECT_REQUIRES_ORDERED_RETURN_MODEL`。

## 未知数量的账本关联

数量没有被数值解析，不等于两处读取之间的身份关系也丢失。账本写入与 Transfer 事件使用同一个已捕获值时，可以保留对应资金记录，并继续将金额标为 partial。

不透明值的关联同时要求：

- 相同表达式身份或有效的已捕获标量版本；只有相同文字不建立关联。
- 写入和事件处于相同源码调用范围。
- 写入的条件包含在事件的条件中。
- 存在实际余额账本写入，且该账本由经过验证的 `balanceOf(address)` 方法体读取；事件本身不形成付款或增发结论。

两次独立调用即使源码文字相同，也不能当成同一个返回值。不同调用范围、冲突分支或共享预算哨兵均不能建立数量身份关系。对应 `ledger_effects[].value_identity_basis` 为 `shared_opaque_value_in_source_frame` 时，数值及外部结算仍需分别核实。

纯源码输入的自有账本资产使用 `address(this)`，并标记 `asset_scope=symbolic_self_storage`。有观察到的存储执行地址时，保留实际地址并使用 `snapshot_storage_context`。符号自有资产不会给源码输入增加部署地址、区块或观察状态。

## MCP 读取

使用已有工具提交和查询，不增加工具。取得方法 ID 后，可以按方法分页读取：

```json
{
  "analysis_id": "<analysis_id>",
  "section": "source_scalar_versions",
  "method_id": "<method_id>",
  "page_size": 200
}
```

同样可以读取 `source_scalar_phis`、`dataflow`、`funds` 和 `method_details`。方法详情中的 `scalar_model_references` 给出分区查询条件和记录数。现有接口按 `method_id` 查询；`frame_id` 在返回记录中读取。

完整机器结果、分区工件和证据包通过 `get_contract_analysis_artifact` 读取。值版本、表达式、来源及工件标识在保存后保持关联，工件按完整 SHA256 核对。

## 边界与验收

预算维持每方法 512 个、每任务 4096 个标量版本。达到上限后保留具体缺项，预算哨兵不提供跨变量的值身份。

循环不变量尚未证明的更新、未建模的汇编写入、动态函数返回、try/catch 合流、复杂待返回值和缺少内存值模型的聚合引用继续传播缺项。数值依赖已展开也不等于整数溢出、全部执行路径、任意代币结算或完整权限与经济模型已经证明。

候选分别核对编译场景、实际 EVM、冻结真实材料、严格差分、MCP 保存及分页、工件、数据库和审计。测试记录只绑定对应镜像。被拒绝候选的通过记录和失败反例均保留，不能转算给后续镜像；部分结果不作为通用验收通过。


## 未知元组与返回消息参数

未知tuple分量显示为 `tuple_component(调用表达式, 索引)`，索引从0开始。表达式记录保留编译器分量类型，原调用、外部执行或不支持返回模型的缺项继续传播。不同分量不会显示成同一个调用值；这个显示方式不证明实际数值。

内部只读返回摘要支持字符串消息参数作为require的只读消息表达式，并保存条件和消息参数的求值记录。会写入局部量、调用其他消息生产函数或不能证明无副作用的消息仍保留缺项。消息处理不建立Gas可执行性证明。

匿名返回声明不能由函数正文或modifier引用，不创建局部标量绑定；返回表达式仍保存在返回模型。命名返回值继续捕获初始值、赋值及modifier边界，原512/4096上限不变。


## 默认值的类型表示

源码局部值与只读返回摘要共用标量默认字面量。address、address payable及合约类型保留完整20字节零地址；bytesN保留编译器声明的N字节；bool使用false，整数及enum使用0。数组、结构体、bytes/string引用不作为标量默认值。结构化记录继续保留编译器类型和声明ID。

默认值的完整地址表示只描述源码类型和值，观察到的部署地址、历史状态和账户余额仍由各自证据给出。

## 隐式字节转换

固定长度字节在赋值、内部参数和返回值绑定时保留来源类型及目标宽度。bytes2转为bytes4时在右侧补零；展开为数值公式时仍显示来源类型，避免十六进制常量或参数按另一种类型重新解释。tuple先取编译器给出的实际分量类型，再转换为目标声明类型；括号不会新增tuple分量。类型缺失时保留明确缺项。

可赋给bytesN的短字符串、hex及unicode字面量使用编译器的原始字节，再补齐目标宽度。该处理限于不超过32字节的常量，不替代动态bytes或字符串内存模型。候选须分别核对原始合约付款、独立编译的类型化金额公式、CALL数量及双方余额。

整数赋值也保留目标位宽及符号。uint8输入转为uint256后参与加法时，展开式包含uint256(uint8(输入))；窄整数常量及默认零保留声明类型，不能改为无界字面量运算。正常付款、旧编译器回绕与新编译器checked回滚分别验证。
