# V1 实现说明（Implementation Notes）

> 对应《链上分析平台_V1_Ankr下载器_总体架构与Ubuntu环境部署.md》的开发约束（§103）。

## 架构落位

- **API 进程**（`cmd/api`）：REST / SSE / Export / Query / 健康检查。
- **Worker 进程**（`cmd/worker`）：Scheduler + Downloader + Ingestion（独立进程，可多实例）。
- **控制面**：PostgreSQL 18（任务四级模型、Checkpoint、Coverage、用量、审计、导出）。
- **数据面**：ClickHouse（transactions / token_transfers / nft_transfers / logs / address_balances，均为 ReplacingMergeTree 幂等去重）。
- **Raw 层**：MinIO（`raw/ankr/{chain}/{dataset}/{yyyy}/{mm}/{requestHash}/part-*.json.gz + manifest.json.gz`，含 SHA256/行数/字节/pageToken/RequestHash）。
- **事件**：NATS JetStream（`events.jobs|progress|shards.{job_id}`，只传轻量元数据，大数据不进 NATS）。

## 关键决策（有意为之）

1. **调度 = PostgreSQL SKIP LOCKED + NATS 事件**。Shard 调度采用 PG 行级租约（lease/heartbeat/reaper/retry backoff 全部落库），NATS 负责事件扇出（SSE）。任务状态绝不只存 Worker 内存，NATS 重启不丢任务；比纯 JetStream 队列更易多 Worker 水平扩展且天然持久。
2. **Ankr 分页串行**：pageToken 决定单个 Shard 内只能串行翻页；并行维度为 Address × Chain × Dataset × Shard（§21/§22）。
3. **自适应 Range**：首个 Probe Shard（≤500k 块）完成后按实测 rows/block 反推窗口（50k~2M 块，目标 20 万行/Shard），再拆分剩余缺口（§23）。
4. **自适应并发**：AIMD + Provider 断路器（NORMAL/DEGRADED/OPEN/HALF_OPEN）+ 全局限流令牌桶（RPM），2→4→8→…→32 按 429/5xx/错误率/背压调整（§24/§25）。
5. **Coverage 提交协议**：Raw 落盘 + SHA256 → Normalize → ClickHouse 写入成功 → 才 COMMIT Coverage（§18）。全量(live)/时间范围下载保守提交“观察到的内部块”（min+1 … max-1），边界块允许重复下载，靠 ReplacingMergeTree 幂等去重收敛。
6. **金额**：raw 永远 String；ClickHouse `Decimal256(38)` 通过 shopspring/decimal 写入，禁止 float64（§103.10）。
7. **Mock Provider**：无 ANKR_API_KEY 时 `ANKR_MODE=mock` 提供确定性数据（分页/多页/各 Dataset 字段与 Ankr 官方 camelCase 对齐），可跑通全链路与端到端验收；接入真实 key 只需 `ANKR_MODE=live`。
8. **PostgreSQL 18 镜像**：18+ 官方镜像要求挂载 `/var/lib/postgresql`（非 `.../data`），compose 已按新约定配置。
9. **前端**：Vite 代理 `/api` → API，SSE 同源直连；表格/进度为轻量自绘组件（未引入 AG Grid/ECharts，进入数据浏览与图谱阶段再引入，符合“不过度设计”）。

## 与文档的偏差（已论证）

| 文档 | 实现 | 理由 |
|---|---|---|
| NATS queue 直接派发 Shard | PG SKIP LOCKED 派发 + NATS 事件 | 状态持久化/多 Worker 安全/恢复语义更简单可靠 |
| 时间范围先解析为块 | 直接透传 fromTimestamp/toTimestamp，Coverage 提交内部块 | 无独立 block→time 索引源，保守提交保证不误标 |
| shadcn/ui + AG Grid + ECharts | 轻量 CSS 组件 | V1 聚焦下载闭环，数据浏览阶段再上重型组件 |

## Benchmark（VM 16 核，2026-08-31 实测）

| Benchmark | 结果 |
|---|---|
| Coverage 区间合并（1000 区间） | 24 µs/op · 50 KB/op · 15 allocs |
| NormalizeTransaction（Ankr JSON → CH 行） | 9.4 µs/op · 3 KB/op |
| TokenBucket Acquire（软件令牌桶） | 纳秒级/op |
| 端到端（mock，4 并发起步） | ~22k rows/s（含 ClickHouse 批量写入 + MinIO gzip） |

## 运行

```bash
cp deploy/.env.example deploy/.env   # 修改密码 / ANKR_API_KEY
make infra-up && make migrate
# 终端1：API     make backend-dev
# 终端2：Worker  make worker-dev
# 终端3：前端    make frontend-dev
```

访问：前端 http://SERVER:3000 · API http://SERVER:8080 · Health http://SERVER:8080/health/ready
MinIO 控制台 http://SERVER:9002 · NATS 监控 http://SERVER:8222

## 目录速查

- `internal/dataset` —— Dataset Service（Ensure / Coverage Preview / 缺口计算）
- `internal/download` —— Planner / Scheduler / Runner / Progress / Control
- `internal/provider/ankr` —— Ankr Client（分页/重试/限流/熔断）+ Mock
- `internal/ingest` —— Normalizer / ClickHouse 批量写入
- `internal/rawstore` —— MinIO Raw JSON（gzip + manifest + SHA256）
- `internal/api` —— REST/SSE/Export
- `internal/query` —— 统一数据面查询
- `migrations` —— PostgreSQL / ClickHouse 迁移（幂等，自动执行）

## V1 验收对照（文档 §101）

- 数据正确性：重复下载不翻倍（ReplacingMergeTree + Coverage 区间合并）；Raw 可回溯（manifest → part → SHA256）；Time/Block Range 不越界（shard 区间持久化）。
- 稳定性：429 自动降速（AIMD+断路器）；5xx 指数退避重试；Worker 崩溃可恢复（Checkpoint 每页落库 + Reaper 收回租约）；暂停/恢复/取消全落库；NATS 不可用时事件总线自动降级。
- 性能：单 Shard 最大 pageSize；多地址并行；并发受 Provider 健康状态约束。
- 复用：Coverage HIT 不调 Ankr；PARTIAL 只下缺口（见端到端验收）。
- UX：SSE 实时进度（rows/sec、bytes/sec、req/sec、成本、命中率、ETA）、暂停/继续/取消、错误详情。
