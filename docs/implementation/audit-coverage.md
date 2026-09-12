# 审计覆盖报告

> 由 `python3 scripts/gen_audit_coverage.py` 从代码生成，不手工维护。
> 依据：docs/dual-system-development-plan.md §6.5、§6.6.1、§27.1 AUDIT-001。

## 1. 认证与登录安全事件

每一次登录尝试（成功与失败）都写入 `login_events`，并进入 append-only 的 `login` 哈希链。

| 事件类型 | 是否会产生 |
|---|---|
| `auth.device.trusted` | **否**（常量已定义但无调用点，功能未实现） |
| `auth.login.blocked` | 是 |
| `auth.login.denied` | 是 |
| `auth.login.failure` | 是 |
| `auth.login.success` | 是 |
| `auth.logout` | 是 |
| `auth.mfa.failure` | **否**（常量已定义但无调用点，功能未实现） |
| `auth.mfa.recovery_used` | **否**（常量已定义但无调用点，功能未实现） |
| `auth.mfa.success` | **否**（常量已定义但无调用点，功能未实现） |
| `auth.password.changed` | 是 |
| `auth.password.reset` | 是 |
| `auth.session.expired` | **否**（常量已定义但无调用点，功能未实现） |
| `auth.session.forced_offline` | 是 |
| `auth.session.refresh` | 是 |
| `auth.session.revoked` | 是 |
| `auth.session.touch` | **否**（常量已定义但无调用点，功能未实现） |

共定义 16 个事件常量，其中 **10** 个在当前代码中真的会被写入；其余 6 个属于尚未实现的能力（如 MFA），不得据此认为已支持。

## 2. 业务与管理写操作

写操作通过 `declaredPermission` 判定是否审计；敏感读取即使只是 GET 也会审计。

| 权限键 | 端点 |
|---|---|
| `admin:audit:export` | `POST /api/admin/v1/audit/export` |
| `admin:audit:read` | `GET /api/admin/v1/audit/events`, `GET /api/admin/v1/audit/events/{id}` |
| `admin:audit:verify` | `GET /api/admin/v1/audit/chains`, `POST /api/admin/v1/audit/verify` |
| `admin:dashboard:read` | `GET /api/admin/v1/overview` |
| `admin:job:control` | `POST /api/admin/v1/jobs/{id}/commands/{command}` |
| `admin:job:list` | `GET /api/admin/v1/jobs` |
| `admin:job:read` | `GET /api/admin/v1/jobs/{id}/diagnostics` |
| `admin:login-event:read` | `GET /api/admin/v1/login-events` |
| `admin:member:manage` | `DELETE /api/admin/v1/users/{id}/roles/{binding}`, `POST /api/admin/v1/users/{id}/roles` |
| `admin:member:read` | `GET /api/admin/v1/members`, `GET /api/admin/v1/users/{id}/roles` |
| `admin:org:manage` | `PATCH /api/admin/v1/organizations/{id}` |
| `admin:org:read` | `GET /api/admin/v1/organizations`, `GET /api/admin/v1/organizations/{id}` |
| `admin:permission:read` | `GET /api/admin/v1/permissions` |
| `admin:role:read` | `GET /api/admin/v1/roles`, `GET /api/admin/v1/roles/{id}/permissions` |
| `admin:session:read` | `GET /api/admin/v1/sessions` |
| `admin:session:revoke` | `POST /api/admin/v1/sessions/{id}/commands/revoke` |
| `admin:system:read` | `GET /api/admin/v1/system/health` |
| `admin:user:disable` | `POST /api/admin/v1/users/{id}/commands/disable`, `POST /api/admin/v1/users/{id}/commands/enable` |
| `admin:user:password:reset` | `POST /api/admin/v1/users/{id}/commands/reset-password` |
| `admin:user:read` | `GET /api/admin/v1/users`, `GET /api/admin/v1/users/{id}` |
| `admin:user:update` | `PATCH /api/admin/v1/users/{id}` |
| `admin:workspace:manage` | `PATCH /api/admin/v1/workspaces/{id}`, `POST /api/admin/v1/workspaces` |
| `admin:workspace:read` | `GET /api/admin/v1/workspaces` |
| `canvas:branch` | `POST /api/v2/canvases/{id}/branches` |
| `canvas:create` | `POST /api/v2/investigations/{id}/canvases` |
| `canvas:list` | `GET /api/v2/investigations/{id}/canvases` |
| `canvas:read` | `GET /api/v2/canvases/{id}` |
| `canvas:snapshot` | `GET /api/v2/canvases/{id}/snapshots` |
| `canvas:snapshot:create` | `POST /api/v2/canvases/{id}/snapshots` |
| `canvas:update` | `PATCH /api/v2/canvases/{id}` |
| `case:activity:read` | `GET /api/v2/cases/{id}/activity` |
| `case:create` | `POST /api/v2/cases` |
| `case:list` | `GET /api/v2/cases` |
| `case:member:list` | `GET /api/v2/cases/{id}/members`, `GET /api/v2/workspace-members` |
| `case:member:manage` | `DELETE /api/v2/cases/{id}/members/{identity_id}`, `POST /api/v2/cases/{id}/members` |
| `case:read` | `GET /api/v2/cases/{id}` |
| `case:state:update` | `POST /api/v2/cases/{id}/commands/{command}` |
| `case:update` | `PATCH /api/v2/cases/{id}` |
| `comment:create` | `POST /api/v2/cases/{id}/comments` |
| `comment:list` | `GET /api/v2/cases/{id}/comments` |
| `dashboard:read` | `GET /api/v2/dashboard` |
| `download:create` | `GET /api/v2/meta/datasets`, `POST /api/v2/downloads`, `POST /api/v2/downloads/preview` |
| `evidence:list` | `GET /api/v2/investigations/{id}/evidence` |
| `evidence:read` | `GET /api/v2/evidence/{id}` |
| `evidence:review` | `POST /api/v2/evidence/{id}/commands/dispute`, `POST /api/v2/evidence/{id}/commands/supersede`, `POST /api/v2/evidence/{id}/commands/verify` |
| `explorer:read` | `GET /api/v2/explorer/addresses/{chain}/{address}/activity`, `GET /api/v2/explorer/addresses/{chain}/{address}/assets`, `GET /api/v2/explorer/addresses/{chain}/{address}/counterparties`, `GET /api/v2/explorer/addresses/{chain}/{address}/flow-profile`, `GET /api/v2/explorer/addresses/{chain}/{address}/overview`, `GET /api/v2/explorer/addresses/{chain}/{address}/semantic`, `GET /api/v2/explorer/transactions/{chain}/{hash}`, `GET /api/v2/explorer/transactions/{chain}/{hash}/logs`, `GET /api/v2/explorer/transactions/{chain}/{hash}/trace`, `GET /api/v2/meta/chains` |
| `export:create` | `POST /api/v2/exports` |
| `export:download` | `GET /api/v2/exports/download/{ticket}`, `POST /api/v2/exports/{id}/download-ticket` |
| `export:list` | `GET /api/v2/exports` |
| `export:read` | `GET /api/v2/exports/{id}` |
| `finding:list` | `GET /api/v2/investigations/{id}/findings` |
| `finding:read` | `GET /api/v2/findings/{id}` |
| `finding:review` | `POST /api/v2/findings/{id}/commands/confirm`, `POST /api/v2/findings/{id}/commands/reject`, `POST /api/v2/findings/{id}/commands/supersede` |
| `finding:update` | `PATCH /api/v2/findings/{id}` |
| `inbox:read` | `GET /api/v2/inbox` |
| `investigation:cancel` | `POST /api/v2/investigations/{id}/commands/cancel` |
| `investigation:create` | `POST /api/v2/investigations`, `POST /api/v2/investigations/preview` |
| `investigation:list` | `GET /api/v2/investigations` |
| `investigation:pause` | `POST /api/v2/investigations/{id}/commands/pause` |
| `investigation:read` | `GET /api/v2/investigations/{id}`, `GET /api/v2/investigations/{id}/runtime` |
| `investigation:resume` | `POST /api/v2/investigations/{id}/commands/resume` |
| `investigation:retry` | `POST /api/v2/investigations/{id}/commands/retry` |
| `investigation:start` | `POST /api/v2/investigations/{id}/commands/start` |
| `investigation:submit_review` | `POST /api/v2/investigations/{id}/commands/submit-review` |
| `job:control` | `POST /api/v2/jobs/{id}/commands/{command}` |
| `job:list:self` | `GET /api/v2/jobs` |
| `job:read` | `GET /api/v2/jobs/{id}`, `GET /api/v2/jobs/{id}/events` |
| `notification:list` | `GET /api/v2/notifications`, `POST /api/v2/notifications/{id}/open` |
| `notification:update` | `POST /api/v2/notifications/read-all`, `POST /api/v2/notifications/{id}/read` |
| `profile:read` | `GET /api/v2/me/preferences`, `GET /api/v2/me/profile` |
| `profile:update:self` | `PATCH /api/v2/me/preferences`, `PATCH /api/v2/me/profile` |
| `report:create` | `POST /api/v2/cases/{id}/reports`, `POST /api/v2/reports/{id}/commands/submit` |
| `report:download` | `GET /api/v2/reports/download/{ticket}`, `POST /api/v2/reports/{id}/download-ticket` |
| `report:list` | `GET /api/v2/cases/{id}/reports`, `GET /api/v2/reports` |
| `report:read` | `GET /api/v2/reports/{id}` |
| `report:review` | `POST /api/v2/reports/{id}/commands/approve`, `POST /api/v2/reports/{id}/commands/archive`, `POST /api/v2/reports/{id}/commands/reject` |
| `report:sign` | `POST /api/v2/reports/{id}/commands/sign` |
| `search:read` | `GET /api/v2/search` |
| `subject:create` | `POST /api/v2/cases/{id}/subjects` |
| `subject:list` | `GET /api/v2/cases/{id}/subjects` |
| `subject:remove` | `DELETE /api/v2/cases/{id}/subjects/{subject_id}` |

### 兼容层兜底

`/api/v1` 的历史写操作没有权限声明，因此单独由 `isLegacyMutation` 兜底审计：
动作名为 `legacy:<METHOD> <path>`，路径中的 UUID 会被归一化为 `{id}`，
避免 action 标签基数爆炸。下载票据路由不重复记录。

## 3. 敏感读取

以下权限在权限目录中标记为 `sensitive=true`，读取这些端点即使使用 GET 也会产生审计事件：

| 权限键 |
|---|
| `admin:apikey:read` |
| `admin:audit:export` |
| `admin:audit:read` |
| `admin:export:list` |
| `admin:export:read` |
| `admin:login-event:read` |
| `admin:request:read` |
| `evidence:download` |
| `evidence:sensitive:read` |
| `export:download` |
| `report:download` |

## 4. 未覆盖的类别（如实记录）

| §6.6.1 类别 | 状态 | 说明 |
|---|---|---|
| 登录安全 | 已覆盖 | 见第 1 节 |
| 授权结果（401/403/越权） | 已覆盖 | `authorization.denied` 与 `authorization.object_denied` |
| 业务操作（案件/调查/证据/图谱/报告/导出） | 部分覆盖 | 新路由按权限键记录；案件与证据端点属 S4/S5，尚未实现 |
| 管理操作（Provider/预算/Kill Switch/配置） | 部分覆盖 | 身份与权限管理已覆盖；Provider/配置类端点属 S6 |
| 页面访问遥测 | NOT_IMPLEMENTED | `user_activity_events` 表已建，**尚无写入方**，前端也未上报；计划在 S4 随案件页面上报 |
| 查询操作（搜索/筛选/分页） | NOT_IMPLEMENTED | 同上，与页面遥测一同实现 |
| IP 情报（国家/ASN/代理标记） | NOT_IMPLEMENTED | `login_events.ip_intel` 字段已预留，未接入情报源 |

## 5. 可按 Trace 还原

`audit_events_v2` 与 `login_events` 都记录 `request_id` / `trace_id`；
HTTP 响应头返回 `X-Request-Id` 与 `X-Trace-Id`，后台审计页支持按 Trace ID 检索。
端到端验证见 `tests/run-iam-e2e.sh` 的 AUDIT-001 用例。
