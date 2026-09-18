# PITR 演练：当前进度、两轮失败根因与剩余修复清单

> 记录时间：2026-09-13 05:2x +08:00 / 2026-09-12T21:2xZ
> 脚本：`scripts/backup/pitr-drill.sh`（sha256 `5f21a4f3a300e502cc7f955905a4ef033095e00d074f331c7c5d67fa73768889`）
> **判定：PITR 尚未验收（gate 仍 BLOCKED）。本文件不是通过声明。**

## 0. 生产系统完整性（已独立核实）

| 项 | 实测 |
|---|---|
| 生产 WAL 归档 | **16 段**，`/data/blockchain-platform/postgres/wal-archive`，最后 `…FE/…FF` |
| `__moved` 目录 | **不存在** → §9 从未移动过生产归档的任何段 ✓ |
| 归档读取方式 | 目录为 `0700 uid999`，宿主 euripides 无权限 → **只读挂载容器**读取，**未放宽生产目录权限** ✓ |
| PG 归档状态 | `archive_mode=on`、`archive_timeout=60`、`archived` 持续增长 |
| 失败证据 | `pitr-20260912T211627Z.{md,logs}`、`pitr-20260912T211721Z.{json,md,logs}` **全部保留**，未覆盖 |
| 失败容器 | `pitr-restore-20260912T211721Z Exited(1)` **保留**，未删除 |

## 1. 两轮失败的真实根因（都已实测确认，不是推测）

### 第一轮 `20260912T211627Z`
- `pg_basebackup` 走 TCP `127.0.0.1:5433` → `no pg_hba.conf entry for replication connection from host "172.19.0.1"`
  （docker 发布端口会做 SNAT，源地址变成网桥网关）。
- `T1_seq` / `T1_count` 变量大小写写错 → `set -u` 直接中止。

### 第二轮 `20260912T211721Z`
- `pg_basebackup` 改为容器内 unix socket（无需改 pg_hba），但 `-D /var/lib/postgresql/pitr-base-…` **Permission denied**
  （该目录属 `/var/lib/postgresql`，对 `postgres` 用户不可写）。
- §4 等待归档超时：查询写成 `select coalesce(last_archived_wal,'')` **缺 `FROM pg_stat_archiver`** → 语句报错、永远取不到值。
- §5 连续性检查 **检查 0 段**：真实原因是 `BB_START_LSN` 为空（manifest 0 字节，因为 basebackup 失败），
  叠加**宿主 `os.listdir` 对 0700 归档目录无权限** → 归档集合恒为空。
- §6 恢复容器退出 1：**postgres:18 官方镜像的 PGDATA 不是 `/var/lib/postgresql/data`**，实测
  `PGDATA=/var/lib/postgresql/18/docker`；而 `pg_basebackup` 产出的是**扁平**目录，挂到旧路径必然失败。
- 结果：`verdict=FAIL`、8 FAIL / 1 NV，**在基础备份失败后仍继续进入恢复阶段**（错误行为）。

## 2. 本轮已修复（脚本已更新并 `bash -n` 通过，**尚未重跑**）

| # | 修复 | 说明 |
|---|---|---|
| F1 | **fail-fast** | `pg_basebackup` 失败 / manifest 缺失 / Start-LSN 缺失 → **立即写 FAIL JSON 并 exit 1**，绝不进入恢复阶段 |
| F2 | 归档读取经容器 | `CREAD` 只读挂载列目录，落 `$LOGDIR/archive-listing.txt`；**不再用宿主 `os.listdir`**，也不放宽生产目录权限 |
| F3 | 归档查询 | `last_archived_wal` 改为 `… from pg_stat_archiver` |
| F4 | manifest 字段 | `Start-LSN` 取自 `WAL-Ranges[0]`（不在顶层） |
| F5 | 段命名 | 不再自算 LSN→段名，改用服务器 `pg_walfile_name()` 给出首尾段名，再按 `(log,seg)` 每 256 回绕枚举 |
| F6 | **§9 不再动生产归档** | 只读复制所需段到本轮私有 `$LOGDIR/neg-archive`，在**副本**里隐藏一段做缺段负向；并断言生产归档中该段仍在原处 |
| F7 | RPO 符号 | 负 delta 不再 `max(d,0)` 掩盖，输出 `NEGATIVE:` 并由 shell 判 **FAIL** |
| F8 | 恢复布局 | `PGDATA=/pgdata` + `-v $BASE_HOST:/pgdata`，并保存 `restore-container.log` |
| F9 | §9 检查器 | 改为段名比对逻辑，与 §5 一致 |

## 3. **仍未修复**（下一轮必须先做完再重跑真实演练）

| # | 缺口 | 要求 |
|---|---|---|
| G1 | **"到达目标时刻"未被证明** | `pg_is_in_recovery()=f` 只证明 promote。必须解析恢复日志里的
`recovery stopping before/after …` 与 `recovery target reached`，并与**业务 before/after marker** 绑定 |
| G2 | **marker 非原子** | `T1_TS`/`T1_seq`/`T1_count` 分三条查询读取，存在并发窗口；需在**同一 REPEATABLE READ 事务**内取，
且显式记录 target 之前/之后两个 marker，断言边界 |
| G3 | **RTO 字段缺失** | 需记录并输出：恢复启动时刻、恢复完成时刻、业务验证完成时刻 → RTO 三段 |
| G4 | **RPO 语义** | 当前 `目标时刻 − last_xact_replay` 只是**回放精度**，不是"故障时刻 → 可恢复点"的 durability RPO；
需记录模拟 failure 点与**已持久化备份边界**（base backup + 已归档 WAL 覆盖范围） |
| G5 | 跨 timeline | 段枚举只用 start 的 timeline；需要显式声明不支持跨 timeline，或改为按 `pg_walfile_name` 逐段求证 |
| G6 | 恢复实例未验证 | 本次修复后**尚未重跑**；PG18 扁平目录 + PGDATA 显式覆盖是否被入口接受需实测确认 |

## 4. 重跑前置（不可跳）

1. 先修 G1–G4（至少 G1/G2/G3）；
2. 用**唯一 run_dir / 冻结副本**运行，不改运行中的脚本；
3. 基础备份与恢复目标一律新建，**失败也保留**；
4. 不与其它 QA 并发；
5. 生产归档**只读**，任何缺段负向只在私有副本上进行。
