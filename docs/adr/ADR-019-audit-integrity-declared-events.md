# ADR-019：审计完整性「稀疏声明」——不可修复事实的显式登记

- 状态：Accepted
- 日期：2026-09-16
- 关联：ADR-018（不可恢复缺口的不可变边界）、AD-004、§27.6 AUDIT-003/AUDIT-004、迁移 `0063_audit_integrity_declarations.sql`
- 触发事件：两起已在链上留痕、无法回改的真实缺陷（下方「背景」）

## 背景

ADR-018 解决的是**连续前缀缺口**（seq 1..2343 被 TRUNCATE 清空）：缺口区间内没有任何行，
边界之后的链可以用 `sealed_tail_hash` 逐行复算。

2026-09-16 的复核发现两类事实用 0055/ADR-018 的模型**表达不了**：

| # | 事实 | 根因 | 现状 |
|---|---|---|---|
| 1 | 登录链 **8 处稀疏空洞**（seq 3791 / 3799 / 5911 / 5914 / 5915 / 5918 / 5919 / 5920） | 链头推进（`audit_chain_heads`）与写行不在同一事务；插入被约束拒绝时链头已前进 | 写入侧已修（`loginEventResultAllowed` + 同事务 + 失败必留痕）；空洞仍是事实 |
| 2 | 审计链 **2 处内容哈希不可复算行**（seq 52394 / 52399） | 写入侧把不可落库的 UUID 文本（`workspace_id='not-a-uuid'`）放进哈希载荷；uuid 列落库为 NULL、校验时读回空串 | 写入侧已修（`audit.UUIDPayloadValue`）；两行的 `event_hash` 永远无法由其行内容复算 |

两类事实的共同点：**append-only 链上不可修复，也不允许被忽略**。稀疏空洞夹在真实数据行之间，
0055 的 anti-backfill 检查（"已封存区间内必须 0 行"）会把任何覆盖它们的声明判成
`boundary_conflict`；内容不可复算行则根本没有缺失，只是哈希推导输入与落库值不一致。

## 决策

### 1. 新增第二个 kind：`declared_integrity_events`

`audit_integrity_incidents.kind` 由 `unrecoverable_gap` 扩展到
`{unrecoverable_gap, declared_integrity_events}`。稀疏声明不要求区间内为空，
因此它是**逐条**的，而不是区间的。

### 2. 逐条登记受影响 seq 与**链上哈希证据**

新表 `audit_integrity_declared_events(incident_id, chain, seq, disposition, observed_event_hash, evidence)`：

| disposition | 含义 | `observed_event_hash` 的确切语义 |
|---|---|---|
| `lost` | 该 seq 的行不存在 | 后继行的 `prev_hash`（= 被丢事件的 `event_hash`），见下方"连续缺失段"说明 |
| `content_anomaly` | 行存在、链接完好，但内容无法复算 | 该行**实际存储**的 `event_hash` |

**连续缺失段的诚实性**：链上只保留了段尾那一行的哈希（即后继行的 `prev_hash`）；
段内其余行的哈希不可能恢复。声明因此对整段记录同一个哈希，并在 evidence 里用
`hash_role: run_tail | run_interior` 明确标注它对本行到底是什么——
**不制造"每一行都有哈希"的假象**。

### 3. 校验器据此跳过已声明 seq 的内容复算，但**不跳过链接校验**

`VerifyChain` 的新行为：

- 遇到缺口：若缺失的每个 seq 都在声明里且 `disposition=lost`，则用声明的哈希证据接续并继续；
  否则判 `undeclared_gap`（`valid=false`，不合法状态）。
- 遇到已声明 `content_anomaly` 的行：跳过内容复算，但**必须**要求该行存储哈希与声明一致；
  不一致 = 行被改写 → `boundary_conflict`。
- 已声明 `lost` 的 seq 上出现行 → `declared_lost_row_present` → `boundary_conflict`（禁止回填）。
- 链头领先于最后一行（尾部落空）同样必须被声明，否则 `sequence_gap`——此前这条完全静默。
- 新增状态 `declared_events`：无缺口、但存在已声明事实。**绝不退回 `intact`**，
  且响应必须携带 `declared_events` / `declared_lost_count` / `declared_anomaly_count` 与
  `integrity_warnings`，聚合口径要求状态与计数同时成立。

### 4. 声明走 0055 的封存审批链

`POST /api/admin/v1/audit/integrity/incidents/requests` 增加 `kind` 参数；
声明同样需要 `admin:audit:seal`（critical）+ step-up 口令 + **第二名人员** `admin:approval:decide`
批准，执行时重新扫描并逐条比对 seq 与哈希，任何漂移都中止且不写入任何行。
声明表对运行角色 append-only（SELECT/INSERT，无 UPDATE/DELETE/TRUNCATE），并带不可变触发器。

### 5. 写入侧的根因修复（防新增）

- `audit.UUIDPayloadValue`：哈希载荷中的 UUID 字段必须等于"落库后读回"的值
  （不可解析 → `""`，与 `COALESCE(col::text,'')` 一致）。对合法 UUID 与空串是恒等映射。
- `iam.recordLoginEventTx` 同样对 `identity_id / organization_id / session_id / device_id` 归一化。
- 回归：`internal/audit/payload_normalize_test.go`（载荷 ≡ 读回载荷）、
  `internal/iam/loginevent_guard_test.go`（越界 result 在触碰数据库前被拒）。

## 被否决的方案

| 方案 | 否决原因 |
|---|---|
| 重写 seq 3791 之后全部幸存行的 seq/prev_hash/event_hash（"重锚"） | 这就是改写审计历史：既有导出哈希与锚点全部失效，且此后无法证明今日之前未被篡改 |
| 回填缺失行 / 直接改那两行的 `event_hash` | 伪造证据；一旦回填，链的可检测性本身被破坏 |
| 对稀疏空洞复用 0055 的区间模型 | 区间内含真实行，anti-backfill 检查必然判 `boundary_conflict` |
| 只靠"已知空洞基线台账"（外部文件）+ 校验器继续报 `valid=false` | 台账不是链上证据，无法在链内被审计；且 `valid=false` 会让所有依赖校验的门禁永久不可通过 |
| 把 `declared_events` 当作 `intact` 上报 | 把"已声明事实"偷换成"链完整" |

## 影响

- 迁移 `0063_audit_integrity_declarations.sql`（kind 扩展 + 新表 + 不可变触发器 + 权限自检）。
- `audit.VerifyChainResult` 新增 `declared_events` / `declared_lost_count` / `declared_anomaly_count`；
- `integrity_status` 新增取值 `declared_events`；`VerifyChain` 改用 `LoadGapIncident`
  （按 kind 精确取边界，避免把声明区间误当缺口）。
- 新增 `audit.ScanChain`（只读全链扫描，收敛**全部**问题）与
  `audit.SealDeclaredEvents`；后台 API 支持 `kind=declared_integrity_events` 的发起/审批/执行。
- 审计链接口 `valid` 由 `false` 变为 `true`（边界之后逐行复算通过 + 2 条已声明事实）；
  登录链由 `undeclared_gap` 变为 `declared_events`（8 条已声明缺失行）。

## 本环境的声明结果（可复算）

| 链 | incident_ref | 内容 | 校验结果 |
|---|---|---|---|
| audit | `INC-AUDIT-DECLARED-CONTENT_ANOMALY-2-52394-52399` | 2 条 `content_anomaly`（52394 / 52399） | `valid=true`、`integrity_status=sealed_incident`、`declared_anomaly_count=2` |
| login | `INC-LOGIN-DECLARED-LOST-8-3791-5920` | 8 条 `lost` | `valid=true`、`integrity_status=declared_events`、`declared_lost_count=8` |

## 回滚方式

迁移是 additive（kind 约束放宽 + 新增表），旧二进制可继续运行；
回滚到旧二进制后校验器不认识声明，会回到 `valid=false` 的旧结论（事实仍然在库里）。
**已写入的声明不可删除也不可改写**——如需重新审计，只能新增声明或新的审计结论。
