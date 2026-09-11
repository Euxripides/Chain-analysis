# AD-008：灰度路径 `/app-v2` 与 `/admin`

- 状态：Accepted
- 日期：2026-09-12
- 关联：§29.3、§28.9（S7-RELEASE-01）

## 决策

1. 用户层系统挂载在 `/app-v2/`，后台系统挂载在 `/admin/`，旧 SPA 继续占用 `/`。
   这样可以在不切断现有用户的前提下观察新系统的权限差异与审计覆盖。
2. 每个应用用 Vite `base` 固定前缀，react-router `basename` 与之对应；
   登录响应中的 `next` 是**应用内相对路径**（`/home`、`/overview`），前缀由前端 basename 负责拼接。
   服务端同时在 `sanitizeReturnToDefault` 中剥离 `/admin`、`/app-v2` 前缀，避免双重拼接。
3. 旧 SPA 与新用户层共用同一个 `cs_user_session` Cookie（`Path=/`），因此登录一次后旧 SPA 也带上真实身份。

## 影响

- 灰度期需要同时维护三套构建产物；`/` 的下线需要单独发布步骤与 ADR。
- 灰度期间发现的真实缺陷：Nginx `sites-enabled/blockchain-platform.conf` 是普通文件而不是软链接，
  只更新 `sites-available` 不会生效，导致 `/app-v2/` 被旧 SPA 的 SPA fallback 吞掉。
  部署脚本已改为同时写入两处。
