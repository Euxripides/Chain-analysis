# 源码同步审计 + D-B2RAW-02（脱离请求的审计写入必须**有界**）

- 时间：2026-09-13T22:23–22:55+08（14:23–14:55Z）
- 写入者：新唯一 DSH 会话（22:22 启动；禁用 create_goal，仅任务清单）
- 触发：根 22:21 独立发现「本地 backend/internal/api/export.go 没有 auditRequestDetached，
  而旧交接宣称 VM 已修 7 个调用点」→ 必须先做**源码同步核对**，再做有界超时核对。

## 1. 冻结：本地 / VM 三文件 SHA（同步前）

| 文件 | 本地（同步前） | VM（同步前） | 关系 |
| --- | --- | --- | --- |
| backend/internal/api/export.go | ab3a51a0b16d65cd8c9fc4d833f5aa193f78bf1d0492580aa9f237e059821f42 | 427a2aa75728d93a6135877da6ec8b6231d2a2d634d2d283104392b4054f7f15 | 本地**缺** auditRequestDetached |
| backend/internal/api/export_download_audit_test.go | 6f546d8faf9fac0410c65553bbee8c3ba65360218d7cc8a84d5bd89266b8065f | b9a6aa8c7528b9682af80ebf33c51f33f3f0fba0852bccd05400c95c8008e795 | 本地是 VM 版的**严格前缀子集** |
| VM 侧修复前原件（保留） | — | b984eea10c2f891e9366cdae0e1dbbe67ea421b89ae09a30f2ef4d7dab9a5cf9（.pre-auditdetach-20260913T135725Z/export.go.orig） | 参照基线 |

### 1.1 本地 export.go 与 VM「修复前原件」的逐字节比对（结论：无本地独有功能改动）
- `local==pre ? False`，长度 25440 vs 25439，首个差异字节 **6068**（同一行的注释块内）。
- unified diff（pre → local）**只有一处 hunk**：中文文档注释里
  `//   object_bucket ...` 两行 与 gofmt 归一化后的 `//` + `//\tobject_bucket ...` 三行之差。
- 即：本地 export.go = VM 修复前内容 + **未 gofmt 的注释排版**；**没有任何本地独有逻辑**。
  因此套用 VM 修复版不会丢失本地 dirty 改动（本地原件已另存 local-before/）。

### 1.2 本地测试文件与 VM 的比对
- unified diff 只有一个追加 hunk：VM 版 = 本地版 + 末尾 69 行
  （cancelAwareAuditSink / cancelOnLastWrite / TestDownloadFullAuditSurvivesClientDisconnect）。
- 本地无独有内容，可直接对齐。

### 1.3 同步结果（双向逐字节验证）
| 文件 | 同步后本地 | 同步后 VM | 一致 |
| --- | --- | --- | --- |
| export.go | 427a2aa75728d93a6135877da6ec8b6231d2a2d634d2d283104392b4054f7f15 | 同 | ✓ |
| export_download_audit_test.go（D-B2RAW-01 版） | b9a6aa8c7528b9682af80ebf33c51f33f3f0fba0852bccd05400c95c8008e795 | 同 | ✓ |

本地原件保存在 `local-before/`（未删除、未覆盖）；VM 上传通道 sftp_upload 仍不可用
（`local path must be under /workspace`，5 种路径写法全部失败，与旧交接一致），
改用 sftp_read_text 拉取 VM 原文 → 本地重建 → SHA 逐一比对（427a2aa7…/b9a6aa8c…/b984eea1… 三份全部命中）。

## 2. 新发现的安全/可用性缺陷 D-B2RAW-02（真实、已修、已负向校准）

### 2.1 现象与证明链
D-B2RAW-01 的修复用 `context.WithoutCancel` 让审计写入脱离客户端断连 —— 但
**context.WithoutCancel 同时会去掉 deadline**（Go 官方语义：返回的 ctx 无 Deadline、
无 Err、Done 为 nil）。于是审计写入变成**完全无界**。三条独立证据：

1. 代码：`Server.emitAudit` → `auditSink.Write(ctx, ev)` / `s.audit.Write(ctx, ev)`，
   而 `audit.Writer.Write` 用**同一个 ctx** 执行 `pool.Begin` / `WriteTx`（含 INSERT 与哈希链推进）/ `tx.Commit`，
   内部无任何超时。
2. 连接池：`internal/store/db.go` 用 `pgxpool.ParseConfig(url)` + `AfterConnect: SET ROLE`，
   **未设置**任何查询级/语句级超时（无 ConnectTimeout 之外的 timeout 字段）。
3. 数据库实测（`docker exec deploy-postgres-1 psql`，只读）：
   `statement_timeout=0`、`lock_timeout=0`、`idle_in_transaction_session_timeout=0`、
   `idle_session_timeout=0`（全部 source=default）；**19 个角色 rolconfig 全部为空 {}**；
   仓库全量 grep `statement_timeout` **0 命中**。

风险：DB 一旦卡住（锁等待 / 连接池饱和 / 存储抖动），全量下载 handler 的 goroutine 连同其
pgx 连接会被**永久占用**；反复下载即可耗尽连接池 = 把「审计可靠性修复」变成可用性/P1 缺陷。
（修复前 ctx 会被客户端断连取消，所以失败得快；修复后从「丢审计」变成「可能挂死」。）

### 2.2 修复（最小、集中、可回归）
`internal/api/server.go`：
- `emitAudit` 统一出口在写入前调用新 helper，并在返回时释放 cancel；
- 新增 `var auditWriteTimeout = 5 * time.Second`（var 而非 const：回归测试要调小它才能确定性验证切断）；
- 新增 `auditBoundedContext(ctx) (context.Context, context.CancelFunc)`：
  **有 deadline 的 ctx 原样返回**（不缩短也不放宽调用方语义），无 deadline 的补 5s 上界。
- 行为不变点：审计失败仍然「不静默」（保留原有的 Error 日志与错误返回策略）。

### 2.3 回归测试（`export_download_audit_test.go`）
- `TestDetachedDownloadAuditIsBounded`：忠实复刻「最后一个字节写出即取消请求 ctx」的真实时序，
  注入 `deadlineBlockingAuditSink`（模拟卡死的 DB：阻塞到 ctx 结束并记录是否带 deadline），
  断言 (a) 下载仍完整交付真实字节，(b) 脱离后的审计 ctx **必须带 deadline**，
  (c) 卡死写入被 **DeadlineExceeded** 切断，(d) 剩余 deadline 落在配置上界内。
- `TestAuditBoundedContextAddsAndPreservesDeadline`：无 deadline → 补界；已有 deadline → 原样保留。
- `TestDownloadFullAuditSurvivesClientDisconnect`：D-B2RAW-01 原回归，保持通过。

### 2.4 负向校准（**只在隔离副本**，生产源码零替换）
隔离副本：`.work/negcal-d-b2raw-02-20260913T1430Z/backend`（backend 全量拷贝，排除 bin/）。

| 校准 | 输入 | 期望 | 实测输出 | 结论 |
| --- | --- | --- | --- | --- |
| A / D-B2RAW-01 | 修复前 export.go `b984eea1…` + 修复前 server.go `eae16329…` + D-B2RAW-01 测试 | FAIL | `client disconnect must NOT suppress the success audit: got 0 export.download events`，`NEGCAL_A_RC=1` | ✓ 测试确实能抓到此缺陷 |
| B / D-B2RAW-02 | 修复版 export.go `427a2aa7…` + **去掉有界调用**的 server.go `3152f8eb…` + 新测试 | FAIL | `detached audit write never returned: the audit context is unbounded (D-B2RAW-02)`，30.00s 后被守卫判失败，`NEGCAL_B_RC=1` | ✓ 上界确实是新测试在守 |
| A'/B' 正向 | 修复后源码 | PASS | 三测试全 PASS（本地 1.03s / VM 9.73s） | ✓ |

### 2.5 上传与字节一致性
- 补丁载荷 **ASCII-only**（英文注释），以 `count==1` 锚点断言做两段替换，脚本本身
  SHA `7c38c7e8cb0db94e8cc9d9e75df5437ad184cc87239d1171a0c36a42fd9c81c2`，
  本地空跑（对 VM 原文副本）与 VM 实跑**输出完全一致**。
- VM 侧先备份到 `vm-before/`（`eae16329…` / `b9a6aa8c…`，未删）。
- 三文件最终 SHA（本地 == VM，逐字节）：
  - server.go **2a26fe01bf06df55d7e58fbde99fed547d827e608f4078f1c9a0ebb9e04da849**（37525 B）
  - export_download_audit_test.go **0670b1d20b30cde188bafdff1a516d9e6fca9d75055f6a8125f26df57c028b68**（19157 B）
  - export.go **427a2aa75728d93a6135877da6ec8b6231d2a2d634d2d283104392b4054f7f15**（26629 B）

### 2.6 gofmt 说明（可审计，不隐藏）
本地与 VM 的 server.go 在**改动前**就存在 gofmt 偏差（Server 结构体字段对齐，第 98–110 行，
与本次改动无关）；因为本次改动**触碰了该文件**，按 gofmt 门禁一并归一化。
server.go gofmt 前 `dfeaca71da824190b7242386fa6769b8bc92e763e847755b5c3cdb892511468b`
→ 本次改动 + 归一化后 `2a26fe01…`。该 gofmt 只改空白/对齐，无语义变化。

## 3. 红线遵守
未 TRUNCATE/reset/clean/drop；未删任何对象/证据/备份；未覆盖本地 dirty 原件（全部另存）；
未在生产源码里做负向切换（校准仅在 `.work` 隔离副本）；未改 object_ref；未回填 66 条空 PG SHA；
未重启生产服务（本阶段只改源码 + 本地/VM 测试，部署在后续受控步骤）；未 commit。
