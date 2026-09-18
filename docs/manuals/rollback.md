# 回滚手册（应用 · 迁移 · 配置 · 灰度 · 紧急 Kill Switch）

> 适用环境：`euripides@192.168.31.152`，项目路径 `/home/euripides/projects/blockchain-platform`。
> 命令前缀（下文每条命令都假设已执行）：
>
> ```bash
> ssh euripides@192.168.31.152
> export XDG_RUNTIME_DIR=/run/user/$(id -u)
> cd /home/euripides/projects/blockchain-platform
> set -a; . deploy/.env; set +a
> ```
>
> ✅ = 已在 2026-09-12 实机执行验证；⚠️ = 来自脚本/代码分析，本次未执行。
> 关联文档：`operations.md`（部署与备份）、`troubleshooting.md`（故障）、`admin-guide.md`（Kill Switch 页面流程）、
> `docs/adr/AD-006`（迁移与 expand-compatible 发布）、`AD-008`（灰度路径）、`ADR-018`（审计完整性边界）。

---

## 0. 先决策：回滚还是修复？

| 判据 | 选择 |
|---|---|
| 新版本导致功能不可用，且**没有数据库破坏性变更** | **回滚应用二进制**（最快，分钟级） |
| 数据错写/漏写，但服务仍可用 | 定点修复 + 冻结点，不要整体回滚 |
| 迁移引入破坏性 schema 变更 | 本批次**没有**破坏性变更（见 §3），按 §3 评估 |
| 审计链出现 `boundary_conflict` | **禁止回滚数据库**；按安全事故处理（§9） |
| 安全事件（密钥泄露、越权、数据泄露） | 先 **Kill Switch / 冻结**（§6），再决定回滚 |
| 前端产物问题 | 只回滚前端（§2.3），后端不动 |

**回滚黄金规则**：任何回滚都必须**先记录回滚点**（版本 SHA、二进制备份名、备份 dump 名、当前时间），
并在回滚后执行 §7 验证清单。

---

## 1. 回滚点与前置准备

### 1.1 现存回滚点 ✅

```bash
ls -lt backend/bin/ | head -20
```

✅ 实测：`backend/bin/` 中存在大量历史备份，命名规则为
`api.bak-dualsystem-<YYYYMMDD-HHMMSS>`、`worker.bak-dualsystem-<YYYYMMDD-HHMMSS>`，
以及更早的 `api.pre-*` / `worker.pre-*`。部署脚本会为 api 与 worker **各留一份**。

> ⚠️ 注意：原 `docs/implementation/dual-system-runbook.md` §6 的回滚示例**只回滚了 api**，
> 而部署脚本实际备份了 api 与 worker 两个二进制。回滚时**两个都要考虑**
> （`docs/implementation/sprint6-report.md` 给出了包含 worker 的版本）。

### 1.2 回滚前必须做的三件事

```bash
STAMP=$(date +%Y%m%d-%H%M%S); echo "STAMP=$STAMP"

# 1) 记录当前版本与健康
curl -sS -o /tmp/rollback-health.json -w 'ready=%{http_code}\n' http://127.0.0.1:8080/health/ready
sha256sum backend/bin/api backend/bin/worker | tee /tmp/rollback-from.sha256

# 2) 额外再备份一次当前二进制（防止误覆盖已损坏版本）
cp backend/bin/api    backend/bin/api.bak-pre-rollback-$STAMP
cp backend/bin/worker backend/bin/worker.bak-pre-rollback-$STAMP

# 3) 数据库备份（回滚应用二进制不改数据库，但保留现场是强制要求）
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 \
  sh -c "pg_dump -U blockchain -d blockchain_platform -Fc -f /tmp/pre-rollback-$STAMP.dump"
docker cp deploy-postgres-1:/tmp/pre-rollback-$STAMP.dump /data/blockchain-platform/backups/
docker exec deploy-postgres-1 rm -f /tmp/pre-rollback-$STAMP.dump
```

✅ 已验证：`pg_dump -Fc` 单库备份可用（本次实测产出 86 MB dump）；
`sha256sum`、`cp` 常规可用。

---

## 2. 应用回滚

### 2.1 后端二进制回滚（API + Worker）⚠️

```bash
STAMP=<选择要回滚到的备份时间戳，例如 20260912-233320>

cp backend/bin/api.bak-dualsystem-$STAMP    backend/bin/api
cp backend/bin/worker.bak-dualsystem-$STAMP backend/bin/worker
chmod +x backend/bin/api backend/bin/worker

systemctl --user restart blockchain-api.service blockchain-worker.service
```

✅ 已验证部分：`systemctl --user restart blockchain-api.service` 可用，
重启后服务进入 `active`，就绪轮询返回 `ready=200`；
`backend/bin/api.bak-dualsystem-*` 与 `worker.bak-dualsystem-*` 备份文件真实存在。
⚠️ 本次**未真的用备份覆盖**运行中的二进制。

只回滚 API（Worker 保持新版）适用于：新版 Worker 行为正常、问题只在 API 时。

### 2.2 单服务回滚（最小影响）

```bash
systemctl --user restart blockchain-api.service        # ✅ 已验证
systemctl --user restart blockchain-worker.service     # ✅ is-active 已验证
```

- 先重启 API：SSE 与 REST 短连接自动重连，影响面最小。
- Worker 重启会中断进行中的分页，但**保留已成功页、游标与已提交数据**（DATA-006 语义），
  恢复后从最后成功游标继续。

### 2.3 前端产物回滚 ✅（2026-09-16 起有受控部署与留档）

两套前端由 `vite preview` 提供静态产物。**部署与回滚都走同一个脚本**：
`scripts/s7/deploy-frontend-controlled.sh` 固定顺序为
「记录 pre-state → `--outDir` 隔离构建 → 备份 dist → 原子替换 → 重启 → 服务端实测」，
因此每一版上线都会留下 `apps/<app>-console/dist.bak-<ts>` 作为回滚材料。

```bash
# 部署（构建 + 备份 + 原子替换 + 实测）
APP=user  bash scripts/s7/deploy-frontend-controlled.sh
APP=admin bash scripts/s7/deploy-frontend-controlled.sh

# 回滚：脚本在 summary 里直接打印可复制的一行
#   rollback: cp -a <app>/dist.bak-<ts> <app>/dist && systemctl --user restart blockchain-<app>-console
```

⚠️ **不要直接跑 `npm run build:user` / `build:admin` 来上线**：它们的 `emptyOutDir` 会就地重写
`dist`，而运行中的 `vite preview` 立即读取新产物 —— 等于「构建即部署」，且上一版产物被清空、
部署失败时没有回滚材料（2026-09-16 真实发生过一次）。自建产物请用 `STAGED=/abs/dist` 交给上面的脚本替换。

✅ 已验证：`http://127.0.0.1:3001/app-v2/` 与 `http://127.0.0.1:3002/admin/` 均可访问（200），
标题分别为「ChainSight 调查工作台」与「ChainSight 后台管理」。

### 2.4 MCP 回滚 ⚠️

```bash
cd mcp && git checkout <旧版本> && npm ci && npm run build   # 具体构建脚本以 mcp/package.json 为准
systemctl --user restart blockchain-mcp.service
```

---

## 3. 迁移回滚

### 3.1 原则（AD-006）

- 迁移**只新增、不修改历史文件**；`schema_migrations` 按文件名顺序、单文件单事务执行。
- 本批次（0040–0057）**全部是新增表、触发器、索引与约束，没有删列/删表** →
  **旧二进制可以继续运行**（expand-compatible）。因此**绝大多数情况下回滚应用即可，不需要回滚迁移**。
- **没有 down 迁移工具，也没有自动回滚编排**。只有两个手写脚本。

### 3.2 可用的回滚脚本

| 脚本 | 作用 | 前置条件 |
|---|---|---|
| `backend/migrations/rollback/rollback-0056.sql` | 回退 S7 的作用域**约束**（`DROP CONSTRAINT`、`DROP COLUMN scope_user_id`、`DROP FUNCTION s7_scope_audit`） | **不回退数据**（脚本注释明确：`feature_flag_evaluations` 的回填行不回退） |
| `backend/migrations/rollback/rollback-0057.sql` | 回退 S7 新增**索引**与分区辅助函数 | **分区结构不自动回滚** |

用法（脚本头部声明的用法）：

```bash
docker exec -i -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 \
  psql -U blockchain -d blockchain_platform < backend/migrations/rollback/rollback-0056.sql
```

⚠️ 本次**未执行**（会改动生产库结构）。执行前必须：
1. 做 §1.2 的数据库备份；
2. 确认没有代码依赖被删除的约束/函数；
3. 在维护窗口内执行，并逐条核对 `ALTER TABLE ... DROP CONSTRAINT` 的结果。

### 3.3 `rollback-0057.sql` 的两个真实缺陷（务必先读）

1. ~~`DROP FUNCTION` 签名不匹配~~ —— **已于 2026-09-12 修复**：脚本现在同时执行
   `DROP FUNCTION IF EXISTS public.s7_ensure_user_activity_partitions(int, date)`（正确签名）
   与 `(int)` 变体兜底。
2. ~~引用的文档不存在~~ —— **已修复**：分区结构回滚的分步命令已直接写入
   `rollback-0057.sql` 注释，并指向本文档。执行 `DETACH` + `RENAME` 仍需人工确认数据归属，
   历史数据会留在 `user_activity_events_hist`。

### 3.4 迁移回滚后必须验证

```bash
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 \
  psql -U blockchain -d blockchain_platform -Atc "select name from schema_migrations order by name desc limit 5"
./backend/bin/auditverify; echo "exit=$?"                 # ✅ 可用，期望 0
curl -sS -o /dev/null -w 'ready=%{http_code}\n' http://127.0.0.1:8080/health/ready
```

> 注意：`schema_migrations` 记录**不会**因执行 rollback 脚本而删除——回滚脚本只改结构，
> 不清理迁移记录。若要让工具重新应用某迁移，需要人工处理该表（**高风险，需评审**）。

---

## 4. 配置回滚

### 4.1 `.env`（进程环境）

```bash
cp deploy/.env deploy/.env.bak-$(date +%Y%m%d-%H%M%S)   # 改前先备份
# 编辑后重启受影响服务
systemctl --user restart blockchain-api.service blockchain-worker.service
```

常见需要回滚的配置项：

| 变量 | 回滚到 | 说明 |
|---|---|---|
| `AUTH_MODE` | `session` | 生产唯一允许值；`hybrid`/`legacy` 仅测试（`APP_ENV=production` 时会强制降级为 `session`） |
| `DB_RUNTIME_ROLE` | `blockchain_app` | ✅ 实测当前值；**丢失会失去数据库层 append-only 保护** |
| `AUTH_COOKIE_SECURE` / `AUTH_COOKIE_PREFIX` | 与入口协议一致 | 经 TLS 终止对外时应为 `true` / `__Host-` |
| `ANKR_*_CONCURRENCY`、`RPC_GLOBAL_CONCURRENCY` | 上一个安全值 | 并发上调属高风险操作，需审批 |
| `GOVERNANCE_DELETION_EXECUTION_ENABLED` | `false`（默认关闭） | 实际删除必须默认关闭 |

### 4.2 配置中心（`/admin/settings`，带 revision）

```bash
# 查看历史版本
curl -sS -b $JAR http://127.0.0.1:8080/api/admin/v1/config/revisions
# 回滚（命令端点；若目标版本含 secret，会要求 step-up）
curl -sS -b $JAR -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json' \
  -X POST -d '{"reason":"回滚到上一版本","step_up_password":"<密码>"}' \
  http://127.0.0.1:8080/api/admin/v1/settings/<key>/commands/rollback
```

### 4.3 功能开关与路由/性能模式

```bash
# 功能开关回滚（后台：/admin/feature-flags）
curl -sS -b $JAR -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json' \
  -X POST -d '{"reason":"回滚灰度"}' \
  http://127.0.0.1:8080/api/admin/v1/feature-flags/<key>/commands/rollback

# 性能模式回滚（后台：/admin/runtime/performance）；回滚受保护，可能要求 step-up
curl -sS -b $JAR -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json' \
  -X POST -d '{"reason":"回滚性能模式","step_up_password":"<密码>"}' \
  http://127.0.0.1:8080/api/admin/v1/runtime/performance/commands/rollback
```

**开关回滚只切换读/入口，数据库新数据保留，禁止通过回滚删除数据**（§29.3）。
另注：功能开关的评估**当前未接入请求路径**，所以开关回滚不能替代代码回滚。

---

## 5. 灰度回退（入口级）

灰度路径（AD-008）：用户层 `/app-v2/` → `127.0.0.1:3001`，后台 `/admin/` → `127.0.0.1:3002`，
旧 SPA `/` → `127.0.0.1:3000`。

**典型场景**：新用户层有问题，但后端要保留 → 把 `/app-v2/` 指回旧 SPA（或下线该 location）。

⚠️ 以下 `sed` 命令来自原运行手册，**本次未执行**（会改 Nginx 配置）：

```bash
sudo cp /etc/nginx/sites-enabled/blockchain-platform.conf /tmp/nginx-before-rollback.conf   # 先备份
sudo sed -i '/location ^~ \/admin\//,/^    }/d; /location ^~ \/app-v2\//,/^    }/d' \
     /etc/nginx/sites-enabled/blockchain-platform.conf
sudo nginx -t && sudo systemctl reload nginx
```

✅ 已验证可用：`sudo -n nginx -t`、`sudo -n systemctl reload nginx`。
⚠️ 本环境 `/etc/nginx/sites-enabled/blockchain-platform.conf` 是**普通文件**（不是软链接），
因此改 `sites-enabled` 即生效；但**下次部署会从 `deploy/nginx-blockchain-platform.conf` 覆盖回来**，
所以长期回退要同时改仓库里的源文件。

**更安全的替代**：只改 `deploy/nginx-blockchain-platform.conf` 里的 `proxy_pass` 目标
（例如 `/app-v2/` 指向 `http://127.0.0.1:3000`），再 `nginx -t` + reload。

**验证**：

```bash
curl -sS -o /dev/null -w 'app-v2=%{http_code}\n' http://127.0.0.1:8088/app-v2/   # ✅ 基线 200
curl -sS -o /dev/null -w 'admin=%{http_code}\n'  http://127.0.0.1:8088/admin/    # ✅ 基线 200
```

---

## 6. 紧急 Kill Switch

### 6.1 何时使用

- P0 级事件需要立即冻结自动化行为（成本失控、Provider 异常放大、数据错写扩散）；
- 需要"停止新增后台工作但保留现场与数据"时。

### 6.2 标准流程（后台 `/control/kill-switch`）

```text
影响预览 → 提交请求（原因 + step-up）→ 第二人审批 → 执行 → 健康检查 → 恢复或保持
```

对应 API（前缀 `/api/admin/v1`）：

```bash
curl -sS -b $JAR -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json' \
  -X POST -d '{"reason":"P0 成本失控"}' http://127.0.0.1:8080/api/admin/v1/control/kill-switch/preview

curl -sS -b $JAR -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json' \
  -X POST -d '{"reason":"P0 成本失控","step_up_password":"<密码>"}' \
  http://127.0.0.1:8080/api/admin/v1/control/kill-switch/requests

# 由**另一名**具备 admin:approval:decide 的人员审批
curl -sS -b $JAR2 -H "X-CSRF-Token: $CSRF2" -H 'Content-Type: application/json' \
  -X POST -d '{"note":"同意"}' \
  http://127.0.0.1:8080/api/admin/v1/control/kill-switch/requests/<id>/approve

# 发起人执行（再次 step-up）
curl -sS -b $JAR -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json' \
  -X POST -d '{"step_up_password":"<密码>"}' \
  http://127.0.0.1:8080/api/admin/v1/control/kill-switch/requests/<id>/execute

# 健康验证
curl -sS -b $JAR http://127.0.0.1:8080/api/admin/v1/control/kill-switch/health

# 恢复（需 admin:killswitch:release，同样受保护）
curl -sS -b $JAR -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json' \
  -X POST -d '{"step_up_password":"<密码>"}' \
  http://127.0.0.1:8080/api/admin/v1/control/kill-switch/requests/<id>/restore
```

### 6.3 实际效果与边界（实现事实）

- 执行会 `UPDATE api_control_settings SET kill_switch=$1, background_paused=$1 WHERE id=1`，
  随后运行真实健康探针（PG / ClickHouse / NATS / MinIO / 未关闭告警）。
- 当前环境中的真实消费点是 **token metadata 后台回填**（`tokenmeta/backfill.go` 读取
  `background_paused OR kill_switch`）。
- **默认不删除任务、不清空队列、不丢弃 Checkpoint**。
- 请求与审批**不能是同一用户**（应用层 + 数据库 CHECK 双保险；违规 403 `TWO_PERSON_REQUIRED`）。
- 恢复操作与开启操作同级保护，恢复后自动执行健康检查。

### 6.4 ⚠️ 遗留入口警告

`/api/v1` 兼容层存在另一条 `PATCH /api/v1/api-control/control/kill-switch`：
它只做角色判定（owner|admin），**不走审批、也不设置 `background_paused`**，
且 `/api/v1` 层整体没有 `s.require` 权限声明。
**紧急回滚流程只应使用 §6.2 的受控端点**；使用遗留端点会造成审批链缺失。

---

## 7. 回滚后的统一验证清单

- [ ] `systemctl --user is-active blockchain-api.service blockchain-worker.service` → `active` ✅
- [ ] `curl -sS -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8080/health/ready` → 200 ✅
- [ ] `curl ... http://127.0.0.1:8088/health/ready` → 200（经 Nginx）✅
- [ ] `/app-v2/`、`/admin/`、`/` → 200 ✅
- [ ] `./backend/bin/auditverify; echo $?` → 0 ✅
- [ ] `tail -n 50 /tmp/api.log` 无持续 `"level":"ERROR"`
- [ ] 后台 `/overview`、`/system` 正常；`/jobs` 有任务在推进
- [ ] 用 `/admin/audit` 按 Trace 核对本次回滚动作是否留下审计（重启不产生审计事件，但后续操作应有）
- [ ] 记录：回滚原因、回滚点（备份文件名）、执行人、时间、验证结果

---

## 8. 不可回滚 / 高风险回滚项（红线）

| 项 | 为什么不能回滚 | 正确做法 |
|---|---|---|
| `audit_integrity_incidents` 边界行 | 0055 起三张表有 `BEFORE UPDATE OR DELETE` 触发器，运行角色无 UPDATE/DELETE/TRUNCATE | 只能**新增**边界事件或新结论（ADR-018） |
| `audit_chain_anchors` 既有行 | 内容冻结触发器 + 禁止删除 | 保留原样；需要新结论就生成新锚点 |
| 审计链已写入事件 | append-only 触发器 + 哈希链 | 不回退；如需更正则新增事件说明 |
| 已封存缺口区间内的行 | 回填会被判 `boundary_conflict` | 永不回填 |
| 已 commit 的 ClickHouse 数据 | 无分区级回滚流程 | 按 Dataset/范围重新覆盖写入或走删除审批 |
| 已发放的下载票据 | 一次性、60 秒过期、只存摘要 | 等待自然过期 |
| `schema_migrations` 记录 | 回滚脚本不改它 | 人工处理需评审，不可顺手 DELETE |
| 用户活动分区的历史数据 | `DETACH` + `RENAME` 后数据留在 `_hist` | 人工确认归属后再动（§3.3） |

**受控维护逃生舱**（仅限经过审批的保留期/归档操作）：

```sql
BEGIN;
SET LOCAL app.audit_maintenance = 'on';
-- 仅限已审批的保留期/归档操作
COMMIT;
```

必须经过 `admin:deletion:approve` 审批，并记录到审计。

---

## 9. 演练建议

- 每季度做一次**应用回滚演练**：备份点 → 覆盖二进制 → 重启 → 跑 §7 清单 → 再前进到新版本。
- 每季度做一次**备份恢复演练**：按 `operations.md` §6.3 在临时库恢复并逐表比对行数，记录 RTO。
- 每次演练必须产出证据（命令输出、时间线、`auditverify` 结果），并更新本手册中标记 ⚠️ 的条目。

---

## 10. 待确认项

1. `scripts/deploy_dual_system.py` 在失败时打印的回滚命令**只包含 api 二进制**；
   worker 需按 §2.1 一并处理（`sprint6-report.md` 有包含 worker 的版本）。
2. 部署脚本第 4 步的说明文字写「应用迁移 0040 / 0041 / 0042」，实际执行的是**全量迁移**（含 0057）——文档与行为不一致。
3. ~~`rollback-0057.sql` 的函数签名缺陷与缺失文档~~ —— **已于 2026-09-12 修复**（见 §3.3 注 1/注 2）。
4. 前端产物回滚的具体构建命令未在本文固化（本次未实测构建），以 `scripts/deploy_dual_system.py` 第 5 步为准。
5. 迁移记录表的人工修正流程（示例：删除 `schema_migrations` 某行以强制重放）**未定义且高风险**，需要评审后补充。
6. 无自动化回滚编排；所有回滚均为人工步骤。
