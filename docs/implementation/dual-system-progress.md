# 双系统开发进度对照（对照 dual-system-development-plan.md 全量）

## 0. 2026-09-16 夜场（DR 演练 / 镜像扫描 / 回滚演练脚本）

同日晚间对 **备份—恢复链路与依赖镜像**做了真机演练与扫描，又暴露出 4 个真实工具缺陷（其中 2 个让 DR 门禁**结构性无法取证**），
逐条定位到行并修复；详细清单与证据见 `ROUND-20260916-defect-closure.md` §4。

- **D-25 恢复库"数据在、业务角色用不了"**：`restore-drill.sh` 用 `pg_restore --no-owner --no-privileges`，
  丢弃 GRANT 后运行时角色 `blockchain_app`（API/Worker 走 `SET ROLE`）读不到 `audit_events_v2`，演练被判 FAIL。
  已改为保留 ACL，并新增"运行时角色可用性"断言；修复后实测恢复库中 `blockchain_app` 读到 93,075 行（= manifest 备份点）。
- **D-26 备份缺 MinIO 语义基线**：`backup-all.sh` 从不调用 `minio-metadata-verify.sh capture`，
  导致 DR 门禁第 4 项（版本/ILM/标签/元数据）在任何备份上都无法取证。已在备份流程内新增 capture 步骤（失败即 `__INCOMPLETE`）。
- **D-27 kill9 `/jsz` 探针用错参数**：`/jsz?streams=EVENTS&consumers=1` 在 NATS 2.14.6 返回 HTTP 400，被写成"端点不可用"；
  改正后实测 `messages=477757, durable_consumers=0`（EVENTS 流没有 durable consumer，服务端不存在重投窗口）。
- **D-28 元数据维度无法单轮闭环**：新增 `RESTORE_DRILL_MINIO_META_VERIFY=1`，在**本轮目标桶**上就地取证并做 5 项交叉校验。

实测结果（冻结版本 `7831bff0…`）：

- **备份**：`20260916T150033Z`，39 GB / 177,899 个文件，`complete=true`、`pg_snapshot_consistent=true`。
- **恢复演练（首轮，脚本 `928c0a841c89…`）**：manifest 177,899/177,899 全对；PG `pg_restore` 退出码 0、**RTO 58.83 s**；
  CH `RESTORE` 退出码 0、**RTO 477.02 s**，四张大表 `count(FINAL)` 与生产**完全一致**（transactions 24,962,612、token_transfers 67,644,164、
  address_activity 75,229,670、logs 9,473,112）；MinIO **5 桶逐对象比对零差异**（blockchain-raw 177,347 个对象，RTO 280.01 s）；
  判定 FAIL 的唯一原因是 D-25（已修）。
- **恢复演练（次轮，脚本 `f74540ad…`）**：验证 D-25 修复生效（运行时角色可读恢复库），其余同上。
- **镜像漏洞扫描**（`scripts/s7/run-s7-scan-image-iac.sh`，本地漏洞库 `UpdatedAt=2026-09-16T13:02:23Z`）：
  `postgres:18` 14 CRITICAL/106 HIGH、`minio/minio:latest` 6/100、`minio/mc:latest` 2/45、`nats:2-alpine` 0/2、
  `clickhouse/clickhouse-server:26.7` 0/0 —— 合计 **22 CRITICAL / 253 HIGH（可修 19 / 188）**，属发布阻断 B-7 的实测依据。
- **IaC**：trivy `config` 对本仓库 IaC 面检出配置文件数 **0**（k8s 对照样本可正常报 3 条），因此**不主张** IaC 已扫描通过。
- **回滚演练脚本**：新增 `scripts/release/rollback-drill.sh`（`ba3f5662…`），真实回滚 API/Worker 与两套前端并测 RTO，
  带 trap 保证异常退出也回到冻结版本；待窗口内执行。

> 结论不变：**NO-GO for cutover**。§27 矩阵仍为 **63 PASS + 2 发布阻断**（DATA-007、DATA-009），
> 其余发布门禁（B-2/B-3/B-4/B-5/B-6/B-7）逐条状态见 `section27-conformance.md` §4 与 `RELEASE-WINDOW-REQUEST-20260916.md`。

---

## 0.1 2026-09-16 日场（缺陷关闭 + 稀疏声明）

本轮对照 `docs/dual-system-completion-acceptance-criteria.md` 复核并关闭了两类**真实缺陷**，
同时修掉了让套件"整体红"的夹具/编排阻断。完整清单与证据见
`acceptance-evidence/dual-system-v1/ROUND-20260916-defect-closure.md`。

- **D-01 地址搜索永远返回空**：搜索对 `transactions`/`token_transfers` 用 `FINAL` 且无 `chain` 谓词
  → 实测 80 s 超时，错误被 `if err == nil` 吞掉，于是"超时"表现为"查不到"。已按地址形态限定链、
  去掉 `FINAL`、去重下沉到聚合层；同一地址由"0 结果"变为 0.37 s 返回 `tron · 17431 笔`。
- **D-02 后台创建用户 100% 失败**：`sprint6_identity.go` 查询用了 `workspace_memberships.id`
  （该表没有 id 列），语句失败使请求事务 aborted，提交门再把结果改写成误导性的
  `409 AUTHZ_WORKSPACE_SCOPE_STALE`。已修 SQL 并区分"没有成员关系"与"查询失败"；
  提交门新增 `handler_status` 诊断字段（D-03）。
- **D-04/D-05 审计链不可验证**：写入侧把不可落库的 UUID 文本放进哈希载荷（`audit.UUIDPayloadValue`
  已修）；两条既有行与登录链 8 处稀疏空洞按 **ADR-019 稀疏声明**逐条登记（不改写任何历史行）。
  两条链的 `valid` 由 `false` 变为 `true`，并如实回报 `declared_anomaly_count=2` /
  `declared_lost_count=8`。
- **D-06 尾部缺失检查的读竞态**（本轮自查发现并修复）。
- **D-07 夹具与编排四处阻断**：`admin-fixture`/`admin-init` 用受限应用账号、多工作区身份登录未带
  `workspace_id`、运行日志套件读 `/tmp/api.log` 而 API 写在 journald、声明类套件缺 `PGPASS`。
  修完后 sprint4 由 `PASS=0` 变为 `PASS=81 / FAIL=0`。

- **D-15 evidence 状态命令固定卡 60 s 且丢一条审计**（本轮实测发现，P0 可用性 + 审计完整性）：
  提交门把整个 handler 放进一个事务，请求内先写的同事务审计（`evidence:review`）拿到了
  `audit_chain_heads` 行锁，随后 `GetEvidence` 的读审计（`evidence:sensitive:read`）又另开一条连接去取
  同一把锁 —— 请求与自己的事务互等，60.0 s 后被 HTTP 超时解开，该审计行被静默丢弃。
  修复见 **ADR-020**：gate 请求内的审计写入加入请求事务（`dbtx.WithGate`），
  `WithoutTx` 同时清除标记（中间件拒绝行语义不变），读路径审计失败改为 `WriteOrLog` 不再静默。
  实测 `verify` **60.011 s → 0.046 s**，同请求恢复记录两条审计，两条哈希链仍 `valid=true`。

- **D-17 两套控制台导航未按权限裁剪**（§7.5）：后台 `navGroups()` 是静态列表、用户端 `navGroups(permissions)`
  收了权限参数却没用（`ROUTE_PERMISSIONS` 定义了从未消费）→ 低权限角色仍看到打不开的入口。
  已改为"入口权限从路由表反查、两套前端同源过滤"，并修掉 `OrganizationsPage` 把次要资源 403 当整页失败的写法；
  新增双向浏览器用例 `dual-system-nav-permission.spec.ts`（Owner 基线 + Platform Admin/Auditor 逐路径对照），实测 4/0。
- **D-18 系统角色「有写无读」权限缺口**（§7.1/§7.4）：Platform Admin 能创建/审批删除请求却列不出来
  （缺 `admin:deletion:read`），Security Admin 能处置告警与审批却列不出来（缺 `admin:alert:read`、`admin:approval:read`）。
  迁移 `0065_role_read_permission_gaps.sql` 按通用规则补齐并自检；补后双向导航用例转绿。

- **D-19 功能开关没有接入真实请求路径**（§7.3 / §16.7）：平台有完整开关后台能力，但没有任何业务请求读过
  `feature_flags`（关掉 `user.explorer_v2` 后 Explorer 照样返回数据）。已按 ADR-021 接线三条能力面
  （Explorer 读端点、后台告警中心、控制面只读视图），关闭时返回 `503 FEATURE_DISABLED`，真实流量求值写入
  `feature_flag_evaluations`；Kill Switch 等安全/恢复路径**不挂开关**（契约测试静态断言）。
  实测套件 `run-sprint7-featureflags.sh`：PASS 21 / FAIL 0。

> **发布结论未变**：`APP_ENV` 仍为 `development`、OIDC 范围未决、功能开关未接入请求路径、
> 百万行导出/DR/重扫与灰度切流未执行、无批准人签署 → 仍为 **NO-GO for cutover**。

---

> 结论：**Sprint 1 / 2 / 3 / 4 / 5 / 6 均已完成**（Sprint 6 见 `docs/implementation/sprint6-report.md`）。
> **Sprint 7 已于 2026-09-13 执行完毕**（见 `docs/implementation/sprint7-report.md`）：
> DB 硬化（0056/0057）、安全取证与 6 项真实缺陷修复、分层性能、备份恢复与 Kill -9 演练、§27 全量复跑、手册全部完成。
>
> **2026-09-16 晚更新（取代上面这几行里的旧数字）**：§27 矩阵已按冻结版本 `7831bff0…` 完整重建
> （`acceptance-evidence/dual-system-v1/section27-conformance.md`）：65 个 Test ID → **63 PASS + 2 发布阻断**，
> 矩阵内 `FAIL=0 / BLOCKED=0 / NOT_VERIFIED=0 / PARTIAL=0`。旧的"浏览器 4 条 FAIL"与"6 条 P1"表述已不适用：
> 浏览器主档位 37/0、扩展档位（含 user-mobile）57/0。
> **结论：未达结项条件**——发布决策仍为 **NO-GO for cutover**（保持灰度、不切换 `/` 入口）；
> 当前阻断项为矩阵 §4 的 B-1…B-6（DATA-007 订阅者幂等、DATA-009 恢复演练、§11 性能预算、RPO/PITR、
> 回滚门禁、production 切换/灰度/签署），逐条关闭条件见 `RELEASE-DECISION.md` 与矩阵 §4。
> 发布范围决定见 `docs/implementation/release-scope-decision-20260916.md`（OIDC 与三个未接线开关均不在本次范围）。
> Sprint 6 的审计修复阻断项已闭环：不可恢复缺口的**不可变完整性事件边界**已实现并部署，
> AUDIT-004 不再有 NOT_VERIFIED 项（见 `docs/adr/ADR-018-audit-integrity-incident-boundary.md`
> 与该报告 §6bis）。
>
> - Sprint 2 的 OIDC 已完成 adapter + httptest 隔离测试（AUTH-011 协议负向覆盖）；
>   没有真实 IdP，`/api/auth/v1/methods` 仍返回 `oidc:false`，`/oidc/*` 返回 501，
>   **live IdP 验收标记 NOT_VERIFIED**。MFA/TOTP 仍为 NOT_IMPLEMENTED。
> - Sprint 4 案件与调查闭环：Investigator → Reviewer → approved report；
>   API E2E PASS 79 / FAIL 0，Playwright 桌面 + 移动端 PASS 4 / FAIL 0。
> - Sprint 5 Explorer/下载/任务/导出/图谱/后台诊断已完成：
>   API E2E PASS 104 / FAIL 0 / NOT_VERIFIED 0；Playwright 适用用例 14 PASS / 0 FAIL
>   （13 个跨项目守卫 skip）；S1–S4 回归全绿。详见 `docs/implementation/sprint5-report.md`。
> - RBAC-007（Canvas 跨案件 Evidence）与 RBAC-004（无 evidence 权限读取正文）
>   已由 S5 套件实际验证，不再是 BLOCKED。
> - Sprint 6：API E2E PASS 152 / FAIL 0 / **NOT_VERIFIED 0**；Playwright 8 PASS / 0 FAIL；
>   ADMIN-001..010 全部通过；AUDIT-003 用例暴露并修复了真实 append-only 缺口（TRUNCATE），
>   详见 `docs/implementation/sprint6-report.md` §6。
>   套件状态判定已同时要求 FAIL=0 与 NOT_VERIFIED=0（`run-sprint6-e2e.sh` 的 result.json
>   写入 `criteria: fail==0 and not_verified==0 and pass>0`），不再存在"有未验证项仍报 PASS"的路径。
>
> 生成时间：2026-09-12 · 分支 `feat/dual-system-iam-foundation`
> 代码基线：`2131c8c`、`6cdc318`、`f781c56`、`3e87311`、`3771763`、`e9638b6`、`e639707`（S5）
> S6 审计修复阻断项部署提交：`7dba1eb`（`bin/api` `59ac3cfd…`；见 `tests/sprint6/COMMITS.md`）
> Sprint 6 基线：见 `docs/implementation/sprint6-report.md` 头部与 commit 记录

## 1. Sprint 任务完成度

| Sprint | 主题 | 任务数 | 完成 | 部分 | 未开始 | 完成度 |
|---|---|---|---|---|---|---|
| S1 | 基线、契约、双应用骨架 | 8 | 7 | 1 | 0 | ~95% |
| S2 | IAM、Session、登录审计 | 8 | 6 | 2 | 0 | ~85%（OIDC adapter 完成、live IdP 未验） |
| S3 | RBAC、租户与审计 v2 | 8 | 8 | 0 | 0 | **100%** |
| S4 | 用户案件与调查闭环 | 8 | 8 | 0 | 0 | **100%** |
| S5 | Explorer、下载、任务、导出、图谱 | 8 | 8 | 0 | 0 | **100%** |
| S6 | 后台控制、治理与可观测性 | 9 | 9 | 0 | 0 | **100%**（含审计修复阻断项：缺口已封存，NOT_VERIFIED 0） |
| S7 | 硬化、真实数据验收与切流 | 8 | 7 | 1 | 0 | ~90%（7 任务完成；切流受发布门禁阻断，见 `sprint7-report.md`） |

### Sprint 1（摘要）
| 任务 | 状态 | 缺口 |
|---|---|---|
| S1-ARCH-01 | 完成 | AD-001 ～ AD-016 |
| S1-API-01 | **部分** | 契约与双向一致性检查完成；TS 代码生成与“生成无 diff”检查仍未做 |
| S1-FE-01 | 完成 | 双应用 + 共享包，独立构建通过 |
| S1-FE-02 | 完成 | tokens / AppShell / PageHeader / Dialog / DataTable 等 |
| S1-FE-03 | **部分** | 未按文档搭 fixture server；页面全部来自真实 API |
| S1-BE-01 | 完成 | 路由分区 + legacy 包装 |
| S1-QA-01 | 完成 | user/admin/cross-role/visual 四个 project |
| S1-OPS-01 | **部分** | systemd + 静态产物；未单独建测试环境 |

### Sprint 2（摘要）
| 任务 | 状态 | 缺口 |
|---|---|---|
| S2-DB-01 | 完成 | 0040 迁移 + 索引 + 回填 |
| S2-BE-01 | **部分** | OIDC adapter/隔离测试完成，无真实 IdP；MFA 仍 501 |
| S2-BE-02/03 | 完成 | 可信代理/设备/登录事件；user/admin 双受众会话 |
| S2-FE-01 | **部分** | MFA 页未实现 |
| S2-ADMIN-01 | 完成 | 会话列表 + 登录记录 |
| S2-SEC-01 | **部分** | AUTH-004 NOT_IMPLEMENTED；AUTH-011 live NOT_VERIFIED |
| S2-MIG-01 | 完成 | `cmd/admin-init`，不写默认密码 |

### Sprint 3（摘要）
| 任务 | 状态 | 缺口 |
|---|---|---|
| S3-DB-01 | 完成 | 0041/0042（163 权限、9 角色、审计 v2） |
| S3-BE-01/02/03 | 完成 | Guard/Tenant/Policy；审计 v2 哈希链/Outbox/锚点；租户作用域 |
| S3-ADMIN-01/02 | 完成 | 组织/工作区/成员角色；审计列表/详情/登录关联 |
| S3-QA-01 | 完成 | PASS 29 / FAIL 0 / BLOCKED 2；RBAC-004/007 已在 S5 套件转 PASS |
| S3-OBS-01 | 完成 | authz/audit/outbox/tenant 指标 + 7 条告警 |

### Sprint 4（摘要）
| 任务 | 状态 | 交付与证据 |
|---|---|---|
| S4-DB-01 | 完成 | 0047/0048 迁移与权限补齐（ADR-016） |
| S4-BE-01 | 完成 | `/api/v2/cases` 全生命周期 + audit v2 + case_activity |
| S4-BE-02 | 完成 | Investigation v2 compatibility（Coverage 命中本地复用） |
| S4-BE-03 | 完成 | Finding/Evidence 复核与不可变 supersede 链 |
| S4-BE-04 | 完成 | 报告 revision/审批/签署/归档与一次性下载票据 |
| S4-FE-01/02/03 | 完成 | 案件中心、调查工作台、报告/Inbox；桌面+移动通过 |
| S4-QA-01 | 完成 | PASS 79 / FAIL 0 / NOT_VERIFIED 0；Playwright 4/4 |

### Sprint 5（本轮）
| 任务 | 状态 | 交付与证据 |
|---|---|---|
| S5-BE-01 | 完成 | Explorer v2 cursor/来源/口径 envelope；overview/assets/activity/counterparties/semantic/flow-profile；交易详情/Logs/local Trace；全类型搜索 |
| S5-BE-02 | 完成 | downloads preview/create；默认 `prefer_local`；显式 `refresh`（跳过 Ensure 与 Planner Coverage 短路）；用户 scope（0049/0050） |
| S5-BE-03 | 完成 | jobs v2 cursor/详情/控制；SSE Last-Event-ID 重放；exports v2 + 60 秒一次性票据 + HEAD/Range |
| S5-FE-01 | 完成 | 全局搜索、Explorer、地址详情 9 Tab、交易详情 3 Tab；桌面+移动 Playwright PASS |
| S5-FE-02 | 完成 | 下载表单、Jobs、Job Detail 三层进度、Exports 中心；SSE 实测连接 |
| S5-FE-03 | 完成 | Fund Flow SVG、Lens、Evidence Rail、Canvas revision 412+字段级 diff、移动列表视图 |
| S5-ADMIN-01 | 完成 | 跨组织任务列表 + 固定七层诊断链（DownloadJob→…→ClickHouseCoverage） |
| S5-QA-01 | 完成 | `run-sprint5-e2e.sh`：PASS 104 / FAIL 0 / NOT_VERIFIED 0（build_sha=e639707）；Playwright 14 PASS / 0 FAIL / 13 project skip；S1–S4 回归全绿 |

## 2. §27 测试计划完成度（Sprint 5 后）

65 个 Test ID：**PASS 51 / PARTIAL 2 / NOT_RUN 11 / NOT_IMPLEMENTED 1**
（权威对照见 `acceptance-evidence/dual-system-v1/section27-conformance.md`）。

- Sprint 5 转 PASS：USER-002、USER-003、USER-007、USER-009、USER-011、USER-012、
  DATA-001 至 DATA-008、UX-004；RBAC-004、RBAC-007 也在 S5 套件转 PASS。
- 仍 PARTIAL：AUTH-011（无真实 IdP，live NOT_VERIFIED）、RBAC-008（API Key/scope 路径未覆盖）。
- 仍 NOT_IMPLEMENTED：AUTH-004（MFA/TOTP）。
- 仍 NOT_RUN：ADMIN-001 至 ADMIN-010、DATA-009（S6/S7）。
- 本轮实测回归：sprint4 79/0/0；sprint5 104/0/0；rbac 29/0/2（BLOCKED 历史标注）；
  sprint12 19/0/1；iam 32/0；quality 27/27。

## 3. §31 最终交付物清单完成度

| 类别 | 条目 | 状态 |
|---|---|---|
| 代码 | 两套前端 + 共享包 | 完成 |
| 代码 | IAM / Policy / Audit 模块 | 完成（OIDC adapter 完成，live IdP 未验；MFA 未实现） |
| 代码 | Tenancy / Casework / Notification / Explorer / Download / Export / Canvas | 完成 |
| 代码 | `/api/auth/v1`、`/api/v2`、`/api/admin/v1`、`/api/v1` | 完成（S5 v2 Explorer/下载/任务/导出/Canvas/后台任务） |
| 代码 | 0040–0050 迁移与校验脚本 | 完成（0049/0050 为本轮 S5） |
| 代码 | Nginx / systemd / 回滚脚本 | 完成；monitoring 配置未做 |
| 文档 | ADR、权限目录、OpenAPI | 索引级 OpenAPI 已同步生成并通过一致性检查；完整响应模型仍未做 |
| 文档 | 用户手册 / 管理员手册 / 审计员手册 | **未完成**（只有运维 Runbook） |
| 文档 | 部署/升级/回滚 Runbook | 部署与回滚完成；升级/密钥轮换/备份恢复未写 |
| 文档 | 能力边界表 | 完成（sprint5-report + 本文件） |
| 证据 | S5 API/UI/DB/文件/运行态证据 | 完成（tests/sprint5/） |
| 证据 | 权限覆盖报告 / 审计覆盖报告 | 审计覆盖完成；权限覆盖报告未生成 |
| 证据 | 秘密扫描 / SBOM / 漏洞报告 | **未完成**（S7） |
| 证据 | 性能分层结果 / 容量预测 / RPO-RTO 演练 | **未完成**（S7） |
| 证据 | `RELEASE-DECISION.md` | **未完成**（S7） |

### Sprint 6（本轮）
| 任务 | 状态 | 交付与证据 |
|---|---|---|
| S6-DB-01 | 完成 | 0051/0052/0053/0054；一次性库 0001→0053 全量 + 重放幂等验证 |
| S6-BE-01 | 完成 | `/api/admin/v1` 控制面（control/kill-switch/approvals/performance/身份补齐），统一权限 + preview/command + If-Match + step-up + 审计 |
| S6-BE-02 | 完成 | Provider 描述符/连接测试/断开影响/密钥轮换（只存摘要）、路由策略与历史、请求审计、成本与预算 |
| S6-BE-03 | 完成 | 告警生命周期（含六层关联链）、功能开关（确定性灰度 + 评估记录）、配置版本（diff/rollback） |
| S6-BE-04 | 完成 | 保留预览、删除请求双人审批（执行默认关闭 412）、配置快照备份与校验、恢复预览/请求、审计导出（manifest+SHA-256+回读校验） |
| S6-FE-01 | 完成 | Provider/详情/路由/性能/API Key、请求审计、成本、预算页面 |
| S6-FE-02 | 完成 | 告警、控制面、Kill Switch（四步双人流程）、配置、功能开关、备份恢复、保留、版本页面 |
| S6-OBS-01 | 完成 | `/observability` 总览/SLO/告警/指标 + §25.1 结构化日志字段 + 12 个新指标 |
| S6-QA-01 | 完成 | `run-sprint6-e2e.sh` PASS 152 / FAIL 0 / NOT_VERIFIED 0；Playwright 8 PASS / 0 FAIL；ADMIN-001..010 全通过；AUDIT-003/004 覆盖边界破坏性操作拒绝、缺口封存与边界之后逐项验证 |
| S6-AUDIT-FIX-01 | 完成 | 不可恢复缺口的不可变完整性事件边界（迁移 0055 + ADR-018）：缺口检测/双人审批封存/边界后链与锚点逐项验证/运行角色对边界证据全破坏性操作拒绝 |

## 4. 下一步（进入 S7）

1. **S7 必须优先处理**：应用账号与 owner/超级用户的彻底分离（应用账号目前仍是 owner，
   ADR-017 §5 / S7-SEC-01）、PG/CH/MinIO 物理备份与 RPO/RTO 演练。
   （原「AUDIT-004 的链边界方案」已由 Sprint 6 审计修复阻断项完成，见 ADR-018。）
2. **S7 常规**：威胁矩阵/SBOM/依赖扫描、性能分层、全 §27、灰度与切流、手册与 RELEASE-DECISION。
3. **S2 遗留**：MFA/TOTP；真实 IdP 接入后的 AUTH-011 live 验收。S6 的 step-up 为密码重认证（ADR-017）。
4. **S6 遗留**：A-DATA-02/03（数据质量/存储）、A-RISK-01（风险事件）仍为显式占位页；
   功能开关尚未接入请求路径自动评估。