# Sprint 6 报告：后台控制、治理和可观测性

> 分支：`feat/dual-system-iam-foundation`
> 计划：`docs/dual-system-development-plan.md` §19 / §21.6 / §22 / §25 / §27.4–27.7 / §28.8
> 环境：Ubuntu VM `192.168.31.152`（Nginx `127.0.0.1:8088`，API `:8080`，Worker `:8081`，PostgreSQL/ClickHouse/MinIO/NATS 为 Docker Compose 依赖）
> 迁移：`0051_s6_control_governance`、`0052_s6_data_governance`、`0053_s6_permission_grants`、`0054_s6_append_only_hardening`

---

## 0. 结论摘要

Sprint 6 的 9 个任务（S6-DB-01、S6-BE-01..04、S6-FE-01/02、S6-OBS-01、S6-QA-01）已全部实现并部署到验收环境。

| 验收项 | 结果 | 证据 |
|---|---|---|
| API E2E（`run-sprint6-e2e.sh`） | **PASS 111 / FAIL 0 / NOT_VERIFIED 1** | `tests/sprint6/result.json`、`run.log` |
| Playwright（`dual-system-sprint6.spec.ts`） | **8 PASS / 0 FAIL** | `tests/sprint6/playwright-full.log` |
| ADMIN-001 至 ADMIN-010 | 10/10 真实通过 | 见 §4 对照表 |
| AUDIT-002 / AUDIT-003 / AUDIT-004 | 2 PASS + 1 PASS + 部分通过（见 §6 事件） | 同上 |
| OpenAPI 契约检查 | 契约 178 = 后端路由 178，错误模型完整 | `python3 scripts/check_openapi.py` |
| 后端单元/集成测试 | `go test ./...` 全绿 | 见 §7 |

**唯一 NOT_VERIFIED**：每日锚点（`audit_chain_anchors.head_hash`）与其覆盖区间事件行的一致性
无法验证，因为该区间的事件行在 §6 记录的事件中已被清空。这是**如实标注**，不是跳过。

---

## 1. S6-DB-01：配置、审批、保留与备份元数据

计划 §22.1 的 0045/0046 语义因编号顺延落到 **0051 / 0052**（ADR-016 已说明顺延规则），本轮另加 0053 / 0054。

### 0051 `s6_control_governance`

新增：`configuration_settings`、`configuration_revisions`、`feature_flags`、`feature_flag_evaluations`、
`kill_switch_engagements`、`approval_request_events`、`provider_descriptors`、`provider_secrets`、
`routing_policies`、`routing_policy_revisions`；扩展 `api_control_alerts`（+14 列：生命周期、
`priority`、`slo_key`、`links`、`labels`）、`api_control_budgets`（+8 列：维度/动作/阈值/版本）、
`gpt_action_api_keys`（轮换与吊销字段）；为 `approval_requests` 增加
`CHECK (decided_by IS NULL OR decided_by <> requested_by)`（双人约束的数据库兜底）。

### 0052 `s6_data_governance`

新增：`retention_policies`、`retention_runs`、`deletion_requests`、`backup_jobs`、
`restore_previews`、`audit_exports`。基线数据：6 条保留策略全部 `enabled=false`。

### 0053 `s6_permission_grants`

不新增权限键（继续以 `policy.Catalog` 为唯一来源），把 13 个「已声明但只有 owner 持有」的写权限
授予 `platform_admin`（11 个）与 `security_admin`（7 个）。`auditor` 保持只读（DB 可验证）。

### 0054 `s6_append_only_hardening`

见 §6。引入受限运行角色 `blockchain_app` 并回收审计表上的 UPDATE/DELETE 与全表 TRUNCATE。

### 回滚策略

全部为新增表/新增列/新增角色与授权；旧二进制可继续运行（expand-compatible，AD-006）。
回滚代码即可，不需要向下迁移。保留角色 `blockchain_app` 不影响旧二进制（`DB_RUNTIME_ROLE` 为空时行为不变）。

---

## 2. S6-BE-01：控制面迁移到 /api/admin/v1（统一权限、preview/command、审计）

原 `/api/v1/api-control/*` 保留不动（AD-013 兼容），Sprint 6 新增受权限保护的后台控制面：

| 能力 | 端点 |
|---|---|
| 控制状态 / 动作历史 / 策略 | `GET /control/state`、`GET /control/actions`、`GET|PATCH /control/policies` |
| Kill Switch | `POST /control/kill-switch/preview`、`GET|POST /control/kill-switch/requests`、`POST .../{id}/{approve|reject|execute|restore}`、`GET /control/kill-switch/health` |
| 审批中心 | `GET /approvals` |
| 性能控制 | `GET /runtime/performance`、`POST /runtime/performance/preview`、`POST /runtime/performance/commands/{apply|rollback}` |
| 身份补齐 | `POST /users`、`GET /users/{id}/effective-permissions`、`GET /users/{id}/impact-preview` |

统一约束：`s.require(permission)` 逐端点声明；命令必带 `reason` 并写 `audit_events_v2`；
高风险命令要求 `step_up_password`；配置类修改要求 `If-Match`（缺失 428 / 过期 412）。

---

## 3. S6-BE-02 / 03 / 04 与 S6-OBS-01

### S6-BE-02 Provider、路由、密钥、预算与成本

- Provider 描述符：`GET /providers`、`GET|PATCH /providers/{id}`（If-Match）、
  `POST /providers/{id}/commands/{connect|test|disconnect|rotate-secret}`。
  密钥只保存 `secret_ref` + 前 6 位前缀 + SHA-256 指纹，明文永不回显（响应中恒有 `plaintext_available:false`）。
  连接测试使用 `Provider.Health` 受控探针，并写一条 `usage_type=provider_test` 的 usage 记录（含 trace_id）。
  断开先计算影响面（受影响链/Dataset/运行任务/无 fallback 的策略），存在运行任务时必须 step-up。
- 路由策略：`GET /routing-policies`、`GET|PATCH /routing-policies/{id}`（含 `routing_policy_revisions` 历史）。
- API Key：`GET|POST /api-keys`、`POST /api-keys/{id}/commands/{rotate|revoke}`，明文只在创建/轮换响应出现一次。
- 请求审计：`GET /requests`（cursor 分页、provider/status/usage_type 过滤）、`GET /requests/{id}`（含 trace 关联审计数）。
- 成本与预算：`GET /costs/summary`（十进制字符串金额、按 Provider/用途/日拆分）、
  `GET|POST /budgets`、`PATCH /budgets/{id}`（If-Match）、`POST /budgets/{id}/commands/evaluate`。

### S6-BE-03 告警生命周期、功能开关、配置版本

- 告警：`GET /alerts`（按 P0–P3 排序 + `open_p0`）、`GET /alerts/{id}`（返回 Provider→request→job→shard→checkpoint→object 关联链，
  每一层标注命中与否及原因）、`POST /alerts/{id}/commands/{acknowledge|mute|resolve}`（必填 note，状态不允许时 409）。
- 功能开关：`GET|POST /feature-flags`、`PATCH /feature-flags/{key}`（If-Match）、
  `GET /feature-flags/{key}/evaluations`、`POST .../commands/{evaluate|rollback}`。
  灰度用 `sha256(key:subject_id)` 的确定性分桶，评估结果落库可复现。
- 配置版本：`GET /settings`、`GET /config/revisions`、`PATCH /settings/{key}`（If-Match，secret 类型强制 step-up 且只接受引用名）、
  `POST /settings/{key}/commands/{diff|rollback}`。

### S6-BE-04 保留、删除、备份与恢复（实际删除默认关闭）

- 保留策略：`GET /retention-policies`、`POST /retention-policies/preview`（只统计、不改数据，写 `retention_runs`）、
  `PATCH /retention-policies/{id}`（If-Match）。
- 删除请求：`GET|POST /deletion-requests`（step-up）、`POST /deletion-requests/{id}/commands/{approve|reject|cancel|execute}`。
  `execute` 在 `governance.deletion.execution_enabled=false`（默认）时返回 **412 DELETION_DISABLED**，只登记审批链。
- 备份：`GET|POST /backups`（生成不含秘密的配置快照并写入 MinIO 备份桶，记录 SHA-256/大小/校验状态）、
  `POST /backups/{id}/commands/verify`（重新读取对象并比对 checksum）。
- 恢复：`POST /restores/preview`（影响面 + 冲突计数 + 维护窗口要求）、`POST /restores`（step-up + 双人审批）、`GET /restores`。
- 审计导出：`POST /audit/export`（step-up；JSONL/CSV + manifest + SHA-256）、
  `GET /audit/exports`、`GET /audit/exports/{id}/download`（回读产物以便校验 hash）。

### S6-OBS-01 §25 dashboard / SLO / alerts

- `GET /observability/overview`：健康轨道、API 指标、**按来源拆分的吞吐**（provider_fetch / pipeline /
  job_wall_clock / export / download_events）、队列、告警优先级、成本，全部带 `as_of` 与 `window_start`。
- `GET /observability/slo`：9 条 SLO 全部登记（§25.3），每条返回 target/current/status/samples/source/query_status。
- `GET /observability/alerts`、`POST /observability/alerts/evaluate`：按 §25.4 生成 P0–P3 告警（fingerprint 幂等）。
- `GET /observability/metrics`：`cs_*` 指标族直读（含新增的 HTTP/IAM/审计链路指标）。
- 新增运行时指标：`cs_http_requests_total`、`cs_http_request_duration_seconds`（P50/P95/P99）、
  `cs_http_inflight_requests`、`cs_http_rate_limited_total`、`cs_http_auth_failures_total`、
  `cs_login_total`、`cs_session_revocations_total`、`cs_session_revoke_latency_seconds`、
  `cs_audit_export_duration_seconds`、`cs_audit_write_duration_seconds`、`cs_alert_evaluations_total`、
  `cs_process_start_time_seconds`。
- §25.1 结构化日志字段补齐：`service/environment/build_sha/route_template/request_id/trace_id/actor_id/
  organization_id/workspace_id/error_code`（actor 通过请求级槽位在鉴权后回填）。

---

## 4. S6-FE-01 / S6-FE-02：后台页面

新增 16 个页面（全部代码分割，独立 chunk）：

| 页面 | 路由 | 权限 |
|---|---|---|
| Provider 账户 | `/providers` | admin:provider:read |
| Provider 详情（7 Tab） | `/providers/:provider` | admin:provider:read |
| 路由策略 | `/providers/routing` | admin:routing:read |
| 性能控制 | `/runtime/performance` | admin:performance:read |
| API / MCP Key | `/api-keys` | admin:apikey:read |
| 请求审计 | `/requests` | admin:request:read |
| 成本 | `/costs` | admin:cost:read |
| 预算 | `/budgets` | admin:budget:read |
| 告警中心 | `/alerts` | admin:alert:read |
| 系统设置 | `/settings` | admin:config:read |
| 功能开关 | `/feature-flags` | admin:flag:read |
| 实时控制面 | `/control` | admin:killswitch:read |
| Kill Switch | `/control/kill-switch` | admin:killswitch:read |
| 备份与恢复 | `/system/backups` | admin:backup:read |
| 保留与删除 | `/data/retention` | admin:retention:read |
| 版本与迁移 | `/system/version` | admin:version:read |
| 可观测性 | `/observability` | admin:metrics:read |

`/overview` 增加可观测性区块（P0/P1 告警计数、健康轨道、队列、吞吐拆分、近期高风险动作）。

**共享基础设施**：`src/lib/adminApi.ts`（`useAdminList` / `useAdminResource` / `useAdminCommand` /
`useAdminPatch` / 展示辅助）、`src/components/s6.tsx`（Tabs / KeyValueList / Timeline / MaskedSecret /
DiffList / JsonBlock / 表单控件）。

**规范遵守**：所有警告、说明、成功、错误、风险与确认提示只通过 `useDialog()` 弹窗呈现；
页面正文不含横幅/提示卡/说明段落（Playwright 用例逐页扫描 `[role=alert]`、`.banner`、`.alert`、
`.notice`、`.callout`、`.toast-inline` 并断言为空）。按钮统一 `disabled={!can(permission)}`，
并在 handler 内二次校验后才发请求。

---

## 5. 与 §27.4 验收矩阵的对照

| Test ID | 场景 | 结果 | 关键证据 |
|---|---|---|---|
| ADMIN-001 | 创建用户并绑定工作区角色 | PASS | 创建返回一次性密码；DB `identities`/`workspace_memberships`/`member_role_bindings`；有效权限来自角色来源；新账号真实登录；`admin.user.create` 审计 |
| ADMIN-002 | 停用用户影响预览 | PASS | `impact-preview` 返回会话/任务/审批/案件/密钥；无 `confirm` → 428；确认后 DB `status=disabled` 且会话全部撤销 |
| ADMIN-003 | Provider 连接测试 | PASS | 受控探针 `Provider.Health`、trace_id、`provider_usage` 新增 1 条 `provider_test`、密钥列表只有摘要 |
| ADMIN-004 | Provider 断开有受影响任务 | PASS | 影响面（链/Dataset/运行任务/无 fallback 策略）+ step-up（存在运行任务时缺密码 → 403），DB 状态 `disconnected` |
| ADMIN-005 | 性能模式切换 | PASS | apply 返回 requested 与 DB 一致；回读 requested/effective/reason 三要素非空；审计落库 |
| ADMIN-006 | Kill Switch 双人审批 | PASS | 自审批 403；未审批执行 412；第二审批人通过（DB `decided_by<>requested_by`）；执行后 `kill_switch=true`；恢复后健康检查通过；审批事件落库 |
| ADMIN-007 | 预算超阈值 | PASS | 评估判定 `action_triggered`/P1；生成 `budget:<id>` 告警；登记 `budget_alert` 动作；审计落库 |
| ADMIN-008 | 审计导出 | PASS | 无 step-up → 403；导出 JSONL 行数与计数一致、下载文件 SHA-256 与响应/DB manifest 一致、可解析且含链字段 |
| ADMIN-009 | 配置并发修改 | PASS | 缺 `If-Match` → 428；正确版本 200；复用旧版本 → 412；revision 只 +1；`configuration_revisions` 写入 |
| ADMIN-010 | 功能开关灰度 | PASS | 50% 灰度成功；确定性分桶试算；评估记录落库；回滚后同一用户不再命中；审计落库 |
| AUDIT-002 | 日志秘密扫描 | PASS | 验收日志 0 处明文；审计表 detail/before/after 无 `sk_*`/密码；密钥轮换只存引用与指纹；运行日志 0 处明文 |
| AUDIT-003 | 修改/删除审计事件 | PASS | 运行角色 `blockchain_app`：UPDATE/DELETE/TRUNCATE/DROP 全部拒绝，SELECT/INSERT 正常，受限角色下审计写入仍成功 |
| AUDIT-004 | Hash chain | **部分通过** | 篡改检测与定位、幸存区间逐行一致、链头一致性均 PASS；**每日锚点一致性 NOT_VERIFIED**（原因见 §6） |

---

## 6. 事件记录：AUDIT-003 负向用例暴露并触发了 append-only 缺口

**这是本轮最重要的事实，必须显式记录。**

### 经过

1. AUDIT-003 的原始设计是「以应用 DB 角色执行 UPDATE / DELETE / TRUNCATE，断言被拒绝」。
2. 首次执行时，UPDATE / DELETE 被 0043 的行级触发器拒绝（预期行为），
   但 **TRUNCATE 成功执行**：PostgreSQL 的 `TRUNCATE` 不触发行级触发器。
3. 该 TRUNCATE 真实执行在验收环境上，**清空了 `audit_events_v2` 中 chain='audit' 的
   seq 1–2343（约 2343 条审计事件）**。`login_events` 链路未受影响（1674 条完整）。
4. 后续定位过程中，作者对 `configuration_revisions` 执行过一次 `DROP TABLE` 探针，同样成功；
   该表已通过重放 0051 幂等迁移完整恢复（8 行配置版本）。

### 影响

- 审计链 `audit` 的 seq 1–2343 不可恢复：本环境没有该库的物理备份，MinIO 审计归档只保存
  每日锚点（`anchor.json`，含 head_hash），不含事件本体；导出产物在事件发生前未生成。
- 因此：**全链 `valid=true` 在当前数据上无法达成**，且 2026-09-11 的每日锚点与其覆盖区间
  无法再比对（AUDIT-004 的锚点一致性因此标 NOT_VERIFIED）。
- 链头与幸存区间仍然自洽：从 `min(seq)` 之后的窗口逐行哈希重算全部通过（AUDIT-004d/e）。

### 修复（0054 + 应用代码）

1. **受限运行角色**：新增 `blockchain_app`（NOLOGIN，通过 `SET ROLE` 使用），只授予
   `SELECT/INSERT/UPDATE/DELETE`；审计事件表回收 `UPDATE/DELETE`，全库回收 `TRUNCATE`。
   应用连接池通过 `store.NewWithRuntimeRole` 在每条连接上 `SET ROLE`（`DB_RUNTIME_ROLE=blockchain_app`）。
   之所以必须做角色分离：应用账号同时是表 owner 且在本部署中是超级用户，
   PostgreSQL 对 owner 恒定放行 ACL 检查，`REVOKE` 对其无效；事件触发器也不支持 `TRUNCATE TABLE`。
2. **迁移自检**：0054 在结尾断言运行角色权限矩阵（TRUNCATE/UPDATE/DELETE 必须为 false、
   INSERT/SELECT 必须为 true、`audit_chain_heads` 的 UPDATE 必须保留），任一项不符则迁移失败。
3. **验收改为以运行角色执行**：AUDIT-003 用 `SET ROLE blockchain_app` 执行负向用例，
   并增加正向对照（同角色仍可读、审计写入仍成功）。
4. **发现记录**：本事件作为 S6 的真实缺陷（P1）记入本节；它不是理论风险，而是由验收用例
   在真实环境上触发并修复的。

### 未能达成的验收与后续

- AUDIT-004 的「每日锚点一致」在当前数据上 **NOT_VERIFIED**；
- 若要恢复全链可验证性，需要在 S7 采用新的链边界方案（例如显式记录链断裂边界并重新锚定），
  **不得**通过改写现存行来"修复"历史，这会破坏 append-only 与可检测性本身。
- 权限分离的彻底形态（应用账号不再是 owner/超级用户，使用独立 LOGIN 角色 + 最小授权）
  列入 S7-SEC-01。

---

## 7. 测试与验证

### 本机

```text
cd backend && go vet ./...            -> 0
cd backend && go test ./...           -> 全绿（含 policy catalog 与 SQL 种子一致性测试）
cd frontend && npx tsc -p apps/admin-console/tsconfig.json --noEmit   -> 0
cd frontend && npx vite build --config apps/admin-console/vite.config.ts -> 0（16 个 S6 页面各自成 chunk）
python3 scripts/gen_openapi_index.py && python3 scripts/check_openapi.py -> 契约 178 = 路由 178
```

### 迁移

```text
一次性数据库 blockchain_platform_migtest_s6：0001 -> 0053 全量应用通过；
重放 0051/0052/0053（删除 schema_migrations 记录后重跑）：settings/flags 行数不变（幂等）；
生产库应用 0051..0054 后重放 0051/0052/0054：无副作用。
```

### 验收环境

```bash
# 部署（含迁移、后端编译、双前端构建、重启、健康检查）
python3 scripts/deploy_dual_system.py --yes

# API E2E
ssh euripides@192.168.31.152 \
  'cd ~/projects/blockchain-platform && set -a && . deploy/.env && set +a && \
   . /tmp/s6-e2e-env.sh && export S6_BUILD_SHA=<sha> && \
   bash acceptance-evidence/dual-system-v1/tests/run-sprint6-e2e.sh'

# Playwright（后台桌面 + 移动宽度）
ssh euripides@192.168.31.152 \
  'cd ~/projects/blockchain-platform/frontend && set -a && . ../deploy/.env && set +a && \
   . /tmp/s6-e2e-env.sh && set +a && \
   export E2E_S6_ADMIN_EMAIL=admin@local E2E_S6_ADMIN_PASSWORD="$ADMIN_PASSWORD" \
          E2E_S6_AUDITOR_EMAIL=fixture-auditor@fixture.local E2E_S6_AUDITOR_PASSWORD="$AUDITOR_PASSWORD" \
          E2E_S6_PROVIDER_KEY=ankr-multichain \
          E2E_BASE_URL=http://127.0.0.1:8088 E2E_CHROMIUM_PATH=/usr/bin/chromium-browser && \
   /home/euripides/.nvm/versions/node/v24.20.0/bin/node node_modules/@playwright/test/cli.js test \
     --config playwright.dual.config.ts tests/dual-system-sprint6.spec.ts --project=admin-desktop'
```

`/tmp/s6-e2e-env.sh` 为运维临时文件（导出夹具密码），**不进入仓库**；`ids.env` 只包含 UUID 与邮箱。

---

## 8. 已知限制与后续

1. **AUDIT-004 锚点一致性 NOT_VERIFIED**（§6）。S7 需要给出链边界方案。
2. **step-up 是密码重认证，不是 MFA**（ADR-017 §3）。MFA/TOTP 仍为 `NOT_IMPLEMENTED`（AUTH-004）。
3. `/storage`（A-DATA-03）与 `/data-quality`（A-DATA-02）、`/risk`（A-RISK-01）仍为显式
   `NOT_IMPLEMENTED` 占位页：S6 范围内的数据治理只交付了保留/删除（A-DATA-04）。
4. 备份/恢复只覆盖平台配置快照；PG/ClickHouse/MinIO 的物理备份与 RPO/RTO 演练属 S7-DR-01。
5. 运行期降权角色仍是 `NOLOGIN` + `SET ROLE`；独立 LOGIN 角色与最小授权属 S7-SEC-01。
6. `gauge_rollout` 等开关的灰度评估目前按需触发（页面「试算」/评估接口），尚未接入请求路径的自动评估。

---

## 9. 回滚

```bash
ssh euripides@192.168.31.152 'cd /home/euripides/projects/blockchain-platform/backend && \
  cp bin/api.bak-dualsystem-<stamp> bin/api && cp bin/worker.bak-dualsystem-<stamp> bin/worker && \
  systemctl --user restart blockchain-api.service blockchain-worker.service'
```

迁移全部 additive；如需彻底退回 S5 行为，另需在 `deploy/.env` 中移除 `DB_RUNTIME_ROLE`（旧二进制不识别该变量）。
