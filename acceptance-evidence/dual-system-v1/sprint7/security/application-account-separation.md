# 应用账号与超级用户分离实测报告（ADR-017 §5 / S7-SEC-01）

> 任务：S7-SEC-01
> 要求来源：ADR-017 §5「应用账号与 owner/超级用户彻底分离」；计划 §23.7「权限提升」「审计篡改」两行
> 环境：Ubuntu VM `192.168.31.152`，PostgreSQL 18.6（容器 `deploy-postgres-1`），库 `blockchain_platform`
> 采集时间：2026-09-12T23:49+07:00（原始输出：`sprint7/security/application-account-separation/raw-privilege-measurement.txt`）
> 结论摘要：**运行角色隔离成立（PARTIAL PASS）；连接账号仍是超级用户 → §5 未完全满足（GAP-01）**

---

## 1. 实测对象与身份链

```text
API/Worker 进程  ──连接（LOGIN）──►  角色 blockchain（超级用户）
                                       │  每条新连接 AfterConnect 执行 SET ROLE
                                       ▼
                                  角色 blockchain_app（受限运行角色，NOLOGIN）
```

- 连接初始化代码：`backend/internal/store/db.go:21`（注释）、`backend/internal/store/db.go:34`（`conn.Exec(ctx, "SET ROLE "+role)`）
- 角色来源：迁移 `backend/migrations/postgres/0054_s6_append_only_hardening.sql:20-46`
- 开关：`deploy/.env` 中 `DB_RUNTIME_ROLE=blockchain_app`
- 运行期证据：`pg_stat_activity` 中 17 条应用连接的 `usename` 全部是 `blockchain`（连接账号），实际权限由 `SET ROLE` 收窄。

---

## 2. 关键测量结果（全部来自 raw-privilege-measurement.txt）

### 2.1 连接账号 blockchain —— **是超级用户**（GAP-01）

| 属性 | 值 |
|---|---|
| `rolsuper` | **t** |
| `rolcreatedb` | t |
| `rolcreaterole` | t |
| `rolbypassrls` | t |
| `rolreplication` | t |
| `rolcanlogin` | t |
| 应用实连数 | 17 |

SQL 原文：
```sql
select rolname, rolsuper, rolcreatedb, rolcreaterole, rolcanlogin, rolbypassrls, rolreplication
from pg_roles where rolname = 'blockchain';
--  blockchain | t | t | t | t | t | t
```

### 2.2 运行角色 blockchain_app —— 隔离正确

| 属性 | 值 | 结论 |
|---|---|---|
| `rolsuper` | f | ✅ |
| `rolcreatedb` / `rolcreaterole` | f / f | ✅ |
| `rolbypassrls` | f | ✅ |
| `rolcanlogin` | f | ✅ 不能直接登录，只能被 SET ROLE 进入 |
| 角色成员关系 | 0 行 | ✅ 未被授予任何其他角色 |
| 拥有的对象 | 表 0 / 序列 0 / schema 0 | ✅ 不是 owner |
| `CREATE ON SCHEMA public` | **false** | ✅ 无 DDL 能力 |
| 表级权限 | 149 表：SELECT 149 / INSERT 149 / UPDATE 135 / DELETE 134 | 业务读写正常 |

### 2.3 审计相关表逐表权限（has_table_privilege）

| 表 | SELECT | INSERT | UPDATE | DELETE | TRUNCATE | owner |
|---|---|---|---|---|---|---|
| `audit_events_v2` | t | t | **f** | **f** | **f** | blockchain |
| `audit_chain_anchors` | t | t | **f** | **f** | **f** | blockchain |
| `audit_integrity_incidents` | t | t | **f** | **f** | **f** | blockchain |
| `login_events` | t | t | **f** | **f** | **f** | blockchain |
| `cases` / `case_comments` / `identities` / `sessions` | t | t | t | t | **f** | blockchain |

```sql
select tableowner from pg_tables where tablename='audit_events_v2';   -- blockchain
select count(*) from pg_tables where tablename='audit_events_v2' and tableowner='blockchain_app';  -- 0
```

### 2.4 实测越权尝试（`SET ROLE blockchain_app` 后逐条执行）

| 语句 | 实际结果 |
|---|---|
| `SELECT count(*) FROM audit_events_v2` | ✅ 允许（1326 行） |
| `UPDATE audit_events_v2 SET action='x' WHERE seq=1` | ⛔ `ERROR: permission denied for table audit_events_v2` |
| `DELETE FROM audit_events_v2 WHERE seq=1` | ⛔ `ERROR: permission denied for table audit_events_v2` |
| `TRUNCATE audit_events_v2` | ⛔ `ERROR: permission denied for table audit_events_v2` |
| `UPDATE audit_chain_anchors SET chain='x'` | ⛔ `ERROR: permission denied for table audit_chain_anchors` |
| `DELETE FROM audit_chain_anchors` | ⛔ `ERROR: permission denied for table audit_chain_anchors` |
| `TRUNCATE cases` | ⛔ `ERROR: permission denied for table cases` |
| `DROP TABLE cases` | ⛔ `ERROR: must be owner of table cases` |
| `ALTER TABLE cases ADD COLUMN s7_probe int` | ⛔ `ERROR: must be owner of table cases` |
| `CREATE TABLE s7_probe(id int)` | ⛔ `ERROR: permission denied for schema public` |
| `CREATE ROLE s7_probe_role` | ⛔ `ERROR: permission denied to create role` |
| `GRANT blockchain_app TO blockchain` | ⛔ `ERROR: permission denied to grant role "blockchain_app"` |

同一批断言已在套件 `run-sprint7-sec-negative.sh` 的 `AUDIT-a`..`AUDIT-k` 中自动化（PASS）。

### 2.5 第二层防护（即使权限被误授也拦得住）

| 触发器 | 定义 | 状态 |
|---|---|---|
| `audit_events_v2_append_only` | `BEFORE DELETE OR UPDATE ON audit_events_v2` → `audit_append_only_guard()` | enabled |
| `audit_chain_anchors_immutable_trg` | `BEFORE UPDATE ON audit_chain_anchors` → `audit_chain_anchors_immutable()` | enabled |
| `audit_chain_anchors_no_delete` | `BEFORE DELETE ON audit_chain_anchors` → `audit_append_only_guard()` | enabled |

### 2.6 连接账号的提权能力（GAP-01 的可执行证明）

连接账号是超级用户，而 `SET ROLE` 是会话级、可被 `RESET ROLE` 撤销的：

```sql
-- 任何应用连接（以 blockchain 登录）都可以这样回到超级用户：
SET ROLE blockchain_app;   -- 应用当前的降权状态
SELECT current_user;       -- blockchain_app
RESET ROLE;                -- ← 单条 SQL 即恢复
SELECT current_user, (SELECT rolsuper FROM pg_roles WHERE rolname = current_user);
--  blockchain | t
```

代码审计：全仓库（`backend/**/*.go`）**不存在** `RESET ROLE`，也不存在 `set_config('role',...)`；
唯一的角色语句是 `db.go:34` 的 `SET ROLE`。因此**当前没有可利用的代码路径**，
但数据库层面没有任何机制阻止这条路径（例如连接角色不是超级用户、或 `ALTER ROLE ... SET ROLE`）。

---

## 3. 结论

| 判定项 | 结果 | 依据 |
|---|---|---|
| 应用运行角色 `blockchain_app` 是否受限（非超级用户、无 DDL、非 owner、无 BYPASSRLS） | **满足** | §2.2 / §2.4 |
| 审计表是否对应用只读追加（UPDATE/DELETE/TRUNCATE 拒绝） | **满足** | §2.3 / §2.4 / §2.5 |
| 应用是否**以非特权账号登录数据库**（与超级用户/owner 彻底分离） | **不满足（GAP-01）** | §2.1 / §2.6 |

**ADR-017 §5「应用账号与 owner/超级用户彻底分离」= PARTIAL。**
现状是「**运行期降权**」（superuser 登录 + 每条连接 SET ROLE），而不是「**账号分离**」。
两者的差别只在一条 SQL 语句：`RESET ROLE`。

本报告**不修改任何数据库权限**（按任务要求只报告）。

---

## 4. 建议的可执行 gate（供后续任务落地，本轮不改）

### GATE-01（建议 P1）：新增专用登录角色，应用不再以超级用户连接

```sql
-- 1) 建专用登录角色（NOLOGIN 的 blockchain_app 保持不变）
CREATE ROLE blockchain_runtime LOGIN PASSWORD '<由 secret store 注入>' NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS;
GRANT blockchain_app TO blockchain_runtime;
-- 2) 应用连接串改用 blockchain_runtime；不得改回 blockchain
-- 3) 收回 blockchain 的登录能力（确认无其他依赖后）
ALTER ROLE blockchain NOLOGIN;
```

落地后的自动化 gate（可直接加入 CI / 验收脚本）：

```bash
# G-1 连接账号必须是普通角色
psql -Atc "select not rolsuper and not rolcreatedb and not rolcreaterole and not rolbypassrls
           from pg_roles where rolname = '<应用连接角色>'"      # 期望 t
# G-2 运行角色仍然受限
psql -Atc "select not rolsuper and not rolbypassrls and not rolcanlogin
           from pg_roles where rolname='blockchain_app'"        # 期望 t
psql -Atc "select not has_table_privilege('blockchain_app','audit_events_v2','UPDATE')
              and not has_table_privilege('blockchain_app','audit_events_v2','DELETE')
              and not has_table_privilege('blockchain_app','audit_events_v2','TRUNCATE')"  # 期望 t
# G-3 降权开关必须显式存在
grep -c '^DB_RUNTIME_ROLE=blockchain_app' deploy/.env            # 期望 1
# G-4 应用连接不得是超级用户（运行期实测，而非只看配置）
psql -Atc "select count(*) from pg_stat_activity where datname='blockchain_platform'
           and usename in (select rolname from pg_roles where rolsuper)"   # 期望 0
```

### GATE-02（建议 P2）：把降权钉死在角色上，而不是依赖代码自律

```sql
-- 即使应用误用超级用户登录，也让降权在会话建立时自动生效且不可撤销
ALTER ROLE blockchain_runtime SET ROLE blockchain_app;
-- 并确保没有 RESET ROLE 的代码路径（当前已满足，需回归守门）
grep -rn "RESET ROLE" backend/ frontend/ scripts/   # 期望 0 命中
```

### GATE-03（建议 P3）：把 §2.4 的 12 条越权尝试固化进回归

本报告的 §2.4 表格已由 `run-sprint7-sec-negative.sh` 的 `AUDIT-a`..`AUDIT-k` 覆盖（PASS 11 条），
建议随 S7-QA-01 一起跑，防止 0054 的 REVOKE 被后续迁移无意放宽。

---

## 5. 证据清单

| 文件 | 内容 |
|---|---|
| `application-account-separation/raw-privilege-measurement.txt` | 本报告全部 SQL 与原始输出（15 组查询） |
| `negative-tests/files/AUDIT-role.txt` | 套件运行时对 `blockchain_app` 的 8 条越权实测原始结果 |
| `negative-tests/result.json` | `AUDIT-a`..`AUDIT-k` 的 PASS/FAIL 判定 |
| `backend/internal/store/db.go:21,34` | SET ROLE 的实现位置 |
| `backend/migrations/postgres/0054_s6_append_only_hardening.sql:20-46` | 角色创建与 REVOKE 的权威定义 |

## 6. 未验证项

1. 未验证「修改连接串为专用角色后应用是否仍正常工作」——本轮不允许改配置，属 GATE-01 的落地验证范围。
2. 未验证 ClickHouse / MinIO / NATS 的应用账号是否与各自超级用户分离（本轮只覆盖 PostgreSQL）。
3. 未验证 MCP 进程（`deploy/.env` 中另有 MCP 配置）是否也走 `DB_RUNTIME_ROLE`。
