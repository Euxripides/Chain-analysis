# ADR-018：审计完整性事件边界（不可恢复缺口的封存与逐项验证）

- 状态：Accepted
- 日期：2026-09-12
- 关联：`docs/dual-system-development-plan.md` §6.6.5 / §27.6 AUDIT-003、AUDIT-004 / §22、AD-004、AD-005、ADR-017
- 触发事件：Sprint 6 验收期间 AUDIT-003 负向用例真实执行 `TRUNCATE audit_events_v2`，清空 `chain='audit'` 的 seq 1..2343

## 背景

Sprint 6 的 AUDIT-003 以「应用 DB 角色执行破坏性操作必须被拒绝」为验收目标。首次执行时
UPDATE / DELETE 被 0043 的行级触发器拒绝，但 **TRUNCATE 成功执行**（PostgreSQL 的行级触发器
不覆盖 `TRUNCATE`，事件触发器也不支持 `TRUNCATE TABLE`，且应用账号同时是表 owner，
`REVOKE` 对 owner 恒定放行）。该语句在验收环境上真实清空了审计链的 seq 1..2343。

Sprint 6 的修复（ADR-017 §5 + 迁移 0054）引入受限运行角色 `blockchain_app`，
使后续运行角色无法再执行 TRUNCATE / DROP / UPDATE / DELETE。但**已经丢失的历史事件不可恢复**。

本轮（Sprint 6 审计修复阻断项）对恢复源做了逐项核查，结论是**不存在任何可用恢复源**：

| 恢复源 | 核查结果 |
|---|---|
| PostgreSQL 物理基备 / `pg_dump` | 环境内不存在；`archive_mode=off` |
| WAL 归档 / PITR | `archive_command=(disabled)`；且无基备时 WAL 无法单独重建数据库 |
| 文件系统 / 虚拟机快照 | `lvs` 只有 `ubuntu-lv`/`data_lv`，无快照卷；无块设备或虚拟机快照 |
| MinIO 审计归档 | `blockchain-audit` 桶只有 2 个 `anchor.json`（每日锚点），不含事件本体 |
| ClickHouse 审计镜像 | `blockchain` 库只有链上数据表，没有审计事件镜像表 |
| 审计导出产物 | `audit_exports` 全部记录的 `from_seq` 均 ≥ 2344，均晚于事件发生时间 |

环境同时满足一组**不可违反的约束**：不得伪造或回填历史审计行；不得删除或改写既有锚点；
不得把 NOT_VERIFIED 表述为 PASS；实际删除功能仍默认关闭。

在此之前，链的状态是：全链 `valid=false` 且永久如此，每日锚点一致性永远只能标注 NOT_VERIFIED。
这既不可验收，也无法表达"事实是什么"。

## 决策

### 1. 把缺口当作必须显式声明的事实，而不是待修复的缺陷状态

引入不可变表 `audit_integrity_incidents` 记录「完整性事件」：明确缺口区间、缺口事件数、
新链可信起点、封存尾哈希、检测方式、恢复尝试结论与证据哈希。缺口本身保持为空——
**系统不提供任何回填历史审计行的入口**。

### 2. 用 `sealed_tail_hash` 把新链密码学地接回旧链

幸存首行（本环境 seq 2344）的 `prev_hash` 就是**被销毁的最后一条事件的 `event_hash`**。
它随幸存行一起保留下来，是旧链尾唯一留存的密码学证据。把它记为 `sealed_tail_hash` 后，
校验器可以从 `trusted_from_seq` 起、以该哈希为起点逐行复算，
于是「边界之后的链」不仅自洽，而且与旧链的断点**显式衔接**。

### 3. `valid` 与「完整性」解耦，二者不可互相替代

- `valid`：只描述**实际被校验到的区间**是否自洽。
- `integrity_status`：`intact` / `sealed_incident` / `undeclared_gap` / `boundary_conflict`。
- `prefix_status`：`intact` / `sealed_unrecoverable` / `unsealed_gap`。

封存之后 `integrity_status` 永远不是 `intact`。这就是「不把 NOT_VERIFIED 偷换成 PASS」的
工程实现：验收不再断言"全链完整"（那是假的），而是断言
**"已封存缺口被如实声明 + 边界之后逐行验证通过 + 保留锚点原样可核对"** 这三件事同时成立。

### 4. 反回填控制：篡改方向双向可检测

校验器在存在封存边界时会主动检查：

- 已封存缺口区间内出现任何行 → `boundary_conflict`，`valid=false`；
- 幸存首行的 `prev_hash` 不等于 `sealed_tail_hash` → `boundary_conflict`；
- 可信起点行缺失 → `boundary_conflict`。

因此既不能靠"补行"掩盖缺口，也不能靠"删边界记录"让缺口无声通过
（删除边界后校验器回到 `undeclared_gap`，`valid=false`）。

### 5. 既有锚点永久不可改写，归档件必须可回读核对

0055 为 `audit_chain_anchors` 增加内容不可变触发器，并回收运行角色的
`UPDATE / DELETE / TRUNCATE`。归档器从 `ON CONFLICT DO UPDATE` 改为
`ON CONFLICT DO NOTHING` + 回读复核，不再重写、也不重传归档对象。
校验侧对每个锚点逐项给出结论，并重新读取 MinIO 对象与其 `content_hash` 比对，
证明保留证据没有被替换。

### 6. 纠正锚点 head_hash 的派生方式（并如实标注历史数据）

0055 之前的归档器用 `max(event_hash)` 作为锚点 `head_hash`——那是区间内的字典序最大值，
**不是链头哈希**，属于真实缺陷。规范语义应为区间末行的 `event_hash`。

既有锚点不可改写，因此 `AnchorHeadMatches` 同时支持两种派生：
`chain_head`（规范）与 `legacy_max_hash`（历史）。使用历史派生的锚点标注为
`verified_legacy_derivation`，并在结论里写明其承诺强度弱于规范语义，不并入普通 `verified` 计数。

### 7. 边界创建走双人审批 + step-up，证据本身也是哈希链

封存需要独立权限键 `admin:audit:seal`（critical）、当前账号密码二次确认，
以及第二名具备 `admin:approval:decide` 的人员批准（请求人不能自审批，DB CHECK 兜底）。
创建 / 审批 / 封存事件写入 `audit_integrity_incident_events`，逐条哈希链接，
对应用运行角色完全 append-only。

### 8. 保留一条无缺口的对照链

`login` 链完整（seq 1..2027，无缺口）。验收同时断言 `login` 的
`integrity_status=intact`——证明边界机制是针对真实缺口的事实声明，而不是全局放行开关。

## 被否决的方案

| 方案 | 否决原因 |
|---|---|
| 回填或重建 seq 1..2343 的行 | 伪造证据；一旦回填，链的可检测性本身就被破坏 |
| 直接改写锚点 / 链头以"修复"链 | 破坏 append-only 与不可抵赖性，且掩盖了真实事件 |
| 把 NOT_VERIFIED 直接改为 PASS / 放宽验收断言 | 偷换结论；用户明确禁止 |
| 删除 audit 链并从 seq 1 重新开始 | 丢弃全部历史证据，且掩盖事件 |
| 仅依赖 0043 的行级触发器 | 不覆盖 TRUNCATE（已由真实事件证伪） |
| 由应用运行角色执行封存 | 运行角色对边界表只有 INSERT/SELECT，创建必须走审批后的受控路径 |

## 影响

- 新增迁移 `0055_s6_audit_integrity_boundary`（3 张表 + 2 类不可变触发器 + 权限键 + 权限自检）。
- 新增权限键 `admin:audit:seal`，授予 `owner` 与 `security_admin`。
- `POST /api/admin/v1/audit/verify` 响应新增 `integrity_status` / `prefix_status` /
  `verified_from_seq` / `sealed_boundary` / `integrity_warnings`（向后兼容）。
- 新增 `/api/admin/v1/audit/integrity* ` 与 `/api/admin/v1/audit/anchors* ` 端点与后台页面
  `/audit/integrity`。
- 归档器语义变更（不再 upsert 覆盖；head_hash 派生纠正）。
- 无缺口的链行为不变（`intact` 路径与 S3 版一致）。

## 回滚方式

迁移全部是 additive（新增表 / 触发器 / 授权），旧二进制可继续运行。
回滚到 S6 代码后，`/audit/verify` 仍返回 `valid=false` + `sequence_gap`，
即回到"Sprint 6 的未验收状态"。
**已封存的边界行不可删除也不可改写**——这是本 ADR 的设计意图；
如果未来需要重新审计，只能新增边界事件或新的审计结论，不能修改既有记录。
