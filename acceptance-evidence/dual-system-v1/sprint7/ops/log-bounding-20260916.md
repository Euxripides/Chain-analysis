# 日志不会打满磁盘：日志落盘口径与容量上限（2026-09-16）

> 依据用户验收要求「日志不会打满磁盘」。本文记录**改动前的实测状态**、**改动内容**与**改动后的复算结果**。
> 全部命令可在部署机 `dataserver` 上重放。

## 1. 改动前的实测状态（问题）

| 落盘通道 | 改动前状态 | 风险 |
|---|---|---|
| 服务 `StandardOutput=append:/tmp/*.log` | `/etc/logrotate.d/` 下**没有任何针对 `/tmp/*.log` 的配置**；`/tmp/api.log` 7.97 MB、`/tmp/worker.log` 12.10 MB 且**只增不减** | 无限增长 ⇒ 打满根分区 |
| journald | `/etc/systemd/journald.conf` 的 `SystemMaxUse` 等**全部为注释（无上限）**；实测 `journalctl --disk-usage` = **742.6 MB** | 无限增长 |
| Docker 容器日志 | `daemon.json` 已配置 `log-driver=json-file` + `max-size=50m`/`max-file=3` | ✅ 已有上限（无需改动） |
| `/var/log` 系统日志 | 由发行版 logrotate 管理，实测 1.2 GB | ✅ 已受控 |

同日的磁盘告急（根分区 147 GB 一度只剩 0）由三部分叠加：**服务日志无轮转**、`~/.cache/go-build` 9.1 GB、
`backend/bin.new` 3.0 GB 与 153 个历史构建产物 7.1 GB（后两者已在同日清理并留删除记录）。

## 2. 改动内容

### 2.1 新增 `/etc/logrotate.d/blockchain-platform`

```
/tmp/api.log /tmp/worker.log /tmp/vite.log /tmp/mcp.log
/tmp/user-console.log /tmp/admin-console.log /tmp/containers-watchdog.log
/tmp/backup.log /tmp/drill.log
{
    daily
    size 100M
    rotate 7
    missingok
    notifempty
    compress
    delaycompress
    copytruncate
    su root root
}
```

- `copytruncate` 是必需的：服务由 systemd 以 `append:` 持有文件描述符，重命名方案会让写端继续写到旧 inode。
- 触发条件「每天一次 或 单文件超过 100 M」，保留 7 份并压缩。

### 2.2 新增 `/etc/systemd/journald.conf.d/99-blockchain-platform.conf`

```
[Journal]
SystemMaxUse=500M
RuntimeMaxUse=200M
MaxRetentionSec=2week
```

## 3. 改动后的复算（同一台机器实测）

| 验证项 | 命令 | 实测结果 |
|---|---|---|
| journald 上限生效 | `systemctl restart systemd-journald; journalctl --disk-usage` | **742.6M → 498.8M**（落在 500M 上限内，自行裁剪） |
| logrotate 识别到日志 | `logrotate -d /etc/logrotate.d/blockchain-platform` | 逐个 `considering log /tmp/*.log`，未超阈值时 `does not need rotating` |
| 强制轮转产出归档 | `logrotate -f /etc/logrotate.d/blockchain-platform` | 生成 `/tmp/api.log.1`（7,973,582 B）、`/tmp/worker.log.1`（12,100,060 B）、`/tmp/vite.log.1`、`/tmp/user-console.log.1`、`/tmp/admin-console.log.1`、`/tmp/mcp.log.1`，当前文件截断为 0 |
| 轮转后写入不中断 | 轮转后向服务发请求并观察 | `/tmp/worker.log` 继续增长（44,338 → 66,921 B）；API 实际落 journald（见下） |
| 每日触发 | `systemctl list-timers logrotate.timer` | NEXT = 次日 00:00（UTC+7），LAST = 当日 00:00 |

## 4. 落盘通道与上限（现状台账）

| 服务 | 有效落盘通道 | 上限机制 |
|---|---|---|
| `blockchain-api` | journald（drop-in `10-journald.conf` 覆盖 unit 内的 `append:` 行） | journald `SystemMaxUse=500M` |
| `blockchain-worker` | `/tmp/worker.log`（append） | logrotate（100M/7 份/压缩） |
| `blockchain-user-console` / `blockchain-admin-console` | `/tmp/{user-console,admin-console}.log` | 同上 |
| `blockchain-vite` / `blockchain-mcp` | `/tmp/{vite,mcp}.log` | 同上 |
| 容器（postgres/clickhouse/minio/nats） | docker `json-file` | `max-size=50m` × `max-file=3` = 每容器 ≤150 MB |
| 每日备份 | `/tmp/backup.log` | 同上（logrotate） |

> 说明：`blockchain-api.service` 文件里仍保留 `StandardOutput=append:/tmp/api.log` 两行，但被 `10-journald.conf` 覆盖，
> 实际写 journald（`systemctl --user show -p StandardOutput blockchain-api` → `journal`）。该重复配置不影响容量结论，
> 已在本台账中写明，避免后续误读。

## 5. 结论

- 改动前：**服务文件日志与 journald 都无上限**（用户要求「日志不会打满磁盘」当时**不成立**）。
- 改动后：所有落盘通道都有明确上限（文件 100 M×7 份、journald 500 M、容器 50 M×3），并有每日触发；实测轮转与裁剪均已生效。
- 复算命令见 §3；若后续新增 append 日志文件，需要同步补进 `/etc/logrotate.d/blockchain-platform`。
