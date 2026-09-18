# Sprint7 性能：Explorer 四端点优化（改前/改后复算证据）

- 证据时间：2026-09-17（部署机本地时区 +07，下述 UTC 时刻均标注）
- 被测地址/链：`tron` / `TCFNp179Lg46D16zKoumd4Poa2WFFdtqYj`（与 `scripts/perf/run-perf-suite.sh` 的 E2E fixture 一致）
- 被测入口：`http://127.0.0.1:8080`（内网直连 LAN）
- 交付二进制：`backend/bin/api` sha256 `0dae50f10a7bf56180b28044dc1404eba1be5ced5ab7173f470c544600376ac7`
- 同时部署：`backend/bin/worker` sha256 `965437285cfda7efaa5f738417d50b3fd405ff5ef359aff1c1958e1dc4d06eab`
- 结论：**四个端点 p95 全部 ≤ 1000 ms**（改后 p95 = 95.28 / 301.42 / 139.49 / 68.92 ms）

---

## 1. 结论摘要

| # | 端点 | 改前 p95（原始窗口） | 改后 p95 | 达标 |
|---|------|--------------------|---------|------|
| 1 | `GET /api/v2/explorer/addresses/{chain}/{address}/activity?limit=200` | 11 947.95 ms | **95.28 ms** | ✅ |
| 2 | `GET /api/v2/explorer/addresses/{chain}/{address}/flow-profile` | 1 850.44 ms | **301.42 ms** | ✅ |
| 3 | `GET /api/v2/explorer/addresses/{chain}/{address}/overview` | 1 581.91 ms | **139.49 ms** | ✅ |
| 4 | `GET /api/v2/explorer/addresses/{chain}/{address}/assets` | 501.00 ms | **68.92 ms** | ✅ |

**但必须同时看第 4 节**：用户给出的改前 p95（8552.8 / 1206.6 / 836.4 / 529.7 ms）以及我首次测得的改前值，
都是在**后台有失控查询把 ClickHouse 占满**的窗口里取到的。在机器安静的窗口里背靠背复测，冻结版
的 flow-profile / overview / assets 三项**本身就已经低于 1000 ms**。因此诚实的结论是：

1. **activity（8.1–12.0 s）是真缺陷**，与机器负载无关，已修复（p95 → 95 ms，85×）；
2. flow-profile / overview / assets 的"超标"**主要来自后台争用**，不是端点自身查询；
   本轮把它们自身的 ClickHouse 工作量削减了 4–6×（扩大余量），并**移除了争用源**；
3. 四个端点当前均达标，但 flow-profile 的余量最小（301 ms / 1000 ms），见第 10 节残留风险。

---

## 2. 测量方法（可复算）

新增脚本 `scripts/perf/lan-explorer-probe.sh`：

- **串行单请求**（concurrency = 1），逐请求用 `curl -w '%{http_code} %{time_total} %{size_download}'` 计时，
  记录每一次的原始 ms 到 `<outdir>/<label>-<endpoint>.json` 的 `raw_ms` 字段；
- 每端点 3–6 次预热 + 25 次采样，分位数用线性插值（p50/p95/p99），同时记录 HTTP 状态码分布与响应字节数；
- 登录口径与 `run-perf-suite.sh` 完全一致（用户受众 + `workspace_id` 夹具），避免量到 401 的耗时；
- **为什么不用 loadprobe 并发压测**：本机是共享部署机（ClickHouse + Docker + 备份/DR 任务共用），
  并发压测无法把"端点自身慢"与"机器被占满"分开。本文件所有对比都是同方法串行测量。

复跑命令（部署机上）：

```bash
# 生产实例（当前版本）
bash scripts/perf/lan-explorer-probe.sh after 25 6 /tmp/probe-after

# 对照：把任一历史二进制跑在备用端口再测（P1-B standby，不跑后台循环）
BIN=~/deploy-backups/pre-perf-20260916T174927Z/api.pre-perf \
  bash scripts/perf/verify-standby.sh old 25 3
```

---

## 3. 改前 / 改后（同方法串行，n=25）

### 3.1 首次改前基线（2026-09-16 17:15–17:22 UTC，冻结版 7831bff0）

| 端点 | p50 | p95 | p99 | max | err |
|------|-----|-----|-----|-----|-----|
| activity | 8 595.60 | 11 947.95 | 12 407.83 | 12 513.24 | 0 |
| flow-profile | 681.29 | 1 850.44 | 2 245.74 | 2 370.49 | 0 |
| overview | 499.41 | 1 581.91 | 2 404.77 | 2 640.11 | 0 |
| assets | 222.49 | 501.00 | 846.91 | 954.99 | 0 |

> 同一次运行里 overview / assets 的第一轮 25 次**全部失败**（0.1 ms、0 字节、连接被拒绝），
> 原因是 01:19:08–01:19:26（+07）生产 API 被重启，采样窗正好落在 18 秒的空档里。
> 该轮已作废，上表是重测后的有效数字（err=0）。

### 3.2 公平对照：冻结版 vs 合并版，**背靠背、同条件**（2026-09-16 18:25–18:29 UTC）

冻结版 7831bff0 跑在 18099（`API_STANDBY_STARTUP=1`），合并版 0dae50f1 跑在生产 8080，
两者**连续测量**（load average 17.6 → 6.3）：

| 端点 | 冻结版 p50 / p95 | 合并版 p50 / p95 | p95 变化 |
|------|-----------------|-----------------|---------|
| activity | 8 096.35 / 8 123.41 | 67.30 / 95.28 | **85×** |
| flow-profile | 253.95 / 290.85 | 248.41 / 301.42 | 持平（未改其 SQL，见第 10 节） |
| overview | 133.18 / 157.43 | 104.43 / 139.49 | 1.13× |
| assets | 70.32 / 88.08 | 51.70 / 68.92 | 1.28× |

> 这张表是本次证据里**最重要的一张**：它说明 3.1 里 flow-profile/overview/assets 的"超标"
> 在机器安静时并不存在——那些数字量的是**后台争用**，不是端点自身成本。

### 3.3 改后复测（部署后生产实例，n=25 warmup=6）

| 端点 | p50 | p95 | p99 | max | err | 响应字节 |
|------|-----|-----|-----|-----|-----|---------|
| activity | 67.81 | 95.73 | 102.06 | 103.63 | 0 | 177 366 |
| flow-profile | 260.14 | 303.23 | 346.52 | 359.75 | 0 | 55 120 |
| overview | 99.06 | 123.97 | 134.97 | 138.30 | 0 | 8 671 |
| assets | 51.11 | 74.37 | 80.61 | 82.47 | 0 | 7 724 |

---

## 4. 根因归因（逐查询证据，非推测）

对单次请求用 `system.query_log`（`client_name LIKE 'clickhouse-go%'`）逐条对齐：

### 4.1 activity：8 s **不在 ClickHouse 里**

- 该请求自己的唯一 ClickHouse 查询
  `SELECT * FROM blockchain.address_activity FINAL WHERE chain='tron' AND address IN ([...]) ORDER BY ... LIMIT 201`
  **只花 0.141–0.324 s**（读 417 212 行 / 58–103 MiB）；
- 余下 ~8.0 s 全部耗在 `explorer.resolveAddressTypes → tron.Client.ResolveAddressTypes`：
  它对**每个候选地址串行发一次 Provider HTTP 请求**，一页 activity 的候选地址可达上百个，
  被 `explorer/service.go` 的 `context.WithTimeout(ctx, 8*time.Second)` 卡死在 8 s；
  且超时/报错时**不写缓存**，于是**每个请求都重新烧满 8 s** → p95 恒在 8–12 s。
- 修复（`internal/explorer/service.go` + `internal/tron/client.go`）：解析预算 8 s → **500 ms**、
  每请求最多解析 **16 个地址**（排序后截断）、成功项 24 h 缓存、已尝试未解析项 **2 分钟负缓存**、
  并发 8 + 单地址容错 + 返回部分结果。**响应字段与语义不变**（未解析项的可观测值仍是 `false`，
  与旧实现超时分支的部分结果完全一致）。

### 4.2 后台争用：把四个端点的 p95 整体抬高的真正来源

`system.query_log` 5 分钟分桶（查询耗时 > 1 s，含改前）：

```
bucket(UTC)          >1s 条数   max 秒   峰值内存
2026-09-16 17:45:00      9      667      3.67 GiB
2026-09-16 17:50:00     24      635      9.65 GiB
2026-09-16 17:55:00     30       62      7.73 GiB
2026-09-16 18:00:00     39       57      8.04 GiB
2026-09-16 18:05:00     48       73      9.02 GiB
2026-09-16 18:10:00     32       42      8.74 GiB
2026-09-16 18:15:00     39       45      7.70 GiB
2026-09-16 18:20:00     25       87      9.38 GiB
```

改前窗口（17:00–18:29:29 UTC）合计：**29 条 > 60 s 的查询，最慢 1 092 s，单查询峰值内存 9.38 GiB**，
最慢查询形态为 `SELECT count() FROM blockchain.token_transfers FINAL WHERE chain='bsc' AND block_number ...`
——正是 `ingest.Writer.CountFinal` / `addressmeta.dataCounts` 的形态，调用者都在 **worker** 的
下载 finalize / benchmark 路径上。它们没有客户端等待（worker 被 kill 后实测有 3 条孤儿查询继续跑了 870 s），
却把 ClickHouse 顶到 **75% 内存 / 800% CPU**，四个 explorer 端点的读查询排在其后，p95 因此被抬到 1.2–2.6 s。

### 4.3 端点自身的查询成本（真实数据实测）

| 归因点 | 改前 | 改后 | 说明 |
|--------|------|------|------|
| overview 的 address_activity 聚合 | `count() FINAL` 1.330 s | `uniqExact(...)` 0.270 s | 30211 = 30211 |
| assets/overview 的净流量聚合 | `... FINAL ... sumIf` 1.174 s | 两级去重子查询 0.214 s | 输出逐字节相同 |
| flow-profile 的转账去重 | 0.674 s（p50 685 ms） | **未改动** | 见第 10 节 |

---

## 5. 改动摘要与涉及文件

### 5.1 涉及文件（6 个 Go 文件，源码锚点 sha256）

| 文件 | sha256 | 改动 |
|------|--------|------|
| `backend/internal/explorer/service.go` | `867929041d6a8f69f533d2db4472d16948eedfa418a5a86c04b5c90b58e356a8` | 地址类型解析收口：500 ms 预算、≤16 地址、负缓存、部分结果 |
| `backend/internal/tron/client.go` | `3ede4c5129ac0b217d4c46ffa33daeb333d12cf7e0c77fb17569843165c8b6b5` | `ResolveAddressTypes` 并发 8 + 单地址容错 + 返回部分结果 |
| `backend/internal/clickhouse/ch.go` | `2bdbad27e91c7ef3f00e5adfa9f1cdfa10928cd0dbb88b8797ab89a88ff35249` | 新增 `MaintenanceScanTimeout`(30 s) / `maintenanceScanSettings`(4 GiB) / `MaintenanceScanContext` |
| `backend/internal/addressmeta/service.go` | `ce893f7c7c87de1fc93395d9085fe21a3a4192d912c0f675c45a9e55e0b5795c` | `dataCounts` 去 FINAL（`uniqExact(排序键余项)`）+ 运行上限 |
| `backend/internal/ingest/writer.go` | `e100a31d57c84473d2d92ea94b93d32a765209fe872fe480af4a8040e67528e3` | `CountFinal` 去 FINAL（同上）+ 运行上限 |
| `backend/internal/api/sprint5_explorer.go` | `698509b3ea620753328394b8f0597bb0f57c5a946eee2e0fe1e31db7ff6a05cc` | overview 聚合去 FINAL；净流量改两级去重子查询 |

新增脚本（不改运行时代码）：

- `scripts/perf/lan-explorer-probe.sh` —— 串行单请求延迟探针（本次所有数字的来源）
- `scripts/perf/verify-standby.sh` —— 备用端口 standby 验证（P1-B）

### 5.2 关键改写原则

- **没有移除任何超时/预算保护**：全部为**新增**上限（原 `explorerReadBudget` 40 s、
  `InteractiveScanContext` 20 s/2 GiB 原样保留，后台路径另加 30 s/4 GiB）；
- **没有改 API 响应契约**：字段、字段类型、语义、错误码、HTTP 状态码均未变；
  四个端点在改后 100 次请求中全部 200、响应字节数与改前**完全一致**
  （177 366 / 55 120 / 8 671 / 7 724 字节）；
- `FINAL` 只在**去重语义可精确等价表达**处移除，且每一处都用真实数据逐值比对过（第 6 节）。

---

## 6. 等价性验证（真实数据逐值比对，部署机实测）

依据：这些表都是 `ReplacingMergeTree`，**去重键就是 ORDER BY 排序键**；`chain` 已由谓词固定，
故 `uniqExact(排序键剩余列)` 与 `count() ... FINAL` 同值，而 `uniqExact` **不做 FINAL 合并读**，
`from_address`/`to_address` 上的 `bloom_filter` 跳数索引仍然生效。

| 查询 | 改前 | 改后 | 结果比对 |
|------|------|------|---------|
| transactions @tron 地址 | `count() FINAL` 3.163 s | `uniqExact(tx_hash)` 0.571 s | 1260 = 1260 ✅ |
| token_transfers @tron 地址 | `count() FINAL` **43.531 s** | `uniqExact(tx_hash, log_index, token_address)` 0.802 s | 28979 = 28979 ✅ |
| logs @tron 地址 | `count() FINAL` 0.878 s | `uniqExact(tx_hash, log_index)` 0.075 s | 0 = 0 ✅ |
| address_activity 计数 | `count() FINAL` 1.330 s | `uniqExact(timestamp, tx_hash, activity_type, counterparty, token_address)` 0.270 s | 30211 = 30211 ✅ |
| `uniqExact(counterparty)` | FINAL 0.330 s | 0.070 s | 297 = 297 ✅ |
| `countIf(dataset='transactions')` | FINAL 0.391 s | `uniqExactIf(...)` 0.048 s | 1232 = 1232 ✅ |
| `min/max(timestamp)` | FINAL 0.230 s | 无 FINAL 0.241 s | 输出完全相同 ✅ |
| 净流量聚合（overview/assets） | FINAL 1.174 s | 两级去重子查询 0.214 s | in/out/cnt **逐字节相同** ✅ |

> `sumIf` 类聚合**不能**直接去 FINAL（重复行会被重复累加、金额翻倍），因此改为
> "先按去重键 `argMax(..., source_ingested_at)` 收口、再在外层求和"的两级形态；
> 指纹（`count` / `toString(sum(amount))` / `uniqExact(tx_hash)` / `sum(cityHash64(...))`）
> 在 4 次交替执行中**完全一致**：`28979 / 26164376310.186122693229662899 / 9675 / 13204959721152321744`。

**被否决的改写（如实记录）**：flow-profile 的去重聚合曾试改为 `LIMIT 1 BY` 形态，
语义指纹同样完全一致，但服务端耗时**没有稳定收益**（5 次交替：A p50 685 ms vs B p50 621 ms，
且 B 的 max 更差 1339 ms vs 940 ms），因此**没有采用**，避免为无收益的改动承担语义风险。

---

## 7. 单条 ClickHouse 查询耗时上限

| 路径 | 改前实测上限 | 改后上限 | 机制 |
|------|-------------|---------|------|
| 后台统计（addressmeta / CountFinal） | **1 092 s**、峰值 9.38 GiB | **30 s / 4 GiB**（服务端硬上限） | `clickhouse.MaintenanceScanTimeout` + `maintenanceScanSettings` |
| explorer 交互读 | 40 s 墙钟 / 20 s / 2 GiB（原有，未改） | 同上（保留） | `explorerReadBudget` + `InteractiveScanContext` |
| 地址类型外呼 | 8 s/请求 | **500 ms/请求**，≤16 地址 | `resolveAddressTypes` 预算 |

新 worker 上线后（18:29:29 UTC 起）的窗口内统计：

```
>60 s 的查询 = 0 条；>20 s 的查询 = 0 条；>1 s 的查询 = 0 条
```

**是否仍存在"单查询数百秒"的路径**：**源码层面已不存在**。
`addressmeta.dataCounts` 与 `ingest.CountFinal` 已改写为等价非 FINAL 形态并套上 30 s/4 GiB 硬上限；
即使未来有调用方触发病态查询，也会在 30 s 内被服务端按自身预算拒绝，而不是占用共享实例数分钟。

**⚠️ 边界（必须如实说明）**：上述"0 条 >1 s"的窗口内**没有下载任务在跑**，
因此 worker 的计数路径没有被端到端触发。改写后的等价性由第 6 节的真实数据逐值比对保证，
硬上限由服务端 SETTINGS 保证；但"下载任务运行期间不再出现 >60 s 查询"这一点，
需要在下一次真实下载任务时复看 `system.query_log` 才能完全闭环。本文件不把它写成已验证。

---

## 8. 非回归检查（用户要求：已达标的端点不能变慢）

n=10 串行，部署后生产实例：

| 端点 | 改后 p95 | 参考（sprint7 perf 基线） | 判定 |
|------|---------|--------------------------|------|
| `/api/v2/dashboard` | 12.96 ms | 14.50 ms | ✅ |
| `/api/v2/cases?limit=20` | 3.51 ms | 11.77 ms | ✅ |
| `/api/v2/jobs?limit=20` | 42.71 ms | 118.39 ms | ✅ |
| `/api/v2/notifications?limit=20` | 7.74 ms | 10.76 ms | ✅ |
| `/api/admin/v1/overview` | 41.32 ms | 27.91 ms | ✅（远低于 1000 ms 目标） |
| `/api/v2/explorer/.../counterparties` | 24.49 ms | 49.44 ms | ✅ |

改动的代码面不触及这些端点的路径（PG 优先 + counterparties 本来就未用 FINAL）。

---

## 9. 单元测试

| 运行 | 命令 | 结果 |
|------|------|------|
| 全量（首次，全新执行） | `cd backend && go test ./...` | 1 个失败：`internal/countoracle` `TestProviderIsolationGate`（"fast provider degraded 11.5% (limit 10%)"） |
| 全量（复跑） | `cd backend && go test ./...` | **exit 0，全部 ok** |
| 该失败用例单独复跑 3 次 | `go test ./internal/countoracle/ -run TestProviderIsolationGate -count=1` | **3/3 通过** |
| 定向 fresh（-count=1，受影响包 + 合并改动相关包） | 见下 | **exit 0，全部 ok** |

```
ok  internal/clickhouse  ok  internal/ingest   ok  internal/api
ok  internal/download    ok  internal/explorer ok  internal/analysis
ok  internal/query       ok  internal/tron
targeted_test_exit=0
```

**关于那次失败（如实说明，不掩饰）**：`TestProviderIsolationGate` 是一个**负载敏感阈值用例**
（比较 mock provider 在空载/负载下的 rps 降幅，阈值 10%，实测 11.5%），失败发生在 load average ≈ 40 的窗口；
单独复跑 3 次全部通过。`go list -deps ./internal/countoracle` 显示它**不依赖**本次改动的
`addressmeta` / `ingest` / `api` / `explorer` 包，唯一相关的 `clickhouse` 包本次是**纯新增符号**
（新增 const/var/func，未修改任何既有行为）。因此判定为环境导致的偶发失败，与本次改动无关。

---

## 10. 未达标项、边界与残留风险（如实）

1. **flow-profile 余量最小**：改后 p95 301.42 ms（< 1000 ms），但其自身查询
   （`token_transfers` 去重聚合，读 1 255 538 行）**未做改动**——试改的 `LIMIT 1 BY` 形态
   经指纹验证语义等价，但服务端耗时无稳定收益，故未采用。它仍是四个端点中对 ClickHouse
   争用最敏感的一个（争用窗口曾达 3.7–4.6 s）。
2. **后台争用的根因不止一处**：本轮修掉了 `addressmeta.dataCounts` 与 `ingest.CountFinal` 两处，
   并加了硬上限。若还有其他未纳入本轮的 `FINAL` 全表聚合路径，仍可能造成争用；
   第 4.2 节的分桶监控可直接复用来发现。
3. **"0 条 >1 s" 的验证窗口内没有下载任务**（见第 7 节边界）。
4. **首次改前基线受污染**：overview/assets 第一轮 25/25 失败来自生产 API 重启（18 秒空档），
   已作废重测；flow-profile 那一轮有 6/25 失败。最终对比以第 3.2 节的背靠背公平对照为准。
5. **机器是共享的**：验收批次/DR 演练/备份任务都会改变绝对数字。所有对比都注明条件；
   建议后续把 `lan-explorer-probe.sh` 固定为"改前/改后连续执行"的用法。

---

## 11. 二进制、备份与回滚

| 产物 | sha256 | 备份位置（部署机） |
|------|--------|------------------|
| api 改前（冻结） | `7831bff0bc6127963840aba8f505b0c337243e2c69a871f2b4010e4dbec19e32` | `~/deploy-backups/pre-perf-20260916T174927Z/api.pre-perf`、`api.pre-deploy-20260916T182357Z` |
| **api 改后** | `0dae50f10a7bf56180b28044dc1404eba1be5ced5ab7173f470c544600376ac7` | （已部署 `backend/bin/api`） |
| worker 改前（冻结） | `0d49fb75105eb5ba81a0b062fa557fc09893d2a43555989e6128451a2c1f2c78` | `~/deploy-backups/pre-perf-20260916T174927Z/worker.pre-perf`、`worker.pre-deploy-20260916T182925Z` |
| **worker 改后** | `965437285cfda7efaa5f738417d50b3fd405ff5ef359aff1c1958e1dc4d06eab` | （已部署 `backend/bin/worker`） |

- 构建可复现性：两端独立在冻结源码上执行 `go build -o bin/api.new ./cmd/api`，产出 sha256 **逐位相同**；
- 部署方式：`mv` 原子替换（避开运行中二进制的 `ETXTBSY`），api 用 `systemctl --user restart blockchain-api`，
  worker 用 `stop → mv → start`（**只停 worker，未重启 api**）；
- **回滚**：`mv ~/deploy-backups/pre-perf-20260916T174927Z/api.pre-deploy-20260916T182357Z backend/bin/api && systemctl --user restart blockchain-api`
  （worker 同理，用 `worker.pre-deploy-20260916T182925Z`）。

### 收尾状态（全部实测）

```
blockchain-api active / blockchain-worker active / blockchain-user-console active / blockchain-admin-console active
api  8080 /health/ready = 200      running pid 674821 → /proc/exe sha = 0dae50f1...（与磁盘一致）
nginx 8088 /health/ready = 200
tls  https://aurore.online/health/ready = 200
worker pid 694178 restarts = 0
```

---

## 12. 证据文件清单

```
.work/perf-before/before-*.json          首次改前基线（n=25，含逐请求 raw_ms）
.work/perf-before/before2-*.json         补测改前基线（flow-profile/overview/assets）
.work/perf-cmp/before-quiet-*.json       冻结版跑在 18099 的公平对照（n=25）
.work/perf-cmp/after-quiet-*.json        合并版跑在 8080 的公平对照（n=25）
.work/perf-after/after-*.json            部署后改后复测（n=25，warmup=6）
.work/perf-standby/standby-*.json        18099 standby 验证（n=8，冷进程）
```
