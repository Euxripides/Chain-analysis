# 勘误：minio-metadata-verify.sh v1 的假 PASS 输出（已保留，不得引用为证据）

- 记录时间：2026-09-13T03:4x+07 / 2026-09-12T20:4xZ
- 结论：**本目录下 v1 的产物不是有效证据，禁止引用**。原始文件按"保留失败证据"原则原样留存，未删除、未改写。

## 1. 为什么是假 PASS

v1 在 verify 模式下**从未真正比对恢复目标**：`compared` 恒为 0，只做了"清单自洽性"断言，
却在 `verdict` 里写 **PASS**；退出码也取自一个空的 shell 数组，于是"verdict=FAIL 却 exit 0"。
即"没有对象比较的假 PASS"。

## 2. v1 的缺陷清单（v2 已逐条修掉）

| # | 缺陷 | v2 处置 |
|---|---|---|
| D1 | verify 也调用 capture_semantics，覆写备份目录的 `bucket-semantics.json`，且探测生产桶而非恢复目标 | capture 只新建不覆写；verify 完全只读备份目录，目标语义另写 `$EV/minio-target-semantics-<TS>.json` |
| D2 | `version info`/`ilm` 任意失败都判成"无版本、无规则"（权限拒绝→假 PASS） | 只有明确的 API 语义错误（`is un-versioned` / `lifecycle configuration does not exist`）才判"确实没有"，其余一律 FAIL |
| D3 | 标签查询失败被 `2>/dev/null`/`|| true` 吞成空标签 | 严格检查 rc；非"无标签"的失败一律 FAIL |
| D4 | 声称比对用户元数据指纹，实际只比 ContentType/ETag/标签；size 采集未比对 | 采集并比对**全部用户元数据指纹**与 size |
| D5 | "unversioned" 被当成"无历史版本" | 全量 `ls --versions`，断言无 delete marker；多版本/delete marker 存在即 FAIL（覆盖"曾启用后 suspended"） |
| D6 | 默认只取前 200 个对象，未记录总量/截断 | 记录 total/sampled/truncated 与抽样策略；**键集合与版本全量比对** |
| D7 | `dimensions` 用 inventory_ok 而代码写 compared_ok，维度不完整却 PASS | 维度取值与真实比对结果一一对应 |
| D8 | 允许目标桶 == 源桶（拿生产桶自比） | 显式拒绝目标桶与源桶同名 |

## 3. v1 产物（保留）

- `v1-false-pass/minio-object-metadata-20260912T203*.json` —— 其中 `verdict: PASS`、`compared: 0`
- `v1-false-pass/minio-object-metadata-20260912T203*.md`
- `v1-false-pass/backup/minio/` —— v1 生成的 bucket-semantics.json 与抽样清单

引用规则：这些文件只能作为"v1 缺陷的现场证据"引用，**不得**作为 MinIO 维度已覆盖的证据。
