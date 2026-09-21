# 封存交接 — S7 JobScope / P0 口令轮换（2026-09-13 06:50 +07）

> **本会话所有写入已停止。** 本文件为脱敏交接：不含任何明文口令，口令一律以 **长度 / sha256 前 12 位** 表示。
> 整体判定：**NO-GO**（切流条件不足）。本文件**不是**验收通过声明，也不假称任何未执行项已完成。

## 0. 封存时点真实运行身份（实测，非沿用旧值）

| 项 | 实测值 |
|---|---|
| 时间 | 2026-09-13T06:49:52+07:00 |
| blockchain-api.service | MainPID **3742871**，active/running，`/proc/<pid>/exe` sha256 `bc4982058b994f4cdfc914135727a98e35adccb837dfce20abdc587fa4db18d0` |
| blockchain-worker.service | MainPID **3742977**，active/running，`/proc/<pid>/exe` sha256 `68d85c212700fce1d08495061b37e590929f626bf75d41fb479b6bb82d1c6b85` |
| `backend/bin/api` | `bc4982058b994f4cdfc914135727a98e35adccb837dfce20abdc587fa4db18d0` |
| `backend/bin/worker` | `68d85c212700fce1d08495061b37e590929f626bf75d41fb479b6bb82d1c6b85` |
| `backend/bin/migrate` | `f37226520410f1206bbd53bb1eab415e9637d54dc1c45a31bd3d2c3e9319553e`（本轮**重新构建**的新产物） |
| 其它常驻 | mcp 1304370、user-console 2619253、admin-console 3626770、vite 3744085、e2e-tls 3261389（均 active） |
| ExecStart | 均为 `/home/euripides/projects/blockchain-platform/backend/bin/{api,worker}`，`EnvironmentFile=deploy/.env`（无参数） |
| 部署机 | `/home/euripides/projects/blockchain-platform` 是 **rsync 目录，无 .git**；Windows 工作树 HEAD `51596f75e0f9472c3e051205b0d0cf2896a3637c`（feat/dual-system-iam-foundation），**dirty 源码原样保留，未 commit / 未 reset / 未 clean** |
| 旧产物 | 保留于 `backend/bin/.bak-deploy-20260912T232121Z/`（api `e0a7c3bd…`、worker `9c41079d…`、migrate `ab10efe3…`） |

## 1. 生产口令轮换（已执行）与**中段脚本错误 + verification-only 边界**

### 1.1 轮换范围与指纹

| 键 | 轮换前 len/sha12 | 轮换后 len/sha12 |
|---|---|---|
| `POSTGRES_PASSWORD`（role `blockchain`） | 17 / `8cafc51c210a` | 64 / `481fd4393fd8` |
| `CLICKHOUSE_PASSWORD`（user `blockchain`） | 17 / `8690ac3034f2` | 64 / `5c1876afa54a` |
| `MINIO_ROOT_PASSWORD`（user `blockchain_admin`） | 20 / `c0b83d9d44f5` | 64 / `a03a8e5e415e` |

轮换动作已在失败前**全部完成**：`deploy/.env` 结构化原子更新、`ALTER ROLE`、CH/MinIO 容器**保卷重建**、api/worker 重启。明文仅落 0600 文件（`new-credentials.env`、`deploy.env.pre-rotation.bak`）。

### 1.2 中段脚本真实失败（保留，不假称干净）

`scripts/release/rotate-exposed-credentials.sh`（sha256 `e36fbc88662e1186bab8e16906a4722218e0fe350ac33197a63f3c032426c1aa`）在 `[7]` 段 CH 分支真实失败：

```
chmod: cannot access '.../secret-rotation-20260912T233856Z/.ch.netrc': No such file or directory
rotate-exposed-credentials.sh: line 190: $1: unbound variable      → ROTATION_RC=1
```

根因：NETRC 创建语句被写在**函数外**（顶层），其中 `$1` 未定义，`set -u` 触发退出。
**未执行**：CH/MinIO 的旧拒新成验证、轮换前后清单比对、功能回归。
处置：**不重跑整个轮换**（避免把已生效的新值当旧值、避免二次不可中断操作），改用 verification-only 脚本读取原 `deploy.env.pre-rotation.bak` + 本轮新凭据做纯后验。原脚本/日志/退出码原样保留。

另记真实门禁缺口：该脚本对 `compose`/`restart` 的 rc 只 echo、**未汇入 FAILED**（v1 的真实缺陷，未热改）。

### 1.3 verification-only 结果（v1 与 v2）

`verify-secret-rotation.sh`（sha256 `502ba4e731e97044305d85cd20c16d38a96a2e72eed89644bed18f293b97281a`）→ **FAILED=0**。
`verify-secret-rotation-v2.sh`（sha256 `66396a3ff0d56c906d983792906c14d08b2a196425bc13227f4dbd01e0038ad4`）→ **FAILED=0**，并补上**真实拒绝原因**（v1 只取 rc/HTTP 码）：

- PG：`password authentication failed`（真实 TCP + SCRAM，非工具/端口原因）；新口令同路径业务查询 `download_jobs=2424`。
- CH：`Code: 516 … AUTHENTICATION_FAILED`（v1 只看到 HTTP 403）；新口令业务读 `token_transfers=56605708`。**SQL 经 stdin，无 argv 口令**。
- MinIO：`The request signature we calculated does not match…`（真实 S3 签名拒绝）；新口令可真实列桶。
- 数据：MinIO 前后递归清单各 **341,381 行 / 114,614,396 B 逐字节一致**（含 `blockchain-raw` **170,515** 对象与版本行）；CH `system.parts` 物理行数 **178,897,405 不变**。

### 1.4 MinIO 元数据：182 stat 是 **SAMPLE**，不等于 full，也不等于跨恢复一致

- v1（`post-minio-deep-inventory.txt`，63,879,231 B）声称的"170,675 条 stat 记录含 ETag+user metadata" **已撤回**：
  该 170,675 条**全部是版本列表行**；`metadata_section_records=0`、`records_with_metadata_field=0`。
  根因：`grep -c '"etag"'` 把版本列表行当 stat；`sed` 按 `"key":"` 匹配而实际 JSON 是 `"key": "`（冒号后有空格）→ 提取 0 个 key。见 `ERRATUM-minio-metadata-coverage-20260913.md`。
- v2/v3 采集器（`collect-minio-inventory-v2.sh` / `-v3.sh`）；v3 修正字段名（`mc stat` 用 `status/name/etag/metadata`，版本号是 `versionId`）。v3 真实计数：
  - **full**：expected/accounted versions **170,675 / 170,675**，`versions_with_versionid`、`versions_with_etag` 均 170,675；
  - **SAMPLE（每桶前 50 个当前对象，显式范围）**：stat 182 次、成功 182、错误 0、含 ETag 182、含非空 user metadata 182；
  - `blockchain-backups` 0 对象 → 该桶 `metadata_verified=false`（不当作已验）；verdict = `METADATA_VERIFIED_SAMPLE`。
- **边界**：没有轮换前的原始 metadata/ETag baseline（旧快照 ETag 已被擦为 `<e>`），**不补造旧 metadata、不重写 scrubbed ETag**。
  `minio-object-metadata` 门禁**仍未闭合** —— 须在 `backup-all` 之前采真实 before 的 versions/ETag/userMeta/ILM，再在 restore 之后比对。

## 2. `blockchain_api`（43 字符）**未暴露**的纠正

旧 SEC 报告称"完整应用 DSN 含实际 blockchain_api 凭据内嵌三 Go 发行二进制" —— **该结论错误，已撤回**。
本会话与根监控**各自独立**按值/完整 DSN 对旧发行二进制字节匹配：password hits **0**、full-DSN hits **0**（三个二进制全部）。
根因：旧 SEC 把 **90 字符迁移 DSN**（user `blockchain`、口令 17 字符，hits 1/1）与 **43 字符 `blockchain_api` 随机口令**混为一条。
→ `blockchain_api` **不轮换**（未暴露），其唯一落点是 `deploy/.env`(0600) 与本轮两个 0600 轮换前配置备份。见 `ERRATUM-blockchain-api-credential-exposure-20260913.md`。

## 3. 部署与构建绑定（已完成）

证据 `deploy-fix-20260912T232040Z/`：源码 manifest 365 文件 sha256 `baa56c84eb67bd62f9ffc31789035382d0eb54f1479f878bf6aa2359bf0f4a0c`；
api/worker 重建结果与旧 `/tmp/api.fix2`、`/tmp/worker.fix2` **逐字节相同**（可复现），migrate 为重建新产物。
严格后验三代（v1 `623d3b5b…` → v2 `acf484ac…` → v3 `862968c2…`）：v1 有**假 MISMATCH**、v2 有 `done | tee` **子 shell 计数器假 PASS 风险**，均保留；v3 主 shell 聚合 + 状态门槛 + **负向校准**（私有 manifest 故意改错 api 期望 sha → 必须非 0 且 MISMATCH）**正向 FAILED=0、负向按预期失败**。

## 4. R7 真实 UI 最终结论（`jobs-scope-r7-ui-20260912T234331Z`）

- 真实浏览器登录 UI：`/app-v2/login`（用户控制台 basename `/app-v2`；根 `/login` 只是旧 V1 SPA fallback，**不能**当已登录证据）→ 提交后真实跳转 `https://127.0.0.1:8448/app-v2/home`；`/api/auth/v1/session` = 200。
- **两个 project 各自独立视口与独立证据**（不共用文件、不在 project 内改 viewport）：
  - `user-desktop/`：viewport **1440×900**、isMobile=false、3 张 PNG 均 1440×900；
  - `user-mobile/`：viewport **390×844**、isMobile=true、PNG 390×844（`03-jobs.png` fullPage 422×914，视口以 JSON 记录为准）；
  - 各自 `r7-ui-evidence.json`：DOM 行数 3 == API 3 == DB 3、`api_matches_db=true`、`leaked_job_nos=[]`、2/2 accounted。
- 早期 v1 run（`…233635Z/…233646Z/…233656Z/…234312Z`）的真实失败与 trace **全部保留**（含"API 假登录 + 同名截图被覆盖"两处缺陷）。

## 5. R1–R8 作用域回归（`jobs-scope-r1r8-20260912T233134Z`）

专用测试组织 `s7jobscope`（org `5edc0f75…`；ws `s7js-a` 3 任务 / `s7js-b` 2 任务，均 CANCELLED 惰性、`job_no` `DL-20260913-9xxx`）＋ `backend/cmd/fixture-identity` 生成的身份。

**PASS 7 / FAIL 0 / NOT_VERIFIED 1**：R1 同组织不同 ws 集合精确匹配、wsB 泄漏 0；R2 双向跨组织 0 泄漏（ACME 72 == DB）；R4 请求头不改变作用域；R5 status/limit/offset 8 项全对；R6 `[]` 非 null；R7 UI（§4）；R8 旧 500 证据保留。
**R3 HTTP 组织级空 ws = NOT_VERIFIED**（无产品路径；`iam.defaultWorkspace` 取该组织 slug 最小者，永不空），store 层 `go test -tags live` 给出 org 级 5 = wsA3+wsB2、overlap 0。
EXPLAIN：R1/R3 均走 `download_jobs_identity_scope_idx`（Index Cond 含 org+ws，参数侧 `::uuid` 保索引）。

## 6. 明文泄露前向修复（不改历史）

| 文件 | 新 sha256 |
|---|---|
| `deploy/.env.example` | `5ffbf3f639382c1caf82e1f6237a3e59d78e6f2dfa426cde3b488d971d8c0bb1` |
| `backend/internal/config/config.go` | `741c6f9727af8c8288e29f143eebf6a16f86116839be4d0449f6d093ce2afb3d` |
| `backend/internal/clickhouse/bench_test.go` | `f4a78a1c8862ffc12773b03c70d89b40d73e3fbee0e126b70be28efc49583e5f` |
| `tests/run-sprint6-e2e.sh` | `7dbf881a6a35ad421bbfe751c1e04370731f8671d1c12a7578bb537eb81cdc9e` |

扫描器 fail-closed 修正：v1 `1fae56f9…`（子 shell 计数 + fingerprints 恒空）→ v2 `96c80e62…` → v3 `d2b97cd5…`（required 任意长度都扫、DSN URL-decode 实字节、JSON 交 python3 安全编码）。
校准 `secret-scan-calibrate-v4.sh` `52c84ab1…`：**6/6 accounted**（C1→0、C2→1、C3→2 early、C4→1、C5 短 required 仍扫→1、C6 DSN 解码命中→1）。
真实扫描：冻结 `*.build` 与已安装 bin **0 命中 CLEAN**；同一扫描器对**旧发行二进制 rc=1 HIT**（真实正对照）。

## 7. 新增脚本 / 产物 / 证据路径与 sha256

**脚本（部署机 = Windows 逐字节一致）**

| 路径 | sha256 |
|---|---|
| `scripts/release/build-and-freeze.sh` | `6802a9ae80864d36962c85bde5aaaecdb629e3cb6c8c23b3de8fe1ad548a9fbb` |
| `scripts/release/deploy-binaries.sh` | `a3014497e9adc433e828a94b8a0e3f9198b6fc28b5145e7630ef3b42653704bb` |
| `scripts/release/deploy-postverify-strict.sh` | `623d3b5b923a9d7e3f8951dc98c46740e77fee8d228eac22dfd1a3471f7938b4` |
| `scripts/release/deploy-postverify-strict-v2.sh` | `acf484ac6b1a42c5ef7a5310c9140608e01eb62bb2115732d0d0a7d02f49cda2` |
| `scripts/release/deploy-postverify-strict-v3.sh` | `862968c299d9f55a596077fbfca50757cf4a90bc6dd0a061a8f9049449b7fb0e` |
| `scripts/release/secret-scan.sh` / `-v2` / `-v3` | `1fae56f9…` / `96c80e62…` / `d2b97cd54315356f0699095fa2811392382f636fbe0203e246663e7ecb96a02d` |
| `scripts/release/secret-scan-calibrate-v4.sh` | `52c84ab107aee930a7521de308c61bddbcf29e5e61fe8212695ba392de5e2aab` |
| `scripts/release/rotate-exposed-credentials.sh` | `e36fbc88662e1186bab8e16906a4722218e0fe350ac33197a63f3c032426c1aa` |
| `scripts/release/verify-secret-rotation.sh` / `-v2` | `502ba4e731e97044305d85cd20c16d38a96a2e72eed89644bed18f293b97281a` / `66396a3ff0d56c906d983792906c14d08b2a196425bc13227f4dbd01e0038ad4` |
| `scripts/release/collect-minio-inventory-v2.sh` / `-v3.sh` | `255f387578f91ecda3ad4d2f21eadbcb8762474b5531b8159f80ad4c27b975ae` / `4548334ad1d4b7db83baf905bd58acb086a86380a4ff83ca02543d70ceeffc5b` |
| `scripts/s7/jobs-scope-regression.sh` | `fffb59163be09ee19f6a868e3b4a6efdebd1cc9b44cf39b018157731171d35d1` |
| `scripts/s7/run-r7-jobs-scope-ui.sh` | `690b79c5c3cc8b1bce926e0825d15229b22245cda48099b47fbbfc2c96b22554` |
| `backend/cmd/fixture-identity/main.go` | `a7025010e0694637f54059f1436ee8563a79a323f9b784c803c1fbc0fd39e779` |
| `backend/internal/store/jobs_scope_live_test.go` | `01494094d40aa41235015b705d5ea95d2c70f9e566c989deab40caf44d13910a` |
| `frontend/tests/dual-system-r7-jobs-scope.spec.ts` | `95ca621505640dfc55c508ba4baa3fdeb63091186e20c5e2ddcdaec35be0dc20` |

**证据目录**

| 目录 | 内容 |
|---|---|
| `acceptance-evidence/dual-system-v1/sprint7/deploy-fix-20260912T232040Z/` | 源码/产物 manifest、BUILD-MANIFEST、secret-literal-scan、postverify-strict 三代输出（含负向夹具 manifest） |
| `acceptance-evidence/dual-system-v1/sprint7/jobs-scope-r1r8-20260912T233134Z/` | R1–R8 run.log / result.json / db-*-ids / EXPLAIN / 索引 |
| `acceptance-evidence/dual-system-v1/sprint7/jobs-scope-r7-ui-20260912T234331Z/` | R7 最终：user-desktop/ 与 user-mobile/ 各自 PNG + JSON |
| `acceptance-evidence/dual-system-v1/sprint7/secret-rotation-20260912T233856Z/` | 轮换全量：pre/post 指纹、MinIO 清单、CH 计数、verify v1/v2、`minio-inventory-v3/` |
| `acceptance-evidence/dual-system-v1/sprint7/ERRATUM-blockchain-api-credential-exposure-20260913.md` | `7d1a9597221bdf5f52876eac36e59ef7ca2cc05917a8bac1f2e010278c0150cc` |
| `acceptance-evidence/dual-system-v1/sprint7/ERRATUM-minio-metadata-coverage-20260913.md` | `b4c854cafff67668023a9f366ceee07e54cc9d5f542b6872a52f93c96f2edc98` |
| `acceptance-evidence/dual-system-v1/sprint7/S7-JOBSCOPE-SECROTATE-STATUS-20260913.md` | `7854b582a0590e99feb479d2a828e628536dd708b552c3ed8d75a0bf55e591fa` |
| `acceptance-evidence/dual-system-v1/sprint7/s7jobscope-seed-20260912T233020Z.log` | 专用测试夹具 seed 日志 |

## 8. 本会话自身事件（如实记录，不假称已恢复）

- 早期侦察一次性把 `DATABASE_URL` 明文口令打印到会话输出（该值已随后轮换失效）。
- 删除了两个临时失败日志（`/tmp/afx3.log`、`/tmp/afx-$$.log`）→ **属证据缺口，不假称可恢复**；此后一律唯一时间戳落盘、不删失败证据。
- `rotate-exposed-credentials.sh` 的真实中途失败见 §1.2。

## 9. 仍未闭合（完整计划，不得当作完成）

1. **产品级工作区切换缺失**：`AppShell` 工作区选择器（计划 §822）、ReactQuery key 含 org/ws 与切换清缓存（§2460）、跨 ws 切换审计（§350）、真实跨 org/ws 拒绝+审计（§466）均未实现；`iam.DefaultWorkspace` 仍按"组织 slug 最小"而非"本人已授权可见工作区"选取。**R1–R8 全部为专用夹具层级，不构成产品多工作区验收**。
2. **依赖未修复**：pgx v5.7.4→5.9.2、chi v5.2.1→5.3.2、excelize v2.9.0→2.11.0、x/crypto v0.54.0→0.56.0（`golang.org/x/crypto/openpgp` GO-2026-5932 无修复版，需书面豁免）；npm `xlsx@0.18.5`（2 HIGH，npm 无修复版）需换官方 CDN tarball 0.20.3 + SHA-512 门禁 + 真实导入/导出回归。调研结论已就绪（含 CVE/可达性/命令），**均未执行**。
3. **基础镜像**：4 个镜像 20 CRITICAL / 203 HIGH 未升级；compose **未 pin digest**（版本 tag 可重指，RepoDigest=`<none>`）。PG 升级须维持 PG18 PGDATA 路径/卷权限/WAL 归档，且**必须用新版本自身** basebackup+WAL 走同一套 PITR gate（不得用旧版本备份"验证"新部署）。
4. **S7-DR 四项 JSON producer 未产出**：`restore-drill-*.json`、`kill9-*.json`、`minio-object-metadata-*.json`、`consumer-idempotency-*.json`；`dr-gate.sh` 仍有 4 条阻断。PITR 222045 仅 **PG 部分** PASS（RPO 51.637s / RTO 11s），不替代 PG+CH+MinIO 全量恢复、Worker/API/NATS Kill9 与真实 outbox/inbox 幂等；RPO≤15min / RTO≤4h 未整体受控。
5. **MinIO 元数据跨恢复对照缺失**（§1.4 边界）；CH 只取了 1 张表的逻辑口径。
6. **§27/§31 其余**：AUTH-004 MFA（`/api/auth/v1/mfa/verify`→501）未实现、AUTH-011 无真实 IdP、AUTH-007、USER-011 mobile、UX 四断点、全 pages/buttons、SSRF 真实负向、SSE/百万行导出/full Provider pipeline 内公网性能、OpenAPI SDK/事件目录/数据字典/容量预测/六手册、CI、功能开关未接入请求路径、`/storage``/data-quality``/risk` 占位页。
7. **审计**：`seq 1–2343` 依 0055 + ADR-018 封存为不可恢复（`sealed_unrecoverable`），可信自 2344 起 —— **不得回填、改锚点或声称历史完整**。
8. **发布**：`APP_ENV=development` 未切 production；灰度/app-v2/admin 权限审计观察、旧页只读、**实际双写观测**、入口切换与回滚验证均未执行 → **NO-GO，不假称已切流**。
9. 删除类操作默认关闭；备份 retention `0`/`APPROVED=0`，不清理，旧失败与旧证据全部保留。

## 10. 封存声明

- 本会话（唯一主工程会话）**所有写入已停止**，此后不再修改任何文件/服务/容器/数据。
- 未删除任何用户数据、证据、备份、容器或历史；未 reset / 未 TRUNCATE / 未改审计锚点 / 未 git 历史重写。
- 未唤醒旧主会话或其子代理；依赖调研子代理已 terminal 且不再使用。
- 生产当前健康：api/worker active、CH healthy、MinIO running、PG healthy、`/health/live`=200、匿名 `/api/v1/download-jobs`=401。
