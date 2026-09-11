# AD-005：审计表通过数据库触发器强制 append-only

- 状态：Accepted
- 日期：2026-09-12
- 关联：§6.6.5、§27 AUDIT-003

## 背景

计划要求"应用运行账号无 UPDATE/DELETE 权限"。但当前部署里应用账号 `blockchain` 就是这些表的 owner，
PostgreSQL 中 owner 会绕过 `REVOKE`，因此单纯 REVOKE 只是形式上的安全。

## 决策

`0043_audit_append_only.sql` 用 `BEFORE UPDATE OR DELETE` 触发器强制：

- `audit_events_v2` / `login_events`：拒绝 UPDATE 与 DELETE；
- `audit_chain_heads`：拒绝 DELETE，并禁止 `last_seq` 回退或"同序号换哈希"。

维护逃生舱为事务级开关 `SET LOCAL app.audit_maintenance = 'on'`，只能由持有数据库凭据的受控流程设置，
且必须走 `admin:deletion:approve` 审批。

## 实测结果（灰度实例）

```text
UPDATE audit_events_v2 ...  -> ERROR: audit table audit_events_v2 is append-only
DELETE FROM audit_events_v2 -> ERROR: audit table audit_events_v2 is append-only
UPDATE login_events ...     -> ERROR: audit table login_events is append-only
UPDATE audit_chain_heads SET last_seq=0 -> ERROR: last_seq must not decrease (2 -> 0)
UPDATE audit_chain_heads SET last_hash='deadbeef' -> ERROR: head hash must not be replaced
```

## 后续

独立的非 owner 应用角色（S7-SEC）仍需完成，以形成"最小权限 + 触发器"的双层控制；
在此之前本触发器是主要防线。
