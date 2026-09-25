# 双系统手册索引

对应 `docs/dual-system-development-plan.md` §28.9 `S7-DOC-01` 与 §31「最终交付物清单」中的
**用户手册、管理员手册、审计员手册**，以及部署/升级/回滚/密钥轮换/备份恢复/事故处置 Runbook。

| 文档 | 面向对象 | 内容 |
|---|---|---|
| [user-guide.md](user-guide.md) | 调查员、分析员、复核人、只读协作者 | 登录、案件、调查、Explorer、下载与任务、导出、图谱、报告、Inbox/通知、个人资料、移动端操作 |
| [admin-guide.md](admin-guide.md) | owner、platform_admin、security_admin | 后台 28 个页面的用途/操作/权限/审批与 step-up/常见错误码 |
| [auditor-guide.md](auditor-guide.md) | auditor、安全与合规人员 | 审计链校验口径、`integrity_status`/`prefix_status` 语义（ADR-018）、锚点核对、审计导出与校验、只读边界 |
| [permissions.md](permissions.md) | 管理员、安全评审 | 177 条权限目录、9 个系统角色默认映射、作用域模型、风险等级与 step-up 要求 |
| [operations.md](operations.md) | 运维 | 环境概览、部署、迁移、升级、密钥轮换、备份恢复、分区维护、日常巡检、SLO 与告警处置 |
| [troubleshooting.md](troubleshooting.md) | 运维、值班 | 按症状→诊断→处置→验证组织的故障手册（含 §27 DATA-006/007/008、审计链、SSO、限流、Provider） |
| [rollback.md](rollback.md) | 运维、应急响应 | 应用回滚、迁移回滚、配置回滚、灰度回退、紧急 Kill Switch、不可回滚红线 |
| [data-download-api.md](data-download-api.md) | 需要脚本取数的调查员、分析员、外部协作方 | 数据下载接口调用方式：Bearer API Key、`/api/v1/data/ensure` 与 `/api/v1/export/*` 全流程、票据/直连下载、只读查询、v2 会话面差异、错误码与实测记录 |

## 阅读顺序建议

- **新用户**：user-guide → permissions（了解自己能做什么）。
- **新管理员**：admin-guide → permissions → operations。
- **审计/合规**：auditor-guide → permissions（只读边界核对 SQL）。
- **值班/应急**：troubleshooting → rollback → operations。

## 命令验证状态约定

各手册中的命令按以下约定标记：

- **✅ 已验证**：2026-09-12 在 `euripides@192.168.31.152`（`dataserver`）实机执行并核对回显。
- **⚠️ 未实测**：来自脚本/迁移/文档分析，执行前需在维护窗口验证。

## 上游事实来源

- 计划：`docs/dual-system-development-plan.md`（§18 用户层逐页规格、§19 后台逐页规格、§20 状态机、
  §21 API 规格、§23 安全设计、§24 异步任务、§25 可观测性、§27 测试矩阵、§28.9 S7-DOC-01、§30 容量预算、§31 交付物）。
- ADR：`docs/adr/`（AD-003 权限命名、AD-004 审计哈希链、AD-005 append-only、AD-006 迁移与发布、
  AD-008 灰度路径、AD-009 弹窗约定、ADR-017 控制面与运行期降权、ADR-018 审计完整性事件边界）。
- 代码：`backend/internal/policy/catalog.go`、`backend/internal/api/*`、`frontend/apps/{user,admin}-console/src/*`。
- 既有 Runbook：`docs/implementation/dual-system-runbook.md`（灰度阶段）；Sprint 报告见 `docs/implementation/sprint{4,5,6}-report.md`。

> 维护约定：任何手册中的命令若被实际执行并确认，请在文中把 ⚠️ 升级为 ✅ 并注明日期；
> 未实现的能力必须标注 `NOT_IMPLEMENTED`，未验证的结论必须标注 `NOT_VERIFIED`（§32 约束 10）。
