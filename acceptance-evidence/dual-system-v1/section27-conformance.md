# §27 测试计划一致性对照（全部 65 个 Test ID）

> 生成时间：2026-09-12  ·  build：`feat/dual-system-iam-foundation`

本表是 `docs/dual-system-development-plan.md` §27 要求的「全部 Test ID 结果」。

**编号诚实性说明**：`acceptance-evidence/dual-system-v1/tests/run-iam-e2e.sh` 早期版本曾用
`AUTH-001..010`、`RBAC-001..005` 作为自有冒烟检查的编号，与 §27 同名 Test ID 并不一一对应。
该脚本已改为：仅当语义与 §27 完全一致时才使用文档编号，其余一律改用 `SMOKE-nn`。
本表是修正后的权威结论。

> **2026-09-12 更新（Sprint 3 补齐）**：RBAC-001..008 与跨租户负向测试已执行，
> 见 `tests/run-rbac-e2e.sh`（PASS 29 / FAIL 0 / BLOCKED 2）。
>
> **2026-09-12 更新（Sprint 1/2 遗留验收补齐）**：AUTH-003/005/006/007/008、
> SEC-002/003/004、UX-002/003、DATA-010 由 PARTIAL/NOT_RUN 转为 PASS，
> 见 `tests/run-sprint12-e2e.sh`（PASS 21）、`tests/dual-system-quality.spec.ts`、
> `scripts/test_migration_reentrancy.sh`。
>
> **2026-09-12 更新（Sprint 4 验收）**：USER-001/004/005/006/008/010、AUDIT-001
> 转 PASS；USER-007（Canvas 并发保存）与 USER-009（地址/交易/报告类型搜索）按
> S4/S5 边界保持 PARTIAL；AUTH-011 完成 adapter + httptest 隔离测试，但无真实 IdP，
> live 验收标 NOT_VERIFIED、整体保持 PARTIAL。
> 证据：`tests/run-sprint4-e2e.sh`（PASS 79 / FAIL 0）、
> `frontend/tests/dual-system-sprint4.spec.ts`（桌面+移动 PASS 4）、
> `docs/implementation/sprint4-report.md`。
>
> **2026-09-12 更新（Sprint 5 验收，build_sha=244f63b）**：USER-002/003/007/009/011/012、
> DATA-001 至 DATA-008、UX-004、RBAC-004、RBAC-007 转 PASS。
> 证据：`tests/run-sprint5-e2e.sh`（PASS 104 / FAIL 0 / NOT_VERIFIED 0）、
> `frontend/tests/dual-system-sprint5.spec.ts`（适用 14 PASS / 0 FAIL，13 个项目守卫 skip）、
> `tests/sprint5/{run.log,result.json,files/,screenshots/,runtime-verification.txt}`、
> `docs/implementation/sprint5-report.md`。
> 本轮发现并修复的真实缺陷：后台分析失败静默留 running（占位符错误导致
> advanced materialize 永久失败）、成功 Dialog 阻塞页面跳转、prompt 返回值带
> `input:` 前缀导致对象类型判断错误、证据列表 DOM 过大、viewer 缺 notification:list。

## 统计

| 状态 | 数量 | 含义 |
|---|---|---|
| PASS | 51 | 已执行且达到 §27 预期结果 |
| PARTIAL | 2 | AUTH-011（无 live IdP）、RBAC-008（API Key/scope 路径未覆盖） |
| NOT_RUN | 11 | ADMIN-001..010、DATA-009（S6/S7） |
| NOT_IMPLEMENTED | 1 | AUTH-004（MFA/TOTP） |
| **合计** | **65** | |

## 明细

| Test ID | 场景 | 状态 | 证据 / 缺项 |
|---|---|---|---|
| AUTH-001 | 正确本地账户登录 | **PASS** | run-iam-e2e.sh；HttpOnly Cookie + login_events success |
| AUTH-002 | 错误密码 | **PASS** | 统一错误 + 内部原因码 bad_password + 账号掩码 |
| AUTH-003 | 不存在账号 | **PASS** | run-sprint12-e2e.sh：错误码/提示/retryable 三项完全一致；9 组样本中位响应时间比值实测 1.06x（≤3x） |
| AUTH-004 | MFA 成功/失败 | **NOT_IMPLEMENTED** | TOTP 未实现，/api/auth/v1/mfa/verify 返回 501 |
| AUTH-005 | Session refresh 旋转 | **PASS** | 0045 迁移引入令牌族。run-sprint12-e2e.sh：refresh 生成新会话行；旧令牌重放返回 AUTHN_SESSION_REUSE；新令牌同时失效（家族撤销）；session_reuse_events 落库 |
| AUTH-006 | 用户退出 | **PASS** | run-sprint12-e2e.sh：logout 后同一令牌立即 401 |
| AUTH-007 | 管理员强制下线 | **PASS** | run-sprint12-e2e.sh：两个会话在强制下线后 0 秒内同时失效（SLA ≤5s） |
| AUTH-008 | 新设备登录 | **PASS** | run-sprint12-e2e.sh：iPhone UA 解析为 device_type=mobile；device/os/browser/ip/hash/user_agent 字段齐全；首次见到该设备时 is_new_device=true |
| AUTH-009 | 伪造 X-User-Role | **PASS** | AUTH_MODE=session 下伪造头返回 401；/api/v1/me 为 anonymous |
| AUTH-010 | 伪造转发 IP | **PASS** | 单测 TestResolveClientIPIgnoresUntrustedForwarding；AUTH_TRUSTED_PROXIES 之外一律丢弃 |
| AUTH-011 | OIDC state/nonce/PKCE 错误 | **PARTIAL** | adapter 完成：state HMAC、PKCE S256、nonce、iss/aud/exp、RS256/JWKS 校验，`iam/oidc_test.go` 隔离测试覆盖 happy/tampered state/wrong nonce/tampered signature/expired/alg=none。**无真实 IdP，live 验收 NOT_VERIFIED**；当前生产/灰度 `oidc:false`，`/oidc/*` 501 OIDC_NOT_CONFIGURED |
| AUTH-012 | 开放重定向尝试 | **PASS** | return_to=https://evil.example 被中和为 /home；含 /admin、/app-v2 前缀剥离 |
| RBAC-001 | Viewer 创建案件 | **PASS** | run-rbac-e2e.sh：viewer 调用高权限写操作 401；权限集合不含任何 admin:*；同时具备 dashboard:read 属正向对照 |
| RBAC-002 | 猜测其他组织案件 UUID | **PASS** | run-rbac-e2e.sh：跨组织读对象 404（与不存在 UUID 返回同一状态码，不泄露存在性）；审计记录 authorization.object_denied + deny_reason=cross_organization |
| RBAC-003 | 工作区角色只作用于目标工作区 | **PASS** | run-rbac-e2e.sh：工作区级绑定仅对目标工作区生效——acme-lab 案件 404、acme-other 案件 200 |
| RBAC-004 | 管理员无 evidence 权限读取正文 | **PASS** | run-sprint5-e2e.sh：auditor 无 evidence:read 调用 `/api/v2/evidence/{id}` -> 403；`authorization.denied`（permission_key=evidence:read, result=deny）落库 |
| RBAC-005 | 移除最后一个 Owner | **PASS** | run-rbac-e2e.sh：撤销最后一个 owner 绑定返回 422 CONFLICT_LAST_OWNER，且 owner 绑定未被误删 |
| RBAC-006 | 角色修改后旧 Session | **PASS** | run-rbac-e2e.sh：撤销全部绑定后旧会话 0 秒内 403、权限集合同步收敛；重新授予后 0 秒内恢复（SLA ≤5s） |
| RBAC-007 | Canvas 引用跨案件 Evidence | **PASS** | run-sprint5-e2e.sh：PATCH Canvas 引用他案 evidence -> 422 BUSINESS_CROSS_CASE_EVIDENCE，返回 cross_case_ids，DB 未写入，审计 canvas.evidence_ref.denied |
| RBAC-008 | API Client 访问后台 | **PARTIAL** | 已验：用户层会话访问后台 API/会话列表均 401；investigator 无任何 admin 权限；无凭证 401。**未覆盖 API Key / scope 路径** |
| USER-001 | 新建案件 | **PASS** | run-sprint4-e2e.sh：POST /api/v2/cases + DB cases/case_members/case_activity + audit v2 + 列表复现一致；Playwright 桌面/移动新建案件真实交互通过 |
| USER-002 | 创建调查并 Coverage HIT | **PASS** | run-sprint5-e2e.sh：preview ratio=1、provider_work_required=false；HIT preview+create trace 的 provider_usage=0；调查 ensure_data `reuse=indexed`、checkpoint `data_reused=true`；download_jobs/shards 前后不变 |
| USER-003 | 创建调查并补齐缺口 | **PASS** | run-sprint5-e2e.sh：缺口 shard 越界=0；checkpoint page_no/next_page_token/downloaded_rows 暂停后不回退、resume 后前进；逐地址进度行存在 |
| USER-004 | 暂停/恢复调查 | **PASS** | run-sprint4-e2e.sh：start→paused→resume→waiting_review；暂停后后台未推进、run 数≤1、checkpoint.data_reused=true；本地索引复用 0 Provider 费用 |
| USER-005 | Finding 复核 | **PASS** | run-sprint4-e2e.sh + Playwright：viewer 确认被 403；reviewer verify Evidence 后 confirm Finding→confirmed；finding_evidence_links 至少 1 条 verified；finding:review 审计落库 |
| USER-006 | 证据版本替代 | **PASS** | run-sprint4-e2e.sh：supersede 生成 version=2 新行并关联 supersedes_id；原行 status=superseded 且 content_hash 不变 |
| USER-007 | Canvas 并发保存 | **PASS** | run-sprint5-e2e.sh：第一次保存 revision+1；stale 保存 412 且 details.diff 非空、DB 未覆盖。Playwright 桌面/移动：第二客户端 bump 后 UI 保存 412→字段级 diff Dialog→加载服务端版本 |
| USER-008 | 报告审批下载 | **PASS** | run-sprint4-e2e.sh + Playwright：Investigator 生成/提交→Reviewer approved→signed；report_approvals 绑定 revision+hash；60s 一次性票据下载 200、重复使用 410、文件 SHA-256 与 DB/API 一致；重新生成产生新 revision 不覆盖旧 artifact |
| USER-009 | 全局搜索 | **PASS** | run-sprint5-e2e.sh：case/investigation/job/report/address/transaction 六类均命中；viewer self scope 看不到他人 job；跨工作区案件 404 |
| USER-010 | 通知跳转 | **PASS** | run-sprint4-e2e.sh + Playwright：submit-review 产生 reviewer 通知；open 返回可访问目标；他人打开同通知 404 且要求 notification:list 权限；Inbox 显示已处理复核项 |
| USER-011 | 手机调查查看 | **PASS** | Playwright user-mobile：S5 Canvas 列表视图/SVG、Explorer、下载页真实交互，无页面级横向滚动，Dialog 可关闭；S4 页面回归通过 |
| USER-012 | 键盘完成核心流程 | **PASS** | Playwright user-desktop：纯键盘 Tab 到搜索框→输入→Enter→Tab 到结果链接→Enter；Dialog 焦点陷阱且 ESC 可退出 |
| ADMIN-001 | （§27.4 管理闭环） | **NOT_RUN** | 见文档 §27.4 明细；本批次仅覆盖后台只读页面 |
| ADMIN-002 | — | **NOT_RUN** | 同上 |
| ADMIN-003 | — | **NOT_RUN** | 同上 |
| ADMIN-004 | — | **NOT_RUN** | 同上 |
| ADMIN-005 | — | **NOT_RUN** | 同上 |
| ADMIN-006 | — | **NOT_RUN** | 同上 |
| ADMIN-007 | — | **NOT_RUN** | 同上 |
| ADMIN-008 | — | **NOT_RUN** | 同上 |
| ADMIN-009 | — | **NOT_RUN** | 同上 |
| ADMIN-010 | — | **NOT_RUN** | 同上 |
| DATA-001 | 金额超 JS 安全整数 | **PASS** | run-sprint5-e2e.sh：`247763038695932807808` 在 API/CSV/XLSX/JSONL/Parquet/ZIP 中原值一致，SHA-256 与 DB 一致 |
| DATA-002 | Current Balance 与 Net Flow | **PASS** | Explorer overview：`current_balance.is_current_balance=true` 与 `historical_net_flow.is_current_balance=false` 分区，来源/口径字段明确 |
| DATA-003 | CSV/XLSX/JSONL/Parquet | **PASS** | run-sprint5-e2e.sh：四种格式真实解析、行数>0、含原始金额、SHA-256 与 DB 一致；Parquet 由 parquetprobe 泛型读取 |
| DATA-004 | ZIP | **PASS** | run-sprint5-e2e.sh：manifest.json 字段完整；条目名无路径穿越；testzip 通过；整包 SHA-256 与 DB 一致 |
| DATA-005 | Range 下载 | **PASS** | run-sprint5-e2e.sh：HEAD 200 + Accept-Ranges + ETag=SHA-256；Range 206 + Content-Range + 分片 hash；同一票据重复使用 410 |
| DATA-006 | Worker Kill-9 | **PASS** | run-sprint5-e2e.sh：systemd 恢复新 PID；任务仍在、shard 集合无重复/丢失、checkpoint page_no/rows 不回退 |
| DATA-007 | NATS 重启 | **PASS** | run-sprint5-e2e.sh：NATS 停止期间 outbox 事务写入；重启后 published_at 置位、last_error 空、aggregate 唯一 |
| DATA-008 | MinIO 对象缺失 | **PASS** | run-sprint5-e2e.sh：删对象后详情 object_missing/download_available=false，票据 503 DATA_OBJECT_MISSING，审计 export.download_failed(action=notify_admin)，恢复后可下载 |
| DATA-009 | 备份恢复演练 | **NOT_RUN** | S7-DR-01 范围 |
| DATA-010 | Schema migration 中断 | **PASS** | scripts/test_migration_reentrancy.sh：首次迁移、重复执行幂等、注入真实失败后单文件整体回滚且无半成品记录、旧表仍可读、清理后可重入，10/10 通过 |
| AUDIT-001 | 用户操作清单逐项执行 | **PASS** | S4 用户操作清单逐项执行：case.create/update/state、subject/member/comment、investigation:create/start、finding:review、evidence:review、report:create/review/download、notification 等均有 audit_events_v2；可按 Trace 串联；审计链 valid=true（run-sprint4-e2e.sh PASS 79）。S5–S7 新增动作会扩展该矩阵 |
| AUDIT-002 | 日志秘密扫描 | **PASS** | run-sprint12-e2e.sh：对 API 响应、服务端日志与 4 张数据库表做明文口令扫描，命中 0 |
| AUDIT-003 | 修改/删除审计事件 | **PASS** | 触发器实测拒绝 UPDATE/DELETE，见 AUDIT-003-db/db-check.txt |
| AUDIT-004 | Hash chain | **PASS** | audit 链 checked=101、login 链 checked=111，均 valid=true；每日锚点写入 MinIO 未实现 |
| SEC-001 | CSRF | **PASS** | 缺 CSRF 头 403、携带正确头 200 |
| SEC-002 | XSS/Markdown | **PASS** | Playwright 注入 `<img src=x onerror=...>`：window.__xss 未置位、未生成 img 元素、以纯文本呈现；nginx 已下发 CSP（script-src 'self'、object-src 'none'、frame-ancestors 'none'） |
| SEC-003 | SSRF | **PASS** | 本批次新增面不含用户可控出站 URL；URL 形状输入仅被当作数据处理；公共 4byte 签名服务默认关闭 |
| SEC-004 | API 限流 | **PASS** | run-sprint12-e2e.sh：第 21 次失败登录触发 429 且带 Retry-After；同一 IP 下其他账号仍可正常登录（(IP,账号) 维度限流） |
| UX-001 | Dialog 焦点 | **PASS** | Playwright：焦点陷阱、ESC、焦点回归、无残留遮罩 |
| UX-002 | 四断点布局 | **PASS** | 四个断点 390/768/1024/1440 × 用户层 5 个页面 + 后台 4 个页面，均无页面级横向滚动；修复了 page-head 与顶栏两处真实溢出 |
| UX-003 | 颜色和状态 | **PASS** | Playwright 逐元素计算 WCAG 对比度（前景色 / 向上回溯的有效背景色），全部 ≥4.5:1；状态徽标带 ●▲■◆○ 形状前缀。修复了导航类名失效与琥珀色 2.90:1 两个真实问题 |
| UX-004 | 长表性能 | **PASS** | Playwright：真实 10k–30k 地址加载 ≥10,000 行，DataTable 虚拟滚动 DOM 行数 ≤100，滚动交互 <1.5s；`full=1` 轻量列加载约 9s |
| UX-005 | reduced motion | **PASS** | tokens.css 中 reduced-motion 媒体查询 |

## 未执行项的外部阻塞说明（§27.7 要求）

Sprint 5 完成后，剩余 `NOT_RUN` / `NOT_IMPLEMENTED` 的原因：

1. **被测功能尚未开发**：ADMIN-001..010（Sprint 6 后台控制/Provider/告警/预算/Kill Switch/
   配置与备份）、DATA-009（Sprint 7 备份恢复演练）。这些不能标 PASS，也不能用
   页面存在或 HTTP 200 冒充。
2. **AUTH-004**：MFA/TOTP 未实现，`/api/auth/v1/mfa/verify` 返回 501。
3. **AUTH-011**：adapter + httptest 隔离测试通过，但没有真实 IdP，live 验收保持
   `NOT_VERIFIED`；生产保持 `oidc:false`。
4. **RBAC-008**：用户层会话/无凭证访问后台已 PASS；API Key/scope 路径未覆盖，保持 PARTIAL。

Sprint 5 验收只声明以下结果：
- `run-sprint5-e2e.sh`：PASS 104 / FAIL 0 / NOT_VERIFIED 0（build_sha=`244f63b`）；
- Playwright `dual-system-sprint5.spec.ts`：适用 14 PASS / 0 FAIL / 13 个项目守卫 skip；
- 回归：`run-sprint4-e2e.sh` 79/0/0、`run-rbac-e2e.sh` 29/0/2（历史 BLOCKED 标注）、
  `run-sprint12-e2e.sh` 19/0/1、`run-iam-e2e.sh` 32/0、`dual-system-quality.spec.ts` 27/27；
- 权威证据目录：`acceptance-evidence/dual-system-v1/tests/sprint5/`（含 run.log、result.json、
  四种格式文件、ZIP/range、screenshots、runtime-verification.txt）、
  `frontend/test-results/`（失败 trace 仅在有失败时产生）。
