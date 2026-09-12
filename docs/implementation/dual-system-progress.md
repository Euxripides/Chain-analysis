# 双系统开发进度对照（对照 dual-system-development-plan.md 全量）

> 结论：**Sprint 1 / 2 / 3 已完成**（Sprint 2 的 OIDC 与 MFA 明确为 NOT_IMPLEMENTED，
> Sprint 3 的 RBAC-007 因被测端点属 S5 而 BLOCKED）。Sprint 4–7 未开始，
> 整体约为计划工作量的 **30%**。
>
> 2026-09-12 追加：Sprint 1/2 的遗留**验收**已补齐（AUTH-003/005/006/007/008、
> SEC-002/003/004、UX-002/003、DATA-010、AUDIT-002 全部转 PASS）。
> 补齐过程本身又发现并修复了 6 个真实缺陷，其中"会话令牌族缺失"与
> "限流器 && 短路"属安全实现错误。
> 生成时间：2026-09-12 · 分支 `feat/dual-system-iam-foundation`

## 1. Sprint 任务完成度

文档 §28.3–28.9 共定义 **59 个任务 ID**。

| Sprint | 主题 | 任务数 | 完成 | 部分 | 未开始 | 完成度 |
|---|---|---|---|---|---|---|
| S1 | 基线、契约、双应用骨架 | 8 | 7 | 1 | 0 | ~95% |
| S2 | IAM、Session、登录审计 | 8 | 6 | 2 | 0 | ~85% |
| S3 | RBAC、租户与审计 v2 | 8 | 8 | 0 | 0 | **100%**（RBAC-007 的被测端点属 S5） |
| S4 | 用户案件与调查闭环 | 7 | 0 | 0 | 7 | 0% |
| S5 | Explorer、下载、任务、导出、图谱 | 8 | 0 | 0 | 8 | 0% |
| S6 | 后台控制、治理与可观测性 | 9 | 0 | 0 | 9 | 0% |
| S7 | 硬化、真实数据验收与切流 | 8 | 0 | 0 | 8 | 0% |
| **合计** | | **56\*** | **12** | **9** | **35** | **~20%** |

\* 表中 56 为按 Sprint 归类的可执行任务数；文档中另有 S3-OBS-01 等跨 Sprint 观测项已计入。

逐项状态：

### Sprint 1
| 任务 | 状态 | 缺口 |
|---|---|---|
| S1-ARCH-01 | 完成 | AD-001 ～ AD-010 |
| S1-API-01 | **部分** | 已交付 `openapi/{auth-v1,user-v2,admin-v1}.yaml` + `common/*` + `scripts/check_openapi.py`（契约↔路由双向一致性、错误模型完整性、错误码登记）。**缺 TS 代码生成与"生成无 diff"检查**，且 user-v2/admin-v1 目前是索引级契约（响应体模型待补） |
| S1-FE-01 | 完成 | 双应用 + 3 个共享包，独立构建通过 |
| S1-FE-02 | 完成 | tokens / AppShell / PageHeader / Dialog / DataTable / EntityLink / Amount / 状态组件 |
| S1-FE-03 | **部分** | 页面未硬编码数据（更好），但未按文档搭 fixture server |
| S1-BE-01 | 完成 | 路由分区 + legacy 包装 |
| S1-QA-01 | 完成 | user/admin/cross-role/visual 四个 project |
| S1-OPS-01 | **部分** | 用 systemd + vite preview 而非容器镜像；未单独建测试环境 |

### Sprint 2
| 任务 | 状态 | 缺口 |
|---|---|---|
| S2-DB-01 | 完成 | 0040 迁移 + 索引 + 回填 |
| S2-BE-01 | **部分** | 本地凭据/会话/Cookie/CSRF/令牌族轮换与重放检测完成；**OIDC adapter 与 MFA challenge 仍返回 501**（需接入 IdP 与 TOTP 实现，属功能而非验收） |
| S2-BE-02 | 完成 | 可信代理解析、设备解析、登录事件 |
| S2-BE-03 | 完成 | user/admin 双受众会话 |
| S2-FE-01 | **部分** | 登录/退出/过期/个人安全页完成；**MFA 页与 OIDC 回调页不存在** |
| S2-ADMIN-01 | 完成 | 会话列表 + 登录记录 |
| S2-SEC-01 | **部分** | AUTH-001/002/003/005/006/007/008/009/010/012 与 SEC-001..004、AUDIT-002 均已执行通过；**AUTH-004(MFA)/AUTH-011(OIDC) 因功能未实现而无法验收** |
| S2-MIG-01 | 完成 | `cmd/admin-init`，不写默认密码 |

### Sprint 3
| 任务 | 状态 | 缺口 |
|---|---|---|
| S3-DB-01 | 完成 | 0041/0042（163 权限、9 角色、审计 v2） |
| S3-BE-01 | 完成 | `internal/access` 对象级二次授权 + TenantContext + PolicyEngine + 路由声明；v2 详情路由逐对象校验，列表查询在 SQL 层带租户条件 |
| S3-BE-02 | 完成 | 审计 v2 + 哈希链 + 事务 Outbox + 定时校验器 + 每日锚点归档到 MinIO `blockchain-audit`（`internal/audit/{archive,scheduler}.go`） |
| S3-BE-03 | 完成 | `0044_tenant_scope.sql`：6 张表租户列收敛为 NOT NULL、新增 reports 作用域列、7 个复合索引、`data_asset_registry` 数据资产登记；已在生产库克隆体上演练回填 |
| S3-ADMIN-01 | 完成 | 组织 / 工作区 / 成员角色页面 + API（`/api/admin/v1/{organizations,workspaces,members,users/{id}/roles}`） |
| S3-ADMIN-02 | 完成 | 审计列表/详情/登录关联 |
| S3-QA-01 | 完成 | `run-rbac-e2e.sh`：RBAC-001..008，PASS 29 / FAIL 0 / BLOCKED 2（2 项被测端点属 S4/S5） |
| S3-OBS-01 | 完成 | `cs_authz_*` / `cs_audit_*` / `cs_outbox_*` / `cs_tenant_scope_denied_total` 指标 + `deploy/monitoring/dual-system-alerts.yaml`（7 条告警） |

### Sprint 4–7
全部未开始。对应功能（案件、调查、Finding/Evidence、Canvas、Explorer v2、下载、导出、
报告、通知、Provider/路由/成本、告警、Kill Switch、配置/Flag、保留与删除、备份恢复、
硬化与切流）在界面上显式标注目标 Sprint，不使用占位数据。

## 2. §27 测试计划完成度

65 个 Test ID：**PASS 27 / PARTIAL 7 / NOT_RUN 19 / NOT_IMPLEMENTED 12**（Sprint 3 与 Sprint 1/2 遗留验收补齐后）。
权威对照见 `acceptance-evidence/dual-system-v1/section27-conformance.md`。

## 3. §31 最终交付物清单完成度

| 类别 | 条目 | 状态 |
|---|---|---|
| 代码 | 两套前端 + 共享包 | 完成 |
| 代码 | IAM / Policy / Audit 模块 | 完成 |
| 代码 | Tenancy / Notification / Config-Governance 模块 | **未开始** |
| 代码 | `/api/auth/v1`、`/api/v2`、`/api/admin/v1`、`/api/v1` | 完成（`/api/v2` 仅 dashboard/search/me/cases/investigations/jobs 只读） |
| 代码 | 0040–0048 迁移与校验脚本 | **0040–0043 完成；0044–0048 未开始** |
| 代码 | Nginx / systemd / 回滚脚本 | 完成；monitoring 配置未做 |
| 文档 | ADR、权限目录 | 完成 |
| 文档 | OpenAPI、事件目录、数据字典 | **未完成** |
| 文档 | 用户手册 / 管理员手册 / 审计员手册 | **未完成**（只有运维 Runbook） |
| 文档 | 部署/升级/回滚 Runbook | 部署与回滚完成；升级、密钥轮换、备份恢复、事故处置未写 |
| 文档 | 能力边界表 | 完成（final-report §5 + 本文件的对照） |
| 证据 | 全部 Test ID 结果 | 完成对照表（但 25 项 NOT_RUN） |
| 证据 | 权限覆盖报告 / 审计覆盖报告 | **未完成** |
| 证据 | 秘密扫描 / SBOM / 漏洞报告 | **未完成** |
| 证据 | 性能分层结果 / 容量预测 / RPO-RTO 演练 | **未完成** |
| 证据 | `RELEASE-DECISION.md` | **未完成** |

## 4. 缺口优先级建议

按"先安全后功能"的文档约束，建议顺序：

Sprint 3 已完成，剩余优先级：

1. ~~补齐 Sprint 1/2 遗留验收~~ **已完成**（见 §27 对照表）。
2. **仍未闭合的功能缺口**：AUTH-004（MFA/TOTP）、AUTH-011（OIDC，需要 IdP）、
   S1-API-01 的 TypeScript 代码生成与"生成无 diff"检查、UX-004 的 10k 量级复测。
3. **测试夹具治理**：`cmd/admin-fixture` 生成的 acme 组织目前常驻在实例上，
   进入 Sprint 4 前应决定保留（作为回归夹具）还是用 `-cleanup` 移除。
4. **进入 Sprint 4**：案件与调查闭环（案件创建/归档/成员/时间线、调查工作台、Finding/Evidence 复核）。
