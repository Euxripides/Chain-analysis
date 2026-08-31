# 链上分析平台 V1 —— Ankr 数据下载器

> Coverage-aware Blockchain Dataset Platform（具备本地覆盖感知能力的链上数据平台）
> 文档：见 docx/ 目录《链上分析平台_V1_Ankr下载器_总体架构与Ubuntu环境部署.md》

V1 范围：平台总体框架 + 数据下载（地址下载、任务调度、本地数据复用、暂停/恢复/取消、SSE 实时进度、CSV/XLSX 导出）。

## 架构

React SPA → Go API → (PostgreSQL 控制面 / NATS 事件 / ClickHouse 数据面 / MinIO Raw) ← Go Worker → Ankr Provider

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
