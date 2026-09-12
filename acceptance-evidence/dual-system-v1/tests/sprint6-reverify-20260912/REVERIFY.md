# Sprint 6 部署映射独立复核（2026-09-12）

- 复核触发：外部独立检查报告称运行中的 api / worker 二进制 SHA-256 为
  `33440571…` / `7c2f26b7…`，与验收记录 `59ac3cfd…` / `ac98ce5d…` 不一致，
  且 `systemctl chainsight-api` / `chainsight-worker` 均为 inactive。
- **复核结论：验收记录正确；独立检查取错了二进制路径与 systemd 单元名。
  不存在部署漂移，未做任何重新部署。**
- 复核执行者：独立会话（非首次交付会话），在 VM `dataserver`（192.168.31.152）上实机执行。

## 1 差异归因（两个互不相关的原因）

### 1.1 哈希差异——被检查的是另一个目录里的遗留二进制

VM 上存在两个都叫 `bin/api` 的文件，只有 `backend/bin/` 下的那个是部署产物：

| 绝对路径 | 角色 | mtime | api SHA-256 |
|---|---|---|---|
| `/home/euripides/projects/blockchain-platform/backend/bin/api` | **systemd 实际执行、正在运行** | 2026-09-12 22:54:28 | `59ac3cfd24d0932e0ff77259400b1ebcefa7fcc659e020fd1d306659e976c2e6` |
| `/home/euripides/projects/blockchain-platform/bin/api` | 遗留目录，无任何单元或脚本引用 | 2026-09-08 22:37:48 | `33440571c848795bc787d437d32ec07f6c3621f57d44adddd5be4f440dccf5ad` |

worker 同理：部署产物 `backend/bin/worker` = `ac98ce5d…`；
遗留 `bin/worker` = `7c2f26b77a5fe19863734b502115e4d0cfec91cbced714812977b2a031b87995`。

该遗留目录的成因是可解释的，不是部署脚本写错的路径：

- `scripts/deploy_dual_system.py` 的 `SKIP_DIRS` 包含 `"bin"`，同步时**不会**上传任何 `bin` 目录；
  它的编译步骤把产物写到 `REMOTE/backend/bin/`（`cd backend && go build -o bin/api ./cmd/api`）。
- 因此顶层 `bin/` 既不会被同步覆盖，也不会被部署改写，只会原样留下更早手工构建的产物
  （时间戳早于本次验收部署 4 天）。
- `grep -rn 'blockchain-platform/bin/'` 在 `~/.config/systemd/user/` 与 `deploy/` 下**无任何命中**，
  即该目录不被任何服务或部署路径引用。

### 1.2 单元名差异——服务确实是 systemd 管理的，但单元名不是 `chainsight-*`

`systemctl chainsight-api` / `chainsight-worker` 报 "Unit could not be found" 是预期的：
这两个单元名在系统单元与用户单元中都不存在。真实单元是 **systemd 用户单元**：

```text
$ systemctl --user list-unit-files 'blockchain-*'
blockchain-admin-console.service enabled
blockchain-api.service           enabled
blockchain-mcp.service           enabled
blockchain-user-console.service  enabled
blockchain-vite.service          enabled
blockchain-worker.service        enabled

$ systemctl --user status blockchain-api blockchain-worker
● blockchain-api.service    Active: active (running) since Sat 2026-09-12 22:54:37 +07; Main PID: 2619250 (api)
● blockchain-worker.service Active: active (running) since Sat 2026-09-12 23:01:08 +07; Main PID: 2642013 (worker)
```

`~/.config/systemd/user/blockchain-api.service` 的 `ExecStart` 即
`/home/euripides/projects/blockchain-platform/backend/bin/api`，
`EnvironmentFile` 为 `deploy/.env`，`WorkingDirectory` 为 `backend`。
"运行进程由其他启动方式管理" 不成立：两者的父进程都是 `systemd --user`（PID 1666）。

## 2 溯源链（三重独立证据，任一环可单独复核）

用户要求的核心问题是"运行中的二进制是否精确由已验收 commit 编译"。二进制内**没有** VCS 戳
（见 §4），因此结论只能由"源码逐字节相同 + 编译可复现"两步闭合，本次两步都已实机重做：

1. **运行进程 → 磁盘二进制**：`readlink /proc/<pid>/exe` +
   `sha256sum /proc/<pid>/exe`，与磁盘文件哈希逐字节相同。

   ```text
   port=8080 pid=2619250 exe=/home/euripides/projects/blockchain-platform/backend/bin/api
     proc_exe_sha256=59ac3cfd24d0932e0ff77259400b1ebcefa7fcc659e020fd1d306659e976c2e6
     on_disk_sha256=59ac3cfd24d0932e0ff77259400b1ebcefa7fcc659e020fd1d306659e976c2e6
   port=8081 pid=2642013 exe=/home/euripides/projects/blockchain-platform/backend/bin/worker
     proc_exe_sha256=ac98ce5de71bebb4fa75b9ba9860cda860fd8e1b30a28fe6f928e27e901bbd63
     on_disk_sha256=ac98ce5de71bebb4fa75b9ba9860cda860fd8e1b30a28fe6f928e27e901bbd63
   ```

2. **磁盘二进制 → VM 源码**：在 VM 上用同步后的同一份源码重新编译，产物与部署二进制哈希**完全一致**。

   ```text
   $ cd /home/euripides/projects/blockchain-platform/backend
   $ /usr/local/go/bin/go build -o /tmp/repro-api ./cmd/api
   $ /usr/local/go/bin/go build -o /tmp/repro-worker ./cmd/worker
   59ac3cfd24d0932e0ff77259400b1ebcefa7fcc659e020fd1d306659e976c2e6  /tmp/repro-api
   ac98ce5de71bebb4fa75b9ba9860cda860fd8e1b30a28fe6f928e27e901bbd63  /tmp/repro-worker
   ```

3. **VM 源码 → 已验收 commit `7dba1eb`**：对 `backend/` 下 423 个受版本控制的文件逐个计算
   Git blob 哈希（`sha1("blob <len>\0" + content)`），与 `git ls-tree -r 7dba1eb -- backend`
   记录的 blob 哈希**逐文件比对**：

   ```text
   VM_FILES_HASHED=423
   VM_AGGREGATE_SHA256=3d576875a4d487abb70a8305346dccbab5fed65deea87c5254921a4d39ea9341
   MISMATCH_COUNT=0
   MISSING_COUNT=0
   ```

   聚合值由本地对同一 commit 独立计算得到，两侧相同。因此 VM 上的后端源码与
   `7dba1eb` 中提交的字节**完全相同**（无行尾转换、无未提交改动）。

三步串联即得：**PID 2619250 / 2642013 运行的二进制，精确由 `7dba1eb` 的源码编译而来。**

## 3 E2E 复跑（真实 VM，未改动部署）

在同一进程上重跑 `run-sprint6-e2e.sh`，证据写入本目录（**未覆盖** `sprint6/` 下的已验收证据）：

```text
$ bash run-sprint6-e2e.sh        # BASE=http://127.0.0.1:8080, S6_BUILD_SHA=7dba1eb…
=== wrapper start 2026-09-12T23:11:54+07:00 ===
59ac3cfd…  /home/euripides/projects/blockchain-platform/backend/bin/api
ac98ce5d…  /home/euripides/projects/blockchain-platform/backend/bin/worker
api_listen_pid=2619250 exe_sha=59ac3cfd24d0932e0ff77259400b1ebcefa7fcc659e020fd1d306659e976c2e6
==== Sprint 6 E2E 结束 2026-09-12T23:12:15+07:00 PASS=159 FAIL=0 NOT_VERIFIED=0 ====
SUITE_EXIT=0
```

- `result.json`：`pass=159 fail=0 not_verified=0 status=PASS`，判定式 `fail==0 and not_verified==0 and pass>0`。
- 关键点：套件开始前记录了**监听 8080 的 PID 与其 `/proc/exe` 哈希**，
  即 §2 第 1 步证明的就是这同一个进程——API / RBAC / 审计边界端点的调用方与服务方是同一二进制。
- 与已验收 `sprint6/run.log` 的差异仅有易变值（审计链头 seq 3199→3375、被改写 seq 3195→3371）
  与两行输出顺序，其余逐行相同。

本次同时复核了已验收证据自身是否被篡改：`sprint6/` 下 11 个证据文件的 SHA-256
与 `manifest.md` 表格记录的哈希**全部一致**（`1cba6c36…`、`60b181ef…`、`8d5b8ebb…`、
`78cda7f8…`、`93ec5eb2…`、`a4d650c0…`、`3cdee12a…`、`bcdb1fc8…`、`944f285d…`、
`1b42a94f…`、`316d4e31…`）。因此 `sprint6/runtime-verification.txt` 保持字节不变，未作任何编辑。

## 4 已知限制（据实记录，未做无关改动）

部署二进制内不含提交标识：`go version -m backend/bin/api` 显示模块版本为 `(devel)`，
且无 `vcs.revision` / `vcs.time` 构建设置——因为 VM 上的部署目录**不是 Git 仓库**
（`git rev-parse` 报 "not a git repository"），源码由 `scripts/sync_to_vm.py` 以文件形式同步。
这正是"无法只读二进制就断言 commit"的原因，也是本次必须用 blob 清单 + 可复现编译来闭合溯源的原因。
若要让二进制自带提交标识，需要改为带 `-ldflags` 注入或在 VM 保留 Git 元数据，
会改变二进制哈希并触发重新部署，属于本次复核范围之外，故未实施。

## 5 本次复核未改动的内容

- 未重新部署、未重启任何服务：现有部署已被证明正确，重新部署会无谓产生新二进制与新风险。
- 未改动 `sprint6/` 下任何已验收证据字节。
- 未删除顶层遗留 `bin/` 目录：它不被任何单元引用，删除属于独立清理事项，未经确认不做。
- 未改动数据库中的业务数据；E2E 复跑按套件既有设计写入其自身的验收夹具记录。
