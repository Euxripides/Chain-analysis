# BUG-013：Worker 重启启动探测产生错误率尖峰，引发自动保护误判 DEGRADED

- 严重等级：P3
- 状态：可复现（重启即触发）
- 发现时间：2026-09-02 23:34 +07:00
- 环境：VM 192.168.31.152
- 影响模块：Worker 启动探测 / 自动保护状态机
- 影响范围：每次 Worker 重启后 eth_getBalance 探测（archive state 不可用/无节点）注入 10~26 条 error 到 provider_usage，5 分钟窗口错误率升至 12.5% → 自动保护进入 DEGRADED/OBSERVE（本次约 5 分钟后自行恢复 NORMAL）。健康探测类失败应不计入业务错误率
- 复现步骤：重启 worker（如 REC-003/004 操作）→ 观察 /api-control/control/states 与 provider_usage 错误行
- 实际结果：状态机往返 DEGRADED→OBSERVE→NORMAL（恢复功能正常），但存在误判
- 预期结果：探测流量不污染业务错误率指标
- 证据：10-api-control/error_spike.sh；11-recovery 各阶段日志
- 数据影响：无（状态自动恢复）
