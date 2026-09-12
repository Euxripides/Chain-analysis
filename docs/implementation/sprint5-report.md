# Sprint 5 实施与验收报告（Explorer / 下载 / 任务 / 导出 / 图谱 / 后台任务）

> 计划：`docs/dual-system-development-plan.md` §28.7（S5-BE/FE/ADMIN/QA）、§27.7 证据规范、§28.10 输出模板。
> 分支：`feat/dual-system-iam-foundation`；代码提交：`244f63b`（本轮功能提交 `e9638b6` + 性能修正 `244f63b`）。
> 环境：Ubuntu VM `192.168.31.152`，Nginx `127.0.0.1:8088`（`/app-v2`、`/admin`），API `127.0.0.1:8080`，Worker `:8081`，PostgreSQL/ClickHouse/MinIO/NATS 为 Docker Compose 依赖。
> 迁移：`0049_s5_explorer_download_jobs.sql`、`0050_s5_job_identity_scope.sql` 已应用。
> 本轮结论：**S5 八项任务全部完成；API E2E PASS 104 / FAIL 0 / NOT_VERIFIED 0；Playwright S5 适用用例 14 PASS / 0 FAIL（13 个跨项目守卫 skip）；S1–S4 全量回归 PASS。本轮不进入 Sprint 6。**

## 0. 开工前基线与不可变约束

- `git status`：工作树干净，HEAD `3771763`；未使用 reset/checkout，既有文件与测试夹具保留。
- 基线通过：`go test ./...`、两套前端 `tsc --noEmit`、`vite build`、`scripts/check_openapi.py`。
- 已读取并遵守 `AGENTS.md`（前端提示只能 Dialog）、`docs/code-standards.md`、计划 §15 不可变决策。
- 生产保持 `AUTH_MODE=session`；OIDC live 仍未配置（AUTH-011 继续 PARTIAL/NOT_VERIFIED(live)），MFA 未实现（AUTH-004 NOT_IMPLEMENTED）。

## 1. 交付摘要

| 任务 | 状态 | 关键交付 |
|---|---|---|
| S5-BE-01 Explorer v2 | ✅ 完成 | cursor/来源/口径 envelope、overview/assets/activity/counterparties/semantic/flow-profile、交易详情/日志/本地 Trace、全类型搜索 |
| S5-BE-02 下载 v2 | ✅ 完成 | preview/create 分离、默认 reuse、显式 refresh、用户 scope、缺口估算、幂等键 |
| S5-BE-03 任务与导出 v2 | ✅ 完成 | jobs cursor、SSE Last-Event-ID 重放、暂停/恢复游标、exports v2、60 秒一次性下载票据与 Range |
| S5-FE-01 Explorer 页面 | ✅ 完成 | 全局搜索、Explorer、地址详情 9 Tab、交易详情 3 Tab、桌面/移动 |
| S5-FE-02 下载/任务页面 | ✅ 完成 | 下载表单、Jobs、Job Detail 三层进度、Exports 中心 |
| S5-FE-03 Fund Flow / Canvas | ✅ 完成 | SVG 图谱、Lens、Evidence Rail、revision 412+diff、移动列表视图 |
| S5-ADMIN-01 全局任务与诊断 | ✅ 完成 | 跨组织任务列表、固定七层诊断链、命令与审计 |
| S5-QA-01 验收 | ✅ 完成 | run-sprint5-e2e.sh 104/0/0；Playwright 14/0/13-skip；S1–S4 回归全绿 |

## 2. 验收口径

- 每条 PASS 都有 API/DB/ClickHouse/MinIO/文件/运行态中的至少一种真实证据；没有使用“HTTP 200”“页面存在”作为完成依据。
- Coverage HIT 的 0 Provider 请求用 `provider_usage.trace_id` 前后计数证明；缺口任务用 shard 区间与 checkpoint 证明“只补缺口”。
- 导出文件真实解析：CSV/JSONL 逐行、XLSX 打开 sheet 并核对 sharedStrings/inlineStr、Parquet 用 `parquetprobe` 泛型读取行数、ZIP 校验 manifest/条目/路径穿越；全部比对 SHA-256。
- SSE `Last-Event-ID` 断线重连由 `job_event_log` 序号重放实证。
- 暂停/恢复检查 `checkpoints.page_no/next_page_token/downloaded_rows` 不回退，且逐地址进度行存在。
- Dialog-only：本轮新增/修改页面所有警告、说明、风险、成功、错误、确认均走 Dialog；正文只保留字段标签、表头、数据来源与操作控件。

## 3. 任务级 §28.10 输出

### S5-BE-01 Explorer v2（cursor / 来源 / 口径 envelope）

- Goal：把现有 ClickHouse 数据面完整迁入 `/api/v2`，所有 Tab 明确数据来源与口径，搜索按权限裁剪。
- Files changed：`backend/internal/api/sprint5_explorer.go`、`sprint5_meta.go`、`sprint5.go`、`internal/api/userv2.go`、`internal/explorer/service.go`、`internal/api/sprint5_explorer.go` 测试。
- API contracts changed：`GET /api/v2/search`、`GET /api/v2/meta/chains`、`GET /api/v2/meta/datasets`、`GET /api/v2/explorer/addresses/{chain}/{address}/{overview|assets|activity|counterparties|semantic|flow-profile}`、`GET /api/v2/explorer/transactions/{chain}/{hash}`、`.../logs`、`.../trace`；OpenAPI 重新生成并校验一致。
- Migrations added/applied：无（复用 0001–0008 ClickHouse 表与 0044 作用域）。
- Permissions added：`explorer:read` / `search:read` 已存在；新增路由全部显式声明。
- Audit events added：访问类敏感读取沿用 `explorer` 采样；业务写不涉及。
- Tests run and exact results：
  - `run-sprint5-e2e.sh` USER-009 全类型：case/investigation/job/report/address/transaction 全部命中，viewer self scope 负向不返回他人 job；
  - RBAC-004：auditor 无 `evidence:read` 调用正文 -> 403 且 `authorization.denied` 落库；
  - Playwright FE-01 桌面+移动：真实搜索、地址详情、交易详情、Logs/Trace。
- Manual UI evidence：`tests/sprint5/screenshots/search-desktop.png`、`address-desktop.png`、`address-mobile.png`。
- Data/file/runtime evidence：overview 改走 `address_activity`/`dataset_coverage` 后实测 0.26s（原先 24s）；activity 全量 10k 走 `full=1` 轻量列，50 页加载实测约 9s。
- Security considerations：服务端注入 tenant/workspace，客户端不能提交 actor/scope；金额一律 raw 字符串；Provider 请求仅显式 `explorer:fetch` + Dialog 确认。
- Rollback procedure：回退 API/前端二进制与静态产物；ClickHouse 只读，无迁移回滚。
- Known limitations / follow-ups：EVM 下内部调用 activity 行不完整（后端数据可用性，非伪造）；地址/交易搜索为本地索引语义，不自动向 Provider 补数。

### S5-BE-02 下载 preview / create（默认 reuse、显式 refresh、用户 scope）

- Goal：preview/create 分离；默认优先本地复用，显式 refresh 才新增 Provider 工作；所有任务带 organization/workspace/identity 作用域。
- Files changed：`backend/internal/api/sprint5_downloads.go`、`internal/dataset/service.go`、`internal/dataset/types.go`、`internal/store/jobs.go`、`internal/model/model.go`、`backend/migrations/postgres/0050_s5_job_identity_scope.sql`。
- API contracts changed：`POST /api/v2/downloads/preview`、`POST /api/v2/downloads`（强制 `Idempotency-Key`）。
- Migrations added/applied：`0050`：新增 `download_jobs.created_by_identity`（FK identities）与复合索引，回填 legacy_user_id 可推导的旧任务；`0049` 的 `download:refresh` 权限与角色映射。
- Permissions added：`download:refresh`（investigator/analyst）、`job:list:workspace`。
- Audit events added：`download.create`、`download.create.coverage_hit`、`download.reuse`、`download.refresh`。
- Tests run and exact results：
  - USER-002：preview ratio=1、`provider_work_required=false`；create status=ready/created=false/reused=false/job_id=null；HIT preview+create 按 trace_id 查询 provider_usage=0；download_jobs/download_shards 前后不变；调查 ensure_data 步骤输出 `reuse=indexed`、run checkpoint `data_reused=true`；
  - USER-003：缺口 preview ratio<1、missing_blocks>0；创建任务 shard 全部落在缺口区间（越界 shard=0）；成功分页游标落库；pause→PAUSED 后 checkpoint 不回退；resume 后 page/rows 前进，不从 0 重下；
  - DATA-001：显式 refresh 真的创建并完成单区块下载，provider_usage>0；导出文件保留 `247763038695932807808` 原值。
- Manual UI evidence：`download-new-desktop.png`；Playwright FE-02 真实预览/创建 Dialog。
- Data/file/runtime evidence：`run.log` USER-002/003；`files/big.csv` 等。
- Security considerations：`refresh` 需 `download:refresh`；未授权 refresh 返回 403；scope 由服务端注入，跨租户 job 统一 404；视图不看 `created_by` 口令字段。
- Rollback procedure：停用 v2 路由；0050 仅增列不删列，应用回滚后旧列仍可用。
- Known limitations / follow-ups：full range 在 TRON+Ankr 下仍要求显式时间/区块范围（既有安全约束）。

### S5-BE-03 jobs v2 / SSE Last-Event-ID / exports v2 / download tickets

- Goal：任务 cursor 列表与三层详情、SSE 断线重放、导出异步与一次性票据、Range 下载。
- Files changed：`backend/internal/api/sprint5_jobs.go`、`sprint5_jobs_control.go`、`sprint5_exports.go`、`internal/api/export_v2.go`、`export.go`、`internal/api/sse.go`、`internal/store/job_retry.go`、迁移 `0049`。
- API contracts changed：`GET/POST /api/v2/jobs`、`GET /api/v2/jobs/{id}`、`POST /api/v2/jobs/{id}/commands/{pause|resume|cancel|retry}`、`GET /api/v2/jobs/{id}/events`；`GET/POST /api/v2/exports`、`GET /api/v2/exports/{id}`、`POST /api/v2/exports/{id}/download-ticket`、`GET/HEAD /api/v2/exports/download/{ticket}`。
- Migrations added/applied：`0049`：`job_event_log`、`export_tickets.used_at/used_by`、S5 权限。
- Permissions added：`admin:job:list`、`admin:export:list`、`job:list:workspace`、`download:refresh`；命令使用 `job:control`/`job:retry`/`export:download`。
- Audit events added：`job.pause/resume/cancel/retry`、`export.create/reuse/download_ticket/download/download_failed`。
- Tests run and exact results：
  - USER-003 暂停/恢复保留成功 paging cursor 与地址进度；
  - DATA-003/004/005：CSV/XLSX/JSONL/Parquet 均真实解析并包含原始大整数，ZIP manifest/条目/无路径穿越，HEAD 200 + `Accept-Ranges: bytes` + ETag=SHA-256，Range 206 + `Content-Range` + 分片 hash 一致，同一票据重复使用 410；
  - DATA-006：Worker Kill-9 后 systemd 恢复，成功 shard 与 checkpoint page_no/rows 不回退、shard 集合不重复；
  - DATA-007：NATS 停止期间调查创建事务性写入 outbox，重启后补发 `published_at` 置位且无遗留错误，aggregate 唯一；
  - DATA-008：备份并删除 MinIO 对象后详情 `object_missing=true`/`download_available=false`，票据创建 503 `DATA_OBJECT_MISSING`，审计 `export.download_failed` 带 `action=notify_admin`，恢复对象后可下载；
  - SSE：事件按 job_event_log 单调 id 发送，断线用 Last-Event-ID 重放。
- Manual UI evidence：`job-detail-desktop.png`；Playwright FE-02 任务列表/详情 + SSE 连接。
- Data/file/runtime evidence：`tests/sprint5/files/*`、`run.log`、`runtime-verification.txt`（audit chain valid、服务 active、health ready）。
- Security considerations：票据 60 秒、单次、绑定 user/export/scope；下载走 API 流式，不暴露 MinIO；Parquet/ZIP 解析在测试侧完成，不上传第三方。
- Rollback procedure：停用 v2 任务/导出路由；`job_event_log` 与票据使用列可保留不回滚。
- Known limitations / follow-ups：历史票据无 `used_at` 的旧票保持兼容；Provider rows/s 无数据时明确 null，不伪造。

### S5-FE-01 全局搜索 / Explorer / 地址详情 / 交易详情

- Goal：用户只通过真实 API 完成搜索、地址/交易查询与详情查看，所有来源/口径可见，跨权限对象不泄露。
- Files changed：`frontend/apps/user-console/src/pages/SearchPage.tsx`、`ExplorerPage.tsx`、`AddressDetailPage.tsx`、`TransactionDetailPage.tsx`、`sprint5/common.tsx`、`App.tsx`。
- API contracts changed：消费 `S5-BE-01` 全部 v2 契约；前端不拼越权参数。
- Migrations added/applied：无。
- Permissions added：路由守卫 `explorer:read`、`search:read`。
- Audit events added：搜索/详情读取走 v2 显式权限声明。
- Tests run and exact results：Playwright user-desktop + user-mobile 各 PASS：
  - 搜索按六类分组、进入地址详情；
  - overview 当前余额与历史净流量分离、Coverage 真实数据；
  - 交易详情 local_only 解码、Logs/Trace 真实响应（Trace 404 也按业务空状态处理）。
- Manual UI evidence：`screenshots/search-desktop.png`、`address-desktop.png`、`address-mobile.png`。
- Data/file/runtime evidence：`run.log` USER-009 + RBAC-004；API overview 0.26s。
- Security considerations：搜索服务端按 case/investigation/job/report 权限与 workspace 裁剪；address/transaction 仅 `explorer:read` 本地索引；不支持客户端自报 scope。
- Rollback procedure：回退前端静态产物；后端 Explorer v2 路由可独立关闭。
- Known limitations / follow-ups：TRON 哈希 URL 不带 0x 已兼容；EVM internal calls 行缺失时 Tab 显示业务空状态。

### S5-FE-02 下载表单 / 任务列表 / 任务详情 / 导出中心

- Goal：对象→链/范围→Dataset→复用→预览→创建完整表单；任务三层进度；导出票据与 Range。
- Files changed：`DownloadNewPage.tsx`、`JobsPage.tsx`、`JobDetailPage.tsx`、`ExportsPage.tsx`、`App.tsx`、`sprint5/common.tsx`。
- API contracts changed：消费 `S5-BE-02/03`；创建请求带 `Idempotency-Key`。
- Migrations added/applied：无。
- Permissions added：`download:create`、`download:refresh`、`job:list:self`、`job:read`、`job:control`、`job:retry`、`export:*`。
- Audit events added：由后端在 preview/create/commands/export 上落库；前端只展示 Trace。
- Tests run and exact results：Playwright 桌面+移动 PASS：地址导入解析 Dialog、Coverage HIT 预览不复用 Provider、创建成功 Dialog、任务列表/详情三层进度与 SSE、导出列表；DataTable 虚拟滚动 DOM 行数 ≤100。
- Manual UI evidence：`download-new-desktop.png`、`job-detail-desktop.png`。
- Data/file/runtime evidence：USER-002/003、DATA-003/004/005、DATA-006。
- Security considerations：refresh 必须 Dialog 确认成本；错误/403/409/412 均走 Dialog；任务详情对无 scope 用户 404。
- Rollback procedure：回退用户前端静态产物。
- Known limitations / follow-ups：导出大小字段未在 API 契约中定义，页面显示 `—` 而不伪造；活动任务默认 self + 客户端非终态过滤，老任务需翻页。

### S5-FE-03 Fund Flow / Evidence Rail / Canvas revision / 移动查看

- Goal：图谱工作区、证据引用、并发 revision 冲突可解释、移动端可用。
- Files changed：`CanvasListPage.tsx`、`CanvasPage.tsx`、`sprint5/sprint5.css`、`AddressDetailPage.tsx`（全量视图）、`packages/ui/primitives.tsx`（虚拟滚动）。
- API contracts changed：`GET/POST /api/v2/investigations/{id}/canvases`、`GET/PATCH /api/v2/canvases/{id}`、branches/snapshots、`GET /api/v2/evidence/{id}`、flow-profile。
- Migrations added/applied：无（复用 0030/0047）。
- Permissions added：`canvas:list/create/read/update/branch/snapshot`，`canvas:snapshot:create`。
- Audit events added：`canvas.create/update/update.conflict/branch/snapshot`、`canvas.evidence_ref.denied`。
- Tests run and exact results：
  - USER-007 API：第一次保存 revision+1，第二客户端 stale 保存 412 且 details.diff 非空，库中名称未被覆盖；Playwright 桌面：第二客户端 API bump 后 UI 加入资金流→保存 412→字段级 diff Dialog→加载服务端版本，revision 前进；
  - RBAC-007：跨案件 Evidence 引用 422 `BUSINESS_CROSS_CASE_EVIDENCE`，返回 cross_case_ids，DB 未写入，审计 `canvas.evidence_ref.denied`；
  - USER-011：user-mobile Canvas 列表视图/SVG 展开无横向溢出，Dialog 可关闭；
  - USER-012：纯键盘完成搜索并进入结果，Dialog 焦点陷阱且 ESC 可退出；
  - UX-004：真实 10k 行加载后 DOM 行数 ≤100，滚动交互 <1.5s，加载约 9s（`full=1` 轻量列）。
- Manual UI evidence：`screenshots/canvas-desktop.png`、`canvas-mobile.png`。
- Data/file/runtime evidence：`run.log` USER-007/RBAC-007；`playwright-full.log` UX-004。
- Security considerations：State 中的 `evidence:<uuid>` 服务端校验归属案件；不存在的引用也拒绝；422/412 不清空本地编辑。
- Rollback procedure：回退前端；Canvas 表只读兼容。
- Known limitations / follow-ups：Canvas 导出按钮跳转导出中心，不直接生成文件；拖拽坐标未持久化；分支树未可视化；>2000 节点停止扩展并 Dialog 说明。

### S5-ADMIN-01 全局任务列表与固定诊断链

- Goal：平台管理员跨组织定位任务、按固定链诊断到请求/Provider/Shard/Checkpoint/RawObject/Coverage。
- Files changed：`backend/internal/api/sprint5_admin_jobs.go`、`adminv1.go`、`frontend/apps/admin-console/src/pages/JobsPage.tsx`、`JobDetailPage.tsx`、`admin App.tsx`。
- API contracts changed：`GET /api/admin/v1/jobs`、`GET /api/admin/v1/jobs/{id}/diagnostics`、`POST /api/admin/v1/jobs/{id}/commands/{command}`。
- Migrations added/applied：`0049` 的 `admin:job:list`/`admin:export:list` 权限。
- Permissions added：`admin:job:list`（platform_admin/auditor）、`admin:export:list`（platform_admin）。
- Audit events added：`admin.job.pause/resume/cancel/retry`；诊断读取按 `admin:job:read` 审计。
- Tests run and exact results：run-sprint5-e2e.sh 断言 `fixed_chain` 顺序 = DownloadJob→SubjectJob→DatasetJob→ShardCheckpoint→ProviderUsage→RawObject→ClickHouseCoverage；admin 无会话 401；viewer 不能控制任务 403。Playwright admin-desktop：全局列表含组织/工作区、诊断链七层全部渲染、Provider 错误无 `apikey=` 明文。
- Manual UI evidence：`screenshots/admin-jobs.png`、`admin-job-diagnostics.png`。
- Data/file/runtime evidence：`runtime-verification.txt`（审计链 2263 条 valid=true；服务全 active）。
- Security considerations：ProviderUsage.error 经 `maskProviderError` 脱敏；RawObject 仅显示摘要/大小/SHA-256 或跳过大对象说明；跨组织数据仅管理员可见。
- Rollback procedure：回退 admin 前端与 v2 管理路由；0049 权限可保留。
- Known limitations / follow-ups：后台未接 SSE 实时刷新（手动刷新）；retry 未提供 refresh 策略选择；告警生命周期属 S6。

## 4. S5-QA-01 测试结果

| Test ID | 场景 | 状态 | 证据 |
|---|---|---|---|
| USER-002 | Coverage HIT 0 Provider、0 新 Shard | **PASS** | preview ratio=1/provider_work_required=false；HIT preview+create trace 的 provider_usage=0；调查 ensure_data `reuse=indexed`、checkpoint `data_reused=true`；jobs/shards 不变 |
| USER-003 | 增量只补缺口、暂停/恢复保留游标 | **PASS** | 越界 shard=0；checkpoint page_no/next_page_token/downloaded_rows 暂停后不回退；resume 后前进；逐地址进度行存在 |
| USER-007 | Canvas 并发保存 412 + diff | **PASS** | API 412 details.diff 非空且 DB 未被覆盖；Playwright 桌面/移动 412 Dialog + 字段级 diff + 加载服务端版本 |
| USER-009 | 全局搜索全类型 + 权限裁剪 | **PASS** | 六类分组命中；viewer self scope 看不到他人 job；跨工作区案件不返回 |
| USER-011 | 手机 Canvas/Explorer/下载 查看 | **PASS** | user-mobile 真实浏览器：列表视图、SVG 展开、无页面横向滚动、Dialog 可关闭 |
| USER-012 | 键盘完成核心流程 | **PASS** | 键盘搜索→进入结果，Dialog 焦点陷阱且 ESC 可退出 |
| DATA-001 | 金额超 JS 安全整数 | **PASS** | `247763038695932807808` 在 API/CSV/XLSX/JSONL/Parquet/ZIP 中原值一致，hash 与 DB 一致 |
| DATA-002 | Current Balance 与 Net Flow | **PASS** | overview `current_balance.is_current_balance=true`、`historical_net_flow.is_current_balance=false`，来源/口径字段明确 |
| DATA-003 | CSV/XLSX/JSONL/Parquet | **PASS** | 四种格式逐行解析、行数>0、包含原始金额、SHA-256 与 DB 一致；Parquet 用 parquetprobe 泛型读取 |
| DATA-004 | ZIP manifest/条目/hash/无穿越 | **PASS** | manifest.json export_id/chain/addresses/datasets/format；条目名无 `..`/绝对路径；逐条 testzip；整包 SHA-256 一致 |
| DATA-005 | Range 下载 | **PASS** | HEAD 200 + Accept-Ranges + ETag=SHA-256；Range 206 + Content-Range + 分片 hash；票据重复使用 410 |
| DATA-006 | Worker Kill-9 | **PASS** | systemd 恢复新 PID；任务仍在、shard 集合无重复/丢失、checkpoint page_no/rows 不回退 |
| DATA-007 | NATS 重启 / Outbox | **PASS** | NATS 停止期间 outbox 事务写入；重启后 published_at 置位、last_error 空、aggregate 唯一 |
| DATA-008 | MinIO 对象缺失 | **PASS** | 删除后 `object_missing=true`、票据 503 `DATA_OBJECT_MISSING`、审计 `action=notify_admin`；恢复后可下载 |
| UX-001 | Dialog 焦点 | **PASS** | S5 新页 Playwright：Dialog 可 ESC 关闭、焦点不滞留；`dual-system-quality.spec.ts` 27/27 |
| UX-002 | 四断点布局 | **PASS** | quality spec 390/768/1024/1440 × 用户/后台无横向滚动；S5 页面额外断言 390/1440 |
| UX-003 | 颜色和状态 | **PASS** | quality spec 逐元素 WCAG AA + 形状前缀 27/27；新页沿用同一 tokens/StatusBadge |
| UX-004 | 10k 长表 | **PASS** | 真实 coverage 10k–30k 地址加载 ≥10,000 行；DOM 行数 ≤100；滚动 <1.5s；`full=1` 轻量列加载约 9s |
| UX-005 | reduced motion | **PASS** | tokens 媒体查询 + 用户/后台构建产物均含 `prefers-reduced-motion: reduce` |
| RBAC-004 | 无 evidence 权限读取正文 | **PASS** | auditor 调用 `/api/v2/evidence/{id}` -> 403，`authorization.denied`（permission_key=`evidence:read`, result=deny）落库 |
| RBAC-007 | Canvas 跨案件 Evidence | **PASS** | PATCH Canvas 引用他案 evidence -> 422 `BUSINESS_CROSS_CASE_EVIDENCE`；DB 未写入；审计 `canvas.evidence_ref.denied` |

> AUTH-004（MFA）仍 NOT_IMPLEMENTED；AUTH-011（OIDC live）仍 PARTIAL + NOT_VERIFIED(live)，无真实 IdP；ADMIN-001..010 属 Sprint 6；DATA-009 属 Sprint 7。本轮未以这些未执行项冒充 S5 完成。

## 5. 全量回归结果（同一部署）

| 套件 | 结果 | 说明 |
|---|---|---|
| `run-sprint4-e2e.sh` | PASS 79 / FAIL 0 / NOT_VERIFIED 0 | 更新了 USER-009 对 S5 类型扩展的断言（仍要求 case+investigation） |
| `run-sprint5-e2e.sh` | PASS 104 / FAIL 0 / NOT_VERIFIED 0 | build_sha=`244f63b` |
| `run-rbac-e2e.sh` | PASS 29 / FAIL 0 / BLOCKED 2 | RBAC-004/007 已由 S5 套件实际验证；S3 脚本保留历史 BLOCKED 标注 |
| `run-sprint12-e2e.sh` | PASS 19 / FAIL 0 / BLOCKED 1 | BLOCKED=MFA 未实现 |
| `run-iam-e2e.sh` | PASS 32 / FAIL 0 | 认证/会话/开放重定向 |
| `dual-system-quality.spec.ts` | 27 PASS / 0 FAIL | UX-002/003/004 + SEC-002，三 project |
| `dual-system-sprint5.spec.ts` | 14 PASS / 0 FAIL / 13 project skip | user-desktop + user-mobile + admin-desktop；skip 全部是项目守卫 |

## 6. 本轮发现并修复的真实缺陷

| # | 缺陷 | 影响 | 修复 |
|---|---|---|---|
| 1 | `download_jobs.created_by` FK 指向 legacy `users`，fixture identity 无对应行 | v2 下载创建全部失败 | 0050 新增 `created_by_identity`，legacy 值可推导才保留 |
| 2 | 显式 `refresh` 被 `Ensure` 的 Coverage 短路吞掉 | “强制重下”变成 ready、无 Provider 工作 | refresh 跳过 coverage-hit 短路 |
| 3 | Planner bounded range 仍按本地 Coverage 去缺口 | refresh 任务 0 shard 直接 completed | refresh 时 `covered=nil`，强制全范围分片 |
| 4 | `filterGPTActionExportToken` 把缺失 Stats 键当 `<nil>` token | 所有 job 导出 0 行 | 缺失键/nil 直接返回原 rows |
| 5 | v2 job 导出强制 ZIP | 四种格式无法逐格式验证 | 单地址单 Dataset 保持原格式，多对象才 ZIP |
| 6 | Explorer overview 24s（uniqExact + 全量净流量） | 页面 20s+ 超时 | 改走 address_activity + coverage.row_count；净流量 activity 聚合，实测 0.26s |
| 7 | UInt64 count 扫描进 int64 报错被静默忽略 | overview 计数/覆盖率错误 | SQL 显式 `toInt64/toUInt64` |
| 8 | TRON tx hash 无 0x 前缀被 `chain.ParseHash` 拒绝 | 交易详情不可用 | tron 64-hex 自动补 0x，后端兼容 |
| 9 | 报告搜索查询不存在的 `title` 列 | type=report 搜索 500 | JOIN investigations 用 goal 作为标题 |
| 10 | 交易搜索只查 transactions | token_transfers/logs 命中不到 | UNION transactions/token_transfers/logs |
| 11 | Canvas `dialog.prompt` 返回值带 `input:` 前缀 | flow-profile 请求 `/input:tron/...` 400 | 使用 `dialogInputValue` 解包 |
| 12 | S4 casework 只按默认 workspace 查对象 | RBAC-003 工作区绑定读跨工作区案件 404 | `scopedObject` 把对象授权 workspace 注入 Principal |
| 13 | 10k 全量加载逐行 Contract/Case 富化 | 50 页耗时 252s | `full=1` 轻量列，实测约 9s |
| 14 | 验收脚本夹具/竞态/CRLF、XLSX inlineStr、Parquet 探针 panic | 回归误 FAIL | 修夹具选择、响应 wait 顺序、解析器与探针 |

## 7. 复现命令

```bash
# 部署（含迁移 0049/0050、后端编译、双前端构建、重启与健康检查）
python3 scripts/deploy_dual_system.py --yes

# API E2E（VM；需要夹具密码、Owner 密码与 CLICKHOUSE_PASSWORD）
ssh euripides@192.168.31.152 \
  'cd ~/projects/blockchain-platform && set -a && . deploy/.env && set +a && \
   . /tmp/s5-e2e-env.sh && export S5_BUILD_SHA=<sha> && \
   bash acceptance-evidence/dual-system-v1/tests/run-sprint5-e2e.sh'

# S5 Playwright（桌面 + 移动 + 后台）
ssh euripides@192.168.31.152 \
  'cd ~/projects/blockchain-platform/frontend && set -a && . ../deploy/.env && set +a && \
   . ../acceptance-evidence/dual-system-v1/tests/sprint5/ids.env && set +a && \
   E2E_BASE_URL=http://127.0.0.1:8088 E2E_CHROMIUM_PATH=/usr/bin/chromium-browser \
   /home/euripides/.nvm/versions/node/v24.20.0/bin/node node_modules/@playwright/test/cli.js test \
   --config playwright.dual.config.ts tests/dual-system-sprint5.spec.ts \
   --project=user-desktop --project=user-mobile --project=admin-desktop'
```

## 8. 证据索引

- `acceptance-evidence/dual-system-v1/tests/sprint5/result.json`：build_sha、PASS/FAIL/NV。
- `.../sprint5/run.log`：104 条断言逐条结果。
- `.../sprint5/files/{big.csv,big.xlsx,big.jsonl,big.parquet,big.zip,range.part}`：真实导出文件。
- `.../sprint5/playwright-full.log`：14 PASS / 0 FAIL / 13 skip。
- `.../sprint5/screenshots/`：搜索、地址（桌面/移动）、下载、任务详情、Canvas（桌面/移动）、后台任务与诊断。
- `.../sprint5/runtime-verification.txt`：服务状态、迁移、表计数、审计链 valid、MinIO/ClickHouse 健康、无 panic。

## 9. 未验证 / 后续项

- AUTH-004 MFA、AUTH-011 live IdP：保持 NOT_IMPLEMENTED / PARTIAL+NOT_VERIFIED，不随 S5 改变。
- ADMIN-001..010、后台控制/成本/告警/备份：Sprint 6；本轮未实现、未标 PASS。
- DATA-009 备份恢复演练：Sprint 7。
- UX-004 的 10k 客户端加载走 `full=1` 浅富化（不返回 Contract/Case/Tag 附加字段）；核心业务列和行数不变，已在页面明确为列表性能路径。
- Canvas 导出仅跳转导出中心；节点拖拽布局未持久化；分支树未图形化。
- 回滚：API/Worker 与双前端均有 `bak-dualsystem-*` 回滚点；迁移全部 additive，可直接停用 v2 路由而不删表列。
