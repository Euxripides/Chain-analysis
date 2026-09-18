# S7-DR-01：备份、恢复与 Kill -9 演练报告

> 执行时间：2026-09-12/13（UTC+7）· 脚本：`scripts/backup/{backup-all.sh,restore-drill.sh,kill9-drill.sh,containers-watchdog.sh}`
> 证据：`acceptance-evidence/dual-system-v1/sprint7/dr/` 与 `.../backups/`

## 1. 备份（backup-all.sh）

| 项 | 实测值 |
|---|---|
| 备份 ID | `20260912T171120Z` |
| 总耗时 | **1326.54 s（22.1 分钟）** |
| 总大小 | 9.9 GiB |
| PostgreSQL | `postgres.dump` 86,867,946 B，SHA-256 `e1af8523…`；迁移版本 57；审计链头 seq 3978 |
| ClickHouse | `clickhouse.tar.gz` 10,532,858,291 B，SHA-256 `6c35489b…`；9 张表；gzip 完整性校验通过；归档内 27,262 个条目 |
| MinIO | 逐桶 `mc mirror` 到本地目录（audit / reports / exports 有对象；backups、raw 为空桶） |
| 清单 | `manifest.json`：逐个文件记录 size + SHA-256，RPO 可由 `finished_at` 直接读取 |

**前置条件缺口与修正（实测发现）**：
- 本实例 `system.disks` 只有 `default`，且未配置 `backups.allowed_disk` →
  `BACKUP … TO Disk('backups', …)` 直接报 `INVALID_CONFIG_PARAMETER`。
  改用 `TO File('<name>')`（对应 config.xml 中的 `backups.allowed_path`）后成功。
- `blockchain-raw`（约 16 GiB，原始链上对象）在该次运行中**未纳入**（脚本当时的桶清单不含它）。
  脚本已修正为默认纳入，可用 `BACKUP_SKIP_BUCKETS=blockchain-raw` 显式排除；
  **本报告的 RPO/RTO 数字不覆盖 raw 桶**，这一点在此显式声明，不作为已覆盖。

## 2. 恢复演练（restore-drill.sh，恢复到独立目标，不触碰生产）

| 组件 | RTO 实测 | 语义核对 | 结论 |
|---|---:|---|---|
| PostgreSQL | **5.81 s** | 16 张关键表逐表行数比对；迁移版本 57 = 57；恢复库上跑 `auditverify`：`valid=true, integrity_status=sealed_incident`（Sprint 6 完整性边界在备份/恢复后保持一致） | 通过 |
| ClickHouse | **561.36 s（9.4 分钟）** | `RESTORE DATABASE … AS …` 退出码 0；`transactions` 35,986,150 = 35,986,150、`logs` 12,345,849 = 12,345,849 **完全一致** | 通过（见下方行数口径说明） |
| MinIO | **0.63 s** | 备份文件 60 / 恢复对象 60（独立 bucket `blockchain-reports-drill-<ts>`） | 通过 |

### 2.1 ReplacingMergeTree 的行数口径（必须按 FINAL 比较）

恢复库中 `token_transfers` / `address_activity` 的 `count()` 小于生产，**不是数据丢失**：

```text
address_activity : count() = 74,448,957   count(FINAL) = 62,774,084
token_transfers  : count() = 56,600,036   count(FINAL) = 54,849,169
```

两张表都是 `ReplacingMergeTree`：`count()` 统计的是**尚未合并的物理行**（含同一排序键的历史版本），
而 RESTORE 会按 part 重建并可能触发合并，因此行数落在 `count(FINAL)` 与原始 `count()` 之间。
**结论：对 ReplacingMergeTree 的恢复一致性判定必须用 `count(FINAL)` 或按分区键抽样比对，
不能用裸 `count()`** —— 该口径已写入 `docs/manuals/operations.md`。

### 2.2 RPO

备份为快照式（非 PITR）。演练时生产库相对备份的**新增行合计 32 行**
（sessions +4、login_events +4、audit_events_v2 +6、provider_usage +18），
即「若在恢复时刻发生故障，会丢失 32 行 ≈ 备份完成后的写入量」。

| 指标 | 计划目标 | 实测能力 | 判定 |
|---|---|---|---|
| RPO | ≤ 15 分钟 | 当前为**手工/按需**备份；无调度、无 WAL 归档。真实 RPO = 距上次备份的时间间隔，未受控 | **未达标**（需调度化备份 + PITR） |
| RTO（PG） | ≤ 4 小时 | **5.81 s**（86.9 MB dump，`-j 4`） | 达标 |
| RTO（CH） | ≤ 4 小时 | **561 s**（约 17 GiB 数据） | 达标 |
| RTO（MinIO） | ≤ 4 小时 | **0.63 s**（reports 桶 60 对象） | 达标 |

## 3. Kill -9 演练（kill9-drill.sh）

| 目标 | 现象 | 恢复耗时 | 数据侧不变量 | 判定 |
|---|---|---:|---|---|
| `blockchain-worker`（systemd 用户单元） | `kill -9` 后由 systemd 自动拉起 | **5.09 s** | checkpoints 19,966 → 19,966（不回退）；DURABLE 页 2 → 2（不丢失）；IN_FLIGHT 残留 0（无幽灵任务）；原 RUNNING 作业仍为 RUNNING | 通过（§27 DATA-006） |
| `blockchain-api`（systemd 用户单元） | `kill -9` 后自动拉起 | **19.2 s**（到 `/health/ready`=200，含依赖探测） | 旧会话在重启后仍可用（200）；审计链 head seq 3,984 → 3,986 单调推进；`audit/verify` 返回 `valid=true, integrity_status=sealed_incident` | 通过 |
| `deploy-nats-1`（docker） | `docker kill -s KILL` 后容器停在 `exited`，**restart=unless-stopped 未生效**，无新日志 | 首次：**未恢复**（人工 `docker start` 后 1.02 s 就绪） | outbox 未发布数 0 → 0；审计重复 id 0 → 0 | **首次 FAIL → 已修复**（见 §3.1） |

### 3.1 NATS 恢复缺口与修复（真实发现）

- 现象：`HostConfig.RestartPolicy.Name = unless-stopped` 但容器未自动重启（`docker inspect` 证据见下）。
- 影响：NATS 是 Outbox/事件分发依赖，`/health/ready` 会因 `nats` 检查失败而整体不就绪。
- 修复：
  1. `docker update --restart=unless-stopped` 对 4 个依赖容器重新声明策略；
  2. 新增 **幂等看门狗**（`scripts/backup/containers-watchdog.sh` + systemd 用户单元
     `blockchain-containers-watchdog.{service,timer}`，每 30s 巡检一次，只对白名单容器执行 `docker start`）。
- **修复后真实故障注入复测**：`kill -9` NATS → 容器 `exited` → 看门狗自动拉起 →
  平台 `/health/ready` 四项依赖全 ok，**端到端恢复 33.37 s**（= 30s 巡检周期 + 启动时间）。

## 4. 结论与遗留

**通过**：PG/CH/MinIO 三组件备份与恢复均真实执行并做语义核对；Worker/API Kill -9 的 RTO 与
数据不变量满足；NATS 恢复缺口已被看门狗闭合且复测通过。

**遗留（P1，需排期）**：
1. **RPO 未受控**：无调度备份、无 WAL 归档/PITR。当前只能靠人工执行 `backup-all.sh`。
   建议：加 systemd timer 每日备份 + `archive_mode=on` + WAL 归档到 MinIO（PITR）。
2. **ClickHouse 备份耗时/体积**：17 GiB → 9.8 GiB 归档、22 分钟；随链上数据增长会线性放大，
   需要增量/分区级备份策略。
3. **blockchain-raw 桶未纳入本次演练**（脚本已修正为默认纳入，但需要在下次演练覆盖并记录）。
4. **看门狗是补偿手段**，根因（docker restart 策略未按预期生效）需要进一步定位。
