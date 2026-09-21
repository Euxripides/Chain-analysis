# ADR-025：Coverage 断言按 (address, dataset) 判定，作业状态不再连坐

| 项 | 值 |
|---|---|
| 状态 | Accepted（2026-09-21；产品/运维明确「按 dataset 判定」） |
| 关联 | `internal/api/actions.go` 的 `actionCoverageMeta`、`download_datasets`、`dataset_coverage` |
| 触发项 | DL-20260921-0041（tron，815/816 分片）与 DL-20260916-0011（bsc）收敛为 `PARTIAL_FAILED` 后，同作业内已 COMPLETED 的 dataset 也返回 `coverage.complete=false` |

## 背景

- MCP / GPT Actions 响应里的 `meta.coverage` 是「该地址该数据集是否已完整抓取」的唯一证据断言。
  下游（AI 分析、人工研判）据此决定「没查到转出」能否当作「链上不存在转出」。
- 原实现的 full-range 断言要求 **作业级** `download_jobs.status='COMPLETED'` 且该 dataset
  `download_datasets.status='COMPLETED'`。一个作业里任意 dataset 的任意一个分片失败，
  作业收敛为 `PARTIAL_FAILED`，于是同一作业里**已经全部完成并 commit 覆盖**的 dataset 也一起失去断言。
- 现场记录：
  - DL-20260921-0041：8 个 tron 地址的 `transactions` 全部 COMPLETED，`token_transfers` 只有 1 个
    地址的 1 个分片失败，但 8/8 地址当时都显示 `coverage.complete=false`；
  - DL-20260916-0011：同一形态，一个 bsc 大地址的 `transactions` 一个分片失败，其余 dataset / 地址已 COMPLETED。
- 数据层面 dataset COMPLETED 的含义本身已经足够强：`RecomputeDataset` 只有在全部有效分片 COMPLETED
  时才写 COMPLETED；每个分片在 `CompleteShardFinalized` 之前先通过 `validateShard` 并 `commitCoverage`。
  作业级状态只是跨 dataset 的汇总，不是单数据集的证据。

## 决策

1. `actionCoverageMeta` 的 full-range 断言改为按 `(subject, dataset)` 判定：
   - 在一个 `range_type='full'` 的下载作业里，该 dataset 的 `download_datasets.status='COMPLETED'`
     即构成该 (address, dataset) 的完整性证明；
   - 不再要求 `download_jobs.status='COMPLETED'`；同一作业内其它 dataset 的失败不连坐。
2. 断言仍然**不**使用 ClickHouse 行存在性作为证据（零行地址合法）；`start_block/end_block` 仍取自
   `dataset_coverage`（`status='COMPLETE'`）的已提交窗口。
3. `missing_ranges` 的元素粒度就是 (address, dataset)，文案明确到 dataset；`coverage.complete=false`
   只应出现在真正缺数据的 (address, dataset) 上。
4. 不引入新表/新列；`internal_transactions` 仍按调用方显式声明的 `not_asserted_datasets` 处理，
   不因本 ADR 变成「已断言」。

## 被否决的方案

- **保留作业级连坐，出了失败就重跑整作业**：DL-20260921-0041 / DL-20260916-0011 已用「重跑到
  COMPLETED」临时恢复（DL-20260921-0043 / DL-20260921-0044）。但每次一个分片失败都要重抓同作业
  其它 dataset 的完整历史，成本高，而且「已完成数据被当作未知」的窗口无法消除，不能作为长期语义。
- **用 `address_dataset_sync_state` 直接当断言**：它是 per-dataset 水位，但只记录
  `complete_from/complete_through` 时间窗；要证明「全历史」仍要回溯 `source_job_id` 的
  `range_type='full'`，多一跳，而且 0038 迁移的历史 seed 只从 COMPLETED 作业灌入，对本次事故的形态
  反而更弱。保留它作为**增量下载复用**的水位，语义不变。
- **放宽成「ClickHouse 有行即完整」**：与既有证据原则冲突，零行地址会从「未知」错判为「完整」。

## 影响

- 正面：`PARTIAL_FAILED` / `FAILED` / `CANCELLED` 作业中已完成 dataset 立即恢复
  `coverage.complete=true`；`missing_ranges` 只列真正缺口；AI 侧不再把完整数据当未知。
- 边界：作业级 `PARTIAL_FAILED` 仍有运维价值（提示确实有 dataset 失败），只是不再影响断言；失败
  dataset 依旧需要重跑修补——本 ADR 只改断言口径，不自动修复数据。
- 未改动：`download_jobs` / `download_datasets` 状态机、`dataset_coverage` 提交时机、
  `address_dataset_sync_state` 水位与 0038 的历史 seed、前端下载页的覆盖率展示。

## 验证

- tron DL-20260921-0041 的 8 个地址：`batch-overview` 返回 `coverage.complete=true`、
  `missing_ranges=[]`（修复作业 DL-20260921-0043 已完成）。
- bsc DL-20260916-0011：两个 dataset 都 COMPLETED 的地址立即 `complete=true`；唯一 transactions
  失败的大地址只报 `missing_ranges=[{dataset: transactions}]`——即只缺真正缺的那个 dataset。

## 回滚方式

单点回滚：在 `actionCoverageMeta` 的断言 SQL 里恢复 `AND j.status='COMPLETED'` 即可，无 schema、
无数据迁移；`coverage` 只存在于响应中，不持久化。
