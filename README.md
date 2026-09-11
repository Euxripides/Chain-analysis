# 链上分析平台 V1 —— Ankr 数据下载器

> Coverage-aware Blockchain Dataset Platform（具备本地覆盖感知能力的链上数据平台）
> 文档：见 docx/ 目录《链上分析平台_V1_Ankr下载器_总体架构与Ubuntu环境部署.md》

V1 范围：平台总体框架 + 数据下载（地址下载、任务调度、本地数据复用、暂停/恢复/取消、SSE 实时进度、CSV/XLSX 导出）。

## 架构

React SPA → Go API → (PostgreSQL 控制面 / NATS 事件 / ClickHouse 数据面 / MinIO Raw) ← Go Worker → Ankr Provider

Investigation Platform Phase 0 已部署：稳定 Contract、Capability Registry、
Durable Investigation State、统一 Job Contract、Transactional Outbox、Schema /
Projection Registry、Finding / Evidence / Graph Patch 持久化。实施与扩展边界见
[docs/phase0-architecture-baseline.md](docs/phase0-architecture-baseline.md)。

- 所有业务通过 Dataset Service 取数（Coverage 检查 → 只下载缺口）
- 原始 JSON（gzip）永久保留在 MinIO，标准化数据进 ClickHouse（ReplacingMergeTree 幂等）
- 下载任务四级模型：DownloadJob → SubjectJob → DatasetJob → Shard
- 任务状态、Checkpoint、Coverage、审计全部持久化在 PostgreSQL

## 快速启动

```bash
cp deploy/.env.example deploy/.env   # 修改密码与 ANKR_API_KEY（没有 key 时 ANKR_MODE=mock 可先跑通全链路）
make infra-up
make migrate
make backend-dev    # 终端 1: API (8080)
make worker-dev     # 终端 2: Worker
make frontend-dev   # 终端 3: Frontend (3000)
```

访问：Frontend http://SERVER:3000 · API http://SERVER:8080 · Health http://SERVER:8080/health/ready
MinIO 控制台 http://SERVER:9002（仅本机/SSH 隧道，勿暴露公网）· NATS 监控 http://SERVER:8222

> 当前开发机（192.168.31.152）：前端 http://192.168.31.152:3000 · API http://192.168.31.152:8080
> Prometheus：API /metrics（8080）与 Worker /metrics（8081）；数据浏览页（Transactions/Transfers/NFT/Logs + 按地址 CSV 导出）已上线
> 没有 ANKR_API_KEY 时默认 `ANKR_MODE=mock`，可跑通全部链路；接入真实数据改 `ANKR_MODE=live`。

## 验收（V1）

- 端到端：36M 行（transactions/token_transfers/logs/balances × 2 地址）经 mock Ankr → Raw(gzip+SHA256 manifest) → ClickHouse → Coverage 全链路入库，14/14 Shards COMPLETED。
- 复用：同请求二次创建任务 0 次 Ankr 调用、~2s COMPLETED（Coverage HIT 100%）。
- 暂停/恢复：暂停后行数冻结（4.1M 行停住），恢复重算 Coverage 后继续到 18/18 Shards、16.2M 行。
- 取消：CANCELLED，无幽灵任务。
- 导出：CSV/XLSX/NDJSON 流式写 MinIO 并经 API 下载。
- 测试：`go test ./...` 全绿；Benchmark 见 docs/implementation-notes.md。

## 目录

- backend/  Go API + Worker（modular monolith，双进程）
- frontend/ React 19 + Vite + TypeScript
- deploy/   docker compose（PostgreSQL 18 / ClickHouse / MinIO / NATS）
- docs/     实现说明与 API 文档
- scripts/  部署与同步脚本

## ChatGPT Actions 链上查询 API

执行迁移并设置 `GPT_ACTIONS_API_KEY` 后，可将 `https://你的域名/openapi.json` 导入 ChatGPT Actions。所有 Actions 数据接口均位于 `/api/v1`，要求 `Authorization: Bearer <API_KEY>`，支持 gzip，并统一返回 `success/data/meta/error` 包装。人工文档入口为 `/docs` 和 `/redoc`。

第一阶段地址概览、资产、普通交易、Token Transfer、Internal Transaction、交易对手、Flow Summary、Top Flows、首笔资金和交易详情均已提供；批量地址、路径/网络、Trace/Logs/Decode 与异步 Parquet/CSV 导出也通过同一 schema 暴露。金额保持字符串，地址与交易哈希完整返回。

API Key 管理接口位于 `/api/v1/admin/action-api-keys`，单独使用 `GPT_ACTIONS_ADMIN_KEY` 鉴权。创建接口只返回一次明文，数据库仅保存摘要，并支持 scope、每分钟配额、过期时间、IP/CIDR 白名单、禁用、删除和请求日志。公网部署仍必须由反向代理提供 HTTPS，并只把 `/openapi.json` 与明确允许的 Actions 路由暴露到公网。

## MCP / ChatGPT App

`mcp/` 是与 REST API 解耦的远程 MCP Server。它不访问链节点，只通过 `REST_API_BASE_URL` 调用现有 `/api/v1`。P0 提供 10 个只读工具，使用官方 TypeScript MCP SDK和 Streamable HTTP，公网地址建议为 `https://你的域名/mcp`。

```bash
cd mcp
npm ci
npm run check
npm run build
MCP_API_KEY=... REST_API_KEY=... npm start
```

生产环境使用 `deploy/blockchain-mcp.service`。MCP客户端请求必须携带 `Authorization: Bearer <MCP_API_KEY>`；服务端使用独立的 `REST_API_KEY` 调用 REST API。列表工具单次最多返回500条，昂贵查询受60秒超时、并发上限和每分钟配额约束。`get_address_assets` 返回的 `balance_source` 与 `is_current_balance` 必须用于区分余额快照和历史净流量。

P0工具：`get_address_overview`、`get_address_assets`、`get_token_transfers`、`get_transactions`、`get_internal_transactions`、`get_counterparties`、`get_flow_summary`、`get_top_flows`、`get_first_funding`、`get_transaction_detail`。

## 开发规范

前后端代码规范见 [docs/code-standards.md](docs/code-standards.md)。其中前端提示信息必须使用弹窗呈现，禁止把警告、说明、成功或错误提示直接嵌入页面布局。
