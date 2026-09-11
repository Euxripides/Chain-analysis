# AD-010：前端共享包先走 Vite alias，而非 pnpm workspace 链接

- 状态：Accepted（临时）
- 日期：2026-09-12
- 关联：§6.1、§28.3（S1-FE-01）

## 背景

计划目标是 `frontend/apps/*` + `frontend/packages/*` 的 pnpm workspace。
但当前 `frontend/node_modules` 是 pnpm 布局，本机与部署机的可用包管理器不一致，
一次性切换 workspace 链接会同时影响旧 SPA 的构建，风险大于收益。

## 决策

1. 目录结构按目标落地（`apps/`、`packages/` 各自有 `package.json` 与 `tsconfig.json`）。
2. 应用通过 Vite `resolve.alias` + tsconfig `paths` 解析 `@chain/*`，依赖仍从 `frontend/node_modules` 解析。
   alias 必须把**具体文件**（`@chain/ui/ui.css`）放在包入口之前，否则 `index.ts` 会被当目录前缀拼接。
3. `pnpm-workspace.yaml` 保留，供后续正式切换到 workspace 链接。

## 影响

- 共享包不会被独立发布或版本化，改动即刻影响两个应用（当前可接受）。
- 迁移到真正 workspace 时必须验证：共享包无反向依赖（不得 import apps）、两个应用可独立构建。

## 后续

纳入 S1-FE-01 收尾项；切换前需先在 CI 中同时构建两个应用与旧 SPA。
