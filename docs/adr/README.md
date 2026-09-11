# 架构决策记录（ADR）

依据 `docs/dual-system-development-plan.md` §15「不可变架构决策」与 §28.3 `S1-ARCH-01`。

规则：

1. 任何超出双系统计划范围的架构变化，必须先新增 ADR 再改代码。
2. ADR 一经 `Accepted` 不修改正文；变更通过新增 ADR 或追加 `## 修订` 段落说明。
3. 每个 ADR 必须写明：决策、背景、被否决的方案、影响、回滚方式。

| ID | 标题 | 状态 | 对应计划条目 |
|---|---|---|---|
| [AD-001](AD-001-two-frontends-one-backend.md) | 两套前端、一个模块化单体后端 | Accepted | §1、§6.1、§6.2 |
| [AD-002](AD-002-identity-is-server-derived.md) | 身份只能由服务端会话推导 | Accepted | §6.3、§28.4 |
| [AD-003](AD-003-permission-naming-and-declaration.md) | `resource:action` 权限键与强制路由声明 | Accepted | §4.2、§28.5 |
| [AD-004](AD-004-audit-hash-chain.md) | 审计与登录事件使用可复算的顺序哈希链 | Accepted | §6.5、§6.6.5、§27 AUDIT-004 |
| [AD-005](AD-005-append-only-enforcement.md) | 审计表通过数据库触发器强制 append-only | Accepted | §6.6.5 |
| [AD-006](AD-006-migration-and-rollback-strategy.md) | 增量迁移与 expand-compatible 发布 | Accepted | §29.1 |
| [AD-007](AD-007-identity-id-reuses-legacy-user-id.md) | identities.id 复用既有 users.id | Accepted | §6.4、§28.2 |
| [AD-008](AD-008-gray-release-paths.md) | 灰度路径 `/app-v2` 与 `/admin` | Accepted | §29.3、§28.9 |
| [AD-009](AD-009-dialog-only-notifications.md) | 提示信息只允许通过弹窗呈现 | Accepted | AGENTS.md、§7.3 |
| [AD-010](AD-010-frontend-workspace-via-aliases.md) | 未启用 pnpm workspace 链接的过渡方案 | Accepted (临时) | §6.1、§28.3 |
