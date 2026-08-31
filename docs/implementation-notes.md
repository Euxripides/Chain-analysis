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

## Ankr Live 实测行为（2026-08-31，Advanced API 真机验证）

| 数据集 | 区块边界行为 | 平台策略 |
|---|---|---|
| transactions | 带 `toBlock` 返回空；`fromBlock` 被忽略；从链头往前翻页；索引为稀疏近期集合（实测热钱包仅 174 条） | 不传边界 → 客户端按 [from,to] 过滤 → 降序越过下边界即停止 |
| token_transfers | 同上（带 `toBlock` 返回空；fromBlock 忽略；实测全量 12,237 行可完整翻页） | 同上 |
| nft_transfers | 同上（最新在前） | 同上 |
| logs | **尊重 from/toBlock**（实测 1,722 行全部落在请求区间内） | 直接传边界 |
| balances | 快照（无分页），返回 `assets`（74 资产/热钱包） | 直接请求 |

字段形态：transactions 为 RPC 风格（`blockNumber`/十六进制、`hash`、`from`/`to`、`timestamp` 为十六进制秒）；token/nft transfers 为 camelCase（`transfers` 键、`blockHeight` 十进制、`timestamp` 秒）；normalizer 双形态兼容并统一小写地址。0 行结果不提交 Coverage（防止误标）。

## 文档剩余项补齐（§74/§33/§89/§88/§92/§51/§87）

1. **Prometheus 指标**：`GET /metrics`（API:8080 / Worker:8081，独立指标端口），含 ankr_requests_total/429/5xx/延迟直方图/rows/bytes、download_active_jobs/shards、rows-per-sec(EWMA)、clickhouse_insert_*、raw_write_latency_ms、coverage_hit_ratio/saved_requests、export_jobs_total。
2. **数据浏览页**：主导航「数据浏览」→ Transactions / Token Transfers / NFT Transfers / Logs（地址过滤 + 分页 + 按地址 CSV 导出）。
3. **Benchmark 矩阵**（见下）。
4. **企业化预留**：workspaces / projects / quotas 表，download_jobs 增加 organization_id/workspace_id/project_id；auth 中间件（X-User-* 头注入上下文，`GET /api/v1/me` 返回用户与角色列表，V1 不强制鉴权）。
5. **resolved_at**：创建任务时持久化“全量”解析时刻（§92 可审计/可恢复）。
6. **MinIO buckets**：blockchain-raw / blockchain-exports / **blockchain-backups** 启动时自动确保。
7. **address_activity**：token_transfers/transactions 入库时同步写活动表（direction in/out/self、counterparty、amount），`GET /api/v1/addresses/{address}/activity` 为资金分析统一查询入口（§65/§87）。
8. **job_no 并发冲突**：Ensure 对唯一约束冲突自动递增重试；controlLoop 新增自愈收敛（数据集全完成但 subject 卡 RUNNING / 任务卡 RUNNING 时自动翻 COMPLETED）。

## Benchmark（VM 16 核，2026-08-31 实测）

### Provider 分页吞吐（§89，本地 httptest 合成 512B/行）

| pageSize | 延迟/页 | 吞吐 |
|---|---|---|
| 1000 | 15.8 ms | 32.3 MB/s |
| 5000 | 69.7 ms | 36.7 MB/s |
| 10000 | 112.8 ms | 45.4 MB/s |

### 并发扩展性（§89，1/2/4/8/16/32，合成负载）

并发 1→32 单请求延迟稳定在 3.8~4.7 ms（本地服务端饱和，说明客户端无锁竞争瓶颈）。

### ClickHouse 批次写入（§89，ReplacingMergeTree 实际写入）

| 批次行数 | 延迟 | 折合吞吐 |
|---|---|---|
| 10,000 | 68 ms | ~147k rows/s |
| 50,000 | 230 ms | ~217k rows/s |
| 100,000 | 485 ms | ~206k rows/s |
| 500,000 | 1,585 ms | ~315k rows/s |

### 端到端（§89，mock Provider，块区间 0-20000，transactions+token_transfers）

| 地址数 | 行数 | 耗时 | 吞吐 |
|---|---|---|---|
| 1 | 6,031 | 4 s | 1,507 rows/s |
| 10 | 54,024 | 7 s | 7,717 rows/s |
| 100 | 539,931 | 29 s | 18,618 rows/s |
| 1000 | 5,397,229 | 下载主体 ~225 s（1800 shards） | ~24k rows/s |

### 单元基准

| Benchmark | 结果 |
|---|---|
| Coverage 区间合并（1000 区间） | 24 µs/op · 50 KB/op · 15 allocs |
| NormalizeTransaction（Ankr JSON → CH 行） | 9.4 µs/op · 3 KB/op |
| TokenBucket Acquire（软件令牌桶） | 纳秒级/op |

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

## V1.1 稳定性与性能调整（2026-09-01）

依据《Ankr下载器_V1.1_稳定性性能调整_超时响应体与自适应PageSize.md》完成：

- **响应体保护**：maxBytes+1 检测 + Content-Length 快速拒绝；软限制 32MiB / 硬限制 64MiB；截断响应返回 `ErrResponseTooLarge`（不再误报 bad json）。
- **错误模型重构**：`provider.Error{Class,RPCCode,TraceID,Retryable,ReduceRate/Concurrency/PageSize,SplitRange,RetryAfter...}`；context 优先分类（Canceled→不重试；DeadlineExceeded→request_timeout）；官方错误码映射：-32090/-32005(legacy)→rate_limit、-32067/-32068/-32069/-32077/-32070→connection_limit、-32062→request_too_large、-32071→request_timeout、-32055/-32056/-32057/-32059/-32061/-32063/-32064/-32076→upstream；HTTP 429 先解析 JSON-RPC 错误码再兜底；Retry-After 优先。
- **PagePolicy 状态机**：阶梯 1000/2000/5000/10000；Timeout/TooLarge→降一级+短抖动重试同一逻辑页；连续 3 次快成功(<30s,<16MiB)+降档后 5 页 cooldown→升一级（防振荡）；策略按 chain|dataset|subject 隔离；logs 初始 5000。
- **Checkpoint 持久化**：shards 新增 page_size/page_state，每页 Checkpoint 保存策略状态，Resume 继承（不重置回 10000）。
- **Range 拆分**：最小页连续失败 3 次→自动拆半区间，原 Shard 不标 COMPLETE、不提交 Coverage。
- **四控制器解耦**：Rate（令牌桶+Retry-After）、Concurrency（仅连接数限制/全局健康触发，不因单地址超时降全局）、Page（策略状态机）、Range（拆分）。
- **可观测**：provider_usage 新增 error_class/rpc_code/trace_id/page_size/returned_rows/response_bytes/duration_ms/retry_attempt 等；Prometheus 新增 ankr_inflight_requests、ankr_request_duration_seconds、ankr_response_bytes/rows、ankr_page_size_current/降升档、ankr_response_too_large/request_timeout/invalid_json/rate_limit/connection_limit。
- **测试**：errors_test（64MiB±1、坏 JSON、超时、取消、官方错误码矩阵、HTTP429 兜底与 -32068 区分、Retry-After）、page_policy_test（降档阶梯/最小页拆分/cooldown 升档/Resume 继承）全部通过。
- **Canary**：真实 bsc 地址小范围下载验证——usage.page_size=10000、response_bytes≈5.5MB/页、checkpoint 页状态落库、新指标上线（Worker /metrics 可见真实延迟直方图）。

## V1.2 统一数据浏览 / 案件标签 / 混合增量更新 / 公网导出（2026-09-01）

依据《链上分析平台_V1.2_…设计》完成：

- **元数据模型**：cases / tags / workspace_addresses / address_case_links / address_tag_links / token_metadata / update_previews / export_tickets（迁移 0004）；公共链数据与用户元数据分离（§2/§77.1）。
- **案件/标签 API**：CRUD（workspace 隔离）；地址目录 keyset cursor + 案件/标签/状态筛选 + 搜索（服务端）；地址元数据 PATCH。
- **下载页案件/标签**：Ensure 扩展 case_name/tags/subject_meta/create_missing_meta；CSV/XLSX/TXT 导入支持 address,chain,case,tags 列（Per-Address Override）；Metadata 与下载解耦（Coverage 100% 也保存）。
- **统一数据浏览**：POST /data-explorer/query（多地址 QUALIFY 窗口去重、cursor 分页禁止深 OFFSET、方向/Token/金额/时间/区块/Method 筛选、matched_addresses/case_names/tag_names/token_symbol 富化）；地址详情页 Tabs。
- **OKLink 外链**：utils/explorer-url.ts 集中配置 6 链 slug，target=_blank + noopener noreferrer，未知链不生成链接。
- **混合增量更新**：SafeHead（本地最大块-15 reorg buffer）；Preview：无缺口→精确 0 不调 Ankr；小增量→精确；大增量→估算（min/est/low/high/bytes/confidence）；15min 过期 + 60s 缓存；Confirm：冻结 target_block、复检 Coverage、复用 EnsureDataset 只下缺口；Balance 作为 Snapshot 刷新。
- **公网导出**：异步 ExportJob；CSV/XLSX/JSONL/Parquet；**>3 地址强制 ZIP**（addresses/{addr}/{dataset}.{ext} + manifest.json）；下载 Ticket（15min）→ 短时签名 URL；HTTP Range 断点续传（OpenRange 206）；MinIO Private Bucket；对象 24h 过期。
- **worker 同步**：shard 完成后刷新 workspace_addresses 统计/sync_status（§46）。
- **修复**：job_no 跨时区重复（统一 UTC 计数+冲突重试）；display_name NULL 扫描；ListCoverage 空 dataset 全量。
- **验证**：真实地址冒烟——案件/标签创建、目录展示（盘古/入金地址/current/242 行）、Preview up_to_date=1（0 次 Ankr）、Confirm 建任务、4 地址 ZIP 导出（521,113 行/zip=True）、Ticket 下发；go vet 干净、go test 全绿、前端 tsc 通过。

## V1 验收对照（文档 §101）

- 数据正确性：重复下载不翻倍（ReplacingMergeTree + Coverage 区间合并）；Raw 可回溯（manifest → part → SHA256）；Time/Block Range 不越界（shard 区间持久化）。
- 稳定性：429 自动降速（AIMD+断路器）；5xx 指数退避重试；Worker 崩溃可恢复（Checkpoint 每页落库 + Reaper 收回租约）；暂停/恢复/取消全落库；NATS 不可用时事件总线自动降级。
- 性能：单 Shard 最大 pageSize；多地址并行；并发受 Provider 健康状态约束。
- 复用：Coverage HIT 不调 Ankr；PARTIAL 只下缺口（见端到端验收）。
- UX：SSE 实时进度（rows/sec、bytes/sec、req/sec、成本、命中率、ETA）、暂停/继续/取消、错误详情。
