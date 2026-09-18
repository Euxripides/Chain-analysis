# 双系统完成与验收判定标准

## 1. 文档用途

本文用于判定区块链调查平台的用户端、后台管理端和共同后端是否已经完成开发并具备正式切流条件。

“页面已经存在”“接口返回 200”“构建成功”“某个 Sprint 已完成”均不能单独作为系统完成依据。系统只有同时满足本文全部硬性门禁，才能在 `RELEASE-DECISION.md` 中写为 `GO`。

## 2. 完成的唯一判定

系统完成必须同时满足以下条件：

1. 发布范围内所有需求均已实现，需求追踪矩阵中没有 `PARTIAL`、`NOT_IMPLEMENTED`、`NOT_VERIFIED`、`NOT_RUN` 或无依据的 `SKIPPED`。
2. P0 缺陷为 0，P1 缺陷为 0。
3. 全量自动化和人工验收 `FAIL=0`、`BLOCKED=0`、`NOT_VERIFIED=0`。
4. 用户端、后台端均在真实浏览器中完成桌面端和移动端验证，操作结果与 API、数据库、对象文件、运行日志一致。
5. API、数据库迁移、OpenAPI、前端调用代码、用户手册和管理员手册描述一致。
6. 当前验收代码、线上二进制、前端静态资源、数据库迁移和证据目录属于同一个冻结发布候选版本。
7. 生产配置、安全、性能、备份恢复、监控告警、灰度切流和回滚均通过。
8. `RELEASE-DECISION.md` 已更新为当前版本，经批准人签署，并明确写出 `GO`。

任意一项不满足，系统状态只能是 `NO-GO`。

## 3. 状态定义

| 状态 | 含义 | 是否允许发布 |
|---|---|---|
| PASS | 当前冻结版本已按预期结果完成实测，证据可复算 | 是 |
| PARTIAL | 已实现或已测试一部分，但仍缺页面、接口、数据、异常分支或证据 | 否 |
| NOT_IMPLEMENTED | 发布范围内功能未实现 | 否 |
| NOT_VERIFIED | 功能可能存在，但当前版本没有有效实测证据 | 否 |
| NOT_RUN | 尚未执行 | 否 |
| BLOCKED | 因环境、凭据、依赖或测试编排无法执行 | 否 |
| FAIL | 实际结果不符合预期 | 否 |
| NOT_APPLICABLE | 经需求负责人确认不属于本次范围，并已同步修改计划、OpenAPI 和追踪矩阵 | 可以 |

`NOT_APPLICABLE` 必须有书面范围决定，不能用来规避失败或未实现功能。

## 4. 发布候选版本冻结

开始最终验收前先冻结候选版本，生成唯一 `release_id`，至少记录：

- Git commit SHA；
- 工作树状态；
- API、Worker、MCP 二进制 SHA-256；
- 用户端、后台端构建产物 SHA-256；
- PostgreSQL migration 最大版本；
- ClickHouse schema 版本或表结构摘要；
- Nginx、systemd、compose 配置 SHA-256；
- Go、Node.js、pnpm、浏览器和操作系统版本；
- 验收开始时间、环境名称、执行人。

硬性要求：

- 不允许一边验收一边覆盖线上二进制或前端资源；
- 代码发生任何功能性修改后，受影响测试及其上游门禁必须重新执行；
- 证据中的 build SHA 必须与线上实际文件相同；
- 未提交文件如果进入发布包，必须纳入 manifest 并计算哈希；
- 旧构建的 PASS 不能自动继承给新构建。

建议证据文件：

```text
acceptance-evidence/dual-system-release/<release_id>/
├─ manifest.json
├─ environment.json
├─ source-status.txt
├─ deployed-hashes.txt
├─ test-summary.json
├─ user-console/
├─ admin-console/
├─ api/
├─ database/
├─ files/
├─ security/
├─ performance/
├─ disaster-recovery/
├─ cutover/
└─ RELEASE-DECISION.md
```

## 5. 总门禁

| 门禁 | 完成条件 | 必需证据 |
|---|---|---|
| G-01 范围 | 需求追踪矩阵每一行均闭合；范围外项目有批准记录 | 需求追踪矩阵、范围决定 |
| G-02 构建 | API、Worker、MCP、用户端、后台端全部构建成功 | 构建日志、产物哈希 |
| G-03 自动化 | 单元、集成、API E2E、Playwright、安全负向全部无失败和阻塞 | 原始日志、结构化摘要 |
| G-04 用户端 | 全部用户流程在真实浏览器和真实 API 上通过 | 截图、网络响应、DB/文件核对 |
| G-05 后台端 | 全部管理、审批、诊断、恢复流程通过 | 截图、审计事件、状态变更证据 |
| G-06 权限 | 角色、租户、工作区、资源级权限正向和负向测试全部通过 | 权限矩阵、403/404、审计记录 |
| G-07 审计 | 登录和全部关键业务操作可按 request/trace 还原，哈希链有效 | 审计覆盖报告、链校验、MinIO 锚点 |
| G-08 数据 | API、PostgreSQL、ClickHouse、MinIO、导出文件语义一致 | 查询结果、行数、哈希、解析结果 |
| G-09 安全 | P0/P1 为 0，秘密扫描、依赖、镜像、IaC 和负向攻击测试通过 | 扫描报告、处置记录 |
| G-10 性能 | 交互查询、长表、图谱、导出、Provider、完整流水线达到预算 | 原始压测数据、P50/P95/P99 |
| G-11 恢复 | PostgreSQL、ClickHouse、MinIO、NATS、API、Worker 故障与恢复演练达到 RPO/RTO | 备份、恢复日志、前后数据核对 |
| G-12 生产运行 | `APP_ENV=production`，运行账号降权，监控告警有效 | 环境快照、角色权限、告警实测 |
| G-13 切流 | 新入口切为正式入口，旧入口只读观察或下线，回滚实测可用 | Nginx 配置、入口截图、回滚演练 |

## 6. 用户端完成标准

用户端必须验证以下完整流程，不能只验证登录页或页面加载。

### 6.1 登录和个人安全

- 本地账号正确登录、错误密码、不存在账号返回一致的安全错误；
- 用户会话与后台会话 audience 严格分离；
- Session refresh、退出、全部下线、会话过期行为正确；
- MFA 注册、确认、登录第二步、错误码、失败次数、重放拒绝、恢复码、关闭 MFA 全部通过；
- OIDC 如果属于发布范围，必须接真实 IdP 验证 state、nonce、PKCE、回调失败和审计；
- 如果 OIDC 不属于发布范围，必须从本次需求、页面、OpenAPI 和测试矩阵中一致移除；
- 安全设置、设备、登录记录和会话撤销结果与数据库一致；
- Cookie 必须为 HttpOnly、Secure、SameSite，HTTPS 入口使用规定的 `__Host-` 前缀；
- 前端不能读取或持久化认证 Token。

### 6.2 案件和调查

- 创建案件、编辑基本信息、添加调查对象、添加成员、变更角色；
- 创建调查并完成 preview，Coverage 命中和缺口行为正确；
- 调查运行、暂停、恢复、失败重试、Checkpoint 保留正确；
- Finding 候选、确认、驳回和复核权限正确；
- Evidence 内容、来源、完整哈希、版本替代链和操作记录正确；
- Investigator 到 Reviewer 再到报告批准形成完整真实流程；
- 并发修改返回 412 和字段差异，不覆盖他人数据；
- 跨组织、跨工作区、跨案件引用必须拒绝且留下审计。

### 6.3 Explorer、下载和任务

- 地址、交易、案件、调查、任务、报告搜索结果正确分组；
- 地址详情、资产、交易、对手方、语义、标签、数据覆盖均来自真实数据；
- 交易详情的交易、回执、内部调用、日志和方法解析一致；
- 下载 preview 正确区分 HIT、增量缺口和 refresh；
- 默认相同请求复用已有数据，显式 refresh 才重新下载；
- 暂停后 Provider 不继续推进，恢复后保留成功游标；
- 任务详情中的地址、Dataset、Shard、Checkpoint 和 Provider 状态一致；
- SSE 断线重连、`Last-Event-ID` 重放、会话撤销和跨租户隔离通过；
- 下载失败、限流、Provider 不可用和数据量保护均显示可操作错误。

### 6.4 图谱、导出和报告

- 图谱节点、边、Lens、搜索、Inspector、Evidence Rail 正确；
- 大图达到节点预算时使用 LOD、聚合或明确拒绝；
- CSV、XLSX、JSONL、Parquet、ZIP 均能被标准工具解析；
- 导出行数、字段、金额精度、时间、地址和交易哈希与数据源一致；
- 下载票据单次使用、过期、HEAD、Range 及 ETag 行为正确；
- 文件 SHA-256 与 API、数据库、manifest 一致；
- 报告版本、审批、签署、下载和归档状态正确。

### 6.5 用户端浏览器要求

- Chromium 桌面 1440×900；
- Chromium 移动端 390×844；
- 至少补充一个平板宽度；
- 无白屏、错误覆盖层、横向溢出、遮挡、滚动陷阱和不可关闭 Dialog；
- 键盘焦点、Escape、关闭后焦点返回、减少动态效果均实测；
- 长表 10,000 行保持 DOM 有界且可操作；
- 浏览器 console 没有未解释的 error 或 warning；
- 每个主要流程至少保留开始、操作后、错误态和移动端截图。

用户端完成条件：上述项目全部 PASS，`USER-*`、相关 `AUTH-*`、`DATA-*`、`UX-*` 没有 PARTIAL、BLOCKED 或 NOT_VERIFIED。

## 7. 后台完成标准

### 7.1 组织、工作区、用户和角色

- 组织、工作区创建、查看、停用和恢复正确；
- 用户邀请、停用、启用、重置密码、重置 MFA 正确；
- 角色权限矩阵、作用范围、影响预览和版本冲突正确；
- 最后一个 Owner 不能被删除或撤销；
- 用户权限变更在规定时间内对现有会话生效；
- 所有跨组织和跨工作区操作均有负向测试。

### 7.2 会话、登录和审计

- 会话列表、设备、IP、风险、撤销和全部下线正确；
- 登录成功、失败、锁定、MFA、刷新、退出事件字段完整；
- 审计中心可按 actor、IP、设备、动作、资源、结果、request、trace 查询；
- 敏感读取、权限拒绝、Provider、任务、导出、报告和配置操作全部留痕；
- before/after、授权结果和资源范围完整；
- 审计应用账号不能 UPDATE、DELETE 或 TRUNCATE 审计链；
- 哈希链、每日锚点和封存事件边界均可复算。

### 7.3 任务、Provider 和运行控制

- 全局任务和七层诊断链可从任务追到 Shard、Checkpoint、Provider、对象和 Trace；
- Provider 连接测试、断开、路由、fallback、限流、熔断和密钥轮换正确；
- SAFE、FAST、TURBO 或实际采用的性能模式与运行时参数一致；
- 成本、预算、请求审计和超限动作正确；
- 告警认领、抑制、解决和关联对象正确；
- Kill Switch 完成影响预览、step-up、双人审批、执行和恢复；
- 功能开关必须在真实请求路径求值，不能只在后台页面试算；
- 配置更新使用版本控制和 `If-Match`，冲突不能静默覆盖。

### 7.4 数据治理和恢复

- Coverage、数据质量、存储、保留、归档和删除候选来自真实 API；
- 删除实际执行保持审批和安全开关控制；
- 备份列表、校验、恢复预览、恢复申请和演练结果可查询；
- 版本页显示真实 build SHA、迁移版本和组件兼容关系；
- 风险事件页面存在真实检测结果和处置流程；
- 通知渠道如果属于发布范围，必须完成 Email/Webhook 配置、测试和密钥摘要显示。

### 7.5 后台浏览器要求

- 管理员、安全管理员、审计员分别登录测试；
- 每个角色只看到允许的导航和按钮；
- 直接调用无权限接口仍必须由服务端拒绝；
- 复杂管理操作验证桌面端和平板端；
- 高风险操作统一使用 Dialog，明确展示影响、原因、step-up 和结果；
- 列表筛选、分页、排序、详情、刷新和错误重试均完成交互测试。

后台完成条件：`ADMIN-001` 至 `ADMIN-010`、后台相关 `RBAC-*`、`AUDIT-*`、`DATA-*` 全部 PASS，并且所有后台页面均连接真实 API，不存在发布范围内的占位页。

## 8. API 和契约完成标准

- `/api/auth/v1`、`/api/v2`、`/api/admin/v1` 与兼容 `/api/v1` 的实际路由已清点；
- 每个新增 API 都有 OpenAPI path、请求、响应、错误码和权限声明；
- OpenAPI 不能把已实现功能写成 501，也不能把未实现能力写成可用；
- TypeScript SDK 或当前采用的客户端生成物与 OpenAPI 同步；
- OpenAPI lint、生成、diff 检查进入 CI；
- 写请求使用 `Idempotency-Key`，版本更新使用 ETag/`If-Match`；
- 列表统一使用 cursor，不能因分页丢行或重复；
- 错误响应包含稳定 error code、request ID、trace ID；
- 未认证、越权、跨租户、CSRF、重放、非法输入和超限路径均有测试；
- 兼容 `/api/v1` 的读写权限、审计和下线计划已完成，不保留无声明的高风险路径。

建议执行：

```powershell
python scripts/check_openapi.py
python scripts/gen_openapi_index.py
git diff --exit-code -- openapi
```

如果脚本参数与仓库当前实现不同，以脚本 `--help` 为准，并把实际命令记录到证据目录。

## 9. 数据、迁移和文件完成标准

- PostgreSQL 从空库按顺序应用全部迁移成功；
- 在当前生产 schema 上重放迁移不产生破坏性结果；
- migration 版本、文件清单和数据库记录一致；
- scope 回填、约束、索引和分区均完成验证；
- API 运行账号为非超级用户，并通过 `SET ROLE blockchain_app` 或批准的等效方式降权；
- Migration 账号与运行账号分离；
- ClickHouse 查询限制、扫描预算和超限行为有效；
- MinIO 的 Raw、Evidence、Report、Export、Audit 对象均有存在性和哈希验证；
- PostgreSQL、ClickHouse、MinIO 的对象标识、行数、金额、时间和状态一致；
- 导出文件不是空壳，能够解析并抽样回查原始记录；
- 测试数据和生产真实数据严格隔离。

建议执行：

```powershell
Set-Location backend
go test ./...
Set-Location ..
bash scripts/test_migration_reentrancy.sh
```

Windows 本机如果不是目标架构，不得用 `windows/386` 的失败或通过代替 Linux/amd64 结果。正式后端结果必须来自与生产一致的 Linux/amd64 环境。

## 10. 安全完成标准

- P0=0、P1=0；
- 依赖扫描、Go 漏洞扫描、前端 audit、镜像扫描和 IaC 扫描全部针对当前冻结产物；
- 所有可达 Critical/High 漏洞已修复，或按批准规则确认不适用；
- 不允许用无责任人、无期限的“风险接受”关闭 P0/P1；
- SBOM 与实际二进制、前端 lockfile、容器 digest 一致；
- 镜像使用不可变 digest；
- Secret 不出现在代码、日志、审计、前端资源、构建产物和证据包；
- 客户端伪造 `X-User-*`、转发 IP、组织、工作区、角色均无效；
- CSRF、CSP、SSRF、开放重定向、IDOR、XSS、文件路径、压缩包和公式注入完成负向测试；
- 登录、权限拒绝、审计失败、outbox lag、Provider 异常和数据删除均有告警。

## 11. 性能完成标准

至少完成以下真实负载：

- 用户首页、后台首页、案件列表、任务列表、审计列表；
- Explorer 地址 activity、flow-profile、counterparties；
- 10,000 行浏览器长表；
- 2,000 节点图谱或系统规定上限；
- 百万行 CSV、XLSX、JSONL、Parquet、ZIP 导出；
- Provider 单调用与完整下载流水线；
- 内网、Nginx 入口和公网分层测试。

判定要求：

- PostgreSQL 常规 API 查询 P95 小于 200 ms，不包含公网网络时间；
- 前端缓存命中后的路由切换 P75 小于 1 秒；
- 超预算查询转为异步任务或返回明确、可操作的限制提示；
- 所有结果保留样本数、并发数、数据规模、P50/P95/P99、错误率和原始日志；
- 不能用健康检查或空数据请求代替真实业务负载。

## 12. 备份恢复完成标准

- PostgreSQL 定时备份已由 systemd timer 或批准的调度器实际运行；
- WAL/PITR 已启用并完成恢复演练；
- ClickHouse 全量和增量恢复通过；
- MinIO 所有发布范围内 bucket 均纳入备份，不遗漏 Raw、Evidence、Report、Export、Audit；
- NATS、API、Worker Kill-9 后能够按设计恢复；
- 恢复后 API、数据库、文件、审计链和关键业务流程重新验证；
- RPO、RTO 都达到计划值；
- 备份成功但恢复未验证，不能记 PASS；
- 恢复脚本必须验证实际恢复对象数量，不能出现 `compared=0` 仍判成功。

## 13. 生产配置和切流完成标准

切流前必须满足：

- `APP_ENV=production`；
- `AUTH_MODE=session` 或批准的等效生产认证方式；
- Cookie Secure、`__Host-`、SameSite 设置正确；
- API/Worker 使用受限数据库运行角色；
- Nginx 只暴露批准入口；
- 用户端 `/app-v2/` 和后台 `/admin/` 均使用当前冻结资源；
- 健康检查、日志、指标、告警和版本页显示当前 build SHA；
- 旧入口已进入只读观察，旧写路径关闭；
- 灰度阶段完成指定工作区、10%、50%、100% 观察；
- 每步核对错误率、权限拒绝差异、审计覆盖和用户任务成功率；
- 回滚脚本在当前版本上实际演练通过；
- 取得人工切流批准。

切流完成后至少观察一个完整业务周期，期间不得出现未解释的权限差异、审计缺失、数据不一致或持续错误率上升。

## 14. 最终执行顺序

### 14.1 冻结和静态检查

```powershell
git status --short
git rev-parse HEAD
git diff --check
```

记录已有修改，不得使用 reset、checkout 或清理命令覆盖现有工作。

### 14.2 后端测试

在部署机 Linux/amd64 环境执行：

```bash
cd /home/euripides/projects/blockchain-platform/backend
/usr/local/go/bin/go test -count=1 ./...
```

不能只使用缓存结果；最终轮至少执行一次 `-count=1`。

### 14.3 前端构建

```powershell
Set-Location frontend
pnpm run build:user
pnpm run build:admin
pnpm run check:tokens
```

### 14.4 API 和业务 E2E

在隔离验收数据上执行：

```bash
bash acceptance-evidence/dual-system-v1/tests/run-iam-e2e.sh
bash acceptance-evidence/dual-system-v1/tests/run-rbac-e2e.sh
bash acceptance-evidence/dual-system-v1/tests/run-sprint4-e2e.sh
bash acceptance-evidence/dual-system-v1/tests/run-sprint5-e2e.sh
bash acceptance-evidence/dual-system-v1/tests/run-sprint6-e2e.sh
bash acceptance-evidence/dual-system-v1/tests/run-sprint7-sec-negative.sh
bash acceptance-evidence/dual-system-v1/tests/run-sprint7-qa.sh
```

每个脚本必须输出结构化 result，且聚合器同时检查 `FAIL`、`BLOCKED`、`NOT_VERIFIED` 和退出码。

### 14.5 浏览器验收

```bash
export E2E_BASE_URL=https://127.0.0.1:8448
bash acceptance-evidence/dual-system-v1/tests/run-sprint7-ui.sh
```

最终轮必须包含：

- user-desktop；
- user-mobile；
- admin-desktop；
- admin-tablet 或批准的平板尺寸；
- cross-role；
- reduced-motion；
- 错误态和网络中断恢复。

### 14.6 性能、安全和恢复

```bash
bash scripts/perf/run-perf-suite.sh
bash scripts/backup/backup-all.sh
bash scripts/backup/restore-drill.sh
bash scripts/backup/kill9-drill.sh
```

秘密扫描、依赖、镜像、IaC、SBOM 使用仓库当前脚本执行，并记录工具版本、目标 digest、命令和原始输出。

### 14.7 线上核对

```bash
systemctl --user is-active blockchain-api.service blockchain-worker.service
curl -fsS http://127.0.0.1:8080/health/ready
sha256sum backend/bin/api backend/bin/worker
```

同时核对用户端、后台端静态资源哈希、数据库 migration、ClickHouse、MinIO、NATS、监控和告警。

## 15. 证据合格要求

每个 Test ID 至少包含：

```text
Test ID:
Release ID:
Build SHA:
Environment:
Executed at:
Preconditions:
Steps:
Expected:
Actual:
Status:
Request ID:
Trace ID:
Database evidence:
File evidence:
Runtime evidence:
Screenshot evidence:
Defect ID:
```

证据必须满足：

- 文件真实存在且非空；
- 时间在冻结版本部署之后；
- build SHA 与线上一致；
- 截图能看出页面身份和操作结果；
- API 证据保留状态码和响应体；
- 数据证据保留查询和结果；
- 文件证据保留格式解析、行数和 SHA-256；
- 运行证据保留服务、进程、二进制路径和日志；
- 失败轮次保留，不覆盖或伪装为成功；
- 聚合摘要必须由明细自动生成，不能手工把 NV 或 BLOCKED 排除后写 PASS。

## 16. 当前需要关闭的事项

根据 2026-09-16 检查，至少需要完成以下工作后才能重新申请验收：

1. 把线上 `APP_ENV` 从 `development` 切换为 `production`，并验证生产安全配置。
2. 为当前 9 月 16 日部署版本重新冻结 build SHA、二进制和两套前端资源哈希。
3. 重新执行完整 §27 测试，生成新的聚合摘要，保证 `FAIL=0`、`BLOCKED=0`、`NOT_VERIFIED=0`。
4. MFA 已实现并部署，需要更新 OpenAPI、验收矩阵、审计覆盖报告、用户手册和管理员手册，并用当前冻结版本复跑 AUTH-004。
5. 决定 OIDC 是否属于本次正式发布；属于则接真实 IdP 验收，不属于则同步修改需求和契约。
6. 复跑当前版本的用户移动端、后台平板端、cross-role 和 reduced-motion 浏览器项目。
7. 重新验证功能开关是否已经接入真实请求路径。
8. 重新验证 RPO/PITR、全 MinIO bucket、百万行导出和 Provider 完整流水线。
9. 对当前依赖、容器镜像和 IaC 重新扫描，关闭所有 P0/P1。
10. 更新 `section27-conformance.md`、`section31-deliverables.md`、`dual-system-progress.md` 和 `RELEASE-DECISION.md`，消除互相矛盾的旧结论。
11. 完成灰度观察、正式入口切换和回滚演练。
12. 由批准人核对证据后签署发布决定。

## 17. 发布决定

只有以下表格全部为 PASS，才能写 `GO`：

| 项目 | 要求结果 | 实际结果 | 证据 | 批准人 |
|---|---|---|---|---|
| 发布范围 | 全部闭合 |  |  |  |
| P0 | 0 |  |  |  |
| P1 | 0 |  |  |  |
| 自动化测试 | FAIL=0、BLOCKED=0、NV=0 |  |  |  |
| 用户端 | 全部 PASS |  |  |  |
| 后台端 | 全部 PASS |  |  |  |
| 权限和审计 | 全部 PASS |  |  |  |
| 数据和文件 | 全部 PASS |  |  |  |
| 安全 | 全部 PASS |  |  |  |
| 性能 | 全部达标 |  |  |  |
| RPO/RTO | 全部达标 |  |  |  |
| 生产配置 | 全部 PASS |  |  |  |
| 灰度和切流 | 全部 PASS |  |  |  |
| 回滚 | 实测 PASS |  |  |  |

最终结论只能填写以下之一：

```text
GO：全部硬性门禁通过，批准正式切流。
NO-GO：存在未关闭门禁，不允许正式切流。
```

只要表格中存在空白、PARTIAL、NOT_IMPLEMENTED、NOT_VERIFIED、NOT_RUN、BLOCKED 或 FAIL，最终结论必须为 `NO-GO`。
