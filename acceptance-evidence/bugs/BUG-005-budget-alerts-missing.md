# BUG-005：预算超阈值告警未实现；预算无 DELETE API

- 严重等级：P2
- 状态：可复现（代码级确认无生成路径）
- 发现时间：2026-09-02 21:31 +07:00
- 环境：VM 192.168.31.152，commit 62a6d98
- 影响模块：API 控制中心（api_control_budgets / api_control_alerts）
- 影响范围：CTL-006 预算告警链：预算创建与 utilization 计算正确，但超 warn_percent 后无任何告警生成（worker 无预算告警评估循环，代码检索无 api_control_alerts 写入路径）；alerts 表仅记录状态机告警
- 前置条件：存在预算且 used_usd/limit 超过 warn_percent
- 完整测试地址：不适用
- Job ID：不适用（预算 id 640d84a2-11ce-4398-92b1-305d1e553bf6，已删除）

### 复现步骤

1. POST /api-control/budgets 创建 limit_usd=1e-7、warn_percent=1 的预算（utilization=372500%）。
2. 等待 40s+，GET /api-control/alerts → 无对应告警。
3. GET /api-control/budgets 无删除端点（只能 SQL 删除）。

### 实际结果

无预算告警；测试预算最终通过 SQL 删除恢复原状。

### 预期结果

超阈值生成 WARN/CRITICAL 告警，支持静默/解决，且提供删除入口。

### 证据

- 请求与响应：10-api-control/ctl006_cleanup.sh 输出
- PostgreSQL 查询：api_control_budgets / api_control_alerts
- 数据影响：无（已恢复）

### 临时绕行方式

无（功能缺失，需实现告警评估循环）。
