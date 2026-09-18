# 运行手册（部署 · 迁移 · 升级 · 轮换 · 备份 · 巡检 · 告警）

> 适用范围：生产/灰度主机 `euripides@192.168.31.152`（主机名 `dataserver`），项目路径
> `/home/euripides/projects/blockchain-platform`。
> **命令验证状态**：本文用 ✅ 标记"已在 2026-09-12 于该主机实际执行并核对回显"的命令；
> 用 ⚠️ 标记"来自脚本/文档但本次未实测"的步骤。**未标记 ✅ 的命令不要当作已验证。**
> 关联文档：`docs/manuals/rollback.md`（回滚）、`docs/manuals/troubleshooting.md`（故障）、
> `docs/implementation/dual-system-runbook.md`（灰度阶段历史手册）、`docs/adr/AD-006`、`AD-008`、`ADR-017`。

---

## 1. 环境概览

### 1.1 远端登录与会话环境 ✅

```bash
ssh euripides@192.168.31.152
export XDG_RUNTIME_DIR=/run/user/$(id -u)      # 所有 systemctl --user / journalctl --user 命令的前提
cd /home/euripides/projects/blockchain-platform
set -a; . deploy/.env; set +a                 # 让 $POSTGRES_PASSWORD 等变量在后续命令中可用
```

> ✅ 实测：`hostname` = `dataserver`；`id -un` = `euripides`。
> 不导出 `XDG_RUNTIME_DIR` 时 `systemctl --user` 会报 "Failed to connect to bus"。

### 1.2 服务与端口 ✅

| 单元 | 端口 | 说明 |
|---|---|---|
| `blockchain-api.service` | `*:8080`（配置为 127.0.0.1:8080） | REST/SSE API、认证授权、审计写入 |
| `blockchain-worker.service` | `*:8081` | 下载调度 + 审计链定时校验/锚点归档 |
| `blockchain-user-console.service` | `127.0.0.1:3001` | 用户层系统（vite preview） |
| `blockchain-admin-console.service` | `127.0.0.1:3002` | 后台管理系统（vite preview） |
| `blockchain-vite.service` | `0.0.0.0:3000` | 旧 SPA（灰度期保留） |
| `blockchain-mcp.service` | `127.0.0.1:8090` | MCP Server |
| `nginx` | `127.0.0.1:8088` + `:443` | 对外入口（Cloudflare/直连） |

```bash
export XDG_RUNTIME_DIR=/run/user/$(id -u)
systemctl --user list-units --type=service --all --no-pager | grep -iE 'blockchain|vite'
systemctl --user is-active blockchain-api.service blockchain-worker.service \
  blockchain-user-console.service blockchain-admin-console.service blockchain-vite.service blockchain-mcp.service
# ✅ 实测（2026-09-12）：6 个单元全部 active，list-units 输出与上表一致
```

### 1.3 依赖服务（docker compose）✅

| 容器 | 镜像 | 主机端口 |
|---|---|---|
| `deploy-postgres-1` | postgres:18 | `127.0.0.1:5433` → 5432 |
| `deploy-clickhouse-1` | clickhouse/clickhouse-server:26.7 | `127.0.0.1:8123`、`127.0.0.1:9000` |
| `deploy-minio-1` | minio/minio | `127.0.0.1:9001`（API）、`127.0.0.1:9002`（Console） |
| `deploy-nats-1` | nats:2-alpine | `127.0.0.1:4222`、`127.0.0.1:8222` |

```bash
docker ps --format '{{.Names}}|{{.Status}}|{{.Ports}}'
cd deploy && docker compose --env-file .env ps --format '{{.Name}}|{{.State}}|{{.Health}}'
# ✅ 实测：postgres/clickhouse healthy；minio/nats running（无 healthcheck）
```

数据卷在 `/data/blockchain-platform/{postgres,clickhouse,minio,nats,backups,exports}`；
`/data` 为 `ubuntu--vg-data_lv`（✅ 实测 1.1T，已用 41G，可用 943G）。

### 1.4 日志位置 ✅

| 日志 | 路径 |
|---|---|
| API 结构化 JSON | `/tmp/api.log` |
| Worker | `/tmp/worker.log` |
| 用户端 / 管理端 / 旧 SPA | `/tmp/user-console.log`、`/tmp/admin-console.log`、`/tmp/vite.log` |
| MCP | `/tmp/mcp.log` |
| Nginx | `/var/log/nginx/blockchain-platform.{access,error}.log` |

```bash
tail -n 3 /tmp/api.log
tail -n 3 /tmp/worker.log
# ✅ 实测：API 行为 JSON（含 request_id/trace_id/route_template/status/duration_ms）
```

### 1.5 关键环境变量（`deploy/.env`）✅

```bash
set -a; . deploy/.env; set +a
echo "DB_RUNTIME_ROLE=$DB_RUNTIME_ROLE"   # ✅ 实测 blockchain_app
echo "AUTH_MODE=$AUTH_MODE"               # ✅ 实测 session（生产唯一允许值）
echo "POSTGRES_PORT=$POSTGRES_PORT"       # ✅ 实测 5433
```

- `DB_RUNTIME_ROLE=blockchain_app` **必须保持开启**：API/Worker 的每条数据库连接会 `SET ROLE` 到受限角色，
  从数据库层拒绝审计表 `UPDATE/DELETE`、拒绝 `TRUNCATE`（0054）。缺失时启动日志会打 WARN。
- `APP_ENV=production` 且 `AUTH_MODE` 为 `hybrid`/`legacy` 时，进程会把模式**强制降级为 `session`** 并打 ERROR 日志。

---

## 2. 日常巡检（建议每日执行一次）

### 2.1 健康与就绪 ✅

```bash
curl -sS -m 5 -o /dev/null -w 'ready=%{http_code}\n' http://127.0.0.1:8080/health/ready
curl -sS -m 5 -o /dev/null -w 'live=%{http_code}\n'  http://127.0.0.1:8080/health/live
curl -sS -m 5 -o /dev/null -w 'metrics=%{http_code}\n' http://127.0.0.1:8080/metrics
# ✅ 实测：ready=200 live=200 metrics=200
```

`/health/ready` 会真实检查依赖；任一依赖非 ok 返回 **503**（不是 200）。
经 Nginx 的同一检查：`curl -sS -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8088/health/ready` ✅ 实测 200。

### 2.2 前端入口 ✅

```bash
curl -sS -m 5 -o /dev/null -w 'app-v2=%{http_code}\n' http://127.0.0.1:8088/app-v2/
curl -sS -m 5 -o /dev/null -w 'admin=%{http_code}\n'  http://127.0.0.1:8088/admin/
# ✅ 实测：app-v2=200 admin=200
```

### 2.3 数据库连通与规模 ✅

```bash
# 表数量（✅ 实测 149）
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 \
  psql -U blockchain -d blockchain_platform -Atc \
  "select count(*) from information_schema.tables where table_schema='public'"

# 迁移记录（表名 schema_migrations；✅ 实测存在）
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 \
  psql -U blockchain -d blockchain_platform -Atc \
  "select name from schema_migrations order by name desc limit 5"

# 权限目录与角色授权条数（✅ 实测 177；owner=177 platform_admin=70 security_admin=37 auditor=30
#   investigator=85 analyst=72 reviewer=55 viewer=33 api_client=4）
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 \
  psql -U blockchain -d blockchain_platform -Atc "select count(*) from permissions"
```

### 2.4 审计链与完整性 ✅

```bash
./backend/bin/auditverify                    # 输出 JSON：链自洽性 + 完整性边界
echo "exit=$?"                                # 0=通过（valid 且 integrity_status 受支持）、1=不通过、2=参数/连接错误

bash scripts/s7/audit-chain-gap-check.sh     # 空洞巡检：出现**新增**空洞 -> 退出码 1
```

- `audit` 链存在已封存缺口 `INC-AUDIT-GAP-1-2343`，`integrity_status` 恒为 `sealed_incident`；
  另有 **2 条已声明的内容哈希不可复算行**（seq 52394、52399，`declared_anomaly_count=2`）。
- `login` 链存在 **8 处稀疏空洞**（seq 3791、3799、5911、5914、5915、5918、5919、5920），
  已逐条声明（`INC-LOGIN-DECLARED-LOST-8-3791-5920`）：
  `auditverify` 报 `valid=true`、`integrity_status=declared_events`、`declared_lost_count=8`。
  这类空洞**无法**用连续缺口封存表达（封存要求区间内没有幸存行，稀疏空洞必然违反），
  因此走 ADR-019 的**稀疏声明**：链上哈希证据逐条登记，其余区间仍逐行复算。
  声明是 append-only 的不可变事实，**不代表链完整**；判读口径见 `docs/manuals/auditor-guide.md` §3.4。
- 无论是否有声明，**新增空洞都必须为零**：
  `bash scripts/s7/audit-chain-gap-check.sh` 以 `deploy/audit-chain-gap-baseline.tsv` 为基线，
  出现新增空洞立即 FAIL 并列出具体 seq（负向对照见 `FIX-20260916-audit-chain-gap-controls.md`）。

### 2.5 运行期降权核对 ✅

```bash
grep '^DB_RUNTIME_ROLE=' deploy/.env      # ✅ 实测 blockchain_app
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 \
  psql -U blockchain -d blockchain_platform -Atc \
  "select has_table_privilege('blockchain_app','audit_events_v2','TRUNCATE')"   # 期望 f
```

### 2.6 Worker 日志巡检 ✅

```bash
tail -n 50 /tmp/worker.log | grep -i error
# ✅ 实测样例（真实存在，属 DATA-006/008 类现象）：
#   "page ingest failed" ... "spool load: open /var/lib/chain-platform/spool/jobs/<job>/token_transfers/<shard>/page-000177.raw: no such file or directory"
#   "incomplete fetched shard replay scheduled"
```

处置流程见 `docs/manuals/troubleshooting.md` §"spool 文件缺失 / Provider 对象缺失"。

---

## 3. 部署

### 3.1 标准部署（推荐）⚠️

```bash
# 在开发机（Windows/Git Bash），不是远端
python3 scripts/sync_to_vm.py                       # 只同步文件
python3 scripts/deploy_dual_system.py --yes         # 完整部署（备份二进制+同步+编译+装单元与站点+迁移+构建前端+重启+就绪等待+冒烟）
python3 scripts/deploy_dual_system.py --yes --skip-sync
python3 scripts/deploy_dual_system.py --yes --skip-migrate
```

> ⚠️ 本次**未执行**部署脚本本身（会改动运行中的服务与配置）。以上命令来自
> `docs/implementation/dual-system-runbook.md` §1 与 `scripts/deploy_dual_system.py` 的 `--help` 语义。
> 脚本固定顺序：备份二进制 → 同步 → 编译后端 → 安装 systemd 单元与 Nginx 站点 → **迁移** →
> 构建两套前端 → 重启 → 轮询 `/health/ready`（最多 90s）→ 冒烟 → 打印回滚命令；任一步失败即中止。
> 脚本会同时写入 `/etc/nginx/sites-available/blockchain-platform.conf` **与**
> `/etc/nginx/sites-enabled/blockchain-platform.conf`（后者在本环境是**普通文件**而非软链接，
> 只改 available 不生效——这是灰度期真实踩过的缺陷，见 AD-008）。

### 3.2 手工部署（脚本不可用时）✅ 关键步骤已验证

```bash
export XDG_RUNTIME_DIR=/run/user/$(id -u)
cd /home/euripides/projects/blockchain-platform

# 1) 备份当前二进制（可回滚点）
cp backend/bin/api    backend/bin/api.bak-dualsystem-$(date +%Y%m%d-%H%M%S)
cp backend/bin/worker backend/bin/worker.bak-dualsystem-$(date +%Y%m%d-%H%M%S)

# 2) 编译（远端 Go 在 /usr/local/go）
cd backend && /usr/local/go/bin/go build -o bin/api ./cmd/api && \
             /usr/local/go/bin/go build -o bin/worker ./cmd/worker && cd ..

# 3) 迁移（见 §4）
./backend/bin/migrate

# 4) 前端部署：**必须**用受控脚本（构建到隔离目录 → 备份 dist → 原子替换 → 重启 → 实测）
#    APP=user  bash scripts/s7/deploy-frontend-controlled.sh
#    APP=admin bash scripts/s7/deploy-frontend-controlled.sh
#    直接跑 npm run build:user/build:admin 会就地重写 dist（emptyOutDir），等于构建即部署且没有回滚材料。

# 5) 重启服务
systemctl --user daemon-reload
systemctl --user restart blockchain-user-console.service blockchain-admin-console.service
systemctl --user restart blockchain-api.service blockchain-worker.service

# 6) 等待就绪并验证
for i in $(seq 1 30); do
  code=$(curl -sS -m 5 -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/health/ready 2>/dev/null)
  echo "try $i: ready=$code"; [ "$code" = "200" ] && break; sleep 3
done
systemctl --user is-active blockchain-api.service blockchain-worker.service
```

✅ 已验证部分：`systemctl --user daemon-reload`、`systemctl --user restart blockchain-api.service`、
就绪轮询（实测重启后服务 `active`，下一次轮询 `ready=200`）、`curl` 检查、
`/usr/local/go/bin/go build` 的路径与 `make build` 等价（`Makefile` 第 47 行）。

### 3.3 Nginx 变更 ✅

```bash
sudo -n nginx -t                      # ✅ 实测：syntax is ok / test is successful
sudo -n systemctl reload nginx        # ✅ 实测：reload ok；reload 后 /health/ready、/app-v2/、/admin/ 均 200
sudo systemctl daemon-reload          # 若 systemd 提示 nginx 单元文件变更，需先执行
```

改配置时**必须同时改两处**（否则 `/app-v2/` 会被旧 SPA 的 fallback 吞掉）：

```bash
sudo cp deploy/nginx-blockchain-platform.conf /etc/nginx/sites-available/blockchain-platform.conf
sudo cp deploy/nginx-blockchain-platform.conf /etc/nginx/sites-enabled/blockchain-platform.conf
sudo nginx -t && sudo systemctl reload nginx
```

### 3.4 部署后验证清单

- [ ] `systemctl --user is-active` 六个单元全部 `active`
- [ ] `/health/ready` = 200（直连 8080 与经 Nginx 8088 各一次）
- [ ] `/app-v2/`、`/admin/`、`/` 均 200
- [ ] `./backend/bin/auditverify` 已知状态符合预期（`audit=sealed_incident` 且 `declared_anomaly_count=2`；
      `login=declared_events` 且 `declared_lost_count=8`；两者 `valid=true`，退出码 0）
- [ ] `bash scripts/s7/audit-chain-gap-check.sh` 输出 `GAP_CHECK=PASS`（**没有新增空洞**）
- [ ] `tail /tmp/api.log` 无 `level":"ERROR"`
- [ ] 记录 build SHA 与二进制哈希（部署脚本会打印）

---

## 4. 迁移

### 4.1 迁移工具与语义 ✅

```bash
cd /home/euripides/projects/blockchain-platform
./backend/bin/migrate                 # ✅ 实测：幂等，重复执行安全
```

- 工具是独立一次性程序（`backend/cmd/migrate`），**API/Worker 启动不会隐式执行迁移**（§29.1、AD-006）。
- 顺序：先 PostgreSQL（按**文件名**升序、单文件单事务、失败整体回滚、取
  `pg_advisory_lock('blockchain-platform:postgres-migrations')`）再 ClickHouse（逐条语句、设计为幂等、**无版本记录**）。
- 记录表：`schema_migrations(name text primary key, applied_at timestamptz)`。
- ✅ 实测输出（节选）：`postgres migrations applied: [postgres/0001_init.sql ... postgres/0057_s7_indexes_partitions.sql]
  clickhouse migrations applied: [clickhouse/0001_init.sql ... clickhouse/0007_address_query_indexes.sql] migrate done`。
- 当前最高迁移号：`0057_s7_indexes_partitions.sql`（编号存在重复前缀 0010/0013/0020 与缺口 0022/0023，
  因按完整文件名排序，结果仍然确定）。
- ⚠️ `Makefile` 中的 `make migrate-ch`（`go run ./cmd/migrate -target clickhouse`）**参数无效**：
  `cmd/migrate` 不解析任何命令行参数，任何一次执行都会先 PG 再 CH。

### 4.2 迁移前后检查清单

- [ ] 迁移前：确认已在**一次性数据库**验证过（`scripts/test_migration_reentrancy.sh` 会建
      `blockchain_platform_migtest` 并注入失败用例验证单文件事务）。
- [ ] 迁移前：备份（§6）；确认 `/data` 剩余空间充足。
- [ ] 迁移后：`select name from schema_migrations order by name desc limit 5` 包含新文件。
- [ ] 迁移后：`/health/ready` = 200；`./backend/bin/auditverify` 退出码 0。
- [ ] 迁移后：核对权限目录条数（本次基线 177）与角色授权条数未意外变化。

### 4.3 迁移回滚 ⚠️

只存在两个手写回滚脚本：`backend/migrations/rollback/rollback-0056.sql`、`rollback-0057.sql`（用法 `psql -f`）。
**没有 down 迁移工具，也没有自动回滚编排**。细节与注意事项见 `docs/manuals/rollback.md`。

---

## 5. 升级

1. 记录当前版本（`/admin/system/version` 页面或 `GET /api/admin/v1/system/version`）。
2. 按 §6 做一次备份（至少要 `pg_dump` 一份）。
3. 按 §3 部署新版本（`--skip-migrate` 仅用于明确不动数据库的场景）。
4. 按 §4 应用迁移。
5. 按 §3.4 验证；观察 30 分钟的错误率、`429`、审计落库与任务推进。
6. 出现异常按 `docs/manuals/rollback.md` 回滚。

灰度发布路径（AD-008）：用户层 `/app-v2/`、后台 `/admin/`、旧 SPA `/` 三套并存；
功能开关的分桶评估**尚未接入请求路径**，因此当前灰度主要靠路径与部署控制，不要依赖开关做线上发布闸门。

---

## 6. 备份与恢复

### 6.1 现状（务必先读）

| 能力 | 现状 |
|---|---|
| 产品内"备份"接口 | `POST /api/admin/v1/backups` 的 `target` 虽接受 `postgres|clickhouse|minio|config|all`，**实现只生成配置快照**（`configuration_settings` + `feature_flags` 的 JSON，写 MinIO `backups/config/YYYY/MM/DD/<id>.json`） |
| 产品内"恢复"接口 | `POST /api/admin/v1/restores` **只登记 queued 请求**（kind=`restore`、status=`queued`），**没有执行恢复的端点** |
| 数据库物理备份 | 仓库已新增 `scripts/backup/backup-all.sh`（PG + ClickHouse + MinIO + manifest）；**尚未同步到主机、也未纳入调度**（见 §6.7）。手工流程见 §6.2 |
| WAL / PITR | `archive_mode=off`、`archive_command=(disabled)`、`wal_level=replica`（✅ 实测）→ **无 PITR 能力** |
| ClickHouse 备份 | `backup-all.sh` 使用 `BACKUP DATABASE ... TO Disk('backups', ...)`，但**当前 ClickHouse 只配置了 `default` 磁盘**（✅ 实测 `system.disks` 仅一行），`/var/lib/clickhouse/backups` 不存在 → **该步骤会失败，需先配置 backup disk** |
| MinIO 桶级备份 | `backup-all.sh` 用 `minio/mc` 镜像做 `mc mirror`（✅ 实测主机已存在 `minio/mc:latest` 镜像）；另有对象级工具 `backend/bin/objectprobe` |
| systemd timer / cron | **不存在**（仓库 0 命中，主机无相关定时单元）；备份脚本注释提到"定时器据此告警"，但定时器本身尚未落地 |

> 直接后果：**RPO 无法由 PITR 保证**。Sprint 6 期间的真实审计缺口事件（ADR-018）核查结论就是
> "环境内不存在任何可用恢复源"。要满足计划的 `RPO<=15min / RTO<=4h`，必须先建立本节流程并演练。

### 6.2 PostgreSQL 备份 ✅（命令已实测）

```bash
set -a; . deploy/.env; set +a
STAMP=$(date +%Y%m%d-%H%M%S)
OUT=/data/blockchain-platform/backups/pg-$STAMP.dump

docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 \
  sh -c "pg_dump -U blockchain -d blockchain_platform -Fc -f /tmp/pg-$STAMP.dump"
docker cp deploy-postgres-1:/tmp/pg-$STAMP.dump "$OUT"
docker exec deploy-postgres-1 rm -f /tmp/pg-$STAMP.dump
sha256sum "$OUT" | tee "$OUT.sha256"
ls -lh "$OUT"
# ✅ 实测：命令可用；本次样例 dump 大小 86 MB（数据库含真实链上数据与审计事件）
```

- 自定义格式（`-Fc`）便于 `pg_restore -j` 并行恢复与选择性恢复。
- `pg_basebackup` 在容器内可用（`/usr/bin/pg_basebackup`，✅ 实测存在），但**基础备份要求归档配置**，
  当前 `archive_mode=off`，做完基础备份也无法 PITR。
- 备份产物放在 `/data/blockchain-platform/backups` 或转存到主机外的存储；**不要只留在同一块盘上**。

### 6.3 PostgreSQL 恢复 ⚠️（未实测；步骤来自 `scripts/s7/_run701.sh`）

```bash
# 1) 先建一个空库做验证恢复，绝不要直接覆盖生产库
TARGET=blockchain_platform_dr_restore
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 \
  psql -U blockchain -d postgres -c "DROP DATABASE IF EXISTS $TARGET"
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 \
  psql -U blockchain -d postgres -c "CREATE DATABASE $TARGET"

# 2) 把 dump 拷进容器后恢复（并行 4）
docker cp /data/blockchain-platform/backups/pg-<stamp>.dump deploy-postgres-1:/tmp/dr.dump
time docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 \
  pg_restore -U blockchain -d $TARGET -j 4 --no-owner --no-privileges /tmp/dr.dump

# 3) 逐表比对行数（示例：审计事件）
for t in audit_events_v2 login_events permissions role_permissions; do
  a=$(docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U blockchain -d blockchain_platform -Atc "select count(*) from $t")
  b=$(docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U blockchain -d $TARGET -Atc "select count(*) from $t")
  echo "$t src=$a restored=$b"
done
```

> ⚠️ 上述命令**本次未执行**（会在共享环境建库）。执行前确认磁盘空间与目标库名。
> 恢复演练的验收要求见 §27 DATA-009（达到 RPO/RTO，且 hash chain 与对象引用完整）。
> 演练完成后务必 `DROP DATABASE` 清理临时库。

### 6.4 ClickHouse 备份 ⚠️（脚本已存在，前置条件未满足）

仓库中的 `scripts/backup/backup-all.sh` 使用原生 `BACKUP DATABASE`：

```bash
docker exec deploy-clickhouse-1 clickhouse-client --user $CLICKHOUSE_USER --password $CLICKHOUSE_PASSWORD \
  --query "BACKUP DATABASE $CLICKHOUSE_DB TO Disk('backups', '<UTC时间戳>')"
docker exec deploy-clickhouse-1 sh -c "cd /var/lib/clickhouse/backups && tar czf - <UTC时间戳>" > clickhouse.tar.gz
```

**阻断点（✅ 实测）**：当前 `system.disks` 只返回一行 `default /var/lib/clickhouse/ Local`，
没有名为 `backups` 的磁盘，`/var/lib/clickhouse/backups` 目录也不存在。
必须先为 ClickHouse 配置 backup disk（例如在容器配置中加入
`<storage_configuration><disks><backups><type>local</type><path>/var/lib/clickhouse/backups/</path></backups></disks></storage_configuration>`
并把该路径挂载到宿主持久卷），否则备份会在第 2 步失败。

恢复侧由 `scripts/backup/restore-drill.sh` 用
`RESTORE DATABASE <db> AS <db>_dr_<TS> FROM Disk('backups','<备份名>')` 恢复到**独立新库**，核对后 `DROP`。⚠️ 未实测。

### 6.5 MinIO 备份 ⚠️（对象级工具）

`backend/bin/objectprobe` 提供 `stat|backup|delete|restore` 四个动作与 `-bucket`/`-key`/`-file` 参数：

```bash
./backend/bin/objectprobe -h
# ✅ 实测输出：
#   Usage of ./backend/bin/objectprobe:
#     -action string   stat|backup|delete|restore (default "stat")
#     -bucket string   bucket（默认 config RawBucket）
#     -file string     backup/restore 本地文件
#     -key  string     （必填）
```

- 主机上没有 `mc` 客户端二进制（✅ 实测 `which mc` 无输出），
  但 `scripts/backup/backup-all.sh` 通过 `docker run --rm --network host minio/mc:latest` 使用它
  （✅ 实测主机已存在该镜像）。
- 桶：`RAW_BUCKET=blockchain-raw`、`EXPORT_BUCKET=blockchain-exports`、`BACKUPS_BUCKET=blockchain-backups`（`.env` 配置），
  审计归档桶为 `blockchain-audit`（代码常量）。
- ⚠️ 桶级备份/恢复流程未实现，需按业务重要性补齐并演练。

### 6.6 MinIO 备份的桶清单（来自 `scripts/backup/backup-all.sh`）

脚本对以下桶执行 `mc mirror`：`blockchain-audit`、`blockchain-backups`、`blockchain-exports`、`blockchain-reports`；
`blockchain-raw`（约 16 GiB 大对象桶）默认**不在**列表内，可用 `BACKUP_SKIP_BUCKETS` 进一步排除。
备份根目录默认为 `/home/euripides/backups/blockchain-platform/<UTC 时间戳>/`，
内含 `postgres.dump`、`clickhouse.tar.gz`、`minio/<bucket>/`、`manifest.json`（每个文件的 size + sha256）
与 `postgres.migration_count`/`postgres.audit_head_seq`/`clickhouse.table_count` 等口径文件；
失败时写 `__FAILED` 标记并非 0 退出；保留 7 天（`BACKUP_RETENTION_DAYS`）。

### 6.7 S7-DR-01 备份与演练脚本（仓库内已存在，本次未在主机执行）

| 脚本 | 作用 | 关键前置条件（已实测核对） |
|---|---|---|
| `scripts/backup/backup-all.sh` | PG + ClickHouse + MinIO 一次性备份 + manifest | ⚠️ **ClickHouse backup disk 未配置**（`system.disks` 只有 `default`）；⚠️ 脚本尚未同步到 `/home/euripides/projects/blockchain-platform/scripts/backup/`（✅ 实测远端无该目录） |
| `scripts/backup/restore-drill.sh` | 恢复到**独立新目标**（新库 `blockchain_platform_dr_<TS>`、CH `AS <db>_dr_<TS>`、新 bucket），实测 RTO、逐表行数比对，并对恢复库跑 `auditverify` | 需要先有备份目录；✅ 主机已有 `minio/mc:latest` 镜像 |
| `scripts/backup/kill9-drill.sh` | Worker/API/NATS 的 Kill -9 恢复演练（覆盖 DATA-006/007），记录基线→kill→RTO→语义不变量 | 需要 `/tmp/s6-e2e-env.sh` 中的 `ADMIN_PASSWORD` |

演练报告输出到 `acceptance-evidence/dual-system-v1/sprint7/dr/`；
✅ 实测主机上 `acceptance-evidence/dual-system-v1/sprint7/` 已存在（`backups/`、`db/`、`dr/`、`security/`）。
执行前请先解决 ClickHouse backup disk 配置，否则 `backup-all.sh` 会在第 2 步失败（会留下 `__FAILED` 标记）。

### 6.8 RPO / RTO 现状对照

| 指标 | 计划目标 | 当前能力 |
|---|---|---|
| RPO | ≤ 15 分钟 | **不满足**（无 WAL 归档、无调度备份；只能靠手工 `pg_dump` 间隔） |
| RTO | ≤ 4 小时 | 未演练；`pg_restore` 单库恢复时间需实测（`scripts/s7/_run701.sh` 中有 RTO 计时逻辑） |

---

## 7. 密钥轮换

### 7.1 Provider 密钥（后台/API）✅ 路由与行为已确认

```bash
# 后台页面：/admin/providers/:provider → 密钥列表 → 轮换
# API（需 admin 会话 + CSRF + step-up）：
curl -sS -b $JAR -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json' \
  -X POST -d '{"reason":"90 天定期轮换","step_up_password":"<你的密码>","secret_ref":"<引用名>"}' \
  http://127.0.0.1:8080/api/admin/v1/providers/<provider>/commands/rotate-secret
```

- 只接受 **`secret_ref` 引用名**，拒绝明文；响应恒为 `plaintext_returned:false`。
- 旧记录置 `retiring`、插入新 `active` 行、记录前缀（≤6 字符）与指纹；审计动作 `admin.provider.rotate_secret`。
- **无条件 step-up**；权限声明为 `admin:provider:read`（目录中的 `admin:provider:key:rotate` 未在该处理器内校验——见 `permissions.md` 待确认项）。
- 新密钥值本身通过环境注入或 Secret Store 提供，系统**不回显、不入库明文**。

### 7.2 应用密钥与 API/MCP Key ✅

```bash
# 后台页面：/admin/api-keys → 轮换 / 吊销
# API：POST /api/admin/v1/api-keys/{id}/commands/rotate|revoke （step-up + 原因必填）
```

明文只在创建响应中出现一次；数据库只存 SHA-256 摘要。环境变量层面的密钥：
`GPT_ACTIONS_API_KEY`/`GPT_ACTIONS_API_KEYS`、`MCP_API_KEY`、`ANKR_RPC_API_KEY`、
`ANKR_MANAGEMENT_ENCRYPTION_KEY`（启用 Management 时强制 ≥32 字符）。

### 7.3 会话令牌轮换（自动）✅

刷新令牌每次 refresh 都旋转；旧 token 再次使用会触发整个 token family 撤销
（`internal/iam/session.go`，审计 `AUTHN_SESSION_REUSE`）。无需人工轮换。

### 7.4 管理账号密码 ✅

```bash
cd /home/euripides/projects/blockchain-platform
set -a; . deploy/.env; set +a
./backend/bin/admin-init -email admin@local            # 交互式输入，不写默认密码
echo '<强随机密码>' | ./backend/bin/admin-init -email admin@local -stdin   # 管道方式
```

规则：设置后 `must_change_password=true`，该身份的全部既有会话被撤销，
写入 `auth.password.reset`（风险等级 critical）。`admin@local` 在本环境 `must_change_password=t`（✅ 实测）。

### 7.5 轮换检查清单

- [ ] 轮换前记录旧密钥的最后使用时间（`/admin/providers/:provider` 密钥摘要）。
- [ ] 轮换后立即做一次受控连接测试（`commands/test`）。
- [ ] 确认审计中出现 `admin.provider.rotate_secret` / `admin.apikey.rotate`，且包含 `second_confirm`。
- [ ] 观察 24 小时错误率与 Provider 鉴权失败。
- [ ] 定期（建议 90 天）轮换；保留双 key overlap 窗口后再撤销旧 key。

---

## 8. 分区维护（`user_activity_events`）

`0057_s7_indexes_partitions.sql` 提供了 4 个函数与 1 张日志表：

| 函数 | 作用 | 是否只读 |
|---|---|---|
| `s7_ensure_user_activity_partitions(p_months_ahead int, p_start date)` | 幂等创建月度分区并授权 | 否（建分区 + 写日志） |
| `s7_partition_archive_candidates(p_retention_days int)` | 列出可归档候选分区 | **是**（不执行 DETACH/DROP） |
| `s7_partition_status()` | 列出全部分区与行数 | **是** |
| `s7_partition_readiness()` | 对 `login_events`/`audit_events_v2` 给出分区改造就绪门禁 | **是** |

### 8.1 查看现状 ✅

```bash
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 \
  psql -U blockchain -d blockchain_platform -c "select * from s7_partition_status()"
# ✅ 实测：user_activity_events 共 5 个分区：_202609 / _202610 / _202611 / _default / _hist

docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 \
  psql -U blockchain -d blockchain_platform -c "select * from s7_partition_archive_candidates(12)"
# ✅ 实测：0 行（当前没有满足归档条件的分区）

docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 \
  psql -U blockchain -d blockchain_platform -c "select * from s7_partition_readiness()"
# ✅ 实测：login_events 与 audit_events_v2 均为 ready=f，blockers 明确列出：
#   "UNIQUE(chain,seq) 需要全局唯一，PostgreSQL 分区表不支持全局唯一索引"
#   "organization_id 参与审计哈希链规范化，重写数据会破坏 AUDIT-004 链校验"
#   "append-only 行触发器 audit_append_only_guard 属 Sprint 6 完整性边界（0054/0055），不得绕过"
```

**结论（务必遵守）**：`login_events` 与 `audit_events_v2` **当前不可分区**——
分区化需要重写数据，会破坏哈希链与 append-only 边界。门禁函数就是用来阻止这件事的。

### 8.2 维护操作 ⚠️

```bash
# 提前创建未来分区（幂等；默认提前 2 个月）
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 \
  psql -U blockchain -d blockchain_platform -c "select s7_ensure_user_activity_partitions(2)"

# 维护日志
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 \
  psql -U blockchain -d blockchain_platform -Atc \
  "select table_name, action, partition_name, detail, created_at from partition_maintenance_log order by created_at desc limit 20"
```

- ⚠️ 以上两条本次未执行（会创建分区/写入日志）。
- **没有任何调度**调用这些函数（无 systemd timer、无 cron、代码中无调用点）；需人工按周期执行并记录。
- 归档动作只到"列出候选"为止；真正的 `DETACH`/`DROP` 需在导出 + `admin:deletion:approve` 双人审批后人工执行，
  且必须先确认数据归属。相关回滚限制见 `rollback.md`。

---

## 9. 监控与告警处置

### 9.1 可观测入口

| 入口 | 说明 |
|---|---|
| `GET /metrics` | Prometheus 指标（API 进程；✅ 实测 200） |
| `GET /health/live`、`/health/ready` | 存活/就绪（就绪会检查依赖） |
| Worker | `WORKER_METRICS_PORT=8081` 提供独立指标端口 |
| `/admin/observability` | SLO、指标、告警、手工评估 |
| `/admin/alerts` | 告警中心（认领/抑制/解决） |
| `postgres_exporter` | 未部署（如需请自行接入） |

### 9.2 SLO（计划 §25.3）

| SLO | 目标 | 窗口 |
|---|---|---|
| 用户 API 可用性 | 99.9% | 30 天 |
| 管理只读 API 可用性 | 99.9% | 30 天 |
| 登录成功请求 P95 | <800ms（不含外部 IdP） | 1 小时 |
| 常规查询 P95 | <2s | 1 小时 |
| 后台列表 P95 | <1.5s | 1 小时 |
| 审计落库 | 99.99% <2s | 1 小时 |
| Session/权限撤销生效 | <5s | 每次 |
| 终态通知 | <10s | 每次 |
| Outbox 最老未发布 | <30s | 持续 |
| RPO / RTO | ≤15 分钟 / ≤4 小时 | 灾难 |

> 吞吐指标必须分开统计：Provider 拉取、Fetch+Spool、完整 Pipeline、Job wall-clock、导出生成、
> 内网下载、公网下载——不可混合口径。

### 9.3 告警级别与首轮处置（计划 §25.4）

| 级别 | 定义 | 首轮处置 |
|---|---|---|
| **P0** | 数据跨租户泄露、审计无法写入且写操作仍继续、密钥泄露、数据不可恢复、全局不可用 | 安全优先：**若关键写操作无法记录审计，默认 fail closed**；启用 Kill Switch 或停机；保留现场（日志/DB 快照）后上报 |
| **P1** | 登录大面积失败、权限撤销不生效、Provider 全不可用、任务进度不前进、导出数据损坏 | 定点降级：切 fallback Provider、暂停问题任务、核对 `authz_version` 与缓存失效 |
| **P2** | 单模块降级、SLO 违约、延迟升高、有 fallback 的 Provider 故障 | 按 `troubleshooting.md` 对应症状处理 |
| **P3** | 容量趋势、非关键 enrichment 失败、低风险 UI 错误 | 记录并纳入例行维护 |

Kill Switch 的使用流程见 `admin-guide.md` §5.21 与 §6（影响预览 → 原因 → step-up → 第二人审批 → 执行 → 健康验证）。
当前环境 Kill Switch 的真实消费点是 token metadata 后台回填（`background_paused OR kill_switch`）；
它**不会**停止已运行的任务、不清队列、不丢 Checkpoint。

---

## 10. 配置与开关

```bash
# 配置项与历史（后台：/admin/settings）
curl -sS -b $JAR http://127.0.0.1:8080/api/admin/v1/settings
curl -sS -b $JAR http://127.0.0.1:8080/api/admin/v1/config/revisions

# 功能开关（后台：/admin/feature-flags）
curl -sS -b $JAR http://127.0.0.1:8080/api/admin/v1/feature-flags
```

- 修改配置需 `If-Match` + 原因；secret 类配置额外需要 step-up。
- 配置只保存 `secret_ref`，不保存明文。
- 关键治理开关：`governance.deletion.execution_enabled`（关闭时删除请求执行返回 412 `DELETION_DISABLED`）、
  `audit.export.max_events`（审计导出行数上限，默认 200000）、`control.kill_switch.default_scope`。

---

## 11. 常见运维任务速查

| 任务 | 命令 |
|---|---|
| 重启 API | `systemctl --user restart blockchain-api.service` ✅ |
| 重启 Worker | `systemctl --user restart blockchain-worker.service` ✅（`is-active` 已验证） |
| 重载 systemd 单元 | `systemctl --user daemon-reload` ✅ |
| 看最近日志 | `journalctl --user -u blockchain-api.service -n 50 --no-pager` ✅ |
| 校验 Nginx | `sudo -n nginx -t` ✅ |
| 重载 Nginx | `sudo -n systemctl reload nginx` ✅ |
| 跑迁移 | `./backend/bin/migrate` ✅ |
| 校验审计链 | `./backend/bin/auditverify` ✅ |
| 备份数据库 | 见 §6.2 ✅ |
| 看分区状态 | `select * from s7_partition_status()` ✅ |

---

## 12. 待确认项与已知缺口

1. **备份尚未纳入调度**：仓库已有 `scripts/backup/backup-all.sh`，但主机上既没有该脚本（未同步）、
   也没有 systemd timer/cron 承担定时备份；需补齐后再声称满足 RPO。
2. **恢复无执行端点**：`POST /restores` 只登记请求；实际恢复必须按 §6.3/§6.7 手工执行或跑演练脚本（本次未演练）。
3. **ClickHouse 备份前置条件未满足**：`system.disks` 只有 `default`，需要先为 ClickHouse 配置
   `backups` 磁盘（并在 compose 中持久化），否则 `BACKUP DATABASE ... TO Disk('backups', ...)` 会失败。
4. ~~**`rollback-0057.sql` 存在缺陷**~~：**已于 2026-09-12 修复**——`DROP FUNCTION` 改为
   正确签名 `(int, date)`（并保留 `(int)` 变体兜底），文档引用改指向 `docs/manuals/rollback.md`，
   分区结构回滚的分步命令直接写入脚本注释。
5. **升级/密钥轮换 Runbook 原先缺失**（`dual-system-progress.md` 自述未写）——本文补上了可执行部分，
   但 ClickHouse/MinIO 备份与恢复步骤仍是空白。
6. `acceptance-evidence/dual-system-v1/sprint7/` 目录已存在（本地与主机均有 `backups/`、`db/`、`dr/`、`security/`），
   但 `dr/` 下的备份/恢复与 Kill -9 演练报告**是否已产出并通过**未在本次核对（本文所有 ⚠️ 项均需在维护窗口验证）。
7. `.codex-deploy-actions/` 目录只有空目录、0 个文件，用途未确认。
8. 本次**未执行**部署脚本与恢复流程；上表中标记 ⚠️ 的步骤需在维护窗口内验证后再标记为已验证。

---

## 13. S7-DR-01 实测补充（2026-09-12/13 演练后更新，替换 §6.7/§6.8 与 §12 的相关条目）

> 本节所有命令与数字均来自 2026-09-12/13 的真实演练，证据在
> `acceptance-evidence/dual-system-v1/sprint7/dr/` 与 `.../backups/`。

### 13.1 备份（已验证可用）

```bash
ssh euripides@192.168.31.152 'cd ~/projects/blockchain-platform && bash scripts/backup/backup-all.sh'
# 实测：总耗时 1326.54 s（22.1 分钟）/ 9.9 GiB / PG 86,867,946 B / CH 归档 10,532,858,291 B
# 产出 <BACKUP_ROOT>/<UTC 时间戳>/{postgres.dump,clickhouse.tar.gz,minio/**,manifest.json}
```

- **ClickHouse 必须用 `TO File(...)`**：本实例 `system.disks` 只有 `default` 且未配置
  `backups.allowed_disk`，`TO Disk('backups', ...)` 会直接报 `INVALID_CONFIG_PARAMETER`。
  `backup-all.sh` 已改为 `BACKUP DATABASE … TO File('<name>')`（对应 `config.xml` 的
  `backups.allowed_path`），归档位置 `/var/lib/clickhouse/backups/<name>`。
- `BACKUP_BUCKETS` 默认包含 `blockchain-raw`（约 16 GiB，原始链上对象）。
  本次演练的主备份**未包含** raw 桶，报告里已显式声明；下次演练需覆盖。
- 保留策略：`BACKUP_RETENTION_DAYS`（默认 7 天），只清理自己产出的时间戳目录。

### 13.2 恢复演练（已验证可用）

```bash
ssh euripides@192.168.31.152 'cd ~/projects/blockchain-platform && bash scripts/backup/restore-drill.sh'
```

| 组件 | 命令要点 | 实测 RTO |
|---|---|---|
| PostgreSQL | `pg_restore -j 4 --no-owner --no-privileges` 到新库 `blockchain_platform_dr_<ts>` | **5.81 s** |
| ClickHouse | `RESTORE DATABASE <db> AS <db>_dr_<ts> FROM File('<name>')` | **561.36 s** |
| MinIO | `mc mirror /backup/blockchain-reports s3/<新 bucket>` | **0.63 s** |

**两条必须遵守的口径（本次踩过坑）**：
1. **演练目标名一律小写**：PostgreSQL 会把未加引号的标识符折叠为小写、MinIO 桶名不允许大写字母，
   用带 `T`/`Z` 的时间戳会出现"建的是小写库、连的是大写库"的假失败。
2. **ReplacingMergeTree 不能用裸 `count()` 比对**：
   `address_activity` 生产 `count()`=74,448,957 而 `count(FINAL)`=62,774,084；
   RESTORE 会按 part 重建并可能触发合并，行数会落在两者之间。
   一致性判定请用 `count(FINAL)` 或按分区键抽样。
3. CH 归档本身就是 gzip 的 tar，提取时用 `cat x.tar.gz | docker exec -i … tar xzf -`，
   **不要再 `gunzip` 一次**（双重解压会静默产出空目录，看起来像"备份丢了"）。

### 13.3 Kill -9 演练与容器看门狗

```bash
ssh euripides@192.168.31.152 'cd ~/projects/blockchain-platform && bash scripts/backup/kill9-drill.sh'
```

| 目标 | 恢复耗时 | 说明 |
|---|---:|---|
| `blockchain-worker` | 5.09 s | systemd 自动拉起；checkpoints 不回退、无 IN_FLIGHT 幽灵页 |
| `blockchain-api` | 19.2 s | 到 `/health/ready`=200；旧会话仍可用；审计链继续单调推进 |
| `deploy-nats-1` | 首次**未恢复** → 修复后 **33.37 s** | `restart=unless-stopped` 对 SIGKILL 未生效，已加看门狗 |

**容器看门狗**（新增，已 enable）：

```bash
systemctl --user status blockchain-containers-watchdog.timer   # 每 30s 巡检
bash scripts/backup/containers-watchdog.sh                     # 手动跑一次（幂等）
```

看门狗只对白名单容器（postgres/clickhouse/minio/nats）执行 `docker start`，
全部 running 时不做任何写操作。

### 13.4 RPO/RTO 现状（实测）

| 指标 | 计划目标 | 实测 | 判定 |
|---|---|---|---|
| RPO | ≤ 15 分钟 | 无调度备份、无 WAL 归档；RPO = 距上次手工备份的时间 | **未达标（需调度 + PITR）** |
| RTO（PG / CH / MinIO） | ≤ 4 小时 | 5.81 s / 561 s / 0.63 s | 达标 |
| Worker / API / NATS 恢复 | — | 5.09 s / 19.2 s / 33.37 s（看门狗） | 达标 |

### 13.5 应用数据库账号（S7-SEC-01 R-07 变更后）

| 变量 | 账号 | 用途 |
|---|---|---|
| `DATABASE_URL` | `blockchain_api`（LOGIN / NOSUPERUSER / **NOINHERIT**） | api、worker 运行期连接 |
| `MIGRATION_DATABASE_URL` | `blockchain`（owner） | `cmd/migrate`、`admin-init`、`admin-fixture`，以及 API 启动期迁移 |
| `DB_RUNTIME_ROLE` | `blockchain_app` | 连接后 `SET ROLE` 到受限角色 |

一次性创建（owner 执行）：

```sql
CREATE ROLE blockchain_api LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS NOREPLICATION;
GRANT blockchain_app TO blockchain_api;
ALTER ROLE blockchain_api PASSWORD '<强随机>';
```

核对（应全部为 `f`）：

```bash
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U blockchain -d blockchain_platform -c \
 "select rolname, rolsuper, rolbypassrls, rolinherit from pg_roles where rolname in ('blockchain','blockchain_app','blockchain_api')"
```

`RESET ROLE` 之后 `blockchain_api` 对业务表没有任何权限（实测 `has_table_privilege('audit_events_v2','SELECT')` = `f`），
因此"一条 `RESET ROLE` 提权回超级用户"的路径已被关闭。

### 13.6 会话 Cookie（S7-SEC-01 R-05 变更后）

| 入口 | 判定依据 | Cookie 名 | 属性 |
|---|---|---|---|
| 公网 HTTPS | `X-Forwarded-Proto: https` | `__Host-cs_admin_session` | `HttpOnly; Secure; SameSite=Lax; Path=/`，无 Domain |
| 内网 HTTP | 无 XFP | `cs_admin_session` | `HttpOnly; SameSite=Lax` |

`AUTH_COOKIE_SECURE=auto`、`AUTH_COOKIE_PREFIX=__Host-`。
**注意**：curl 在 `http://` 上不会回送 Secure Cookie，因此内网 HTTP 经 Nginx 的带鉴权测试不可得；
验收统一走公网 HTTPS 或直连 8080。

### 13.7 §12 待确认项的更新

| 原条目 | 现状 |
|---|---|
| 1 备份未纳入调度 | **仍缺**：脚本已可执行并通过演练，但**没有 systemd timer/cron 定时备份** → RPO 未达标 |
| 3 ClickHouse backup disk 未配置 | 已绕过（改用 `TO File`），无需改 ClickHouse 配置 |
| 5 CH/MinIO 备份与恢复步骤空白 | **已补齐**（§13.1/§13.2，含实测 RTO） |
| 6 dr/ 演练是否通过 | **已产出并通过**（§13.2/§13.3） |
| — | 新增：容器看门狗（§13.3）、应用连接账号分离（§13.5）、Cookie 安全属性（§13.6） |
