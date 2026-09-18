# 会话状态与证据索引 — S7 JobScope 回归 / P0 口令轮换（2026-09-13）

> 判定：**整体仍为 NO-GO**。本文件只记录本轮**实际完成**与**仍未闭合**，不构成验收通过声明。

## 1. 部署（已完成，运行身份已核实）

| 项 | 值 |
|---|---|
| 构建证据目录 | `acceptance-evidence/dual-system-v1/sprint7/deploy-fix-20260912T232040Z/` |
| 源码 manifest | 365 文件，sha256 `baa56c84eb67bd62f9ffc31789035382d0eb54f1479f878bf6aa2359bf0f4a0c` |
| api.build | `bc4982058b994f4cdfc914135727a98e35adccb837dfce20abdc587fa4db18d0` |
| worker.build | `68d85c212700fce1d08495061b37e590929f626bf75d41fb479b6bb82d1c6b85` |
| migrate.build | `f37226520410f1206bbd53bb1eab415e9637d54dc1c45a31bd3d2c3e9319553e` |
| 运行 api | MainPID 3742871（轮换后重启），`/proc/<pid>/exe` sha == api.build |
| 运行 worker | MainPID 3742977，`/proc/<pid>/exe` sha == worker.build |
| 旧产物 | 保留于 `backend/bin/.bak-deploy-20260912T232121Z/` |

构建可复现性：从最终源码（`store/jobs.go` `702da337…`、`config/config.go` `281a3d67…` 等）重建得到
**与 /tmp/api.fix2、/tmp/worker.fix2 逐字节相同**的 api/worker，migrate 为**重新构建**的新产物。

## 2. 严格后验脚本的失败与修正（保留全部中间失败）

| 版本 | sha256 | 真实缺陷 | 处置 |
|---|---|---|---|
| v1 `deploy-postverify-strict.sh` | `623d3b5b…` | expected 清单写成 `expected <name> <sha>`，`exp()` 取错列 → **假 MISMATCH** | 输出保留，v2 修正 |
| v2 `deploy-postverify-strict-v2.sh` | `acf484ac…` | 三处循环 `done \| tee` 使 `FAILED=1` 落在 pipeline 子 shell → **假 PASS 风险**；runtime 未查 ActiveState/SubState/PID | 输出保留，v3 修正 |
| v3 `deploy-postverify-strict-v3.sh` | `862968c2…` | 主 shell 聚合 + 状态门槛 + **负向校准**（私有 manifest 故意改错 api 期望 sha → 必须非 0 且 MISMATCH） | **正向 FAILED=0，负向按预期失败** 双证据落盘 |

## 3. R1–R8 作用域回归（已完成，API 层）

专用测试组织 `s7jobscope`（org `5edc0f75…`，ws `s7js-a` 3 个任务 / `s7js-b` 2 个任务，均 `CANCELLED` 惰性测试任务，
`job_no` 前缀 `DL-20260913-9xxx`），身份 `s7js-inv@fixture.local`；夹具由新增 `backend/cmd/fixture-identity` 生成。

证据 `jobs-scope-r1r8-20260912T233134Z/`：**PASS=7 / FAIL=0 / NOT_VERIFIED=1**

| 用例 | 结果 |
|---|---|
| R1 同组织不同工作区 | API 集合 == wsA 3 个 JobID，wsB 泄漏 0 |
| R2 跨组织隔离 | 双向 0 泄漏；ACME 会话 72 == DB 72 |
| R3 组织级空工作区（HTTP） | **NOT_VERIFIED** — 无产品路径产生空 ws 会话（`iam.defaultWorkspace` 取该组织 slug 最小者） |
| R3（store 层） | `go test -tags live`：org 级 5 = wsA3+wsB2，跨组织 overlap 0 |
| R4 请求头扩权 | `X-Org-Id`/`X-Workspace-Id`/`X-User-*` 不改变作用域 |
| R5 status/limit/offset | 8 项全部与 DB 顺序一致 |
| R6 空结果 | `[]` 而非 null |
| R7 真实 UI（v2） | desktop 1440x900 + mobile 390x844 各独立证据；DOM=3 == API==DB，无 wsB 泄漏，真实登录 UI `/app-v2/login` → `/app-v2/home` |
| R8 旧 500 证据 | 保留于 `r3-jobs500-handover-20260912T232500Z/` |

EXPLAIN 证据：R1/R3 查询均走 `download_jobs_identity_scope_idx`（Index Cond 含 org+ws），参数侧 `::uuid` 保索引。

### R7 的两轮证据真实性修正（保留原失败）

- v1（`r7-jobs-scope-ui-20260912T233656Z`）真实缺陷：用 `page.context().request.post('/api/auth/v1/login')` 而非真实登录 UI；
  两个 project 共用同名截图且在每个 project 内 `setViewportSize(390x844)` → 后跑的 mobile 覆盖 desktop，**文件名不能证明桌面证据**。
- v2（`jobs-scope-r7-ui-20260912T234331Z`）修正后 2 passed；投影证据：`user-desktop/*.png` 均 1440x900、
  `user-mobile/*.png` 390x844（fullPage 的 03 为 422x914，视口已在 JSON 记录 390x844、isMobile=true）。

## 4. P0 口令轮换（已完成，旧拒新成 + 数据完整性）

证据 `secret-rotation-20260912T233856Z/`。

- 轮换范围：`POSTGRES_PASSWORD`（role `blockchain`）、`CLICKHOUSE_PASSWORD`（user `blockchain`）、`MINIO_ROOT_PASSWORD`（user `blockchain_admin`）。
  **不含** `blockchain_api`（未暴露，见 `ERRATUM-blockchain-api-credential-exposure-20260913.md`），也不含 Actions/MCP/Provider key。
- 轮换前指纹（sha12）：PG `8cafc51c210a` / CH `8690ac3034f2` / MinIO `c0b83d9d44f5`；新值 len=64、sha12 `481fd4393fd8` / `5c1876afa54a` / `a03a8e5e415e`（**明文只落 0600 文件**）。
- 旧拒新成（真实网络路径 + 真实拒绝原因）：
  - PG：`password authentication failed`（TCP + SCRAM）；新口令业务查询 `download_jobs=2424`。
  - CH：`Code: 516 … AUTHENTICATION_FAILED`；新口令业务读 `token_transfers=56605708`。
  - MinIO：`The request signature we calculated does not match…`；新口令可真实列桶。
- 数据完整性：MinIO 轮换前后递归清单**各 341,381 行 / 114.6MB 逐字节一致**（含 `blockchain-raw` **170,515** 个对象与版本行）；
  CH `system.parts` 物理行数 178,897,405 **不变**。
- 边界（未证明）：轮换前快照 ETag 被擦为 `<e>` → **"内容哈希跨轮换一致"没有前后对照**；
  `post-minio-deep-inventory.txt`（170,706 行 / 170,675 条 stat 记录，含 ETag+user metadata+version-id+ILM）只作为**轮换后基线**。

### 轮换脚本的真实中途失败（保留）

`rotate-exposed-credentials.sh`（sha256 `e36fbc88662e1186bab8e16906a4722218e0fe350ac33197a63f3c032426c1aa`）
在 `[7]` 段 CH 分支真实失败：`chmod: cannot access …/.ch.netrc` + `line 190: $1: unbound variable` → **ROTATION_RC=1**。
根因：NETRC 创建语句被写在**函数外**（顶层），其中 `$1` 未定义，`set -u` 触发退出。
失败前已完成：`.env` 结构化更新、`ALTER ROLE`、CH/MinIO 容器重建、api/worker 重启；**未执行**：CH/MinIO 验证、清单比对、功能回归。
处置：**不重跑轮换**，改用 `verify-secret-rotation.sh`（v1）与 `verify-secret-rotation-v2.sh` 做纯后验（读取原 `deploy.env.pre-rotation.bak` 与本轮新凭据）。原脚本/日志/退出码原样保留。

## 5. 本轮同时修正的仓库明文泄露（前向修复，不改历史）

`.env.example`（git 跟踪）曾含与生产同值的 3 个口令；`config/config.go` 注释、`clickhouse/bench_test.go`、
`run-sprint6-e2e.sh` 亦含明文。已前向脱敏并核对 Windows==VM 字节：

| 文件 | 新 sha256 |
|---|---|
| `deploy/.env.example` | `5ffbf3f639382c1caf82e1f6237a3e59d78e6f2dfa426cde3b488d971d8c0bb1` |
| `backend/internal/config/config.go` | `741c6f9727af8c8288e29f143eebf6a16f86116839be4d0449f6d093ce2afb3d` |
| `backend/internal/clickhouse/bench_test.go` | `f4a78a1c8862ffc12773b03c70d89b40d73e3fbee0e126b70be28efc49583e5f` |
| `tests/run-sprint6-e2e.sh` | `7dbf881a6a35ad421bbfe751c1e04370731f8671d1c12a7578bb537eb81cdc9e` |

## 6. 固定扫描器的 fail-closed 修正（保留全部失败版本）

`build-and-freeze.sh` 原实现的 `SCAN_FAIL` 写在 `{ … } | tee` 大括号块内（pipeline 子 shell），**外层恒为 0 → 假 PASS**。

| 版本 | sha256 | 缺陷 |
|---|---|---|
| `secret-scan.sh` v1 | `1fae56f9…` | 同"子 shell 计数器"模式；`pattern_fingerprints` 用 `/^pattern /` 匹配而 META 前缀是 `key=` → 字段恒空 |
| `secret-scan-v2.sh` | `96c80e62…` | 修正字段匹配，但仍手写 printf 拼 JSON（未安全编码）且把短 required 值静默过滤 |
| `secret-scan-v3.sh` | `d2b97cd5…` | required 值**任何长度都扫描**；DSN 口令 **URL-decode 后真实字节**也扫描；JSON 交 python3 `json.dump` 安全编码 |

校准 `secret-scan-calibrate-v4.sh`（`scanner_sha256=d2b97cd5…`）6 例全部 OK：
C1 干净→0、C2 内嵌 dummy→1、C3 缺 required 键→2（early exit）、C4 产物不可读→1、
C5 **短 required 仍被扫描**→1、C6 **DSN 百分号编码按解码字节命中**→1。accounted=6/6。

真实产物扫描（v3，10 个凭据模式）：冻结 `*.build` 与已安装 `backend/bin/*` **hits=0 CLEAN**；
同一扫描器对**旧发行二进制**返回 **rc=1 HIT** —— 真实正对照。

## 7. 仍未闭合（不得当作完成）

1. **R3 HTTP 组织级空工作区**：NOT_VERIFIED（无产品路径），只有 store 层证据。
2. **产品级工作区切换缺失**：`AppShell` 工作区选择器（计划 §822）、ReactQuery key 含 org/ws 与切换清缓存（§2460）、
   跨 ws 切换审计（§350）、真实跨 org/ws 拒绝+审计（§466）均未实现；`iam.DefaultWorkspace` 仍取"该组织 slug 最小工作区"而非"本人已授权可见工作区"。
   本轮 R1–R8 均为**专用夹具**层级，**不能**视为产品多工作区验收通过。
3. **依赖/镜像**：pgx/chi/excelize/x-crypto 升级、npm xlsx 换官方 tarball+哈希门禁、4 个基础镜像 20C/203H 升级与 digest pin —— 均**未执行**（调研结论已就绪）。
4. **DR 四项**：`restore-drill`/`kill9`/`minio-object-metadata`/`consumer-idempotency` 的 JSON producer 仍未产出。
5. **MinIO 内容哈希前后对照缺失**（见 §4 边界）；CH 逻辑口径只取了 1 张表。
6. `APP_ENV=development` 未切 production；灰度/切流条件仍不足 → **NO-GO**。

## 8. 本轮自身事件（如实记录，不假称已恢复）

- 本轮早期侦察一次性把 `DATABASE_URL` 明文口令打印到会话输出（该值随后已轮换失效）。
- 本轮删除了两个临时失败日志（`/tmp/afx3.log`、`/tmp/afx-$$.log`）——**属证据缺口，不假称可恢复**；后续一律唯一时间戳落盘、不删失败证据。
- `rotate-exposed-credentials.sh` 已在 §4 记录真实中途失败与处置。


---

## 附注（2026-09-13 08:44 +08 追加，**不修改上文任何原文**）

上文第 4 节第 62 行「**不含** `blockchain_api`（未暴露，见 `ERRATUM-blockchain-api-credential-exposure-20260913.md`）」
**已不再准确，现由 `ERRATUM-2-session-channel-app-credential-exposure-20260913.md` 取代**：

- ERRATUM-1 的**二进制 0 命中**结论**继续成立、不予撤回**；
- 但"未暴露"的**外推不成立**：`blockchain_api` 口令确实经**DSH 会话输出渠道**
  （会话 `session-f2c7e043-75c0-476a-8f01-c80b1c04e412`，seq **8699/8700**）泄漏且当时仍有效；
- 该凭据**已真实旋转**（旧拒新成、pool 真重连、内容/进度双重独立证明），证据
  `app-credential-rotation-20260913T000325Z/`；
- 故"已暴露凭据集合"应为 **4 条**，见 ERRATUM-2 §7。

本附注为**追加记录**，上文所有原始失败（含 `-v3.sh` 的 FAILED=1）**原样保留、未改写**。
