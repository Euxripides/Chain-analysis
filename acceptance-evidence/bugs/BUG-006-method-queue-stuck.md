# BUG-006：method_resolve_queue 68 条 UNRESOLVED 停留超 24 小时

- 严重等级：P2
- 状态：新建（未见恢复机制）
- 发现时间：2026-09-02 21:17 +07:00
- 环境：VM 192.168.31.152，commit 62a6d98
- 影响模块：Method/ABI 解析队列
- 影响范围：68 条队列项自 2026-09-01 14:12~14:19 进入后一直 UNRESOLVED（>24h），未观察到 worker 重试或重排；相关交易 Method 长期无法解析
- 前置条件：队列项解析失败（公共签名库或 RPC 失败）
- Job ID：不适用

### 复现步骤

1. SELECT status, count(*), min/max(created_at) FROM method_resolve_queue → UNRESOLVED=68，created_at 2026-09-01 14:12~14:19。
2. 观察 24h 后无变化（09-02 21:17 复核仍 68 条）。

### 实际结果

队列滞留。

### 预期结果

失败项按退避策略重试、达上限后标记终态或告警。

### 证据

- PostgreSQL 查询：08-method-abi/queue_age.sh 输出
- 数据影响：无
