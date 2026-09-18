# S7-SEC-01 修复补记：R-05（Cookie 安全属性）与 R-07（应用连接账号分离）

> 本文件是 `S7-SEC-01-completion.md` 的补记。原报告在 2026-09-12 给出
> **R-05 = FAIL / R-07 = PARTIAL**；同日完成修复并在真实入口上复验，结论更新如下。
> 所有验证都记录了构建 SHA 与实际命令输出。

## 1. R-05：会话 Cookie 缺少 Secure 与 `__Host-` 前缀

### 1.1 原缺陷（实测）
`AUTH_COOKIE_SECURE=false`、`AUTH_COOKIE_PREFIX`（空）→ 会话 Cookie 既无 `Secure`
也无 `__Host-` 前缀，与 §23.2 冲突（HTTPS 入口下仍可被明文信道降级窃取）。

### 1.2 修复方式（按入口协议自适应，而不是全局开关）
新增 `SessionConfig.SecureMode`（`auto` / `always` / `never`）：

| 入口 | 判定依据 | Cookie 名 | 属性 |
|---|---|---|---|
| 公网 HTTPS（Cloudflare → tunnel → nginx `X-Forwarded-Proto: https`） | `SecureFor("https")` | `__Host-cs_admin_session` | `HttpOnly; Secure; SameSite=Lax; Path=/`，无 Domain |
| 内网 HTTP（直连 `:8080`，仅测试/运维） | `SecureFor("http")` | `cs_admin_session` | `HttpOnly; SameSite=Lax`（不加 `__Host-`：前缀要求 Secure，否则浏览器直接拒收） |

读取侧统一改为**候选名**（先 `__Host-` 名、再普通名），因此 HTTP/HTTPS 切换或回滚都不会
让既有会话因改名而失效；登出会同时清除两种命名形态。配置：`AUTH_COOKIE_SECURE=auto`、
`AUTH_COOKIE_PREFIX=__Host-`。

### 1.3 端到端复验（2026-09-12，构建 `fd04bfda…`）

```text
# 公网 HTTPS（真实链路：Cloudflare → cloudflared → nginx → api）
login  HTTP/2 200
Set-Cookie: __Host-cs_admin_session=...; Path=/; Max-Age=604800; HttpOnly; Secure; SameSite=Lax
Set-Cookie: __Host-cs_admin_session_csrf=...; Path=/; Max-Age=604800; Secure; SameSite=Lax
GET  /api/auth/v1/session      -> 200
GET  /api/admin/v1/overview    -> 200
POST /api/admin/v1/audit/verify-> 200
POST /api/auth/v1/logout       -> 清除 4 个 Cookie（两种命名形态各 2 个），随后 session 401

# 经 Nginx（`X-Forwarded-Proto: https`）
Set-Cookie: __Host-cs_admin_session=...; HttpOnly; Secure; SameSite=Lax

# 直连 8080（HTTP，内网验收链路，行为与修复前一致）
Set-Cookie: cs_admin_session=...; Path=/; Max-Age=604800; HttpOnly; SameSite=Lax
```

### 1.4 修复过程中引入并已消除的 P0（F-01，如实记录）
首版实现只把 **CSRF Cookie** 的读取改成候选名，`checkCSRF` 里的**会话 Cookie** 仍用
`cfg.CookieName(audience)`（无条件加前缀）→ HTTP 入口下"CSRF token 正确却 403 `csrf_failed`"，
会让所有直连 `:8080` 的既有套件全线变红。
**发现方式**：安全套件在部署循环中交叉复核时上报（决定性对照：仅把 Cookie 名换成
`__Host-` 前缀即 200）。
**修复**：`checkCSRF` 改用 `cookieByName(r, cfg.CookieNameCandidates(audience))`。
**回归验证（三个入口各测一次写请求）**：

```text
直连 8080   POST /api/admin/v1/audit/verify -> 200
Nginx 8088  POST /api/admin/v1/audit/verify -> 200
公网 HTTPS  POST /api/admin/v1/audit/verify -> 200
```

结论：**R-05 关闭**（HTTPS 入口 Secure + `__Host-`；HTTP 入口保持兼容；三入口写请求均 200）。

## 2. R-07：应用连接账号是超级用户（ADR-017 §5 未满足）

### 2.1 原状态
运行角色 `blockchain_app` 的隔离本身成立，但**连接账号 `blockchain` 是超级用户**：
一条 `RESET ROLE` 即可回到超级用户；DB 层没有任何东西阻止它。实测
`rolsuper/createdb/createrole/bypassrls/replication` 全为 `t`。

### 2.2 修复方式
新增非超级用户的登录角色，并把它与运行角色解耦：

```sql
CREATE ROLE blockchain_api LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS NOREPLICATION;
GRANT blockchain_app TO blockchain_api;     -- 连接后仍由应用显式 SET ROLE 到受限角色
```

应用与运维账号拆成两个变量：

| 变量 | 账号 | 用途 |
|---|---|---|
| `DATABASE_URL` | `blockchain_api`（受限登录角色，NOINHERIT） | api / worker 运行期连接 |
| `MIGRATION_DATABASE_URL` | `blockchain`（owner） | `cmd/migrate`、`cmd/admin-init`、`cmd/admin-fixture`、启动期迁移 |

代码侧：`app.Bootstrap` 的迁移连接改用 `MigrationDatabaseURL`；`cmd/migrate` 同步改用该变量。

### 2.3 复验（2026-09-12）

```text
rolname        | rolsuper | rolcreatedb | rolcreaterole | rolbypassrls | rolinherit | rolcanlogin
blockchain     | t        | t           | t             | t            | t          | t      <- 运维账号（仍需 DDL）
blockchain_app | f        | f           | f             | f            | t          | f      <- 运行角色（NOLOGIN）
blockchain_api | f        | f           | f             | f            | f          | t      <- 应用连接账号

# 关键：RESET ROLE 之后不再有任何特权
psql -U blockchain_api -c "select rolsuper from pg_roles where rolname=current_user"        -> f
psql -U blockchain_api -c "select has_table_privilege('audit_events_v2','SELECT')"          -> f
# SET ROLE blockchain_app 之后恢复业务权限，但审计表仍不可改写
SET ROLE blockchain_app; select has_table_privilege('audit_events_v2','UPDATE')             -> f
                         select has_table_privilege('audit_events_v2','SELECT')             -> t

# 运行期实连账号（pg_stat_activity）
blockchain_api | 6 connections      （修复前为 blockchain / 超级用户）
```

服务与业务回归：`/health/ready` 四项依赖 ok；审计链校验 `valid=true, integrity_status=sealed_incident,
checked=1634`（Sprint 6 完整性边界保持）；登录事件持续写入（`login_events` 正常增长）。

结论：**R-07 关闭**（应用连接账号非超级用户，`RESET ROLE` 不再提权，审计保护不变）。

## 3. 修复期间的 P0（F-01）与守门
`tests/run-sprint7-sec-negative.sh` 已由安全套件补充 `AUTH-013e/f` 守门用例：
HTTP 入口下"带正确 CSRF token 的写请求必须 200"、以及会话 Cookie 名在两种入口下的正确形态。
最终回归以**冻结构建**重跑（见 `S7-SEC-01-final-regression.md`）。

## 4. 仍未关闭的项（不在本次修复范围）
| ID | 状态 | 说明 |
|---|---|---|
| R-09 | 未关闭 | 依赖 P0=2/P1=5（含前端 `xlsx@0.18.5` 在 npm 无修复版本）—— 见 `dependency-scan/SUMMARY.md` |
| R-10 | 未关闭 | 基础镜像 CRITICAL 20 / HIGH 203 —— 见 `image-iac/SUMMARY.md` |
| R-11 | 未关闭 | IaC 配置面 **NOT_VERIFIED**：trivy/checkov 结构性不覆盖 compose/systemd/nginx |
| DEF-01 | **已修复** | `POST /api/v1/admin/action-api-keys` 省略 `allowed_ips` 由 500 改为正常创建（空数组=不限来源） |
| F-04 | 已核查 | `display_name` 未发现 XSS 残留行（复核 `where display_name like '%<img%' or like '%onerror%'` = 0 行） |
