# 交接：`GET /api/v1/download-jobs` 500 —— 后端缺陷，已确证，**本轮未修**

> R-3 代理仅做**只读取证**；未修改任何后端文件、未重新部署、未重启服务。
> 本目录与 `../r3-jobs500-20260912T231500Z/`（首次发现包）互为补充，均为独立新目录，未覆盖任何历史证据。

## 1. 结论

`GET /api/v1/download-jobs` 对**任意已认证主体**（含平台管理员）返回
**500 `ERROR: operator does not exist: text = uuid (SQLSTATE 42883)`**。
属 **P0 作用域过滤修复引入的回归**，与 R-3 的客户端受众改动**无关**（见 §4 判据）。

## 2. 真实证据（本目录内文件）

| 文件 | 内容 |
| --- | --- |
| `nginx-access.log.txt` | nginx 访问日志原始行：`GET /jobs` → 200（460 B SPA 外壳）；`GET /api/v1/download-jobs` → **500**（73 B，`rt=0.002`），HeadlessChrome 与 curl 两个来源都有 |
| `api-log.txt` | **API 结构化日志真实路径是 `/tmp/api.log`（unit `StandardOutput=append:/tmp/api.log`），不在 journald** —— 我先前"journalctl 无匹配行"的结论由此更正：不是"后端没记录"，是我查错了日志源。共解析出 **16** 条 `/api/v1/download-jobs` 500 行 |
| `backend-repro.txt` | 直接在 SQL 层复现（`docker exec deploy-postgres-1 psql -U blockchain`，只读），含列类型、错误原文、正确写法对照 |
| `data-counts.txt` | `download_jobs` 共 2419 行；investigator 的 org/ws 命中 **72** 行 ⇒ 页面本应有数据 |
| `trace-contract.txt` | trace 契约 gate 项：匿名请求**响应头**有真实 `X-Request-Id`/`X-Trace-Id`，而**响应体 `meta.request_id`/`meta.trace_id` 为空字符串** |

### 2.1 nginx（节选）

```
"GET /jobs HTTP/1.1" 200 460 rt=0.004 ... HeadlessChrome/152
"GET /api/v1/download-jobs HTTP/1.1" 500 73 rt=0.002 ... HeadlessChrome/152
"GET /api/v1/download-jobs HTTP/1.1" 500 73 rt=0.005 ... curl/8.5.0     <- 直接后端调用，不经前端
"GET /api/v1/download-jobs HTTP/1.1" 401 225 rt=0.000 ... curl/8.5.0    <- 匿名，正常
```

### 2.2 API 日志（`/tmp/api.log`，节选，id 已截断）

```json
{"level":"INFO","msg":"http","method":"GET","path":"/api/v1/download-jobs","route_template":"/api/v1/download-jobs",
 "status":500,"duration_ms":2,"request_id":"89c73119-…","trace_id":"72d5d68a-…",
 "actor_id":"6e192e52-…","organization_id":"d3b152f2-…","workspace_id":"000e31bb-…",
 "error_code":"INTERNAL_ERROR","build_sha":""}
{"level":"INFO","msg":"http",...,"status":401,"request_id":"17588c7f-…","actor_id":"","error_code":"UNAUTHENTICATED"}
```

关键：**admin@local（actor 6e192e52，admin 受众）同样 500**，匿名请求则是正常的 401。

### 2.3 SQL 层直接复现（不经 HTTP / 不经前端）

```sql
-- 列类型实测
--  id | uuid ; organization_id | uuid ; workspace_id | uuid

-- PR1：uuid 列与 text 参数比较（= handler 传 Go string 的效果）
PREPARE t1(text,text,int,int) AS
  SELECT j.id FROM download_jobs j
   WHERE j.organization_id=$1 AND ($2='' OR j.workspace_id::text=$2) LIMIT 1;
--> ERROR:  operator does not exist: uuid = text
--> HINT:  No operator matches the given name and argument types. You might need to add explicit type casts.

-- PR2：加 ::uuid 转换后同一查询正常
PREPARE t2(uuid,text,int,int) AS
  SELECT count(*) FROM download_jobs j
   WHERE j.organization_id=$1 AND ($2='' OR j.workspace_id::text=$2);

-- PR3：参数序号错位（把 orgID 当 wsID 用）
PREPARE t3(uuid,text,int,int) AS
  SELECT count(*) FROM download_jobs j
   WHERE j.organization_id=$1 AND ($2='' OR j.workspace_id::text=$1);
--> ERROR:  operator does not exist: text = uuid
```

## 3. 代码位置（只读，来自工作树，**未改动**）

`backend/internal/api/handlers.go:100-111`：

```go
// P0 修复：任务列表必须按主体作用域过滤。
scope, ok := s.tenantScopeOr401(w, r)
if !ok { return }
jobs, err := s.deps.Store.ListJobsScoped(r.Context(), scope.OrganizationID, scope.WorkspaceID, status, limit, offset)
if err != nil {
    writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
    return
}
```

`backend/internal/store/jobs.go:205-210`：

```go
if orgID != "" {
    conds = append(conds, "j.organization_id=$"+fmt.Sprint(len(args)+1))            // (a) 缺 ::uuid
    args = append(args, orgID)
    conds = append(conds, "($"+fmt.Sprint(len(args)+1)+"='' OR j.workspace_id::text=$"+fmt.Sprint(len(args))+")")  // (b) $1 应为 $2
    args = append(args, wsID)
}
```

展开后是 `($2='' OR j.workspace_id::text=$1)`：
- **(a)** `j.organization_id=$1`：列是 `uuid`，参数是 Go `string`（pgx 按 text 发送）→ 本 500 的直接成因；
- **(b)** 第二个条件里 `$1` 是 **orgID**，却用来和 `workspace_id` 比较 —— 应为 `$2`（wsID）。
  这是**独立的第二处缺陷**：只修 (a) 之后，(b) 会让工作区过滤静默用错值（不报错但语义错），必须一并修。

## 4. 判据（为什么判定为后端缺陷，而非客户端受众选择问题）

| 直接 curl（**不经前端**） | 受众头 | 结果 |
| --- | --- | --- |
| fixture-investigator（user 受众） | 无 | **500** |
| fixture-investigator | `user` | **500** |
| **admin@local（owner，177 权限，admin 受众）** | `admin` | **500** |
| fixture-investigator，`limit=5` | 无 | **500** |
| admin@local | 无 | 401（`/api/v1` 按 user 受众解析且无 user cookie，预期） |

管理员也 500 ⇒ 不是权限、不是受众选择、与 `X-Auth-Audience` 存在与否无关 ⇒
**不由 R-3 的 `frontend/src/api/client.ts` 改动引入**（该改动只新增受众头与 CSRF 头）。

## 5. 附带发现（一并交接，未修）

1. **trace 契约**：401/403 响应**头** `X-Request-Id`/`X-Trace-Id` 有真实值，响应**体**的
   `meta.request_id`/`meta.trace_id` 却是空字符串（`trace-contract.txt`）。
   而 500 的响应体**根本没有 `meta` 信封**（见下条）。
2. **错误信息外泄**：`handlers.go:109` 直接把 `err.Error()` 回给客户端，
   响应体里出现原始 SQL 报错文本 `ERROR: operator does not exist: text = uuid (SQLSTATE 42883)`。
   建议改为稳定错误码 + 服务端日志留详情。
3. **API 日志源易误判**：unit 用 `StandardOutput=append:/tmp/api.log`，
   `journalctl --user -u blockchain-api.service` 里**看不到** HTTP 日志。排查时请直接看 `/tmp/api.log`。

## 6. 边界与分工

- 本项**未修**：超出 R-3 代理授权（仅前端文件），且不得在途并发改后端。
- R-3 的 **30 绿不含**该失败；两者**分别封存**，不得表述为"完整前端通过"或"系统可验收"。
- 该 500 已作为完整验收**阻断项**，后端修复 + 回归由主代理承担。
