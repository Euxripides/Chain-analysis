# API 新二进制发布与验证（2026-09-18）

> 依据：`docs/multiuser-login-and-isolation-plan.md` §4（P1-02 / P2-03/04）与需求方 2026-09-18 的发布指令。
> 发布方式（按需求方要求）：**以 VM 上当前已部署源码为基线，只移植本次后端改动**；不从本地脏工作树出包。

## 1. 发布前基线（回滚锚点）

| 项 | 值 |
|---|---|
| `bin/api` | `7b1deb54ea4890cbada6037461ce4872aa2455ab596f55aef6df6c69a56bedd6`（2026-09-17 23:53:37，57,345,865 B） |
| `bin/migrate` | `d48e58fec83bc2af83ed322cf3f47da2f84ed4fe7f3873c1887c5481d06bf062` |
| `bin/worker` | `f2dec36f0c157629ca7ce7019de65cd3da573bfa312f7dd236ed95a816fd31d3`（本次**未重建**） |
| 启动参数 | `ExecStart=<repo>/backend/bin/api`、`WorkingDirectory=<repo>/backend`、`EnvironmentFile=deploy/.env`（systemd user unit `blockchain-api.service`） |
| 构建配方 | `cd backend && go build -o bin/api ./cmd/api`（Makefile / `scripts/deploy_dual_system.py`），工具链 `go1.26.7 linux/amd64`，`CGO_ENABLED=1` |

**基线已证明**：把本次新增的 3 个迁移文件临时移出后，在**同一目录**用同一工具链重建，产物与在运行的二进制**逐字节一致**（同为 `7b1deb54…`）。这证明"VM 源码树 = 运行中二进制的源码"，发布基线成立。

## 2. 移植的改动（只此 9 处替换 + 5 个新文件）

| 文件 | 改动 |
|---|---|
| `internal/api/adminv1.go` | 注册 `DELETE /api/admin/v1/users/{id}`（`admin:user:delete`） |
| `internal/api/sprint6_identity.go` | 建用户即建**个人空间**（`kind='personal'` + `owner_identity_id` + `basic_user` 绑定）；删除"回退到组织最早工作区"分支；清理不再使用的 `errors`/`pgx` 导入 |
| `internal/iam/identity.go` | 新增 `StatusDeleted` |
| `internal/iam/loginevent.go` | 新增原因码 `account_deleted` |
| `internal/iam/service.go` | 密码登录 + MFA 第二步两条路径均拒绝 `deleted` 账号 |
| `internal/api/admin_delete_user.go`（新） | 删除（离职）处理器：软删 + 匿名化 + 撤权 + 归档个人空间 + 强制下线 + 审计 |
| `internal/api/user_isolation_static_test.go`（新） | 5 条静态护栏（禁止回退默认工作区 / 禁止绑 owner / 软删语义 / 禁止注册路由 / 删除态必须被拒） |
| `internal/policy/catalog.go` | 声明 `admin:user:delete`（**见 §4 缺陷一**） |
| `migrations/postgres/0066`、`0067`、`0068`、`0069` | 见 §3 |

移植手法：以 JSON 描述"文件 + 精确字符串对"（9 对），由脚本在 VM 基线上做**定点替换**，任一锚点匹配数 ≠ 1 即整体中止且不写盘。副作用仅为：源码树备份 `/home/euripides/backups/backend-src-pre-release-20260918.tar.gz`（121 MB）。

## 3. 数据库迁移

| 迁移 | 内容 | 状态 |
|---|---|---|
| `0066_workspace_kind.sql` | `workspaces.kind/owner_identity_id/created_by_identity/archived_at` + 约束与索引 | 已应用（2026-09-18） |
| `0067_identity_deletion.sql` | `identities.deleted_at` + 状态机 `deleted` | 已应用 |
| `0068_basic_user_role.sql` | `basic_user` 角色（73 权限 / 0 个 `admin:*`）+ `admin:user:delete` 键并授予 `platform_admin`/`security_admin` | 已应用 |
| `0069_admin_user_delete_permission_seed.sql` | 以单行形式重复声明同一条权限种子（**见 §4 缺陷二**） | 已应用 |

应用方式与 `internal/migrations.ApplyPostgres` 完全同语义（每个文件一个事务 + 写入 `schema_migrations`，名称带 `postgres/` 前缀）。**重复执行验证**：用新构建的 `migrate` 二进制再跑一次，`schema_migrations` 行数不变（68→68，无重复变更）。

## 4. 发布过程中发现并修复的两个缺陷

**缺陷一（会导致功能不可用）：权限键只进 SQL、没进 Go 目录。**
`admin:user:delete` 只写在迁移里，`internal/policy/catalog.go` 未声明。运行时策略引擎以 Go 目录为准，端点直接返回
`403 permission_denied, details.reason = "permission_not_declared"` —— 实测复现。
> 教训：AD-003 的"权限键必须声明"不是文档要求，是**运行时判定依据**；只加 SQL 会让功能静默不可用。

**缺陷二（会让一致性测试失真）：种子行换行导致解析器看不见。**
`internal/policy/catalog_sql_test.go` 以字面量 `…sensitive) VALUES` 定位种子块，而 0068 在该处换行，于是"Go 有、SQL 无"。已应用迁移不改写，新增 `0069` 以单行形式重复声明同一条（幂等）。

修复后：`go test ./internal/policy/`、`./internal/api/`、`./internal/iam/` 全部通过。

## 5. 发布产物

| 二进制 | SHA-256 | 说明 |
|---|---|---|
| `bin/api`（当前） | `08a2bcf496657e069cfd5cb3eeb9fa6bc7898c3a6ed7cafa13c4d87edaac1dc8` | 第二次构建（含 catalog 修复） |
| `bin/migrate`（当前） | `8741601a625a2dfff9aefabb2c35fea9dcfdc0474525b252455e9f067ea8ee45` | 内嵌 0066–0069 |
| `bin/api.prev-7b1deb54` | `7b1deb54…` | 回滚锚点（旧 api） |
| `bin/migrate.prev-d48e58fe` | `d48e58fe…` | 回滚锚点（旧 migrate） |

启动验证：`systemctl --user restart blockchain-api` → `health/live=200`、`health/ready=200`，日志出现
`clickhouse ready` → `minio ready` → `provider: ankr (live)` → `method signature seed ready` → `api listening`。

## 6. 端到端验证（隔离夹具组织内，47 项全通过）

脚本：`acceptance-evidence/dual-system-v1/tests/verify-user-lifecycle-isolation.py`（可重复执行；每轮自动建/停用夹具身份）。
结果：**PASS=47 FAIL=0**。

覆盖要点：

| 组 | 结论 |
|---|---|
| 建号即建个人空间 | `POST /users` → 201，返回 `personal_workspace_id`；个人空间 `kind=personal`、`owner_identity_id`=新用户、active 成员关系、绑定 `basic_user`；**新用户不落入任何共享工作区** |
| 新用户权限 | 会话权限 ≥50 条、**`admin:*` 为 0**；默认工作区 = 自己的个人空间 |
| 删除权限边界 | `owner` 删除 → **403**；`platform_admin` 创建 → 403；仅 `platform_admin` 删除 → 200；缺二次确认 → 428 |
| 软删语义 | `status=deleted`、`deleted_at` 已写、邮箱/显示名匿名化、个人空间 `archived_at` 已写、角色绑定撤销、两类成员关系移除、**身份行保留**、重复删除 → 409 |
| 已删除账号登录 | 403 `AUTHZ_ACCOUNT_DISABLED`（**与"停用"对外同形**）、不下发任何 Cookie、该身份无有效会话；`login_events` 记 `account_deleted`；删除前的旧会话立即 401 |
| 非授权身份 | `viewer` 创建/删除均 403 |
| 回归不变 | `basic_user` 的 `admin:*`=0、`admin:user:delete` 仅 `platform_admin,security_admin`、`admin:user:create` 仍仅 `owner`、**既有 20 个共享工作区与总数均未变化** |

## 7. 管理员权限口径（2026-09-18 需求方已定，已落地）

需求方口径：**「管理员有最高、所有权限」**。

实测事实：权限目录共 **179** 条，系统角色 `owner`（生产管理员 `admin@local` 的角色）原先持有 **178** 条 —— **唯一缺口正是 0068 新引入的 `admin:user:delete`**。也就是说按最初"删除仅限 platform_admin/security_admin"落地后，会出现：管理员能建号不能删人、platform_admin/security_admin 能删人不能建号，**没有任何真人账号能走完"建号 → 离职删除"闭环**。

处理（新增迁移 `0070_owner_full_permissions.sql`）：

- 授予 `owner` 的 `admin:user:delete`（幂等，只加一条授权）；
- **把"管理员有最高权限"固化为不变量**：迁移自带自检 —— owner 必须持有权限目录里的每一条；未来任何新增权限键若忘记授予 owner，迁移当场失败，而不是等线上发现"管理员点不动"。

落地后实测：`owner` 权限数 **179 / 179**，`admin:user:delete` 授予范围 = **owner, platform_admin, security_admin**（platform_admin/security_admin 的删除权保留，作为平台运维/安全角色的能力）。

端到端复验：**PASS=51 / FAIL=0**（管理员可删除并完成全部软删语义；platform_admin 仍不能建号；viewer 两个端点均 403）。

## 8. 回滚

```bash
cd /home/euripides/projects/blockchain-platform/backend/bin
install -m 755 api.prev-7b1deb54 api && install -m 755 migrate.prev-d48e58fe migrate
systemctl --user restart blockchain-api
```
数据库是**兼容性增量迁移**（只加列/加角色/加种子），不需要同步回滚；若必须回退数据侧，用
`/home/euripides/backups/blockchain_platform-pre-0066-20260918.dump`。