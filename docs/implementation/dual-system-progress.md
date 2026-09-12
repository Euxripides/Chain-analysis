# 双系统开发进度对照（对照 dual-system-development-plan.md 全量）

> 结论：**Sprint 1 / 2 / 3 / 4 已完成**。
>
> - Sprint 2 的 OIDC 已完成 adapter + httptest 隔离测试（AUTH-011 协议负向覆盖）；
>   没有真实 IdP，`/api/auth/v1/methods` 仍返回 `oidc:false`，`/oidc/*` 返回 501，
>   **live IdP 验收标记 NOT_VERIFIED**，未伪称通过。MFA/TOTP 仍为 NOT_IMPLEMENTED。
> - Sprint 3 的 RBAC-007 因被测端点属 S5 而 BLOCKED。
> - Sprint 4 案件与调查闭环已形成真实垂直路径：Investigator → Reviewer → approved report；
>   API E2E PASS 79 / FAIL 0，Playwright 桌面 + 移动端 PASS 4 / FAIL 0。
> - Sprint 5 未开始，不进入 S5 实施。
>
> 生成时间：2026-09-12 · 分支 `feat/dual-system-iam-foundation`
> 代码基线：至少 2131c8c、6cdc318、f781c56 + 本 Sprint 提交

## 1. Sprint 任务完成度

文档 §28.3–28.9 共定义可执行任务。本表按 Sprint 归类统计：

| Sprint | 主题 | 任务数 | 完成 | 部分 | 未开始 | 完成度 |
|---|---|---|---|---|---|---|
| S1 | 基线、契约、双应用骨架 | 8 | 7 | 1 | 0 | ~95% |
| S2 | IAM、Session、登录审计 | 8 | 6 | 2 | 0 | ~85%（OIDC adapter 完成、live IdP 未验） |
| S3 | RBAC、租户与审计 v2 | 8 | 8 | 0 | 0 | **100%**（RBAC-007 被测端点属 S5） |
| S4 | 用户案件与调查闭环 | 8 | 8 | 0 | 0 | **100%**（见下方说明） |
| S5 | Explorer、下载、任务、导出、图谱 | 8 | 0 | 0 | 8 | 0% |
| S6 | 后台控制、治理与可观测性 | 9 | 0 | 0 | 9 | 0% |
| S7 | 硬化、真实数据验收与切流 | 8 | 0 | 0 | 8 | 0% |

### Sprint 1
| 任务 | 状态 | 缺口 |
|---|---|---|
| S1-ARCH-01 | 完成 | AD-001 ～ AD-016 |
| S1-API-01 | **部分** | 契约与双向一致性检查完成；TS 代码生成与“生成无 diff”检查仍未做 |
| S1-FE-01 | 完成 | 双应用 + 3 个共享包，独立构建通过 |
| S1-FE-02 | 完成 | tokens / AppShell / PageHeader / Dialog / DataTable 等 |
| S1-FE-03 | **部分** | 未按文档搭 fixture server；页面全部来自真实 API |
| S1-BE-01 | 完成 | 路由分区 + legacy 包装 |
| S1-QA-01 | 完成 | user/admin/cross-role/visual 四个 project |
| S1-OPS-01 | **部分** | systemd + 静态产物；未单独建测试环境 |

### Sprint 2
| 任务 | 状态 | 缺口 |
|---|---|---|
| S2-DB-01 | 完成 | 0040 迁移 + 索引 + 回填 |
| S2-BE-01 | **部分** | 本地凭据/会话/Cookie/CSRF/令牌族完成；OIDC adapter 与隔离测试完成，但无真实 IdP，live 验收 NOT_VERIFIED；MFA 仍 501 |
| S2-BE-02 | 完成 | 可信代理解析、设备解析、登录事件 |
| S2-BE-03 | 完成 | user/admin 双受众会话 |
| S2-FE-01 | **部分** | 登录/退出/过期/个人安全页完成；MFA 页未实现；OIDC 按钮只在 methods 声明可用时出现 |
| S2-ADMIN-01 | 完成 | 会话列表 + 登录记录 |
| S2-SEC-01 | **部分** | AUTH-001..003/005..010/012 已通过；AUTH-004(MFA) NOT_IMPLEMENTED；AUTH-011 协议单测通过、live NOT_VERIFIED |
| S2-MIG-01 | 完成 | `cmd/admin-init`，不写默认密码 |

### Sprint 3
| 任务 | 状态 | 缺口 |
|---|---|---|
| S3-DB-01 | 完成 | 0041/0042（163 权限、9 角色、审计 v2） |
| S3-BE-01 | 完成 | access.Guard + TenantContext + PolicyEngine + 路由声明 |
| S3-BE-02 | 完成 | 审计 v2 + 哈希链 + Outbox + 定时校验器 + MinIO 每日锚点 |
| S3-BE-03 | 完成 | 0044 租户作用域收敛 + 复合索引 + 数据资产登记 |
| S3-ADMIN-01 | 完成 | 组织/工作区/成员角色页面 + API |
| S3-ADMIN-02 | 完成 | 审计列表/详情/登录关联 |
| S3-QA-01 | 完成 | RBAC-001..008：PASS 29 / FAIL 0 / BLOCKED 2（被测端点属 S4/S5） |
| S3-OBS-01 | 完成 | authz/audit/outbox/tenant 指标 + 7 条告警 |

### Sprint 4
| 任务 | 状态 | 交付与证据 |
|---|---|---|
| S4-DB-01 | 完成 | `0047_case_collaboration.sql`（案件协作/复核/审批/通知/票据 + 权限补齐）、`0048_s4_permission_fix.sql`（viewer notification:list）；迁移编号偏差见 ADR-016 |
| S4-BE-01 | 完成 | `/api/v2/cases` 创建/详情/版本化 PATCH/状态命令/成员/对象/动态/评论；全部事务内写 audit v2 + case_activity |
| S4-BE-02 | 完成 | Investigation v2 compatibility：创建/预览/Coverage 命中走本地索引复用/start/pause/resume/cancel/retry/submit-review/runtime；后台失败落库可见 |
| S4-BE-03 | 完成 | Finding/Evidence 复核、不可变版本与 supersede 链、verified evidence 前置规则、finding_evidence_links 关联 |
| S4-BE-04 | 完成 | 报告 revision/审批/签署/归档、report_approvals、60 秒一次性下载票据、ETag/SHA-256 校验、下载审计 |
| S4-FE-01 | 完成 | 案件列表/新建/概览 + 对象/调查/证据/报告/动态 Tab + 成员管理；桌面/移动真实交互通过 |
| S4-FE-02 | 完成 | 调查列表/新建预览/工作台阶段步骤/Findings 复核/Evidence 详情与 Rail |
| S4-FE-03 | 完成 | 报告中心、报告详情审批状态机、Inbox、邀请通知；所有提示均走 Dialog |
| S4-QA-01 | 完成 | `run-sprint4-e2e.sh`：PASS 79 / FAIL 0 / NOT_VERIFIED 0；Playwright 桌面+移动 PASS 4 / FAIL 0 |

## 2. §27 测试计划完成度

65 个 Test ID：**PASS 36 / PARTIAL 5 / NOT_RUN 12 / NOT_IMPLEMENTED 12**（Sprint 4 后，权威对照见
`acceptance-evidence/dual-system-v1/section27-conformance.md`）。

Sprint 4 转 PASS：USER-001、USER-004、USER-005、USER-006、USER-008、USER-009、USER-010。
USER-007 的 revision 冲突在 S4 范围（案件 PATCH / 报告 status_revision）已实测 412；
Canvas 并发保存与 diff UI 属 S5，仍标 PARTIAL。
AUDIT-001 的 S4 用户操作清单已逐项验证（审计链、关键动作、Trace、object_denied）。

## 3. §31 最终交付物清单完成度

| 类别 | 条目 | 状态 |
|---|---|---|
| 代码 | 两套前端 + 共享包 | 完成 |
| 代码 | IAM / Policy / Audit 模块 | 完成（OIDC adapter 完成，live IdP 未验；MFA 未实现） |
| 代码 | Tenancy / Casework / Notification 模块 | casework + 通知完成；tenancy 在 S3 完成 |
| 代码 | `/api/auth/v1`、`/api/v2`、`/api/admin/v1`、`/api/v1` | 完成（S4 v2 案件/调查/复核/报告/通知） |
| 代码 | 0040–0048 迁移与校验脚本 | 完成（0049+ 留给 S5–S7） |
| 代码 | Nginx / systemd / 回滚脚本 | 完成；monitoring 配置未做 |
| 文档 | ADR、权限目录 | 完成（+ ADR-016） |
| 文档 | OpenAPI、事件目录、数据字典 | **未完成**（v2/admin 仍是索引级） |
| 文档 | 用户手册 / 管理员手册 / 审计员手册 | **未完成**（只有运维 Runbook） |
| 文档 | 部署/升级/回滚 Runbook | 部署与回滚完成；升级/密钥轮换/备份恢复未写 |
| 文档 | 能力边界表 | 完成（final-report §5 + 本文件） |
| 证据 | 全部 Test ID 结果 | 完成对照表（仍有 NOT_RUN） |
| 证据 | 权限覆盖报告 / 审计覆盖报告 | 审计覆盖完成；权限覆盖报告未生成 |
| 证据 | 秘密扫描 / SBOM / 漏洞报告 | **未完成** |
| 证据 | 性能分层结果 / 容量预测 / RPO-RTO 演练 | **未完成** |
| 证据 | `RELEASE-DECISION.md` | **未完成** |

## 4. 下一步（不进入 S5 实施，仅登记）

1. **S5**：Explorer v2、download preview/create、jobs v2/SSE、exports、Fund Flow + Evidence Rail；
   届时补 USER-002/003/007/011/012、DATA-001..008。
2. **S2 遗留**：MFA/TOTP；真实 IdP 接入后的 AUTH-011 live 验收。
3. **治理**：S1-API-01 TS 生成、测试夹具保留到 S7 后清理、性能与恢复演练。
