# P0 旧兼容面修复 —— 部署与隔离验收状态（2026-09-13，UTC+7 04:0x / 2026-09-12T21:0xZ）

> 作者：接手执行 Agent。**性质：局部已证明 + 明确未闭合清单。整体判定仍为 NO-GO。**
>
> **对本文件早前一次表述的更正**：我此前写过「P0 IS CLOSED」，**该表述过宽，已作废**。
> 正确表述是：**匿名泄露路径已关闭；真实跨组织对象隔离已用正负向对照证明；
> 但完整 P0（多租户/权限/受众全维度）尚未闭合**，理由见 §4。

---

## 1. 部署身份（真实、可复核）

| 项 | 值 |
|---|---|
| 源码快照 | `/home/euripides/p0-snapshot-20260912T2045Z`（37 个 .go + go.mod/go.sum，逐文件 sha256 校验 37/37 一致） |
| 快照清单哈希 | `/tmp/snap-final.sha` → sha256 `bffbb4142df0c6939881897525a2834b8378e14db9af8a5d529759eaaf924496`（39 行） |
| 构建（VM 本机 go1.26.7） | `go build ./...` **rc=0**；`go vet ./...` **rc=0** |
| 测试 | P0 专项 `go test -run 'TestLegacyV1|TestTenantScope|TestWritableWorkspace' ./internal/api/...` **rc=0**；受影响 10 包 **rc=0** |
| 部署 api | `backend/bin/api` = `164f33dae3bbe2e4250b7f1663646cf95bbbb309313d7b8ac91b2ec109a89235` |
| 部署 worker | `backend/bin/worker` = `6fc03e10f216d5125575417958f313e358dd5af09797185bc61c97e8e63105c2` |
| 部署 migrate | `backend/bin/migrate` = `ab10efe36791e49ea820fb7b293c6aa4104bf93788dacacc87f7f7895ca19b4f` |
| **运行中进程** | `/proc/<api MainPID>/exe` 与 `/proc/<worker MainPID>/exe` 的 sha256 **等于上表**（不只是磁盘文件） |
| ExecStart | `…/backend/bin/api`、`…/backend/bin/worker`（未变） |
| 健康 | `/health/ready` 200，checks postgres/clickhouse/minio/nats 全 ok |
| 回滚点 | `bin/api.bak-p0-20260912T2050Z`（旧 `d221dfef…`）、`bin/worker.bak-p0-20260912T2050Z`（旧 `c3fe5898…`）、`bin/migrate.bak-p0-…`；源码备份 `/home/euripides/p0-prev-backup-20260912T2048Z`（37 文件） |

**Windows↔VM 差异审计（部署前完成，未盲目覆盖）**：
- `backend/internal/config/config.go`：**去注释去空白后代码完全一致**（差异仅 gofmt 对齐）→ 无安全策略差异
- `backend/internal/iam/session.go`：**去注释去空白后代码完全一致**（差异仅一行空注释 `//`）→ **Cookie 安全策略无差异**
- `go.mod`：Windows 把 `prometheus/client_model` 列为 direct、VM 列为 indirect（版本号相同；`sprint6_obs.go` 直接 import 它，即 Windows 分类更准）。
  **决定：保留 VM 的 go.mod 不覆盖**（避免改动依赖语义），仅覆盖 37 个 .go；`go.sum` 两侧一致。

---

## 2. 已证明（可复现，49 PASS / 0 FAIL / 0 BLOCKED）

脚本：`scripts/s7/p0-legacy-v1-negative-tests.sh`（sha256 `$(sha256sum scripts/s7/p0-legacy-v1-negative-tests.sh 2>/dev/null | cut -d' ' -f1)`）
证据：`acceptance-evidence/dual-system-v1/sprint7/p0-legacy-v1/result-20260912T205743Z.json`（含 `scope` 与 `not_covered` 字段）

### 2.1 匿名泄露路径已关闭
21 条旧读路由（含修复前返回 200 的 9 条）→ **401**；未认证响应体无 `organization_id`/`workspace_id`（224 B）。
公开白名单保持 **200**：`/api/v1/me`、`/api/v1/capabilities`、`/api/v1/chains/capabilities`、`/api/v1/methods/search`、`/openapi.json`、`/health/ready`。
伪造 `X-User-*` 在 `/api/v1`、`/api/v2`、`/api/admin/v1` 均 **401**（不提权）。

### 2.2 受众（audience）确实生效
- `admin@local` + `X-Auth-Audience: admin` 读 `/api/v1/api-control/overview` → **200**（证明修复未误伤后台页面）
- 同一后台会话改带 `user` 受众 → **401**

### 2.3 真实跨组织对象隔离（**不是"不存在 ID 404"**）
夹具为库中**真实存在且带 organization_id** 的对象，并在测试前落盘取证（`tenant-fixtures-*.json`）：
- owner(`default`) 读**本组织**真实 case → **200**；读 **acme 真实 case** → **404**；读 **acme 真实 investigation** → **404**
- viewer(`acme`) 读**本组织**真实 case → **200**（证明 404 不是能力缺失）；读 **default 真实 case** → **404**
- investigator(`acme`) 读**本组织**真实 investigation → **200**；读 **default 真实 case** → **404**

### 2.4 权限拒绝（非 CSRF、非超级 admin 冒称）
- viewer 创建案件 → **403**，业务码 `permission_denied`（不是 `csrf_failed`：已先取 `cs_user_session_csrf` 并随请求携带）
- **auditor 角色实测不含 `evidence:read`（count=0）也不含 `evidence:sensitive:read`（count=0）**，其读**真实 evidence 正文** → **403** `permission_denied`；权限集合已落盘 `role-permissions-*.json`

### 2.5 拒绝事件审计绑定
最近 `authorization.denied` 事件**同时带 `request_id` 与 `trace_id`**（从 `audit_events_v2` 实查）。

### 2.6 GPT Actions 独立契约未被误伤
无 Key → 401；伪造 Key → 401；**环境主 Key 正向 → 200**（说明 session 规则没有波及 Actions API Key 面）。

---

## 3. 相关但独立的一项：S7-DR 机器门禁（已完成并自测）

- `scripts/s7/dr-gate.sh`：**对计划要求范围内的任何 NOT_VERIFIED（structural/check）与任何 PARTIAL 一律阻断，无豁免开关**；
  另校验 PITR/RPO≤900s、RTO≤14400s、5 个桶全覆盖、MinIO 四维度、消费者幂等真实维度表。
- `scripts/s7/dr-gate-selftest.sh`：8 个合成用例在**真实 Linux** 上全部符合预期（该阻断的阻断、全闭合才 PASS）。
- 对真实证据目录运行 → **BLOCKED（5 条）**：无 restore/kill9 的机器可读结果、无 PITR、无 MinIO 元数据比对、无消费者幂等维度。
- `scripts/backup/minio-metadata-verify.sh`（v2）：修掉 v1 的 8 类缺陷（含"无对象比较的假 PASS"）；
  v1 的假 PASS 产物**原样保留**于 `sprint7/dr/v1-false-pass/`，勘误见 `sprint7/dr/ERRATA-minio-metadata-v1-false-pass.md`。
  实测并**检出真实缺口**：旧演练桶 `blockchain-reports-drill-20260912t175029z` 相对备份点**缺 8 个对象**（旧报告的"60/60 通过"不是全键集合比对）。

---

## 4. **未闭合**清单（不得据此宣称 P0=0 或可验收）

| # | 未闭合项 | 证据/位置 |
|---|---|---|
| R-1 | **受限 GPT Actions key 的跨组织负向未验证**：`gpt_action_api_keys` 实测 **0 行**，无受限 key 可测（仅环境主 Key） | 需创建受限 scope 测试 key（夹具动作）后补测 RBAC-008 |
| R-2 | **调查 runtime 仍走 dataset 默认组织回退**：`dataset.Ensure`/`ReuseCandidateLocal` 的 `DefaultOrgWorkspace` 仅由非 HTTP 的 `investigation.Runtime` 触发 → HTTP 之外的跨租户**写**路径未收敛 | P0 文档 §7.4；需把租户作用域并入运行时 |
| R-3 | **前端受众适配未做**：旧 V1 前端（nginx `location /` 的 `frontend/src`）不发 `X-Auth-Audience: admin`，其 api-control/admin 页面在带 admin 受众前会 401；**桌面/移动真实浏览器回归未跑** | 新客户端 `frontend/packages/api-client/client.ts:115` 已发；旧客户端未发 |
| R-4 | **无租户列的表**：`dataset_coverage`、`provider_usage`、`api_control_*`、`ankr_benchmark_runs` 只能靠路由层权限收敛；共享公共链数据 vs 租户配额/账号/诊断的划分**未按计划明确定义并实现** | P0 文档 §7.5；需 ADR + 加法迁移 |
| R-5 | **公开白名单中的本地数据探针**：`GET /api/v1/transactions/{chain}/{tx_hash}` 查的是**本地数据集**，可用于判断平台是否已入库该交易（存在性探针）；未确认其是否回显租户信息 | P0 文档 §7.2 |
| R-6 | **Actions 环境主 Key 跳过 scopes 校验**：未按计划界定"显式系统 key"与"受限 key"的权限边界 | P0 文档 §7.6/§3.2 |
| R-7 | P0 代理的 8 个测试**不含真实 DB**；本轮已补真实 DB 负向，但覆盖面止于上述 49 条 | 本文件 §2 |

**结论**：R-1..R-7 任一未闭合，**P0 即未关闭、不可宣称系统 P0=0**。当前整体发布判定：**NO-GO**（另有 §27 G-1..G-12、§31 19 条未闭合、S7-DR gate BLOCKED）。

---

## 5. 本轮续做：R-1 / R-2 已闭合，R-3 已实测并**保持未闭合**

### 5.1 R-2 已闭合（非 HTTP 路径的租户不变量）

**发现的真实缺陷（比原 P0 文档所述更宽）**：不止调查运行时——`backend/internal/update/service.go` 的 `Confirm` 里
**有三处 `DS.Ensure(...)` 完全没有传租户作用域**（第 589/647/660 行），而更早的第 535 行
`_, wsID, err := s.Store.UpdatePreviewScope(...)` **把 organization ID 用 `_` 丢弃了**。
这些调用会顺着 `dataset.Ensure` 的回退落到**默认组织** → 增量更新触发的下载作业落错租户。

**修复（4 处，全部 fail closed）**：

| 位置 | 改动 |
|---|---|
| `update/service.go` `Confirm` | 取回 `orgID`；`orgID=="" \|\| wsID==""` 直接报错；三处 `Ensure` 注入 `OrganizationID: orgID, WorkspaceID: wsID` |
| `dataset/service.go` `Ensure` | **删除** `Store.DefaultOrgWorkspace` 回退；org/ws 任一为空 → `tenant scope required` 错误 |
| `dataset/service.go` `ReuseCandidateLocal` | 同上 |
| `investigation/runtime.go` `Start` | 调查对象缺 `organization_id` 或 `workspace_id` → 直接报错，拒绝以默认组织启动下载 |

**证据**：
- 新增 `backend/internal/dataset/scope_tenant_test.go`：`TestEnsureRequiresTenantScope`（5 个缺失/空白组合全部 fail closed）、
  `TestReuseCandidateLocalRequiresTenantScope`、`TestDatasetPackageDoesNotFallBackToDefaultOrg`（静态：`service.go` 代码行不得再出现 `DefaultOrgWorkspace(`）
  → **三测全 PASS**
- VM 本机 `go build ./...` **rc=0**、`go vet ./...` **rc=0**、受影响 6 包 `go test` **rc=0**
- 部署后 api `e0a7c3bd1983cc18da85bd5e1a2e3712b9dde43bb67d48ee5e4722f53368abb7`、
  worker `c227acacffed93400cbcad3dfaa9807e544f2760be4ae7c7c287d7574324d749`，
  `/proc/<PID>/exe` 与之一致；回滚点 `api.bak-r2-20260912T2105Z` / `worker.bak-r2-…`
- 回归：P0 隔离套件复跑仍 **49 PASS / 0 FAIL / 0 BLOCKED**（HTTP 路径未被 fail-closed 误伤）

### 5.2 R-1 已闭合（受限 GPT Actions key 的 scope 与跨组织）

`gpt_action_api_keys` 原为 0 行，无法验证受限 key。本轮**创建一次性测试 key 作为夹具**（用完 **disable、不删除**，保留审计）：

| 用例 | 结果 |
|---|---|
| `read` scope key → read 范围端点 `/api/v1/address/overview` | **200** |
| `read` scope key → export 范围端点 `/api/v1/export/status` | **401**（scope 隔离生效） |
| `read` scope key → 后台 API `/api/admin/v1/jobs` | **401** |
| `export` scope key → **他组织（org=acme）真实导出** `job_id=7286a67b-…` | **404**（收窄生效、不泄露存在性） |
| `export` scope key → 不存在导出 UUID | **404** |

**5 PASS / 0 FAIL / 0 BLOCKED**；脚本 `scripts/s7/p0-actions-scope-tests.sh`，结果 `actions-scope-result-20260912T210736Z.json`。

### 5.3 R-3 **仍开放**（旧 V1 前端用户可见功能失败 —— 实测，不是推测）

旧前端 `frontend/src/api/client.ts` 的 `http()`（第 90–91 行）**只发 `Content-Type`，不发 `X-Auth-Audience`**；
`frontend/src/features/api-control/ApiControlPage.tsx` 与 `frontend/src/pages/SystemPage.tsx` 走 `/api-control/*`。
且 `frontend/src` 中**没有任何登录代码**（`grep audience|login` 无命中）→ 旧应用根本不会建立 admin 受众会话。

**实测（admin@local 以 user 受众登录，模拟旧前端）**：

| 请求 | 结果 |
|---|---|
| 旧前端式登录（`audience=user`） | 200（拿到 **user** 会话） |
| 不带 audience 头调 `/api/v1/api-control/overview` | **403** |
| 同会话补 `X-Auth-Audience: user` | **403** |
| 同会话补 `X-Auth-Audience: admin` | **401**（该 jar 没有 admin 会话） |

**结论：旧页面的 api-control / system 页现在是真实用户可见的功能失败（403）。**
这不是"没有回归"，而是 P0 收紧后的**必然结果**，且**必须**由下列二者之一收口，否则 R-3 保持开放：
1. 给旧客户端加受众感知（`http()` 按路径带 `X-Auth-Audience`）**并且**提供 admin 受众登录路径（旧 src 目前完全没有登录）；
2. 正式把旧 api-control/system 页列入下线范围，并证明新后台 `/admin/` 已覆盖同等功能（需真实浏览器回归）。

在 (1) 或 (2) 完成前，旧入口**不得**按"旧页只读/可用"描述。

### 5.4 仍未闭合（延续）

R-4（无租户列表的共享/租户数据划分）、R-5（transactions 公开路由为本地数据存在性探针）、
R-6（Actions 环境主 Key 跳过 scopes；本轮只证明**受限 key** 的 scope 生效）、R-7（覆盖面）。
另有 S7-DR gate **BLOCKED**（无 PITR、无 MinIO 元数据比对、无消费者幂等维度）、`archive_mode=off` 导致 RPO 结构性不可达。

**当前发布判定：NO-GO（未变）。**

---

## 6. 本轮续做：PITR 前置条件（WAL 归档）已真实生效

此前实测 `archive_mode=off` / `archive_command=(disabled)` / `archived_count=0` / 无调度备份 →
**RPO ≤ 15 分钟在结构上不可达**。本轮把机制落地：

### 6.1 变更内容（可逆，已备份）

| 项 | 值 |
|---|---|
| 配置文件 | 容器内 `/var/lib/postgresql/18/docker/postgresql.conf`（卷 `/data/blockchain-platform/postgres`） |
| 备份 | `postgresql.conf.bak-20260912T2115Z`（32657 B，`postgres:postgres` 0600） |
| 追加片段 | `archive_mode = on`；`archive_command = 'test ! -f /var/lib/postgresql/wal-archive/%f && cp %p /var/lib/postgresql/wal-archive/%f'`；`archive_timeout = 60` |
| 归档目录 | 容器内 `/var/lib/postgresql/wal-archive`（0700 postgres，位于持久卷内） |
| 注入方式 | **先写入宿主 `/tmp/pg-pitr.conf` 再 `docker cp` + `cat >>`** —— 第一版用嵌套 heredoc 时引号被外层 shell 截断，`archive_command` 退化成 `test`，导致 `failed=9`；已用 `sed -i '/^# PITR (S7-DR)/,$d'` 清除错误行后重做（错误行已不在文件中） |
| 回滚方式 | 删除 `# PITR (S7-DR)` 起至文件末尾的片段并把 `archive_mode` 置回 `off`，或直接恢复 `.bak-20260912T2115Z`，随后 `docker restart deploy-postgres-1` |

### 6.2 生效证据（实测）

```
archive_mode=on
archive_command=test ! -f /var/lib/postgresql/wal-archive/%f && cp %p /var/lib/postgresql/wal-archive/%f
archive_timeout=60
wal_level=replica
pg_stat_archiver: archived=3 failed=9 last=0000000100000002000000F2
/var/lib/postgresql/wal-archive: files=3
  0000000100000002000000F0
  0000000100000002000000F1
  0000000100000002000000F2
```

- `failed=9` 是**修复前** `archive_command=test` 期间累积的失败计数；修复后 `archived` 开始增长且 `last_archived_wal` 即为曾失败的那个段。
- `pg_switch_wal()` 触发后 **真实落盘 3 个 WAL 段**（不是只改了配置）。
- 平台 `/health/ready` 仍 **200**，postgres/clickhouse/minio/nats 全 ok。

### 6.3 **仍不构成 RPO 达标**（必须与上面一起读）

| 缺口 | 说明 |
|---|---|
| **归档只在本机卷内** | `wal-archive` 与数据目录同卷同盘 → 防的是逻辑损坏/误删，**不防主机或磁盘损毁**。要真正 DR 必须把 WAL 运到异机（如 MinIO 独立 prefix） |
| **仍无调度备份** | 无 systemd timer/cron；快照式全量备份仍靠人工 |
| **PITR 恢复从未演练** | 没有"恢复到任意时间点并断言 RPO 实测值"的证据 → 生成的 `pitr-*.json` 仍是 gate 的阻断项 |
| **archive_timeout=60 的代价** | 空闲时也会每 60s 切一个 WAL 段（换取 RPO 上界），WAL 体积随时间增长，需要纳入容量预测与清理策略（当前无自动清理） |

→ S7-DR gate 仍为 **BLOCKED**；RPO ≤ 15 分钟**尚未被证明**，只是**机制就位**。

### 6.4 归档命令改为"绝不覆盖"语义（2026-09-13T04:09+07 / 2026-09-12T21:09Z）

第一版 `archive_command` 是 `test ! -f … && cp …`：同名段已存在且**内容不同**时它会直接返回 0，
PostgreSQL 会把该段标记为"已归档" → 恢复时用错段，且**没有任何告警**。已替换为：

```
archive_command = 'if [ -f /var/lib/postgresql/wal-archive/%f ]; then cmp -s %p /var/lib/postgresql/wal-archive/%f; else cp %p /var/lib/postgresql/wal-archive/%f; fi'
```

语义：不存在 → 复制；已存在 → 必须 **`cmp` 完全一致**，不一致返回非 0，PG 会重试并写入失败计数（**不再静默接受**）。

**真实负向测试**（容器内构造同名不同内容的段）：`cmp -s` 返回 **1** → 会触发 PG 重试/告警路径。✓

**改动前后配置与哈希（按要求保留）**：

| 时点 | `postgresql.conf` SHA-256 |
|---|---|
| 原始备份（改动前基线） | `898a7b93bb1f01088baafb6d7c522e7120cdd0fd6efdc0a517fadbb7e37b49ad`（`postgresql.conf.bak-20260912T2115Z`） |
| 第一版（`test ! -f && cp`） | `19049cd33a350dcfb7323fa781fdcd8da070b49f0d2f36a31e62e13c7c1aa9d2` |
| 当前（cmp 不覆盖） | `5a48b0d27f5d55ae412ecbc9325a4dcd1f22556d8f3c86adc1e979af423c6e18` |

证据文件：`sprint7/pitr/pg-archive-before-20260912T2120Z.txt`、`pg-archive-after-20260912T2120Z.txt`、`pg-container-log-20260912T2120Z.txt`。

**重启后无损复核**（实测）：`/health/ready` 200（四依赖全 ok）；`active_jobs=1`；
`audit_events_v2` 链头 `seq=50925`（相对早前 50374 单调推进）；归档持续推进 `archived=5 last_ok=…F4`。

### 6.5 同一窗口发现的独立缺陷（已单独记录并修复）

重启后复核日志时发现 `provider_usage` 上的高频 ERROR（15 分钟 897 次），
根因是 `internal/download/v14.go` 查询了不存在的列 `created_at`（正确列名 `request_time`），
且错误被 `_ =` 静默吞掉 → §28 的 Retry Amplification 信号长期缺失，且该口径本身把 `success` 漏算、把缓存算成外部调用。

详见 `sprint7/defects/S7-DEF-OBS-01-provider-usage-created-at.md`（含 1.5789 → 1.1667 的同刻口径对比与 /metrics 端到端验证）。
