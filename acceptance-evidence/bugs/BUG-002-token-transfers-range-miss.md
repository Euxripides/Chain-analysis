# BUG-002：token_transfers 下载对 from 侧活跃地址未达区块范围即静默完成（数据遗漏）

- 严重等级：P1
- 状态：已修复并复验通过（commit bb719e2 根因修复 + b0b1e79 守卫；见 final-report.md §11）
- 发现时间：2026-09-02 21:27 +07:00
- 环境：VM 192.168.31.152，commit 62a6d98，ANKR_MODE=live，BSC
- 影响模块：下载执行器 / Ankr Provider 分页与范围校验
- 影响范围：对转账活跃地址（单页 10000 条不足覆盖）请求历史区间时，Ankr 返回最新页、未达目标区块即 HasMore=false，平台以 0 行 COMPLETED 结束，目标区间真实转账被遗漏且无任何警告/拆分/重试
- 前置条件：subject 在目标区间之外有大量转账（如活跃地址 0x6d7d33ab0d492acc7122e0918df54e2dcf35fb28）
- 完整测试地址：0x6d7d33ab0d492acc7122e0918df54e2dcf35fb28（from 侧）
- 完整交易哈希：0x4b61b5353106750a10f66d12d61eba904ce0def3c09b5d43aaa4d55700d28e2a（链上真实转账，log_index 216）
- Job ID / Export ID / Request ID / Trace ID：Job dbdf5970-a1d7-40a4-9072-85c78229db3c（DL-20260902-0043）、a87145a1-950c-419c-aeea-14ef13787061（DL-20260902-0045）

### 复现步骤

1. 以 subject=0x6d7d33ab... 创建 token_transfers 任务，range=[114147137,114147137]（链上存在该地址为 from 的转账，见回执 log_index 216）。
2. 等待 COMPLETED：1 次 Provider 请求、0 行。
3. 以相同 subject 创建 range=[119546000,119546500]：1 次请求、0 行。
4. 两次下载的响应字节数完全相同（5,817,731 B），shard page_state.last_response_bytes 一致 → Ankr 返回同一“最新页”，区块过滤未生效。
5. 检查 shard.page_token 游标（block=112361152，index=81）：扫描位置远高于目标 114147137，说明该页数据在目标区间之外被全部过滤，而平台未继续分页即完成。

### 实际结果

- 目标区间真实转账（tx 0x4b61b535...）未写入该地址的数据；任务显示 COMPLETED、无 error、无重试、无拆分。
- 该行仅因对端地址（0xb3f18f75...）的下载才存在于 ClickHouse。

### 预期结果

- 分页必须推进到覆盖目标区块范围后才可 COMPLETED；未达范围应报错/拆分 Range/重试，绝不应静默 0 行完成。
- 或者平台侧先按区块过滤 Provider 响应并校验最后页位置。

### 证据

- 请求与响应：shard page_state（11-recovery/ 与 02-download/ 查询输出：last_response_bytes=5817731 两次一致）
- PostgreSQL 查询：download_shards.page_token/page_no/downloaded_rows（02-download/paging_check.sh）
- ClickHouse 查询：该行存在于对端 subject 数据（tx+log_index 216 回执复核通过，04-data-quality）
- MinIO/导出文件：manifest.json.gz parts=[]（02-download/peer3_manifest.sh）
- API/Worker 日志：无 WARN/ERROR（静默）
- 链上回执：/api/v1/transactions/bsc/0x4b61b53531.../receipt log_index=216 topics 确认 from=0x6d7d33ab

### 数据影响判断

- 是否写入错误 Coverage：否（0 行不提交 Coverage）
- 是否产生业务重复：否
- 是否存在数据遗漏：是（目标区间转账未下载，任务却报告 COMPLETED）
- 是否泄露 Secret：否
- 是否需要停止相关任务：建议排查期间避免对活跃地址创建历史区间下载任务

### 临时绕行方式

无（需修复分页/范围校验逻辑；可考虑以对端地址为 subject 交叉补数）。