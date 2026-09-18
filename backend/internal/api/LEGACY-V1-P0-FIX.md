# Legacy /api/v1 无凭证越权读取 —— P0 修复记录

- 仓库：`blockchain-platform`（Windows 工作树，分支 `feat/dual-system-iam-foundation`，基线 HEAD `51596f7`）
- 缺陷编号：**S7-SEC-LEGACY-V1**（P0）
- 结论：`/api/v1` 兼容面下**全部 127 条 session 路由**都已显式分类：**113 条声明权限**，**14 条进入公开白名单**；另有 **26 条 GPT Actions 路由**走独立 API Key 认证，本修复不动其 session 规则。
- 本次只改 Go 源码与单元测试，未做数据库迁移、未部署、未 commit。

## 1. 缺陷与根因

监控者与执行 Agent 实测确认：**无 Cookie / 无 Authorization / 无 API Key / 无 `X-User-*` 头**直接请求内网 API，以下读接口返回 200 且带真实业务数据：

| 请求 | 修复前 | 体积 |
|------|--------|------|
| `GET /api/v1/cases` | 200 | 9234 B（含 `cases` 数组与 `organization_id` / `workspace_id` / `name`） |
| `GET /api/v1/investigations` | 200 | 28831 B |
| `GET /api/v1/download-jobs?limit=1` | 200 | — |
| `GET /api/v1/api-control/overview` | 200 | — |
| `GET /api/v1/api-control/provider-accounts` | 200 | Provider 账户列表 |
| `GET /api/v1/api-control/requests` | 200 | 25305 B |
| `GET /api/v1/admin/planning-decisions` | 200 | 40620 B |
| `GET /api/v1/admin/ankr/benchmarks` | 200 | 374399 B |
| `GET /api/v1/data-explorer/addresses` | 200 | 57619 B |

正对照：`GET /api/v2/cases` 与 `GET /api/admin/v1/jobs` 均 401（新面正确）。

根因是**两层叠加**，只修一层都不够：

1. **路由层**：`backend/internal/api/server.go` 的 `r.Route("/api/v1", ...)` 中大量读路由直接注册 handler，没有 `s.require(...)`；只有写路由声明了权限。
2. **handler 层**：这些 handler 用 `Store.DefaultOrgWorkspace(ctx)` 取"库里 `slug=default` 的第一个组织/工作区"，**完全忽略已认证主体**。因此即便加上 `require`，任何已登录但属于别的组织/工作区的用户，仍会读到默认组织的数据。

## 2. 修复原则

- **不使用"全局登录"中间件**：每条路由的权限键必须与该域已有写权限相称（从 `internal/policy/catalog.go` 现有目录中选取，**未新造任何权限键**）。
- **公开白名单只保留不含租户业务数据的内容**，逐条在代码注释与本文件中写明理由。
- **未认证 = 未认证**：不明文放行、不保留 200、不因前端依赖而妥协；`X-User-*` 兼容头**始终不是授权来源**（仅 `AuthMode=hybrid/legacy` 的迁移期路径可达，生产强制 session）。
- **越界统一 404**：单对象读取保持 `s.requireObject` / `s.scopedObject` 既有语义（404 + `authorization.object_denied` 审计），未改成 403。
- **列表必须 SQL 层过滤**：不允许"先全表查再内存过滤"。

## 3. 路由表（覆盖 /api/v1 下全部注册路由，一条不漏）

共 **127** 条 session 路由（Actions 面 26 条另见表 4.2）。
统计：**require 113 条 / 公开白名单 14 条**；处理前只有 **48** 条带 require，且全部是非 GET 方法（即**没有任何一条读路由**声明过权限）。

| # | Method | Path | 处理前有 require | 处理后的 require 键 | handler 已改为主体作用域 | 备注 |
|---|--------|------|------------------|---------------------|---------------------------|------|
| 1 | GET | `/api/v1/me` | 否 | **（公开白名单）** | — | 公开：只回显当前主体（未登录为匿名），无租户字段 |
| 2 | GET | `/api/v1/system/architecture` | 否 | **（公开白名单）** | — | 公开：架构基线元数据 |
| 3 | GET | `/api/v1/capabilities` | 否 | **（公开白名单）** | — | 公开：能力注册表描述符 |
| 4 | GET | `/api/v1/system/chains/{chain}/capabilities` | 否 | **（公开白名单）** | — | 公开：RPC 网关链级能力位 |
| 5 | GET | `/api/v1/chains/capabilities` | 否 | **（公开白名单）** | — | 公开：链能力矩阵（链级静态元数据，无租户列） |
| 6 | GET | `/api/v1/chains/{chain}/head` | 否 | **（公开白名单）** | — | 公开：链上公共 head |
| 7 | GET | `/api/v1/transactions/{chain}/{tx_hash}/receipt` | 否 | **（公开白名单）** | — | 公开：链上公共回执 |
| 8 | GET | `/api/v1/transactions/{chain}/{tx_hash}` | 否 | **（公开白名单）** | — | 公开：链上公共交易详情/解码（残余风险：可判断本地是否有该交易） |
| 9 | GET | `/api/v1/tokens/search` | 否 | **（公开白名单）** | — | 公开：链上公共 Token 查询 |
| 10 | GET | `/api/v1/tokens/{chain}/{contract}/logo` | 否 | **（公开白名单）** | — | 公开：链上公共 Token 图标 |
| 11 | GET | `/api/v1/tokens/{chain}/{contract}` | 否 | **（公开白名单）** | — | 公开：链上公共 Token 元数据 |
| 12 | GET | `/api/v1/methods/search` | 否 | **（公开白名单）** | — | 公开：链上公共方法签名搜索 |
| 13 | GET | `/api/v1/methods/{selector}` | 否 | **（公开白名单）** | — | 公开：链上公共 ABI/签名查询 |
| 14 | POST | `/api/v1/investigations` | 是 | `investigation:create` | — | 该 handler 原本就用 `auth.FromContext().OrganizationID`，非默认组织 |
| 15 | GET | `/api/v1/investigations` | 否 | `investigation:list` | 是 |  |
| 16 | GET | `/api/v1/investigations/{id}` | 否 | `investigation:read` | 是 |  |
| 17 | GET | `/api/v1/investigations/{id}/runtime` | 否 | `investigation:read` | 是 |  |
| 18 | GET | `/api/v1/investigations/{id}/artifacts` | 否 | `investigation:read` | 是 |  |
| 19 | POST | `/api/v1/investigations/{id}/{action}` | 是 | `investigation:run` | 是 |  |
| 20 | GET | `/api/v1/investigations/{id}/reports` | 否 | `report:list` | 是 |  |
| 21 | POST | `/api/v1/investigations/{id}/reports` | 是 | `report:create` | 是 |  |
| 22 | GET | `/api/v1/reports/{id}/download` | 否 | `report:download` | 是 |  |
| 23 | GET | `/api/v1/investigations/{id}/subjects` | 否 | `subject:list` | 是 |  |
| 24 | POST | `/api/v1/investigations/{id}/subjects` | 是 | `subject:create` | 是 |  |
| 25 | POST | `/api/v1/investigations/{id}/relationships` | 是 | `investigation:update` | 是 |  |
| 26 | GET | `/api/v1/analysis/addresses/{chain}/{address}/flow-profile` | 否 | `explorer:read` | — |  |
| 27 | GET | `/api/v1/analysis/addresses/{chain}/{address}/semantic-events` | 否 | `explorer:read` | — |  |
| 28 | POST | `/api/v1/analysis/addresses/{chain}/{address}/semantic-events/rebuild` | 是 | `admin:dataset:manage` | — |  |
| 29 | GET | `/api/v1/analysis/addresses/{chain}/{address}/advanced` | 否 | `explorer:read` | — |  |
| 30 | POST | `/api/v1/analysis/addresses/{chain}/{address}/advanced/rebuild` | 是 | `admin:dataset:manage` | — |  |
| 31 | POST | `/api/v1/datasets/ensure` | 是 | `download:create` | 是 |  |
| 32 | POST | `/api/v1/datasets/coverage/preview` | 是 | `explorer:read` | 是 |  |
| 33 | GET | `/api/v1/download-jobs` | 否 | `job:list:workspace` | 是 |  |
| 34 | GET | `/api/v1/download-jobs/{id}` | 否 | `job:read` | 是 |  |
| 35 | POST | `/api/v1/download-jobs/{id}/pause` | 是 | `job:control` | — |  |
| 36 | POST | `/api/v1/download-jobs/{id}/resume` | 是 | `job:control` | — |  |
| 37 | POST | `/api/v1/download-jobs/{id}/cancel` | 是 | `job:control` | — |  |
| 38 | GET | `/api/v1/download-jobs/{id}/guard` | 否 | `job:read` | 是 |  |
| 39 | POST | `/api/v1/download-jobs/{id}/guard/continue` | 是 | `job:control` | — |  |
| 40 | POST | `/api/v1/download-jobs/{id}/guard/abort` | 是 | `job:control` | — |  |
| 41 | POST | `/api/v1/download-jobs/{id}/subjects/{subjectID}/{action}` | 是 | `job:control` | — |  |
| 42 | GET | `/api/v1/download-jobs/{id}/events` | 否 | `job:read` | 是 |  |
| 43 | GET | `/api/v1/providers/ankr/status` | 否 | `admin:provider:read` | — | Provider 端点/健康属运维面 |
| 44 | GET | `/api/v1/providers/ankr/usage` | 否 | `admin:cost:read` | — | 成本/用量属 admin.ops 域（写入侧是 admin:performance:update） |
| 45 | GET | `/api/v1/providers/ankr/global-concurrency` | 否 | `admin:performance:read` | — |  |
| 46 | GET | `/api/v1/providers/ankr/fetch-runtime` | 否 | `admin:performance:read` | — |  |
| 47 | GET | `/api/v1/providers/ankr/production-profile` | 否 | `admin:performance:read` | — |  |
| 48 | POST | `/api/v1/providers/ankr/performance-mode` | 是 | `admin:performance:update` | — |  |
| 49 | POST | `/api/v1/download-jobs/{id}/performance-mode` | 是 | `admin:performance:update` | — |  |
| 50 | GET | `/api/v1/providers/ankr/fetch-profile` | 否 | `admin:performance:read` | — |  |
| 51 | GET | `/api/v1/providers/ankr/ready-queue` | 否 | `admin:performance:read` | — |  |
| 52 | GET | `/api/v1/count-oracle/status` | 否 | `admin:provider:read` | — |  |
| 53 | GET | `/api/v1/count-oracle/providers` | 否 | `admin:provider:read` | — |  |
| 54 | GET | `/api/v1/count-oracle/resolutions` | 否 | `admin:provider:read` | — |  |
| 55 | GET | `/api/v1/count-oracle/observations` | 否 | `admin:provider:read` | — |  |
| 56 | GET | `/api/v1/count-oracle/calibration` | 否 | `admin:provider:read` | — |  |
| 57 | GET | `/api/v1/admin/planning-decisions` | 否 | `admin:job:read` | — | 计划决策按 job_id 检索，属全局任务数据面 |
| 58 | GET | `/api/v1/addresses/{chain}/{address}/runtime` | 否 | `address:read` | — | 会消耗付费 Provider 配额（地址类型/原生余额） |
| 59 | GET | `/api/v1/transactions/{chain}/{tx_hash}/trace` | 否 | `transaction:read` | — | 路由层收敛为 transaction:read；handler 内仍保留 canForceDecode 角色校验 |
| 60 | GET | `/api/v1/transactions/{chain}/{tx_hash}/internal-calls` | 否 | `transaction:read` | — |  |
| 61 | GET | `/api/v1/api-control/overview` | 否 | `admin:dashboard:read` | — |  |
| 62 | GET | `/api/v1/api-control/provider-accounts` | 否 | `admin:provider:read` | — |  |
| 63 | POST | `/api/v1/api-control/provider-accounts/ankr/connect` | 是 | `admin:provider:update` | — |  |
| 64 | GET | `/api/v1/api-control/provider-accounts/ankr/oauth/callback` | 否 | **（公开白名单）** | — | 公开：授权证明是服务端签发的一次性 PKCE state（10min、单次、绑定 verifier），非会话；加 require 会因 /api/v1 受众解析为 user 而打断合法回调 |
| 65 | POST | `/api/v1/api-control/provider-accounts/ankr/sync` | 是 | `admin:provider:update` | — |  |
| 66 | DELETE | `/api/v1/api-control/provider-accounts/ankr` | 是 | `admin:provider:update` | — |  |
| 67 | GET | `/api/v1/api-control/control/states` | 否 | `admin:routing:read` | — |  |
| 68 | GET | `/api/v1/api-control/control/policies` | 否 | `admin:routing:read` | — |  |
| 69 | PATCH | `/api/v1/api-control/control/policies` | 是 | `admin:routing:update` | — |  |
| 70 | POST | `/api/v1/api-control/control/overrides` | 是 | `admin:performance:update` | — |  |
| 71 | PATCH | `/api/v1/api-control/control/kill-switch` | 是 | `admin:killswitch:engage` | — |  |
| 72 | GET | `/api/v1/api-control/control/actions` | 否 | `admin:routing:read` | — |  |
| 73 | GET | `/api/v1/api-control/requests` | 否 | `admin:request:read` | — |  |
| 74 | GET | `/api/v1/api-control/requests/{id}` | 否 | `admin:request:read` | — |  |
| 75 | GET | `/api/v1/api-control/errors` | 否 | `admin:request:read` | — |  |
| 76 | GET | `/api/v1/api-control/costs` | 否 | `admin:cost:read` | — |  |
| 77 | GET | `/api/v1/api-control/operations` | 否 | `admin:request:read` | — |  |
| 78 | GET | `/api/v1/api-control/providers` | 否 | `admin:provider:read` | — |  |
| 79 | GET | `/api/v1/api-control/usage-types` | 否 | `admin:cost:read` | — |  |
| 80 | GET | `/api/v1/api-control/budgets` | 否 | `admin:budget:read` | — |  |
| 81 | POST | `/api/v1/api-control/budgets` | 是 | `admin:budget:update` | — |  |
| 82 | GET | `/api/v1/api-control/alerts` | 否 | `admin:alert:read` | — |  |
| 83 | POST | `/api/v1/api-control/alerts/{id}/actions` | 是 | `admin:alert:manage` | — |  |
| 84 | POST | `/api/v1/admin/ankr/benchmarks` | 是 | `admin:performance:update` | — |  |
| 85 | GET | `/api/v1/admin/ankr/benchmarks` | 否 | `admin:performance:read` | — |  |
| 86 | GET | `/api/v1/admin/ankr/benchmarks/{id}` | 否 | `admin:performance:read` | — |  |
| 87 | GET | `/api/v1/admin/ankr/range-certifications` | 否 | `admin:performance:read` | — |  |
| 88 | POST | `/api/v1/admin/ankr/range-certifications` | 是 | `admin:performance:update` | — |  |
| 89 | GET | `/api/v1/coverage` | 否 | `explorer:read` | — | 与同域 POST /datasets/coverage/preview 同键（explorer:read）；dataset_coverage 无租户列 |
| 90 | GET | `/api/v1/coverage/stats` | 否 | `explorer:read` | — | 同上；平台级聚合统计 |
| 91 | GET | `/api/v1/datasets/{dataset}` | 否 | `explorer:read` | — |  |
| 92 | GET | `/api/v1/addresses/{address}/activity` | 否 | `explorer:read` | — |  |
| 93 | GET | `/api/v1/data-explorer/addresses` | 否 | `explorer:read` | 是 |  |
| 94 | POST | `/api/v1/data-explorer/query` | 是 | `explorer:read` | 是 |  |
| 95 | POST | `/api/v1/exports` | 是 | `export:create` | — |  |
| 96 | GET | `/api/v1/exports/{id}` | 否 | `export:read` | 是 |  |
| 97 | GET | `/api/v1/exports/{id}/download` | 否 | `export:download` | 是 |  |
| 98 | HEAD | `/api/v1/exports/{id}/download` | 否 | `export:download` | 是 |  |
| 99 | POST | `/api/v1/exports/{id}/download-ticket` | 是 | `export:download` | 是 |  |
| 100 | GET | `/api/v1/cases` | 否 | `case:list` | 是 |  |
| 101 | POST | `/api/v1/cases` | 是 | `case:create` | 是 |  |
| 102 | DELETE | `/api/v1/cases/{id}` | 是 | `case:delete` | 是 |  |
| 103 | GET | `/api/v1/cases/{id}/canvases` | 否 | `canvas:list` | 是 |  |
| 104 | POST | `/api/v1/cases/{id}/canvases` | 是 | `canvas:create` | 是 |  |
| 105 | GET | `/api/v1/canvases/{id}` | 否 | `canvas:read` | 是 |  |
| 106 | PATCH | `/api/v1/canvases/{id}` | 是 | `canvas:update` | 是 |  |
| 107 | POST | `/api/v1/canvases/{id}/branch` | 是 | `canvas:branch` | — |  |
| 108 | GET | `/api/v1/canvases/{id}/snapshots` | 否 | `canvas:snapshot` | 是 |  |
| 109 | POST | `/api/v1/canvases/{id}/snapshots` | 是 | `canvas:snapshot:create` | 是 |  |
| 110 | GET | `/api/v1/tags` | 否 | `case:read` | 是 |  |
| 111 | POST | `/api/v1/tags` | 是 | `case:update` | 是 |  |
| 112 | DELETE | `/api/v1/tags/{id}` | 是 | `case:update` | — |  |
| 113 | PATCH | `/api/v1/addresses/{chain}/{address}/metadata` | 是 | `admin:dataset:manage` | 是 |  |
| 114 | GET | `/api/v1/tokens/enrichment` | 否 | `admin:dataset:read` | — | 平台元数据覆盖率统计（会暴露本地处理规模），非单条 Token 查询 |
| 115 | POST | `/api/v1/admin/tokens/{chain}/{contract}/resolve` | 是 | `admin:dataset:manage` | — |  |
| 116 | POST | `/api/v1/admin/tokens/{chain}/{contract}/override` | 是 | `admin:dataset:manage` | — |  |
| 117 | POST | `/api/v1/admin/token-metadata/backfill` | 是 | `admin:dataset:manage` | — |  |
| 118 | GET | `/api/v1/admin/token-metadata/backfill` | 否 | `admin:dataset:read` | — |  |
| 119 | GET | `/api/v1/methods/enrichment` | 否 | `admin:dataset:read` | — | 同上 |
| 120 | POST | `/api/v1/updates/preview` | 是 | `admin:dataset:manage` | 是 |  |
| 121 | POST | `/api/v1/updates/{id}/confirm` | 是 | `admin:dataset:manage` | — |  |
| 122 | POST | `/api/v1/admin/methods/backfill` | 是 | `admin:dataset:manage` | — |  |
| 123 | GET | `/api/v1/admin/methods/backfill` | 否 | `admin:dataset:read` | — |  |
| 124 | GET | `/api/v1/admin/methods/backfill/{id}` | 否 | `admin:dataset:read` | — |  |
| 125 | POST | `/api/v1/admin/methods/backfill/{id}/{action}` | 是 | `admin:dataset:manage` | — |  |
| 126 | POST | `/api/v1/admin/methods/signatures` | 是 | `admin:dataset:manage` | — |  |
| 127 | POST | `/api/v1/admin/contracts/{chain}/{address}/abi` | 是 | `admin:dataset:manage` | — |  |


### 3.1 公开白名单（14 条）与理由

| Method | Path | 公开理由 |
|--------|------|----------|
| GET | `/api/v1/me` | 前端探测登录态；只回显当前主体（未登录返回匿名用户），不含任何组织/工作区业务数据。要求登录才能探测登录态会让登录页无法工作。 |
| GET | `/api/v1/system/architecture` | 架构基线元数据（组件就绪位、契约版本），无租户字段。 |
| GET | `/api/v1/capabilities` | 能力注册表描述符（名称/版本/契约）。 |
| GET | `/api/v1/system/chains/{chain}/capabilities` | RPC 网关链级能力位（archive/trace/batch 是否可用），静态元数据。 |
| GET | `/api/v1/chains/capabilities` | 链能力矩阵（`chain_capabilities` 表），链级静态元数据，**无租户列**。 |
| GET | `/api/v1/chains/{chain}/head` | 链上公共 head。 |
| GET | `/api/v1/transactions/{chain}/{tx_hash}/receipt` | 链上公共回执，不含平台侧数据。 |
| GET | `/api/v1/transactions/{chain}/{tx_hash}` | 链上公共交易详情与 ABI 解码。**残余风险见 §7**（可判断本地是否已入库该交易）。 |
| GET | `/api/v1/tokens/search` | 链上公共 Token 查询。 |
| GET | `/api/v1/tokens/{chain}/{contract}` | 链上公共 Token 元数据。 |
| GET | `/api/v1/tokens/{chain}/{contract}/logo` | 链上公共 Token 图标。 |
| GET | `/api/v1/methods/search` | 链上公共方法签名搜索。 |
| GET | `/api/v1/methods/{selector}` | 链上公共 ABI / 签名查询（返回全部候选，不含租户数据）。 |
| GET | `/api/v1/api-control/provider-accounts/ankr/oauth/callback` | OAuth 提供方发起的**浏览器顶级跳转**：浏览器不会带自定义头，且 `/api/v1` 路径按受众规则解析为 `user`，后台会话在这里本来就取不到。回调本身不构成授权——`CompleteOAuth` 校验的是服务端签发的、**单次使用、10 分钟过期、绑定 PKCE verifier** 的 `state`（`ankr_management_oauth_state`），而 `state` 只能由已通过 `admin:provider:update` 的 connect 端点取得。缺 `state`/`code` 时只回一个 error 重定向，不改变任何状态。 |

### 3.2 GPT Actions 面（26 条，**未被本次 session 规则改动**）

鉴权模型（`backend/internal/api/actions.go`）：独立的 `Authorization: Bearer <API Key>`，key 摘要存 `gpt_action_api_keys`，并叠加 `scopes`（read/analysis/export）与 `allowed_ips`、每分钟限流；`/admin/action-api-keys/*` 另有 `GPT_ACTIONS_ADMIN_KEY`。这些路由**不得**套用 session require，否则会误伤其 API Key 契约。

| Method | Path | 鉴权 |
|--------|------|------|
| GET | `/api/v1/admin/action-api-keys/` | `actionAdminAuth`（GPT_ACTIONS_ADMIN_KEY） |
| POST | `/api/v1/admin/action-api-keys/` | 同上 |
| POST | `/api/v1/admin/action-api-keys/{id}/disable` | 同上 |
| DELETE | `/api/v1/admin/action-api-keys/{id}` | 同上 |
| GET | `/api/v1/admin/action-api-keys/requests` | 同上 |
| GET | `/api/v1/address/overview` | API Key + scope `read` |
| GET | `/api/v1/address/assets` | 同上 |
| GET | `/api/v1/address/transactions` | 同上 |
| GET | `/api/v1/address/token-transfers` | 同上 |
| GET | `/api/v1/address/internal-transactions` | 同上 |
| GET | `/api/v1/address/counterparties` | 同上 |
| GET | `/api/v1/address/flow-summary` | 同上 |
| GET | `/api/v1/address/top-flows` | 同上 |
| GET | `/api/v1/address/first-funding` | 同上 |
| GET | `/api/v1/transaction/detail` | 同上 |
| GET | `/api/v1/transaction/logs` | 同上 |
| GET | `/api/v1/transaction/decode` | 同上 |
| GET | `/api/v1/transaction/trace` | 同上 |
| POST | `/api/v1/addresses/batch-overview` | 同上 |
| POST | `/api/v1/addresses/batch-assets` | 同上 |
| POST | `/api/v1/analysis/path` | API Key + scope `analysis` |
| POST | `/api/v1/analysis/address-network` | 同上 |
| POST | `/api/v1/export/create` | API Key + scope `export` |
| GET | `/api/v1/export/status` | API Key + scope `export` |
| POST | `/api/v1/data/ensure` | API Key + scope `read` |
| GET | `/api/v1/data/download-status` | 同上 |

**Actions 侧发现并修复的越权读取（单项）**：

- `GET /api/v1/export/status?job_id=<uuid>`：旧实现只要 `job_id` 是合法 UUID 就返回**任意**导出的元数据，并在 `COMPLETED` 时签发下载票据——也就是说一个 `export` scope 的 API Key 能读取任何组织创建的导出，仅靠 UUID 不可枚举性兜底，**不构成授权**。
  修复：先把可读范围收窄到本 Actions 平台作用域内的导出（比对 `exports.organization_id`/`workspace_id`），越界仍返回 404（不泄露存在性），路径/字段/状态码语义**保持不变**，API Key 契约未破坏。
- `POST /api/v1/export/create`、`POST /api/v1/data/ensure`：原先用 `Store.DefaultOrgWorkspace` 隐式落库。已改为经 `s.systemScope(ctx)` 显式解析**平台默认组织**这一 Actions 绑定（`gpt_action_api_keys` 无 org/workspace 列），不再散落隐式默认值。

**未修复、列为残余风险**：由环境变量 `GPT_ACTIONS_API_KEY(S)` 配置的"主密钥"在 `actionAuth.middleware` 中命中即放行，**跳过 `scopes` 校验**。这是既有设计（运维主密钥），本次未改动其契约。



## 4. handler 层：租户/对象作用域改造

### 4.1 新增集中式解析器（`backend/internal/api/objectaccess.go`）

```go
type tenantScope struct {
    OrganizationID string
    WorkspaceID    string // 空 = 该组织下全部工作区；绝不能被解释为"默认工作区"
    System         bool
}

func (s *Server) tenantScope(ctx) (tenantScope, error)   // 只用主体自己的 org/ws
func (s *Server) scopeFromPrincipal(p) (tenantScope, error) // 纯函数内核
func (s *Server) systemTenantScope(p) (tenantScope, error)  // 迁移期系统主体
func (s *Server) tenantScopeOr401(w, r) (tenantScope, bool) // 失败即 401
func (s *Server) writableWorkspace(ctx, scope) (string, error) // 写路径的**具体**工作区
func (s *Server) systemScope(ctx) (tenantScope, error)     // 平台级（Actions）绑定，显式
func (s *Server) exportWorkspaceID(ctx, exportID) string    // 导出对象自己的工作区
```

规则：

- **未认证主体**：直接返回 `errNoTenantScope`，handler 写 401（路由层 `require` 通常已先拦下，这是第二道防线）；
- **已认证主体**：只用 `Principal.OrganizationID` / `WorkspaceID`，**永不回退** `DefaultOrgWorkspace`；
- **主体无组织归属**：拒绝，而不是用默认组织兜底；
- **系统主体（hybrid/legacy 兼容头）**：使用其显式绑定的组织；兼容头未提供组织时收敛到**平台默认组织**（仍然是"一个具体组织"，不是"任意组织"）；
- **API Key（GPT Actions）**：由 `systemScope` 显式解析平台数据面绑定，与 session 路径完全隔离。

### 4.2 替换清单（`DefaultOrgWorkspace` 调用点）

| 文件 | 原用途 | 修复 |
|------|--------|------|
| `api/v12.go` | 8 处（cases×3、tags×3、explorer catalog、export ticket） | 全部改为 `tenantScopeOr401` / `writableWorkspace` / `requireObject("export")` |
| `api/handlers.go` | Ensure / CoveragePreview 留空作用域 | 由 handler 注入 `req.OrganizationID` / `WorkspaceID`（字段为 `json:"-"`，客户端无法提交） |
| `api/export_v2.go` | createExportV2 兜底、annotateRows | 作用域必须由调用方注入（缺失即 500）；注释改用**导出对象自己的工作区** |
| `api/actions.go` | 2 处（export create / export status） | 改为 `systemScope`，并收窄 export status 的可读范围 |
| `addressmeta/service.go` | `Workspace()` 取默认组织（Catalog / Meta / EnsureLinked / SyncFromCoverage） | 删除默认取值；四个方法都要求调用方显式传入 org/ws |
| `explorer/service.go` | `enrich` 用默认工作区附加案件/标签名 | `QueryRequest` 新增 `OrgID`/`WSID`（`json:"-"`，服务端注入）；为空则**跳过富化** |
| `update/service.go` | Preview 取默认组织、Confirm 写回默认工作区 | Preview 要求显式作用域；Confirm 读 `UpdatePreviewScope`（写回**预览自己**的租户） |
| `dataset/service.go` | Ensure / ReuseCandidateLocal 兜底 | HTTP 路径已全部显式注入；仅保留**非 HTTP 内部路径**（调查运行时）的回退，见 §7 残余风险 |

仍然出现 `DefaultOrgWorkspace` 的位置（全部经审计，均非"以默认组织替代主体作用域"）：

| 位置 | 说明 |
|------|------|
| `store/meta.go` | 定义本身 |
| `api/objectaccess.go:systemTenantScope` | 迁移期系统主体的组织收敛（仅 hybrid/legacy 可达） |
| `api/objectaccess.go:systemScope` | GPT Actions 的平台绑定（显式命名，禁止 session handler 调用） |
| `dataset/service.go` ×2 | 非 HTTP 内部调用（调查运行时）的兼容回退 |
| `cmd/admin-fixture/main.go` | 测试夹具 CLI，非 HTTP |

### 4.3 列表查询的 SQL 层作用域

| Store / Service 方法 | 改动 |
|----------------------|------|
| `store.ListCases(org, ws)` | 原 `WHERE workspace_id=$1`（且 handler 传默认 ws）→ `WHERE organization_id=$1 AND ($2='' OR workspace_id::text=$2)` |
| `store.ListTags(org, ws)` | 同上 |
| `store.DeleteCase(org, ws, id)` / `store.DeleteTag(org, ws, id)` | 增补组织条件（第二道防线） |
| `store.ListJobsScoped(org, ws, ...)` | 新增；原 `ListJobs` 无任何租户条件、被 HTTP 直接使用 → 保留给下载引擎内部使用 |
| `store.ListWorkspaceAddressesScoped(org, ws, ...)` | 新增；原方法由 `addressmeta.Catalog` 用默认工作区调用 |
| `investigation.ListScoped(org, ws, limit)` | 新增；原 `List` 无租户条件、被 HTTP 直接使用 |
| `store.UpdatePreviewScope(id)` | 新增，Confirm 据此写回正确租户 |

### 4.4 单对象读取的对象级授权（越界 → 404 + 审计）

以下 handler 现在都在取数据**之前**调用 `s.scopedObject` / `s.requireObject`：

`cases/{id}`（DELETE）、`cases/{id}/canvases`、`canvases/{id}`（GET/PATCH/branch/snapshots 列表与创建）、
`investigations/{id}`（GET/runtime/artifacts/reports/subjects/relationships/{action}）、`reports/{id}/download`、
`download-jobs/{id}`（GET/guard/events）、`exports/{id}`（GET/download）、`exports/{id}/download-ticket`。

`GET|HEAD /exports/{id}/download` 通过新的 `handleExportDownloadScoped` 包装做授权——`handleDownloadExport` 自身仍被 `/api/v1/download/{ticket}` 票据链路复用（票据是短时能力令牌，无 session 主体），在其内部加作用域检查会把票据链路一并打断。

## 5. 测试

新增 `backend/internal/api/legacy_v1_p0_test.go`，全部走**真实路由表**（`s.buildRouter`），不使用真实数据库：

| 测试 | 覆盖 |
|------|------|
| `TestLegacyV1ReadRoutesRejectUnauthenticated` | 63 条租户/敏感读路由（含监控者实测返回 200 的 9 条）无凭证 → **401**，并断言响应体不含 `organization_id`/`workspace_id` |
| `TestLegacyV1AuthenticatedViewerForbidden` | 已认证 viewer（仅 `explorer:read`）调高权限读 → **403**；并验证缺少 `explorer:read` 时 `/coverage` 也 403 |
| `TestLegacyV1CrossTenantObjectReadIs404` | 已认证、属另一组织的用户读默认组织对象 → **404**（不泄露存在性，且不回显被访问的 ID） |
| `TestLegacyV1PublicWhitelistStaysOpen` | 公开路由 `/health/live`、`/api/v1/me`、`/api/v1/capabilities` 仍 **200** |
| `TestTenantScopeNeverFallsBackToDefaultWorkspace` | 未认证/无组织主体必须解析失败；有主体时必须返回主体自己的 org/ws |
| `TestTenantScopeOr401WritesUnauthorized` | handler 第二道防线写 401 |
| `TestWritableWorkspaceFailsClosedWithoutStore` | 写路径缺工作区时 fail closed |
| `TestLegacyV1RouteTableRequiresDeclaredPermission` | **静态回归**：解析 `server.go`，`/api/v1` 下每条路由必须 `s.require(...)` 或在文档化白名单内；白名单条目失效也会失败 |

## 6. 构建与静态检查

```
cd backend
go build ./...     # PASS
go vet ./...       # PASS（exit 0）
go test ./internal/api/... ./internal/store/... ./internal/access/... ./internal/policy/... \
        ./internal/addressmeta/... ./internal/dataset/... ./internal/explorer/... \
        ./internal/update/... ./internal/investigation/...   # 全部 ok
```

工具链：`go1.23.2 windows/386`（`go test` 走 toolchain go1.26.0）。

## 7. 残余风险与未覆盖项

1. **前端/端到端脚本的适配（最高优先级）**：`/api/v1/api-control/*` 与 `/api/v1/admin/*` 现在使用 `admin:*` 读键，而 `/api/v1` 路径按 `authmw.audienceForPath` 解析为 **user 受众**。因此：
   - 后台会话访问这些路径**必须**带 `X-Auth-Audience: admin`（否则后台 Cookie 不会被解析，得到 401）；
   - 用户会话即使拥有同名权限也会因 `audience_mismatch` 得到 403。
   `frontend/src/api/client.ts`（旧 V1 前端）**不发** `X-Auth-Audience`，其 api-control 页面与 `frontend/tests/api-control.spec.ts`、`performance-modes.spec.ts` 等用例预计会变红，需要由执行 Agent 改为带受众头/已登录会话。这不是回归，而是"未认证不再放行"的必然结果。
2. `GET /api/v1/transactions/{chain}/{tx_hash}` 保持公开（链上公共查询白名单），但它查的是**本地数据集**，因此仍可用于判断"本平台是否已入库该交易"（存在性探针）。若认为不可接受，应把该路由移入 `transaction:read`。
3. `GET /api/v1/download/{ticket}`（在 `/api/v1` Route 块之外注册）是**票据能力 URL**：15 分钟、UUID 不可枚举、票据在签发时已绑定导出对象自己的 org/ws，但校验端没有主体可比对，因此不做作用域检查。这是既有设计（§54/§56），本次未改。
4. `dataset.Ensure` / `ReuseCandidateLocal` 仍保留 `DefaultOrgWorkspace` 回退，仅由**非 HTTP** 路径（`investigation.Runtime` 后台执行）触发；HTTP 路径已全部显式注入作用域。建议后续把租户作用域并入调查运行时。
5. `dataset_coverage`、`provider_usage`、`api_control_*`、`ankr_benchmark_runs` 等表**没有租户列**，无法按 org/workspace 过滤，只能靠路由层 `require` 收敛到"已认证且具备相应域权限"的主体。若要真正多租户隔离，需要表结构演进（属于迁移工作，本次未做）。
6. GPT Actions 的环境变量主密钥跳过 `scopes` 校验（§3.2 已列）。
7. 未做：真实数据库/端到端实测（按任务约束，由执行 Agent 负责）；未跑 `go test ./...` 全量（其余包与本次改动无关，已跑受影响包）。
8. 未提交（按要求不 commit、不 reset、不删除文件）；`acceptance-evidence/` 下既有文件只读、未改动。
