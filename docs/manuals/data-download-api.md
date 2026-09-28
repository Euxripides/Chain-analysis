# 数据下载接口调用方式（本地脚本 / 离线下载）

> 适用对象：需要**在自己机器上用脚本调用接口，把平台数据导出并拉到本地**的人。
> 实测状态：2026-09-22 在部署机 `euripides@dataserver` 上按本文档逐条执行并核对回显，全部命令与回显见[附录 B](#附录-b实测记录2026-09-22)。
> 配套脚本：[`scripts/ops/download-data.sh`](../../scripts/ops/download-data.sh)（见 §3.6）。

## 0. 先选接口（30 秒决策）

平台有三套并存的接口面，**别混用**：

| 你的目标 | 用哪一面 | 鉴权 | 章节 |
|---|---|---|---|
| 脚本：把平台已下载的数据导出成 CSV / Parquet 拉回本地 | Actions 面 `/api/v1/export/*` | Bearer API Key | §3.3–3.5 |
| 脚本：先把某地址的数据下载进平台（**可能产生真实 Provider 费用**） | Actions 面 `/api/v1/data/ensure` | Bearer API Key | §3.1–3.2 |
| 脚本：只取少量记录做核对，不落文件 | Actions 面 `/api/v1/address/*`、`/transaction/*` | Bearer API Key | §4 |
| 需要 Coverage/成本预览、多地址打包导出、暂停/恢复任务 | 控制台会话面 `/api/v2/*` | 浏览器 Cookie + CSRF | §5 |

**只在本地做数据导出（不触发取数）时，最小路径是：** `POST /api/v1/export/create` → 轮询 `GET /api/v1/export/status` → `GET` 返回的 `download_url`。全程只要一个 Bearer Key，不碰 Cookie、不碰 CSRF。

## 1. 入口地址

| 场景 | Base URL | 说明 |
|---|---|---|
| 部署机自己（loopback） | `http://127.0.0.1:8080` | 直连 Go API，不经过 nginx |
| 局域网 / 公网（推荐） | `https://aurore.online` | Cloudflare Tunnel → nginx `127.0.0.1:8088` → API |
| 大文件直连下载通道 | `https://download.aurore.online:8443` | 只暴露导出下载端点，绕过隧道；把 `download_url` 的路径拼到该域名即可 |
| 交互式接口文档（Swagger UI） | `https://aurore.online/docs`（本机 `http://127.0.0.1:8080/docs`） | 21 个 Actions 端点，可点 `Authorize` 填 Bearer Key 后直接试；描述文件 `/openapi.json` |

⚠️ **API 的 8080 端口只对本机开放**：iptables 里有 `-A INPUT ! -s 127.0.0.0/8 -p tcp --dport 8080 -j DROP`。在别的机器上直连 `http://<服务器IP>:8080` 会超时，必须走 `https://aurore.online`。

## 2. 鉴权：Bearer API Key

所有 Actions 面请求带同一个头：

```http
Authorization: Bearer <GPT_ACTIONS_API_KEY>
```

### 2.1 Key 在哪里

- 部署机上：`deploy/.env`（权限 `600`；已被 `.gitignore` 的 `.env` 规则忽略，但仍**不要粘贴到聊天/工单/脚本仓库**）：
  - `GPT_ACTIONS_API_KEY` —— 数据面用（本文档所有示例用这个）
  - `GPT_ACTIONS_ADMIN_KEY` —— 只管 API Key 的签发/吊销，**不能**用来读数据
- 在部署机上安全读取（不回显明文）：

  ```bash
  KEY=$(grep -m1 '^GPT_ACTIONS_API_KEY=' deploy/.env | cut -d= -f2- | tr -d '"' | tr -d "'")
  echo "${#KEY}"   # 只打印长度，例如 64
  ```

- 在其他机器上：向管理员申请一把 Key（下述），用环境变量注入：`export GPT_ACTIONS_KEY=...`

### 2.2 自建受限 Key（推荐给个人脚本）

用管理员 Key 调 Key 管理端点，可以限制 scope、速率、来源 IP、有效期；返回的 `api_key` **只显示一次**：

```bash
curl -sS -X POST https://aurore.online/api/v1/admin/action-api-keys \
  -H "Authorization: Bearer $GPT_ACTIONS_ADMIN_KEY" \
  -H 'Content-Type: application/json' \
  -d '{
        "name": "alice-laptop-download",
        "scopes": ["read", "export"],
        "rate_limit_per_minute": 120,
        "allowed_ips": ["203.0.113.7"],
        "expires_at": "2026-12-31T00:00:00Z"
      }'
# => {"success":true,"data":{"id":"...","api_key":"bca_...","key_prefix":"bca_abcd1234", ...},
#     "meta":{"api_key_shown_once":true}}
```

| 字段 | 取值 | 说明 |
|---|---|---|
| `scopes` | `read` / `analysis` / `export` / `*` | 路径决定所需 scope：`/data/*`、`/address/*`、`/transaction/*` → `read`；`/analysis/*` → `analysis`；`/export/*` → `export`。省略时默认全部三个 |
| `rate_limit_per_minute` | 默认 `300` | 每把 Key 的固定窗口计数（进程内），超限返回 `429 RATE_LIMITED` |
| `allowed_ips` | IP 或 CIDR 数组，省略/空 = 不限制 | 命中 `X-Forwarded-For` 第一段或直连来源 IP |
| `expires_at` | RFC3339 或省略 | 过期后立即失效 |

管理端点（均需 `GPT_ACTIONS_ADMIN_KEY`）：`GET /api/v1/admin/action-api-keys`、`POST /`、`POST /{id}/disable`、`DELETE /{id}`、`GET /requests`（请求日志）。

> 注意：写在进程环境变量里的 `GPT_ACTIONS_API_KEY` 是**不受 scope 限制**的总钥；scope/IP 限制只对数据库里签发的 `bca_*` Key 生效。

### 2.3 失败外观

```bash
# 不带 Key
curl -s -o - -w '\n%{http_code}\n' "https://aurore.online/api/v1/data/download-status?download_job_id=00000000-0000-0000-0000-000000000000"
# {"success":false,"data":null,"meta":{},"error":{"code":"UNAUTHORIZED","message":"Bearer API key required"}}
# 401
```

## 3. 标准下载流程（Actions 面）

响应统一是信封：`{"success":bool,"data":{...},"meta":{...},"error":null|{"code","message"}}`。

### 3.1 第 1 步：确保数据已下载到平台 —— `POST /api/v1/data/ensure`

> ⚠️ **这一步会调用外部 Provider、产生真实费用。** 平台按 `prefer_local` 复用本地 Coverage，缺口部分才会去取数。只想拿已有数据请直接跳到 §3.3（只读端点里的余额快照同样可能触发一次链上读取，但不会创建下载任务）。

```bash
curl -sS -X POST https://aurore.online/api/v1/data/ensure \
  -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' \
  -d '{
        "chain": "bsc",
        "addresses": ["0x536494ef8680a0065a28edea17bc502fd1bbd76a"],
        "datasets": ["transactions", "token_transfers"],
        "range": {"type": "full"},
        "performance_mode": "fast"
      }'
```

请求字段：

| 字段 | 必填 | 说明 |
|---|---|---|
| `chain` | 是 | 链标识，见[附录 A](#附录-a链--dataset-矩阵) |
| `addresses` | 是 | 1–200 个地址；超出返回 400 `INVALID_QUERY` |
| `datasets` | 否 | 见附录 A；省略时服务端取默认集合 |
| `range` | 否 | `{"type":"full"}` / `{"type":"block","from_block":N,"to_block":M}` / `{"type":"time","from":"<RFC3339>","to":"<RFC3339>"}`；默认 `full` |
| `performance_mode` | 否 | `safe` / `fast`（默认）/ `turbo` |

服务端固定行为（不可由请求覆盖）：`reuse_policy=prefer_local`、`priority=90`、`source=chatgpt_mcp`，并自动把地址挂到案件 `ChatGPT MCP`、标签 `ai-managed`/`chatgpt-mcp` 下。

响应：

```json
{"success":true,
 "data":{"status":"planned","dataset_ref":"...","download_job_id":"<uuid>",
         "coverage_before":0.42,"source":"chatgpt_mcp","ai_managed":true},
 "meta":{"download_engine":"system_dataset_service","reuse_policy":"prefer_local"},"error":null}
```

- `status` = `planned`：新建/复用了下载任务，记下 `download_job_id`。
- `status` = `ready` 且 `download_job_id` 为 `null`：本地 Coverage 已完整覆盖，**没有创建任务、没有调用 Provider**，可以直接进入 §3.3 导出。
- `400 DOWNLOAD_PLAN_FAILED`：链暂不支持该 Dataset（例如 `tron` + `logs`），服务端不做静默降级。

### 3.2 第 2 步：轮询下载任务 —— `GET /api/v1/data/download-status`

```bash
curl -sS -H "Authorization: Bearer $KEY" \
  "https://aurore.online/api/v1/data/download-status?download_job_id=<uuid>"
```

返回 `data.status`（小写）：`created` / `planning` / `queued` / `running` / `waiting_user_confirmation` / `pause_requested` / `paused` / `paused_fallback_too_expensive` / `resuming` / `cancel_requested` / `cancelled` / `completed` / `partial_failed` / `failed` / `aborted_by_user`，另含 `job_no`、`chain`、`datasets`、`range_type`、`from_block`/`to_block`、`from_time`/`to_time`、`stats`、`error`。

两个限制：
- 该端点**只认 `source=chatgpt_mcp` 的任务**（即本接口自己创建的）；控制台里建的下载任务在这里是 404 `JOB_NOT_FOUND`，要用 §5 的 `/api/v2/jobs/{id}`。
- 没有"暂停/取消"入口，Actions 面只管创建与查看；要控制任务用控制台。

### 3.3 第 3 步：创建导出 —— `POST /api/v1/export/create`

**一次只能导出一个地址**（多地址请在 §5 用控制台面，或在本地循环调用）。

```bash
curl -sS -X POST https://aurore.online/api/v1/export/create \
  -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' \
  -d '{
        "chain": "bsc",
        "address": "0x536494ef8680a0065a28edea17bc502fd1bbd76a",
        "format": "csv",
        "compression": "zip",
        "data_types": ["transactions"],
        "StartTime": "2024-12-09T10:00:00Z",
        "EndTime": "2024-12-09T13:00:00Z"
      }'
# => 202 {"success":true,"data":{"job_id":"2c362d1a-9fd2-44c2-a7b7-c5a72460bfe0","status":"processing"},"meta":{},"error":null}
```

| 字段 | 说明 |
|---|---|
| `chain` + `address` | 必填；地址按链归一化（EVM 折叠小写，TRON 归一到 Base58Check） |
| `format` | `csv` 或 `parquet`（默认 `parquet`） |
| `compression` | 只允许两对：`csv`+`zip`、`parquet`+`zstd`；可省略由服务端按 format 补默认，填错返回 400 |
| `data_types` | 必填，取值为 `transactions` / `token_transfers` / `internal_transactions` / `logs` 的非空子集 |
| `StartTime` / `EndTime` | 可选时间范围，RFC3339。**必须写驼峰**：这两个字段没有 JSON tag，写成 `start_time` 会得到 `400 INVALID_QUERY: json: unknown field "start_time"`（实测） |
| `token` / `token_contract` | 可选，按代币过滤（按 symbol / 合约地址） |

产物形态（实测）：

| format | 产物 | 说明 |
|---|---|---|
| `csv` | `export-<id>.zip` | zip 内是 `manifest.json` + `addresses/<address>/<dataset>.csv`；**单地址也是 zip** |
| `parquet` | `export-<id>.parquet` | 单个 Parquet 文件，不打包 |

**导出不截断**：单地址单数据集的 csv 导出走列式直写 + 键集分页，一直取到取空，没有行数上限（实测一个 2,331,094 个版本键的地址，导出 2,331,094 行，与库侧 `uniqExact` 一致）。

需要指定范围时在请求体里给 `start_time` / `end_time`（RFC3339，闭区间，可只给一端）；都不给就是全量。xlsx / parquet / 多条目 ZIP 这类"必须把全部行放进内存"的写出器仍有 2,000,000 行的内存上限，超过时会返回明确错误并建议改用 csv 或缩小时间范围——不会静默截断。

### 3.4 第 4 步：轮询导出 —— `GET /api/v1/export/status`

需要 `export` scope。

```bash
curl -sS -H "Authorization: Bearer $KEY" \
  "https://aurore.online/api/v1/export/status?job_id=<上一步的 job_id>"
```

完成时（实测回显）：

```json
{"success":true,
 "data":{"job_id":"2c362d1a-...","status":"completed","format":"csv",
         "total_rows":18,"row_count":18,
         "download_url":"https://aurore.online/api/v1/download/90f05d27-61fc-47aa-a059-61ea079ff5a1",
         "expires_at":"2026-09-22T04:59:27Z",
         "files":[],"manifest_url":null,"sha256":null,"compression":null},
 "meta":{...},"error":null}
```

- `status`：`queued` / `running` / `completed` / `failed` / `expired`（失败时另有 `data.error`）。
- `download_url` 只在 `completed` 时出现，是**15 分钟有效**的下载票据 URL。
- `sha256` 在该面为 `null`，需要校验就本地算（脚本会打印）。

**直连 127.0.0.1:8080 时注意**：`download_url` 的 scheme 默认拼 `https`、Host 取自请求头，直连时会得到 `https://127.0.0.1:8080/...`（无法使用）。两种解法：请求时加 `-H 'X-Forwarded-Proto: http'`（实测得到可用的 `http://127.0.0.1:8080/...`），或走 `https://aurore.online` 由 nginx 自动带上正确协议。

### 3.5 第 5 步：下载文件

```bash
curl -fL -o export.zip "https://aurore.online/api/v1/download/<ticket>"
```

- 该 URL **不需要** Cookie / Bearer —— 票据本身就是能力令牌。
- 支持 HTTP Range（实测 `-r 0-99` 返回 `206` + `Content-Range`），可用 `curl -C -` 断点续传。
- **不支持 HEAD**（`curl -I` 会得到 `405 Method Not Allowed`，`Allow: GET`）；探测用 `curl -r 0-0`。
- 有效期 15 分钟，**在 v1 票据路径上可重复 GET**（实测同一票据第二次仍 200）；过期/伪造返回 `403 {"error":"ticket expired or invalid"}`。这是 v1 与 v2 的关键差异，见 §5。
- 大文件建议改用直连通道，把路径整体替换域名：

  ```bash
  curl -fL -o export.zip "https://download.aurore.online:8443/api/v1/download/<ticket>"
  ```

### 3.6 一键脚本

仓库内的 `scripts/ops/download-data.sh` 把上面五步串好（bash + curl + python3）：

```bash
# 只导出已有数据（不触发取数）——最常用
scripts/ops/download-data.sh export \
  --chain bsc --address 0x536494ef8680a0065a28edea17bc502fd1bbd76a \
  --datasets transactions --format csv \
  --from 2024-12-09T10:00:00Z --to 2024-12-09T13:00:00Z \
  --out ./downloads

# 先确保数据在平台，再导出
scripts/ops/download-data.sh all --chain bsc --address 0x... --datasets transactions,token_transfers --range full

# 在部署机上直连本机 API（脚本会自动补 X-Forwarded-Proto: http），并走直连下载通道
BASE=http://127.0.0.1:8080 scripts/ops/download-data.sh export --chain bsc --address 0x... --direct
```

Key 解析顺序：环境变量 `GPT_ACTIONS_KEY` → 仓库 `deploy/.env` 的 `GPT_ACTIONS_API_KEY`。用 `--help` 看全部参数；脚本不打印 Key，导出完成后打印文件大小与本地 `sha256`。

## 4. 只读直查（不落文件）

适合"只想看几条记录/核对金额"的场景，不需要先 ensure，也不产生导出对象。

```bash
# 地址概览
curl -sS -H "Authorization: Bearer $KEY" \
  "https://aurore.online/api/v1/address/overview?chain=bsc&address=0x5364..."

# 交易明细（分页）
curl -sS -H "Authorization: Bearer $KEY" \
  "https://aurore.online/api/v1/address/transactions?chain=bsc&address=0x5364...&page=1&page_size=100&sort=desc"
```

常用端点：

| 端点 | 用途 |
|---|---|
| `GET /api/v1/address/overview` | 地址概览（类型、余额快照、首末活动、交易计数） |
| `GET /api/v1/address/transactions` | 交易明细 |
| `GET /api/v1/address/token-transfers` | 代币转账 |
| `GET /api/v1/address/internal-transactions` | 内部交易 |
| `GET /api/v1/address/assets` | 资产持仓 |
| `GET /api/v1/address/counterparties` | 对手方聚合（`min_tx_count`、`sort_by`） |
| `GET /api/v1/address/flow-summary`、`/address/top-flows` | 资金流入流出汇总 / Top 流向 |
| `GET /api/v1/address/first-funding` | 首次资金来源 |
| `GET /api/v1/transaction/detail`、`/logs`、`/decode`、`/trace` | 交易详情 / 日志 / 方法解码 / 调用树 |
| `POST /api/v1/addresses/batch-overview`、`/batch-assets` | 批量地址（POST JSON） |
| `POST /api/v1/analysis/path`、`/analysis/address-network` | 资金路径 / 地址网络 |

明细类端点查询参数（除 `chain`/`address` 外均可选）：`page`、`page_size`（**上限 500**，超出 400）、`start_time`/`end_time`（RFC3339，**这里是下划线命名**）、`direction=all|in|out`、`status=success|failed`、`sort=asc|desc`、`token`、`token_contract`、`counterparty`、`from_address`、`to_address`、`method_id`、`min_amount`/`max_amount`。分页信息在响应的 `meta.pagination`。

只读端点在 Actions 面支持 `bsc / ethereum(eth) / tron / polygon / arbitrum / optimism / base`（**不含 solana**）。

## 5. 控制台会话面（`/api/v2/*`）—— 需要预览/多地址/任务控制时用

Actions 面刻意砍掉了 Coverage 预览、成本估算、多地址打包和控制命令。这些只在控制台面提供，鉴权是**浏览器会话 Cookie + CSRF**：

```bash
# 1) 登录：拿到 cs_user_session（HttpOnly）与 cs_user_session_csrf 两个 Cookie
curl -sS -c cookies.txt -X POST https://aurore.online/api/auth/v1/login \
  -H 'Content-Type: application/json' \
  -d '{"identifier":"you@example.com","password":"...","audience":"user"}'
# data.csrf_token 与 cs_user_session_csrf 同值

# 2) 写请求必须带 CSRF 头（GET 不需要）
CSRF=$(awk '$6=="cs_user_session_csrf"{print $7}' cookies.txt)
curl -sS -b cookies.txt -H "X-CSRF-Token: $CSRF" -H 'X-Auth-Audience: user' \
  -H 'Content-Type: application/json' \
  -d '{"chain":"bsc","addresses":["0x..."],"datasets":["transactions"],"range":{"type":"full"},
       "reuse_policy":"prefer_local","performance_mode":"fast"}' \
  https://aurore.online/api/v2/downloads/preview
```

| 端点 | 方法 | 用途 |
|---|---|---|
| `/api/v2/downloads/preview` | POST | Coverage 命中率、缺口、预估行数/CU/成本（只读，不建任务、不调 Provider） |
| `/api/v2/downloads` | POST | 创建下载（**必须带 `Idempotency-Key`**，否则 400） |
| `/api/v2/jobs`、`/api/v2/jobs/{id}` | GET | 任务列表 / 详情（含进度） |
| `/api/v2/jobs/{id}/commands/{command}` | POST | 任务控制（`command` = `pause` / `resume` / `cancel` / `retry`） |
| `/api/v2/jobs/{id}/events` | GET | SSE 实时进度 |
| `/api/v2/exports` | POST | 多地址导出（`addresses[]`、`datasets[]`、`format`、`include_meta`、`force_zip`、`include_decoded_input`），必须 `Idempotency-Key` |
| `/api/v2/exports?status=&limit=&cursor=` | GET | 导出列表（cursor 分页，`download_available` 指示能否下载） |
| `/api/v2/exports/{id}` | GET | 导出详情（行数、SHA-256、过期时间） |
| `/api/v2/exports/{id}/download-ticket` | POST | 生成 **60 秒、单次** 票据 |
| `/api/v2/exports/download/{ticket}` | GET | 用票据下载（第二次使用返回 410） |

两面的关键差异：

| | Actions 面（`/api/v1/export/*`） | 控制台面（`/api/v2/exports/*`） |
|---|---|---|
| 鉴权 | Bearer API Key，适合无人值守脚本 | Cookie + CSRF，适合浏览器/已登录脚本 |
| 地址数 | 单地址 | 多地址（>3 个地址或 >1 个 Dataset 时自动打 ZIP） |
| 票据 | 15 分钟，**可重复 GET** | 60 秒，**单次**（重放 410） |
| 成本/覆盖预览 | 无 | `/api/v2/downloads/preview` |
| 任务控制 | 无 | pause / resume / cancel / retry |
| 响应信封 | `{success,data,meta,error}` | `{success,data,meta,error}`（字段名不同） |

## 6. 限制与约束速查

| 项 | 值 |
|---|---|
| `data/ensure` 地址数 | 1–200 / 次 |
| `export/create` 地址数 | 1 / 次 |
| 单次导出行数上限 | csv：无上限（列式 + 键集分页取全量）；xlsx/parquet/多条目 ZIP：2,000,000 行，超过时明确报错而非截断 |
| 时间范围导出 | 可选 `start_time` / `end_time`（RFC3339，闭区间，可只给一端），不传即全量 |
| 明细接口 `page_size` | ≤ 500 |
| Key 速率 | 默认 300 次/分钟（可签发时自定义） |
| 时间格式 | RFC3339 UTC，例如 `2024-12-09T10:00:00Z` |
| 幂等键 | Actions 面 `ensure` 无幂等键；v2 的 `POST /downloads`、`POST /exports` 必带 `Idempotency-Key` |
| 数据集与链 | 见[附录 A](#附录-a链--dataset-矩阵) |

## 7. 常见坑（均为实测）

1. **`start_time` 在导出接口不认** —— `export/create` 的时间字段是 `StartTime`/`EndTime`（无 JSON tag）。写 `start_time` 立刻 400，且错误信息是 `json: unknown field`。只读查询接口反而用下划线 `start_time`。
2. **直连 127.0.0.1:8080 时 `download_url` 变成 `https://127.0.0.1:8080/...`** —— 加 `-H 'X-Forwarded-Proto: http'`，或走 `https://aurore.online`。
3. **`curl -I` 探测下载链接得到 405** —— v1 下载路由只注册了 GET；用 `curl -r 0-0` 探测。
4. **在非部署机上打 8080 不通** —— 该端口被 iptables 限制为 loopback；改用 `https://aurore.online`。
5. **`download-status` 看不到控制台建的任务** —— 该端点只服务 `source=chatgpt_mcp` 的任务，别用它轮询控制台任务。
6. **票据时效记错** —— Actions 面 15 分钟可重复；v2 面 60 秒且单次。过期重取：重新调 status/ticket 端点即可。
7. **重复调用 `ensure` 不等于重复取数** —— `prefer_local` 会复用本地 Coverage 或已有候选任务；但覆盖不全时仍会真的发起 Provider 取数并计费。
8. **导出产物永远是 ZIP（CSV）** —— 单地址 CSV 也是 zip，别把 `.zip` 当异常；Parquet 才是裸文件。
9. **链标识必须是注册表里的 slug** —— 服务端在 `Ensure` 阶段用链注册表校验（`ValidateChain`），写错会得到 `400 DOWNLOAD_PLAN_FAILED`；`ethereum` 是 `eth` 的别名，只在 Actions 只读接口被折叠，下载接口请直接写 `eth`。
10. **Key 会进审计与请求日志** —— Key 管理处可查 `GET /api/v1/admin/action-api-keys/requests`（记录 method/path/status/耗时/来源 IP）。不要把 Key 写进脚本仓库或粘贴到工单。

## 8. 错误码对照（Actions 面）

| HTTP | code | 含义 / 处置 |
|---|---|---|
| 400 | `INVALID_QUERY` | 请求体字段名/取值错误（未知字段、地址数超限、`page_size>500`） |
| 400 | `INVALID_CHAIN` / `INVALID_ADDRESS` | 链标识或地址不合法 |
| 400 | `DOWNLOAD_PLAN_FAILED` | 该链不支持所选 Dataset，或下载计划无法建立 |
| 401 | `UNAUTHORIZED` | 缺 Bearer / Key 无效或已过期或 IP 不在白名单 |
| 503 | `AUTH_NOT_CONFIGURED` | 服务端未配置 Actions 鉴权（Key 管理端点未配好时同码） |
| 404 | `JOB_NOT_FOUND` | 任务/导出不存在，或不属于 Actions 作用域 |
| 429 | `RATE_LIMITED` | 超过 Key 的每分钟限额 |
| 500 | `INTERNAL_ERROR` | 服务端内部错误（带 `X-Request-Id` 找运维） |
| 503 | `SCAN_TOO_LARGE` / `SCAN_TIMEOUT` | 查询超出有界扫描预算：用 `start_time`/`end_time` 缩小范围 |

## 附录 A：链 × Dataset 矩阵

数据来源：`backend/internal/model/model.go` 的 `chainDatasetsByChain`（与 `/api/v2/meta/datasets` 同源）。

| chain | transactions | token_transfers | nft_transfers | logs | balances | internal_transactions |
|---|---|---|---|---|---|---|
| `bsc` | 可下载 | 可下载 | 可下载 | 可下载 | 可下载 | — |
| `eth`（别名 `ethereum`） | 可下载 | 可下载 | 可下载 | 可下载 | 可下载 | — |
| `polygon` | 可下载 | 可下载 | 可下载 | 可下载 | 可下载 | — |
| `arbitrum` | 可下载 | 可下载 | 可下载 | 可下载 | 可下载 | — |
| `base` | 可下载 | 可下载 | 可下载 | 可下载 | 可下载 | — |
| `optimism` | 可下载 | 可下载 | 可下载 | 可下载 | 可下载 | — |
| `avalanche` | 可下载 | 可下载 | 可下载 | 可下载 | 可下载 | — |
| `tron` | 可下载 | 可下载 | — | — | — | 可下载 |
| `solana` | 可下载 | 可下载 | 视通道而定 | 可下载 | 可下载 | 可下载 |

说明：
- `balances` 是"取数时刻的快照"，没有区间语义（传 `range` 会被忽略/规范化）。
- `solana` 的 `nft_transfers` 取决于是否走 dRPC Data API 通道；Actions 面的只读接口不接受 `solana`（§4 末尾）。
- `avalanche` 在下载白名单里，但不在 Actions 只读接口的链表中。

## 附录 B：实测记录（2026-09-22）

在部署机上用一条真实数据（BSC 地址 `0x536494ef8680a0065a28edea17bc502fd1bbd76a`，时间窗 `2024-12-09T10:00Z ~ 13:00Z`）跑通全链路：

| 步骤 | 命令 | 实测结果 |
|---|---|---|
| 只读概览 | `GET /api/v1/address/overview` | 200，`transaction_count=18`，`meta.cached=true` |
| 创建导出（csv） | `POST /api/v1/export/create` | 202，`job_id=2c362d1a-…` |
| 轮询导出 | `GET /api/v1/export/status` | `running`×7（约 21 s）→ `completed`，`row_count=18`，返回 15 分钟票据 URL |
| 下载票据文件 | `GET /api/v1/download/<ticket>` | 200 `application/zip` 3729 B；解包为 `manifest.json` + `addresses/<addr>/transactions.csv`（18 行数据） |
| 重复使用票据 | 同 URL 第二次 GET | 200（v1 票据在有效期内可重复） |
| Range 下载 | `-r 0-99` | 206，`Content-Range: bytes 0-99/3728`，`Accept-Ranges: bytes` |
| HEAD 探测 | `curl -I` | 405，`Allow: GET` |
| 直连通道 | `https://download.aurore.online:8443/api/v1/download/<ticket>` | 200，/download-health 探测 204 |
| 公网入口 | `https://aurore.online/api/v1/data/download-status`（无 Key） | 401 `UNAUTHORIZED` |
| Parquet 导出 | `format=parquet` | 15850 B 裸 `.parquet` 文件（`Content-Type: application/vnd.apache.parquet`） |
| 时间字段大小写 | `"start_time"` | 400 `json: unknown field "start_time"` |
| 一键脚本 | `scripts/ops/download-data.sh export …` | 全流程成功；`--direct` 亦成功 |
