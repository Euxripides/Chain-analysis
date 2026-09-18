# 权限说明（RBAC、作用域、风险等级与 step-up）

> 适用范围：用户层系统（`/app-v2`）、后台管理系统（`/admin`）、`/api/auth/v1`、`/api/v2`、`/api/admin/v1`、MCP/Actions 密钥调用。
> 对应计划：`docs/dual-system-development-plan.md` §4（角色和权限模型）、§21.6、§23.4、§27.2（RBAC-001〜008）。
> 关联 ADR：AD-003（权限命名与强制声明）、ADR-017（权限键映射与运行期降权）、ADR-018（审计完整性事件边界）。

## 0. 事实来源与核对口径

本文档所有权限键、角色映射、风险等级均为**实测值**，来源三处必须一致：

| 来源 | 位置 | 作用 |
|---|---|---|
| 代码侧目录 | `backend/internal/policy/catalog.go` 的 `policy.Catalog` | 路由声明校验、风险等级、敏感标记 |
| 数据库种子 | `backend/migrations/postgres/0041_rbac.sql`、`0048_s4_permission_fix.sql`、`0053_s6_permission_grants.sql` | 权限行 + 角色授权（外键依赖） |
| 运行库 | `permissions` / `roles` / `role_permissions` 表 | 实际生效值 |

`backend/internal/policy/catalog_sql_test.go` 的 `TestCatalogMatchesMigrationSeed` 双向比对两侧，
任何只改一侧的提交都会失败。**权限目录共 177 条**
（由 `policy.Catalog`、SQL 种子、运行库三重核对一致）。

> **口径更正**：`docs/adr/AD-003-permission-naming-and-declaration.md` 结尾写「权限目录当前 163 条」，
> 该数字是 S3 时期的快照，已被 S4/S6 新增权限（`notification:list`、`admin:audit:seal`、
> `admin:killswitch:engage/release`、`admin:approval:decide` 等）超越。
> 以实测 177 条为准；引用 163 的旧文档不要作为执行依据。

### 0.1 一条命令核对当前生效目录

```bash
ssh euripides@192.168.31.152
export XDG_RUNTIME_DIR=/run/user/$(id -u)
cd /home/euripides/projects/blockchain-platform
set -a; . deploy/.env; set +a

# 权限目录条数（期望 177）
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 \
  psql -U blockchain -d blockchain_platform -Atc "select count(*) from permissions"

# 每个系统角色的授权条数（期望见下表"实时库"列）
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 \
  psql -U blockchain -d blockchain_platform -Atc \
  "select r.key||'='||count(*) from roles r join role_permissions rp on rp.role_id=r.id where r.is_system group by r.key order by r.key"
```

> 已在 2026-09-12 在 `euripides@192.168.31.152` 实测：权限目录 177 条；
> 角色授权条数 owner=177、platform_admin=70、security_admin=37、auditor=30、investigator=85、analyst=72、reviewer=55、viewer=33、api_client=4。

## 1. 授权模型总览

```text
请求 → Nginx（剥离伪造身份头） → Go API
                                      ├─ authmw：从会话 Cookie 解析身份（AD-002：身份只由服务端推导）
                                      ├─ policy.Engine.Authorize(principal, permission)   动作级
                                      └─ access.Guard（对象级：organization / workspace / case 收窄）
```

- **动作级**（`policy.Engine`）：回答"这个身份能不能执行这类操作"。主体 `Principal` 只包含服务端解析出的字段
  （`identity_id`、`organization_id`、`workspace_id`、`audience`、`roles`、`permissions`），
  绝不接受客户端自报字段（`backend/internal/policy/engine.go` 第 14–29 行）。
- **对象级**（`access.Guard`）：回答"能不能碰这一个对象"。跨租户对象统一按不可见处理，
  避免用 403/404 差异泄露对象存在性（§23.4）。
- **fail closed**：`policy.Engine.Authorize` 对未在 Catalog 登记的权限直接拒绝，`reason=permission_not_declared`；
  `policy.RiskOf` 对未登记权限按 `critical` 处理（`catalog.go` 第 239–245 行）。
- **缓存与撤销**：权限按 `identityID|organizationID` 缓存，默认 TTL 30 秒；
  `Engine.Invalidate` 必须按前缀清除（否则角色变更要等 TTL 才生效，违反 RBAC-006 的 5 秒要求）。

### 1.1 拒绝原因码（`Decision.Reason`）

| Reason | 含义 | 常见触发 |
|---|---|---|
| `permission_not_declared` | 权限键不在目录中 | 路由声明打错字 / 新增权限未同步 SQL |
| `unauthenticated` | 无有效会话 | 未登录、会话过期/被撤销 |
| `missing_permission` | 命中目录但主体未持有 | 角色映射缺失 |
| `audience_mismatch` | 用户层会话调用后台权限（`admin:` 前缀） | 用 `cs_user_session` 调 `/api/admin/v1/*` |

## 2. 权限目录（177 条）

### 2.1 分类统计

| 分类 | 条数 |
|---|---|
| 案件（`case`） | 18 |
| 调查（`investigation`） | 13 |
| 结论（Finding）（`finding`） | 5 |
| 证据（`evidence`） | 8 |
| 图谱（Canvas）（`canvas`） | 8 |
| 数据查询（Explorer）（`explorer`） | 7 |
| 下载（`download`） | 7 |
| 任务（`job`） | 7 |
| 导出（`export`） | 5 |
| 报告（`report`） | 8 |
| 协作与通知（`notification`） | 6 |
| 个人（self）（`self`） | 6 |
| 工作台（`dashboard`） | 1 |
| 后台 · 运营总览（`admin.ops`） | 1 |
| 后台 · 身份与权限（`admin.identity`） | 15 |
| 后台 · 安全与审计（`admin.security`） | 12 |
| 后台 · 控制面（`admin.control`） | 7 |
| 后台 · Provider 与成本（`admin.provider`） | 11 |
| 后台 · 任务与数据治理（`admin.data`） | 18 |
| 后台 · 系统（`admin.system`） | 14 |
| **合计** | **177** |

### 2.2 风险等级分布

| 风险等级 | 条数 | 处置含义 |
|---|---|---|
| `low` | 68 | 常规读写，无需 step-up |
| `medium` | 51 | 影响有限，需审计留痕 |
| `high` | 34 | 需原因说明；多数需影响预览 |
| `critical` | 24 | 需 step-up 重认证，部分需双人审批 |
| **合计** | **177** | |

### 2.3 敏感权限（`sensitive=true`，11 条）

敏感权限的读取需要额外审计记录，后台管理员默认**没有** `evidence:read`（§23.4）：

- `admin:export:list`
- `admin:export:read`
- `admin:request:read`
- `admin:apikey:read`
- `admin:audit:export`
- `admin:audit:read`
- `admin:login-event:read`
- `evidence:download`
- `evidence:sensitive:read`
- `export:download`
- `report:download`

### 2.4 全量目录


#### 案件（`case`，18 条）

| 权限键 | 说明 | 风险 | 敏感 |
|---|---|---|---|
| `case:activity:read` | 查看案件动态 | low |  |
| `case:archive` | 归档/恢复案件 | medium |  |
| `case:comment:create` | 发表案件评论 | low |  |
| `case:create` | 创建案件 | low |  |
| `case:delete` | 删除案件 | high |  |
| `case:list` | 列出案件 | low |  |
| `case:member:list` | 查看案件成员 | low |  |
| `case:member:manage` | 管理案件成员 | high |  |
| `case:read` | 查看案件 | low |  |
| `case:state:update` | 案件状态命令 | medium |  |
| `case:subject:manage` | 管理案件对象 | medium |  |
| `case:update` | 修改案件 | medium |  |
| `comment:create` | 发表案件评论 | low |  |
| `comment:list` | 查看案件评论 | low |  |
| `subject:create` | 新增案件对象 | medium |  |
| `subject:list` | 查看案件对象 | low |  |
| `subject:remove` | 移除案件对象 | high |  |
| `subject:update` | 修改案件对象 | medium |  |

#### 调查（`investigation`，13 条）

| 权限键 | 说明 | 风险 | 敏感 |
|---|---|---|---|
| `investigation:approve` | 复核调查结论 | high |  |
| `investigation:cancel` | 取消调查 | medium |  |
| `investigation:create` | 创建调查任务 | low |  |
| `investigation:delete` | 删除调查 | high |  |
| `investigation:list` | 列出调查任务 | low |  |
| `investigation:pause` | 暂停调查 | medium |  |
| `investigation:read` | 查看调查任务 | low |  |
| `investigation:resume` | 恢复调查 | medium |  |
| `investigation:retry` | 重试调查 | medium |  |
| `investigation:run` | 启动/暂停/继续调查 | medium |  |
| `investigation:start` | 启动调查 | medium |  |
| `investigation:submit_review` | 提交调查复核 | medium |  |
| `investigation:update` | 修改调查参数 | medium |  |

#### 结论（Finding）（`finding`，5 条）

| 权限键 | 说明 | 风险 | 敏感 |
|---|---|---|---|
| `finding:create` | 新增 Finding | low |  |
| `finding:list` | 列出 Finding | low |  |
| `finding:read` | 查看 Finding | low |  |
| `finding:review` | 复核 Finding | high |  |
| `finding:update` | 修改 Finding | medium |  |

#### 证据（`evidence`，8 条）

| 权限键 | 说明 | 风险 | 敏感 |
|---|---|---|---|
| `evidence:create` | 添加证据 | low |  |
| `evidence:delete` | 移除证据 | high |  |
| `evidence:download` | 下载证据文件 | medium | 是 |
| `evidence:list` | 列出证据 | low |  |
| `evidence:read` | 查看证据元数据 | low |  |
| `evidence:review` | 复核证据状态 | high |  |
| `evidence:sensitive:read` | 读取敏感证据正文 | critical | 是 |
| `evidence:snapshot:create` | 固化证据快照 | medium |  |

#### 图谱（Canvas）（`canvas`，8 条）

| 权限键 | 说明 | 风险 | 敏感 |
|---|---|---|---|
| `canvas:branch` | 创建图谱分支 | medium |  |
| `canvas:create` | 创建图谱画布 | low |  |
| `canvas:delete` | 删除图谱 | high |  |
| `canvas:list` | 列出图谱画布 | low |  |
| `canvas:read` | 查看图谱 | low |  |
| `canvas:snapshot` | 创建/查看图谱快照 | medium |  |
| `canvas:snapshot:create` | 创建图谱快照 | medium |  |
| `canvas:update` | 编辑图谱 | medium |  |

#### 数据查询（Explorer）（`explorer`，7 条）

| 权限键 | 说明 | 风险 | 敏感 |
|---|---|---|---|
| `address:read` | 查看地址详情 | low |  |
| `explorer:fetch` | 触发链上取数 | medium |  |
| `explorer:read` | 查询本地数据 | low |  |
| `search:global` | 使用全局搜索 | low |  |
| `search:read` | 执行全局搜索 | low |  |
| `token:read` | 查看 Token 元数据 | low |  |
| `transaction:read` | 查看交易详情 | low |  |

#### 下载（`download`，7 条）

| 权限键 | 说明 | 风险 | 敏感 |
|---|---|---|---|
| `download:control` | 暂停/继续/取消下载 | medium |  |
| `download:create` | 创建下载任务 | medium |  |
| `download:delete` | 删除下载任务数据 | high |  |
| `download:preview` | 预览 Coverage 与影响 | low |  |
| `download:read` | 查看下载任务 | low |  |
| `download:refresh` | 显式刷新并新增 Provider 取数 | high |  |
| `download:reuse` | 复用已有 Coverage 数据 | low |  |

#### 任务（`job`，7 条）

| 权限键 | 说明 | 风险 | 敏感 |
|---|---|---|---|
| `job:admin` | 全局任务控制 | high |  |
| `job:control` | 控制任务 | medium |  |
| `job:diagnose` | 查看任务诊断信息 | medium |  |
| `job:list:self` | 列出本人任务 | low |  |
| `job:list:workspace` | 列出工作区内任务 | low |  |
| `job:read` | 查看任务 | low |  |
| `job:retry` | 重试任务 | medium |  |

#### 导出（`export`，5 条）

| 权限键 | 说明 | 风险 | 敏感 |
|---|---|---|---|
| `export:create` | 创建导出 | medium |  |
| `export:delete` | 删除导出 | high |  |
| `export:download` | 下载导出文件 | medium | 是 |
| `export:list` | 列出导出 | low |  |
| `export:read` | 查看导出 | low |  |

#### 报告（`report`，8 条）

| 权限键 | 说明 | 风险 | 敏感 |
|---|---|---|---|
| `report:approve` | 审批报告 | high |  |
| `report:create` | 生成报告 | low |  |
| `report:delete` | 删除报告 | high |  |
| `report:download` | 下载报告 | medium | 是 |
| `report:list` | 列出报告 | low |  |
| `report:read` | 查看报告 | low |  |
| `report:review` | 复核报告 | high |  |
| `report:sign` | 签署报告 | critical |  |

#### 协作与通知（`notification`，6 条）

| 权限键 | 说明 | 风险 | 敏感 |
|---|---|---|---|
| `inbox:assign` | 分派待办 | medium |  |
| `inbox:read` | 查看待我处理 | low |  |
| `notification:list` | 列出通知 | low |  |
| `notification:manage` | 管理通知偏好 | low |  |
| `notification:read` | 查看通知 | low |  |
| `notification:update` | 更新通知状态 | low |  |

#### 个人（self）（`self`，6 条）

| 权限键 | 说明 | 风险 | 敏感 |
|---|---|---|---|
| `preference:update` | 修改使用偏好 | low |  |
| `profile:read` | 查看个人资料 | low |  |
| `profile:update` | 修改个人资料 | low |  |
| `profile:update:self` | 修改本人资料 | low |  |
| `security:update` | 修改安全设置 | high |  |
| `session:self:revoke` | 撤销自己的会话 | medium |  |

#### 工作台（`dashboard`，1 条）

| 权限键 | 说明 | 风险 | 敏感 |
|---|---|---|---|
| `dashboard:read` | 查看个人工作台 | low |  |

#### 后台 · 运营总览（`admin.ops`，1 条）

| 权限键 | 说明 | 风险 | 敏感 |
|---|---|---|---|
| `admin:dashboard:read` | 查看运营总览 | low |  |

#### 后台 · 身份与权限（`admin.identity`，15 条）

| 权限键 | 说明 | 风险 | 敏感 |
|---|---|---|---|
| `admin:member:manage` | 管理成员关系 | high |  |
| `admin:member:read` | 查看成员关系 | medium |  |
| `admin:org:manage` | 管理组织 | high |  |
| `admin:org:read` | 查看组织 | low |  |
| `admin:permission:read` | 查看权限目录 | low |  |
| `admin:role:manage` | 管理角色与权限 | critical |  |
| `admin:role:read` | 查看角色 | low |  |
| `admin:user:create` | 创建用户 | high |  |
| `admin:user:disable` | 停用用户 | critical |  |
| `admin:user:invite` | 邀请用户 | medium |  |
| `admin:user:password:reset` | 重置用户密码 | critical |  |
| `admin:user:read` | 查看用户 | medium |  |
| `admin:user:update` | 修改用户 | high |  |
| `admin:workspace:manage` | 管理工作区 | high |  |
| `admin:workspace:read` | 查看工作区 | low |  |

#### 后台 · 安全与审计（`admin.security`，12 条）

| 权限键 | 说明 | 风险 | 敏感 |
|---|---|---|---|
| `admin:apikey:create` | 创建 API/MCP 密钥 | critical |  |
| `admin:apikey:read` | 查看 API/MCP 密钥 | high | 是 |
| `admin:apikey:revoke` | 吊销 API/MCP 密钥 | critical |  |
| `admin:audit:export` | 导出审计日志 | critical | 是 |
| `admin:audit:read` | 查看审计日志 | medium | 是 |
| `admin:audit:seal` | 封存审计链完整性缺口边界 | critical |  |
| `admin:audit:verify` | 校验审计哈希链 | medium |  |
| `admin:login-event:read` | 查看登录记录 | medium | 是 |
| `admin:risk:manage` | 处置风险事件 | high |  |
| `admin:risk:read` | 查看风险事件 | medium |  |
| `admin:session:read` | 查看会话 | medium |  |
| `admin:session:revoke` | 强制下线会话 | critical |  |

#### 后台 · 控制面（`admin.control`，7 条）

| 权限键 | 说明 | 风险 | 敏感 |
|---|---|---|---|
| `admin:alert:manage` | 处置告警 | high |  |
| `admin:alert:read` | 查看告警 | low |  |
| `admin:approval:decide` | 审批高风险操作 | critical |  |
| `admin:approval:read` | 查看审批请求 | medium |  |
| `admin:killswitch:engage` | 启用 Kill Switch | critical |  |
| `admin:killswitch:read` | 查看 Kill Switch | medium |  |
| `admin:killswitch:release` | 解除 Kill Switch | critical |  |

#### 后台 · Provider 与成本（`admin.provider`，11 条）

| 权限键 | 说明 | 风险 | 敏感 |
|---|---|---|---|
| `admin:budget:read` | 查看预算 | medium |  |
| `admin:budget:update` | 修改预算 | critical |  |
| `admin:cost:read` | 查看成本 | medium |  |
| `admin:performance:read` | 查看性能模式 | low |  |
| `admin:performance:update` | 修改性能模式 | high |  |
| `admin:provider:key:rotate` | 轮换 Provider 密钥 | critical |  |
| `admin:provider:read` | 查看 Provider | low |  |
| `admin:provider:update` | 修改 Provider 配置 | high |  |
| `admin:request:read` | 查看请求审计 | medium | 是 |
| `admin:routing:read` | 查看路由与限流 | low |  |
| `admin:routing:update` | 修改路由与限流 | high |  |

#### 后台 · 任务与数据治理（`admin.data`，18 条）

| 权限键 | 说明 | 风险 | 敏感 |
|---|---|---|---|
| `admin:coverage:read` | 查看 Coverage 与数据质量 | low |  |
| `admin:dataset:manage` | 重建 Dataset | critical |  |
| `admin:dataset:read` | 查看 Dataset | low |  |
| `admin:deletion:approve` | 审批删除请求 | critical |  |
| `admin:deletion:read` | 查看删除请求 | medium |  |
| `admin:deletion:request` | 发起删除请求 | critical |  |
| `admin:export:delete` | 删除导出对象 | critical |  |
| `admin:export:list` | 列出全局导出 | medium | 是 |
| `admin:export:read` | 查看全部导出 | medium | 是 |
| `admin:job:cancel` | 取消任意任务 | critical |  |
| `admin:job:control` | 控制全局任务 | high |  |
| `admin:job:list` | 列出全局任务 | low |  |
| `admin:job:read` | 查看全局任务 | low |  |
| `admin:job:retry` | 重试任意任务 | high |  |
| `admin:retention:manage` | 管理保留策略 | critical |  |
| `admin:retention:read` | 查看保留策略 | low |  |
| `admin:storage:manage` | 管理对象存储 | high |  |
| `admin:storage:read` | 查看对象存储用量 | low |  |

#### 后台 · 系统（`admin.system`，14 条）

| 权限键 | 说明 | 风险 | 敏感 |
|---|---|---|---|
| `admin:backup:create` | 创建备份 | high |  |
| `admin:backup:read` | 查看备份 | medium |  |
| `admin:backup:restore` | 恢复备份 | critical |  |
| `admin:config:history:read` | 查看配置历史 | medium |  |
| `admin:config:read` | 查看系统配置 | medium |  |
| `admin:config:update` | 修改系统配置 | critical |  |
| `admin:flag:read` | 查看功能开关 | low |  |
| `admin:flag:update` | 修改功能开关 | high |  |
| `admin:metrics:read` | 查看平台指标 | low |  |
| `admin:notification-channel:manage` | 管理通知渠道 | high |  |
| `admin:notification-channel:read` | 查看通知渠道 | low |  |
| `admin:system:read` | 查看系统健康 | low |  |
| `admin:version:manage` | 执行版本与迁移操作 | critical |  |
| `admin:version:read` | 查看版本与迁移 | low |  |

## 3. 九个系统角色与默认映射

系统角色由 `0041_rbac.sql` 种子写入（`roles.organization_id IS NULL`、`is_system=true`），
后续授权补充来自 `0048`（Viewer/Auditor 的 `notification:list`）与 `0053`（控制面写权限）。
授权事实来源是 `member_role_bindings`（`users.role` 仅用于迁移回填）。

| 角色键 | 名称 | 授权条数（实时库） | 定位 |
|---|---|---|---|
| `owner` | Owner | 177 | 组织所有者：全部权限 |
| `platform_admin` | Platform Admin | 70 | 平台管理员：系统、Provider、任务、告警与配置 |
| `security_admin` | Security Admin | 37 | 安全管理员：认证策略、会话、密钥与风险事件 |
| `auditor` | Auditor | 30 | 审计员：只读访问审计、配置历史与导出校验 |
| `investigator` | Investigator | 85 | 调查员：案件、调查、图谱、结论、证据与报告 |
| `analyst` | Analyst | 72 | 分析员：查询、分析、导出与协作 |
| `reviewer` | Reviewer | 55 | 复核人：查看证据、审批结论、签署报告 |
| `viewer` | Viewer | 33 | 只读协作者：查看已授权案件与报告 |
| `api_client` | API Client | 4 | API 客户端：仅声明的 scope，不能登录后台 |

#### `owner` — Owner（实时库 177 条）

> 组织所有者：全组织案件、数据与后台全局配置

| 权限分类 | 权限键 |
|---|---|
| 案件（`case`） | `case:activity:read`、`case:archive`、`case:comment:create`、`case:create`、`case:delete`、`case:list`、`case:member:list`、`case:member:manage`、`case:read`、`case:state:update`、`case:subject:manage`、`case:update`、`comment:create`、`comment:list`、`subject:create`、`subject:list`、`subject:remove`、`subject:update` |
| 调查（`investigation`） | `investigation:approve`、`investigation:cancel`、`investigation:create`、`investigation:delete`、`investigation:list`、`investigation:pause`、`investigation:read`、`investigation:resume`、`investigation:retry`、`investigation:run`、`investigation:start`、`investigation:submit_review`、`investigation:update` |
| 结论（Finding）（`finding`） | `finding:create`、`finding:list`、`finding:read`、`finding:review`、`finding:update` |
| 证据（`evidence`） | `evidence:create`、`evidence:delete`、`evidence:download`、`evidence:list`、`evidence:read`、`evidence:review`、`evidence:sensitive:read`、`evidence:snapshot:create` |
| 图谱（Canvas）（`canvas`） | `canvas:branch`、`canvas:create`、`canvas:delete`、`canvas:list`、`canvas:read`、`canvas:snapshot`、`canvas:snapshot:create`、`canvas:update` |
| 数据查询（Explorer）（`explorer`） | `address:read`、`explorer:fetch`、`explorer:read`、`search:global`、`search:read`、`token:read`、`transaction:read` |
| 下载（`download`） | `download:control`、`download:create`、`download:delete`、`download:preview`、`download:read`、`download:refresh`、`download:reuse` |
| 任务（`job`） | `job:admin`、`job:control`、`job:diagnose`、`job:list:self`、`job:list:workspace`、`job:read`、`job:retry` |
| 导出（`export`） | `export:create`、`export:delete`、`export:download`、`export:list`、`export:read` |
| 报告（`report`） | `report:approve`、`report:create`、`report:delete`、`report:download`、`report:list`、`report:read`、`report:review`、`report:sign` |
| 协作与通知（`notification`） | `inbox:assign`、`inbox:read`、`notification:list`、`notification:manage`、`notification:read`、`notification:update` |
| 个人（self）（`self`） | `preference:update`、`profile:read`、`profile:update`、`profile:update:self`、`security:update`、`session:self:revoke` |
| 工作台（`dashboard`） | `dashboard:read` |
| 后台 · 运营总览（`admin.ops`） | `admin:dashboard:read` |
| 后台 · 身份与权限（`admin.identity`） | `admin:member:manage`、`admin:member:read`、`admin:org:manage`、`admin:org:read`、`admin:permission:read`、`admin:role:manage`、`admin:role:read`、`admin:user:create`、`admin:user:disable`、`admin:user:invite`、`admin:user:password:reset`、`admin:user:read`、`admin:user:update`、`admin:workspace:manage`、`admin:workspace:read` |
| 后台 · 安全与审计（`admin.security`） | `admin:apikey:create`、`admin:apikey:read`、`admin:apikey:revoke`、`admin:audit:export`、`admin:audit:read`、`admin:audit:seal`、`admin:audit:verify`、`admin:login-event:read`、`admin:risk:manage`、`admin:risk:read`、`admin:session:read`、`admin:session:revoke` |
| 后台 · 控制面（`admin.control`） | `admin:alert:manage`、`admin:alert:read`、`admin:approval:decide`、`admin:approval:read`、`admin:killswitch:engage`、`admin:killswitch:read`、`admin:killswitch:release` |
| 后台 · Provider 与成本（`admin.provider`） | `admin:budget:read`、`admin:budget:update`、`admin:cost:read`、`admin:performance:read`、`admin:performance:update`、`admin:provider:key:rotate`、`admin:provider:read`、`admin:provider:update`、`admin:request:read`、`admin:routing:read`、`admin:routing:update` |
| 后台 · 任务与数据治理（`admin.data`） | `admin:coverage:read`、`admin:dataset:manage`、`admin:dataset:read`、`admin:deletion:approve`、`admin:deletion:read`、`admin:deletion:request`、`admin:export:delete`、`admin:export:list`、`admin:export:read`、`admin:job:cancel`、`admin:job:control`、`admin:job:list`、`admin:job:read`、`admin:job:retry`、`admin:retention:manage`、`admin:retention:read`、`admin:storage:manage`、`admin:storage:read` |
| 后台 · 系统（`admin.system`） | `admin:backup:create`、`admin:backup:read`、`admin:backup:restore`、`admin:config:history:read`、`admin:config:read`、`admin:config:update`、`admin:flag:read`、`admin:flag:update`、`admin:metrics:read`、`admin:notification-channel:manage`、`admin:notification-channel:read`、`admin:system:read`、`admin:version:manage`、`admin:version:read` |


#### `platform_admin` — Platform Admin（实时库 70 条）

> 平台管理员：系统、Provider、任务、告警与配置

| 权限分类 | 权限键 |
|---|---|
| 案件（`case`） | `case:activity:read`、`case:list`、`case:member:list`、`case:read`、`comment:list` |
| 调查（`investigation`） | `investigation:read` |
| 数据查询（Explorer）（`explorer`） | `search:read` |
| 下载（`download`） | `download:read` |
| 任务（`job`） | `job:read` |
| 导出（`export`） | `export:read` |
| 报告（`report`） | `report:read` |
| 工作台（`dashboard`） | `dashboard:read` |
| 后台 · 运营总览（`admin.ops`） | `admin:dashboard:read` |
| 后台 · 身份与权限（`admin.identity`） | `admin:member:read`、`admin:org:read`、`admin:permission:read`、`admin:role:read`、`admin:user:read`、`admin:workspace:read` |
| 后台 · 安全与审计（`admin.security`） | `admin:apikey:read`、`admin:audit:read`、`admin:audit:verify`、`admin:login-event:read`、`admin:risk:read`、`admin:session:read` |
| 后台 · 控制面（`admin.control`） | `admin:alert:manage`、`admin:alert:read`、`admin:approval:decide`、`admin:approval:read`、`admin:killswitch:engage`、`admin:killswitch:read`、`admin:killswitch:release` |
| 后台 · Provider 与成本（`admin.provider`） | `admin:budget:read`、`admin:budget:update`、`admin:cost:read`、`admin:performance:read`、`admin:performance:update`、`admin:provider:key:rotate`、`admin:provider:read`、`admin:provider:update`、`admin:request:read`、`admin:routing:read`、`admin:routing:update` |
| 后台 · 任务与数据治理（`admin.data`） | `admin:coverage:read`、`admin:dataset:manage`、`admin:dataset:read`、`admin:deletion:approve`、`admin:deletion:request`、`admin:export:list`、`admin:export:read`、`admin:job:control`、`admin:job:list`、`admin:job:read`、`admin:job:retry`、`admin:retention:manage`、`admin:retention:read`、`admin:storage:manage`、`admin:storage:read` |
| 后台 · 系统（`admin.system`） | `admin:backup:create`、`admin:backup:read`、`admin:backup:restore`、`admin:config:history:read`、`admin:config:read`、`admin:config:update`、`admin:flag:read`、`admin:flag:update`、`admin:metrics:read`、`admin:notification-channel:read`、`admin:system:read`、`admin:version:read` |


#### `security_admin` — Security Admin（实时库 37 条）

> 安全管理员：认证策略、会话、密钥与风险事件

| 权限分类 | 权限键 |
|---|---|
| 案件（`case`） | `case:activity:read`、`case:list`、`case:member:list`、`case:read`、`comment:list` |
| 数据查询（Explorer）（`explorer`） | `search:read` |
| 工作台（`dashboard`） | `dashboard:read` |
| 后台 · 运营总览（`admin.ops`） | `admin:dashboard:read` |
| 后台 · 身份与权限（`admin.identity`） | `admin:member:read`、`admin:org:read`、`admin:permission:read`、`admin:role:read`、`admin:user:read`、`admin:workspace:read` |
| 后台 · 安全与审计（`admin.security`） | `admin:apikey:create`、`admin:apikey:read`、`admin:apikey:revoke`、`admin:audit:read`、`admin:audit:seal`、`admin:audit:verify`、`admin:login-event:read`、`admin:risk:manage`、`admin:risk:read`、`admin:session:read`、`admin:session:revoke` |
| 后台 · 控制面（`admin.control`） | `admin:alert:manage`、`admin:approval:decide`、`admin:killswitch:engage`、`admin:killswitch:read`、`admin:killswitch:release` |
| 后台 · Provider 与成本（`admin.provider`） | `admin:provider:key:rotate` |
| 后台 · 系统（`admin.system`） | `admin:config:history:read`、`admin:config:read`、`admin:config:update`、`admin:flag:read`、`admin:flag:update`、`admin:system:read` |


#### `auditor` — Auditor（实时库 30 条）

> 审计员：只读访问审计、配置历史与导出校验

| 权限分类 | 权限键 |
|---|---|
| 案件（`case`） | `case:activity:read`、`case:list`、`case:member:list`、`case:read`、`comment:list`、`subject:list` |
| 证据（`evidence`） | `evidence:list` |
| 数据查询（Explorer）（`explorer`） | `search:read` |
| 协作与通知（`notification`） | `notification:list` |
| 工作台（`dashboard`） | `dashboard:read` |
| 后台 · 运营总览（`admin.ops`） | `admin:dashboard:read` |
| 后台 · 身份与权限（`admin.identity`） | `admin:org:read`、`admin:permission:read`、`admin:role:read`、`admin:user:read`、`admin:workspace:read` |
| 后台 · 安全与审计（`admin.security`） | `admin:audit:export`、`admin:audit:read`、`admin:audit:verify`、`admin:login-event:read`、`admin:risk:read`、`admin:session:read` |
| 后台 · 任务与数据治理（`admin.data`） | `admin:export:read`、`admin:job:list`、`admin:job:read` |
| 后台 · 系统（`admin.system`） | `admin:backup:read`、`admin:config:history:read`、`admin:config:read`、`admin:system:read`、`admin:version:read` |


#### `investigator` — Investigator（实时库 85 条）

> 调查员：案件、调查、图谱、结论、证据与报告

| 权限分类 | 权限键 |
|---|---|
| 案件（`case`） | `case:activity:read`、`case:archive`、`case:comment:create`、`case:create`、`case:list`、`case:member:list`、`case:member:manage`、`case:read`、`case:state:update`、`case:subject:manage`、`case:update`、`comment:create`、`comment:list`、`subject:create`、`subject:list`、`subject:remove`、`subject:update` |
| 调查（`investigation`） | `investigation:cancel`、`investigation:create`、`investigation:list`、`investigation:pause`、`investigation:read`、`investigation:resume`、`investigation:retry`、`investigation:run`、`investigation:start`、`investigation:submit_review`、`investigation:update` |
| 结论（Finding）（`finding`） | `finding:create`、`finding:list`、`finding:read`、`finding:update` |
| 证据（`evidence`） | `evidence:create`、`evidence:download`、`evidence:list`、`evidence:read`、`evidence:review`、`evidence:snapshot:create` |
| 图谱（Canvas）（`canvas`） | `canvas:branch`、`canvas:create`、`canvas:list`、`canvas:read`、`canvas:snapshot`、`canvas:snapshot:create`、`canvas:update` |
| 数据查询（Explorer）（`explorer`） | `address:read`、`explorer:fetch`、`explorer:read`、`search:global`、`search:read`、`token:read`、`transaction:read` |
| 下载（`download`） | `download:control`、`download:create`、`download:preview`、`download:read`、`download:refresh`、`download:reuse` |
| 任务（`job`） | `job:control`、`job:diagnose`、`job:list:self`、`job:list:workspace`、`job:read`、`job:retry` |
| 导出（`export`） | `export:create`、`export:download`、`export:list`、`export:read` |
| 报告（`report`） | `report:create`、`report:download`、`report:list`、`report:read`、`report:review` |
| 协作与通知（`notification`） | `inbox:read`、`notification:list`、`notification:manage`、`notification:read`、`notification:update` |
| 个人（self）（`self`） | `preference:update`、`profile:read`、`profile:update`、`profile:update:self`、`security:update`、`session:self:revoke` |
| 工作台（`dashboard`） | `dashboard:read` |


#### `analyst` — Analyst（实时库 72 条）

> 分析员：查询、分析、导出与协作

| 权限分类 | 权限键 |
|---|---|
| 案件（`case`） | `case:activity:read`、`case:comment:create`、`case:list`、`case:member:list`、`case:read`、`comment:create`、`comment:list`、`subject:create`、`subject:list`、`subject:update` |
| 调查（`investigation`） | `investigation:create`、`investigation:list`、`investigation:pause`、`investigation:read`、`investigation:resume`、`investigation:retry`、`investigation:run`、`investigation:start`、`investigation:update` |
| 结论（Finding）（`finding`） | `finding:create`、`finding:list`、`finding:read`、`finding:update` |
| 证据（`evidence`） | `evidence:create`、`evidence:list`、`evidence:read`、`evidence:snapshot:create` |
| 图谱（Canvas）（`canvas`） | `canvas:branch`、`canvas:create`、`canvas:list`、`canvas:read`、`canvas:snapshot`、`canvas:snapshot:create`、`canvas:update` |
| 数据查询（Explorer）（`explorer`） | `address:read`、`explorer:fetch`、`explorer:read`、`search:global`、`search:read`、`token:read`、`transaction:read` |
| 下载（`download`） | `download:control`、`download:create`、`download:preview`、`download:read`、`download:refresh`、`download:reuse` |
| 任务（`job`） | `job:control`、`job:list:self`、`job:list:workspace`、`job:read`、`job:retry` |
| 导出（`export`） | `export:create`、`export:download`、`export:list`、`export:read` |
| 报告（`report`） | `report:create`、`report:download`、`report:list`、`report:read` |
| 协作与通知（`notification`） | `inbox:read`、`notification:list`、`notification:manage`、`notification:read`、`notification:update` |
| 个人（self）（`self`） | `preference:update`、`profile:read`、`profile:update`、`profile:update:self`、`security:update`、`session:self:revoke` |
| 工作台（`dashboard`） | `dashboard:read` |


#### `reviewer` — Reviewer（实时库 55 条）

> 复核人：查看证据、审批结论、签署报告

| 权限分类 | 权限键 |
|---|---|
| 案件（`case`） | `case:activity:read`、`case:comment:create`、`case:list`、`case:member:list`、`case:read`、`comment:create`、`comment:list`、`subject:list` |
| 调查（`investigation`） | `investigation:approve`、`investigation:cancel`、`investigation:list`、`investigation:read`、`investigation:submit_review` |
| 结论（Finding）（`finding`） | `finding:list`、`finding:read`、`finding:review` |
| 证据（`evidence`） | `evidence:download`、`evidence:list`、`evidence:read`、`evidence:review`、`evidence:sensitive:read` |
| 图谱（Canvas）（`canvas`） | `canvas:list`、`canvas:read`、`canvas:snapshot` |
| 数据查询（Explorer）（`explorer`） | `address:read`、`explorer:read`、`search:global`、`search:read`、`token:read`、`transaction:read` |
| 下载（`download`） | `download:read` |
| 任务（`job`） | `job:list:self`、`job:read` |
| 导出（`export`） | `export:download`、`export:list`、`export:read` |
| 报告（`report`） | `report:approve`、`report:download`、`report:list`、`report:read`、`report:review`、`report:sign` |
| 协作与通知（`notification`） | `inbox:assign`、`inbox:read`、`notification:list`、`notification:manage`、`notification:read`、`notification:update` |
| 个人（self）（`self`） | `preference:update`、`profile:read`、`profile:update`、`profile:update:self`、`security:update`、`session:self:revoke` |
| 工作台（`dashboard`） | `dashboard:read` |


#### `viewer` — Viewer（实时库 33 条）

> 只读协作者：查看已授权案件与报告

| 权限分类 | 权限键 |
|---|---|
| 案件（`case`） | `case:activity:read`、`case:list`、`case:read`、`subject:list` |
| 调查（`investigation`） | `investigation:list`、`investigation:read` |
| 结论（Finding）（`finding`） | `finding:list`、`finding:read` |
| 证据（`evidence`） | `evidence:list`、`evidence:read` |
| 图谱（Canvas）（`canvas`） | `canvas:list`、`canvas:read` |
| 数据查询（Explorer）（`explorer`） | `address:read`、`explorer:read`、`search:global`、`search:read`、`token:read`、`transaction:read` |
| 任务（`job`） | `job:list:self`、`job:read` |
| 导出（`export`） | `export:list`、`export:read` |
| 报告（`report`） | `report:list`、`report:read` |
| 协作与通知（`notification`） | `inbox:read`、`notification:list`、`notification:read`、`notification:update` |
| 个人（self）（`self`） | `preference:update`、`profile:read`、`profile:update:self`、`session:self:revoke` |
| 工作台（`dashboard`） | `dashboard:read` |


#### `api_client` — API Client（实时库 4 条）

> API 客户端：仅声明的 scope，不能登录后台

| 权限分类 | 权限键 |
|---|---|
| 数据查询（Explorer）（`explorer`） | `address:read`、`explorer:read`、`token:read`、`transaction:read` |


### 3.1 角色设计要点（来自迁移注释与 ADR）

- **Owner 是唯一"全量"角色**：`owner` 的授权由 `CROSS JOIN permissions` 生成，
  因此权限目录新增条数时 Owner 自动跟随；其他角色必须显式授权。
- **审计员只读是配置事实，不是口头约定**：`0053` 不向 auditor 授予任何 Sprint 6 写权限，
  迁移内附自检 SQL（期望 0）：

  ```sql
  SELECT count(*) FROM role_permissions rp JOIN roles r ON r.id = rp.role_id
  WHERE r.key='auditor' AND rp.permission_key IN
    ('admin:killswitch:engage','admin:killswitch:release','admin:approval:decide',
     'admin:provider:key:rotate','admin:budget:update','admin:config:update',
     'admin:backup:create','admin:backup:restore','admin:retention:manage',
     'admin:deletion:request','admin:deletion:approve','admin:provider:update',
     'admin:routing:update','admin:performance:update','admin:alert:manage',
     'admin:flag:update','admin:job:control');  -- 期望 0
  ```

- **API Client 不能进后台**：`api_client` 只有 `explorer:read`、`address:read`、
  `transaction:read`、`token:read`；且 `admin:` 前缀权限额外要求 `audience == "admin"`（AD-003 第 4 条）。
- **后台管理员默认看不到案件正文**：`platform_admin`/`security_admin` 均不含 `evidence:read`，
  读取案件正文需要单独授权并触发敏感读取审计（RBAC-004）。

## 4. 作用域模型

授权记录表 `member_role_bindings` 的 `scope_type` 取值：`organization` / `workspace` / `case` / `self`
（`0041_rbac.sql` 第 65 行的 CHECK 约束）。

```text
member_role_bindings(identity_id, role_id, scope_type, scope_id, granted_by, granted_at, expires_at, revoked_at, reason)
```

### 4.1 生效规则（`policy.Engine.Load` 的真实 SQL）

角色绑定生效需要同时满足：

1. `revoked_at IS NULL` 且 `expires_at IS NULL OR expires_at > now()`；
2. 该身份在目标组织内存在 `organization_memberships.status='active'`；
3. 作用域命中以下之一：
   - `scope_type='self'`（不限组织）；
   - `scope_type='organization'` 且 `scope_id = 当前组织`；
   - `scope_type='workspace'` 且 `scope_id` 属于当前组织的工作区；
   - `scope_type='case'` 且 `scope_id` 属于当前组织的案件。

### 4.2 两层分工（重要）

`policy.Engine` **刻意不按工作区裁剪动作权限**：工作区级绑定同样能拿到动作权限，
"能不能碰这个对象"由 `access.Guard` 在工作区/案件层面收窄。
若在动作层就按工作区裁剪，工作区级绑定会连动作权限都拿不到，结果是 403 而不是对象级 404，
既不可用也泄露了"对象存在但无权"的差异（`engine.go` 第 113–120 行注释）。

### 4.3 实时作用域分布

```bash
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 \
  psql -U blockchain -d blockchain_platform -Atc \
  "select scope_type, count(*) from member_role_bindings where revoked_at is null group by 1 order by 1"
```

> 2026-09-12 实测：`organization=7`、`workspace=11`（`case`/`self` 当前无绑定）。
> 该分布随运营变化，仅用于说明查询口径。

### 4.4 迁移回填规则

`0041` 把旧 `users.role` 回填为组织作用域的绑定：
`owner→owner`、`admin→platform_admin`、`analyst/investigator/reviewer/viewer/api_client` 同名映射，
其余一律 `viewer`（`0041_rbac.sql` 第 413–431 行）。回填的 `reason` 为
`migration from users.role=<原值>`，可在绑定记录里直接追溯。

## 5. 后台权限的会话隔离

- 后台权限（`admin:` 前缀）除权限本身外，额外要求会话 `audience == "admin"`；
  用户层会话即使被误授权限也会得到 `audience_mismatch` 拒绝（`engine.go` 第 204–208 行）。
- 两个前端使用不同 Cookie（用户层 `cs_user_session`，后台 `cs_admin_session`），
  Nginx 分别以 `/app-v2/`、`/admin/` 反代到 127.0.0.1:3001 与 127.0.0.1:3002。
- Actions/MCP API Key 不创建浏览器会话，且不获得 `admin:` 权限。

## 6. 风险等级与 step-up 要求

### 6.1 step-up 是什么（真实实现，不要与 MFA 混淆）

高风险命令在请求体中携带 `step_up_password`（当前账号密码重认证），
审计记录 `step_up_method=password`。**step-up 不等于 MFA**：MFA/TOTP 已于 2026-09-16 实现
（登录第二步 + 自助注册 + 管理台 `admin:user:reset_mfa`，AUTH-004），但它是**第二因素**，
不能替代 step-up，也不得把密码重认证表述为"MFA 已完成"（ADR-017 §3）。

```json
{
  "reason": "Kill Switch 演练",
  "step_up_password": "<当前账号密码>"
}
```

### 6.2 双人审批

`approval_requests` 有数据库级 CHECK：
`CHECK (decided_by IS NULL OR decided_by <> requested_by)`（`0051`），
应用层 `s6DecideApproval` 同时先判定；**请求人不能审批自己发起的请求**。
需要双人审批的典型场景：Kill Switch、审计完整性封存、备份恢复、删除请求。

### 6.3 计划 §20.7 的高风险管理操作清单

统一命令流程：

```text
preview -> confirm_reason -> step_up -> approval(optional) -> execute
        -> expired         -> rejected                   -> verify -> audit
```

适用：Kill Switch、Provider 断开/密钥轮换、全局并发上调、批量用户停用、权限大范围变更、
数据删除、恢复备份、审计导出。

### 6.4 计划 §21.6 文档权限键 → 实现权限键映射

计划文档使用一套更粗的键，实现使用 `policy.Catalog` 的细粒度键（ADR-017 §1）：

| 计划 §21.6 写法 | 实现使用 |
|---|---|
| `admin:kill_switch:update` | `admin:killswitch:engage`（开启） / `admin:killswitch:release`（恢复） |
| `admin:feature_flag:manage` | `admin:flag:update`（读用 `admin:flag:read`） |
| `admin:setting:read/update` | `admin:config:read` / `admin:config:update` |
| `admin:key:list/create/rotate/revoke` | `admin:apikey:read` / `admin:apikey:create` / `admin:apikey:revoke` |
| `admin:restore:run` | `admin:backup:restore`（预览与执行同权） |
| `admin:routing:update` / `admin:performance:update` / `admin:budget:manage` | 同名，或 `admin:budget:update` |
| `admin:deletion:request/approve` | `admin:deletion:request` / `admin:deletion:approve` |

## 7. 排查：401 / 403 的判定顺序

| 现象 | `error.code` | 判定位置 | 处置 |
|---|---|---|---|
| 未登录/会话过期 | `AUTHN_*`，HTTP 401 | `authmw` | 重新登录；检查 `/api/auth/v1/session` |
| 用户层会话调后台 API | `AUTHZ_FORBIDDEN`，HTTP 403，reason `audience_mismatch` | `policy.Engine.Authorize` | 使用 `/admin` 入口重新登录 |
| 角色缺少权限 | 403，reason `missing_permission` | `policy.Engine` | 在 `/admin/roles/:id` 补授权 |
| 路由声明的权限不在目录 | 403，reason `permission_not_declared` | `policy.Engine.Authorize` | 代码缺陷：补 Catalog + SQL + 测试 |
| 跨租户/跨工作区对象 | 404 或策略 403 | `access.Guard` | 确认对象归属；不要按 403 猜测对象存在 |
| CSRF 头缺失/过期 | `csrf_failed` | CSRF 中间件 | 刷新页面重新获取 `/session` 的 csrf token |

命令核对示例（未登录访问后台必须 401）：

```bash
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8080/api/admin/v1/overview   # 期望 401
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8080/api/v2/dashboard        # 期望 401
```

> 2026-09-12 实测均为 401。

## 8. 变更权限的正确流程（检查清单）

新增一个权限键时必须同时改动四处，缺一不可：

- [ ] `backend/internal/policy/catalog.go`：加入 `Catalog`（key/category/description/risk/sensitive）。
- [ ] `backend/migrations/postgres/`：新建迁移，`INSERT INTO permissions ... ON CONFLICT (key) DO UPDATE`
      （保持与 Go 侧逐字段一致）。
- [ ] 建一个新增迁移把权限授予目标系统角色（`ON CONFLICT DO NOTHING`，不删除既有授权）。
- [ ] 跑 `cd backend && go test ./internal/policy/...`，确认 `TestCatalogMatchesMigrationSeed` 与
      `TestRoleSeedPermissionsExist` 通过。
- [ ] 应用迁移（`./backend/bin/migrate`，幂等）后用第 0.1 节命令核对条数增长。

修改角色授权后必须让缓存失效：`policy.Engine.Invalidate` / `InvalidateAll`（角色权限编辑走后者），
否则变更最长 30 秒才生效；RBAC-006 要求 5 秒内旧授权失效。

## 9. 待确认项

- 自定义（非系统）角色的创建/删除接口路径与行为未在本文档中逐一验证，需以 `/admin/roles` 页面实际实现为准。
- `case` / `self` 作用域在当前环境中没有真实绑定数据，其端到端效果未在本环境实测。
- MFA/TOTP 与 `AUTH_REQUIRE_ADMIN_MFA` 仍为未实现/关闭状态；后台登录不强制 MFA。
