# R-3：旧 V1 前端 api-control / system 真实 403 缺陷 —— 根因、修复与真实验证

- 状态：**PASS（局部闭合）** —— 最终 run `acceptance-evidence/dual-system-v1/sprint7/ui-r3-20260912T230122Z`
  30 用例全绿（桌面 + 移动）、审计 22/22 按真实 request_id 匹配。
- 本轮**未改后端**（详见 §5）。所有结论均附真实命令输出 / 真实 HTTP 响应 / 真实截图路径。
- 范围声明：本文件只闭合 **R-3**（旧 V1 后台页面受众缺陷 + 其导航回跳 + 共享趋势图坐标轴裁剪）。
  它**不**代表 §27 UI 全部完成（§27 还含移动端 USER-011、四断点 UX-002、MFA/IdP、SSE 等），也不代表整体验收。

---

## 1. 缺陷事实

新增 admin audience 校验后，旧 V1 前端（`frontend/src`，由 nginx `/` → vite:3000 提供）的
`/api-control` 与 `/system` 在真实浏览器里无法读取数据。

真实基线复现（`ui-r3-20260912T223223Z`，修复前，playwright_rc=1）：

```
passed=8 failed=6 skipped=0
✘ [r3-desktop] api-control：真实接口全部成功、关键数据来自接口、无 401/403
✘ [r3-desktop] system：页面可打开、无失败弹窗、关键数据来自接口、无 401/403
✘ [r3-desktop] api-control：弹窗可开关（不做任何写操作）
✘ [r3-mobile] 同上三条
```

curl 层面的真实请求/响应（`https://127.0.0.1:8448`，正确后台登录 `admin@local` + audience=admin）：

| 请求 | 状态码 | 响应体 |
| --- | --- | --- |
| `GET /api/v1/api-control/overview?range=24h`（不带受众头，即旧客户端行为） | **401** | `{"error":{"code":"unauthenticated","message":"需要登录"}}` |
| `GET /api/v1/api-control/provider-accounts`（同上） | **401** | 同上 |
| `GET /api/v1/providers/ankr/status`（同上） | **401** | 同上 |
| 同一组请求带 `X-Auth-Audience: admin` | **200** | 真实数据（overview 3050 B） |
| user 受众会话调 `/api/v1/api-control/overview` | **403** | `{"code":"permission_denied","details":{"permission":"admin:dashboard:read","reason":"audience_mismatch"}}` |
| 匿名 | **401** | `unauthenticated` |

> 监控者看到的 **403** 来自"浏览器里存在 user 受众会话"的那条路径（`audience_mismatch`）；
> 只有 admin cookie 时是 **401**（服务端根本没去读 admin Cookie）。两者同一根因。

---

## 2. 根因

旧 V1 前端的后台页面把**后台接口挂在 `/api/v1/*` 前缀下**：

| 前端调用 | 服务端路由权限键 |
| --- | --- |
| `GET /api/v1/api-control/overview` | `admin:dashboard:read` |
| `GET /api/v1/api-control/provider-accounts` | `admin:provider:read` |
| `GET /api/v1/api-control/errors` | `admin:request:read` |
| `GET /api/v1/api-control/control/states` | `admin:routing:read` |
| `GET /api/v1/providers/ankr/status` | `admin:provider:read` |
| `GET /api/v1/providers/ankr/fetch-runtime` | `admin:performance:read` |
| `GET /api/v1/tokens/enrichment` | `admin:dataset:read` |

而 `backend/internal/api/authmw.go:audienceForPath()` **只按路径前缀**决定会话受众：

```go
func audienceForPath(path string) string {
    if strings.HasPrefix(path, "/api/admin/") { return iam.AudienceAdmin }
    return iam.AudienceUser
}
```

`/api/v1/...` 不匹配 `/api/admin/` → 一律按 **user 受众**解析 Cookie。
旧 `frontend/src/api/client.ts` 的 `http()` **既不发送 `X-Auth-Audience`，也不发送 `X-CSRF-Token`**，于是：

1. 只有后台会话时：中间件只找 `cs_user_session`，找不到 → `require()` 返回 **401 unauthenticated**（admin Cookie 根本没被读取）；
2. 浏览器里存在 user 会话时：主体受众 = user，`policy.Engine.Authorize` 命中
   `policy/engine.go:205 IsAdminPermission(def.Key) && p.Audience != "admin"` → `audience_mismatch` → **403 permission_denied**；
3. 即便受众修好，**写操作仍会失败**：`require()` 对非 GET 调 `checkCSRF`，而旧客户端不发 `X-CSRF-Token`
   → 实测 `403 csrf_failed`（见 §6 对照 A/B）。

即：**"受众声明"这条客户端契约只在新的双系统前端（`frontend/packages/api-client/client.ts`）里实现过，
旧 V1 SPA 从未实现**。这不是权限漏洞，而是旧前端没有走到正确的受众通道。

---

## 3. 修复设计

原则：**只用"正确的管理员登录 + 正确受众 + 正确导航"修复**；服务端授权判定（Policy）一个字不改，
不放宽任何 audience/permission，不允许 user 会话访问后台，不使用 `X-User-*` 兼容头。

**`X-Auth-Audience` 为什么不是"绕过"**（`authmw.go:109-120` 原文注释 + `iam/session.go:345`）：

- 它是**收窄信号**，只决定服务端读哪一套 Cookie；
- 会话令牌本身仍要 `Sessions().Authenticate(ctx, token, audience)` 校验
  "`sess.Audience == 声明受众`"，不匹配直接拒绝（§7 负向用例 4 实测）；
- 授权结论仍由 Policy 引擎给出：user 会话即使持有 `admin:*` 权限也照样 `audience_mismatch` 403（§7 负向用例 2b 实测）。

导航修复：`/api-control`、`/system` 在**没有 admin 受众会话**时叠加一个阻断式弹窗，明确告知这是后台管理面，
并提供通往后台入口 `/admin/login?return_to=<原页面>` 的路径；**不再让用户面对一个静默报错的后台页面**。

---

## 4. 改动文件清单与 sha256

所有文件均通过"原始字节 → `.tmp` → 原子 `mv -f`"上到 VM，并逐文件核对
Windows `Get-FileHash` == VM `sha256sum` 且字节数一致（见 §5.3 冻结 manifest）。

| # | 文件 | 字节 | sha256 | 说明 |
| --- | --- | --- | --- | --- |
| 1 | `frontend/src/api/client.ts` | 19732 | `0900441d5cdc1f0085bfd0cda4091a12a8ec92aebb522346c04267211cb94809` | 受众+CSRF 感知 HTTP 层；后台方法标 `admin`；新增 `probeAdminSession`/`adminGetJson` |
| 2 | `frontend/src/App.tsx` | 11616 | `b0a9bbd36a4c5d6bc2cb625255c9c7b2ef422c6ff99b583642e83bd90c2e83f7` | `AdminSessionProvider` + `BackendRoute` 闸门；后台导航项加锁标记；侧栏显示真实后台会话 |
| 3 | `frontend/src/auth/AdminSession.tsx` | 2877 | `374bc87637830bbf6daaa8abdae8487c670b87bbd1a3e432763e49044817faeb` | **新增**：只读的后台会话探测上下文（admin 受众 + `GET /api/auth/v1/session`） |
| 4 | `frontend/src/components/BackendLoginDialog.tsx` | 1854 | `8075f8709cc88c271b98d9ba9e57348c0090a34a8f2a99b1debd680941aaa3b9` | **新增**：缺失后台会话时的阻断弹窗 + 后台入口链接（符合"提示只用弹窗"的仓库规范） |
| 5 | `frontend/src/pages/SystemPage.tsx` | 8851 | `1a3f36d4a818c8004e554aaf4f51dc3e6fb8cd330ed4b6772e69269a321bce33` | `/api/v1/coverage/stats` 改走 admin 受众 |
| 6 | `frontend/src/features/api-control/TrendChart.tsx` | 3542 | `2f3d491e7990605ef63820eb70533f6f089960a097914e7dcf1de1bc41aaf0f9` | 共享趋势图：SVG 渲染器 + `containLabel`（坐标轴裁剪修复，见 §8） |
| 7 | `frontend/apps/admin-console/src/App.tsx` | 12300 | `da979bf64b88753648350b580fb389f518aa0f73aab7925eb2b6d376f43cff3a` | 未认证 `*` 跳转保留 `search`；`/login` 改为 `LoginLanding`（已认证时按 `return_to` 回原页） |
| 8 | `frontend/apps/admin-console/src/pages/LoginPage.tsx` | 4422 | `2b017a66617cca13ea6bb65fbfc77d907f4110351e1885d8c5c74104d006037c` | 读取并校验 `?return_to=`；跨 SPA 用 `window.location.assign` |
| 9 | `frontend/apps/admin-console/src/lib/returnTo.ts` | 4261 | `db3311ac3967ba6aece8bfe6d36606e7e2701644703f9b3abee2def0697fc254` | **新增**：`return_to` 本地白名单清洗（开放重定向防护） |
| 10 | `frontend/tests/r3-helpers.ts` | 5401 | `405a1b89950994407d99b0054d820b38d601a41441d51a40fbad7d5155d86c9a` | **新增**：证据采集/登录/几何测量工具 |
| 11 | `frontend/tests/r3-legacy-ui-audience.spec.ts` | 8068 | `5d8f77878ec6ddd785950e7c767e0a686e05157263734345589177c62b50c8ca` | **新增**：正向 4 用例 |
| 12 | `frontend/tests/r3-legacy-ui-negative.spec.ts` | 8956 | `3a8c06374ab8f88bc6cfc72e68264e184a6cb1373b67632604351a3171776663` | **新增**：负向 5 用例 |
| 13 | `frontend/tests/r3-nav-return-to.spec.ts` | 7610 | `a2531f4cd8b6c596d2aa57700227d92137847a69455634a5d99d25d8f4bd2655` | **新增**：导航闭环 + `return_to` 白名单 4 用例 |
| 14 | `frontend/tests/r3-chart-axes.spec.ts` | 8403 | `b5743d108ad33bef982e479671a9fd646445d2a8599723a6ada50629a4035308` | **新增**：坐标轴几何断言 4 用例 |
| 15 | `frontend/playwright.r3.config.ts` | 1324 | `844cc8eee040765f768d93a7e7a4246e35781912583fd147eb0eb6f69622bb49` | **新增**：桌面 1440×900 / 移动 390×844 两个 project |
| 16 | `acceptance-evidence/dual-system-v1/tests/run-r3-legacy-ui.sh` | 7074 | `b7df084de32fec485f30343e2832705efd085fa1a8e2c4b7d762dd23db03e216` | **新增**：runner（唯一时间戳目录、HTTP 入口前置检查、JSON reporter、审计门禁） |
| 17 | `acceptance-evidence/dual-system-v1/tests/r3-summarize.py` | 6953 | `e76e50dc5fb2e7aaf5bf1e11c55294c9af0e1bf51d283ee0fb4cc1c06e378b8f` | **新增**：结构化汇总 + 收紧判据 |
| 18 | `acceptance-evidence/dual-system-v1/tests/r3-collect-audit.py` | 8187 | `30ea5491f0a9b014bd20aa44c4f60ae92733cde7bcb6ceae25113316b6e5345a` | **新增**：审计证据采集（容器内只读 psql + 按真实 request_id 核对） |

未改动：**backend/\*\*（零改动）**、`acceptance-evidence/dual-system-v1/tests/sprint7/dr/`（PITR）、
`.../sprint7/security*`（SEC 证据）、任何用户身份数据；未 TRUNCATE / 未清库 / 未删除任何证据 / 未 commit / 未 reset。

---

## 5. 部署与部署身份核实

### 5.1 本轮**没有改后端**

```
$ sha256sum backend/bin/api backend/bin/worker
e0a7c3bd1983cc18da85bd5e1a2e3712b9dde43bb67d48ee5e4722f53368abb7  backend/bin/api
9c41079d5c59067e0585166d049219de0f14fd93069b3c3a194dff0ecf9fccb6  backend/bin/worker

$ sha256sum /proc/<api-pid>/exe /proc/<worker-pid>/exe
e0a7c3bd1983cc18da85bd5e1a2e3712b9dde43bb67d48ee5e4722f53368abb7  /proc/3413432/exe
9c41079d5c59067e0585166d049219de0f14fd93069b3c3a194dff0ecf9fccb6  /proc/3413434/exe

$ stat -c '%n %s %y' backend/bin/api backend/bin/worker
backend/bin/api    55570964 2026-09-13 04:14:09 +0700
backend/bin/worker 42397378 2026-09-13 04:14:09 +0700   # 早于本次会话（05:27 +07 起）
```

**结论：api/worker 二进制与运行时 `/proc/<PID>/exe` 均为 `e0a7c3bd…` / `9c41079d…`，
mtime 早于本会话，本轮未重新构建、未替换、未重启后端服务。** 修复完全落在前端。

### 5.2 服务身份（`systemctl --user show -p ExecStart` + `/proc/<PID>/exe`）

```
blockchain-api.service            MainPID=3413432  ActiveState=active  SubState=running
  ExecStart={ path=/home/euripides/projects/blockchain-platform/backend/bin/api ; argv[]=…/backend/bin/api }
  /proc/3413432/exe -> /home/euripides/projects/blockchain-platform/backend/bin/api   started Sun Sep 13 04:14:09 2026

blockchain-worker.service         MainPID=3413434  ActiveState=active  SubState=running
  ExecStart={ path=…/backend/bin/worker ; argv[]=…/backend/bin/worker }
  /proc/3413434/exe -> …/backend/bin/worker                                            started Sun Sep 13 04:14:09 2026

blockchain-vite.service           MainPID=3744085   # 旧 V1 SPA（dev server，按需读盘，无需重建）
  ExecStart=…/node …/vite/bin/vite.js --host 0.0.0.0

blockchain-admin-console.service  MainPID=3626770   # 后台管理系统（vite preview，服务静态产物）
  ExecStart=…/node …/vite/bin/vite.js preview --config apps/admin-console/vite.config.ts --host 127.0.0.1 --port 3002 --strictPort
  started Sun Sep 13 05:45:56 2026
```

本轮**有部署**的只有前端两处：

1. **旧 V1 SPA**：源码直改，`vite` dev server 按需读盘（无需构建、无需重启）。
   实测 `curl -sk https://127.0.0.1:8448/src/api/client.ts | grep -c X-Auth-Audience` → `3`，确认线上提供的就是新代码。
2. **后台管理系统**：`vite preview` 读静态产物，必须重建。
   重建前**先备份**旧产物到 `frontend/apps/admin-console/dist-backup-20260912T224545Z`（未删除任何旧文件），
   再 `vite build --config apps/admin-console/vite.config.ts`（`✓ built in 3.40s`，rc=0），然后
   `systemctl --user restart blockchain-admin-console.service`（新 MainPID=3626770）。

### 5.3 冻结上传 manifest

通道：**原始字节 → base64 → SFTP 写 `.tmp` → 远端 shell `mv -f`（同文件系统原子替换）**。
逐文件核对 **Windows `Get-FileHash -Algorithm SHA256` == VM `sha256sum` 且字节数一致**。

> 过程中发现并绕开了两个通道缺陷（真实教训）：
> (a) 直接用 read 工具取内容再上传会**读到陈旧缓存**，导致远端仍是旧版；
> (b) `sftp_rename` 在**目标已存在时静默失败**（`.tmp` 残留、目标未更新）。
> 因此改为"原始字节 + 远端 `mv -f` + 逐文件 hash/size 双向核对"，并且**只有全部一致才允许 build/run**。
> 判定不只依赖工具回显，而是以 hash/size 为准。

18 个 owned 文件 **ALL_CONSISTENT = true**；完整清单落盘：
`acceptance-evidence/dual-system-v1/sprint7/ui-r3-20260912T230122Z/upload-manifest.json`
（每项含 `size|sha256` 与 `consistent`；同样内容亦记录于 §4 表格）。

---

## 6. 修复后真实验证（桌面 ≥1440 + 移动 390×844，同一 run 内全量 30 用例）

runner：`bash acceptance-evidence/dual-system-v1/tests/run-r3-legacy-ui.sh`
入口：`https://127.0.0.1:8448`（仅回环 TLS 验收入口；HTTP 源下 Playwright 会丢弃 Secure Cookie，runner 会拒绝执行）
判据：`cases_accounted==cases_expected AND skipped==0 AND playwright_rc==0 AND failed==0 AND audit_evidence.status=='OK'`

### 6.1 全部 run 的真实结果（历史失败证据全部保留，不覆盖不删除）

| run（UTC 时间戳） | 用例数 | passed | failed | skipped | rc | status | 说明 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `ui-r3-20260912T223223Z` | 14 | 8 | **6** | 0 | 1 | FAIL | **修复前基线复现** |
| `ui-r3-20260912T224600Z` | 24 | 22 | **2** | 0 | 1 | FAIL | 受众修复后；2 条失败=审计原因断言写错 |
| `ui-r3-20260912T224838Z` | 26 | 24 | **2** | 0 | 1 | FAIL | 拆出 2a/2b；失败=测试漏写 `status()` 括号 |
| `ui-r3-20260912T225057Z` | 26 | **26** | 0 | 0 | 0 | **PASS** | 受众 + 导航闭环全绿 |
| `ui-r3-20260912T225603Z` | 30 | 26 | **4** | 0 | 1 | FAIL | 新增图表几何用例；失败=X 轴角色判定写错 |
| `ui-r3-20260912T225855Z` | 30 | **30** | 0 | 0 | 0 | **PASS** | 含图表几何 |
| `ui-r3-20260912T230122Z` | 30 | **30** | 0 | 0 | 0 | **PASS** | **最终 run（左/右轴分别复算旧预算）** |

最终 run 按 spec 拆分：

| spec | 用例数×2 project | 结果 |
| --- | --- | --- |
| `r3-legacy-ui-audience.spec.ts` | 4×2 | 全 passed |
| `r3-legacy-ui-negative.spec.ts` | 5×2 | 全 passed |
| `r3-nav-return-to.spec.ts` | 4×2 | 全 passed |
| `r3-chart-axes.spec.ts` | 2×2 | 全 passed |

**skipped=0**：没有任何用例被跳过或放宽断言来"变绿"。

### 6.2 "关键数据来自真实接口"如何证明

`api-control` 用例从服务端取 `/api/v1/api-control/overview?range=24h` 的真实
`metrics.logical_calls`（断言是 `number` 类型，不是硬编码），再断言页面 `.ac-hero-metrics`
渲染出同一数值（`compact.format(...)`）。`system` 用例同理比对 "N Calls"。
原始请求/响应（URL / 状态码 / `X-Request-Id` / `X-Trace-Id` / 受众头 / 响应体）落盘于
`net-r3-{desktop,mobile}-{api-control,system}.json`。

### 6.3 审计证据（必须采集成功才允许 PASS）

采集路径：**容器内已授权的只读 psql**（`docker exec deploy-postgres-1 psql -U blockchain -d blockchain_platform`），
未授予任何新权限、未改 `pg_hba`、未扩大应用运行账号权限。按**本轮 negative 用例产生的真实 request_id** 逐条取行：

```
AUDIT: OK rows=22 matched=22 missing=0
audit_evidence: {"status":"OK","rows_returned":22,"request_ids_matched":22,"missing_request_ids":[],"incomplete_fields":[]}
```

字段完整性核对（`audit-denials.json` → `matched[].missing_fields`）：
`action / category / result / http_status / route_template / permission_key / policy_decision / deny_reason / occurred_at / event_hash` 全部非空。
样例（真实一行）：

```json
{
  "request_id": "186cbb89-df08-4683-908f-498f65ae7dfd",
  "occurred_at": "2026-09-12T22:47:14.988541+00:00",
  "action": "authorization.denied", "category": "authorization", "result": "deny",
  "http_status": 401, "http_method": "GET",
  "route_template": "/api/v1/api-control/control/states",
  "permission_key": "admin:routing:read", "policy_decision": "deny", "deny_reason": "unauthenticated",
  "event_hash": "861d3b3af69d86a45310dcdbd2799f0ed85afbbc3ac5b575604f38e91345efd2"
}
```

> 首轮曾把"`psql` 采集失败（`ERROR: permission denied for table audit_events_v2`、0 字节输出）"
> 误写成 `audit_denials_count=0`——即把"没采到"冒充成"已核对为 0 条"。**该错误已修正**：
> 采集状态现在是 PASS 的**必要条件**，采集失败一律 FAIL 并在 `unclosed` 写明原因（`r3-collect-audit.py`）。

### 6.4 弹窗可开关 + 无横向溢出

- 弹窗：`/api-control?tab=control` 打开"启用全局停机"确认弹窗 → 断言可见 → 点"取消" → 断言关闭；
  **不提交任何写操作**，并额外用只读接口复核 `kill_switch === false`（`dialog-*.json`）。
- 横向溢出：`document.scrollWidth <= clientWidth` 在两端两页均为真。
  （注意：**该断言不足以证明坐标轴可读**，见 §8。）

---

## 7. 负向对照（受众收紧后仍然"该拒就拒"）

| # | 场景 | 真实结果 |
| --- | --- | --- |
| 1 | 用户层会话访问 `/api-control`，页面自身发出的后台接口 | 全部被拒（`unauthenticated`），并显示"需要管理员后台会话"阻断弹窗 |
| 2a | 普通用户层会话 + `X-Auth-Audience: user` 调 `admin:*` 路由 | **403** `permission_denied`（该身份同时缺权限 → `missing_permission`） |
| 2b | **已持有 `admin:*` 权限**的 owner 身份，但会话受众为 `user` | **403** `permission_denied`，`reason = **audience_mismatch**` ← 证明受众本身是独立闸门 |
| 3 | 匿名访问 `/api-control` | **401**（全部后台接口） |
| 4 | 把 user 会话令牌塞进 admin Cookie 名 + 声明 `X-Auth-Audience: admin` | **401** `unauthenticated` ← 证明受众头**不能**提权 |

每一条都记录了真实状态码、响应体与 `X-Request-Id`（`negative-*.json`），并已通过 §6.3 映射到审计行。

**写通道对照（curl 实测，均不改变任何状态）**：

| 请求 | 结果 |
| --- | --- |
| `POST /api/v1/api-control/budgets` + admin 受众 + `X-CSRF-Token`（payload 非法） | **400** 校验错误 → 鉴权与 CSRF **均已通过** |
| 同上但**不带** CSRF 头 | **403** `csrf_failed` → CSRF 确实强制 |
| 同上但声明 user 受众 | **401** `unauthenticated` |

即：修复是"把令牌按契约带上"，不是"关掉 CSRF"。

---

## 8. 共享趋势图坐标轴裁剪修复

### 8.1 缺陷（真实截图证据）

趋势图右轴（`yAxis[1]`，formatter `{value}ms`）刻度被容器右缘裁掉，只剩 `15,` `12,` `9,` 一类残段，
纵轴单位丢失。该缺陷由监控者从真实截图发现并已独立留存
（`.work/dsh-supervisor/verified-ui-r3-224600/r3-desktop-api-control.png` 126,415 B、
`.../r3-mobile-system.png` 65,600 B）。

### 8.2 根因与修复

旧实现 `grid.right = 24`（以及 `grid.left = 48`）是**固定像素预算**，
而刻度文本宽度取决于真实数值（`15000ms` 这类），宽度超过预算就被容器裁掉。`TrendChart.tsx` 改为：

- **渲染器由 Canvas 改为 SVG**。Canvas 下刻度是像素，既无法被无障碍技术读取，
  也无法用 DOM 几何判定是否被裁——只能靠肉眼。SVG 下每个刻度都是真实 `<text>`，可被验收度量。
- **`grid: { containLabel: true, left: 10, right: 16, top: 40, bottom: 26 }`**：
  由 ECharts 按实际刻度文本宽度预留空间（**不是**截断字符串凑宽度）；`left/right` 退化为标签之外的固定留白。
- 左轴刻度改用 `Intl.NumberFormat('zh-CN', { notation: 'compact' })` 保持短而完整；右轴保留 `{value}ms` 单位后缀；
  X 轴加 `hideOverlap: true`。

### 8.3 升级后的检测口径（几何断言，不再是"无横向滚动"）

对每个刻度取 `getBoundingClientRect()`，与图表容器 `getBoundingClientRect()` 比较，
要求 `left ≥ 容器 left - 1px` 且 `right ≤ 容器 right + 1px`；逐刻度落盘 text + rect。
最终 run（4 个 页×断点 组合）全部 **`out_of_bounds = 0`**，`verdict = "axes fully inside container"`：

```
route /api-control  renderer svg  y ticks ['0','300','600','900','1200','1500','1800','0ms','3000ms','6000ms','9000ms','12000ms']
route /api-control  x ticks ['09/12 12时','09/12 14时','09/12 16时','09/12 18时','09/12 20时','09/12 22时']
```

### 8.4 "修复前必然裁剪"的可复算推导

用同一份渲染文本的**实测宽度**复算旧固定预算（含 8px 刻度间隙）：

| 侧 | 旧预算 | 最宽刻度文本 | 实测宽度 | 需要 | 旧预算是否必然裁剪 |
| --- | --- | --- | --- | --- | --- |
| 左轴 | 48 px | `1200` | 35.02 px | 43.02 px | 否 |
| **右轴** | **24 px** | **`12000ms`** | **49.38 px** | **57.38 px** | **是** |

右轴 57.38 px 需求 vs 24 px 预算 → 溢出 33 px，被容器右缘裁掉后恰好只剩前 3 个字符
（`15000ms` → `15,`、`12000ms` → `12,`、`9000ms` → `9,`），**与截图完全一致**。
该推导在 4 个组合上逐条落盘（`chart-axes-*.json → legacy_budget_check`）。

截图：`chart-axes-r3-desktop-{api-control,system}.png`、`chart-axes-r3-mobile-{api-control,system}.png`。
该修复已由根监控独立查看真实新截图确认消除（`.work/dsh-supervisor/verified-ui-r3-225855/`）。

> 说明：检索式 UI 指引库中**没有**针对"坐标轴标签被裁剪"的已验证方案，本项修复以
> **真实截图的裁剪证据 + 实测文本宽度复算**为依据，未引用任何推荐库结论。

---

## 9. return_to 白名单（开放重定向防护）

旧 V1 后台页 → 后台登录 → **回到原页面（含 query）** 的真实链路，跨 SPA 必须用
`window.location.assign(<站点绝对路径>)`，否则会被 admin 的 basename 拼成 `/admin/api-control` 这类死路由。
共修了三处"吞掉返回目标"的代码：`LoginPage` 不读 `return_to`、未认证 `*` 跳转丢失 `search`、
已认证访问 `/login` 无条件去 `/overview`。

白名单语义与服务端 `authv1.go:sanitizeReturnToDefault` 同一安全取向（只允许站内相对路径，
拒绝 `//`、反斜线、`://`），但**更严格**——只放行 `/api-control`、`/system` 及其合法子路由 + query。

正向（真实浏览器最终 location，桌面与移动各一条）：

| 场景 | 最终 `pathname + search` |
| --- | --- |
| 匿名 `/api-control?tab=control` → 点"去后台登录" → 输入账号口令 → 提交 | `/api-control?tab=control` |
| 已认证状态下访问 `/admin/login?return_to=%2Fsystem%3Fview%3Dsettings` | `/system?view=settings` |
| 未认证 `/admin/sessions?return_to=%2Fapi-control%3Ftab%3Derrors` → 登录 | `/api-control?tab=errors` |

负向（每条均为真实浏览器跳转后的真实最终位置，全部落到白名单内）：

| 恶意 `return_to` | 结果 |
| --- | --- |
| `//evil.example.com/api-control` | 未跳外域，落到默认 `/admin/overview` |
| `https://evil.example.com/api-control` | 同上 |
| `javascript:alert(document.domain)` | 同上 |
| `/\evil.example.com` | 同上 |
| `/api-control/../../etc/passwd` | 同上（URL 规范化后逃出白名单被拒） |
| `/admin/overview`（同源但非白名单） | 同上 |
| `/api-control%2f%2fevil.example.com` | 同上 |
| `/%5c%5cevil.example.com` | 同上 |
| `/api-control@evil.example.com` | 同上 |

**正向对照**：`/admin/login?return_to=%2Fapi-control%3Ftab%3Dbudgets` 确实回到
`/api-control?tab=budgets` —— 证明上面的拒绝不是因为功能整体失效。
逐条落盘：`nav-r3-{desktop,mobile}-return-to-allowlist.json`。

---

## 10. 回归对照：本轮改动对 V1 用户层页面的影响

`http()` 现在会显式声明 `X-Auth-Audience: user`（用户层业务调用）与 `admin`（后台调用）。
对 V1 用户层页面（`/`、`/jobs`、`/data-explorer`、`/investigations`、`/fund-flow`）
以 **user 受众会话**实测（`regression-user-layer-v1-pages.json`）：

| 路由 | /api 调用 | 200 | 其他 | 业务接口是否受影响 |
| --- | --- | --- | --- | --- |
| `/` | 7 | 5 | 401×2 | 否（1 条为后台会话探测、1 条为 admin 权限端点） |
| `/jobs` | 4 | 0 | 304×1, 401×1, **500×2** | 见下 |
| `/data-explorer` | 6 | 4 | 304×1, 401×1 | 否 |
| `/investigations` | 9 | 7 | 304×1, 401×1 | 否 |
| `/fund-flow` | 4 | 3 | 401×1 | 否 |

唯一 401（除有意为之的后台探测外）都是 `/api/auth/v1/session`（admin 受众探测，浏览器无后台会话 → 401，符合设计）。
业务接口全部 200。

**受众模式三态对照**（同一 user 受众会话，`regression-audience-mode-comparison.json`）：

| 端点 | 无受众头（=修复前行为） | `user` 头 | `admin` 头 |
| --- | --- | --- | --- |
| `/api/v1/providers/ankr/production-profile` | 403 | 403 | 401 |
| `/api/v1/tokens/enrichment` | 403 | 403 | 401 |
| `/api/v1/download-jobs?limit=5` | **500** | **500** | 401 |

结论：**用户层业务接口行为不变**；后台权限端点在"浏览器没有后台会话"时由 403 变 401
（同样是拒绝，语义更准确）。**不存在"用户层页面从可用变不可用"的回归。**

---

## 11. 未闭合项 / NOT_VERIFIED

1. **既有后端契约缺陷（只记录，本轮未改）**：拒绝响应的**响应体** `meta.request_id` / `meta.trace_id` 是空字符串，
   而**响应头** `X-Request-Id` / `X-Trace-Id` 有真实值，审计行写入的是响应头同源的值。
   证据：`net-*.json`（响应头有值）与 `negative-*-user-session.json`（body 内 `meta` 为空）。
   按约束"不得在 R-3 在途部署期间并发改后端"，**转交主会话处理**；不影响本次授权判定结论。
2. **trace 维度部分 NOT_VERIFIED**：`audit-trace-correlation.json` 显示 22 条 request_id **全部**匹配到审计行；
   其中 16 条"响应头 trace_id == 审计 trace_id"（True），6 条由 `page.evaluate` 发起、证据里只记了 request_id
   而**未记 trace_id**，故为 `None`（**未验证**，不是不匹配）。不得据此宣称 trace 维度全覆盖。
3. **既有后端 SQL 缺陷 —— 完整验收阻断项（已判定为后端缺陷，本轮未修）**：
   `GET /api/v1/download-jobs`（`limit=100` 与 `limit=5` 均可复现）返回
   **500 `ERROR: operator does not exist: text = uuid (SQLSTATE 42883)`**。

   **判据（只读、不经前端）**：

   | 直接 curl 调用（无前端参与） | 受众头 | 结果 |
   | --- | --- | --- |
   | fixture-investigator | （无） | **500** |
   | fixture-investigator | `user` | **500** |
   | **admin@local（owner，177 权限）** | `admin` | **500** |
   | fixture-investigator（`limit=5`） | （无） | **500** |
   | admin@local | （无） | 401（`/api/v1` 按 user 受众解析且无 user Cookie，预期行为） |

   **平台管理员同样 500 ⇒ 既不是权限问题、也不是受众选择问题，与 `X-Auth-Audience`
   是否存在无关 ⇒ 不源于 R-3 客户端改动。** 该 500 会让 `/jobs` 页面在任意主体下报错。

   响应证据：`HTTP/1.1 500`，`X-Request-Id: 12cd8359-4eac-4de7-8d1b-2f2f77339e5f`、
   `X-Trace-Id: 2c47b772-a754-4c8c-bd36-5aedaf21c19a`，
   响应体 `{"error":"ERROR: operator does not exist: text = uuid (SQLSTATE 42883)"}`
   （注意该 500 的响应体**没有 `meta` 信封**，字段根本不存在）。

   **服务端日志（含一处自我更正）**：我先查 `journalctl --user -u blockchain-api.service` 得"0 行匹配"，
   并据此写了"后端未记录该错误"——**这个结论是错的**：api 单元用
   `StandardOutput=append:/tmp/api.log`，HTTP 日志**不在 journald**。改查 `/tmp/api.log` 后取到
   **16 条** `/api/v1/download-jobs` 500 行，其中包含 admin@local（actor `6e192e52…`）与 investigator
   （actor `2f545a30…`）两类主体，`error_code=INTERNAL_ERROR`。
   排查提示：**看 `/tmp/api.log`，不要看 journalctl**。

   **代码位置（只读，本轮未改）**：`backend/internal/api/handlers.go:100-111 handleListJobs` →
   `backend/internal/store/jobs.go:205-210 listJobs`。实测列类型 `organization_id/workspace_id = uuid`，
   而 `j.organization_id=$1` 的参数是 Go `string`（pgx 按 text 发送）→ `uuid = text` 报错；
   同一处在 SQL 层可**不经 HTTP 直接复现**（`backend-repro.txt` 的 PR1）。
   另有**第二处独立缺陷**：该行 `($2='' OR j.workspace_id::text=$1)` 里的 `$1` 是 orgID，
   与 `workspace_id` 比较应为 `$2`（PR3 复现）——只修类型转换会留下静默的工作区过滤错误。

   附带发现：`handlers.go:109` 直接把 `err.Error()` 返回给客户端，原始 SQL 报错文本因此外泄到响应体。

   证据包（两个独立新目录，互为补充）：
   - `acceptance-evidence/dual-system-v1/sprint7/r3-jobs500-20260912T231500Z/`
     （首次发现：`HANDOVER.md`、`jobs500-summary.json`、原始 `hdr-*/body-*/status-*`、`session-*.json`、
     `subject-scope.json`、`observation-scripts/`）
   - `acceptance-evidence/dual-system-v1/sprint7/r3-jobs500-handover-20260912T232500Z/`
     （终版交接：`nginx-access.log.txt`、`api-log.txt`、`backend-repro.txt`、`data-counts.txt`、
     `trace-contract.txt`、`HANDOVER.md`）
   两者均已扫描确认**不含任何明文口令/token**。数据侧：`download_jobs` 共 2419 行，
   investigator 的 org/ws 命中 **72** 行 ⇒ 页面本应有数据。
   无 Playwright trace：该失败由附加的用户层回归脚本（ad-hoc 浏览器脚本）发现，不是 Playwright 用例。

   **按约束本轮不改后端（超出本代理授权范围且禁止在途并发改后端），移交主代理。**
   R-3 的 30 绿**不含**该失败，两者分别封存。
4. **V1 用户层页面的端到端回归不是全量套件**：§10 是自建的真实浏览器对照（5 条路由 × 真实响应码），
   不是全量回归套件。仓库既有 `frontend/tests/api-control.spec.ts` 等旧 spec 仍按"不带受众头"的旧契约编写，
   本轮未改它们（`LEGACY-V1-P0-FIX.md §7.1` 早已把它们列为待适配项）。
5. **§27 其余 UI 项不在本项**：移动端 USER-011、四断点 UX-002、MFA/IdP、SSE 等未在本轮验证。
   R-3 局部 PASS **不能**推断 §27 UI 全部完成，也不等于整体验收。

---

## 12. 复现命令

```bash
# VM
cd /home/euripides/projects/blockchain-platform
bash acceptance-evidence/dual-system-v1/tests/run-r3-legacy-ui.sh
# 输出：acceptance-evidence/dual-system-v1/sprint7/ui-r3-<UTC 时间戳>/
#   逐步：entry-preflight.txt（含 http 入口拒绝逻辑）
#         deployment-identity.txt（ExecStart / /proc/<PID>/exe / sha256）
#         playwright.log + playwright-report.json（JSON reporter）
#         net-*.json / negative-*.json / nav-*.json / chart-axes-*.json / *.png
#         audit-collection-status.json + audit-denials.json + audit-trace-correlation.json
#         result.json（收紧判据：cases_accounted==cases_expected AND skipped==0 AND rc==0 AND failed==0 AND audit==OK）
```

## 13. 证据目录

```
acceptance-evidence/dual-system-v1/sprint7/
├── ui-r3-20260912T223223Z/   # 修复前基线（8/6 FAIL）
├── ui-r3-20260912T224600Z/   # 22/2 FAIL
├── ui-r3-20260912T224838Z/   # 24/2 FAIL
├── ui-r3-20260912T225057Z/   # 26/0 PASS
├── ui-r3-20260912T225603Z/   # 26/4 FAIL
├── ui-r3-20260912T225855Z/   # 30/0 PASS
├── ui-r3-20260912T230122Z/   # 30/0 PASS ← 最终；含 upload-manifest.json + regression-*.json
└── r3-jobs500-20260912T231500Z/   # 附加发现的真实失败（后端 500）证据包 + HANDOVER.md
```

全部 run 的 `playwright.log`、trace、截图**原样保留**，未覆盖、未删除。
