# 阶段 O 汇总：性能与长稳

- PERF-001 PASS（经 SSH 隧道采集，含 ~1ms 网络开销）:
  | 端点 | 冷 P50/P95/P99 | 热 P50/P95/P99 |
  |---|---|---|
  | /health/live | 75/88/88 ms | 61/77/119 ms |
  | 地址目录 limit=20 | 61/63/63 ms | 62/78/94 ms |
  | token_transfers limit=100（FINAL） | 1649/1952/1952 ms | 1567/1911/4734 ms |
  | coverage/stats | 61/63/63 ms | 62/91/115 ms |
  | api-control/overview 24h | 32/51/51 ms | 31/68/120 ms |
  说明: 数据查询 ~1.6s 为 CH FINAL 查询（242 行地址 + 高基数全局表），采集期间 worker 仍在处理任务，热缓存 P99 含并发抖动
- PERF-002 实测: 1 地址小范围 3.3s/1 行；100 地址小范围任务 planning 70ms，收敛 168s（98 次真实 Provider 请求，0.58 req/s）；10 地址与多页日志任务数据见 11-recovery；Provider 探测/规划与真实下载行速已分开记录（log 中 planning job 与页级 page_state 时长）
- PERF-003 部分执行: 100 行页渲染即时（<1s 级别）、过滤/统计由 smoke 用例断言（11.7s 全流程含导出）；大文件导出为流式写 MinIO（writeZipExport 内存稳定）；交互主线程长任务未系统采集（未执行完整 Performance 面板分析）
- PERF-004 未执行: 需 6-24 小时长稳窗口；已提供当前基线快照（API/Worker RSS 无异常、goroutine 未采集、CH part/merge 未采集）；原因：本轮验收会话时间窗不足以覆盖 6 小时以上持续采集
