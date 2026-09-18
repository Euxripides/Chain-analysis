# §27 验收矩阵 逐 Test ID 证据清单（2026-09-16 冻结件实况）

> **勘察时间**：2026-09-16 20:33 +07（VM 本地时间，`date` 输出 `Wed Sep 16 08:33:43 PM +07 2026`）
> **勘察方式**：只读。SSH 到部署机 `euripides@dataserver:/home/euripides/projects/blockchain-platform`，
> 用 `sha256sum` / `ls` / `cat` / `grep` 读取既有证据；**未执行任何测试套件**，**未写入 VM**。
> **冻结件核对（已实测）**：
> ```
> 7831bff0bc6127963840aba8f505b0c337243e2c69a871f2b4010e4dbec19e32  backend/bin/api
> stat: 2026-09-16 19:30:23.061851527 +0700   57261432 bytes
> ```
> 与任务给定值一致。同批冻结清单 `acceptance-evidence/dual-system-v1/release-20260916T125603Z/manifest.txt`
> （`generated_at_local=2026-09-16T19:56:03+07:00`）记录 `blockchain-api state=active pid=3777807 … sha256=7831bff0…`、
> `blockchain-worker … sha256=0d49fb75…`、`migrate d48e58fe…`、`migration_head=postgres/0065_role_read_permission_gaps.sql`。

> **本清单的 freshness 判据**
> - `current-build`：证据产出自 2026-09-16 的 QA 重跑批次，且该批由 `tests/run-sprint7-qa.sh` 锚定到
>   `tests/sprint7/qa/summary.json` 的 `"build_api_sha": "7831bff0bc6127963840aba8f505b0c337243e2c69a871f2b4010e4dbec19e32"`
>   （`started_at 2026-09-16T12:33:25Z` → `finished_at 2026-09-16T12:48:17Z`，即本地 19:33–19:48，均晚于 19:30:23 的二进制落盘时间）；
>   该脚本第 26 行 `BUILD_SHA="$(sha256sum "$PROJECT/backend/bin/api" | awk '{print $1}')"` 即锚定动作。
> - `older-build`：证据带更早的构建 sha（`d221dfef…`、`f10a1421…`、`f59bff69…`）或生成于 2026-09-12/13。
> - `none`：VM 上找不到证据（本清单 0 条）。
>
> 路径为**仓库相对路径**，根 = `acceptance-evidence/dual-system-v1/`（下表简写 `E/`）。引号内文字为文件原文照抄（含对齐空格）。

---

## 1. 逐 Test ID 证据（65 条）

| Test ID | 场景 | 当前最佳证据（路径 : 行 + 原文数字/字符串） | 产出日期/轮次 | freshness | gap（是否真的覆盖场景） |
|---|---|---|---|---|---|
| AUTH-001 | 正确本地账户登录 | `E/tests/sprint7/qa/iam/console.log:2` `PASS  AUTH-001 admin 登录成功                    200`；`:3` `PASS  AUTH-001 会话 Cookie 为 HttpOnly            1` | 2026-09-16 QA 批次 | current-build | 无 |
| AUTH-002 | 错误密码 | `E/tests/sprint7/qa/iam/console.log:33` `PASS  AUTH-002 记录 bad_password 原因码         True`；`:35` `失败事件结果标记为 failure   True`；`:36` `账号以掩码形式入库           True` | 2026-09-16 QA 批次 | current-build | 无 |
| AUTH-003 | 不存在账号 | `E/tests/sprint7/qa/sprint12/console.log:38` `PASS     AUTH-003   中位响应时间比值 ≤ 3x（实测 1.11x）    within`；`:35` `两种失败的错误码与提示完全一致        AUTHN_INVALID_CREDENTIALS 账号或密码不正确 False` | 2026-09-16 QA 批次 | current-build | 无 |
| AUTH-004 | MFA 成功/失败 | `E/section27-conformance.md:20` “冻结版本 `f10a1421…` 上两个 live 用例均通过：… 4.18 s PASS … 0.67 s PASS，ALL PASS；事件计数 challenge=94 / mfa_failure=61 / mfa_success=47 / login_success=84” | 报告 2026-09-16，实跑件 `f10a1421…` | **older-build** | **不覆盖当前构建**：VM 上**没有** MFA 原始测试日志或 result.json，只有报告叙述 + `E/sprint7/mfa-e2e-*/` 下的 PNG（最新 `mfa-e2e-1789529068482` 时间戳 10:24 本地）；没有任何证据把 MFA 用例锚定到 `7831bff0…`。 |
| AUTH-005 | Session refresh 旋转 | `E/tests/sprint7/qa/sprint12/console.log:8` `PASS     AUTH-005   重放后整个会话族被撤销（新令牌同时失效） 401`；`:7` `旧令牌重放被识别（AUTHN_SESSION_REUSE）    AUTHN_SESSION_REUSE` | 2026-09-16 QA 批次 | current-build | 无 |
| AUTH-006 | 用户退出 | `E/tests/sprint7/qa/sprint12/console.log:12` `PASS     AUTH-006   退出后同一令牌立即 401                      401` | 2026-09-16 QA 批次 | current-build | 无 |
| AUTH-007 | 管理员强制下线 | `E/sprint7/c1-scope-commit-latency-d15c-20260916T102818Z/gate-console.log:62` `PASS[31] every contended scope mutation stayed within AUTH-007 (177 ms <= 5000 ms)`；`:63` `AUTH007_VIOLATION=0` | 2026-09-16T10:28:18Z（本地 17:28） | **older-build** | **当前构建无实测**：该门禁 `:3` 记 `bin=/tmp/staging/api-calib bin_sha256=f59bff69…`、`:8` `prod_api_pid=3227022`（= 上一冻结件 `f10a1421…` 的进程）；`7831bff0…` 上 `E/tests/sprint7/qa/sprint12/console.log:15` 只有 `BLOCKED  AUTH-007   需要夹具账号口令                             设置 FIXTURE_VIEWER_PASSWORD 后重跑`。 |
| AUTH-008 | 新设备登录 | `E/tests/sprint7/qa/sprint12/console.log:20` `PASS     AUTH-008   新设备被标记（is_new_device=true）           True` | 2026-09-16 QA 批次 | current-build | 无 |
| AUTH-009 | 伪造 `X-User-Role` | `E/tests/sprint7/qa/iam/console.log:10` `PASS  AUTH-009 伪造 X-User-* 头被忽略          401`；另 `E/tests/sprint7/qa/ui-extended/playwright-extended.log:23` `✓  20 [cross-role] … AUTH-009 后台会话调用用户层 API -> 服务端 401（受众严格分离） (9.0s)` | 2026-09-16 QA 批次 | current-build | 无（伪造头 + 双向受众分离均实测） |
| AUTH-010 | 伪造转发 IP | `E/sprint7/security/negative-tests/run.log:39` `PASS         AUTH-010a      Cloudflare 路径：client_ip 取 CF-Connecting-IP（XFF 被丢弃） 198.51.100.9`；`:41` `来源链完整记录（peer/client_ip/proxy_chain 均落库）    1` | 2026-09-12T18:58Z，`build_api_sha=d221dfef…`（`run.log:4`） | **older-build** | **当前构建无覆盖**：2026-09-16 批次中 `iam/console.log:8` 的分节标题写「AUTH-009/010 伪造身份与转发头」，但节内只有 `AUTH-009` 两条断言，**没有**任何 XFF / CF-Connecting-IP / proxy_chain 断言。 |
| AUTH-011 | OIDC state/nonce/PKCE 错误 | `E/tests/sprint7/qa/iam/console.log:24` `PASS  SMOKE-09 未启用能力显式 501（OIDC 未接入） 501` | 2026-09-16 QA 批次 | current-build | **不覆盖场景**：501 冒烟只证明能力未启用，对 state/nonce/PKCE 错误**零断言**；矩阵自身记 `NOT_VERIFIED`（`E/section27-conformance.md:127`），live 层无可验证对象。 |
| AUTH-012 | 开放重定向尝试 | `E/tests/sprint7/qa/iam/console.log:40` `PASS  AUTH-012 return_to 开放重定向被中和    /home` | 2026-09-16 QA 批次 | current-build | **仅部分覆盖**：当前构建只测 1 个载荷；6 载荷全量回落（`6 个开放重定向载荷全部回落到站内路径              6`）只在旧件 `E/sprint7/security/negative-tests/run.log:48`。 |
| RBAC-001 | Viewer 创建案件 | `E/tests/sprint7/qa/sprint4/run.log:177` `PASS         AUTHZ      viewer 创建案件 -> 403                           403`；`E/tests/sprint7/qa/rbac/console.log:10` `PASS     RBAC-001   viewer 调用需 admin:session:revoke 的写操作 401` | 2026-09-16 QA 批次 | current-build | 标签不一致（套件标成 `AUTHZ` 而非 `RBAC-001`）；§27 的「按钮不可见」半句只在 `ui-extended/playwright-extended.log:24` 以「导航不出现后台条目」间接覆盖。 |
| RBAC-002 | 猜测其他组织案件 UUID | `E/tests/sprint7/qa/rbac/console.log:17` `PASS     RBAC-002   owner 读 acme 组织案件 -> 404（反向同样拒绝） 404`；`:20` `越权尝试已写入审计（authorization.object_denied） yes` | 2026-09-16 QA 批次 | current-build | 无 |
| RBAC-003 | 工作区角色只作用于目标工作区 | `E/tests/sprint7/qa/rbac/console.log:26` `PASS     RBAC-003   analyst 读 acme-other 案件 -> 200（已绑定） 200`；`:25` `analyst 读 acme-lab 案件 -> 404（未绑定该工作区） 404` | 2026-09-16 QA 批次 | current-build | 无 |
| RBAC-004 | 管理员无 evidence 权限读取正文 | `E/tests/sprint7/qa/sprint5/run.log:248` `PASS         RBAC-004     auditor 无 evidence:read 读取正文 -> 403              403`；`:250` `拒绝写入 authorization.denied（permission_key=evidence:read） yes` | 2026-09-16 QA 批次 | current-build | 同 ID 在 rbac 套件仍记 `BLOCKED`（`E/tests/sprint7/qa/rbac/console.log:29` `依赖 S4/S5 的 evidence 端点（当前未实现）`）——历史标注，实测由 sprint5 提供。 |
| RBAC-005 | 移除最后一个 Owner | `E/tests/sprint7/qa/rbac/console.log:33` `PASS     RBAC-005   撤销最后一个 owner 绑定 -> 422       422`；`:34` `owner 绑定仍然有效（未被误删）   200` | 2026-09-16 QA 批次 | current-build | 无 |
| RBAC-006 | 角色修改后旧 Session | `E/tests/sprint7/qa/rbac/console.log:40` `PASS     RBAC-006   重新授予后 5 秒内恢复权限（实测 0s） within_sla`；`:38` `撤销后旧会话在 5 秒内失去权限（实测 0s） within_sla` | 2026-09-16 QA 批次 | current-build | 无 |
| RBAC-007 | Canvas 引用跨案件 Evidence | `E/tests/sprint7/qa/sprint5/run.log:116` `PASS         RBAC-007     Canvas 引用跨案件 Evidence -> 422                     422`；`:119` `拒绝后未写入 Canvas                                  0` | 2026-09-16 QA 批次 | current-build | rbac 套件同 ID 仍 `BLOCKED`（`E/tests/sprint7/qa/rbac/console.log:42`），实测由 sprint5 提供。 |
| RBAC-008 | API Client 访问后台 | `E/tests/sprint7/qa/iam/console.log:14` `PASS  RBAC-008 用户层会话访问后台 API 被拒 401`；`E/tests/sprint7/qa/ui-extended/playwright-extended.log:22` `✓  19 [cross-role] … RBAC-008 用户层会话调用后台 API -> 服务端 401，页面不进入后台外壳 (1.9s)` | 2026-09-16 QA 批次 | current-build | **当前构建只覆盖会话受众隔离**；API Key 的 audience/scope 负向、`api_client` 角色不含 `admin:*`、`allowed_ips` 回归门等 9 条只在旧件 `E/sprint7/security/negative-tests/run.log:117-135`（build `d221dfef…`）。 |
| USER-001 | 新建案件 | `E/tests/sprint7/qa/sprint4/run.log:19` `PASS         USER-001   DB cases 行与 API 一致（name/status/classification） S4-E2E-20260916-193326\|active\|confidential` | 2026-09-16 QA 批次 | current-build | 无 |
| USER-002 | 创建调查并 Coverage HIT | `E/tests/sprint7/qa/sprint5/run.log:25` `PASS         USER-002     preview overall_ratio=1                                    1`；`:37` `Coverage HIT preview/create 未产生 Provider 调用（按 trace 实证） 0` | 2026-09-16 QA 批次 | current-build | 无 |
| USER-003 | 创建调查并补齐缺口 | `E/tests/sprint7/qa/sprint5/run.log:63` `PASS         USER-003     缺口预览 provider_work_required=true                   True`；`:72` `所有 Shard 只在缺口区间内，不重复已覆盖块 0` | 2026-09-16 QA 批次 | current-build | 无 |
| USER-004 | 暂停/恢复调查 | `E/tests/sprint7/qa/sprint4/run.log:65` `PASS         USER-004   运行 Checkpoint 仍保留（data_reused=true）    true`；`:63` `暂停时未创建野 run；恢复后 run 数受控（<=1） yes` | 2026-09-16 QA 批次 | current-build | 无 |
| USER-005 | Finding 复核 | `E/tests/sprint7/qa/sprint4/run.log:73` `PASS         USER-005   viewer 确认 Finding -> 403                         403`；`:79` `confirmed Finding 至少一条 verified Evidence     1` | 2026-09-16 QA 批次 | current-build | 无 |
| USER-006 | 证据版本替代 | `E/tests/sprint7/qa/sprint4/run.log:87` `PASS         USER-006   原 Evidence 状态变为 superseded 但内容 hash 不变 superseded\|3f24637a52805e25973873d144463500a412cfca57e0d585cfa81617538dd299` | 2026-09-16 QA 批次 | current-build | 无 |
| USER-007 | Canvas 并发保存 | `E/tests/sprint7/qa/sprint5/run.log:110` `PASS         USER-007     第二客户端 stale 保存 -> 412                        412`；`:112` `412 返回字段级 diff                                   True` | 2026-09-16 QA 批次 | current-build | 无 |
| USER-008 | 报告审批下载 | `E/tests/sprint7/qa/sprint4/run.log:113` `PASS         USER-008   票据返回 SHA-256 与审批绑定 hash 一致     f8764860cb917d4b083daa7ee0fab3b275c3c1db557bd0123999131a67de9ff7`；`:119` `票据重复使用 -> 410                            410` | 2026-09-16 QA 批次 | current-build | 无 |
| USER-009 | 全局搜索 | `E/tests/sprint7/qa/sprint5/run.log:88` `PASS         USER-009     搜索命中案件类型                                   True`；`:98` `搜索命中地址类型（TRON Base58 不被 EVM 正则拒绝） True` | 2026-09-16 QA 批次 | current-build | 无（D-01 地址搜索缺陷已在本轮修复并有实测） |
| USER-010 | 通知跳转 | `E/tests/sprint7/qa/sprint4/run.log:137` `PASS         USER-010   通知 open 返回可访问目标                    /reports/a13044b0-f5a1-40f0-8888-1e1e273cf809`；`:139` `他人打开该通知 -> 404（安全失败）       404` | 2026-09-16 QA 批次 | current-build | 无 |
| USER-011 | 手机调查查看 | 旧件 `E/tests/sprint5/playwright-full.log:27` `✓  24 [user-mobile] › tests/dual-system-sprint5.spec.ts:306:3 › … USER-011 移动端 Canvas 列表视图可关闭 Dialog 且无横向溢出 (2.9s)`；当前构建 `E/tests/sprint7/qa/ui/playwright.log:20` `-  17 [user-desktop] … USER-011 移动端 Canvas 列表视图可关闭 Dialog 且无横向溢出`（skip） | 旧件 2026-09-12（S5 构建） | **older-build** | **当前构建无覆盖**：`E/tests/run-sprint7-ui.sh:91` 默认 `PROJECTS` 只含 `--project=user-desktop --project=admin-desktop`，`run-sprint7-ui-extended.sh` 的 project 集合是 `cross-role / reduced-motion / admin-tablet`——**都不含 user-mobile**，390px 真实浏览器证据仍是 S5 构建的。 |
| USER-012 | 键盘完成核心流程 | `E/tests/sprint7/qa/ui/playwright.log:21` `✓  18 [user-desktop] › tests/dual-system-sprint5.spec.ts:360:3 › … USER-012 键盘完成搜索并进入结果，Dialog 焦点可逃逸 (3.7s)` | 2026-09-16 QA 批次（19:48） | current-build | 无 |
| ADMIN-001 | 创建用户并绑定工作区角色 | `E/tests/sprint7/qa/sprint6/run.log:5` `PASS         ADMIN-001a   创建用户返回 UUID                                      uuid`；`:7` `一次性初始密码只返回一次                         once`；`:11` `工作区成员关系已建立                               1` | 2026-09-16 QA 批次（该文件第 1 行自记 `build=7831bff0…`） | current-build | 无（D-02「创建用户 100% 失败」已在本轮修复） |
| ADMIN-002 | 停用用户影响预览 | `E/tests/sprint7/qa/sprint6/run.log:22` `PASS         ADMIN-002a   影响预览返回活跃会话数（新账号已登录=1）  1`；`:26` `未带 confirm 的停用被拒绝（428）                   428`；`:32` `停用同时撤销全部会话                               0` | 2026-09-16 QA 批次 | current-build | 无 |
| ADMIN-003 | Provider 连接测试 | `E/tests/sprint7/qa/sprint6/run.log:39` `PASS         ADMIN-003b   连接测试使用受控探针方法                         Provider.Health`；`:43` `连接测试写入 Usage（DB 证据）                      1`；`:45` `密钥列表只返回摘要（无明文）                   summary` | 2026-09-16 QA 批次 | current-build | 无 |
| ADMIN-004 | Provider 断开有受影响任务 | `E/tests/sprint7/qa/sprint6/run.log:50` `PASS         ADMIN-004a   存在运行任务时断开必须 step-up（403）           403`；`:52` `带 step-up 断开成功                                     disconnected`；`:54` `响应包含受影响链/Dataset/任务/fallback             has` | 2026-09-16 QA 批次 | current-build | 无 |
| ADMIN-005 | 性能模式切换 | `E/tests/sprint7/qa/sprint6/run.log:61` `PASS         ADMIN-005a   返回 requested/effective/reason 三要素                  all`；`:65` `apply 返回值与 DB requested_mode 一致                  safe\|safe` | 2026-09-16 QA 批次 | current-build | 无 |
| ADMIN-006 | Kill Switch 双人审批 | `E/tests/sprint7/qa/sprint6/run.log:82` `PASS         ADMIN-006d   请求人自审批被拒绝（403）                         403`；`:86` `第二位管理员审批通过                               approved`；`:88` `DB 记录 requester<>approver                                1` | 2026-09-16 QA 批次 | current-build | 无（D-14「Kill Switch 无法恢复」已修复，`:94` 记 `恢复后 kill_switch=false`） |
| ADMIN-007 | 预算超阈值 | `E/tests/sprint7/qa/sprint6/run.log:105` `PASS         ADMIN-007b   预算评估判定已超动作阈值                         action_triggered`；`:107` `触发优先级为 P1                                        P1`；`:109` `生成了预算告警（DB）                                1` | 2026-09-16 QA 批次 | current-build | 无 |
| ADMIN-008 | 审计导出 | `E/tests/sprint7/qa/sprint6/run.log:129` `PASS         ADMIN-008b   下载文件行数与导出计数一致                      500`；`:131` `下载文件 SHA-256 与响应一致                         1cba6c36eaaf9bc1ce0059ccfe1a3d10688bafcd0721a2a9e5d64d87fe61d680` | 2026-09-16 QA 批次 | current-build | 无 |
| ADMIN-009 | 配置并发修改 | `E/tests/sprint7/qa/sprint6/run.log:140` `PASS         ADMIN-009a   缺少 If-Match 返回 428                                   428`；`:144` `复用旧版本被拒绝（412）                            412` | 2026-09-16 QA 批次 | current-build | 无 |
| ADMIN-010 | 功能开关灰度 | `E/tests/sprint7/qa/sprint6/run.log:151` `PASS         ADMIN-010a   灰度设为 50% 成功（200）                             200`；`:155` `第二名用户独立评估（互不干扰）                ok`；`:161` `回滚后同一用户不再命中                            False` | 2026-09-16 QA 批次 | current-build | **场景只覆盖一半**：以上断言走**管理端试算端点**；真实请求路径证据在 `E/tests/sprint7/qa/featureflags/console.log:5` `PASS A1 开关开启时真实 Explorer 请求 200 (200)`、`:7` `PASS A3 开关关闭后真实请求 503 (503)`，但那是**整体开关 on/off**，未证明「50% 灰度只影响目标 scope 的用户」。 |
| DATA-001 | 金额超 JS 安全整数 | `E/tests/sprint7/qa/sprint5/run.log:126` `PASS         DATA-001     找到 >2^53 的原始金额样本                         yes`；`:131` `单区块任务收敛到 COMPLETED                         COMPLETED` | 2026-09-16 QA 批次 | current-build | 无 |
| DATA-002 | Current Balance 与 Net Flow | `E/tests/sprint7/qa/ui/playwright.log:15` `✓  12 [user-desktop] › tests/dual-system-sprint5.spec.ts:133:3 › S5-FE-01 … › 全局搜索按类型分组，并可从结果进入地址详情 (12.3s)`（该用例体内含 `frontend/tests/dual-system-sprint5.spec.ts:152` `expect(overview.summary.current_balance.is_current_balance).toBe(true)`、`:153` `…historical_net_flow.is_current_balance).toBe(false)`） | 2026-09-16 QA 批次（19:48） | current-build | **可追溯性弱**：playwright 日志行**不含** `DATA-002` 字样，判据完全依赖 spec 行号 152–153 落在 :133 起始的用例内；日志不打印该断言的实测值。 |
| DATA-003 | CSV/XLSX/JSONL/Parquet | `E/tests/sprint7/qa/sprint5/run.log:139` `PASS         DATA-003     CSV 文件 SHA-256 与 DB 一致                            62dced4795c39dd1af4f5db1ef1269bb8f8446a56016ca0c6e76610fd210a6b5`；`:145` XLSX `73cdc8b0606f1da4825f97c5ecc0eb286559642ec8da674c60e56ef389668131`；`:151` JSONL `f52a95e3236f6f56ba763e7f025d57502333f0de82223e59d0c54c5d2b9484a9`；`:159` Parquet `93a1a802e04991323d5a16ab7a79abf4efcc4b6cc0e6c52d030bf800e9a6f872` | 2026-09-16 QA 批次 | current-build | 无（产物在 `E/tests/sprint7/qa/sprint5/files/big.{csv,xlsx,jsonl,parquet}`，mtime 2026-09-16 19:37–19:39） |
| DATA-004 | ZIP | `E/tests/sprint7/qa/sprint5/run.log:167` `PASS         DATA-004     ZIP 文件 SHA-256 与 DB 一致                           a08cec5dbe514e7ccd15732ff7f581a1f3d06e1803fb41333aa0381f06f65f5e`；`:163` `ZIP manifest/条目/路径穿越校验通过               ok` | 2026-09-16 QA 批次 | current-build | 无（`files/big.zip` 19:39，1068 B） |
| DATA-005 | Range 下载 | `E/tests/sprint7/qa/sprint5/run.log:179` `PASS         DATA-005     Range 分片与完整文件前 100 字节 SHA-256 一致   efe0c8aad10e98f3dfaddc0c7185dfb239b22da4cabe9cec2db72d83e1f46c89`；`:175` `Range 请求返回 206                                     206`；`:181` `同一张票据重复使用 -> 410                         410` | 2026-09-16 QA 批次 | current-build | 无 |
| DATA-006 | Worker Kill-9 | `E/tests/sprint7/qa/sprint5/run.log:192` `PASS         DATA-006     Worker 进程被 Kill-9 并由 systemd 恢复              yes`；`:194` `任务未被吞掉（无幽灵任务）                    1`；`:198` `逐地址(逐 shard) page_no/rows/page_count/成功页/Provider成功页均不回退 ok` | 2026-09-16 QA 批次 | current-build | 无（不变量快照 `E/tests/sprint7/qa/sprint5/kill9-signature-before.json` / `-after.json`，19:40/19:41，两份逐字节相同：`"success_pages" : 1, "provider_ok" : 3`） |
| DATA-007 | NATS 重启 | `E/tests/sprint7/qa/sprint5/run.log:216` `PASS         DATA-007     NATS 不可用时事件仍事务性写入 outbox           yes`；`:214` `NATS 已真实停机（stop rc=0 / State.Status=exited / 4222+8222 不可达） yes`；`:225` `Outbox 幂等键唯一（同 aggregate 仅 1 条）        1` | 2026-09-16 QA 批次 | current-build | 无（首轮 FAIL 证据仍保留在 `E/tests/sprint7/qa/sprint5/preserved-original-20260913T0148Z/run.log:200`） |
| DATA-008 | MinIO 对象缺失 | `E/tests/sprint7/qa/sprint5/run.log:239` `PASS         DATA-008     返回可操作的 DATA_OBJECT_MISSING                     DATA_OBJECT_MISSING`；`:233` `对象已从两个导出桶真实删除                    yes`；`:243` `对象恢复后详情恢复可下载                       exists=true` | 2026-09-16 QA 批次 | current-build | 无 |
| DATA-009 | 备份恢复演练 | `E/sprint7/dr/restore-drill-20260912T175029Z.md` §4 汇总：PostgreSQL `5.81 s`、ClickHouse `561.36 s`、MinIO `0.63 s`；同文件 RPO 说明 `合计 32 行` | 2026-09-12（备份 id `20260912T171120Z`） | **older-build** | **不满足场景**：RPO 未达标（快照式、无 WAL/PITR，暴露 32 行）、`blockchain-raw`（≈16 GiB）未纳入；门禁 `E/sprint7/dr/dr-gate-20260912T222846Z.json` 记 `"verdict": "BLOCKED", "block_count": 4`（缺 restore-drill / kill9 / MinIO 元数据 / 消费者幂等的机器可读结果）；2026-09-16 未重跑（`E/ROUND-20260916-defect-closure.md` §3 明列「RPO/PITR、全 MinIO bucket 备份 未在本轮重跑」）。 |
| DATA-010 | Schema migration 中断 | `E/tests/DATA-010/result.json:8` `"actual": "首次迁移成功；重复执行幂等；注入真实失败后单文件整体回滚、无半成品记录、旧表仍可读；清理后可重入（10/10）"`；`E/manifest.json` `"migration_reentrancy_checks_pass": 10, \"migration_reentrancy_checks_fail\": 0` | 2026-09-12（`build_sha=f781c564…`；manifest `3e87311`） | **older-build** | **只有自述、无可复算原始输出**：`scripts/test_migration_reentrancy.sh` 在 VM 上存在但**没有任何结果文件**（`E/` 全树只在 `section27-conformance.md`、`tests/DATA-010/result.json`、`manifest.json`、`section31-deliverables.md` 中被提到）；`E/sprint7/db/verification-raw.txt:3` 只显示 `postgres/0056…`、`0057…` 两行 applied，`pre-migration-20260912T162853Z.txt` 记 `55`，因此「55 → 57」是推断而非实测行；`7831bff0…` 上无任何中断场景证据。 |
| AUDIT-001 | 用户操作清单逐项执行 | `E/tests/sprint7/qa/sprint4/run.log:161` `PASS         AUDIT-001  关键动作已落审计：case.create               yes`（同类 `:163` investigation:create、`:165` investigation:start、`:167` finding:review、`:169` report:create、`:171` report:approve、`:173` report:download 全 `yes`）；`E/tests/sprint7/qa/iam/console.log:29` `PASS  AUDIT-001 审计事件已产生                yes` | 2026-09-16 QA 批次 | current-build | **「逐项执行 100%」不可复算**：`E/sprint7/release/audit-coverage-summary.txt` 仍是 **0 字节空文件**（`wc -c` = 0）；24h 动作计数表 `E/sprint7/release/audit-actions-24h.txt` 是 2026-09-13 的（`authorization.denied\|321`、`case.create\|28`、`export.download\|38` …共 21 行）。 |
| AUDIT-002 | 日志秘密扫描 | `E/tests/sprint7/qa/sprint6/run.log:166` `PASS         AUDIT-002a   验收日志中 0 处密码/密钥明文                     0`；`:168` `审计表 detail/before/after 无秘密明文                0`；`:172` `运行日志无秘密明文                                  0` | 2026-09-16 QA 批次 | current-build | 深度/交叉验证扫描仍是旧件：`E/sprint7/security/secret-scan/REPORT.md`（生成 2026-09-12T23:51:51+07:00；三目标 `api.log 8566393 B/38878 行`、`worker.log 29158011 B/125882 行`、`audit_events_v2 1340 行`；结论 `(c) 类真实明文秘密 = 0`）——当前构建只做了抽样断言。 |
| AUDIT-003 | 修改/删除审计事件 | `E/tests/sprint7/qa/sprint6/run.log:179` `PASS         AUDIT-003c   UPDATE 审计事件被运行角色拒绝                     denied`；`:177` `运行角色权限为 INSERT/SELECT 允许、UPDATE/TRUNCATE 拒绝 true/true/false/false`；`:211` `运行角色仍可读取边界记录（正向对照）       t` | 2026-09-16 QA 批次 | current-build | 无 |
| AUDIT-004 | Hash chain | `E/tests/sprint7/qa/iam/console.log:30` `PASS   audit 哈希链校验                                    checked=56695 valid=True integrity=sealed_incident declared_lost=0 declared_anomaly=2 reason=-`；`:31` `login 哈希链校验                                    checked=7858 valid=True integrity=declared_events declared_lost=8 declared_anomaly=0 reason=-` | 2026-09-16 QA 批次 | current-build | **全链「任意篡改可检测」不成立**：`E/tests/sprint7/qa/sprint6/run.log:240` `封存后 integrity_status=sealed_incident（不是 intact） sealed_incident`、`:242` `prefix_status=sealed_unrecoverable（旧链不连续已声明） sealed_unrecoverable`，`:214` 定位缺口 `1-2343`——该区间内篡改**无法**被检测。 |
| SEC-001 | CSRF | `E/tests/sprint7/qa/iam/console.log:16` `PASS  SEC-001 写操作缺少 CSRF 头被拒         403`；`:17` `PASS  SEC-001 写操作携带正确 CSRF 头通过   200` | 2026-09-16 QA 批次 | current-build | **深度断言缺失**：`csrf_failed` 错误码、错误 token → 403、跨站 Origin 写请求 → 403、无副作用，以及套件自记的 R-02（带正确 CSRF 时 Origin 未被二次校验）只在旧件 `E/sprint7/security/negative-tests/run.log:55-68`。 |
| SEC-002 | XSS/Markdown | `E/tests/sprint7/qa/ui/playwright.log:12` `✓   9 [user-desktop] › tests/dual-system-quality.spec.ts:240:3 › SEC-002 XSS / Markdown 注入 › 注入的 HTML 载荷以纯文本渲染，不执行 (7.8s)`；另 `E/tests/sprint7/qa/sprint4/run.log:39` `PASS         SEC-002    评论中的 HTML 标签被服务端移除           True` | 2026-09-16 QA 批次 | current-build | CSP / `nosniff` / DB 层剥离等服务器头断言只在旧件 `negative-tests/run.log:70-87`；残余 F-04（`identities.display_name` 服务端未剥离，依赖 React 转义）仍开放。 |
| SEC-003 | SSRF | `E/tests/sprint7/qa/sprint12/console.log:23` `PASS     SEC-003    URL 形状输入仅被当作数据（未触发服务端请求） yes`；`:24` `PASS     SEC-003    公共 4byte 签名服务默认关闭（无外网出站） 1` | 2026-09-16 QA 批次 | current-build | 出站守卫单测、未认证/Viewer 改 Provider 端点 → 401、端点改动写审计，以及 R-03 残余（`no_validation`）只在旧件 `negative-tests/run.log:89-101`。 |
| SEC-004 | API 限流 | `E/tests/sprint7/qa/sprint12/console.log:30` `PASS     SEC-004    连续失败登录触发 429                         yes`；`:31` `429 响应带 Retry-After 且为正整数            yes`；`:32` `限流不影响其他账号（无共享 bucket）    200` | 2026-09-16 QA 批次 | current-build | 稳定错误码口径不符：旧件 `negative-tests/run.log:108` 标签写 `429 响应体含稳定错误码 AUTHN_RATE_LIMITED`，实测值却是 `RATE_LIMIT_EXCEEDED`；`限流事件写入 login_events` 也只在旧件（`:112`）。 |
| UX-001 | Dialog 焦点 | `E/tests/sprint7/qa/ui/playwright.log:21` `✓  18 [user-desktop] › tests/dual-system-sprint5.spec.ts:360:3 › … USER-012 键盘完成搜索并进入结果，Dialog 焦点可逃逸 (3.7s)` | 2026-09-16 QA 批次（19:48） | current-build | **只覆盖「焦点可逃逸」**：§27 的「关闭后焦点回到触发元素」在当前构建未单独断言（该断言存在于 reduced-motion 用例 `dual-system-reduced-motion.spec.ts:64`，属 UX-005 档位）；旧件 `E/tests/UX-001/result.json` 自记两 project 通过（`build_sha=d7b6fee…`）。 |
| UX-002 | 四断点布局 | `E/tests/sprint7/qa/ui/playwright.log:4` `✓   1 [user-desktop] › tests/dual-system-quality.spec.ts:117:5 › UX-002 四断点布局 › mobile (390px) 无页面级横向滚动 (10.5s)`；`:8` `✓   5 … 后台管理系统在四个断点下无横向滚动 (14.3s)`；`:33` admin-desktop 同套通过 | 2026-09-16 QA 批次 | current-build | 无 |
| UX-003 | 颜色和状态 | `E/tests/sprint7/qa/ui/playwright.log:9` `✓   6 [user-desktop] › tests/dual-system-quality.spec.ts:162:3 › UX-003 颜色对比度与状态可辨识 › 所有可见文本满足 WCAG AA 对比度 (9.0s)`；`:10` `✓   7 … 状态徽标带形状前缀，不只依赖颜色 (7.5s)` | 2026-09-16 QA 批次 | current-build | 无 |
| UX-004 | 长表性能 | `E/tests/sprint7/qa/ui/playwright.log:22` `✓  19 [user-desktop] › tests/dual-system-sprint5.spec.ts:412:3 › … UX-004 10k 真实长表：虚拟滚动 DOM 有界、滚动可交互 (15.0s)`；`:11` `✓   8 … 表格按页窗口渲染，DOM 行数不随数据量线性增长 (6.5s)` | 2026-09-16 QA 批次 | current-build | 无（旧 `tests/UX-004/result.json` 的 PARTIAL 口径已不适用） |
| UX-005 | reduced motion | `E/tests/sprint7/qa/ui-extended/playwright-extended.log:31` `✓  28 [reduced-motion] › tests/dual-system-reduced-motion.spec.ts:46:1 › 减少动态效果下弹窗仍可用：打开、焦点陷阱、ESC 关闭、焦点返回 (3.4s)`；`:32` `✓  29 … 减少动态效果下没有超过 200ms 的过渡（不是只有观感变快） (2.4s)` | 2026-09-16 QA 批次（19:55；`ui-extended/result.json` `"passed": 29, "failed": 0`） | current-build | **仅标签缺失**：用例标题不含 `UX-005` 字样，对齐靠 spec 文件名 `dual-system-reduced-motion.spec.ts`；旧 PARTIAL 的「只有 CSS 层证据」缺口已被这两条浏览器用例关闭（前置断言见 `:30` `✓  27 … 预置条件：浏览器确实处于 prefers-reduced-motion=reduce (682ms)`）。 |

---

## 2. 支撑套件与命中 ID 的对应（2026-09-16 那一批）

| 证据文件（仓库相对路径） | mtime | 覆盖到的 Test ID |
|---|---|---|
| `E/tests/sprint7/qa/summary.json` | 19:48 | 批次锚点：`"build_api_sha": "7831bff0…"`，各 API 套件 `fail 0 / not_verified 0`，playwright `"passed": 37, "failed": 0, "skipped": 21` |
| `E/tests/sprint7/qa/sprint4/run.log` | 19:33 | USER-001/004/005/006/007/008/009/010、AUDIT-001、SEC-002（`:1` `==== Sprint 4 E2E 开始 2026-09-16T19:33:25+07:00 build=no-git ====`；`:186` `==== 结果：PASS=81 FAIL=0 NOT_VERIFIED=0 ====`） |
| `E/tests/sprint7/qa/sprint5/run.log` | 19:41 | USER-002/003/007/009、RBAC-004/007、DATA-001/003/004/005/006/007/008（末行 `==== Sprint 5 E2E 结束 2026-09-16T19:41:27+07:00 PASS=114 FAIL=0 NOT_VERIFIED=0 ====`） |
| `E/tests/sprint7/qa/sprint6/run.log` | 19:42 | ADMIN-001…010、AUDIT-002/003/004（`:1` 自记 `build=7831bff0…`，末行 `PASS=159 FAIL=0 NOT_VERIFIED=0`） |
| `E/tests/sprint7/qa/iam/console.log` | 19:42 | AUTH-001/002/003/009/011/012、RBAC-008、SEC-001、AUDIT-001/002/004（末行 `PASS=33 FAIL=0`） |
| `E/tests/sprint7/qa/rbac/console.log` | 19:42 | RBAC-001/002/003/004/005/006/007/008（末行 `PASS=29 FAIL=0 BLOCKED=2`） |
| `E/tests/sprint7/qa/sprint12/console.log` | 19:42 | AUTH-003/005/006/007/008、SEC-003/004、AUDIT-002（末行 `PASS=19 FAIL=0 BLOCKED=1`） |
| `E/tests/sprint7/qa/featureflags/{console.log,run.log,result.json}` | 19:42 | ADMIN-010 的真实请求路径旁证（`==== 结果：PASS=21 FAIL=0 ====`） |
| `E/tests/sprint7/qa/ui/playwright.log` + `ui/result.json` | 19:48 | USER-011（skip）/USER-012、UX-001/002/003/004、SEC-002、DATA-002（经 spec 行号）、ADMIN-003/004/006/010 的浏览器半句（`37 passed (5.9m)`） |
| `E/tests/sprint7/qa/ui-extended/playwright-extended.log` + `ui-extended/result.json` | 19:55 | UX-005（reduced-motion 档位）、AUTH-009、RBAC-001/008（cross-role 档位）（`29 passed (4.8m)`） |
| `E/release-20260916T125603Z/manifest.txt` | 19:56 | 冻结件复核（api/worker/migrate + 两套前端 + auditverify） |
| `E/ROUND-20260916-defect-closure.md` | 19:56 | AUTH-004 的 live 结论、D-01…D-19 的实测数字（**报告，不是原始日志**） |

---

## 3. 门禁脚本与结果文件（只读清点）

### 3.1 `acceptance-evidence/dual-system-v1/tests/*.sh`

| 脚本 | mtime | 是否有结果文件 |
|---|---|---|
| `run-sprint7-qa.sh` | 2026-09-16 20:32 | **有**：`E/tests/sprint7/qa/summary.json` + `summary.txt`（19:48） |
| `run-sprint4-e2e.sh` | 2026-09-16 12:31 | **有**：`qa/sprint4/{run.log,console.log}`（19:33） |
| `run-sprint5-e2e.sh` | 2026-09-16 18:18 | **有**：`qa/sprint5/{run.log,console.log,result.json}`（19:41） |
| `run-sprint6-e2e.sh` | 2026-09-16 14:56 | **有**：`qa/sprint6/{run.log,console.log,result.json}`（19:42） |
| `run-rbac-e2e.sh` | 2026-09-16 20:32 | **有**：`qa/rbac/console.log`（19:42） |
| `run-iam-e2e.sh` | 2026-09-16 12:53 | **有**：`qa/iam/console.log`（19:42） |
| `run-sprint12-e2e.sh` | 2026-09-16 20:30 | **有**：`qa/sprint12/console.log`（19:42） |
| `run-sprint7-featureflags.sh` | 2026-09-16 19:31 | **有**：`qa/featureflags/{console.log,run.log,result.json}`（19:42） |
| `run-sprint7-ui.sh` | 2026-09-16 17:40 | **有**：`qa/ui/{playwright.log,result.json,entry-preflight.txt}`（19:42–19:48） |
| `run-sprint7-ui-extended.sh` | 2026-09-16 17:40 | **有**：`qa/ui-extended/{playwright-extended.log,result.json}`（19:55） |
| `run-sprint7-sec-negative.sh` | 2026-09-16 12:31 | **只有旧结果**：`E/sprint7/security/negative-tests/{run.log,result.json}` 是 **2026-09-12T18:58:32Z→18:58:59Z**（`build_api_sha=d221dfef…`，`PASS 97 / FAIL 0 / NV 0`）；**2026-09-16 无重跑结果** |
| `run-sprint7-secret-scan.sh` | 2026-09-12 23:49 | **只有旧结果**：`E/sprint7/security/secret-scan/REPORT.md`（2026-09-12T23:51:51+07:00） |
| `run-fault-drill-guarded.sh` | 2026-09-13 03:11 | 部分：`qa/guarded-runs/20260912T200749Z-run-sprint5-e2e/`（`frozen-manifest.txt`、`drill/`、`preserved-abort/`，2026-09-13） |
| `run-guarded-fault-suite.sh` | 2026-09-16 12:19 | **无独立结果文件** |
| `run-sprint7-rerun.sh` | 2026-09-16 12:19 | **无独立结果文件**（输出写回 `qa/rbac`、`qa/iam`、`qa/ui`） |
| `run-r3-legacy-ui.sh` | 2026-09-16 12:19 | 输出落在 `E/sprint7/r3-*`（2026-09-13） |
| `bootstrap-e2e-env.sh` | 2026-09-16 13:36 | 无结果文件（环境准备） |
| `run-rbac-e2e.sh.result.txt` | 2026-09-12 22:54 | 是**结果文本**（2032 B），不是脚本 |

### 3.2 `scripts/perf/`、`scripts/backup/`、`scripts/release/`

| 脚本 | 结果文件 |
|---|---|
| `scripts/perf/run-perf-suite.sh`（2026-09-13 01:17） | **有**：`E/sprint7/perf/summary.json` + 19 个场景 json（见 §4.1）；该目录**没有** `README.md` |
| `scripts/backup/backup-all.sh`、`restore-drill.sh`、`kill9-drill.sh`、`pitr-drill.sh`、`minio-metadata-verify.sh`、`containers-watchdog.sh`、`README-DR-FIXES.md`（均 2026-09-13） | **有**：`E/sprint7/dr/*`、`E/sprint7/backups/pre-s7-pg-20260912T162853Z.dump`（85,948,607 B，与 `ls` 实测一致）；**`scripts/backup/` 目录内本身没有任何结果文件** |
| `scripts/release/freeze-manifest.sh`（2026-09-16 19:19） | **有**：`E/release-20260916T125603Z/manifest.txt`（19:56）；另有 `release-20260916T115912Z/manifest.txt`（18:59，api=`f10a1421…`）、`release-20260916T061304Z/manifest.txt`（13:13）、`release-20260916T115855Z/manifest.txt` |
| `scripts/release/build-and-freeze{,-v2}.sh`、`deploy-binaries.sh`、`secret-scan*.sh`、`strict-*.{sh,py}` 等 40+ 个脚本 | 目录内无结果文件；产物落在 `E/sprint7/` 下对应证据目录 |

---

## 4. 任务点名的证据根：存在性与数字

### 4.1 `sprint7/perf/` — 存在（VM 实际路径 `E/sprint7/perf/`，**不是** `E/tests/sprint7/perf/`）

- `E/tests/sprint7/` 下**只有** `perf`、`qa`、`release` 三个子目录（见 §5）。
- `E/sprint7/perf/summary.json`（mtime 2026-09-13 02:05）**存在**，`"build_api_sha": "0971a2b269d179b710dd8f2f6c5359464e31d2608f9f1ad7059c22b97c6e2f83"`（旧件），并发 8、每场景 20 s，共 19 个 label。
  关键读数（p95 ms）：`lan_graph_flow 21058.22`、`lan_longtable_p1 8617.83`、`wan_admin_overview 2872.66`、`lan_graph_overview 1273.02`、`wan_dashboard 1236.64`、`lan_longtable_assets 581.03`、`lan_admin_jobs 152.30`、`lan_graph_counter 49.44`、`lan_admin_audit 41.23`、`lan_admin_login_ev 37.89`、`nginx_admin_overview 27.33`、`lan_longtable_p2 5.80`。
  多个场景带 errors：`nginx_dashboard err=51930`、`lan_longtable_p2 err=66008`、`lan_graph_counter err=6403`、`wan_dashboard err=206`。
- `E/sprint7/perf/README.md` **不存在**（matrix §0.1 已注明它只在 Windows 工作树）。

### 4.2 `sprint7/security/` — 存在（`E/sprint7/security/`，mtime 2026-09-13）

- `negative-tests/`：`run.log` 末行 `==== S7-SEC-01 渗透负向用例结束 2026-09-13T01:58:59+07:00 PASS=97 FAIL=0 NOT_VERIFIED=0 ====`，头部 `build_api_sha=d221dfefb8052f8e973b5f068a3b89a998c517f4222e130896887e69554e2e05`；
  `result.json` `"pass": 97, "fail": 0, "not_verified": 0, "status": "PASS"`（`started_at 2026-09-12T18:58:32Z` / `finished_at 18:58:59Z`）；
  另有 `result.build-6106d8e4.json`、`result.prior-build-753ba724.json`、两个 `.INVALID-*.json`（构建漂移作废记录）。
- `secret-scan/REPORT.md`：结论 `(c) 类真实明文秘密 = 0`，脚本 SHA256 `96b178608587283218ab08ea14e23cc8bc59f21eb25681f77005404fd4480cdd`。
- `threat-matrix.md`（33,819 B）、`S7-SEC-01-completion.md`、`application-account-separation.md`、`dependency-scan/`、`image-iac/`、`sbom/`、`dependency-remediation-20260913T014342Z/` 均在。
- `E/sprint7/security/SUMMARY.md` **不存在**（matrix §0.1 已注明只在工作树）。

### 4.3 `sprint7/dr/` 与 `sprint7/db/` — 都存在（`E/sprint7/dr/`、`E/sprint7/db/`）

- `dr/restore-drill-20260912T175029Z.md`：PG `RTO（恢复到可用）= 5.81 s`、CH `RTO = 561.36 s`、MinIO `RTO = 0.63 s`；逐表比对 `transactions 35986150 / 35986150`、`logs 12345849 / 12345849`；RPO 说明 `合计 32 行`。
- `dr/pg-restore-20260912T163022Z.rto.txt`：`RTO_pg_restore_seconds=5.06`（更早一次）；`…counts.txt` 中 `provider_usage live=414691 restored=414663 delta=28`。
- `dr/kill9-20260912T180409Z.md`：worker `RTO（新进程存活）= 5.09 s`、`checkpoints 不回退（19966 → 19966）`、`残留 IN_FLIGHT 页（幽灵任务）= 0`；API `19.2 s`；NATS `未恢复 s`。
- `dr/dr-gate-20260912T222846Z.json`：`"verdict": "BLOCKED", "block_count": 4`（缺 restore-drill / kill9 / MinIO 元数据 / 消费者幂等的机器可读结果）；同名 `.md` 另记 `PITR verdict / archive_mode / RPO = PASS / on / 51s`。
- `dr/ERRATA-minio-metadata-v1-false-pass.md`、`pitr-20260912T222045Z.json`/`.md`、`dr-postverify-pitr-20260912T222045Z.json` 均在；`dr/README.md` **不存在**。
- `db/`：`pre-migration-20260912T162853Z.txt`（`bin sha 753ba724…`、`## schema_migrations count` → `55`）、`before.txt`/`after.txt`（Q1–Q10 查询计划）、`diff.txt`（`Q4_audit_seq_desc before=43.086 after=0.117`）、`verification-raw.txt`（`postgres/0056_s7_scope_backfill_constraints.sql`、`postgres/0057_s7_indexes_partitions.sql` 两行 applied）。**没有任何文件直接写 `57`**，「55 → 57」是从 `55` + 0056/0057 已应用推断。

### 4.4 `sprint7/backups/` — 存在

- `E/sprint7/backups/pre-s7-pg-20260912T162853Z.dump`（85,948,607 B，2026-09-12 23:29）+ `.dump.sha256`。**`tests/sprint7/backups/` 不存在**。

### 4.5 `sprint7/release/` — 存在

- `audit-actions-24h.txt`（21 行，2026-09-13）、`audit-coverage.log`（`wrote docs/implementation/audit-coverage.md：16 个认证事件常量（10 个实际会触发）、81 个审计权限键、11 个敏感权限`）、
  **`audit-coverage-summary.txt` = 0 字节**、`dual-write-check.md`、`entry-matrix.txt`、`policy-divergence.md`、`release-gate.md`、`uc-download-import-20260913T021911Z/`。

---

## 5. 找不到 / 不存在的东西（MISSING 清单）

| # | 缺失项 | 说明 |
|---|---|---|
| M-1 | `acceptance-evidence/dual-system-v1/tests/sprint7/security/` | **MISSING**（`ls` 报 No such file）。`tests/sprint7/` 下只有 `perf`、`qa`、`release`；安全证据实际在 `E/sprint7/security/`。 |
| M-2 | `E/tests/sprint7/{dr,db,backups}/` | **MISSING**；对应目录在 `E/sprint7/{dr,db,backups}/`。 |
| M-3 | `E/tests/sprint7/perf/summary.json` | **MISSING** 于该路径（`E/tests/sprint7/perf/` 整个目录不存在）；实际在 `E/sprint7/perf/summary.json`。 |
| M-4 | 2026-09-16 的安全负向重跑结果 | **MISSING**：`E/sprint7/security/negative-tests/result.json` 仍是 2026-09-12T18:58:59Z 的（`d221dfef…`）；`run-sprint7-sec-negative.sh` 已于 2026-09-16 12:31 更新但**没有对应的新结果文件**。 |
| M-5 | AUTH-004 MFA 的原始 live 测试日志 | **MISSING**：只有 `E/section27-conformance.md:20` 的叙述与 `E/sprint7/mfa-e2e-*/`、`mfa-reset-ui-*/`、`mfa-selfservice-ui-*/` 的 PNG 截图，无 `go test` 输出文件。 |
| M-6 | `scripts/test_migration_reentrancy.sh` 的运行输出 | **MISSING**：脚本存在，但全树没有它的结果文件；DATA-010 只有 `tests/DATA-010/result.json` 的自述与 `manifest.json` 的计数。 |
| M-7 | `E/sprint7/release/audit-coverage-summary.txt` | **存在但为空（0 字节）** → AUDIT-001 的「逐项执行 100%」无可复算对照。 |
| M-8 | `E/sprint7/dr/restore-drill-*.json`、`kill9-*.json`、`minio-object-metadata-*.json`、`consumer-idempotency-*.json` | **MISSING**（`dr-gate-20260912T222846Z.json` 的 4 条 block_reason 逐条点名）。 |
| M-9 | `E/sprint7/{perf,dr,security}/README.md`、`E/sprint7/security/SUMMARY.md` | **MISSING on VM**（matrix §0.1 记载只在 Windows 工作树）；`perf 说明文档` §5 悬空引用的 `export-pipeline.md` / `provider-pipeline.md` 在两棵树都不存在。 |
| M-10 | `E/tests/run-guarded-fault-suite.sh`、`E/tests/run-sprint7-rerun.sh` 的专属结果文件 | **MISSING**（两者把输出写进别的目录，本身无 result）。 |
| M-11 | 2026-09-16 UI/QA 运行器与产出证据的一致性 | **对不上**：`E/tests/run-rbac-e2e.sh`、`run-sprint12-e2e.sh`、`run-sprint7-qa.sh` 的 mtime 是 **20:30–20:32**，晚于它们产出的证据（19:41–19:48）与冻结清单（19:56）——磁盘上的脚本已不再等于产出那批证据的脚本。 |

---

## 6. 计数与结论

| freshness | 条数 | Test ID |
|---|---:|---|
| `current-build`（2026-09-16 QA 批次，锚定 `build_api_sha=7831bff0…`） | **59** | 除下列 6 条以外的全部 |
| `older-build`（更早构建 sha / 2026-09-12·13） | **6** | AUTH-004、AUTH-007、AUTH-010、USER-011、DATA-009、DATA-010 |
| `none`（VM 上无证据） | **0** | — |
| 合计 | **65** | |

**完全找不到证据（freshness = none）的 ID**：无。

**证据存在但不能真正覆盖场景（严格口径）的 ID**（20 条）：AUTH-004、AUTH-007、AUTH-010、AUTH-011、AUTH-012、RBAC-001、RBAC-008、USER-011、DATA-002、DATA-009、DATA-010、AUDIT-001、AUDIT-004、ADMIN-010、SEC-001、SEC-002、SEC-003、SEC-004、UX-001、UX-005。

逐条理由简述：

- **AUTH-004**：只有报告叙述 + 截图，无原始日志，且未锚定 `7831bff0…`。
- **AUTH-007**：`7831bff0…` 上是 `BLOCKED`；唯一的实测门禁跑在 `f59bff69…`（校准进程）/`f10a1421…`（生产进程）上。
- **AUTH-010**：当前构建批次没有 XFF / CF-Connecting-IP / proxy_chain 断言。
- **AUTH-011**：`501` 冒烟不等于 state/nonce/PKCE 错误处理。
- **AUTH-012**：当前构建只测 1 个载荷，6 载荷全量扫描在旧件。
- **RBAC-001**：套件实测标签是 `AUTHZ`，与 §27 的 ID 不一致；「按钮不可见」只有间接覆盖。
- **RBAC-008**：当前构建只有会话受众隔离，缺 API Key audience/scope 与 `allowed_ips` 回归门。
- **USER-011**：当前构建被 skip（`user-mobile` project 不在任何 2026-09-16 的 UI 运行器里）。
- **DATA-002**：日志行不含 ID，靠 `dual-system-sprint5.spec.ts:152-153` 行号推断，且不打印实测值。
- **DATA-009**：RPO 未达标（32 行暴露）、`blockchain-raw` 未覆盖、DR 门禁 `BLOCKED`。
- **DATA-010**：只有 `result.json` 自述「10/10」，没有可复算的脚本输出；`55 → 57` 是推断。
- **AUDIT-001**：`audit-coverage-summary.txt` 为 0 字节，缺「逐项执行 100%」对照。
- **AUDIT-004**：`seq 1–2343` 封存不可恢复，全链「任意篡改可检测」不成立。
- **ADMIN-010**：真实请求路径证据是整体开关 on/off，未证明「50% 灰度只影响目标 scope」。
- **SEC-001**：缺 `csrf_failed` 错误码、错误 token、跨站 Origin 与 R-02 残余断言。
- **SEC-002**：CSP / DB 层剥离断言在旧件；F-04 残余仍开放。
- **SEC-003**：出站守卫单测与 R-03 残余只在旧件。
- **SEC-004**：稳定错误码口径不一致（标签 `AUTHN_RATE_LIMITED` vs 实测 `RATE_LIMIT_EXCEEDED`）。
- **UX-001**：未单独断言「关闭后焦点回到触发元素」。
- **UX-005**：用例标题不含 ID，仅靠 spec 文件名对齐（场景本身已被浏览器用例覆盖）。

> 以上清单只陈述 VM 上**当前存在**的证据，未对任何场景作通过/不通过的验收判定。
