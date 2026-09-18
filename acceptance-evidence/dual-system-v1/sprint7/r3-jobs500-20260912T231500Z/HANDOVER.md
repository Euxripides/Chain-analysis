# 交接：/api/v1/download-jobs 500（R-3 附加发现的真实失败）

**判据结论：后端缺陷（BACKEND_DEFECT），不是 R-3 客户端受众选择问题。**

## 1. 判据命令与输出（只读，不经前端）

```bash
. /tmp/s6-e2e-env.sh
B=https://127.0.0.1:8448
JI=$(mktemp); JA=$(mktemp)
curl -sk -c "$JI" -o /dev/null -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -d "{\"identifier\":\"fixture-investigator@fixture.local\",\"password\":\"$INVESTIGATOR_PASSWORD\",\"audience\":\"user\"}" "$B/api/auth/v1/login"
curl -sk -c "$JA" -o /dev/null -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' \
  -d "{\"identifier\":\"admin@local\",\"password\":\"$ADMIN_PASSWORD\",\"audience\":\"admin\"}" "$B/api/auth/v1/login"

curl -sk -b "$JI"                     -w '\n%{http_code}\n' "$B/api/v1/download-jobs?limit=100"
curl -sk -b "$JI" -H 'X-Auth-Audience: user'  -w '\n%{http_code}\n' "$B/api/v1/download-jobs?limit=100"
curl -sk -b "$JA" -H 'X-Auth-Audience: admin' -w '\n%{http_code}\n' "$B/api/v1/download-jobs?limit=100"
```

真实输出：

```
investigator 无受众头            -> 500  {"error":"ERROR: operator does not exist: text = uuid (SQLSTATE 42883)"}
investigator X-Auth-Audience:user -> 500  同上
admin@local  X-Auth-Audience:admin -> 500  同上
investigator limit=5（无受众头）   -> 500  同上
```

**关键点：平台管理员 admin@local（owner 角色、177 条权限）同样 500。**
所以既不是"权限不足"，也不是"受众选错"，也不依赖 `X-Auth-Audience` 是否存在 —— 与 R-3 客户端改动无关。

## 2. 响应证据

```
HTTP/1.1 500 Internal Server Error
X-Request-Id: 12cd8359-4eac-4de7-8d1b-2f2f77339e5f
X-Trace-Id:   2c47b772-a754-4c8c-bd36-5aedaf21c19a

{"error":"ERROR: operator does not exist: text = uuid (SQLSTATE 42883)"}
```

注意：该 500 的响应体**没有 `meta` 信封**（与 401/403 的 `meta.request_id`/`meta.trace_id` 为**空字符串**不同，
这里是字段根本不存在）。请求侧的真实追踪 ID 只在响应头里。

## 3. 发起请求的身份与 scope（已脱敏）

| 主体 | audience | roles | org 前缀 | ws 前缀 | 权限数 | 相关权限 |
| --- | --- | --- | --- | --- | --- | --- |
| fixture-investigator | user | investigator | 627c131a | 14dfbf1b | 85 | `download:read`, `job:control` |
| admin@local | admin | owner | d3b152f2 | 000e31bb | 177 | `admin:job:list`, `admin:job:read` |

（id 只留前 8 位前缀；不含口令 / token / 完整 UUID。完整值见 `session-*.json`，其中 token 从未落盘。）

## 4. 服务端日志

`journalctl --user -u blockchain-api.service` 与 `-u blockchain-worker.service` 在出证时间窗内
**匹配 `42883` / `operator does not exist` / `download-jobs` 的行数为 0**。
原始日志全文见 `server-logs.txt`。
→ 后端**没有**为该 SQL 错误写日志；这是可观测性缺口，一并交接（不是本项要求修的）。

## 5. Playwright trace

**无 trace**。该失败由 R-3 的附加用户层回归脚本（`frontend/r3-regress.mjs`，ad-hoc 浏览器脚本）发现，
不是 Playwright test 用例，因此没有 `trace.zip`。原始捕获：
`acceptance-evidence/dual-system-v1/sprint7/ui-r3-20260912T230122Z/regression-user-layer-v1-pages.json`
与此目录下的 `hdr-*.txt` / `body-*.json` / `status-*.txt`。

## 6. 分工与边界

- 本项**未修**：R-3 在途部署期间不得并发改后端（也超出本代理授权范围：仅前端文件）。
- R-3 的 **30 绿不含**该失败；两者分别封存，**不得**表述为"完整前端通过"或"系统可验收"。
- 该 500 已作为完整验收**阻断项**加入主计划。
