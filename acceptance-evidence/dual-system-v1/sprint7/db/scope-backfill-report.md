# S7-DB-01 核对报告（一）：organization/workspace 作用域回填与约束

> 对应计划 `docs/dual-system-development-plan.md` §22.1 中「0047 organization/workspace NOT NULL 回填与约束」的语义。
> 实际迁移编号：**0056_s7_scope_backfill_constraints.sql**（0044–0055 已被 S3/S4/S5/S6 占用，编号顺延见 ADR-016）。
>
> - 执行时间：2026-09-12（UTC+7）
> - 目标库：`blockchain_platform`（生产验收库）
> - 迁移前备份：`acceptance-evidence/dual-system-v1/sprint7/backups/pre-s7-pg-20260912T162853Z.dump`
>   SHA-256 = `646743927dd3d3a4e738cdbb838e7229c6c55d4ac078ceaaf36f68a3ec89f372`
> - 一次性库全量演练：`blockchain_platform_s7_migtest`，0001→0057 全量 + 重放两次均成功（57 条记录）

## 1. 审计口径（先审计，再回填，再约束）

新增可复查函数 `public.s7_scope_audit()`，迁移与验收报告共用同一口径：

```sql
select table_name, rows, org_null, ws_null, ws_without_org from s7_scope_audit();
```

- 覆盖范围：所有含 `organization_id` 列的 public 表（25 张有数据，31 张含该列）。
- `ws_without_org` = 工作区有值但组织为空的**跨作用域不一致**行数（真正需要修复的缺陷）。

### 1.1 迁移前 / 迁移后实测

| 表 | 行数 | org 为空 | ws 为空 | ws 有值但 org 为空（迁移前→后） |
|---|---:|---:|---:|---:|
| audit_events_v2 | 1046 → 1135 | 51 → 61 | 57 → 67 | 0 → 0 |
| login_events | 2241 → 2261 | 966 | 1058 | 0 → 0 |
| feature_flag_evaluations | 33 | 33 | 33 | 0 → 0 |
| roles | 9 | 9（系统角色，设计如此） | — | — |
| 其余 21 张租户表 | — | **0** | **0** | **0 → 0** |

结论：**不存在跨作用域不一致的数据**；存在的 NULL 全部属于「合法为空」的三类语义：

1. `roles.organization_id`：系统内置角色（无组织归属）。
2. `login_events`：认证前失败/被拦截事件（`auth.login.failure` 756 条、`auth.login.blocked` 100 条）在认证成功前
   无法确定组织；另有 110 条会话类事件（`auth.session.revoked` 91、`forced_offline` 11、
   `auth.password.reset` 7、`reuse_detected` 1）由维护/风控路径写入，未携带组织上下文。
3. `audit_events_v2`：51 条全部是 `authorization.denied`（未通过鉴权的请求还没有租户上下文）。

## 2. 为什么这两张表**不做**回填（有代码证据，不是省略）

`organization_id` 参与审计哈希链的规范化载荷，回填即篡改事件内容：

```text
backend/internal/audit/verify.go:72    COALESCE(organization_id::text,''), COALESCE(workspace_id::text,''),   ← audit 链载荷
backend/internal/audit/verify.go:105   "organization_id":        orgID,                                       ← audit 载荷字段
backend/internal/audit/verify.go:139   subject_digest, COALESCE(organization_id::text,''), audience, ...     ← login 链载荷
backend/internal/audit/writer.go:245   "organization_id":        e.OrganizationID,
```

同时 `login_events` 带 append-only 行触发器 `audit_append_only_guard`（0054/0055），
且 ADR-018 规定了不可恢复缺口的**反回填**控制：任何向已封存区间写入行的尝试都会被判定为
`boundary_conflict`。

因此本迁移对这两张表**只加约束、不改数据**，并把「禁止回填」写成可复查的迁移注释。

## 3. 回填执行结果（可推导行 = 0，已实证）

迁移中的回填语句都是「可推导才回填」的保守形式。用当前库直接复核推导条件：

```sql
-- feature_flag_evaluations 组织可由主体用户的活跃成员关系推导的行数
select count(*) from feature_flag_evaluations ffe
  join organization_memberships m on m.identity_id::text = ffe.subject_id
 where ffe.organization_id is null and ffe.subject_type='user' and m.status='active';
-- => 0
-- 票据表可由 exports 推导的行数
select count(*) from export_tickets et join exports e on e.id = et.export_id
 where et.organization_id is null and e.organization_id is not null;
-- => 0
```

即：**本次回填实际影响 0 行**（迁移前两张票据表的 org 空值本就是 0；评估表剩余 NULL 的主体的
`subject_id` 是测试用合成字符串 `gauge-a-…`，不指向任何真实身份，不可推导）。
这一点在报告里如实记录，不把「回填脚本已执行」写成「数据已修正」。

## 4. 追加的约束（NOT VALID + VALIDATE，全部 `convalidated=true`）

| 约束 | 表 | 内容 |
|---|---|---|
| `audit_events_v2_s7_scope_coherence_check` | audit_events_v2 | `workspace_id is null or organization_id is not null` |
| `login_events_s7_scope_coherence_check` | login_events | 同上 |
| `user_activity_events_s7_scope_coherence_check` | user_activity_events | 同上 |
| `notifications_s7_scope_coherence_check` | notifications | 同上 |
| `export_tickets_s7_scope_coherence_check` | export_tickets | 同上 |
| `report_download_tickets_s7_scope_coherence_check` | report_download_tickets | 同上 |
| `feature_flag_evaluations_s7_scope_coherence_check` | feature_flag_evaluations | 同上 |
| `feature_flags_scope_target_check` | feature_flags | 各 `scope_type` 必须有对应目标（见 §5） |
| `feature_flags_ws_implies_org_check` | feature_flags | `scope_workspace_id` 有值必须有 `scope_org_id` |

另外收紧两列为 `NOT NULL`（写入方结构性保证，迁移前空值实测为 0）：

```text
export_tickets.organization_id            NOT NULL
report_download_tickets.organization_id   NOT NULL
```

## 5. 顺带修复的真实缺陷：feature_flags 用户级作用域不可用

审计中发现（S7-DB-01 范围内的作用域缺口，非理论风险）：

- `feature_flags` 只有 `scope_org_id`（FK organizations）与 `scope_workspace_id`（FK workspaces），
  **没有承载目标用户的列**；
- 评估实现 `s6EvaluateFlag` 在 `scope_type='user'` 分支比较的是
  `flag.ScopeOrgID != subjectID`，即拿**组织 UUID** 与**主体用户 ID** 比较，任何真实用户都不可能命中；
- 结果：用户级灰度恒回落默认值（`scope_mismatch`），与 §27 ADMIN-010「只影响目标 scope」不符。

修复内容：

| 层 | 变更 |
|---|---|
| 迁移 0056 | 新增 `feature_flags.scope_user_id uuid REFERENCES users(id)`；新增 `feature_flags_scope_target_check` 与 `feature_flags_ws_implies_org_check` |
| API | `s6Flag`/`s6FlagInput`/`s6FlagSelect`/`scanS6Flag` 增加 `scope_user_id`；创建/更新写库；`workspace` 作用域自动从 `workspaces.organization_id` 补齐组织；`organization`/`workspace`/`user` 三种作用域缺少目标时返回 422 |
| 评估 | `s6EvaluateFlag` 的 `user` 分支改为比较 `scope_user_id`；新增 `s6ResolveEvalScope`，在调用方未给出作用域时按主体用户的活跃成员关系推导（唯一才推导）并落库，使 ADMIN-010 的「只影响目标 scope」可事后核对 |

## 6. 回滚

`backend/migrations/rollback/rollback-0056.sql`：删除新增约束与 `scope_user_id` 列、
撤回两列的 `NOT NULL`。**不回退回填行**（本次为 0 行）。

## 7. 未覆盖 / 已知限制

- `login_events` / `audit_events_v2` 的历史 NULL **保持原样**：回填会破坏哈希链与 ADR-018 边界，
  且这些 NULL 属合法语义。向前的正确性由 §4 的一致性约束 + 后续事件携带组织上下文保证。
- 未启用 PostgreSQL RLS（§22.6 列为「可选第二道防线」）；应用层作用域仍是唯一强制点，
  由 RBAC 负向用例（`SEC/TENANT` 套件）持续覆盖。
