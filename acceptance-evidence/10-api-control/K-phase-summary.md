# 阶段 K 汇总：状态总览与 API 控制中心

- CTL-001 PASS: 同窗口三接口 + PG provider_usage 交叉核对：logical_calls 1178=1178、provider_calls 585=585、errors 8=8（状态列实际为 ok/error/cache_hit，方案 SQL 措辞 status!='success' 需按实际 'ok' 理解）、retries 4=4、cache_hits 9=9、credits 371800=371800、cost 0.03718003=0.03718003
- CTL-002 PASS: 状态总览与 API 控制中心同一 24h 指标与官方余额（api-control.spec:116 断言通过，Edge/Firefox；页面区分“本地估算/官方数据”）
- CTL-003 PASS: Ankr Management MCP 已连接（configured/connected/official_available=true，plan=GREEN，remaining_credits=192,245,400，balance_usd=19.22454，source="Ankr Management MCP"，synced_at 实时）；未把本地估算冒充官方值（overview.scope 明确标注口径）；bearer 未出现在 API/日志/HTML/localStorage/DB 明文（AES-GCM 加密存储）
- CTL-004 PASS: 短 TTL 人工覆盖（PROTECTED/1min）→ 立即生效 + 中文状态 + background_paused → TTL 到期自动恢复 NORMAL（manual 字段清空）；api_control_actions 留痕（manual_override + state_transition 双记录，actor/reason/时间齐全）
- CTL-005 PASS: kill_switch 启用 → 状态 PROTECTED，后台任务进入保护（新任务 QUEUED、Provider 请求 0 增长）；解除 → NORMAL，任务恢复并 COMPLETED；启用/解除均有 audit
- CTL-006 部分通过: 预算创建/利用计算正确（utilization=372500% 显示）；超阈值告警未生成（BUG-005）；mute/resolve API 存在（代码级）但无可触发的活动告警实测；测试预算已删除恢复
- CTL-007 PASS: 状态选项全中文（观察/降级/隔离/保护/恢复中/正常 + 停机开关说明），页面无内嵌警告条/说明卡（api-control.spec 断言 .ac-warning=0、无横幅文本），提示均通过弹窗
- 自动保护状态机实测: 测试负载下自动 DEGRADED→OBSERVE→NORMAL 往返并自愈（22:34→22:39），过程留痕可解释；误判根因见 BUG-013
