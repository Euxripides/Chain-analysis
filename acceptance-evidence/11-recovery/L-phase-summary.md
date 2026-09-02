# 阶段 L 汇总：任务状态与故障恢复（维护窗口内执行，进程 PID 全程记录）

- REC-001 PASS: 多页日志任务 RUNNING 中暂停 → 当前页安全边界收敛（rows 30000→40000 后冻结，req 冻结）→ checkpoint（page_token/page_no）持久化 → 恢复后从 checkpoint 续跑，无重复业务行（FINAL=grain=78171）；at-least-once 物理重写见 BUG-016
- REC-002 PASS: 取消 → CANCEL_REQUESTED → CANCELLED（在途页完成后停止）；已完成 20000 行保留；未完成区间不提交 Coverage（60065001-60100000 无 coverage 行）；2 分钟后无幽灵写入
- REC-003 PASS: Worker 1684126 SIGTERM → 优雅退出（in-flight shard context canceled，2 条 ERROR 可归因）→ 租约+checkpoint 保留（lease_owner=checkpoint）→ 新 Worker 1737823 启动 → 5 分钟租约宽限后 Reaper 收回（retries 0→1）→ 从 page 7 续跑至 COMPLETED（78,171 行，无翻倍）
- REC-004 PASS: Worker SIGKILL → 重启 1748576 → 租约到期 Reaper 收回 → 从 checkpoint 续跑 COMPLETED（33,571 行，physical=FINAL=grain 无任何重复）；Coverage 只含成功提交部分 [60100001,60120000]
- REC-005 PASS: API 1684125 SIGTERM → 重启 1748910 → health ready 即时恢复；下载不受影响（Worker 独立）；任务列表/详情正常；SSE 新连接立即收到当前快照（首帧即进度数据）
- REC-006 PASS: NATS 停 48s → 任务创建/执行/状态不受影响（PG 为事实源）；事件发布降级为 WARN（337 条 nats publish failed 可归因）；恢复后事件可见；任务无丢失。观察：/health/ready 在中断期仍显示 nats=ok（就绪探测未实时反映，P3）
- REC-007 部分执行: 真实 Provider 异常注入需受控代理/维护配置；本次以真实错误佐证（upstream/method_unsupported/timeout 分类、retry 上限、审计）；Go 单测（page_policy_test/errors_test/client_test 等）覆盖 429 降速/超时降页/最小页失败拆 Range 逻辑，AUTO-001 全绿
