# 个人使用备份

每天北京时间 04:30–04:35 自动运行，保留最近 3 份 PostgreSQL 数据库、数据库角色、项目源码、服务配置、案件报告和被案件引用的对象，并把这些小份重要数据同步到原有宿主机共享 `//192.168.31.224/data_bak/personal/blockchain-platform/`，核对文件后两端各保留 3 份。ClickHouse 链上数据库每 7 天更新一次，只保留本机最新一份压缩快照。

备份目录：`/data/backups/blockchain-platform/daily/<UTC时间戳>/` 和 `/data/backups/blockchain-platform/clickhouse/latest.tar.gz`。原始抓取文件、普通导出、测试文件、缓存、构建产物不进入备份；线上业务数据不受备份精简影响。

自动任务：`blockchain-personal-backup.timer`。原每日全量与月度任务已经停用，避免重复运行。日志：`journalctl --user -u blockchain-personal-backup.service`。

手动备份：`python3 scripts/backup/personal_backup.py`。强制更新链上快照：增加 `--force-clickhouse`。检查数据库实际恢复：增加 `--verify-postgres`，该选项会创建独立临时数据库、恢复并计数，然后删除临时数据库。

恢复应用数据库时，先查看 `postgres-roles.sql` 并创建所需数据库角色，再使用 `pg_restore` 恢复 `postgres.dump`。`configuration.tar.gz` 包含数据库连接配置等敏感配置，文件权限仅供本用户读取。`project.tar.gz` 保存源码工作目录，排除编译产物和临时构建目录。

ClickHouse 归档包含原生 BACKUP 目录，将它解包到 ClickHouse 允许的 backups 目录后，可用 `RESTORE DATABASE blockchain ... FROM File('<归档内目录名>')` 恢复到新数据库。不要直接覆盖运行中的业务数据库。

备份临时目录在失败时删除，异常中断留下的临时目录会在下次运行时清理，旧的成功集合仍保留。裁剪按备份时间戳排序，检查删除结果；对象下载以 euripides 的 UID/GID 运行，避免 root 文件导致清理失败。重要数据先同步到宿主机共享，再处理每周链上快照，避免链上备份失败阻断重要数据的异机副本。
