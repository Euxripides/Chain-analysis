# 公网多用户登录与用户级数据隔离改造计划书

> **状态**：待评审（本文件是计划，**不是**发布许可，也不构成对任何变更的批准）
> **日期**：2026-09-18（UTC+7）
> **范围**：`blockchain-platform`（ChainSight 双系统：用户层 + 后台管理 + 老控制台）
> **需求来源**：用户原话"公网走真正的用户登录，每个用户的数据独立保存，但是查询系统是共用的，用户账号由管理员进行增删、控制，用户不能自行注册"
> **依据**：`docs/dual-system-development-plan.md`、`docs/dual-system-completion-acceptance-criteria.md` §6.1/§7.2、`docs/legacy-console-decoupling.md`、ADR-001 / AD-002 / AD-003 / AD-008 / ADR-016 / ADR-017，以及 2026-09-18 对部署实例 `192.168.31.152` 的实测（附录 A）

---

## 0. 摘要

**结论一：账号体系、登录、MFA、RBAC、审计、管理员用户管理——已经建好了。** 这次不是"从零做多用户"，而是三件事：**① 把公网入口收敛到登录门之后；② 把数据隔离从"工作区级"下沉到"用户级"；③ 补齐管理员生命周期里唯一缺失的"删除"动作**，然后用可复跑的验收把边界钉死（§2.1 逐条列了已有能力与代码位置）。

**结论二：当前公网入口不符合要求，属于 P0。** 公网 nginx 的 `/` 仍然是老控制台，且 nginx 在满足条件时注入一个**共享服务凭据**——实测未登录、只带一个 Referer 就能拿到真实业务数据（§2.2）。

**结论三："每个用户的数据独立"目前不成立。** 系统没有"个人空间"概念，管理员新建用户时若未指定工作区，会被塞进**该组织最早创建的工作区**；线上 `default` 工作区有 30 名成员、26 个案件，`acme-lab` 有 15 名成员、149 个案件（§2.3）。

**决策已定（2026-09-18，见 §8 决策记录）**：链上数据共用、其余对象用户独有；老控制台只留内网回环；新用户默认角色可执行全部用户层操作；账号由管理员在后台生成（账号 + 密码）；删人后其私有对象归档；管理员启用谷歌验证器（TOTP，后端已就绪，缺后台注册页，见 §3.7）。仍待定的只有**存量案件归属（D2）**，P2 会先出只读盘点报表再定。

**工作量初估**：8–13 人天（P0 半天到一天即可单独上线），不含评审与联调窗口（§7）。

---

## 1. 目标与非目标

### 1.1 目标（每条都必须可验收）

| 编号 | 目标 | 判定口径 |
|---|---|---|
| **G1** | 公网（nginx `127.0.0.1:8088` → cloudflared 域名）上，**未登录只能看到登录页** | 公网上不存在任何"无会话即可读到业务数据"的路径（含带 Referer 的绕过） |
| **G2** | 账号**只能由管理员创建**，系统不存在自助注册路径 | `/api/auth/v1` 路由枚举无 register；登录页无注册入口；OIDC 若启用也不自动开户 |
| **G3** | 每个用户在用户层的**案件/调查/画布/报告/Finding/Evidence/导出/下载任务默认只有自己可见** | 两个真实账号交叉验证：列表为空、详情 404/403 且不泄露存在性 |
| **G4** | 链上公共数据与查询引擎**全平台共享**，不按用户复制、不按用户切分 | 两个账号查询同一地址/交易结果一致；覆盖口径、Provider 配额仍按平台/组织计 |
| **G5** | 管理员可对账号执行：创建 / 停用 / 启用 / **删除（离职）** / 重置密码 / 重置 MFA / 撤销全部会话 / 改角色，全部留审计 | 后台 UI 与 API 双通道可用；审计 actor 精确到人 |
| **G6** | 上述每条都有可复跑的证据 | 验收脚本 + 证据归档 + 回归全绿（§5） |

### 1.2 非目标（本期不做，避免范围漂移）

- 自助注册、邀请链接自助激活、社交登录（如需要，作为二期单独立项）
- 多组织 SaaS 化：本期仍是"一个组织（`default`）+ 多用户"
- 按用户计费/结算：`quotas` 表继续按 `organization` 维度
- 老控制台的功能重写或迁移：只做退役或内网保留
- 对象级"点对点共享给某人"的协作模型（本期用"共享工作区"表达共享）
- SCIM / HR 系统同步、密码自助找回的邮件通道

---

## 2. 现状盘点（2026-09-18 实测）

### 2.1 已具备，不需要重做

| 能力 | 位置 / 接口 | 说明 |
|---|---|---|
| 登录 / 登出 / 刷新 / 会话 | `POST /api/auth/v1/{login,logout,refresh}`、`GET /session` | 会话带 audience（user/admin）、workspace、mfa_satisfied、device |
| MFA（TOTP） | `POST /api/auth/v1/mfa/{enroll/start,enroll/confirm,verify,disable}` | 迁移 `0060_mfa_totp.sql`、`0061_mfa_login_challenges.sql`；登录页已实现第二步与倒计时 |
| 会话与设备管理 | `GET /sessions`、`DELETE /sessions/{id}`、`POST /sessions/revoke-all`、`GET/PATCH /devices` | 用户自助 |
| 改密与强制改密 | `POST /password/change` + `must_change_password` 提交门 | `backend/internal/api/mustchangepw.go`：未改密只放行 session/logout/password-change/methods |
| 组织账号登录（OIDC） | `GET /api/auth/v1/{oidc/start,oidc/callback}` | ADR-016；未配置即关闭（fail closed） |
| 管理员：建用户 | `POST /api/admin/v1/users`（`admin:user:create`） | 生成一次性初始密码、`must_change_password=true`、同事务写组织成员关系与角色绑定、写 `admin.user.created` 登录事件与审计 |
| 管理员：停用 / 启用 | `POST /users/{id}/commands/{disable,enable}` | 二次确认 + 原因必填 + **自动 ForceLogout** + 最后一个 Owner 保护（`CONFLICT_LAST_OWNER`）+ 自锁保护 |
| 管理员：重置密码 / 重置 MFA | `POST /users/{id}/commands/{reset-password,reset-mfa}`（`0062_reset_mfa_permission.sql`） | 后台 UsersPage 已接 |
| 管理员：权限与影响面 | `GET /users/{id}/effective-permissions`、`/impact-preview` | 变更前可预览 |
| 组织 / 工作区 / 成员 / 角色 | `GET/POST/PATCH /api/admin/v1/{organizations,workspaces}`、`GET /members`、`GET/POST /users/{id}/roles`、`DELETE /users/{id}/roles/{binding}`、`GET /roles`、`GET /permissions` | 后台 `OrganizationsPage` 已接工作区创建与成员角色授予/撤销 |
| 数据模型 | `0040_iam.sql`（identities/credentials/trusted_devices/sessions）、`0041_rbac.sql`（roles/role_permissions/organization_memberships/workspace_memberships/member_role_bindings） | 角色绑定已有 `scope_type ∈ {organization, workspace, case, self}` |
| 租户作用域与对象级授权 | S3 起；`scope_tenant_test.go` 静态禁止回退 `Store.DefaultOrgWorkspace` | 缺作用域 **fail closed**，不再泄漏默认组织数据 |
| 审计 | `login_events`（哈希链）+ `audit_events_v2` + `s6AuditChange` / `writeAuditWithChange` | 管理动作带 reason 与前后值 |
| 前端 | 用户层 `LoginPage`（含 MFA 第二步、OIDC）、`ProfilePage`；后台 `UsersPage`、`OrganizationsPage`、`SessionsPage`、`LoginEventsPage` | 提示一律弹窗（AGENTS.md + AD-009） |
| **无自助注册** | `/api/auth/v1` 全部路由：methods / login / logout / refresh / session / session/workspaces / session/workspace / sessions / sessions/{id} / sessions/revoke-all / devices / devices/{id} / password/change / oidc/* / mfa/* | **实测枚举中不存在 register**（附录 A.4） |

### 2.2 公网入口不符合要求（P0）

实测（VM `192.168.31.152`，nginx `127.0.0.1:8088`，`Host: aurore.online`）：

```text
GET /api/v1/cases?limit=1                              → 401 AUTHN_SESSION_MISSING
GET /api/v1/cases?limit=1   -e https://aurore.online/   → 200 + 真实案件数据（9234 B）
GET /                                                  → 200 老控制台（<title>Trace Desk · 链上调查工作台</title>）
GET /app-v2/login                                      → 200 用户层登录页（ChainSight 调查工作台）
GET /admin/login                                       → 200 后台登录页（ChainSight 后台管理）
```

- 注入规则在 `/etc/nginx/conf.d/legacy-console.conf`：**无 `cs_user_session`/`cs_admin_session` Cookie** 且 **Referer 指向老控制台** 且 **URI 前缀 `/api/v1/`** 时注入 `X-Legacy-Console-Token`。
- 后端把该凭据解析成**一个固定主体**：`LEGACY_CONSOLE_IDENTITY_ID=6e192e52-…`、组织 `d3b152f2-…`、工作区 `000e31bb-…`、`AUDIENCE=admin`，权限清单含 `admin:dataset:manage`、`admin:performance:update`（**写权限**）。
- 局域网入口 `192.168.31.152:3000` 更松：vite 代理对**任意 `^/api/` 且无 Cookie** 的请求注入（**不校验 Referer**），实测 `/api/v1/cases` → 200、`/api/v2/cases` → 200；并且该入口的 `/login`、`/admin/login`、`/app-v2/login` **全部被 SPA 兜底成老控制台首页**——在这个入口上根本不存在登录页。

**影响**：公网上存在"不登录即可读数据"的通道；且这个主体的审计 actor 是同一个 id，**无法区分到人**。这正是本需求要消除的东西。

### 2.3 数据隔离现状（不满足"每人独立"）

- `workspaces` 表只有 `(id, organization_id, name, slug, created_at)`——**没有 `kind` / `owner_identity_id`**，无法表达"个人空间"。
- 建用户逻辑（`backend/internal/api/sprint6_identity.go` L131–160）：未显式给 `workspace_id` 时 → 先查该身份的成员关系，再回退到**该组织最早创建的工作区**，并写入 `workspace_memberships` 与角色绑定 → **新用户直接进入公共工作区**。
- 线上实测（附录 A.5）：组织 `default` 的工作区 `default`（`000e31bb-…`）**30 名 active 成员 / 26 个案件**；`acme-lab` 15 名成员 / 149 个案件；全库 20 个工作区、47 个 active 身份（绝大多数是夹具）。
- 结论：今天的隔离粒度是**工作区**，不是**用户**；同一工作区内的用户互相可见。

### 2.4 差距清单

| # | 需求 | 现状 | 差距 | 优先级 |
|---|---|---|---|---|
| 1 | G1 公网只有登录后可见 | `/` 仍是老控制台；`X-Legacy-Console-Token` 在公网可触发 | 入口收敛 + 老线退出公网 + 共享凭据关闭 | **P0** |
| 2 | G3 每人数据独立 | 工作区级共享；无个人空间概念 | 个人空间模型 + 创建即分配 + 列表默认拒绝 + 存量归属迁移 | **P0** |
| 3 | G5 管理员"删" | 只有停用/启用，**没有 DELETE 用户** | 新增离职删除（软删 + 匿名化 + 审计保留）+ UI | P1 |
| 4 | G5 成员-工作区管理 | 有工作区与角色的管理端点/页面 | "把用户加入/移出某工作区"的一键操作与 UI | P1 |
| 5 | G2 无自助注册 | 无 register 路由 | 补**证明性测试**（路由清单断言 + 登录页无注册入口）+ OIDC 自动开户的显式决策 | P1 |
| 6 | G4 查询系统共用 | 已如此（`0002`：公共链数据不按租户复制，只挂归属） | 把共享面写成清单并做防滥用（限流/配额归属到人/审计到人） | P1 |
| 7 | G6 证据 | 分散 | 端到端验收脚本 + 双端（桌面/移动）证据 | P1 |

---

## 3. 目标态设计

### 3.1 信任边界与入口

```text
公网 ── cloudflared ──▶ nginx 127.0.0.1:8088
                          ├─ /                 → 302 /app-v2/login        （不再代理老控制台）
                          ├─ /app-v2/          → 3001 用户层（未登录只渲染登录页）
                          ├─ /admin/           → 3002 后台管理（独立 admin 会话）
                          ├─ /api/auth/v1/*    → 8080 登录/会话（不需要会话）
                          ├─ /api/v2/*         → 8080 用户层（需要 user 会话）
                          ├─ /api/admin/v1/*   → 8080 后台（需要 admin 会话 + admin:* 权限）
                          ├─ /docs /redoc /openapi.json /mcp  （按现有对外策略保留）
                          └─ 老控制台 ❌ 不再出现在公网
```

| 规则 | 内容 |
|---|---|
| **R1** | 公网 `location /` 改为 302 → `/app-v2/`（或一个不含业务数据的落地页），删除 `proxy_pass http://127.0.0.1:3000`。 |
| **R2** | 删除公网侧的 `X-Legacy-Console-Token` 注入与其 `map`（`/etc/nginx/conf.d/legacy-console.conf`）；后端 `LEGACY_CONSOLE_TOKEN_SHA256` 置空 → 整条路径 **fail closed**（现有语义：为空即关闭）。 |
| **R3** | **（D3 已定）老控制台只留内网回环**：vite 的 `--host 0.0.0.0` 改回 `127.0.0.1`，内网排障经 SSH 隧道访问；保留**专用审计身份**（不使用真人账号）；`frontend/vite.config.ts` 的凭据注入只在内网实例生效。 |
| **R4** | 用户层/后台任何路由在未登录时只渲染登录页（现状已如此），并补 Playwright 断言。 |
| **R5** | Cookie 语义不变：`cs_user_session` / `cs_admin_session` host-only + `Secure` + SameSite；公网仅经 https 域名访问。 |

### 3.2 账号生命周期（全部由管理员控制）

| 动作 | 现状 | 本期要做 |
|---|---|---|
| 创建 | ✅ `POST /admin/v1/users`（一次性密码 + 强制改密） | **（D5 已定）只走"管理员在后台生成账号与密码"**，不做邮件邀请、不做自助激活；后台 `UsersPage` 补新建表单（初始密码只在弹窗中出现一次）；创建时**自动建个人空间**（§3.3 N1）；密码加**有效期 24h** + 首登前禁止业务接口（提交门已有，补有效期） |
| 停用 / 启用 | ✅ 含 ForceLogout、二次确认、原因必填、最后 Owner 保护 | 无 |
| **删除（离职）** | ❌ 不存在 | 新增 `DELETE /api/admin/v1/users/{id}`：二次确认 + 原因必填 + 影响预览（名下对象数、成员关系、会话数）；**软删**（`identities.deleted_at` + 状态 `deleted`）并**匿名化** `email/display_name`（保留 id 以维持审计与 append-only 外键）；删除即 ForceLogout + 撤销所有角色绑定；**（D6 已定）其私有对象随个人空间一并归档为只读**（写入一律 403，管理员仍可在后台只读查阅并留痕） |
| 重置密码 / 重置 MFA | ✅ | 无 |
| 撤销全部会话 | ✅（用户自助 `sessions/revoke-all`、按会话 revoke） | 补"按用户一键撤销"（后台侧），复用 ForceLogout |
| 角色 | ✅ 授予/撤销（组织/工作区/case/self 作用域） | 后台补"加入/移出工作区"的一键动作与 UI |
| 注册 | ✅ 不存在 | **（D5 已定）只由管理员在后台生成账号与密码**，无邀请链接、无自助激活；补**断言性测试**防止回归；OIDC 若启用则只允许 allowlist 邮箱登录（不自动开户） |

### 3.3 数据隔离模型（核心）

三层结构：

1. **组织**：本期 1 个（`default`），所有用户同属一个组织（账号由管理员在同一组织内管理）。
2. **工作区**：新增属性 `kind ∈ {personal, shared}`；`personal` 工作区与用户 **1:1**，`owner_identity_id` 指向该用户。
3. **对象**：案件、调查、Finding、Evidence、画布、报告、导出、下载任务——沿用既有 `workspace_id` 归属，但**默认落点从公共工作区改为个人空间**。

> **（D1 已定）共享与私有的分界线**：**数据库里的链上数据全部共享**（transactions / token_transfers / address_activity / internal_transactions / 区块与地址维度等），任何登录用户查到的都是同一份事实；**其余对象由创建者独立拥有**（案件、调查、Finding、Evidence、画布、报告、导出、下载任务），默认对其他人不可见，共享只能由管理员把两人放进同一个共享工作区来显式发生。

| 规则 | 内容 |
|---|---|
| **N1** | 创建用户 → **同事务**创建个人空间（`slug = p-<identity 前 12 位>`、`name = <display_name> 的个人空间`、`kind='personal'`、`owner_identity_id`=该用户）+ `workspace_memberships` + **`basic_user`** 角色绑定（`scope_type='workspace'`）；**彻底移除**「回退到组织最早工作区」的分支（已落地，见 §4.3）。管理员显式指定 `workspace_id` 时才额外加入该共享工作区。 |
| **N2** | 用户层所有列表/详情查询以"当前会话工作区 + 我是 active 成员"为准；跨工作区一律 403 或空集，且**不泄露存在性**（统一 404 语义）。 |
| **N3** | 共享只通过"加入同一共享工作区"表达；管理员在后台操作成员关系，用户不能自行加入/退出（本期）。 |
| **N4** | 管理员身份**不**带来对用户私有数据的默认可见性；后台要跨用户看数据必须走**审计化的后台视图**（现状 `/api/admin/v1/jobs` 等已如此），每次读取留痕。 |
| **N5** | **（D6 已定）删除用户时其个人空间与对象一并归档为只读**：空间标记 `archived_at`，其下对象一切写入返回 403；管理员仍可只读查阅并留审计痕迹（P2-10）。 |

### 3.4 共享面：'查询系统共用'的精确边界

| 面 | 归属 | 本期动作 |
|---|---|---|
| 链上原始数据（transactions / token_transfers / address_activity / internal_transactions …） | **共享**（不按租户复制，只挂归属；`0002` §70） | 不变 |
| 查询引擎与读接口（`/api/v2/explorer/*`、`/api/v2/search`、`/api/v1` 兼容读） | **共享**，只校验 `explorer:read`/`search:read` | 不变；补按用户的**限流**与热点查询配额 |
| 数据覆盖与新鲜度、Token/Method 元数据、覆盖率统计 | **共享**（平台口径） | 不变 |
| Provider 配额 / 成本 / 路由 / 预算 | **共享（按组织计）** | 不变；如需按用户核算，二期 |
| 下载任务 / 导出任务与产物 | **私有**（按创建者的工作区） | 归属精确到 `created_by_identity`（`0050` 已具备该列），配额与审计落到人 |
| 案件 / 调查 / 画布 / 报告 / Finding / Evidence | **私有**（个人空间或共享工作区） | 本期核心改动 |
| 审计 / 登录事件 | **平台级**，管理员可读 | 用户仅能看自己的会话、设备、登录记录 |

> 一句话：**"查询"共用，"调查"私有**。用户在共享的链上数据上做出来的所有产物，默认只属于他自己，除非管理员把他放进同一个共享工作区。

### 3.5 权限与角色

沿用现有 9 个系统角色：`viewer / analyst / investigator / reviewer / auditor / owner / security_admin / platform_admin / api_client`（`0041_rbac.sql` + `0065`）。

| 项 | 计划 |
|---|---|
| 新用户默认角色 | **（D4 已定）基础操作** → 新增系统角色 `basic_user`，权限 = 查询 + 自己案件的常规读写（逐条见 §3.5.1）。**不含任何 `admin:*`**，也不含删除 / 审批 / 复核 / 签署类动作；**后台管理只属于管理员角色**，普通用户拿不到 `/admin/` 入口 |
| 个人空间角色 | **`basic_user`**（`scope_type='workspace'`）——**不是 `owner`**。2026-09-18 实测：`owner` 持有 178 个权限、其中 **79 个是 `admin:*`**，把它绑给普通用户等于把后台控制面一起给出去（与 D4 冲突）。个人空间的「归属」由 `workspaces.owner_identity_id` 表达，权限由角色表达，两者不混用 |
| 新增权限键 | `admin:user:delete`（高风险，授予 `security_admin` / `platform_admin`）；按 AD-003 在 `permissions` 目录声明，并纳入静态覆盖率检查 |
| 不变式 | 任何新增端点都必须 `require(权限键)`；`/api/admin/v1` 默认拒绝；`scope_tenant_test.go` 的"禁止回退默认工作区"继续生效并扩展到新代码 |

#### 3.5.1 `basic_user` 的具体权限（逐条，按线上 `permissions` 目录）

| 组 | 授权键 |
|---|---|
| 查询（共享链上数据） | `explorer:read`、`explorer:fetch`、`address:read`、`transaction:read`、`token:read`、`search:read`、`search:global`、`dashboard:read` |
| 案件 | `case:list`、`case:read`、`case:create`、`case:update`、`case:activity:read`、`case:comment:create`、`comment:list`、`subject:list`、`subject:create`、`subject:update`、`case:member:list` |
| 调查 | `investigation:list`、`investigation:read`、`investigation:create`、`investigation:update`、`investigation:run`、`investigation:start`、`investigation:pause`、`investigation:resume`、`investigation:retry`、`investigation:cancel` |
| 画布 | `canvas:list`、`canvas:read`、`canvas:create`、`canvas:update`、`canvas:branch`、`canvas:snapshot`、`canvas:snapshot:create` |
| 报告 / 发现 / 证据 | `report:list`、`report:read`、`report:create`、`report:download`；`finding:list`、`finding:read`、`finding:create`、`finding:update`；`evidence:list`、`evidence:read`、`evidence:create`、`evidence:download`、`evidence:snapshot:create` |
| 下载 / 作业 / 导出 | `download:create`、`download:read`、`download:preview`、`download:refresh`、`download:reuse`、`download:control`、`job:list:self`、`job:read`、`job:control`、`job:retry`、`export:create`、`export:read`、`export:list`、`export:download` |
| 通知与个人 | `notification:list`、`notification:read`、`notification:update`、`inbox:read`、`profile:read`、`profile:update`、`profile:update:self`、`preference:update`、`security:update`、`session:self:revoke` |

**明确不给（按需由管理员单独授予 `reviewer` / `auditor` / `owner`）**：

- 后台控制面：全部 `admin:*`；
- 删除类：`case:delete`、`case:archive`、`canvas:delete`、`investigation:delete`、`report:delete`、`evidence:delete`、`download:delete`、`export:delete`、`subject:remove`；
- 审批 / 复核 / 签署：`investigation:approve`、`investigation:submit_review`、`report:approve`、`report:review`、`report:sign`、`finding:review`、`evidence:review`；
- 敏感与共享配置：`evidence:sensitive:read`、`case:member:manage`、`case:state:update`。

> 落地方式：迁移 `0068_basic_user_role.sql` 在 `roles`（`is_system=true`）与 `role_permissions` 里声明该角色；后台 `RolesPage` 可直接看到。需要放宽时按人加白名单，不改默认角色。

### 3.6 前端改动

**用户层（`frontend/apps/user-console`）**
- 登录页（已有）：补"停用/锁定/需改密"分支的弹窗文案；补**无注册入口**的断言。
- 导航与作用域：显示当前工作区（个人空间 / 共享工作区），工作区切换沿用 `GET /session/workspaces` + `POST /session/workspace`。
- `ProfilePage`：改密、MFA、会话与设备（接口已有）；补"被管理员重置后强制改密"的引导弹窗。

**后台（`frontend/apps/admin-console`）**
- `UsersPage`：补**新建用户**（一次性密码只在弹窗出现一次 + 强制改密提示）、**删除/离职**（二次确认 + 影响预览 + 原因必填）、**撤销全部会话**、**加入/移出工作区**。
- `OrganizationsPage`：工作区列表增加 `kind`（个人/共享）列与成员管理入口；工作区选择不再默认第一个（`workspaces[0]` 的隐式默认要显式化）。
- **账号安全（新增页，D7）**：后台目前**没有** MFA 注册入口（只有用户层 `ProfilePage` 有）。需补“账号安全”页：MFA 注册（二维码 + 密钥 + 恢复码）、改密、我的会话与设备；直接复用 `/api/auth/v1/mfa/*` 与 `frontend/packages/ui/QrCode`。

**共同约束（`docs/code-standards.md` + `AGENTS.md` + AD-009）**
- 所有警告/说明/成功/错误/风险/确认**只能用弹窗**；页面正文只留业务数据、字段标签、导航与操作控件。
- 前端改动**不能以构建通过为准**，必须在真实渲染下验证桌面端 + 一个移动端宽度（遮罩、层级、关闭、键盘焦点、滚动）。
- 涉及后端数据的界面必须核对接口真实响应，不得用占位值冒充。

### 3.7 管理员 MFA（D7 回答）

**能，而且后端已经就绪——缺的只是后台的注册页面。** 逐项事实：

| 项 | 现状 | 证据 |
|---|---|---|
| 算法与参数 | RFC 6238 TOTP，**SHA1 / 6 位 / 30 秒**，issuer=`ChainSight` | `backend/internal/mfa/totp.go`（`Digits=6`、`PeriodSeconds=30`、HMAC-SHA1）；`totp_test.go` 用 RFC 6238 附录 B 官方向量 |
| 与谷歌验证器兼容 | **是**——这就是 Google Authenticator / Microsoft Authenticator / 1Password 的默认参数 | `mfa/totp_test.go:127` 断言 otpauth URI 含 `otpauth://totp/`、`issuer=ChainSight`、`digits=6`、`period=30` |
| 注册流程 | `POST /mfa/enroll/start` 返回 `secret` + `otpauth_uri`；`POST /mfa/enroll/confirm` 用一次正确验证码确认并**一次性下发恢复码** | `backend/internal/api/authmfa.go`；用户层 `ProfilePage.tsx` 已用内联 SVG 渲染二维码（`frontend/packages/ui/QrCode.tsx`，零依赖） |
| 登录第二步 | 用户层与**后台**登录页都已实现 202 / `state=mfa_required` 的第二步（TOTP 或恢复码，带倒计时、防重放、失败次数） | `apps/user-console/src/pages/LoginPage.tsx`、`apps/admin-console/src/pages/LoginPage.tsx` |
| 强制策略 | `AUTH_REQUIRE_ADMIN_MFA`（**启动期环境变量，默认 false**）：为 true 时后台受众必须完成 MFA 第二步 | `internal/config/config.go:970`、`internal/iam/service.go:251`；后台设置页目前只做**只读展示** |
| 逐人策略与重置 | `identities.mfa_required` 列；管理员可 `POST /users/{id}/commands/reset-mfa`（迁移 `0062`）清空某人的密钥与恢复码 | 后台 `UsersPage` 已接 |

**要补的两件事（已并入 §4 P1 任务）**：

1. **后台“账号安全”页**：注册 MFA、展示恢复码、管理自己的会话与设备——否则开启强制 MFA 后，管理员自己无处注册。
2. **开启顺序与逃生路径**：先让各管理员完成注册，再打开 `AUTH_REQUIRE_ADMIN_MFA`；开关打开后未注册的管理员登录会被卡在第二步（fail closed 是对的），因此必须保留“管理员用 `reset-mfa` 重置他人 / 由另一名已注册管理员协助”的路径，并演练一次。

### 3.8 迁移与兼容

| 编号 | 迁移 | 内容 | 破坏性 |
|---|---|---|---|
| **M-1** | `0066_workspace_kind.sql` | `workspaces` 加 `kind text NOT NULL DEFAULT 'shared' CHECK (kind IN ('personal','shared'))`、`owner_identity_id uuid REFERENCES identities(id)`；`CHECK (kind='shared' OR owner_identity_id IS NOT NULL)`；补索引 | 只加列，无 |
| **M-2** | `0067_identity_deletion.sql` | `identities` 加 `deleted_at timestamptz`（+ 部分索引）；`status` CHECK 增加 `'deleted'` | 只加列，无 |
| **M-3** | 数据回填脚本（可 `--dry-run`） | 为存量**真实用户**建个人空间；按 `created_by_identity`（`0050`）→ `created_by`（legacy users）→ 工作区默认 的顺序判定归属，逐条留痕到证据目录 | 仅插入/更新归属，可重放 |
| **M-4** | 语义降级 | `default` 工作区保留为共享工作区（团队），成员与角色**显式确认**后才保留；不再作为新用户默认落点 | 配置层 |
| **M-5** | 老控制台退役 | nginx 去掉 `location /` → 3000；删注入；清空 `LEGACY_CONSOLE_*`；`docs/legacy-console-decoupling.md` 增补"已退役"章节（保留历史，不删文件） | 配置层，可回滚 |

**回滚**：M-1/M-2 只加列，回滚 = 保留列不读；M-3 用可重放脚本 + 变更前后快照；M-5 回滚 = 恢复上一版 nginx 配置（保留 `/etc/nginx/*.bak-<时间戳>`）。

---

## 4. 实施计划

### 4.1 阶段划分

| 阶段 | 名称 | 目标 | 预估 | 可独立上线 |
|---|---|---|---|---|
| **P0** | 公网收敛 | 公网未登录只能看到登录页；老线与共享凭据退出公网 | 0.5–1 天 | ✅ 立即 |
| **P1** | 账号生命周期补齐 | 管理员"增删改控"闭环 + 无自助注册的证明性测试 | 2–3 天 | ✅ |
| **P2** | 个人空间与数据隔离 | 新用户自动个人空间；存量归属迁移；列表默认拒绝 | 3–5 天 | ✅（灰度） |
| **P3** | 共享面治理 | 查询限流/配额归属到人、审计到人、口径文档 | 1–2 天 | ✅ |
| **P4** | 端到端验收与发布 | §5 全清单 + 双端证据 + 发布决策更新 | 1–2 天 | — |

### 4.2 任务清单

**P0（公网收敛）**

| 编号 | 任务 | 改动面 | 验收 | 证据 |
|---|---|---|---|---|
| P0-01 | 公网 `location /` 改为 302 `/app-v2/` | `deploy/nginx/blockchain-platform.conf` + VM 实例 | `GET /` 返回 302 且 Location 指向登录 | `curl -sI` 输出 |
| P0-02 | 删除公网共享凭据注入 | `/etc/nginx/conf.d/legacy-console.conf`、`deploy/nginx/legacy-console.conf.template` | 带老线 Referer 的 `/api/v1/cases` → 401 | 同 A1 命令 |
| P0-03 | 后端 `LEGACY_CONSOLE_TOKEN_SHA256` 置空并重启 | api 服务环境（systemd） | 老线路径 fail closed；启动日志无 legacy 主体 | `/proc/<pid>/environ` + 401 探测 |
| P0-04 | 局域网 :3000 收敛（绑回环或内网专用） | VM 上 vite 进程 / 内网 nginx | 局域网入口不再对公众网段开放 | `ss -lntp` 输出 |
| P0-05 | 回归：`/app-v2/login`、`/admin/login`、API 三线可用 | — | 登录/取数/后台均可 | Playwright + curl |

**P1（账号生命周期）**

| 编号 | 任务 | 改动面 | 验收 |
|---|---|---|---|
| P1-01 | `admin:user:delete` 权限键声明 | `migrations/0068_*_permission.sql`、权限目录 | 静态覆盖率检查通过；后台授予页可见 |
| P1-02 | `DELETE /api/admin/v1/users/{id}`（软删+匿名化+ForceLogout） | `internal/api/adminv1.go`（或 `sprint6_identity.go`） | 删除后不可登录；审计仍可查该 id；`identities` 无物理删除 |
| P1-03 | 影响预览扩展到删除 | `/users/{id}/impact-preview` | 返回名下对象数、成员关系、活跃会话数 |
| P1-04 | 按用户"撤销全部会话"后台动作 | admin API + `SessionsPage`/`UsersPage` | 撤销后旧会话立即 401 |
| P1-05 | "加入/移出工作区"端点与 UI | `adminorg.go` + `OrganizationsPage` | 移出后该用户列表立即为空 |
| P1-06 | 无自助注册的证明性测试 | `internal/api/*_test.go`（路由清单断言）、Playwright（登录页无注册入口/DOM 断言） | 测试红灯可复现（人为加 register 路由即失败） |
| P1-07 | 一次性密码有效期 + 首登前提交门 | `mustchangepw.go` / `credentials.expires_at` | 过期密码登录被拒并与"密码错误"同码同文案 |
| P1-08 | OIDC 自动开户策略显式化 | OIDC 回调 handler | 未在 allowlist 的邮箱不得登录（或明确关闭 OIDC） |
| P1-09 | **后台“账号安全”页**（MFA 注册 / 恢复码 / 我的会话与设备） | `apps/admin-console/src/pages/SecurityPage.tsx`（新增）+ 路由与导航 | **已完成并真机验证**：Playwright 用测试端现算的 TOTP 完成注册（7/7）；注册后该账号重新登录返回 `202 state=mfa_required`（见 `acceptance-evidence/dual-system-v1/ADMIN-CONSOLE-ACCOUNT-UI-20260918.md`） |
| P1-10 | 开启 `AUTH_REQUIRE_ADMIN_MFA` 的前置与演练 | 部署环境变量 + runbook | 全部管理员注册完成后再开启；未注册者被拒且可经 `reset-mfa` 恢复 |

**P2（个人空间与数据隔离）**

| 编号 | 任务 | 改动面 | 验收 |
|---|---|---|---|
| P2-01 | 迁移 `0066`（workspace kind/owner） | `migrations/postgres/0066_*.sql` | 迁移可重放；`kind` 约束生效 |
| P2-02 | 迁移 `0067`（identity deleted_at/status） | 同上 | 同上 |
| P2-03 | 建用户即建个人空间 | `sprint6_identity.go` | 新用户登录后只看到自己的空空间；无"默认工作区"落点 |
| P2-04 | 移除"回退组织最早工作区"分支 | `sprint6_identity.go` L131–160 | 静态测试禁止该回退再次出现 |
| P2-05 | 列表/详情默认拒绝的回归测试 | `internal/api` 测试 + `scope_tenant_test.go` | 两账号交叉看不到对方对象，且不泄露存在性 |
| P2-06 | 存量归属盘点脚本（只读） | `scripts/` 新脚本 | 产出：每个用户的私有对象数、无归属对象数 |
| P2-07 | 归属回填（dry-run → apply） | 同 P2-06 | dry-run 报表经确认后才 apply；逐条留痕 |
| P2-08 | 个人/共享工作区在前后端的可辨识 | 用户层导航、后台工作区列表 | 用户能看出自己当前在哪个空间 |
| P2-09 | 后台跨用户查看的审计化视图确认 | `/api/admin/v1/jobs` 等 | 每次读取有审计记录 |
| P2-10 | **删除即归档只读**（D6）：个人空间标记 `archived_at`，其下对象一切写入 403 | `workspaces` 迁移 + 各写入路径校验 | 删除后管理员仍可只读审计查阅，但任何角色都不能再写入 |

**P3（共享面治理）**

| 编号 | 任务 | 验收 |
|---|---|---|
| P3-01 | 查询接口按用户限流（登录态维度） | 压测下不误伤共享查询；超限返回 429 且有审计 |
| P3-02 | 下载/导出配额与审计归属到 `created_by_identity` | 两个账号各自配额独立计数，数据仍共享 |
| P3-03 | 共享面清单落文档（本文 §3.4 升级为正式文档） | 文档与实现一致并纳入 surface inventory |

**P4（验收与发布）**

| 编号 | 任务 | 验收 |
|---|---|---|
| P4-01 | §5 全清单脚本化并归档 | 证据目录 + 可复跑 |
| P4-02 | 桌面端 + 移动端 Playwright | 0 FAIL |
| P4-03 | 回归 S4/S5/S6/S7 + `go test ./...` | 全绿 |
| P4-04 | 更新 `current-surface-inventory.md`、`RELEASE-DECISION.md` | 与实现一致 |

### 4.3 实施进度（2026-09-18）

已完成并验证的部分：

| 任务 | 交付物 | 验证 |
|---|---|---|
| P0-01 公网根路径收敛 | `deploy/nginx-blockchain-platform.conf`：`location = /` 与 `location /` 均 302 到 `/app-v2/`，不再代理老控制台 | **已部署并验证**：公网 `/` → 302，`/api/v1/cases`（带老线 Referer）→ 401（见 `acceptance-evidence/dual-system-v1/P0-PUBLIC-ENTRY-LOCKDOWN-20260918.md`） |
| P0-02 / R3 老控制台内网化 | `frontend/vite.config.ts` 绑定 `127.0.0.1`；`deploy/nginx/legacy-console.conf.template` 标注「仅内网回环实例」 | **已部署并验证**：`ss -lntp` 为 `127.0.0.1:3000`；公网 map 文件已停用；真实 token 直打后端 → 401（fail closed） |
| P1-01 权限键 `admin:user:delete` | `0068_basic_user_role.sql`（授予 `platform_admin` / `security_admin`） | **已应用到生产库**（`schema_migrations` 已登记，`basic_user` 73 权限 / 0 个 `admin:*`） |
| P1-02 删除（离职）用户 | `backend/internal/api/admin_delete_user.go` + `adminv1.go` 路由 `DELETE /api/admin/v1/users/{id}`：软删 + 匿名化 + 撤权 + 归档个人空间 + 强制下线 + 审计 | `go build ./...` 通过；静态护栏测试通过 |
| P2-01/02 迁移 | `0066_workspace_kind.sql`（kind / owner_identity_id / archived_at / created_by_identity）、`0067_identity_deletion.sql`（deleted_at + `deleted` 状态） | **已应用到生产库**；现有 20 个工作区保持 `kind='shared'`（语义未变）；迁移前有全库备份 |
| P2-03/04 建用户即建个人空间 | `sprint6_identity.go`：删除「回退到组织最早工作区」分支，改为一律建 `kind=personal` 个人空间并绑 `basic_user` | 静态护栏 `TestCreateUserDoesNotFallBackToFirstWorkspace` / `TestPersonalWorkspaceBindsBasicUserNotOwner` 通过 |
| 登录侧状态机 | `iam/identity.go` 增 `StatusDeleted`；`iam/service.go` 在**密码登录与 MFA 第二步两条路径**都拒绝已删除账号 | `TestDeletedIdentityIsRejectedOnEveryLoginPath` 通过 |
| 无自助注册护栏 | `internal/api/user_isolation_static_test.go`：扫描全部路由注册，禁止 `register` / `signup` | `TestNoSelfRegistrationRoutes` 通过 |
| 夹具清理 | 见 `acceptance-evidence/dual-system-v1/ACME-FIXTURE-CLEANUP-20260918.md` | 清理后 17 张表计数全 0；MinIO 171 个孤儿对象已删 |

未开始：P1-03/04/05（影响预览扩展、按用户强制下线、加入/移出工作区）、P1-07（初始密码有效期）、P1-10（强制 MFA 的开启演练）、P2-05..P2-10（隔离回归、归属回填、归档只读语义）、P3（共享面治理）、P4（验收）。已完成：P1-09（后台账号安全页，见 §4.5）。

**证据命令**（本地）：`go build ./...` 与 `go test ./internal/api/ ./internal/iam/` 均通过（GOARCH=amd64）。

---
### 4.4 二进制发布与真机验证（2026-09-18）

按需求方要求以「VM 上已部署源码」为基线发布（不从本地脏工作树出包）：

| 项 | 结果 |
|---|---|
| 基线证明 | 移出本次新增的 3 个迁移后在同目录重建，产物与在运行二进制**逐字节一致**（`7b1deb54…`） |
| 移植方式 | JSON 描述「文件 + 精确字符串对」共 9 处定点替换 + 5 个新文件；锚点匹配数 ≠ 1 即整体中止 |
| 迁移 | `0066`–`0070` 已应用（`0069` 解析器一致性补丁、`0070` 管理员全权）；用新 `migrate` 重跑后 `schema_migrations` 行数不变（无重复变更） |
| 发布产物 | `api` `08a2bcf4…`、`migrate` `8741601a…`；旧二进制归档为 `api.prev-7b1deb54` / `migrate.prev-d48e58fe` |
| 启动验证 | `health/live`、`health/ready` 均 200；日志 `clickhouse ready → minio ready → provider(ankr live) → method signature seed ready → api listening` |
| 端到端验证 | **PASS=47 / FAIL=0**（隔离夹具组织；脚本 `acceptance-evidence/dual-system-v1/tests/verify-user-lifecycle-isolation.py`） |

发布中修掉两个缺陷（详见发布证据 §4）：

1. `admin:user:delete` 只进 SQL 迁移、未进 `internal/policy/catalog.go` → 运行时策略引擎按 Go 目录判定，端点返回 `403 permission_not_declared`；**AD-003 的「权限键必须声明」是运行时判定依据**，不是文档要求。
2. 权限种子行换行导致 `catalog_sql_test.go` 的解析器看不见该行（Go 有、SQL 无）→ 新增 `0069` 以单行形式幂等重复声明（不改写已应用迁移）。

**管理员权限口径（已定）**：需求方明确「管理员有最高、所有权限」→ 迁移 `0070` 给 `owner` 补齐最后一条 `admin:user:delete`（实测 179/179），并把该口径写成迁移自检不变量。端到端复验 **PASS=51 / FAIL=0**。

---
### 4.5 后台管理台 UI：用户生命周期 + 账号安全（2026-09-18）

| 交付 | 内容 | 验证 |
|---|---|---|
| 新建用户（D5） | 表单（邮箱 / 显示名 / 附加角色键 / 原因）→ **一次性初始密码只在结果弹窗出现一次** + 复制按钮；同时说明已创建个人空间 | Playwright 真机 20/20 |
| 删除（离职）（D6） | 详情弹窗入口 → 影响预览（归档 / 匿名化 / 保留身份行）→ 执行（原因必填 + 影响勾选）→ 结果弹窗 | 同上；落库复核 `status=deleted`、邮箱匿名化、个人空间 `archived_at` 非空 |
| 账号安全页（D7） | TOTP 注册（本地内联 QR + 密钥 + 一次性 10 枚恢复码）、关闭 MFA、修改密码、我的会话与设备 | Playwright 真机 7/7；注册后重新登录返回 `202 mfa_required`（methods=totp,recovery_code） |
| 迁移 `0071` | 把 self 类权限授予**全部**系统角色（此前 platform_admin / security_admin / auditor 无任何 self 权限，新页面会被 fail-closed 藏起来） | 已应用；`security:update` 持有者覆盖全部系统角色 |

**真机验证抓到的真实缺陷**：`packages/ui/Dialog.tsx` 的字段判定是 `required !== false` —— **不写 `required` 即等于必填**。最初把「显示名称 / 附加角色键」当选填（只写 `pattern`），导致这两个字段留空时提交按钮永远 disabled、管理员根本建不了号；`tsc` 与 `vite build` 全绿，只有真机点击才暴露。已改为显式 `required: false` 并加注释。

---
## 5. 验收标准（可执行）

| 编号 | 验收项 | 命令 / 方法 | 期望 |
|---|---|---|---|
| **A1** | 公网未登录不可读数据 | `curl -s -o /dev/null -w '%{http_code}' https://<公网域名>/api/v1/cases?limit=1`（含带老线 Referer 的变体） | `401`（两种都 401） |
| **A2** | 公网 `/` 不再暴露老控制台 | `curl -sI https://<公网域名>/` | `302` → `/app-v2/login` |
| **A3** | 无自助注册面 | 路由枚举断言 + Playwright 断言登录页无"注册" | 无 register 路由；DOM 无注册入口 |
| **A4** | 管理员增删控制 | 后台创建 → 一次性密码首登 → 强制改密 → 停用（登录被拒）→ 启用 → 重置密码/MFA → 删除（登录被拒且审计仍可查） | 全链路通过，每步有审计 |
| **A5** | **用户数据隔离**（核心） | 账号 A 建案件/画布/报告/导出/下载；账号 B 登录 | B 的列表为空、详情 404/403；B 无法通过 ID 猜测访问 |
| **A6** | **查询系统共用**（核心） | A、B 各自查询同一地址 overview/activity 与同一交易详情 | 结果一致；覆盖/新鲜度口径一致 |
| **A7** | 共享工作区生效 | 管理员把 B 加入 A 的共享工作区 → B 可见；移出后不可见 | 两个方向都验证 |
| **A8** | 会话安全 | 改密/管理员重置/删除后旧会话失效；`revoke-all` 生效；停用自动登出 | 旧 Cookie 立即 401 |
| **A9** | 审计可区分到人 | 检查 `login_events`/`audit_events_v2` 的 actor | 每步 actor = 真实用户 id，**不再出现共享主体** |
| **A10** | 老线 fail closed | `LEGACY_CONSOLE_TOKEN_SHA256` 为空时老线请求 | 401，且无匿名放行 |
| **A11** | 前端规范 | 桌面 + 移动端 Playwright | 提示均为弹窗；无遮挡/溢出/不可关闭 |
| **A12** | 回归 | `go test ./...`、`run-sprint{4,5,6,7}-e2e.sh`、`dual-system-nav-permission.spec.ts` | 全绿（0 FAIL） |
| **A13** | 谷歌验证器（D7） | 后台“账号安全”页扫码注册 → 退出 → 重新登录 | 必须过第二步；验证码错误计数与锁定生效；恢复码一次性可用；`reset-mfa` 后回落为未启用 |
| **A14** | 删除即归档（D6） | 删除某用户后，尝试在其个人空间创建/修改对象 | 一律 403；管理员仍可只读查阅且留审计 |

---

## 6. 风险与回滚

| # | 风险 | 影响 | 缓解 |
|---|---|---|---|
| R1 | 存量对象归属信息不全（`created_by` 指向 legacy users 或为空） | 迁移可能把对象错分到某人名下 | 先跑**只读盘点**（P2-06）出报表；无法判定的对象暂留共享工作区并单列清单，人工指认后再迁 |
| R2 | 删除用户与 append-only 审计/外键冲突 | 审计链断或删除失败 | **绝不物理删除** `identities`：软删 + 匿名化，保留 id 与审计引用（与 AD-005/ADR-018 一致） |
| R3 | 隔离切换误伤既有协作 | 用户突然看不到共享案件 | 灰度：先建个人空间但**暂不切换默认落点**，观察一周；切换前把`default`工作区成员盘点并显式确认 |
| R4 | 老控制台退役影响内部排障 | 运维效率下降 | 保留内网回环实例 + 专用审计身份；或明确由后台 `/admin/` 替代后退役 |
| R5 | 公网登录后仍可能被撞库 | 账号被爆破 | 已有 `failed_login_count`/`locked_until`；补登录失败率告警与来源 IP 限流；建议对管理员强制 MFA（`mfa_required` 列已存在） |
| R6 | 一次性密码经转述泄露 | 首次登录前账号被抢用 | 强制首登改密（已有）+ 密码有效期（P1-07）+ 首登前禁止业务接口（提交门已有） |
| R7 | 新增权限键漏声明 | 端点静默放行/拒绝 | AD-003 静态覆盖率检查 + `current-surface-inventory` 同步 |

---

## 7. 工作量与里程碑

| 阶段 | 人天（估） | 说明 |
|---|---|---|
| P0 公网收敛 | 0.5–1 | 配置级改动 + 验证，风险最低 |
| P1 账号生命周期 | 3–4 | 后端 1 个新端点 + 2 个扩展现端点 + 后台 UI（新建/删除/成员）+ **后台 MFA 注册页（D7）** |
| P2 个人空间与隔离 | 3–5 | 迁移 + 建用户逻辑 + 归属回填脚本（最大不确定项） |
| P3 共享面治理 | 1–2 | 限流/配额/文档 |
| P4 验收与发布 | 1–2 | 脚本化 + 双端证据 + 文档更新 |
| **合计** | **9–14 人天** | 不含评审、联调窗口与人工数据指认时间 |

**里程碑**
- **M1（P0 完成即上线）**：公网只能登录后可见 —— 建议**今天就做**，因为它独立于其它所有改动。
- **M2（P1 完成）**：管理员增删控制闭环，注册面被测试钉死。
- **M3（P2 完成）**：新用户默认个人空间；存量归属迁移完成并留痕。
- **M4（P4 完成）**：验收全绿，更新发布决策（当前为 **NO-GO for cutover**，见 `RELEASE-DECISION.md`）。

---

## 8. 决策记录（2026-09-18 需求方已定）

| # | 决策点 | 决定 | 落地影响 |
|---|---|---|---|
| **D1** | 共享与私有的边界 | **数据库里的链上数据全部共享；其余对象由用户独立拥有** | §3.3：个人空间作为默认落点；共享只能由管理员放进同一共享工作区而显式发生。查询引擎、链上数据、覆盖口径、Provider 配额保持全平台共享（§3.4） |
| **D2** | 存量案件归属 | **已查明来源，按来源分治（待确认）** | `default` 的 26 个 = 3 个真人案件（盘古 / 星链 / ChatGPT MCP）+ 23 个验收脚本与夹具产物；`acme-lab` 的 149 个 = **全部 Sprint 4 夹具**（148 个属 `fixture-investigator@fixture.local`）。建议：acme-lab 留作验收环境不迁；default 的 3 个真实案件归 `admin@local` 个人空间，其余归档。来源明细见附录 A.6 |
| **D3** | 老控制台 `/` | **只留内网回环** | §3.1 R3：vite 绑回 `127.0.0.1`，公网 `location /` 改为 302 到登录页；内网排障经 SSH 隧道，使用专用审计身份 |
| **D4** | 新用户默认角色 | **基础操作；后台只属于管理员** | §3.5 + §3.5.1：新增 `basic_user` = 查询 + 自己案件的常规读写，**不含任何 `admin:*`、不含删除/审批/复核/签署**。D4-b 关闭：后台管理不对普通用户开放 |
| **D5** | 账号创建方式 | **管理员在后台生成账号与密码** | §3.2：无邮件邀请、无自助激活、无注册入口；初始密码只在弹窗出现一次 + 24h 有效期 + 首登强制改密 |
| **D6** | 删人后其私有对象 | **归档** | §3.2 + P2-10：个人空间标记归档、其下对象只读；管理员仍可审计查阅并留痕 |
| **D7** | 管理员接谷歌验证器 | **要接（后端已就绪）** | §3.7：TOTP SHA1 / 6 位 / 30 秒、`issuer=ChainSight`，与 Google Authenticator 默认参数一致；**待补**后台“账号安全”页（P1-09）与 `AUTH_REQUIRE_ADMIN_MFA` 开启演练（P1-10） |

| **D8** | 「管理员」的权限范围 | **最高、所有权限**（2026-09-18 需求方确定） | 迁移 `0070`：`owner` 补齐 `admin:user:delete` → 实测 owner 权限数 **179 / 179**；并在迁移里自检「owner 必须持有目录全集」，把该口径固化成不变量。`platform_admin`/`security_admin` 保留删除权（0068） |

**仍待确认（都不阻塞 P0）**：

- **D2 的分治口径**：按附录 A.6 的建议执行吗（acme-lab 留作验收环境不迁；`default` 只把 3 个真实案件归 `admin@local`，其余 23 个脚本产物归档）？
- **§3.5.1 的基础权限清单**：逐条过一遍，要加要减直接说，我按你的口径定 `basic_user`。

---

## 附录 A：现状证据（2026-09-18 实测）

### A.1 入口探测（VM `192.168.31.152`）

```text
# 公网 nginx（Host: aurore.online）
GET /api/v1/cases?limit=1                                → 401
GET /api/v1/cases?limit=1  -e https://aurore.online/     → 200（9234 B 真实数据）
GET /                                                  → 200（Trace Desk · 链上调查工作台）
GET /app-v2/login                                      → 200（ChainSight 调查工作台）
GET /admin/login                                       → 200（ChainSight 后台管理）

# 局域网 vite（:3000，宿主机 0.0.0.0）
GET /                        → 200（老控制台；<title>Trace Desk · 链上调查工作台</title>）
GET /login                   → 200（**同一个老控制台首页**，非登录页）
GET /admin/login             → 200（**同上**）
GET /app-v2/login            → 200（**同上**）
GET /api/v1/cases?limit=1    → 200（真实数据）
GET /api/v2/cases?limit=1    → 200
GET /api/admin/v1/jobs       → 403
GET /api/auth/v1/session     → 401 AUTHN_SESSION_MISSING
```

### A.2 注入规则（`/etc/nginx/conf.d/legacy-console.conf`，脱敏）

```nginx
map $cookie_cs_user_session$cookie_cs_admin_session $legacy_console_nocookie { "" "1"; default "0"; }
map $http_referer $legacy_console_from_legacy_app {
    default                                          "0";
    "~*^https?://[^/]+/(?:app-v2|admin|api)(?:/|$)"  "0";
    "~*^https?://[^/]+/"                             "1";
    ""                                               "0";
}
map "$legacy_console_nocookie$legacy_console_from_legacy_app" $legacy_console_inject { "11" "1"; default "0"; }
map "$legacy_console_inject:$request_uri" $legacy_console_value { default ""; "~^1:/api/v1/" "<TOKEN>"; }
```

### A.3 后端主体（api 进程环境，脱敏）

```text
LEGACY_CONSOLE_TOKEN_SHA256=<set>
LEGACY_CONSOLE_PERMISSIONS=investigation:list,investigation:read,investigation:create,report:list,subject:list,
  download:create,explorer:read,job:list:workspace,job:read,job:control,export:create,export:read,export:download,
  case:list,case:read,case:create,canvas:list,admin:provider:read,admin:cost:read,admin:performance:read,
  admin:performance:update,admin:dataset:read,admin:dataset:manage
LEGACY_CONSOLE_ORGANIZATION_ID=d3b152f2-44c0-441c-b047-be24c137a84b
LEGACY_CONSOLE_WORKSPACE_ID=000e31bb-8c48-435d-af97-95bbc75a496c
LEGACY_CONSOLE_IDENTITY_ID=6e192e52-d0aa-4e4a-9497-4953a332cfd3
LEGACY_CONSOLE_AUDIENCE=admin
```

### A.4 `/api/auth/v1` 路由清单（无 register）

`GET /methods`、`POST /login`、`POST /logout`、`POST /refresh`、`GET /session`、`GET /session/workspaces`、`POST /session/workspace`、`GET /sessions`、`DELETE /sessions/{id}`、`POST /sessions/revoke-all`、`GET /devices`、`PATCH /devices/{id}`、`POST /password/change`、`GET /oidc/start`、`GET /oidc/callback`、`POST /mfa/enroll/start`、`POST /mfa/enroll/confirm`、`POST /mfa/verify`、`POST /mfa/disable`
来源：`backend/internal/api/authv1.go`、`authmfa.go`；**没有** register/signup。

### A.5 线上租户与账号规模

```text
组织：11 个（default + 10 个验收夹具组织）
工作区：20 个；其中 default 工作区（000e31bb-…）30 名 active 成员 / 26 个案件
                         acme-lab（14dfbf1b-…）15 名成员 / 149 个案件
身份：47 个 active（仅 admin@local 为 is_platform_admin）；另有若干 disabled 夹具账号
角色：viewer / analyst / investigator / reviewer / auditor / owner / security_admin / platform_admin / api_client
迁移头：0065（`0065_role_read_permission_gaps.sql`）
```

---

### A.6 存量案件与对象的来源（回答“26 / 149 是哪来的”）

**`default` 工作区（`000e31bb-…`）26 个案件**：

| 来源 | 数量 | 明细 |
|---|---|---|
| 真人建的 | 3 | `盘古`（08-31）、`星链`（09-03）、`ChatGPT MCP`（09-09） |
| 画布功能验收脚本 | 20 | `画布验收-desktop-<时间戳>` / `画布验收-mobile-<时间戳>`（09-09，成对出现） |
| 其他验收脚本与夹具 | 3 | `Phase3验收-20260909210812`、`夹具案件（默认组织）`、`s7-owner-probe`（draft，`admin@local`） |

**`acme-lab` 工作区（`14dfbf1b-…`）149 个案件**：

| 来源 | 数量 | 明细 |
|---|---|---|
| 夹具身份 `fixture-investigator@fixture.local` | 148 | 命名 `S4-E2E-20260912-*`、`S4-UI-<时间戳>`，2026-09-12 ~ 09-16，Sprint 4 自动化验收产物 |
| 夹具初始化 | 1 | `夹具案件（acme-lab）` |

**同工作区的其他私有对象**（同一次实测）：

| 对象 | `default` | `acme-lab` |
|---|---|---|
| 调查 investigations | 24 | 176 |
| 下载任务 download_jobs | 2363 | 108 |
| 导出 exports | 136 | 171 |
| 画布 investigation_canvases | 42 | 74 |

**两个要点**：

1. **现有“用户私有数据”几乎全是验收夹具与脚本产物**：`acme-lab` 组织名本身就写着「ACME 调查组（测试夹具）」，其 148 个案件全部挂在夹具身份名下；`default` 的 26 个里也只有 3 个是真人建的。
2. **`default` 里 23 个案件的 `created_by` 为空**（老控制台时期未记录归属）——这正是 §6 R1 的风险来源：迁移时无法自动判定归属，只能按命名与时间人工指认。

**由证据得出的建议（D2）**：

1. `acme-lab` 整个组织保留为**验收环境**，不进公网、不迁个人空间；
2. `default` 的 3 个真实案件归 `admin@local` 的个人空间，其余 23 个脚本产物归档（保留可查，不参与新用户视图）；
3. `download_jobs` 2363 条按 `created_by_identity`（`0050` 已有列）归属到人，判不出的留在共享工作区并单列清单。

## 附录 B：相关文档与代码索引

| 主题 | 位置 |
|---|---|
| 双系统总计划 | `docs/dual-system-development-plan.md`（§18.2 登录页 U-AUTH-01、§21.4 认证 API） |
| 验收标准 | `docs/dual-system-completion-acceptance-criteria.md` §6.1 登录与个人安全、§7.2 会话/登录/审计 |
| 认证与 IAM 实现 | `backend/internal/api/authv1.go`、`authmfa.go`、`authmw.go`、`mustchangepw.go`、`backend/internal/iam/` |
| 后台身份/组织管理 | `backend/internal/api/sprint6_identity.go`、`adminorg.go`、`adminv1.go`、`admin_delete_user.go` |
| 用户层 API | `backend/internal/api/userv2.go`（explorer / search / downloads / exports / canvas） |
| IAM / RBAC 迁移 | `backend/migrations/postgres/0040_iam.sql`、`0041_rbac.sql`、`0050_s5_job_identity_scope.sql`、`0060–0070` |
| 老控制台解耦（历史） | `docs/legacy-console-decoupling.md` |
| 入口配置 | `deploy/nginx/blockchain-platform.conf`、`deploy/nginx/legacy-console.conf.template`、`frontend/vite.config.ts` |
| 前端规范 | `docs/code-standards.md`、`AGENTS.md`、`docs/adr/AD-009-dialog-only-notifications.md` |
| 面清单（需同步） | `docs/implementation/current-surface-inventory.md` |
| 发布证据（2026-09-18） | `acceptance-evidence/dual-system-v1/RELEASE-20260918-API-BINARY.md` |
| 端到端验证脚本 | `acceptance-evidence/dual-system-v1/tests/verify-user-lifecycle-isolation.py` |
| 夹具清理证据 | `acceptance-evidence/dual-system-v1/ACME-FIXTURE-CLEANUP-20260918.md` |