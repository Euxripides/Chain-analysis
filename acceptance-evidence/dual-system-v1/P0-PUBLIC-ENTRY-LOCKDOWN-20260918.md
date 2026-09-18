# P0 公网入口收敛 —— 部署与验证证据（2026-09-18）

> 依据：`docs/multiuser-login-and-isolation-plan.md` §3.1 R1/R2/R3、决策 D3；需求方 2026-09-18 批准执行。
> 环境：`192.168.31.152`（dataserver），nginx `127.0.0.1:8088`（cloudflared 隧道入口）、api `127.0.0.1:8080`、老控制台 vite `:3000`、用户层 `:3001`、后台 `:3002`。

## 1. 变更清单

| # | 变更 | 目标 | 备份 / 回滚 |
|---|---|---|---|
| 1 | 生产库应用迁移 `0066`/`0067`/`0068` | 个人空间、软删、`basic_user` 角色与 `admin:user:delete` | 迁移前全库备份 `/home/euripides/backups/blockchain_platform-pre-0066-20260918.dump`（127 MB，附 `.sha256`） |
| 2 | nginx：`location = /` 与 `location /` 改 302 → `/app-v2/`；删除公网 `X-Legacy-Console-Token` 注入行 | 公网不再暴露老控制台、不再注入共享凭据 | `/home/euripides/backups/nginx-blockchain-platform.conf.bak-20260918-legacy-public` |
| 3 | 停用 `/etc/nginx/conf.d/legacy-console.conf`（改名为 `.disabled-20260918`） | 共享凭据 map 在公网入口彻底不存在 | 同名 `.disabled-20260918` 文件即原文件 |
| 4 | 老控制台 vite 由 `--host 0.0.0.0` 改为 `--host 127.0.0.1` | 该实例只留内网回环（决策 D3） | `/home/euripides/backups/blockchain-vite.service.bak-20260918` |
| 5 | `deploy/.env` 注释掉 `LEGACY_CONSOLE_TOKEN_SHA256` 并重启 api | 服务凭据整条路径 **fail closed** | `/home/euripides/backups/deploy.env.bak-20260918`；原值另存 `legacy-console-env.restore-20260918`（0600） |

## 2. 迁移结果（应用后实测）

```text
schema_migrations：postgres/0066_workspace_kind.sql / 0067_identity_deletion.sql / 0068_basic_user_role.sql 均已登记
workspaces.kind 列存在；现有 20 个工作区全部 kind='shared'（既有语义未变）
identities.deleted_at 列存在
roles.basic_user：73 个权限，其中 admin:* = 0
admin:user:delete 授予：platform_admin, security_admin
```

## 3. 验证矩阵（部署后实测）

| 探测 | 变更前 | 变更后 | 结论 |
|---|---|---|---|
| `GET /`（公网，Host: aurore.online） | 200 老控制台 | **302 → /app-v2/** | 公网不再有老控制台 |
| `GET /some/deep/link` | 200 老控制台 | **302 → /app-v2/** | 无第二条可达业务数据的页面 |
| `GET /api/v1/cases`（带老控制台 Referer） | **200 + 真实案件数据** | **401** | 共享凭据通道关闭 |
| `GET /api/v1/cases`（后端直连，带真实 token） | 200 | **401** | 凭据本身已失效（fail closed） |
| `GET /api/v1/cases`（局域网回环实例注入路径） | 200 | **401** | 回环实例也不再放行匿名读 |
| `GET /app-v2/login` | 200 | 200 | 用户层登录页正常 |
| `GET /admin/login` | 200 | 200 | 后台登录页正常 |
| `GET /api/auth/v1/methods` | 200 | 200 | 认证接口正常 |
| `POST /api/auth/v1/login`（错误凭据） | — | 401 统一错误（非 5xx） | 登录接口正常 |
| `GET /api/v2/cases` / `/api/admin/v1/users`（未登录） | — | 401 / 401 | 用户层与后台均要求会话 |
| `/health/live` / `/health/ready` | 200 | 200 | api 健康 |
| `ss -lntp` | `0.0.0.0:3000` | **`127.0.0.1:3000`** | 老控制台仅回环可达 |

## 4. 过程中的两次纠偏

1. **备份文件不能放在 `sites-enabled/`**：第一次 `cp` 出的 `.bak` 与主配置同目录，nginx 会一并加载 → `duplicate "log_format" name "blockchain_proxy"`，`nginx -t` 失败、**未 reload**（旧配置继续生效，零影响）。备份移到 `/home/euripides/backups/` 后 `nginx -t` 通过并 reload。
2. **api 重启有约 30 秒初始化窗口**：重启后立刻探测得到 `000`/`502`，是进程仍在初始化 Provider 与方法签名种子；等待后 `/health/live`、`/health/ready` 均 200。**这 30 秒内公网 API 不可用**，后续滚动重启应放在低峰并预告。

## 5. 尚未部署（明确边界）

- **api 二进制仍是 2026-09-17 的构建**：本次上线的代码改动（`DELETE /api/admin/v1/users/{id}`、建用户即建个人空间、登录拒绝已删除账号）**尚未进入运行中的二进制**。迁移已先行（纯增量，旧二进制兼容）。
- 计划中的 P1/P2 其余项（后台 UI、加入/移出工作区、归档只读拦截、隔离回归、共享面治理）尚未实现或未部署。
