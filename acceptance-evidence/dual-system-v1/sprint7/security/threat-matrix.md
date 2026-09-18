# 威胁矩阵与攻击面分析（S7-SEC-01）

> 计划依据：`docs/dual-system-development-plan.md` §23（第 1776 行起，安全设计）、§27.6（第 2171 行起，安全测试矩阵）、§28.9（第 2369 行起，Sprint 7）、§23.8（第 1857 行起，安全发布门）
> 证据目录：`acceptance-evidence/dual-system-v1/sprint7/security/`
> 执行环境：Ubuntu VM `192.168.31.152`；API `:8080`（手工进程，非 systemd），Nginx `:8088`，PostgreSQL 18.6 / ClickHouse 26.7 / NATS 2 / MinIO（Docker Compose）
> 构建指纹：`backend/bin/api` SHA-256 `753ba724335e4be4231d40ca65809f22403eb3323b79f144b1161f88a6f21c78`（见 `negative-tests/files/environment.txt`）
> 负向套件结果：**PASS 87 / FAIL 3 / NOT_VERIFIED 0**（`negative-tests/result.json`）

**引用约定**：所有「现有控制」都必须能定位到真实文件行号、迁移编号或测试 ID。行号以本轮实测的代码版本为准。
**结论约定**：残余风险分 P0/P1/P2/P3；P0/P1 会直接否决 §23.8 发布门。

---

## 0. 方法与边界声明

| 项 | 说明 |
|---|---|
| 黑盒部分 | 对**运行中的真实 API** 执行 `negative-tests/run-sprint7-sec-negative.sh`（87 条断言，每条同时断言状态码与副作用） |
| 白盒部分 | 逐条审读 §23 涉及的中间件/服务实现并记录行号（`authmw.go`、`device.go`、`session.go`、`actions.go`、`logo_worker.go`、`sprint6_provider.go`、`nginx-blockchain-platform.conf` 等） |
| 数据层部分 | 以 `SET ROLE blockchain_app` 直接对数据库实测越权（`sprint7/security/application-account-separation/`） |
| 供应链部分 | govulncheck + pnpm audit + syft SBOM + trivy（`dependency-scan/`、`sbom/`、`image-iac/`） |
| **不覆盖** | 真实 Cloudflare 边缘（本环境为本机 Nginx 模拟）；真实 IdP（OIDC 未接，`AUTH_OIDC_ENABLED` 未开）；Windows 客户端侧；第三方依赖的运行时可利用性（污点分析） |
| **§23.1 的专门提醒** | 计划明确「Cloudflare Tunnel 下 TCP peer 可能是 localhost，因此必须同时验证入口来源、Nginx 配置和 Cloudflare 头，**不能把本地伪造头测试当生产证明**」。本报告对 client_ip 的结论已按此分档（见 B2-3、R-01）。 |

---

## 1. 信任边界总览（§23.1）

```text
[B1] Internet/User Device
      │  （本环境：直连 127.0.0.1:8088 模拟 Cloudflare 之后的入口）
      ▼
     Cloudflare / Nginx          ← 可信代理边界；只有配置内的代理地址可提供真实客户端 IP 头
      │
[B2]  ▼  Nginx → Go API (:8080)  ← 认证 + 策略 + 审计三重关卡
      │
      ├──[B3] 身份与会话（横向面，贯穿 B1/B2）
      ├──[B4] 授权与对象访问（租户/工作区，横向面）
      │
[B5]  ├──► PostgreSQL / ClickHouse / MinIO / NATS   ← 数据与审计边界
[B6]  ├──► 外部 Provider / OSINT 出站 URL            ← 出站边界（SSRF 面）
[B7]  └──► 构建与依赖供应链                           ← 供应链边界
```

---

## 2. 边界 B1：Internet/User Device → Cloudflare → Nginx

### 2.1 攻击面

| 资产 | 攻击面 | 实测状态 |
|---|---|---|
| 后台 SPA `/admin/*` | 浏览器渲染、CSP、点击劫持 | CSP 已下发（实测见 SEC-002a–d） |
| 用户层 SPA `/app-v2/*` | 同上 | 同上 |
| `/api/*` | 反向代理、超时、请求体大小 | `client_max_body_size 100m`，`proxy_connect_timeout 10s`，`proxy_read_timeout 3600s`（导出流式） |
| 静态资源 | 目录穿越 | `location ^~ /admin/` 等前缀匹配，无 `alias`/自动索引 |

### 2.2 STRIDE

| # | 类别 | 威胁 | 现有控制（代码/迁移/测试） | 残余风险 | 验证方式 |
|---|---|---|---|---|---|
| B1-1 | **S**poofing | 伪造客户端 IP 以绕过按 IP 的限流/审计 | Nginx 用 `$proxy_add_x_forwarded_for` **追加**而非覆盖（`deploy/nginx-blockchain-platform.conf:57,81,103,116`）；API 侧只信任 `AUTH_TRUSTED_PROXIES` 内的 peer，并从右向左走到第一个不可信地址（`backend/internal/iam/device.go:155-186`）；`CF-Connecting-IP` 优先于 XFF（`device.go:162-167`） | **R-01（P2）**：`AUTH_TRUSTED_PROXIES=127.0.0.1/32,::1/128`（`deploy/.env`；默认值见 `backend/internal/config/config.go:841`）。本机可达 `:8080` 者即是「可信代理」，其 XFF 会被采信。见 §9 R-01 | AUTH-010a（CF 头优先，client_ip=198.51.100.9）、AUTH-010b/c/d（来源链落库） |
| B1-2 | **T**ampering | 篡改静态资源 / 注入第三方脚本 | CSP `script-src 'self'`（无 `unsafe-inline`）、`object-src 'none'`、`base-uri 'self'`、`form-action 'self'`、`frame-ancestors 'none'`（`nginx-blockchain-platform.conf:73,95`）；`X-Content-Type-Options: nosniff`（:74,96） | 无 | SEC-002a–d（真实响应头） |
| B1-3 | **R**epudiation | 代理层日志缺失导致无法追溯 | `log_format blockchain_proxy` 记录 `$http_cf_connecting_ip`/`$request_method $uri`/`$status`/`$request_time`（`nginx-blockchain-platform.conf:6,11`） | 无 | 配置文件审读 |
| B1-4 | **I**nformation disclosure | 错误页/头泄露内部信息 | `Referrer-Policy: strict-origin-when-cross-origin`（:75,97）；API 错误体只回稳定错误码（`backend/internal/api/respond.go`） | **R-04（P3）**：§23.2 要求 `no-referrer`，实际为 `strict-origin-when-cross-origin` | 配置审读 + SEC-002a–d |
| B1-5 | **D**oS | 大请求体 / 慢连接耗尽 | `client_max_body_size 100m`（:13）、`proxy_connect_timeout 10s`（:61,86,108）、API 侧 `middleware.Timeout(60 * time.Second)`（`backend/internal/api/server.go:265`） | 无 | 配置审读 |
| B1-6 | **E**levation | 通过 Nginx 配置缺陷直达内部端口 | 仅监听 `127.0.0.1:8088`（:9）；MinIO `:9000`、PG `:5432`、NATS `:4222` 实测均只绑 `127.0.0.1` | 无 | `ss -lntp` 实测（见 §5.1） |

---

## 3. 边界 B2：Nginx → Go API（认证 + 策略 + 审计）

### 3.1 中间件链（`backend/internal/api/server.go:262-271`）

```text
Recoverer → Timeout(60s) → corsMiddleware → requestMetaMiddleware → logMiddleware
          → metricsMiddleware → identityMiddleware → auditCaptureMiddleware → 路由(require)
```

### 3.2 STRIDE

| # | 类别 | 威胁 | 现有控制 | 残余风险 | 验证方式 |
|---|---|---|---|---|---|
| B2-1 | **S**poofing | 提交 `X-User-Id`/`X-User-Role: owner` 直接提权 | `AUTH_MODE=session`（`deploy/.env`，实测）时完全不读兼容头：只有 `s.authMode.TrustsHeaders()` 为真才会解析 `auth.HeaderIdentity`（`backend/internal/api/authmw.go:233-238`；头解析在 `backend/internal/auth/auth.go:103,112`）；生产环境会强制降级（runbook §3） | 无 | **AUTH-009a–e**（无 Cookie → 401；Viewer 会话 + 伪造 owner 头 → 401；审计有 `authorization.denied`） |
| B2-2 | **S**poofing | 用用户层会话调用后台接口 | 受众与路径绑定：`/api/admin/` 只接受 admin 会话（`authmw.go:95-102`）；客户端 `X-Auth-Audience` 只能**收窄**不能放宽（`authmw.go:109-120`）；仅 `/api/auth/v1/*` 自助端点允许跨受众回退（`authmw.go:104-107,193-199`） | 无 | **AUTH-009b**、**RBAC-008a**（API Key → 后台 401）、S6 的 RBAC-S6e |
| B2-3 | **S**poofing | 伪造 `X-Forwarded-For` 覆盖 `client_ip` | `iam.ResolveClientIP`（`device.go:143-188`）：仅当 peer 命中可信网段才读转发头；`CF-Connecting-IP` 优先；否则从右向左走到第一个不可信地址即停（`device.go:171-181`），左侧伪造条目被忽略 | **R-01（P2）**（同 B1-1） | **AUTH-010a**：伪造 `X-Forwarded-For: 203.0.113.77` + `CF-Connecting-IP: 198.51.100.9` 时，`sessions.client_ip` 实测 = `198.51.100.9`（XFF 被丢弃）；AUTH-010b/c/d 证明 peer/client_ip/proxy_chain 三列均落库 |
| B2-4 | **T**ampering | 跨站写请求（CSRF） | `require()` 在授权后强制校验 CSRF：不满足 → 403 `csrf_failed`（`authmw.go:317-323`）；`csrfRequired` 只对非 GET/HEAD/OPTIONS 且存在会话的请求生效（`authmw.go:357-367`）；`checkCSRF` 要求 header 与 `<cookie>_csrf` 常量相等、会话可再次认证、并做服务端 hash 比对（`authmw.go:369-397`）；自助端点另有独立校验（`auth.go:562`）；SameSite=Lax 实测生效 | **R-02（P2）**：§23.2 要求「Origin/Referer 做第二检查」，代码中**不存在**任何 Origin/Referer 校验（全仓 grep 仅命中 CORS 的 `Access-Control-Allow-Origin`），实测带正确 CSRF token + `Origin: https://evil.example.com` 的写请求返回 200 | **SEC-001a–g**（缺 token 403 / 错 token 403 / 对 token 200 / 跨站 Origin 无 token 403 / 无副作用 0 行） |
| B2-5 | **T**ampering | 会话 Cookie 被 JS 读取或跨站携带 | `Set-Cookie` 实测：`HttpOnly` ✅、`SameSite=Lax` ✅；token 只存 `token_hash`（`sessions` 表列）；CSRF secret 单独 Cookie | **R-05（P1）**：`AUTH_COOKIE_SECURE=false`、`AUTH_COOKIE_PREFIX` 为空 → **无 `Secure`、无 `__Host-` 前缀**，与 §23.2 直接冲突 | **AUTH-013a–d**（FAIL×2：`__Host-` 计数 0、`Secure` 计数 0），原始头见 `negative-tests/files/cookie-attributes.txt` |
| B2-6 | **R**epudiation | 401/403 不留痕 | 每次拒绝都写 `audit_events_v2`：`auditDeny` 记录 `action=authorization.denied`/`category=authorization`/`result=deny`/`http_status`/`permission_key`/`deny_reason`（`authmw.go:399-433`）；业务写与非敏感读的规则见 `authmw.go:435-448` | 无 | **AUTH-009e**、**UNAUTH-b**、**RBAC-008g**（均以 SQL 计数为证据） |
| B2-7 | **I**nformation disclosure | 错误响应泄露 SQL/堆栈/密钥 | 统一响应信封 + 稳定错误码（`backend/internal/api/respond.go`）；5xx 才写内部 cause 到日志且不回显（`auth.go:249-256`）；错误码前缀表见计划 §21.3（第 1380 行） | 无 | 全部负向用例的响应体存档（`negative-tests/files/*.json`），未见堆栈/SQL |
| B2-8 | **D**oS | 暴力破解登录 | 双层限流：账号维 `AUTH_LOGIN_RATE_PER_MINUTE=20`/分、IP 维 `max(20×5,100)=100`/分（`backend/internal/iam/service.go:460-477`）；任一维度超限即阻断（`service.go:142-146`，注释明确说明早期 `&&` 短路 bug）；**只在失败后消耗配额**，正常用户不被 NAT 邻居拖累（`service.go:136-139`）；成功登录重置账号桶（`service.go:501-506`）；超限返回 429 + `Retry-After`（`backend/internal/api/authv1.go:240-248`，值取自 `service.go:509-517`） | **R-06（P3）**：现有账号第 10 次失败即被锁定（423，`AUTH_MAX_FAILED_LOGINS=10`/`AUTH_LOCK_DURATION_SECONDS=900`），因此 429 路径只在**不存在**的账号上可观测；两条路径都不可枚举账号（见 B2-9） | **SEC-004a–f**（26 次失败 → 401×20 后 429×6；429 带 `Retry-After`；错误码 `RATE_LIMIT_EXCEEDED`；同 IP 的正常账号仍 200） |
| B2-9 | **I**nformation disclosure | 通过响应差异枚举账号 | 未知账号对固定 dummy hash 做一次校验再返回，避免耗时差异（`service.go:155-161`）；统一 `invalid_credentials`（`service.go:148-160`）；登录审计用 `subject_masked`（`fi************@fixture.local`） | 无 | 实测 `login_events` 中成功/失败/阻断三类事件的 `subject_masked` 均为掩码形态 |

---

## 4. 边界 B3：身份与会话（横向面）

| # | 类别 | 威胁 | 现有控制 | 残余风险 | 验证方式 |
|---|---|---|---|---|---|
| B3-1 | **S**poofing | 盗用/重放会话令牌 | 令牌为 32 字节随机、库中只存 `token_hash`（`backend/internal/iam/session.go:155-160`，`sessions` 表列实测无明文列）；refresh 轮换并保留 family/parent（`sessions.family_id/parent_id/replaced_by/rotation_count`）；重放旧令牌触发整族撤销（`authmw.go:214-220` → `handleTokenReuse`） | 无 | **AUTHZ-a–f**（空 Cookie 401、尾字符篡改 401、原 Cookie 200 对照、过期 401、撤销后 1s 内 401） |
| B3-2 | **T**ampering | 会话被篡改为他人 | 服务端会话：令牌只是查找键，身份/组织/工作区/角色全部来自 DB 行，客户端不可写入 | 无 | AUTHZ-b（篡改 → 401） |
| B3-3 | **R**epudiation | 登录事件不完整 | `login_events` 记录 `event_type/result/reason_code/risk_level/client_ip/proxy_chain/device_*/user_agent/`hash 链（`prev_hash`/`event_hash`）；实测成功/失败/阻断三类都落库 | 无 | `negative-tests/files/AUTH-010-db.txt`、SEC-004e/f |
| B3-4 | **I**nformation disclosure | 会话 Cookie 被 XSS 读取 | `HttpOnly` 实测 ✅；token 不进响应体（登录响应只回 `csrf_token` 与 `session_id`） | R-05（Cookie 属性）不涉及 HttpOnly，故本项无残余 | 登录响应体 + cookie 属性实测 |
| B3-5 | **D**oS | 会话耗尽 | `AUTH_LOCK_DURATION_SECONDS=900`；会话有 idle/absolute TTL（`sessions.expires_at/absolute_expires_at`）；账号锁定为**临时**而非永久（`service.go:166` `UnlockIfExpired`） | 无 | 代码审读 + 实测（CLEANUP-c：无账号被遗留锁定） |
| B3-6 | **E**levation | 角色被降级后旧会话仍可写 | 会话认证时按 identity 现算角色/权限（`authmw.go:256-273` `principalFromSession` → `engine.Load`）；RBAC-006 的 5 秒失效依赖 `Engine.Invalidate` 前缀清理 | 未在本轮覆盖（S5/S3 套件覆盖 RBAC-006）；本轮只验证了**撤销会话**的 5 秒内失效 | AUTHZ-e（撤销后 ≤1s 生效） |

---

## 5. 边界 B4：授权与对象访问（横向面）

### 5.1 控制点

| 控制 | 位置 |
|---|---|
| 每端点声明权限 | `require(permission, handler)`：未认证 401、无权限 403、CSRF 失败 403，三者都写审计（`authmw.go:296-326`） |
| 后台路由表 | `backend/internal/api/adminv1.go:22-69`（含 `registerAdminOrgRoutes/Control/Provider/Governance/Observability`） |
| 用户层路由表 | `backend/internal/api/userv2.go:18-90`（每个 `r.Get/Post/Patch` 均带 `s.require`） |
| 对象级二次授权 | 列表在 SQL 层按 `organization_id`/`workspace_id` 过滤；详情/写入先过 `scopedObject`（如 `backend/internal/api/sprint4.go:236`，服务层再校验 `cases.organization_id AND workspace_id`：`backend/internal/casework/cases.go:943`） |
| 跨租户统一 404 | `backend/internal/api/adminv1.go:189-192`；用户层实测同样 404 |
| 工作区头不提升作用域 | `policyDenyByScope`（`authmw.go:352-355`）拒绝 `X-Workspace-Id` 与主体不一致的请求 |

### 5.2 STRIDE

| # | 类别 | 威胁 | 现有控制 | 残余风险 | 验证方式 |
|---|---|---|---|---|---|
| B4-1 | **S**poofing | 伪造 `X-Org-Id`/`X-Workspace-Id` 越租户 | 主体作用域只来自会话行；伪造头实测不改变结果集 | 无 | **AUTH-009d**（100 条记录中 0 条属于伪造组织）、**IDOR-g** |
| B4-2 | **T**ampering | IDOR：猜 UUID 读/写他组织对象 | 对象级 `organization_id + workspace_id` 双条件；越权读/写统一 404 | 无 | **IDOR-a–e**（跨组织读 404、响应体不含案件名、错误码 `RESOURCE_NOT_FOUND`、跨组织写 404 且评论数不变） |
| B4-3 | **E**levation | 同组织跨工作区越权 | 工作区成员关系决定可读范围（`workspace_memberships`） | 无 | **IDOR-f**：临时 Viewer 仅绑定工作区 A，读工作区 B 的案件 → 拒绝；**IDOR-f2**：读本工作区案件 → 200（对照组） |
| B4-4 | **E**levation | API Key 越 audience/scope | `actionAuth`（`backend/internal/api/actions.go:275-340`）：哈希常量时间比对 → DB 查 `gpt_action_api_keys`（`:293-299`）→ 作用域 `actionScopeAllowed`（`:360-367`）/ 路径映射 `actionScopeForPath`（`:351-359`）→ IP 允许列表（`:379-396`）；后台键管理另需静态 `GPT_ACTIONS_ADMIN_KEY`（`:503-523`），浏览器会话**不可**管理 | 无（scope 路径本轮首次覆盖，收口了 `dual-system-progress.md:103` 记录的 PARTIAL） | **RBAC-008a–g**（Key→后台 401；read-scope Key→export 端点 401、→analysis 端点 401；read 端点 200；无效 Key 401；会话不能管理 Key；`api_client` 角色 0 条 `admin:*`） |
| B4-5 | **E**levation | 权限矩阵外的隐式提权 | 13 个写权限在 0053 中显式授予 `platform_admin`(11)/`security_admin`(7)；`auditor` 保持只读（DB 可验证）；`approval_requests` 有 `CHECK (decided_by IS NULL OR decided_by <> requested_by)` 兜底双人约束 | 无 | S6 的 RBAC-S6a–f + 本轮 **RBAC-008f** |
| B4-6 | **R**epudiation | 越权尝试无记录 | 同上 B2-6；`auditDeny` 同时记录 `permission_key` 与 `deny_reason` | 无 | UNAUTH-b、AUTH-009e |

### 5.3 §23.8「管理 endpoint 权限声明覆盖率 100%」实测

对后台路由注册代码做结构化统计：**105 条 `r.Get/Post/Patch/Put/Delete/Head` 注册，105 条均带 `s.require(...)`/`s.requireAny(...)` 包装**（唯一一条 `requireAny` 是 `sprint6_provider.go:52` 的 `/providers/{id}/commands/{command}`）。
**结论：覆盖率 100%（静态核对）。** 缺口：仓库中**没有**自动化 gate 强制这条规则（只有 `scripts/check_openapi.py` 管契约数量），建议补一条路由-权限一致性测试（见 §9 GATE-04）。

---

## 6. 边界 B5：Go API → PostgreSQL / ClickHouse / MinIO / NATS

### 6.1 端口暴露实测

| 服务 | 监听 | 结论 |
|---|---|---|
| PostgreSQL | `127.0.0.1:5432` | 未对公网开放 ✅ |
| MinIO | `127.0.0.1:9000` | 未对公网开放 ✅ |
| NATS | `127.0.0.1:4222` | 未对公网开放 ✅ |
| API / Worker | `*:8080` / `*:8081` | 监听所有网卡，但由 Nginx 前置；VM 防火墙未在本轮验证 |
| Nginx | `127.0.0.1:8088` | 仅本机 ✅ |

### 6.2 STRIDE

| # | 类别 | 威胁 | 现有控制 | 残余风险 | 验证方式 |
|---|---|---|---|---|---|
| B5-1 | **T**ampering | 应用删除/改写不利审计记录 | 迁移 `0054` 建受限角色 `blockchain_app`（`REVOKE UPDATE,DELETE,TRUNCATE ON audit_events_v2/login_events/audit_chain_heads/audit_chain_anchors/approval_request_events/user_activity_events`）；运行期每条连接 `SET ROLE`（`backend/internal/store/db.go:34`，开关 `DB_RUNTIME_ROLE=blockchain_app`）；0055 另加触发器 `audit_events_v2_append_only` / `audit_chain_anchors_immutable_trg` / `audit_chain_anchors_no_delete` | 无 | **AUDIT-a–k**（`has_table_privilege` 全 f；实际 UPDATE/DELETE/TRUNCATE/DDL 全部 `permission denied`；INSERT 仍可用） |
| B5-2 | **T**ampering | 事后伪造 hash 链 | 每条事件带 `prev_hash`/`event_hash`（`audit_events_v2` 列）；链校验 `POST /api/admin/v1/audit/verify` 返回 `valid`/`broken_at_seq`/`integrity_status`；ADR-018 明确 `valid=true ≠ 链路完整` | 无（S6 已封存不可恢复缺口并保持 `sealed_incident`，本轮未触碰） | S6 的 AUDIT-004/AUDIT-004T（含真实篡改-检测-还原演练）；本轮未重复 |
| B5-3 | **E**levation | 应用账号提权为超级用户 | 运行角色非超级用户、无 BYPASSRLS、非 owner、无 `CREATE ON SCHEMA public`、不能被授予其他角色 | **R-07 / GAP-01（P1）**：**连接账号 `blockchain` 是超级用户**；`SET ROLE` 可被一条 `RESET ROLE` 撤销。代码中无 `RESET ROLE`（全仓 0 命中），但数据库层无强制 | `application-account-separation.md` §2.1/§2.6（实测 `rolsuper=t`、17 条实连） |
| B5-4 | **I**nformation disclosure | 备份/导出泄露秘密 | `admin.provider.*` 只存 `secret_ref` + 前缀 + 指纹，响应恒有 `plaintext_available:false`（`sprint6_provider.go:309-320`）；审计导出产物带 manifest + SHA-256 | 无 | S6 的 ADMIN-003/008；本轮 AUDIT-*（秘密扫描 0 明文） |
| B5-5 | **D**oS | 慢查询/长事务拖垮库 | `middleware.Timeout(60s)`（`server.go:265`）；列表查询强制 limit/offset（`pagination(r)`）；导出走异步任务 + 一次性票据 | 无 | 配置审读（性能面由 S7-PERF-01 覆盖） |
| B5-6 | **R**epudiation | 审计与业务写入不一致 | 审计快照表 `audit_events_v2` 与 `audit_events`(v1) 并存；S6 建立「不可变完整性边界」（迁移 0055 + ADR-018），缺口被显式封存而非回填 | 无 | `docs/adr/ADR-018-audit-integrity-incident-boundary.md`；本轮保持只读未触碰 |

---

## 7. 边界 B6：Go API → 外部 Provider / OSINT 出站 URL

### 7.1 出站调用面清点（全仓 `http.NewRequest*`/`client.Do` 审计）

| 出站目标 | 位置 | URL 来源 | 是否有私网/回环/metadata 校验 |
|---|---|---|---|
| Token logo 取图 | `backend/internal/tokenmeta/logo_worker.go:138` | 元数据里的 `LogoURL`（来自 Provider/registry 响应，`extractor.go:46`） | **有**：`ValidatePublicHTTPS`（`:29-60`）只允许 https、拒绝 `localhost`/`*.localhost`、解析 DNS 后逐个 IP 用 `blockedIP` 判定（`:61-63`：loopback/private/unspecified/link-local/multicast/`169.254.169.254`） |
| OIDC discovery/token/JWKS | `backend/internal/iam/oidc.go:253,366,397` | 受控配置（`AUTH_OIDC_*`），未启用 | 生产要求 https issuer（runbook §1.2） |
| Ankr 管理 MCP / OAuth | `backend/internal/provider/ankrmgmt/service.go:182,228`、`oauth.go:116` | 受控配置 `MCPURL`/`IssuerURL` | 受控配置 |
| RPC/链上 Provider | `ankr/client.go:269`、`rpc/client.go:193`、`tron/client.go:317,437` | Provider 配置 | 受控配置 |
| 公开签名库 | `backend/internal/method/public_signature.go:63` | `endpoint`（配置） | 受控配置 |

### 7.2 STRIDE

| # | 类别 | 威胁 | 现有控制 | 残余风险 | 验证方式 |
|---|---|---|---|---|---|
| B6-1 | **S**poofing/**I**nfo disclosure | SSRF：让服务端访问 metadata/私网 | 唯一的「内容驱动出站 URL」路径（token logo）有完整守卫（上表）；守卫有专门单测 | **R-03（P2）**：`PATCH /api/admin/v1/providers/{id}` 接受任意 `endpoint` 字符串且**无任何 URL/私网校验**（`sprint6_provider.go:230-294`）；实测写入 `http://169.254.169.254/latest/meta-data/` 返回 200。**未构成可利用 SSRF**：连接/测试探针走运行时实现 `s.deps.Prov.Health()`（`:481-521`），不读取该列。属纵深防御缺口 | **SEC-003a**（守卫单测 PASS）、**SEC-003e**（实测无校验）、**SEC-003c/d**（未认证/Viewer 改端点 401）、**SEC-003f**（已还原原值）、**SEC-003g**（改动有审计） |
| B6-2 | **T**ampering | 出站重定向绕过校验 | 守卫在发起前对**解析后的 IP** 判定（`logo_worker.go:47-58`） | **R-08（P3）**：`client.Get` 未禁用重定向跟随、未二次校验重定向目标（§23.5 要求「重定向重新校验」） | 代码审读（`logo_worker.go:107-140`） |
| B6-3 | **D**oS | Provider 被反复打爆/成本攻击 | 出站并发/限流/熔断在 Provider 层（`provider/rpc/gateway.go`、`routing_policies`）；logo worker 队列上限 128 且有「队列满」告警（`logo_worker.go:68-104`）；下载/导出走异步任务 | 无 | 代码审读（性能面由 S7-PERF-01 覆盖） |
| B6-4 | **I**nformation disclosure | 出站请求把秘密写进日志 | 日志脱敏实测：366 行 `***` 掩码（如 `https://rpc.ankr.com/tron_jsonrpc/***`） | 无 | `secret-scan/REPORT.md`（AUDIT-002） |

---

## 8. 边界 B7：供应链（依赖 / 构建 / 镜像 / IaC）

| # | 类别 | 威胁 | 现有控制 | 残余风险 | 验证方式 |
|---|---|---|---|---|---|
| B7-1 | **T**ampering | 恶意 npm/go 依赖 | lockfile 固定（审计前后 md5 一致）；govulncheck + pnpm audit 进流程 | **R-09（P1）**：**P0=2 / P1=5**（详见 §9 与 `dependency-scan/SUMMARY.md`）。可达的高危：excelize `GO-2026-5960`（调用点在 `internal/api/export.go:414`）；前端 `xlsx@0.18.5` 两条 HIGH **npm 上无修复版本**（SheetJS 已迁出 npm） | govulncheck v1.8.0（DB 2026-09-10）、pnpm audit |
| B7-2 | **I**nformation disclosure | 构建产物含 secret / SBOM 缺失 | SBOM 由 syft 生成（`sbom/`）；构建产物秘密扫描见 `secret-scan/` | 见 `sbom/SUMMARY.md` | syft CycloneDX/SPDX |
| B7-3 | **E**levation | 镜像内已知漏洞被利用 | trivy 对 compose 实际使用的 4 个镜像逐个扫描（`image-iac/images/`） | **R-10（P1）**：实测 **CRITICAL 20 / HIGH 203**（postgres:18 14C+101H、minio:latest 6C+100H、nats:2-alpine 0C+2H、clickhouse 26.7 0C+0H）。多为基础镜像（Debian）层的历史 CVE，需按「是否暴露到公网 + 是否可达」逐条定级，不能直接全量接受 | `image-iac/images/*.json` + `image-iac/images/*.txt`（trivy 0.74.0，镜像 digest 见 `images/image-digests.txt`） |
| B7-4 | **T**ampering | IaC/运行配置的安全基线未被检查 | — | **R-11（P2）**：trivy 的 misconfig 扫描器集合（azure-arm/cloudformation/dockerfile/helm/kubernetes/terraform/ansible）**不覆盖 nginx conf、systemd unit、compose.yaml**；实测 `trivy config /w/deploy` 输出 `Detected config files num=0`。**该 "0" 是「0 个文件被扫描」，不能读作「0 个问题」** | `image-iac/misconfig/trivy-config.txt` 与 `raw/trivy-config-run.log`；替代覆盖见 `image-iac/SUMMARY.md` |

---

## 9. 残余风险登记册

| ID | 级别 | 风险 | 证据 | 建议处置 |
|---|---|---|---|---|
| **R-09** | **P1** | 依赖漏洞 P0=2 / P1=5；可达高危 excelize `GO-2026-5960`；前端 `xlsx` 两条 HIGH 无 npm 修复版 | `dependency-scan/SUMMARY.md` | 升级 pgx≥5.9.2、chi≥5.3.0、excelize≥2.11.0；xlsx 走替换库/CDN 完整性校验/正式风险接受（需供应链审批） |
| **R-10** | **P1** | 基础镜像 CRITICAL 20 / HIGH 203（postgres:18 与 minio:latest 占绝大多数） | `image-iac/images/*.json` | 逐条按「公网暴露 + 可达性」定级；优先基镜像升级/固定 digest；MinIO/PostgreSQL 已只绑 127.0.0.1，实际暴露面受限 |
| **R-11** | P2 | trivy misconfig 不覆盖本仓库的 nginx/systemd/compose，配置基线实际未由 trivy 覆盖 | `image-iac/raw/trivy-config-run.log`（`num=0`） | 用 checkov/hadolint/systemd-analyze security 补充；或把 nginx/systemd 检查写成自定义策略 |
| **R-07** | **P1** | 应用**连接账号是超级用户**（`blockchain`），`SET ROLE` 可被 `RESET ROLE` 撤销 → ADR-017 §5 未完全满足 | `application-account-separation.md` §2.1/§2.6 | GATE-01 / GATE-02 |
| **R-05** | **P1** | 会话 Cookie 无 `Secure`、无 `__Host-` 前缀，与 §23.2 冲突 | AUTH-013c/d（FAIL） | 生产启用 `AUTH_COOKIE_SECURE=true` + `AUTH_COOKIE_PREFIX=__Host-`（要求 HTTPS + Path=/ + 无 Domain，本环境为 HTTP 故未开） |
| **R-01** | P2 | `AUTH_TRUSTED_PROXIES` 含 `127.0.0.1/32`：任何能连到 `:8080` 的本机进程都可伪造 `client_ip`（经 Nginx 且无 `CF-Connecting-IP` 时同理） | AUTH-010b/c 实测 | 生产确保请求必经 Cloudflare（`CF-Connecting-IP` 优先）或在 Nginx 用 `proxy_set_header X-Forwarded-For $remote_addr` 覆盖而非追加 |
| **R-02** | P2 | 缺少 §23.2 要求的 Origin/Referer 第二检查 | SEC-001g（带正确 CSRF 时跨站 Origin 仍 200） | 在 `require()` 中增加 Origin/Referer 白名单校验（CSRF token 仍是主防线） |
| **R-03** | P2 | Provider `endpoint` 写入无 URL 校验（未接入出站请求，属纵深防御缺口） | SEC-003e（实测 200） | 复用 `tokenmeta.ValidatePublicHTTPS` 同款校验（https + 非私网/回环/metadata） |
| **DEF-01** | P2 | `POST /api/v1/admin/action-api-keys` 省略 `allowed_ips` 字段即 500（`allowed_ips` NOT NULL 无默认值兜底，handler 传 NULL） | RBAC-008h（FAIL，实测 500）；`RBAC-008i` 证明无半成品记录 | 写入前把 nil 切片规范化为 `[]string{}`（或让列有默认值） |
| **DEF-02** | P3 | 用户层 `/api/v2/cases/{id}` 未注册 DELETE（405），而权限 `case:delete` 与 `handleDeleteCase` 均已存在（v1 兼容路由有） | CLEANUP-e 实测 405 | 明确「用户层是否允许删案件」的产品结论；若要允许则补路由，若不允许则文档化 |
| **R-04** | P3 | `Referrer-Policy` 为 `strict-origin-when-cross-origin`（§23.2 要求 `no-referrer`） | 实测响应头 | 与产品确认后收紧 |
| **R-06** | P3 | 429 路径只在不存在账号上可观测（现有账号第 10 次即 423 锁定） | SEC-004a–f | 文档化两档语义；如需 429 优先可在锁定前先判限流 |
| **R-08** | P3 | logo 取图未禁止重定向跟随 | 代码审读 | `CheckRedirect` 内复用 `ValidatePublicHTTPS` |

**P0 计数 = 0**（基于权威构建 `6106d8e4`：未发现可远程未授权利用的严重缺陷）；**P1 = 4**（R-09 依赖、R-07 账号分离、R-05 Cookie 属性、R-10 基础镜像）。

> ⚠️ **任务执行期间**，产品侧的并发改动在**更晚的构建**上引入了一个 **P0 可用性回归（F-01）**：
> HTTP 入口下所有 Cookie 鉴权写请求 403 `csrf_failed`。它不属于权威构建的评估结论，
> 但会打红所有直连 `:8080` 的既有验收套件。详见 §12 与 `negative-tests/CURRENT-BUILD-FINDINGS.md`。

---

## 10. 与 §23.8「安全发布门」的逐条对照

| §23.8 门 | 判定 | 依据 |
|---|---|---|
| P0/P1 漏洞为 0 | ❌ **不满足** | 依赖扫描 P0=2/P1=5（`dependency-scan/SUMMARY.md`）；基础镜像 CRITICAL 20/HIGH 203（R-10）；应用侧 R-05/R-07 亦为 P1 |
| 所有管理 endpoint 权限声明覆盖率 100% | ✅ 满足（静态核对 105/105） | §5.3 |
| 跨组织对象访问负向用例覆盖所有资源类型 | 🟡 **部分** | 本轮覆盖案件/评论（读+写）、工作区、后台 10 端点、API Key；**未覆盖** findings/evidence/report/canvas/download 资源的跨租户负向（S5 套件覆盖 RBAC-004/007，资源类型未穷尽） |
| 日志秘密扫描 0 命中；构建产物不含 secret | ✅ 满足 | `secret-scan/REPORT.md`（(c) 类明文 = 0；已知秘密阴性对照 0 泄漏；阳性对照自测 10/10） |
| Session 撤销在 5 秒内生效 | ✅ 满足 | AUTHZ-e（撤销后 1 秒内 401） |
| 角色停用后的旧请求不能继续写入 | 🟡 **未在本轮验证** | 属 RBAC-006（S3/S5 套件）；本轮只验证了会话撤销 |
| 审计 hash chain 和每日归档校验通过 | ✅ 维持（未触碰） | ADR-018 / 迁移 0055；S6 套件断言 `integrity_status=sealed_incident` 且边界之后 `valid=true` |
| 依赖漏洞、SBOM、容器镜像和 IaC 检查进入 CI | 🟡 **本地产出完成，尚未接 CI** | `dependency-scan/`、`sbom/`、`image-iac/` 均已产出可重跑命令；`docs/dual-system-development-plan.md` §29.1 的管道尚未接入这些步骤；且 IaC 侧需先解决 R-11（扫描器不覆盖本仓库配置类型） |

### 结论

**S7-SEC-01 的「完成」是：威胁矩阵、负向用例、依赖/镜像/IaC/SBOM 证据、日志秘密扫描、账号分离实测全部落地且可复现；
但 §23.8 安全发布门当前不通过**，原因是 R-09（依赖 P0/P1）、R-07（连接账号仍为超级用户）、R-05（Cookie 缺 Secure/__Host-）。

---

## 11. 建议新增的自动化 gate

| ID | gate | 命令/判定 |
|---|---|---|
| GATE-01 | 应用连接账号非超级用户 | `select not rolsuper and not rolbypassrls from pg_roles where rolname='<连接角色>'` 期望 t（详见 `application-account-separation.md` §4） |
| GATE-02 | 降权不可被 `RESET ROLE` 撤销 | 连接角色 `ALTER ROLE ... SET ROLE blockchain_app` + 全仓 `RESET ROLE` 0 命中 |
| GATE-03 | 审计只读性回归 | `run-sprint7-sec-negative.sh` 的 AUDIT-a..k（已实现） |
| GATE-04 | 后台路由 100% 声明权限 | 新增单测：遍历 chi 路由树，断言每个 `/api/admin/v1/*` 节点在无权限主体下返回 403/401 而非 200 |
| GATE-05 | 依赖扫描进 CI | `govulncheck ./...` 与 `pnpm audit --audit-level=high` 作为必需步骤（当前 CI 无） |
| GATE-06 | Cookie 安全属性 | **按入口分档断言**：`X-Forwarded-Proto: https` → `Set-Cookie` 含 `Secure` 与 `__Host-` 前缀；HTTP 入口 → 不下发前缀，**且该入口下的写请求必须仍可用**（已实现为套件 `AUTH-013a..f`） |

---

## 12. 任务执行期间产品侧并发改动引入的回归（F-01，P0）

> 完整证据与最小复现见 `negative-tests/CURRENT-BUILD-FINDINGS.md`。

| 项 | 内容 |
|---|---|
| 现象 | HTTP 入口（直连 `http://127.0.0.1:8080`）下，**任何** Cookie 鉴权的写请求返回 **403 `csrf_failed`**，即使 CSRF token 完全正确；读请求 200 正常 |
| 影响面 | **所有**直连 `:8080` 的自动化与运维脚本（run-sprint4/5/6、run-iam-e2e、run-rbac-e2e …）。经 Nginx / 生产 HTTPS 入口的流量不受影响 |
| 决定性对照 | 同一会话、同一 token，仅把会话 Cookie 名由 `cs_admin_session` 换成 `__Host-cs_admin_session` → **200 OK** |
| 根因 | `deploy/.env` 被改为 `AUTH_COOKIE_SECURE=auto` + `AUTH_COOKIE_PREFIX=__Host-` 后：写入端 `SetCookiesFor`（`iam/session.go:459-461`）用 `CookieNameFor(audience, secure)`（HTTP 下**不加**前缀），而 `checkCSRF`（`api/authmw.go:459`）用旧的 `cfg.CookieName(audience)`（`NamePrefix` 非空即**无条件加**前缀）→ 读到的名字与写入的不一致 → `return false` |
| 对应威胁（本矩阵） | B2-4（CSRF）与 B2-5（Cookie 属性）的**修复过程本身**引入的可用性风险；说明「安全属性收紧」必须与「读写路径一致性」一起回归 |
| 修复建议 | `checkCSRF` 的会话 Cookie 读取改用 `CookieNameCandidates(audience)`（与同一函数里的 CSRF Cookie 保持一致），或 `CookieNameFor(audience, cfg.SecureFor(requestScheme(r)))`；修复后**必须用直连 `:8080` 路径回归** |
| 已加入套件的守门用例 | `AUTH-013e`（HTTP 入口不下发前缀 = 设计行为）与 **`AUTH-013f`（HTTP 入口下带正确 CSRF 的写请求必须 200）** |
