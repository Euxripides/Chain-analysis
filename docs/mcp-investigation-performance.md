# MCP 性能与功能优化（2026-10-03）

本轮直接扩展现有 MCP 与服务器 API，保持 102 个工具及 2 个资源模板。接口参数向后兼容，模型版本仍为 investigation-v2.1。没有新增数据库迁移。

## 已实现

### 1. 授权状态与监控的真正 RPC 批量读取

`get_token_allowances` 和 allowance_change 监控使用现有 ChainRead 网关的 `BatchCallContract`。每个 spender 仍是独立的 eth_call，网关继续负责路由、限流、预算、缓存、provider 批次上限和逐项错误。批量减少网络请求开销，不把多个 RPC 方法调用假装成一个计费单位。

所有调用固定在同一个区块。结果保留 owner/spender 对应关系、`allowance_raw` 精确十进制字符串及 unlimited_uint256。查询允许部分结果失败并设置 reads_complete=false；监控遇到任一项失败时不替换旧基线，不制造零余额或零授权变更。

### 2. 精简分析状态与分页

`get_analysis_status`、`get_analysis_results` 新增 `include_metadata`，默认 true。旧调用无需修改。设为 false 时，数据库仅投影 section 统计，不拉取完整结果摘要；响应省略 metadata，保留 `available_sections`。

```json
{
  "analysis_id": "944041bf-5172-4c20-883a-88e58e499dd6",
  "section": "evidence",
  "page_size": 137,
  "include_metadata": false
}
```

结果新增 `returned_records` 与 `has_more`，继续返回完整记录总数和 next_cursor。翻页读取完成时发布的精确 section 总数，避免每一页重复 COUNT(*)。无统计的旧记录保留 SQL 回退；非法、负数、非整数、溢出的统计明确报错，避免浮点四舍五入。

游标签名、analysis/result SHA/section 绑定规则保持不变。精简页与完整页可交替使用；旧游标和跨进程重启游标继续有效。金额字符串、逐笔证据和分页上限不变，没有前 200 名截断。

### 3. 幂等提交在节点或对象存储异常时仍可复用

先按服务器推导的组织、工作区与 idempotency_key 查找任务，校验语义 SHA，再决定是否读取当前节点快照。匹配时返回原任务的实际 status、version、snapshot 和 reused=true，而不是先重新访问 RPC。新任务仍要求对象存储可用。

创建事务在工作区 advisory lock 内再次检查同一键，防止并发创建重复任务。参数变化仍返回 IDEMPOTENCY_CONFLICT；不同工作区不能看到或复用彼此的任务。

### 4. 证据 ZIP 复用

`create_evidence_bundle` 已有已完成任务的证据包时，读取持久化记录并检查对象存在和长度，返回原 bundle_id、SHA、manifest 与 reused=true。缺失对象或长度异常明确报错；不会为同一 ZIP 重复读取全部原始结果、压缩、解压校验及上传。

首次构建仍校验 ZIP，再写入对象存储。等待构建容量后会再次查询是否已有包。`verify_evidence_bundle` 继续做整体 SHA 与逐项大小、CRC、SHA 校验；复用时的存在/长度检查不等价于完整密码学校验。

`get_evidence_bundle` 继续使用已有范围读取实现；本轮未将这项此前已有的优化计为新增。MCP 直接附件与 evidence:// 资源读取保留。

### 5. 过期租约清理的栅栏

已有的三次重试耗尽清理逻辑增加 fence 递增和 lease_owner/lease_until 清空，并限定当前处理器版本。清理幂等，未过期任务不受影响。旧 worker 无法按原栅栏发布或继续心跳；任务可通过 resume_analysis 恢复。

错误码保持 WORKER_RETRY_EXHAUSTED，兼容已有调用方。隔离 PostgreSQL 测试验证过期、重复清理、未过期、旧 worker 发布拒绝与恢复。

### 6. Unicode 监控名称和稳定的传输错误

后端与 MCP schema 都按 Unicode 字符数限制名称长度为 1–200，支持中文与 emoji。后端拒绝无效 UTF-8。

REST POST 请求补齐 Accept 和 User-Agent。网关返回 HTML、null 或缺失字段的 JSON 时，统一报 REST_INVALID_RESPONSE，包含 HTTP 状态，不泄露上游响应内容。8 MiB 响应上限、超时与取消机制保留；写请求不自动重试。

## 实测

对同一台服务器的现生产 API（8080）与隔离候选 API（8089）交替请求。

| 场景 | 原实现 | 本轮实现 | 验证范围 |
|---|---:|---:|---|
| Case B 单条证据页响应大小 | 7,712 B | 精简模式 1,818 B | 减少 76.43%，默认完整模式 7,841 B |
| 单条证据页中位耗时 | 5.828 ms | 完整 4.939 ms；精简 4.688 ms | 每模式 20 次，局域网/本机测量 |
| 重复创建相同 ZIP 中位耗时 | 29.357 ms | 5.310 ms | 每实现 3 次，包 ID/SHA 完全一致 |
| 20 个 spender 首次查询 | 9,327.877 ms | 6,464.474 ms | 每实现仅 1 次，不能视为稳定延时承诺 |

授权查询耗时受 provider 负载和各进程缓存影响。Mock 网关实测 200 次授权方法调用分为 2 个 provider 批请求，并验证了单项失败、顺序、固定区块和 uint256 精度。真实 provider 仍按自身上限拆批，方法调用和计费单位仍按 spender 数量计算。

Case B 的既有 918 条证据完整分页已核对，旧游标可用，页内金额与旧服务一致。既有 ZIP 为 116,190 字节，SHA256 为 `5f8ddbacc345adabf37ee7f0278a542da159c6399608f1abcc085aa411e3fe8d`，整体校验与范围分块校验通过。

## 验收与发布

后端测试、竞态测试、隔离 PostgreSQL 测试及 34 个 MCP 测试已通过。完整候选服务回归也通过：既有余额/历史/聚合/ABI/资金追踪/批量/附件，新增对账、联合归因、任务幂等、监控基线、取消恢复、重启后的旧游标与证据包，均已核对。

本轮新生成的 Case B 分析 ID 为 `7f12b311-c8e2-4603-8537-cc196a7b23a7`：918 条证据、42 条模型边、13 条模型/源/地址记录，种子金额仍为 65,729.7883 USDT，两种模型的守恒与单边上限均通过。对应 ZIP ID 为 `c05d6d5c-8e2c-49b6-bd88-967bfca78569`，116,193 字节，SHA256 为 `0a82a93105e831648441a05bf3d6b7cae9ab06b31d06468640b15fac342e2bb9`。包内容包括分析 ID，因此不同分析的归档 SHA 可以不同。

详细回归与部署状态以本目录中的 next-verification.json、existing-verification.json、extra-verification.json、optimization-verification.json 和 deployment-verification.json 为准。

发布仅更新本轮改动文件和对应构建产物。部署前校验当前来源及运行产物 SHA，确认没有活动导出和分析任务，备份源码/API/MCP dist；失败时恢复。保留服务器已有未提交改动，不重置仓库，不覆盖其他开发工作。

## 调查边界

性能改动不改变归因假设，也不扩张可证明的资金范围。Case B 仍以两笔来源合计 65,729.7883 USDT 为种子，同时保留 FIFO 与比例分摊结果；不能将混币地址其他来源的数百万 USDT 出金归属于这两笔资金。完整性仍由 coverage、frontier、complete 等字段表达。
