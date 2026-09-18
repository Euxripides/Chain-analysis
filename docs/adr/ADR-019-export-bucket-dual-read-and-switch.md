# ADR-019：导出产物桶的读优先级、可审计迁移与写切换（F-R10-BUCKET）

- 状态：**Proposed（设计已定，实现未完成）**
- 日期：2026-09-13
- 关联缺陷：F-R10-BUCKET（P2）
- 关联证据：acceptance-evidence/dual-system-v1/sprint7/f-r10-bucket-inventory-20260913T110228Z（只读 inventory）
- 前置 ADR：ADR-017（运行角色）、ADR-018（审计完整性边界，本 ADR 不触碰审计链）

## 1. 问题（实测，不是推测）

`backend/internal/api/export.go` 与 `export_v2.go` 的 6 个写入点全部使用
`s.deps.Raw.Client().PutObject(ctx, s.deps.Raw.Bucket(), key, ...)`，而 `app.go:106` 用
`cfg.RawBucket` 构造 rawstore，因此**产品导出产物一直落在 `RAW_BUCKET`（blockchain-raw）**，
`EXPORT_BUCKET`（blockchain-exports）只被 governance 审计导出路径（PutNamed/OpenNamed）使用。

只读 inventory（20260913T110228Z，exclude 任何写操作）实测：

| 项 | 值 |
| --- | --- |
| PG `exports` 总行数 | 277（COMPLETED=270 / FAILED=7） |
| PG `object_ref` 非空行 | **270** |
| `blockchain-raw/exports/` | **270 objects / 2,749,993,808 bytes（2.6 GiB）** |
| `blockchain-exports/` | 28 objects / 5,274,869 bytes，**全部是 `audit-exports/...`**（无产品导出） |
| 引用命中 RAW | 270 / 270 |
| 引用命中 EXPORT 桶 | 0 / 270 |
| 两侧都不存在 | 0 |
| 孤儿对象（RAW 有、PG 无） | 0 |

> inventory 自身修正记录（保留）：第一版用 `mc ls <bucket>/exports/` 得到的是**相对前缀**的 key，
> 且 `exports` 表并无 `size_bytes` 列、`psql -c "SET ROLE ...; SELECT ..."` 会先打印 `SET` 标签行，
> 三者叠加曾产生「270 个孤儿、PG 0 行」的假结论；已在同轮修正并重跑（旧轮 20260913T110148Z 原样保留）。

## 2. 决策

### 2.1 读：双读 + 明确优先级

- `EXPORT_READ_ORDER`（默认 `export,raw`）：读取导出对象时按序尝试 `EXPORT_BUCKET` → `RAW_BUCKET`。
- 命中即返回，并在日志/审计中记录**实际命中的桶**（不允许静默）。
- 错误语义：
  - 对象在**任一**桶存在且可读 → 成功（不因第一个桶 404 而失败）；
  - 两个桶都返回 NoSuchKey → 保持既有 DATA-008 语义：503 `DATA_OBJECT_MISSING` + `export.download_failed` 审计（不宣称成功）；
  - 非 404 错误（权限、网络、5xx）→ **不**回退、直接失败（避免把权限问题伪装成"对象缺失"）。

### 2.2 写：显式开关，默认不变

- `EXPORT_WRITE_BUCKET`：`raw`（默认，保持 100% 现有行为）| `export`（切写后新产物只落新桶）。
- 写入完成事件 `export.completed` 的 detail 增补 `object_bucket` 与 `object_ref`（PG 侧可审计的落桶证据）。

### 2.3 迁移：只复制不删除，逐对象字节级校验，中断可续跑

- 新增 `backend/cmd/exportmigrate`（`-mode inventory|migrate|verify`，默认 inventory；迁移必须显式 `-apply`）。
- 迁移对每个对象：从 RAW 流式读出 → 计算 sha256 → 写入 EXPORT → **回读**新对象并复算 sha256 → 一致才记账。
- 账本表（迁移 0058）：`export_object_migrations(export_id, object_ref, from_bucket, to_bucket, bytes, sha256, status, attempts, last_error, created_at, updated_at)`，
  `unique(export_id, to_bucket)` 保证**重复运行幂等**；`status` 让**中断后可续跑**（只处理未 VERIFIED 的行）。
- **绝不删除源对象、绝不改写审计链、绝不修改历史证据**；失败对象保留并记录 last_error。

### 2.4 切写与回滚

- 迁移全部 VERIFIED 且 `verify` 通过后，才把 `EXPORT_WRITE_BUCKET` 切到 `export` 并重启 API（受控部署）。
- 回滚 = 把该变量改回 `raw` 并重启；**读路径不受影响**（双读保证迁移后对象与切写后对象都可读）。
- 回滚不需要移动任何对象，因此不存在数据丢失窗口。

## 3. 必须取得的验收证据（缺一不可，禁止用文档/单测代替）

1. 双读优先级与错误语义：RAW-only 对象可读、EXPORT-only 对象可读、两桶皆无 → 503 DATA_OBJECT_MISSING、非 404 不回退。
2. 迁移 inventory 对账：对象数 / 总字节 / 每对象 SHA（或 ETag）与迁移前一致。
3. PG 绑定：`object_ref`、组织/工作区归属在迁移前后不变（`SELECT` 前后对照）。
4. 时序证据：迁移前读旧桶成功 → 迁移后读新桶成功 → 切写后**新导出只写新桶**（MinIO 对象列表 + PG 行对照）。
5. 真实输出字节一致：API 下载 / UI 下载 / MinIO 对象三者 SHA 相同。
6. 非授权工作区 fail-closed：其他 workspace 的导出下载被拒（403/404），不因双读而放宽。
7. 迁移中断可续跑 + 重复运行幂等：人为中断后重跑，账本无重复、无对象丢失。
8. 切写回滚后新旧对象仍可读且不丢。
9. 运行身份、配置 SHA、审计 `request_id`/`trace_id` 与产物一一对应。
10. 失败证据全部保留（不覆盖、不删除、不 clean）。

## 4. 影响面

- 代码：`internal/config/config.go`（已加入 2 个字段/环境变量，默认值保持行为不变）、
  `internal/api/export_object_store.go`（新增，读/写/stat 三个入口）、`internal/api/export.go`、`internal/api/export_v2.go`、
  `internal/api/sprint5_exports.go`（DATA-008 stat 改为双读）、`backend/cmd/exportmigrate`（新增）、`migrations/postgres/0058_*.sql`（新增）。
- 运维：部署时需要新增 `EXPORT_WRITE_BUCKET`/`EXPORT_READ_ORDER` 两个环境变量（默认值即现状，先加变量不变行为）。
- 不改动：审计链、`blockchain-raw` 既有对象、`exports` 表历史行（除新增账本表）。
