# DR 脚本复核修复记录（S7-DR-01）

> 触发原因：独立监控者复核 scripts/backup/ 下的三个灾备脚本，发现多处会造成「假成功 / 掩盖失败」的缺陷。
> 本文件逐条记录「修复前 / 修复后」，编号与复核清单一致（1–13），并列出**无法只在脚本层彻底解决**、
> 必须在演练时用实测证据补充的条目。
>
> 本次为**只改脚本、不执行演练**：未在主机上运行 backup-all.sh / restore-drill.sh / kill9-drill.sh，
> 也未连接 VM 执行任何命令。已做的验证见文末「验证记录」。
>
> 改动文件：
> - scripts/backup/backup-all.sh（对应 #1–#6）
> - scripts/backup/restore-drill.sh（对应 #7–#10）
> - scripts/backup/kill9-drill.sh（对应 #11–#13）
> - scripts/backup/README-DR-FIXES.md（本文件，新建）

## 0. 三条全局约定（所有脚本）

1. 保持 `set -uo pipefail`（未改成 -e，避免与"必须显式检查退出码"的复核要求互相掩盖）；
   在此基础上**每条关键命令都显式检查退出码**，不再用 `|| true` / `2>/dev/null` 吞掉必需步骤的失败。
   （仅"尽力而为的清理"与"只读探测失败后如实记录 NOT_VERIFIED"两处例外，且都在注释里写明。）
2. 输出目录、日志、失败标记（`__FAILED` / `__INCOMPLETE` / `__INCONSISTENT` / `FAILED-*`）一律保留，
   任何脚本都不会自动删除证据；备份裁剪只可能发生在"本次备份完整"且"有书面批准"两个条件同时成立时。
3. 未改动、未删除 acceptance-evidence/ 下的任何既有文件（本次改动只落在 scripts/backup/ 下）。

---

## 1.（backup-all.sh）默认 7 天自动裁剪 —— 改为默认禁用 + 三重保护

| | 内容 |
|---|---|
| 修复前 | `BACKUP_RETENTION_DAYS` 默认 7；脚本末尾无条件执行 `find BACKUP_ROOT -maxdepth 1 -type d -name '20*' -mtime +7 -print -exec rm -rf {} +`，无审批、无清单、无保底，且会把任何名字以 20 开头的目录（哪怕不是本脚本产出）一起删掉。 |
| 修复后 | ① `BACKUP_RETENTION_DAYS` 默认 **0（禁用）**，脚本明确打印"保留策略：已禁用"；② 只有 `BACKUP_RETENTION_DAYS > 0` **且** `BACKUP_RETENTION_APPROVED=1`（表示已有书面批准的保留策略）才进入裁剪；③ 进入后先把"将被删除的目录清单 + 保留清单"写入 **日志（stdout）与裁剪记录文件** `$BACKUP_ROOT/retention-<TS>.log`，然后才删除；④ 永远保留最近 `BACKUP_RETENTION_KEEP_MIN`（默认 3）份与任何带 `__FAILED` / `__INCONSISTENT` / `__INCOMPLETE` 标记的目录；⑤ **绝不删除没有 manifest.json 的目录**（非本脚本产出）；⑥ 本次备份不完整时**根本不进入**裁剪流程（避免"新备份已坏、旧备份被删"）；⑦ 删除前对每个目录再做一次同样条件的复核（TOCTOU 兜底）。 |
| 残留 | `BACKUP_RETENTION_APPROVED=1` 是"人肉声明"，脚本无法验证批准文件真的存在。批准证据（变更单/审批记录）仍需在演练或运维记录中留痕。 |

## 2.（backup-all.sh）manifest 里 minio 的 object_count 恒为 null —— 修正读取路径

| | 内容 |
|---|---|
| 修复前 | 脚本把计数写到 `$DIR/minio/<bucket>.object_count`，而 manifest 生成时用 `read(b + ".object_count")` 相对 `$DIR` 读取，路径不存在 → `manifest.minio` 里每个桶的 object_count **恒为 null**（报告里却按"对象数"引用）。 |
| 修复后 | 改为 `read(os.path.join("minio", entry + ".object_count"))`，并同时输出 `minio_detail.buckets` / `skipped_buckets`。计数失败时写 `ERROR:...` 而不是静默留空。 |
| 残留 | 无（属确定性缺陷，已闭环）。 |

## 3.（backup-all.sh）manifest 生成失败仍打印 backup done —— 检查退出码 + 二次校验

| | 内容 |
|---|---|
| 修复前 | python heredoc 的退出码没有检查；即使 manifest 生成失败（例如磁盘满、JSON 写入异常），脚本仍会走到末尾打印 `backup done`。 |
| 修复后 | ① 捕获 `MANIFEST_RC`，非 0 立即 `fail` 并写 `__FAILED`；② 增加**二次校验**：重新读取 manifest.json，断言必需字段（backup_id / complete / pg_snapshot_consistent / postgres / clickhouse / minio / consistency / files）齐全、`files` 含 `postgres.dump`、`consistency.cross_store_consistency == "bounded_by_window"`，任一不满足即 fail；③ 用 `manifest.json.tmp` + `os.replace` 原子落盘，避免半个文件被下游当真。 |
| 残留 | 无。 |

## 4.（backup-all.sh）元数据查询吞错写空值 —— 失败即 ERROR + 标记不完整 + 非 0 退出

| | 内容 |
|---|---|
| 修复前 | `count(*) from schema_migrations`、`max(seq) from audit_events_v2`、CH 的 `system.tables` 计数全部 `2>/dev/null`，失败就写空文件，manifest 里对应字段是 `null`，脚本照样 exit 0。 |
| 修复后 | 统一走 `write_metric` / CH 计数分支：查询失败或返回值不是数字时，① 口径文件写入 `ERROR:<原因>`（含 psql stderr 摘要）；② 追加一条 `__INCOMPLETE` 原因；③ `manifest.complete=false` 且 `manifest.incomplete_reasons` 列出原因；④ 脚本末尾检测到不完整即打印明细并 **exit 1**（并明确提示"禁止当作成功备份"）；`__INCOMPLETE` 目录受裁剪保护。 |
| 残留 | 备份不完整时仍会保留已有产物（这是刻意的：便于诊断），但退出码与标记已经让定时器/报告不可能把它当成成功。 |

## 5.（backup-all.sh）一致性边界 —— pg_export_snapshot + pg_dump --snapshot + consistency 段

| | 内容 |
|---|---|
| 修复前 | 先 `pg_dump`，再单独一次 psql 读 `max(seq)`：两者不在同一备份点；PG / CH / MinIO 三段可能相隔很久（实测 22 分钟），报告却把三者当作一个时刻。 |
| 修复后 | ① 用 FIFO 打开一个**长活 psql 会话**：`BEGIN ISOLATION LEVEL REPEATABLE READ; SELECT pg_export_snapshot();`，取得 snapshot id 后保持事务存活，用 `pg_dump --snapshot=<id>` 导出；② 导出完成后在**同一会话、同一快照、同一事务**内读取 `schema_migrations` 计数、`audit_events_v2` 的 `max(seq)` 与 `count(*)`、`pg_current_wal_lsn()`、`now()`，再 `COMMIT`；③ manifest 记录 `postgres.pg_snapshot_id` / `pg_current_wal_lsn` / `metrics_read_at` / `read_within_exported_snapshot`，并新增 `consistency` 段：三个 store 各自的 `started_at` / `finished_at` / `seconds` + 整体窗口 `window_seconds`，显式写 `"cross_store_consistency": "bounded_by_window"` 与"禁止用行数 delta 宣称跨库一致性"的说明；④ **退化路径**（快照无法建立，例如 psql/docker 异常）：manifest 顶层显式写 `"pg_snapshot_consistent": false` + `pg_snapshot_note`（含 stderr 摘要），写 `__INCONSISTENT` 标记，并且**除非**显式设置 `BACKUP_ALLOW_INCONSISTENT=1`（表示已知并接受），否则记为不完整、exit 1。 |
| 残留（**必须靠演练实测补证**） | ① 快照只保证 **PG 内部**一致；CH / MinIO 仍各有自己的时刻，跨库一致性只能声明"落在 window_seconds 窗口内"，真正的跨库原子备份需要分布式快照/冻结写入，脚本层做不到；② 无 WAL 归档 → 仍是快照式备份，**RPO 不受控**；③ 快照路径在实机上的可行性（pg_dump --snapshot 与长活 FIFO 会话的配合、22 分钟导出期间事务不被中断）**必须由一次真实演练验证**，验证前不得在任何报告中写"PG 备份点已一致"。 |

## 6.（backup-all.sh）ClickHouse 行数口径与引擎描述 —— count(FINAL) + 统一为 File 引擎

| | 内容 |
|---|---|
| 修复前 | 备份只记录 `system.tables` 计数，没有任何行数口径；注释写 `TO Disk('backups',...)` 而实现用 `TO File(...)`（文档与实现不一致，恢复侧报告也照抄了 Disk）。 |
| 修复后 | ① 逐表同时取 `count()` 与 `count(FINAL)`，写入 `$DIR/clickhouse.rowcounts` 与 `manifest.clickhouse.rowcounts`，并写明"ReplacingMergeTree 一致性判定必须用 count_final"；`count(FINAL)` 取不到即 `ERROR:count(FINAL)` + 标记备份不完整 + exit 1；② 注释、manifest 的 `clickhouse.engine = "File"` 与 `engine_note` 统一为 File 引擎，并说明原因（`system.disks` 只有 default、未配置 `backups.allowed_disk`，`Disk('backups',...)` 会直接报 INVALID_CONFIG_PARAMETER；config.xml 配的是 `backups.allowed_path`）；restore-drill 的报告文本也同步改为 `FROM File(...)`。 |
| 残留 | `count(FINAL)` 在 `address_activity` 这类大表上代价高（实测口径：count() 74,448,957 / count(FINAL) 62,774,084），会拉长备份时间；数值本身要与生产库对比，需要演练时确认是否可接受。 |

---

## 7.（restore-drill.sh）恢复目标 —— 不再 DROP + CREATE，改随机 UUID 新目标且绝不自动清理

| | 内容 |
|---|---|
| 修复前 | `DROP DATABASE IF EXISTS $PGR` + `CREATE DATABASE`；CH 用固定 `<db>_dr_<ts>`；MinIO 用固定 `blockchain-reports-drill-<ts>`，并在核对后执行 `DROP DATABASE IF EXISTS $CHR` 自动清理。 |
| 修复后 | ① 目标名一律带**随机 UUID 后缀**（`blockchain_platform_dr_<uuid12>` / `<db>_dr_<uuid12>` / `<bucket>-drill-<uuid12>`）；② 建目标**之前先查存在性**：PG 查 `pg_database`、CH 查 `system.databases`、MinIO 用 `mc stat`（失败再 `mc ls`，**判定不了就 fail-closed 中止**），已存在即**显式拒绝并退出**，脚本内不再出现任何 `DROP DATABASE` / `mc rb`；③ 演练结束**不清理**：报告 §5 与结果 JSON 记录目标名、**脱敏连接串**、日志目录路径，并给出"需人工确认后执行"的清理命令；④ 失败的目标同样保留。 |
| 残留（需演练实测） | 目标仍建在同一套集群/同一 MinIO 上，演练会真实占用生产资源（一个 9.4 分钟、17 GiB 级别的 CH 恢复）；"不清理"意味着**磁盘会持续增长**，需要人工按报告 §5 定期清理，脚本故意不代劳。 |

## 8.（restore-drill.sh）恢复失败被吞 —— 每一步都要么通过、要么 FAIL

| | 内容 |
|---|---|
| 修复前 | `pg_restore` 的 `RC` 只写进报告不影响退出码；CH `RESTORE` 失败仅追加一行；逐表/逐对象比对用 `2>/dev/null`，取不到值就用空串参与算术展开（空值被当成 0）；auditverify 的输出直接 `head -60` 进报告，退出码无人检查；MinIO 只比对象数。 |
| 修复后 | 统一 `fail_check()`：写入 `FAILURES.txt` + 报告 + stderr，最终写 `FAILED-restore-drill-<TS>.md` 并 **exit 1**。逐一覆盖：`docker cp` / `pg_restore`（rc≠0 即 FAIL，附日志末行）/ 逐表行数（查询失败写 `ERROR` 并 FAIL，**绝不按 0 计算差值**；恢复库数量大于生产也 FAIL）/ 恢复库元数据 / auditverify（rc 0=通过，1=链不通过，2=参数连接错误，全部 FAIL）/ 对象引用核对（对象缺失、大小不符、sha256 与 `content_hash` 不符均 FAIL）/ CH 归档 `tar tzf` 与解包 / CH `RESTORE` / CH 逐表 `count(FINAL)` / 逐桶对象数与逐对象比对 / 结果文件写入。恢复失败不会被任何"后续步骤照跑、最后 exit 0"的路径掩盖。 |
| 残留 | `pg_restore` 对"警告"也可能返回非 0（例如对象已存在的告警）。本次按复核要求一律 FAIL——这是刻意的严格：非 0 必须人工分诊，日志保留在 `restore-drill-<TS>.logs/pg_restore.log`。若实测确认某类告警是良性的，应当**改判据并留痕**，而不是回头忽略退出码。 |

## 9.（restore-drill.sh）源 manifest 前置校验 —— 显式传参 + 逐文件校验 + 拒绝失败标记

| | 内容 |
|---|---|
| 修复前 | 备份目录参数可省略，缺省时 `ls -dt "$BACKUP_ROOT"/20* | head -1` **盲选最近一个目录**；不读 manifest，不校验 size/sha256，也不看 `__FAILED` 标记。 |
| 修复后 | ① 备份目录**必须显式传入**，缺参打印用法并 exit 2（不再盲选）；② 拒绝 `__FAILED` 目录（打印标记内容后中止）；带 `__INCOMPLETE` / `__INCONSISTENT` 的目录同样拒绝，除非显式 `RESTORE_DRILL_ACCEPT_INCOMPLETE=1`（接受动作会在报告中留痕）；③ 逐条校验 manifest：`files` 中每个文件都存在、size 与实际一致、sha256 与实际一致（另列出"未登记的额外文件"作为提示，不计入校验链），并断言 `postgres.dump` / `clickhouse.tar.gz` 在列；任何不一致即中止（未创建任何演练目标，exit 2）；④ 校验通过后把 `backup_id` / `complete` / `pg_snapshot_consistent` / `cross_store_consistency` / 审计边界值带进报告，供后续"与备份点交叉核对"。 |
| 残留（需演练实测） | manifest 的 sha256 只能证明"备份目录里的文件与 manifest 记录一致"（**存储/传输完整性**），**不能**证明 dump 本身可恢复、更不能证明备份时刻的数据本身正确——那只能靠真实恢复 + §1.1 的备份点交叉核对来证明。9.9 GiB 级别的 sha256 全量校验会额外花几分钟，这是刻意的代价。 |

## 10.（restore-drill.sh）MinIO 全桶恢复与逐对象比对 —— 版本/生命周期显式 NOT_VERIFIED

| | 内容 |
|---|---|
| 修复前 | 只恢复 `blockchain-reports`，且只比"文件数 vs 对象数"，用 `2>/dev/null` + `grep -c . || true` 取数；其余桶（audit / backups / exports / raw）完全没有恢复与比对；报告却写"对象数比对通过"。 |
| 修复后 | ① 遍历 `manifest.minio` 记录的**全部桶**，逐桶新建 `<bucket>-drill-<uuid12>` 并 `mc mirror`；② 校验必需桶清单（默认 blockchain-audit / blockchain-backups / blockchain-exports / blockchain-reports / blockchain-raw）：既不在 manifest 又不在 `skipped_buckets` 里 → FAIL（备份未覆盖该桶）；被显式 skip 的桶 → NOT_VERIFIED（不得计入已覆盖）；③ 逐对象比对：键集合（缺失/多余）、每个对象的 size、单分片对象的 `etag = MD5` 与本地文件 MD5 直接比对；多分片对象按预算（默认字节上限 256 MiB、对象数上限 50）抽样用 `mc cat | sha256sum` 与 manifest 的 sha256 做**真实内容**比对，报告里如实给出"etag 逐对象 + 抽样 sha256 x/y 通过"的覆盖数字，绝不写成"全量校验通过"；④ 版本化 / 生命周期 / 对象元数据：只做只读探测（`mc version info` / `mc ilm rule ls`），并在报告结论里**显式写 NOT_VERIFIED**，说明 `mc mirror` 只复制当前版本、不复制历史版本/ILM/元数据/tag。 |
| 残留（**结构性，脚本层无法解决**） | 版本历史、生命周期规则、对象元数据/tag、对象锁（WORM）等维度**永远无法**用 `mc mirror` 证明；要拿证据必须另配工具（版本列举、规则导出、对象元数据比对）单独演练、单独出报告。本次不在任何地方默认通过。 |

---

## 11.（kill9-drill.sh）checkpoint / 成功页核对 —— 从"总 count"改为逐 (shard, page) 不变量

| | 内容 |
|---|---|
| 修复前 | 只比 `count(*) from download_shards` / `count(*) from download_pages where status='DURABLE'` / `count(*) from checkpoints` 三个总数，且把"IN_FLIGHT 残留"当作幽灵任务。但**代码里不存在 IN_FLIGHT 状态**（状态机只有 DURABLE/INGESTED/VALIDATED/FAILED），该项恒为 0，属于假成功；总数不变也完全掩盖"A 的页丢了、B 多了一页"。 |
| 修复后 | 演练前冻结基线（jobs / shards / checkpoints / pages / provider / outbox 的 TSV），演练后**按同一 ID 集合**逐条比对：① `checkpoints`：逐 shard 的 `page_no` 与 `downloaded_rows` 不回退，checkpoint 消失即 FAIL（游标 token 变化只作为提示，因为页号未回退时属正常重取）；② `download_pages`：逐 (shard_id, page_no) 的成功页集合（VALIDATED / INGESTED / DURABLE）不丢失、状态等级不回退（DURABLE < INGESTED < VALIDATED）、**同一 (shard,page) 的 `next_page_token_hash` 不发生变化**、成功页总数不回退；③ `download_shards`：`downloaded_rows` 不回退、`COMPLETED` 不回退；④ Provider 成功页：`provider_usage.status in ('success','ok')` **按 shard 分组**计数不回退（合计值同时列出）；⑤ 幽灵任务改成两项真实检查：`status='RUNNING' AND 租约已过期` 的 stale lease（最多等 60s 期望回到基线，否则 FAIL）+ `download_pages` 中出现状态机之外的状态（必须为 0）。 |
| 残留（需演练实测校准） | ① stale lease 的判定依赖租约时长与观察窗（当前最多等 60s），**需要在真实演练里确认 worker 回收租约的实际耗时**，否则可能出现假 FAIL；② "job 状态不再是 RUNNING"只记 NOT_VERIFIED 供人工判读（作业可能真的跑完了），这是刻意的：宁可留给人判，也不自动算通过。 |

## 12.（kill9-drill.sh）消费者幂等 —— 不再用主键唯一性冒充，缺维度就写 NOT_VERIFIED

| | 内容 |
|---|---|
| 修复前 | `DUPS_BEFORE/DUPS_AFTER` 统计的是 `audit_events_v2` 的重复 `id`，报告却写成"审计重复 id 0→0（期望 0，消费幂等）"——一个主键唯一性检查被当成订阅者幂等的证据。 |
| 修复后 | ① `outbox_events`：冻结基线 id 集合，演练后断言**全部仍存在**（这是"outbox 未丢事件"的证据，报告里明确标注"**不等于**消费者幂等"）；② 订阅者幂等只在**真实 event id / Nats-Msg-Id 维度**上核对：先在库里探测去重/投递表（`%dedupe%` / `%processed_event%` / `%consumed_event%` / `%event_delivery%` / `%event_offset%` / `%consumer_offset%` / `%inbox%`，显式排除 notification% 以免拿通知投递表充数），找到就用其 event id 列做同一 ID 集合断言；③ 找不到维度时写 **NOT_VERIFIED**，并说明理由（JetStream 的 Nats-Msg-Id 去重只在服务端窗口内生效、/jsz 不暴露重复计数），同时把 /jsz 的 stream/consumer 只读证据（delivered / ack_floor / num_ack_pending / num_redelivered）附进报告；④ `audit_events_v2` 重复 id 计数降级为"链完整性观察项"，单独列出并明确标注不作为幂等证据。 |
| 残留（**结构性，需产品侧改造**） | 当前库里**没有**消费者去重/投递维度，因此"订阅者幂等"在本次演练口径下**只能长期是 NOT_VERIFIED**。要真正证明，必须先让消费者把处理过的 event id（或 Nats-Msg-Id）落库（inbox/offset 表），再重跑本演练；`backend/internal/events/bus.go` 把 outbox event id 作为 Nats-Msg-Id 发送，那只是**代码阅读证据**，不能替代演练证据。 |

## 13.（kill9-drill.sh）先 baseline、按同一 ID 集合复核 —— 不按"当前活跃 status"重新筛选

| | 内容 |
|---|---|
| 修复前 | 只在杀进程前后各跑一次"当前 RUNNING 作业 / 当前计数"，没有任何 ID 冻结；被删的行、被改状态的行都可能因为"不再符合筛选条件"而从对比里消失。 |
| 修复后 | ① 演练最开始执行 §0 baseline，把 jobs / shards / checkpoints / pages / provider_usage(success,ok) / outbox 的 ID 集合（以及 page_no、downloaded_rows、next_page_token_hash 等字段）冻结成 TSV 存在 `kill9-<TS>.logs/baseline/`；② 演练后用**完全相同的 SQL** 抓复核对到 `after/`，由内嵌的 checker **只遍历基线的 ID 集合**做逐条比对（不重新筛选、不按 status 过滤）；③ 基线抓取有行数上限（默认 200000），一旦触顶会在 `truncated.txt` 记录并在对应核对行打上 **PARTIAL**、同时打印 WARN——截断的核对不会被当作完整通过。 |
| 残留 | 基线规模大时（例如 provider_usage 按 shard 分组的中间结果）TSV 会变大，读取与比对耗时随之上升；上限可按需用 `KILL9_BASELINE_MAX_ROWS` 调整，但调大或截断都必须在报告里被看见（已实现 PARTIAL 标注）。 |

---

## 14. 本次附带修掉的同类缺陷（复核清单外的"假成功"）

1. **MinIO 迁移/挂载参数错位隐患**：原脚本把 `docker run` 的参数拼进字符串变量再调用，容易出现 `-v` 之类落到镜像名之后被当成 `mc` 自身参数的错位（本次已按函数化调用重写：卷/环境变量一律在镜像名之前）。
2. **manifest 相对路径分隔符**：`os.path.relpath` 在 Windows 上返回反斜杠，会让"按 manifest 键匹配文件"的下游逻辑整体失配。backup-all 与 restore-drill 都已统一归一为 `/`。
3. **恢复侧报告抄错引擎**：原报告文本写 `FROM Disk('backups',...)` 而实现是 `FROM File(...)`，已统一。
4. **退出码空转**：`head -1` 之类的管道在 `pipefail` 下会让整条 pipeline 的 rc 失真，凡是作为判据的地方都改成了"先落文件、再单独判定 rc"。
5. **.env 值含空格会被 source 破坏**：manifest 中可能出现的 `ERROR:...（含中文括号与空格）` 一旦被 `. envfile` source 会变成命令执行错误；restore-drill 的输出已改为 `shlex.quote`（已用离线用例验证）。

---

## 15. 无法只在脚本层彻底解决的条目（需演练实测证据）

| 条目 | 为什么脚本层解决不了 | 需要什么证据 |
|---|---|---|
| #5 跨库一致性 | PG 快照只覆盖 PG；CH/MinIO 各有时刻，脚本最多声明 `bounded_by_window` | 一次真实备份的 `consistency.timing` 窗口值 + 明确声明"只能按窗口理解"；要更强只能上分布式快照/PITR 改造 |
| #5 快照路径可行性 | FIFO + 长活事务 + `pg_dump --snapshot` 的组合未在主机实测 | 首次演练时确认 `postgres.snapshot_id` 非空、`pg_snapshot_consistent=true`、dump 可用；失败则退化路径必须显式 `false` |
| #5 RPO | 无 WAL 归档 → 快照式备份，RPO 由两次备份间隔决定 | 需要调度化备份 + `archive_mode=on` + WAL 归档（PITR）才能真正压到分钟级 |
| #6 CH `count(FINAL)` | 与生产的一致性只能在生产相对静止的瞬间成立；恢复侧的行数还会受合并状态影响 | 演练时同时给出"恢复库 vs manifest 备份点（精确相等）"与"生产 vs 恢复库（参考差值）"两组数字 |
| #9 manifest 校验 | sha256 只证明"目录内文件与 manifest 一致"，不证明 dump 可恢复、更不证明备份时刻数据正确 | 必须靠一次真实 `pg_restore` + `auditverify` + 与备份点交叉核对来闭环 |
| #9/#4 快照边界值 | 边界值取自快照，但"pg_dump 是否真的按该快照导出了全部对象"仍需恢复侧验证 | 演练时断言"恢复库 audit head seq / 事件数 / 迁移版本 == manifest 备份点值"（脚本已实现为 FAIL 判据） |
| #10 版本/生命周期/对象元数据 | `mc mirror` 只反映当前对象版本，结构上无法覆盖版本历史、ILM、元数据、对象锁 | 必须另配工具（版本列举、规则导出、元数据比对）单独演练并单独出报告 |
| #11 幽灵任务（stale lease） | "多久算未被回收"取决于租约时长与 worker 回收节奏 | 首次演练实测回收耗时，据此校准观察窗（当前 60s），并在报告中记录实际值 |
| #12 订阅者幂等 | 库里没有 event id / Nats-Msg-Id 的去重落库维度，JetStream 也不暴露重复计数 | 需要产品侧先加消费者 inbox/offset 记录；在此之前只能长期 NOT_VERIFIED |
| #1 保留策略审批 | 脚本无法验证"书面批准"真实存在 | 批准文件/变更单需在运维记录中留痕，脚本只负责"没有该标志就绝不删" |
| #7 演练资源占用 | 新目标仍在同一集群，"不自动清理"会让磁盘持续增长 | 人工按报告 §5 的清理命令定期回收，并把清理动作留痕 |

---

## 16. 验证记录（本次未执行演练）

> 约束：VM 上正在跑另一个故障注入套件，本次**没有**连接 VM、**没有**执行任何演练脚本。
> 只做本地代码修改与静态/离线验证。

1. **bash 语法检查**：`bash -n` 对三个脚本均通过（exit 0，GNU bash 5.3.9）。
2. **转义/编码复核**：三个脚本中不存在多余的转义残留（无字面反斜杠、无未解析的占位符），shell 变量展开位置经人工复核。
3. **kill9 不变量 checker 离线单测**（抽取内嵌 python，用合成 TSV 跑）：
   - 用例 A（人为注入 10 处违规：job 丢失、shards 行数回退、COMPLETED 回退、checkpoint 消失、page_no 回退、成功页状态回退、页游标哈希变化、provider 成功页回退、outbox id 丢失、消费者维度 id 丢失）→ 全部被识别，共 10 条 FAIL，无漏报；
   - 用例 B（基线与复核对完全一致 + 一个 PARTIAL 截断标记）→ **0 条 FAIL**（无误报），并且正确打印截断提示。
4. **restore-drill manifest 校验器离线单测**（合成备份目录：6 个文件 + manifest + 额外文件 + 含空格/括号的 `ERROR:` 值）：
   - 正常用例：6/6 文件 size+sha256 一致，`CHECK_OK=true`，桶清单/跳过清单正确；
   - 篡改用例（翻转 `postgres.dump` 一个字节）→ `FILES_SHA_MISMATCH=1`、`CHECK_OK=false`（脚本随即中止，不创建任何演练目标）；
   - `. manifest_check.env` 由 bash 实测可正确 source（含带空格与括号的值）。
5. **未执行**：backup-all.sh / restore-drill.sh / kill9-drill.sh 的实机演练——按任务要求留给后续维护窗口；届时产生的报告才是这些脚本的最终验收依据。
