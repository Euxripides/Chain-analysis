# EVM 条件计算与局部赋值

2026-10-15。执行已批准的 EVM 覆盖扩展方案第 2、3 阶段及用户继续补缺口的指令。以已部署 funds-semantics-4 为基线，使用独立候选目录，不改动原工作区。

本轮补齐只读方法内条件表达式中的局部标量赋值、复合赋值和前后自增减，覆盖旧版 SafeMath 的 require 赋值，以及 &&、||、三元表达式的条件执行。保留成功路径、失败条件、整数宽度、检查溢出和旧版回绕。表达式兄弟节点读写相互影响而 Solidity 未规定求值顺序时，记录具体缺口，不猜测顺序。存储写入、引用别名、外部副作用及动态循环继续使用已有缺口结构。

主要修改 analysis-engines/ir/internal-returns.mjs、ir/dataflow.mjs、source.mjs、capabilities.mjs 与针对性固定源码、本地执行验证；沿用现有方法详情、报告和导出结构。接口、路由、权限和数据库结构保持兼容。路径最多 32 条、每个循环最多 16 次、累计循环步数最多 64，沿用现有资源限制。

验证顺序为源码和金标准、本地 Anvil 执行与回退结果、既有执行回归、隔离 API/MCP/数据库/工件和真实页面。固定旧版 Uniswap SafeMath 原始源码，在优化开启与关闭的编译结果中比较金额与失败域。补充短路未执行分支、赋值前后值、整数边界和未规定求值顺序的负例。

累计外部请求沿用 2000 次、1000000 credits、估算 USD 10 上限；本地执行不连接公共链写入。验收通过后冻结源码、镜像、二进制和资产，只替换四个合约服务，保留旧镜像、环境和旧任务兼容配置，核对在线镜像、工具同步、工件完整性和原业务服务。

依据：Solidity control-structures 的 Order of Evaluation of Expressions、Checked or Unchecked Arithmetic；Uniswap v2-core 的 contracts/libraries/SafeMath.sol。固定源码文件和哈希随验证材料保存。
