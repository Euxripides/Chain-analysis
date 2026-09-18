# ADR-020：commit gate 请求内的审计写入加入请求事务

| 项 | 值 |
|---|---|
| 状态 | Accepted（2026-09-16） |
| 关联 | AD-004 审计哈希链、AD-005 append-only、ADR-016 P1-A/AUTH-007、internal/dbtx、internal/api/scopecommit_gate.go |
| 计划条目 | §6.5、§6.6.5、§27 AUDIT-004、§28.6 |
| 触发缺陷 | D-15（ROUND-20260916-defect-closure.md）：evidence verify 固定 60.0 s 且丢失一条审计 |

## 背景

P1-A 提交门（commit gate）把**整个 handler** 放进一个事务：进入时读一次作用域令牌，
handler 的每一次 PG 写入都通过 `internal/dbtx` 加入这个事务，释放时用 `FOR UPDATE` 重读会话行比较令牌，
未变则提交，变了则整体回滚并返回 409。这样作用域变更（切工作区、登出、强制下线）不必排在
整个 handler 后面（AUTH-007 要求 5 s 内生效）。

设计文档里有一条重要边界（`internal/dbtx/txpool.go`、`internal/api/scopecommit_gate.go`、`api.emitAudit`）：
**审计写入永不加入这个事务**，理由是

1. 业务回滚时，拒绝行必须留下；
2. 审计链头 `audit_chain_heads` 不能被 handler 持有一整个请求。

第 1 条对**中间件**审计成立：`emitAudit` 用 `dbtx.WithoutTx` 显式剥离事务后再写。
第 2 条从一开始就不成立，因为**服务层**的同事务审计是刻意加入事务的：
`casework.auditTx` 把 `tx` 交给 `audit.Writer.WriteTx`，再由 `ChainAppend` 执行
`SELECT last_seq, last_hash FROM audit_chain_heads WHERE chain=$1 FOR UPDATE` —— 行锁按定义持有到提交，
而在 gate 语境下「提交」发生在 handler 返回之后。

## 问题（2026-09-16 实测）

`POST /api/v2/evidence/{id}/commands/verify`：

1. handler 先写同事务审计 `evidence:review` → gate 事务持有链头行锁；
2. handler 随后调用 `GetEvidence`，其中 `evidence:sensitive:read` 走 `audit.Writer.Write`（raw pool、另开一条连接）→ 同一个链头行锁；
3. 请求要等这条审计写完才返回，gate 事务却要等请求返回才提交 —— **请求与自己的事务互等**；
4. 60.0 s 后 `middleware.Timeout(60s)` 取消请求上下文把它解开：响应是 200，但 `evidence:sensitive:read` 这条审计被静默丢弃，
   日志里多出一条 `superfluous response.WriteHeader call`。

pg_stat_activity 现场（可复算）：`bpgate:<request_id>` 连接停在 `idle in transaction`，
正在等待链头的 `SELECT last_seq … FOR UPDATE` 的 `pg_blocking_pids` 正是它自己那条请求事务。

## 决策

1. **gate 请求内，审计写入加入请求事务。**
   `dbtx.WithGate(ctx, requestID)` 标记 gate 请求上下文；`audit.Writer.Write` 只要看到
   「有请求事务 **且** 是 gate 请求」就走 `WriteTx`，不再开第二条连接。同一事务、同一把锁，没有互等。
2. **`dbtx.WithoutTx` 同时清除 gate 标记。** 中间件审计（`emitAudit`）与 detached 审计
   （`auditRequestDetached`）保持原语义：拒绝行不随业务回滚消失。
3. **读路径审计失败不再静默。** 新增 `audit.Writer.WriteOrLog`，`casework` 的读路径审计
   （`evidence:sensitive:read`、`report:download` 等）改用它。
4. **诚实记录代价。** handler 一旦写了同事务审计，链头行锁就持有到 gate 提交，即「handler 剩余时间」。
   因此同事务审计应尽量靠近 handler 末尾；长 handler（导出、批量导入）不得在写审计之后再做事。
   这一条写进 `scopecommit_gate.go` 的边界说明，不再声称「链头永不被 handler 持有」。

## 被否决的方案

| 方案 | 否决理由 |
|---|---|
| gate 不再包住 handler（回到入口锁） | 直接违反 AUTH-007：作用域变更重新排在整个 handler 后面（实测 12 s handler → 11 s 排队） |
| 审计改为提交后再写（outbox / 延迟队列） | 破坏「审计与业务同事务、审计失败即回滚」（fail closed），并新增一套持久化队列及其故障模式 |
| 保留第二条连接，只加超时 | 只把 60 s 变成更短的失败时间，审计行照样丢；可用性缺陷留在原地 |
| 链头锁改成语句级（不跨语句） | 哈希链要求先读 `prev_hash` 再写新哈希，锁必须跨语句；AD-004 已确定 `FOR UPDATE` 串行化 |
| 给链头加 `lock_timeout` | 把并发竞争变成失败，且不解决「自己等自己」 |

## 影响

- **正向**：请求内不再自死锁；被丢弃的审计行恢复记录；`verify` 由 60.011 s 降到 0.046 s
  （同一夹具、同一入口，见 `ROUND-20260916-defect-closure.md` §2.2）；不再产生 `superfluous WriteHeader`。
- **约束**：链头行锁的持有时间 = 写同事务审计之后剩余的 handler 时间。这是本决策接受的真实代价，
  用「审计靠后写」约束控制，并由提交门的慢提交告警（`scopeCommitGateSlowDefaultMS`）观测。
- **不影响**：中间件拒绝行、detached 审计、非 gate 请求的审计（仍各自独立短事务）。

## 回滚方式

删除 `audit.Writer.Write` 中的 gate 加入分支（一处 `if`），并去掉 `dbtx.WithGate` 的调用点，
即回到「审计永远独立事务」。无迁移、无数据变化、无需要回滚的写入。
回归测试 `internal/audit/writer_gate_test.go` 与
`internal/dbtx/txpool_test.go::TestGateMarkerIsSetAndStrippedWithTheTransaction`
会在回滚发生时立刻失败（已用「临时关闭 join 分支」验证过它们会红）。
