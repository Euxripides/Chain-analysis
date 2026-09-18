# ERRATUM-2 — 会话渠道真实泄漏 + 已完成真实旋转（app 运行角色）（2026-09-13）

> 本文件是**追加纠正**，不修改、不删除任何旧报告或旧材料。
> 前序 `ERRATUM-blockchain-api-credential-exposure-20260913.md`（07:44，下称 **ERRATUM-1**）与其全部原始结论**原样保留**；
> 本文件只纠正 ERRATUM-1 中**关于"暴露渠道"的那一步外推**，不改其字节级复核结果。

## 1. ERRATUM-1 仍然成立的部分

ERRATUM-1 §2/§3 的**二进制渠道**结论经两方独立复核，**继续成立、不予撤回**：

- 旧发行二进制 `backend/bin/.bak-deploy-20260912T232121Z/{api,worker,migrate}`
  （sha256 `e0a7c3bd…` / `9c41079d…` / `ab10efe3…`）中，
  `blockchain_api` 口令与完整 DSN 的 **password hits = 0、fullDSN hits = 0**。
- 真正被内嵌的是 `MIGRATION_DATABASE_URL`（role `blockchain`，hits 1/1）与 `CLICKHOUSE_URL`（user `blockchain`，hits 1/1），二者已随 20260912T233856Z 轮换。
- 证据：`.work/dsh-supervisor/verify-original-app-credential-exposure.py` → `original-app-credential-exposure-reverify.json`。

## 2. 被纠正的部分：ERRATUM-1 §4 的"未暴露 → 不轮换"

ERRATUM-1 §4 写道：
> "`blockchain_api` 角色口令**未被暴露** → **不轮换**"

**该结论的外推不成立，现纠正。** "二进制 0 命中"只否证了**发行产物渠道**，
不能外推为"**所有渠道**均未泄漏"。实际存在**第二条真实有效的泄漏渠道：DSH 会话输出本身**。

### 2.1 原始事件（真实、可定位、值不输出）

- 会话：`session-f2c7e043-75c0-476a-8f01-c80b1c04e412`（已封存会话，**原样保留、未修改**）
- 事件：**seq 8699**（`tool/code-dispatch` 参数）+ **seq 8700**（`tool/result`）
- 命令：`grep -E '^(APP_ENV|...|DATABASE_URL_?)=' deploy/.env | grep -vE 'PASSWORD'`
- 缺陷语义：作者以为 `grep -vE 'PASSWORD'` 能过滤口令，但 `DATABASE_URL` **自带内嵌口令**，
  于是 `postgres://blockchain_api:<43字符>@127.0.0.1:5433/...` **明文进入会话输出**。
- 全量扫描 **97 个 DSH 会话日志**：该 43 字符值**仅**出现在这 1 个会话的这 2 个事件。
  扫描器 `.work/dsh-supervisor/scan-stdin-secrets.cjs` / `scan-stdin-target.cjs` **只输出 len/sha12，绝不打印值**。

### 2.2 为什么它是"仍然有效"的真实泄漏

| 项 | 旧（泄漏）值 | 依据 |
|---|---|---|
| 角色 | `blockchain_api` | 会话输出 |
| 长度 | 43 | 扫描指纹 |
| sha12 | `49da98fb5866` | 扫描指纹 |
| 字符集 | 全字母数字 | 扫描指纹 |
| 与当时 `deploy/.env` 是否同类 | **逐字节同类** | 解析 `deploy.env.pre-rotation.bak` |
| 是否被 20260912T233856Z 覆盖 | **否** | 该轮只轮换 `blockchain`(PG/CH) 与 MinIO root |

→ 泄漏时**仍然有效**，且**从未被后续轮换覆盖**。ERRATUM-1 §5 的"已暴露凭据集合不含 `blockchain_api`"因此**不成立**。

## 3. 已执行的补救（真实旋转，非声明）

证据目录 `acceptance-evidence/dual-system-v1/sprint7/app-credential-rotation-20260913T000325Z/`。

| 检查 | 真实结果 |
|---|---|
| 旧凭据被拒 | `FATAL: password authentication failed for user "blockchain_api"`（真实 TCP + SCRAM，rc=2；事后实时复核仍被拒） |
| 新凭据可用 | `SET ROLE blockchain_app; SELECT count(*) FROM download_jobs;` = **2424** |
| 新口令指纹 | len **64** / sha12 **6d9a4d25e36b**（明文只落 0600 文件 `new-app.pw`） |
| `deploy/.env` | 原子改写、权限 0600；sha `fe5b0abc…`（pre）→ `fe8f3eb7…`（post） |
| `DB_RUNTIME_ROLE` | `blockchain_app` **保持不变**（未借机提权/改架构） |
| 服务重启 | `blockchain-api` / `blockchain-worker` / `blockchain-mcp` 均 active；三份 restart `*.err` 大小 0 |
| 真实业务回归 | `/health/ready` = **200** `{postgres:ok, clickhouse:ok, nats:ok, minio:ok}`；`/health/live` = 200；匿名 `/api/v1/download-jobs` = **401** |
| pool 真重连 | 8 个 backend 均在重启后建立（`backend_start > 00:03:31Z`），见 `api-journal-since-restart.txt` |

**架构事实（必须连带记录）**：`blockchain_api` → `blockchain_app` 的成员资格是
**`INHERIT FALSE / SET TRUE`**（ADR-017 运行时角色分离），因此 app 角色**必须 `SET ROLE`** 才有表权限；
直接 `SELECT … FROM download_jobs` 得到 `permission denied` **不是权限缺失**。

## 4. 内容 / 进度保持（双重独立证明）

| 证明 | 工具 | 结果 |
|---|---|---|
| 本会话严格比对 | `scripts/release/strict-content-check-v2.py`（sha `151e00a5075e…`） | rc=0、ok=true、**0 failures**；7 表 0 changed / 0 missing / 0 bad_row / 0 duplicate_pk |
| 负向校准 | `strict-content-calibrate-v2.sh` | **9/9 PASS**，且要求 rc **精确等于 1** 并命中具体规则 ID（C2 `R1 row loss download_shards`／C3 `R4 progress regressed download_shards`（真实 index=17、pk=`0000f915-5fb5-45cc-b8db-76dfb65923c4`）／C4 `S2 POST checkpoints row`／C5·C9 `S3 duplicate pk`／C6 `S1 table set mismatch`／C7 `S1 columns descriptor empty`／C8 `S4 missing dump`）。**python 崩溃 rc=3 不计为通过。** |
| 根独立复核 | `.work/dsh-supervisor/app-rotation-raw-root-proof.json`（由 `verify-app-rotation-raw-root.py` 对**实际 gz 重新 hash**） | jobs **2424** / shards **28385** / checkpoints **19975** / outbox **163**；`platform_jobs`、`backup_jobs` 两空表 PRE=POST **原始字节完全一致**；`job_event_log` **50030 → 50043**，原 50030 个 PK **与整行 100% 保留**，新增 13 |
| identity v2 | `strict-identity-v2.json`（sha `de16d5b7eab7…`） | rc=0、ok=true、0 failures；`raw_identical=True`、pre PK 逐行保留、空表 `empty_dump_proven_valid=True` |

**边界（必须照抄，不得省略）**：
1. PRE/POST 各是**逐表顺序快照，不是单一事务**；
2. POST 是**旋转刚结束时的历史快照，不等于当前 DB 状态**；
3. `strict-identity-final.json` 与 `strict-content-final.json` sha 相同（均 `226c9cf24c38…`）——
   这是 ERRATUM-1 轮 `strict-identity-proof.py` **v1 无 `__main__` 守卫**缺陷的产物，
   **该文件是内容检查的重复输出，不得当作 identity 证据**；identity 证据以 `strict-identity-v2.json` 为准。

## 5. 认证失败事件归属（不夸大）

PG `log_line_prefix='%m [%p] '` 且 `log_connections=off` → **日志不含客户端 IP / application_name**，
归属只能靠**时间戳 + 工具调用记录 + 文件 mtime**，**不能靠 IP**。6 小时内共 9 条：

| PG 时间(UTC) | user | PID | 归属证据 |
|---|---|---|---|
| 23:37:15 / 23:39:49 / 23:40:34 / 23:42:40 | blockchain | 120231/122210/122811/124461 | 20260912T233856Z 轮换 verification-only 的负向测试 |
| 00:03:30.014 | blockchain_api | 141005 | 本轮 body step 6 负向测试；`old-auth-after.txt` mtime 07:03:30.013915 毫秒级吻合 |
| 00:04:08.385 | blockchain_api | 141474 | 工具调用 pwsh「Verify PG auth failure attribution」00:03:57.647 起始，脚本含旧口令拒绝测试；**无落盘文件，属时间区间+脚本内容归属，非 IP 归属** |
| 00:04:23.344 | blockchain_api | 141679 | `verify-old-rejected.txt` mtime 07:04:23.344113 |
| 00:04:41.812 | blockchain_api | 141929 | `verify2-old-rejected.txt` mtime 07:04:41.812524 |
| 00:04:55.848 | blockchain_api | 142118 | `verify3-old-rejected.txt` mtime 07:04:55.847835 |

- **决定性正向证据**：静默窗口 **00:05:14Z–00:05:59Z**（不做任何负向认证）内认证失败 = **0**；
  API/Worker journal 自 00:03:30 起认证错误 = **0**（`verify-rotation-v4-quiet-window.json`，sha `abdef13bc38e…`，`quiet-window-authfailures.txt` 大小 0）。
- **未闭合的诚实表述**：00:04:08.385 那一条**不是由 IP 证明**的归属；不得表述为"已由 IP 证实"。
- **静默窗口的能力边界**：它只能证明**该后窗口内无认证错误**，不能证明"任何时刻无异常认证"。

## 6. 本轮真实缺陷（全部保留原始失败，未改写）

| # | 脚本(sha) | 真实缺陷 | 处置 |
|---|---|---|---|
| 1 | `rotate-app-credential-20260913.sh` `00071b03…` | step5 用 `python3 - "$NEW"` 把新口令放进 **argv**（注释误称 no argv）；step8 仅凭计数宣称 "no task/progress loss" 过强；precheck 未 SET ROLE 而误报"旧凭据被拒" | **未执行 ALTER**，fail-closed 生效；保留 |
| 2 | `-v2.sh` `6dc8fc1b…` | 在 pre-ALTER 网关中止（`columns-pre.json` 大小写不匹配） | **未执行 ALTER**；真实 PRE 快照保留于 `app-credential-rotation-20260913T000233Z/` |
| 3 | `-v3.sh` `52f53c92…` | 实际完成旋转；**退出码 FAILED=1**，原因是自身的 postgres 日志门槛没有排除它自己的负向测试（00:03:30.014） | **FAILED=1 原样保留、未改写** |
| 4 | `verify-…T000325Z.sh` v1/v2/v3 | v1 `VERIFY_FAILED>0` 仍 `exit 0`；v2 list-vs-tuple 崩溃；v3 因自身负向测试误报 3 条 UNEXPLAINED | v4 改静默窗口才给出决定性结论 |
| 5 | `strict-content-calibrate.sh` v1 `95da9f66…` | 开头 `rm -rf "$CAL"` 违反不清理红线 | 事实核查：`strict-calibration/` birth time **07:07:14**（`stat -c %w`），运行前不存在 → **rm 实为 no-op，未删除任何旧证据**；v2 已改唯一 UTC+nonce 目录并在已存在时 fail-closed |
| 6 | Windows→ssh 双重插值 | `OUT` 被展开为空，PRE 落在仓库根下名为空格 `" "` 的目录 `<repo>/ /` | 该目录及其真实文件**已保留、未移动未删除**，仅作缺陷记录 |

冻结脚本 sha256（本地 = VM 逐字节一致）见 `HANDOVER-20260913-0720-dsh-app-rotation.md` §5。

## 7. 门禁口径更新（本文件为权威替换口径）

- **"已暴露凭据集合"应记为 4 条**：
  1. `POSTGRES_PASSWORD`（role `blockchain`）
  2. `CLICKHOUSE_PASSWORD`（user `blockchain`）
  3. `MINIO_ROOT_PASSWORD`（user `blockchain_admin`）
  4. **`DATABASE_URL` 内嵌的 `blockchain_api` 口令（渠道 = DSH 会话输出 seq 8699/8700，**已真实旋转**）**
- 前 3 条见 `secret-rotation-20260912T233856Z/`；第 4 条见 `app-credential-rotation-20260913T000325Z/`。
- **凡引用 ERRATUM-1 §4「`blockchain_api` 未暴露 → 不轮换」与 §5「暴露集合不含 `blockchain_api`」的结论，一律以本文件为准。**
- 仍**不得**把本事件表述为"一切凭据均已失效"：本轮**未**轮换 Actions / MCP / Provider key（无证据表明其泄漏）。

## 8. 方法论外推（写入 gate 口径）

> "**产物 0 命中**" ≡ 只否证**发行产物渠道**；≠ "**凭据未泄漏**"。
> 泄漏面必须**逐渠道**枚举并各自取证：发行二进制 / 构建缓存 / CI 日志 / 会话与 shell 历史 / 配置备份 / 监控与 trace。
> 任一渠道缺失独立取证时，结论只能是 **UNKNOWN**，不得记为"未暴露"。

## 9. 本 ERRATUM 自身边界

- 未修改 ERRATUM-1、旧 SEC 报告、旧失败报告、旧二进制、旧配置备份、封存会话日志；
- 未 TRUNCATE / reset / clean / 删除任何证据；
- 未 commit、未改审计锚点；
- 口令值在本文件、argv、日志、命令输出中**一律只以 len/sha12 指纹出现**。
