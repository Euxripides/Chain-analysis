# 申请人工发布窗口（2026-09-16 冻结版本）

| 项 | 内容 |
|---|---|
| 申请事项 | 为冻结版本 `7831bff0…` 申请**人工发布窗口**，按 §16 顺序执行：production 切换 → 灰度 → 入口切换 → 观察 → 签署 |
| 冻结版本 | `backend/bin/api` `7831bff0bc61…`、`worker` `0d49fb75…`、`migrate` `d48e58fe…`、用户台 `71714cdc…`、后台台 `2d4a129e…`、迁移头 `0065`（清单 `release-20260916T125603Z/manifest.txt`） |
| 当前状态 | 自动化验收全绿：sprint4 81/0/0、sprint5 114/0/0、sprint6 159/0/0、rbac 32/0/0、iam 33/0、sprint12 21/0/0、featureflags 21/0、安全负向 97/0/0、密钥扫描 PASS、MFA live PASS、浏览器主档位 37/0、扩展档位 57/0；§27 矩阵 65 条 = **63 PASS + 2 发布阻断**。夜场（同日）新增：备份恢复演练**已真机执行**（PG RTO 58.83 s、CH RTO 477.02 s、MinIO 5 桶逐对象零差异）、镜像漏洞扫描**已执行**（22 CRITICAL / 253 HIGH）、4 个 DR 工具缺陷已定位修复 |
| 发布决定 | **NO-GO for cutover**（`RELEASE-DECISION.md`）；本窗口申请**不是**请求批准上线，而是请求执行"关闭阻断项"的维护窗口 |
| 申请执行人 | 执行方（AI 协作）；**批准人：待指定**（§16.12） |

---

## 1. 本次窗口内需要执行的事项（按依赖顺序）

| 顺序 | 事项 | 对应阻断 | 预计时长 | 前置/风险 |
|---|---|---|---|---|
| 1 | **先做一份带语义基线的新备份**：`scripts/backup/backup-all.sh`（已修复 `[F13]`：MinIO 逐桶 mirror 之后、manifest 之前做只读语义 capture，产出 `bucket-semantics.json`）。当前唯一完整备份 `20260916T150033Z` **不含**该基线（它产出于修复前），因此元数据维度只能等新备份 | B-2（DATA-009） | 35 min | 需磁盘余量；建议 `BACKUP_ROOT=/data/backups/blockchain-platform`（`/` 仅剩 21 GB，`/data` 尚余 647 GB） |
| 2 | **恢复演练单轮闭环**：`RESTORE_DRILL_MINIO_META_VERIFY=1 bash scripts/backup/restore-drill.sh <新备份>` —— 用修复后的 `restore-drill.sh`（`f74540ad…`，保留 ACL + 运行时角色可用性检查 + 就地调用 `minio-metadata-verify.sh verify`） | B-2（DATA-009） | 40–60 min | 演练目标一律新建、不覆盖、失败保留；CH 恢复 ≈ 8 min、MinIO raw 桶 ≈ 5 min（首轮实测） |
| 3 | **PITR**：先开启 WAL 归档（实测当前 `archive_mode=off`、`archive_command=(disabled)`，PITR 不可能执行），再跑 `scripts/backup/pitr-drill.sh` | B-4（RPO/PITR） | 开启归档 + 重启 30 min；演练 30–60 min | 需要归档存储与重启窗口；脚本在私有副本上做缺段负向，全程只读生产归档 |
| 4 | **回滚演练**：`bash scripts/release/rollback-drill.sh`（本夜新增，`ba3f5662…`）：真实回滚 API/Worker 到 `deploy-backups/tron-address-catalog-20260909/{api,worker}`、回滚两套前端到 `dist.prev-*`，测回滚与前滚 RTO，并逐条校验最终状态=冻结版本 | B-5（回滚门禁） | 10–20 min | 需要短时服务重启（演练期间 API/Worker/控制台各中断数秒）；脚本带 trap，异常退出也会把系统恢复回冻结版本 |
| 5 | **消费者幂等**：worker 消费路径落库 processed event id / Nats-Msg-Id（新迁移 + inbox/offset 表），再用修复后的 `kill9-drill.sh`（`a2d9ef98…`，`/jsz` 探针已改为 `?streams=true&consumers=true`）重跑演练 | B-1（DATA-007） | 1–2 天（含开发） | 改动影响 outbox 消费，必须在窗口内 + 有回滚方案；EVENTS 流当前没有任何 durable consumer，服务端不存在重投窗口 |
| 6 | **依赖镜像升级决策 + IaC 处置**：镜像扫描实测 `postgres:18` 14 CRITICAL/106 HIGH、`minio/minio` 6/100、`minio/mc` 2/45、`nats` 0/2、`clickhouse-server:26.7` 0/0（合计 22/253，其中可修 19/188）；IaC 人工核对清单 **115 项 = 41 PASS + 74 FINDING**（日志无轮转、无 healthcheck、`minio:latest` 未固定 tag、systemd 未加固、nginx 未声明 `ssl_protocols`/无限流等）。要么修复，要么逐条书面接受风险 | B-7 | 镜像 2–4 h；IaC 处置 2–4 h | 处置后复跑 `scripts/s7/run-s7-scan-image-iac.sh` 与 `scripts/s7/run-s7-iac-checklist.py` 复算，并重新冻结镜像 digest；`echarts` 1 moderate 同样需要升级或书面接受 |
| 7 | **性能专项**：10k 地址分页/聚合路径优化 + 复跑 `scripts/perf/run-perf-suite.sh` | B-3（§11 预算） | 1–3 天（含开发） | 最差场景 `lan_longtable_p1` p95 8552.8 ms；方案建议：游标分页 + 预聚合/物化视图，先做单地址 10k 基准 |
| 8 | `APP_ENV=production` 切换 + 生产安全配置复核 | B-6（§16.1） | 1 h | **会同时关闭测试夹具与本地验收入口**，因此必须在 1–7 全部完成后执行，且执行后不再跑夹具类套件 |
| 9 | 灰度观察 → 入口切换（`/` 切到新前端）→ 观察一个完整业务周期 → 回滚预案待命 | B-6（§16.11） | 1 个业务周期 | 需要业务方在场与明确的观察指标（错误率、权限拒绝差异、审计覆盖、任务成功率） |
| 10 | 批准人核对证据后签署发布决定 | B-6（§16.12） | — | 只能由具备发布权限的人完成 |

## 2. S7-DR 聚合门禁（`scripts/s7/dr-gate.sh`）的 5 项证据与当前状态

| # | 门禁要求 | 当前状态 |
|---|---|---|
| 1 | `restore-drill-*.json` verdict=PASS、NV=0、PARTIAL=0、5 桶全覆盖、RTO ≤ 4 h | 演练**已执行两轮**：首轮 FAIL（唯一失败项 = D-25 ACL 缺陷，已修）；次轮用修复后脚本重跑。RTO 实测 PG 58.83 s / CH 477.02 s / MinIO 最慢 280.01 s，5 桶内容比对零差异；**元数据维度**仍需"带语义基线的新备份 + `MINIO_META_VERIFY=1`"才能在单轮内归零 |
| 2 | `kill9-*.json` verdict=PASS、NV=0、无截断基线 | 首轮 NV 2 条：`/jsz`（**工具缺陷 D-27，已修**）+ 消费者幂等（**真实产品缺口**）。需按窗口事项 5 落库后重跑；当前 kill9 记录 Worker RTO 5.07 s / API RTO 42.38 s / NATS 14.15 s |
| 3 | `pitr-*.json`：verdict=PASS、`archive_mode=on`、reached_target=true、RPO ≤ 900 s | **未取证**：当前 `archive_mode=off`，PITR 结构性不可能执行（窗口事项 3） |
| 4 | `minio-object-metadata-*.json` verdict=PASS，维度 versions/ilm/tags/metadata 全覆盖 | **未取证**：工具存在，但任何既有备份都没有 `bucket-semantics.json` 基线（流程缺口 D-26，已在 `backup-all.sh` 修复；需新备份后取证） |
| 5 | `consumer-idempotency-*.json` verdict=PASS 且 `table` 非空 | **未取证**：库中不存在消费者去重/投递表（窗口事项 5） |

## 3. 进入窗口前必须已成立的条件（当前成立情况）

| 条件 | 状态 |
|---|---|
| 冻结版本可复算（二进制/前端/迁移头哈希 + 清单脚本） | ✅ `release-20260916T125603Z/manifest.txt` |
| §27 矩阵无软状态（FAIL/BLOCKED/NV/PARTIAL 全 0），阻断项单列 | ✅ `section27-conformance.md` §1/§4 |
| 范围决定成文（OIDC + 三个未接线开关） | ✅ `release-scope-decision-20260916.md` |
| 观察项已归档（O-01/O-02） | ✅ `ROUND-20260916-defect-closure.md` |
| 关键缺陷已关闭（D-01…D-28，含 P0 级：创建用户不可用、Kill Switch 无法恢复、审计链不可验证、evidence 命令卡 60 s、导航未按权限裁剪、权限矩阵有写无读；DR 侧：恢复库业务角色不可用、备份缺 MinIO 语义基线、kill9 `/jsz` 误判、元数据维度无法单轮闭环） | ✅ 见 `ROUND-20260916-defect-closure.md` §1/§4 |
| 审计链与登录链可验证 | ✅ `auditverify` 退出码 0，`audit valid=true sealed_incident declared_anomaly_count=2`、`login valid=true declared_lost_count=8` |

## 4. 明确不在本次范围（已书面决定）

- OIDC / 联邦登录：不接线，保持 `/oidc/*` 501；若纳入范围需真实 IdP 与 AUTH-011 live 负向脚本。
- `admin.provider_registry` / `admin.backup_restore` / `user.gauge_rollout`：不接线（语义决定未定，其中备份恢复属恢复路径，按 ADR-021 不得被开关灰度掉）。
- IaC 扫描：trivy `config` 对本仓库的 IaC 面（`deploy/compose.yaml`、systemd unit、nginx 配置）**检出文件数 0**（用 k8s Pod 对照样本验证工具本身可用，能报 3 条 misconfiguration），因此本轮**不主张** IaC 已扫描通过；需要专用扫描器或人工核对清单，逐条留记录。

## 5. 本申请**不含**的动作

- 不请求上线批准（当前决定是 NO-GO for cutover）；
- 不请求切换 `APP_ENV=production`（按指示：先把矩阵与发布材料整理到没有矛盾，再进生产窗口）；
- 不请求删除任何历史证据或旧结论（旧结论一律以"去向"方式记录在矩阵 §5；本轮失败的演练目标与备份目录一律保留）。
