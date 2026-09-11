# AD-002：身份只能由服务端会话推导

- 状态：Accepted
- 日期：2026-09-12
- 关联：§6.3、§6.6.4、§28.4、§32 约束 8

## 背景

历史实现 `internal/auth/auth.go` 允许客户端用 `X-User-Id` / `X-User-Email` /
`X-Org-Id` / `X-User-Role` 头自报身份，未携带时回退为默认 `admin@local`/owner。
这意味着任何能访问 API 的人都等价于平台所有者——在公网部署下是致命的。

## 决策

1. 引入 `internal/iam`：本地凭据（Argon2id）+ 服务端会话（不透明随机令牌，DB 只存 SHA-256）+ HttpOnly Cookie。
2. 受众分离：用户层会话 `cs_user_session` 与后台会话 `cs_admin_session`，`sessions.audience` 参与每次校验。
   跨受众访问一律拒绝，业务与后台 API **不做**跨受众回退。
3. `AUTH_MODE` 三档：`session`（生产唯一允许）/ `hybrid` / `legacy`。
   `APP_ENV=production` 时若配置为 hybrid/legacy，启动会把模式强制降级为 `session` 并留下 ERROR 日志（fail closed，不崩溃）。
4. 客户端可用 `X-Auth-Audience` **收窄**服务端接受的受众；该头部永远不能放宽授权。
5. 写操作使用 double-submit CSRF：可读 Cookie + `X-CSRF-Token` 头 + 会话内哈希三方一致。
6. 转发头（`X-Forwarded-For` / `CF-Connecting-IP`）只在请求来自 `AUTH_TRUSTED_PROXIES` 时才采信；
   不可信来源的转发头一律丢弃，且 `peer_ip` 与 `client_ip` 分别入库。

## 被否决的方案

- **保留兼容头默认开启**：会让"生产不接受自报身份"的验收无法成立。
- **JWT 存 localStorage**：不满足"前端无 JS 可读 Token"。
- **直接上 OIDC**：当前环境没有 IdP；接口位置已预留（`/api/auth/v1/oidc/*` 返回 501，而不是假装可用）。

## 影响

- 旧 SPA 的 `/api/v1` 调用不再自动获得 owner 身份；未持会话时是匿名。
  用户登录一次（`cs_user_session` 对全站 `Path=/` 生效）后旧 SPA 也带上身份。
- 需要一次性初始化流程给 Owner 设置密码：`go run ./cmd/admin-init`（不写默认密码）。

## 回滚

把 `AUTH_MODE` 改回 `hybrid` 并重启 API 即可恢复兼容头（仅限非生产环境）。
