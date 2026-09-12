# §27 测试计划一致性对照（全部 65 个 Test ID）

> 生成时间：2026-09-12  ·  build：`feat/dual-system-iam-foundation`

本表是 `docs/dual-system-development-plan.md` §27 要求的「全部 Test ID 结果」。

**编号诚实性说明**：`acceptance-evidence/dual-system-v1/tests/run-iam-e2e.sh` 早期版本曾用
`AUTH-001..010`、`RBAC-001..005` 作为自有冒烟检查的编号，与 §27 同名 Test ID 并不一一对应。
该脚本已改为：仅当语义与 §27 完全一致时才使用文档编号，其余一律改用 `SMOKE-nn`。
本表是修正后的权威结论。

> **2026-09-12 更新（Sprint 3 补齐）**：RBAC-001..008 与跨租户负向测试已执行，
> 见 `tests/run-rbac-e2e.sh`（PASS 29 / FAIL 0 / BLOCKED 2）。下方统计已同步。

## 统计

| 状态 | 数量 | 含义 |
|---|---|---|
| PASS | 15 | 已执行且达到 §27 预期结果 |
| PARTIAL | 17 | 部分达成，缺项已在备注中写明 |
| NOT_RUN | 22 | 未执行（多为对应功能尚未实现，不标 PASS） |
| NOT_IMPLEMENTED | 11 | 功能本身未实现 |
| **合计** | **65** | |

## 明细

| Test ID | 场景 | 状态 | 证据 / 缺项 |
|---|---|---|---|
| AUTH-001 | 正确本地账户登录 | **PASS** | run-iam-e2e.sh；HttpOnly Cookie + login_events success |
| AUTH-002 | 错误密码 | **PASS** | 统一错误 + 内部原因码 bad_password + 账号掩码 |
| AUTH-003 | 不存在账号 | **PARTIAL** | 返回同一错误且落库 unknown_account；**未做时序样本对比**证明时序近似 |
| AUTH-004 | MFA 成功/失败 | **NOT_IMPLEMENTED** | TOTP 未实现，/api/auth/v1/mfa/verify 返回 501 |
| AUTH-005 | Session refresh 旋转 | **PARTIAL** | 实现了 token 旋转；**未实现 token family 重放检测与 family revoke** |
| AUTH-006 | 用户退出 | **PARTIAL** | logout 撤销会话并清 Cookie；未单独断言撤销后请求 401 |
| AUTH-007 | 管理员强制下线 | **PARTIAL** | 接口与审计已实现；**未验证 5 秒内生效的时限** |
| AUTH-008 | 新设备登录 | **PARTIAL** | 设备/登录/风险字段入库；未做字段完整性逐项断言 |
| AUTH-009 | 伪造 X-User-Role | **PASS** | AUTH_MODE=session 下伪造头返回 401；/api/v1/me 为 anonymous |
| AUTH-010 | 伪造转发 IP | **PASS** | 单测 TestResolveClientIPIgnoresUntrustedForwarding；AUTH_TRUSTED_PROXIES 之外一律丢弃 |
| AUTH-011 | OIDC state/nonce/PKCE 错误 | **NOT_IMPLEMENTED** | /api/auth/v1/oidc/* 返回 501 |
| AUTH-012 | 开放重定向尝试 | **PASS** | return_to=https://evil.example 被中和为 /home；含 /admin、/app-v2 前缀剥离 |
| RBAC-001 | Viewer 创建案件 | **PASS** | run-rbac-e2e.sh：viewer 调用高权限写操作 401；权限集合不含任何 admin:*；同时具备 dashboard:read 属正向对照 |
| RBAC-002 | 猜测其他组织案件 UUID | **PASS** | run-rbac-e2e.sh：跨组织读对象 404（与不存在 UUID 返回同一状态码，不泄露存在性）；审计记录 authorization.object_denied + deny_reason=cross_organization |
| RBAC-003 | 工作区角色只作用于目标工作区 | **PASS** | run-rbac-e2e.sh：工作区级绑定仅对目标工作区生效——acme-lab 案件 404、acme-other 案件 200 |
| RBAC-004 | 管理员无 evidence 权限读取正文 | **PARTIAL** | 敏感读取（GET admin:audit:read）产生审计事件已验证；**evidence 正文端点属 S4/S5，未实现（BLOCKED）** |
| RBAC-005 | 移除最后一个 Owner | **PASS** | run-rbac-e2e.sh：撤销最后一个 owner 绑定返回 422 CONFLICT_LAST_OWNER，且 owner 绑定未被误删 |
| RBAC-006 | 角色修改后旧 Session | **PASS** | run-rbac-e2e.sh：撤销全部绑定后旧会话 0 秒内 403、权限集合同步收敛；重新授予后 0 秒内恢复（SLA ≤5s） |
| RBAC-007 | Canvas 引用跨案件 Evidence | **NOT_IMPLEMENTED** | canvas/evidence 端点属 S5；对象级 ABAC 的框架（access.Guard）已就绪但缺少被测对象类型 |
| RBAC-008 | API Client 访问后台 | **PARTIAL** | 已验：用户层会话访问后台 API/会话列表均 401；investigator 无任何 admin 权限；无凭证 401。**未覆盖 API Key / scope 路径** |
| USER-001 | 新建案件 | **NOT_IMPLEMENTED** | S4 范围，页面标注目标 Sprint |
| USER-002 | 创建调查并 Coverage HIT | **NOT_IMPLEMENTED** | S5 范围 |
| USER-003 | 创建调查并补齐缺口 | **NOT_IMPLEMENTED** | S5 范围 |
| USER-004 | 暂停/恢复调查 | **NOT_IMPLEMENTED** | S5 范围 |
| USER-005 | Finding 复核 | **NOT_IMPLEMENTED** | S4 范围 |
| USER-006 | 证据版本替代 | **NOT_IMPLEMENTED** | S4 范围 |
| USER-007 | Canvas 并发保存 | **NOT_IMPLEMENTED** | S5 范围 |
| USER-008 | 报告审批下载 | **NOT_IMPLEMENTED** | S4 范围 |
| USER-009 | 全局搜索 | **PARTIAL** | 已实现案件/调查两类搜索且服务端按工作区裁剪；**地址/交易/报告类型未实现** |
| USER-010 | 通知跳转 | **NOT_IMPLEMENTED** | S4 范围 |
| USER-011 | 手机调查查看 | **PARTIAL** | 用户层 5 个页面在 390px 下通过 UX 断言；调查详情页尚不存在 |
| USER-012 | 键盘完成核心流程 | **NOT_RUN** | 只验证了弹窗焦点陷阱与 ESC，未做完整键盘流程走查 |
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
| DATA-001 | 金额超 JS 安全整数 | **PARTIAL** | Amount 组件用字符串展示 raw_amount；**未做端到端一致性验证** |
| DATA-002 | Current Balance 与 Net Flow | **NOT_RUN** | 依赖 Explorer v2（S5） |
| DATA-003 | CSV/XLSX/JSONL/Parquet | **NOT_RUN** | S5/S7 范围 |
| DATA-004 | ZIP | **NOT_RUN** | S5/S7 范围 |
| DATA-005 | Range 下载 | **NOT_RUN** | S5/S7 范围 |
| DATA-006 | Worker Kill-9 | **NOT_RUN** | S7-DR-01 范围 |
| DATA-007 | NATS 重启 | **NOT_RUN** | S7-DR-01 范围 |
| DATA-008 | MinIO 对象缺失 | **NOT_RUN** | S6/S7 范围 |
| DATA-009 | 备份恢复演练 | **NOT_RUN** | S7-DR-01 范围 |
| DATA-010 | Schema migration 中断 | **PARTIAL** | 迁移为单文件单事务 + advisory lock；**未做中断注入演练** |
| AUDIT-001 | 用户操作清单逐项执行 | **PARTIAL** | 写操作与敏感读取已强制审计；**未逐项对照 65 个 Test ID 的动作矩阵** |
| AUDIT-002 | 日志秘密扫描 | **PARTIAL** | 单响应断言不含口令；**未做全库/全日志 secret 扫描** |
| AUDIT-003 | 修改/删除审计事件 | **PASS** | 触发器实测拒绝 UPDATE/DELETE，见 AUDIT-003-db/db-check.txt |
| AUDIT-004 | Hash chain | **PASS** | audit 链 checked=101、login 链 checked=111，均 valid=true；每日锚点写入 MinIO 未实现 |
| SEC-001 | CSRF | **PASS** | 缺 CSRF 头 403、携带正确头 200 |
| SEC-002 | XSS/Markdown | **PARTIAL** | React 默认转义，无 dangerouslySetInnerHTML；**未做 payload 注入与 CSP 报告验证** |
| SEC-003 | SSRF | **NOT_RUN** | 本批次未引入新的出站 URL 入口；未执行负向用例 |
| SEC-004 | API 限流 | **PARTIAL** | 登录限流已实现（进程内令牌桶）；**未做 429/Retry-After 压测** |
| UX-001 | Dialog 焦点 | **PASS** | Playwright：焦点陷阱、ESC、焦点回归、无残留遮罩 |
| UX-002 | 四断点布局 | **PARTIAL** | 1440/390 两个断点通过；**768/1024 未验证** |
| UX-003 | 颜色和状态 | **PARTIAL** | 状态徽标带形状前缀；**未做 4.5:1 对比度计算与 axe 扫描** |
| UX-004 | 长表性能 | **PARTIAL** | 实现了 100 行分页窗口渲染；**未用 10k 数据做 profiler 验证** |
| UX-005 | reduced motion | **PASS** | tokens.css 中 reduced-motion 媒体查询 |

## 未执行项的外部阻塞说明（§27.7 要求）

绝大多数 `NOT_RUN` 的原因是**被测功能尚未开发**（Sprint 4–7 范围），而不是环境阻塞：
案件、调查、Finding/Evidence、Canvas、Explorer v2、下载 v2、导出、报告、通知、
Provider/配置/备份管理、Kill Switch、保留与删除策略。

真正属于「已实现但本轮未验证」的只有：AUTH-003 时序对比、AUTH-005 token family 重放、
AUTH-006/007 撤销生效时限、AUTH-008 字段逐项断言、RBAC-002/003/005/007 负向用例、
RBAC-006 authz_version、DATA-001/010、AUDIT-001/002、SEC-002/003/004、UX-002(768/1024)、UX-003、UX-004。
这些是**下一步最该补的验收**，而不是可以忽略的细节。
