# AD-003：`resource:action` 权限键与强制路由声明

- 状态：Accepted
- 日期：2026-09-12
- 关联：§4.2、§28.5 验收、§32 约束 3

## 决策

1. 权限键统一为 `resource:action`（如 `case:create`、`admin:provider:update`）。
2. 权限目录有**两份必须一致的定义**：`internal/policy/catalog.go`（代码侧，含风险等级与敏感标记）
   与 `migrations/postgres/0041_rbac.sql`（数据库侧，供角色绑定外键引用）。
   单元测试 `TestCatalogMatchesMigrationSeed` 逐字段比对，任何单侧修改都会失败。
3. 每个新路由必须通过 `s.require("<permission>", handler)` 挂载；查不到目录条目的权限一律拒绝（fail closed），
   未声明权限的端点不允许合并。
4. 后台权限（`admin:` 前缀）额外要求 `audience == "admin"`；用户层会话即使被误授权限也无法调用。
5. 未实现的能力必须显式返回 501 + `NOT_IMPLEMENTED`，不得用存在页面或 HTTP 200 表述为完成。

## 影响

- 新增权限需要同时改两处 + 跑测试；这是刻意的摩擦，用来防止"代码授权但数据库没有角色能绑定"。
- 权限目录当前 163 条，覆盖率见 `docs/implementation/current-surface-inventory.md`。
