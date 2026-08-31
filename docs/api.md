# API V1

Base: `http://SERVER:8080/api/v1`（全部 JSON，时间 UTC）

## 下载任务

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/datasets/ensure` | 创建下载任务（Dataset Service 统一入口） |
| POST | `/datasets/coverage/preview` | 本地 Coverage 预览（复用比例/缺口） |
| GET | `/download-jobs?status=&limit=&offset=` | 任务列表（含进度/行数/请求/成本聚合） |
| GET | `/download-jobs/{id}` | 任务详情（Job + Subjects + Datasets + Shards + 进度） |
| POST | `/download-jobs/{id}/pause` | 暂停（Shard 边界收敛，Checkpoint 落库） |
| POST | `/download-jobs/{id}/resume` | 恢复（重算 Coverage 后只下缺口） |
| POST | `/download-jobs/{id}/cancel` | 取消（已完成数据保留，进入全局 Coverage） |
| GET | `/download-jobs/{id}/events` | SSE 实时事件流 |

### 创建任务示例

```json
POST /api/v1/datasets/ensure
{
  "source": "user_download",
  "priority": 100,
  "subjects": ["0x..."],
  "chain": "bsc",
  "datasets": ["transactions", "token_transfers", "nft_transfers", "logs", "balances"],
  "range": { "type": "full" },
  "reuse_policy": "prefer_local"
}
```

`range.type`：`full` | `block`（from_block/to_block）| `time`（from/to RFC3339）。

## 数据查询（统一数据面）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/datasets/{dataset}?chain=&subject=&limit=&offset=` | 查询 ClickHouse（FINAL 去重） |
| GET | `/coverage?chain=&subject=&dataset=` | 查询 Coverage 区间 |
| GET | `/coverage/stats` | 各 Dataset Coverage 汇总 |

## 导出（流式，不整表进内存）

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/exports` `{job_id, dataset, format}` | format: csv / xlsx / json(NDJSON) |
| GET | `/exports/{id}` | 导出状态 |
| GET | `/exports/{id}/download` | 302 → MinIO 预签名 URL |

## Provider / 健康

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/providers/ankr/status` | 断路器状态/错误率/连续失败 |
| GET | `/providers/ankr/usage?hours=24` | 按 Dataset 的请求/Credits/成本/行数/字节 |
| GET | `/health/live` `/health/ready` | 存活/就绪（PG/CH/MinIO/NATS） |

## SSE 事件

```text
data: {"type":"job_status","job_id":"...","status":"RUNNING","progress":0.42,"ts":"..."}
data: {"job_id":"...","status":"RUNNING","progress":0.42,"downloaded_rows":...,"rows_per_sec":...,"bytes_per_sec":...,"req_per_sec":...,"api_cost_usd":...,"coverage_hit_ratio":...,"elapsed_seconds":...,"eta_seconds":...,"ts":"..."}
```

`events.jobs.{id}` 状态变化；`events.progress.{id}` 1s 聚合进度；`events.shards.{id}` Shard 状态变化。
