# S7-DR 独立只读复核（2026-09-13，UTC+7 03:2x / 2026-09-12T20:2xZ）

> 作者：接手执行 Agent（新主会话）
> 性质：**只读**独立复核。本文件**不是**验收通过声明；S7-DR 当前判定 = **NOT VERIFIED（阻断）**。
> 未修改 scripts/backup/ 下任何脚本，未运行任何演练脚本，未连接 VM 执行任何写操作。
> 所有事实均在本机或部署机上实时复验，非引用旧文档。

---

## 0. 复核方法

- 部署机只读命令：`systemctl --user cat/show`、`sha256sum`、`docker exec ... psql/clickhouse-client`（只读 SELECT/SHOW）、`mc ls/du`、`curl`（仅 GET）。
- 代码级复核：直接读 `scripts/backup/{backup-all.sh,restore-drill.sh,kill9-drill.sh}` 的退出码与汇总分支。
- 未执行：任何 `backup-all.sh` / `restore-drill.sh` / `kill9-drill.sh` 调用；任何 DROP / 删除 / 裁剪。

---

## 1. 部署身份与脚本版本（2026-09-13T03:27+07 实测）

| 项 | 实测值 | 与交接文件一致性 |
|---|---|---|
| `blockchain-api.service` ExecStart | `/home/euripides/projects/blockchain-platform/backend/bin/api` | 一致 |
| `blockchain-worker.service` ExecStart | `…/backend/bin/worker` | 一致 |
| api MainPID / 启动 | 3123687 / 2026-09-13 01:42:48 +07 | 一致 |
| worker MainPID / 启动 | 3284415 / 2026-09-13 03:03:24 +07 | 一致 |
| `backend/bin/api` SHA-256 | `d221dfefb8052f8e973b5f068a3b89a998c517f4222e130896887e69554e2e05` | 一致（**旧构建，P0 修复未部署**） |
| `backend/bin/worker` SHA-256 | `c3fe5898e116dcf0406a0b18177df4fc6e32f5158c85a90ae95e5fa7be3a6c3b` | 一致 |
| `backend/bin/migrate` SHA-256 | `225a510fd10d1f2d5db1433e9b393e7c5e37577fcf7c6bbd7825ee900f5a8497` | 一致 |
| `APP_ENV` | `development`（`deploy/.env`） | 一致（切流前必须改 production） |
| 部署目录 `.git` | 不存在（rsync 目录） | 一致 |
| 顶层 `bin/` | 存在（api 50,347,852 B，Sep 8 22:37）—— 遗留，**不得使用** | 一致 |

**部署机与 Windows 工作树的 DR 脚本逐字节一致（4/4 命中）**：

| 文件 | SHA-256（两侧相同） |
|---|---|
| `scripts/backup/backup-all.sh` | `3ca1668b55283f0f73d85574f49607ed5ad713eaa75a9487e5f317d1e5a6858c` |
| `scripts/backup/restore-drill.sh` | `acf33911356d337fbddca3a0c61c1e34f0410652077fd2306c4dec33c02c6c0f` |
| `scripts/backup/kill9-drill.sh` | `7ee748d9369d5ddbddfdb095f776bab45af25559d55f67e9ffc16fea81703898` |
| `scripts/backup/README-DR-FIXES.md` | `5fe60ccdc32574d7de66a529784aa613ce0dd6c72f2447320d4f7e7c273a0524` |

部署机上 `bash -n` 三个脚本均 **OK**。
→ 结论：DR 代理的 13 条修复**已真实落盘并同步**，但**从未执行过**（见 §5）。

---

## 2. 【阻断】RPO ≤ 15 分钟在结构上不可能达成（硬证据）

| 证据 | 实测值 |
|---|---|
| `SHOW archive_mode` | **off** |
| `SHOW archive_command` | **(disabled)** |
| `SHOW wal_level` | replica |
| `SHOW archive_timeout` | 0 |
| `pg_stat_archiver` | last_archived_wal 为空、archived_count=**0**、failed_count=0 |
| 备份调度 | **不存在**。`systemctl --user list-timers` 只有 `launchpadlib-cache-clean.timer` 与 `blockchain-containers-watchdog.timer`；`crontab -l` 无 backup 项 |
| 现存备份目录 | 仅 `/home/euripides/backups/blockchain-platform/20260912T171120Z`（9.9 GiB，finished 2026-09-12T17:33:26Z） |
| 观测时刻 | 2026-09-13T03:28:14+07（= 2026-09-12T20:28Z） |

**实测 RPO = 2 小时 55 分，且无调度 → 随时间是**无界**的**（不是"接近 15 分钟"，是"没有任何机制把它压到 15 分钟以内"）。
且**从未执行过任何 PITR 恢复**：没有 WAL 归档，就没有可恢复的恢复点，RPO 只能是"距上次成功备份的间隔"。

> 计划 §30/§27 DATA-009 要求 RPO ≤ 15 分钟、RTO ≤ 4 小时。
> **RTO 侧有历史实测（PG 5.81 s / CH 561 s）；RPO 侧 0 证据且当前配置下不可达。**

---

## 3. 【阻断】最近一次备份由**修复前脚本**产出，且可证明不完整

`20260912T171120Z/manifest.json` 实测：

- **缺失** `complete` / `incomplete_reasons` / `pg_snapshot_consistent` / `consistency` 字段
  → 这些正是 13 条修复新加的字段；该备份**根本无表达能力声明自己是否完整**。
- `minio`：`{"blockchain-audit": null, "blockchain-reports": null, "blockchain-exports": null}`
  → `object_count` **全为 null**，正是缺陷 #2（读取路径错误）在生产产物上的实锤。
- 只覆盖 **3 个桶**：audit / reports / exports。
- **`blockchain-raw` 与 `blockchain-backups` 未纳入**。
- 无 `__FAILED` / `__INCOMPLETE` / `__INCONSISTENT` 标记 —— 但修复前脚本也不会写这些标记，所以"无标记"不构成完整性证据。

### 3.1 DR 真实范围（实测，决定 RTO/容量可行性）

| 存储 | 实测 |
|---|---|
| `blockchain-raw` | **14 GiB / 170,515 objects**（未备份） |
| `blockchain-exports` | 5.0 MiB / 28 objects |
| `blockchain-reports` | 113 KiB / 68 objects |
| `blockchain-audit` | 1.9 KiB / 4 objects |
| `blockchain-backups` | 0 B / 0 objects |
| PG `blockchain_platform` | 854 MB |
| CH 最大表 | transactions 5.63 GiB / 35,986,150；address_activity 5.43 GiB / 74,454,629；token_transfers 4.51 GiB / 56,605,708；logs 467 MiB / 11,901,727 |
| 磁盘 | 147 G 总 / 79 G 已用 / **61 G 可用（57%）** |

→ 加上 `blockchain-raw` 后备份体积将从 9.9 GiB 升至 ~24 GiB 量级；磁盘余量足以做一次，但"演练目标不自动清理"会持续占用（实测已残留：PG 侧 `blockchain_platform_dr_20260912t175029z` 635 MB、`blockchain_platform_dr_restore` 630 MB、`blockchain_platform_rehearsal` 569 MB、`blockchain_platform_s7_migtest` 17 MB；MinIO 侧残留桶 `blockchain-reports-drill-20260912t175029z`）。

---

## 4. 【阻断】"NV / PARTIAL 不阻断退出码"——代码级实锤

### 4.1 `restore-drill.sh`：**结构上永远 PASS 且 exit 0**

| 位置 | 实测内容 |
|---|---|
| `restore-drill.sh:736` | `not_verified "版本历史 / 生命周期规则 / 对象元数据与 tag（…）"` —— **无条件执行** |
| `restore-drill.sh:863-866` | `if [ "$FAIL_N" -gt 0 ]; then … exit 1; fi` |
| `restore-drill.sh:868-869` | `echo "演练判定：PASS（NOT_VERIFIED 项不计入通过；见报告）"` / `exit 0` |
| `restore-drill.sh:824` | result.json `"verdict": "FAIL" if int(fail_n) > 0 else "PASS"` —— **NV 对机器判据完全不可见** |

→ 因为第 736 行无条件写入，`NV_N` **恒 ≥ 1**，所以该脚本在任何一次成功执行里都会打印
`演练判定：PASS` 并 `exit 0`，无论 NV 内容多严重。这正是"以局部 PASS 冒称 S7-DR 完成"的路径。

### 4.2 `kill9-drill.sh`：NV 不影响退出码，PARTIAL **完全不可机读**

| 位置 | 实测内容 |
|---|---|
| `kill9-drill.sh:705-711` | `FAIL_N>0 → exit 1`；否则 `echo "演练判定：PASS（NOT_VERIFIED 项不计入通过）"` / `exit 0` |
| `kill9-drill.sh:81-82` | 基线截断只写 `$WORK/truncated.txt` + `echo "WARN: …" >&2` |
| `kill9-drill.sh:461-462` | checker 仅给该不变量行追加文本 ` [PARTIAL：基线被截断，核对不完整]` |
| `kill9-drill.sh:684-686` | 截断只在**结论之后**打印一条 stderr WARN |

- PARTIAL **不写入** `NOT_VERIFIED.txt`、**不进入** `FAILURES.txt`、**不改变**退出码。
- 该脚本**根本不生成 `result.json`**（只有 `$EV/kill9-$TS.md` 与 `$WORK/`）→ 没有任何机器可读的 NV/PARTIAL 面。
- 因此：**基线被截断（默认上限 200000 行）+ 所有不变量"通过" ⇒ exit 0 + PASS**，
  而"核对其实只覆盖了前缀"这件事只存在于人眼可读的 Markdown 里。

### 4.3 `backup-all.sh`：这一处是**正确**的

`backup-all.sh:502-513`：`INC != 0` 时打印 `__INCOMPLETE` 明细并 `exit 1`，到不了第 582 行的 `exit 0`。
→ backup-all 的退出码门禁**成立**；缺陷集中在两个 drill 脚本。

### 4.4 缺少聚合 gate

除上述三脚本外，**不存在**任何聚合 S7-DR 门禁脚本（`scripts/s7/` 下无 DR gate）。
→ 当前没有任何一处会把 `NV>0` / `PARTIAL>0` 转成"整体验收非 0"。

---

## 5. 【阻断】现存演练证据由**修复前脚本**产出，且其中存在**自相矛盾的 PASS**

### 5.1 证据双向不同步（Windows 与部署机各缺一半）

| 文件 | 部署机 | Windows 工作树 |
|---|---|---|
| `sprint7/dr/restore-drill-20260912T175029Z.md` | **存在**（5,512 B） | **缺失** |
| `sprint7/dr/kill9-20260912T180409Z.md` | **存在**（1,759 B） | **缺失** |
| `sprint7/dr/pg-restore-20260912T163022Z.counts.txt` / `.rto.txt` | **存在**（760 B / 28 B） | **缺失** |
| `sprint7/dr/README.md` | **缺失** | 存在（6,597 B） |

### 5.2 这些报告产自修复前脚本，**不能作为新版本验收证据**

时间线：演练报告 2026-09-12T17:50Z / 18:04Z；13 条修复落盘约 2026-09-12T21:16Z（`README-DR-FIXES.md` mtime 2026-09-13 04:16 +07）。
演练跑的是**修复前**脚本（含 DROP/CREATE、自动清理、总数比对、假幂等）。
→ **新版本（`acf33911…` / `7ee748d9…`）至今 0 次真实执行、0 份报告、0 条 NV/PARTIAL 记录。**

### 5.3 报告内部的假 PASS（无需重跑即可判定）

**(a) `restore-drill-20260912T175029Z.md` 宣告 PASS，但：**

- ClickHouse 用 `RESTORE … FROM Disk('backups',…)`，而 `README-DR-FIXES.md` #6 已确认本实例
  `system.disks` 只有 default、未配置 `backups.allowed_disk`，`Disk('backups',…)` 会直接 `INVALID_CONFIG_PARAMETER`；
  且报告里 CH 行数**不相等**：`token_transfers` 56,600,036 → 56,220,071（−379,965）、
  `address_activity` 74,448,957 → 68,637,325（−5,811,632），标记为 "-" 却仍计入通过。
  `README.md` §2.1 用 ReplacingMergeTree 的 `count()` vs `count(FINAL)` 解释，
  **但从未给出"恢复库 count(FINAL) == 生产 count(FINAL)"的等式** —— 差异始终未被闭合。
- **演练库被自动删除**（`已在核对后删除`）→ 恰恰销毁了唯一能闭合上述差异的现场。这与新增的"绝不自动清理"要求直接冲突。
- MinIO 只恢复并比对 `blockchain-reports`（60/60），audit / exports / raw / backups **未恢复、未比对**，仍写"通过"。
- RPO 段自认**未达标**（"当前为手工/按需备份；无调度、无 WAL 归档…**未达标**"）。
- `blockchain-raw`（现 14 GiB / 170,515 对象）自认**未纳入**。

**(b) `kill9-20260912T180409Z.md` 存在**自相矛盾**的 PASS：**

- §3 明文记录 NATS：`恢复耗时 = 未恢复 s`、`容器状态=exited`；
  §4 结论却写 `三个目标均由编排自动拉起，且数据侧不变量满足` —— **与 §3 直接冲突**。
- §1 / §3 的计数多处为空字符串（`RUNNING 作业 ，`、`outbox 表 ，`）→ 查询失败被吞，脚本未失败。
- `残留 IN_FLIGHT 页（幽灵任务）= 0` —— `README-DR-FIXES.md` #11 已确认**代码里不存在 IN_FLIGHT 状态**，该检查恒为 0，属假成功。
- `审计事件重复 id：0 → 0（期望 0，消费幂等）` —— 主键唯一性被当作消费者幂等证据（#12 已确认）。
- 无退出码、无 `result.json`、无逐 `(shard,page)` 不变量（只有三个总数）。
  `DURABLE pages` 仅 2，而 `download_pages` 有 165,482 行 → 覆盖面对比度极低。

**(c) 审计封存边界**（此项**正确**，须保持）：
恢复库 `auditverify` 输出 `from_seq 1 / to_seq 3978 / checked 1635 / valid true`，
`integrity_status=sealed_incident`、`prefix_status=sealed_unrecoverable`、`verified_from_seq=2344`、
`sealed_boundary.incident_ref=INC-AUDIT-GAP-1-2343`。**未声称历史完整，未回填，未改锚点。** ✓

---

## 6. 独立判定

| 门禁项 | 判定 | 依据 |
|---|---|---|
| RPO ≤ 15 分钟 | **NOT VERIFIED / 结构性不可达** | §2：archive_mode=off、无调度、无 PITR、实测 RPO 2h55m 且无界 |
| RTO ≤ 4 小时 | **PARTIAL** | 有历史实测（PG 5.81 s / CH 561 s / MinIO 0.63 s），但由修复前脚本产出、演练库已被删、无新版本复现 |
| PG 独立恢复 + 逐表语义 | **PARTIAL** | 历史报告有逐表比对；新脚本未跑；恢复库残留未做闭合 |
| CH 独立恢复 + FINAL 语义 | **NOT VERIFIED** | `count(FINAL)` 恢复侧等式从未给出；历史库已删 |
| MinIO 全桶内容/元数据/版本 | **NOT VERIFIED** | 修复前只做 reports 桶；版本/ILM/元数据结构性不可证 |
| 审计连续可信范围 | **PASS（局部）** | 封存边界正确，2344 起逐项校验有效 |
| Worker/API/NATS Kill9 幂等 | **NOT VERIFIED** | 历史报告自相矛盾；消费幂等维度在库中不存在（需产品侧改造） |
| NV/PARTIAL 阻断整体验收 | **FAIL（门禁缺失）** | §4：两个 drill exit 0；PARTIAL 不可机读；无聚合 gate |
| 备份默认不清理 / 失败保留 | **PASS（代码级）** | retention 默认 0；drill 不自动清理；失败目标保留 |
| 新版本 13 条修复真实执行 | **NOT VERIFIED（0 次）** | §5.2 |

### 总判定：**S7-DR = NOT VERIFIED（阻断整体验收）**

**不得**以 `restore-drill-20260912T175029Z.md` 或 `kill9-20260912T180409Z.md` 作为 S7-DR 通过证据。
**不得**以任一 drill 的 `exit 0` 或 `演练判定：PASS` 作为 S7-DR 通过证据。

---

## 7. 关闭 S7-DR 所需的最小工作集（按依赖排序）

1. **加退出码门禁（脚本层，最小改动）**
   - `restore-drill.sh`：把 NV 分为 `structural`（如版本/ILM/元数据，脚本层不可证）与 `check`（本次核对未完成）；
     仅 `structural` 允许不阻断，且必须写入 `result.json.nv_class`；`check` 类 NV>0 → 非 0 退出。
   - `kill9-drill.sh`：PARTIAL 必须同时写入 `NOT_VERIFIED.txt`（或独立 `PARTIAL.txt`）并参与退出码；
     **新增 `result.json`**（含 failures / not_verified / partial / invariants / truncated 数据集名）。
   - **新增聚合 gate**（如 `scripts/s7/dr-gate.sh`）：读取两份 `result.json`，任一 `verdict != PASS`
     或 `nv_class==check` 非空或 `partial` 非空 → 非 0，并打印逐条原因。
2. **PITR / RPO**：`archive_mode=on` + `archive_command`（归档到 MinIO 独立 prefix）+ `archive_timeout`；
   调度化备份（systemd user timer）+ **真实 PITR 恢复演练**（恢复到任意时间点，断言 RPO 实测值）。
3. **新版本真实演练**（冻结副本 + 唯一 run_dir + 只读，不覆盖运行中脚本）：
   完整 `backup-all.sh`（含 `blockchain-raw`）→ `restore-drill.sh` → `kill9-drill.sh`；
   负向用例（损坏 manifest / 缺文件 / SQL 失败 / 恢复失败）必须**正确非 0 且目标保留**。
4. **闭合 CH 语义**：恢复库 `count(FINAL)` 逐表与 manifest 备份点**精确相等**；不等则 FAIL，不做解释性通过。
5. **MinIO**：逐桶对象键集合/size/etag + 抽样 sha256；版本/ILM/元数据保持显式 NOT_VERIFIED 并**单独出报告**。
6. **消费幂等**：产品侧先落 inbox/offset（记录已处理 event id / Nats-Msg-Id），本演练才能从 NOT_VERIFIED 转 PASS。
7. **证据同步**：把部署机 `sprint7/dr/` 的报告回同步到 Windows 工作树（**只新增、不覆盖**），保持两树一致。
