# ERRATUM — "170,675 stat records 含 ETag+user metadata" 声明撤回（2026-09-13）

> 本文件为纠正说明；原脚本、原输出、`VERIFY-V2-BOUNDARIES.txt` 的错误文字**原样保留**，不热改、不删除。

## 1. 被撤回的声明

`verify-secret-rotation-v2.sh` 输出与其自然语言总结曾声称：
> "deep inventory 170,706 行 / **170,675 条 stat 记录含 ETag + user metadata**"。

**该声明不成立，现撤回。**

## 2. 事实（根监控独立解析 + 本会话复核一致）

根监控用 `verify-minio-metadata-coverage.py` 逐行解析 `secret-rotation-20260912T233856Z/post-minio-deep-inventory.txt`（63,879,231 B）：

| 指标 | 实际值 |
|---|---|
| JSON 记录总数 | 170,675 |
| 位于 `===== user metadata =====` 段**之前**的记录 | **170,675（全部）** |
| `metadata_section_records` | **0** |
| `records_with_metadata_field` | **0** |
| `records_with_etag_field` | 170,675（**版本列表行**） |
| success | 170,675 |

即：那 170,675 条全部是 `mc ls --recursive --versions --json` 的**版本列表行**，不是 stat 记录。

## 3. 根因（两处，均为脚本缺陷）

1. `grep -c '"etag"'` 被当作"stat 记录数" → 实际统计的是**版本列表行**；
2. key 提取写成 `sed -n 's/.*"key":"\([^"]*\)".*/\1/p'`，而 `mc` 的 JSON 是 `"key": "..."`（**冒号后有空格**）→ 提取到 0 个 key →
   `===== user metadata =====` 段的 while 循环一次都没执行 → 该段 0 条记录。
   此外 `head -300`/`head -200` 本来就只是 sample，不是全量。

## 4. 修正与新增真实证据

新增 `scripts/release/collect-minio-inventory-v2.sh`、`collect-minio-inventory-v3.sh`（v2 的字段名修正版）：

- v2 真实缺陷：`mc stat --json` 字段是 `status/name/etag/metadata`（**没有 key**），版本号字段是 `versionId`（不是 `versionID`）；
  v2 用错的谓词 → `stat_success` 与 `versions_with_versionid` 恒为 0 的**错误计数**。v2 脚本与输出保留。
- v3 修正后（`EV=secret-rotation-20260912T233856Z`，输出 `minio-inventory-v3/`）：

| 指标 | 值 | 范围 |
|---|---|---|
| expected_versions / accounted_versions | 170,675 / 170,675 | **full（全部 6 桶）** |
| versions_with_versionid / versions_with_etag | 170,675 / 170,675 | **full** |
| stat_attempted / stat_success / stat_error | 182 / 182 / 0 | **sample（每桶前 50 个当前对象）** |
| records_with_etag / records_with_metadata | 182 / 182 | sample |
| metadata 实际内容示例 | `{"Content-Type":"application/json"}` | sample |
| verdict | `METADATA_VERIFIED_SAMPLE` | 明确为 sample |

`blockchain-backups` 为 0 对象 → 0 条 stat → 该桶 `metadata_verified=false`（无对象可验，如实记录，不当作已验）。

## 5. 边界（不得当作已证明）

- **没有**轮换前的原始 metadata/ETag baseline：旧快照的 ETag 已被擦写为 `<e>`，**不得补造旧 metadata、不得重写 scrubbed etag**。
  本次只新增**真实轮换后 snapshot**，可作为**未来** DR 演练的 before/after 对照基线。
- 因此 `minio-object-metadata` 门禁**仍未闭合**：需在 `backup-all` 之前采真实 before 的
  完整 versions / ETag / userMeta / ILM，再在 restore 之后比对，才算关闭。
- 仍然有效的局部证明（不因本撤回而失效）：新凭据真实认证通过、业务真实读取成功、版本列表 success 且 accounted==expected。

## 6. 门禁口径更新

凡引用"170,675 条 stat 记录含 ETag+user metadata"的结论，一律作废；
MinIO 元数据覆盖一律以 `minio-inventory-v3/` 的**分桶计数 + 显式 sample/full 范围**为准。
