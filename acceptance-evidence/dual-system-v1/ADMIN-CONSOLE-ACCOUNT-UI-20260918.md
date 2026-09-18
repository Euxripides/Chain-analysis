# 后台管理台「用户与成员 + 账号安全」改造与验证（2026-09-18）

> 依据：`docs/multiuser-login-and-isolation-plan.md` §3.2（账号生命周期，D5）、§3.6（前端改动）、§3.7（管理员 MFA，D7）、§4.3（P1-02）。
> 本次只改**后台管理台**（`frontend/apps/admin-console`），用户端不动。

## 1. 交付内容

| 位置 | 变更 |
|---|---|
| `apps/admin-console/src/pages/UsersPage.tsx` | ① **新建用户**（原占位的「邀请用户（S4）」按钮替换为可用流程）：表单（登录邮箱 / 显示名 / 附加角色键 / 原因）→ 提交 → **一次性初始密码只在结果弹窗出现一次**（含复制按钮）。② **删除（离职）**：详情弹窗新增入口 → 影响预览 → 执行（原因必填 + 影响勾选）→ 结果弹窗；不可用时把**原因写进弹窗**（无权限 / 目标是本人 / 目标已删除） |
| `apps/admin-console/src/pages/SecurityPage.tsx`（新） | **账号安全**自助页：认证器 TOTP 注册（本地内联 QR + 密钥 + 验证码确认 + **一次性 10 枚恢复码**）、关闭 MFA、修改密码、**我的会话**（逐条撤销 / 撤销其他会话）、**我的设备**（信任 30 天 / 取消信任） |
| `apps/admin-console/src/App.tsx` | 新增路由 `/account/security`（权限 `security:update`）与导航分组「我的账号 → 账号安全」 |
| `backend/migrations/postgres/0071_self_service_for_all_roles.sql`（新，已应用） | 把 self 类权限（`security:update` 等 5 个）授予**全部**系统角色：此前 `platform_admin`/`security_admin`/`auditor` 不持有任何 self 权限，新页面对它们会被 fail-closed 藏起来 —— 而它们恰恰是最需要绑验证器的角色 |

契约遵从：所有提示（成功/失败/风险/确认）一律弹窗，页面正文只留数据与操作控件（`AGENTS.md`、`docs/code-standards.md`、AD-009）。

## 2. 浏览器真机验证（Playwright 1.62.1 + Chromium，经公网入口 `127.0.0.1:8088/admin/`）

脚本：`tests/verify-admin-console-ui.mjs`（20 项）、`tests/verify-admin-mfa-enroll.mjs`（7 项）。
结果：**PASS=20 / FAIL=0** 与 **PASS=7 / FAIL=0**。

| 组 | 断言 |
|---|---|
| 登录与导航 | 管理员登录落到 `/admin/overview`；导航出现「账号安全」与「用户与成员」 |
| 账号安全页 | MFA 卡片 + 「启用 MFA」按钮、密码卡片、我的会话（含当前会话标记）、我的设备均渲染；页头注明"本页只影响你自己的账号" |
| 新建用户 | 弹窗四个字段齐全 → 提交 → 弹出**一次性初始密码**（含"关闭后无法再次查看"说明与复制按钮）→ 列表出现新用户 |
| 删除（离职） | 详情弹窗出现入口 → 影响预览列出"归档 / 匿名化 / 保留身份行" → 执行弹窗（原因 + 影响勾选）→ 结果弹窗给出匿名化邮箱与已归档个人空间 |
| **管理员 TOTP 注册（D7）** | 点「启用 MFA」→ 二维码为**本地内联 SVG**、密钥为 32 位 Base32 → 用**测试端现算的 RFC 6238 验证码**（SHA1/6 位/30 秒）确认 → 服务端接受 → 一次性下发 **10 枚恢复码** → 页面状态变为已启用 |
| 注册确实生效 | 该账号**重新登录返回 `202 state=mfa_required`，methods=[totp, recovery_code]** —— 第二步被真正启用，不只是 UI 状态 |

落库复核（UI 建号 → UI 删除）：

```text
identities: status=deleted, deleted_at 非空, email=deleted+<uuid>@invalid, display_name=已删除用户
workspaces: slug=p-<id 前 12 位>, kind=personal, archived_at 非空, owner_identity_id 非空
login_events: 存在 reason_code=account_deleted 的记录
```

## 3. 真机验证抓到的一个真实缺陷（仅靠构建成功发现不了）

**Dialog 的字段默认必填**：`packages/ui/Dialog.tsx` 的判定是 `field.required !== false` —— 也就是**不写 `required` 就等于必填**。我最初把「显示名称」「附加角色键」当作选填（只写了 `pattern`），结果两个字段留空时**提交按钮永远 disabled**，管理员根本建不了号；`tsc` 与 `vite build` 全绿，只有真机点击才暴露。已改为显式 `required: false` 并在代码里留下注释。

> 这正是 `docs/code-standards.md`「前端修改不能只以构建成功作为通过标准」的价值所在。

## 4. 已知边界

- 会话列表展示的是**该身份的全部会话**（后端按身份返回），"当前会话"以 `session_id` 比对高亮；撤销当前会话会立即登出并跳转登录页。
- 设备信任只提供 30 天 / 取消两种，未做自定义天数（后端支持 `trust_days`）。
- 后台会话是 admin 受众；本页的自助接口（`/api/auth/v1/mfa/*`、`/sessions`、`/devices`）本身只要求已确认会话，不需要 `admin:*`。
- 验证结束后夹具身份已全部停用（`active=0`），本轮创建的 `s8ui-*` 测试账号已按设计处于软删状态。
