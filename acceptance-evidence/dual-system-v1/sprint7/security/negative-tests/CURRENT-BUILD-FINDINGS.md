# 当前构建（S7-SEC-01 之后发生的改动）的追加实测发现

> 执行者：S7-SEC-01 主执行者
> 时间窗：2026-09-12T17:00Z – 17:12Z
> **背景：本 VM 上存在并发部署循环，`backend/bin/api` 在约 40 分钟内被重建至少 5 次**
> （`753ba724` → `6106d8e4` → `bae72e0a` → `23a8dd77` → `47b95f29` → `7816e168`）。
> 因此本文件记录的是**运行中构建的时点行为**，不是 S7-SEC-01 的权威套件结果
> （权威结果见 `result.json`，对应构建 `6106d8e4`）。

---

## F-01（P0）：HTTP 入口下**所有 Cookie 鉴权的写请求** 403 `csrf_failed`

### 现象（可复现）

```bash
# 1) 直连 API（HTTP 入口，不声明 X-Forwarded-Proto）
curl -s -c jar -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' \
  -d '{"identifier":"admin@local","password":"<pw>","audience":"admin"}' \
  http://127.0.0.1:8080/api/auth/v1/login          # 200，拿到 csrf_token（43 字符）
CSRF=$(grep cs_admin_session_csrf jar | awk '{print $7}')

curl -s -o /dev/null -w '%{http_code}\n' -b jar -H 'X-Auth-Audience: admin' \
  http://127.0.0.1:8080/api/admin/v1/overview       # 200  ← 会话有效

curl -s -b jar -H 'Content-Type: application/json' -H "X-CSRF-Token: $CSRF" \
  -H 'X-Auth-Audience: admin' -X POST -d '{"chain":"audit"}' \
  http://127.0.0.1:8080/api/admin/v1/audit/verify
# → 403 {"error":{"code":"csrf_failed"}}   ← 即使 token 完全正确
```

同一现象出现在 `PATCH /api/admin/v1/settings/*` 与用户层 `POST /api/v2/cases`（均 403 `csrf_failed`）。
**读请求正常（200）**。已排除 Origin/Referer 因素：试过 7 种组合（无 Origin、同源 8080/8088、`localhost`、
恶意 Origin `https://evil.example.com`、带 Referer）——**全部 403**。

### 决定性对照（证明是 Cookie 名不一致，而非 token 错误）

| 发送的会话 Cookie 名 | 结果 |
|---|---|
| `cs_admin_session`（登录时**实际下发的名字**） | **403 csrf_failed** |
| `__Host-cs_admin_session`（带前缀的名字） | **200 OK** |
| 两个都带 | 200 OK |

### 根因（代码级）

`deploy/.env` 现已被改为 `AUTH_COOKIE_SECURE=auto` + `AUTH_COOKIE_PREFIX=__Host-`（R-05 修复进行中）。于是：

- **写入**：`SetCookiesFor`（`backend/internal/iam/session.go:459-461`）用 `CookieNameFor(audience, secure)`；
  HTTP 入口 `secure=false` → 下发**不带前缀**的 `cs_admin_session`。
- **读取**：`checkCSRF`（`backend/internal/api/authmw.go:459`）用 `cfg.CookieName(audience)` —— **旧的、不看 secure 的函数**
  （`session.go:141` 起），只要 `NamePrefix` 非空就**无条件加前缀** → 去找 `__Host-cs_admin_session` → 找不到 → `return false`。
- 同一个函数里，**CSRF Cookie 用的是 `CSRFSecretCookieCandidates(audience)`（多候选，正确）**，
  而**会话 Cookie 用的是单名 `CookieName(audience)`（带前缀，错误）** —— 两者不对称。

**修复**：把 `checkCSRF` 里的会话 Cookie 读取也改成候选列表（`CookieNameCandidates`），或
`CookieNameFor(audience, cfg.SecureFor(requestScheme(r)))`，与写入端保持一致。

### 影响

- **受影响**：所有直连 `http://127.0.0.1:8080` 的自动化（run-sprint4/5/6、run-iam-e2e、run-rbac-e2e 等**全部既有验收套件**）
  与直连 8080 的运维脚本——它们的写操作会整体 403。
- **不受影响**：经 Nginx（`/api/` 会设置 `X-Forwarded-Proto https`）或生产 HTTPS 入口的流量。
- **必须回归**：修复后要用**直连 :8080** 的路径回归，否则内网验收链路会整体变红。

### R-05 本身在 HTTPS 入口下是正确的（正面结论）

```text
带 X-Forwarded-Proto: https 登录
  Set-Cookie: __Host-cs_admin_session=…; Path=/; HttpOnly; Secure; SameSite=Lax
  Set-Cookie: __Host-cs_admin_session_csrf=…; Path=/; Secure; SameSite=Lax
  同一入口下的写请求 → 200 ✅
不带 XFP（HTTP 入口）
  Set-Cookie: cs_admin_session=…; Path=/; HttpOnly; SameSite=Lax        （无前缀、无 Secure，符合 SecureMode=auto 的设计）
```

---

## F-02：Provider 端点写入**已被拒绝**（先前报告的 R-03 看来已修）

- S7-SEC-01 期间实测：`PATCH /api/admin/v1/providers/{id}` 写 `http://169.254.169.254/latest/meta-data/` 返回 **200**（无 URL 校验）→ R-03。
- 当前构建：同一操作不再被接受（`SEC-003e` 观测为 `validated`）。需要作者确认是**新增了 URL 校验**还是别的原因，
  并补一条与 `tokenmeta.ValidatePublicHTTPS` 等价的正/负单测。

## F-03：跨组织写评论由 404 变为 403

- `IDOR-d`：Investigator 跨组织 `POST /api/v2/cases/{id}/comments` 由 404 变为 **403**。
- §23.4 允许「统一 404 或策略确定的 403」，因此 403 本身不违规；但**读 404 / 写 403 的不一致**会向攻击者
  泄露「该对象存在但你无权写」这一位信息。建议统一（或明确记录该策略选择）。

## F-04：`admin@local` 的 `display_name` 被写入了 XSS 载荷（第三方测试造成的生产数据污染）

实测（登录响应原文）：

```json
"display_name": "\u003cimg src=x onerror=\"window.__xss=1\"\u003eSCRI PT"
```

即 `<img src=x onerror="window.__xss=1">SCRI PT`。两点：

1. **数据污染**：这是对既有账号资料的修改，不是本任务所为（S7-SEC-01 全程未写 `identities`）。建议定位来源并清理该字段。
2. **控制缺口**：`display_name` 服务端**未做标签剥离/转义**，而案件评论有专门处理
   （`backend/internal/casework/cases.go:1013` `sanitizeComment`，在 `:932` 应用）。前端 React 默认转义可兜住，
   但**依赖渲染层兜底不是纵深防御**——建议对昵称/标题类字段统一做输出上下文编码或在写入时剥离标签。

## F-05：环境不稳定，S7-SEC-01 的权威结果基于更早的构建

| 轮次 | 构建 | 结果 | 是否权威 |
|---|---|---|---|
| 1 | `753ba724` | PASS 72 / FAIL 10（含 7 条脚本口误，已修正） | 否 |
| 2 | `753ba724` | PASS 87 / FAIL 3 / NV 0 | 是（同构建复现） |
| 3 | `6106d8e4` | **PASS 87 / FAIL 3 / NV 0** | **是（`result.json` 采用此轮）** |
| 4 | `bae72e0a` | 完成；FAIL 仅 2 条 Cookie（DEF-01 已修） | 参考（运行中有一次构建替换） |
| 5 | `47b95f29` | FAIL 9 / NV 5（全为 `csrf_failed`，即 F-01） | 否（记录 F-01 现象） |
| 6 | 经 Nginx | `000/502` 大量出现（API 正在重启） | 否（归档为 `result.INVALID-env-unstable-nginx-502.json`） |

**结论**：在 `6106d8e4` 上，S7-SEC-01 的结论是 **FAIL 3 条**（Cookie `__Host-`/`Secure` 两条 + DEF-01）。
在那之后，产品侧（并发部署）又发生了 R-05 与 R-03 相关的改动；这些改动**尚未经过本套件的完整回归**，
且引入了 F-01 这个 P0。**建议在部署循环停止后，用 `bash acceptance-evidence/dual-system-v1/tests/run-sprint7-sec-negative.sh`
重跑一次，作为 S7-SEC-01 的最终回归证据。**

## 复现方式

```bash
# 权威套件（直连 8080，需部署稳定）
bash acceptance-evidence/dual-system-v1/tests/run-sprint7-sec-negative.sh

# F-01 最小复现（见本文档第 1 节代码块）
```
