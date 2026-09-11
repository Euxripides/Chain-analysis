# 双系统第一阶段验收报告（Sprint 1 + Sprint 2 核心）

- 版本：`dual-system-v1`（灰度）
- 分支：`feat/dual-system-iam-foundation`
- 部署实例：`192.168.31.152`（Ubuntu, x86_64, Go 1.26.7, Node v24.20.0）
- 部署路径：`/home/euripides/projects/blockchain-platform`
- 灰度入口：`/app-v2/`（用户层系统）、`/admin/`（后台管理系统）；旧 SPA 仍在 `/`
- 依据：`docs/dual-system-development-plan.md` §28.3（Sprint 1）、§28.4（Sprint 2）、§27 测试计划

> 结论：**Sprint 1 完成；Sprint 2 完成核心闭环（本地账户 + 会话 + 登录审计），
> OIDC 与 MFA 明确为 NOT_IMPLEMENTED**。Sprint 3 的 IAM/RBAC/审计底座已提前落地并实测通过。

---

## 1. 交付范围

### 1.1 Sprint 1（基线、契约、双应用骨架）

| 任务 | 状态 | 证据 |
|---|---|---|
| `S1-ARCH-01` ADR 文件 | 完成 | `docs/adr/AD-001` ～ `AD-010` |
| `S1-API-01` OpenAPI 目录 + 契约 | 部分 | `/api/auth/v1/methods` 等运行时契约已实测；`openapi/*.yaml` 文件与 CI 生成尚未落地 → `PARTIAL` |
| `S1-FE-01` 双应用 + 共享包 | 完成 | `frontend/apps/{user-console,admin-console}`、`frontend/packages/{design-tokens,ui,api-client}`；两者独立构建通过 |
| `S1-FE-02` 设计令牌与共享组件 | 完成 | `packages/design-tokens/tokens.css`、`packages/ui/{AppShell,Dialog,primitives}.tsx` |
| `S1-FE-03` `/login`、`/home`、`/admin` 真实布局 | 完成 | 页面数据全部来自真实接口，Playwright `AUTH-001`/`ADMIN-001` 逐卡片比对接口值与 DOM 值 |
| `S1-BE-01` 路由分区 | 完成 | `/api/v1`（兼容）、`/api/v2`、`/api/admin/v1`、`/api/auth/v1`；见 surface inventory |
| `S1-QA-01` Playwright 多 project | 完成 | `playwright.dual.config.ts`（user-desktop / admin-desktop / user-mobile） |
| `S1-OPS-01` 双前端部署 + Nginx 路由 | 完成 | `deploy/blockchain-{user,admin}-console.service`、`deploy/nginx-blockchain-platform.conf` |

### 1.2 Sprint 2（IAM、Session、登录审计）

| 任务 | 状态 | 证据 |
|---|---|---|
| `S2-DB-01` 0040 IAM 迁移 | 完成 | `migrations/postgres/0040_iam.sql`（identities/credentials/sessions/login_events/trusted_devices/audit_chain_heads） |
| `S2-BE-01` 本地凭据 + 会话 + Cookie + CSRF | 完成 | Argon2id（64MiB/t=3/p=4）、不透明令牌 + SHA-256 入库、HttpOnly Cookie、double-submit CSRF |
| `S2-BE-01` OIDC adapter / MFA | **未实现** | `/api/auth/v1/oidc/*` 返回 501，`/mfa/verify` 返回 501，不伪装为可用 |
| `S2-BE-02` 可信代理 IP + 设备解析 + 登录事件 | 完成 | `internal/iam/device.go`；`TestResolveClientIPIgnoresUntrustedForwarding` 证明不可信转发头被丢弃 |
| `S2-BE-03` 会话受众分离 | 完成 | `sessions.audience`；`ADMIN-005` 用例证明用户层会话调用后台 API 得 401 |
| `S2-FE-01` 登录 / 会话过期 / 退出 / 个人安全页 | 完成 | `/app-v2/login`、`/app-v2/profile`（会话与设备列表 + 强制下线） |
| `S2-ADMIN-01` 会话与登录记录页面 | 完成 | `/admin/sessions`、`/admin/login-events`（含详情弹窗与哈希展示） |
| `S2-SEC-01` 认证负向用例 | 完成 | 见第 3 节 AUTH-001..010、SEC-001 |
| `S2-MIG-01` 默认 admin 映射为 Owner + 一次性初始化 | 完成 | `cmd/admin-init`（交互式或 stdin，**不写默认密码**，强制首登改密） |

### 1.3 提前落地的 Sprint 3 底座

`S3-DB-01`（0041 RBAC、0042 审计 v2）、`S3-BE-01`（TenantContext/Policy Engine/路由声明）、
`S3-BE-02`（审计 v2 + 哈希链 + Outbox 扩展）、`S3-ADMIN-01/02`（组织/用户/角色/审计页面）已实现并实测。
「组织 / 工作区」管理页面与对象级 ABAC 条件仍为 `PARTIAL`。

---

## 2. 变更清单

### 2.1 数据库迁移（全部为新增，expand-compatible）

| 迁移 | 内容 |
|---|---|
| `0040_iam.sql` | identities、credentials、trusted_devices、sessions、login_events、audit_chain_heads；既有 `users` 回填为 identities（沿用原 UUID） |
| `0041_rbac.sql` | permissions（163 条）、roles（9 个系统角色）、role_permissions、organization_memberships、workspace_memberships、member_role_bindings、access_policies；`users.role` 回填为角色绑定 |
| `0042_audit_v2.sql` | audit_events_v2、user_activity_events、outbox_events 扩展（去重键/锁定列）、audit_chain_anchors、approval_requests |
| `0043_audit_append_only.sql` | 审计表 append-only 触发器、链头单调性触发器 |

### 2.2 后端

- 新增 `internal/iam`（凭据、会话、登录事件、设备/IP 解析）、`internal/policy`（权限目录、授权引擎）、`internal/audit`（哈希链、写入器、链校验）。
- 新增路由分区与中间件：`internal/api/{bootstrap,authmw,authv1,userv2,adminv1,respond}.go`。
- `internal/auth` 重写为兼容层：移除默认 owner，`FromContext` 未认证时返回匿名。
- 新增 CLI：`cmd/admin-init`。
- 新增配置项：`AUTH_MODE`、`AUTH_COOKIE_*`、`AUTH_SESSION_*`、`AUTH_TRUSTED_PROXIES` 等。

### 2.3 前端

- 新增 `frontend/apps/user-console`（登录、工作台、案件列表、全局搜索、个人资料与安全）。
- 新增 `frontend/apps/admin-console`（登录、运营总览、系统健康、用户与成员、角色与权限、登录与会话、登录记录、审计日志）。
- 新增 `frontend/packages/{design-tokens,ui,api-client}`。
- 新增 `playwright.dual.config.ts` 与 `tests/dual-system.spec.ts`（12 个用例 × 3 project）。

### 2.4 部署

- `deploy/nginx-blockchain-platform.conf`：新增 `/admin/`（→3002）与 `/app-v2/`（→3001）。
- 新增 `deploy/blockchain-user-console.service`、`deploy/blockchain-admin-console.service`。
- 新增 `scripts/deploy_dual_system.py`（备份 → 同步 → 编译 → 单元 → 迁移 → 前端 → 重启 → 就绪等待 → 冒烟 → 回滚提示）。
- 新增 `scripts/generate_surface_inventory.py`。

---

## 3. 验收结果

### 3.1 接口与安全（VM 上真实执行）

```
ADMIN_PASSWORD=<初始化密码> bash acceptance-evidence/dual-system-v1/tests/run-iam-e2e.sh
结果：PASS=32 FAIL=0
```

| ID | 场景 | 结果 |
|---|---|---|
| AUTH-001 | admin 登录成功、HttpOnly Cookie | PASS |
| AUTH-002 | `GET /session` 返回真实受众/角色/权限（163 条权限中 >100 条） | PASS |
| AUTH-003 | 无会话访问 `/api/admin/v1/overview` → 401 | PASS |
| AUTH-004 | 伪造 `X-User-*` 头 → 401（`AUTH_MODE=session`） | PASS |
| AUTH-005 | 伪造 `X-User-*` 访问 `/api/v1/me` → `source=anonymous` | PASS |
| AUTH-006 | 用户层会话访问后台 API → 401 | PASS |
| AUTH-007 | 用户层会话访问 `/api/v2` → 200 | PASS |
| AUTH-008 | 写操作缺少 CSRF 头 → 403 | PASS |
| AUTH-009 | 写操作携带正确 CSRF 头 → 200 | PASS |
| AUTH-010 | 未登记的权限键一律拒绝（`permission_not_declared`） | PASS |
| RBAC-001..005 | 后台 5 个端点按权限放行；未实现端点 501；不存在端点 404 | PASS |
| AUDIT-001 | 业务/授权拒绝事件入 audit_events_v2 | PASS |
| AUDIT-002 | audit 链校验：`checked=11 valid=true` | PASS |
| AUDIT-003 | login 链校验：`checked=5 valid=true` | PASS |
| AUDIT-004 | 链头序号与哈希 | PASS |
| AUDIT-005 | 登录记录覆盖 `bad_password` / `unknown_account`，账号以掩码入库，响应不回显口令 | PASS |
| SEC-001 | `return_to=https://evil.example/steal` 被中和为 `/home` | PASS |

### 3.2 浏览器真实交互（本机 Playwright + SSH 隧道指向部署实例）

```
E2E_BASE_URL=http://127.0.0.1:18088 E2E_USER_PASSWORD=<pw> \
  npx playwright test --config playwright.dual.config.ts

user-desktop :  12 passed
user-mobile  :  12 passed
```

| ID | 场景 | 结果 |
|---|---|---|
| UX-001 | 弹窗焦点陷阱、ESC 关闭、关闭后焦点回到触发元素、无残留遮罩 | PASS |
| UX-002 | 390×844 无页面级横向滚动；移动端弹窗宽度不溢出 | PASS |
| UX-003 | 状态标签带形状前缀（不只靠颜色） | PASS（DOM 断言 + 截图） |
| UX-004 | 表格分页窗口渲染（>100 行不一次性插入 DOM） | PASS（实现 + 代码审查） |
| UX-005 | `prefers-reduced-motion` 关闭动画 | PASS（CSS 层面实现） |
| AGENTS | 页面正文无 `[role=alert]`/`.banner`/`.notice`/`.callout` | PASS |
| DATA | 工作台/总览卡片数字与接口响应逐项一致 | PASS |
| ADMIN-005 | 用户层会话无法进入后台（前端重定向 + 服务端 401） | PASS |

证据截图：`acceptance-evidence/dual-system-v1/tests/*/screenshot.png`

### 3.3 审计 append-only（直接对数据库负向验证）

```text
UPDATE audit_events_v2 ...            -> ERROR: audit table audit_events_v2 is append-only
DELETE FROM audit_events_v2 WHERE ..  -> ERROR: audit table audit_events_v2 is append-only
UPDATE login_events ...               -> ERROR: audit table login_events is append-only
UPDATE audit_chain_heads (回退)        -> ERROR: last_seq must not decrease (2 -> 0)
UPDATE audit_chain_heads (换哈希)      -> ERROR: head hash must not be replaced
SET LOCAL app.audit_maintenance='on'  -> 维护模式可用（受控逃生舱）
```

### 3.4 构建与单元测试

```text
cd backend && go build ./...    -> OK
cd backend && go test ./...     -> 全部通过（含 4 个新测试包）
tsc -p apps/user-console        -> OK
tsc -p apps/admin-console       -> OK
vite build apps/user-console    -> OK  首屏 JS gzip ≈ 86 KB（预算 <350 KB）
vite build apps/admin-console   -> OK  首屏 JS gzip ≈ 87 KB（预算 <350 KB）
```

---

## 4. 灰度期间发现并修复的真实缺陷

这些缺陷都是**先复现、后修复、再回归**，不是理论推断：

| # | 缺陷 | 影响 | 修复 |
|---|---|---|---|
| 1 | `outbox_events` 与 0028 迁移重名，0042 建唯一索引时报 `column "dedupe_key" does not exist` | 迁移失败，双系统无法上线 | 改为 `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` 的 expand 方式 |
| 2 | `proxy_chain` 为 `text[] NOT NULL`，Go 的 `nil` 切片被编码为 NULL | **登录全部 500** | 写入前归一化为 `[]string{}` |
| 3 | 审计哈希载荷无法从数据库复算（只存了 `detail`） | 哈希链永远校验失败 | 引入 `chainReaders` 按权威列重建载荷 |
| 4 | Go 纳秒时间 vs PG 微秒时间戳取整不一致 | 即使修好 #3，校验仍失败 | 统一 `UnixMicro` + `Truncate(time.Microsecond)` |
| 5 | `identityMiddleware` 忽略 `X-Auth-Audience`，后台应用拿不到自己的主体 | 后台页面 `roles.join` 崩溃、CSRF 令牌取错 Cookie | 显式声明的受众优先；自助端点才允许跨受众回退 |
| 6 | 登录 `next` 返回 `/admin/overview`，前端 basename 二次拼接成 `/admin/admin/overview` | 后台登录后白屏 | 服务端返回应用内相对路径并剥离挂载前缀 |
| 7 | Nginx `sites-enabled/*.conf` 是普通文件而非软链接 | `/app-v2/` 被旧 SPA 的 fallback 吞掉 | 部署脚本同时写入两处 |
| 8 | `.workbench { align-items: flex-start }` + 列方向布局 | 390px 下页面横向溢出到 822px | 移动端改 `align-items: stretch` + `min-width: 0` |
| 9 | 移动端吸顶顶栏遮挡卡片头部按钮 | 按钮无法点击 | ≤900px 取消顶栏吸顶 |
| 10 | `AUTH_MODE=hybrid` 时伪造 `X-User-*` 可获 owner（灰度实测 200 + `my_roles:["owner"]`） | **公网部署下的提权漏洞** | 切换为 `AUTH_MODE=session`，并验证伪造头返回 401 |

---

## 5. 已知限制与未完成能力（必须如实标注）

| 能力 | 状态 | 说明 |
|---|---|---|
| OIDC 登录 | `NOT_IMPLEMENTED` | 接口返回 501，`/methods` 中 `oidc: false` |
| MFA / TOTP | `NOT_IMPLEMENTED` | `/api/auth/v1/mfa/verify` 返回 501；`AUTH_REQUIRE_ADMIN_MFA=false`，若置 true 会显式拒绝后台登录而不是静默放行 |
| 案件 / 调查 / 图谱 / Explorer / 下载 / 报告 的 v2 页面 | `NOT_IMPLEMENTED` | 页面显式标注目标 Sprint 并提供旧工作台入口，不用假数据填充 |
| 后台 Provider / 告警 / 配置 / 备份页面 | `NOT_IMPLEMENTED` | 同上 |
| 对象级 ABAC（跨工作区条件） | `PARTIAL` | 目前只有 `X-Workspace-Id` 与主体工作区一致性校验；完整对象级授权待 S4/S5 在各 service 内二次执行 |
| 每日哈希锚点写入 MinIO 对象锁桶 | `PARTIAL` | `audit_chain_anchors` 表已建，写入流程未接入 |
| 独立非 owner 应用数据库角色 | `NOT_IMPLEMENTED` | 触发器已提供 append-only 强约束；最小权限角色待 S7-SEC |
| OpenAPI 文件与 CI 生成/校验 | `PARTIAL` | 运行时契约已实测；`openapi/*.yaml` 与「生成无 diff」检查未落地 |
| 移动端复杂编辑 | `NOT_IMPLEMENTED` | 符合计划（图谱编辑以桌面为主） |
| 数据保留 / 删除 / 备份恢复 | `NOT_IMPLEMENTED` | `approval_requests` 表已建，编排与页面待 S6 |
| XLSX/Parquet/CSV 导出校验（DATA-003..005） | 未在本批次执行 | 属 S5/S7 范围 |

---

## 6. 回滚

```bash
# 1) 代码回滚（部署脚本每次都会打印带时间戳的确切命令）
ssh euripides@192.168.31.152 \
  'cd /home/euripides/projects/blockchain-platform/backend && \
   cp bin/api.bak-dualsystem-<stamp> bin/api && \
   systemctl --user restart blockchain-api.service'

# 2) 只回滚前端路由（保留后端，立即恢复旧入口）
#    从 /etc/nginx/sites-enabled/blockchain-platform.conf 删除 /admin/ 与 /app-v2/ 两个 location 后
#    sudo nginx -t && sudo systemctl reload nginx

# 3) 回滚认证模式（仅限非生产环境）
#    deploy/.env: AUTH_MODE=hybrid && systemctl --user restart blockchain-api.service
```

数据库不提供向下迁移：0040–0043 全部是新增表与触发器，旧二进制可继续运行。

---

## 7. 下一步（Sprint 3 收尾 → Sprint 4）

1. 补 `S3-ADMIN-01` 的组织 / 工作区管理页面与 `S3-QA-01` 的跨租户负向测试。
2. 把一次性数据库迁移校验与权限目录一致性检查接入 CI（当前为脚本手动执行）。
3. 落地 `openapi/*.yaml`、lint 与生成 diff 检查。
4. 为 `audit_events_v2` / `login_events` 建立按月分区与每日锚点写入。
5. 进入 Sprint 4：案件与调查闭环页面。
