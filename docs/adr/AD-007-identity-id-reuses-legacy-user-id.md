# AD-007：`identities.id` 复用既有 `users.id`

- 状态：Accepted
- 日期：2026-09-12
- 关联：§6.4、§28.2「users.role → membership/role binding」

## 背景

既有表 `download_jobs.created_by`、`investigations.created_by`、`audit_events.user_id` 等
都以 `users(id)` 为外键。如果新身份体系另起一套 UUID，迁移期需要同时维护两套 ID 映射，
任何遗漏都会造成"任务查不到创建人"这类静默数据问题。

## 决策

1. `identities.id` 在回填时**直接沿用** `users.id`，并额外保留 `legacy_user_id` 指向原行用于核对与回滚。
2. `users.role` 只作为迁移来源，回填为 `organization_memberships` + `member_role_bindings`；
   运行时授权事实来源是 `member_role_bindings`（通过 `policy.Engine`）。
3. `users` 表与 `users.role` 列本批次**不删除**，用于新旧对照；下线需要单独 ADR。

## 影响

- 迁移是幂等的（`WHERE NOT EXISTS` / `ON CONFLICT DO NOTHING`），重复执行安全。
- 未来若要更换身份 ID 体系，需要一次性重写所有外键，届时必须新增 ADR。
