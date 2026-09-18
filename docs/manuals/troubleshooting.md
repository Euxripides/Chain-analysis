# 故障处置手册（症状 → 诊断 → 处置 → 验证）

> 适用环境：`euripides@192.168.31.152`，项目路径 `/home/euripides/projects/blockchain-platform`。
> 命令前缀（除特别说明外每条命令都假设已执行）：
>
> ```bash
> ssh euripides@192.168.31.152
> export XDG_RUNTIME_DIR=/run/user/$(id -u)
> cd /home/euripides/projects/blockchain-platform
> set -a; . deploy/.env; set +a
> ```
>
> ✅ 标记的命令已在 2026-09-12 实机执行验证；⚠️ 标记的步骤来自代码/脚本分析，未实测执行。
> 关联文档：`operations.md`（运行手册）、`rollback.md`（回滚）、`auditor-guide.md`（审计口径）、
> `admin-guide.md`（后台页面）、`docs/adr/ADR-018`（审计完整性边界）。

---

## 0. 通用分诊顺序

1. **看就绪**：`curl -sS -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8080/health/ready`（503 = 有依赖不健康）。
2. **看进程**：`systemctl --user is-active blockchain-api.service blockchain-worker.service`。
3. **看日志**：`tail -n 50 /tmp/api.log`、`tail -n 50 /tmp/worker.log`（结构化 JSON，字段 `request_id`/`trace_id`/`error_code`）。
4. **拿 Trace**：任何界面的错误弹窗都带 Trace ID；用它在 `/admin/requests`、`/admin/audit` 关联同一请求。
5. **区分"未实现"与"故障"**：`501 NOT_IMPLEMENTED`、`OIDC_NOT_CONFIGURED`（MFA 已于 2026-09-16 实现，不再返回 501）
   是设计状态，不要当缺陷处理。

---

## 1. 服务不可用 / 就绪检查失败

**症状**：`/health/ready` 返回 503；或 `curl` 连接被拒绝（`Failed to connect to 127.0.0.1 port 8080`）。

**诊断**

```bash
curl -sS -m 5 http://127.0.0.1:8080/health/ready | python3 -m json.tool   # 看哪个依赖 not ok
systemctl --user is-active blockchain-api.service blockchain-worker.service
journalctl --user -u blockchain-api.service -n 30 --no-pager
tail -n 30 /tmp/api.log
docker ps --format '{{.Names}}|{{.Status}}'
ss -lntp | grep -E ':(8080|8081|8090|5433|8123|4222|9001)'
```

**常见原因与处置**

| 原因 | 判据 | 处置 |
|---|---|---|
| API 进程刚重启，仍在启动 | 进程 `active` 但 ready 非 200 | 等待；✅ 实测重启后首次轮询（约 3s 内）即 200。持续 >90s 视为异常 |
| 容器未起（PG/CH/NATS/MinIO） | `docker ps` 缺容器或 unhealthy | `cd deploy && docker compose --env-file .env up -d` ⚠️ |
| PG 连接失败 | ready 显示 postgres not ok；日志有 pgx 错误 | 检查 `/data` 空间（`df -h /data` ✅）、容器日志 `docker logs deploy-postgres-1 --tail 50` ⚠️ |
| 运行角色问题 | 日志出现 `postgres ready without runtime role separation` | 确认 `grep '^DB_RUNTIME_ROLE=' deploy/.env` = `blockchain_app`（✅ 实测值） |

**验证**：`curl` 8080 与 8088 两个入口均 200，且 `./backend/bin/auditverify` 退出码 0。

---

## 2. 前端白屏 / 页面 404 / 显示旧界面

**症状**：`/app-v2/` 或 `/admin/` 返回旧 SPA；或页面空白但 API 200。

**诊断**

```bash
curl -sS -m 5 -o /dev/null -w 'app-v2=%{http_code}\n' http://127.0.0.1:8088/app-v2/
curl -sS -m 5 -o /dev/null -w 'admin=%{http_code}\n'  http://127.0.0.1:8088/admin/
curl -sS -m 5 http://127.0.0.1:3001/app-v2/ | head -c 200      # 期望 title「ChainSight 调查工作台」
curl -sS -m 5 http://127.0.0.1:3002/admin/  | head -c 200      # 期望 title「ChainSight 后台管理」
ls -la /etc/nginx/sites-enabled/blockchain-platform.conf        # 注意：本环境是普通文件，不是软链接
```

**处置**

- ✅ 实测三个入口均 200，控制台标题如上。
- **显示旧界面**：Nginx 只更新了 `sites-available`。必须同时写入 `sites-enabled`（AD-008 记录的灰度期真实缺陷）：
  ```bash
  sudo cp deploy/nginx-blockchain-platform.conf /etc/nginx/sites-available/blockchain-platform.conf
  sudo cp deploy/nginx-blockchain-platform.conf /etc/nginx/sites-enabled/blockchain-platform.conf
  sudo -n nginx -t && sudo -n systemctl reload nginx     # ✅ 实测可用
  ```
- **页面空白但 API 正常**：检查浏览器控制台与 `X-Auth-Audience`；
  确认 basename 前缀（`/admin`、`/app-v2`）没有被二次拼接。
- **502/504**：`/tmp/user-console.log`、`/tmp/admin-console.log`、`/tmp/vite.log` 是否在输出启动错误。

**验证**：三个入口 200 + 两个控制台标题正确 + 浏览器打开目标路径能看到导航。

---

## 3. 登录与 SSO 相关

### 3.1 登录失败

**症状**：弹窗「账号或凭证无效」；HTTP 401 `AUTHN_INVALID_CREDENTIALS`。

**诊断**

```bash
grep -i 'login failed' /tmp/api.log | tail -20
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 \
  psql -U blockchain -d blockchain_platform -Atc \
  "select email, status, must_change_password from identities order by email limit 20"
```

✅ 实测：`admin@local` 与 6 个 fixture 账号为 active，且 `must_change_password=t`。
若账号 `status='disabled'`，需管理员启用（`POST /api/admin/v1/users/{id}/commands/enable`）。

**处置**

- 密码错误：核对；注意 `AUTH_MAX_FAILED_LOGINS`（示例 10）后锁定 `AUTH_LOCK_DURATION_SECONDS`（示例 900 秒）。
- `must_change_password=true`：先按提示修改密码。
- 管理员重置：`POST /api/admin/v1/users/{id}/commands/reset-password`（需 `admin:user:password:reset` + `confirm: true`）。

**验证**：再次登录返回 `{"state":"authenticated",...}`（✅ 实测响应形状）。

### 3.2 OIDC / SSO 不可用（**当前为预期状态**）

**症状**：点「使用组织账号登录」失败；`/api/auth/v1/oidc/start` 返回 501。

**诊断**

```bash
curl -sS http://127.0.0.1:8080/api/auth/v1/methods
# ✅ 实测：{"success":true,"data":{"audiences":["user","admin"],"local":true,"mfa_required":false,"oidc":false},...}
curl -sS -o /dev/null -w 'oidc_start=%{http_code}\n' http://127.0.0.1:8080/api/auth/v1/oidc/start
# ✅ 实测：501（OIDC_NOT_CONFIGURED）
```

**处置**：本环境未接入真实 IdP，`AUTH_OIDC_ENABLED=false` 是**设计状态**（ADR-016 §3）。
接入真实 IdP 需要：`AUTH_OIDC_ENABLED=true` + `AUTH_OIDC_ISSUER/CLIENT_ID/CLIENT_SECRET/REDIRECT_URL`
（生产要求 https issuer）。回调只允许已验证邮箱匹配**既有 active identity**，不做自动开通。
**不得**用隔离测试（httptest 假 IdP）冒充 AUTH-011 live 验收。

**验证**：配置后 `/api/auth/v1/methods` 返回 `"oidc":true`，并完成一次真实回调登录。

### 3.3 登录 500 错误

**诊断**：`tail -50 /tmp/api.log | grep -i 'login failed'`。
**已知成因**（原运行手册记录）：`proxy_chain` 等 `text[]` 列写入 NULL。
**处置**：确认请求中的代理链解析结果非 NULL；必要时用受控 SQL 修正该列，并复核 `login_events` 的哈希链未受影响。

### 3.4 写操作 403 `csrf_failed`

**症状**：后台写操作失败，错误码 `csrf_failed`（中间件）或 `AUTHZ_CSRF_FAILED`。

**原因**：CSRF Cookie 与会话受众不匹配，或页面长时间停留导致 Cookie 过期。

**处置**：刷新页面重新获取 `/api/auth/v1/session`（其中返回 `csrf_token`），再重试。
**验证**：写操作成功并出现在 `/admin/audit`。

---

## 4. 权限与 403 相关

| 症状 | 判据（`error.details.reason`） | 处置 |
|---|---|---|
| 403 `permission_denied` | `missing_permission` | 角色缺权限：`/admin/roles/:id` 补授权；核对 `permissions.md` |
| 403 `permission_denied` | `audience_mismatch` | 用错了入口：后台 API 必须用 `/admin/` 会话（`cs_admin_session`） |
| 403 `permission_denied` | `workspace_scope_mismatch` | 请求头 `X-Workspace-Id` 与主体工作区不一致 |
| 403 `permission_denied` | `policy_engine_unavailable` | 策略引擎未装配（fail closed）：查 API 启动日志 |
| 403 `permission_denied` | `permission_not_declared` | 路由声明的权限不在目录：**代码缺陷**，补 Catalog + SQL |
| 403 `TWO_PERSON_REQUIRED` | 自己审批自己的请求 | 换一名具备 `admin:approval:decide` 的人员审批 |
| 404 而非 403 | 跨租户/跨工作区对象统一按不可见处理 | 不要据 403/404 猜测对象是否存在 |

**权限变更后不生效（RBAC-006 要求 5 秒内）**：

```bash
# 权限缓存键为 identityID|organizationID，TTL 默认 30 秒；变更走 Invalidate/InvalidateAll
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 \
  psql -U blockchain -d blockchain_platform -Atc \
  "select count(*) from member_role_bindings where revoked_at is null"
```

处置：确认变更走的是角色/成员关系接口（会触发缓存失效）；
若仍不生效，重启 API 作为兜底并上报（`Engine.Invalidate` 曾出现只删 `identityID` 键的真实缺陷，已修复）。

**验证**：被撤销权限的账号在 5 秒内重新请求得到 403。

---

## 5. 限流（429）

**症状**：HTTP 429，`retryable=true`（`respondError` 对 429/503 置 true）。

**诊断**

```bash
grep '"status":429' /tmp/api.log | tail -20
# 登录相关：AUTH_LOGIN_RATE_PER_MINUTE（示例 20 / IP / 分钟）
# GPT-Actions：GPT_ACTIONS_RATE_LIMIT_PER_MINUTE（示例 300）
# MCP：MCP_RATE_LIMIT_PER_MINUTE（示例 60）
# 统一 RPC：RPC_REQUESTS_PER_SECOND（示例 25）、ANKR_REQUESTS_PER_MINUTE（示例 900）
```

**处置**

- 正常用户被误伤：检查是否有共享出口 IP 或上游重试风暴；按 `/admin/requests` 找来源。
- 需要临时放宽：改 `.env` 后重启对应服务（**不要**用后台改限流去绕过程序内限流，路由/限流的治理项在
  `/admin/providers/routing`，但那不覆盖登录限流）。
- 429 的 `Retry-After` 语义需尊重，避免客户端立即重试放大。

**验证**：同一主体的请求恢复 200，且日志中不再出现连续 429。

---

## 6. Provider 故障与降级

**症状**：`PROVIDER_UNAVAILABLE`（502）、`DATA_NOT_AVAILABLE_LOCALLY`（404）、`DATA_UNAVAILABLE`（503）、
`DATA_OBJECT_MISSING`（503）、熔断打开、任务大量重试。

**诊断**

```bash
# 后台：/admin/providers（健康、配额、错误率、P95、熔断）
# 受控探针（不消耗大量额度）
curl -sS -b $JAR -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json' \
  -X POST -d '{"reason":"故障排查"}' \
  http://127.0.0.1:8080/api/admin/v1/providers/<provider>/commands/test
grep -iE 'provider|circuit|breaker|ankr' /tmp/api.log | tail -50
```

**处置**

1. 确认是**单个 Provider** 还是**全部 Provider**：单个 → 切路由 fallback（`/admin/providers/routing`，需 `admin:routing:update` + `If-Match`）；
   全部 → 按 P1 处置，考虑 Kill Switch 暂停后台回填，冻结新增 Provider 请求（暂停任务）。
2. 熔断：等待半开；不要手工把熔断阈值调到 0 来"恢复"。
3. 断开 Provider 前先看影响预览（受影响链、Dataset、运行中任务、fallback 能力）；
   有运行中任务时断开需 step-up。
4. 凭证类故障（401/403）：按 `operations.md` §7.1 轮换密钥并做连接测试。

**验证**：`/admin/providers` 健康恢复、错误率回落；新任务能推进；`/admin/requests` 无持续 5xx。

---

## 7. 下载任务：卡住、进度不前进

**症状**：Job 长时间停留在同一阶段；地址级进度不动；`ready queue` 堆积。

**诊断**

```bash
# 后台：/admin/jobs → 诊断页（固定七层链）
#   DownloadJob → SubjectJob → DatasetJob → Shard/Checkpoint → Provider request/usage → Raw object/SHA-256 → ClickHouse rows/Coverage
tail -n 100 /tmp/worker.log | grep -iE 'error|retry|stuck|replay'
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 \
  psql -U blockchain -d blockchain_platform -Atc \
  "select status, count(*) from download_jobs group by 1 order by 2 desc"
```

**处置**

- 单地址卡点：在诊断页定位到具体 Shard/Checkpoint，只重试**可重试的失败单元**
  （`POST /api/admin/v1/jobs/{id}/commands/retry`，需 `admin:job:retry`）。
- 需要完整重下：必须显式 `reuse_policy=refresh` 并说明成本（默认复用本地 Coverage）。
- 暂停/取消：`pause` 冻结新增 Provider 请求但保留已成功页、游标与已提交数据；`cancel` 不删除历史与 Coverage。
- 绝不修改数据库状态字段来"推进"任务。

**验证**：诊断页 `成功游标时间` 前进；Worker 日志不再重复同一错误；Coverage 增长与任务终态一致。

---

## 8. §27 DATA-006：Worker 被 Kill -9 后的恢复

**症状**：Worker 进程被杀（OOM、手动 kill -9、宿主重启）；重启后出现"幽灵任务"风险或进度倒退。

**诊断**

```bash
tail -n 50 /tmp/worker.log | grep -iE 'replay|recover|checkpoint|incomplete'
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 \
  psql -U blockchain -d blockchain_platform -Atc \
  "select id, status, updated_at from download_jobs where status not in ('completed','cancelled','failed') order by updated_at limit 20"
```

**期望行为**：保留游标与已成功页，恢复后不重复成功页，不产生幽灵任务
（Worker 启动会执行恢复流程；✅ 实测日志中可见 `incomplete fetched shard replay scheduled` 一类恢复事件）。

**处置**

1. 重启 Worker：`systemctl --user restart blockchain-worker.service` ✅（单元 `active` 已验证）。
2. 观察恢复日志与地址级进度：应从最后成功游标继续，而不是从 0 重来。
3. 若出现重复成功页或进度倒退：**不要**手工改游标；按事故上报并保留
   `/tmp/worker.log`、`download_jobs`/`download_shards`/`checkpoints` 行作为证据。

**验证**：恢复后 Job 能到达终态；`provider_usage` 未出现同一区间的重复计费；Coverage 未出现空洞。

---

## 9. §27 DATA-007：NATS 重启与 Outbox 补发

**症状**：NATS 容器重启/不可用；事件丢失或重复；前端 SSE 长时间无更新。

**诊断**

```bash
docker ps --format '{{.Names}}|{{.Status}}' | grep nats
curl -sS -m 5 http://127.0.0.1:8222/varz | head -20      # NATS 监控端点
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 \
  psql -U blockchain -d blockchain_platform -Atc \
  "select count(*), min(created_at) from outbox_events where published_at is null"
```

**期望行为**：业务事务与 outbox 同事务写入 → forwarder 用 `FOR UPDATE SKIP LOCKED` 补发 →
消费者以 `event_id` 唯一约束去重（至少一次投递下的幂等）。

**处置**

1. 启动 NATS：`cd deploy && docker compose --env-file .env up -d nats` ⚠️。
2. 观察 outbox 积压（数量与最老年龄）回落；SLO 要求最老未发布 < 30s。
3. 若积压持续增长：检查 forwarder 日志与消费失败重试（有上限，最终进 DLQ）。
4. SSE 客户端会指数重连并携带 `Last-Event-ID`；断开不改变业务状态，前端每 30 秒做有条件查询校正。

**验证**：outbox 未发布计数回到 0 附近；`/admin/observability` 的 Outbox lag 恢复正常；
前端在无需人工刷新的情况下恢复更新。

---

## 10. §27 DATA-008：MinIO 对象缺失

**症状**：导出/报告/原始对象下载失败；`DATA_OBJECT_MISSING`（503）或 `OBJECT_NOT_FOUND`（404）；
Worker 日志出现 spool/对象读取失败。

**真实样例（本环境 Worker 日志，2026-09-12 实测）**

```text
{"level":"ERROR","msg":"page ingest failed","shard_id":"d1433b0d-...","page_no":177,
 "err":"spool load: open /var/lib/chain-platform/spool/jobs/ca8e4250-.../token_transfers/d1433b0d-.../page-000177.raw: no such file or directory"}
{"level":"INFO","msg":"incomplete fetched shard replay scheduled","shard_id":"d1433b0d-...","pages":1}
```

**诊断**

```bash
docker ps --format '{{.Names}}|{{.Status}}' | grep minio
./backend/bin/objectprobe -action stat -bucket blockchain-raw -key <对象键>     # ⚠️ 需真实键
ls -l /var/lib/chain-platform/spool/jobs/<job>/<dataset>/<shard>/ | head
```

**处置原则（关键）**

- **不得宣称完成**：对象缺失时系统**不会**把任务标记为完成，必须产生可操作的告警（DATA-008 的验收要求）。
- spool 文件缺失：说明该页在崩溃/清理后丢失，但 Provider 侧已取到的数据不可用 →
  让 Worker 按重放逻辑重新取该页（不要手工造一个空 spool 文件）。
- 原始对象缺失但已入 ClickHouse：核对 Coverage 是否已 commit；若未 commit，需要重新下载该区间。
- 对象存储故障：先恢复 MinIO（`docker logs deploy-minio-1 --tail 50`），再重试失败单元。

**验证**：相关下载/导出重试成功；`/admin/alerts` 中该告警被处置并关联新事件；
Coverage 与 ClickHouse 行数一致。

---

## 11. 审计链问题

### 11.1 `integrity_status=undeclared_gap`（观测到缺口但未封存）

这是**不合法状态**，必须走封存流程（需要 `admin:audit:seal` + 第二人审批）：

```bash
# 1) 只读检测
curl -sS -b $JAR -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json' \
  -X POST -d '{"chain":"audit"}' http://127.0.0.1:8080/api/admin/v1/audit/integrity/preview
# 2) 发起封存（step-up + 原因）→ 3) 另一人 approve → 4) 发起人 execute（再次 step-up）
# 5) 生成并核对锚点
```

**禁止**：回填或伪造缺口区间内的行；修改/删除锚点；把封存后的状态描述为 `intact`。
完整流程与语义见 `auditor-guide.md` §6。

### 11.2 `boundary_conflict`（疑似回填或删边界）

**含义**：已封存缺口区间内出现行 / 幸存首行 `prev_hash` 与 `sealed_tail_hash` 不一致 / 可信起点行缺失。
**处置**：视为安全事故——保留现场、按 P0 上报，**不要**试图"修好"链。校验器已能双向检测（补行与被删边界都会被识别）。

### 11.3 `valid=false` 且 `reason=event_hash_mismatch`

**含义**：某行的载荷被改写（复算不一致），这是确凿的篡改证据。
**处置**：记录 `broken_at_seq` 与 `reason`，导出该区间证据，走安全事故流程；**不要** UPDATE 该行。

### 11.4 校验失败但怀疑是版本差异

原运行手册记录：确认写入方与校验方版本一致（时间戳精度、载荷字段）。
当前实现要求载荷**可由数据库同一行重新推导**，并按 `occurred_at_unix_micro`（微秒）参与哈希。
若在校验失败后升级过二进制，先确认两侧版本，再判断是否真篡改。

### 11.5 常见误判

- `valid=true` **不代表链完整**；必须同时看 `integrity_status`/`prefix_status`。
- `integrity_status=intact` **也不代表全链完好**：扫描期发现的序号跳跃/哈希不匹配只让 `valid=false`，
  不会把状态改成别的值。真篡改信号是 `internal_gap_count > 0`。

**通用验证**：`./backend/bin/auditverify; echo $?`（✅ 可用；0=通过、1=不通过、2=参数/连接错误）。

---

## 12. 数据库与迁移问题

| 症状 | 诊断 | 处置 |
|---|---|---|
| 迁移中途失败 | `./backend/bin/migrate` 输出与 `schema_migrations` | 单文件单事务，失败整体回滚；修因后**重跑同一命令**（幂等） |
| 迁移重复执行 | — | 安全：工具按文件名顺序且带 advisory lock，已应用文件会跳过（✅ 实测重复执行输出 "migrate done"） |
| 权限目录条数与预期不符 | `select count(*) from permissions`（基线 ✅ 177） | 检查是否漏跑迁移；两侧（catalog.go 与 SQL）必须一致，属代码缺陷则补测试 |
| 审计表被拒写 | 日志出现 `append-only` 错误 | 这是**正确行为**；受控维护必须走 `SET LOCAL app.audit_maintenance='on'` 且经 `admin:deletion:approve` 审批 |
| 磁盘空间 | `df -h /data` ✅ | `/data` 为数据卷；低于 15% 时清理临时文件、归档旧 dump |

---

## 13. 已知缺陷与"看起来像故障"的行为（2026-09-12 记录）

| 项 | 说明 | 影响 |
|---|---|---|
| ~~`POST /api/admin/v1/audit/export` 重复注册~~ | **S7 已修复（2026-09-12）**：`adminv1.go` 中注册 501 占位的语句已删除，现在只有 `sprint6_governance.go:64` 的真实实现 | ✅ 修复后实测 `POST`（带 owner 会话、不带 reason）返回真实业务的 400「必须填写导出原因」；此前 chi 对同 method+pattern 静默覆盖、生效者取决于注册顺序，属顺序地雷 |
| `-target clickhouse` 无效 | `cmd/migrate` 不解析任何参数 | 任何一次迁移都会同时跑 PG 与 CH；CH 无版本记录、靠幂等 |
| ~~`rollback-0057.sql` 函数签名不匹配~~ | **S7 已修复（2026-09-12）**：改为 `DROP FUNCTION IF EXISTS public.s7_ensure_user_activity_partitions(int, date)`，并保留 `(int)` 变体兜底；缺失的文档引用改为指向本文档 | ✅ 签名与 `0057_s7_indexes_partitions.sql` 中的定义一致 |
| ~~Provider 写命令用读权限键~~ | **S7 已修复（2026-09-12）**：`POST /providers/{id}/commands/{command}` 入口改为 `requireAny(provider:read / provider:update / provider:key:rotate)`，并在 handler 内按命令精确鉴权（test→read，connect/disconnect→update，rotate-secret→key:rotate） | ✅ 实测：auditor 调 rotate-secret 与 test 均 403；owner 不带 step-up 调 rotate-secret 403。同时修好了"security_admin 被授予 key:rotate 却因入口只认 read 而永远 403"的不一致 |
| ~~`MFA_NOT_IMPLEMENTED`~~ | **2026-09-16 已实现**：`/mfa/verify` 不再返回 501；无有效挑战时返回 401 `AUTHN_MFA_CHALLENGE_INVALID`，验证码错误 401 `AUTHN_MFA_INVALID` | MFA 是第二因素；step-up 仍是密码重认证，两者不可混称 |
| `OIDC_NOT_CONFIGURED` | 未配置 IdP | 预期 501 |
| 功能开关未接入请求路径 | `s6EvaluateFlag` 仅被管理端 evaluate 调用 | 开关不能作为线上发布闸门 |
| `rollout_percent` 无消费点 | 路由策略字段被 CRUD 读写 | 同上 |
| 管理端无移动端样式 | 只有 Shell 的 900px/1100px 断点 | 复杂管理操作用桌面 |

---

## 14. 上报与证据固定

发生 P0/P1 时，在动手修复前先固定现场：

```bash
STAMP=$(date +%Y%m%d-%H%M%S); OUT=/tmp/incident-$STAMP; mkdir -p $OUT
cp /tmp/api.log /tmp/worker.log $OUT/ 2>/dev/null
systemctl --user status blockchain-api.service blockchain-worker.service --no-pager > $OUT/units.txt
docker ps --format '{{.Names}}|{{.Status}}|{{.Ports}}' > $OUT/containers.txt
curl -sS http://127.0.0.1:8080/health/ready > $OUT/ready.json 2>&1
./backend/bin/auditverify > $OUT/auditverify.json 2>&1; echo "exit=$?" >> $OUT/auditverify.json
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 \
  psql -U blockchain -d blockchain_platform -Atc "select chain, last_seq, last_hash from audit_chain_heads" > $OUT/chain_heads.txt
```

上报内容至少包含：现象、时间线（本地时间 +07:00 与时区说明）、`request_id`/`trace_id`、
影响范围（组织/案件/任务）、已执行的命令、是否触及审计链。

---

## 15. 待确认项

1. ClickHouse 与 MinIO 的备份/恢复**脚本已存在但前置条件未满足**（ClickHouse 缺 backup disk、脚本未同步到主机），
   故障处置时应先按 `operations.md` §6.7 核对前置条件，再执行。
2. NATS 重启后的 outbox 补发与 DLQ 查看入口未在后台页面中逐一确认（`/admin/observability` 有指标，
   但 DLQ 的具体页面/接口未验证）。
3. 本文所有 ✅ 命令均在 2026-09-12 实测；带 ⚠️ 的步骤需在维护窗口验证后升级为已验证。
4. 告警规则的完整清单（`deploy/monitoring/dual-system-alerts.yaml`）未逐条展开到本文，
   处置时应以该文件与 `/admin/alerts` 的实际内容为准。
