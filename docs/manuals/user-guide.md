# 用户手册（调查工作台 ChainSight）

> 适用对象：调查员、分析员、复核人、只读协作者（系统角色 `investigator` / `analyst` / `reviewer` / `viewer`）。
> 入口：用户层系统部署在 **`/app-v2/`** 前缀下（灰度期与旧工作台 `/` 并存）。
> 本文与真实前端代码一致：路由与按钮文案取自 `frontend/apps/user-console/src/App.tsx` 与各页面组件（2026-09-12 读取）。
> 关联文档：`docs/manuals/permissions.md`（权限）、`docs/manuals/troubleshooting.md`（故障）。

---

## 1. 登录与账号

### 1.1 登录

1. 打开用户层入口（灰度期为 `https://<域名>/app-v2/`；内网直连为 `http://127.0.0.1:3001/app-v2/`）。
2. 未登录时只会渲染登录页（页面卡片标题：**ChainSight 调查工作台**）。
3. 输入**工作邮箱**与**密码**，点击 **登录**（提交中按钮变为「正在登录…」并禁用）。
4. 成功后进入 `/home`；若 `return_to` 合法则跳回原页面。

登录页上的其它控件：

- **使用组织账号登录**：走 OIDC。当前环境 **OIDC 未启用**（`GET /api/auth/v1/methods` 返回 `"oidc":false`，
  `/api/auth/v1/oidc/start` 返回 501 `OIDC_NOT_CONFIGURED`）。
- **登录说明**：以弹窗展示（页面正文不含说明段落）。
- **返回旧工作台**：跳回 `/`。

### 1.2 密码与会话规则

| 项 | 规则 | 来源 |
|---|---|---|
| 密码最小长度 | 12 字符（允许长密码与密码管理器，不强制符号组合） | `AUTH_MIN_PASSWORD_LENGTH=12` |
| 会话空闲超时 | 普通用户按部署配置（`.env` 的 `AUTH_SESSION_IDLE_TTL_SECONDS`，示例值 43200 秒 = 12 小时） | `deploy/.env.example` |
| 会话绝对超时 | 按 `AUTH_SESSION_ABSOLUTE_TTL_SECONDS`（示例值 604800 秒 = 7 天） | 同上 |
| 登录失败锁定 | 连续失败 `AUTH_MAX_FAILED_LOGINS`（示例 10 次）后锁定 `AUTH_LOCK_DURATION_SECONDS`（示例 900 秒） | 同上 |
| 登录限流 | 每 IP 每分钟 `AUTH_LOGIN_RATE_PER_MINUTE`（示例 20）次 | 同上 |
| MFA（两步验证） | **已实现（2026-09-16）**：在「个人资料与安全」中启用并保存恢复码后，登录需要认证器 6 位验证码；认证器丢失可用恢复码（每个只能用一次），或请管理员在后台重置 | ADR-017 §3 |
| 登录错误提示 | 统一「账号或凭证无效」弹窗，不区分账号是否存在；失败详情只进安全日志 | §18.2 |

> 忘记密码：当前没有自助重置入口。请联系管理员通过后台「用户与成员」执行重置密码；
> 重置后你会被要求首次登录时修改密码（`must_change_password=true`）。

### 1.3 修改密码与退出其它设备

进入 **个人资料与安全**（`/profile`）：

> 该页面下的「多因素认证（MFA）」区块可以自助启用两步验证：点「启用」后扫二维码（或手工输入
> base32 密钥）→ 输入认证器上的一次性验证码确认 → **弹窗一次性展示 10 个恢复码**，必须勾选
> 「我已保存」才能关闭，此后再也无法查看。启用后每次登录都需要第二步；关闭 MFA 在同一个区块内。


- **保存**：更新个人资料。
- **修改密码**：弹窗输入当前密码与新密码。
- **退出其它设备** / 对单个会话点 **撤销**：撤销该会话；「退出其它设备」对应
  `POST /api/auth/v1/sessions/revoke-all`。

---

## 2. 界面总览

### 2.1 导航结构（与代码一致）

| 分组 | 菜单项 | 路由 |
|---|---|---|
| 工作台 | 我的概览 | `/home` |
| 工作台 | 全局搜索 | `/search` |
| 案件与调查 | 案件中心 | `/cases` |
| 案件与调查 | 调查任务 | `/investigations` |
| 案件与调查 | 资金图谱 | `/graph` |
| 数据工具 | 地址与交易查询 | `/explorer` |
| 数据工具 | 数据下载 | `/downloads/new` |
| 数据工具 | 任务中心 | `/jobs` |
| 数据工具 | 导出中心 | `/exports` |
| 协作 | 待我处理 | `/inbox` |
| 协作 | 报告中心 | `/reports` |
| 协作 | 通知 | `/notifications` |
| 个人 | 个人资料与安全 | `/profile` |

顶栏（AppShell）：品牌名与标签、**收起导航**、移动端 **打开主导航**（汉堡按钮）、当前用户与角色、**退出**。

### 2.2 提示一律是弹窗（重要交互约定）

本系统**没有**横幅、提示卡、警告条或常驻说明段落。所有提示、说明、成功、错误、风险、确认都以**弹窗**呈现：

| 弹窗类型 | 角标 | 默认按钮 | 典型用途 |
|---|---|---|---|
| `info` | 提示 | **知道了** | 数据口径、字段解释、Coverage 说明 |
| `success` | 已完成 | **知道了** | 操作成功 |
| `warning` | 注意 | **知道了** | 风险提示 |
| `error` | 错误 | **知道了** | 失败原因；详情区显示键值表（含 Trace / 错误码） |
| `confirm` | 请确认 | **取消** / **确认** | 危险操作；可要求输入确认词 |
| 输入弹窗 | 请确认 | **取消** / **确定** | 填写原因等 |

弹窗支持：焦点陷阱、`ESC` 关闭、关闭后焦点回到触发元素、长内容在弹窗体内滚动。
若看到「**口径**」「**说明**」按钮，点击即打开解释性弹窗——这是本系统放置说明的正确位置。

---

## 3. 我的概览（`/home`）

- 页面标题：**你好，{显示名}**。
- **工作台口径**按钮：弹窗说明各项指标的口径与时间窗。
- 内容来自单个聚合接口 `GET /api/v2/dashboard`（同一 `as_of` 快照），不需要手动拼多个页面。
- 卡片点击进入对应筛选后的列表；没有数据时给出「发起调查」或「查询地址」入口。

---

## 4. 全局搜索（`/search`）

1. 在搜索框输入关键字，点击 **搜索**（按钮在提交中显示「搜索中…」）。
2. 结果按类型分组（地址、交易、案件、调查、任务、报告等）；**只返回你有权访问的对象**——无权对象不会出现。
3. 点击结果跳转到对应详情页。

> 前端对输入做 250ms debounce 并取消旧请求（§30.1），不会每次按键都发请求。
> 权限键：`search:read`。

---

## 5. 案件

### 5.1 案件中心（`/cases`）

- 页面标题：**案件中心**；支持搜索与分页。
- **新建案件**：进入 `/cases/new`。
- 权限键：`case:list`（`viewer` 也有）。

### 5.2 新建案件（`/cases/new`）

1. 填写基本信息（名称、编号、描述、密级、优先级、工作区、成员、标签）。
2. 点击 **创建案件**（提交中为「正在创建…」）；**取消**返回列表。
3. 成功后进入案件详情。

权限键：`case:create`（`viewer`/分析员之外的角色注意：`viewer` 无此权限，按钮不可见且直接调 API 也会被拒绝）。

### 5.3 案件详情（`/cases/:caseId/:tab`）

页面标题为案件名称；二级 Tab 通过 URL 直达（可分享链接）。主要操作按钮及所需权限：

| 操作 | 按钮 | 权限 |
|---|---|---|
| 启用 | 启用 | `case:state:update` |
| 关闭 | 关闭 | `case:state:update` |
| 归档 | 归档 | `case:state:update` |
| 重新打开 | 重新打开 | `case:state:update` |
| 发起调查 | 发起调查 | `investigation:create` |
| 添加成员 | 添加成员 | `case:member:manage` |
| 移除成员 | 移除 | `case:member:manage` |
| 新增对象 | 新增对象 | `subject:create`（移除用 `subject:remove`） |
| 生成报告 | 生成报告 | `report:create` |
| 下载报告 | 下载票据 `/reports/{id}/download-ticket` | `report:download` |

案件状态机（§20.2）：

```text
draft -> active -> under_review -> closed -> archived
  |        |            |            |
  +--------+------------+------------+-> reopened(active)
```

- `archived` 为只读；恢复后回到 `active`。
- 关闭前必须处理所有待复核 Finding 与未完成的报告审批，或填写例外原因。
- 修改状态、归档、成员变化都会弹出确认框并**要求填写原因**。
- 并发修改：若看到 **412 冲突** 弹窗，说明他人已先修改；刷新后按提示比较版本，**不会静默覆盖**。

### 5.4 案件相关子资源

| 子资源 | 接口 | 说明 |
|---|---|---|
| 成员 | `/api/v2/cases/{id}/members` | 权限 `case:member:list` / `case:member:manage` |
| 对象（Subjects） | `/api/v2/cases/{id}/subjects` | 权限 `subject:list` / `subject:create` / `subject:remove` |
| 动态（业务时间线） | `/api/v2/cases/{id}/activity` | 权限 `case:activity:read`；**这是业务时间线，不等于审计日志** |
| 评论 | `/api/v2/cases/{id}/comments` | 权限 `comment:list` / `comment:create` |
| 报告 | `/api/v2/cases/{id}/reports` | 权限 `report:list` / `report:create` |

---

## 6. 调查

### 6.1 调查任务列表（`/investigations`）

- 页面标题：**调查任务**；**发起调查**进入 `/investigations/new`。
- 权限键：`investigation:list`。

### 6.2 发起调查（`/investigations/new`）

表单采用真实顺序：**对象 → 链与范围 → Dataset → 复用策略 → Coverage/成本预览 → 确认创建**。

1. 选择案件、填写调查目标。
2. 添加对象（地址/域名/IP 等），选择链。
3. 点击 **Coverage 预览**（`POST /api/v2/investigations/preview`）：返回本地覆盖、缺口区间、预计行数/成本/时间。
4. 点击 **创建调查**（提交中为「处理中…」）。创建请求必须带 `Idempotency-Key`，重复提交不会产生两个任务。

权限键：`investigation:create`。默认复用本地数据（`prefer_local`）；选择刷新会新增 Provider 取数，需在弹窗中确认。

### 6.3 调查工作台（`/investigations/:id/workbench`）

页面标题为调查目标。阶段轨道：**数据准备 → 画像 → 资金流 → 语义 → 模式 → 复核**。
三栏结构：左侧 Steps、中间当前结果、右侧 Evidence Rail（来源 / confidence / data version / hash / refs）。

控制按钮与权限：

| 按钮 | 权限 |
|---|---|
| 启动 | `investigation:start` |
| 暂停 | `investigation:pause` |
| 恢复 | `investigation:resume` |
| 重试 | `investigation:retry` |
| 提交复核 | `investigation:submit_review` |
| 取消 | `investigation:cancel` |
| Findings | `finding:list` |
| 刷新 | `investigation:read` |

调查状态机（§20.3）：

```text
draft/created -> running -> waiting_data -> running
                         -> waiting_review -> completed
running -> paused -> running
running/waiting/paused -> cancelled
running -> failed_retryable -> running
running -> failed_final
```

要点：

- 状态只能通过**显式命令接口**（`/commands/{start|pause|resume|retry|cancel|submit-review}`）改变，没有通用 PATCH 改状态。
- 每个结论都展示 `nature`、`confidence`、`algorithm_version`、`data_version` 与证据边界。
- `NET_FLOW_ONLY` 结论**不得**命名为"利润"；`INFERRED` 结论**不得**命名为"身份确认"。
- 运行状态通过 SSE（`GET /api/v2/events` 等）更新；断线重连使用 `Last-Event-ID`，并用 REST 快照校准。

### 6.4 Findings 复核（`/investigations/:id/findings`）

- 页面标题：**Findings 复核**。
- 按钮：**Verify**、**Confirm**、**Reject**、**Supersede**。
- 权限：查看 `finding:list`/`finding:read`；复核 `finding:review`；证据状态 `evidence:review`。
- 状态机：`candidate -> in_review -> confirmed | rejected | superseded`。
- `confirmed` 的 Finding 至少引用一个 `verified` Evidence，或记录经过审批的例外。
- 点击「确认 Finding」时，弹窗会列出引用的 Evidence 及其验证状态；不满足规则时主操作被禁用。

### 6.5 证据详情（`/investigations/:id/evidence/:evidenceId`）

- 按钮：**返回 Findings**、**Verify**、**Dispute**、**Supersede**。
- 权限：`evidence:read`；复核 `evidence:review`；读取敏感正文 `evidence:sensitive:read`（`critical`）。
- 证据状态机：`collected -> verified | disputed -> superseded`。
- 内容**不可覆盖**：修正会生成新版本并用 `supersedes` 关联。
- 页面固定展示 source refs、content hash、provenance、采集/验证时间、supersedes 链。

---

## 7. 地址与交易查询（Explorer）

### 7.1 查询入口（`/explorer`）

- 页面标题：**地址与交易查询**。
- 输入链 + 地址/交易哈希，点击 **查询**；**打开**进入详情；**说明**弹窗解释查询口径。
- 权限键：`explorer:read`。

### 7.2 地址详情（`/addresses/:chain/:address`）

- Tab 顺序：**概览、资产、普通交易、Token Transfer、Internal、对手方、语义事件、分析、Coverage**。
- 按钮：**口径说明**（弹窗）、**复制地址**、**退出全量视图**、**回到最新**。
- 要点：
  - 资产页把「当前 Provider 余额」与「本地历史净流量」**分开**；`is_current_balance=false` 的行**不并入**当前余额总计。
  - 金额默认人类可读，可切换原始整数；复制永远复制完整值（不会因 JS 安全整数丢失精度）。
  - 表格筛选与分页游标写入 URL，可分享；查询参数摘要进入审计。
  - 数据集不完整时只显示业务状态（例如「Coverage 78%」），解释在 **Info 弹窗**里。

### 7.3 交易详情（`/transactions/:chain/:hash`）

- 按钮：**复制哈希**、**展开/收起**、**向 Provider 补取 Trace**（需要 `explorer:fetch`）。
- 内容：交易、回执、内部调用、日志、方法解析（ABI decode）、证据引用。
- Trace/Logs 缺失时显示业务空状态；若触发 Provider 补取，先用弹窗告知成本与数据来源。

---

## 8. 数据下载与任务

### 8.1 新建数据下载（`/downloads/new`）

顺序表单：**对象 → 链与范围 → Dataset → 复用策略 → Coverage/成本预览 → 确认创建**。

1. 添加地址：支持粘贴与批量导入；**解析导入**先在浏览器内显示解析摘要，服务端会再校验一次。
2. 选择链与范围（全历史 / 区块范围 / 时间范围 / 从上次同步继续）。
3. 选择 Dataset（选项来自 capabilities 接口，不在前端写死）。
4. 点击 **立即预览**（`POST /api/v2/downloads/preview`）查看本地覆盖、缺口、预估行数/成本/时间与复用的既有任务。
5. 点击 **创建下载任务**（提交中为「正在创建…」）；**取消**放弃；**任务中心**前往 `/jobs`。

要点：

- 默认 **`prefer_local`**（优先复用本地已有数据）；选择刷新会新增 Provider 取数，必须在确认弹窗中确认。
- 重复判断键是 `chain + 规范化地址`；相同输入默认复用进行中的任务。
- 创建接口必须带 `Idempotency-Key`。
- 权限键：`download:create`（`download:preview` 语义相同），`viewer` 无此权限。

### 8.2 任务中心（`/jobs`）

- 页面标题：**任务中心**；**刷新**、按来源筛选、进入**详情**。
- 权限键：`job:list:self`（默认只显示本人/有权任务）；`viewer` 只有 `job:read`/`job:list:self`。

### 8.3 任务详情（`/jobs/:jobId`）

- 按钮：**刷新详情**、**暂停**、**恢复**、**取消任务**、**重试失败分片**、**全部收起/展开**、**导出中心**、**新建导出**、**下载票据**。
- 权限：`job:read`（查看）、`job:control`（暂停/恢复/取消/重试）。
- 结构：**DownloadJob → Subject → Dataset → Shard**；地址进度**逐地址**显示，不用 Job 聚合进度掩盖卡住的地址。
- 顶部展示 `requested/effective` 性能模式、成功游标时间、wall-clock rows/s、Provider rows/s——**口径不可混合**。
- 日志只显示结构化事件；错误点击后以弹窗展示详情与 Trace。
- SSE 与轮询按 `revision` 合并，旧 revision 事件不会覆盖新状态。

---

## 9. 导出中心（`/exports`）

- 页面标题：**导出中心**；**刷新**、**新建导出**、**创建并下载**、**下载票据**。
- 权限：`export:list`（列表）、`export:create`（创建）、`export:download`（下载，`sensitive`）。
- 下载通过**一次性短票据**完成（默认 60 秒过期、单次使用、绑定用户/资源）。
- 导出记录包含格式、状态、行数、文件哈希、大小、有效期与 Range 支持。
- 文件下载完成后页面可显示服务端 hash；**真正的校验必须由下载客户端完成**——浏览器不会自动验证哈希。

---

## 10. 资金图谱

### 10.1 图谱列表（`/graph`）

- 页面标题：**资金图谱**；**刷新**、**新建画布**、**查看说明**。
- 权限键：`canvas:list`。

### 10.2 画布（`/canvases/:canvasId`）

- 三栏：左侧 **Lens**（资金流/时间/资产/风险/实体/证据层）、中间 **Canvas**、右侧 **Inspector / Evidence Rail**。
- 顶部：搜索、深度、预算、布局、保存、**分支**、快照、**导出**。
- 权限：`canvas:read`（查看）、`canvas:update`（编辑）、`canvas:branch`（分支）、`canvas:snapshot` / `canvas:snapshot:create`（快照）。
- 1000+ 节点启用 LOD/聚合；达到硬预算（2000 节点）时停止扩展并弹窗说明。
- 保存使用 `If-Match`；并发冲突返回 412，弹窗比较两个版本，不静默覆盖。
- **跨案件引用会被拒绝**（`BUSINESS_CROSS_CASE_EVIDENCE` 422）。

---

## 11. 报告

### 11.1 报告中心（`/reports`）

- 页面标题：**报告中心**（跨案件）；权限键 `report:list`。

### 11.2 报告详情（`/reports/:id`）

- 页面标题：**报告 revision {N}**。
- 按钮与权限：

| 按钮 | 权限 |
|---|---|
| 返回 | `report:read` |
| 提交复核 | `report:create` |
| 批准 | `report:review` |
| 驳回 | `report:review` |
| 签署 | `report:sign`（`critical`，默认只有 `reviewer`/`owner`） |
| 归档 | `report:review` |
| 下载（票据） | `report:download` |

- 状态机（§20.6）：

```text
draft -> generated -> in_review -> approved -> signed -> archived
                     -> rejected -> generated(new revision)
```

- 报告 revision 绑定 Investigation、Finding、Evidence、Canvas Snapshot 的**精确版本**；
  已批准/签署的文件不可覆盖，重新生成产生新 revision 与新 hash。
- 页面展示 artifact 版本快照，**不会**用"当前最新结果"动态替换已生成报告的内容。

---

## 12. 待我处理与通知

### 12.1 待我处理（`/inbox`）

- 页面标题：**待我处理**；汇总待复核、待审批、失败任务与 @提及。权限键 `inbox:read`。

### 12.2 通知（`/notifications`）

- 页面标题：**通知**；**显示全部 / 只看未读**、**查看对象**、**标记已读**。
- 权限键：`notification:list`（查看）、`notification:update`（标记已读）。
- 「查看对象」会先校验对象可访问性；**无权时安全失败**（不会跳转到无权页面）。

---

## 13. 移动端使用

本系统有真实响应式实现（不是缩放桌面版）：

| 断点 | 行为 | 实现位置 |
|---|---|---|
| ≤900px | 左侧导航变为抽屉：汉堡按钮（aria-label **打开主导航**）打开，带遮罩；顶栏取消 sticky 并换行；用户名截断 | `packages/ui/ui.css` |
| ≤1100px | 工作台三栏改为纵向堆叠，Evidence Rail 占满宽度 | `packages/ui/ui.css` |
| ≤960px | 案件相关网格与表单单列化，Evidence Rail 自适应 | 用户端 `sprint4.css` |
| ≤640px | 列表项纵向排列，页头操作区占满宽度、按钮等宽 | 用户端 `sprint4.css` |
| ≤1100px / ≤760px | 图谱三列收两列 / 单列，画布高度降到 320px，宽表格横向滚动 | 用户端 `sprint5.css` |
| 系统设置 | `prefers-reduced-motion: reduce` 时关闭非必要动画 | `packages/design-tokens/tokens.css` |

手机上的通用约定（§18.13）：

- 列表页默认卡片显示 4–6 个关键字段，次要字段进详情；批量操作进入底部弹出的确认弹窗。
- 表单一列；主操作固定在底部时会为内容留出安全区，不遮挡焦点。
- 所有提示仍是弹窗，弹窗可滚动、可用 `ESC` 关闭、关闭后焦点回到触发按钮。

> 用户端的移动端适配只覆盖用户层系统；后台管理系统没有应用级移动端样式，
> 管理操作请在桌面或平板（≥900px）上完成。

---

## 14. 键盘与无障碍

- 全流程可仅用键盘完成：Tab 顺序可见、无焦点陷阱（弹窗内为受控焦点循环，关闭后归还焦点）。
- 弹窗：`role="dialog"` + `aria-modal`，支持 `ESC` 关闭与遮罩点击关闭。
- 状态不只靠颜色表达（§27 UX-003 要求对比度 4.5:1）。
- 无障碍名称包含关键信息（例如告警条目会读出严重级别与数量）。

---

## 15. 常见错误与处理

| 现象 | 含义 | 处理方法 |
|---|---|---|
| 弹窗「账号或凭证无效」 | 登录失败（不区分账号是否存在） | 核对邮箱/密码；连续失败会临时锁定 |
| 页面空白但接口 200 | 会话受众与入口不匹配 | 使用 `/app-v2/` 入口重新登录；清除旧 Cookie |
| 按钮不可见 | 当前角色缺少该权限（前端按权限隐藏） | 查看 `docs/manuals/permissions.md`；直接调 API 同样会被 403 |
| 403 `permission_denied` | 角色缺少权限 | 联系管理员补授权（附错误弹窗中的 Trace） |
| 404 | 对象不存在或无权访问（统一按不可见处理） | 确认案件/工作区归属 |
| 409 `CONFLICT_STATE` | 当前状态不允许该操作 | 刷新查看最新状态 |
| 409 `CONFLICT_CANVAS` / 412 | 并发编辑冲突 | 弹窗会比较版本，按提示合并，不会覆盖他人数据 |
| 429 | 触发限流 | 稍后重试；弹窗会带 Trace |
| 501 `NOT_IMPLEMENTED` | 该能力未实现（当前如 OIDC 未配置；MFA 已于 2026-09-16 实现） | 属预期行为，不要当作缺陷 |

---

## 16. 待确认项

1. 各页面「查看说明/口径」弹窗的具体字段清单随页面迭代，本文只给出用途；
   以页面实际弹窗内容为准。
2. `/investigations/:id/workbench`、`/cases/:caseId/:tab`、`/canvases/:canvasId`、
   `/investigations/:id/evidence/:evidenceId` 的页面标题来自后端数据（目标、案件名、画布名、证据标题），没有固定文案。
3. 用户端目前没有自助注册、没有忘记密码入口、没有 MFA 绑定入口（均未实现）。
4. 旧工作台（`/`）在灰度期仍然可用，与新用户层共用同一会话 Cookie；旧工作台的停止计划见
   `docs/adr/AD-008-gray-release-paths.md` 与 `docs/implementation/dual-system-progress.md`。
