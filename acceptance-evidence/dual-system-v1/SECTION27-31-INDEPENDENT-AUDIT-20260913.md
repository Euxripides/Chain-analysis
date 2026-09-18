# §27（65 个 Test ID）与 §31（最终交付物清单）独立验收审计

> **审计性质**：只读独立复核（第三方审计代理执行），**不是**验收通过声明。
> **审计时间**：2026-09-13 04:36–05:05（UTC+7；部署机 `date -Is` 实测 2026-09-13T04:36:03+07:00 起）。
> **审计对象**：`docs/dual-system-development-plan.md` §27（第 2092–2208 行）65 个 Test ID 与 §31（第 2478–2500 行）最终交付物清单。
> **被审计主张**：`acceptance-evidence/dual-system-v1/section27-conformance.md`（宣称 PASS 56 / PARTIAL 7 / NOT_RUN 0 / NOT_IMPLEMENTED 1 / NOT_VERIFIED 1）与 `section31-deliverables.md`（宣称 完成 32 / 部分 16 / 未交付 2 / 证据缺失 1）。
> **被测系统**：Windows 工作树 `E:\Code\远程虚拟机\harness\blockchain-platform`（分支 `feat/dual-system-iam-foundation`，实测 `git rev-parse HEAD = 51596f75e0f9472c3e051205b0d0cf2896a3637c`，`git status --short` 71 条，dirty 保留）；部署机 `euripides@192.168.31.152:/home/euripides/projects/blockchain-platform`（rsync 目录，无 .git）。
> **路径约定**：`AE/` = `acceptance-evidence/dual-system-v1/` ；`[VM]` = 仅部署机存在；`[WT]` = 仅 Windows 工作树存在；未标注 = 两树同名路径均存在（内容可能不同）。

---

## 0. 判定口径（本次独立使用，不沿用被审计文档的宽松口径）

| 结论 | 含义 |
|---|---|
| **VERIFIED** | 证据文件**实际打开**、SHA-256 已复算、文件内时间与结论字段可读，且断言范围**覆盖 §27 预期结果的全部要点** |
| **PARTIAL** | 有真实证据，但范围受限（只覆盖部分维度/部分租户/部分断点/仅 API 无 UI/仅旧构建）——**必须逐条写明缺什么** |
| **NOT_VERIFIED** | 没有可用于当前构建的有效证据（含：证据被判无效、只存在于旧构建、关键前提缺失） |
| **NOT_IMPLEMENTED** | 代码级确认能力未实现 |

**不采用**的推断方式：不把矩阵 md 里的 PASS 字样当结论；不把「文件存在」当「证据有效」；不把 HTTP 200 或构建成功当完成；不把「fail=0 且 not_verified=null」自动当 PASS。每条均给出：证据路径 + SHA-256 + 证据时间 + 原文引用。

## 1. 全局发现（先读，否则单行结论会被误用）

### 1.1 G-A 构建可追溯性断裂（影响全部 65 条）
- 被审计矩阵与 `RELEASE-DECISION.md` 冻结的是 `backend/bin/api = d221dfefb8052f8e973b5f068a3b89a998c517f4222e130896887e69554e2e05`、`worker = c3fe5898e116dcf0406a0b18177df4fc6e32f5158c85a90ae95e5fa7be3a6c3b`、`migrate = 225a510fd10d1f2d5db1433e9b393e7c5e37577fcf7c6bbd7825ee900f5a8497`。
- **本次实测（部署机 2026-09-13 04:36+07）**：`backend/bin/api = e0a7c3bd1983cc18da85bd5e1a2e3712b9dde43bb67d48ee5e4722f53368abb7`、`worker = 9c41079d5c59067e0585166d049219de0f14fd93069b3c3a194dff0ecf9fccb6`、`migrate = ab10efe36791e49ea820fb7b293c6aa4104bf93788dacacc87f7f7895ca19b4f`。
- `blockchain-api.service` 实测 `MainPID=3413432`、`start_time=[Sun 2026-09-13 04:14:09 +07]`、`ActiveState=active`；`APP_ENV=development`。
- 全部套件（sprint4/5/6、iam、rbac、sprint12、sec-negative、ui）跑在 **01:43–03:04(+07)** 的旧构建上，且多数自记 `build_sha=working-tree` 或 `build=no-git`。
- 04:14 部署的正是旧 `/api/v1` 未认证读与 handler 默认租户作用域修复（改 `server.go` 路由授权与 handler），**正好覆盖 RBAC/USER/DATA 证据所在代码**。
→ 本报告所有 VERIFIED 均附带这一前提：**在冻结旧构建上成立**；迁移到当前构建必须重跑。

### 1.2 G-B 证据两树分叉（本次逐文件哈希实测）
- 部署机 540 个文件 / 工作树 478；仅部署机 129、仅工作树 67、同路径大小不同 62、**同大小但哈希不同 23**。
- 最危险的一处：`AE/sprint7/security/negative-tests/result.json` —— **部署机 pass 97 / fail 0（2026-09-12T18:58:59Z）**，**工作树 pass 87 / fail 3（2026-09-12T16:57:13Z）**，且工作树 `run.log` 为 GBK 乱码。仅从工作树审计会得到与事实相反的结论。
- 同轮浏览器证据：`AE/tests/sprint7/qa/ui/run-20260913T0300Z-https-entry/playwright.log` 部署机 `e9ff0afe…`（10,604 B）↔ 工作树 `12b766b1…`（10,683 B）。

### 1.3 G-C 聚合摘要与清单过期（会直接误导外部读者）
- `AE/tests/sprint7/qa/summary.json` 仍为 `status: FAIL`（`playwright 33/4/21 rc=1`、`sprint5 105/1`、`finished_at 2026-09-12T18:52:26Z`），与被审计矩阵的 56 PASS 并存。
- `AE/manifest.json` / `AE/environment.json` 仍为 `build_sha 3e87311`、`section27_pass 34 / partial 8 / not_run 19 / not_implemented 4`、`postgres_migrations_highest 0048`。

### 1.4 G-D §27 中与授权/租户相关的旧值已被 P0 修复覆盖
- `AE/sprint7/release/policy-divergence.md` 原文仍列：`viewer 读取案件列表 -> GET /api/v1/cases -> 200；GET /api/v2/cases?limit=20 -> 200；需人工核对` 与 `未认证读取案件列表(对照) -> GET /api/v1/cases -> 200；需人工核对`。
- `AE/sprint7/p0-legacy-v1/P0-DEPLOY-AND-ISOLATION-STATUS.md` 原文：`我此前写过「P0 IS CLOSED」，该表述过宽，已作废`、`R-1..R-7 任一未闭合，P0 即未关闭、不可宣称系统 P0=0`、`当前整体发布判定：NO-GO`。

### 1.5 G-E 同一 Test ID 存在多个互相冲突的数值
- AUTH-003 中位响应比：S7 复跑 `1.10x`（sprint12 console）、旧轮 `1.17x`（`AE/tests/sprint12/result.txt`）、更旧轮 `1.09x`（`AE/tests/AUTH-003/result.json`，build `f781c564`）——引用必须注明轮次。
- `AE/tests/DATA-001/result.json`（S1 期，build `d7b6fee`）语义为「工作台卡片数值与接口一致」，与 §27 的 DATA-001 不同义；`AE/tests/UX-001|UX-002|UX-003/result.json` 同为 `d7b6fee` 构建。

### 1.6 G-F 证据卫生
- `AE/tests/__pycache__/parse_export.cpython-312.pyc` 混入证据目录；`AE/tests/sprint7/release/audit-coverage-summary.txt` 为 **0 字节**（`e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855`）。
- `AE/sprint7/dr/v1-false-pass/` + `ERRATA-minio-metadata-v1-false-pass.md` 明确记录过一次「假 PASS」（原文：`v1 在 verify 模式下从未真正比对恢复目标：compared 恒为 0…却在 verdict 里写 PASS`）。

---

## 2. §27 逐条独立核对（65 条）

> 表格中的 SHA-256 为**该证据文件在部署机的实际哈希**（`[WT]` 行除外，已注明）。证据时间优先取文件内字段，其次取文件 mtime（标注 mtime）。

| Test ID | 旧矩阵声明 | 独立核对结论 | 证据路径 | 证据文件 sha256 | 证据时间 | 缺失 / 局限（原文引用） |
|---|---|---|---|---|---|---|
| AUTH-001 | PASS | **VERIFIED** | `AE/tests/sprint7/qa/iam/console.log [VM+WT]` | `421dc2d222dcd3411d915cd80fb260f63814357a1b9920d28fe3490234581051` | 2026-09-13 01:49:32+07（mtime；文件内无时间戳字段） | 文件内 `PASS AUTH-001 admin 登录成功 200`、`PASS AUTH-001 会话 Cookie 为 HttpOnly 1`。未在本文件断言 host-only/`__Host-`；该属性由 AE/sprint7/security/negative-tests/files/cookie-attributes.txt 佐证（https 入口 `__Host-cs_admin_session`，http 入口无 Secure/无前缀）。 |
| AUTH-002 | PASS | **VERIFIED** | `AE/tests/sprint7/qa/iam/console.log [VM+WT]` | `421dc2d222dcd3411d915cd80fb260f63814357a1b9920d28fe3490234581051` | 2026-09-13 01:49:32+07 | `记录 bad_password 原因码 True`、`失败事件结果标记为 failure True`、`账号以掩码形式入库 True`。§27 要求的「不泄露账号存在」由 AUTH-003 的响应一致性覆盖。 |
| AUTH-003 | PASS | **VERIFIED** | `AE/tests/sprint7/qa/sprint12/console.log [VM]；AE/tests/sprint12/result.txt [VM+WT]` | `19f23b55668357a1b3fa710f125cc1942da4afe23961e1c81808a98a33885c3c` | console.log 2026-09-13 01:49:38+07；result.txt 2026-09-12（旧轮） | `两种失败的错误码与提示完全一致 AUTHN_INVALID_CREDENTIALS`、`中位响应时间比值 ≤ 3x（实测 1.10x）`。**同一 Test ID 存在三个不同实测值**：console 1.10x、旧 result.txt 1.17x、AE/tests/AUTH-003/result.json 1.09x（build f781c564）——引用时必须注明取自哪一轮。 |
| AUTH-004 | NOT_IMPLEMENTED | **NOT_IMPLEMENTED** | `backend/internal/api/authv1.go:43 [VM+WT]；docs/implementation/audit-coverage.md [VM+WT]` | `87c5b37f8792aef15c5a0d2359f07436f8f7609161897c22ab94bcf80aad4ee7` | 源码当前（VM/WT 同 sha） | 代码原文 `pr.Post("/mfa/verify", s.handleNotImplemented("MFA_NOT_IMPLEMENTED"))`；audit-coverage.md 原文 `auth.mfa.failure \| **否**（常量已定义但无调用点，功能未实现）`。无任何 live 证据，符合 NOT_IMPLEMENTED。 |
| AUTH-005 | PASS | **VERIFIED** | `AE/tests/sprint7/qa/sprint12/console.log` | `19f23b55668357a1b3fa710f125cc1942da4afe23961e1c81808a98a33885c3c` | 2026-09-13 01:49:38+07 | 5 条断言：`轮换前旧令牌可用 200`、`refresh 下发了不同的新令牌 yes`、`旧令牌重放被识别（AUTHN_SESSION_REUSE）`、`重放后整个会话族被撤销（新令牌同时失效）401`。 |
| AUTH-006 | PASS | **VERIFIED** | `AE/tests/sprint7/qa/sprint12/console.log` | `19f23b55668357a1b3fa710f125cc1942da4afe23961e1c81808a98a33885c3c` | 2026-09-13 01:49:38+07 | `退出前会话可用 200`、`退出后同一令牌立即 401`。§27 另要求 DB revoked_at，本套件未断言 DB 字段。 |
| AUTH-007 | PARTIAL | **PARTIAL** | `AE/tests/sprint7/qa/sprint12/console.log（本轮 BLOCKED）+ AE/tests/AUTH-007/result.json（旧构建）` | `19f23b55668357a1b3fa710f125cc1942da4afe23961e1c81808a98a33885c3c` | BLOCKED 行 2026-09-13 01:49:38+07；旧 result.json executed_at 2026-09-12T15:17:57+08:00、build f781c564 | 当前构建原文：`BLOCKED AUTH-007 需要夹具账号口令 设置 FIXTURE_VIEWER_PASSWORD 后重跑`。唯一 PASS 证据来自旧构建 `f781c564`（`actual: 两个会话在 0 秒内同时失效`），且 §27 要求的 SSE 与审计两个维度在该证据里均未出现。**当前构建无实测。** |
| AUTH-008 | PASS | **VERIFIED** | `AE/tests/sprint7/qa/sprint12/console.log` | `19f23b55668357a1b3fa710f125cc1942da4afe23961e1c81808a98a33885c3c` | 2026-09-13 01:49:38+07 | `移动端 UA 被解析为 device_type=mobile`、`设备/IP/哈希字段齐全 complete`、`新设备被标记（is_new_device=true）True`。§27 要求的 Client Hints / Trace 未在断言文本中出现。 |
| AUTH-009 | PASS | **VERIFIED** | `AE/tests/sprint7/qa/iam/console.log + AE/sprint7/security/negative-tests/run.log` | `421dc2d222dcd3411d915cd80fb260f63814357a1b9920d28fe3490234581051` | iam 01:49:32+07；negative PASS=97/0/0 结束 2026-09-12T18:58:59Z(=01:58:59+07) | `伪造 X-User-* 头被忽略 401`、`伪造头访问 /api/v1/me 为匿名 anonymous`；负向套件 AUTH-009a..e 共 5 组 PASS。 |
| AUTH-010 | PASS | **VERIFIED** | `AE/sprint7/security/negative-tests/run.log + files/AUTH-010*.json/db.txt` | `833f96d1c8513346ad048f28fe8b47d3e600bbea80ccc1a84ff6cabc33d432ba` | 2026-09-13 01:58:59+07 | AUTH-010a..d 4 组 PASS，断言文本覆盖 `client_ip 取 CF-Connecting-IP（XFF 被丢弃）`、`peer/client_ip/proxy_chain 均落库`。工作树同名 files/AUTH-010-db.txt 仅 167 B（VM 256 B），引用须用 VM 版。 |
| AUTH-011 | NOT_VERIFIED | **NOT_VERIFIED** | `backend/internal/api/authv1.go:49-50 [VM+WT]；AE/tests/sprint7/qa/iam/console.log；AE/environment.json` | `87c5b37f8792aef15c5a0d2359f07436f8f7609161897c22ab94bcf80aad4ee7` | 代码当前；environment.json 2026-09-12T17:13:32+08:00 | `SMOKE-09 未启用能力显式 501（OIDC 未接入） 501`；environment.json `"oidc": {"mode": "disabled_without_idp", "start_http": 501}`。live 层不存在可验证对象 → 维持 NOT_VERIFIED。 |
| AUTH-012 | PASS | **VERIFIED** | `AE/sprint7/security/negative-tests/run.log + AE/tests/sprint7/qa/iam/console.log` | `833f96d1c8513346ad048f28fe8b47d3e600bbea80ccc1a84ff6cabc33d432ba` | 2026-09-13 01:58:59+07 | `AUTH-012a 6 个开放重定向载荷全部回落到站内路径`、`AUTH-012b 无载荷落到站外/伪协议 0`、`AUTH-012c 无 Location: evil 0`；iam `return_to 开放重定向被中和 /home`。 |
| RBAC-001 | PASS | **PARTIAL** | `AE/tests/sprint7/qa/sprint4/run.log:177-178 + AE/tests/sprint7/qa/rbac/console.log` | `76cb0369eb768f3eacc62d0859333e6fabbe50f0a06c472765396637cbef5df3` | sprint4 2026-09-13T01:43:26+07 起；rbac 01:49:25+07 | 403 半句成立但**套件里标签是 AUTHZ 而非 RBAC-001**：`PASS AUTHZ viewer 创建案件 -> 403 403`（run.log:177）。rbac 套件的 RBAC-001 三条是**另一权限口径**：`viewer 调用需 admin:session:revoke 的写操作 401`。§27 的「按钮不可见」UI 半句在 S7 浏览器套件（58 用例）中**无任何 RBAC 断言** → 降为 PARTIAL。 |
| RBAC-002 | PASS | **VERIFIED** | `AE/tests/sprint7/qa/rbac/console.log:15-21 + AE/sprint7/security/negative-tests/run.log（IDOR-a..f）` | `8d5b8ebb15f68f02d9b3b3f7d699e87770bbedd80de273c6aff914ace00345ec` | 2026-09-13 01:49:25+07 | `viewer(acme) 读默认组织案件 -> 404（不泄露存在性）`、`owner 读 acme 组织案件 -> 404（反向同样拒绝）`、`不存在的 UUID 与越权返回同一状态码 404`、`越权尝试已写入审计（authorization.object_denied）`、`deny_reason 标记为 cross_organization`。 |
| RBAC-003 | PASS | **VERIFIED** | `AE/tests/sprint7/qa/rbac/console.log:24-26` | `8d5b8ebb15f68f02d9b3b3f7d699e87770bbedd80de273c6aff914ace00345ec` | 2026-09-13 01:49:25+07 | `授予 acme-other 工作区级 analyst 绑定 True`、`analyst 读 acme-lab 案件 -> 404（未绑定该工作区）`、`analyst 读 acme-other 案件 -> 200（已绑定）`。 |
| RBAC-004 | PASS | **VERIFIED** | `AE/tests/sprint7/qa/sprint5/run.log（RBAC-004 4 条）` | `e0de75049e1c1280e76715b9f9d1e6d98fb43834ea4645efde4db7b5db93a882` | 2026-09-13T03:03:52+07（S5 复跑 PASS=108/0/0） | `auditor 无 evidence:read 读取正文 -> 403`、`拒绝写入 authorization.denied（permission_key=evidence:read） yes`。本 ID 在 rbac 套件中仍是 BLOCKED（`依赖 S4/S5 的 evidence 端点（当前未实现）`），已由 S5 覆盖、旧 BLOCKED 不适用。 |
| RBAC-005 | PASS | **VERIFIED** | `AE/tests/sprint7/qa/rbac/console.log:33-34` | `8d5b8ebb15f68f02d9b3b3f7d699e87770bbedd80de273c6aff914ace00345ec` | 2026-09-13 01:49:25+07 | `撤销最后一个 owner 绑定 -> 422 422`、`owner 绑定仍然有效（未被误删） 200`。§27 另要求审计，本 ID 无独立审计断言。 |
| RBAC-006 | PASS | **VERIFIED** | `AE/tests/sprint7/qa/rbac/console.log:37-40` | `8d5b8ebb15f68f02d9b3b3f7d699e87770bbedd80de273c6aff914ace00345ec` | 2026-09-13 01:49:25+07 | `撤销后旧会话在 5 秒内失去权限（实测 0s） within_sla`、`重新授予后 5 秒内恢复权限（实测 0s）`、`同一会话的权限集合已同步收敛 False`。 |
| RBAC-007 | PASS | **VERIFIED** | `AE/tests/sprint7/qa/sprint5/run.log（RBAC-007 8 条）` | `e0de75049e1c1280e76715b9f9d1e6d98fb43834ea4645efde4db7b5db93a882` | 2026-09-13T03:03:52+07 | `Canvas 引用跨案件 Evidence -> 422`、`返回跨案件 evidence id True`、`拒绝后未写入 Canvas 0`。rbac 套件同名 BLOCKED（`依赖 S5 的 canvas/evidence 端点（当前未实现）`）已不适用。 |
| RBAC-008 | PASS | **VERIFIED** | `AE/sprint7/security/negative-tests/run.log（RBAC-008a..j，20 条）` | `833f96d1c8513346ad048f28fe8b47d3e600bbea80ccc1a84ff6cabc33d432ba` | 2026-09-13T01:58:59+07（PASS=97 FAIL=0 NV=0） | 覆盖 API Key audience/scope 负向、`api_client` 权限集、拒绝审计、DEF-01 回归门（`008h 省略 allowed_ips 应成功 201`）。**该 run.log 的 result.json 内不含 build_sha 字段**，无法从证据自身证明构建；WT 同名 result.json 是另一轮 FAIL 87/3（见 §1.5）。 |
| USER-001 | PASS | **VERIFIED** | `AE/tests/sprint7/qa/sprint4/run.log（USER-001 16 条）+ AE/tests/sprint7/qa/ui/run-20260913T0300Z-https-entry/playwright.log` | `76cb0369eb768f3eacc62d0859333e6fabbe50f0a06c472765396637cbef5df3` | sprint4 2026-09-13T01:43:26+07；浏览器 2026-09-13T02:57:52+07（37 passed/0 failed） | 浏览器用例原文 `✓ 10 [user-desktop] dual-system-sprint4.spec.ts:106 USER-001/004/005/006：案件、对象、调查、Finding/Evidence 页面真实交互 (55.5s)`；API 侧 `DB cases 行与 API 一致`。 |
| USER-002 | PASS | **VERIFIED** | `AE/tests/sprint7/qa/sprint5/run.log（USER-002 32 条）` | `e0de75049e1c1280e76715b9f9d1e6d98fb43834ea4645efde4db7b5db93a882` | 2026-09-13T03:03:52+07 | `preview overall_ratio=1`、`preview provider_work_required=false`、HIT 路径 `provider_usage=0`。 |
| USER-003 | PASS | **VERIFIED** | `AE/tests/sprint7/qa/sprint5/run.log（USER-003 22 条）` | `e0de75049e1c1280e76715b9f9d1e6d98fb43834ea4645efde4db7b5db93a882` | 2026-09-13T03:03:52+07 | `缺口预览 ratio<1 True`、`缺口预览 provider_work_required=true`、`创建缺口任务返回 HTTP 200/201 yes`。「只下载缺口」的字节级核对未在断言文本中。 |
| USER-004 | PASS | **VERIFIED** | `AE/tests/sprint7/qa/sprint4/run.log:46-66（USER-004 20 条）` | `76cb0369eb768f3eacc62d0859333e6fabbe50f0a06c472765396637cbef5df3` | 2026-09-13T01:43:26+07 起 | `pause 命令 -> paused`、`暂停期间后台未偷偷推进（仍为 paused）`、`resume 命令 -> running`、`运行 Checkpoint 仍保留（data_reused=true）`、`暂停时未创建野 run；恢复后 run 数受控（<=1）`。Worker 日志维度的证据未落盘，只有 API/DB 断言。 |
| USER-005 | PASS | **VERIFIED** | `AE/tests/sprint7/qa/sprint4/run.log（USER-005 14 条）` | `76cb0369eb768f3eacc62d0859333e6fabbe50f0a06c472765396637cbef5df3` | 2026-09-13T01:43:26+07 起 | `viewer 确认 Finding -> 403`、`存在带 Evidence 引用的 candidate Finding yes`、`Finding 引用了可复核 Evidence yes`。 |
| USER-006 | PASS | **VERIFIED** | `AE/tests/sprint7/qa/sprint4/run.log（USER-006 8 条）` | `76cb0369eb768f3eacc62d0859333e6fabbe50f0a06c472765396637cbef5df3` | 2026-09-13T01:43:26+07 起 | `新版本 supersedes_id 指向原版本 28b2fa9f-…`、`原 Evidence 状态变为 superseded 但内容 hash 不变 superseded\|b514e30c…`。 |
| USER-007 | PASS | **VERIFIED** | `AE/tests/sprint7/qa/sprint5/run.log（10 条）+ 浏览器 playwright.log:16` | `e0de75049e1c1280e76715b9f9d1e6d98fb43834ea4645efde4db7b5db93a882` | 2026-09-13T03:03:52+07；浏览器 02:57:52+07 | `第二客户端 stale 保存 -> 412`；浏览器 `✓ 16 [user-desktop] sprint5.spec.ts:246 Canvas 第二客户端 stale 保存收到 412 与字段级 diff，且不静默覆盖 (13.0s)`。 |
| USER-008 | PASS | **VERIFIED** | `AE/tests/sprint7/qa/sprint4/run.log:95-118（USER-008 32 条）` | `76cb0369eb768f3eacc62d0859333e6fabbe50f0a06c472765396637cbef5df3` | 2026-09-13T01:43:26+07 起 | `票据返回 SHA-256 与审批绑定 hash 一致 06e7979f…`、`下载文件 SHA-256 与 DB/API 一致 06e7979f…`、`签署产生不可空 signature_hash yes`。ETag 维度在 DATA-005 覆盖，本 ID 未断言 HTTP ETag。 |
| USER-009 | PASS | **VERIFIED** | `AE/tests/sprint7/qa/sprint5/run.log（18 条）+ sprint4/run.log + 浏览器` | `e0de75049e1c1280e76715b9f9d1e6d98fb43834ea4645efde4db7b5db93a882` | 2026-09-13T03:03:52+07 | `搜索命中案件类型 True`、`搜索命中调查类型 True`、`跨工作区案件不会出现在搜索结果 False(=不出现)`；浏览器 `✓ 12 sprint5.spec.ts:113 全局搜索按类型分组，并可从结果进入地址详情 (11.5s)`。 |
| USER-010 | PASS | **VERIFIED** | `AE/tests/sprint7/qa/sprint4/run.log（USER-010 10 条）+ 浏览器 playwright.log:11` | `76cb0369eb768f3eacc62d0859333e6fabbe50f0a06c472765396637cbef5df3` | 2026-09-13T01:43:26+07 起 | 浏览器 `✓ 11 [user-desktop] dual-system-sprint4.spec.ts:274 USER-010：通知页 Dialog 交互与无效跳转安全失败 (2.2s)`。 |
| USER-011 | PARTIAL | **PARTIAL** | `AE/tests/sprint5/playwright-full.log:24（S5 构建）+ AE/tests/run-sprint7-ui.sh:88` | `e1a8b211c4e2d639d3c4d1ee66504dd6b417ff1ae8e4e609e94eb1cb48440644` | S5 轮 2026-09-12 22:54:18（mtime）；S7 轮 2026-09-13T02:57:52+07 该用例为 skip | S5 原文 `✓ 24 [user-mobile] … USER-011 移动端 Canvas 列表视图可关闭 Dialog 且无横向溢出 (2.9s)`。S7 轮 playwright.log 原文 `-  17 [user-desktop] … USER-011 移动端 Canvas 列表视图可关闭 Dialog 且无横向溢出`（无耗时=skip），运行器 `run-sprint7-ui.sh:88 PROJECTS="${PROJECTS:---project=user-desktop --project=admin-desktop}"` **不含 user-mobile**。**390px 证据不属当前构建。** |
| USER-012 | PASS | **VERIFIED** | `AE/tests/sprint7/qa/ui/run-20260913T0300Z-https-entry/playwright.log:18` | `e9ff0afebc908379d2591095d21244b13f881f90f2db6983bbd70fd3e67dab4e` | 2026-09-13T02:57:52+07 | `✓ 18 [user-desktop] dual-system-sprint5.spec.ts:326 USER-012 键盘完成搜索并进入结果，Dialog 焦点可逃逸 (2.3s)`；用例源码断言 Tab 到搜索框、输入回车、Tab 到结果链接、Dialog 焦点包含与 ESC 关闭。「焦点顺序/可见性」全程未逐元素断言。 |
| ADMIN-001 | PASS | **VERIFIED** | `AE/tests/sprint7/qa/sprint6/run.log（ADMIN-001a..h，16 条）` | `ff9498e9939db6f3110c9d08d991011e371fd5269dbe2a622c413ac3a5bb929f` | finished_at 2026-09-13T01:49:22+07 | `创建用户返回 UUID`、`一次性初始密码只返回一次 once`、`工作区成员关系已建立 1`、`绑定 scope 落到工作区 workspace`、`创建用户写入审计 yes`。 |
| ADMIN-002 | PASS | **VERIFIED** | `AE/tests/sprint7/qa/sprint6/run.log（14 条）` | `ff9498e9939db6f3110c9d08d991011e371fd5269dbe2a622c413ac3a5bb929f` | 2026-09-13T01:49:22+07 | `未带 confirm 的停用被拒绝（428）`、`停用同时撤销全部会话 0`、`影响预览包含任务/审批/案件/密钥维度 all`。 |
| ADMIN-003 | PASS | **VERIFIED** | `AE/tests/sprint7/qa/sprint6/run.log（12 条）+ 浏览器 playwright.log:52` | `ff9498e9939db6f3110c9d08d991011e371fd5269dbe2a622c413ac3a5bb929f` | run.log 01:49:22+07；浏览器 02:57:52+07 | `连接测试使用受控探针方法 Provider.Health`、`连接测试写入 Usage（DB 证据）1`、`密钥列表只返回摘要（无明文）summary`；浏览器 `✓ 52 [admin-desktop] ADMIN-003/004 Provider 连接测试与密钥摘要均为真实交互 (2.6s)`。 |
| ADMIN-004 | PASS | **VERIFIED** | `AE/tests/sprint7/qa/sprint6/run.log（10 条）` | `ff9498e9939db6f3110c9d08d991011e371fd5269dbe2a622c413ac3a5bb929f` | 2026-09-13T01:49:22+07 | `存在运行任务时断开必须 step-up（403）`、`带 step-up 断开成功 disconnected`、`响应包含受影响链/Dataset/任务/fallback has`。 |
| ADMIN-005 | PASS | **VERIFIED** | `AE/tests/sprint7/qa/sprint6/run.log（12 条）` | `ff9498e9939db6f3110c9d08d991011e371fd5269dbe2a622c413ac3a5bb929f` | 2026-09-13T01:49:22+07 | `返回 requested/effective/reason 三要素 all`、`预览返回目标并发与预估原因 yes`。 |
| ADMIN-006 | PASS | **VERIFIED** | `AE/tests/sprint7/qa/sprint6/run.log（ADMIN-006a..m）+ 浏览器 playwright.log:53` | `ff9498e9939db6f3110c9d08d991011e371fd5269dbe2a622c413ac3a5bb929f` | 2026-09-13T01:49:22+07 | `请求人自审批被拒绝（403）`、`未审批先执行被拒绝（412）`、`DB 记录 requester<>approver 1`、`恢复后 kill_switch=false`。 |
| ADMIN-007 | PASS | **VERIFIED** | `AE/tests/sprint7/qa/sprint6/run.log（12 条）` | `ff9498e9939db6f3110c9d08d991011e371fd5269dbe2a622c413ac3a5bb929f` | 2026-09-13T01:49:22+07 | `预算评估判定已超动作阈值 action_triggered`、`触发优先级为 P1`、`生成了预算告警（DB）1`、`登记了配置动作（DB）yes`。 |
| ADMIN-008 | PASS | **VERIFIED** | `AE/tests/sprint7/qa/sprint6/run.log（12 条）+ AE/tests/sprint6/files/audit-export.jsonl` | `ff9498e9939db6f3110c9d08d991011e371fd5269dbe2a622c413ac3a5bb929f` | 2026-09-13T01:49:22+07 | `缺少 step-up 的审计导出被拒绝（403）`、`下载文件行数与导出计数一致 500`、`下载文件 SHA-256 与响应一致 1cba6c36…`、`DB manifest 记录同一 hash`。 |
| ADMIN-009 | PASS | **VERIFIED** | `AE/tests/sprint7/qa/sprint6/run.log（10 条）` | `ff9498e9939db6f3110c9d08d991011e371fd5269dbe2a622c413ac3a5bb929f` | 2026-09-13T01:49:22+07 | `缺少 If-Match 返回 428`、`复用旧版本被拒绝（412）`、`版本号递增一次（未静默覆盖）1`。 |
| ADMIN-010 | PARTIAL | **PARTIAL** | `AE/tests/sprint7/qa/sprint6/run.log:150-164 + backend/internal/api/sprint6_governance.go（调用点核对）` | `670843e506cab096d4ab5e811ef1caa42567e4b4c402785c0d8a5cf63868bae3` | run.log 2026-09-13T01:49:22+07；源码当前 | API 侧 7 组断言全 PASS（`灰度设为 50% 成功`、`第二名用户独立评估（互不干扰）`、`回滚百分比归零 0`）。**代码级缺口**：全仓库 `s6EvaluateFlag` 仅 1 个调用点（sprint6_governance.go:598 定义、:707 管理端 evaluate handler），**无任何请求路径中间件调用** → §27「只影响目标 scope」在产品行为上不可验证；计划 §29.3 的 flag key（`user_v2_cases`/`admin_v1_iam`/`audit_v2_read`）在代码中不存在。 |
| DATA-001 | PASS | **VERIFIED** | `AE/tests/sprint7/qa/sprint5/run.log（8 条）` | `e0de75049e1c1280e76715b9f9d1e6d98fb43834ea4645efde4db7b5db93a882` | 2026-09-13T03:03:52+07 | `找到 >2^53 的原始金额样本 yes` + 四格式原值一致 + `CSV 文件 SHA-256 与 DB 一致 da7bc274…`。注意 `AE/tests/DATA-001/result.json`（S1 期）语义是「工作台卡片数值与接口一致」（build d7b6fee），**与 §27 的 DATA-001 不同义**，本审计不采信该文件。 |
| DATA-002 | PASS | **VERIFIED** | `frontend/tests/dual-system-sprint5.spec.ts:130-133（浏览器用例，当前构建 ✓）` | `d1ed91371b4b96bbc11ef867e6a58f1b86fc5aafee9ac3ec21b64b9fee80ada0` | 用例源码当前；执行 2026-09-13T02:57:52+07（`✓ 12 … (11.5s)`） | 源码断言 `expect(overview.summary.current_balance.is_current_balance).toBe(true)`、`expect(…historical_net_flow.is_current_balance).toBe(false)`。§27 要求的「来源字段」在后端实现（provider_snapshot/local_index），证据为源码而非响应体采样。 |
| DATA-003 | PASS | **VERIFIED** | `AE/tests/sprint7/qa/sprint5/run.log:139-160（26 条）+ files/big.{csv,xlsx,jsonl,parquet}` | `e0de75049e1c1280e76715b9f9d1e6d98fb43834ea4645efde4db7b5db93a882` | 2026-09-13T03:03:52+07 | 四格式各自 `文件 SHA-256 与 DB 一致`（CSV da7bc274…、XLSX 6ca10843…、JSONL d97e0a36…、Parquet 7233c4d6…）。行数与字段级比对仅在 CSV 明确（`包含完整原始金额 contains=1`）。 |
| DATA-004 | PASS | **VERIFIED** | `AE/tests/sprint7/qa/sprint5/run.log:163-168 + files/big.zip` | `e0de75049e1c1280e76715b9f9d1e6d98fb43834ea4645efde4db7b5db93a882` | 2026-09-13T03:03:52+07 | `ZIP manifest/条目/路径穿越校验通过 ok`、`ZIP 文件 SHA-256 与 DB 一致 8eaee097…`。big.zip VM 1807 B / WT 1806 B → **两树该产物不同**（引用须指树）。 |
| DATA-005 | PASS | **VERIFIED** | `AE/tests/sprint7/qa/sprint5/run.log:169-180（14 条）` | `e0de75049e1c1280e76715b9f9d1e6d98fb43834ea4645efde4db7b5db93a882` | 2026-09-13T03:03:52+07 | `HEAD 返回 200`、`HEAD Accept-Ranges=bytes`、`HEAD ETag 等于文件 SHA-256 da7bc274…`、`Range 请求返回 206`、`Range 分片与完整文件前 100 字节 SHA-256 一致 efe0c8aa…`。**本审计实际读到的断言未包含 Content-Range 头与「票据重复使用 410」两行**（该两条可能落在未逐行展开的其余断言中，未能确认）。 |
| DATA-006 | PASS | **PARTIAL** | `AE/tests/sprint7/qa/sprint5/run.log:184-197 + AE/sprint7/dr/kill9-20260912T180409Z.md（另有独立复核否定）` | `e0de75049e1c1280e76715b9f9d1e6d98fb43834ea4645efde4db7b5db93a882` | S5 run.log 2026-09-13T03:03:52+07；kill9 报告 2026-09-13 01:05:50（mtime） | S5 断言真实且细（`Worker 进程被 Kill-9 并由 systemd 恢复 yes`、`成功页数与游标不回退（逐 shard page_no/rows 不减少）ok`、`Shard 集合无重复/无丢失 ok`）。但**专门演练报告不可用**：kill9-20260912T180409Z.md §3 写 `NATS 监控端点恢复耗时 = 未恢复 s`、`容器状态=exited`，§4 结论却写 `三个目标均由编排自动拉起，且数据侧不变量满足`（自相矛盾）；`AUDIT-*`/`IN_FLIGHT` 类恒零检查被独立复核判为假成功（DR-INDEPENDENT-REVIEW-20260913.md §5.3(b)）。§27 要求的 Provider usage 维度在本 ID 断言中未出现。 |
| DATA-007 | PASS | **VERIFIED** | `AE/tests/sprint7/qa/sprint5/run.log:199-213 + run-20260913T0300Z-nats-hold-fixed/{run.log,RESULT-NOTE.txt,result.json}` | `e0de75049e1c1280e76715b9f9d1e6d98fb43834ea4645efde4db7b5db93a882` | 2026-09-13T03:03:52+07（RESULT-NOTE: run_finished=2026-09-13T03:04:11+07） | 12 条断言含 `NATS 已真实停机（stop rc=0 / State.Status=exited / 4222+8222 不可达）`、`NATS 不可用时事件仍事务性写入 outbox yes`、`Outbox 幂等键唯一（同 aggregate 仅 1 条）1`。**RESULT-NOTE 自记 `guard_status=INVALID — 首版保护壳在运行中被新版本覆盖（bash 边读边执行），本轮的 guard 结论不采信`**；首轮 FAIL 证据保留（`preserved-original-20260913T0148Z/run.log:200 FAIL DATA-007 … got=no want=yes`）。 |
| DATA-008 | PASS | **VERIFIED** | `AE/tests/sprint7/qa/sprint5/run.log（14 条）+ files/object-backup.bin` | `e0de75049e1c1280e76715b9f9d1e6d98fb43834ea4645efde4db7b5db93a882` | 2026-09-13T03:03:52+07 | `对象已真实从 MinIO 删除（stat exit=3）`、`详情返回 object_missing=true 且 download_available=false`、`对象缺失时拒绝创建下载票据 503`、`失败写入审计线索（action=notify_admin）yes`、`对象恢复后详情恢复可下载 exists=true`。 |
| DATA-009 | PARTIAL | **NOT_VERIFIED** | `AE/sprint7/dr/{restore-drill-20260912T175029Z.md, kill9-20260912T180409Z.md, dr-gate-20260912T212458Z.json, ERRATA-minio-metadata-v1-false-pass.md, pitr-20260912T2117/2126*.{md,json}}` | `5616332c1290a5d544231ff1b5cc3d05c59fbeca6495bdcc557cb3ce8e6e5ca8` | restore 报告 2026-09-13 01:00:04；DR gate **2026-09-13 04:24:59+07（BLOCKED）**；PITR 三轮 04:16–04:26（全 FAIL） | ①机器门禁原文 `"verdict": "BLOCKED", "block_count": 6`，含 `找不到 restore-drill 的机器可读结果…没有执行过的恢复演练，不能判 DR 通过`、`找不到 kill9-drill 的机器可读结果…不能判 DATA-006 通过`、`PITR 演练 verdict=FAIL`、`找不到 MinIO 版本/ILM/元数据比对结果`、`找不到消费者幂等结果`。②独立复核原文：`RPO ≤ 15 分钟在结构上不可能达成`（`SHOW archive_mode = off`、`archived_count=0`、无调度）、`RTO ≤ 4 小时 = PARTIAL`、`CH 独立恢复 + FINAL 语义 = NOT VERIFIED`、`MinIO 全桶内容/元数据/版本 = NOT VERIFIED`、总判定 `S7-DR = NOT VERIFIED（阻断整体验收）`。③restore-drill 报告自身表格中 ClickHouse `token_transfers 56,600,036 → 56,220,071`、`address_activity 74,448,957 → 68,637,325` 不相等却计入通过（旧矩阵只引用相等的两表）。④`v1-false-pass/` 勘误原文：`v1 在 verify 模式下从未真正比对恢复目标：compared 恒为 0…却在 verdict 里写 PASS`。→ 结论必须为 **NOT_VERIFIED**，不是 PARTIAL。 |
| DATA-010 | PASS | **PARTIAL** | `AE/tests/DATA-010/result.json + scripts/test_migration_reentrancy.sh + AE/sprint7/db/{before,after,diff,verification-raw}.txt` | `744a5e61d58116068609d0c4797c1336a0ad7a6b826bb4847c0c25adbe16c95a` | db/*.txt 2026-09-12 23:33:23–23:35:59+07；result.json executed_at 2026-09-12T15:17:57+08:00（build f781c564） | result.json 自述 `首次迁移成功；重复执行幂等；注入真实失败后单文件整体回滚、无半成品记录、旧表仍可读；清理后可重入（10/10）`，但 **证据目录内不存在任何 `*reentran*` 的脚本 stdout/日志文件**（本审计在两树均未找到），10/10 属自述而非可复算产物。S7-DB-01 侧有真实产物：`diff.txt` 记 `Q1_download_pages_job_sum before=120.801 after=0.205`、`Q4_audit_seq_desc before=43.086 after=0.117`。 |
| AUDIT-001 | PARTIAL | **PARTIAL** | `AE/tests/sprint7/qa/sprint4/run.log:155-162 + docs/implementation/audit-coverage.md + AE/sprint7/release/audit-coverage-summary.txt + audit-actions-24h.txt` | `6617a1995079281ad5f3931a47d469229964ed9e16f698d75983f6993bef0d66` | audit-coverage.md 2026-09-13 00:28:38；release/* 02:05:24 | 链校验断言真实（`审计哈希链可验证区间 valid=true`、`审计链完整性状态已明确声明（intact 或已封存缺口）declared`）。缺口：`audit-coverage-summary.txt` **0 字节**（sha256 = e3b0c442…，空文件哈希），且 audit-coverage.md 原文记 `auth.mfa.*`、`auth.device.trusted`、`auth.session.expired` 等 `**否**（常量已定义但无调用点，功能未实现）` → §27「关键事件 100% 记录」无机器可读对照；24h 计数文件只有 20 行动作计数，不是逐项执行矩阵。 |
| AUDIT-002 | PASS | **VERIFIED** | `AE/sprint7/security/secret-scan/REPORT.md + AE/sprint7/qa/sprint6/run.log:165-173 + sprint12/console.log` | `27e50d1935bcb03fe317159bd5576f5edd7d2f6dc56c05a243edd68947c8383d` | REPORT 生成 2026-09-12T23:51:51+07 | 报告结论原文 `结论: (c) 类真实明文秘密 = 0`，三目标 `api.log 8,566,393 B/38,878 行`、`worker.log 29,158,011 B/125,882 行`、审计表 `1340 行`，`分类器阳性对照自测 = PASS（10/10 通过）`、`已知秘密阴性对照泄漏数 = 0`、`api.log 残差条数: 0`/`worker.log 残差条数: 0`。**扫描目标是 `/tmp/api.log`、`/tmp/worker.log` 两个时点快照**，不对滚动日志全历史与生产容器外日志负责。 |
| AUDIT-003 | PASS | **VERIFIED** | `AE/tests/sprint7/qa/sprint6/run.log:174-187 + AE/sprint7/security/negative-tests/run.log:162-183` | `ff9498e9939db6f3110c9d08d991011e371fd5269dbe2a622c413ac3a5bb929f` | sprint6 01:49:22+07；negative 01:58:59+07 | `运行角色权限为 INSERT/SELECT 允许、UPDATE/TRUNCATE 拒绝 true/true/false/false`、`UPDATE/DELETE/TRUNCATE/DROP 被运行角色拒绝`、`blockchain_app 对 audit_events_v2 无 UPDATE 权限 f`、`运行角色非超级用户、无 BYPASSRLS false/false`。AE/tests/AUDIT-003-db/db-check.txt 是**人工整理的 psql 会话抄录**（含 `$ psql -c` 提示符，checked=11/5），非机器生成，不能单独作为当前构建证据。 |
| AUDIT-004 | PARTIAL | **PARTIAL** | `AE/tests/sprint7/qa/sprint6/run.log:213-248 + AE/tests/sprint7/qa/iam/console.log:30 + AE/sprint7/dr/restore-drill-*.md（auditverify 输出）` | `ff9498e9939db6f3110c9d08d991011e371fd5269dbe2a622c413ac3a5bb929f` | sprint6 01:49:22+07；iam 01:49:32+07 | `封存后 integrity_status=sealed_incident（不是 intact）`、`prefix_status=sealed_unrecoverable`、`实际校验起点等于新链可信起点 2344`、`边界之后逐行复算通过 valid=true`；iam 原文 `audit 哈希链校验 checked=48077 valid=True integrity=sealed_incident`。**缺口不可回填**：seq 1–2343 已封存为不可恢复（INC-AUDIT-GAP-1-2343），故 §27「任意篡改可检测」在该区间不成立，只能声明。 |
| SEC-001 | PASS | **VERIFIED** | `AE/sprint7/security/negative-tests/run.log（SEC-001a..g，14 条）+ AE/tests/sprint7/qa/iam/console.log:16-17` | `833f96d1c8513346ad048f28fe8b47d3e600bbea80ccc1a84ff6cabc33d432ba` | 2026-09-13 01:58:59+07 | 缺 CSRF→403、错误 token→403、正确 token→200 正对照、跨站 Origin 无 CSRF→403、未产生副作用。**残余**（套件自记）：`SEC-001g 带正确 CSRF 时 Origin 未被二次校验（偏离 §23.2，残余风险 R-02）`。 |
| SEC-002 | PASS | **VERIFIED** | `AE/tests/sprint7/qa/ui/run-20260913T0300Z-https-entry/playwright.log:9,38 + AE/sprint7/security/negative-tests/run.log（SEC-002a..i）` | `e9ff0afebc908379d2591095d21244b13f881f90f2db6983bbd70fd3e67dab4e` | 浏览器 2026-09-13T02:57:52+07；负向 01:58:59+07 | 浏览器 `✓ 9 [user-desktop] SEC-002 XSS / Markdown 注入 › 注入的 HTML 载荷以纯文本渲染，不执行 (3.4s)`（admin-desktop 同轮 ✓ 38）。**残余 F-04 原文**：`admin@local 的 display_name 被写入了 XSS 载荷`、`display_name 服务端未做标签剥离/转义…依赖渲染层兜底不是纵深防御`。 |
| SEC-003 | PASS | **PARTIAL** | `AE/sprint7/security/negative-tests/run.log（SEC-003a..g）+ AE/sprint7/security/negative-tests/CURRENT-BUILD-FINDINGS.md（F-02/R-03）` | `833f96d1c8513346ad048f28fe8b47d3e600bbea80ccc1a84ff6cabc33d432ba` | run.log 2026-09-13 01:58:59+07；findings 文档（WT） | 套件内 SEC-003 的 **live 断言是授权类**（`SEC-003d 未认证修改 Provider 端点 → 401`、`SEC-003e Viewer 会话修改 Provider 端点 → 401`），SSRF 本体只有单测与「URL 形状输入仅被当作数据」（sprint12 console）两条。findings 原文：`S7-SEC-01 期间实测：PATCH /api/admin/v1/providers/{id} 写 http://169.254.169.254/latest/meta-data/ 返回 200（无 URL 校验）→ R-03`；F-02 又写 `Provider 端点写入已被拒绝（先前报告的 R-03 看来已修）` 但 `未补等价正/负单测`。→ **无「已授权管理员写入 metadata/内网 URL 被拒绝」的当前构建 live 证据**，降为 PARTIAL。 |
| SEC-004 | PASS | **VERIFIED** | `AE/sprint7/security/negative-tests/run.log（SEC-004a..f，12 条）+ AE/tests/sprint7/qa/sprint12/console.log:30-32` | `833f96d1c8513346ad048f28fe8b47d3e600bbea80ccc1a84ff6cabc33d432ba` | 2026-09-13 01:58:59+07 | `认证失败达阈值后出现 429`、`429 携带 Retry-After`、`被攻击账号限流期间正常账号仍可登录（bucket 不越权共享）`、`限流事件写入 login_events`。§27 的「压测」维度为单账号窗口探测，非并发压测。 |
| UX-001 | PASS | **PARTIAL** | `AE/tests/UX-001/result.json（build d7b6fee）+ AE/tests/sprint7/qa/ui/run-20260913T0300Z-https-entry/playwright.log:18` | `6a0194839bb49c32e67c6db8274baab31960b0fbe6047c6ba14bcdc28471f328` | 旧 result.json executed_at 2026-09-12T03:28:40+08:00（build d7b6fee）；S7 轮 02:57:52+07 | S7 浏览器套件 SPECS（run-sprint7-ui.sh:87）**不含 UX-001 用例**，最新构建只有 USER-012 的 `Dialog 焦点可逃逸` 间接覆盖 ESC/焦点包含；「关闭后焦点回到触发元素」「长内容滚动」在 S7 构建无断言。旧 result.json 原文 `Playwright UX-001 在 user-desktop 与 user-mobile 两个 project 均通过`（S1/S2 构建）。→ 降为 PARTIAL。 |
| UX-002 | PASS | **PARTIAL** | `AE/tests/sprint7/qa/ui/run-20260913T0300Z-https-entry/playwright.log:1-5,30-34 + quality.spec.ts:95-144` | `a19ea79b1592ebcbb7609d164cb8ef52d680c4823d0a5daf5f74b6fa0f195082` | 2026-09-13T02:57:52+07 | 用户层四断点（390/768/1024/1440 × 5 页）无横向滚动：真实且两 project 通过。**但后台用例名与实现不符**：标题为 `后台管理系统在四个断点下无横向滚动`，源码循环却是 `for (const width of [1024, 1440])`（仅 2 个宽度）；后台 390px 仅由另一用例覆盖 3 个 S6 页面。§27 另要求「遮挡/布局跳动」与「截图」：**S7 轮无新截图**（`ui/` 下只有 playwright.log 与 trace），旧 `tests/UX-002/result.json` 为 build d7b6fee。 |
| UX-003 | PASS | **VERIFIED** | `AE/tests/sprint7/qa/ui/run-20260913T0300Z-https-entry/playwright.log:6-7,35-36 + quality.spec.ts:148-189` | `a19ea79b1592ebcbb7609d164cb8ef52d680c4823d0a5daf5f74b6fa0f195082` | 2026-09-13T02:57:52+07 | 逐元素计算对比度、`checked > 30` 且 failures 为空；状态徽标断言 `●/▲/■/◆/○` 前缀。**范围有限**：对比度只在 `/app-v2/home\|cases\|profile` 三页采样，后台页面未做对比度采样；徽标只在 `/admin/login-events` 一页。 |
| UX-004 | PASS | **VERIFIED** | `AE/tests/sprint7/qa/ui/run-20260913T0300Z-https-entry/playwright.log:19 + sprint5.spec.ts:371-408` | `d1ed91371b4b96bbc11ef867e6a58f1b86fc5aafee9ac3ec21b64b9fee80ada0` | 2026-09-13T02:57:52+07 | `✓ 19 UX-004 10k 真实长表：虚拟滚动 DOM 有界、滚动可交互 (17.9s)`；源码断言 `expect(total).toBeGreaterThanOrEqual(10_000)`、`expect(domRows).toBeLessThanOrEqual(100)`、滚动耗时 `<1500ms`、加载 `<180000ms`。旧 `tests/UX-004/result.json` 的 PARTIAL（`10k 量级未验证`）已被当前构建取代。 |
| UX-005 | PARTIAL | **PARTIAL** | `AE/tests/UX-005/result.json（build d7b6fee）` | `b19e7bf63bff4b34167d09978596e6c5a0e4908dc73c316a98afff77c5383b81` | executed_at 2026-09-12T03:28:40+08:00（build d7b6fee） | 原文 `tokens.css 中 reduced-motion 媒体查询将动画与过渡压缩到 0.001ms`，`notes: 仅 CSS 层验证，未做浏览器录制`。S7 浏览器套件 SPECS 中**没有** `prefers-reduced-motion` 断言（本审计在 `frontend/tests/` 未检索到）。→ 维持 PARTIAL。 |

---

## 3. 统计（与上表逐条一致）

| 结论 | 数量 | 占比 | 旧矩阵同项数量 |
|---|---:|---:|---|
| **VERIFIED** | 50 | 76.9% | PASS 56（差 -6） |
| **PARTIAL** | 12 | 18.5% | PARTIAL 7（差 +5） |
| **NOT_VERIFIED** | 2 | 3.1% | NOT_VERIFIED 1（差 +1） |
| **NOT_IMPLEMENTED** | 1 | 1.5% | NOT_IMPLEMENTED 1（差 0） |
| **合计** | **65** | 100% | 65 |

**由旧矩阵 PASS 降级为 PARTIAL 的 6 条**：RBAC-001（403 有、UI 半句无）、DATA-006（套件有、专用演练报告自相矛盾）、DATA-010（10/10 仅自述、无脚本输出落盘）、SEC-003（SSRF 本体无 live 负向）、UX-001（当前构建无 UX-001 用例）、UX-002（后台四断点实为 2 个宽度、无截图）；另 **DATA-009 由 PARTIAL 降级为 NOT_VERIFIED**（见 §5 P0-3）。

按 §27 分组：

| 分组 | 条数 | VERIFIED | PARTIAL | NOT_VERIFIED | NOT_IMPLEMENTED |
|---|---:|---:|---:|---:|---:|
| §27 AUTH | 12 | 9 | 1 | 1 | 1 |
| §27 RBAC | 8 | 7 | 1 | 0 | 0 |
| §27 USER | 12 | 11 | 1 | 0 | 0 |
| §27 ADMIN | 10 | 9 | 1 | 0 | 0 |
| §27 DATA | 10 | 7 | 2 | 1 | 0 |
| §27.6 AUDIT/SEC/UX | 13 | 7 | 6 | 0 | 0 |

**引用本报告时必须一并说明**：
1. 所有 VERIFIED 的证据都来自 **已被替换的构建**（`d221dfef`），不是当前部署的 `e0a7c3bd`（见 §5 P0-1）。
2. `AE/tests/sprint7/qa/summary.json` 的机器判据仍是 FAIL（33/4/21），与逐套件复跑结论并存；单看摘要会得到相反结论（见 §5 P0-4）。
3. DATA-009（备份恢复演练）**不是 PARTIAL 而是 NOT_VERIFIED**：机器门禁 BLOCKED、RPO 结构性不可达、既有演练报告被独立复核判定不可用（见 §5 P0-3）。

---

## 4. §31 最终交付物清单逐子项独立核对

> 计划原文见 `docs/dual-system-development-plan.md` 第 2478–2500 行（代码 2480–2486、文档 2488–2493、证据 2495–2500）。
> 状态口径：**已交付** = 产物在仓库中存在且可定位（本次实测存在性，两侧树分别标注）；**部分** = 存在但覆盖不全或证据不完整；
> **未交付** = 仓库内不存在；**证据缺失** = 报告引用但两棵树都不存在的文件。
> 路径除注明外为仓库相对路径；`[VM]` = 仅部署机 `/home/euripides/projects/blockchain-platform/`；`[WT]` = 仅 Windows 工作树。

| # | 交付项 | 旧表声明 | 独立核对结论 | 实际路径 / 证据（本次实测） | 缺失 / 局限（原文或实测） |
|---|---|---|---|---|---|
| 1.1 | 用户层前端应用（可独立部署） | 完成 | **已交付** | `frontend/apps/user-console/` [VM+WT]；`deploy/blockchain-user-console.service` [VM+WT]；`AE/tests/sprint7/release/entry-matrix.txt` 记 `/app-v2/ nginx= public=200` | 入口矩阵**标题写 6 条路由、正文只有 5 行**（`/`、`/app-v2/`、`/admin/`、`/api/auth/v1/methods`、`/health/ready`） |
| 1.2 | 后台前端应用（可独立部署） | 完成 | **部分** | `frontend/apps/admin-console/` [VM+WT]；`deploy/blockchain-admin-console.service` | `/storage`、`/data-quality`、`/risk` 仍为 `<Placeholder>`（`frontend/apps/admin-console/src/App.tsx:213-215`，WT sha256 `624f522e…`），且 Render 文本为 `title + " · NOT_IMPLEMENTED"` |
| 1.3 | 共享包 | 完成 | **已交付（体积证据陈旧）** | `frontend/packages/{design-tokens,ui,api-client}/` [VM+WT] | 首屏 gzip 体积证据来自 S1/S2 构建记录；S7 构建未复测（旧表自记） |
| 1.4 | 双入口 Nginx 路由与部署单元 | 完成 | **已交付** | `deploy/nginx-blockchain-platform.conf` [VM+WT] | 见 1.1 的路由行数问题 |
| 2.1 | IAM 模块 | 完成 | **部分** | `backend/internal/iam/` [VM+WT]（含 `device_test.go`、`oidc_test.go`） | MFA 未实现（`authv1.go:43` → 501）；OIDC 无 live IdP（`/oidc/*` 501） |
| 2.2 | Policy 模块 | 完成 | **部分** | `backend/internal/policy/` [VM+WT]；`AE/sprint7/security/threat-matrix.md:135` [VM+WT] sha256 `6d2c1fca…` | 报告原文：**结论：覆盖率 100%（静态核对）**、`缺口：仓库中没有自动化 gate 强制这条规则`；只有「声明覆盖率」，无「声明→调用→拒绝」对照 |
| 2.3 | Tenancy 模块 | 部分完成 | **部分（无独立模块）** | **`backend/internal/tenancy` 两侧均不存在**（实测 WT `Test-Path=false`，VM `MISS`）；作用域落在 `policy` + SQL 约束（`backend/migrations/postgres/0044_tenant_scope.sql`、`0056_s7_scope_backfill_constraints.sql` [VM+WT]） | 计划列为独立后端模块，实际为横切实现；§33 的「迁移/存储」列只能间接证明 |
| 2.4 | Audit 模块 | 完成 | **部分** | `backend/internal/audit/` [VM+WT]（`archive.go`/写入器/校验器） | 全链完整性存在已封存缺口 seq 1–2343（见 AUDIT-004） |
| 2.5 | Notification 模块 | 部分完成 | **部分（无独立模块）** | `backend/internal/api/userv2.go`（`/notifications`、`/read`、`/read-all`、`/open`）[VM+WT]；迁移 `0043_audit_append_only.sql` 同目录 | 无独立投递/重试组件与单测 |
| 2.6 | Config/Governance 模块 | 完成 | **部分** | `backend/internal/api/sprint6_governance.go` [VM+WT] sha256 `670843e5…`；`sprint6_provider.go` [VM+WT] | 功能开关未接请求路径（`s6EvaluateFlag` 全仓库唯一调用点在 `sprint6_governance.go:707`） |
| 3.1 | `/api/auth/v1` | 完成 | **已交付（能力有边界）** | `backend/internal/api/authv1.go` [VM+WT] sha256 `87c5b37f…`；`entry-matrix.txt` 记 `/api/auth/v1/methods nginx= public=200` | `/mfa/verify` 501、`/oidc/*` 501 |
| 3.2 | `/api/v2` | 完成 | **已交付（构建待复跑）** | S4/S5/S6 套件经该前缀通过（`AE/tests/sprint7/qa/sprint{4,5,6}/run.log`） | 这些日志锚定的是已被替换的二进制（见 §5 P0-1） |
| 3.3 | `/api/admin/v1` | 完成 | **已交付（构建待复跑）** | `backend/internal/api/{adminv1.go,sprint6_governance.go,sprint6_provider.go}` [VM+WT] | 同上 |
| 3.4 | 兼容 `/api/v1` | 完成 | **部分（P0 未关闭）** | `backend/internal/api/server.go` [VM+WT] sha256 `a79a3897…` | `AE/sprint7/release/policy-divergence.md` 原文：`viewer 读取案件列表 \| GET /api/v1/cases -> 200 \| GET /api/v2/cases?limit=20 -> 200 \| 需人工核对`、`未认证读取案件列表(对照) \| GET /api/v1/cases -> 200 \| … \| 需人工核对`；`AE/sprint7/p0-legacy-v1/P0-DEPLOY-AND-ISOLATION-STATUS.md` §5.3 原文 `R-3 仍开放（旧 V1 前端用户可见功能失败 —— 实测，不是推测）` |
| 4.1 | 0040–0048 增量迁移 | 完成 | **已交付** | `backend/migrations/postgres/0040_iam.sql`…`0048_s4_permission_fix.sql`（**实测 10 个文件 0040–0049 连续存在**）[VM+WT] | 旧表把 0047 写作 `0047_s4_collab.sql`，实际文件名为 `0047_case_collaboration.sql` |
| 4.2 | 0049–0057 迁移 | 完成 | **已交付** | `0050_s5_job_identity_scope.sql`…`0057_s7_indexes_partitions.sql`（实测 0050–0057 全覆盖）[VM+WT]；生产 `schema_migrations` 55→57 见 `AE/sprint7/db/{before,after}.txt` [VM] | 计划写「0040–0048」，实际到 0057，差异未在 ADR/计划中登记（旧表自记） |
| 4.3 | 回填与校验脚本 | 部分完成 | **部分（证据分居两树）** | 脚本 `scripts/s7/` [VM+WT]；原始测量 `AE/sprint7/db/{before,after,diff,verification-raw}.txt` **[VM] 独有**；说明报告 `AE/sprint7/db/{scope-backfill-report,index-partition-verification}.md` **[WT] 独有** | `diff.txt` 实测值 `Q1_download_pages_job_sum before=120.801 after=0.205`、`Q4_audit_seq_desc before=43.086 after=0.117`（真实产物，但与解释文档不在同一棵树） |
| 4.4 | 迁移重入 / 回滚验证 | 完成 | **部分** | `scripts/test_migration_reentrancy.sh` [VM+WT]；回滚脚本 `backend/migrations/rollback/rollback-00{56,57}.sql` [VM+WT] | **两树均无该脚本的执行日志**（本审计检索 `*reentran*` 无命中），10/10 仅见于 `AE/tests/DATA-010/result.json` 自述；`rollback-0057.sql` 有 2 个已知缺陷（`docs/manuals/rollback.md` §3.3 [WT]） |
| 5.1 | Nginx 配置 | 完成 | **已交付** | `deploy/nginx-blockchain-platform.conf` [VM+WT] | — |
| 5.2 | systemd 配置 | 完成 | **已交付（含补偿手段）** | `deploy/blockchain-{api,worker,mcp,vite,user-console,admin-console}.service`、`-containers-watchdog.{service,timer}`、`-e2e-tls.service` 实测全部存在 [VM] | 看门狗是 NATS 重启缺口的补偿手段，根因未定位（旧表自记） |
| 5.3 | compose 配置 | 完成 | **部分** | `deploy/compose.yaml` [VM+WT]；`AE/sprint7/security/image-iac/raw/compose.yaml.txt` [VM] | IaC 配置面扫描结构性不覆盖本仓库 compose/systemd/nginx → R-11 NOT_VERIFIED |
| 5.4 | monitoring 配置 | 完成 | **已交付** | `deploy/monitoring/dual-system-alerts.yaml` [VM+WT]；`AE/tests/sprint7/qa/sprint6/run.log` S6-OBS-01 + 浏览器 `✓ 57 … S6-OBS-01 可观测性页面展示 P0 告警、SLO 与吞吐拆分 (2.7s)` | 告警规则本身未经真实触发演练（无 firing 证据） |
| 5.5 | 回滚脚本 | 完成 | **部分** | `backend/migrations/rollback/rollback-00{56,57}.sql` [VM+WT]；`docs/manuals/rollback.md` [WT]；`RELEASE-DECISION.md` §4 [WT] | 回滚点表中的 `api.bak-s7-<ts>` 等**备份文件本次未在任一树中定位到**（清单只列命名模式） |
| 6.1 | ADR | 完成 | **已交付（编号不连续）** | `docs/adr/` 实测 14 个文件：`AD-001-two-frontends-one-backend.md`…`AD-010-frontend-workspace-via-aliases.md`、`ADR-016-s4-migration-runtime-oidc.md`、`ADR-017-s6-control-plane-and-runtime-role.md`、`ADR-018-audit-integrity-incident-boundary.md`、`README.md` [VM+WT] | `AD-011`～`AD-015` 编号缺失（无文件、无说明） |
| 6.2 | OpenAPI | 部分完成 | **部分** | `openapi/{auth-v1,user-v2,admin-v1}.yaml`、`openapi/common/{envelope,errors}.yaml` [VM+WT]；`scripts/check_openapi.py`、`gen_openapi_index.py` [VM+WT] | **实测两侧树均无 `.github/` 或任何 CI 配置**（`Test-Path .github = False`）→ 无生成/diff 门禁、无 SDK 产物 |
| 6.3 | 权限目录 | 完成 | **已交付（仅工作树）** | `docs/manuals/permissions.md` **[WT]** 44,970 B、sha256 `d35b1977…` | **部署机无 `docs/manuals/` 目录**（实测 `ls: cannot access 'docs/manuals': No such file or directory`）→ 值班/运维侧取不到权限目录 |
| 6.4 | 事件目录 | 部分完成 | **部分** | `docs/implementation/audit-coverage.md` [VM+WT] sha256 `6617a199…`（两侧**同哈希**）；`AE/sprint7/release/audit-actions-24h.txt` [VM+WT] | 无独立「事件目录」文档：业务/管理/通知/Provider 事件无统一清单与字段定义 |
| 6.5 | 数据字典 | 未交付 | **未交付** | **`docs/data-dictionary.md` 不存在（两树实测 MISS）**；`docs/` 下无同类产物 | §32.4 要求的「数据所有者/保留期/敏感级别/索引/迁移回滚」无汇总产物 |
| 7.1 | 用户手册 | 完成 | **已交付（仅工作树）** | `docs/manuals/user-guide.md` **[WT]** 22,060 B、`e78de44c…` | 部署机无该文件 |
| 7.2 | 管理员手册 | 完成 | **已交付（仅工作树）** | `docs/manuals/admin-guide.md` **[WT]** 30,265 B、`714421bf…` | 部署机无该文件 |
| 7.3 | 审计员手册 | 完成 | **已交付（仅工作树）** | `docs/manuals/auditor-guide.md` **[WT]** 29,279 B、`994f7fdd…` | 部署机无该文件 |
| 8.1 | 部署 Runbook | 完成 | **部分** | `docs/manuals/operations.md` **[WT]** 40,183 B、`22b5b58f…` §3 | 文档自记 §3.1 脚本化标准部署 **⚠️ 未实测** |
| 8.2 | 升级 Runbook | 部分完成 | **部分** | `docs/manuals/operations.md` §5 | 无实测标记，无「expand-compatible 双版本共存」执行记录 |
| 8.3 | 回滚 Runbook | 部分完成 | **部分** | `docs/manuals/rollback.md` [WT] §1–§10 | §3.3 自记 `rollback-0057.sql` 两个真实缺陷；§2.1/2.3/2.4 多处 ⚠️ 未实测 |
| 8.4 | 密钥轮换 Runbook | 完成 | **已交付（仅工作树）** | `docs/manuals/operations.md` §7 | 无真实轮换执行记录（未做密钥轮换演练） |
| 8.5 | 备份恢复 Runbook | 完成 | **部分** | `docs/manuals/operations.md` §6/§13 [WT]；`scripts/backup/` [VM+WT] | RPO 未受控（§27 DATA-009 结论）；新版本 DR 脚本从未真实执行（独立复核 §5.2：**新版本 13 条修复至今 0 次真实执行**） |
| 8.6 | 事故处置 Runbook | 完成 | **已交付（仅工作树）** | `docs/manuals/troubleshooting.md` [WT] 23,580 B、`a4cce43f…` | 部署机无该文件 |
| 9 | 能力边界表 | 部分完成 | **部分** | `RELEASE-DECISION.md`（**[WT] 独有**，VM 实测不存在）；`AE/final-report.md` [VM+WT]；`docs/implementation/current-surface-inventory.md` [VM+WT] `38bbe087…` | 三处口径不同步；`RELEASE-DECISION.md` 单列：批准人=「执行 Agent」、**无人工签署**、**未纳入版本控制**；且其 §2「已满足的发布前置」仍列「备份恢复 PG 5.81 s / CH 561 s / MinIO 0.63 s」——被 `AE/sprint7/dr/DR-INDEPENDENT-REVIEW-20260913.md` 判定为不可用作通过证据 |
| 10.1 | 全部 Test ID 结果 | 部分完成 | **部分** | `AE/section27-conformance.md`（WT 49,494 B / VM 15,132 B，**两树不同**）；分套件日志 `AE/tests/sprint7/qa/**`、`AE/sprint7/security/negative-tests/run.log` | `AE/tests/sprint7/qa/summary.json` 仍 `"status": "FAIL"`（`playwright 33/4/21`、`sprint5 105/1`、`finished_at 2026-09-12T18:52:26Z`）；`AE/manifest.json` 仍 `build_sha 3e87311`、`section27_pass 34 / not_run 19 / not_implemented 4`；`AE/environment.json` 仍 `postgres_migrations_highest 0048` |
| 10.2 | 截图 | 部分完成 | **部分** | `AE/tests/sprint5/screenshots/`（10 张）、`AE/tests/sprint6/screenshots/`（20 张）、`AE/tests/{UX-001,UX-002-mobile-home,U-AUTH-01,U-HOME-01,A-HOME-01,A-LOGIN-01,A-AUDIT-01}/screenshot.png` | **S7 复跑轮次无新截图**（`AE/tests/sprint7/qa/ui/` 只有日志与 `test-results/`）；USER-011 的 390px 截图属 S5 构建 |
| 10.3 | API/DB/日志/文件/运行证据 | 完成 | **部分** | `AE/tests/sprint7/qa/*/{run.log,console.log}`、`files/`（`big.{csv,xlsx,jsonl,parquet,zip}`、`range.part`、`object-backup.bin`、`audit-export.jsonl` 519,624 B）[VM+WT 但部分内容不同]、`AE/sprint7/db/*.txt` [VM]、`AE/sprint7/dr/*` [VM] | `AE/tests/__pycache__/parse_export.cpython-312.pyc` 混入证据目录；同一路径两树内容不同的文件见 §7 |
| 11.1 | 权限覆盖报告 | 部分完成 | **部分** | `AE/sprint7/security/threat-matrix.md` [VM+WT] `6d2c1fca…` | 只有「声明覆盖率 105/105（静态核对）」；无「实际调用/拒绝行为」对照，且无自动化 gate |
| 11.2 | 审计覆盖报告 | 部分完成 | **部分** | `docs/implementation/audit-coverage.md` [VM+WT 同哈希]；`AE/sprint7/release/audit-coverage-summary.txt` **0 字节** [VM+WT] | 「实执行逐项 100%」无机器可读对照（空文件） |
| 11.3 | 秘密扫描 | 完成 | **已交付** | `AE/sprint7/security/secret-scan/{REPORT.md,scan-secrets.sh,raw/}` **[VM+WT 同哈希** `27e50d19…`**]** | 报告只覆盖两个时点日志快照（`/tmp/api.log`、`/tmp/worker.log`）与审计表 1,340 行 |
| 11.4 | SBOM | 完成 | **已交付（材料来源有保留）** | `AE/sprint7/security/sbom/` [VM+WT]（`backend.{cdx,spdx}.json`、`frontend.{cdx,spdx}.json`、`SUMMARY.md`、`validation.txt`、`commands.txt`） | `SUMMARY.md` 自记 syft 镜像来自镜像站、未能与上游逐字节比对（NOT_VERIFIED）；工作树另有 `discarded-wrong-target/` |
| 11.5 | 漏洞报告 | 部分完成 | **部分** | `AE/sprint7/security/dependency-scan/SUMMARY.md`、`image-iac/SUMMARY.md` [VM+WT]；`go/osv/` 原始 JSON [VM+WT] | R-09 前端 `xlsx@0.18.5` 无修复版本、R-10 基础镜像未升级固定 digest、R-11 IaC 配置面 NOT_VERIFIED —— 三项均未关闭 |
| 12.1 | 性能分层结果 | 部分完成 | **部分** | `AE/sprint7/perf/summary.json` [VM+WT] `a2953b2d…`、`lan_*.json`(14)、`nginx_*.json`(3)、`wan_*.json`(2)；说明文档 `AE/sprint7/perf/README.md` **[WT] 独有** | `summary.json` 实测 `"violations": {"p95_over_200ms": ["lan_graph_flow","lan_graph_overview","lan_longtable_assets","lan_longtable_p1","wan_admin_overview","wan_dashboard"], "with_errors": [全部 19 个场景]}`——**19/19 场景都有 errors**（`nginx_dashboard` 51,930 请求 / 51,930 errors；`wan_dashboard` 206/206）；`build_api_sha=0971a2b2…`（**第三个构建**，与冻结 d221dfef、当前 e0a7c3bd 均不同） |
| 12.2 | 容量预测 | 未交付 | **未交付** | **两树均不存在**（`§30.3` 的 rows/bytes 30/90/365 天预测、分区前事件数估算无任何产物） | `AE/sprint7/db/index-partition-verification.md` [WT] 只给索引 A/B 与分区现状 |
| 12.3 | RPO/RTO 演练 | 部分完成 | **未交付（判定 NOT_VERIFIED）** | `AE/sprint7/dr/restore-drill-20260912T175029Z.md`、`kill9-20260912T180409Z.md`、`pg-restore-*.{rto,counts}.txt`、`backups/pre-s7-pg-20260912T162853Z.dump`(85,948,607 B) 全部 **[VM] 独有**；`AE/sprint7/dr/README.md`、`DR-INDEPENDENT-REVIEW-20260913.md`、`PITR-DRILL-STATUS.md` **[WT] 独有** | `dr-gate-20260912T212458Z.json` 原文 `"verdict": "BLOCKED", "block_count": 6`；`restore-drill` 报告内 CH 两表行数不等（`token_transfers 56,600,036 → 56,220,071`）；`pitr-20260912T211721Z.json` `"verdict": "FAIL", "reached_target": false, "failures_count": 8`；独立复核总判定 `S7-DR = NOT VERIFIED（阻断整体验收）`、`RPO ≤ 15 分钟在结构上不可能达成` |
| 12.4 | 百万行导出与 Provider/全链路性能证据 | 证据缺失 | **证据缺失** | `AE/sprint7/perf/README.md` §5 引用的 `export-pipeline.md`、`provider-pipeline.md` **两棵树都不存在**（本审计在 VM 与 WT 均实测 MISS） | §28.9 `S7-PERF-01` 的实质缺口 |
| 13 | `RELEASE-DECISION.md` | 部分完成 | **部分（阻断依据已过期）** | `AE/RELEASE-DECISION.md` **[WT] 独有**（VM 实测 `No such file or directory`）；137 行 | ①批准人=「执行 Agent」，**无人工签字**；②未纳入版本控制（仅工作树）；③**内容已更新**（§0.1 新增 P0、§0.2 更正 4 FAIL 归零、§0.3 RPO 口径），但 §2「已满足的发布前置」仍把被判不可用的 DR 数字列为已满足 |

**§31 统计（本次独立核对，逐行计数）**

| 结论 | 数量 | 占比 | 旧表同项 | 旧表数量 |
|---|---:|---:|---|---:|
| **已交付** | **20** | 39.2% | 完成 | 32 |
| **部分** | **27** | 52.9% | 部分完成 | 16 |
| **未交付** | **3** | 5.9% | 未交付 | 2 |
| **证据缺失** | **1** | 2.0% | 证据缺失 | 1 |
| **合计** | **51** | 100% | — | 51 |

> 结论差异原因：旧表把「产物存在」直接判为完成；本次独立核对增加了两个维度——**①是否只在其中一棵树存在**（部署机）与 **②产物是否真能被引用为验收证据**。
> 因①降级的子项：6.3 权限目录、7.1/7.2/7.3 三本手册、8.6 事故处置手册、13 `RELEASE-DECISION.md`（**仅工作树**，部署机实测不存在）；
> 因②降级的子项：2.2（只有声明覆盖率、无自动化 gate）、4.3（报告与原始测量分居两树）、4.4（重入 10/10 只有自述、无脚本输出落盘）、10.1/10.2（聚合摘要过期、S7 轮无截图）、11.1/11.2（无对照表、摘要 0 字节）、11.5（R-09/R-10/R-11 未关闭）、12.1（6 项 P95 超预算且 19/19 场景带 errors）、12.3（DR 门禁 BLOCKED）、13（无人工签署、未入版本控制）。

---

## 5. 必须先修的 P0 / P1 差距清单（按阻断强度排序）

### P0-1（最高）§27 全部 live 证据锚定的二进制已被替换，P0 修复正好改动授权路径
- **实测**：本次审计在部署机 `2026-09-13 04:36+07` 读到 `backend/bin/api = e0a7c3bd1983cc18da85bd5e1a2e3712b9dde43bb67d48ee5e4722f53368abb7`、`worker = 9c41079d…`、`migrate = ab10efe3…`，服务 `MainPID=3413432`、`start_time=[Sun 2026-09-13 04:14:09 +07]`；而 §27 矩阵与 `RELEASE-DECISION.md` 冻结的是 `d221dfef…` / `c3fe5898…` / `225a510f…`，S4/S5/S6/iam/rbac/sprint12/sec-negative/ui 各套件全部跑在**冻结前或冻结时**的构建上。
- **为何阻断**：2026-09-13 04:14 部署的正是旧 `/api/v1` 未认证读/跨租户 P0 修复，它改动的就是 `backend/internal/api/server.go` 的路由授权与 handler 作用域——也就是 RBAC/USER/DATA 证据所在的那段代码。
- **闭合条件**：① 冻结新的 `api/worker/migrate` SHA 并写入证据；② 在**同一冻结构建**上一次性重跑 `tests/run-sprint7-qa.sh`（含 iam/rbac/sprint12/sprint4/5/6 + `run-sprint7-ui.sh` https 入口 + `run-sprint7-sec-negative.sh`）；③ 产出**单一** `qa/summary.json`，其 `status` 与各套件 rc 一致（当前是 FAIL 快照）；④ 矩阵每行的「构建」列填该 SHA，不再出现 `build_sha=working-tree` / `build=no-git`。

### P0-2 旧兼容面 `/api/v1` 未认证读 + 默认租户作用域 P0 未关闭（自述亦未关闭）
- **实测**：`AE/sprint7/release/policy-divergence.md` 仍把 `GET /api/v1/cases -> 200`（含**未认证**对照行）标为「需人工核对」；`AE/sprint7/p0-legacy-v1/P0-DEPLOY-AND-ISOLATION-STATUS.md` 原文：`我此前写过「P0 IS CLOSED」，该表述过宽，已作废`、`R-1..R-7 任一未闭合，P0 即未关闭、不可宣称系统 P0=0`、`当前整体发布判定：NO-GO`。
- **闭合条件**：在当前构建上把 `policy-divergence.md` 重做成**机器可读 JSON**（v1 与 v2、未认证/Viewer/跨组织/受限 Actions key/无 evidence 权限 admin 六类主体 × 读/写），并包含**授权正对照**（合法同组织读取 200）与 `authorization.object_denied` 审计绑定 request/trace 的查询结果；`P0-DEPLOY-AND-ISOLATION-STATUS.md` 的 R-1..R-7 逐条给出实测链接后才能改写为 CLOSED。

### P0-3 S7-DR / RPO-RTO 完全不可用（不是「部分完成」）
- **实测**：`dr-gate-20260912T212458Z.json` = `BLOCKED`（6 条）；三轮 PITR 全 FAIL（`reached_target: false`、`failures_count: 8`）；`ERRATA-minio-metadata-v1-false-pass.md` 自认 v1 MinIO 比对是假 PASS；独立复核判定 `S7-DR = NOT VERIFIED`、`RPO 结构上不可能达成`（当时 `archive_mode=off`、`archived_count=0`、无调度）。
- **闭合条件**：① systemd timer 定时备份 + WAL 归档，且给出 `SHOW archive_timeout` 单位换算后的真实值；② 一次真实 PITR 恢复到指定时间点，**冻结归档集合的 SHA-256** 与 `reached_target=true`、业务 marker（T0/T1 seq 或已提交业务行）三者同时成立；③ `kill9-drill.sh` 产出 `result.json` 且 `PARTIAL/NV` 参与退出码；④ MinIO 逐桶（含 `blockchain-raw`）键集合/大小/etag 全量比对；⑤ CH 恢复库 `count(FINAL)` 与备份点等式成立。

### P0-4 证据两树分叉 + 聚合摘要/清单过期（外部读者会读到与事实相反的数字）
- **实测（本次逐文件哈希）**：部署机 540 个文件 / 工作树 478；`onlyVM 129`、`onlyWT 67`、`sizeDiff 62`、`同大小但哈希不同 23`。典型：`AE/sprint7/security/negative-tests/result.json` **VM = PASS 97/0（18:58:59Z）↔ WT = FAIL 87/3（16:57:13Z）**，且 WT 的 `run.log` 为 GBK 乱码；`AE/tests/sprint7/qa/ui/run-20260913T0300Z-https-entry/playwright.log` VM `e9ff0afe…`(10,604 B) ↔ WT `12b766b1…`(10,683 B)；`tests/run-fault-drill-guarded.sh` VM 15,175 B ↔ WT 51,341 B；`AE/section27-conformance.md` VM 15,132 B ↔ WT 49,494 B。
- **闭合条件**：以部署机为准做**单向、只新增不覆盖**的同步（或反向），并在 §0.1 列出「同路径不同哈希」清单；重算 `qa/summary.json`；重新生成 `manifest.json`/`environment.json`（当前仍 `build_sha 3e87311`、`migrations_highest 0048`、`section27_pass 34`）。

### P0-5 §27 中**当前构建无证据**或**未实现**的项（逐条闭合条件）
| 项 | 现状 | 需要什么才算闭合 |
|---|---|---|
| AUTH-004 MFA | `/mfa/verify` → 501；`auth.mfa.*` 常量无调用点 | 实现 TOTP/恢复码 + 单次 challenge + 失败计数 + live 负向用例 |
| AUTH-011 OIDC | `/oidc/*` → 501；`environment.json` `oidc.mode=disabled_without_idp` | 接真实 IdP 后跑 state/nonce/PKCE 负向 + 审计 |
| AUTH-007 强制下线 | 本轮 `BLOCKED 需要夹具账号口令` | 设置 `FIXTURE_VIEWER_PASSWORD` 后重跑 sprint12，并补 SSE 与审计两维度 |
| USER-011 移动端 390px | 用例在 S7 为 skip；`run-sprint7-ui.sh:88` 固定 desktop projects | 把 `user-mobile` 加入 S7 UI 运行器并复跑（或在当前构建重跑 S5 mobile 用例） |
| ADMIN-010 功能开关 | 仅管理端试算；`s6EvaluateFlag` 唯一调用点在 `:707` | 在请求路径接入求值 + 两用户真实行为对照（含 §29.3 的 flag key） |
| SEC-003 SSRF live | 只有单测与「URL 形状输入当数据」；R-03 无关闭证据 | 用**已授权管理员**写 `http://169.254.169.254/…`、`http://127.0.0.1`、内网段与 302 跳转，断言被拒 + 审计 |
| DATA-009 / 12.3 | 见 P0-3 | 见 P0-3 |
| A-RISK-01 / A-DATA-02 / A-DATA-03 | 计划路由 `/admin/security/risk-events`、`/admin/data/quality`、`/admin/data/storage` **在前端不存在**；实际只有 `/risk`、`/data-quality`、`/storage` 三个 `Placeholder`（`App.tsx:213-215`，渲染文本含 `NOT_IMPLEMENTED`），且 S6 浏览器套件的 17 页清单**不含**这三页 | 接真实 risk/quality/storage API 并纳入 S6 页面清单与截图；否则明确移出 v1 范围并写进能力边界表 |

### P1-1 审计「逐项执行 100%」无对照 + 全链篡改检测有已封存缺口
- `AE/sprint7/release/audit-coverage-summary.txt` = **0 字节**（`e3b0c442…`）；`integrity_status=sealed_incident`、seq 1–2343 不可恢复。
- **闭合条件**：生成「用户操作清单 → 实际审计动作 → 24h 计数 → Trace 串联」的机器可读对照表；缺口只能**声明**（禁止回填/改锚点）。

### P1-2 性能：6 项 P95 超预算 + 19/19 场景带错误 + 两项全链路证据缺失
- `perf/summary.json` 的 `violations` 原文列出 `p95_over_200ms` 6 项与 `with_errors` 全部场景；`export-pipeline.md`/`provider-pipeline.md` 两树均不存在。
- **闭合条件**：explorer `activity`/`flow-profile` 加预算或预聚合；解释并消除压测错误率（或改为「限流导致 429」的可判读分类）；补百万行导出与 Provider 全链路实测文件。

### P1-3 依赖/镜像/IaC 阻断项未关闭
- R-09（前端 `xlsx@0.18.5` 无修复版本）、R-10（基础镜像 CRITICAL 20/HIGH 203 未固定 digest）、R-11（IaC 配置面 NOT_VERIFIED）。
- **闭合条件**：升级/替换 + 复扫 + SBOM 重建，或书面风险接受（含责任人）；IaC 补扫或出具人工核对清单并签字。

### P1-4 文档与脚本缺口
- 数据字典、事件目录、容量预测未交付；升级 Runbook 未实测；`rollback-0057.sql` 两个缺陷；能力边界表三处口径不一。
- **闭合条件**：出两份目录 + 容量预测；实测一次升级与一次回滚；修脚本或将其标为不可用。

### P1-5 移动端与截图
- S7 轮次零截图；`user-mobile` project 未纳入 S7 运行器。
- **闭合条件**：UI 运行器加 `--project=user-mobile`，复跑并在 `qa/ui/` 下产出新截图与 trace。

### P1-6 证据卫生
- `AE/tests/__pycache__/parse_export.cpython-312.pyc` 混入证据目录；`entry-matrix.txt` 表头 6 条路由、正文 5 行；`manifest.json`/`environment.json` 过期；`tests/DATA-001/result.json`、`tests/UX-00x/result.json` 等 S1/S2 期文件与 §27 同名 ID **语义不同**，必须逐条标注或移出。

---

## 6. 审计方法与本次**未**做的事（只读声明）

**已执行（全部只读）**：Windows 工作树文件递归枚举 + SHA-256（478 文件）；部署机 `find -printf` + `sha256sum`（540 文件）；两树逐文件哈希/大小比对；`cat/head/tail/grep/stat` 读取证据原文；`sha256sum backend/bin/{api,worker,migrate}`；`systemctl --user show`；`git rev-parse HEAD` 与 `git status --short`（只读）；源码 `grep`（`s6EvaluateFlag` 调用点、`/mfa/verify`、`/oidc/*`、router 与 Placeholder 路由）。

**未执行（硬约束）**：未修改/删除/移动任何证据文件；未写数据库；未重启任何服务或容器；未执行任何 `systemctl start/stop/restart`、`docker stop/start/rm`、`mc`、迁移或备份命令；未 commit/reset/checkout/clean；未运行任何测试套件（不产生新证据，也不覆盖旧证据）。本文件是本次审计**唯一新增产物**。

**本次审计的自身局限（必须与结论一起读）**
1. 未重跑任何套件：所有「VERIFIED」是**对既有证据文件的独立复核**（文件存在性 + SHA-256 + 原文断言 + 时间 + 结论字段），不是对系统的重新实测；因此 §5 P0-1 的重跑要求对本报告同样适用。
2. 部署机日志类证据多无内嵌时间戳（`iam/rbac/sprint12 console.log` 均无），时间取自文件 mtime 与批次 `qa/summary.json`；本报告已逐条注明来源。
3. `DATA-005` 我只逐行确认了 HEAD/206/ETag/分片 hash 等断言，**未在原文中找到 Content-Range 与「票据重复使用 410」两行**，故在该行显式标注未确认。
4. 两树比对以文件哈希为判据；文本文件在同步时发生 CRLF 转换会同时改变大小与哈希（如需区分「仅换行差异」需再做归一化比对，本次未做）。同路径**同大小但哈希不同**的 23 个文件可确定为真实内容差异。
5. 部分结论依赖两棵树的代码/文档（如 `docs/manuals/*`、`sprint7/perf/README.md`、`CURRENT-BUILD-FINDINGS.md` 只在工作树），已在路径列用 `[WT]` 标出。

### 复算命令（只读，可在部署机直接跑）
```bash
R=/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1
# 关键证据哈希（应与本报告 §2 表一致）
sha256sum $R/tests/sprint7/qa/{iam,rbac,sprint12}/console.log \
          $R/tests/sprint7/qa/{sprint4,sprint5,sprint6}/run.log \
          $R/tests/sprint7/qa/ui/run-20260913T0300Z-https-entry/playwright.log \
          $R/sprint7/security/negative-tests/run.log $R/sprint7/dr/dr-gate-20260912T212458Z.json
# 部署身份（每次引用前重跑）
sha256sum /home/euripides/projects/blockchain-platform/backend/bin/{api,worker,migrate}
systemctl --user show blockchain-api.service -p MainPID -p ExecStart -p ActiveState
# 过期汇总与空文件
cat $R/tests/sprint7/qa/summary.json; wc -c $R/tests/sprint7/release/audit-coverage-summary.txt
# 两树分叉（在 Git Bash/WSL 中对同一路径比对）
```

---

## 7. 附录：本次实测的两树分叉（可直接用于同步）

| 类别 | 数量 | 说明 |
|---|---:|---|
| 仅部署机 (`[VM]`) | 129 | 2026-09-13 复跑证据、`sprint7/dr/*`、`sprint7/db/*.txt`、`sprint7/p0-legacy-v1/*`、`sprint7/pitr/*`、`backups/*.dump` 等 |
| 仅工作树 (`[WT]`) | 67 | `RELEASE-DECISION.md`、`section31-deliverables.md`、`sprint7/{perf,dr,db}/*.md`、`sprint7/security/{SUMMARY,MANIFEST,README,S7-SEC-01-*}.md`、`sprint7/security/sbom/*`、`docs/manuals/*`（在 `acceptance-evidence` 之外，另计）、`.preserved-local-20260912T2030Z/` |
| 同路径、大小不同 | 62 | 多数为 CRLF 同步差异（逐字节 +行数），少数为真实内容差异（`run-fault-drill-guarded.sh` 15,175 ↔ 51,341 B） |
| 同路径、同大小、哈希不同 | 23 | 确定为真实内容差异：`sprint7/security/negative-tests/result.json`（PASS 97/0 ↔ FAIL 87/3）、`files/AUTH-010b-login.json`、`files/RBAC-008*.json`、`files/SEC-002-*.txt`、`tests/sprint5/files/big.{csv,jsonl,parquet,xlsx}`、`tests/sprint4/ids.env` 等 |

> **引用规则建议**：任何引用 `acceptance-evidence/dual-system-v1/` 下文件的位置，都应写成
> `路径 + 所在树 + sha256`，例如
> `[VM] /home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/tests/sprint7/qa/ui/run-20260913T0300Z-https-entry/playwright.log sha256=e9ff0afebc908379d2591095d21244b13f881f90f2db6983bbd70fd3e67dab4e`。
> 只写路径不写树与哈希，在本次实测中至少有 85 个文件（62+23）会导致误读。
