# §31 交付清单逐条核对

> **生成时间**：2026-09-13 03:20（UTC+7）
> **依据构建**：分支 `feat/dual-system-iam-foundation`，HEAD `51596f7`（Windows 工作树，含未提交 S7 改动）；
> 实测环境为部署机 `192.168.31.152:/home/euripides/projects/blockchain-platform`。
> **引用的章节**：`docs/dual-system-development-plan.md` 的 **`## 31. 最终交付物清单`（第 2478–2500 行）**，
> 其下三个小节为「代码」（2480–2486）、「文档」（2488–2493）、「证据」（2495–2500）。
> **请勿与相邻章节混淆**：§32「接手 Agent 的最终约束」（2502–2513）、§33「需求追踪矩阵」（2515–2537）。
> 若他处把「§31」用于别的标题，请以本行号区间为准。
> **证据路径规则**：下表路径相对仓库根；标注 `[VM]` 的文件当前**只存在于部署机**（Windows 工作树尚未同步，
> 详见 `section27-conformance.md` §0.1）。

---

## 0.0 2026-09-16 增量更正（不改变下面的交付清单结构，只更新被取代的结论）

- `§27` 与 `§7` 相关结论以 `acceptance-evidence/dual-system-v1/ROUND-20260916-defect-closure.md` 为准：
  本轮关闭 D-01..D-18（含 AUTH-004 MFA 由 `NOT_IMPLEMENTED` 转为 **PASS**、§7.5 导航权限双向验证、
  系统角色「有写无读」权限缺口迁移 `0065`）。
- 冻结清单：`release-20260916T115912Z/manifest.txt`（api `f10a1421…`、worker `f92d548d…`、migrate `d48e58fe…`、
  用户台 `71714cdc…`、后台台 `2d4a129e…`、迁移头 `0065`）。
- §27 矩阵已按同一冻结版本完整重建（`section27-conformance.md`，65 条 = 63 PASS + 2 发布阻断，
  `FAIL/BLOCKED/NV/PARTIAL` 全部为 0）；发布范围决定见 `docs/implementation/release-scope-decision-20260916.md`。
- 未关闭项与发布决定未变：见 `RELEASE-DECISION.md` §1 与矩阵 §4（B-1…B-6）（**NO-GO for cutover**）。

---

## 0. 状态定义与统计

| 状态 | 含义 |
|---|---|
| **完成** | 交付物存在、可复算验证通过、证据路径可定位 |
| **部分完成** | 交付物存在但覆盖不全（缺一类、缺一个入口、缺一块证据），或已实现但阻断项未关闭 |
| **未交付** | 计划要求存在、仓库内不存在对应产物 |
| **证据缺失** | 产物可能做过，但计划/报告引用的证据文件不存在，无法复算 |

| 状态 | 数量 | 占比 |
|---|---:|---:|
| 完成 | **32** | 62.7% |
| 部分完成 | **16** | 31.4% |
| 未交付 | **2** | 3.9% |
| 证据缺失 | **1** | 2.0% |
| **合计** | **51** | 100% |

> 计划原文是 13 条 bullet；本表把复合条目（如「ADR、OpenAPI、权限目录、事件目录、数据字典」）拆成可判定的子项，
> 得 51 行。**未闭合项（部分完成 + 未交付 + 证据缺失）= 19 条**。

---

## 1. 代码（计划 §31 第 2480–2486 行）

| # | 交付项 | 证据路径 | 状态 | 缺项 / 负责范围 |
|---|---|---|---|---|
| 1.1 | 用户层前端应用（可独立部署） | `frontend/apps/user-console/`；`deploy/blockchain-user-console.service`；`tests/sprint7/release/entry-matrix.txt`：`/app-v2/ nginx= public=200` | **完成** | — |
| 1.2 | 后台前端应用（可独立部署） | `frontend/apps/admin-console/`；`deploy/blockchain-admin-console.service`；`entry-matrix.txt`：`/admin/ nginx= public=200` | **完成** | 残余：`/storage`、`/data-quality`、`/risk` 仍是 `Placeholder`（`App.tsx:213-215`），见 §3 未闭合项 U-05 |
| 1.3 | 共享包 | `frontend/packages/{design-tokens,ui,api-client}/`；首屏 JS gzip ≈86 KB / 87 KB（预算 <350 KB） | **完成** | 证据为 Sprint 1/2 构建记录（`final-report.md` §3.4）；S7 构建未复测 bundle 体积 |
| 1.4 | 双入口 Nginx 路由与部署单元 | `deploy/nginx-blockchain-platform.conf`（`/admin/→3002`、`/app-v2/→3001`、`/api/→8080`） | **完成** | — |
| 2.1 | IAM 模块 | `backend/internal/iam/`（凭据、会话/令牌族、设备、OIDC 适配器、`device_test.go`、`oidc_test.go`） | **完成** | OIDC/MFA 能力边界见 §27 矩阵 G-1/G-2 |
| 2.2 | Policy 模块 | `backend/internal/policy/`；权限目录 177 条授权、105/105 后台权限声明覆盖（`sprint7/security/threat-matrix.md`） | **完成** | — |
| 2.3 | **Tenancy 模块** | 无独立包；组织/工作区作用域落在 `policy` + SQL scope 校验 + 迁移 `0044_tenant_scope.sql`/`0056`（7 条 `workspace_id is null or organization_id is not null` 约束，`convalidated=true`） | **部分完成** | 计划把 Tenancy 列为独立后端模块，实际是横切实现（无 `internal/tenancy`）。§33 要求「矩阵每列至少一条完整垂直路径」，该列目前只能靠 policy+SQL 约束间接证明。负责范围：S3-BE-01 / S7-DB-01 |
| 2.4 | Audit 模块 | `backend/internal/audit/`（哈希链、写入器、`archive.go` 每日锚点→MinIO、`scheduler.go`、`integrity.go`、`verify.go`） | **完成** | 完整性边界缺口见 §27 矩阵 AUDIT-004（`sealed_incident`，seq 1–2343 不可恢复） |
| 2.5 | **Notification 模块** | 无独立包；`backend/internal/api/userv2.go:27-30`（`/notifications`、`/read`、`/read-all`、`/open`）+ 迁移 `0043` + SSE；实测 `qa/sprint4/run.log`（通知投递/跳转）与 `sprint7/perf/lan_notifications.json` | **部分完成** | 计划列为独立模块，实际在 API 层；无独立的通知投递/重试组件与对应单测。负责范围：S4-BE-03 |
| 2.6 | Config/Governance 模块 | `backend/internal/api/sprint6_governance.go`（settings/feature-flags/retention/deletion/backups/restores/system-version/audit-export）+ `sprint6_provider.go`；迁移 `0045/0051/0052/0053` | **完成** | 功能开关未接请求路径（见 §27 矩阵 ADMIN-010 / G-4） |
| 3.1 | `/api/auth/v1` | `backend/internal/api/authv1.go`；`entry-matrix.txt`：`/api/auth/v1/methods nginx= public=200` | **完成** | `/oidc/*`、`/mfa/verify` 返回 501（能力边界，非缺失） |
| 3.2 | `/api/v2` | `backend/internal/api/userv2.go` 等；S4/S5/S6 套件全部经该前缀通过（`qa/sprint{4,5,6}/run.log`） | **完成** | — |
| 3.3 | `/api/admin/v1` | `backend/internal/api/adminv1.go`、`sprint6_governance.go`、`sprint6_provider.go`；`UNAUTH-a`：10 个后台端点未认证全部 401 | **完成** | — |
| 3.4 | 兼容 `/api/v1` | `backend/internal/api/server.go`；S7 修复记录：48 条写路由加 `s.require(...)`，复测未认证写 → 401、viewer 删除 → 403 | **完成** | 已登记 P2：`/api/v1` 127 条**读**路由无权限声明（`RELEASE-DECISION.md` §3），随旧入口下线一并移除 |
| 4.1 | 0040–0048 增量迁移 | `backend/migrations/postgres/0040_iam.sql` … `0048_s4_permission_fix.sql`（9 个文件齐全） | **完成** | — |
| 4.2 | 0049–0057 迁移（计划外扩展） | `0049_s5_…`、`0050–0055_s6_…`、`0056_s7_scope_backfill_constraints.sql`、`0057_s7_indexes_partitions.sql`；生产 `schema_migrations` 55 → 57 | **完成** | 计划写「0040–0048」，实际到 0057；差异需在计划或 ADR 中登记（当前只在 sprint7-report 说明） |
| 4.3 | 回填与校验脚本 | `scripts/s7/_*.sql`（scope 回填/角色/探针）；核对报告 `sprint7/db/scope-backfill-report.md`、`index-partition-verification.md`（**工作树**）与 `sprint7/db/{before,after,diff,verification-raw}.txt`（**`[VM]`**） | **部分完成** | 报告与原始测量**分居两处**，任一侧单独看都不完整；索引 A/B 结论（`download_pages` 120.801ms → 0.205ms、`audit 列表` 43.086ms → 0.117ms）只在 sprint7-report 转述。负责范围：S7-DB-01 |
| 4.4 | 迁移重入 / 回滚验证 | `scripts/test_migration_reentrancy.sh`（10/10）；`backend/migrations/rollback/rollback-0056.sql`、`rollback-0057.sql`；一次性库 `blockchain_platform_s7_migtest` `0001→0057` 全量 + 重放两次成功 | **完成** | `docs/manuals/rollback.md` §3.3 记录 `rollback-0057.sql` 仍有 2 个真实缺陷（见 8.3） |
| 5.1 | Nginx 配置 | `deploy/nginx-blockchain-platform.conf`；`entry-matrix.txt` 6 条路由全部实测 | **完成** | — |
| 5.2 | systemd 配置 | `deploy/blockchain-api.service`、`-worker`、`-mcp`、`-vite`、`-user-console`、`-admin-console`、`-containers-watchdog.{service,timer}`、`-e2e-tls.service`（验收入口） | **完成** | 看门狗是 NATS 重启缺口的**补偿手段**，根因未定位（`sprint7/dr/README.md` §4） |
| 5.3 | compose 配置 | `deploy/compose.yaml`；`sprint7/security/image-iac/raw/compose.yaml.txt`、`compose-images.txt` | **完成** | IaC 扫描覆盖缺口见 11.5（R-11） |
| 5.4 | monitoring 配置 | `deploy/monitoring/dual-system-alerts.yaml`；`qa/sprint6/run.log` `S6-OBS-01 可观测性页面展示 P0 告警、SLO 与吞吐拆分`（浏览器 ✓） | **完成** | — |
| 5.5 | 回滚脚本 | `backend/migrations/rollback/rollback-00{56,57}.sql`；`docs/manuals/rollback.md`（10 节）；`RELEASE-DECISION.md` §4 回滚点表（`api.bak-s7-<ts>`、`.env.bak-s7-*`、`pre-s7-pg-*.dump`） | **完成** | `rollback-0057.sql` 缺陷见 8.3；数据库向下迁移不可用属设计（AD-006） |

## 2. 文档（计划 §31 第 2488–2493 行）

| # | 交付项 | 证据路径 | 状态 | 缺项 / 负责范围 |
|---|---|---|---|---|
| 6.1 | ADR | `docs/adr/`：`AD-001`～`AD-010`（10 篇）+ `ADR-016`（S4 运行时/OIDC）、`ADR-017`（S6 控制面与运行角色）、`ADR-018`（审计完整性边界）+ `README.md` | **完成** | `AD-011`～`AD-015` 编号缺失（无文件、无说明），编号空间不连续 |
| 6.2 | **OpenAPI** | `openapi/auth-v1.yaml`(16,607 B)、`openapi/user-v2.yaml`(63,171 B)、`openapi/admin-v1.yaml`(84,114 B)、`openapi/common/{envelope,errors}.yaml`；`scripts/check_openapi.py`、`scripts/gen_openapi_index.py` | **部分完成** | 无 CI 生成/diff 门禁（仓库**无 `.github/`**、无任何 CI 配置）；无 SDK 生成产物；运行时契约与文件一致性没有自动校验。负责范围：S1-API-01 / S7-DOC-01 |
| 6.3 | 权限目录 | `docs/manuals/permissions.md`(769 行)；后台权限声明覆盖 **105/105 = 100%**（`sprint7/security/threat-matrix.md`）；实测角色授权 177 条（`operations.md` §2.3） | **完成** | — |
| 6.4 | **事件目录** | 部分覆盖：`docs/implementation/audit-coverage.md`（16 个认证事件常量、81 个审计权限键、11 个敏感权限）；审计动作 24h 计数 `tests/sprint7/release/audit-actions-24h.txt` | **部分完成** | 没有独立的「事件目录」文档：业务/管理/通知/Provider 事件没有统一清单与字段定义。负责范围：S7-DOC-01 |
| 6.5 | **数据字典** | 仓库内不存在 | **未交付** | `docs/` 下无数据字典；表/字段/保留期/敏感级别的唯一线索散落在迁移 SQL 与 `current-surface-inventory.md`。计划 §32.4 也要求「每新增表先写数据所有者、保留期、敏感级别、索引和迁移/回滚方案」，当前无汇总产物。负责范围：S7-DOC-01 |
| 7.1 | 用户手册 | `docs/manuals/user-guide.md`(437 行) | **完成** | — |
| 7.2 | 管理员手册 | `docs/manuals/admin-guide.md`(495 行) | **完成** | — |
| 7.3 | 审计员手册 | `docs/manuals/auditor-guide.md`(556 行) | **完成** | — |
| 8.1 | 部署 Runbook | `docs/manuals/operations.md` §3（标准部署 ⚠️ 未实测 / 手工部署 ✅ 关键步骤已验证）、§3.3 Nginx ✅、§3.4 验证清单 | **完成** | §3.1 标注 ⚠️（脚本化标准部署未实测），§3.2 补足 |
| 8.2 | 升级 Runbook | `docs/manuals/operations.md` §5；迁移章节 §4（§4.1 ✅ / §4.3 ⚠️） | **部分完成** | §5 没有实测标记，也没有「expand-compatible 双版本共存」的实际执行记录（计划 §29.1 要求），属纸面步骤。负责范围：S7-DOC-01 |
| 8.3 | 回滚 Runbook | `docs/manuals/rollback.md`（10 节：决策树、回滚点、应用/迁移/配置/灰度回退、Kill Switch、验证清单、红线） | **部分完成** | 文档本身完整，但 §3.3 自记 `rollback-0057.sql` 的**两个真实缺陷**、§2.1/§2.3/§2.4 多处标 ⚠️（未实测），§10 有待确认项。负责范围：S7-DB-01 / S7-DOC-01 |
| 8.4 | 密钥轮换 Runbook | `docs/manuals/operations.md` §7：§7.1 Provider 密钥 ✅、§7.2 应用/API/MCP Key ✅、§7.3 会话令牌轮换 ✅、§7.4 管理账号密码 ✅、§7.5 轮换检查清单 | **完成** | — |
| 8.5 | 备份恢复 Runbook | `docs/manuals/operations.md` §6（§6.2 PG 备份 ✅ 实测 / §6.3–6.5 ⚠️）+ **§13.1–13.6 S7 实测补充**（替换 §6.7/§6.8/§12 相关条目）；`scripts/backup/{backup-all,restore-drill,kill9-drill,containers-watchdog}.sh` | **完成** | RPO 未受控见 12.3 |
| 8.6 | 事故处置 Runbook | `docs/manuals/troubleshooting.md`(450 行，15 节，含 §8 DATA-006、§9 DATA-007、§10 DATA-008 专节) | **完成** | — |
| 9 | 已实现 / 部分实现 / 未实现能力边界表 | `RELEASE-DECISION.md` §3（未解决 P2 七条）、`acceptance-evidence/dual-system-v1/final-report.md` §5（已知限制表）、`docs/implementation/current-surface-inventory.md`(32,684 B) | **部分完成** | 三处分散、口径不同步（`final-report.md` 仍是 S1/S2 视角：OIDC/MFA/case-v2 全标 NOT_IMPLEMENTED，与 S7 事实矛盾）；**没有一张单一、按 §32.10 口径维护的能力边界表**。负责范围：S7-DOC-01 |

## 3. 证据（计划 §31 第 2495–2500 行）

| # | 交付项 | 证据路径 | 状态 | 缺项 / 负责范围 |
|---|---|---|---|---|
| 10.1 | 全部 Test ID 结果 | `acceptance-evidence/dual-system-v1/section27-conformance.md`（本轮重建，65/65 逐条含可复算数字）；分套件原始日志 `tests/sprint7/qa/**`、`sprint7/security/negative-tests/run.log` | **部分完成** | `tests/sprint7/qa/summary.json` **未重算**（仍 `status: FAIL`、playwright 33/4/21、sprint5 105/1），与 2026-09-13 复跑结果矛盾；2026-09-13 的复跑证据只在部署机。负责范围：S7-QA-01 |
| 10.2 | 截图 | `tests/sprint5/screenshots/`（10 张：地址/交易/Canvas/下载/任务/搜索，桌面+移动）、`tests/sprint6/screenshots/`（20 张：providers/control/costs/alerts/observability/backups/…含 mobile 两张）、`tests/UX-001\|UX-002-mobile-home/screenshot.png`、各 `tests/<ID>/screenshot.png` | **部分完成** | **S7 复跑轮次没有新截图**（`qa/ui/` 下只有日志与 trace，无 png）；USER-011 的 390px 截图来自 S5。负责范围：S7-QA-01 |
| 10.3 | API / DB / 日志 / 文件 / 运行证据 | `tests/sprint7/qa/*/run.log`、`console.log`、`files/`（`big.{csv,xlsx,jsonl,parquet,zip}`、`range.part`、`object-backup.bin`、`audit-export.jsonl` 519,624 B）、`sprint7/db/*.txt` `[VM]`、`sprint7/dr/*.md` `[VM]`、`sprint7/perf/*.json` | **完成** | 单件证据齐全；跨目录一致性受 10.1 的摘要过期影响。注：`tests/__pycache__/parse_export.cpython-312.pyc` 属误入证据目录的构建副产物，应清理 |
| 11.1 | 权限覆盖报告 | `sprint7/security/threat-matrix.md`（7 条信任边界 STRIDE + 后台权限声明覆盖 105/105）；`docs/manuals/permissions.md`；实测授权条数 177（`operations.md` §2.3） | **部分完成** | 只有「声明覆盖率」；没有独立的「权限覆盖报告」文件把「声明 → 实际调用 → 拒绝行为」三者对照。负责范围：S7-SEC-01 |
| 11.2 | 审计覆盖报告 | `docs/implementation/audit-coverage.md`（由 `scripts/gen_audit_coverage.py` 生成：16 认证事件常量/10 实际写入/81 审计权限键/11 敏感权限） | **部分完成** | `tests/sprint7/release/audit-coverage-summary.txt` 是 **0 字节空文件** →「实执行逐项 100%」无机器可读对照（与 §27 矩阵 AUDIT-001 同源）。负责范围：S7-QA-01 |
| 11.3 | 秘密扫描 | `sprint7/security/secret-scan/REPORT.md` + `raw/`（脚本 SHA256 `96b17860…`；三目标 8.5 MB/29.1 MB/1,340 行扫描；(c) 类明文 **0**；阴性对照 10/10、阳性对照 10/10） | **完成** | — |
| 11.4 | SBOM | `sprint7/security/sbom/`：`backend.cdx.json`(60,246 B)、`backend.spdx.json`(86,473 B)、`frontend.cdx.json`(225,134 B)、`frontend.spdx.json`(382,784 B)、`SUMMARY.md`、`validation.txt`、`commands.txt` | **完成** | 材料性保留：syft 镜像来自镜像站（`docker.1ms.run`），**未能与上游 Docker Hub 逐字节比对**（SUMMARY.md 自记 NOT_VERIFIED 项）；工作树中这些 JSON 只在 `discarded-wrong-target/` 下 |
| 11.5 | 漏洞报告 | `sprint7/security/dependency-scan/SUMMARY.md`（15 条：P0=2/P1=5/P2=3/P3=1/无等级 4，govulncheck 判定可达 3）；`image-iac/SUMMARY.md`（CRITICAL 20 / HIGH 203 / MEDIUM 331 / LOW 201）；`go/osv/` 26 个原始 JSON | **部分完成** | 报告齐全但**阻断项未关闭**：R-09 依赖（前端 `xlsx@0.18.5` npm 无修复版本）、R-10 基础镜像未升级固定 digest、**R-11 IaC 配置面 NOT_VERIFIED**（trivy/checkov 结构性不覆盖 compose/systemd/nginx）。不得用风险接受代替零缺陷门槛。负责范围：S7-SEC-01 + 人工批准 |
| 12.1 | 性能分层结果 | `sprint7/perf/README.md`（工作树）+ `summary.json`(23,423 B)、`lan_*.json`(14)、`nginx_*.json`(3)、`wan_*.json`(2) | **部分完成** | 内网 **8/8 场景 P95 < 200 ms** 达标；但 explorer `activity` **8.09 s**、`flow-profile` **9.88 s** 超 §30.2 预算（P1-1 未关闭）；`counterparties` 由 503→200（0.022 s）已修复 |
| 12.2 | **容量预测** | 仓库内不存在（§30.3 要求的「按组织/工作区/dataset 的每日新增 rows/bytes 与 30/90/365 天预测」「Login/Audit 分区前事件数估算」均无产物） | **未交付** | `sprint7/db/index-partition-verification.md` 只给出索引 A/B 与分区现状，不是容量预测。负责范围：S7-DB-01 / S7-PERF-01 |
| 12.3 | RPO/RTO 演练 | `sprint7/dr/README.md`（工作树）+ `dr/restore-drill-20260912T175029Z.md`、`dr/kill9-20260912T180409Z.md`、`dr/pg-restore-*.{rto,counts}.txt`、`backups/pre-s7-pg-20260912T162853Z.dump`(85,948,607 B) `[VM]` | **部分完成** | **RTO 达标**（PG 5.81 s / CH 561.36 s / MinIO 0.63 s，均 ≤4 h）、语义一致性经 16 表逐表比对 + 恢复库 `auditverify valid=true`；**RPO 未达标**（无调度备份、无 WAL/PITR，演练时新增 32 行）、`blockchain-raw`(≈16 GiB) 未纳入。负责范围：S7-DR-01 |
| 12.4 | 百万行导出与 Provider/全链路性能证据 | `sprint7/perf/README.md` §5 指向 `export-pipeline.md` 与 `provider-pipeline.md` | **证据缺失** | **两个被引用的文件在仓库与部署机上都不存在**（部署机 `sprint7/perf/` 只有 `lan_*/nginx_*/wan_*/summary.json`）。`sprint7-report.md` §8 亦承认「百万行导出与 Provider/全链路两项在本轮未取得完整实测」。这是 §28.9 S7-PERF-01 的实质缺口，不能算完成。负责范围：S7-PERF-01 |
| 13 | `RELEASE-DECISION.md` | `acceptance-evidence/dual-system-v1/RELEASE-DECISION.md`（69 行）：版本/冻结构建 SHA、决策（NO-GO 切流，保持灰度）、7 条阻断理由、10 项已满足前置、7 条未解决 P2、回滚点表、再次评审进入条件 | **部分完成** | ①**批准人**一栏是「执行 Agent」，无人工批准签字（计划要求「批准人」）；②文件**未纳入版本控制**（`git status` 显示未跟踪，仅存在于工作树）；③其阻断理由第 1 条仍是「§27 浏览器验收 4 条 FAIL 未消除」，而该 FAIL 已于 2026-09-13 归零 → **必须先更新再作为发布依据**。负责范围：S7-RELEASE-01/02 + 人工批准 |

---

## 4. 未闭合项汇总（19 条）与建议顺序

| 序 | 未闭合项 | 状态 | 阻断强度 | 负责范围 | 关闭条件 |
|---|---|---|---|---|---|
| U-01 | `RELEASE-DECISION.md` 的阻断理由已过期（4 FAIL 已归零）、无人工批准人、未入版本控制 | 部分完成 | **高** | S7-RELEASE-01/02 + 人工 | 更新 §1 理由表并取得签署 |
| U-02 | 依赖 P0=2/P1=5、镜像 CRITICAL 20/HIGH 203、IaC 配置面 NOT_VERIFIED | 部分完成 | **高** | S7-SEC-01 | 升级/替换 + digest 固定 + 补扫，或书面风险接受（含责任人） |
| U-03 | RPO 未受控（无调度备份、无 PITR）、`blockchain-raw` 未纳入 | 部分完成 | **高** | S7-DR-01 | systemd timer + WAL 归档 + 全桶演练 |
| U-04 | 百万行导出 / Provider 全链路证据缺失 | 证据缺失 | **高** | S7-PERF-01 | 补跑并落 `export-pipeline.md`/`provider-pipeline.md` |
| U-05 | `/storage`、`/data-quality`、`/risk` 仍为占位页 | 部分完成 | 中 | S6/S7 | 接真实 API 或移出 v1 范围 |
| U-06 | 功能开关未接请求路径（仅管理端试算） | 部分完成 | 中 | S6-BE-03 | 请求路径求值 + 两用户行为对照 |
| U-07 | explorer `activity`/`flow-profile` 超交互预算 | 部分完成 | 中 | S7-PERF-01 | 查询预算 + 超限可操作错误，或预聚合 |
| U-08 | `qa/summary.json` 未重算（仍 FAIL） | 部分完成 | 中 | S7-QA-01 | 复跑 `run-sprint7-qa.sh` 或重算摘要 |
| U-09 | 审计覆盖「逐项执行 100%」无对照（摘要 0 字节）；seq 1–2343 已封存缺口 | 部分完成 | 中 | S7-QA-01 / S6 | 生成逐动作对照；缺口只能声明 |
| U-10 | 容量预测未交付（§30.3） | 未交付 | 中 | S7-DB-01/PERF-01 | 出 30/90/365 天预测 |
| U-11 | 事件目录、数据字典未交付 | 部分完成 / 未交付 | 中 | S7-DOC-01 | 出两份目录文档 |
| U-12 | OpenAPI 无 CI 门禁/SDK；无 CI 配置 | 部分完成 | 中 | S1-API-01 | 接 CI（OpenAPI diff + 迁移静态检查） |
| U-13 | 能力边界表三处口径不一致（`final-report.md` 仍是 S1/S2 视角） | 部分完成 | 中 | S7-DOC-01 | 合并为单一权威表 |
| U-14 | Tenancy / Notification 无独立模块 | 部分完成 | 低 | S3/S4-BE | 明确架构决定（ADR）或补齐 |
| U-15 | 升级 Runbook 未实测；`rollback-0057.sql` 两个缺陷 | 部分完成 | 低 | S7-DOC-01 / S7-DB-01 | 实测 + 修脚本 |
| U-16 | 权限覆盖报告只有「声明覆盖率」 | 部分完成 | 低 | S7-SEC-01 | 补「声明→调用→拒绝」对照 |
| U-17 | S7 复跑轮次无截图；`tests/__pycache__` 混入证据目录 | 部分完成 | 低 | S7-QA-01 | 补截图 + 清理 |
| U-18 | AD-011～AD-015 编号缺失 | 部分完成 | 低 | S7-DOC-01 | 补缺号说明或重排 |
| U-19 | `/api/v1` 127 条读路由无权限声明；F-02/F-04/R-02/R-03 残余 | 部分完成 | 低 | 随旧入口下线 | 下线或补声明/校验 |

## 5. 与 §28.9 / §33 的对应关系

* **§28.9 任务覆盖**：`S7-DB-01`→4.3/4.4/12.2（U-10/U-15）；`S7-SEC-01`→11.3/11.4/11.5（U-02/U-16）；
  `S7-PERF-01`→12.1/12.4（U-04/U-07）；`S7-DR-01`→12.3（U-03）；`S7-QA-01`→10.1/10.2/11.2（U-08/U-09/U-17）；
  `S7-RELEASE-01/02`→13（U-01）；`S7-DOC-01`→6.4/6.5/8.x/9（U-11/U-13/U-15/U-18）。
* **§28.9 验收口径**（计划 2384 行）：「P0=0、P1=0；真实 UI/API/数据库/文件/运行证据齐全；备份恢复达 RPO/RTO；
  新旧功能对照无未解释差异」——当前 **P0=0**，但 **P1 至少 6 项未关闭**、**RPO 未达标**，故本清单整体**不构成验收通过**。
* **§33 合并前条件**：「任何 Epic 合并前，矩阵对应的页面、API、迁移、权限/审计和测试至少形成一条完整垂直路径；
  缺少任意一列时只能标记 `PARTIAL`」——据此 2.3（Tenancy）、2.5（Notification）、6.4（事件目录）、
  12.2（容量预测）四项在该矩阵中只能记 `PARTIAL`，与本表结论一致。

> 本文件只做核对，不改动任何既有文件、不执行任何部署/迁移/测试命令。数字与路径可用
> `section27-conformance.md` §7 的只读命令在部署机上复算。

---

## 6. 2026-09-16 夜场追加交付物与状态更新（不改动上文既有判定）

| 交付物 | 路径 | 状态 |
|---|---|---|
| 恢复演练（真实执行：manifest 全量校验 + 逐表/逐对象比对 + RTO） | `sprint7/dr/restore-drill-20260916T154355Z.{md,json}` 与 `.logs/`（部署机） | 首轮 FAIL，唯一失败项是当时尚未修复的 ACL 缺陷（D-25）；次轮用修复后的脚本重跑 |
| 完整备份（含 PG 一致性快照 / CH / MinIO 全桶） | `/home/euripides/backups/blockchain-platform/20260916T150033Z`（39 GB、177,899 个文件，部署机） | 已产出：`complete=true`、`pg_snapshot_consistent=true`、177,899/177,899 文件 size+sha256 一致 |
| 镜像 / IaC 扫描脚本与证据 | `scripts/s7/run-s7-scan-image-iac.sh`、`sprint7/security/image-scan-20260916T161155Z/`（部署机） | 镜像扫描完成（合计 22 CRITICAL / 253 HIGH，可修 19 / 188）；IaC 检出配置文件数 0 ⇒ **不主张** IaC 已扫描通过 |
| 回滚演练脚本（真实回滚 + 前滚 + RTO） | `scripts/release/rollback-drill.sh` | 已就绪（`ba3f5662…`），待维护窗口内执行 |
| DR 工具缺陷修复 | `scripts/backup/{backup-all.sh,restore-drill.sh,kill9-drill.sh}` | D-25…D-28 四条，已部署、已 `bash -n` 校验、旧证据保留 |

结论更新（不改动本文既有判定）：

- 整体**仍不构成验收通过**：§27 矩阵保持 63 PASS + 2 发布阻断（`DATA-007`、`DATA-009`），发布门禁 B-1…B-7 全部开放。
- §28.9 的「备份恢复达 RPO/RTO」：**RTO 侧已实测达标**（PG 58.83 s、CH 477.02 s、MinIO 最慢单桶 280.01 s，均远低于 4 h 上限），
  **RPO 侧仍未取证**（实测 `archive_mode=off`，PITR 结构性不可执行），因此该条仍不能判达标。
- §33 的 `PARTIAL` 判定不受本轮影响（本次未改动其依据）。
