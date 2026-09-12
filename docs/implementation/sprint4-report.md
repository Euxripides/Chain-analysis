# Sprint 4 交付与验收报告（用户案件与调查闭环）

> 计划：`docs/dual-system-development-plan.md` §28.6、§27.3
> 分支：`feat/dual-system-iam-foundation`
> 日期：2026-09-12
> 结论：S4-DB-01、S4-BE-01..04、S4-FE-01..03、S4-QA-01 完成。
> 本轮 API E2E：PASS 79 / FAIL 0 / NOT_VERIFIED 0；Playwright 桌面+移动：PASS 4 / FAIL 0。
> 未进入 Sprint 5。

## 1. 验收前置与不可变约束

- 遵守 `AGENTS.md`：所有新增提示（错误/成功/确认/警告/说明）只通过 `@chain/ui` Dialog 呈现；
  发现并修复了「成功 Dialog 阻塞跳转」「表单 hint 内嵌」两个真实违规。
- 遵守 AD-001..AD-016；迁移编号偏差、runtime 失败可见性、OIDC 禁用策略见 `docs/adr/ADR-016-s4-migration-runtime-oidc.md`。
- 保留既有夹具：`cmd/admin-fixture` 生成的 acme 组织/账号继续保留到 S7，不做 cleanup。
- 生产 OIDC 保持禁用：无真实 IdP 时 `/api/auth/v1/methods` 返回 `oidc:false`，
  `/api/auth/v1/oidc/start|callback` 返回 501 `OIDC_NOT_CONFIGURED`；adapter 与隔离测试见下，
  **AUTH-011 live 验收 NOT_VERIFIED**。

## 2. 任务级 §28.10 输出

### S4-DB-01：0047 协作/审批迁移

```text
Task ID: S4-DB-01
Goal: 为案件协作、Finding/Evidence 复核、报告审批与通知提供 additive 数据底座
Files changed: backend/migrations/postgres/0047_case_collaboration.sql,
               backend/migrations/postgres/0048_s4_permission_fix.sql,
               backend/internal/policy/catalog.go, backend/internal/policy/catalog_sql_test.go
API contracts changed: openapi/user-v2.yaml（由路由生成，61 个 v2 端点）
Migrations added/applied: 0047、0048（VM 已应用，schema_migrations 最高为 0048）
Permissions added: investigation:start/pause/resume/retry/submit_review, subject:list/create/update/remove,
                   evidence:list, report:review；并补授 owner/investigator/analyst/reviewer/viewer/auditor
Audit events added: 本迁移不直接写事件，新增表均由业务事务写 audit_events_v2
Tests run and exact results:
  - 迁移在 VM 一次性成功：postgres migrations applied [...0047,0048]
  - go test ./internal/policy → ok（Catalog↔全部迁移种子一致）
  - schema_migrations 实测最高 0048
Manual UI evidence: N/A（数据层）
Data/file/runtime evidence: PostgreSQL 表 case_members/case_subjects/case_comments/case_activity/
  review_requests/report_approvals/report_download_tickets/notifications 均存在并被 E2E 写入
Security considerations: 全部 additive；不删除旧列；票据只存 token 哈希；audit 应用角色无 UPDATE/DELETE
Rollback procedure: 应用只回滚二进制；表保留，新功能仅停止写入（ADR-006）
Known limitations / follow-ups: 计划 §22.1 原定 0044 的名称为历史偏差，由 ADR-016 记录；
  Sprint 7 若新增回填/分区迁移从 0049 开始
```

### S4-BE-01：案件 API

```text
Task ID: S4-BE-01
Goal: /api/v2/cases 创建/查询/版本化更新/状态命令/成员/对象/动态/评论
Files changed: backend/internal/casework/{service,cases,helpers}.go,
               backend/internal/api/{userv2.go,sprint4.go,server.go}
API contracts changed: openapi/user-v2.yaml：POST/PATCH cases、commands/{activate|close|archive|reopen}、
               members、subjects、activity、comments、workspace-members
Migrations added/applied: 0047/0048（见上）
Permissions added: case:state:update、case:member:*, subject:*、comment:*、case:activity:read（已有权限键补齐声明）
Audit events added: case.create、case.update、case.state:update、case.member:manage、subject:create/remove、
               comment:create；业务时间线另写 case_activity
Tests run and exact results:
  - run-sprint4-e2e.sh USER-001/revision：创建案件 API+DB+审计+动态一致；stale PATCH→412 不覆盖
  - 成员/对象/评论/动态 API 均实测 200/201，HTML 评论被净化为纯文本
  - Playwright user-desktop + user-mobile：新建案件、Tab、对象 Dialog 真实交互通过
Manual UI evidence: 桌面/移动案件列表、新建、概览、对象、动态、成员页面
Data/file/runtime evidence: PostgreSQL cases/case_members/case_subjects/case_comments/case_activity
Security considerations: 所有写事务同写 audit v2；服务端解析 organization/workspace，拒绝客户端 tenant；
               archived 状态只读；最后 lead 不可移除
Rollback procedure: 前端入口下线；API 回滚二进制；数据 additive 可保留
Known limitations / follow-ups: 标签/Tag 关联在 S5 视觉与筛选统一时补齐
```

### S4-BE-02：Investigation v2 compatibility

```text
Task ID: S4-BE-02
Goal: 复用既有 investigation runtime/artifacts，提供 v2 创建、Coverage 预览、状态命令与 runtime 视图
Files changed: backend/internal/casework/{review.go,investigation_v2.go},
               backend/internal/investigation/runtime.go,
               backend/internal/dataset/types.go
API contracts changed: openapi/user-v2.yaml：GET/POST investigations、preview、runtime、
               commands/{start|pause|resume|cancel|retry|submit-review}、findings、evidence
Migrations added/applied: 无新增（复用 0028–0035）
Permissions added: investigation:start/pause/resume/retry/submit_review
Audit events added: investigation:create/start/pause/resume/cancel/retry/submit_review
Tests run and exact results:
  - USER-004：Coverage 预览 ratio=1；start→paused→（暂停期间不推进）→resume→waiting_review；
    run 数≤1，checkpoint.data_reused=true；本地索引复用 0 Provider 费用
  - 幂等：同一 Idempotency-Key 二次创建返回既有 investigation 且 idempotent_replay=true
  - 真实缺陷修复：advanced materialize SQL 使用错误的占位符，导致 executeAnalysis 静默失败、
    step 永久 running；现已改为错误落库（step.error/run failed/investigation failed）
Manual UI evidence: 调查列表、新建预览、工作台阶段/步骤/Evidence Rail 桌面+移动真实渲染
Data/file/runtime evidence: PostgreSQL investigations/investigation_runs/stages/steps/findings/evidence；
  /tmp/api.log 可见 /api/v2/investigations/* 且 0 panic/fatal
Security considerations: 命令全部 require 权限 + If-Match；对象级 guard 二次校验；
  后台 runner 只接受服务端状态机推进
Rollback procedure: 停止 v2 命令入口；既有 /api/v1 investigation runtime 不受影响
Known limitations / follow-ups: 缺口数据的实际下载与暂停游标恢复属 S5（USER-002/003）
```

### S4-BE-03：Finding/Evidence 复核与不可变版本

```text
Task ID: S4-BE-03
Goal: Finding/Evidence 状态命令、verified evidence 前置规则、supersede 版本链、敏感读取审计
Files changed: backend/internal/casework/review.go, backend/internal/investigation/runtime.go
API contracts changed: openapi/user-v2.yaml：findings/{id}、commands confirm/reject/supersede、
               evidence/{id}、commands verify/dispute/supersede
Migrations added/applied: 0047（新增 status/nature/version/revision/supersedes 列与索引）
Permissions added: finding:review、evidence:review、evidence:list
Audit events added: finding:update/review、evidence:read、evidence:review（含 sensitive 标记）
Tests run and exact results:
  - USER-005：viewer confirm→403；reviewer verify Evidence→verified；
    confirm Finding→confirmed 且 finding_evidence_links 有 1 条 verified；
    finding:review 审计落库 =1
  - USER-006：supersede 生成 version=2 新 Evidence 并写 supersedes_id；
    原行 status=superseded 且 content_hash 不变；DB 校验链记录 =1
Manual UI evidence: Findings 列表/详情/引用 Evidence/Verify/Confirm Dialog；Evidence 详情与 Provenance
Data/file/runtime evidence: PostgreSQL investigation_findings/investigation_evidence/finding_evidence_links；
  证据 source_refs 的 tx_hash 在 ClickHouse blockchain.token_transfers 真实存在
Security considerations: 算法字段不可改；确认需 verified Evidence 或审批例外；
  敏感读取单独审计并需 evidence:sensitive:read
Rollback procedure: 回滚二进制；review 列 additive，旧页面继续读元数据
Known limitations / follow-ups: supersede 新版本不会自动改写原 Finding 引用，需复核人重新关联（S5 Canvas 联动）
```

### S4-BE-04：报告 revision/审批/下载票据

```text
Task ID: S4-BE-04
Goal: 报告 revision 生成、审批状态机、签署、归档与 60 秒一次性下载票据
Files changed: backend/internal/casework/{reports.go,service.go},
               backend/internal/report/service.go, backend/internal/api/sprint4.go
API contracts changed: openapi/user-v2.yaml：cases/{id}/reports、reports、reports/{id}、
               commands submit/approve/reject/sign/archive、download-ticket、reports/download/{ticket}
Migrations added/applied: 0047（report_approvals、report_download_tickets、report 状态列）
Permissions added: report:review（approve/reject/archive 使用 report:review，签署使用 report:sign）
Audit events added: report:create、report:submit、report:approve、report:reject、report:sign、report:archive、report:download
Tests run and exact results:
  - USER-008：generated→in_review→approved→signed；report_approvals 绑定 revision+content_hash；
    提交人自批→403（职责分离）；stale status_revision→412；
    票据下载 200，文件 SHA-256=DB/API hash，重复使用→410；
    重新生成 revision+1 且旧 signed artifact 不变
  - 报告文件同时存在于 MinIO blockchain-reports，大小与 DB size_bytes 一致
Manual UI evidence: 报告中心、报告详情审批/签署 Dialog、Inbox 处理入口、下载票据 Dialog（桌面+移动）
Data/file/runtime evidence: PostgreSQL investigation_reports/report_approvals/report_download_tickets；
  MinIO /data/blockchain-reports/<object_key>/xl.meta；/tmp/api.log 下载审计
Security considerations: 只有 approved/signed/archived 可下载；token 32B 随机、只存 SHA-256、60s 单次；
  审批前重新读取 artifact 校验 hash；下载响应 ETag/Content-Disposition/SHA-256
Rollback procedure: 停用 report 命令入口；artifact 与审批记录 additive 保留
Known limitations / follow-ups: PDF/签名证书格式属后续专项；S4 使用 markdown + SHA-256 签署哈希
```

### S4-FE-01..03：用户层页面

```text
Task ID: S4-FE-01/02/03
Goal: 案件列表/新建/概览+Tab；调查列表/预览/工作台/Findings/Evidence；报告中心/复核 Inbox
Files changed: frontend/apps/user-console/src/App.tsx, main.tsx, sprint4.css,
               pages/{CasesPage,CaseNewPage,CaseDetailPage,InvestigationsPage,InvestigationNewPage,
                      InvestigationWorkbenchPage,FindingsPage,EvidenceDetailPage,ReportsPage,
                      ReportDetailPage,InboxPage,NotificationsPage,HomePage,LoginPage}.tsx,
               sprint4/common.tsx, frontend/playwright.dual.config.ts,
               frontend/tests/dual-system-sprint4.spec.ts
API contracts changed: 消费 openapi/user-v2.yaml；无手写重复 DTO（页面内联为视图类型，请求统一走 api-client）
Migrations added/applied: N/A
Permissions added: 页面按 session.permissions 显示/隐藏，但服务端仍二次校验
Audit events added: 页面本身不写审计；所有动作通过后端 API 产生
Tests run and exact results:
  - tsc user-console --noEmit → 通过
  - vite build user/admin → 通过
  - Playwright user-desktop + user-mobile → 4/4 PASS（案件新建、Tab、对象 Dialog、
    工作台、Findings verify/confirm、报告审批/签署、通知页）
Manual UI evidence: 桌面 1440×900、移动 390×844 真实 Chromium 交互；无页面级横向滚动
Data/file/runtime evidence: 页面数据全部来自 /api/v2，无占位值；证据文件见 tests/sprint4/
Security considerations: 提示只用 Dialog；Token 不可 JS 读取；路由 guard 只是体验，服务端是边界
Rollback procedure: Nginx 移除 /app-v2 新入口即可回退旧 SPA；后端不回滚也可用
Known limitations / follow-ups: Canvas/Explorer/下载/导出页面仍显式标 S5 NOT_IMPLEMENTED
```

### S4-QA-01：验收

```text
Task ID: S4-QA-01
Goal: USER-001、004–010、AUDIT-001 的真实闭环验收，并覆盖 PostgreSQL/ClickHouse/MinIO/日志/权限负向/审计链
Files changed: acceptance-evidence/dual-system-v1/tests/run-sprint4-e2e.sh,
               frontend/tests/dual-system-sprint4.spec.ts,
               acceptance-evidence/dual-system-v1/section27-conformance.md,
               docs/implementation/dual-system-progress.md, docs/implementation/sprint4-report.md,
               docs/adr/ADR-016-s4-migration-runtime-oidc.md
API contracts changed: N/A
Migrations added/applied: N/A
Permissions added: N/A
Audit events added: N/A
Tests run and exact results:
  - run-sprint4-e2e.sh：PASS 79 / FAIL 0 / NOT_VERIFIED 0（含用户层角色流转与负向）
  - Playwright：user-desktop + user-mobile 4/4 PASS
  - 审计链 POST /api/admin/v1/audit/verify → valid=true
  - ClickHouse：Evidence source_refs 的 tx_hash 存在
  - MinIO：报告 artifact 存在且 size 匹配
  - API 日志：0 panic/fatal，Sprint 4 路由有真实调用记录
Manual UI evidence: 见 tests/sprint4/ 与 frontend/test-results/（Playwright trace 仅在失败时产生）
Data/file/runtime evidence: PostgreSQL/ClickHouse/MinIO/运行日志四类证据均有断言
Security considerations: 无会话 401、用户层访问后台 401、viewer 写 403、跨组织 404+object_denied
Rollback procedure: 见 Runbook §6
Known limitations / follow-ups: USER-007 Canvas 并发 diff、USER-009 地址/交易/报告类型搜索、
  AUTH-011 真实 IdP live 验收仍属 S5/外部依赖
```

## 3. 本轮发现并修复的真实缺陷

| # | 缺陷 | 影响 | 修复 |
|---|---|---|---|
| 1 | advanced materialize SQL 占位符错误 | 后台分析静默失败，investigation 永久 running，页面无法收敛 | 修正占位符，并让 runtime 失败写 step.error/run.failed/investigation.failed |
| 2 | 案件/调查创建成功 Dialog 未关闭就跳转 | 遮罩覆盖新页面，拦截所有后续交互 | `await dialog.open(...)` 关闭后再 navigate |
| 3 | `dialog.prompt` 返回值带 `input:` 前缀 | 对象类型判断失败，地址对象缺 chain；所有原因/意见被污染 | 统一使用 `dialogInputValue` / 显式解析 |
| 4 | 证据列表一次渲染 100+ 行 | DOM 过大、主要操作按钮难以触达 | 详情最多渲染 8 条，其余通过 Dialog 查看 |
| 5 | viewer 缺 `notification:list` | 通知页路由 403，USER-010 负向无法验证 | 0048 迁移补授 viewer/auditor |
| 6 | 表单 hint 内嵌页面 | 违反 AGENTS.md Dialog-only 约束 | 移除内嵌 hint，必要说明改由 Dialog/字段本身承担 |

## 4. 复现命令

```bash
# 本地
cd backend && go test ./...
cd ../frontend && ./node_modules/.bin/tsc -p apps/user-console/tsconfig.json --noEmit
npm run build:user && npm run build:admin
cd .. && python3 scripts/check_openapi.py

# VM 部署（含迁移/前端构建/重启/冒烟）
python3 scripts/deploy_dual_system.py --yes

# S4 API E2E（需夹具密码与 admin-init -force 的 Owner 密码）
ssh euripides@192.168.31.152 \
  'cd /home/euripides/projects/blockchain-platform && set -a && . deploy/.env && set +a && \
   INVESTIGATOR_PASSWORD=... REVIEWER_PASSWORD=... VIEWER_PASSWORD=... ADMIN_PASSWORD=... \
   CLICKHOUSE_PASSWORD="$CLICKHOUSE_PASSWORD" \
   bash acceptance-evidence/dual-system-v1/tests/run-sprint4-e2e.sh'

# Playwright（桌面 + 移动）
ssh euripides@192.168.31.152 \
  'cd /home/euripides/projects/blockchain-platform/frontend && \
   E2E_BASE_URL=http://127.0.0.1:8088 E2E_CHROMIUM_PATH=/usr/bin/chromium-browser \
   E2E_S4_INVESTIGATOR_EMAIL=... E2E_S4_INVESTIGATOR_PASSWORD=... \
   E2E_S4_REVIEWER_EMAIL=... E2E_S4_REVIEWER_PASSWORD=... \
   /home/euripides/.nvm/versions/node/v24.20.0/bin/node node_modules/@playwright/test/cli.js test \
   --config playwright.dual.config.ts tests/dual-system-sprint4.spec.ts \
   --project=user-desktop --project=user-mobile'
```
