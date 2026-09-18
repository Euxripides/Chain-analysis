# 缺陷记录 S7-DEF-OBS-01：`provider_usage.created_at` 不存在 + amplification 口径错误

- 发现方式：PG 容器日志中出现高频 ERROR（**15 分钟 897 次，约 1 次/秒**），追查到 `backend/internal/download/v14.go`。
- 发现时间：2026-09-13 04:0x–04:1x +07 / 2026-09-12T21:0x–21:1xZ
- 严重度：**P1**（不是崩溃，但一个 §28 要求的观测信号长期静默失效 + 每秒刷数据库错误日志）
- 状态：**已修并在真实部署上验证**

## 1. 缺陷一：查询了不存在的列

`provider_usage` 的时间列是 **`request_time`**，没有 `created_at`（列清单实测确认）。`v14.go` 的两处查询写的是 `created_at`：

| 位置 | 影响 |
|---|---|
| `v14.go`（phase ratio，30s 缓存） | 查询每次报错 → `phaseRatio` 永远停留在初值 → `ankr_fetch_waiting_ttfb` / `ankr_fetch_body_inflight` 的相位拆分**不反映真实情况** |
| `v14.go`（retry amplification，每调用一次） | 查询每次报错 → **`AnkrRetryAmplification` 从不写入**，§28 的 Retry Amplification 信号在运行时缺失 |

两处错误都被 `_ = sc.store.Pool.QueryRow(...)` **静默吞掉**，所以既没有告警也没有降级标记。

**修复**：列名改为 `request_time`；并把两处 `_ =` 改为显式 `if qerr := ...; qerr != nil { slog.Warn(...) }` —— 查询失败必须留痕，**不允许把上一次的旧值当作实时结果**。

**真实验证（不是只看配置）**：worker 于 `2026-09-12T21:10:52Z` 重启；以日志时间戳严格过滤：

| 窗口 | `column "created_at" does not exist` 计数 |
|---|---|
| 重启前（含重启时刻，约 60 s） | 56 |
| **严格晚于 21:10:53Z** | **0** |

## 2. 缺陷二：amplification 口径错误（列修好之后仍然错）

`progress.go` 的失败判据把成功状态定义为
`status IN ('ok','success','cache_hit','singleflight_hit')` 且 `http_status < 400`；
而 `v14.go` 用 `COUNT(*) FILTER (WHERE status='ok')` 当分子/分母，导致：

1. **漏算 `success`**：近 1 小时实测 `ok=96 / success=25 / error=21 / cache_hit=6` —— 只认 `ok` 会把 25 行成功漏算成失败。
2. **把缓存命中算成外部调用**：`cache_hit` / `singleflight_hit` 不是外部 Provider 请求，会让 attempts 与"未成功"同时虚高。

**同一时刻两种口径的实测对比（同一份数据、同一个 5 分钟窗口）**：

| 口径 | 结果 | ratio |
|---|---|---|
| 旧：`COUNT(*)` / `COUNT(status='ok')` | attempts=30 / ok=19 | **1.5789** |
| 新：外部尝试 / 外部成功（含 success，排除 cache/singleflight，http_status<400） | ext_attempts=28 / ext_ok=24 | **1.1667** |

**部署后端到端验证**：`/metrics` 上 `ankr_retry_amplification` 由 `1.5789473684210527` 变为
`1.1666666666666667` —— 与独立算出的新口径**完全一致**，说明改的是真实运行时行为而不是注释。

## 3. 仍未证明的部分（不得据此称指标已验收）

- `ankr_fetch_waiting_ttfb` / `ankr_fetch_body_inflight` 当前读数为 **0**：这是 `active=0`（无在途 fetch）时的**正确值，但无法与"陈旧值"区分**。
  本次只加了失败告警，**没有**给这两个 gauge 加"最近更新时刻"或 staleness 标记 → 仍需补。
- `retry amplification` 在"窗口内没有成功外部调用"时**分母为 0**：现按 `slog.Debug` 留痕并**不更新**指标。
  该语义（未定义 vs 保留旧值）尚未与计划/运维口径对齐。
- §28 原始要求的分母究竟是"Provider 成功调用"还是"实际 DURABLE 页"**尚未在计划文档中得到明确确认**；
  当前实现取的是**外部 Provider 成功调用**（与 metric help `attempts/requests` 一致）。
  若计划本意是 DURABLE 页，需要改成 join `download_pages` —— **这一点必须由计划/ADR 定稿，不能由实现自行决定**。

## 4. 部署身份（本次变更后重新核实）

| 项 | 值 |
|---|---|
| `backend/bin/api` | `e0a7c3bd1983cc18da85bd5e1a2e3712b9dde43bb67d48ee5e4722f53368abb7`（未变：v14.go 的改动未进入 api 链接产物） |
| `backend/bin/worker` | `9c41079d5c59067e0585166d049219de0f14fd93069b3c3a194dff0ecf9fccb6` |
| 运行中进程 | `/proc/3413432/exe` = api 哈希；`/proc/3413434/exe` = worker 哈希（均一致） |
| 回滚点 | `api.bak-sem-20260912T2130Z`、`worker.bak-sem-20260912T2130Z` |
| 构建/检查 | `go build ./...` rc=0、`go vet` rc=0、`go test ./internal/download/... ./internal/api/...` rc=0 |
| 健康 | `/health/ready` 200（postgres/clickhouse/minio/nats 全 ok） |
