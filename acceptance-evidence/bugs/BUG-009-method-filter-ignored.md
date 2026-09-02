# BUG-009：数据浏览 token_transfers 下 method 筛选被忽略

- 严重等级：P3
- 状态：可复现
- 发现时间：2026-09-02 21:12 +07:00
- 环境：VM 192.168.31.152，commit 62a6d98
- 影响模块：POST /data-explorer/query
- 影响范围：对 token_transfers 数据集传 method=unknown 返回全部 242 行（筛选未生效），method 语义只对 transactions 有意义时应明确拒绝或文档化
- 复现步骤：POST /data-explorer/query {chain:bsc, addresses:[benchmark], dataset:token_transfers, filters:{method:"unknown"}} → count=242（等于全量）
- 实际结果：筛选被忽略
- 预期结果：报错提示 method 筛选不适用，或返回 0
- 证据：阶段 E 执行记录（05-explorer）
- 数据影响：无
