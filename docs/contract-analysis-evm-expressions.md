# EVM 条件计算与局部赋值

本轮在已部署的 funds-semantics-4 上增加只读方法的表达式局部赋值与有序执行记录，规则为 funds-semantics-5。

## 计算方式

局部标量赋值、复合赋值、前置和后置自增减生成独立表达式绑定。后续读取使用赋值后的绑定，后置表达式返回修改前的值。保留整数宽度、符号、Solidity 0.8 的检查运算、unchecked 和旧版回绕，以及显式转换的截断。

&&、|| 和三元表达式只执行选中的分支。每条成功返回路径保存 execution_steps，按实际顺序列出计算与 guard；变量初始化、被覆盖或丢弃的计算也保留。因此，返回常量 1 的方法仍可记录之前的除零或溢出条件。只读内部调用的返回模型与调用参数分别绑定，包含赋值的参数先归一化，再进入被调用方法。

普通二元运算、函数参数和元组的兄弟表达式先比较局部读写集合。如果它们的读写会互相影响，记录 INTERNAL_RETURN_EVALUATION_ORDER_UNSPECIFIED。Solidity 没有为这些节点保证统一的求值顺序，当前模型不选取猜测顺序。引用类型、存储写入和动态调用没有因此取得完整解析状态。

## 数据与报告

表达式模型、dataflow、方法详情和完整结果保留有序执行记录。Markdown 报告在对应返回路径下按序输出计算和条件。原始源码分析中尚未经过有序模型的赋值表达式标记 LOCAL_EXPRESSION_EFFECT_REQUIRES_ORDERED_RETURN_MODEL。

本轮沿用 contract-analysis-v1、既有 API/MCP/Actions、分页、权限、审计和任务状态。没有数据库迁移。旧任务保留原镜像与已发布工件，新任务按候选镜像分析。

## 验证样本

固定 Uniswap v2-core v1.0.1 的提交 4dd59067c76dea4a0e8e4bfdda41877a6b16dedc，保留原始 SafeMath.sol、LICENSE 和 SHA-256。Solidity 0.5.16 实际编译、部署和执行原始加减乘库；Solidity 0.8.30 验证检查运算、短路、三元、参数赋值和前后值。两代编译器分别开启和关闭优化。

新增本地执行检查覆盖精确大整数、8 位和有符号整数边界、加减乘溢出、除零、空回退数据、带原因的回退、Panic 错误、跳过分支、丢弃计算以及实际付款/回退。所有交易在本地 Anvil 模拟网执行。公开合约样本固定区块和代码哈希，比较升级前后的具体方法缺口，不将样本数量转换为全体合约解析率。

## 仍有的缺口

动态循环边界、复杂汇编、存储引用别名、含状态写入的返回计算、动态外部返回与交易上下文绑定、后置或条件 modifier、完整角色成员枚举、复杂签名与多签整体授权、通用协议经济证明和缺失历史 witness 继续保留具体状态。

只读返回模型最多 8 个标量输出、32 条路径、每个循环 16 次、累计循环 64 步、调用深度 12。超出限制产生缺口，不标记为完整路径证明。

依据：[Solidity 求值与控制结构](https://docs.soliditylang.org/en/latest/control-structures.html#order-of-evaluation-of-expressions)、[固定 Uniswap SafeMath 源码](https://github.com/Uniswap/v2-core/blob/4dd59067c76dea4a0e8e4bfdda41877a6b16dedc/contracts/libraries/SafeMath.sol)。
