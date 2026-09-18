# 管理员手册（后台管理系统 ChainSight Admin）

> 适用对象：`owner`、`platform_admin`、`security_admin`（以及只读的 `auditor`）。
> 入口：后台管理系统部署在 **`/admin/`** 前缀下（灰度期与用户层系统 `/app-v2/`、旧工作台 `/` 并存）。
> 事实来源：`frontend/apps/admin-console/src/App.tsx` 与各页面组件、三个 `routes/*.routes.tsx`、
> `backend/internal/api/` 路由注册、`backend/internal/policy/catalog.go`（2026-09-12 实测）。
> 关联文档：`docs/manuals/permissions.md`（权限目录与角色）、`docs/manuals/operations.md`（运行手册）、
> `docs/manuals/troubleshooting.md`（故障处置）、`docs/adr/ADR-017`（控制面契约与运行期降权）。

---

## 1. 入口、登录与访问边界

### 1.1 登录

1. 打开 `https://<域名>/admin/`（内网直连：`http://127.0.0.1:3002/admin/`）。
2. 未登录时只渲染登录页（卡片标题 **ChainSight 后台管理**）。
3. 输入邮箱与密码，点击 **登录后台**（提交中为「正在登录…」）；另有 **访问边界说明**（弹窗）、
   **返回用户层系统** 两个入口。

### 1.2 后台访问边界（务必理解）

- 后台权限全部以 `admin:` 开头；此类权限**额外要求会话受众为 `admin`**
  （`policy.Engine.Authorize`：`IsAdminPermission(key) && audience != "admin" → audience_mismatch`）。
  用用户层会话（`cs_user_session`）调用 `/api/admin/v1/*` 一律 403。
- 两个前端使用**独立 Cookie**：用户层 `cs_user_session`、后台 `cs_admin_session`；
  Nginx 以 `/admin/` 反代到 `127.0.0.1:3002`，以 `/app-v2/` 反代到 `127.0.0.1:3001`。
- 会话超时与 step-up 要求见 §3.3。
- `viewer`/`analyst`/`investigator`/`reviewer`/`api_client` 登录 `/admin/` 后，页面按权限隐藏，
  绝大部分页面为空或不可见——这不是缺陷。

### 1.3 未登录自检（实测）

```bash
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8080/api/admin/v1/overview   # 期望 401
```

---

## 2. 导航结构（与代码一致）

后台导航在代码中按分组拼装，**存在"路径去重"副作用**，请以实际菜单文案为准：

| 分组 | 菜单项（实际文案） | 路由 |
|---|---|---|
| 运营总览 | 运营总览 | `/overview` |
| 运营总览 | 可观测性 | `/observability` |
| 运营总览 | 系统健康 | `/system` |
| 组织与权限 | 组织与工作区 | `/organizations` |
| 组织与权限 | 用户与成员 | `/users` |
| 组织与权限 | 角色与权限 | `/roles` |
| 组织与权限 | 登录与会话 | `/sessions` |
| 组织与权限 | 登录记录 | `/login-events` |
| 任务与数据 | 全局任务控制 | `/jobs` |
| 任务与数据 | 导出与对象存储 | `/storage`（**占位页，未实现**） |
| 任务与数据 | Coverage 与数据质量 | `/data-quality`（**占位页，未实现**） |
| 任务与数据 | 保留与删除 | `/data/retention` |
| Provider 与 API | Provider 账户 | `/providers` |
| Provider 与 API | 路由策略 | `/providers/routing` |
| Provider 与 API | 性能控制 | `/runtime/performance` |
| Provider 与 API | API Key | `/api-keys` |
| Provider 与 API | 请求审计 | `/requests` |
| Provider 与 API | 成本 | `/costs` |
| Provider 与 API | 预算 | `/budgets` |
| 安全与审计 | 审计日志 | `/audit` |
| 安全与审计 | 完整性边界 | `/audit/integrity` |
| 安全与审计 | 告警中心 | `/alerts` |
| 安全与审计 | 风险事件 | `/risk`（**占位页，未实现**） |
| 安全与审计 | **系统配置** | `/settings`（S6 的「系统设置」菜单项因路径已存在被跳过，文案以「系统配置」为准） |
| 安全与审计 | 功能开关 | `/feature-flags` |
| 系统治理 | 实时控制面 | `/control` |
| 系统治理 | Kill Switch | `/control/kill-switch` |
| 系统治理 | 备份与恢复 | `/system/backups` |
| 系统治理 | 版本与迁移 | `/system/version` |

> 三个占位页（`/storage`、`/data-quality`、`/risk`）只有「查看说明」按钮，
> 页面标注相应能力尚未实现（`NOT_IMPLEMENTED`），不要据此判断系统故障。

---

## 3. 后台交互约定（所有页面一致）

### 3.1 提示一律是弹窗

与用户层相同：页面正文只放业务数据、字段标签、导航与操作控件；
警告、说明、成功、错误、风险、确认全部走弹窗（`packages/ui/Dialog.tsx`，AD-009）。
每个页面右上角的「**口径**」按钮就是这个约定的体现——它打开解释当前页面统计口径的信息弹窗。

弹窗等级与角标：`info→提示`、`success→已完成`、`warning→注意`、`error→错误`、`confirm→请确认`；
`notify` 固定单按钮 **知道了**，`confirm` 默认 **取消**/**确认**。

### 3.2 高风险命令的固定四步链

后台所有命令型操作（`POST .../commands/{verb}`）都走同一条前端编排：

```text
确认弹窗 → 原因弹窗（标题「填写操作原因」，正文「原因会写入审计日志，无法为空。」，1–500 字）→
[可选] 二次身份确认弹窗（标题「二次身份确认」，按钮「确认身份并提交」）→ POST（带 Idempotency-Key）→
成功弹窗「操作已提交」/ 失败弹窗「命令执行失败」（详情含 Trace 与错误码）
```

- **原因必填**：后端同样校验 `reason` 非空（为空返回 400）。
- **step-up（二次身份确认）**：请求体携带 `step_up_password`——即**重新输入当前账号密码**。
  这不是 MFA：审计记 `step_up_method=password`。MFA/TOTP 已于 2026-09-16 实现（第二因素，需账号自行注册认证器），
  但它不能替代 step-up —— 高风险命令仍要求重新输入当前账号密码。
- 失败码：`STEP_UP_REQUIRED`（403，未提供）、`STEP_UP_INVALID`（403，密码错误）、
  `STEP_UP_UNAVAILABLE`（503，密码服务不可用）。

#### 2.x.1 重置 MFA（`POST /api/admin/v1/users/{id}/commands/reset-mfa`）

用于「用户丢失认证器、无法完成登录第二步」的恢复场景 —— 没有这条命令时，启用过 MFA 的账号
一旦丢失认证器就再也登录不进来。

- 请求体：`{"confirm": true, "reason": "…", "step_up_password": "…"}`（原因与 step-up 都必填）。
- 行为：删除该账号的 TOTP 密钥与全部恢复码，并**吊销其全部会话**（否则旧会话会让重置形同虚设）。
- 页面会先弹**影响预览**（将删除什么、会吊销几个活跃会话、重置后该账号登录不再需要第二步）。
- 结果码：`409 CONFLICT_MFA_NOT_ENROLLED`（该账号本来就没启用 MFA，不做无意义的成功）、
  `412 CONFLICT_CONFIRMATION_REQUIRED`、`404 RESOURCE_NOT_FOUND`（跨组织不可见）、
  以及上表中的 step-up 三类失败码。
- 审计动作：`admin:user:reset_mfa`（before/after 记 `mfa_enrolled`，detail 记删除的恢复码数量与会话数）。

### 3.3 并发与冲突（`If-Match`）

配置类修改（`PATCH`）走乐观并发：

| 情况 | HTTP | 错误码 | 前端表现 |
|---|---|---|---|
| 未带 `If-Match` | 428 | `CONFLICT_REVISION_REQUIRED` | 提示重新加载 |
| 版本不匹配 | 412 | `CONFLICT_REVISION` | 弹窗追加「冲突：数据已被其他会话修改，请刷新后重试」 |
| 需要二次确认（部分写操作） | 428 | `CONFLICT_CONFIRMATION_REQUIRED` | 需在请求体带 `confirm: true` |

### 3.4 状态码与错误码速查

| HTTP | 典型 `error.code` | 处置 |
|---|---|---|
| 401 | `unauthenticated`、`AUTHN_SESSION_*` | 后台入口重新登录 |
| 403 | `permission_denied`（含 `reason`）、`TWO_PERSON_REQUIRED`、`AUTHZ_FORBIDDEN` | 补权限或换人审批 |
| 404 | `NOT_FOUND`、`RESOURCE_NOT_FOUND` | 对象不存在或无权（统一按不可见处理） |
| 409 | `CONFLICT_STATE`、`CONFLICT_DUPLICATE`、`NO_GAP_DETECTED` | 当前状态不允许该操作 |
| 412 | `CONFLICT_REVISION`、`APPROVAL_REQUIRED`、`DELETION_DISABLED` | 见对应章节 |
| 422 | `CONFLICT_LAST_OWNER`、`VALIDATION_UNKNOWN_ROLE`、`VALIDATION_SELF_LOCKOUT` | 业务规则拒绝 |
| 501 | `NOT_IMPLEMENTED`、`OIDC_NOT_CONFIGURED`（MFA 已于 2026-09-16 实现，不再 501） | 能力未实现，非缺陷 |
| 503 | `INTERNAL_NOT_READY`、`STEP_UP_UNAVAILABLE` | 依赖未就绪，查 `/system` |

> 注意：中间件（权限/CSRF）返回**小写蛇形**码（`unauthenticated`、`permission_denied`、`csrf_failed`），
> 业务处理器返回**大写下划线**码（`AUTHZ_PERMISSION_DENIED`、`INTERNAL_ERROR` 等）。排障时两种都要匹配。

---

## 4. 角色与权限速查

完整目录见 `docs/manuals/permissions.md`（**177 条权限、9 个系统角色**）。后台相关的关键事实：

| 角色 | 授权条数 | 后台能力 |
|---|---|---|
| `owner` | 177（全部） | 无限制 |
| `platform_admin` | 70 | 平台日常运维：Provider/路由/性能/成本/预算/告警/配置/开关/保留/备份/任务控制 |
| `security_admin` | 37 | 安全控制：Kill Switch、审批决定、API Key 生命周期、风险处置、配置修改 |
| `auditor` | 30 | **只读**：审计、登录记录、会话、风险、配置历史、备份、版本、导出 |
| 其它角色 | 4–85 | 无后台页面（缺 `admin:*` 权限） |

需要注意的三处"读键承载写动作"（实现事实，非笔误修正前以代码为准）：

- `POST /api/admin/v1/providers/{id}/commands/{command}` 声明的是 `admin:provider:read`
  （`sprint6_provider.go`），而目录中另有 `admin:provider:key:rotate`（critical）；
- `POST /api/admin/v1/backups/{id}/commands/verify` 用 `admin:backup:read`；
- `POST /api/admin/v1/jobs/{id}/commands/{command}` 外层是 `admin:job:control`，
  `retry` 在处理器内二次校验 `admin:job:retry`。

因此在授权时**不要只看目录风险等级**，还要看实际路由声明的键。

---

## 5. 逐页规格

### 5.1 运营总览（`/overview`）

| 项 | 内容 |
|---|---|
| 用途 | 系统健康、P0/P1 告警、队列、失败任务、成本配额、请求趋势、最近高风险动作与登录异常 |
| 主要操作 | **刷新**；点击指标进入预置筛选页面 |
| 后端 | `GET /api/admin/v1/overview`、`GET /api/admin/v1/observability/overview` |
| 权限 | `admin:dashboard:read` |
| 审批/step-up | 无 |
| 注意 | 所有指标带 `as_of`、窗口与来源；不同窗口的数字不可直接比较；系统健康只展示真实探针结果 |

### 5.2 系统健康（`/system`）

| 项 | 内容 |
|---|---|
| 用途 | 依赖组件（API/Worker/PG/CH/MinIO/NATS/Provider）健康 |
| 主要操作 | **刷新** |
| 后端 | `GET /api/admin/v1/system/health` |
| 权限 | `admin:system:read` |

### 5.3 可观测性（`/observability`）

| 项 | 内容 |
|---|---|
| 用途 | SLO、指标、告警视图；可手工触发一次告警评估 |
| 主要操作 | **刷新**、**评估并写入告警**（提交中「评估中…」）、**说明**弹窗 |
| 后端 | `/observability/overview`、`/observability/slo`、`/observability/metrics`、`/observability/alerts`、`POST /observability/alerts/evaluate` |
| 权限 | `admin:metrics:read`（overview 用 `admin:dashboard:read`） |
| 注意 | 「评估并写入告警」会写告警记录，属写操作 |

### 5.4 组织与工作区（`/organizations`）

| 项 | 内容 |
|---|---|
| 用途 | 组织、工作区、成员、角色绑定的管理 |
| 主要操作 | **刷新**、**新建工作区**、**切换/当前**、**重命名**、**管理角色** |
| 后端 | `/organizations`、`/organizations/{id}`、`/workspaces`、`/members`、`/roles`、`/users/{id}/roles`、`/users/{id}/roles/{roleId}` |
| 权限 | 页面 `admin:org:read`；写操作 `admin:org:manage` / `admin:workspace:manage` / `admin:member:manage` |
| 审批 | 写操作需 `confirm: true`（缺省 428 `CONFLICT_CONFIRMATION_REQUIRED`），并填原因 |
| 常见错误 | `VALIDATION_UNKNOWN_ROLE`（422，角色键不存在）、`CONFLICT_DUPLICATE`（409） |

### 5.5 用户与成员（`/users`）

| 项 | 内容 |
|---|---|
| 用途 | 用户列表、邀请用户、启用/停用、查看用户详情与有效权限 |
| 主要操作 | **邀请用户**、**查询**、**启用**、**停用** |
| 后端 | `GET/POST /users`、`GET/PATCH /users/{id}`、`POST /users/{id}/commands/{disable|enable|reset-password|reset-mfa}`、`GET /users/{id}/effective-permissions`、`GET /users/{id}/impact-preview` |
| 权限 | 页面 `admin:user:read`；创建 `admin:user:create`；停用/启用 `admin:user:disable`（critical）；改资料 `admin:user:update`；重置密码 `admin:user:password:reset`（critical）；**重置 MFA `admin:user:reset_mfa`（critical）**；有效权限 `admin:permission:read` |
| 审批 | 停用/启用/重置密码/重置 MFA 需 `confirm: true` + 原因；**停用前必须看影响预览**（其运行任务、待审批、拥有的案件、API Key） |
| 常见错误 | `VALIDATION_SELF_LOCKOUT`（422，不能停用自己）；`CONFLICT_LAST_OWNER`（422，移除最后一个 Owner） |
| 安全要求 | 停用用户后其会话与角色绑定立即失效（`authz_version` + 会话撤销），要求 5 秒内生效 |

### 5.6 角色与权限（`/roles`）

| 项 | 内容 |
|---|---|
| 用途 | 查看系统/自定义角色、创建组织角色、查看权限矩阵 |
| 主要操作 | **新建组织角色**、**查看权限** |
| 后端 | `GET /roles`、`GET /roles/{id}/permissions`、`GET /permissions` |
| 权限 | 页面 `admin:role:read`；权限目录 `admin:permission:read`；**管理角色与权限为 `admin:role:manage`（critical）** |
| 注意 | 系统角色（`is_system=true`）不可直接修改；为自己移除最后一个 Owner 权限必须被拒绝；组织至少保留一个可用 Owner |

### 5.7 登录与会话（`/sessions`）

| 项 | 内容 |
|---|---|
| 用途 | 活跃会话列表、强制下线 |
| 主要操作 | **刷新**、**强制下线** |
| 后端 | `GET /sessions`、`POST /sessions/{id}/commands/revoke` |
| 权限 | 查看 `admin:session:read`；下线 `admin:session:revoke`（critical） |
| 审批 | `confirm: true` + **原因必填** |
| 注意 | 撤销自己的当前会话会立即把自己登出，界面会明确提示影响 |

### 5.8 登录记录（`/login-events`）

| 项 | 内容 |
|---|---|
| 用途 | 成功/失败登录、IP、设备、MFA、风险、Trace |
| 主要操作 | **刷新**、**查询**、**详情** |
| 后端 | `GET /api/admin/v1/login-events` |
| 权限 | `admin:login-event:read`（`sensitive`，读取会记敏感访问审计） |
| 注意 | 设备信息区分「客户端声明」与「服务端确认」；地区是推断值；批量失败会产生 risk event，**不能修改原始登录事件** |

### 5.9 审计日志（`/audit`）与完整性边界（`/audit/integrity`）

| 项 | 内容 |
|---|---|
| 用途 | 审计事件查询/详情；哈希链校验；完整性缺口检测、封存与锚点核对 |
| 主要操作 | **刷新**、**校验哈希链**（提交中「校验中…」）、**查询**、**详情**；**检测缺口**、**逐项校验锚点**、**生成当日锚点** |
| 后端 | `/audit/events`、`/audit/events/{id}`、`/audit/chains`、`POST /audit/verify`；`/audit/integrity`、`POST /audit/integrity/preview`、`/audit/integrity/incidents`、`POST /audit/integrity/incidents/requests`、`.../{id}/approve|reject|execute`、`GET/POST /audit/anchors` |
| 权限 | 读事件 `admin:audit:read`（`sensitive`）；校验与锚点读取 `admin:audit:verify`；**封存与生成锚点 `admin:audit:seal`（critical，仅 owner/security_admin）**；审批 `admin:approval:decide` |
| 审批/step-up | 封存请求需 step-up + 原因，并需**第二人**审批（请求人不能自审批，审批 TTL 24 小时）；执行封存再次 step-up |
| 注意 | 本环境 `audit` 链已有封存缺口 `INC-AUDIT-GAP-1-2343`（`integrity_status` 永远为 `sealed_incident`），并有 2 条已声明的**内容哈希不可复算行**；`login` 链有 8 条已声明的**历史缺失行**（状态 `declared_events`）。**声明与封存行都不可删除、不可改写**：声明只登记事实，不改变任何历史行，也不代表链完整。审计事件的详细口径见 `docs/manuals/auditor-guide.md` §3.4（ADR-019） |

### 5.10 全局任务控制（`/jobs`）与任务诊断（`/jobs/:jobId`）

| 项 | 内容 |
|---|---|
| 用途 | 跨组织任务列表与固定诊断链 |
| 主要操作 | 列表：筛选口径、**刷新**、**诊断**、上一页/下一页；详情：固定链口径、**刷新**、命令（暂停/恢复/取消/重试） |
| 后端 | `GET /jobs`、`GET /jobs/{id}/diagnostics`、`POST /jobs/{id}/commands/{command}` |
| 权限 | 列表 `admin:job:list`；诊断 `admin:job:read`；控制 `admin:job:control`（retry 需 `admin:job:retry`）；取消任意任务为 `admin:job:cancel`（critical） |
| 诊断链（固定七层） | `DownloadJob → SubjectJob → DatasetJob → Shard/Checkpoint → Provider request/usage → Raw object/SHA-256 → ClickHouse rows/Coverage` |
| 注意 | 「推进至链上时间」显示最后成功分页游标对应时间，不是计划 Shard 边界；重试默认只重试可重试的失败单元；完整重下必须显式选择 `reuse_policy=refresh` 并说明成本 |

### 5.11 Provider 账户（`/providers`、`/providers/:provider`）

| 项 | 内容 |
|---|---|
| 用途 | Provider 健康、账户、能力、请求、错误、成本、路由历史、配置历史、密钥摘要 |
| 主要操作 | **刷新**、**详情**、**测试连接**、**连接**、**断开**、**轮换密钥**、**刷新密钥列表**、**返回列表** |
| 后端 | `GET /providers`、`GET /providers/{key}`、`POST /providers/{key}/commands/{test|connect|disconnect|rotate-secret}`、`GET /providers/{key}/secrets`、`PATCH /providers/{key}` |
| 权限 | 读取与命令：`admin:provider:read`；修改配置：`admin:provider:update`（并需 `If-Match`） |
| 审批/step-up | `disconnect` 且受影响运行中任务 > 0 → step-up；`rotate-secret` **无条件 step-up**；两者均需原因 |
| 注意 | 密钥**只显示摘要/前缀/指纹**，永不回显明文（`plaintext_returned:false`）；轮换只接受 `secret_ref` 引用名，拒绝明文；断开前必须先看影响预览（受影响链、Dataset、任务与 fallback 能力） |

### 5.12 路由策略（`/providers/routing`）

| 项 | 内容 |
|---|---|
| 用途 | 链/Dataset 的 Provider 优先级、fallback、限流、熔断阈值、灰度比例 |
| 主要操作 | **刷新**、上一页/下一页、**口径** |
| 后端 | `GET /routing-policies`、`GET/PATCH /routing-policies/{id}` |
| 权限 | 读 `admin:routing:read`；改 `admin:routing:update`（需 `If-Match`） |
| 注意 | `rollout_percent` 字段存在且被管理端读写，但**当前未接入请求路径**（无消费点），不要据此认为灰度已在线上生效。Provider 路由的灰度请使用功能开关 `admin.provider_registry`（同样尚未接线到请求路径，见 §5.19） |

### 5.13 性能控制（`/runtime/performance`）

| 项 | 内容 |
|---|---|
| 用途 | SAFE/FAST/TURBO 性能模式、并发与内存、背压、历史 |
| 主要操作 | **刷新**、**口径**；预览 / 应用 / 回滚 |
| 后端 | `GET /runtime/performance`、`POST /runtime/performance/preview`、`POST /runtime/performance/commands/{apply|rollback}` |
| 权限 | 读 `admin:performance:read`；改 `admin:performance:update` |
| 审批/step-up | **仅当目标模式 ≥ turbo 时**要求 step-up（条件式）；回滚同样受保护 |
| 注意 | 必须同时显示 `requested / effective / reason`，禁止只显示请求模式 |

### 5.14 API / MCP Key（`/api-keys`）

| 项 | 内容 |
|---|---|
| 用途 | Key 的 scope、IP、限额、过期、轮换、吊销 |
| 主要操作 | **刷新**、上一页/下一页、**口径**；创建、轮换、吊销 |
| 后端 | `GET/POST /api-keys`、`POST /api-keys/{id}/commands/{rotate|revoke}` |
| 权限 | 读 `admin:apikey:read`（`sensitive`）；创建 `admin:apikey:create`；轮换/吊销（路由声明）`admin:apikey:revoke` |
| 审批/step-up | 轮换/吊销**无条件 step-up** + 原因必填 |
| 注意 | 明文密钥**只在创建响应中出现一次**；数据库只存 SHA-256 摘要 |

### 5.15 请求审计（`/requests`）

| 项 | 内容 |
|---|---|
| 用途 | Provider / Actions / MCP / 用户 API 的请求记录、耗时、错误、Trace 关联 |
| 主要操作 | **口径**、**刷新**、**查询**、**重置**、**Trace 关联** |
| 后端 | `GET /requests`、`GET /requests/{id}` |
| 权限 | `admin:request:read`（`sensitive`） |
| 注意 | 响应已脱敏（URL query、Authorization、Cookie 与响应秘密字段不进日志） |

### 5.16 成本（`/costs`）与预算（`/budgets`）

| 项 | 内容 |
|---|---|
| 用途 | Provider/组织/用途维度的成本与趋势；预算周期、维度、阈值、动作 |
| 主要操作 | 成本：**口径**、**刷新**、明细；预算：**口径**、**刷新**、编辑 |
| 后端 | `GET /costs/summary?days=`、`GET /costs`、`GET/POST /budgets`、`PATCH /budgets/{id}`、`POST /budgets/{id}/commands/evaluate` |
| 权限 | 成本 `admin:cost:read`；预算读 `admin:budget:read`；预算写 `admin:budget:update`（critical） |
| 审批 | 预算修改需 `If-Match`；评估命令用 `admin:budget:read` |
| 注意 | 金额为**十进制字符串**，不是 JSON 数字（避免精度丢失） |

### 5.17 告警中心（`/alerts`）

| 项 | 内容 |
|---|---|
| 用途 | P0–P3 告警、关联链、认领/抑制/解决 |
| 主要操作 | **口径**、**刷新**；命令：acknowledge / mute / resolve |
| 后端 | `GET /alerts`、`GET /alerts/{id}`、`POST /alerts/{id}/commands/{command}` |
| 权限 | 读 `admin:alert:read`；处置 `admin:alert:manage` |
| 注意 | 告警处置产生**新事件关联原事件**，不修改原始记录；P0 定义见 `operations.md` 的告警级别表 |

### 5.18 系统配置（`/settings`）

| 项 | 内容 |
|---|---|
| 用途 | 分组配置、来源、当前版本、修改历史（diff/回滚） |
| 主要操作 | **口径**、**刷新**；修改配置项、查看历史、回滚 |
| 后端 | `GET /settings`、`GET /config/settings`、`GET /config/revisions`、`PATCH /settings/{key}`、`POST /settings/{key}/commands/{command}` |
| 权限 | 读 `admin:config:read`；历史 `admin:config:history:read`；改 `admin:config:update`（critical） |
| 审批/step-up | 修改需 `If-Match` + 原因；**当配置项是 secret / secret-ref 时无条件 step-up**；回滚到含 secret 的版本同样 step-up |
| 注意 | 配置只保存 `secret_ref`，不保存明文密钥 |

### 5.19 功能开关（`/feature-flags`）

| 项 | 内容 |
|---|---|
| 用途 | 环境与作用域灰度、百分比、试算、回滚；`scope_type` 取值 `global|organization|workspace|user`（后端校验枚举） |
| 主要操作 | **口径**、**刷新**；新增/修改开关、查看评估记录 |
| 后端 | `GET/POST /feature-flags`、`PATCH /feature-flags/{key}`、`GET /feature-flags/{key}/evaluations`、`POST /feature-flags/{key}/commands/{command}` |
| 权限 | 读 `admin:flag:read`；写 `admin:flag:update` |
| 审批 | 写入需 `If-Match` |
| 注意 | 分桶是**确定性**的（`sha256(key:subject_id)` 前两字节 % 100 与 percentage 比较），同一主体结果稳定；`100`/`0` 走全量/零灰度分支。开关**已在真实请求路径上求值**（ADR-021，2026-09-16 起），关闭时对应接口返回 `503 FEATURE_DISABLED` |
| 已接线的开关 | `user.explorer_v2` → 用户层 Explorer 全部读端点；`admin.alert_center` → 后台告警列表/详情/命令；`admin.control_plane_v2` → 控制面**只读**视图（`/control/state`、`/control/actions`、`/control/policies` GET、`/control/kill-switch/requests` GET、`/control/kill-switch/health`） |
| **不接线的路径（硬性边界）** | Kill Switch 的影响预览、发起、审批、执行、恢复：安全与恢复能力必须始终可用，不得被功能开关灰度掉 |
| 关闭后的行为 | 503 + `error.code=FEATURE_DISABLED`，`details` 带 `flag` / `reason`（`disabled`、`scope_mismatch`、`environment_mismatch`、`scope_match_zero_rollout`、`percentage_bucket_miss`、`flag_not_found`、`evaluation_error`）/ `revision`；**不会**静默返回空数据 |
| 生效时延 | 后台写入（create/update/rollback）后缓存立即失效；其他进程最长 10 秒（TTL） |
| 评估记录 | 拒绝时**同步**写入 `feature_flag_evaluations`（subject=真实调用者、reason、revision），放行时异步写入并按每分钟去重；因此"线上按哪个 revision 放行/拒绝了谁"可直接查询 |

### 5.20 实时控制面（`/control`）

| 项 | 内容 |
|---|---|
| 用途 | 保护状态、队列、熔断、自动保护开关、动作历史 |
| 主要操作 | **口径**、**刷新**、**详情** |
| 后端 | `GET /control/state`、`GET /control/actions`、`GET /control/policies`、`PATCH /control/policies`、`GET /control/kill-switch/health` |
| 权限 | `admin:killswitch:read`；修改控制策略 `admin:killswitch:engage`（**无条件 step-up** + 原因必填） |

### 5.21 Kill Switch（`/control/kill-switch`）

| 项 | 内容 |
|---|---|
| 用途 | 全局保护开关的预览、请求、双人审批、执行与恢复 |
| 主要操作 | **口径**、**刷新**、**本人提交·查看规则**、**详情**、**时间线**、上一页/下一页 |
| 后端 | `POST /control/kill-switch/preview`、`GET/POST /control/kill-switch/requests`、`GET /control/kill-switch/requests/{id}`、`POST .../{id}/approve|reject|execute|restore`、`GET /control/kill-switch/health`、`GET /approvals` |
| 权限 | 读 `admin:killswitch:read`；发起/执行 `admin:killswitch:engage`；恢复 `admin:killswitch:release`；审批 `admin:approval:decide` |
| 审批/step-up | 发起：step-up + 原因 + 影响预览；审批：**必须由另一人**（请求人自审批 → 403 `TWO_PERSON_REQUIRED`，数据库 CHECK 兜底）；执行/恢复：需已批准 + step-up |
| 实际效果 | 执行会 `UPDATE api_control_settings SET kill_switch=$1, background_paused=$1`，随后跑真实健康探针（PG/CH/NATS/MinIO/未关闭告警）；当前环境中的实际消费点是 token metadata 后台回填（`tokenmeta/backfill.go` 读取 `background_paused OR kill_switch`） |
| 注意 | 默认不删除任务、不清空队列、不丢弃 Checkpoint；请求与审批**不能是同一用户** |

### 5.22 备份与恢复（`/system/backups`）

| 项 | 内容 |
|---|---|
| 用途 | 备份列表、创建、校验；恢复预览与登记 |
| 主要操作 | **口径**、**创建备份**、**校验**、上一页/下一页、**详情** |
| 后端 | `GET/POST /backups`、`POST /backups/{id}/commands/verify`、`GET /restores`、`POST /restores/preview`、`POST /restores` |
| 权限 | 读 `admin:backup:read`；创建 `admin:backup:create`；恢复 `admin:backup:restore`（critical） |
| 审批/step-up | `POST /restores` 需 step-up + 审批（`system.restore`，TTL 4 小时） |
| **重要现状** | `POST /backups` 的 `target` 虽接受 `postgres|clickhouse|minio|config|all`，但**实现只生成配置快照**（`configuration_settings` + `feature_flags` 的 JSON，写 MinIO `backups/config/YYYY/MM/DD/<id>.json`）；`POST /restores` **只登记 queued 请求，没有执行恢复的端点**。数据库/对象存储的物理备份与恢复需按 `operations.md` 的手工流程执行 |

### 5.23 保留与删除（`/data/retention`）

| 项 | 内容 |
|---|---|
| 用途 | 保留策略、影响预览、删除请求与审批 |
| 主要操作 | **口径**、**编辑**、上一页/下一页；预览、提交删除请求、审批 |
| 后端 | `GET /retention-policies`、`POST /retention-policies/preview`、`PATCH /retention-policies/{id}`、`GET/POST /deletion-requests`、`POST /deletion-requests/{id}/commands/{command}` |
| 权限 | 读 `admin:retention:read`；改策略 `admin:retention:manage`（critical）；发起删除 `admin:deletion:request`；审批 `admin:deletion:approve` |
| 审批/step-up | 修改策略需 `If-Match`；发起删除需 step-up + 审批（TTL 4 小时）；审批需**另一人**（自审批 403） |
| 注意 | **实际删除默认关闭**：执行删除受配置开关 `governance.deletion.execution_enabled` 控制，关闭时返回 412 `DELETION_DISABLED` |

### 5.24 版本与迁移（`/system/version`）

| 项 | 内容 |
|---|---|
| 用途 | build SHA、schema 版本、组件兼容矩阵 |
| 主要操作 | **口径**、**刷新** |
| 后端 | `GET /api/admin/v1/system/version` |
| 权限 | `admin:version:read` |
| 注意 | 迁移操作本身（`admin:version:manage`，critical）**不在本环境开放**：迁移由运维在服务器上执行（见 `operations.md`） |

---

## 6. 高风险操作标准流程

计划 §20.7 的统一命令流程：

```text
preview -> confirm_reason -> step_up -> approval(optional) -> execute
        -> expired         -> rejected                   -> verify -> audit
```

适用于：Kill Switch、Provider 断开/密钥轮换、全局并发上调、批量用户停用、权限大范围变更、
数据删除、恢复备份、审计导出。

### 6.1 标准操作清单（每次高风险操作都走一遍）

- [ ] **预览影响**：调用 `/preview`（只读、可重复），记录影响清单与口径来源。
- [ ] **写明原因**：原因会写入审计，1–500 字，能说明"为什么现在做"。
- [ ] **二次身份确认**：输入当前账号密码（`step_up_password`）。
- [ ] **确认是否需要第二人**：Kill Switch / 删除 / 恢复 / 审计封存需要；
      确认审批人**不是自己**。
- [ ] **执行后验证**：Kill Switch 走 `/control/kill-switch/health`；Provider 走测试连接；
      性能模式核对 `requested/effective/reason`。
- [ ] **核对审计**：在 `/audit` 用 Trace ID 搜索本次操作，确认 `second_confirm` / `requires_review` 标记与审批引用齐全。

### 6.2 双人审批的硬约束

- `approval_requests` 有数据库约束 `CHECK (decided_by IS NULL OR decided_by <> requested_by)`（0051），
  应用层也先判定；二者同时存在。
- 违规返回 403 `TWO_PERSON_REQUIRED`。
- 审批 TTL：Kill Switch 1800 秒；删除与恢复 4 小时；审计完整性封存 24 小时。过期需重新发起。

---

## 7. 日常检查清单

**每日**

- [ ] `/overview`：P0/P1 告警为 0 或有明确处置记录；失败任务数量与原因。
- [ ] `/system`：所有依赖健康；`/observability` SLO 无违约。
- [ ] `/jobs`：无长时间不前进的任务；有卡点则进诊断链。
- [ ] `/login-events`：无异常失败激增；新设备/IP 跃迁已确认。
- [ ] `/audit/integrity`：`rows_inside_sealed_gap=0`、`internal_gap_count=0`。

**每周**

- [ ] `/providers`：错误率、配额、熔断状态；密钥摘要与最后使用时间。
- [ ] `/costs`、`/budgets`：是否接近阈值；超阈值动作是否已触发。
- [ ] `/requests`：错误率与 P95 异常。
- [ ] `/sessions`：长期未活动的会话是否需要下线。

**每月**

- [ ] `/system/backups`：创建并**校验**备份；执行一次恢复演练（按 `operations.md` 的手工流程）。
- [ ] `/roles`、`/users`：离职/转岗账号的停用与角色回收。
- [ ] `/settings`、`/feature-flags`：配置版本与开关是否符合发布计划。
- [ ] 审计导出一次并离线校验哈希（见 `auditor-guide.md` §7）。

---

## 8. 管理侧禁止事项

- **不得**绕过审批窗口直接改数据库来实现"解除"或"回滚"（会破坏 append-only 与哈希链）。
- **不得**修改或删除 `audit_chain_anchors` 与审计完整性边界行；系统在数据库层也会拒绝。
- **不得**用"页面存在"或"接口返回 200"作为功能完成的证据（§32 约束 10）。
- **不得**把密码重认证（step-up）描述为 MFA 已完成。
- **不得**在生产放宽 `DB_RUNTIME_ROLE`；缺失该变量会失去 AUDIT-003 的数据库层保证。
- 后台管理员**默认没有** `evidence:read`；读取案件正文需单独授权并触发敏感读取审计。

---

## 9. 待确认项

1. `Provider 命令`、`备份校验`、`任务诊断` 使用"读键"承载写动作，是否为设计取舍未在 ADR 中说明（见 `permissions.md`）。
2. `admin:job:retry` 等 59 个目录权限键没有任何 `s.require` 直接引用（部分仅在内层 `Has()` 校验），属预留还是漏接线未确认。
3. `/storage`、`/data-quality`、`/risk` 三个导航项对应占位页，未实现。
4. 管理端没有应用级移动端样式（只有 Shell 的 900px/1100px 断点），复杂管理操作应在 ≥900px 屏幕执行。
5. `approval_requests` 的双人约束除应用层与 CHECK 外是否还有其他兜底，未逐一验证。
