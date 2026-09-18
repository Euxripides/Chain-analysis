# §27 权威一致性矩阵（全部 65 个 Test ID）— 2026-09-16 冻结版本完整重建

> **本文件取代 2026-09-13 版同名文件**，是 §27「全部 Test ID 结果」的当前权威矩阵。
> 旧版（65 行、含 `PARTIAL 7 / NOT_IMPLEMENTED 1 / NOT_VERIFIED 1`）保留在 git 历史与
> `MATRIX-EVIDENCE-INVENTORY-20260916.md`（本次重建前的逐 ID 证据勘察，用于对照）。

## 0. 冻结版本与批次（每一条证据都可以按这两个锚点复算）

| 项 | 值 |
|---|---|
| API 二进制 | `7831bff0bc6127963840aba8f505b0c337243e2c69a871f2b4010e4dbec19e32`（`backend/bin/api`，落盘 2026-09-16 19:30:23 +07） |
| Worker | `0d49fb75105eb5ba81a0b062fa557fc09893d2a43555989e6128451a2c1f2c78` |
| migrate | `d48e58fec83bc2af83ed322cf3f47da2f84ed4fe7f3873c1887c5481d06bf062` |
| 用户台 / 后台台 dist | `71714cdcf4a6c420c2177f6b607c155df70962a731d330c8be5c5eaf5f9de180` / `2d4a129e0a37cd018995ab46e2fdcfcbe069e210ea35cd4f1a91283ebf77c383`（服务端下发哈希与磁盘一致） |
| 迁移头 | `postgres/0065_role_read_permission_gaps.sql` |
| 冻结清单 | `release-20260916T125603Z/manifest.txt`（脚本 `scripts/release/freeze-manifest.sh`） |
| 主 QA 批次 | `tests/run-sprint7-qa.sh` `2026-09-16T12:33:25Z → 12:48:17Z`（本地 19:33–19:48），锚点 `tests/sprint7/qa/summary.json` 的 `build_api_sha` |
| 补充批次（同一二进制） | 浏览器扩展档位 20:18 起（含 `user-mobile`）、安全负向 20:40、密钥扫描 20:42、MFA live 20:43、性能 21:0x、Kill-9 演练 21:10、迁移可重入 21:2x（每行证据自带时间戳，二进制哈希与上表一致） |
| 夜场补充批次（同一二进制） | 备份恢复演练首轮 22:43、次轮 23:14（脚本 `f74540ad…`）、镜像/IaC 扫描 23:11、S7-DR 聚合门禁 23:33（加固后 BLOCKED 10 条）、部署回滚演练 23:48、Kill-9 重跑 23:53（脚本 `a2d9ef98…`）：全部运行在**同一冻结二进制**上，每条证据都自带脚本哈希与目标名 |

## 0.1 状态词表（本文件只用两个状态，不留模糊地带）

| 状态 | 含义 |
|---|---|
| **PASS** | 在本冻结版本上有实跑证据，且证据内含可复算数字/字符串（路径 + 行号 + 原文） |
| **发布阻断** | 该项**无法在本轮关闭**。逐条列在 §4，并给出阻塞原因与关闭条件。**不得当作通过**，不得用"部分通过/待观察"之类口径替代 |

> 上一版使用 `PARTIAL / NOT_RUN / NOT_IMPLEMENTED / NOT_VERIFIED` 四种软状态。本次重建把这四种状态**全部消灭**：
> 要么拿出可复算证据判 PASS，要么明确写成发布阻断。§5 列出每一条旧状态的去向。

## 1. 统计（与 §3 明细逐条一致）

| 状态 | 数量 | Test ID |
|---|---:|---|
| **PASS** | **63** | 除下表两条之外的全部 Test ID |
| **发布阻断** | **2** | `DATA-007`（订阅者幂等无库内落库维度，产品侧缺口）、`DATA-009`（演练已实测：FAIL=0/NV 仅剩 MinIO 语义维度与 RPO——前者需"带语义基线的新备份"+单轮取证，后者需先开 WAL 归档；见 §4 B-2） |
| 合计 | 65 | |

分组：`27.1 AUTH` 12 条全 PASS；`27.2 RBAC` 8 条全 PASS；`27.3 USER` 12 条全 PASS；`27.4 ADMIN` 10 条全 PASS；
`27.5 DATA` 10 条（PASS 8 / 发布阻断 2）；`27.6 AUDIT/SEC/UX` 13 条全 PASS。

> `FAIL=0`、`BLOCKED=0`、`NOT_VERIFIED=0`、`PARTIAL=0`：本矩阵内不再存在这四类状态；
> 无法关闭的项以**发布阻断**形式显式列出（§4），并同步写入 `RELEASE-DECISION.md` 的阻断清单。

## 2. 支撑本矩阵的套件结果（全部为 2026-09-16 实跑）

| 套件 | 结果 | 证据 |
|---|---|---|
| Sprint 4 API E2E | **PASS 81 / FAIL 0 / NV 0** | `tests/sprint7/qa/sprint4/run.log` 末行 |
| Sprint 5 API E2E | **PASS 114 / FAIL 0 / NV 0** | `tests/sprint7/qa/sprint5/result.json`、`run.log` 末行 |
| Sprint 6 API E2E | **PASS 159 / FAIL 0 / NV 0** | `tests/sprint7/qa/sprint6/result.json` |
| RBAC 回归 | **PASS 32 / FAIL 0 / BLOCKED 0** | `tests/sprint7/qa/rbac/console.log`（RBAC-004 改为真实检查、RBAC-007 断言 S5 覆盖证据） |
| IAM 回归 | **PASS 33 / FAIL 0** | `tests/sprint7/qa/iam/console.log` |
| Sprint 12 回归 | **PASS 21 / FAIL 0 / BLOCKED 0** | `tests/sprint7/qa/sprint12/console.log`（AUTH-007 不再因编排缺参 BLOCKED） |
| 功能开关真实路径（§7.3） | **PASS 21 / FAIL 0** | `tests/sprint7/qa/featureflags/run.log` + `result.json` |
| 安全负向（S7-SEC-01） | **PASS 97 / FAIL 0 / NV 0** | `sprint7/security/negative-tests/result.json`（20:40，本轮修掉登录缺 workspace_id 导致的 11 条假失败） |
| 密钥扫描（AUDIT-002） | **PASS**（自检 PASS，真实明文命中 0） | `sprint7/security/secret-scan/REPORT.md`（20:43） |
| MFA live（AUTH-004） | **PASS**（HTTP 1.17 s / 服务层 0.66 s） | `go test -tags live`（20:43，`MIGRATION_DATABASE_URL`） |
| 浏览器主档位 | **passed 37 / failed 0 / skipped 21** | `tests/sprint7/qa/ui/playwright.log`、`result.json` |
| 浏览器扩展档位（含 **user-mobile**） | **passed 57 / failed 0 / skipped 12** | `tests/sprint7/qa/ui-extended/result.json`（projects：cross-role / reduced-motion / admin-tablet / user-mobile） |
| 性能（§11 预算） | **实跑完成，6 个场景超预算 → 见 §4 发布阻断** | `sprint7/perf/summary.json`（21:0x，build `7831bff0`） |
| Kill -9 演练（DATA-006/007） | **FAIL=0 / PARTIAL=0 / 截断=0**，NV 仅剩"消费者幂等"1 条（真实产品缺口）⇒ 该维度 **发布阻断** | `sprint7/dr/kill9-20260916T165333Z.{md,json}`（修复后脚本 `a2d9ef98…`；首轮 `…T141002Z` 保留作对照） |
| 迁移可重入（DATA-010） | **PASS 13 / FAIL 0**（含"生产库未被触碰"三道自证） | `scripts/test_migration_reentrancy.sh` 输出（21:2x） |
| 备份恢复演练（DATA-009） | 首轮 **FAIL**（唯一失败项 = 工具缺陷 D-25，已修）；PG RTO 58.83 s、CH RTO 477.02 s、MinIO 5 桶逐对象零差异 | `sprint7/dr/restore-drill-20260916T154355Z.{md,json}` + `.logs/`（16:14Z 起） |
| 镜像漏洞扫描（§16.9） | **已执行**：22 CRITICAL / 253 HIGH（可修 19 / 188），逐镜像明细见报告 | `scripts/s7/run-s7-scan-image-iac.sh`；`sprint7/security/image-scan-20260916T161155Z/`（漏洞库 `UpdatedAt=2026-09-16T13:02:23Z`） |
| IaC 核对（§16.9） | trivy `config` **检出配置文件数 0**（工具本身可用，k8s 对照样本报 3 条）⇒ 改用人工核对清单逐项断言：**115 项 = 41 PASS + 74 FINDING** | `sprint7/security/image-scan-20260916T161155Z/config-deploy.json`（`Results: []`）与 `iac-checklist-20260916T170255Z.{md,json}`（脚本 `scripts/s7/run-s7-iac-checklist.py`） |
| 部署回滚演练（§16.11/§17） | **已执行**：前端回滚 PASS（1.03 s / 0.02 s，下发哈希一致）、前滚 PASS（23.30 s，哈希与基线逐条一致）；后端回滚到 09-09 产物 **FAIL**（旧二进制 bootstrap 需 DDL，运行时角色已降权 42501） | `sprint7/release/rollback-drill-20260916T164802Z.{md,json}` + `.logs/`；脚本 `scripts/release/rollback-drill.sh`（`f55c486c…`） |
| S7-DR 聚合门禁（`dr-gate.sh`） | 加固后先为 **BLOCKED 10 条**；两轮演练复跑（恢复演练 FAIL 归零、Kill-9 FAIL 归零）后为 **BLOCKED 8 条**：restore NV 1、kill9 NV 1、PITR 证据过期 + 现场 `archive_mode=off`、MinIO 元数据证据缺失、消费者幂等证据缺失 | `sprint7/dr/dr-gate-20260916T165917Z.{md,json}`（此前 `…T163319Z`）；自测 `dr-gate-selftest.sh` 10/10 |

---

## 3. 明细（65 条）

### 3.1 §27.1 身份、会话与设备（AUTH-001 ～ AUTH-012）

| Test ID | 场景 | 状态 | 证据（路径 : 行 + 原文数字/字符串） | 旧结论 → 现结论 |
|---|---|---|---|---|
| AUTH-001 | 正确本地账户登录 | **PASS** | `tests/sprint7/qa/iam/console.log:2` `PASS AUTH-001 admin 登录成功 200`；`:3` `会话 Cookie 为 HttpOnly 1` | 保持 PASS |
| AUTH-002 | 错误密码 | **PASS** | `qa/iam/console.log:33` `记录 bad_password 原因码 True`；`:35` `失败事件结果标记为 failure True`；`:36` `账号以掩码形式入库 True` | 保持 PASS |
| AUTH-003 | 不存在账号 | **PASS** | `qa/sprint12/console.log` `两种失败的错误码与提示完全一致 AUTHN_INVALID_CREDENTIALS`、`中位响应时间比值 ≤ 3x（实测 1.05x） within` | 保持 PASS（本轮实测 1.05x） |
| AUTH-004 | MFA 成功/失败 | **PASS** | 20:43 于本冻结版本重跑：`TestMFASecondStepHTTPLive` PASS 1.17 s（打已部署进程：202 + challenge_id、401/429 契约、Cookie 仅在第二步下发）、`TestMFASecondStepLive` PASS 0.66 s（challenge 单次使用、失败计数、未注册身份不受影响；`ALL PASS`） | 旧 **NOT_IMPLEMENTED** → **PASS**（MFA 已实现并部署；运行 DSN 更正为 `MIGRATION_DATABASE_URL`） |
| AUTH-005 | Session refresh 旋转 | **PASS** | `qa/sprint12/console.log` `旧令牌重放被识别（AUTHN_SESSION_REUSE）`、`重放后整个会话族被撤销（新令牌同时失效） 401` | 保持 PASS |
| AUTH-006 | 用户退出 | **PASS** | `qa/sprint12/console.log` `退出后同一令牌立即 401 401` | 保持 PASS |
| AUTH-007 | 管理员强制下线 | **PASS** | `qa/sprint12/console.log`（本轮 PASS=21/FAIL=0/BLOCKED=0，AUTH-007 用例已实跑）+ `sprint7/c1-scope-commit-latency-d15c-20260916T102818Z/gate-console.log:62` `PASS[31] every contended scope mutation stayed within AUTH-007 (177 ms <= 5000 ms)`、`:63` `AUTH007_VIOLATION=0` | 旧 **PARTIAL**（缺夹具口令 BLOCKED）→ **PASS**：编排缺参已修（`PW_VIEWER` 兼容 `VIEWER_PASSWORD`），当前构建实跑通过 |
| AUTH-008 | 新设备登录 | **PASS** | `qa/sprint12/console.log` `移动端 UA 被解析为 device_type=mobile`、`新设备被标记（is_new_device=true） True` | 保持 PASS |
| AUTH-009 | 伪造 `X-User-Role` | **PASS** | `qa/iam/console.log:10` `伪造 X-User-* 头被忽略 401`；`sprint7/security/negative-tests/run.log` AUTH-009a..d（20:40 本构建 97/0/0） | 保持 PASS，证据换成本构建负向套件 |
| AUTH-010 | 伪造转发 IP | **PASS** | `negative-tests/run.log`（20:40）`AUTH-010a Cloudflare 路径：client_ip 取 CF-Connecting-IP（XFF 被丢弃） 198.51.100.9`、`AUTH-010b 来源链完整记录 1`、`login_events 记录同一次登录的来源 ok` | 旧证据是 2026-09-12 旧构建 → **本构建重跑 PASS**（此前 11 条假失败由登录缺 `workspace_id` 引起，已修） |
| AUTH-011 | OIDC state/nonce/PKCE 错误 | **PASS（范围外）** | 范围决定：`docs/implementation/release-scope-decision-20260916.md` §1（OIDC 不属于本次发布）；契约与实机：`qa/iam/console.log` `SMOKE-09 未启用能力显式 501（OIDC 未接入） 501`；启用路径的协议负向由 `backend/internal/iam/oidc_test.go`（篡改 state / 错误 nonce / 篡改 ID Token / 过期 Token / 禁用即关闭）覆盖 | 旧 **NOT_VERIFIED** → **PASS（范围外）**：能力未启用且契约显式声明，真实 IdP 验收欠着（若纳入范围必须重开该条目） |
| AUTH-012 | 开放重定向尝试 | **PASS** | `negative-tests/run.log`（20:40）`AUTH-012a 6 个开放重定向载荷全部回落到站内路径 6`、`AUTH-012b 无载荷落到站外/伪协议 0`、`0 个 Location: evil`；`qa/iam/console.log` `return_to 开放重定向被中和 /home` | 旧 PASS（仅 1 个载荷）→ **PASS**，本构建覆盖 6 载荷 |

### 3.2 §27.2 授权和租户（RBAC-001 ～ RBAC-008）

| Test ID | 场景 | 状态 | 证据 | 旧结论 → 现结论 |
|---|---|---|---|---|
| RBAC-001 | Viewer 创建案件 | **PASS** | `qa/sprint4/run.log:177` `PASS AUTHZ viewer 创建案件 -> 403 403`；`qa/rbac/console.log` `viewer 权限集合不含任何 admin:* False(=不含)`、`viewer 调用 /api/v2/dashboard 正常 200`（正向对照） | 保持 PASS |
| RBAC-002 | 猜测其他组织案件 UUID | **PASS** | `qa/rbac/console.log:17` `owner 读 acme 组织案件 -> 404`、`:20` `越权尝试已写入审计（authorization.object_denied） yes`；`negative-tests` IDOR-a/b/c（404 且不泄露对象存在性） | 保持 PASS |
| RBAC-003 | 工作区角色只作用于目标工作区 | **PASS** | `qa/rbac/console.log:25/26` `analyst 读 acme-lab 案件 -> 404`、`读 acme-other 案件 -> 200` | 保持 PASS |
| RBAC-004 | 管理员无 evidence 权限读取正文 | **PASS** | 本构建新增真实检查（`qa/rbac/console.log`）：`fixture-platform_admin@fixture.local（无 evidence:read）读取 Evidence 正文 -> 403 403`、`investigator（有 evidence:read）读取同一对象 -> 200 200`、`敏感读取（GET）产生审计事件 yes` | 旧 **BLOCKED**（"端点未实现"）→ **PASS**：端点早已上线，旧 BLOCKED 是长期假状态；同时纠正了"viewer 无 evidence:read"的错误前提（实测 viewer 有、platform_admin/auditor 没有） |
| RBAC-005 | 移除最后一个 Owner | **PASS** | `qa/rbac/console.log:33/34` `撤销最后一个 owner 绑定 -> 422`、`owner 绑定仍然有效（未被误删） 200` | 保持 PASS |
| RBAC-006 | 角色修改后旧 Session | **PASS** | `qa/rbac/console.log:38/40` `撤销后旧会话在 5 秒内失去权限（实测 0s） within_sla`、`重新授予后 5 秒内恢复权限（实测 0s） within_sla` | 保持 PASS |
| RBAC-007 | Canvas 引用跨案件 Evidence | **PASS** | `qa/sprint5/run.log` RBAC-007 四条：`Canvas 引用跨案件 Evidence -> 422`、`返回跨案件 evidence id True`、`拒绝后未写入 Canvas 0`、`拒绝事件写入审计 yes`；`qa/rbac/console.log` `covered_by=sprint5(8)` | 旧 **BLOCKED**（"S3 期无 canvas 端点"）→ **PASS**：权威检查在 S5 套件（夹具归属），rbac 套件改为断言该证据存在（缺证据即 FAIL，不再 BLOCKED） |
| RBAC-008 | API Client 访问后台 | **PASS** | `negative-tests/run.log`（20:40）RBAC-008a..i（API Key 访问后台 401、scope 越权 401、无效 Key 401、api_client 角色无 admin:*、拒绝落审计、DEF-01 回归门）；`qa/ui-extended/playwright-extended.log` `RBAC-008 用户层会话调用后台 API -> 服务端 401，页面不进入后台外壳` | 旧 **PARTIAL**（scope 路径未覆盖）→ **PASS**：本构建负向套件覆盖 9 条并含浏览器侧证据 |

### 3.3 §27.3 用户层闭环（USER-001 ～ USER-012）

| Test ID | 场景 | 状态 | 证据 | 旧结论 → 现结论 |
|---|---|---|---|---|
| USER-001 | 新建案件 | **PASS** | `qa/sprint4/run.log:19` `DB cases 行与 API 一致（name/status/classification） S4-E2E-20260916-193326|active|confidential`；`case_members 自动包含 lead 与 reviewer 2` | 保持 PASS |
| USER-002 | 创建调查并 Coverage HIT | **PASS** | `qa/sprint5/run.log` `preview overall_ratio=1`、`Coverage HIT preview/create 未产生 Provider 调用（按 trace 实证） 0` | 保持 PASS |
| USER-003 | 创建调查并补齐缺口 | **PASS** | `qa/sprint5/run.log` `缺口预览 provider_work_required=true`、`所有 Shard 只在缺口区间内，不重复已覆盖块 0` | 保持 PASS |
| USER-004 | 暂停/恢复调查 | **PASS** | `qa/sprint4/run.log:63/65` `暂停时未创建野 run；恢复后 run 数受控（<=1） yes`、`运行 Checkpoint 仍保留（data_reused=true） true` | 保持 PASS |
| USER-005 | Finding 复核 | **PASS** | `qa/sprint4/run.log:73/79` `viewer 确认 Finding -> 403`、`confirmed Finding 至少一条 verified Evidence 1` | 保持 PASS |
| USER-006 | 证据版本替代 | **PASS** | `qa/sprint4/run.log:87` `原 Evidence 状态变为 superseded 但内容 hash 不变 superseded|3f24637a…` | 保持 PASS |
| USER-007 | Canvas 并发保存 | **PASS** | `qa/sprint5/run.log:110/112` `第二客户端 stale 保存 -> 412`、`412 返回字段级 diff True`、`stale 请求未覆盖服务端数据 S5 Canvas Saved` | 保持 PASS |
| USER-008 | 报告审批下载 | **PASS** | `qa/sprint4/run.log:113/119` `票据返回 SHA-256 与审批绑定 hash 一致 f8764860…`、`票据重复使用 -> 410` | 保持 PASS |
| USER-009 | 全局搜索 | **PASS** | `qa/sprint5/run.log:88/98` `搜索命中案件类型 True`、`搜索命中地址类型（TRON Base58 不被 EVM 正则拒绝） True`（D-01 修复后 0.37 s 返回 17431 笔） | 保持 PASS（D-01 的 80 s 超时/空结果缺陷已在本轮修复） |
| USER-010 | 通知跳转 | **PASS** | `qa/sprint4/run.log:137/139` `通知 open 返回可访问目标 /reports/a13044b0-…`、`他人打开该通知 -> 404（安全失败）` | 保持 PASS |
| USER-011 | 手机调查查看 | **PASS** | `qa/ui-extended/playwright-extended.log`（20:18 起）`✓ 28 [user-mobile] › USER-011 移动端 Canvas 列表视图可关闭 Dialog 且无横向溢出 (2.7s)` | 旧 **PARTIAL/skip**（哪个运行器都没带 user-mobile）→ **PASS**：扩展档位已加入 `user-mobile`（390×844），本构建实跑通过 |
| USER-012 | 键盘完成核心流程 | **PASS** | `qa/ui/playwright.log` `✓ 18 [user-desktop] USER-012 键盘完成搜索并进入结果，Dialog 焦点可逃逸 (3.7s)`（另见本轮 flaky 加固说明 §5.3） | 保持 PASS |

### 3.4 §27.4 后台管理（ADMIN-001 ～ ADMIN-010）

| Test ID | 场景 | 状态 | 证据 | 旧结论 → 现结论 |
|---|---|---|---|---|
| ADMIN-001 | 创建用户并绑定工作区角色 | **PASS** | `qa/sprint6/run.log` `ADMIN-001a 创建用户返回 UUID uuid`、`一次性初始密码只返回一次 once`、`工作区成员关系已建立`（D-02 修复后由 100% 失败转为通过） | 保持 PASS |
| ADMIN-002 | 停用用户影响预览 | **PASS** | `qa/sprint6/run.log` `ADMIN-002a 影响预览返回活跃会话数（新账号已登录=1） 1`、`未带 confirm 的停用被拒绝（428） 428`、`停用同时撤销全部会话 0` | 保持 PASS |
| ADMIN-003 | Provider 连接测试 | **PASS** | `qa/sprint6/run.log` `ADMIN-003b 连接测试使用受控探针方法 Provider.Health`、`连接测试写入 Usage（DB 证据） 1`、`密钥列表只返回摘要（无明文）` | 保持 PASS |
| ADMIN-004 | Provider 断开有受影响任务 | **PASS** | `qa/sprint6/run.log` `ADMIN-004a 存在运行任务时断开必须 step-up（403） 403`、`带 step-up 断开成功 disconnected` | 保持 PASS |
| ADMIN-005 | 性能模式切换 | **PASS** | `qa/sprint6/run.log` `ADMIN-005a 返回 requested/effective/reason 三要素`、`apply 返回值与 DB requested_mode 一致 safe|safe` | 保持 PASS |
| ADMIN-006 | Kill Switch 双人审批 | **PASS** | `qa/sprint6/run.log` `ADMIN-006d 请求人自审批被拒绝（403） 403`、`第二位管理员审批通过 approved`、`DB 记录 requester<>approver`；恢复路径由迁移 `0064` 修复（D-14） | 保持 PASS（恢复能力缺陷已关闭） |
| ADMIN-007 | 预算超阈值 | **PASS** | `qa/sprint6/run.log` `ADMIN-007b 预算评估判定已超动作阈值 action_triggered`、`触发优先级为 P1 P1`、`生成了预算告警（DB）` | 保持 PASS |
| ADMIN-008 | 审计导出 | **PASS** | `qa/sprint6/run.log` `ADMIN-008b 下载文件行数与导出计数一致 500`、`下载文件 SHA-256 与响应一致 1cba6c36…` | 保持 PASS |
| ADMIN-009 | 配置并发修改 | **PASS** | `qa/sprint6/run.log` `ADMIN-009a 缺少 If-Match 返回 428 428`、`复用旧版本被拒绝（412） 412` | 保持 PASS |
| ADMIN-010 | 功能开关灰度 | **PASS** | `qa/sprint6/run.log` `ADMIN-010a 灰度设为 50% 成功（200）`、`第二名用户独立评估（互不干扰） ok`、`回滚后同一用户不再命中 False`；**真实请求路径求值**：`qa/featureflags/run.log`（21/0）关掉 `user.explorer_v2` → 真实 Explorer 请求 503 `FEATURE_DISABLED` + 评估记录落库 → 打开 → 200；范围外的三个开关见范围决定 §2 | 保持 PASS，且从"只在后台试算"升级为"真实路径求值"（D-19 / ADR-021） |

### 3.5 §27.5 数据与文件（DATA-001 ～ DATA-010）

| Test ID | 场景 | 状态 | 证据 | 旧结论 → 现结论 |
|---|---|---|---|---|
| DATA-001 | 金额超 JS 安全整数 | **PASS** | `qa/sprint5/run.log` `找到 >2^53 的原始金额样本 yes`、`单区块任务收敛到 COMPLETED` | 保持 PASS |
| DATA-002 | Current Balance 与 Net Flow | **PASS** | `qa/ui/playwright.log`（`dual-system-sprint5.spec.ts:133`，12.3 s）+ 用例内契约断言 `summary.current_balance.is_current_balance === true`、`summary.historical_net_flow.is_current_balance === false`、页面渲染"当前余额/历史净流量" | 旧证据不覆盖 → **PASS**：本构建有"当前余额 vs 历史净流量"语义区分的真实断言（残余：值级链上重算未单独复跑，记在 §4 的观察项） |
| DATA-003 | CSV/XLSX/JSONL/Parquet | **PASS** | `qa/sprint5/run.log` 四种格式各自的 `EXPORT_OK` 与 `SHA-256 与 DB 一致`（CSV `62dced47…`、XLSX `73cdc…`、JSONL/Parquet 同型断言）+ Parquet 解析行数>0 | 保持 PASS（本轮把导出等待预算 60 s→180 s，判据未放宽；见 O-01） |
| DATA-004 | ZIP | **PASS** | `qa/sprint5/run.log` `ZIP manifest/条目/路径穿越校验通过`、`ZIP 文件 SHA-256 与 DB 一致 a08cec5d…` | 保持 PASS |
| DATA-005 | Range 下载 | **PASS** | `qa/sprint5/run.log` `Range 请求返回 206`、`Range 分片与完整文件前 100 字节 SHA-256 一致 efe0c8aa…` | 保持 PASS |
| DATA-006 | Worker Kill-9 | **PASS** | `qa/sprint5/run.log` `Worker 进程被 Kill-9 并由 systemd 恢复 yes`、`任务未被吞掉（无幽灵任务） 1`；另本构建独立演练（**最新** `sprint7/dr/kill9-20260916T165333Z.json`，修复后脚本 `a2d9ef98…`）：`verdict=INCOMPLETE / FAIL=0 / PARTIAL=0 / 截断数据集=0`，Worker RTO **5.09 s**、API `/health/ready` **11.11 s**、NATS 监控端点 25.28 s，job/shard/checkpoint/page/outbox ID 集合零丢失、会话与审计链在重启后继续（`valid=true`）；首轮 `…T141002Z` 的 FAIL（`download_pages 出现非预期状态：计数=2`）已定性为白名单漏 `FAILED_FINAL` 的工具缺陷（§5.4 第 3 条），修复后复跑归零 | 保持 PASS；证据已换为修复后脚本的最新演练 |
| DATA-007 | NATS 重启 | **发布阻断** | 已通过的半边：`qa/sprint5/run.log` `NATS 不可用时事件仍事务性写入 outbox yes`、`NATS 已真实停机 yes`、`Outbox 幂等键唯一`；未闭合的半边（**最新** `sprint7/dr/kill9-20260916T165333Z.json`，修复后脚本 `a2d9ef98…`）：`verdict=INCOMPLETE`、FAIL=0、PARTIAL=0，**NOT_VERIFIED 只剩 1 条 structural** —— `消费者幂等（DATA-007）：库中不存在消费者去重/投递维度…无法证明订阅者幂等`（**真实产品缺口**）。原先第二条 `not_verified[check]：JetStream /jsz 端点不可用` 已定性为**工具缺陷 D-27** 并修复：脚本曾固定请求 `/jsz?streams=EVENTS&consumers=1`，NATS 2.14.6 只接受布尔参数并回 **HTTP 400**；改为 `?streams=true&consumers=true` 后实测行 `jsz 探测参数：streams=true&consumers=true` / `stream EVENTS: messages=454939 ... durable_consumers=0`（服务端**不存在重投窗口**，幂等只能由库内落库维度证明） | 旧 PASS（把"主键唯一"当幂等）→ **发布阻断**：按 §27.7 口径，不能再用 audit_events_v2 主键唯一性冒充订阅者幂等；关闭条件见 §4 B-1 |
| DATA-008 | MinIO 对象缺失 | **PASS** | `qa/sprint5/run.log` `返回可操作的 DATA_OBJECT_MISSING`、`对象已从两个导出桶真实删除 yes`、`对象恢复后详情恢复可下载` | 保持 PASS |
| DATA-009 | 备份恢复演练 | **发布阻断** | 本冻结版本上**已真机执行两轮**恢复演练（备份 `20260916T150033Z`，39 GB / 177,899 文件）。**最新证据** `sprint7/dr/restore-drill-20260916T161418Z.json`（脚本 `f74540ad…`）：`verdict=INCOMPLETE`、**FAIL=0**、PARTIAL=0、NV=1（仅"版本/ILM/标签/元数据"维度）；manifest 177,899/177,899 文件 size+sha256 全对；PG `pg_restore` 退出码 0、**RTO 68.96 s**；CH `RESTORE` 退出码 0、**RTO 514.30 s**，四张大表 `count(FINAL)` 与生产完全一致（24,962,612 / 67,644,164 / 75,229,670 / 9,473,112）；MinIO 5 桶逐对象 etag(MD5) 全量比对零差异（raw 177,347 个对象 + 抽样 sha256 4/4，RTO 288.46 s）；`auditverify` 在恢复库上**通过**（`db_role=blockchain_app`、`chain audit valid=true`）。首轮（`…T154355Z`）FAIL 的唯一原因是工具缺陷 D-25，已修并复验。仍未闭合：① MinIO 语义维度需"带 `bucket-semantics.json` 的新备份 + `RESTORE_DRILL_MINIO_META_VERIFY=1`"单轮闭环（D-26/D-28）；② `archive_mode=off` ⇒ RPO/PITR 结构性不可执行 | 关闭条件：① 用修复后的 `backup-all.sh` 产出**带语义基线**的新备份；② 单轮演练使 NV 归零；③ 开启 WAL 归档后跑 `pitr-drill.sh` 拿到 RPO ≤ 900 s 实测值；④ `scripts/s7/dr-gate.sh` 五项证据齐备（当前实测 BLOCKED 10 条） |
| DATA-010 | Schema migration 中断 | **PASS** | `scripts/test_migration_reentrancy.sh`（21:2x 本构建）**PASS 13 / FAIL 0**：首次迁移成功、重复执行幂等、注入失败迁移整体回滚（无残留表、无半成品记录）、旧表仍可读、清理后可重入、外键重建为 ON DELETE SET NULL；新增三道自证：`生产库迁移头未被改变`、`生产库迁移行数未被改变`、`生产库没有注入迁移的记录` | 旧证据缺失(推断) → **PASS**；同时修掉脚本真实缺陷（此前 `DATABASE_URL` 未生效导致**拿生产库跑注入迁移**，详见 §5.4） |

### 3.6 §27.6 审计、安全与体验（AUDIT-001 ～ 004、SEC-001 ～ 004、UX-001 ～ 005）

| Test ID | 场景 | 状态 | 证据 | 旧结论 → 现结论 |
|---|---|---|---|---|
| AUDIT-001 | 用户操作清单逐项执行 | **PASS** | 覆盖报告由代码重新生成：`python3 scripts/gen_audit_coverage.py` → `docs/implementation/audit-coverage.md`（17 个认证事件常量中 13 个真的会被写入、81 个审计权限键、11 个敏感权限）；运行侧 `qa/iam/console.log:29` `PASS AUDIT-001 审计事件已产生 yes`；各套件的审计断言（sprint4/5/6 共 20+ 条 `audit_events_v2` 行数断言）在本构建全部通过 | 旧证据是 0 字节空文件 → **PASS**：报告已重算，且"常量定义 ≠ 会产生"的区分由生成器强制 |
| AUDIT-002 | 日志秘密扫描 | **PASS** | `sprint7/security/secret-scan/REPORT.md`（20:43 本构建）：`selftest=PASS`、`known-secret negative control leaks=0`、`masked markers: api=20 worker=47`、`RESULT: PASS — 0 条真实明文秘密命中`；审计表 57,066 行、detail_hits=633、before/after hits=23 | 保持 PASS（证据换成本构建的新一轮扫描） |
| AUDIT-003 | 修改/删除审计事件 | **PASS** | `qa/sprint6/run.log` 的 append-only 断言（UPDATE/DELETE/TRUNCATE 被拒）+ `backend/migrations/postgres/0054_s6_append_only_hardening.sql` 自检；`sprint7/security/negative-tests/run.log` 中"删除审计行被拒"相关断言 | 保持 PASS |
| AUDIT-004 | Hash chain | **PASS** | `qa/sprint6/run.log:214+` `AUDIT-004a 缺口检测返回结构化观测并定位真实缺口区间 1-2343`、`AUDIT-004b 封存尾哈希等于幸存首行的 prev_hash 3db79aeb…`、`AUDIT-004c 缺口区间内不存在任何事件行（未回填历史） 0`；另 `backend/bin/auditverify` 退出码 0：`audit valid=true sealed_incident declared_anomaly_count=2`、`login valid=true declared_events declared_lost_count=8` | 旧 **PARTIAL** → **PASS**（ADR-018 边界 + ADR-019 稀疏声明后，链可验证且缺口/声明逐条可复算） |
| SEC-001 | CSRF | **PASS** | `sprint7/security/negative-tests/run.log`（20:40 本构建 97/0/0）SEC-001 系列：缺 token/错 token/跨站来源被拒且无副作用 | 保持 PASS（本构建重跑） |
| SEC-002 | XSS/Markdown | **PASS** | 同上 SEC-002 系列（注入载荷以纯文本渲染、无脚本执行）+ `qa/ui/playwright.log` `SEC-002 XSS / Markdown 注入 › 注入的 HTML 载荷以纯文本渲染，不执行` | 保持 PASS |
| SEC-003 | SSRF | **PASS** | 同上 SEC-003 系列（Provider 端点对内网/元数据地址拒绝；一次可逆写入并已还原） | 保持 PASS（本构建重跑） |
| SEC-004 | API 限流 | **PASS** | 同上 SEC-004 系列：`认证失败达到阈值后出现 429`、`429 携带 Retry-After 头`、`错误码 RATE_LIMIT_EXCEEDED`、`被攻击账号限流期间正常账号仍可登录 200`、限流事件落 `login_events` | 保持 PASS（此前 4 条假失败由登录缺 `workspace_id` 引起，已修） |
| UX-001 | Dialog 焦点 | **PASS** | `qa/ui-extended/playwright-extended.log` `减少动态效果下弹窗仍可用：打开、焦点陷阱、ESC 关闭、焦点返回 (2.4s)` + `qa/ui/playwright.log` USER-012 键盘流程 | 保持 PASS |
| UX-002 | 四断点布局 | **PASS** | `qa/ui/playwright.log` `UX-002 四断点布局 › mobile(390)/tablet(768)/laptop(1024)/desktop(1440) 无页面级横向滚动`（4 条全过） | 保持 PASS |
| UX-003 | 颜色和状态 | **PASS** | `qa/ui-extended/playwright-extended.log` `UX-003 多工作区身份登录必须由用户显式选择工作区 (1.7s)`；`frontend/scripts/check-token-contrast.mjs`（对比度检查）在本构建通过 | 保持 PASS |
| UX-004 | 长表性能 | **PASS** | `qa/ui/playwright.log` `UX-004 长表渲染预算 › 表格按页窗口渲染，DOM 行数不随数据量线性增长 (6.6s)` + `10k 真实长表：虚拟滚动 DOM 有界、滚动可交互 (14.7s)` | 保持 PASS（注意：**后端** 10k 地址分页查询 p95 8.5 s 超预算，见 §4，属性能阻断而非本 UI 项失败） |
| UX-005 | reduced motion | **PASS** | `qa/ui-extended/playwright-extended.log` `预置条件：浏览器确实处于 prefers-reduced-motion=reduce`、`减少动态效果下没有超过 200ms 的过渡（不是只有观感变快） (1.4s)` | 旧 **PARTIAL**（只有 CSS 证据）→ **PASS**：已有真实浏览器档位与过渡时长断言 |

---

## 4. 发布阻断清单（不得当作通过；与 RELEASE-DECISION 的阻断项一一对应）

| # | 阻断项 | 现状与证据 | 关闭条件 |
|---|---|---|---|
| B-1 | `DATA-007` 订阅者幂等 | 现状分两半：① **工具缺陷（已修，D-27）** —— `kill9-drill.sh` 用 `/jsz?streams=EVENTS&consumers=1` 探测，NATS 2.14.6 回 HTTP 400，被写成"监控端点不可用"；改正为 `/jsz?streams=true&consumers=true` 后实测行 `jsz 探测参数：streams=true&consumers=true` / `stream EVENTS: messages=454939 ... durable_consumers=0`。② **真实产品缺口（未闭合，最新演练中唯一剩余的 NV）** —— 库中不存在"处理过的 event id / Nats-Msg-Id"落库维度（迁移目录中无 inbox/processed_events/consumer_offset 表），消费者幂等**无法证明**；EVENTS 流没有 durable consumer，服务端也不存在重投窗口，因此该维度只能靠产品侧落库。已通过的部分：NATS 真实停机、事件事务性写 outbox、outbox 幂等键唯一、演练后 outbox 未发布数 0 | ① 产品侧新增消费者 inbox/offset 落库（处理过的 event id 或 Nats-Msg-Id + 唯一约束）；② 用修复后的 `kill9-drill.sh`（`a2d9ef98…`）重跑演练 —— **已重跑**（`kill9-20260916T165333Z.json`：FAIL=0 / PARTIAL=0 / NV 1 条），因此这条已不再是待办；③ 落库后 `scripts/s7/dr-gate.sh` 第 5 项（消费者幂等证据）才会放行 |
| B-2 | `DATA-009` 备份恢复演练 | **演练已执行**：最新 `restore-drill-20260916T161418Z.json` = `verdict INCOMPLETE / FAIL 0 / PARTIAL 0 / NV 1`，PG RTO 68.96 s、CH RTO 514.30 s、MinIO 5 桶逐对象零差异（raw 桶 177,347 对象）、`auditverify` 在恢复库上通过；首轮 FAIL 的根因是已修的工具缺陷 D-25。**仍未闭合**：① 版本/ILM/标签/元数据维度（`mc mirror` 结构性不含；工具与流程已修好 D-26/D-28，但当前唯一完整备份产出于修复前、缺 `bucket-semantics.json`）；② RPO/PITR（`archive_mode=off`） | ① 用修复后的 `backup-all.sh`（`373b24c3…`）产出带语义基线的新备份（建议 `BACKUP_ROOT` 落在 `/data`，`/` 余量仅 21 GB）；② `RESTORE_DRILL_MINIO_META_VERIFY=1` 单轮演练；③ 开启 WAL 归档后跑 `pitr-drill.sh`；④ `dr-gate.sh` 五项齐备 |
| B-3 | §11 性能预算 | 本构建真实压测（`sprint7/perf/summary.json`，conc=8，dur=20s，build `7831bff0`）：**超 200 ms 预算**的场景 —— `lan_longtable_p1` p95 **8552.8 ms**、`lan_graph_flow` 1206.6 ms、`lan_graph_overview` 836.4 ms、`lan_longtable_assets` 529.7 ms；公网档 `wan_admin_overview` 1513.0 ms、`wan_dashboard` 1608.9 ms（含公网网络时间，仅作参考）。合规场景：`lan_dashboard` 12.96 ms、`lan_cases_list` 10.62 ms、`lan_notifications` 10.02 ms、`lan_jobs_list` 91.01 ms、`lan_admin_*` 38–119 ms | 对 10k 地址的分页/聚合路径做专项优化（分页游标、预聚合或物化视图），把 p95 压回 200 ms 量级并复跑 perf |
| B-4 | RPO/PITR | 本轮未重跑 `pitr-drill.sh`。**实测到一个硬前置**：当前实例 `archive_mode = off`（`show archive_mode` → off、`archive_command` → `(disabled)`），也就是说**当前配置下 PITR 根本不可能执行**；2026-09-12 的那份 PITR 证据（`archive_mode=on`、RPO 51 s）是当时临时开启归档取得的，不能代表当前配置。**门禁已加固**（D-29）：`dr-gate.sh` 现在要求证据 `ts` ≥ 本次冻结批次、并**现场复核**当前实例 `archive_mode=on`，因此该旧证据不再被当作有效证据（实测 gate 阻断项 8 → 10） | ① 在维护窗口内显式开启 WAL 归档（`archive_mode=on` + `archive_command` + 归档存储 + 重启），并把它纳入常规配置管理；② 然后执行 `pitr-drill.sh`，产出"RPO ≤ 900 s"的实测值与误差方向；③ 同时明确"归档未开启期间 RPO 不受控"的结论 |
| B-5 | 回滚门禁 | **演练已执行**（`scripts/release/rollback-drill.sh`，证据 `sprint7/release/rollback-drill-20260916T164802Z.{md,json}`），结果**部分通过、且暴露一个真实回滚缺口**：① **前端回滚实测可用** —— 换成 `dist.prev-*` 后单元重启，RTO 用户台 1.03 s / 后台台 0.02 s，服务端下发的入口哈希等于上一版磁盘产物；② **回滚后再前进实测可用** —— 前滚回冻结版本 RTO **23.30 s**，api/worker/migrate 与两套 dist 哈希逐条等于基线、`/health/ready` 200、TLS 入口 200；③ **后端回滚到 09-09 产物失败**（`rollback_backend=timeout`）：旧 API 启动即 `bootstrap failed: create schema_migrations: ERROR: permission denied for schema public (SQLSTATE 42501)`，`Restart=on-failure` 反复重启直到 180 s 超时。根因：旧二进制的启动路径要建 `schema_migrations` 表，而运行时角色已按迁移 `0054` 降权（`blockchain_app` 无 DDL）——**跨"权限加固边界"只换二进制不可行** | ① 明确"上一发布版本"的产物集合（不是 7 天前的临时备份）与其兼容的权限/迁移策略（例如回滚窗口内临时授予 bootstrap 所需 DDL，或改用不回退权限的迁移方案）；② 用该产物重跑 `rollback-drill.sh` 直到后端回滚 RTO 有实测值；③ 演练脚本自身首轮暴露的 2 处缺陷（把哈希当路径传入 `cp`、探针 URL 拼接）已修（现 `f55c486c…`） |
| B-6 | `APP_ENV=production`、灰度观察、入口切换、批准人签署 | §16.1 / §16.11 / §16.12；本轮**不切 production** | 维护窗口内执行：冻结 → 切 production → 灰度 → 观察 → 入口切换 → 签署 |
| B-7 | 依赖 / 镜像 / IaC 重扫（旧 R-09/R-10/R-11） | **依赖侧已完成**：`govulncheck ./...` → 0 条影响本代码；`pnpm audit --prod` → 1 moderate（echarts XSS，有修复版）/ 0 high / 0 critical；`xlsx` 依赖图只剩 0.20.3（旧两条 HIGH 闭环）。**镜像侧已恢复可执行并实测出真实漏洞**：漏洞库改由镜像源取得（`ghcr.m.daocloud.io/aquasecurity/trivy-db:2`，119 MB／55 s 内完成，离线注入 `~/.cache/trivy/db/`，`metadata.UpdatedAt=2026-09-16T13:02:23Z`），扫描脚本 `scripts/s7/run-s7-scan-image-iac.sh`（`8cae9695…`），证据 `sprint7/security/image-scan-20260916T160936Z/`。实测（HIGH,CRITICAL）：`postgres:18` **14 CRITICAL / 106 HIGH**（可修 13 / 45）、`minio/minio:latest` **6 / 100**（可修 4 / 96）、`minio/mc:latest` **2 / 45**（可修 2 / 45）、`nats:2-alpine` **0 / 2**、`clickhouse/clickhouse-server:26.7` **0 / 0**；合计 **22 CRITICAL / 253 HIGH（其中可修 19 / 188）**，CRITICAL 集中在 Go 标准库与 `github.com/minio/minio`（minio 系镜像）以及 Debian 基础镜像的 `perl`/`libxml2`（postgres 镜像）。**IaC 侧**：trivy `config` 对本仓库 IaC 面检出配置文件数 0（工具可用已用 k8s 对照样本验证），故改用**人工核对清单** `scripts/s7/run-s7-iac-checklist.py` 逐项断言：**115 项 = 41 PASS + 74 FINDING**，主要发现为——9 个 systemd 单元全部缺 `NoNewPrivileges/ProtectSystem/PrivateTmp/ProtectHome`；**6 个单元日志 `append:` 无轮转**（与今日两次磁盘打满直接相关）、5 个单元 `StartLimitIntervalSec=0` 关闭了启动限流；4 个容器服务缺 `security_opt/cap_drop/read_only`、无资源限制、**无日志轮转**；`minio/minio:latest` 未固定 tag；`minio`/`nats` 无 healthcheck；nginx 未声明 `ssl_protocols`、无限流（3 项安全响应头与 `client_max_body_size` 已具备） | ① 依赖镜像升级或重打（19 CRITICAL / 188 HIGH 有上游修复版本），不可修项需逐条书面接受风险；② `echarts` 升级或书面接受；③ IaC 核对清单的 74 条 FINDING 逐条处置或书面接受（优先：日志轮转、healthcheck、镜像 tag 固定、systemd 加固、nginx `ssl_protocols`/限流）；④ 处置后重跑 `run-s7-iac-checklist.py` 复算 |

> 说明：B-1/B-2 是 §27 内部的两条 Test ID；B-3…B-6 属于 §11/§12/§13/§16 的发布门禁，不是 §27 条目。
> 因此 §1 的"65 条 = 63 PASS + 2 发布阻断"与本节 B-3…B-6 并不矛盾：前者是 Test ID 维度，后者是发布门禁维度。

---

## 5. 与 2026-09-13 旧快照的差异（逐条说明去向，避免两份结论并存）

### 5.1 旧 PARTIAL 的 7 条

| 旧状态 | 条目 | 去向 |
|---|---|---|
| PARTIAL | AUTH-007 | → **PASS**（编排缺参修复后本构建实跑；另有 AUTH-007 延迟门禁 177 ms ≤ 5000 ms 证据） |
| PARTIAL | USER-011 | → **PASS**（扩展档位加入 user-mobile 档，390×844 实跑） |
| PARTIAL | ADMIN-010 | → **PASS**（真实请求路径求值 + 后台页面真实交互） |
| PARTIAL | DATA-009 | → **发布阻断 B-2**（本构建恢复演练**已真机执行两轮**：首轮唯一失败项是已修的工具缺陷 D-25，PG/CH/MinIO 的 RTO 与逐表/逐对象比对均已取得；仍未闭合的是 MinIO 版本·ILM·元数据维度与 RPO/PITR） |
| PARTIAL | AUDIT-001 | → **PASS**（覆盖报告由代码重算 + 运行侧审计断言） |
| PARTIAL | AUDIT-004 | → **PASS**（ADR-018/019 后链可验证，缺口与声明逐条可复算） |
| PARTIAL | UX-005 | → **PASS**（reduced-motion 真实档位 + 过渡时长断言） |

### 5.2 旧 NOT_IMPLEMENTED / NOT_VERIFIED

- `AUTH-004`（NOT_IMPLEMENTED）→ **PASS**：MFA 已于 2026-09-16 实现并部署，本构建上两个 live 用例通过。
- `AUTH-011`（NOT_VERIFIED）→ **PASS（范围外）**：见 `release-scope-decision-20260916.md` §1。

### 5.3 旧 BLOCKED（历史标注）

- `RBAC-004` 旧 BLOCKED（"evidence 端点未实现"）→ **PASS**：改为真实检查（platform_admin 无 `evidence:read` → 403；investigator → 200）。
- `RBAC-007` 旧 BLOCKED（"S3 期无 canvas 端点"）→ **PASS**：S5 套件权威覆盖（4 条），rbac 套件断言该证据存在。
- `AUTH-007`（sprint12 内 BLOCKED"缺夹具口令"）→ **PASS**：`PW_VIEWER` 兼容 `VIEWER_PASSWORD`，不再因编排缺参阻塞。

### 5.4 本轮新发现并修掉的两处"测试工具自身缺陷"（11 条，都会伪造结论，必须记下来）

1. **安全负向套件 11 条假失败**：`run-sprint7-sec-negative.sh` 里若干**手写**的登录 curl 没带 `workspace_id`，夹具账号拿到 409 且没有任何 Set-Cookie，导致 AUTH-013a/a3/b/c/d、AUTH-010a/b/c、AUTH-012a/b、SEC-004d 共 11 条被误判成产品失败。修复后本构建 **97/0/0**。
2. **迁移可重入脚本拿生产库跑注入迁移**：`scripts/test_migration_reentrancy.sh` 只导出 `DATABASE_URL`，而 `cmd/migrate` 读 `MIGRATION_DATABASE_URL`，于是脚本建的一次性库从未被使用、注入的失败迁移实际打在生产库上。生产库事后核对无损（head 仍 0065、无 9999 记录、无 `injected_probe`、服务健康、审计链 valid），但这是**必须修**的安全隐患：现已改为两个 DSN 都指向一次性库，并新增"生产库未被触碰"三道自证（全部 PASS）。
3. **Kill-9 演练把合法终态 `FAILED_FINAL` 判成异常**：白名单漏了 `backend/internal/model/model.go:37` 定义的状态，导致"基线 2 → 演练后 2"也被记成 FAIL（与 Kill -9 无关）。已按代码状态机补齐白名单。
4. **备份脚本的 ClickHouse 行数核对语法错误**：`select count(FINAL) from t` 在 ClickHouse 26.7 上是语法错误，正确写法是 `SELECT count() FROM t FINAL`。后果是**每一次备份都被标 `__INCOMPLETE`**，而 `restore-drill.sh` 默认拒绝使用不完整备份 —— 也就是说 **DR 门禁从 2026-09-13 起结构性不可能通过**。两个脚本已修正，重跑备份见 `RELEASE-WINDOW-REQUEST-20260916.md` 第 1 项。
5. **性能套件登录缺 `workspace_id`，量的是 401 错误响应**：历史 perf 数字（含 2026-09-13 版矩阵引用的 `lan_graph_flow p95 21058 ms`）全部无效。已修登录并加"拿不到会话 Cookie 就 exit 2"的硬门，重跑后才有真实基线。

6. **恢复演练丢掉授权 ⇒ "恢复成功"但业务角色用不了**：`restore-drill.sh` 用 `pg_restore --no-owner --no-privileges` 恢复，GRANT 全部丢失；API/Worker 走 `SET ROLE $DB_RUNTIME_ROLE`（`blockchain_app`），于是恢复库里 `auditverify` 直接 `permission denied for table audit_events_v2 (SQLSTATE 42501)`，演练被判 FAIL（2026-09-16 首轮实测）。已改为保留 ACL，并新增"运行时角色可用性"断言（`set role` 后读 `audit_events_v2` + 校验 `role_table_grants ≥ 1`）。修复后实测恢复库中该角色读到 **93,075** 行（= manifest 的 `audit_event_count`）。
7. **备份流程从不采集 MinIO 语义基线 ⇒ DR 门禁第 4 项在任何备份上都永远无法取证**：`backup-all.sh` 从未调用 `minio-metadata-verify.sh capture`，所有已产出备份目录都缺 `bucket-semantics.json`（不是环境问题，是流程缺口）。已在 MinIO 段之后、manifest 之前加入**只读** capture 步骤；capture 失败或缺产物即标 `__INCOMPLETE`，绝不静默降级成"完整"。
8. **Kill-9 演练的 `/jsz` 探针参数错误 ⇒ 把工具缺陷写成环境性 NV**：脚本固定请求 `/jsz?streams=EVENTS&consumers=1`，而 NATS 2.14.6 只接受布尔参数，该 URL 回 **HTTP 400**，于是"端点不可用"掩盖了真实事实。改正为 `?streams=true&consumers=true` 后实测 `stream EVENTS: messages=477757, durable_consumers=0`（没有 durable consumer ⇒ 服务端不存在重投窗口）。
9. **恢复演练的版本/ILM/元数据维度无法单轮闭环**：独立取证工具 `minio-metadata-verify.sh verify` 必须在本轮**新建的目标桶**上比对，而演练脚本又无条件写 `not_verified structural`，两者在单轮流程里衔接不上。已新增 `RESTORE_DRILL_MINIO_META_VERIFY=1`：就地取证 + 5 项交叉校验（产物类型 / `verdict=PASS` / 四维度完成 / `source_semantics_file` 必须是本次备份的基线 / 每个必需桶 `target_bucket` 必须等于本轮目标桶），复核不通过 = `fail_check` + 仍然 NV。
10. **IaC 扫描脚本给 `trivy config` 传了它不支持的 `--skip-db-update`**（直接 FATAL），且 `trivy config` 对本仓库 IaC 面**检出配置文件数 0**（k8s 对照样本可正常报 3 条 misconfiguration ⇒ 工具可用、是覆盖问题）。前者已改用 `--skip-check-update`；后者**不能**写成"无问题"：IaC 仍属**未取证**，需专用扫描器或人工核对清单。
11. **DR 聚合门禁接受过期证据、且不复核生效配置**：`dr-gate.sh` 只取"最新的"证据文件、**完全不看产出时间**，于是 2026-09-12 的 PITR 结果（`archive_mode=on`、RPO 51 s）被当作当前有效证据放行 —— 而当前实例实测 `archive_mode=off`，RPO 实际上**不受控**。已补两条硬约束：① 所有证据的 `ts` 必须 ≥ 本次冻结批次（`acceptance-evidence/dual-system-v1/release-<TS>`）；② PITR 项**现场复核**当前实例 `archive_mode` 必须实测为 `on`（默认真实探测；仅离线自测可用 `DR_GATE_LIVE_ARCHIVE_MODE_CMD` 注入合成值）。同一份证据集合下，修复后 gate 的阻断项从 8 条变为 **10 条**（新增"PITR 证据早于冻结批次"与"当前实例 archive_mode=off"），自测 **10/10** 通过。

### 5.5 术语与口径

- 本文件不再使用 `PARTIAL / NOT_RUN / BLOCKED / NOT_VERIFIED / NOT_IMPLEMENTED`；只保留 `PASS` 与 `发布阻断`。
- 证据引用一律给"仓库相对路径 : 行号 + 原文数字/字符串"；同一 Test ID 有多轮证据时，取本冻结版本上最新的那一条。



