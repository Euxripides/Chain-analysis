# 当前系统面清单（Current Surface Inventory）

> 生成时间：2026-09-12 23:12 +0800  
> 生成方式：`python3 scripts/generate_surface_inventory.py --with-db`（全部取自仓库真实文件与部署实例）  
> 依据：docs/dual-system-development-plan.md §28.1 第 3 步

本文件是迁移对照基线：新增或修改路由、权限、迁移、页面时都必须同步更新。

## 1. 后端路由

共 256 条，其中双系统新增路由 **110** 条全部声明了权限键（覆盖率 89%）；`/api/v1` 为兼容层，尚未逐条声明（见 §28.2 迁移映射）。

| Method | Path | Permission |
|---|---|---|
| GET | `/api/admin/v1/audit/chains` | `admin:audit:verify` |
| GET | `/api/admin/v1/audit/events` | `admin:audit:read` |
| GET | `/api/admin/v1/audit/events/{id}` | `admin:audit:read` |
| POST | `/api/admin/v1/audit/export` | `admin:audit:export` |
| POST | `/api/admin/v1/audit/verify` | `admin:audit:verify` |
| GET | `/api/admin/v1/jobs` | `admin:job:list` |
| POST | `/api/admin/v1/jobs/{id}/commands/{command}` | `admin:job:control` |
| GET | `/api/admin/v1/jobs/{id}/diagnostics` | `admin:job:read` |
| GET | `/api/admin/v1/login-events` | `admin:login-event:read` |
| GET | `/api/admin/v1/overview` | `admin:dashboard:read` |
| GET | `/api/admin/v1/permissions` | `admin:permission:read` |
| GET | `/api/admin/v1/roles` | `admin:role:read` |
| GET | `/api/admin/v1/roles/{id}/permissions` | `admin:role:read` |
| GET | `/api/admin/v1/sessions` | `admin:session:read` |
| POST | `/api/admin/v1/sessions/{id}/commands/revoke` | `admin:session:revoke` |
| GET | `/api/admin/v1/system/health` | `admin:system:read` |
| GET | `/api/admin/v1/users` | `admin:user:read` |
| GET | `/api/admin/v1/users/{id}` | `admin:user:read` |
| PATCH | `/api/admin/v1/users/{id}` | `admin:user:update` |
| POST | `/api/admin/v1/users/{id}/commands/disable` | `admin:user:disable` |
| POST | `/api/admin/v1/users/{id}/commands/enable` | `admin:user:disable` |
| POST | `/api/admin/v1/users/{id}/commands/reset-password` | `admin:user:password:reset` |
| GET | `/api/auth/v1/devices` | (未声明) |
| PATCH | `/api/auth/v1/devices/{id}` | (未声明) |
| POST | `/api/auth/v1/login` | (未声明) |
| POST | `/api/auth/v1/logout` | (未声明) |
| GET | `/api/auth/v1/methods` | (未声明) |
| POST | `/api/auth/v1/mfa/verify` | (未声明) |
| GET | `/api/auth/v1/oidc/callback` | (未声明) |
| GET | `/api/auth/v1/oidc/start` | (未声明) |
| POST | `/api/auth/v1/password/change` | (未声明) |
| POST | `/api/auth/v1/refresh` | (未声明) |
| GET | `/api/auth/v1/session` | (未声明) |
| GET | `/api/auth/v1/sessions` | (未声明) |
| POST | `/api/auth/v1/sessions/revoke-all` | (未声明) |
| DELETE | `/api/auth/v1/sessions/{id}` | (未声明) |
| GET | `/api/v1/addresses/{address}/activity` | (legacy / 未声明) |
| PATCH | `/api/v1/addresses/{chain}/{address}/metadata` | (legacy / 未声明) |
| GET | `/api/v1/addresses/{chain}/{address}/runtime` | (legacy / 未声明) |
| GET | `/api/v1/admin/ankr/benchmarks` | (legacy / 未声明) |
| POST | `/api/v1/admin/ankr/benchmarks` | (legacy / 未声明) |
| GET | `/api/v1/admin/ankr/benchmarks/{id}` | (legacy / 未声明) |
| GET | `/api/v1/admin/ankr/range-certifications` | (legacy / 未声明) |
| POST | `/api/v1/admin/ankr/range-certifications` | (legacy / 未声明) |
| POST | `/api/v1/admin/contracts/{chain}/{address}/abi` | (legacy / 未声明) |
| GET | `/api/v1/admin/methods/backfill` | (legacy / 未声明) |
| POST | `/api/v1/admin/methods/backfill` | (legacy / 未声明) |
| GET | `/api/v1/admin/methods/backfill/{id}` | (legacy / 未声明) |
| POST | `/api/v1/admin/methods/backfill/{id}/{action}` | (legacy / 未声明) |
| POST | `/api/v1/admin/methods/signatures` | (legacy / 未声明) |
| GET | `/api/v1/admin/planning-decisions` | (legacy / 未声明) |
| GET | `/api/v1/admin/token-metadata/backfill` | (legacy / 未声明) |
| POST | `/api/v1/admin/token-metadata/backfill` | (legacy / 未声明) |
| POST | `/api/v1/admin/tokens/{chain}/{contract}/override` | (legacy / 未声明) |
| POST | `/api/v1/admin/tokens/{chain}/{contract}/resolve` | (legacy / 未声明) |
| GET | `/api/v1/analysis/addresses/{chain}/{address}/advanced` | (legacy / 未声明) |
| POST | `/api/v1/analysis/addresses/{chain}/{address}/advanced/rebuild` | (legacy / 未声明) |
| GET | `/api/v1/analysis/addresses/{chain}/{address}/flow-profile` | (legacy / 未声明) |
| GET | `/api/v1/analysis/addresses/{chain}/{address}/semantic-events` | (legacy / 未声明) |
| POST | `/api/v1/analysis/addresses/{chain}/{address}/semantic-events/rebuild` | (legacy / 未声明) |
| GET | `/api/v1/api-control/alerts` | (legacy / 未声明) |
| POST | `/api/v1/api-control/alerts/{id}/actions` | (legacy / 未声明) |
| GET | `/api/v1/api-control/budgets` | (legacy / 未声明) |
| POST | `/api/v1/api-control/budgets` | (legacy / 未声明) |
| GET | `/api/v1/api-control/control/actions` | (legacy / 未声明) |
| PATCH | `/api/v1/api-control/control/kill-switch` | (legacy / 未声明) |
| POST | `/api/v1/api-control/control/overrides` | (legacy / 未声明) |
| GET | `/api/v1/api-control/control/policies` | (legacy / 未声明) |
| PATCH | `/api/v1/api-control/control/policies` | (legacy / 未声明) |
| GET | `/api/v1/api-control/control/states` | (legacy / 未声明) |
| GET | `/api/v1/api-control/costs` | (legacy / 未声明) |
| GET | `/api/v1/api-control/errors` | (legacy / 未声明) |
| GET | `/api/v1/api-control/operations` | (legacy / 未声明) |
| GET | `/api/v1/api-control/overview` | (legacy / 未声明) |
| GET | `/api/v1/api-control/provider-accounts` | (legacy / 未声明) |
| DELETE | `/api/v1/api-control/provider-accounts/ankr` | (legacy / 未声明) |
| POST | `/api/v1/api-control/provider-accounts/ankr/connect` | (legacy / 未声明) |
| GET | `/api/v1/api-control/provider-accounts/ankr/oauth/callback` | (legacy / 未声明) |
| POST | `/api/v1/api-control/provider-accounts/ankr/sync` | (legacy / 未声明) |
| GET | `/api/v1/api-control/providers` | (legacy / 未声明) |
| GET | `/api/v1/api-control/requests` | (legacy / 未声明) |
| GET | `/api/v1/api-control/requests/{id}` | (legacy / 未声明) |
| GET | `/api/v1/api-control/usage-types` | (legacy / 未声明) |
| GET | `/api/v1/canvases/{id}` | (legacy / 未声明) |
| PATCH | `/api/v1/canvases/{id}` | (legacy / 未声明) |
| POST | `/api/v1/canvases/{id}/branch` | (legacy / 未声明) |
| GET | `/api/v1/canvases/{id}/snapshots` | (legacy / 未声明) |
| POST | `/api/v1/canvases/{id}/snapshots` | (legacy / 未声明) |
| GET | `/api/v1/capabilities` | (legacy / 未声明) |
| GET | `/api/v1/cases` | (legacy / 未声明) |
| POST | `/api/v1/cases` | (legacy / 未声明) |
| DELETE | `/api/v1/cases/{id}` | (legacy / 未声明) |
| GET | `/api/v1/cases/{id}/canvases` | (legacy / 未声明) |
| POST | `/api/v1/cases/{id}/canvases` | (legacy / 未声明) |
| GET | `/api/v1/chains/capabilities` | (legacy / 未声明) |
| GET | `/api/v1/chains/{chain}/head` | (legacy / 未声明) |
| GET | `/api/v1/count-oracle/calibration` | (legacy / 未声明) |
| GET | `/api/v1/count-oracle/observations` | (legacy / 未声明) |
| GET | `/api/v1/count-oracle/providers` | (legacy / 未声明) |
| GET | `/api/v1/count-oracle/resolutions` | (legacy / 未声明) |
| GET | `/api/v1/count-oracle/status` | (legacy / 未声明) |
| GET | `/api/v1/coverage` | (legacy / 未声明) |
| GET | `/api/v1/coverage/stats` | (legacy / 未声明) |
| GET | `/api/v1/data-explorer/addresses` | (legacy / 未声明) |
| POST | `/api/v1/data-explorer/query` | (legacy / 未声明) |
| POST | `/api/v1/datasets/coverage/preview` | (legacy / 未声明) |
| POST | `/api/v1/datasets/ensure` | (legacy / 未声明) |
| GET | `/api/v1/datasets/{dataset}` | (legacy / 未声明) |
| GET | `/api/v1/download-jobs` | (legacy / 未声明) |
| GET | `/api/v1/download-jobs/{id}` | (legacy / 未声明) |
| POST | `/api/v1/download-jobs/{id}/cancel` | (legacy / 未声明) |
| GET | `/api/v1/download-jobs/{id}/events` | (legacy / 未声明) |
| GET | `/api/v1/download-jobs/{id}/guard` | (legacy / 未声明) |
| POST | `/api/v1/download-jobs/{id}/guard/abort` | (legacy / 未声明) |
| POST | `/api/v1/download-jobs/{id}/guard/continue` | (legacy / 未声明) |
| POST | `/api/v1/download-jobs/{id}/pause` | (legacy / 未声明) |
| POST | `/api/v1/download-jobs/{id}/performance-mode` | (legacy / 未声明) |
| POST | `/api/v1/download-jobs/{id}/resume` | (legacy / 未声明) |
| POST | `/api/v1/download-jobs/{id}/subjects/{subjectID}/{action}` | (legacy / 未声明) |
| GET | `/api/v1/download/{ticket}` | (legacy / 未声明) |
| POST | `/api/v1/exports` | (legacy / 未声明) |
| GET | `/api/v1/exports/{id}` | (legacy / 未声明) |
| GET | `/api/v1/exports/{id}/download` | (legacy / 未声明) |
| POST | `/api/v1/exports/{id}/download-ticket` | (legacy / 未声明) |
| GET | `/api/v1/investigations` | (legacy / 未声明) |
| POST | `/api/v1/investigations` | (legacy / 未声明) |
| GET | `/api/v1/investigations/{id}` | (legacy / 未声明) |
| GET | `/api/v1/investigations/{id}/artifacts` | (legacy / 未声明) |
| POST | `/api/v1/investigations/{id}/relationships` | (legacy / 未声明) |
| GET | `/api/v1/investigations/{id}/reports` | (legacy / 未声明) |
| POST | `/api/v1/investigations/{id}/reports` | (legacy / 未声明) |
| GET | `/api/v1/investigations/{id}/runtime` | (legacy / 未声明) |
| GET | `/api/v1/investigations/{id}/subjects` | (legacy / 未声明) |
| POST | `/api/v1/investigations/{id}/subjects` | (legacy / 未声明) |
| POST | `/api/v1/investigations/{id}/{action}` | (legacy / 未声明) |
| GET | `/api/v1/me` | (legacy / 未声明) |
| GET | `/api/v1/methods/enrichment` | (legacy / 未声明) |
| GET | `/api/v1/methods/search` | (legacy / 未声明) |
| GET | `/api/v1/methods/{selector}` | (legacy / 未声明) |
| GET | `/api/v1/providers/ankr/fetch-profile` | (legacy / 未声明) |
| GET | `/api/v1/providers/ankr/fetch-runtime` | (legacy / 未声明) |
| GET | `/api/v1/providers/ankr/global-concurrency` | (legacy / 未声明) |
| POST | `/api/v1/providers/ankr/performance-mode` | (legacy / 未声明) |
| GET | `/api/v1/providers/ankr/production-profile` | (legacy / 未声明) |
| GET | `/api/v1/providers/ankr/ready-queue` | (legacy / 未声明) |
| GET | `/api/v1/providers/ankr/status` | (legacy / 未声明) |
| GET | `/api/v1/providers/ankr/usage` | (legacy / 未声明) |
| GET | `/api/v1/reports/{id}/download` | (legacy / 未声明) |
| GET | `/api/v1/system/architecture` | (legacy / 未声明) |
| GET | `/api/v1/system/chains/{chain}/capabilities` | (legacy / 未声明) |
| GET | `/api/v1/tags` | (legacy / 未声明) |
| POST | `/api/v1/tags` | (legacy / 未声明) |
| DELETE | `/api/v1/tags/{id}` | (legacy / 未声明) |
| GET | `/api/v1/tokens/enrichment` | (legacy / 未声明) |
| GET | `/api/v1/tokens/search` | (legacy / 未声明) |
| GET | `/api/v1/tokens/{chain}/{contract}` | (legacy / 未声明) |
| GET | `/api/v1/tokens/{chain}/{contract}/logo` | (legacy / 未声明) |
| GET | `/api/v1/transactions/{chain}/{tx_hash}` | (legacy / 未声明) |
| GET | `/api/v1/transactions/{chain}/{tx_hash}/internal-calls` | (legacy / 未声明) |
| GET | `/api/v1/transactions/{chain}/{tx_hash}/receipt` | (legacy / 未声明) |
| GET | `/api/v1/transactions/{chain}/{tx_hash}/trace` | (legacy / 未声明) |
| POST | `/api/v1/updates/preview` | (legacy / 未声明) |
| POST | `/api/v1/updates/{id}/confirm` | (legacy / 未声明) |
| GET | `/api/v2/canvases/{id}` | `canvas:read` |
| PATCH | `/api/v2/canvases/{id}` | `canvas:update` |
| POST | `/api/v2/canvases/{id}/branches` | `canvas:branch` |
| GET | `/api/v2/canvases/{id}/snapshots` | `canvas:snapshot` |
| POST | `/api/v2/canvases/{id}/snapshots` | `canvas:snapshot:create` |
| GET | `/api/v2/cases` | `case:list` |
| POST | `/api/v2/cases` | `case:create` |
| GET | `/api/v2/cases/{id}` | `case:read` |
| PATCH | `/api/v2/cases/{id}` | `case:update` |
| GET | `/api/v2/cases/{id}/activity` | `case:activity:read` |
| POST | `/api/v2/cases/{id}/commands/{command}` | `case:state:update` |
| GET | `/api/v2/cases/{id}/comments` | `comment:list` |
| POST | `/api/v2/cases/{id}/comments` | `comment:create` |
| GET | `/api/v2/cases/{id}/members` | `case:member:list` |
| POST | `/api/v2/cases/{id}/members` | `case:member:manage` |
| DELETE | `/api/v2/cases/{id}/members/{identity_id}` | `case:member:manage` |
| GET | `/api/v2/cases/{id}/reports` | `report:list` |
| POST | `/api/v2/cases/{id}/reports` | `report:create` |
| GET | `/api/v2/cases/{id}/subjects` | `subject:list` |
| POST | `/api/v2/cases/{id}/subjects` | `subject:create` |
| DELETE | `/api/v2/cases/{id}/subjects/{subject_id}` | `subject:remove` |
| GET | `/api/v2/dashboard` | `dashboard:read` |
| POST | `/api/v2/downloads` | `download:create` |
| POST | `/api/v2/downloads/preview` | `download:create` |
| GET | `/api/v2/evidence/{id}` | `evidence:read` |
| POST | `/api/v2/evidence/{id}/commands/dispute` | `evidence:review` |
| POST | `/api/v2/evidence/{id}/commands/supersede` | `evidence:review` |
| POST | `/api/v2/evidence/{id}/commands/verify` | `evidence:review` |
| GET | `/api/v2/explorer/addresses/{chain}/{address}/activity` | `explorer:read` |
| GET | `/api/v2/explorer/addresses/{chain}/{address}/assets` | `explorer:read` |
| GET | `/api/v2/explorer/addresses/{chain}/{address}/counterparties` | `explorer:read` |
| GET | `/api/v2/explorer/addresses/{chain}/{address}/flow-profile` | `explorer:read` |
| GET | `/api/v2/explorer/addresses/{chain}/{address}/overview` | `explorer:read` |
| GET | `/api/v2/explorer/addresses/{chain}/{address}/semantic` | `explorer:read` |
| GET | `/api/v2/explorer/transactions/{chain}/{hash}` | `explorer:read` |
| GET | `/api/v2/explorer/transactions/{chain}/{hash}/logs` | `explorer:read` |
| GET | `/api/v2/explorer/transactions/{chain}/{hash}/trace` | `explorer:read` |
| GET | `/api/v2/exports` | `export:list` |
| POST | `/api/v2/exports` | `export:create` |
| GET | `/api/v2/exports/download/{ticket}` | `export:download` |
| GET | `/api/v2/exports/{id}` | `export:read` |
| POST | `/api/v2/exports/{id}/download-ticket` | `export:download` |
| GET | `/api/v2/findings/{id}` | `finding:read` |
| PATCH | `/api/v2/findings/{id}` | `finding:update` |
| POST | `/api/v2/findings/{id}/commands/confirm` | `finding:review` |
| POST | `/api/v2/findings/{id}/commands/reject` | `finding:review` |
| POST | `/api/v2/findings/{id}/commands/supersede` | `finding:review` |
| GET | `/api/v2/inbox` | `inbox:read` |
| GET | `/api/v2/investigations` | `investigation:list` |
| POST | `/api/v2/investigations` | `investigation:create` |
| POST | `/api/v2/investigations/preview` | `investigation:create` |
| GET | `/api/v2/investigations/{id}` | `investigation:read` |
| GET | `/api/v2/investigations/{id}/canvases` | `canvas:list` |
| POST | `/api/v2/investigations/{id}/canvases` | `canvas:create` |
| POST | `/api/v2/investigations/{id}/commands/cancel` | `investigation:cancel` |
| POST | `/api/v2/investigations/{id}/commands/pause` | `investigation:pause` |
| POST | `/api/v2/investigations/{id}/commands/resume` | `investigation:resume` |
| POST | `/api/v2/investigations/{id}/commands/retry` | `investigation:retry` |
| POST | `/api/v2/investigations/{id}/commands/start` | `investigation:start` |
| POST | `/api/v2/investigations/{id}/commands/submit-review` | `investigation:submit_review` |
| GET | `/api/v2/investigations/{id}/evidence` | `evidence:list` |
| GET | `/api/v2/investigations/{id}/findings` | `finding:list` |
| GET | `/api/v2/investigations/{id}/runtime` | `investigation:read` |
| GET | `/api/v2/jobs` | `job:list:self` |
| GET | `/api/v2/jobs/{id}` | `job:read` |
| POST | `/api/v2/jobs/{id}/commands/{command}` | `job:control` |
| GET | `/api/v2/jobs/{id}/events` | `job:read` |
| GET | `/api/v2/me/preferences` | `profile:read` |
| PATCH | `/api/v2/me/preferences` | `profile:update:self` |
| GET | `/api/v2/me/profile` | `profile:read` |
| PATCH | `/api/v2/me/profile` | `profile:update:self` |
| GET | `/api/v2/meta/chains` | `explorer:read` |
| GET | `/api/v2/meta/datasets` | `download:create` |
| GET | `/api/v2/notifications` | `notification:list` |
| POST | `/api/v2/notifications/read-all` | `notification:update` |
| POST | `/api/v2/notifications/{id}/open` | `notification:list` |
| POST | `/api/v2/notifications/{id}/read` | `notification:update` |
| GET | `/api/v2/reports` | `report:list` |
| GET | `/api/v2/reports/download/{ticket}` | `report:download` |
| GET | `/api/v2/reports/{id}` | `report:read` |
| POST | `/api/v2/reports/{id}/commands/approve` | `report:review` |
| POST | `/api/v2/reports/{id}/commands/archive` | `report:review` |
| POST | `/api/v2/reports/{id}/commands/reject` | `report:review` |
| POST | `/api/v2/reports/{id}/commands/sign` | `report:sign` |
| POST | `/api/v2/reports/{id}/commands/submit` | `report:create` |
| POST | `/api/v2/reports/{id}/download-ticket` | `report:download` |
| GET | `/api/v2/search` | `search:read` |
| GET | `/api/v2/workspace-members` | `case:member:list` |
| GET | `/docs` | (legacy / 未声明) |
| GET | `/health/live` | (legacy / 未声明) |
| GET | `/health/ready` | (legacy / 未声明) |
| GET | `/openapi.json` | (legacy / 未声明) |
| GET | `/redoc` | (legacy / 未声明) |

## 2. 权限目录

共 **176** 个权限键，命名规则 `resource:action`（§4.2）。Go 侧 `internal/policy/catalog.go` 与 SQL 侧 `0041_rbac.sql` 由单元测试强校验一致。

| Key | Category | 说明 | Risk | 敏感 |
|---|---|---|---|---|
| `address:read` | explorer | 查看地址详情 | low | 否 |
| `admin:alert:manage` | admin.control | 处置告警 | high | 否 |
| `admin:alert:read` | admin.control | 查看告警 | low | 否 |
| `admin:apikey:create` | admin.security | 创建 API/MCP 密钥 | critical | 否 |
| `admin:apikey:read` | admin.security | 查看 API/MCP 密钥 | high | 是 |
| `admin:apikey:revoke` | admin.security | 吊销 API/MCP 密钥 | critical | 否 |
| `admin:approval:decide` | admin.control | 审批高风险操作 | critical | 否 |
| `admin:approval:read` | admin.control | 查看审批请求 | medium | 否 |
| `admin:audit:export` | admin.security | 导出审计日志 | critical | 是 |
| `admin:audit:read` | admin.security | 查看审计日志 | medium | 是 |
| `admin:audit:verify` | admin.security | 校验审计哈希链 | medium | 否 |
| `admin:backup:create` | admin.system | 创建备份 | high | 否 |
| `admin:backup:read` | admin.system | 查看备份 | medium | 否 |
| `admin:backup:restore` | admin.system | 恢复备份 | critical | 否 |
| `admin:budget:read` | admin.provider | 查看预算 | medium | 否 |
| `admin:budget:update` | admin.provider | 修改预算 | critical | 否 |
| `admin:config:history:read` | admin.system | 查看配置历史 | medium | 否 |
| `admin:config:read` | admin.system | 查看系统配置 | medium | 否 |
| `admin:config:update` | admin.system | 修改系统配置 | critical | 否 |
| `admin:cost:read` | admin.provider | 查看成本 | medium | 否 |
| `admin:coverage:read` | admin.data | 查看 Coverage 与数据质量 | low | 否 |
| `admin:dashboard:read` | admin.ops | 查看运营总览 | low | 否 |
| `admin:dataset:manage` | admin.data | 重建 Dataset | critical | 否 |
| `admin:dataset:read` | admin.data | 查看 Dataset | low | 否 |
| `admin:deletion:approve` | admin.data | 审批删除请求 | critical | 否 |
| `admin:deletion:read` | admin.data | 查看删除请求 | medium | 否 |
| `admin:deletion:request` | admin.data | 发起删除请求 | critical | 否 |
| `admin:export:delete` | admin.data | 删除导出对象 | critical | 否 |
| `admin:export:list` | admin.data | 列出全局导出 | medium | 是 |
| `admin:export:read` | admin.data | 查看全部导出 | medium | 是 |
| `admin:flag:read` | admin.system | 查看功能开关 | low | 否 |
| `admin:flag:update` | admin.system | 修改功能开关 | high | 否 |
| `admin:job:cancel` | admin.data | 取消任意任务 | critical | 否 |
| `admin:job:control` | admin.data | 控制全局任务 | high | 否 |
| `admin:job:list` | admin.data | 列出全局任务 | low | 否 |
| `admin:job:read` | admin.data | 查看全局任务 | low | 否 |
| `admin:job:retry` | admin.data | 重试任意任务 | high | 否 |
| `admin:killswitch:engage` | admin.control | 启用 Kill Switch | critical | 否 |
| `admin:killswitch:read` | admin.control | 查看 Kill Switch | medium | 否 |
| `admin:killswitch:release` | admin.control | 解除 Kill Switch | critical | 否 |
| `admin:login-event:read` | admin.security | 查看登录记录 | medium | 是 |
| `admin:member:manage` | admin.identity | 管理成员关系 | high | 否 |
| `admin:member:read` | admin.identity | 查看成员关系 | medium | 否 |
| `admin:metrics:read` | admin.system | 查看平台指标 | low | 否 |
| `admin:notification-channel:manage` | admin.system | 管理通知渠道 | high | 否 |
| `admin:notification-channel:read` | admin.system | 查看通知渠道 | low | 否 |
| `admin:org:manage` | admin.identity | 管理组织 | high | 否 |
| `admin:org:read` | admin.identity | 查看组织 | low | 否 |
| `admin:performance:read` | admin.provider | 查看性能模式 | low | 否 |
| `admin:performance:update` | admin.provider | 修改性能模式 | high | 否 |
| `admin:permission:read` | admin.identity | 查看权限目录 | low | 否 |
| `admin:provider:key:rotate` | admin.provider | 轮换 Provider 密钥 | critical | 否 |
| `admin:provider:read` | admin.provider | 查看 Provider | low | 否 |
| `admin:provider:update` | admin.provider | 修改 Provider 配置 | high | 否 |
| `admin:request:read` | admin.provider | 查看请求审计 | medium | 是 |
| `admin:retention:manage` | admin.data | 管理保留策略 | critical | 否 |
| `admin:retention:read` | admin.data | 查看保留策略 | low | 否 |
| `admin:risk:manage` | admin.security | 处置风险事件 | high | 否 |
| `admin:risk:read` | admin.security | 查看风险事件 | medium | 否 |
| `admin:role:manage` | admin.identity | 管理角色与权限 | critical | 否 |
| `admin:role:read` | admin.identity | 查看角色 | low | 否 |
| `admin:routing:read` | admin.provider | 查看路由与限流 | low | 否 |
| `admin:routing:update` | admin.provider | 修改路由与限流 | high | 否 |
| `admin:session:read` | admin.security | 查看会话 | medium | 否 |
| `admin:session:revoke` | admin.security | 强制下线会话 | critical | 否 |
| `admin:storage:manage` | admin.data | 管理对象存储 | high | 否 |
| `admin:storage:read` | admin.data | 查看对象存储用量 | low | 否 |
| `admin:system:read` | admin.system | 查看系统健康 | low | 否 |
| `admin:user:create` | admin.identity | 创建用户 | high | 否 |
| `admin:user:disable` | admin.identity | 停用用户 | critical | 否 |
| `admin:user:invite` | admin.identity | 邀请用户 | medium | 否 |
| `admin:user:password:reset` | admin.identity | 重置用户密码 | critical | 否 |
| `admin:user:read` | admin.identity | 查看用户 | medium | 否 |
| `admin:user:update` | admin.identity | 修改用户 | high | 否 |
| `admin:version:manage` | admin.system | 执行版本与迁移操作 | critical | 否 |
| `admin:version:read` | admin.system | 查看版本与迁移 | low | 否 |
| `admin:workspace:manage` | admin.identity | 管理工作区 | high | 否 |
| `admin:workspace:read` | admin.identity | 查看工作区 | low | 否 |
| `canvas:branch` | canvas | 创建图谱分支 | medium | 否 |
| `canvas:create` | canvas | 创建图谱画布 | low | 否 |
| `canvas:delete` | canvas | 删除图谱 | high | 否 |
| `canvas:list` | canvas | 列出图谱画布 | low | 否 |
| `canvas:read` | canvas | 查看图谱 | low | 否 |
| `canvas:snapshot` | canvas | 创建/查看图谱快照 | medium | 否 |
| `canvas:snapshot:create` | canvas | 创建图谱快照 | medium | 否 |
| `canvas:update` | canvas | 编辑图谱 | medium | 否 |
| `case:activity:read` | case | 查看案件动态 | low | 否 |
| `case:archive` | case | 归档/恢复案件 | medium | 否 |
| `case:comment:create` | case | 发表案件评论 | low | 否 |
| `case:create` | case | 创建案件 | low | 否 |
| `case:delete` | case | 删除案件 | high | 否 |
| `case:list` | case | 列出案件 | low | 否 |
| `case:member:list` | case | 查看案件成员 | low | 否 |
| `case:member:manage` | case | 管理案件成员 | high | 否 |
| `case:read` | case | 查看案件 | low | 否 |
| `case:state:update` | case | 案件状态命令 | medium | 否 |
| `case:subject:manage` | case | 管理案件对象 | medium | 否 |
| `case:update` | case | 修改案件 | medium | 否 |
| `comment:create` | case | 发表案件评论 | low | 否 |
| `comment:list` | case | 查看案件评论 | low | 否 |
| `dashboard:read` | dashboard | 查看个人工作台 | low | 否 |
| `download:control` | download | 暂停/继续/取消下载 | medium | 否 |
| `download:create` | download | 创建下载任务 | medium | 否 |
| `download:delete` | download | 删除下载任务数据 | high | 否 |
| `download:preview` | download | 预览 Coverage 与影响 | low | 否 |
| `download:read` | download | 查看下载任务 | low | 否 |
| `download:refresh` | download | 显式刷新并新增 Provider 取数 | high | 否 |
| `download:reuse` | download | 复用已有 Coverage 数据 | low | 否 |
| `evidence:create` | evidence | 添加证据 | low | 否 |
| `evidence:delete` | evidence | 移除证据 | high | 否 |
| `evidence:download` | evidence | 下载证据文件 | medium | 是 |
| `evidence:list` | evidence | 列出证据 | low | 否 |
| `evidence:read` | evidence | 查看证据元数据 | low | 否 |
| `evidence:review` | evidence | 复核证据状态 | high | 否 |
| `evidence:sensitive:read` | evidence | 读取敏感证据正文 | critical | 是 |
| `evidence:snapshot:create` | evidence | 固化证据快照 | medium | 否 |
| `explorer:fetch` | explorer | 触发链上取数 | medium | 否 |
| `explorer:read` | explorer | 查询本地数据 | low | 否 |
| `export:create` | export | 创建导出 | medium | 否 |
| `export:delete` | export | 删除导出 | high | 否 |
| `export:download` | export | 下载导出文件 | medium | 是 |
| `export:list` | export | 列出导出 | low | 否 |
| `export:read` | export | 查看导出 | low | 否 |
| `finding:create` | finding | 新增 Finding | low | 否 |
| `finding:list` | finding | 列出 Finding | low | 否 |
| `finding:read` | finding | 查看 Finding | low | 否 |
| `finding:review` | finding | 复核 Finding | high | 否 |
| `finding:update` | finding | 修改 Finding | medium | 否 |
| `inbox:assign` | notification | 分派待办 | medium | 否 |
| `inbox:read` | notification | 查看待我处理 | low | 否 |
| `investigation:approve` | investigation | 复核调查结论 | high | 否 |
| `investigation:cancel` | investigation | 取消调查 | medium | 否 |
| `investigation:create` | investigation | 创建调查任务 | low | 否 |
| `investigation:delete` | investigation | 删除调查 | high | 否 |
| `investigation:list` | investigation | 列出调查任务 | low | 否 |
| `investigation:pause` | investigation | 暂停调查 | medium | 否 |
| `investigation:read` | investigation | 查看调查任务 | low | 否 |
| `investigation:resume` | investigation | 恢复调查 | medium | 否 |
| `investigation:retry` | investigation | 重试调查 | medium | 否 |
| `investigation:run` | investigation | 启动/暂停/继续调查 | medium | 否 |
| `investigation:start` | investigation | 启动调查 | medium | 否 |
| `investigation:submit_review` | investigation | 提交调查复核 | medium | 否 |
| `investigation:update` | investigation | 修改调查参数 | medium | 否 |
| `job:admin` | job | 全局任务控制 | high | 否 |
| `job:control` | job | 控制任务 | medium | 否 |
| `job:diagnose` | job | 查看任务诊断信息 | medium | 否 |
| `job:list:self` | job | 列出本人任务 | low | 否 |
| `job:list:workspace` | job | 列出工作区内任务 | low | 否 |
| `job:read` | job | 查看任务 | low | 否 |
| `job:retry` | job | 重试任务 | medium | 否 |
| `notification:list` | notification | 列出通知 | low | 否 |
| `notification:manage` | notification | 管理通知偏好 | low | 否 |
| `notification:read` | notification | 查看通知 | low | 否 |
| `notification:update` | notification | 更新通知状态 | low | 否 |
| `preference:update` | self | 修改使用偏好 | low | 否 |
| `profile:read` | self | 查看个人资料 | low | 否 |
| `profile:update` | self | 修改个人资料 | low | 否 |
| `profile:update:self` | self | 修改本人资料 | low | 否 |
| `report:approve` | report | 审批报告 | high | 否 |
| `report:create` | report | 生成报告 | low | 否 |
| `report:delete` | report | 删除报告 | high | 否 |
| `report:download` | report | 下载报告 | medium | 是 |
| `report:list` | report | 列出报告 | low | 否 |
| `report:read` | report | 查看报告 | low | 否 |
| `report:review` | report | 复核报告 | high | 否 |
| `report:sign` | report | 签署报告 | critical | 否 |
| `search:global` | explorer | 使用全局搜索 | low | 否 |
| `search:read` | explorer | 执行全局搜索 | low | 否 |
| `security:update` | self | 修改安全设置 | high | 否 |
| `session:self:revoke` | self | 撤销自己的会话 | medium | 否 |
| `subject:create` | case | 新增案件对象 | medium | 否 |
| `subject:list` | case | 查看案件对象 | low | 否 |
| `subject:remove` | case | 移除案件对象 | high | 否 |
| `subject:update` | case | 修改案件对象 | medium | 否 |
| `token:read` | explorer | 查看 Token 元数据 | low | 否 |
| `transaction:read` | explorer | 查看交易详情 | low | 否 |

## 3. 前端路由

### user-console

```text
*
/
/addresses/:chain/:address
/canvases/:canvasId
/cases
/cases/:caseId/:tab
/cases/new
/downloads
/downloads/new
/explorer
/exports
/graph
/home
/inbox
/investigations
/investigations/:id/evidence/:evidenceId
/investigations/:id/findings
/investigations/:id/workbench
/investigations/new
/jobs
/jobs/:jobId
/login
/notifications
/profile
/reports
/reports/:id
/search
/transactions/:chain/:hash
```

### admin-console

```text
*
/
/audit
/data-quality
/jobs
/jobs/:jobId
/login
/login-events
/organizations
/overview
/risk
/roles
/sessions
/storage
/system
/users
```

## 4. 迁移

### PostgreSQL

```text
0001_init.sql
0002_enterprise_reserved.sql
0003_page_policy.sql
0004_v12_metadata.sql
0005_method_abi.sql
0006_token_metadata.sql
0007_token_metadata_queue.sql
0008_token_metadata_raw_backfill.sql
0009_token_metadata_raw_failures.sql
0010_rpc_infrastructure.sql
0010_token_metadata_rpc_safety.sql
0011_api_control.sql
0012_ankr_management.sql
0013_ankr_engine.sql
0013_ankr_performance_modes.sql
0014_v11_benchmark.sql
0015_v12_count_oracle.sql
0016_count_provider_port.sql
0017_ankr_fetch_v14.sql
0018_benchmark_exclude_reason.sql
0019_v142_runtime_telemetry.sql
0020_ankr_performance_modes.sql
0021_tron_v1.sql
0024_tron_v51_enrichment_metrics.sql
0025_tron_turbo64.sql
0026_usage_cost_backfill.sql
0027_tron_volume_guard.sql
0028_investigation_phase0.sql
0029_investigation_runtime.sql
0030_canvas_phase3.sql
0031_semantic_engine.sql
0032_semantic_projection_state.sql
0033_advanced_investigation.sql
0034_investigation_reports.sql
0035_multi_subject_osint.sql
0036_gpt_actions_api_keys.sql
0037_download_job_idempotency.sql
0038_address_dataset_sync_state.sql
0039_full_download_incremental_idempotency.sql
0040_iam.sql
0041_rbac.sql
0042_audit_v2.sql
0043_audit_append_only.sql
0044_tenant_scope.sql
0045_session_family.sql
0046_session_device_fk.sql
0047_case_collaboration.sql
0048_s4_permission_fix.sql
0049_s5_explorer_download_jobs.sql
0050_s5_job_identity_scope.sql
0051_s6_control_governance.sql
0052_s6_data_governance.sql
0053_s6_permission_grants.sql
0054_s6_append_only_hardening.sql
```

### ClickHouse

```text
0001_init.sql
0002_address_activity.sql
0003_activity_amount_metadata.sql
0004_method_abi.sql
0005_trace.sql
0006_tron_v1.sql
0007_address_query_indexes.sql
```

## 5. 运行单元

```text
deploy/blockchain-admin-console.service
deploy/blockchain-api.service
deploy/blockchain-mcp.service
deploy/blockchain-user-console.service
deploy/blockchain-vite.service
deploy/blockchain-worker.service
```
