# ADR-017：Sprint 6 控制面契约、权限键映射与运行期降权

- 状态：Accepted
- 日期：2026-09-12
- 关联：`docs/dual-system-development-plan.md` §19 / §21.6 / §25 / §28.8、AD-003、AD-006、AD-009

## 背景

计划文档 §21.6 用一套权限键描述后台 API（例如 `admin:kill_switch:update`、
`admin:feature_flag:manage`、`admin:setting:read`、`admin:key:list`、`admin:restore:run`），
而 Sprint 3 落地的 `internal/policy/catalog.go` 已经把这些能力声明为另一套更细的键
（`admin:killswitch:engage/release` 等），并由 `catalog_sql_test.go` 强制与 0041 迁移一一对应。

Sprint 6 必须在不破坏既有权限目录、不重复授权语义的前提下挂载控制面。此外，
AUDIT-003 的「应用 DB 角色拒绝」在实现过程中暴露了一个真实缺口（见下），需要一并固化决策。

## 决策

### 1. 权限键以 `policy.Catalog` 为唯一事实来源，文档键按语义映射

| 计划 §21.6 写法 | 实现使用（Catalog） |
|---|---|
| `admin:kill_switch:update` | `admin:killswitch:engage`（开启） / `admin:killswitch:release`（恢复） |
| `admin:feature_flag:manage` | `admin:flag:update`（读用 `admin:flag:read`） |
| `admin:setting:read/update` | `admin:config:read` / `admin:config:update` |
| `admin:key:list/create/rotate/revoke` | `admin:apikey:read` / `admin:apikey:create` / `admin:apikey:revoke` |
| `admin:restore:run` | `admin:backup:restore`（预览与执行同权） |
| `admin:routing:update` / `admin:performance:update` / `admin:budget:manage` | 同名，或 `admin:budget:update` |
| `admin:deletion:request/approve` | `admin:deletion:request` / `admin:deletion:approve` |

Sprint 6 没有新增任何权限键；0053 只把 **已声明但未授权**的 13 个键授予
`platform_admin`（运维写）与 `security_admin`（安全控制），auditor 保持只读。

### 2. 统一 preview / command / If-Match 约定

- 只读查询：`GET`；`s.require("<perm>:read")`。
- 影响预览：`POST .../preview`，必须只读、可重复调用、返回来源与口径。
- 状态变更：`POST .../commands/{verb}`，body 必带 `reason`（写入审计）。
- 配置类修改：`PATCH` + `If-Match: <revision>`，缺失 428，过期 412（不静默覆盖）。
- 高风险命令：body 携带 `step_up_password`（当前账号密码重认证）。

### 3. step-up 用密码重认证代替 MFA（并明确标注能力边界）

§19.9 要求 step-up MFA。本平台 MFA/TOTP 仍为 `NOT_IMPLEMENTED`（AUTH-004），
因此 S6 实现的是「重新提交当前账号密码」的等价控制，并在审计中记录
`step_up_method=password`。**不得**把该实现表述为 MFA 已完成。

### 4. 双人审批由数据库约束兜底

`approval_requests` 增加 `CHECK (decided_by IS NULL OR decided_by <> requested_by)`（0051），
应用层在 `s6DecideApproval` 中先判定再落库；两层同时存在，避免只靠前端或只靠代码。

### 5. 运行期降权（AUDIT-003 的实际结论）

行级触发器只能拦截 `UPDATE/DELETE`；PostgreSQL 的 `TRUNCATE` 不触发行触发器，
事件触发器也不支持 `TRUNCATE TABLE`，而应用连接账号同时是表 owner（本部署中还是超级用户），
因此 `REVOKE` 对 owner 无效。

决策：引入受限运行角色 `blockchain_app`（0054 创建），应用连接建立后
`SET ROLE blockchain_app`（`store.NewWithRuntimeRole`，由 `DB_RUNTIME_ROLE` 控制）。
迁移与运维工具继续使用 owner 账号。该角色：

- 允许：`SELECT` / `INSERT` / `UPDATE`（仅限链头推进等必要表）/ `DELETE`（业务表）；
- 拒绝：审计事件表的 `UPDATE` / `DELETE`、全部表的 `TRUNCATE`、审计表的 `DROP`。

## 影响

- 所有 S6 页面与 API 使用同一套权限键；文档键与实现键的差异在本 ADR 内一次性说明。
- `check_openapi.py` / `gen_openapi_index.py` 的路由发现扩展到 `sprint6_*.go`，
  否则契约会静默落后于实现（本次已修复）。
- 生产部署必须设置 `DB_RUNTIME_ROLE=blockchain_app`；未设置时启动日志会给出 WARN。
