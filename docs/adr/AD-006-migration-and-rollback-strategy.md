# AD-006：增量迁移与 expand-compatible 发布

- 状态：Accepted
- 日期：2026-09-12
- 关联：§29.1、§22.1、§28.1 第 6 步

## 决策

1. 迁移只新增，不修改历史文件；`schema_migrations` 按文件名顺序、单文件单事务执行。
2. 迁移是**独立步骤**，API/Worker 启动不隐式执行迁移（`scripts/deploy_dual_system.py` 第 4 步）。
3. 发布顺序固定为：备份二进制 → 同步 → 编译 → 安装单元 → **迁移** → 构建前端 → 重启 → 就绪等待 → 冒烟。
   任一步失败立即中止，不继续。
4. 破坏性变更（删列、删表）不在本批次；`users`、`audit_events` 等旧表原样保留用于对照。
5. 上线前必须先在一次性数据库验证：本次即用 `blockchain_platform_migtest` 跑通 0001→0043 后才动生产库。
   该步骤捕获了 `outbox_events` 与 0028 迁移重名的真实缺陷。

## 回滚

代码回滚：`cp bin/api.bak-dualsystem-<stamp> bin/api && systemctl --user restart blockchain-api.service`。
数据库不提供向下迁移；0040–0043 全部是新增表与触发器，旧二进制可继续运行（expand-compatible）。

## 影响

- 每个部署都会在 `backend/bin/` 留一份回滚二进制，需要人工定期清理。
- 一次性数据库校验应尽快纳入 CI（当前是脚本手动执行，状态 `PARTIAL`）。
