# EVM 方法机制与资金依赖

本轮规则为 funds-semantics-4，承接已批准的 EVM 覆盖扩展。原查询、权限、证据身份和历史结果保持兼容；实际验收记录保存在 acceptance/contract-analysis-mechanisms。

## 方法和金额

已核对 Solidity 源码的 pure/view 方法支持最多 8 个标量组成的返回值、解构声明、解构赋值、交换变量、具名返回和分支返回。付款公式可使用其中某个返回值，保留原始表达式和实际调用参数绑定。只读外部入口新增 entry_return_derivation，直接列出完整返回公式、成功条件和循环展开信息。

支持前置检查后执行一次方法体的 modifier。内部方法的 modifier 同时进入调用方的步骤、条件和资金路径。包含后置或条件执行的复杂 modifier，其返回值计算保留未解析状态，已有语法步骤和原始代码继续查询。

for、while、do-while 的计数器和边界必须能由编译时常量及局部变量确定，单个循环最多展开 16 次，一次模型累计最多 64 个循环步骤，最多表示 32 条成功路径。支持 break、continue 和循环内提前返回。动态输入或状态控制的边界、溢出、不终止及超过预算分别保存具体原因。整数位宽、checked/unchecked、显式转换、逐步取整和条件分支保留在表达式中。

## 权限和配置

permissions.state_predicates 保存布尔状态或映射查询的要求值、条件结构、访问键及主体表达式，也保存状态数值比较。hasRole、名单、禁止名单等条件通过实际函数体和存储访问展开；角色名称没有独立证明作用。数值门槛保存比较操作，不自动当成已验证的签名数量。

权限状态的写入连接到相应资金方法或受控配置方法，保留 state_id、predicate_id、guard_id 和中间控制关系。动态映射键使用 symbolic_mapping_keys_require_identity，完整成员枚举和授权充分性分别记录，不以共享 mapping 声明代替具体成员关系。

## 跨合约调用

目标地址根据编译器绑定的不可变值或链接库、源码完整地址、同一快照的直接地址状态、具有明确先后关系的局部别名及地址类型转换解析。状态观察保存完整地址、状态字、槽、偏移、区块哈希和证据。过期区块、其他存储上下文、未知状态及复杂目标表达式保留待分析节点。

支持直接 CALL 中的 abi.encodeCall、abi.encodeWithSelector 和 abi.encodeWithSignature 转账或授权编码。selector 必须确定，地址和金额的 ABI 字符合相应形状；编码本身没有发出调用、selector 不符或动态参数形状不符时，不生成付款记录。未核对资产标准时，数量与 NFT 编号保持区分待定，实际结算依赖执行证据。

CALL/STATICCALL 使用目标的状态地址；DELEGATECALL/CALLCODE 使用调用方的状态地址。依赖按代码地址、代码哈希和状态地址去重，保存对应方法、代码身份和调用关系。接口方法 selector 来自编译器，旧版编译器缺少该字段时，只对可明确规范化的基础 ABI 类型计算 selector。

部分旧版 Solidity 将常量字符串数据放在 CBOR 元数据之前。只有发布的元数据范围与本地重编译字节完全相符、CBOR 包含匹配的 solc 版本、元数据之外的代码相同且来源标为 metadata match 时，才接受这种尾部差异；代码变化、错误范围及伪造来源仍拒绝。处理依据 [Solidity 0.5.16 元数据格式](https://docs.soliditylang.org/en/v0.5.16/metadata.html)及[对应编译器追加辅助数据的实现](https://github.com/ethereum/solidity/blob/v0.5.16/libsolidity/codegen/Compiler.cpp)。

## 协议计算

协议结果新增 method_models，将源码推导的返回公式、状态输入、执行条件、整数规则和资产操作放在一起。ERC4626 增加预览方法与存入、发行、提现、赎回之间的执行检查要求。金额转换和费用由实际代码推导，具体部署的协议身份、资产单位、完整经济性质及实际执行分别保存状态。

本地样本包括份额与资产换算、费用及虚拟份额偏移，以及 AMM 输入输出金额计算。样本的模型表达式通过独立整数解释器与真实 Anvil 返回值核对，再检查实际入金、赎回到账和角色变化。此类样本是局部执行验收，不计为生态覆盖率。

ERC4626 的预览要求依据 [ERC-4626](https://eips.ethereum.org/EIPS/eip-4626)。角色结构对照已固定的 [OpenZeppelin AccessControl](https://github.com/OpenZeppelin/openzeppelin-contracts/blob/c64a1edb67b6e3f4a15cca8909c9482ad33a02b0/contracts/access/AccessControl.sol)。原开源引擎及依赖版本未升级。

## 工作台和文件

工作台增加角色与状态条件、解析目标地址、依赖状态地址和协议公式。详情弹窗保留所有机器字段；报告新增循环、状态条件、目标代码身份及协议公式，完整 JSON 和 ZIP 使用同一保存结果。地址和哈希完整显示。

动态循环、复杂汇编、状态副作用返回、存储别名、动态外部返回、复杂签名和完整协议经济性质仍可能留下未解析内容。每项状态通过能力清单、方法、公式和 frontier 查询，入口识别与业务验证分别统计。
