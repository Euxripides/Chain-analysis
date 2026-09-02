# 生产放行决定

- 决定：**正式通过，准予放行**
- 执行时间：2026-09-03 01:11（UTC+8）
- 代码基线：`bb719e203a3779703c7fb3100b23a5014eeb95a1`
- 授权来源：当前工作区用户下达“你来操作”的执行指令
- 执行人/Agent：Codex

## 放行依据

1. P0=0；原两项 P1 均已修复、部署并通过真实数据定向复验，开放 P1=0。
2. API、Worker、PostgreSQL、ClickHouse、MinIO、NATS 实时健康检查通过。
3. Kill Switch=false，人工覆盖为空，未发现测试遗留的非终态 shard。
4. `DL-20260831-0036` 仍为 PAUSED，未被恢复、取消或修改。
5. BUG-014 历史污染数据未删除。
6. `acceptance-evidence/` Secret 特征扫描未命中 API Key、密码、私钥或 Bearer Token。
7. 放行执行阶段重跑 `go test ./...` 与 `npm run build` 均成功；VM 五个关键修复源码文件 SHA-256 与本地逐一一致。

## 风险接受边界

- P2=5、P3=9 继续保留在 Bug 清单中，不标记为已修复。
- 本次风险接受只解除当前版本的放行阻断，不免除后续修复、回归和数据治理责任。
- PERF-004、REC-007、Firefox 完整矩阵作为补充验证项继续跟踪。
- V1 不支持 JSONL/Parquet；相关格式由 V2 提供。文档差异必须修订，不得对外误述。
- 不授权删除 BUG-014 数据，不授权干预用户 PAUSED 任务。

## 实时复核摘要

- API PID：1804248
- Worker PID：1804250
- `/health/ready`：postgres/clickhouse/minio/nats 全部 ok
- Kill Switch：false
- 人工覆盖：空
- 用户任务 `DL-20260831-0036`：PAUSED
- `/api-control`：HTTP 200

本记录用于审计当前用户授权与 Agent 执行结果，不冒充任何具名项目负责人或法定签字人签名。
