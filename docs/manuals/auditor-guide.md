# 审计员手册（Auditor Guide）

> 适用对象：持有系统角色 `auditor` 的审计人员，以及需要复核审计结论的安全/合规人员。
> 适用范围：后台管理系统（`/admin`）的审计日志、完整性边界、锚点与审计导出；对应的只读 API 前缀 `/api/admin/v1/audit/*`。
> 关联文档：`docs/adr/ADR-018-audit-integrity-incident-boundary.md`（完整性事件边界）、`AD-004`（哈希链）、`AD-005`（append-only）、`docs/implementation/dual-system-runbook.md` §4。
> 本文所有命令与响应均在 **2026-09-12** 于 `euripides@192.168.31.152` 实机执行并核对，输出为真实回显（敏感值已省略）。

---

## 0. 审计员的职责边界

**可以做**（`auditor` 角色当前共 30 条权限，全部为只读）：

- 读审计事件：`admin:audit:read`
- 校验哈希链与完整性：`admin:audit:verify`
- 导出审计（异步、需 step-up）：`admin:audit:export`
- 查看登录记录、会话、风险事件：`admin:login-event:read`、`admin:session:read`、`admin:risk:read`
- 查看配置与配置历史：`admin:config:read`、`admin:config:history:read`
- 查看备份/版本/组织/工作区/用户/角色/权限目录/全局任务/导出/系统健康：对应 `*:read`

**明确不能做**（这是配置事实，可复算）：

- 不能执行 Kill Switch（`admin:killswitch:engage/release`）、审批（`admin:approval:decide`）
- 不能修改配置（`admin:config:update`）、Provider（`admin:provider:update`/`admin:provider:key:rotate`）、路由与性能、告警处置、功能开关
- 不能控制任务（`admin:job:control`）、管理保留策略与删除请求、创建/恢复备份
- 不能封存审计完整性边界（`admin:audit:seal` 未授予 auditor）

核对命令（已实测，期望 `0`）：

```sql
SELECT count(*) FROM role_permissions rp JOIN roles r ON r.id = rp.role_id
WHERE r.key='auditor' AND rp.permission_key IN
  ('admin:killswitch:engage','admin:killswitch:release','admin:approval:decide',
   'admin:provider:key:rotate','admin:budget:update','admin:config:update',
   'admin:backup:create','admin:backup:restore','admin:retention:manage',
   'admin:deletion:request','admin:deletion:approve','admin:provider:update',
   'admin:routing:update','admin:performance:update','admin:alert:manage',
   'admin:flag:update','admin:job:control');
```

> 注：该 SQL 原文出自 `backend/migrations/postgres/0053_s6_permission_grants.sql` 的注释；
> 迁移自身的期望值是 0，`auditor` 的 30 条授权全部为读权限。**`admin:audit:export` 是唯一有副作用的权限**（产生导出文件），
> 它属于敏感权限（`sensitive=true`，风险 `critical`）。

---

## 1. 当前环境的真实状态（先读这一节）

本环境存在**已封存的不可恢复审计缺口**，这不是故障，而是 Sprint 6 期间真实发生并已按 ADR-018 如实声明的事实：

| 项 | 值（2026-09-12 实测） |
|---|---|
| 事件编号 | `INC-AUDIT-GAP-1-2343` |
| 链 | `audit` |
| 缺口区间 | seq **1..2343**（2343 条事件） |
| 可信起点 | seq **2344** |
| 封存尾哈希 | `3db79aeb77ce12ae8ccab3540891a7ff241bfc34af8f4a9dcdfd283578686ad1` |
| 来源 | `audit_events_v2.seq=2344.prev_hash`（被销毁的最后一条事件的 `event_hash`） |
| 证据哈希 | `f0ad300fa191cf42fd290343763d012d73291f737bde34a10d085f562ab27967` |
| 检测方式 | `verifier.sequence_gap + prev_hash_of_first_surviving_row` |
| 封存时间 | 2026-09-12 22:37:32 +07（`sealed_at`） |
| 发起/审批 | 请求人 `admin@local`；审批请求 `f72ca54a-554b-438c-9edf-2a403b76254d`（双人审批） |

成因（ADR-018 背景）：AUDIT-003 负向用例执行 `TRUNCATE audit_events_v2` 时，PostgreSQL 的行级触发器
**不覆盖 TRUNCATE**，事件触发器也不支持 `TRUNCATE TABLE`，而应用账号同时是表 owner，`REVOKE` 对 owner 无效。
修复（0054 引入受限运行角色 `blockchain_app` + 0055 完整性边界）已完成，但**已丢失的历史事件不可恢复**——
环境内不存在任何可用恢复源（无 pg_dump/物理基备、`archive_mode=off`、无快照卷、MinIO 审计桶只有每日锚点）。

**因此在本环境里**（2026-09-16 起，含 ADR-019 的稀疏声明）：

- `audit` 链的 `integrity_status` 永远是 `sealed_incident`，**永远不会是 `intact`**；
  此外链上有 **2 条已声明的内容哈希不可复算行**（`declared_anomaly_count=2`，见 §3.4）；
- `login` 链无前缀缺口，但存在 **8 条已声明的历史缺失行**（`declared_lost_count=8`），
  因此其 `integrity_status` 是 `declared_events` 而**不是** `intact`；
- 任何把任一条链描述为"链完整"的结论都是错误的：正确表述是
  **"已声明的事实 + 边界之后逐行复算通过 + 保留锚点可核对"** 三件事同时成立。

---

## 2. 哈希链的校验口径

审计链的算法与字段（实现见 `backend/internal/audit/chain.go`、`writer.go`、`verify.go`；决策见 AD-004）：

1. **哈希载荷必须能由数据库同一行重新推导**。校验器 `chainReaders` 按列重建载荷，
   与写入侧 `chainPayload` 逐字段对应。只把 `detail` 落库或用内存 map 计算哈希的写法会导致链永远校验失败。
2. **时间统一用 `occurred_at_unix_micro`**（UnixMicro 整数），写入前 `Truncate(time.Microsecond)`，
   消除 Go 纳秒与 PostgreSQL 微秒的取整差异。
3. **可空字段统一为空字符串**（而不是 NULL）；`jsonb` 列（`before`/`after`/`changes`/`detail`）读回后重新规范化，保证键排序稳定。
4. **链头串行化**：`audit_chain_heads` 用 `SELECT ... FOR UPDATE` 推进，`seq` 单调递增、不重不漏。
   行锁按事务持有到提交：**同一个 handler 里既要写同事务审计、又要写独立审计，就会与自己的事务互等**
   （2026-09-16 D-15 实测 60 s 卡顿并丢一条审计）。因此提交门请求内的审计写入统一加入请求事务，
   见 `docs/adr/ADR-020-gate-request-audit-joins-transaction.md`；审计连续性不因此改变。
5. **校验同时检测四类问题**：载荷被改写、`prev_hash` 不连续、`seq` 跳跃、链头哈希不匹配。
6. 载荷中**不包含超过 2^53 的整数**（会经 float64 往返丢精度）。

### 2.1 哈希公式（可离线复算）

```text
event_hash = sha256( chain + "|" + decimal(seq) + "|" + prev_hash + "|" + hex(sha256(canonical_payload)) )
```

- 实现：`backend/internal/audit/chain.go` 第 110–123 行；同一公式在
  `backend/migrations/postgres/0042_audit_v2.sql` 第 6 行与归档 manifest 的 `algorithm` 字段中声明。
- `canonical_payload`：先 marshal、再 unmarshal、**按键排序**的紧凑 JSON（`chain.go` 第 33–47、78–99 行）；
  数字用 `FormatFloat(-1, 64)`（第 59–60 行）。
- 链头推进：`SELECT last_seq, last_hash FROM audit_chain_heads WHERE chain=$1 FOR UPDATE` → `seq+1` →
  旧 `last_hash` 作为新行的 `prev_hash`（`chain.go` 第 127–145 行），行锁保证 seq 不重不漏。
- 结构约束：`UNIQUE(chain, seq)`（0042 第 83 行）。
- 完整性边界事件使用**独立链**，域分隔为 `"integrity:" + incident_ref`，并加 `advisory lock`
  `"audit-integrity:" + incident_ref`（`integrity.go` 第 337–364 行）。

两条链：

| 链 | 数据表 | 说明 |
|---|---|---|
| `audit` | `audit_events_v2` | 业务与后台操作审计 |
| `login` | `login_events` | 登录/会话事件 |

---

## 3. `integrity_status` 与 `prefix_status` 的语义（ADR-018）

### 3.1 `integrity_status`（链路是否完整）

| 取值 | 含义 | 与 `valid` 的关系 | 处置 |
|---|---|---|---|
| `intact` | **没有观测到前缀缺口**（不宣称"全链完好"） | 与 `valid` 相互独立：`intact` 时 `valid` 仍可能为 false | 正常，但仍需看 `valid`/`errors` |
| `sealed_incident` | 连续缺口已封存，边界之后可验证 | 通常 true（只描述实际校验区间） | 正常运维态（本环境 `audit` 链即此） |
| `declared_events` | **无缺口**，但存在逐条声明的完整性事实（缺失行 / 内容不可复算行，ADR-019） | true 当且仅当声明的 seq 明细与库内一致 | 正常运维态（本环境 `login` 链即此）；必须同时看 `declared_lost_count` / `declared_anomaly_count` |
| `undeclared_gap` | 观测到缺口但**尚未声明/封存** | false | **不合法状态**，必须按 §6 流程封存或按 §3.4 声明 |
| `boundary_conflict` | 声明边界与实际数据矛盾（疑似回填/删边界） | false | 安全事故，立即上报 |

定义来源：`backend/internal/audit/integrity.go` 第 41–46 行；各分支的赋值位置见
`backend/internal/audit/verify.go` 第 200–291 行。

> **⚠️ 两个必须记住的反直觉点（实现事实，非推测）**
>
> 1. **`intact` 不等于"链完好"**。`prev_hash_mismatch` 与未声明的 `event_hash_mismatch`
>    只让 `valid=false` 并在 `reason`/`broken_at_seq` 中体现，不一定改变 `integrity_status`（当状态仍是 `intact` 时）。
>    因此 **`integrity_status=intact` + `valid=false` 是合法且必须上报的组合**。
>    （2026-09-16 起，序号跳跃本身会直接把状态置为 `undeclared_gap`——见下一条。）
> 2. **稀疏空洞与内容异常分别对应不同的状态**：
>    - 序号跳跃且**未**声明 → `undeclared_gap`（不合法状态；2026-09-16 前的实现会错误地留在 `intact`）；
>    - 已逐条声明 → 继续校验且状态为 `sealed_incident`（同时存在封存缺口时）或 `declared_events`；
>    - 已声明为"缺失"的 seq 上出现行 → `boundary_conflict`（禁止回填）。
> 3. **真篡改的第一信号是 `LinkBreaks` / `internal_gap_count`**：`prev_hash` 链接断裂属于真篡改，
>    全链扫描（`audit.ScanChain`）会把它与"可声明的缺失"分开，声明流程遇到链接断裂直接拒绝。

### 3.2 `prefix_status`（前缀状态）

| 取值 | 含义 |
|---|---|
| `intact` | 无缺口前缀 |
| `sealed_unrecoverable` | 前缀缺口已封存且**不可恢复** |
| `unsealed_gap` | 存在未封存缺口 |

定义来源：`backend/internal/audit/integrity.go` 第 49–53 行。

### 3.3 关键结论：`valid` 不等于"链完整"

`valid` 只描述**实际被校验到的区间**（`verified_from_seq`..`to_seq`）是否自洽；
链路是否完整由 `integrity_status` / `prefix_status` 表达，二者不可互相替代。
封存之后校验器**永不返回 `intact`**——这就是"不把 NOT_VERIFIED 偷换成 PASS"的工程实现。

### 3.4 稀疏声明：`declared_integrity_events`（ADR-019）

ADR-018 处理的是**连续前缀缺口**。链上还有两类事实用它表达不了：

| 事实 | 本环境实例 | 为什么不能"修" |
|---|---|---|
| **稀疏空洞**：缺失的 seq 夹在真实数据行之间 | `login` 链 seq 3791 / 3799 / 5911 / 5914 / 5915 / 5918 / 5919 / 5920 | 回填即伪造证据；重锚即改写审计历史（既有导出哈希与锚点全部失效） |
| **内容哈希不可复算行**：行存在、链接完好，但 `event_hash` 无法由行内容复算 | `audit` 链 seq 52394 / 52399（写入侧曾把不可落库的 UUID 文本放进哈希载荷） | 改写该行 `event_hash` 会连带改写其后全部行，等于丢弃整条链的可检测性 |

处理方式：**逐条声明**（`audit_integrity_declarations`，迁移 0063）：

- 每条声明登记 `seq`、`disposition`（`lost` / `content_anomaly`）与该 seq 在**链上留存的哈希证据**；
  连续缺失段只有段尾那行的哈希被保留（后继行的 `prev_hash`），evidence 里用
  `hash_role: run_tail | run_interior` 如实标注——不制造"每一行都有哈希"的假象；
- 声明走与封存相同的双人审批（`admin:audit:seal` + step-up + `admin:approval:decide` 批准），
  执行时重新扫描并逐条比对 seq 与哈希，任何漂移都中止；声明行对运行角色 append-only；
- 校验器对已声明的 seq **跳过内容复算**，但**继续校验 `prev_hash` 链接**，并回报
  `declared_events` / `declared_lost_count` / `declared_anomaly_count` 与 `integrity_warnings`。

**读取声明时的正确结论写法**：

- ✅ "登录链 8 处历史空洞已逐条声明（`declared_lost_count=8`），其余 6,2xx 行逐行复算通过"；
- ❌ "登录链完整"（错：缺失行确实不存在）；
- ❌ "已声明 = 已修复"（错：声明只记录事实，不改变任何历史行）。

**负向控制（必须成立，否则声明机制就成了放行开关）**：

- 已声明的缺失 seq 上出现行 → `boundary_conflict`；
- 已声明的异常行内容与声明哈希不一致 → `boundary_conflict`；
- 未声明的任何跳号 → `undeclared_gap` 且 `valid=false`；
- `prev_hash` 链接断裂 → 声明流程直接拒绝（那是真篡改，不是事实登记）。

---

## 4. 校验审计链（标准操作）

### 4.1 建立审计员会话（已实测）

在跳板机或 API 所在机器上执行：

```bash
export XDG_RUNTIME_DIR=/run/user/$(id -u)
cd /home/euripides/projects/blockchain-platform
set -a; . deploy/.env; set +a

JAR=/tmp/auditor-jar.txt; rm -f $JAR
read -rsp "审计员密码: " PW; echo
curl -sS -c $JAR -b $JAR -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' \
  -X POST -d "{\"identifier\":\"fixture-auditor@fixture.local\",\"password\":\"$PW\"}" \
  http://127.0.0.1:8080/api/auth/v1/login

# 取 CSRF token（写操作/校验 POST 需要）
CSRF=$(curl -sS -b $JAR -c $JAR http://127.0.0.1:8080/api/auth/v1/session \
  | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["csrf_token"])')
echo "csrf_len=${#CSRF}"
```

实测要点：

- 必须带 `X-Auth-Audience: admin`，否则会话受众为 `user`，调用 `/api/admin/v1/*` 会被 `policy.Engine.Authorize`
  以 `audience_mismatch` 拒绝（403）。
- 登录成功返回 `{"state":"authenticated","audience":"admin","roles":["auditor"],...}`，
  并设置 host-only 会话 Cookie（响应中**不返回** Session Token）。
- `/api/auth/v1/session` 返回的 `permissions` 数组即该账号的生效权限，可直接与 §0 对照。

### 4.2 读链头

```bash
curl -sS -b $JAR http://127.0.0.1:8080/api/admin/v1/audit/chains
```

实测响应（2026-09-12）：

```json
{"success":true,"data":{"chains":[
  {"chain":"audit","last_seq":3586,"last_hash":"438db234e35c4a7742e716f5dc950ee82d94db29ba58d1459ef111af89187601"},
  {"chain":"login","last_seq":2299,"last_hash":"2407a5d22f75eec21726aa51f2de9de2b86d1ce3478c4ba931e6526c54f93b00"}]},
 "meta":{"request_id":"...","trace_id":"...","server_time":"..."},"error":null}
```

该端点权限键是 `admin:audit:verify`（`backend/internal/api/adminv1.go:49`）。

### 4.2.1 `reason` 的全部取值（`valid=false` 时的定位依据）

| `reason` | 含义 | 处置 |
|---|---|---|
| `empty_range` | 请求区间没有事件（`valid` 仍为 true） | 调整 `from_seq`/`to_seq` |
| `sequence_gap` | 序号跳跃（链不连续） | 真篡改或缺口；用 `internal_gap_count` 区分 |
| `prev_hash_mismatch` | `prev_hash` 与前一行 `event_hash` 不衔接 | 疑似删行/换行，立即上报 |
| `event_hash_mismatch` | 行内字段被改写（载荷复算不一致） | 确凿的篡改证据，走安全事故流程 |
| `sealed_gap_contains_rows` | 已封存缺口区间内出现行 | `boundary_conflict`，疑似回填 |
| `trusted_start_row_missing` | 可信起点行缺失 | `boundary_conflict` |
| `sealed_tail_hash_mismatch` | 幸存首行 `prev_hash` ≠ `sealed_tail_hash` | `boundary_conflict` |

注意（实现细节，影响判读）：在 `boundary_conflict` 提前返回时，
`verified_from_seq` 仍为请求的 `from_seq`（默认 1），真正异常位置在 `broken_at_seq`；
且此时 `prefix_status` 仍为 `sealed_unrecoverable`（不因冲突而改变）。

### 4.3 执行全链校验

```bash
curl -sS -b $JAR -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json' \
  -X POST -d '{"chain":"audit"}' http://127.0.0.1:8080/api/admin/v1/audit/verify
```

实测响应（2026-09-12，`audit` 链）：

```json
{"success":true,"data":{
  "chain":"audit","from_seq":1,"to_seq":3588,"checked":1245,
  "valid":true,"broken_at_seq":null,"reason":"",
  "head_hash":"b879f31b3e834a4cc689eebf75e71ddd4119779be3b39a3164aad2c83e6e3f79",
  "errors":[],
  "integrity_status":"sealed_incident",
  "prefix_status":"sealed_unrecoverable",
  "verified_from_seq":2344,
  "sealed_boundary":{"incident_ref":"INC-AUDIT-GAP-1-2343","chain":"audit",
    "gap_from_seq":1,"gap_to_seq":2343,"gap_event_count":2343,"trusted_from_seq":2344,
    "sealed_tail_hash":"3db79aeb77ce12ae8ccab3540891a7ff241bfc34af8f4a9dcdfd283578686ad1",
    "evidence_hash":"f0ad300fa191cf42fd290343763d012d73291f737bde34a10d085f562ab27967",
    "sealed_at":"2026-09-12T22:37:32.985638+07:00",
    "statement":"链 audit 的 seq 1..2343 已封存为不可恢复缺口（2343 条事件）；..."},
  "integrity_warnings":["请求区间自 seq 1 起，但 seq 1..2343 已封存为不可恢复缺口，实际校验自 seq 2344 起"]},
 "meta":{...},"error":null}
```

**正确的结论写法**（三件事同时成立才算通过）：

1. 缺口被**如实声明**（`integrity_status=sealed_incident` 且 `sealed_boundary` 字段完整）；
2. 边界之后**逐行验证通过**（`valid=true`、`verified_from_seq=2344`、`errors=[]`）；
3. 保留锚点**原样可核对**（见 §5）。

**错误的结论写法**：把 `valid=true` 单独表述为"审计链完整"；把 `sealed_incident` 描述为"链完整"。

### 4.4 离线校验（不依赖 HTTP）

```bash
./backend/bin/auditverify
```

实测输出（2026-09-12，节选）：

```json
{
  "checked_at": "2026-09-12T16:36:58.296310053Z",
  "results": [
    {"chain":"audit","from_seq":1,"to_seq":3438,"checked":1095,"valid":true,
     "broken_at_seq":null,"reason":"","head_hash":"b674a519...","errors":[],
     "integrity_status":"sealed_incident","prefix_status":"sealed_unrecoverable",
     "verified_from_seq":2344,"sealed_boundary":{...,"incident_ref":"INC-AUDIT-GAP-1-2343"},
     "integrity_warnings":["请求区间自 seq 1 起，但 seq 1..2343 已封存为不可恢复缺口，实际校验自 seq 2344 起"]},
    {"chain":"login","from_seq":1,"to_seq":2251,"checked":2251,"valid":true,
     "broken_at_seq":null,"reason":"","head_hash":"43f79b45...","errors":[],
     "integrity_status":"intact","prefix_status":"intact","verified_from_seq":1,
     "integrity_warnings":[]}
  ],
  "ok": true
}
```

用法要点（`backend/cmd/auditverify/main.go`）：

- 默认同时校验 `audit` 与 `login` 两条链；可用 `-chain`、`-from`、`-to`、`-timeout` 限定。
- 输出 `{checked_at, results[], ok}`，`ok` 为顶层结论。
- **退出码语义**（脚本化巡检必须按此判断）：
  - `0`：`valid=true` **且** `integrity_status ∈ {intact, sealed_incident}`；
  - `1`：校验不通过（含 `valid=false` 或不受支持的 `integrity_status`）；
  - `2`：参数或数据库连接错误。
- 它读取数据库配置，需在 `backend/` 目录下、能读取 `deploy/.env` 的环境中执行。

### 4.5 完整性观测（缺口探测）

```bash
curl -sS -b $JAR "http://127.0.0.1:8080/api/admin/v1/audit/integrity?chain=audit"
curl -sS -b $JAR "http://127.0.0.1:8080/api/admin/v1/audit/integrity/incidents"
```

实测 `data.observed` 关键字段（2026-09-12）：

```json
{"chain":"audit","table":"audit_events_v2","has_prefix_gap":true,
 "gap_from_seq":1,"gap_to_seq":2343,"gap_event_count":2343,"first_surviving_seq":2344,
 "sealed_tail_hash":"3db79aeb...","head_seq":3586,"head_hash":"438db234...",
 "row_count":1243,"internal_gap_count":0,"rows_inside_sealed_gap":0,"sealed":true,
 "incident_ref":"INC-AUDIT-GAP-1-2343"}
```

判读规则：

- `internal_gap_count > 0`：**幸存区间内部**存在断层，属于真正的篡改，不允许用边界掩盖
  （代码返回 `ErrInternalGap = internal_gap_in_surviving_segment`）。
- `rows_inside_sealed_gap > 0`：已封存缺口区间内出现了行 → `boundary_conflict`（疑似回填）。
- `sealed=false` 且 `has_prefix_gap=true` → `undeclared_gap`（不合法状态，走 §6 封存流程）。

---

## 5. 锚点（每日归档）校验

```bash
curl -sS -b $JAR "http://127.0.0.1:8080/api/admin/v1/audit/anchors?chain=audit"
curl -sS -b $JAR "http://127.0.0.1:8080/api/admin/v1/audit/anchors/audit/2026-09-11/object"
```

实测（2026-09-12，节选）：

```json
{"all_accounted":true,"items":[{
  "chain":"audit","anchor_date":"2026-09-11","from_seq":1,"to_seq":92,
  "head_hash":"ff36d308...","event_count":92,
  "object_ref":"audit/audit/2026-09-11/anchor.json",
  "content_hash":"ccfd576e...",
  "disposition":"sealed_unrecoverable","verified":false,"checked":0,
  "reason":"锚点区间 [1,92] 完全落在已封存缺口 [1,2343] 内：事件行不可恢复，head_hash 无法再用事件行复算；锚点行与 MinIO 归档对象原样保留（归档一致性=true）",
  "archive_object_hash":"ccfd576e..."}]}
```

### 5.1 `disposition`（锚点处置结论）取值

| 取值 | 含义 |
|---|---|
| `verified` | 锚点区间可复算且与记录一致（规范派生） |
| `verified_legacy_derivation` | 内容一致，但 `head_hash` 使用 0055 之前的派生方式（区间内 `max(event_hash)`），承诺强度弱于规范语义，**不并入普通 verified 计数** |
| `sealed_unrecoverable` | 锚点区间落在已封存缺口内，**无法复算**（本环境 2026-09-11 锚点即此） |
| `spans_boundary` | 锚点区间跨越封存边界 |
| `broken` | 锚点与数据不一致（异常，需上报） |

锚点条目还会返回 `verified` / `legacy_derivation` / `sealed_unrecoverable` / `unexplained` /
`all_accounted` 汇总计数与 `archive_object_hash`（MinIO 归档对象的实际哈希）。
`GET /api/admin/v1/audit/anchors/{chain}/{date}/object` 会回读归档对象并返回
`object_sha256`、`recorded_content_hash`、`intact`、`manifest`，用于证明保留证据没有被替换。

规范派生为 **区间末行的 `event_hash`**（`chain_head`）；历史派生为 `legacy_max_hash`。
定义来源：`backend/internal/audit/integrity.go` 第 55–70 行。

### 5.2 硬性约束

- **不得修改或删除 `audit_chain_anchors` 既有行**（0055 起由数据库触发器 + 运行角色权限拒绝）。
- 归档器已从 `ON CONFLICT DO UPDATE` 改为 `ON CONFLICT DO NOTHING` + 回读复核：不重写、不重传归档对象。
- 校验侧会重新读取 MinIO 对象与其 `content_hash` 比对，证明保留证据没有被替换
  （响应中的 `archive_object_hash` 与 `content_hash`）。
- 生成新锚点需要 `admin:audit:seal`（**auditor 没有该权限**），因此审计员只能核对、不能生成。

---

## 6. 缺口封存流程（仅供理解；审计员无执行权限）

当观测到 `integrity_status=undeclared_gap` 时，按以下顺序处理（原文见 `docs/implementation/dual-system-runbook.md` §4.1）：

1. **只读检测**：`POST /api/admin/v1/audit/integrity/preview`（权限 `admin:audit:verify`）——
   确认缺口区间、幸存起点与封存尾哈希，**不写任何行**。
2. **发起封存**：`POST /api/admin/v1/audit/integrity/incidents/requests`
   （权限 `admin:audit:seal`，风险 `critical`，需 `step_up_password` + 原因）→ 生成待审批请求。
3. **第二人审批**：`POST .../requests/{id}/approve`（权限 `admin:approval:decide`，**请求人不能自审批**，
   `approval_requests` 有 `CHECK (decided_by IS NULL OR decided_by <> requested_by)` 兜底）。
4. **发起人执行**：`POST .../requests/{id}/execute`（再次 step-up）。
5. **生成并验证锚点**：`POST /api/admin/v1/audit/anchors`，再逐项核对
   `GET /api/admin/v1/audit/anchors` 与 `GET /api/admin/v1/audit/integrity`。

禁止：回填或伪造缺口区间内的 `audit_events_v2` 行（校验器会判 `boundary_conflict`）；
修改或删除锚点；把封存后的 `integrity_status` 表述为 `intact`。

---

## 7. 审计导出

### 7.1 真实行为（实测）

```bash
# 未填原因 → 400（先校验原因，再校验 step-up，无副作用）
curl -sS -b $JAR -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json' \
  -X POST -d '{}' http://127.0.0.1:8080/api/admin/v1/audit/export
```

实测响应：

```json
{"success":false,"data":null,
 "meta":{"request_id":"480420b1-...","trace_id":"656c2cf5-...","server_time":"...","next_cursor":null},
 "error":{"code":"VALIDATION_INVALID_ARGUMENT","message":"必须填写导出原因",
   "field_errors":[{"field":"reason","code":"required","message":"原因必填"}],"retryable":false}}
```

正式导出请求体字段（实现 `backend/internal/api/sprint6_governance.go` 的 `handleAdminAuditExport`）：

| 字段 | 类型 | 说明 |
|---|---|---|
| `format` | string | `jsonl`（默认）或 `csv`；其他值 → 400 |
| `chain` | string | 默认 `audit` |
| `from_seq` / `to_seq` | int64 | 区间过滤 |
| `actor_email` / `action` / `category` / `result` | string | 条件过滤 |
| `from` / `to` | string | 时间范围 |
| `limit` | int | 上限受配置项 `audit.export.max_events` 约束（默认 200000） |
| `reason` | string | **必填**，写入审计 |
| `step_up_password` | string | **必填**（当前账号密码重认证） |

关键事实：

- 导出是**异步任务**：先写 `audit_exports`（`status='running'`，记录 `requested_by`/`requested_by_email`/
  `filters`），再生成文件到对象存储。
- 权限键 `admin:audit:export`（风险 `critical`、`sensitive=true`），**审计员持有该权限**。
- `step_up_password` 是**密码重认证**，不是 MFA。MFA/TOTP 已于 2026-09-16 实现（AUTH-004）：
  账号自行注册认证器后，登录必须通过第二步；但 step-up 与 MFA 是两件事，不得互相替代或混称。
- 导出必须记录导出人、条件、事件范围与文件哈希（§19.8）。

### 7.2 查询与获取导出

```bash
curl -sS -b $JAR http://127.0.0.1:8080/api/admin/v1/audit/exports              # 列表（admin:audit:export）
curl -sS -b $JAR -o audit-export.bin \
  http://127.0.0.1:8080/api/admin/v1/audit/exports/<id>/download               # 下载（admin:audit:export）
```

### 7.3 校验下载结果（审计员必做）

```bash
# 1) 文件哈希与导出记录中的 sha256 比对
sha256sum audit-export.bin

# 2) JSONL：逐行解析并核对条数与 seq 区间
wc -l audit-export.bin
head -1 audit-export.bin | python3 -m json.tool | head -30

# 3) CSV：核对表头与行数，注意导出字段的转义策略（原值保留，防 CSV 注入）
head -1 audit-export.bin; wc -l audit-export.bin
```

### 7.4 导出产物与 manifest（实现事实）

| 项 | 值 |
|---|---|
| 查询范围 | `audit_events_v2`，`ORDER BY seq ASC LIMIT` |
| 对象路径 | `audit-exports/YYYY/MM/DD/<id>.<fmt>`，同目录另有 `<id>.manifest.json` |
| manifest 字段 | `export_id, generated_at, build_sha, format, content_type, filters, event_count, from_seq, to_seq, first_event_at, last_event_at, object_ref, size_bytes, sha256, chain, requested_by, request_id, trace_id, requested_reason, encoding, line_delimited, secret_policy` |
| 落库 | `audit_exports`（含 `sha256`、`manifest`），`expires_at = now() + 30 days` |
| JSONL 行键 | `chain, seq, occurred_at, actor_type, actor_id, actor_email, organization_id, workspace_id, action, category, resource_type, resource_id, result, permission_key, policy_decision, risk_level, sensitive, trace_id, request_id, route_template, prev_hash, event_hash, client_ip`，另有 `before/after/changes/detail` |
| CSV | 列头**不含** `before/after/changes/detail` |

**下载端点的哈希校验**：`GET /api/admin/v1/audit/exports/{id}/download` 会回读对象并在响应头返回
`X-Checksum-Sha256`，可直接与本地 `sha256sum` 比对：

```bash
curl -sS -D headers.txt -b $JAR -o audit-export.jsonl \
  http://127.0.0.1:8080/api/admin/v1/audit/exports/<id>/download
grep -i '^x-checksum-sha256' headers.txt
sha256sum audit-export.jsonl
```

> 票据类下载（导出/报告）为 256-bit 随机值、只保存摘要、默认 60 秒过期、单次使用、绑定 user/export/resource；
> 下载审计在**票据创建、首次使用、失败、过期**时分别记录（§21.7）。

---

## 8. append-only 与只读边界的技术保证

### 8.1 数据库触发器（AD-005 `0043`）

| 对象 | 拒绝的操作 | 真实报错（AD-005 实测记录） |
|---|---|---|
| `audit_events_v2` | UPDATE / DELETE | `audit table audit_events_v2 is append-only` |
| `login_events` | UPDATE / DELETE | `audit table login_events is append-only` |
| `audit_chain_heads` | DELETE；`last_seq` 回退；同序号换哈希 | `last_seq must not decrease (2 -> 0)` / `head hash must not be replaced` |

维护逃生舱：事务级开关 `SET LOCAL app.audit_maintenance = 'on'`，只能由持有数据库凭据的受控流程设置，
且必须走 `admin:deletion:approve` 审批。

> **为什么还需要运行角色**：PostgreSQL 的行级触发器**不覆盖 TRUNCATE**，事件触发器也不支持
> `TRUNCATE TABLE`，而应用连接账号是表 owner（`REVOKE` 对 owner 无效）。
> 因此 0054 引入了受限运行角色 `blockchain_app`，应用连接建立后 `SET ROLE`。

### 8.2 运行期降权（ADR-017 §5，必须保持开启）

```bash
grep '^DB_RUNTIME_ROLE=' deploy/.env
# 实测：DB_RUNTIME_ROLE=blockchain_app

docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 \
  psql -U blockchain -d blockchain_platform -Atc \
  "select has_table_privilege('blockchain_app','audit_events_v2','TRUNCATE')"
# 期望：f（false）
```

`blockchain_app` 允许 `SELECT`/`INSERT`（必要表允许 `UPDATE`）、业务表允许 `DELETE`；
**拒绝**审计事件表的 `UPDATE/DELETE`、全部表的 `TRUNCATE`、审计表的 `DROP`。
未设置该变量时启动日志会打 WARN（`postgres ready without runtime role separation`），且 AUDIT-003 不再成立。

### 8.3 后端权限边界

- 后台权限（`admin:` 前缀）额外要求会话 `audience == "admin"`；用户层会话无法调用后台 API。
- 每次 401/403 都会异步写一条 `authorization.denied` 审计（含 permission、deny_reason、risk_level、sensitive）。

---

## 9. 日常审计工作清单

**每日**

- [ ] `GET /api/admin/v1/audit/integrity?chain=audit`：确认 `rows_inside_sealed_gap=0`、`internal_gap_count=0`、`sealed=true`。
- [ ] `POST /api/admin/v1/audit/verify` 两条链（`audit`、`login`）：`errors` 为空、`login` 链 `integrity_status=intact`。
- [ ] `GET /api/admin/v1/audit/anchors`：`all_accounted=true`；新增锚点的 `disposition` 不是 `broken`。
- [ ] 登录异常：`GET /api/admin/v1/login-events`，关注失败激增与新设备。
- [ ] 敏感读取：`GET /api/admin/v1/audit/events` 过滤 `sensitive` 相关动作（`evidence:sensitive:read`、`admin:apikey:read` 等）。

**每周**

- [ ] `GET /api/admin/v1/audit/exports`：确认无异常导出（导出人、条件、范围、哈希齐全）。
- [ ] `admin:deletion:*` 与保留策略变更：核对审批链（`GET /api/admin/v1/approvals`）。
- [ ] 复核 `authorization.denied` 事件，确认无越权探测。

**每月**

- [ ] 执行一次导出并离线校验哈希与行数（§7.3），归档校验记录。
- [ ] 对照 `docs/implementation/audit-coverage.md` 复核覆盖矩阵是否有新增缺口。
- [ ] 确认 `DB_RUNTIME_ROLE` 与 append-only 触发器仍然生效（§8）。

---

## 10. 结论与报告口径（必须遵守）

- `valid=true` 只能表述为"**实际校验区间（`verified_from_seq`..`to_seq`）自洽**"。
- 存在封存缺口时，结论必须同时写明：缺口区间、事件数、可信起点、封存尾哈希、锚点核对结果。
- 未执行/无法复算的项标 `NOT_VERIFIED`；未实现标 `NOT_IMPLEMENTED`；**不得用"接口返回 200"或页面存在作为通过证据**（§32 约束 10）。
- 本环境的 `audit` 链**永久不是 `intact`**；任何把它写成"完整"的报告都是错误的。

---

## 11. 待确认项

1. `admin:audit:export` 的导出文件在对象存储中的保留策略与生命周期未在本文中逐一验证，需以保留策略页面/配置为准。
2. `GET /api/admin/v1/audit/export`（`adminv1.go:51` 注册的旧 501 桩）与
   `POST /api/admin/v1/audit/export`（`sprint6_governance.go:64` 的真实实现）**同名同方法注册两次**；
   实测 `POST` 返回的是真实实现的 400 校验错误（不是 501），说明后注册的实现生效。
   旧桩为死代码，建议清理（详见 `troubleshooting.md` 的"已知缺陷"）。
3. ~~MFA/TOTP 未实现~~ **2026-09-16 已实现并上线**。本部署 `AUTH_REQUIRE_ADMIN_MFA=false`，
   因此审计员账号登录**不强制** MFA；一旦该账号自行注册认证器，其登录必须通过第二步
   （`/api/auth/v1/login` 返回 202 + challenge_id，再由 `/api/auth/v1/mfa/verify` 收尾）。
   复核口径：挑战单次使用且 5 分钟过期、提交次数上限 5、恢复码一次性、未注册身份不受影响。
4. 审计事件的 UI 侧筛选字段（`/admin/audit` 页面）与 API 查询参数的一一对应关系以页面实现为准。
