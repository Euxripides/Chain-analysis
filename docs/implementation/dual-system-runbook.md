# 双系统运维手册（灰度阶段）

适用范围：`/app-v2`（用户层系统）、`/admin`（后台管理系统）、`/api/auth/v1`、`/api/v2`、`/api/admin/v1`。

---

## 1. 部署

```bash
# 本机（Windows, Git Bash）
python3 scripts/sync_to_vm.py                       # 只同步文件
python3 scripts/deploy_dual_system.py --yes         # 完整部署（编译+迁移+构建+重启+冒烟）
python3 scripts/deploy_dual_system.py --yes --skip-sync         # 不重复同步
python3 scripts/deploy_dual_system.py --yes --skip-migrate      # 只发代码
```

脚本按固定顺序执行，任一步失败即中止：
备份二进制 → 同步 → 编译后端 → 安装 systemd 单元与 Nginx 站点 → **迁移** → 构建两套前端 →
重启 → 轮询 `/health/ready`（最多 90s）→ 冒烟 → 打印回滚命令。

**注意**：`/etc/nginx/sites-enabled/blockchain-platform.conf` 在本环境是普通文件而非软链接，
只改 `sites-available` 不会生效。脚本已同时写入两处。

## 1.1 Sprint 4 新增路由与验收入口

- 用户层：`/api/v2/cases/*`（案件/成员/对象/动态/评论）、`/api/v2/investigations/*`（预览/命令/runtime/findings/evidence）、
  `/api/v2/reports/*`（审批/签署/下载票据）、`/api/v2/notifications`、`/api/v2/inbox`。
- 验收脚本：`acceptance-evidence/dual-system-v1/tests/run-sprint4-e2e.sh`
  （需要夹具密码 + `admin-init -force` 的 Owner 密码 + `deploy/.env` 中的 ClickHouse 密码）。
- 浏览器验收：`frontend/tests/dual-system-sprint4.spec.ts`，用 `E2E_CHROMIUM_PATH` 指向系统 Chromium。

## 1.15 Sprint 5 新增路由与验收入口

- 用户层：`/api/v2/explorer/*`（overview/assets/activity/counterparties/semantic/flow-profile、
  交易详情/Logs/local Trace）、`/api/v2/meta/chains|datasets`、`/api/v2/downloads/preview|create`、
  `/api/v2/jobs*`（含 SSE Last-Event-ID）、`/api/v2/exports*`（60 秒一次性票据、HEAD/Range）、
  `/api/v2/investigations/{id}/canvases`、`/api/v2/canvases/*`、`/api/v2/search` 六类型扩展。
- 后台：`/api/admin/v1/jobs`、`/api/admin/v1/jobs/{id}/diagnostics`（固定七层诊断链）。
- 迁移：`0049_s5_explorer_download_jobs.sql`（job_event_log/票据一次性/S5 权限）、
  `0050_s5_job_identity_scope.sql`（created_by_identity，修复 legacy users FK 缺陷）。
- 验收脚本：`acceptance-evidence/dual-system-v1/tests/run-sprint5-e2e.sh`（PASS 104 / FAIL 0 /
  NOT_VERIFIED 0）与 `frontend/tests/dual-system-sprint5.spec.ts`（桌面/移动/后台）。
- 证据目录：`acceptance-evidence/dual-system-v1/tests/sprint5/`（result.json、run.log、
  files/、screenshots/、runtime-verification.txt）。

## 1.16 Sprint 6 新增路由与运行期降权

后台入口（`/admin`，权限见 `backend/internal/policy/catalog.go`）：

```text
/observability            可观测性总览 / SLO / 告警 / 指标
/providers                 Provider 账户（测试连接、断开影响、密钥摘要与轮换）
/providers/routing         路由策略（优先级、fallback、限流、熔断阈值、灰度）
/runtime/performance       性能控制（requested/effective/reason + preview/apply/rollback）
/api-keys                  API / MCP Key（创建/轮换/吊销，明文只出现一次）
/requests                  请求审计（cursor 分页 + Trace 关联）
/costs  /budgets           成本与预算（十进制字符串金额、阈值动作、If-Match）
/alerts                    告警中心（P0–P3、关联链、认领/抑制/解决）
/control                   实时控制面（状态轨、队列、熔断、自动保护开关）
/control/kill-switch       Kill Switch（预览 → 提交 → 双人审批 → 执行 → 健康检查 → 恢复）
/data/retention            保留策略与删除请求（实际删除默认关闭）
/system/backups            备份、校验与恢复演练
/system/version            build SHA、schema 迁移与组件矩阵
/settings  /feature-flags  配置版本（diff/回滚）与功能开关（灰度/试算/回滚）
```

对应 API 前缀：`/api/admin/v1/{control,runtime,providers,routing-policies,api-keys,requests,costs,budgets,alerts,settings,feature-flags,retention-policies,deletion-requests,backups,restores,system,observability}`。

### 运行期降权（必须保持开启）

`deploy/.env` 中的 `DB_RUNTIME_ROLE=blockchain_app` 让 API/Worker 的每条数据库连接执行
`SET ROLE` 到受限角色，从数据库层拒绝审计表的 UPDATE/DELETE 与全库 TRUNCATE（0054）。
缺失该变量时启动日志会打印 WARN，且 AUDIT-003 不再成立。

```bash
grep '^DB_RUNTIME_ROLE=' deploy/.env            # 期望 blockchain_app
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 \
  psql -U blockchain -d blockchain_platform -Atc \
  "select has_table_privilege('blockchain_app','audit_events_v2','TRUNCATE')"   # 期望 f
```

### step-up（二次身份确认）

高风险命令（Kill Switch、密钥轮换、Turbo 模式、删除/恢复、审计导出、secret 配置）要求请求体带
`step_up_password`（当前账号密码）。这是**密码重认证**，不是 MFA（MFA 仍为 NOT_IMPLEMENTED）。

### Sprint 6 验收复现

```bash
ssh euripides@192.168.31.152 \
  'cd ~/projects/blockchain-platform && set -a && . deploy/.env && set +a && \
   . /tmp/s6-e2e-env.sh && export S6_BUILD_SHA=<sha> && \
   bash acceptance-evidence/dual-system-v1/tests/run-sprint6-e2e.sh'
```

`/tmp/s6-e2e-env.sh` 需导出 `ADMIN_PASSWORD`（Owner）、`PLATFORM_ADMIN_PASSWORD`、
`AUDITOR_PASSWORD`、`VIEWER_PASSWORD`（后三者为 `cmd/admin-fixture` 生成的一次性密码）。

## 1.2 OIDC 当前状态

未接入真实 IdP 时保持生产禁用：

```bash
curl -s http://127.0.0.1:8080/api/auth/v1/methods        # 期望 "oidc":false
curl -s -o /dev/null -w '%{http_code}
' http://127.0.0.1:8080/api/auth/v1/oidc/start  # 期望 501
```

adapter 已实现 state/nonce/PKCE/RS256/JWKS 校验并有 httptest 隔离测试；没有真实 IdP 时
**不得**把隔离测试当作 AUTH-011 live 验收。配置 `AUTH_OIDC_ENABLED=true` +
`AUTH_OIDC_ISSUER/CLIENT_ID/CLIENT_SECRET/REDIRECT_URL` 后才会启用；生产要求 https issuer。

## 2. 首次初始化管理员密码

```bash
ssh euripides@192.168.31.152
cd /home/euripides/projects/blockchain-platform
set -a; . deploy/.env; set +a
./backend/bin/admin-init -email admin@local        # 交互式，输入不可见
# 或管道方式（CI/自动化）：
echo '强随机密码' | ./backend/bin/admin-init -email admin@local -stdin
```

规则：不写默认密码；设置后 `must_change_password=true`；该身份的全部既有会话被撤销；
事件写入 login 链（`auth.password.reset`，风险等级 critical）。

## 3. 认证模式

| `AUTH_MODE` | 行为 | 允许使用场景 |
|---|---|---|
| `session` | 只接受服务端会话；完全忽略 `X-User-*` | 生产（唯一允许） |
| `hybrid` | 会话优先；无会话时回退到 `X-User-*` | 仅测试环境 |
| `legacy` | 完全依赖 `X-User-*` | 仅迁移前回归对照 |

`APP_ENV=production` 且模式为 `hybrid`/`legacy` 时，进程启动会把模式**强制降级为 `session`** 并打 ERROR 日志。

验证当前行为：
```bash
curl -s -o /dev/null -w '%{http_code}\n' -H 'X-User-Id: 00000000-0000-0000-0000-000000000001' \
     -H 'X-User-Role: owner' http://127.0.0.1:8080/api/admin/v1/overview   # 期望 401
```

## 4. 审计链

```bash
# 链头
curl -s -b "$JAR" http://127.0.0.1:8080/api/admin/v1/audit/chains

# 全链校验（需要 admin:audit:verify 权限与 CSRF 头）
curl -s -b "$JAR" -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json' \
     -X POST -d '{"chain":"audit"}' http://127.0.0.1:8080/api/admin/v1/audit/verify
```

链损坏时返回 `valid=false` 与 `broken_at_seq` / `reason`。**不要**手工改行来"修复"，
应导出证据并按事故流程处理。

受控维护（保留期删除、归档重放）：
```sql
BEGIN;
SET LOCAL app.audit_maintenance = 'on';
-- 仅限已审批的保留期/归档操作
COMMIT;
```
必须经过 `admin:deletion:approve` 审批。

## 5. 常见故障

| 现象 | 排查 |
|---|---|
| `/api/*` 200 但前端白屏 | 检查浏览器控制台；确认 `X-Auth-Audience` 与 basename 前缀（`/admin`、`/app-v2`）未被二次拼接 |
| 登录 500 | `tail -50 /tmp/api.log`，搜索 `login failed`；常见原因是 `proxy_chain` 等 `text[]` 列写入 NULL |
| 后台页面 401 但仍显示 | 前端会在 `audience` 不匹配时清空会话；检查 `/api/auth/v1/session` 返回的 `audience` |
| 写操作 403 `csrf_failed` | CSRF Cookie 与会话受众不匹配，或页面长时间停留导致 Cookie 过期；刷新页面重取 `/session` |
| `/app-v2/` 返回旧 SPA | Nginx `sites-enabled` 未更新（见第 1 节注意事项） |
| 哈希链校验失败 | 确认写入方与校验方版本一致（时间戳精度、载荷字段）；先看 `broken_at_seq` 定位第一条不一致 |

## 6. 回滚

```bash
# 代码
ssh euripides@192.168.31.152 'cd /home/euripides/projects/blockchain-platform/backend && \
  cp bin/api.bak-dualsystem-<stamp> bin/api && systemctl --user restart blockchain-api.service'

# 前端入口（保留后端）
sudo sed -i '/location ^~ \/admin\//,/^    }/d; /location ^~ \/app-v2\//,/^    }/d' \
     /etc/nginx/sites-enabled/blockchain-platform.conf && sudo nginx -t && sudo systemctl reload nginx

# 认证模式（仅非生产）
sed -i 's/^AUTH_MODE=.*/AUTH_MODE=hybrid/' deploy/.env && systemctl --user restart blockchain-api.service
```

## 7. 服务与端口

| 服务 | 端口 | 说明 |
|---|---|---|
| `blockchain-api.service` | 127.0.0.1:8080（实际 `*:8080`） | REST/SSE API |
| `blockchain-worker.service` | 8081 | 下载 Worker |
| `blockchain-mcp.service` | 127.0.0.1:8090 | MCP 服务 |
| `blockchain-vite.service` | 0.0.0.0:3000 | 旧 SPA（灰度期保留） |
| `blockchain-user-console.service` | 127.0.0.1:3001 | 用户层系统静态产物 |
| `blockchain-admin-console.service` | 127.0.0.1:3002 | 后台管理系统静态产物 |
| `nginx` | 127.0.0.1:8088 + :443 | 对外路由 |

日志：`/tmp/api.log`、`/tmp/user-console.log`、`/tmp/admin-console.log`、`/tmp/vite.log`。