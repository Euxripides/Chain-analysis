# BUG-008：地址目录非法 cursor 返回 200 空列表而非 400

- 严重等级：P3
- 状态：可复现（实现为 keyset 字符串比较，任意字符串均“合法”）
- 发现时间：2026-09-02 21:10 +07:00
- 环境：VM 192.168.31.152，commit 62a6d98
- 影响模块：GET /data-explorer/addresses
- 影响范围：BRW-001 预期“cursor 非法时返回 400”；实际返回 200 空结果，损坏的 cursor 会静默表现为空列表
- 前置条件：无
- 复现步骤：GET /data-explorer/addresses?cursor=!!!invalid → 200 {"addresses":[],"next_cursor":""}
- 实际结果：200 空列表
- 预期结果：400
- 证据：阶段 E 执行记录（05-explorer）
- 数据影响：无

### 临时绕行方式

前端不持久化损坏 cursor。
