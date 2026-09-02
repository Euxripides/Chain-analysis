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
- `internal/method` —— V1.3 Method/ABI 解析（selector 提取、ABI 解析与解码、签名候选、缓存、异步补全、深度解码、Backfill、指标）
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
- **公网导出**：异步 ExportJob；CSV/XLSX/JSONL/真实 Parquet；**>3 地址强制 ZIP**（addresses/{addr}/{dataset}.{ext} + manifest.json）；下载 Ticket（15min）由 API 校验后代理私有 MinIO 对象；HTTP Range 断点续传（OpenRange 206）；对象 SHA-256 与真实行数可审计；MinIO Private Bucket；对象 24h 过期。
- **worker 同步**：shard 完成后刷新 workspace_addresses 统计/sync_status（§46）。
- **修复**：job_no 跨时区重复（统一 UTC 计数+冲突重试）；display_name NULL 扫描；ListCoverage 空 dataset 全量。
- **验证**：真实地址冒烟——案件/标签创建、目录展示（盘古/入金地址/current/242 行）、Preview up_to_date=1（0 次 Ankr）、Confirm 建任务；CSV/Parquet 均为 242 行且 SHA-256 与下载文件一致，Parquet 首尾均为 `PAR1`；4 地址 ZIP 含 manifest + 4 个地址文件；直接下载和 Ticket 下载分别验证 100-byte Range/206；重复 Worker 端口冲突会在参与调度前退出，SIGTERM 可正常收敛；Edge 桌面和 390px 移动端 6 项浏览器测试通过。

## V1.3 Transaction Method / ABI 解析增强（2026-09-01）

依据《链上分析平台_V1.3_Transaction_Method_ABI解析增强方案.md》完成。核心结论：**批量下载不逐笔调用 Ankr 深度接口**，
而是 input → selector → 本地快速解析 → 未知异步补全 → 重点交易按需 Deep Decode。

- **Schema**：ClickHouse 0004 为 transactions 增加 method_name/signature/source/verified/confidence/type/resolved_at 与
  method_id 跳数索引，新增 `transaction_decoded_inputs`（Decoded Input 独立存表，主表只保留轻量 Method 信息），
  logs 增加 event_name/signature/source/verified；PostgreSQL 0005 新增 method_signatures（唯一键 selector+signature，
  允许碰撞候选并存）、contract_abis、event_signatures、method_resolve_queue（唯一键 chain+contract+method_id 天然去重）、
  method_backfill_jobs，并为 provider_usage 增加 usage_type/tx_hash/cache_hit/decode_priority/decode_budget。
- **Selector 提取**：`method.Classify` 本地解析 input，带 Hex 校验；不信任 Provider 的 methodId。
  分类：contract_creation（to=null，部署字节码不按 Selector 解析）、native_transfer（input=0x 且 value>0）、
  empty_call（input=0x 且 value=0）、contract_call。结构化分类的 source 记为 heuristic 且 verified=false，
  不与"已验证 ABI"混淆。**完整 input 永远保留**。
- **解析优先级**：manual_override > contract_abi（含 Proxy→Implementation 回落）> ankr > local_signature >
  public_signature > heuristic > unknown。verified 只可能来自 contract_abi / manual_override / Provider 已验证解码；
  签名库匹配一律 "Signature Match, Not Contract Verified"。
- **碰撞处理**：候选全部保留；同层多候选先按 input 参数长度（含 tuple/动态类型的最小编码长度）收敛，
  仍不能判定则保留 selector、method_name 留空、confidence=low，**绝不随机挑一个并标 Verified**。
- **Fast Resolver**：只读内存索引（Global Selector Cache + Contract ABI Cache + 5 分钟 TTL 结果缓存），
  无外部网络、无阻塞 DB 查询；解析失败写 unknown 并投递 `events.method.resolve.request`（只带元数据，不带完整 input）。
  Method 解析属于 Enrichment，未解析不影响交易入库与 Coverage COMPLETE。
- **异步补全**：Enqueuer 进程内去重 + DB 唯一约束兜底（百万笔同 selector 只有一条 Job）；
  MethodResolverWorker 领取任务，先复查本地索引，再（仅后台、默认关闭）查公共 4-byte 库，结果一律 verified=false。
- **Seed**：启动时写入 ERC20/721/1155、Uniswap V2/V3、PancakeSwap、WETH/WBNB、Proxy、Ownable、AccessControl
  等 100+ 常见签名与 19 个事件签名，selector 由 keccak256 现算（不硬编码）。
- **Deep Decode**：`GET /api/v1/transactions/{chain}/{tx_hash}?decode=local_only|auto|force_refresh`。
  auto = 本地解码缓存 → 本地 ABI 解码（零成本）→ 才请求 Provider 一次并持久化；同一 tx 第二次不再请求；
  原生转账/空调用/合约创建没有参数可解，直接跳过 Provider；每次真实请求写 provider_usage(usage_type=tx_deep_decode)，
  缓存命中也补记一条 cache_hit=true 便于统计命中率与成本。
- **ABI 解码**：自研 ABI 解析/解码（uint/int/address/bool/bytesN/bytes/string/定长与动态数组/tuple 嵌套），
  uint256/int256 一律输出十进制字符串，地址规范化小写，bytes 输出 0x Hex，**绝不经过 float64**。
- **Backfill**：MethodBackfillJob 按主键 (chain, tx_hash) keyset 分批（默认 20K，上限 100K），
  支持 checkpoint/pause/resume/cancel/progress，批次之间让路给实时下载；重写走 ReplacingMergeTree 新版本行，
  原样保留 input 等所有原始字段；**不重新向 Ankr 下载历史交易**。
- **Data Explorer**：Method 列（已解析显示 Transfer/0xa9059cbb；未解析显示 0x12ab34cd/Unknown，永不留空）、
  Hover Tooltip（Method/Selector/Source/Confidence/Verified，签名库来源额外提示 Not Contract Verified）、
  Method 统一筛选（名称/签名/selector/unknown）、交易详情抽屉（Overview / Method Header / Input Data /
  Decoded Input / Event Logs）与「尝试深度解析」按钮。
- **导出**：transactions 导出默认带 method_id/name/signature/source/verified；`include_decoded_input` 默认关闭；
  导出列顺序改为稳定排序（此前依赖 Go map 迭代顺序，列序不可复现）。
- **指标**：method_selector_extracted_total、method_resolved_total、method_unknown_total、
  method_resolve_{contract_abi,local_signature,public_signature,ankr}_total、method_collision_total、
  method_deep_decode_{requests,cache_hit,error}_total、method_resolve_queue_depth、method_registry_entries。
- **性能（§57，`go test -bench . -benchtime 300000x ./internal/method`，Windows/386 开发机，Linux VM 上更快）**：
  Fast Resolver 热缓存 **527 ns/op、1 alloc**（≈190 万行/秒/核）；冷缓存（每笔都是新合约）1.5 µs/op、6 allocs；
  仅结构分类 277 ns/op；单笔 ABI 参数解码 661 ns/op。相较网络受限的下载吞吐（万行/秒量级），
  解析开销在噪声范围内，满足"Fast Resolver 不得明显降低主下载吞吐"。
- **测试**：selector 提取与非法 hex、四类交易分类、种子 selector 自洽（含 keccak 已知常量）、
  唯一候选/碰撞不标 Verified/input 长度收敛/本地优先公共/manual_override 优先、ABI 优先于本地签名、
  ABI 动态类型与事件解码、未知入队与去重、深度解码缓存（第一次 1 次请求，第二次 0 次，force_refresh 才再请求）、
  local_only 不触发 Provider、Provider 数值精度与签名自洽校验、Unknown 不阻塞交易入库。

## V1.5 RPC Infrastructure 与可扩展链访问层（2026-09-02）

- `chain.Registry` 统一维护链 ID、原生币、确认深度、区块标签和 Logs 窗口；Coverage/Update 使用 `HeadService.SafeHead`，不再拿本地已下载最高块冒充实时链头。
- 标准只读 RPC 全部经过 Method Allowlist、Router、Capability、全局 Rate/Concurrency、Retry、Circuit Breaker、Timeout、Cache/Singleflight、Budget、Usage 和脱敏日志；写方法默认在 Provider 之前拒绝。
- Ankr Chain RPC 的 endpoint 模板与密钥分离；业务服务只拿 `ChainRead/Contract/Balance/AddressType/Logs/Trace`，Token Metadata 和 Data Explorer 合约识别均已迁入统一网关。
- Batch 按 method call 计费，按响应 ID 还原顺序并保留部分失败；自动聚合按链、预算键、用途、优先级、一致性和区块引用隔离，关闭过程不会向已关闭 channel 发送；非 Batch Provider 降级不会重复扣预算。
- `provider_pricing` 生效区间是 RPC 计价主来源，环境变量只作无记录时回退；Provider 请求、method call、缓存/Singleflight 命中和用途分别写入 `provider_usage`。
- ChainReader 对交易、回执、日志和区块的必填地址/hash/hex 数值执行严格解析，损坏上游数据返回 `invalid_response`，不再静默写成零值；Trace 同时支持 parity/geth 格式、规范化调用树、派生内部活动、ClickHouse 持久化和 MinIO 原始响应。
- 对外只开放类型化业务 API 与 Capability API，不开放 Raw RPC；Trace 要求 Analyst/Investigator 及以上角色，能力不足时仅降级 Internal Calls。

## V1 验收对照（文档 §101）

- 数据正确性：重复下载不翻倍（ReplacingMergeTree + Coverage 区间合并）；Raw 可回溯（manifest → part → SHA256）；Time/Block Range 不越界（shard 区间持久化）。
- 稳定性：429 自动降速（AIMD+断路器）；5xx 指数退避重试；Worker 崩溃可恢复（Checkpoint 每页落库 + Reaper 收回租约）；暂停/恢复/取消全落库；NATS 不可用时事件总线自动降级。
- 性能：单 Shard 最大 pageSize；多地址并行；并发受 Provider 健康状态约束。
- 复用：Coverage HIT 不调 Ankr；PARTIAL 只下缺口（见端到端验收）。
- UX：SSE 实时进度（rows/sec、bytes/sec、req/sec、成本、命中率、ETA）、暂停/继续/取消、错误详情。
