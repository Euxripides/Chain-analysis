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
