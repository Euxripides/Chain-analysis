# ADR-016：Sprint 4 迁移编号、运行时失败可见性与 OIDC 禁用策略

- 状态：已接受
- 日期：2026-09-12
- 关联：docs/dual-system-development-plan.md §22.1、§28.6、§32；AD-004、AD-006

## 背景

1. 计划 §22.1 原定 `0044` 承载案件协作/审批迁移，但 Sprint 3 已把 `0044` 用于租户作用域收敛，
   `0045/0046` 用于会话令牌族与外键修复。实际最高迁移号已到 `0046`。
2. Sprint 4 的调查兼容服务复用既有 `investigation.Runtime`。实现过程中发现后台分析失败时
   `executeAnalysis` 会静默 return，导致 `investigation_steps` 永久停留在 `running`、
   investigation 永远无法收敛，且错误不可见（真实缺陷：advanced materialize 的 SQL 占位符错误）。
3. 部署环境没有真实 OIDC IdP。AD-004 要求“OIDC 优先、无 IdP 时本地账户同接口”，
   §32 约束 10 要求未实现能力标记 `NOT_IMPLEMENTED`，禁止伪称 live 验收。

## 决策

1. **迁移编号顺延**：Sprint 4 协作/审批迁移使用 `0047_case_collaboration.sql`；
   权限补齐使用 `0048_s4_permission_fix.sql`。计划 §22.1 中预留给 0047/0048 的 scope 回填与索引
   已在 `0044`/`0047` 完成；Sprint 7 若仍需新的回填/分区迁移，从 `0049` 继续编号。
   本决策只调整编号，不改变 §22.1 的语义（协作/审批、追加式、不删除旧列）。
2. **运行时失败必须落库可见**：`investigation.Runtime` 的收尾事务失败时，
   必须把错误写入 `investigation_steps.error`，并把 run 标记 failed、
   investigation 置为 failed + `state.runtime_error`。禁止留下永久 running 的静默失败。
3. **OIDC 适配器与 live 验收分离**：
   - 实现标准 Authorization Code + PKCE 适配器（discovery、state 签名、nonce、
     code_verifier、RS256/JWKS、issuer/audience/exp/nonce 校验）；
   - 采用 httptest 假 IdP 做隔离单元测试，覆盖 AUTH-011 的负向分支；
   - 没有真实 IdP 时，生产/灰度环境 `AUTH_OIDC_ENABLED` 保持 `false`，
     `/api/auth/v1/methods` 返回 `oidc:false`，`/oidc/start`、`/oidc/callback` 返回 501
     `OIDC_NOT_CONFIGURED`；生产环境只接受 https issuer；
   - 不进行身份自动开通：回调只允许已验证邮箱匹配既有 active identity，否则拒绝并写 login event。
   - AUTH-011 的 live IdP 验收保持 `NOT_VERIFIED`，不得用隔离测试冒充 live 通过。

## 后果

- 迁移可重复、可审计；历史编号偏差有据可查。
- runtime 失败可在调查工作台和数据库中直接定位，避免假 running。
- 未来接入真实 IdP 只需配置 `AUTH_OIDC_*` 并补一次 live 验收，不改协议层。
