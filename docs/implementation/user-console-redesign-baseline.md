# 用户端重构基线记录（蓝图阶段 U0）

生成时间：2026-09（本次重构施工前）
仓库：`blockchain-platform`，分支 `feat/dual-system-iam-foundation`
范围：`frontend/apps/user-console/**`、`frontend/packages/ui/**`、`frontend/packages/design-tokens/**` 及用户端 Playwright 用例。

> 本文只记录**改造前已经存在、且必须继续成立**的公开契约。
> 任何一项在重构后发生变化的，都必须出现在实施记录的"功能行为变化"一节里。

## 1. 仓库状态

- `git status --short` 在开工时为空（工作区干净），不存在会被覆盖的未提交修改。
- 前端技术基线未更换：React 19 + TypeScript 5.8 + Vite 6、React Router 7、Zustand、
  Motion 12.23、XYFlow 12.10、ECharts 5.6、AG Grid 33.3、Lucide 0.468。
- 入口：`frontend/apps/user-console/src/main.tsx`，basename `/app-v2`。

## 2. 路由与权限（重构前后必须一致）

| 路由 | 页面源文件 | 权限 |
|---|---|---|
| `/` | App.tsx | 重定向 `/home` |
| `/login` | pages/LoginPage.tsx | 未登录可见；已登录重定向 `/home` |
| `/home` | pages/HomePage.tsx | `dashboard:read` |
| `/search` | pages/SearchPage.tsx | `search:read` |
| `/cases` | pages/CasesPage.tsx | `case:list` |
| `/cases/new` | pages/CaseNewPage.tsx | `case:create` |
| `/cases/:caseId/:tab` | pages/CaseDetailPage.tsx | `case:read` |
| `/investigations` | pages/InvestigationsPage.tsx | `investigation:list` |
| `/investigations/new` | pages/InvestigationNewPage.tsx | `investigation:create` |
| `/investigations/:id/workbench` | pages/InvestigationWorkbenchPage.tsx | `investigation:read` |
| `/investigations/:id/findings` | pages/FindingsPage.tsx | `finding:list` |
| `/investigations/:id/evidence/:evidenceId` | pages/EvidenceDetailPage.tsx | `evidence:read` |
| `/graph` | pages/CanvasListPage.tsx | `canvas:list` |
| `/canvases/:canvasId` | pages/CanvasPage.tsx | `canvas:read` |
| `/explorer` | pages/ExplorerPage.tsx | `explorer:read` |
| `/addresses/:chain/:address` | pages/AddressDetailPage.tsx | `explorer:read` |
| `/transactions/:chain/:hash` | pages/TransactionDetailPage.tsx | `explorer:read` |
| `/downloads` | App.tsx | 重定向 `/downloads/new` |
| `/downloads/new` | pages/DownloadNewPage.tsx | `download:create` |
| `/jobs` | pages/JobsPage.tsx | `job:list:self` |
| `/jobs/:jobId` | pages/JobDetailPage.tsx | `job:read` |
| `/exports` | pages/ExportsPage.tsx | `export:list` |
| `/inbox` | pages/InboxPage.tsx | `inbox:read` |
| `/reports` | pages/ReportsPage.tsx | `report:list` |
| `/reports/:id` | pages/ReportDetailPage.tsx | `report:read` |
| `/notifications` | pages/NotificationsPage.tsx | `notification:list` |
| `/profile` | pages/ProfilePage.tsx | `profile:read` |
| `*` | App.tsx | 页面不存在 |

导航裁剪依据 `ROUTE_PERMISSIONS`（`frontend/apps/user-console/src/app/navigation.tsx`）：
精确匹配优先，其次最长前缀匹配；**查不到权限声明的入口不渲染（fail-closed）**。
服务端 `<Guard>` 与逐端点鉴权是权威判据。

## 3. 会话内出现的全部权限名（41 个）

```text
canvas:list  canvas:read  case:create  case:list  case:member  case:read  case:state
dashboard:read  download:create  evidence:read  evidence:review  evidence:sensitive
explorer:fetch  explorer:read  export:list  finding:list  finding:review  inbox:read
investigation:cancel  investigation:create  investigation:list  investigation:pause
investigation:read  investigation:resume  investigation:retry  investigation:start
investigation:submit_review  job:list  job:read  notification:list  profile:read
report:create  report:download  report:list  report:read  report:review  report:sign
search:read  subject:create  subject:remove
```

## 4. 用户端消费的全部接口（重构不得改动路径、方法、请求体、响应体）

```text
认证与会话
  GET   /api/auth/v1/session            （AuthProvider）
  POST  /api/auth/v1/login              （登录，含 workspace_id 与 MFA 202 分支）
  POST  /api/auth/v1/mfa/verify         （第二步与 step-up）
  POST  /api/auth/v1/mfa/enroll/start   POST /api/auth/v1/mfa/enroll/confirm   POST /api/auth/v1/mfa/disable
  GET   /api/auth/v1/methods            （OIDC 是否启用）
  GET   /api/auth/v1/session/workspaces （工作区列表）
  GET   /api/auth/v1/sessions           DELETE /api/auth/v1/sessions/{id}
  POST  /api/auth/v1/sessions/revoke-all
  GET   /api/auth/v1/devices
  POST  /api/auth/v1/password/change

工作台与搜索
  GET   /api/v2/dashboard
  GET   /api/v2/search                  （q、type；250ms debounce，可取消）
  GET   /api/v2/workspace-members

案件
  GET   /api/v2/cases                   GET   /api/v2/cases/{caseId}
  POST  /api/v2/cases                   GET   /api/v2/cases/{caseId}/activity
  GET   /api/v2/cases/{caseId}/members  DELETE /api/v2/cases/{caseId}/members/{identity_id}
  GET   /api/v2/cases/{caseId}/reports
  GET   /api/v2/cases/{caseId}/subjects POST /api/v2/cases/{caseId}/subjects
  DELETE /api/v2/cases/{caseId}/subjects/{subjectId}
  POST  /api/v2/cases/{caseId}/commands/{cmd}

调查 / Finding / Evidence / 报告
  GET   /api/v2/investigations          POST /api/v2/investigations
  GET   /api/v2/investigations/{id}     GET  /api/v2/investigations/{id}/runtime
  GET   /api/v2/investigations/{id}/findings   GET /api/v2/investigations/{id}/evidence
  POST  /api/v2/investigations/preview
  POST  /api/v2/investigations/{id}/commands/{cmd}    （start/pause/resume/retry/cancel）
  POST  /api/v2/investigations/{id}/commands/submit-review
  POST  /api/v2/findings/{id}/commands/{command}      （confirm/reject/supersede）
  GET   /api/v2/evidence/{evidenceId}   POST /api/v2/evidence/{id}/commands/{cmd}
  POST  /api/v2/evidence/{id}/commands/verify
  GET   /api/v2/reports                 GET  /api/v2/reports/{id}
  POST  /api/v2/reports/{id}/commands/{cmd}
  POST  /api/v2/reports/{id}/download-ticket

数据工具
  GET   /api/v2/meta/chains             GET  /api/v2/meta/datasets
  POST  /api/v2/downloads/validate-addresses
  POST  /api/v2/downloads/preview       POST /api/v2/downloads
  GET   /api/v2/jobs                    POST /api/v2/jobs/{id}/commands/{cmd}
  GET   /api/v2/exports                 POST /api/v2/exports

协作
  GET   /api/v2/inbox
  GET   /api/v2/notifications           POST /api/v2/notifications/{id}/read
  POST  /api/v2/notifications/read-all  POST /api/v2/notifications/{id}/open
  GET   /api/v2/me/profile
```

写操作继续使用现有 CSRF、`Idempotency-Key`、`If-Match`/revision 与审计链路。

## 5. 测试依赖的选择器与文案（不得删除或改名）

| 选择器 / 文案 | 位置 | 依赖用例 |
|---|---|---|
| `data-testid="workspace-switcher"` + `data-audience="user"` | 顶栏 / 用户菜单 | `dual-system-cross-role.spec.ts` UX-003 |
| `data-testid="table-summary"` | `DataTable` 分页摘要 | 多个用例 |
| `data-testid="finding-confirm" / "finding-reject" / "finding-supersede"` | FindingsPage | Sprint 4 |
| `data-testid="finding-row-{id}"` | FindingsPage | Sprint 4 |
| `data-testid="finding-evidence-verify-{id}"` + 按钮文字 `Verify` | FindingsPage | Sprint 4 |
| `data-testid="server-verdict-summary"` | DownloadNewPage | R9 系列 |
| `#identifier` / `#password` | LoginPage | R7、nav-permission |
| 按钮可访问名精确等于 `登录` | LoginPage | `dual-system-r7-jobs-scope.spec.ts` |
| `dialog` 角色内的 `知道了` 按钮 | DialogProvider | 全部用例的 `acknowledgeSuccess` |
| `[role="alert"]` / `.banner` / `.alert` / `.notice` / `.callout` / `.toast-inline` **数量必须为 0** | 全站 | `dual-system-sprint4.spec.ts`、`dual-system.spec.ts`、`dual-system-sprint6.spec.ts` 的 `expectNoInlineNotice` |

## 6. 用户端测试清单（重构期间按页运行）

```text
frontend/tests/dual-system.spec.ts
frontend/tests/dual-system-sprint4.spec.ts
frontend/tests/dual-system-sprint5.spec.ts
frontend/tests/dual-system-sprint6.spec.ts
frontend/tests/dual-system-nav-permission.spec.ts
frontend/tests/dual-system-cross-role.spec.ts
frontend/tests/dual-system-reduced-motion.spec.ts
frontend/tests/dual-system-quality.spec.ts
frontend/tests/dual-system-r7-jobs-scope.spec.ts
frontend/tests/live-address-progress.spec.ts
frontend/tests/live-download.spec.ts
frontend/tests/fund-flow.spec.ts
frontend/tests/performance-modes.spec.ts
frontend/tests/p1-workspace-scope.spec.ts / p1-workspace-scope-v2.spec.ts
frontend/tests/r9-uc-download-import*.spec.ts（4 个版本）
frontend/tests/r10*-export-excelize.spec.ts / r10*-legacy-export-import.spec.ts（6 个版本）
frontend/tests/udl01-worker-isolation-v5/v5b/v6.spec.ts
frontend/tests/tron-v4.spec.ts
frontend/tests/ui-refactor.spec.ts
frontend/tests/smoke.spec.ts
frontend/tests/b2-raw-ui-export.spec.ts
frontend/tests/api-control.spec.ts
```

质量用例中的硬约束（重构必须继续满足）：

- `dual-system-quality.spec.ts`：四断点布局 + 全站文字对比度（普通文字 ≥4.5:1，大字 ≥3:1），
  逐元素回溯到第一个不透明祖先取背景色计算。
- `ui-refactor.spec.ts`：`.task-table-scroll` 的 `overflow-x` 必须是 `auto`，
  `body` 的 `overflow-x` 必须是 `hidden`；地址表头 `position: sticky`；输入框字号 ≥12px。
- `dual-system-reduced-motion.spec.ts`：`prefers-reduced-motion: reduce` 下动画时长被压到 0。
- `fund-flow.spec.ts`：资金流的聚焦路径动画名必须是 `flow-money-travel`。

## 7. 开工时的构建基线

```text
npx tsc -p apps/user-console/tsconfig.json --noEmit   → 通过
npx vite build --config apps/user-console/vite.config.ts
  index.html                     0.59 kB │ gzip  0.36 kB
  index-*.css                   77.53 kB │ gzip 14.18 kB
  index-*.js                    84.16 kB │ gzip 26.70 kB
  react-vendor-*.js            233.82 kB │ gzip 74.87 kB
  xlsx-*.js                    500.02 kB │ gzip（按需，Web Worker 内动态 import）
  其余 25 个路由 chunk 均为按路由懒加载，最大 32.28 kB
```

首屏 gzip JS = 26.70 + 74.87 ≈ **101.6 KB**，低于蓝图 §15 的 350 KB 预算。

## 8. 未能验证的项（NOT_VERIFIED）

以下项目需要真实后端、种子数据与浏览器环境，本次施工环境无法完整复现，**明确标记为未验证**，
不以"构建通过"替代：

- 需要 `/api/**` 真实响应的全部端到端业务流程（登录→MFA→首页、案件闭环、下载闭环、Canvas、安全闭环）。
- Playwright 全量回归（需要 `OWNER_EMAIL`/`INV_EMAIL` 等环境变量与运行中的服务端）。
- 四断点真实截图与像素级对照。
- SSE / 轮询实时进度在真实任务上的表现。
