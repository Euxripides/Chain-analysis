# AD-004：审计与登录事件使用可复算的顺序哈希链

- 状态：Accepted
- 日期：2026-09-12
- 关联：§6.5、§6.6.5、§27 AUDIT-004

## 背景

第一版实现把"哈希载荷"写成 Go 侧的内存 map，只把 `detail` 列落库。
校验时无法从数据库复算哈希，导致链**永远校验失败**（灰度中实测 `event_hash_mismatch`）。
另一个隐蔽缺陷：Go 的 `time.Now()` 有纳秒精度，而 PostgreSQL `timestamptz` 只有微秒，落库后取整，
时间戳无法字节级复现。此外 `event_hash` 只覆盖 Go map，行内其它列被改写无法检测。

## 决策

1. 哈希载荷必须**能由数据库同一行重新推导**。`internal/audit.chainReaders` 负责按列重建载荷，
   与写入侧的 `chainPayload` 逐字段对应。
2. 时间字段参与哈希时统一用 `occurred_at_unix_micro`（UnixMicro 整数），写入前 `Truncate(time.Microsecond)`，
   彻底消除 Go/PG 精度取整差异。
3. 可空字段在载荷中统一为空字符串（而不是 NULL），避免 `null` 与 `""` 在两侧表现不一致。
4. `jsonb` 列（`before`/`after`/`changes`/`detail`）读回后重新规范化，保证键排序稳定。
5. 链头 `audit_chain_heads` 用 `SELECT ... FOR UPDATE` 串行化，`seq` 单调递增且不重不漏。
6. 校验（`VerifyChain`）同时检测：载荷被改写、`prev_hash` 不连续、`seq` 跳跃、链头哈希不匹配。

## 影响

- 灰度期间发现并修复了上述缺陷。修复前写入的 61 条 audit 与 22 条 login 记录从未有效，
  已在 `app.audit_maintenance` 受控模式下重置（事件记录见最终验收报告）。
- 载荷中不包含超 2^53 的整数（会经 float64 往返丢精度）；如需引入，必须改为字符串编码。

## 已知限制

`content_hash` / 每日锚点（`audit_chain_anchors` 写入启用对象锁的 MinIO 桶）尚未接入，状态 `PARTIAL`，
计划在 S6/S7 完成。
