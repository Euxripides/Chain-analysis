# ERRATUM — 关于 `blockchain_api` 角色口令"已暴露"结论的撤回（2026-09-13）

> 本文件为**纠正说明**，不修改、不删除任何旧报告或旧材料。原 SEC 报告与其证据原样保留。

## 1. 被撤回的结论

旧 SEC 报告（`security-evidence-20260912T223257Z`）中写道：
> "完整应用 DSN 含实际 blockchain_api 凭据"（即认为 43 字符的 `blockchain_api` 随机口令内嵌于三个 Go 发行二进制）。

**该结论不成立，现撤回。**

## 2. 两方独立复核（字节级，均不输出明文）

| 复核方 | 方法 | 结果 |
|---|---|---|
| 本会话 | 从 `secret-rotation-20260912T233856Z/deploy.env.pre-rotation.bak` 解析 `DATABASE_URL`，按**值**与**完整 DSN 字节串**对旧发行二进制 `backend/bin/.bak-deploy-20260912T232121Z/{api,worker,migrate}`（sha256 `e0a7c3bd…` / `9c41079d…` / `ab10efe3…`）执行 `grep -a -o -F` 计数 | password hits **0**，full-DSN hits **0**（三个二进制全部） |
| 根监控 | 独立脚本 `verify-original-app-credential-exposure.py`，产物 `.work/dsh-supervisor/original-app-credential-exposure-reverify.json` | 同上：`blockchain_api`（len 120 / password 43 / sha12 `49da98fb5866`）在三个旧 bin 中 password 与 fullDSN **hits 均为 0** |

同时确认**确实暴露**的是另两条：

| DSN | 用户 | 长度 | password sha12 | 旧 bin hits（password / fullDSN） |
|---|---|---|---|---|
| `MIGRATION_DATABASE_URL` | `blockchain` | 90 | `8cafc51c210a` | **1 / 1** |
| `CLICKHOUSE_URL` | `blockchain` | 67 | `8690ac3034f2` | **1 / 1** |

## 3. 误判根因

旧 SEC 把 **90 字符的迁移 DSN**（用户 `blockchain`、口令 17 字符）与 **43 字符的 `blockchain_api` 随机口令**混为一条，
把"migration DSN 被内嵌"错误表述为"应用运行角色口令被内嵌"。

## 4. 处理决定（已执行）

- `blockchain_api` 角色口令**未被暴露** → **不轮换**（按"不得轮换未暴露凭据"的授权边界）。
  其唯一落点是 `deploy/.env`（0600）与本轮两个轮换前配置备份（0600，位于证据目录，属受限保留）。
- 已暴露的两条（`blockchain` 角色的 PG 口令、`blockchain` 的 CH 口令）**已真实轮换**，
  连同 MinIO root 口令共三条，见 `secret-rotation-20260912T233856Z/`。
- 旧 SEC 报告、旧 SBOM、旧扫描原始材料**全部保留**，不重写；本文件作为门禁口径的纠正来源。

## 5. 门禁口径更新

- "已暴露凭据集合"应记为：`POSTGRES_PASSWORD`（role `blockchain`）、`CLICKHOUSE_PASSWORD`（user `blockchain`）、
  `MINIO_ROOT_PASSWORD`（user `blockchain_admin`）；**不含** `DATABASE_URL` 的 `blockchain_api` 口令。
- 凡引用"三个二进制内嵌应用 DSN 凭据"的结论，一律以本文件为准。
