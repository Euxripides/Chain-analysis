# 老控制台与新线解耦（LEGACY-CONSOLE-01）

## 为什么要做

老控制台（`/` 上的那套前端，局域网入口 `192.168.31.152:3000`）**自身没有登录页**：它的代码里只有
"去 `/admin/login` 登录"的对话框，会话必须在**同一个 host** 上由新系统签发。2026-09-13
S7-SEC-LEGACY-V1 之后 `/api/v1` 的读路由一律要求已认证，于是：

* 在 `aurore.online`（nginx 8088）上：`/` 老前端 + `/admin/` 新后台同源，登录后老前端有数据；
* 在局域网 IP / 任何别的 host 上：没有登录入口，也没有会话 Cookie（host-only），全部 401 → 看不到数据。

两条线因此耦合：老线能不能用，取决于新线的登录入口与会话语义。

## 解耦方式

入口层给老线注入一个**服务凭据**，后端把它解析成一个受限主体；老前端代码不改，新线
（会话 / 工作区 / CSRF / 提交门）也不改。

* 凭据由**入口层**设置（nginx 或 vite 代理），只对**没有会话 Cookie**的请求注入；浏览器永远拿不到它。
* 后端按 SHA-256 常量时间比对；不符/缺失 = 未认证 → 照旧 401（**没有**重新打开被审计判 FAIL 的匿名读）。
* 主体不是 `System`、不是 admin 会话：它只拥有 `LEGACY_CONSOLE_PERMISSIONS` 里列出的权限键，
  仍由原有 policy 引擎逐条判定（`admin:*` 需要 `LEGACY_CONSOLE_AUDIENCE=admin`）。
* 它没有 session 行，因此 CSRF、会话工作区陈旧检查、P1-A 提交门都不适用于它——那些是会话语义。
* **真实会话永远优先**：identityMiddleware 先尝试全部会话候选，只有都失败才回退到这个主体。

## 配置

| 变量 | 含义 |
| --- | --- |
| `LEGACY_CONSOLE_TOKEN_SHA256` | 凭据的 SHA-256（64 位十六进制）。为空 = 整条路径关闭 |
| `LEGACY_CONSOLE_PERMISSIONS` | 逗号分隔的权限键（必须在 policy catalog 里已声明） |
| `LEGACY_CONSOLE_ORGANIZATION_ID` | 老线所属组织 |
| `LEGACY_CONSOLE_WORKSPACE_ID` | 老线所属工作区 |
| `LEGACY_CONSOLE_IDENTITY_ID` | 审计主体：老线的每次请求都以这个真实 identities.id 写审计 |
| `LEGACY_CONSOLE_AUDIENCE` | `user`（默认）或 `admin`；配了 `admin:*` 却写 user 会在启动时关闭整条路径 |

任何一项缺失/非法 → 该主体**关闭**（fail closed），只可能减少访问，不可能放大。

## 入口层接线（未部署，需人工执行）

### 1) nginx（公网入口 `127.0.0.1:8088`）

凭据写进一个 0600 的 include，再按"没有会话 Cookie 才注入"的 map 使用：

```nginx
# /etc/nginx/legacy-console.secret.conf   (chmod 600, 由部署脚本生成，不进 git)
set $legacy_console_token "<64-byte-random-token>";

# http 段：只有"两个会话 Cookie 都不存在"时才注入
map "$cookie_cs_user_session$cookie_cs_admin_session" $legacy_console_inject {
    ""      "1";
    default "0";
}
map "$legacy_console_inject:$legacy_console_token" $legacy_console_value {
    "1:"    "";
    "~^1:(.+)$" "$1";
    default "";
}
```

在 `location ^~ /api/` 里加一行（`proxy_set_header` 会**替换**客户端自带的同名头，因此伪造无效）：

```nginx
proxy_set_header X-Legacy-Console-Token $legacy_console_value;
```

### 2) vite（局域网入口 `192.168.31.152:3000`）

在 `frontend/vite.config.*` 的 `server.proxy['/api']` 上，用 `configure` 钩子注入同一只读凭据：
只在请求没有 `cs_user_session` / `cs_admin_session` Cookie 时添加 `X-Legacy-Console-Token` 头，
并**先删除**客户端传入的同名头。凭据来自服务端环境变量，不写入前端产物。

> 部署时必须同时做：给老线**创建一个专用审计身份**（例如 `legacy-console@local`，无密码、
> 只作为 audit actor 与该工作区成员），并把它写进 `LEGACY_CONSOLE_IDENTITY_ID`，不要长期借用
> 真人账号（例如 `admin@local`）作为审计主体。

## 权限清单（建议起点，可按老前端实际页面裁剪）

```
investigation:list,investigation:read,investigation:create,investigation:run,investigation:update,
report:list,report:create,subject:list,subject:create,
download:create,job:list:workspace,job:read,job:control,
export:create,export:read,export:download,
case:list,case:read,case:create,case:update,canvas:list,canvas:create,
admin:provider:read,admin:cost:read,admin:performance:read,admin:performance:update,
admin:dataset:read,admin:dataset:manage
```

`case:delete`、`tag` 系列、`admin:*` 中未列出的键**不在**清单里：老前端如果确实需要，再逐条加，
每加一条都是一次显式的授权决定。

## 验证

`scripts/s7/p4-legacy-console-decoupling-probe-v1.sh`（候选二进制 + 独立端口）：
带凭据 → `/api/v1/cases`、`/api/v1/download-jobs`、`/api/v1/investigations`、
`/api/v1/providers/ankr/status` 全部 200 并返回真实行；
无凭据/错凭据 → 401；控制面 `/api/admin/v1/jobs` → 403（已认证但无权）。
