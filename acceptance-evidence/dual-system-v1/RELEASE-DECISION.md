# RELEASE-DECISION.md — 双系统 v1 发布决策

> 决策日期：2026-09-13（UTC+7）
> 冻结构建：`backend/bin/api` `d221dfef…` / `backend/bin/worker` `c3fe5898…` / `backend/bin/migrate` `225a510f…`
> （二进制路径一律为 `backend/bin/` 绝对路径；仓库顶层 `bin/` 是遗留，**不得混用**。任何新部署都必须重新核实实际 `ExecStart` 与 SHA-256。）
> 决策人：执行 Agent（依据 §28.9 验收口径与 §23.8 安全发布门自动判定）
> **本文件不是人工签署的发布批准**：批准人一栏为"执行 Agent"，**无人工签字**，且长期未纳入版本控制。
> 正式发布仍须由具备发布权限的人签署；在此之前本文件的效力仅限于"执行侧的现状判定"。
> 依据：`docs/implementation/sprint7-report.md`、`acceptance-evidence/dual-system-v1/sprint7/**`

---

## 0. 2026-09-16 夜场修订（DR 证据与镜像扫描；决定不变：NO-GO）

- 冻结构建仍为 `backend/bin/api` `7831bff0…` / `worker` `0d49fb75…` / `migrate` `d48e58fe…`、
  用户台 `71714cdc…` / 后台台 `2d4a129e…`、迁移头 `0065`（清单 `release-20260916T125603Z/manifest.txt`）。
- 新增实测：备份恢复演练（PG RTO 58.83 s / CH RTO 477.02 s / MinIO 5 桶逐对象零差异）、镜像漏洞扫描
  （22 CRITICAL / 253 HIGH，可修 19 / 188）、回滚演练脚本已就绪（`scripts/release/rollback-drill.sh`）。
- 新增关闭的缺陷：D-25（恢复库业务角色不可用）、D-26（备份缺 MinIO 语义基线）、D-27（kill9 `/jsz` 参数错误）、
  D-28（元数据维度无法单轮闭环）。见 `ROUND-20260916-defect-closure.md` §4。
- **决定：NO-GO for cutover 维持不变。** §27 矩阵仍为 63 PASS + 2 发布阻断（DATA-007 订阅者幂等、DATA-009 备份恢复演练）；
  发布门禁 B-1…B-7 全部保持开放，其中 B-4（PITR，`archive_mode=off`）、B-1（消费者幂等无落库维度）**结构性无法在窗口前关闭**。
- 执行侧不自行推进：production 切换、灰度、入口切换与签署一律等待人工窗口（`RELEASE-WINDOW-REQUEST-20260916.md`）。

---

## 0.1 2026-09-13 修订（新增 P0 与两处状态更正）

本节**优先于**下文原始正文；出现冲突时以本节为准。

### 0.1 新增 P0：旧兼容面 `/api/v1` 未认证读（**阻断，未关闭**）

独立监控者与执行 Agent 各自实测确认：**无凭证**（无 Cookie / 无 Authorization / 无 API Key / 无 X-User-* 头）
直接请求即返回真实业务数据——

| 请求 | 状态 | 响应体 |
|---|---|---|
| `GET /api/v1/cases` | 200 | 9234 B，含 `organization_id` / `workspace_id` / `name` |
| `GET /api/v1/investigations` | 200 | 28831 B |
| `GET /api/v1/download-jobs?limit=1` | 200 | 1054 B |
| `GET /api/v1/api-control/overview` | 200 | 3171 B |
| `GET /api/v1/api-control/provider-accounts` | 200 | 466 B |
| `GET /api/v1/api-control/requests` | 200 | 25305 B |
| `GET /api/v1/admin/planning-decisions` | 200 | 40620 B |
| `GET /api/v1/admin/ankr/benchmarks` | 200 | 374399 B |
| `GET /api/v1/data-explorer/addresses` | 200 | 57619 B |
| 正对照 `/api/v2/cases`、`/api/admin/v1/jobs` | 401 | — |

双层根因：路由层 `r.Route("/api/v1", …)` 多条读路由未声明 `s.require(...)`；
handler 层用 `Store.DefaultOrgWorkspace(ctx)` 而**忽略已认证主体**（因此**仅补全局登录不足**，
已认证的跨组织用户仍可读默认组织/工作区数据）。

**影响**：本节使"P0=0"的旧表述**失效**。在此之前任何"P0=0 / 可切流"的结论都必须以本节为准作废。
修复与回归要求见 `.work/dsh-supervisor/HANDOVER-20260913.md` §3。

### 0.2 状态更正一：§27 浏览器套件 4 条 FAIL **已归零**

原文 §1 理由 1「§27 浏览器验收 4 条 FAIL 未消除」已不适用。真实根因是**验收入口协议**而非产品缺陷：
nginx 对各 `/api` 路由写死 `X-Forwarded-Proto https`，后端因此下发 `Secure`（HTTPS 语义下含 `__Host-`）
会话 Cookie；而 Playwright 的 `APIRequestContext` 在 `http://` 源下**不回送 Secure Cookie**，
导致用例内独立 API 断言全部 401。改用仅回环的本地 TLS 验收入口后：
**passed 37 / failed 0 / skipped 21 / rc 0**（`tests/sprint7/qa/ui/run-20260913T0300Z-https-entry/`，
`playwright.log` SHA-256 `e9ff0afe…`）。原始 4 FAIL 证据保留于 `ui/preserved-original-20260912T1843Z/`。

同批更正：**DATA-007 已于复跑通过**（S5 套件 `PASS=108 / FAIL=0 / NV=0`，`2026-09-13T03:03:52+07:00`；
首轮 FAIL 证据保留于 `sprint5/preserved-original-20260913T0148Z/run.log:200`）。原文理由 6「P1 性能」与
理由 7「APP_ENV」仍然成立；理由 2/3/4/5 仍然成立（见 §0.3）。

### 0.3 状态更正二：RPO/RTO 门槛与门禁口径（不得放宽）

- **要求**：**RPO ≤ 15 分钟**、**RTO ≤ 4 小时**。当前**无**调度备份、**无** WAL/PITR，
  `blockchain-raw`（约 16 GiB）未纳入主备份，MinIO 仅验证 `blockchain-reports` 单桶，
  `mc mirror` 只反映当前对象、不能冒充版本/元数据完整 → **RPO 未受控，仍为阻断项**。
- **门禁口径（硬性）**：DR 与任何套件的聚合 gate **只要存在 NV / PARTIAL 就必须非 0**；
  不得把真实 NV 降级为 NOT_REQUIRED 来"变绿"；不得用 `exit 0` 或
  "PASS（NOT_VERIFIED 项不计入通过）"之类的自述充当通过。
- **保留策略**：备份脚本默认**禁用**自动裁剪；只有既有的、已批准的保留策略才可执行；
  **不得**由执行方自行设 APPROVED=1。

### 0.4 证据一致性问题（**已于 2026-09-16 关闭**）

`tests/sprint7/qa/summary.json` 曾是 `2026-09-12T18:52:26Z` 的旧快照（sprint5 `105/1`、playwright `33/4/21`）。
现已由 2026-09-16 的一次性整体重跑覆盖：`build_api_sha=f10a1421…`、
`started_at=2026-09-16T09:57:44Z`、`finished_at=2026-09-16T10:12:00Z`，
sprint4 `81/0/0`、sprint5 `114/0/0`、sprint6 `159/0/0`、rbac `29/0`（BLOCKED 2）、iam `33/0`、
sprint12 `19/0`（BLOCKED 1）、playwright `37/0/21`，整体 `status: PASS`。
同一次冻结版本上另有浏览器补充档位 `25/0`（`ui-extended/result.json`）与 `go test ./...` 40 个包全绿。

### 0.5 状态更正三：2026-09-16 缺陷关闭轮与 D-15 的性质（不得改写）

- 本轮关闭的真实缺陷、根因与实测证据见 `ROUND-20260916-defect-closure.md`（D-01..D-15）。
- **§0.2 的更正针对的是 2026-09-13 那一轮的 4 条 FAIL（入口协议）**；2026-09-16 复跑出现的
  4 条 FAIL 是**不同**的用例集合，其中 sprint4 那条的根因是**真实产品缺陷**（D-15：
  commit gate 事务内审计写入与独立审计写入互等，导致 evidence 状态命令固定卡 60.0 s、
  并静默丢掉一条 `evidence:sensitive:read` 审计），不是入口协议或测试口径问题。
- 同一轮还关闭了两项 §7 缺陷（详见同文件 D-17/D-18）：**后台与用户端导航未按权限裁剪**（§7.5，
  现由 `tests/dual-system-nav-permission.spec.ts` 以"服务端为唯一判据"做双向验证）、
  **系统角色「有写无读」权限缺口**（§7.1/§7.4：Platform Admin 无法列出自己可审批的删除请求、
  Security Admin 无法列出自己可管理的告警与待审批项；迁移 `0065_role_read_permission_gaps.sql` 已补齐并自检）。
  §3 开放项 **UI-2（后台导航未按角色裁剪）就此关闭**。
- 第三轮（同日）关闭 **§16.7 / §7.3「功能开关必须在真实请求路径求值」**：新增 ADR-021 与套件
  `run-sprint7-featureflags.sh`（PASS 21 / FAIL 0），三条能力面已接线（Explorer 读端点、后台告警中心、
  控制面只读视图），关闭时 `503 FEATURE_DISABLED`，真实流量求值留痕；安全与恢复路径不挂开关。
  **仍有三个开关未接线**（`admin.provider_registry`、`admin.backup_restore`、`user.gauge_rollout`），
  作为公开未完成项记录在 `ROUND-20260916-defect-closure.md`。
### 0.6 状态更正四：§27 矩阵按冻结版本完整重建（2026-09-16 晚）

- `section27-conformance.md` 已**整体重建**（不再是 2026-09-13 快照 + 增量更正）：65 个 Test ID 全部绑定
  冻结版本 `backend/bin/api` `7831bff0…`（`worker` `0d49fb75…`、`migrate` `d48e58fe…`、两套前端
  `71714cdc…` / `2d4a129e…`、迁移头 `0065`），并逐条给出"路径 : 行号 + 原文数字/字符串"。
- 状态词表收敛为两个：**PASS** / **发布阻断**。旧版的 `PARTIAL 7 / NOT_IMPLEMENTED 1 / NOT_VERIFIED 1`
  与三条历史 `BLOCKED` 全部消灭（去向见矩阵 §5）。矩阵内 `FAIL=0 / BLOCKED=0 / NOT_VERIFIED=0 / PARTIAL=0`。
- 无法关闭的项以**发布阻断**显式列出：`DATA-007`（订阅者幂等无证据维度）、`DATA-009`（本构建恢复演练未执行）、
  §11 性能（6 个场景 p95 超 200 ms 预算，最差 `lan_longtable_p1` 8552.8 ms）、RPO/PITR 未重跑、回滚门禁无实测、
  `APP_ENV=production` 与灰度/切流/签署。详见矩阵 §4 与本文 §1。
- 范围决定已成文：`docs/implementation/release-scope-decision-20260916.md`（OIDC 不在本次范围；
  `admin.provider_registry` / `admin.backup_restore` / `user.gauge_rollout` 三个开关不在本次接线）。
- 修复后该组用例全部转绿（`37/0`），但**发布决定不因套件转绿而改变**：
  §1 的其余阻断理由（依赖/镜像、RPO/RTO、IaC 扫描、`APP_ENV`、OIDC 范围、功能开关、灰度与签署）仍然成立。

## 1. 决策

> **不批准全量切流（NO-GO for cutover）。**
> **批准保持当前灰度状态**：`/app-v2`、`/admin` 继续作为灰度入口对外提供，
> `/` 维持旧 SPA，**不执行** §11.6 的入口切换，**不执行**旧页面下线。

理由（按阻断强度排序）：

1. **【已更正，见 §0.2】§27 浏览器验收 4 条 FAIL 未消除**（`sprint7/qa/ui/playwright.log`）。
   —— 该条已由本地 TLS 验收入口修复：复跑 `37 passed / 0 failed / 21 skipped`。
   但**用户设定的"测试无 FAIL/NV"结项口径尚未整体满足**：`qa/summary.json` 未重算，
   且新增 §0.1 的 P0 未关闭。
2. **R-09 依赖 P0=2 / P1=5**，其中前端 `xlsx@0.18.5` 两条 HIGH 在 npm 上**无修复版本**。
3. **R-10 基础镜像 CRITICAL 20 / HIGH 203**（postgres:18 14C+101H、minio:latest 6C+100H）。
4. **R-11 IaC 配置面 NOT_VERIFIED**：现有工具链（trivy/checkov）结构性不覆盖本仓库的
   compose/systemd/nginx，不能把"扫描 0 文件"当作"0 问题"。
5. **RPO 未受控**：无调度备份、无 WAL/PITR，故障时数据回退窗口不可控。
6. **P1 性能**：explorer `activity` / `flow-profile` 单请求 8–10 s，超 §30.2 交互预算。
7. **生产环境保护未开**：`APP_ENV=development`（切流前必须为 `production`）。

## 2. 已满足的发布前置（可复算）

| 门禁 | 实测 | 证据 |
|---|---|---|
| 认证/授权：未认证写路径 | 已关闭（未认证 `DELETE /api/v1/cases/*` → 401） | `sprint7/release/release-gate.md` |
| 会话 Cookie | HTTPS 入口 `__Host-` + Secure + HttpOnly + SameSite（公网端到端复验） | `sprint7/security/S7-SEC-01-fixes-R05-R07.md` |
| 应用连接账号 | 非超级用户（`rolsuper=f`），`RESET ROLE` 不提权 | 同上 |
| 审计不可变边界 | 保持有效：`integrity_status=sealed_incident`、`prefix_status=sealed_unrecoverable` | `bin/auditverify` 输出 |
| 审计只读性 | `blockchain_app` 对 `audit_events_v2` 的 UPDATE/DELETE/TRUNCATE 全 `f` | 同上 |
| 安全负向用例 | **PASS 97 / FAIL 0 / NV 0** | `sprint7/security/negative-tests/result.json` |
| 秘密扫描 | 真实明文 0 | `sprint7/security/secret-scan/` |
| 备份恢复 | PG 5.81 s / CH 561 s / MinIO 0.63 s | `sprint7/dr/README.md` |
| 进程级容灾 | worker 5.09 s / api 19.2 s / NATS 33.37 s（看门狗） | 同上 |
| 内网 API 性能 | 8/8 场景 P95 < 200 ms | `sprint7/perf/README.md` |
| 后台权限声明覆盖率 | 105/105 | `sprint7/security/threat-matrix.md` |

## 3. 未解决 P2（不阻断灰度，需排期）

- 功能开关尚未接入请求路径自动求值（仅管理端试算）。
- 管理端无应用级移动端 CSS（继承 Shell 断点）。
- `/storage`、`/data-quality`、`/risk` 仍为显式占位页。
- MFA/TOTP 未实现；step-up 为密码重认证（无有效期窗口）。
- OIDC adapter 完成但未接真实 IdP（`/oidc/*` 返回 501）。
- `/api/v1` 兼容层 127 条路由无权限声明（读路径），属遗留面，计划随旧入口下线一并移除。
- DEF-02：`/api/v2/cases/{id}` 无 DELETE（合同未定义，**刻意不加**：新增破坏性端点需单独授权）。

## 4. 回滚点

| 层次 | 回滚点 | 命令 |
|---|---|---|
| 应用 | `backend/bin/api.bak-s7-<ts>`、`api.bak-dualsystem-*` | `cp bin/api.bak-… bin/api && systemctl --user restart blockchain-api` |
| 配置 | `deploy/.env.bak-s7-cookie-<ts>`、`.env.bak-s7-role-<ts>` | 覆盖回 `deploy/.env` 后重启 api/worker |
| 数据库 | `0056/0057` 对应的 `backend/migrations/rollback/rollback-00{56,57}.sql` | 见 `docs/manuals/rollback.md` |
| 数据 | `acceptance-evidence/dual-system-v1/sprint7/backups/pre-s7-pg-20260912T162853Z.dump`（迁移前快照） | `pg_restore` 到新库后切换 DSN |
| 容器 | `docker update --restart=unless-stopped`；看门狗持有位 `/run/blockchain/containers-watchdog.hold` | 见 `docs/manuals/operations.md` §13.3 |

## 5. 再次评审的进入条件

1. §27 浏览器套件 **FAIL=0**（4 条用例聚焦调试后复跑，并把结果写入 `sprint7/qa/ui/result.json`）。
2. `APP_ENV=production` 并在生产模式复跑一套冒烟（`AUTH_MODE` 强制 session）。
3. R-09/R-10 处置完成或有**书面风险接受**（含责任人）。
4. R-11 补齐 IaC 扫描（或明确的人工核对清单 + 签字）。
5. 备份调度上线（systemd timer）并至少完成一次无人值守的定时备份 + 恢复演练。
6. explorer 交互端点达标或产品侧明确接受（写进 SLO 例外）。
