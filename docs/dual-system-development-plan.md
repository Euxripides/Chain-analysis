# 链上调查平台双系统完整化开发计划

> 文档状态：Implementation Blueprint v2.0  
> 适用仓库：`harness/blockchain-platform`  
> 目标：将当前工程型单体前端升级为“用户层系统 + 后台管理系统”，共用同一套可信数据、任务、证据和审计底座。
> 执行方式：其他 Agent 可按本文的阶段、接口、页面、迁移和验收编号直接开发；未写入本文的架构变化必须先新增 ADR。

## 0. 文档使用说明

本文同时承担产品需求、交互规格、技术设计、接口契约、安全设计、迁移计划和验收计划，不再只是路线图。

实施者必须按以下优先级解释需求：

1. 仓库根目录 `AGENTS.md` 与 `docs/code-standards.md` 的强制规则。
2. 本文“不可变决策”和页面/API/数据契约。
3. 现有 `docs/phase0-architecture-baseline.md` 的稳定 Contract 与证据边界。
4. 现有代码行为；若与前三项冲突，先记录差异并按增量迁移修复。

本文中的状态标识：

- **EXISTING**：已有实现，迁移时必须保持兼容并重新验收。
- **MODIFY**：已有基础，但需要改造。
- **NEW**：需要新增。
- **FUTURE**：只预留扩展点，本期不得伪装为已实现能力。

每个开发任务的最小交付物：实现代码、迁移或契约、单元/集成测试、真实页面 E2E、审计事件、监控指标、文档和回滚说明。只提交 UI、只提交接口或只通过构建均不算完成。

快速导航：

- 第 1–14 节：产品边界、双系统范围、角色、路线图与完成标准。
- 第 15–17 节：不可变架构、代码组织、完整设计系统。
- 第 18–20 节：用户/后台逐页设计和状态机。
- 第 21–23 节：API、数据库和安全设计。
- 第 24–26 节：事件、可观测性、SLO 与扩展机制。
- 第 27–30 节：测试、Agent 施工顺序、CI/CD 和容量。
- 第 31–33 节：交付物、接手约束和需求追踪矩阵。

## 1. 结论与实施原则

本项目不应重写现有链上数据引擎，也不建议立即拆成大量微服务。推荐方案是：

1. 保留 Go modular monolith、PostgreSQL、ClickHouse、MinIO、NATS 作为共享平台核心。
2. 将当前混合前端拆成两个独立应用：
   - **用户层系统（Investigator Console）**：面向调查员、分析员和只读协作者，以案件和调查任务为中心。
   - **后台管理系统（Admin Console）**：面向平台管理员、审计员和运维人员，以身份权限、任务控制、数据治理、成本、告警和审计为中心。
3. 两套前端共用设计系统、类型化 API SDK、认证会话和领域模型，但独立构建、独立部署、独立路由授权。
4. 先补齐 IAM、RBAC、租户边界和不可抵赖审计，再迁移业务页面；未完成真实鉴权前，不把后台系统暴露到公网。
5. 每个里程碑均按“页面交互 + API + 数据库 + 文件产物 + 运行状态 + 审计证据”验收，不能只以构建成功或 HTTP 200 作为完成标志。

## 2. 当前基线与主要问题

### 2.1 已有能力，可直接复用

- 数据下载：Coverage 感知、地址级增量复用、暂停/恢复/取消、Checkpoint、SSE 进度。
- 数据查询：地址、交易、Token Transfer、Internal Transaction、日志、Trace、方法解析。
- 调查能力：Investigation、运行阶段、Finding、Evidence、Graph Patch、Canvas、Snapshot。
- 分析能力：一跳资金流、语义事件、基础高级模式、关系线索。
- 交付能力：CSV/XLSX/Parquet/Markdown 报告、MinIO 对象存储和哈希。
- 控制能力：Provider 状态、使用量、预算、告警、Kill Switch、性能模式和请求审计基础。
- 基础设施：PostgreSQL 控制面、ClickHouse 数据面、MinIO 原始数据/报告、NATS 事件总线。

### 2.2 当前结构性缺口

| 领域 | 当前状态 | 必须补齐 |
|---|---|---|
| 应用边界 | 用户功能与 API 控制、设置共用一个 SPA 和导航 | 两套独立前端、清晰 URL、独立权限与发布 |
| 身份认证 | 默认 `admin@local`，请求头可直接指定用户和角色 | OIDC/JWT 或安全本地账户、会话、退出、失效、MFA 预留 |
| 授权 | 单一 `users.role`，部分接口靠局部判断 | 组织/工作区成员关系、角色权限表、统一中间件、对象级授权 |
| 审计 | 有 `audit_events`，但覆盖、查询和导出不完整 | 用户层全部业务操作、所有登录尝试、管理操作、敏感读取和导出的完整审计链 |
| 租户隔离 | 表中已有部分 organization/workspace 字段，但未系统执行 | 查询强制作用域、跨租户负向测试、对象存储路径隔离 |
| 产品体验 | 页面偏工程工具，部分入口只是静态外观 | 可执行全局搜索、通知、个人中心、空状态、错误恢复和引导 |
| 管理能力 | API Control 已有较多功能，但不是完整后台 | 用户权限、系统配置、数据治理、审计中心、版本发布和备份恢复 |
| 质量体系 | 已有若干 E2E，但未按两套系统建立发布门 | 权限矩阵、桌面/移动、无障碍、安全、性能和灾备验收 |
| 分析完整度 | 已有一跳和启发式模式 | 多跳、实体标签、价格/成本/利润、协议/跨链仍需分阶段建设 |

### 2.3 本轮规划假设

- 产品首先服务内部调查与审计团队，不按公众消费级 SaaS 设计。
- 一套部署可服务多个组织和工作区，但第一阶段允许只启用单组织。
- 用户层系统需要桌面端完整体验和移动端查询/审批/查看能力；大型图谱编辑以桌面端为主。
- 后台管理系统优先适配桌面和平板，不要求在手机上完成复杂运维操作。
- “完整系统”指产品、管理、安全、审计和交付闭环完整，不代表第一期就宣称所有链上归因算法均已完成。

## 3. 双系统产品边界

### 3.1 用户层系统

核心用户：调查员、分析员、案件负责人、只读审阅人。

核心目标：从线索进入，完成取数、分析、证据固化、协作复核、报告导出和案件归档。

建议导航：

```text
工作台
├─ 我的概览
├─ 全局搜索
案件与调查
├─ 案件中心
├─ 调查任务
├─ 资金图谱
└─ Findings / Evidence
数据工具
├─ 地址与交易查询
├─ 数据下载
├─ 任务中心
└─ 导出中心
协作
├─ 待我处理
├─ 通知
└─ 报告中心
个人
├─ 个人资料
├─ 安全设置
└─ 使用偏好
```

### 3.2 后台管理系统

核心用户：平台 Owner、系统管理员、安全管理员、审计员、运维人员。

核心目标：控制谁能做什么，平台如何运行，数据是否可信，成本和风险是否受控，以及发生过什么。

建议导航：

```text
运营总览
├─ 系统健康
├─ 实时告警
└─ 运营指标
组织与权限
├─ 组织 / 工作区
├─ 用户与成员
├─ 角色与权限
└─ 登录与会话
任务与数据
├─ 全局任务控制
├─ Coverage 与数据质量
├─ 导出与对象存储
└─ 数据保留策略
Provider 与 API
├─ Provider 账户
├─ 路由 / 限流 / 性能模式
├─ API / MCP 密钥
├─ 请求审计
└─ 配额与成本
安全与审计
├─ 审计日志
├─ 风险事件
├─ Kill Switch
└─ 审计导出
系统
├─ 功能开关
├─ 通知渠道
├─ 版本与迁移
├─ 备份恢复
└─ 系统设置
```

### 3.3 明确的边界规则

- 用户可以管理自己有权限的案件和任务，但不能修改全局并发、Provider 密钥、预算或系统开关。
- 后台管理员可控制平台运行，但默认不能读取案件证据正文；需要 `case:evidence:read` 显式权限并产生敏感读取审计。
- 审计员只读访问审计、配置历史和导出校验信息，不能执行 Kill Switch 或更改权限。
- 所有跨系统跳转必须重新检查服务端权限，不能依赖前端隐藏菜单。
- 同一领域对象只有一个事实来源；两套系统不能各自保存一份任务、用户或审计状态。

## 4. 角色和权限模型

### 4.1 建议角色

| 角色 | 用户层能力 | 后台能力 |
|---|---|---|
| Owner | 全组织案件与数据 | 全局配置、成员、角色、账单、紧急控制 |
| Platform Admin | 按授权查看案件元数据 | 系统、Provider、任务、告警、配置管理 |
| Security Admin | 无默认案件正文权限 | 认证策略、会话、密钥、风险事件 |
| Auditor | 只读或无业务权限 | 审计日志、配置历史、证据包校验，只读 |
| Investigator | 创建案件、调查、图谱、Finding、Evidence、报告 | 无后台权限 |
| Analyst | 查询、分析、导出、协作 | 无后台权限 |
| Reviewer | 查看证据、审批结论、签署报告 | 无后台权限 |
| Viewer | 只读已授权案件和报告 | 无后台权限 |
| API Client | 仅声明的 scope | 不能登录后台 |

### 4.2 授权实现

采用“RBAC + 工作区作用域 + 少量 ABAC 条件”：

- 权限命名：`resource:action`，例如 `case:create`、`investigation:approve`、`job:cancel`、`admin:provider:update`。
- 作用域：organization、workspace、case、self。
- 条件：对象所属工作区、对象创建者、案件密级、是否为敏感证据读取、是否需要双人复核。
- 管理接口默认拒绝；未声明权限的新增接口在 CI 中视为失败。
- 高风险操作采用二次确认，关键动作支持双人复核：密钥轮换、全局 Kill Switch、数据删除、审计导出、备份恢复。

## 5. 功能建设清单

### 5.1 用户层系统

#### P0：形成可用闭环

1. 登录、退出、会话过期和无权限页。
2. 我的工作台：待处理调查、运行任务、最近案件、失败任务、最近导出。
3. 案件中心：创建、归档、成员、标签、优先级、状态、时间线。
4. 调查工作台：创建、暂停、继续、取消、复核、运行阶段、证据边界。
5. 全局搜索：地址、交易哈希、案件、调查、下载任务和报告。
6. 地址/交易详情：统一资产、活动、对手方、方法、日志和证据引用。
7. 数据下载：Coverage 预览、增量范围、成本/数据量预估、运行模式。
8. 任务中心：地址级和 Dataset 级进度、异常处理、重试、导出。
9. 报告中心：生成、版本、哈希校验、下载、审批状态。
10. 通知中心：任务完成、失败、待复核、权限变化；支持已读状态。

#### P1：提高调查效率

1. 多地址案件工作区和批量操作。
2. Finding / Evidence 专门视图，支持证据引用、筛选、评论、审批和回放。
3. 资金图谱的保存视图、分支、快照、注释、分组和导出。
4. 可保存筛选器、常用模板、最近访问和个人列设置。
5. 报告模板、章节编排、证据索引和审批流。
6. 案件协作：成员、@提及、活动时间线、冲突提示。

#### P2：高级分析能力

1. 真实多跳追踪：金额约束、深度/节点预算、环路与 Peel Chain。
2. 版本化标签、实体聚类与置信度可解释性。
3. 历史价格、成本基础、FIFO/LIFO/比例法与已实现/未实现收益。
4. DEX、Bridge、跨链消息和协议级语义解析。
5. 动态假设、候选结论对比、Finding/Evidence 差异与重放。

### 5.2 后台管理系统

#### P0：安全可控

1. 管理员登录入口和独立授权策略。
2. 组织、工作区、用户、邀请、停用、角色和权限管理。
3. 在线会话、强制下线、登录失败和高风险登录记录。
4. 全局任务列表：筛选、暂停、恢复、取消、优先级、失败诊断。
5. Provider 管理：连接状态、密钥状态、配额、路由、熔断、性能模式。
6. API/MCP Key：创建一次显示、scope、IP/CIDR、限额、过期、轮换和吊销。
7. 预算与成本：组织/工作区/Provider/用途多维预算和阈值告警。
8. 审计中心：用户、动作、资源、Trace ID、结果、IP、时间和变更前后值。
9. Kill Switch 与人工接管：影响预览、原因必填、确认、恢复和审计。
10. 系统健康：API、Worker、PostgreSQL、ClickHouse、MinIO、NATS 和队列积压。

#### P1：治理与运营

1. Coverage 可视化、异常区间、重复数据、缺失区间和重建任务。
2. 数据保留、归档、合法删除、对象生命周期和空间占用。
3. 告警规则、抑制、认领、处置、恢复和通知渠道。
4. 配置中心和功能开关，支持版本、审批、灰度和回滚。
5. 备份、恢复演练、迁移状态和版本兼容检查。
6. 运营报表：成功率、端到端吞吐、Provider 错误率、成本、存储增长。
7. 审计证据包：CSV/JSONL、清单、SHA-256、时间范围和导出操作者。

## 6. 技术架构方案

### 6.1 前端目录目标

```text
frontend/
├─ apps/
│  ├─ user-console/          # 用户层系统
│  └─ admin-console/         # 后台管理系统
├─ packages/
│  ├─ design-tokens/         # 色彩、排版、间距、状态语义
│  ├─ ui/                    # Dialog、Table、Drawer、Form、EmptyState
│  ├─ api-client/            # OpenAPI 生成的类型化 SDK
│  ├─ auth/                  # 会话、权限、路由守卫
│  ├─ domain/                # 共享领域类型和格式化规则
│  └─ test-utils/            # MSW、fixtures、权限测试工具
└─ tests/
   ├─ user-e2e/
   └─ admin-e2e/
```

迁移期间保留现有 Vite/React 19 技术栈。先建立 workspace 和共享包，再逐页搬迁；禁止一次性推倒重写。

### 6.2 后端边界

继续使用 modular monolith，但形成明确模块：

```text
IAM / Policy
Organization / Workspace
Case / Investigation / Evidence / Report
Dataset / Download / Export
Provider / API Control / Budget
Audit / Notification / Configuration
```

API 建议分为：

- `/api/v1/*`：用户层业务 API。
- `/api/admin/v1/*`：后台管理 API。
- `/api/internal/v1/*`：Worker 和内部服务，仅内网或 mTLS。
- `/api/public/v1/*`：确需公开的下载票据等最小接口。

所有 API 统一返回 Trace ID；写请求支持 Idempotency-Key，带版本对象使用 `If-Match`；错误码与 HTTP 状态分离并保持稳定。

### 6.3 身份认证

推荐优先接入 OIDC；若部署环境暂时没有 IdP，则实现安全的本地账户模式，但接口保持可切换：

- 短时 Access Token + 可撤销 Refresh Session，或安全 HttpOnly/SameSite Cookie 会话。
- 密码仅使用 Argon2id 哈希；提供首次登录改密和管理员重置流程。
- 管理员支持 MFA/TOTP，P0 至少保留策略字段和接口。
- 登录、刷新、退出、失效、强制下线均进入安全审计。
- 反向代理必须删除外部传入的 `X-User-*` 头；应用不再信任客户端自报身份。

### 6.4 数据模型增量

新增或调整以下控制面实体，全部使用增量迁移：

- `identities`、`credentials`、`sessions`、`login_events`、`trusted_devices`
- `organization_memberships`、`workspace_memberships`
- `roles`、`permissions`、`role_permissions`、`member_role_bindings`
- `access_policies`、`approval_requests`
- `notifications`、`notification_preferences`
- `feature_flags`、`system_settings`、`configuration_revisions`
- `audit_events_v2` 或扩展现表：actor、tenant、workspace、session、device、request/trace、IP、UA、result、before/after、hash chain
- `user_activity_events`：页面进入、搜索、筛选、查看、下载、复制、跳转等高频用户行为；与法定/安全审计分表存储
- `retention_policies`、`deletion_requests`、`backup_runs`、`restore_runs`

要求：

- 现有 `users.role` 只作为迁移来源，完成绑定迁移后不再作为授权事实来源。
- 现有 Investigation、Canvas、Case、Export 和 Job 补齐 organization/workspace 强约束。
- ClickHouse 查询必须由服务端注入授权后的数据作用域，前端不能提交任意 tenant ID。
- MinIO object key 按 organization/workspace/resource 分区，下载必须通过短期票据。

### 6.5 审计架构

审计记录必须覆盖：

- 认证：登录、失败、退出、MFA、会话撤销。
- 授权：拒绝事件、角色变化、跨工作区访问。
- 业务：案件、调查、Finding/Evidence、审批、报告和导出。
- 管理：Provider、预算、限流、性能模式、Kill Switch、配置和密钥。
- 数据：下载、重建、归档、删除、恢复。

关键管理事件通过事务 Outbox 发布；审计展示从 PostgreSQL 读取，长期证据包写入 MinIO。高风险事件保存 before/after，敏感字段只保存掩码或摘要，不保存明文密钥。

### 6.6 用户登录与全量操作记录规范

#### 6.6.1 记录范围

用户层系统采用“安全日志 + 业务审计 + 行为遥测”三层记录，确保所有有意义的用户操作均可追溯：

| 类别 | 必须记录的事件 |
|---|---|
| 登录安全 | 登录成功/失败、账号不存在或停用、密码错误、MFA 成功/失败、刷新 Token、退出、超时、强制下线、密码修改、恢复码使用、首次/异常设备登录 |
| 页面访问 | 进入模块和详情页、无权限访问、资源不存在、跨工作区切换；只记录路由和对象 ID，不记录页面敏感正文 |
| 查询操作 | 全局搜索、地址/交易查询、筛选、排序、分页、保存筛选器；保存参数摘要、结果数量、耗时和数据来源，不复制整批结果到日志 |
| 案件操作 | 创建、查看、修改、归档、恢复、成员变化、标签、优先级、状态和评论 |
| 调查操作 | 创建、启动、暂停、继续、取消、重试、重建分析、确认或驳回结论、预算和模式变化 |
| 证据操作 | 查看敏感证据、添加、引用、下载、复制、审批、移除、快照和哈希校验 |
| 图谱操作 | 创建 Canvas、保存、分支、快照、节点/边/分组/注释变化、导入和导出 |
| 数据操作 | Coverage 预览、创建下载、控制任务、更新数据、创建导出、下载文件和下载票据失败 |
| 报告操作 | 生成、查看、下载、审批、驳回、签署、归档和版本变化 |
| 账户操作 | 修改资料、偏好、通知设置、安全设置、设备信任和撤销会话 |
| 授权结果 | 每次 401/403、越权尝试、敏感资源访问和策略决策；记录命中的策略和拒绝原因代码 |

“所有操作”指所有业务动作、敏感读取、身份安全动作和关键页面访问。鼠标移动、每次键盘输入等无业务意义且涉及隐私的原始行为不进入审计；如需产品分析，使用独立、可关闭、短保留期的匿名遥测。

#### 6.6.2 每条登录记录字段

每次登录尝试，无论成功或失败，至少记录：

- `event_id`、服务端时间、事件版本、环境。
- 用户 ID；失败且无法确认用户时，仅保存规范化账号的掩码和不可逆摘要。
- organization/workspace（可确定时）、认证方式、Identity Provider、MFA 方法。
- 结果、失败原因代码、风险等级、风控动作；对客户端返回统一错误，避免账号枚举。
- 源 IP、可信代理链、连接协议、入口域名；IP 必须经过规范化并支持 IPv4/IPv6。
- 原始 User-Agent，以及解析后的设备类型、设备厂商/型号（可可靠识别时）、操作系统及版本、浏览器及版本。
- Client Hints：`Sec-CH-UA`、`Sec-CH-UA-Platform`、`Sec-CH-UA-Mobile` 等浏览器实际提供的字段。
- 随机会话设备 ID、设备首次/最近出现时间、是否新设备、是否受信任；不采用侵入式跨站指纹。
- session ID、request ID、Trace ID；Token、Cookie 和验证码本身绝不入库。
- 可选 IP 情报：国家/地区/城市、ASN、网络类型和代理/VPN/Tor 风险标记，并记录情报来源和库版本，不能把推测位置当作精确定位。
- 服务端处理耗时、限流结果和触发的告警 ID。

#### 6.6.3 每条业务操作记录字段

- 谁：user ID、角色快照、organization/workspace、session/device ID。
- 何时何地：服务端时间、源 IP、可信代理链、User-Agent、客户端版本。
- 做了什么：action、模块、HTTP method、route template、对象类型与对象 ID。
- 操作上下文：case/investigation/job/report ID、request/trace ID、Idempotency-Key 摘要。
- 结果：允许/拒绝、HTTP 状态、业务错误码、耗时、结果数量和影响行数。
- 变化：允许审计的 before/after 字段或 JSON Patch；大对象只保存内容哈希、版本和对象引用。
- 数据来源：本地 Coverage、Provider、缓存或重建版本，便于解释用户当时看到的结果。
- 风险：敏感读取、高风险操作、审批请求、二次确认和关联告警。

#### 6.6.4 可信 IP 和设备信息边界

- 应用仅在请求来自已配置的 Nginx/Cloudflare 等可信代理时读取 `Forwarded`、`X-Forwarded-For` 或 `CF-Connecting-IP`。
- 不可信来源携带的转发头必须丢弃，最终同时保存 `peer_ip` 与经过可信链解析的 `client_ip`。
- 代理信任列表、解析规则和变更必须版本化并进入后台审计。
- User-Agent 和 Client Hints 可能缺失或被伪造，因此界面标注为“客户端声明”；设备 ID 也不是人员身份结论。
- 不采集硬件序列号、通讯录、文件列表等与业务无关的信息；精确定位必须另行取得明确授权。

#### 6.6.5 防篡改、查询和留存

- 安全/业务审计为 append-only；应用运行账号无 UPDATE/DELETE 权限。
- 核心事件进入 PostgreSQL 按月分区，通过事务 Outbox 投递；高频访问事件可镜像到 ClickHouse 供检索分析。
- 每日生成按顺序链接的事件哈希和 manifest，写入启用版本控制/对象锁的 MinIO 审计桶。
- 后台支持按用户、IP、设备、会话、动作、资源、结果、时间和 Trace ID 检索，并能串联完整会话时间线。
- 审计导出包含 JSONL/CSV、manifest、SHA-256、导出条件、导出人和导出时间。
- 建议登录安全日志和核心业务审计在线保留不少于 365 天、归档不少于 3 年；普通行为遥测默认 90 天。最终期限由实际合规和存储预算审批确定。
- 密码、验证码、Access/Refresh Token、Cookie、私钥、API Key、完整 Authorization 头和表单秘密字段禁止记录。
- 地址、交易哈希等案件数据只在业务必要范围内记录；日志查看、导出和删除同样受权限与审计约束。

## 7. 视觉与交互方向

### 7.1 设计主题

主题定位：**证据控制台（Evidence Console）**。用户看到的不是泛化数据仪表盘，而是“对象—路径—证据—结论”的调查工作台；后台看到的是“主体—动作—影响—追踪”的控制台。

签名元素：在两套系统中统一使用可折叠的 **Evidence Rail**。它展示当前对象的来源、时间、置信度、哈希和审计事件，让证据链成为产品辨识度，而不是装饰性渐变或大数字卡片。

### 7.2 设计令牌

| 名称 | 色值 | 用途 |
|---|---|---|
| Graphite | `#101828` | 主文字、关键结构 |
| Cobalt | `#175CD3` | 主操作、选中态、链路 |
| Ice | `#F3F7FC` | 工作区背景 |
| Slate | `#667085` | 次级文字 |
| Evidence Amber | `#D97706` | 待复核、证据边界、风险 |
| Verified Green | `#067647` | 已验证、健康、完成 |

排版：中文正文使用 `Noto Sans SC`，拉丁与标题使用 `IBM Plex Sans`，地址、哈希、金额、Trace ID 使用 `IBM Plex Mono`。所有数字使用 tabular numerals。

### 7.3 布局规则

- 用户层：案件上下文优先，左侧主导航 + 中间工作区 + 按需 Evidence Rail。
- 后台：控制对象优先，左侧模块导航 + 顶部环境/组织切换 + 表格或时间线主区 + 详情抽屉。
- 页面正文只展示业务数据、字段、导航和操作控件；警告、说明、成功、错误、风险和确认继续严格使用 Dialog。
- 手机端不简单压缩桌面三栏：列表先行，详情进入新层级；资金图仅提供查看和轻量筛选。
- 统一键盘焦点、快捷搜索、空状态、加载骨架、错误恢复和 `prefers-reduced-motion`。

## 8. 分阶段实施计划

以下排期按 2 名前端、2 名后端、1 名 QA、0.5 名设计/产品、0.5 名 DevOps 估算，总计约 14 周。人员减少时应保持阶段顺序，不压缩安全和验收。

### 阶段 0：基线冻结与设计定稿（第 1 周）

交付：

- 盘点现有页面、API、表、迁移、运行服务和未提交改动。
- 冻结业务词汇、两套 IA、角色权限矩阵和 API 边界。
- 输出关键页面线框：登录、用户首页、案件、调查、任务、后台首页、用户权限、审计、Provider 控制。
- 建立 ADR：双前端、认证方式、授权模型、审计模型、URL/部署方式。
- 建立迁移/回滚和数据备份方案。

验收门：现有关键流程基线测试通过；所有现有能力明确“保留、迁移、合并或下线”，不得遗漏。

### 阶段 1：共享底座与应用拆分（第 2–3 周）

交付：

- 建立 user-console、admin-console 和共享 packages。
- 设计令牌、基础组件、Dialog、表单、表格、权限守卫和错误边界。
- 完成登录、会话、退出、OIDC/本地账户适配层。
- 完成 RBAC 数据模型、Policy Engine 和统一后端中间件。
- 建立 `/api/admin/v1`，迁移第一批管理 API。
- 完成审计 v2、Trace ID 和敏感字段脱敏。
- 完成登录事件的 IP/可信代理/设备/会话字段，以及用户层全量业务操作审计拦截器。

验收门：伪造 `X-User-*` 无法提权；伪造转发 IP 头不能覆盖真实可信链；登录成功/失败均有 IP、设备、会话和 Trace 记录；跨组织/工作区访问返回 403 且有审计；管理员和普通用户不能进入对方系统未授权路由。

### 阶段 2：用户层 P0（第 4–6 周）

交付：

- 我的工作台、案件中心、调查工作台。
- 地址/交易统一搜索与详情。
- 数据下载、任务中心、任务详情、导出中心。
- 报告列表、生成、版本和下载校验。
- 通知中心、个人资料和安全设置。
- 将现有 Investigation、Fund Flow、Explorer、Download、Jobs 页面迁入新信息架构。

验收门：一名 Investigator 能从登录开始，创建案件、下载缺失数据、完成调查、复核 Finding/Evidence、生成并下载报告；全程有对象 ID、Trace ID 和审计记录。

### 阶段 3：后台管理 P0（第 7–9 周）

交付：

- 运营总览和系统健康。
- 组织、工作区、用户、成员、角色、权限和会话管理。
- 全局任务控制、Provider 管理、API/MCP Key。
- 请求审计、预算成本、告警、Kill Switch。
- 审计中心和审计证据包导出。

验收门：管理员能够定位一次失败任务，从请求、Provider、Shard、Checkpoint、对象文件到审计动作形成完整证据链；审计员只能读取，无法修改控制状态。

### 阶段 4：协作、治理与分析增强（第 10–12 周）

交付：

- Finding/Evidence 工作流、评论、审批、报告模板。
- Canvas 注释、分组、分支、快照和可恢复视图。
- Coverage 数据质量、保留策略、配置版本、功能开关。
- 备份恢复界面和恢复演练记录。
- 多跳追踪第一条受限垂直切片：明确算法、预算、证据和不可断言边界。

验收门：协作冲突不会覆盖他人结果；证据与报告可重放、可校验；高级分析不会把启发式关系表述为已确认身份或真实利润。

### 阶段 5：硬化、迁移与正式发布（第 13–14 周）

交付：

- 安全测试、依赖扫描、权限负向测试、限流和会话攻击测试。
- 性能测试、百万行导出、任务恢复、Kill-9、队列背压和备份恢复演练。
- 桌面/平板/手机真实浏览器视觉与交互验收。
- 灰度发布、监控面板、回滚演练、运维手册和用户手册。
- 历史 URL 重定向、旧单体前端只读观察期和最终下线。

验收门：P0=0、P1=0；P2 必须披露影响。生产发布证据覆盖 UI、API、数据库、对象文件、运行日志和监控，而不是仅记录构建结果。

## 9. 开发任务分解与依赖

```text
现状基线
  └─> 领域词汇 / IA / 权限矩阵
       ├─> IAM + 会话 ─> RBAC/Policy ─> 后台用户权限
       ├─> Design Tokens ─> 共享 UI ─> 两套前端迁移
       ├─> Trace/Audit ─> 审计中心 ─> 审计证据包
       └─> API 分区 ─> 用户业务迁移 ─> 管理控制迁移

共享底座完成
  ├─> 用户 P0 闭环
  └─> 后台 P0 闭环
       └─> 协作与治理
            └─> 安全/性能/灾备验收 ─> 灰度发布
```

不可颠倒的依赖：

- 后台页面开发不能早于统一授权中间件。
- 用户/角色管理不能早于 membership 和 permission 数据模型。
- 审计中心不能只消费旧的低信息量事件，必须先补齐 actor、scope、result 和 trace。
- 数据删除界面不能早于保留策略、审批和可恢复机制。
- 高级利润或归因 UI 不能早于算法、价格来源和证据口径落地。

## 10. 测试与验收策略

### 10.1 自动化层次

- 单元测试：Policy、状态机、金额/精度、权限作用域、审计脱敏。
- 数据库测试：迁移、约束、组织隔离、幂等、并发更新、回滚兼容。
- API 合约测试：OpenAPI schema、权限、错误码、分页、If-Match、Idempotency-Key。
- 集成测试：PostgreSQL、ClickHouse、MinIO、NATS、Provider mock/live 分层验证。
- E2E：用户系统和后台系统分别建套件；每种角色至少一条正向和一条越权路径。
- 视觉测试：1440px、1024px、390px；Dialog 遮罩、焦点、滚动和关闭必须验证。
- 安全测试：登录暴力尝试、Token 重放、会话撤销、CSRF/XSS、越权、SSRF、下载票据泄露。
- 审计完整性测试：按用户端操作清单逐项执行，核对事件数量、字段、会话时间线、脱敏、哈希链和导出包；任何关键事件漏记均为 P0。
- 恢复测试：API/Worker Kill-9、队列重启、Checkpoint 恢复、对象缺失、数据库恢复。

### 10.2 核心端到端场景

1. Investigator 登录 → 创建案件 → 发起调查 → Coverage 命中/补齐 → 查看证据 → 提交复核 → 生成报告。
2. Reviewer 收到通知 → 查看证据边界 → 批准/驳回 → 审计事件可查。
3. Viewer 只能查看已授权案件，不能下载敏感导出或执行控制操作。
4. Platform Admin 处理 Provider 异常 → 调整策略 → 观察影响 → 恢复设置。
5. Auditor 导出指定时间段审计包 → 校验 manifest 与 SHA-256 → 不能修改任何状态。
6. 跨组织用户猜测资源 UUID → 服务端拒绝 → 记录访问拒绝事件，响应不泄露对象存在性。
7. 任务执行中 Worker 重启 → 保留成功游标和地址级进度 → 不重复下载已完成数据。
8. 大规模导出 → 生成、内网传输和公网下载分别计时 → Range 下载与校验通过。

### 10.3 Definition of Done

一个功能只有同时满足以下条件才算完成：

- 真实业务流程可操作，加载、空、错误、无权限和重试状态完整。
- 服务端权限和租户隔离已验证，不能只隐藏按钮。
- 写操作有审计，敏感信息无明文泄露。
- 登录成功/失败记录可信 IP、设备、会话和风控结果；用户层业务操作与敏感读取可按 Trace ID 完整还原。
- API、数据库和文件结果语义一致，金额、余额、净流量等口径明确。
- 桌面和目标移动宽度真实浏览器通过，无遮挡、溢出或不可关闭弹窗。
- 单元、集成、E2E 和必要的性能测试通过。
- 文档、迁移、监控、回滚和运维说明同步更新。

## 11. 发布与迁移策略

1. 新旧前端并行：先以 `/app-v2`、`/admin` 灰度，新系统读取现有业务数据。
2. 身份迁移：把现有默认用户映射为 Owner membership，生成一次性首次登录流程。
3. 权限观察模式：第一周只记录新 Policy 决策与旧行为差异，不自动放宽权限。
4. 页面分批切流：查询 → 案件/调查 → 下载/任务 → 报告；后台随后切流。
5. 写路径切换后保留旧页面只读，不允许双写两套状态。
6. 生产验收通过后，将 `/` 指向用户层系统；后台使用独立域名或 `/admin`，并限制网络入口。
7. 保留可回滚构建和兼容数据库迁移；回滚应用时不能要求回滚已写入的新审计事件。

## 12. 风险与控制

| 风险 | 影响 | 控制措施 |
|---|---|---|
| 在现有大量未提交改动上直接拆分 | 丢失或混淆已有能力 | 阶段 0 建基线、清单和可恢复快照，按页面小步迁移 |
| 前端守卫被当成授权 | 严重越权 | 权限只由后端 Policy 决定，E2E 强制负向测试 |
| 单字段角色无法表达工作区权限 | 跨案件/跨组织泄露 | membership + permission + scope，所有查询强制作用域 |
| 管理员默认可读全部证据 | 违反最小权限 | 管理控制与案件正文权限分离，敏感读取审计 |
| 为追求“完整”一次性重写 | 周期失控、回归严重 | 保留模块化单体，按可验收垂直切片迭代 |
| 将启发式分析表述为事实 | 调查结论失真 | 强制 confidence、provenance、evidence boundary 和人工复核 |
| 大表审计/任务查询拖慢主库 | 后台不可用 | 时间分区、索引、异步导出、查询预算和归档 |
| 配置和 Kill Switch 误操作 | 全局服务中断 | 影响预览、理由、双人复核、版本化回滚和恢复演练 |

## 13. 第一批可直接进入迭代的 Backlog

### Sprint 1

- ADR-001：双前端和部署 URL。
- ADR-002：OIDC/本地账户认证策略。
- ADR-003：RBAC + workspace scope 模型。
- 完成现有路由/API/表/权限/审计清单。
- 建立 design-tokens、ui、api-client 三个共享包。
- 建立 user-console/admin-console 空壳、CI 和独立构建。

### Sprint 2

- 新增 membership、role、permission、session 迁移。
- 实现登录、刷新、退出、强制失效。
- 实现统一 Policy 中间件和权限声明测试。
- 实现审计 v2 与 Trace ID。
- 迁移用户首页和后台系统健康页。

### Sprint 3

- 迁移案件、调查、全局搜索和通知骨架。
- 迁移组织、用户、角色、会话管理。
- 完成跨租户负向测试和管理员敏感读取测试。
- 建立两套 Playwright 冒烟与视觉基线。

## 14. 项目完成判定

达到以下状态，才可称为“双系统完整化第一版”：

- 用户层系统和后台管理系统独立构建、独立部署、独立授权。
- 不再信任客户端传入的身份头；会话可撤销，权限由服务端统一执行。
- 用户可完成案件调查闭环，管理员可完成平台控制闭环，审计员可完成追溯闭环。
- 所有高风险管理动作有原因、确认、权限、结果和审计证据。
- 组织/工作区隔离通过正向和负向验证。
- 任务、查询、导出、报告和对象文件结果可相互核对。
- 真实桌面/移动交互、API、数据库、文件和运行状态均有验收证据。
- 未完成的多跳、归因、价格/利润和跨链能力明确标为后续能力，不以 UI 存在冒充算法完成。

---

# 第二部分：可直接实施的工程规格

## 15. 不可变架构决策

以下决策已经明确，实施 Agent 不再自行选择替代方案：

| 编号 | 决策 | 原因 |
|---|---|---|
| AD-001 | 保留 Go modular monolith，按领域包重组，不立即拆微服务 | 当前数据下载、调查和证据链已形成稳定事务边界，先消除产品与安全缺口 |
| AD-002 | 两个 React 19 + Vite 应用，pnpm workspace 管理共享包 | 独立发布和权限隔离，同时复用设计系统和 SDK |
| AD-003 | 浏览器不保存 Bearer Token；使用服务端会话与 `Secure`、`HttpOnly`、`SameSite=Lax`、host-only Cookie | 降低 XSS 窃取 Token 风险 |
| AD-004 | OIDC Authorization Code + PKCE 为首选；本地账户实现相同 Session 接口 | 兼容内网部署和未来企业 IdP |
| AD-005 | 用户系统和后台系统各自拥有 host-only Session，后台 Session 的 audience 与权限单独校验 | 避免用户会话自动获得后台访问面 |
| AD-006 | RBAC + organization/workspace/case scope；服务端默认拒绝 | 前端菜单仅改善体验，不构成安全边界 |
| AD-007 | PostgreSQL 保存控制面真相，ClickHouse 保存链上数据和高频审计查询镜像，MinIO 保存原始数据/报告/审计归档 | 保持现有存储职责并支持扩展 |
| AD-008 | 写操作使用事务 + Outbox；异步消费者必须幂等 | 保证业务状态与事件一致 |
| AD-009 | 金额和大整数在 API 中使用十进制字符串，时间使用带时区 ISO 8601 | 避免 JavaScript 精度和时区错误 |
| AD-010 | 所有列表使用稳定 cursor 分页；管理导出走异步任务 | 避免 offset 在大表上的漂移和性能问题 |
| AD-011 | 所有说明、警告、错误、成功、风险、确认使用 Dialog；页面正文不放提示横幅或说明卡 | 服从仓库现有强制交互规范 |
| AD-012 | 接口先写 OpenAPI，再生成 TypeScript 类型；禁止页面手写重复 DTO | 保持两套前端和后端契约一致 |
| AD-013 | 现有业务 API 保持兼容，新增用户接口使用 `/api/v2`，新增管理接口使用 `/api/admin/v1` | 支持灰度迁移和旧客户端共存 |
| AD-014 | 所有敏感读取和业务写入由后端记录审计，前端埋点只作补充 | 防止绕过页面直接调用 API 时漏记 |
| AD-015 | 功能扩展通过 Capability、Projection、Provider Adapter、Chain Adapter 和版本化 schema 完成 | 后续加链、算法、Provider 时不修改核心语义 |

禁止事项：

- 禁止一次性删除或重命名现有 `/api/v1` 路由。
- 禁止在 React 组件中硬编码 organization/workspace ID。
- 禁止让客户端提交并决定 actor、role、organization 或审计结果。
- 禁止把 API Key、OIDC Client Secret、密码、Token 写入日志、前端构建或 Git。
- 禁止为高级分析页面制造模拟“已确认”结论。
- 禁止用数据库超级用户作为 API/Worker 日常运行账号。

## 16. 目标代码组织与模块职责

### 16.1 前端目录

```text
frontend/
├─ apps/
│  ├─ user-console/
│  │  ├─ src/app/                 # router、providers、shell、error boundary
│  │  ├─ src/features/            # cases、investigations、explorer、downloads...
│  │  ├─ src/pages/               # 只做路由组合，不直接写 HTTP
│  │  ├─ src/test/
│  │  └─ vite.config.ts
│  └─ admin-console/
│     ├─ src/app/
│     ├─ src/features/            # iam、audit、providers、jobs、governance...
│     ├─ src/pages/
│     ├─ src/test/
│     └─ vite.config.ts
├─ packages/
│  ├─ api-client/                 # OpenAPI 生成代码 + fetch transport
│  ├─ auth/                       # session query、permission hooks、route guard
│  ├─ design-tokens/              # CSS variables、theme、breakpoints
│  ├─ domain/                     # chain/address/amount/status 领域类型
│  ├─ observability/              # trace、前端错误、activity telemetry
│  ├─ ui/                         # 无业务状态的基础组件
│  └─ test-utils/                 # factories、MSW、权限 fixture
├─ e2e/
│  ├─ user/
│  ├─ admin/
│  ├─ cross-role/
│  └─ visual/
├─ package.json
└─ pnpm-workspace.yaml
```

依赖方向必须为：`apps -> packages`。共享包不得反向依赖任何 app；`ui` 不依赖 `api-client`；`domain` 不依赖 React。

页面组件不得直接调用 `fetch`。统一调用 feature repository，再由生成的 `api-client` 完成请求、CSRF、Trace、超时和错误映射。

### 16.2 后端目录

```text
backend/internal/
├─ iam/                 # identity、credential、session、device、login risk
├─ policy/              # permissions、bindings、scope evaluation
├─ tenancy/             # organization/workspace context 与查询约束
├─ audit/               # append-only event、hash chain、export
├─ notification/        # inbox、preference、delivery adapter
├─ configcenter/        # settings、feature flag、revision、approval
├─ governance/          # retention、deletion、backup/restore metadata
├─ casework/            # case、member、activity、comment
├─ investigation/       # EXISTING，运行状态和 artifacts
├─ canvas/              # EXISTING，图谱状态
├─ report/              # EXISTING，扩展审批/模板/签名
├─ dataset/             # EXISTING，Coverage 与 ensure
├─ download/            # EXISTING，任务调度/恢复
├─ explorer/            # EXISTING，数据查询
├─ provider/            # EXISTING，Provider adapters/control
├─ platform/contract/   # EXISTING，不破坏已有 1.0 合约
└─ api/
   ├─ userv2/           # 用户层 handlers
   ├─ adminv1/          # 后台 handlers
   ├─ middleware/       # auth、policy、tenant、csrf、audit、trace
   └─ legacyv1/         # 现有 handler 迁入，不改变路径
```

Handler 只负责解析、校验、调用 service 和序列化。授权检查在统一中间件与领域 service 双层执行；SQL 不得散落在 handler。

### 16.3 运行进程和网络边界

| 进程 | 暴露面 | 身份 | 允许访问 |
|---|---|---|---|
| user-console | 公网/内网 Web | 用户 Session | 用户 API |
| admin-console | 优先内网/VPN | 后台 Session + 可选 MFA | 管理 API |
| Go API | Nginx 后 | Session、API Key、内部身份 | PostgreSQL、ClickHouse、MinIO、NATS |
| Go Worker | 内网，无公网入口 | 独立服务账号 | PostgreSQL、ClickHouse、MinIO、NATS、Provider |
| MCP | 明确公网入口 | 独立 MCP Key | 允许的 REST API scope |
| ChatGPT Actions | 明确公网入口 | 独立 Actions Key | OpenAPI 白名单路由 |

用户、后台、Actions、MCP 四种认证面互不复用密钥。Nginx 只转发已批准路由，删除外部 `X-User-*`，并为不同入口设置独立限流区。

## 17. 完整设计系统与交互规范

### 17.1 产品视觉主张

产品不是通用 BI，也不是交易行情终端。视觉语言来自取证工作：每个对象都有来源、时间、版本、哈希和可复核路径。界面以高密度、低装饰、强层级为主，唯一显著视觉签名是 **Evidence Rail**。

设计自检后的取舍：

- 保留数据密集型工作台，不采用营销式大 Hero、玻璃拟态或大面积渐变。
- 不采用“代码字体做所有标题”的通用技术面板风格；代码字体只用于地址、哈希、金额和 Trace。
- 动效只表达层级变化、任务状态和抽屉进出，不使用滚动装饰动画。
- Amber 只表示待复核/证据边界/风险，不作为普通 CTA，避免状态语义混乱。

### 17.2 Design Tokens

颜色：

```css
:root {
  --color-bg-canvas: #F3F7FC;
  --color-bg-surface: #FFFFFF;
  --color-bg-subtle: #F8FAFC;
  --color-text-strong: #101828;
  --color-text: #344054;
  --color-text-muted: #667085;
  --color-border: #DCE3EC;
  --color-border-strong: #C9D3E0;
  --color-primary: #175CD3;
  --color-primary-hover: #1849A9;
  --color-primary-soft: #EAF2FF;
  --color-success: #067647;
  --color-warning: #B54708;
  --color-danger: #B42318;
  --color-evidence: #D97706;
  --focus-ring: 0 0 0 3px rgba(23, 92, 211, .28);
}
```

排版：

- UI/正文：`Noto Sans SC`, `Microsoft YaHei UI`, sans-serif。
- 英文标题辅助：`IBM Plex Sans`, `Noto Sans SC`, sans-serif。
- 数据：`IBM Plex Mono`, `Cascadia Mono`, monospace。
- 正文最小 14px/1.5；表格辅助信息最小 12px/1.45；禁止低于 12px 的可读正文。
- 页面标题 24px/32px/700；区块标题 16px/24px/650；字段标签 13px/20px/600。

尺寸：

```text
spacing: 4, 8, 12, 16, 20, 24, 32, 40, 48
radius: 4(control-small), 8(control/card), 12(dialog/major panel)
control height: 32 compact, 40 default, 44 touch
sidebar: 232 expanded / 64 collapsed
topbar: 56 desktop / 52 mobile
content max: none for workbench; 1440px for settings forms
```

阴影仅用于浮层：Drawer `0 16px 40px rgba(16,24,40,.16)`；Dialog `0 24px 64px rgba(16,24,40,.22)`。普通卡片只用边框，不堆叠阴影。

### 17.3 共享组件契约

| 组件 | 必须能力 | 禁止 |
|---|---|---|
| AppShell | skip link、可折叠导航、workspace switch、用户菜单、权限裁剪 | 在渲染时写死 admin |
| PageHeader | 标题、对象上下文、主操作、breadcrumb | 放置常驻说明段落 |
| DataTable | cursor 分页、排序、列配置、选择、批量操作、键盘、虚拟化 | 渲染 100+ DOM 行而不虚拟化 |
| FilterBar | URL 同步、保存视图、清除、可访问标签 | 只靠 placeholder 作为标签 |
| StatusBadge | 图标/文字/颜色三重表达 | 仅颜色表达状态 |
| EntityLink | 地址/哈希完整复制、缩略显示、链标识 | 修改原始值或丢失校验格式 |
| Amount | 原始字符串、decimals、symbol、精度切换 | 使用 JS Number 处理链上大整数 |
| EvidenceRail | 来源、置信度、哈希、时间、引用、审计跳转 | 将 heuristic 显示为 confirmed |
| DetailDrawer | 深链接、ESC、焦点返回、移动端全屏 | 无法通过键盘关闭 |
| NoticeDialog | 说明/成功/错误，明确标题和关闭 | toast 代替重要结果 |
| ConfirmDialog | 动作、影响、确认、取消；危险操作原因必填 | 浏览器原生 confirm |
| RiskApprovalDialog | step-up auth、影响预览、审批人、原因 | 一次点击执行高风险动作 |
| EmptyState | 当前为空、可执行下一步 | 只显示“暂无数据” |
| LoadingState | skeleton 保留布局，超过 8s 可取消/重试 | 全屏无限 spinner |
| ErrorBoundary | Trace ID、重试、返回安全页，通过 Dialog 呈现错误 | 展示堆栈或敏感响应 |
| JsonDiff | before/after、脱敏、字段级标识 | 直接渲染未转义 HTML |

所有 Dialog：打开后焦点进入标题或首个控件；Tab 被限制在弹窗；ESC 对非强制操作可关闭；关闭后焦点回到触发控件；正文超长时内部滚动；`aria-labelledby`、`aria-describedby` 完整。

### 17.4 页面状态标准

每个页面必须实现以下状态，测试命名统一：

- `loading.initial`：首屏骨架。
- `loading.refresh`：保留旧数据并显示局部进度。
- `empty.first_use`：没有任何数据，提供创建动作。
- `empty.filtered`：筛选无结果，提供清除筛选。
- `error.recoverable`：Dialog 展示错误码/Trace，允许重试。
- `error.forbidden`：403 页面，不泄露资源是否存在。
- `error.not_found`：404 页面，提供返回上级。
- `error.conflict`：409/412 Dialog，展示当前版本并允许刷新。
- `offline`：Dialog 说明连接中断，恢复后自动刷新只读请求。
- `stale`：数据保留但显示业务状态标签“数据更新时间”；解释通过 Dialog。

### 17.5 响应式与可访问性

断点：`<640` 手机、`640–1023` 平板、`1024–1439` 桌面、`>=1440` 宽屏。

- 触控目标不小于 44×44px，目标间距不少于 8px。
- 桌面表格在手机切换为关键信息卡；确需完整列时使用明确的横向滚动容器。
- 资金图谱手机端仅查看、搜索、选择节点、切换 Lens；编辑、批量布局和复杂对比提示转到桌面。
- 所有交互可用键盘完成，焦点可见且不被固定顶栏/抽屉遮挡。
- 图表有文本摘要、图例和数据表替代；不能只靠红绿区分。
- 登录允许密码管理器和粘贴；不使用阻碍认知的验证码作为唯一认证方式。
- 动画 120–220ms；退出快于进入；`prefers-reduced-motion` 下关闭非必要位移和闪烁。
- 页面加载目标：LCP <2.5s、CLS <0.1、INP <200ms（内网标准环境，P75）。

## 18. 用户层系统逐页规格

### 18.1 路由总表

| ID | 路由 | 页面 | 权限 | 主要 API | 核心内容与操作 |
|---|---|---|---|---|---|
| U-AUTH-01 | `/login` | 登录 | public | `POST /api/auth/v1/login` | 账号、密码、OIDC 登录、登录设备摘要；错误用 Dialog |
| U-AUTH-02 | `/auth/mfa` | MFA 验证 | auth:challenge | `POST /api/auth/v1/mfa/verify` | TOTP/恢复码、安全设备确认 |
| U-AUTH-03 | `/auth/callback` | OIDC 回调 | public | `GET /api/auth/v1/oidc/callback` | 只显示安全加载态，失败进入 Dialog |
| U-HOME-01 | `/home` | 我的工作台 | dashboard:read | dashboard 聚合 API | 待处理、运行任务、最近案件、失败任务、导出、通知 |
| U-SEARCH-01 | `/search` | 全局搜索 | search:read | `GET /api/v2/search` | 地址、交易、案件、调查、任务、报告分组结果 |
| U-CASE-01 | `/cases` | 案件中心 | case:list | `GET /api/v2/cases` | 筛选、排序、保存视图、创建、批量归档 |
| U-CASE-02 | `/cases/new` | 新建案件 | case:create | `POST /api/v2/cases` | 基本信息、工作区、密级、成员、标签 |
| U-CASE-03 | `/cases/:caseId/overview` | 案件概览 | case:read | case summary/activity | 状态、对象、调查、Finding、报告、时间线 |
| U-CASE-04 | `/cases/:caseId/subjects` | 调查对象 | subject:list | case subjects API | 地址/域名/IP/APK/交易等对象及关系 |
| U-CASE-05 | `/cases/:caseId/investigations` | 案件调查 | investigation:list | investigations API | 创建调查、状态、负责人、预算、进度 |
| U-CASE-06 | `/cases/:caseId/evidence` | 证据中心 | evidence:read | evidence API | Finding/Evidence 配对、来源、哈希、复核状态 |
| U-CASE-07 | `/cases/:caseId/reports` | 案件报告 | report:list | reports API | 生成、版本、审批、下载、签署状态 |
| U-CASE-08 | `/cases/:caseId/activity` | 案件动态 | case:activity:read | activity API | 用户可理解的业务时间线，不替代审计中心 |
| U-INV-01 | `/investigations` | 调查任务 | investigation:list | `GET /api/v2/investigations` | 全部/我的/待复核、筛选、批量指派 |
| U-INV-02 | `/investigations/new` | 发起调查 | investigation:create | preview/create | 对象、目的、期望结果、模式、预算、Coverage 预览 |
| U-INV-03 | `/investigations/:id/workbench` | 调查工作台 | investigation:read | detail/runtime/artifacts | 阶段、步骤、Finding、Evidence、Evidence Rail、控制 |
| U-INV-04 | `/investigations/:id/findings` | Findings | finding:list | findings API | 候选/确认/驳回、置信度、依据、审批 |
| U-INV-05 | `/investigations/:id/evidence/:evidenceId` | 证据详情 | evidence:read | evidence detail | 内容、来源、哈希、引用对象、链上原文、操作记录 |
| U-GRAPH-01 | `/canvases/:canvasId` | 资金图谱 | canvas:read | canvas/graph APIs | 图谱、Lens、搜索、Inspector、Evidence Rail、快照 |
| U-EXP-01 | `/explorer` | 地址与交易查询 | explorer:read | search/query APIs | 统一输入、最近查询、批量地址 |
| U-EXP-02 | `/addresses/:chain/:address` | 地址详情 | explorer:read | overview/assets/activity | 概览、资产、交易、对手方、语义、标签、数据覆盖 |
| U-EXP-03 | `/transactions/:chain/:hash` | 交易详情 | explorer:read | tx/trace/log/decode | 交易、回执、内部调用、日志、方法、证据引用 |
| U-DL-01 | `/downloads/new` | 新建数据任务 | download:create | preview/ensure | 地址、链、范围、Dataset、复用策略、预估 |
| U-JOB-01 | `/jobs` | 我的任务 | job:list:self | jobs API | 状态、地址进度、性能模式、失败摘要、批量操作 |
| U-JOB-02 | `/jobs/:jobId` | 任务详情 | job:read | job/events/guard | Pipeline、地址/Dataset/Shard、日志、控制、导出 |
| U-EXPORT-01 | `/exports` | 导出中心 | export:list | exports API | 状态、格式、行数、哈希、过期、重新生成 |
| U-REPORT-01 | `/reports` | 报告中心 | report:list | reports API | 跨案件报告、审批、版本、校验和下载 |
| U-INBOX-01 | `/inbox` | 待我处理 | inbox:read | tasks/reviews API | 待复核、待审批、失败任务、@提及 |
| U-NOTIFY-01 | `/notifications` | 通知 | notification:read | notifications API | 未读、类型、对象跳转、全部标记已读 |
| U-PROFILE-01 | `/profile` | 个人资料 | profile:update:self | me/profile API | 姓名、头像、语言、时区 |
| U-SEC-01 | `/security` | 安全设置 | session:manage:self | sessions/devices API | 密码、MFA、设备、会话、全部下线 |

### 18.2 登录页面 U-AUTH-01

页面唯一任务：安全进入系统并让用户知道当前登录入口和设备上下文。

```text
┌──────────────────────────────────────────────────────────────┐
│ ChainSight / Evidence-led investigation                     │
│                                                              │
│              ┌────────────────────────────┐                  │
│              │ 登录链上调查平台            │                  │
│              │ 工作邮箱 [______________]  │                  │
│              │ 密码     [______________]  │                  │
│              │ [ ] 保持本设备登录 30 天    │                  │
│              │ [        登录        ]      │                  │
│              │ ─────── 或 ────────         │                  │
│              │ [ 使用组织账号登录 ]         │                  │
│              │ 当前入口：用户层系统          │                  │
│              └────────────────────────────┘                  │
└──────────────────────────────────────────────────────────────┘
```

规则：

- 邮箱和密码有可见 label、正确 autocomplete；允许粘贴和密码管理器。
- 连续提交期间禁用按钮并显示“正在验证”；不得重复创建登录事件。
- 失败统一显示“账号或凭证无效”，Dialog 内附 request ID；详细原因只进安全日志。
- 检测新设备或高风险 IP 时进入 MFA/step-up 流程，不在登录响应中泄露用户是否存在。
- 成功后只允许跳转到本站验证过的相对 `return_to`，拒绝开放重定向。

### 18.3 我的工作台 U-HOME-01

```text
┌ 顶栏：工作区 | 全局搜索 | 通知 | 用户 ─────────────────────┐
├ 左导航 ─────┬───────────────────────────────────────────────┤
│             │ 我的工作台                  [发起调查]        │
│             │ ┌ 待我处理 ────────────────────────────────┐ │
│             │ │ 复核 4 | 审批 1 | 失败任务 2              │ │
│             │ └──────────────────────────────────────────┘ │
│             │ ┌ 运行中的调查 ─────┐ ┌ 最近案件 ─────────┐ │
│             │ │ 状态/阶段/负责人   │ │ 状态/成员/更新时间 │ │
│             │ └────────────────────┘ └───────────────────┘ │
│             │ ┌ 最近导出/报告 ───────────────────────────┐ │
│             │ └──────────────────────────────────────────┘ │
└─────────────┴───────────────────────────────────────────────┘
```

- 所有数字都来自单个聚合 API 的同一 `as_of`，不把不同时间快照拼成实时总览。
- 卡片点击进入对应筛选后的列表，不使用无动作 KPI。
- 没有任务时给出“发起调查”或“查询地址”操作；说明通过 Info 按钮打开 Dialog。

### 18.4 案件概览 U-CASE-03

固定结构：顶部案件编号/名称/状态/密级/负责人；二级 Tab；左侧案件事实与进度；中间调查、Finding、报告；右侧 Evidence Rail。

- 修改状态、归档、成员变化使用 ConfirmDialog 并要求填写原因。
- 案件动态展示业务事件，审计事件仅对具备权限者提供“查看审计详情”跳转。
- `revision` 冲突返回 412，Dialog 比较当前版本和用户提交版本；不得静默覆盖。
- 密级变化可能收窄权限，保存前展示将失去访问的成员数量。

### 18.5 调查工作台 U-INV-03

```text
┌ 案件 / 调查标题 / revision / 状态      [暂停][取消][提交复核] ┐
├ 阶段轨道：数据准备 → 画像 → 资金流 → 语义 → 模式 → 复核     ┤
├──────────────┬────────────────────────────┬──────────────────┤
│ Steps        │ 当前结果                    │ Evidence Rail    │
│ ✓ reuse      │ Finding / 图 / 表 / 限制     │ 来源             │
│ ✓ profile    │                             │ confidence       │
│ ● flow       │                             │ data version     │
│ ○ semantic   │                             │ hash / refs      │
├──────────────┴────────────────────────────┴──────────────────┤
│ 事件时间线 / Trace / 重试                                          │
└──────────────────────────────────────────────────────────────────┘
```

- 运行状态通过 SSE 更新，断线后使用 Last-Event-ID 恢复并以 REST 快照校准。
- 每个结论必须展示 nature、confidence、algorithm_version、data_version、evidence boundary。
- `NET_FLOW_ONLY` 禁止以“利润”命名；`INFERRED` 禁止以“身份确认”命名。
- 控制按钮由状态机与权限共同决定，后端仍需重复校验。

### 18.6 地址详情 U-EXP-02

Tab 顺序：概览、资产、普通交易、Token Transfer、Internal、对手方、语义事件、分析、Coverage。

- 当前余额显示 `balance_source`、`is_current_balance` 和观测时间。
- 历史净流量单独分区，不得显示为当前余额。
- 金额默认人类可读，但可切换原始整数；复制永远复制完整值。
- 表格筛选写入 URL，可分享同一查询；查询参数摘要进入审计。
- 数据集不完整时只显示业务状态“Coverage 78%”，解释由 Info Dialog 提供。

### 18.7 资金图谱 U-GRAPH-01

- 左侧 Lens：资金流、时间、资产、风险、实体、证据层。
- 中间 Canvas：LOD、聚合边、方向、金额、时间；节点操作不依赖右键。
- 右侧 Inspector/Evidence Rail：对象属性、证据、相关 Finding、操作。
- 顶部：搜索、深度、预算、布局、保存、分支、快照、导出。
- 1000+ 节点必须启用 LOD/聚类；2000 节点达到硬预算时停止扩展并通过 Dialog 说明。
- 图数据与 Evidence 引用必须可重放，布局变化和证据变化分开存储。

### 18.8 任务详情 U-JOB-02

- Overview：状态、创建人、链、范围、数据集、Coverage 命中、有效性能模式。
- Pipeline：DownloadJob → Subject → Dataset → Shard，可展开查看成功游标、页数、行数和错误。
- 地址进度：每个地址独立显示，不用 Job 聚合进度掩盖卡住地址。
- Logs：只显示结构化事件，错误打开 Dialog 展示详情和 Trace。
- Exports：格式、状态、行数、哈希、文件大小、有效期和 Range 支持。
- 暂停/恢复/取消按实际状态显示；终态任务禁止无意义控制。

### 18.9 Explorer 与地址/交易详情 U-EXP-01/02/03

```text
┌ 全局对象输入：链 | 地址/交易哈希                     [查询] ┐
├ 对象头：类型 | 完整值 | 复制 | 标签 | 案件 | 数据更新时间 ┤
├ Tab：概览 | 资产 | 活动 | 对手方 | 语义 | Logs/Trace      ┤
├──────────────────────────────────────┬────────────────────┤
│ 可筛选、cursor 分页的数据表            │ Evidence Rail      │
│ 时间/方向/资产/方法/金额/对手方/来源    │ Coverage           │
│                                      │ 数据来源/口径       │
└──────────────────────────────────────┴────────────────────┘
```

- 查询前根据 chain registry 识别/验证地址，不允许用 EVM 正则拒绝 TRON Base58。
- 资产页把“当前 Provider 余额”和“本地历史净流量”分开；`is_current_balance=false` 不得放入当前余额总计。
- 金额单元支持原始值、格式化值、decimals、symbol；未知 decimals 不擅自除以 10 的幂。
- 每个详情 Tab 将筛选和 cursor 写入 URL，刷新后可恢复；复制操作进入 activity audit。
- Trace/Logs 缺失时显示业务空状态；如触发 Provider 补取，先通过 Dialog 告知成本和数据来源。

### 18.10 新建下载 U-DL-01

采用真实顺序表单，不使用纯装饰编号：

```text
对象 -> 链与范围 -> Dataset -> 复用策略 -> Coverage/成本预览 -> 确认创建
```

- 地址支持粘贴和 CSV/XLSX 导入；导入先在浏览器显示解析摘要，服务端再次验证。
- 每个地址可单独选择链；重复判断键为 chain + normalized address。
- 范围支持全历史、区块范围、时间范围、从上次同步继续；转换后的区块边界由服务端返回。
- Dataset 选项来自 capabilities API，不在前端写死。
- 预览返回 local coverage、missing ranges、estimated rows/cost/time、reused job 和风险。
- 默认 `prefer_local`；选择 `refresh` 必须打开 ConfirmDialog 并说明将新增的 Provider 工作。
- 提交成功 Dialog 显示 created/reused、job ID 和“查看任务”操作。

### 18.11 任务列表与详情 U-JOB-01/02

- 列表默认“我的活动任务”，支持状态、链、Dataset、案件、创建时间、来源筛选。
- 进度至少展示 Job、地址和 Dataset 三层；Shard 只在详情中显示。
- 批量取消只对可取消状态启用，先显示影响数量和已完成数据保留规则。
- 详情页面顶区显示 requested/effective mode、成功游标时间、wall-clock rows/s、Provider rows/s，口径不可混合。
- SSE 更新与周期查询以 revision 合并；旧 revision 事件不得覆盖新状态。
- 错误展示用户可理解分类；Provider 原始错误放在有权限的诊断详情中并脱敏。

### 18.12 Findings、Evidence 与报告页面

- Findings 列表按状态、类型、nature、confidence、算法/版本筛选。
- confidence 同时给数值和解释入口；nature 使用 `CONFIRMED/INFERRED/HEURISTIC` 明文标签。
- Evidence 详情固定展示 source refs、content hash、provenance、collected/verified time、supersedes 链。
- “确认 Finding”Dialog 列出引用 Evidence 及其验证状态；不满足规则时禁用主操作。
- 报告页展示 artifact 版本快照，不使用“当前最新结果”动态替换已生成报告内容。
- 文件下载完成后前端可显示服务端 hash；真正校验由下载客户端/验收脚本完成，不能假装浏览器已验证。

### 18.13 用户列表页、表单页和移动端统一模板

- 列表页：PageHeader → FilterBar → active filter chips → DataTable/card list → cursor pager → batch action bar。
- 表单页：对象上下文 → 分区字段 → 预览区 → sticky action footer；警告/说明仍用 Dialog。
- 详情页：对象头 → Tab → 主数据 → DetailDrawer/Evidence Rail；所有 Tab 可深链接。
- 手机列表默认卡片显示 4–6 个关键字段，次要字段进入详情；批量操作进入底部 action sheet 式 Dialog。
- 手机表单一列，主操作固定底部时必须为内容留出 safe-area 和滚动空间，不能遮挡焦点。

## 19. 后台管理系统逐页规格

### 19.1 路由总表

| ID | 路由 | 页面 | 权限 | 主要 API | 核心内容与操作 |
|---|---|---|---|---|---|
| A-HOME-01 | `/admin` | 运营总览 | admin:dashboard:read | admin overview | 系统健康、告警、队列、失败任务、成本、审计异常 |
| A-ORG-01 | `/admin/organizations` | 组织列表 | admin:org:list | organizations API | 状态、成员、工作区、用量、创建组织 |
| A-ORG-02 | `/admin/organizations/:id` | 组织详情 | admin:org:read | org detail | 基本信息、成员、工作区、预算、配置、风险 |
| A-WS-01 | `/admin/workspaces` | 工作区 | admin:workspace:list | workspaces API | 组织筛选、状态、成员、案件、数据量 |
| A-WS-02 | `/admin/workspaces/:id` | 工作区详情 | admin:workspace:read | workspace detail | 成员、角色、案件统计、存储、保留策略 |
| A-USER-01 | `/admin/users` | 用户列表 | admin:user:list | users API | 状态、来源、MFA、最近登录、会话、批量停用 |
| A-USER-02 | `/admin/users/:id` | 用户详情 | admin:user:read | user/sessions/logins | 身份、membership、角色、设备、会话、登录时间线 |
| A-ROLE-01 | `/admin/roles` | 角色列表 | admin:role:list | roles API | 系统/自定义角色、成员数、权限数、复制 |
| A-ROLE-02 | `/admin/roles/:id` | 角色编辑 | admin:role:update | role permissions API | 权限矩阵、作用域、影响预览、版本冲突 |
| A-SESSION-01 | `/admin/security/sessions` | 活跃会话 | admin:session:list | sessions API | 用户、IP、设备、风险、撤销 |
| A-LOGIN-01 | `/admin/security/login-events` | 登录记录 | admin:login_event:read | login-events API | 成功/失败、IP、设备、MFA、风险、Trace |
| A-RISK-01 | `/admin/security/risk-events` | 风险事件 | admin:risk:read | risk API | 暴力尝试、新设备、IP 跃迁、异常下载、处置 |
| A-JOB-01 | `/admin/jobs` | 全局任务 | admin:job:list | admin jobs API | 全组织任务、队列、地址卡点、批量控制 |
| A-JOB-02 | `/admin/jobs/:id` | 任务诊断 | admin:job:read | job diagnostics | 状态、Shard、Checkpoint、Provider、spool、对象、Trace |
| A-DATA-01 | `/admin/data/coverage` | Coverage | admin:coverage:read | coverage API | 链/Dataset/区间、缺口、异常、重建 |
| A-DATA-02 | `/admin/data/quality` | 数据质量 | admin:data_quality:read | quality API | 新鲜度、重复、缺失、Schema drift、冲突来源 |
| A-DATA-03 | `/admin/data/storage` | 存储 | admin:storage:read | storage API | PG/CH/MinIO 用量、增长、对象缺失、清理候选 |
| A-DATA-04 | `/admin/data/retention` | 保留策略 | admin:retention:manage | retention API | 策略、影响预览、审批、归档/删除任务 |
| A-EXPORT-01 | `/admin/exports` | 全局导出 | admin:export:list | admin exports API | 用户、案件、格式、大小、下载、过期、异常 |
| A-PROVIDER-01 | `/admin/providers` | Provider 列表 | admin:provider:list | provider status | 健康、配额、错误率、P95、成本、熔断 |
| A-PROVIDER-02 | `/admin/providers/:provider` | Provider 详情 | admin:provider:read | provider detail | 账户、路由、能力、用量、错误、测试和历史 |
| A-ROUTING-01 | `/admin/providers/routing` | 路由策略 | admin:routing:update | routing policy | 链/Dataset 优先级、fallback、限流、熔断 |
| A-PERF-01 | `/admin/runtime/performance` | 性能控制 | admin:performance:update | performance APIs | SAFE/FAST/TURBO、并发、内存、背压、历史 |
| A-KEY-01 | `/admin/api-keys` | API/MCP Key | admin:key:list | key API | scope、IP、限额、过期、轮换、吊销 |
| A-REQ-01 | `/admin/requests` | 请求审计 | admin:request:read | request log API | Provider/Actions/MCP/用户 API 请求、耗时、错误 |
| A-COST-01 | `/admin/costs` | 成本 | admin:cost:read | cost API | Provider、组织、用途、预算、趋势、异常 |
| A-BUDGET-01 | `/admin/budgets` | 预算 | admin:budget:manage | budget API | 周期、维度、阈值、动作、版本 |
| A-ALERT-01 | `/admin/alerts` | 告警中心 | admin:alert:read | alerts API | 告警、认领、抑制、解决、关联对象 |
| A-AUDIT-01 | `/admin/audit` | 审计中心 | admin:audit:read | audit API | actor、IP、设备、动作、资源、结果、Trace |
| A-AUDIT-02 | `/admin/audit/:eventId` | 审计详情 | admin:audit:read | audit detail | before/after、策略、链路、哈希、关联事件 |
| A-AUDIT-03 | `/admin/audit/exports` | 审计导出 | admin:audit:export | audit export | 条件、格式、manifest、SHA-256、下载 |
| A-CONTROL-01 | `/admin/control` | 实时控制面 | admin:control:read | control state | 保护状态、队列、熔断、人工覆盖、动作历史 |
| A-KILL-01 | `/admin/control/kill-switch` | Kill Switch | admin:kill_switch:update | kill switch API | 影响预览、step-up、双人审批、执行/恢复 |
| A-CONFIG-01 | `/admin/settings` | 系统设置 | admin:setting:read | settings API | 分组配置、来源、当前版本、修改历史 |
| A-FLAG-01 | `/admin/feature-flags` | 功能开关 | admin:feature_flag:manage | flags API | 环境、组织/工作区灰度、百分比、回滚 |
| A-NOTIFY-01 | `/admin/notification-channels` | 通知渠道 | admin:notification:manage | channel API | Email/Webhook 等适配器、测试、密钥状态 |
| A-VERSION-01 | `/admin/system/version` | 版本与迁移 | admin:system:read | version/migration API | build SHA、schema version、组件兼容性 |
| A-BACKUP-01 | `/admin/system/backups` | 备份恢复 | admin:backup:manage | backup/restore API | 备份、校验、恢复演练、RPO/RTO |

### 19.2 运营总览 A-HOME-01

```text
┌ 环境 Production | 数据时间 14:32:10 | [刷新] ───────────────┐
├ 健康轨道：API | Worker | PG | CH | MinIO | NATS | Provider ┤
├───────────────┬───────────────────────┬────────────────────┤
│ 需要处理       │ 任务与队列             │ 成本与配额          │
│ P0/P1 告警     │ Running/Waiting/Failed │ 今日/月度/预测       │
├───────────────┴───────────────────────┴────────────────────┤
│ 请求趋势 / 错误率 / P95 / 下载有效 rows/s                   │
├ 最近高风险动作 ──────────────────────── 最近登录异常 ──────┤
└─────────────────────────────────────────────────────────────┘
```

- 所有指标返回 `as_of`、窗口、来源和查询状态；不同窗口不能放在同一数字中比较。
- 吞吐分为 Provider 拉取、Pipeline、端到端 wall-clock、导出生成和公网下载。
- 点击指标进入预置筛选页面；系统健康只展示真实探针结果，不用前端假状态。
- P0 告警必须位于首屏，无障碍名称包含严重级别和数量。

### 19.3 用户详情 A-USER-02

Tab：概览、组织/工作区、角色权限、会话设备、登录事件、操作记录。

- 顶部显示用户状态、身份来源、MFA、最近登录和风险。
- “有效权限”页面同时展示角色来源、作用域和最终 allow/deny，不只列角色名称。
- 停用用户先预览其运行任务、待审批、拥有的案件和 API Key；选择转移接收人。
- 撤销单一会话或全部会话均需原因；当前管理员撤销自身会话时给出明确影响。
- 设备信息标为“客户端声明”或“服务端确认”，不把 UA 推断当硬件事实。

### 19.4 角色编辑 A-ROLE-02

布局：左侧资源域，中央动作矩阵，右侧影响预览。

- 权限矩阵使用原生 checkbox，支持按资源全选，但批量变化列出明确差异。
- 保存必须携带 `If-Match`；冲突时比较两个 revision。
- 系统角色不可直接修改，可复制为自定义角色。
- 删除角色前必须显示成员绑定和替代角色；有绑定时禁止直接删除。
- 为自己移除最后一个 Owner 权限必须拒绝；组织永远至少保留一个可用 Owner。

### 19.5 登录记录 A-LOGIN-01

默认列：时间、用户/账号掩码、结果、MFA、client IP、peer IP、设备、浏览器、地区/ASN、风险、Trace ID。

- 原始 UA、Client Hints、可信代理链放入详情抽屉。
- IP 情报显示来源与版本；地区为推断值，不显示为精确位置。
- 支持“同一 IP”“同一设备”“同一会话”“前后 15 分钟事件”快速关联。
- 批量失败检测产生 risk event，不能修改原始 login event。

### 19.6 全局任务诊断 A-JOB-02

固定诊断链：

```text
DownloadJob
  -> SubjectJob
    -> DatasetJob
      -> Shard / Checkpoint
        -> Provider request / usage
          -> Raw object / SHA-256
            -> ClickHouse rows / Coverage
```

- 每层展示 ID、状态、更新时间、行数、bytes、错误和 Trace。
- “推进至链上时间”显示最后成功分页游标对应时间，不显示计划 Shard 边界。
- 重试默认只重试 retryable 失败单元；重新下载完整范围必须选择 `reuse_policy=refresh` 并说明成本。
- 修改性能模式显示 requested/effective/reason，禁止只显示请求模式。

### 19.7 Provider 详情 A-PROVIDER-02

Tab：健康、账户、能力、请求、错误、成本、路由历史、配置历史。

- Provider 密钥只显示摘要、创建/轮换时间、最后使用时间；永不回显明文。
- 连接测试必须使用受控低成本探针，结果包含方法、时间、延迟、错误分类和 Trace。
- 断开 Provider 前显示受影响链、Dataset、任务和 fallback 能力。
- 实际 Provider 能力与静态配置不一致时，显示状态并产生告警，不自动扩大权限或并发。

### 19.8 审计中心 A-AUDIT-01/02

- 高密度表格默认按时间倒序，cursor 分页，超过 100 行虚拟化。
- 查询条件：时间、actor、组织/工作区、IP、设备、会话、动作、资源、结果、风险、Trace。
- 详情页显示事件原文摘要、策略决策、before/after、关联登录、同 Trace 请求和哈希链验证。
- 审计事件不可编辑、隐藏或“标记已处理”；处置通过新事件关联原事件。
- 导出为异步任务，必须记录导出人、条件、事件范围和文件哈希。

### 19.9 Kill Switch A-KILL-01

执行流程：影响预览 → 输入原因 → step-up MFA → 第二审批人确认 → 执行 → 健康验证 → 恢复或保持。

- 影响预览列出将停止/保持的 API、任务类型、Worker、Actions/MCP 和当前运行任务。
- 默认不删除任务、不清空队列、不丢弃 Checkpoint。
- 请求与审批不能是同一用户；紧急 break-glass 模式需更高等级审计和后续复核。
- 恢复操作与开启操作同级保护，恢复后自动执行健康检查。

## 20. 核心业务流程和状态机

### 20.1 登录与会话

```text
anonymous
  -> credentials_submitted
    -> denied
    -> mfa_required -> mfa_denied
                    -> authenticated
    -> authenticated
authenticated -> refreshed -> authenticated
authenticated -> logged_out
authenticated -> expired
authenticated -> revoked
authenticated -> risk_locked
```

状态规则：

- 登录尝试以 request ID 幂等记账，一次请求只能产生一个最终 login event。
- Session 保存 hashed token、audience、IP/设备摘要、创建/最近活动/绝对过期时间。
- 普通用户 idle 30 分钟、absolute 12 小时；可信设备可配置 30 天 refresh session。
- 后台 idle 15 分钟、absolute 8 小时；高风险操作要求 5 分钟内 step-up。
- 角色/用户停用立即增加 `authz_version` 并撤销相关 Session。

### 20.2 案件状态

```text
draft -> active -> under_review -> closed -> archived
  |        |            |            |
  +--------+------------+------------+-> reopened(active)
```

- `archived` 只读；恢复后进入 `active`。
- `closed` 前必须处理所有待复核 Finding 和未完成报告审批，或填写例外原因。
- 删除不是普通状态。合法删除需要 retention/deletion 审批流程。

### 20.3 Investigation 状态

```text
draft/created -> running -> waiting_data -> running
                         -> waiting_review -> completed
running -> paused -> running
running/waiting/paused -> cancelled
running -> failed_retryable -> running
running -> failed_final
```

- 状态转换使用显式 command endpoint，禁止通用 PATCH 任意写 `status`。
- 每次转换检查 revision、permission、当前状态、原因和依赖状态。
- completed artifacts 不可原地修改；重跑生成新 run/revision。

### 20.4 Finding 与 Evidence

```text
Finding: candidate -> in_review -> confirmed | rejected | superseded
Evidence: collected -> verified | disputed -> superseded
```

- confirmed Finding 至少引用一个 verified Evidence，或记录经过审批的例外。
- Evidence 内容不可覆盖；修正生成新版本并用 `supersedes_id` 关联。
- 删除 UI 实际执行“从当前案件视图移除”，底层保留审计和引用完整性。

### 20.5 下载任务

沿用现有 DownloadJob → Subject → Dataset → Shard 四级状态。补充规则：

- 相同 chain + address + dataset + 范围默认复用 Coverage 和进行中任务。
- intentional redownload 只能显式 `reuse_policy=refresh`，必须审计成本预览。
- 暂停冻结新增 Provider 请求，保留已成功页、游标和已提交数据。
- cancel 不删除历史和 Coverage；未提交临时对象由回收任务处理。
- Job 完成需同时满足所有地址/Dataset 终态、Raw manifest、规范化写入和 Coverage commit。

### 20.6 报告审批

```text
draft -> generated -> in_review -> approved -> signed -> archived
                     -> rejected -> generated(new revision)
```

- 报告 revision 绑定 Investigation、Finding、Evidence、Canvas Snapshot 的精确版本。
- approved/signed 文件不可覆盖；重新生成产生新 revision 和新 hash。
- 下载响应包含 ETag、Content-Length、内容类型和可校验 SHA-256。

### 20.7 高风险管理操作

统一命令流程：

```text
preview -> confirm_reason -> step_up -> approval(optional) -> execute
        -> expired         -> rejected                   -> verify -> audit
```

适用：Kill Switch、Provider 断开/密钥轮换、全局并发上调、批量用户停用、权限大范围变更、数据删除、恢复备份、审计导出。

## 21. API 设计规格

### 21.1 必须提交的契约文件

```text
openapi/
├─ auth-v1.yaml
├─ user-v2.yaml
├─ admin-v1.yaml
├─ common/
│  ├─ errors.yaml
│  ├─ pagination.yaml
│  ├─ money.yaml
│  └─ audit.yaml
└─ generated/
   └─ typescript/       # CI 生成，禁止手改
```

CI 必须验证 OpenAPI 可解析、operationId 唯一、响应错误模型完整、生成结果无 diff。任何新增 handler 没有契约和权限声明不得合并。

### 21.2 通用请求规则

- JSON 使用 `application/json; charset=utf-8`，字段 snake_case。
- 时间为 UTC ISO 8601，例如 `2026-09-12T06:30:00.123Z`。
- UUID 使用小写标准格式；chain 使用 registry canonical slug。
- 地址响应同时给出 `address` 原文和 `normalized_address`；显示由前端处理，查询用标准化值。
- 金额结构：`raw_amount`、`decimals`、`display_amount` 均为字符串或整数，不能把大整数放入 JSON number。
- 所有修改请求携带 `X-CSRF-Token`；创建/命令携带 `Idempotency-Key`。
- 有 revision 的更新携带 `If-Match: "<revision>"`，成功响应返回 `ETag`。
- 查询超时默认 30 秒，管理聚合默认 15 秒；长任务返回 202 + job ID。
- 列表使用 `limit`（默认 50，最大 200）和 opaque `cursor`，响应提供 `next_cursor`。
- 客户端只允许提交公开 filter；tenant、actor 和最终授权 scope 由服务端注入。

### 21.3 通用响应

成功：

```json
{
  "success": true,
  "data": {},
  "meta": {
    "request_id": "req_...",
    "trace_id": "tr_...",
    "server_time": "2026-09-12T06:30:00.123Z",
    "data_version": "optional",
    "next_cursor": null
  },
  "error": null
}
```

```json
{
  "success": false,
  "data": null,
  "meta": {
    "request_id": "req_...",
    "trace_id": "tr_...",
    "server_time": "2026-09-12T06:30:00.123Z"
  },
  "error": {
    "code": "AUTHZ_FORBIDDEN",
    "message": "当前账号无权执行此操作",
    "field_errors": [],
    "retryable": false
  }
}
```

错误码前缀：`AUTHN_`、`AUTHZ_`、`VALIDATION_`、`RESOURCE_`、`CONFLICT_`、`RATE_LIMIT_`、`PROVIDER_`、`DATA_`、`INTERNAL_`。服务端日志保留内部 cause，响应不得返回 SQL、堆栈、密钥或 Provider 原始敏感内容。

HTTP 规则：200 查询/同步更新；201 创建；202 异步接受；204 无正文；400 校验；401 未认证；403 未授权；404 对象不可见/不存在；409 状态冲突；412 revision 冲突；422 业务规则；429 限流；503 依赖不可用。

### 21.4 认证 API

| Method | Path | 认证 | 幂等 | 审计 | 说明 |
|---|---|---|---|---|---|
| GET | `/api/auth/v1/methods` | public | - | access | 返回允许的 OIDC/本地登录方式，不含 secret |
| POST | `/api/auth/v1/login` | public | request ID | login | 本地账户登录，设置 user host-only Session |
| GET | `/api/auth/v1/oidc/start` | public | - | login_start | 生成 state/nonce/PKCE 并重定向 |
| GET | `/api/auth/v1/oidc/callback` | public | state | login | 校验 code/state/nonce，建立 Session |
| POST | `/api/auth/v1/mfa/verify` | challenge | key | mfa | 验证 TOTP/恢复码 |
| POST | `/api/auth/v1/refresh` | session | key | session_refresh | 旋转 Session token，检测重放 |
| POST | `/api/auth/v1/logout` | session | key | logout | 撤销当前 Session、清 Cookie |
| GET | `/api/auth/v1/session` | session | - | sampled access | 当前用户、scope、permissions、csrf token |
| GET | `/api/auth/v1/sessions` | session | - | sensitive_read | 当前用户全部会话 |
| DELETE | `/api/auth/v1/sessions/{id}` | session | key | session_revoke | 撤销一个会话 |
| POST | `/api/auth/v1/sessions/revoke-all` | session+step-up | key | session_revoke_all | 除当前或全部下线 |
| GET | `/api/auth/v1/devices` | session | - | sensitive_read | 设备列表 |
| PATCH | `/api/auth/v1/devices/{id}` | session | key+etag | device_update | 修改信任状态/名称 |

登录请求：

```json
{
  "identifier": "user@example.com",
  "password": "<never-log>",
  "remember_device": false,
  "return_to": "/home"
}
```

登录成功由 `Set-Cookie` 设置 `__Host-cs_user_session`，响应不返回 Session Token：

```json
{
  "success": true,
  "data": {
    "state": "authenticated",
    "user": {"id": "...", "display_name": "..."},
    "next": "/home"
  },
  "meta": {"request_id": "...", "trace_id": "...", "server_time": "..."},
  "error": null
}
```

需要 MFA 时返回 202，`challenge_id` 仅可使用一次且 5 分钟过期。

### 21.5 用户 API 路由

#### 工作台、搜索、案件

| Method | Path | Permission | 操作说明 |
|---|---|---|---|
| GET | `/api/v2/dashboard` | dashboard:read | 同一 `as_of` 的个人聚合数据 |
| GET | `/api/v2/search` | search:read | `q,type[],workspace_id,cursor,limit`，服务端按权限裁剪 |
| GET/POST | `/api/v2/cases` | case:list / case:create | 列表、创建案件 |
| GET/PATCH | `/api/v2/cases/{case_id}` | case:read / case:update | 查询、版本化更新 |
| POST | `/api/v2/cases/{case_id}/commands/{activate|close|archive|reopen}` | case:state:update | 显式状态命令 |
| GET/POST | `/api/v2/cases/{case_id}/members` | case:member:list/manage | 案件成员 |
| DELETE | `/api/v2/cases/{case_id}/members/{user_id}` | case:member:manage | 移除成员，检查最后负责人 |
| GET/POST | `/api/v2/cases/{case_id}/subjects` | subject:list/create | 多类型调查对象 |
| GET | `/api/v2/cases/{case_id}/activity` | case:activity:read | 业务时间线 |
| GET/POST | `/api/v2/cases/{case_id}/comments` | comment:list/create | 协作评论 |

创建案件：

```json
{
  "workspace_id": "uuid",
  "case_no": "HZ-2026-001",
  "name": "资金路径调查",
  "description": "调查目的，不写入秘密凭证",
  "classification": "restricted",
  "priority": "high",
  "member_ids": ["uuid"],
  "tag_ids": ["uuid"]
}
```

#### 调查、Finding、Evidence、Canvas、报告

| Method | Path | Permission | 操作说明 |
|---|---|---|---|
| GET/POST | `/api/v2/investigations` | investigation:list/create | 列表、创建；POST 必须 Idempotency-Key |
| POST | `/api/v2/investigations/preview` | investigation:create | Coverage、预计 Dataset/范围/预算预览 |
| GET | `/api/v2/investigations/{id}` | investigation:read | 详情、revision、边界 |
| GET | `/api/v2/investigations/{id}/runtime` | investigation:read | runs/stages/steps/checkpoints |
| POST | `/api/v2/investigations/{id}/commands/{start|pause|resume|cancel|retry|submit-review}` | 对应 command permission | 状态转换 |
| GET | `/api/v2/investigations/{id}/findings` | finding:list | cursor、status/type/confidence filter |
| GET/PATCH | `/api/v2/findings/{id}` | finding:read/review | 只允许 review 字段，算法结果不可覆盖 |
| POST | `/api/v2/findings/{id}/commands/{confirm|reject|supersede}` | finding:review | 审批命令 |
| GET | `/api/v2/evidence/{id}` | evidence:read | 敏感读取审计 |
| POST | `/api/v2/evidence/{id}/commands/{verify|dispute|supersede}` | evidence:review | 证据状态命令 |
| GET/POST | `/api/v2/investigations/{id}/canvases` | canvas:list/create | Canvas |
| GET/PATCH | `/api/v2/canvases/{id}` | canvas:read/update | If-Match 冲突控制 |
| POST | `/api/v2/canvases/{id}/branches` | canvas:branch | 分支 |
| GET/POST | `/api/v2/canvases/{id}/snapshots` | canvas:snapshot | 快照 |
| GET/POST | `/api/v2/cases/{id}/reports` | report:list/create | 报告列表/异步生成 |
| GET | `/api/v2/reports/{id}` | report:read | 元数据、精确引用版本 |
| POST | `/api/v2/reports/{id}/commands/{submit|approve|reject|sign|archive}` | report 对应权限 | 审批流 |
| POST | `/api/v2/reports/{id}/download-ticket` | report:download | 60 秒一次性短票据 |

调查创建请求必须包含明确的能力和预算版本：

```json
{
  "case_id": "uuid",
  "goal": "追踪目标地址的主要资金来源与去向",
  "mode": "assisted",
  "subjects": [{"type": "ADDRESS", "chain": "tron", "value": "T..."}],
  "expected_outputs": ["FLOW_GRAPH", "FINDINGS", "EVIDENCE_INDEX"],
  "data_requirement": {
    "datasets": ["transactions", "token_transfers"],
    "range": {"type": "full_history"},
    "reuse_policy": "prefer_local"
  },
  "budget": {"max_hops": 3, "max_nodes": 2000, "max_provider_cost_usd": "10.00"},
  "capability_versions": {"analysis.flow": "1.0"}
}
```

#### Explorer、下载、导出、通知、个人

| Method | Path | Permission | 操作说明 |
|---|---|---|---|
| GET | `/api/v2/explorer/addresses/{chain}/{address}/overview` | explorer:read | 口径和 Coverage |
| GET | `/api/v2/explorer/addresses/{chain}/{address}/{assets|activity|counterparties|semantic}` | explorer:read | cursor 查询 |
| GET | `/api/v2/explorer/transactions/{chain}/{hash}` | explorer:read | 交易详情 |
| GET | `/api/v2/explorer/transactions/{chain}/{hash}/{trace|logs|decode}` | explorer:read | 深度数据，标 provenance |
| POST | `/api/v2/downloads/preview` | download:create | Coverage/成本/行数/风险预览 |
| POST | `/api/v2/downloads` | download:create | 创建或复用 Job |
| GET | `/api/v2/jobs` | job:list:self | 默认仅本人/有案件权限任务 |
| GET | `/api/v2/jobs/{id}` | job:read | 四级进度和 runtime |
| POST | `/api/v2/jobs/{id}/commands/{pause|resume|cancel|retry}` | job:control | 控制任务 |
| GET | `/api/v2/jobs/{id}/events` | job:read | SSE，支持 Last-Event-ID |
| GET/POST | `/api/v2/exports` | export:list/create | 创建异步导出 |
| GET | `/api/v2/exports/{id}` | export:read | 状态、行数、哈希 |
| POST | `/api/v2/exports/{id}/download-ticket` | export:download | 一次性短票据 |
| GET | `/api/v2/notifications` | notification:read | cursor、unread/type filter |
| POST | `/api/v2/notifications/{id}/read` | notification:update | 标记已读 |
| POST | `/api/v2/notifications/read-all` | notification:update | 按当前 scope 全部已读 |
| GET/PATCH | `/api/v2/me/profile` | profile:read/update:self | 个人资料 |
| GET/PATCH | `/api/v2/me/preferences` | profile:read/update:self | 时区、语言、列设置 |

### 21.6 后台 API 路由

#### 组织、用户、权限、会话

| Method | Path | Permission | 风险/审计 |
|---|---|---|---|
| GET/POST | `/api/admin/v1/organizations` | admin:org:list/create | 创建需审计 |
| GET/PATCH | `/api/admin/v1/organizations/{id}` | admin:org:read/update | If-Match、before/after |
| GET/POST | `/api/admin/v1/workspaces` | admin:workspace:list/create | 组织 scope |
| GET/PATCH | `/api/admin/v1/workspaces/{id}` | admin:workspace:read/update | If-Match |
| GET/POST | `/api/admin/v1/users` | admin:user:list/create | 创建/邀请审计 |
| GET/PATCH | `/api/admin/v1/users/{id}` | admin:user:read/update | 停用走 command |
| POST | `/api/admin/v1/users/{id}/commands/{disable|enable|reset-mfa}` | 对应敏感权限 | step-up，影响预览 |
| GET | `/api/admin/v1/users/{id}/effective-permissions` | admin:permission:read | 敏感读取 |
| GET/POST | `/api/admin/v1/roles` | admin:role:list/create | 自定义角色 |
| GET/PATCH/DELETE | `/api/admin/v1/roles/{id}` | admin:role:read/update/delete | If-Match、引用检查 |
| POST | `/api/admin/v1/roles/{id}/preview` | admin:role:update | 返回新增/移除权限及受影响成员 |
| GET | `/api/admin/v1/sessions` | admin:session:list | IP/设备敏感读取 |
| POST | `/api/admin/v1/sessions/{id}/revoke` | admin:session:revoke | 原因必填 |
| GET | `/api/admin/v1/login-events` | admin:login_event:read | cursor、时间上限 |
| GET | `/api/admin/v1/risk-events` | admin:risk:read | 只读原始风险事件 |
| POST | `/api/admin/v1/risk-events/{id}/commands/{acknowledge|resolve}` | admin:risk:update | 产生关联新事件 |

#### 任务、数据、Provider、控制

| Method | Path | Permission | 风险/审计 |
|---|---|---|---|
| GET | `/api/admin/v1/jobs` | admin:job:list | 跨组织需显式 scope |
| GET | `/api/admin/v1/jobs/{id}/diagnostics` | admin:job:read | 聚合 Job/Shard/Provider/Object/Trace |
| POST | `/api/admin/v1/jobs/{id}/commands/{pause|resume|cancel|retry}` | admin:job:control | 原因、影响、审计 |
| GET | `/api/admin/v1/data/coverage` | admin:coverage:read | 链/Dataset/区间 |
| GET | `/api/admin/v1/data/quality` | admin:data_quality:read | freshness/duplicates/missingness |
| POST | `/api/admin/v1/data/quality/scans` | admin:data_quality:run | 异步扫描 |
| GET | `/api/admin/v1/storage` | admin:storage:read | PG/CH/MinIO |
| GET/POST | `/api/admin/v1/retention-policies` | admin:retention:manage | 修改需 preview |
| POST | `/api/admin/v1/deletion-requests` | admin:deletion:request | 双人审批、可恢复窗口 |
| GET | `/api/admin/v1/providers` | admin:provider:list | 实时+配置状态 |
| GET/PATCH | `/api/admin/v1/providers/{id}` | admin:provider:read/update | 不接受明文 secret 回显 |
| POST | `/api/admin/v1/providers/{id}/commands/{connect|test|disconnect|rotate-secret}` | 对应 provider 权限 | step-up/影响预览 |
| GET/PATCH | `/api/admin/v1/routing-policies/{id}` | admin:routing:read/update | If-Match/灰度 |
| GET | `/api/admin/v1/runtime/performance` | admin:performance:read | requested/effective/reason |
| POST | `/api/admin/v1/runtime/performance/preview` | admin:performance:update | 预估影响 |
| POST | `/api/admin/v1/runtime/performance/commands/{apply|rollback}` | admin:performance:update | step-up，高并发需审批 |
| GET | `/api/admin/v1/control/state` | admin:control:read | 控制状态 |
| POST | `/api/admin/v1/control/kill-switch/preview` | admin:kill_switch:update | 影响清单 |
| POST | `/api/admin/v1/control/kill-switch/requests` | admin:kill_switch:update | 创建审批请求 |
| POST | `/api/admin/v1/control/kill-switch/requests/{id}/{approve|reject|execute|restore}` | 分离权限 | 双人、step-up |

#### Key、成本、告警、审计、系统

| Method | Path | Permission | 风险/审计 |
|---|---|---|---|
| GET/POST | `/api/admin/v1/api-keys` | admin:key:list/create | 明文只返回一次 |
| POST | `/api/admin/v1/api-keys/{id}/commands/{rotate|revoke}` | admin:key:rotate/revoke | step-up |
| GET | `/api/admin/v1/requests` | admin:request:read | 脱敏、cursor |
| GET | `/api/admin/v1/costs/summary` | admin:cost:read | 字符串金额、来源 |
| GET/POST | `/api/admin/v1/budgets` | admin:budget:list/manage | If-Match、阈值动作 |
| GET | `/api/admin/v1/alerts` | admin:alert:read | OPEN/ACK/RESOLVED |
| POST | `/api/admin/v1/alerts/{id}/commands/{acknowledge|mute|resolve}` | admin:alert:update | 原因/期限 |
| GET | `/api/admin/v1/audit-events` | admin:audit:read | append-only |
| GET | `/api/admin/v1/audit-events/{id}` | admin:audit:read | hash/related events |
| POST | `/api/admin/v1/audit-exports` | admin:audit:export | step-up、异步 |
| GET/POST | `/api/admin/v1/feature-flags` | admin:feature_flag:list/manage | scope/percentage/revision |
| GET/PATCH | `/api/admin/v1/settings/{key}` | admin:setting:read/update | secret reference only |
| GET | `/api/admin/v1/system/version` | admin:system:read | build/schema/component |
| GET/POST | `/api/admin/v1/backups` | admin:backup:list/create | 高风险、异步 |
| POST | `/api/admin/v1/restores/preview` | admin:restore:run | 只做校验和影响预览 |
| POST | `/api/admin/v1/restores` | admin:restore:run | 双人审批、维护窗口 |

### 21.7 下载文件接口

- 下载票据使用 256-bit 随机值，只保存摘要，默认 60 秒过期、单次使用、绑定 user/export/resource。
- 响应支持 `GET`、`HEAD`、单 Range、`ETag`、`Accept-Ranges`、`Content-Length`、正确 MIME 和 `Content-Disposition`。
- 票据放在 URL 时禁止写入普通 access log；Nginx 对 ticket segment 脱敏。
- 下载审计在票据创建、首次使用、失败、过期时分别记录；不能只记录导出生成。
- 文件校验必须解析 CSV/XLSX/JSONL/Parquet/ZIP；Parquet 首尾 `PAR1`、ZIP manifest 和 SHA-256 均属于验收证据。

## 22. 数据库设计与迁移顺序

现有最高 PostgreSQL 迁移为 `0039`。新增迁移从 `0040` 开始，全部 additive；部署过程不删除现有列或表。

> **实际编号说明（2026-09-12）**：Sprint 3 已占用 0044（tenant scope）、0045（session family）、
> 0046（session device FK）；Sprint 4 协作/审批迁移实际为 `0047_case_collaboration.sql`，
> 权限补齐为 `0048_s4_permission_fix.sql`。下表的语义不变，仅编号顺延，详见 ADR-016。

### 22.1 迁移清单

| 迁移 | 内容 | 回滚策略 |
|---|---|---|
| 0040 | identity、credential、session、trusted_device、login_event | 应用停用新写入；表保留 |
| 0041 | roles、permissions、role_permissions、membership、bindings | 回退到观察模式，不删授权历史 |
| 0042 | audit_events_v2、hash anchors、audit outbox | 保留 append-only 数据 |
| 0043 | notification、preference、delivery attempts | 停止消费者，保留通知 |
| 0044 | case members、comments、reviews、report approvals | 新功能只读 |
| 0045 | feature flags、settings revisions、approval requests | 回退到上一配置 revision |
| 0046 | retention、deletion、backup/restore metadata | 取消未执行请求，不删除历史 |
| 0047 | organization/workspace NOT NULL 回填与约束 | 先审计空值，再 `NOT VALID` + validate |
| 0048 | 查询索引、分区和归档任务 | 可删除新索引，不回退数据 |

### 22.2 IAM 表

`identities`：

| 字段 | 类型/约束 | 说明 |
|---|---|---|
| id | UUID PK | 身份 ID |
| user_id | UUID FK users NOT NULL | 对应用户 |
| provider | VARCHAR(32) | local/oidc provider key |
| provider_subject | VARCHAR(255) | OIDC `sub`，local 为规范化 identifier hash |
| email_normalized | CITEXT | 唯一性按 provider 策略 |
| status | VARCHAR(24) | active/disabled/locked |
| claims_snapshot | JSONB | 白名单 claims，禁止保存 token |
| last_authenticated_at | TIMESTAMPTZ | 最近认证 |
| created_at/updated_at | TIMESTAMPTZ | 审计时间 |

唯一索引：`(provider, provider_subject)`；`email_normalized` 使用 CITEXT 或写入时 Unicode/大小写规范化。

`credentials` 仅用于本地账户：`identity_id`、`password_hash`、`password_changed_at`、`failed_attempts`、`locked_until`、`mfa_secret_ciphertext`、`recovery_code_hashes`、`version`。密码使用 Argon2id；加密字段的 key ID 单独保存。

`sessions`：`id`、`user_id`、`audience(user|admin)`、`token_hash`、`csrf_secret_hash`、`device_id`、`client_ip`、`peer_ip`、`user_agent_hash`、`authn_method`、`mfa_at`、`authz_version`、`created_at`、`last_seen_at`、`idle_expires_at`、`absolute_expires_at`、`revoked_at`、`revoke_reason`。索引 `(user_id, revoked_at, last_seen_at desc)`、`token_hash unique`、`absolute_expires_at`。

`trusted_devices`：随机设备 ID 摘要、user、label、首次/最近 IP 与 UA 摘要、first_seen、last_seen、trusted_until、revoked_at；不保存侵入式指纹。

`login_events` 按月分区，字段完全遵循 6.6.2；索引 `(occurred_at desc)`、`(user_id, occurred_at desc)`、`(client_ip, occurred_at desc)`、`(device_id, occurred_at desc)`、`(result, occurred_at desc)`、`trace_id`。

### 22.3 RBAC 表

- `organization_memberships(id, organization_id, user_id, status, joined_at, revision)`，唯一 `(organization_id,user_id)`。
- `workspace_memberships(id, workspace_id, user_id, status, joined_at, revision)`，唯一 `(workspace_id,user_id)`。
- `roles(id, organization_id nullable, key, name, description, system, revision, created_by, timestamps)`。
- `permissions(key PK, resource, action, risk_level, description)`。
- `role_permissions(role_id, permission_key)`。
- `member_role_bindings(id, user_id, role_id, scope_type, scope_id, expires_at, created_by)`。
- `policy_decision_logs` 只记录拒绝、敏感 allow 和采样普通 allow，避免主库被每次普通读取压垮；完整业务读取另入 activity/audit。

约束：scope_type 与 scope_id 匹配；organization role 不得绑定到其他 organization；系统角色不能被修改；最后一个 Owner 不能删除。

#### 22.3.1 Permission Catalog

权限必须逐条 seed，生产角色不使用不可审计的 `*` 通配符。可执行的 seed 清单：

```text
dashboard:read
search:read
case:list, case:create, case:read, case:update, case:state:update,
case:member:list, case:member:manage, case:activity:read
subject:list, subject:create, subject:update, subject:remove
investigation:list, investigation:create, investigation:read,
investigation:start, investigation:pause, investigation:resume,
investigation:cancel, investigation:retry, investigation:submit_review
finding:list, finding:read, finding:review
evidence:list, evidence:read, evidence:create, evidence:review, evidence:download
canvas:list, canvas:create, canvas:read, canvas:update, canvas:branch, canvas:snapshot, canvas:export
explorer:read, explorer:provider_fetch
download:create, download:refresh
job:list:self, job:list:workspace, job:read, job:control
export:list, export:create, export:read, export:download
report:list, report:create, report:read, report:review, report:sign, report:download
inbox:read, notification:read, notification:update
profile:read:self, profile:update:self, session:list:self, session:revoke:self

admin:dashboard:read
admin:org:list, admin:org:read, admin:org:create, admin:org:update
admin:workspace:list, admin:workspace:read, admin:workspace:create, admin:workspace:update
admin:user:list, admin:user:read, admin:user:create, admin:user:update,
admin:user:disable, admin:user:enable, admin:user:reset_mfa
admin:role:list, admin:role:read, admin:role:create, admin:role:update, admin:role:delete
admin:permission:read
admin:session:list, admin:session:revoke
admin:login_event:read, admin:risk:read, admin:risk:update
admin:job:list, admin:job:read, admin:job:control
admin:coverage:read, admin:data_quality:read, admin:data_quality:run
admin:storage:read, admin:retention:manage, admin:deletion:request, admin:deletion:approve
admin:export:list
admin:provider:list, admin:provider:read, admin:provider:update,
admin:provider:connect, admin:provider:disconnect, admin:provider:rotate_secret
admin:routing:read, admin:routing:update
admin:performance:read, admin:performance:update
admin:key:list, admin:key:create, admin:key:rotate, admin:key:revoke
admin:request:read, admin:cost:read, admin:budget:list, admin:budget:manage
admin:alert:read, admin:alert:update
admin:audit:read, admin:audit:export
admin:control:read, admin:kill_switch:request, admin:kill_switch:approve,
admin:kill_switch:execute, admin:kill_switch:restore
admin:setting:read, admin:setting:update
admin:feature_flag:list, admin:feature_flag:manage
admin:notification:manage, admin:system:read
admin:backup:list, admin:backup:create, admin:restore:preview, admin:restore:run
```

默认角色映射：

| Role | 默认权限范围 |
|---|---|
| Owner | 当前 organization 的全部显式 user/admin permission；高风险动作仍需 step-up/双人规则 |
| Platform Admin | admin dashboard/org/workspace/user/job/data/provider/routing/performance/request/cost/budget/alert/control/settings/system；无 `evidence:read`、report download、role delete、restore run 默认权限 |
| Security Admin | user/role/permission/session/login/risk/key/audit 安全相关；不读案件正文、不控制下载 |
| Auditor | admin audit/login/request/cost/system read 与 audit export；所有写权限为空 |
| Investigator | case/subject/investigation/finding/evidence/canvas/explorer/download/job/export/report/inbox 的工作区权限；Finding 最终确认可要求 Reviewer |
| Analyst | case read、subject read、investigation read/create、explorer、download、job、export、canvas read/update；不审批 Finding/Report |
| Reviewer | case/investigation/finding/evidence/report read 与 review；不能修改算法产物或启动高成本下载 |
| Viewer | 已授权对象的 case/investigation/finding/evidence/canvas/report read；默认不能敏感下载 |
| API Client | 只按 key scope 赋予 explorer/download/status/export 等明确机器权限 |

Owner 虽拥有完整权限，Policy 仍需要 risk、step-up、separation-of-duty 和对象 scope 检查。

### 22.4 审计与用户行为表

`audit_events_v2` 必须包含：

```text
id, sequence_no, occurred_at, event_version,
actor_type, actor_id, actor_display_snapshot,
organization_id, workspace_id, session_id, device_id,
client_ip, peer_ip, user_agent_hash,
action, category, resource_type, resource_id,
route_template, request_id, trace_id,
decision, result, http_status, business_error_code,
reason, before_json, after_json, patch_json,
data_source, data_version, sensitivity,
previous_hash, event_hash, outbox_id
```

- `sequence_no` 在分区/组织 hash chain 中单调；`event_hash` 覆盖规范化事件内容和 previous_hash。
- 大对象不放 before/after，只保存 object key、version、content hash。
- `user_activity_events` 保存高频页面/查询事件，按日或月分区，90 天自动归档/删除。
- 审计表应用角色只有 INSERT/SELECT 指定视图权限，无 UPDATE/DELETE/TRUNCATE。

### 22.5 协作和治理表

- `case_members(case_id,user_id,role,added_by,created_at)`。
- `case_comments(id,case_id,thread_id,parent_id,body_markdown,body_hash,created_by,edited_revision,timestamps)`；渲染前净过程严格转义/白名单。
- `review_requests(id,resource_type,resource_id,requested_by,reviewer_id,status,reason,decision_note,revision,timestamps)`。
- `report_approvals(report_id,revision,reviewer_id,decision,content_hash,created_at)`。
- `notifications(id,user_id,type,title_key,object_type,object_id,payload_json,read_at,created_at)`。
- `notification_delivery_attempts` 保存渠道、状态、次数、provider message ID、脱敏错误。
- `feature_flags` 保存 key、默认值、scope、percentage、rules、revision；评估结果可重现。
- `configuration_revisions` 保存 key、非秘密 value 或 secret reference、before hash、after hash、actor、approval、timestamps。
- `approval_requests` 通用承载高风险双人流程，requester 与 approver 约束不同。

### 22.6 多租户约束与查询

- 每个业务 request 在认证后生成不可变 `TenantContext`；repository 方法必须显式接收它。
- 业务 SQL 模板必须包含 organization/workspace 条件；代码审查禁止无 scope 的 list/get。
- 公共链原始数据可以共享，但案件链接、标签、备注、导出、报告和审计必须租户隔离。
- 可选启用 PostgreSQL RLS 作为第二道防线，但不能以 RLS 代替应用授权。
- ClickHouse 查询由后端先解析允许的对象集合/工作区关联，再提交参数化查询。
- MinIO key：`org/{org_id}/ws/{workspace_id}/{resource}/{yyyy}/{mm}/{object_id}`。

## 23. 安全设计

### 23.1 信任边界

```text
Internet/User Device
  -> Cloudflare(optional)
    -> Nginx (trusted proxy boundary)
      -> user/admin/API/MCP routes
        -> Go API (authentication + policy + audit)
          -> PostgreSQL / ClickHouse / MinIO / NATS
          -> External Providers
```

只有明确配置的代理地址可提供真实客户端 IP 头。Cloudflare Tunnel 下 TCP peer 可能是 localhost，因此必须同时验证入口来源、Nginx 配置和 Cloudflare 头，不能把本地伪造头测试当生产证明。

### 23.2 Web 安全基线

- TLS 1.2+；HTTP 自动跳转 HTTPS；HSTS 在确认全部子域 HTTPS 后启用。
- Cookie：`__Host-` 前缀、Secure、HttpOnly、SameSite=Lax、Path=/，不设置 Domain。
- 不安全方法要求 CSRF header + Session 绑定 token；Origin/Referer 做第二检查。
- CORS 默认同源；Actions/MCP 不使用浏览器 CORS 放宽后台接口。
- CSP 至少：`default-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'`，逐步移除 unsafe-inline。
- `X-Content-Type-Options: nosniff`、`Referrer-Policy: no-referrer`、严格 Permissions-Policy。
- 用户输入按上下文输出编码；Markdown 使用白名单 sanitizer；禁止 `dangerouslySetInnerHTML` 渲染未净化内容。
- 上传按 MIME、magic bytes、大小和扩展名多重检查；随机对象名；不在 Web 根目录执行。

### 23.3 认证与凭证

- 密码最少 12 字符，允许长密码和密码管理器，不设置削弱强度的复杂组合规则。
- Argon2id 参数通过基准确定，目标认证耗时 100–300ms；参数和版本随 hash 保存。
- 登录按 IP、账号摘要、设备和全局多层限流；采用渐进延迟，不以永久锁定造成 DoS。
- MFA 恢复码只保存 hash，一次使用；TOTP secret 使用 envelope encryption。
- OIDC 严格校验 issuer、audience、state、nonce、PKCE、签名算法和时钟偏差。
- Session token 每次 refresh 旋转；旧 token 再用触发整个 token family 撤销。
- 后台 session audience 不接受用户层 Cookie；Actions/MCP API Key 不创建浏览器 Session。

### 23.4 授权与对象访问

- 每个 endpoint 在路由注册时声明 permission、scope resolver、audit category 和 risk level。
- service 在加载对象后再次验证对象 organization/workspace，防止 IDOR。
- 对无权对象统一 404 或策略确定的 403，不泄露跨租户对象存在性。
- 下载票据绑定用户、资源、scope、IP 策略和过期；票据本身只保存 hash。
- 后台管理员默认没有 `evidence:read`；读取案件正文需单独权限和敏感读取审计。
- Graph/Canvas Patch 中每个引用对象都验证访问权，防止通过边引用读取其他案件。

### 23.5 Provider、网络和 SSRF

- Provider base URL 来自受控配置，不接受普通用户 URL。
- Webhook/外部 OSINT URL 使用 allowlist、DNS 解析后私网/回环/metadata IP 拒绝、重定向重新校验。
- 出站请求设置连接/响应超时、响应大小上限、重试预算和 circuit breaker。
- Provider 错误日志脱敏 URL query、Authorization、Cookie 和响应秘密字段。
- 生产 MinIO、PostgreSQL、ClickHouse、NATS 管理端口不对公网开放。

### 23.6 密钥和加密

- Secret 存环境注入或外部 Secret Store；数据库只保存 secret reference 或 envelope ciphertext。
- Key/API Secret 创建时明文只返回一次；之后只显示 prefix、digest、last_used_at。
- 建立 90 天轮换能力、双 key overlap 和撤销流程；轮换不要求停机。
- 备份加密并与数据密钥分离；恢复演练验证密钥可用性。
- 日志和审计中的地址/哈希不自动视为秘密，但按案件密级限制读取和导出。

### 23.7 威胁与控制矩阵

| 威胁 | 场景 | 主要控制 | 必测 |
|---|---|---|---|
| 身份伪造 | 客户端提交 `X-User-Role=owner` | Nginx 删除头、服务端 Session | 是 |
| Session 窃取 | XSS 读取 token | HttpOnly Cookie、CSP、输出编码 | 是 |
| CSRF | 第三方页面取消任务 | CSRF token、Origin 检查、SameSite | 是 |
| IDOR | 猜测其他案件 UUID | scope repository、统一对象授权 | 是 |
| 权限提升 | 自己赋予 Owner | 权限矩阵、最后 Owner 约束、step-up | 是 |
| 审计篡改 | 管理员删除不利记录 | append-only 权限、hash chain、对象锁 | 是 |
| 登录枚举 | 依据错误/耗时判断账号 | 统一响应、近似耗时、内部原因码 | 是 |
| 暴力尝试 | 多 IP 猜密码 | 多维限流、MFA、风险告警 | 是 |
| SSRF | OSINT/Webhook 访问 metadata | URL allowlist、IP 检查、重定向复验 | 是 |
| CSV 注入 | 导出字段以 `=` 开头 | CSV 安全转义/前缀策略、原值保留说明 | 是 |
| ZIP 炸弹 | 上传或导入恶意压缩包 | 大小/条目/压缩比限制、隔离解析 | 是 |
| 资源耗尽 | 巨大查询/图谱/导出 | 查询预算、异步任务、并发/内存限制 | 是 |
| Provider 成本攻击 | 反复 refresh 全历史 | Coverage 默认复用、预算、审批、限流 | 是 |
| 供应链攻击 | 恶意 npm/go 依赖 | lockfile、依赖扫描、SBOM、签名构建 | 是 |

### 23.8 安全发布门

- P0/P1 漏洞为 0。
- 所有管理 endpoint 权限声明覆盖率 100%。
- 跨组织对象访问负向用例覆盖所有资源类型。
- 日志秘密扫描 0 命中；构建产物不含 secret。
- Session 撤销在 5 秒内生效；角色停用后的旧请求不能继续写入。
- 审计 hash chain 和每日归档校验通过。
- 依赖漏洞、SBOM、容器镜像和 IaC 检查进入 CI。

## 24. 异步任务、事件与实时更新

### 24.1 事件统一信封

所有 NATS/Outbox 事件使用版本化信封：

```json
{
  "event_id": "uuid",
  "event_type": "investigation.status.changed",
  "event_version": "1.0",
  "occurred_at": "2026-09-12T06:30:00.123Z",
  "producer": "api",
  "organization_id": "uuid",
  "workspace_id": "uuid",
  "actor": {"type": "user", "id": "uuid"},
  "request_id": "req_...",
  "trace_id": "tr_...",
  "resource": {"type": "investigation", "id": "uuid", "revision": 7},
  "data": {},
  "data_classification": "internal"
}
```

- `event_id` 同时作为 JetStream message ID，消费者以它去重。
- 事件 data 只放下游需要的最小字段；Evidence 正文、密钥和 Token 不进入消息。
- 事件 schema 在 registry 注册；破坏性变化发布新 major version 和新 subject。
- 消费失败进入有上限的重试和 DLQ，后台可查看但不能直接编辑原消息。

### 24.2 事件主题

| Subject | Producer | Consumer | 用途 |
|---|---|---|---|
| `events.auth.login.v1` | IAM | risk/audit/notification | 登录结果与风险 |
| `events.auth.session.revoked.v1` | IAM/Admin | session cache/audit | 撤销会话 |
| `events.policy.binding.changed.v1` | Policy | session cache/audit | 权限变更和 authz_version |
| `events.case.changed.v1` | Casework | activity/search/audit | 案件变化 |
| `events.investigation.status.v1` | Investigation | UI notification/report | 调查状态 |
| `events.finding.review.v1` | Investigation | inbox/report/audit | Finding 复核 |
| `events.evidence.verified.v1` | Investigation | report/search/audit | 证据验证 |
| `events.job.progress.v1` | Worker | SSE/metrics | 限频后的任务进度 |
| `events.job.terminal.v1` | Worker | investigation/notification/audit | 任务终态 |
| `events.export.status.v1` | Export worker | notification/audit | 导出状态 |
| `events.provider.health.v1` | Provider monitor | alert/control | Provider 健康 |
| `events.alert.lifecycle.v1` | Alert engine | notification/audit | 告警生命周期 |
| `events.config.changed.v1` | Config center | services/audit | 配置 revision |
| `events.audit.anchor.v1` | Audit archiver | monitor | 每日审计锚点 |

### 24.3 Outbox 处理

- 业务事务同时写领域表和 outbox；禁止提交后再“尽力发送”。
- forwarder 使用 `FOR UPDATE SKIP LOCKED`、租约、指数退避和最大重试。
- publish 成功记录 published_at；重复 publish 由 message ID 去重。
- outbox 积压数量/最老年龄进入 SLO；积压超过阈值触发告警。
- 消费者写入使用 event ID 唯一约束，保证至少一次投递下的幂等。

### 24.4 SSE 实时协议

用户层使用 SSE，不新增 WebSocket 依赖：

```text
GET /api/v2/events?topics=jobs,investigations,notifications
Accept: text/event-stream
Last-Event-ID: <event-id>

id: <event-id>
event: job.progress
data: {"resource_id":"...","revision":7,"progress":"0.42","server_time":"..."}
```

- 建连前校验 topic 和资源 scope；服务端不能向一个连接广播未授权组织事件。
- 15 秒 heartbeat；客户端指数重连并携带 Last-Event-ID。
- 进度事件每对象每秒最多 2 条，终态和风险事件不丢弃。
- 收到未知 major event version 时忽略 data、记录前端错误并回源刷新对象。
- SSE 断开不改变业务状态；前端每 30 秒做有条件查询作为校正。

## 25. 可观测性、SLO 和容量设计

### 25.1 日志

结构化 JSON 字段：`timestamp, level, service, environment, build_sha, request_id, trace_id, actor_id, organization_id, workspace_id, route_template, status, duration_ms, error_code`。

- actor/email/IP 根据日志类型脱敏；普通应用日志不复制完整请求/响应 body。
- API/Worker/MCP 日志使用统一字段；Provider 日志增加 provider/method/attempt/circuit state。
- 前端错误上报包含 build SHA、route template、组件边界、Trace ID，不包含页面证据正文。

### 25.2 指标

| 域 | 指标 |
|---|---|
| API | request rate、error rate、P50/P95/P99、inflight、429、401/403 |
| IAM | login success/failure、MFA failure、new device、session count/revoke latency |
| Policy | allow/deny、decision latency、cache hit、missing declaration |
| Download | wall-clock rows/s、provider rows/s、Shard inflight、ready queue、retry、checkpoint age |
| Pipeline | spool depth/capacity、queue wait P95、ClickHouse lag、memory、backpressure state |
| Provider | calls、error type、P95、credits、cost、circuit state、quota remaining |
| Storage | PG connections/size、CH parts/merge lag、MinIO bytes/objects、NATS lag |
| Audit | insert failure、outbox lag、hash verification、archive success、export duration |
| Frontend | LCP、INP、CLS、route error、API error、SSE reconnect |

吞吐指标必须分开：Provider fetch、Fetch+Spool、Full Pipeline、完整 Job wall-clock、导出生成、内网下载、公网下载。不得用短时探针吞吐冒充持续端到端速度。

### 25.3 初始 SLO

| SLO | 目标 | 窗口 |
|---|---|---|
| 用户 API 可用性 | 99.9% | 30 天 |
| 管理只读 API 可用性 | 99.9% | 30 天 |
| 登录成功请求 P95 | <800ms（不含外部 IdP） | 1 小时 |
| 常规查询 P95 | <2s | 1 小时 |
| 后台列表 P95 | <1.5s | 1 小时 |
| 审计落库 | 99.99% <2s | 1 小时 |
| Session/权限撤销生效 | <5s | 每次 |
| 终态通知 | <10s | 每次 |
| Outbox 最老未发布 | <30s | 持续 |
| RPO | <=15 分钟 | 灾难 |
| RTO | <=4 小时 | 灾难 |

下载速度是独立业务 SLI，按链、Provider、地址数据量和 Dataset 报告，不承诺脱离真实输入条件的单一全局数字。

### 25.4 告警级别

- P0：数据跨租户泄露、审计无法写入且写操作仍继续、密钥泄露、数据不可恢复、全局不可用。
- P1：登录大面积失败、权限撤销不生效、Provider 全不可用、任务进度不前进、导出数据损坏。
- P2：单模块降级、SLO 违约、延迟升高、有 fallback 的 Provider 故障。
- P3：容量趋势、非关键 enrichment 失败、低风险 UI 错误。

P0 时安全优先：若关键写操作无法记录审计，默认 fail closed；健康探针和紧急恢复接口使用独立受控路径。

## 26. 后续功能扩展设计

### 26.1 Chain Adapter

扩展新链必须实现：

```go
type ChainAdapter interface {
    Key() string
    NormalizeAddress(string) (string, error)
    ValidateAddress(string) error
    ResolveHead(context.Context) (Head, error)
    Capabilities() []CapabilityRef
    NormalizeTransaction(RawRecord) ([]NormalizedRecord, error)
    NativeAsset() AssetRef
}
```

并提交：registry 条目、地址测试向量、Dataset 支持矩阵、finality 规则、区块/时间转换、Explorer URL、图标、UI 文案、真实数据验收。前端不通过 `if chain === ...` 大量分支扩链，读取 chain registry descriptor。

### 26.2 Provider Adapter

Provider 扩展必须声明：支持链/Dataset/范围/分页方式、限额单位、成本模型、重试分类、熔断、并发上限、数据来源和完整性边界。

```go
type ProviderAdapter interface {
    Descriptor() ProviderDescriptor
    Fetch(context.Context, FetchRequest) (Page, Usage, error)
    Health(context.Context) HealthResult
    ClassifyError(error) ProviderError
}
```

路由器依赖 capability descriptor，不直接引用 Ankr 特有类型。Provider 凭证通过 secret reference 注入，管理 UI 由 descriptor 决定可编辑字段。

### 26.3 Analysis Capability 与 Projection

每个算法发布：`capability_key`、semantic version、input schema、output schema、预算类型、确定性声明、evidence boundary、algorithm version、支持链、依赖数据和 rebuild 策略。

- 同一 Investigation 保存精确 capability version。
- 新版本并行写新 projection，不覆盖旧结果。
- rebuild 不重新下载原始数据，除非 data requirement 明确缺口。
- Finding 必须包含算法、版本、数据版本、自然属性（CONFIRMED/INFERRED/HEURISTIC）和 Evidence refs。

推荐扩展顺序：

1. 金额约束多跳、环路、Peel Chain。
2. 版本化标签和实体聚类。
3. 历史价格、成本基础、FIFO/LIFO/比例法。
4. Finding-Evidence 通用钻取、重放和 diff。
5. DEX/Bridge 协议解析和跨链配对。
6. 外部 OSINT Adapter 和人工核验工作台。

### 26.4 Subject Type Registry

现有 ADDRESS、DOMAIN、IP、SOCIAL_ACCOUNT、APK、ORGANIZATION、FILE、CONTRACT、TRANSACTION 由 registry 描述：验证器、标准化、显示组件、搜索字段、敏感等级、允许关系类型。

新增 Subject Type 不修改 Investigation 核心表，只新增 registry entry 和可选 detail projection。关系类型同样版本化，禁止任意自由文本成为正式关系语义。

### 26.5 导出和报告扩展

- Exporter 接口按 format 注册：CSV、XLSX、JSONL、Parquet、ZIP。
- 每种格式必须提供 MIME、扩展名、流式能力、行数验证器、文件验证器。
- 报告 Renderer 按模板 version 注册，输入只接受确定的 artifact snapshot。
- PDF/DOCX/XLSX 加入时必须保留同一 evidence index、hash manifest 和 revision，不另造事实来源。

### 26.6 通知扩展

`NotificationChannel` 接口支持 in-app、email、webhook，后续可加企业 IM。模板使用稳定 template key 和 locale，不把业务文本散落在 consumer。

- Webhook 事件签名、重放保护、投递记录和退避。
- 用户偏好不能关闭 P0 安全通知，但可选择非关键渠道。
- 渠道故障不回滚业务事务，只产生 delivery failure 和告警。

### 26.7 前端模块扩展

每个 feature 导出：route config、required permission、navigation item、query keys、error mapping 和可选 command palette action。AppShell 根据静态模块清单和服务端 permissions 组装导航。

禁止远程下载并执行任意前端插件代码。若未来需要第三方插件，必须另行设计签名、沙箱、权限、版本、审计和供应链验证。

### 26.8 API 与数据兼容

- API major version 不在原位破坏；字段只新增 optional，枚举新增时客户端必须容忍 unknown。
- 删除字段先 deprecated 至少两个发布周期，记录调用量后再移除。
- 数据库 expand → backfill → dual-read/observe → enforce → contract，禁止同一发布删列并改代码。
- 事件消费者支持当前和前一个 major；迁移完成前保留 replay 能力。
- Feature Flag 只控制发布，不用来永久分叉领域逻辑。

### 26.9 三个扩展示例

新增一条链：实现 ChainAdapter → 注册 capability/dataset → migration（如需）→ Worker provider route → API registry → 前端自动出现 chain option → 地址/下载/查询/导出/真实数据测试。

新增一种分析：定义 input/output/evidence schema → Capability Registry → projection table → rebuild worker → Finding mapper → API → 工作台结果组件 → replay/diff 测试。

新增一个 Provider：实现 Adapter → secret/config schema → capability/price/rate descriptor → routing policy → health/usage metrics → 后台详情页 descriptor renderer → canary 和故障切换测试。

## 27. 编号化测试与验收矩阵

### 27.1 身份、会话与设备

| Test ID | 场景 | 预期结果 | 证据 |
|---|---|---|---|
| AUTH-001 | 正确本地账户登录 | 设置 host-only HttpOnly Cookie；记录成功事件 | 响应头、DB login_event、UI |
| AUTH-002 | 错误密码 | 统一错误，不泄露账号存在；记录失败原因内部码 | UI Dialog、DB、日志 |
| AUTH-003 | 不存在账号 | 与 AUTH-002 外观/时序近似 | 响应、时序样本、DB |
| AUTH-004 | MFA 成功/失败 | challenge 单次使用，失败计数 | DB、请求、会话 |
| AUTH-005 | Session refresh 旋转 | 旧 token 再用触发 family revoke | Cookie、DB、审计 |
| AUTH-006 | 用户退出 | 当前 Session 立即失效 | 401、DB revoked_at |
| AUTH-007 | 管理员强制下线 | 5 秒内所有目标请求失效 | API、SSE、审计 |
| AUTH-008 | 新设备登录 | device/login/risk 事件字段完整 | IP/UA/Client Hints/Trace |
| AUTH-009 | 伪造 `X-User-Role` | 不能提权，头被删除 | 请求、403、审计 |
| AUTH-010 | 伪造转发 IP | 不可信头不覆盖 client IP | peer/client IP 记录 |
| AUTH-011 | OIDC state/nonce/PKCE 错误 | 登录拒绝且审计 | 回调响应、日志 |
| AUTH-012 | 开放重定向尝试 | 只跳本站 allowlisted 路径 | Location、审计 |

### 27.2 授权和租户

| Test ID | 场景 | 预期结果 | 证据 |
|---|---|---|---|
| RBAC-001 | Viewer 创建案件 | 403，按钮不可见但直接 API 同样拒绝 | UI、API、审计 |
| RBAC-002 | 猜测其他组织案件 UUID | 404/策略 403，不泄露对象 | API、SQL scope、审计 |
| RBAC-003 | 工作区角色只作用于目标工作区 | 其他工作区拒绝 | 两工作区对照 |
| RBAC-004 | 管理员无 evidence 权限读取正文 | 拒绝并记录敏感访问 | API、审计 |
| RBAC-005 | 移除最后一个 Owner | 业务规则拒绝 | 422、DB、审计 |
| RBAC-006 | 角色修改后旧 Session | authz_version 使旧授权失效 | 5 秒内请求结果 |
| RBAC-007 | Canvas 引用跨案件 Evidence | 保存拒绝 | 422/403、DB |
| RBAC-008 | API Client 访问后台 | 拒绝 audience/scope | 401/403、审计 |

### 27.3 用户层闭环

| Test ID | 场景 | 预期结果 | 证据 |
|---|---|---|---|
| USER-001 | 新建案件 | 页面/API/DB/审计一致 | case ID、Trace |
| USER-002 | 创建调查并 Coverage HIT | 0 Provider 调用、0 新 Shard 可合法完成 | API、provider_usage、DB |
| USER-003 | 创建调查并补齐缺口 | 只下载缺口，保留地址级进度 | Job/Shard/Coverage |
| USER-004 | 暂停/恢复调查 | Checkpoint 保留，不重复成功页 | DB、Worker 日志 |
| USER-005 | Finding 复核 | 状态转换合法，Evidence 引用完整 | UI、DB、审计 |
| USER-006 | 证据版本替代 | 原版本不可变，新版本关联 supersedes | DB、hash |
| USER-007 | Canvas 并发保存 | 第二客户端收到 412 和 diff | API、Dialog |
| USER-008 | 报告审批下载 | artifact 版本绑定，ETag/hash 正确 | 文件、DB、审计 |
| USER-009 | 全局搜索 | 只返回有权对象，类型分组正确 | UI、API、负向数据 |
| USER-010 | 通知跳转 | 对象可访问才跳转，无权时安全失败 | UI、审计 |
| USER-011 | 手机调查查看 | 无溢出，Dialog 可关闭，核心信息可达 | 390px 截图/视频 |
| USER-012 | 键盘完成核心流程 | 焦点顺序、可见、无 trap | Playwright/人工记录 |

### 27.4 管理闭环

| Test ID | 场景 | 预期结果 | 证据 |
|---|---|---|---|
| ADMIN-001 | 创建用户并绑定工作区角色 | 有效权限与 scope 正确 | UI、API、DB |
| ADMIN-002 | 停用用户影响预览 | 展示任务/审批/案件后才允许执行 | preview、Dialog、审计 |
| ADMIN-003 | Provider 连接测试 | 受控请求、Usage、Trace、无 secret 日志 | API、日志、DB |
| ADMIN-004 | Provider 断开有受影响任务 | 显示影响并要求 step-up | Dialog、审批、审计 |
| ADMIN-005 | 性能模式切换 | requested/effective/reason 一致 | UI、runtime、DB |
| ADMIN-006 | Kill Switch 双人审批 | requester 不能 approve；执行可恢复 | approval、runtime、审计 |
| ADMIN-007 | 预算超阈值 | 触发配置动作和告警 | usage、alert、control |
| ADMIN-008 | 审计导出 | 条件、行数、manifest、hash 可校验 | ZIP/JSONL/DB |
| ADMIN-009 | 配置并发修改 | 412，不能静默覆盖 | API、Dialog |
| ADMIN-010 | 功能开关灰度 | 只影响目标 scope，可回滚 | 两用户对照、审计 |

### 27.5 数据、文件和恢复

| Test ID | 场景 | 预期结果 | 证据 |
|---|---|---|---|
| DATA-001 | 金额超 JS 安全整数 | UI/API/导出原值一致 | JSON、页面、文件 |
| DATA-002 | Current Balance 与 Net Flow | 来源和 `is_current_balance` 明确，不混用 | API/UI |
| DATA-003 | CSV/XLSX/JSONL/Parquet | 解析行数、字段、精度、hash 正确 | 解析日志 |
| DATA-004 | ZIP | manifest、条目、hash、无路径穿越 | ZIP 检查 |
| DATA-005 | Range 下载 | HEAD、206、Content-Range、ETag 正确 | curl/文件 hash |
| DATA-006 | Worker Kill-9 | 保留游标和成功页，恢复无幽灵任务 | DB/日志/Provider usage |
| DATA-007 | NATS 重启 | Outbox 补发、消费者幂等 | event IDs、DB |
| DATA-008 | MinIO 对象缺失 | 不宣称完成，产生可操作告警 | API/alert |
| DATA-009 | 备份恢复演练 | 达到 RPO/RTO，hash chain 和对象引用完整 | 演练报告 |
| DATA-010 | Schema migration 中断 | 可重入，不破坏旧服务读取 | migration 日志 |

### 27.6 审计、安全和体验

| Test ID | 场景 | 预期结果 | 证据 |
|---|---|---|---|
| AUDIT-001 | 用户操作清单逐项执行 | 关键事件 100% 记录，可按 Trace 串联 | 对照矩阵 |
| AUDIT-002 | 日志秘密扫描 | password/token/key/cookie 0 明文 | 扫描报告 |
| AUDIT-003 | 修改/删除审计事件 | 应用 DB 角色拒绝 | SQL 结果 |
| AUDIT-004 | Hash chain | 任意篡改可检测，每日锚点一致 | verifier 输出 |
| SEC-001 | CSRF | 跨站写操作拒绝 | 请求/403 |
| SEC-002 | XSS/Markdown | payload 被转义，无执行 | 浏览器/CSP report |
| SEC-003 | SSRF | localhost/private/metadata/redirect 被拒绝 | 请求/审计 |
| SEC-004 | API 限流 | 429、Retry-After、正常用户不受越权共享 bucket 影响 | 压测 |
| UX-001 | Dialog 焦点 | trap、ESC、返回焦点、长内容滚动 | Playwright |
| UX-002 | 四断点布局 | 无非预期横向滚动/遮挡/布局跳动 | 截图 |
| UX-003 | 颜色和状态 | 4.5:1，状态不只靠颜色 | axe/人工 |
| UX-004 | 长表性能 | 10k 数据客户端不渲染 10k DOM 行，交互流畅 | profiler |
| UX-005 | reduced motion | 非必要动画关闭，状态仍可理解 | 浏览器录制 |

### 27.7 证据目录规范

```text
acceptance-evidence/dual-system-v1/
├─ manifest.json
├─ environment.json
├─ tests/<TEST-ID>/
│  ├─ result.json
│  ├─ request.txt
│  ├─ response.json
│  ├─ db-check.txt
│  ├─ runtime-log.txt
│  └─ screenshot.png
├─ files/
├─ security/
├─ performance/
└─ final-report.md
```

每个 result.json：test ID、执行时间、build SHA、环境、前置数据、步骤、expected、actual、PASS/FAIL/BLOCKED、证据路径、缺陷 ID。未执行不得标 PASS；BLOCKED 必须说明外部阻塞和影响。

## 28. Agent 可执行实施顺序

本节是实际施工顺序；如与第 8 节粗粒度排期发生细节冲突，以本节为准。每个 Sprint 都必须交付可运行垂直切片，禁止把所有后端、所有前端、所有测试分别堆到最后。

### 28.1 开工前强制步骤

实施 Agent 首先执行：

1. 完整阅读 `AGENTS.md`、`docs/code-standards.md`、本计划和 `docs/phase0-architecture-baseline.md`。
2. 运行 `git status --short`，记录已有修改和未跟踪文件；这些属于现有工作，禁止 reset、checkout 或覆盖。
3. 生成 `docs/implementation/current-surface-inventory.md`：现有路由、handler、表、迁移、运行服务、测试、部署路径。
4. 运行基线：`go test ./...`、`pnpm run build`、现有 Playwright smoke；失败项单独记录，不能归因于新工作。
5. 保存当前用户关键页面桌面/移动截图和 API 样例，作为迁移对照。
6. 确认生产和本地数据库 migration version；不假设未跟踪迁移已部署。
7. 任何需要改变架构决策的情况先写 ADR，不在代码中静默改变本计划。

### 28.2 现有文件迁移映射

| 当前文件/模块 | 目标 | 动作 |
|---|---|---|
| `frontend/src/App.tsx` | 两个 app router/shell | 拆分，保留旧入口至灰度完成 |
| `frontend/src/styles.css` | design-tokens + ui + feature styles | 提取 token，按 feature 迁移，避免全量机械重写 |
| `InvestigationsPage.tsx` | user `features/investigations` | 拆为 list/new/workbench/findings/evidence |
| `FundFlowPage.tsx` | user `features/canvas` | 保留现有图能力，加入 Evidence Rail 和深链接 |
| `DataExplorerPage.tsx`、`AddressDetailPage.tsx` | user `features/explorer` | 统一搜索、cursor、数据来源和权限 |
| `DownloadPage.tsx` | user `features/downloads` | preview/create 分离，显式复用/refresh |
| `JobsPage.tsx`、`JobDetailPage.tsx` | user jobs + admin diagnostics | 共享 domain，不共享页面权限 |
| `SystemPage.tsx` | user dashboard + admin health | 拆解真实指标，不复制假状态 |
| `features/api-control/*` | admin provider/control/cost/request | 按后台路由拆 feature |
| `frontend/src/api/client.ts` | generated SDK + transport | 逐端点替换，旧 client 作为 compatibility adapter |
| `backend/internal/auth/auth.go` | `iam` + middleware | 移除客户端自报身份，保留临时兼容开关仅供测试环境 |
| `backend/internal/api/server.go` | userv2/adminv1/legacy routers | 路由分区、声明 permission/audit/risk |
| `audit_events` | audit v2 | 双写观察、核对、切读，旧表不删除 |
| `users.role` | membership/role binding | 回填后观察，再停止读取，暂不删列 |

### 28.3 Sprint 1（第 1–2 周）：基线、契约、双应用骨架

目标：不改变现有业务行为的前提下，建立未来所有工作的结构。

任务：

- `S1-ARCH-01`：提交 AD-001 至 AD-015 的 ADR 文件，并确认域名/路径。
- `S1-API-01`：创建 OpenAPI 目录和 common schema；接入 lint/generate/check CI。
- `S1-FE-01`：建立 pnpm workspace、两个 apps 和共享 packages。
- `S1-FE-02`：实现 token、AppShell、PageHeader、Dialog、DataTable、EntityLink、Amount、状态组件。
- `S1-FE-03`：实现 `/login`、`/home`、`/admin` 静态真实布局；数据只能来自 fixture server，不硬编码到组件。
- `S1-BE-01`：将现有 router 包装为 `legacyv1`，创建空的 auth/user/admin router。
- `S1-QA-01`：建立 user/admin/cross-role/visual Playwright project。
- `S1-OPS-01`：构建两个前端镜像和 Nginx 路由，但只部署测试环境。

主要新文件：

```text
frontend/apps/*
frontend/packages/*
openapi/*
backend/internal/api/{userv2,adminv1,middleware}
docs/adr/*
```

验收：两个 app 独立构建；共享包无反向依赖；旧 SPA 功能未回归；Dialog 键盘/移动端通过；OpenAPI 生成无 diff。

### 28.4 Sprint 2（第 3–4 周）：IAM、Session、登录审计

目标：替换默认管理员和可伪造身份头。

任务：

- `S2-DB-01`：实现 0040 IAM 迁移、索引和 seed/migration 工具。
- `S2-BE-01`：实现 local credential、OIDC adapter、Session store、Cookie、CSRF、MFA challenge 接口。
- `S2-BE-02`：实现 trusted proxy IP resolver、device parser、login event recorder。
- `S2-BE-03`：实现 session audience，分别签发 user/admin session。
- `S2-FE-01`：完成登录、MFA、回调、Session 过期、退出、个人安全页面。
- `S2-ADMIN-01`：完成会话列表和登录记录只读页面。
- `S2-SEC-01`：完成 AUTH-001 至 AUTH-012、CSRF 和日志秘密扫描。
- `S2-MIG-01`：默认 admin 映射为 Owner identity；生成一次性初始化流程，不写默认密码。

验收：生产模式不接受 `X-User-*`；成功/失败登录字段完整；Session 撤销 5 秒内生效；前端无 JS 可读 Token。

### 28.5 Sprint 3（第 5–6 周）：RBAC、租户与审计 v2

目标：所有新增和核心现有路径具有统一授权和不可抵赖审计。

任务：

- `S3-DB-01`：实现 0041/0042，seed 系统角色和完整 permission catalog。
- `S3-BE-01`：实现 TenantContext、Policy Engine、route declaration、service 二次授权。
- `S3-BE-02`：实现 audit v2、hash chain、Outbox、每日 archive/verifier。
- `S3-BE-03`：为 cases/investigations/canvases/jobs/exports/reports 补 organization/workspace scope。
- `S3-ADMIN-01`：完成组织、工作区、用户、角色权限、用户详情页面。
- `S3-ADMIN-02`：完成审计列表、详情和登录/会话关联。
- `S3-QA-01`：RBAC-001 至 RBAC-008；所有资源跨租户负向测试。
- `S3-OBS-01`：权限拒绝、审计失败、outbox lag 指标和告警。

验收：管理 endpoint 权限声明覆盖率 100%；审计业务事件可按 Trace 还原；应用账号不能更新/删除审计事件；最后 Owner 约束有效。

### 28.6 Sprint 4（第 7–8 周）：用户案件与调查闭环

> **执行状态（2026-09-12）：已完成。** 交付与验收见
> `docs/implementation/sprint4-report.md`；USER-007（Canvas 并发 diff）与
> USER-009（地址/交易/报告类型搜索）按 S4/S5 边界保留 PARTIAL，不进入 S5 实施。
> 迁移编号因 0044–0046 已被 Sprint 3 使用而顺延为 0047/0048，见 ADR-016。

目标：用户能够从案件创建完成至 Evidence/报告复核。

任务：

- `S4-DB-01`：实现 0044 协作/审批迁移。
- `S4-BE-01`：实现 `/api/v2/cases`、members、subjects、activity、comments。
- `S4-BE-02`：建立 Investigation v2 compatibility service，复用现有 runtime/artifacts。
- `S4-BE-03`：实现 Finding/Evidence review command 和不可变版本规则。
- `S4-BE-04`：扩展 report revision/approval/download ticket。
- `S4-FE-01`：完成案件列表、新建、概览、对象、动态、成员。
- `S4-FE-02`：完成调查列表、新建 preview、workbench、Findings、Evidence Rail。
- `S4-FE-03`：完成报告中心和复核 Inbox。
- `S4-QA-01`：USER-001、004–010、AUDIT-001。

验收：Investigator → Reviewer → approved report 的真实流程通过；每个状态转换、敏感查看和下载都有审计；revision 冲突不覆盖数据。

### 28.7 Sprint 5（第 9–10 周）：Explorer、下载、任务、导出与图谱

> **执行状态（2026-09-12）：已完成。** 交付与逐项证据见
> `docs/implementation/sprint5-report.md`：API E2E PASS 104 / FAIL 0 / NOT_VERIFIED 0
> （build_sha=244f63b），Playwright 适用 14 PASS / 0 FAIL，S1–S4 回归全绿；
> 迁移编号 0049/0050；RBAC-004/007、USER-012、UX-004 已在本轮闭合。

目标：把现有核心数据能力完整迁入用户系统，同时提升后台诊断能力。

任务：

- `S5-BE-01`：实现 Explorer v2 cursor/来源/口径 envelope，兼容现有 query。
- `S5-BE-02`：实现 download preview/create、默认 reuse、显式 refresh、用户 scope。
- `S5-BE-03`：实现 jobs v2、SSE Last-Event-ID、exports v2/download tickets。
- `S5-FE-01`：完成全局搜索、Explorer、地址详情、交易详情。
- `S5-FE-02`：完成下载表单、任务列表/详情、地址/Dataset/Shard 进度。
- `S5-FE-03`：迁移 Fund Flow，加入 Evidence Rail、Canvas revision 和移动端查看模式。
- `S5-ADMIN-01`：完成全局任务列表和固定诊断链。
- `S5-QA-01`：USER-002/003/007/011/012、DATA-001 至 DATA-008、UX-001 至 UX-005。

验收：Coverage HIT 不发 Provider 请求；增量任务只补缺口；暂停/恢复保留成功游标；所有导出格式解析和 hash 通过；桌面/移动真实交互通过。

### 28.8 Sprint 6（第 11–12 周）：后台控制、治理和可观测性

目标：平台管理员可以安全地控制、诊断和恢复系统。

任务：

- `S6-DB-01`：实现 0045/0046 配置、审批、保留和备份元数据。
- `S6-BE-01`：迁移 API Control 为 `/api/admin/v1`，统一权限、preview/command、审计。
- `S6-BE-02`：实现 Provider descriptor、routing policy、key rotation、预算和成本 API。
- `S6-BE-03`：实现 alert lifecycle、feature flags、configuration revisions。
- `S6-BE-04`：实现 retention/deletion/backup/restore preview 和审批编排；实际删除仍保持关闭直到专项验收。
- `S6-FE-01`：完成 Provider、路由、性能、成本、预算、请求审计。
- `S6-FE-02`：完成告警、控制面、Kill Switch、配置、功能开关、备份恢复页面。
- `S6-OBS-01`：实现第 25 节 dashboard、SLO 和 alerts。
- `S6-QA-01`：ADMIN-001 至 ADMIN-010、AUDIT-002 至 AUDIT-004。

验收：管理员能从告警定位到请求/Provider/Shard/Checkpoint/Object；Kill Switch 双人审批和恢复通过；审计员无法写；所有 secret 只显示摘要。

### 28.9 Sprint 7（第 13–14 周）：硬化、真实数据验收和切流

目标：完成生产级安全、性能、恢复和迁移证明。

任务：

- `S7-DB-01`：0047 scope 回填/约束、0048 索引/分区；输出核对报告。
- `S7-SEC-01`：完成威胁矩阵、依赖扫描、SBOM、镜像/IaC 检查、渗透负向用例。
- `S7-PERF-01`：API、长表、图谱、百万行导出、Provider/Full Pipeline、内网/公网分层测试。
- `S7-DR-01`：Worker/API/NATS Kill-9、PG/CH/MinIO 备份恢复演练。
- `S7-QA-01`：执行第 27 节全部测试，生成证据目录和 final report。
- `S7-RELEASE-01`：灰度 `/app-v2`、`/admin`，观察 Policy 差异和 audit coverage。
- `S7-RELEASE-02`：切换用户入口，旧页面进入只读观察；确认无双写后下线。
- `S7-DOC-01`：管理员手册、用户手册、权限说明、运行手册、故障处置和回滚手册。

验收：P0=0、P1=0；真实 UI/API/数据库/文件/运行证据齐全；备份恢复达 RPO/RTO；新旧功能对照无未解释差异。

### 28.10 每个任务的完成输出

Agent 完成任何任务时必须提供：

```text
Task ID:
Goal:
Files changed:
API contracts changed:
Migrations added/applied:
Permissions added:
Audit events added:
Tests run and exact results:
Manual UI evidence:
Data/file/runtime evidence:
Security considerations:
Rollback procedure:
Known limitations / follow-ups:
```

禁止使用“应该可以”“理论上完成”“接口返回 200”作为完成陈述。

## 29. CI/CD 和环境设计

### 29.1 Pipeline

```text
lint
 -> unit
 -> OpenAPI lint/generate/diff
 -> migration static + disposable DB test
 -> backend integration
 -> frontend build
 -> component/a11y
 -> Playwright user/admin/cross-role
 -> security/secret/dependency/SBOM
 -> container build + image scan
 -> deploy test
 -> smoke + real integration subset
 -> signed artifact manifest
 -> manual production approval
```

- 每个镜像标记 Git SHA，不使用可变 `latest` 作为发布证据。
- Migration 作为独立一次性 Job，API/Worker 启动不隐式执行破坏性迁移。
- 部署采用 expand-compatible 版本；新 API 必须能在旧 Worker 短期共存。
- 前端、API、Worker、MCP 的版本和兼容矩阵显示在后台版本页。

### 29.2 环境

| 环境 | 数据 | Provider | 用途 |
|---|---|---|---|
| local | fixtures/隔离容器 | mock 默认 | 开发和单元/集成 |
| test | 合成+脱敏样本 | mock/受控 live | CI/E2E/安全 |
| staging | 生产拓扑缩小版 | live canary | 性能、迁移、恢复、发布验收 |
| production | 真实 | live | 正式服务 |

禁止把生产完整案件复制到 test。需要复现时生成最小脱敏 fixture，并保留来源和转换审计。

### 29.3 Feature Flag 发布

- Flag key 示例：`user_v2_cases`、`admin_v1_iam`、`audit_v2_read`。
- 顺序：开发者/测试用户 → 指定工作区 → 10% → 50% → 100%。
- 每步观察错误率、权限拒绝差异、审计完整性和用户任务成功率。
- Flag 回滚只切换读/入口；数据库新数据保留，禁止通过回滚删除。

## 30. 性能和容量预算

### 30.1 前端预算

- 用户/后台首屏 gzip JS 各自目标 <350KB；图谱、ECharts、AG Grid/XLSX 必须按路由 lazy load。
- 路由切换保留可交互时间 P75 <1s（已缓存 API 的内网环境）。
- DataTable >100 行必须虚拟化；Canvas 大于 2,000 节点启用 LOD/聚合或拒绝并提示预算。
- 全局搜索 250ms debounce、取消旧请求；不得每次按键并发请求。
- React Query key 包含 organization/workspace/object/filter；切换工作区必须清除不安全缓存。

### 30.2 后端预算

- 常规 API response body 默认 <1MB；大结果必须分页或导出。
- PostgreSQL API 查询 P95 <200ms（不含网络）；慢查询阈值 500ms。
- ClickHouse 交互查询默认最大扫描 bytes/rows/时间，超限转异步任务。
- Audit 写入不得在正常情况下增加业务写 P95 超过 20%；失败时按风险策略 fail closed。
- SSE 每实例连接容量和事件速率通过压测确定，至少支持计划并发用户的 2 倍。
- Outbox、notification、audit archive 使用有界 worker pool 和队列，禁止无界 goroutine。

### 30.3 存储容量

- 按 organization/workspace/dataset 统计每日新增 rows/bytes 和 30/90/365 天预测。
- Login/Audit 分区建立前估算事件数：用户请求量 × 审计采样/完整记录率。
- MinIO lifecycle 仅在已批准 retention policy 下执行；Raw、Evidence、Report、Audit 使用不同 bucket/prefix policy。
- PostgreSQL 分区归档后验证查询和审计导出仍能跨在线/归档范围工作。

## 31. 最终交付物清单

代码：

- 两套可独立部署的前端应用及共享包。
- IAM、Policy、Tenancy、Audit、Notification、Config/Governance 后端模块。
- `/api/auth/v1`、`/api/v2`、`/api/admin/v1` 和兼容 `/api/v1`。
- 0040–0048 增量迁移、回填和校验脚本。
- Nginx/systemd/compose/monitoring 配置和回滚脚本。

文档：

- ADR、OpenAPI、权限目录、事件目录、数据字典。
- 用户手册、管理员手册、审计员手册。
- 部署、升级、回滚、密钥轮换、备份恢复、事故处置 Runbook。
- 已实现/部分实现/未实现能力边界表。

证据：

- 全部 Test ID 结果、截图、API、DB、日志、文件和运行证据。
- 权限覆盖报告、审计覆盖报告、秘密扫描、SBOM 和漏洞报告。
- 性能分层结果、容量预测、RPO/RTO 演练。
- `RELEASE-DECISION.md`：版本、范围、未解决 P2、批准人和回滚点。

## 32. 接手 Agent 的最终约束

1. 严格按 Sprint 依赖执行，先认证授权审计，再开放后台写能力。
2. 每开始一个页面，先确认本节路由、permission、API、状态、响应式和审计事件。
3. 每开始一个 API，先补 OpenAPI、Policy declaration、tenant scope、audit category、幂等/并发规则和测试。
4. 每新增表先写数据所有者、保留期、敏感级别、索引和迁移/回滚方案。
5. 所有 UI 修改必须真实浏览器验证桌面和移动；管理复杂操作至少验证桌面和平板。
6. 所有数据功能必须核对 API、PostgreSQL/ClickHouse、MinIO 文件和运行日志的语义一致性。
7. 保留当前工作区已有修改，不得用 reset/checkout 覆盖他人工作。
8. 任何超出本计划的权限放宽、数据删除、生产网络变更或 secret 操作必须单独取得授权。
9. 发现计划与实际代码冲突时，先提供证据并更新 ADR/计划，不凭直觉绕过约束。
10. 未验证功能标记 `NOT_VERIFIED`；未实现标记 `NOT_IMPLEMENTED`；部分能力标记 `PARTIAL`，不得用页面存在或 HTTP 200 表述为完成。

## 33. 需求追踪矩阵

| Epic | 页面 | API | 迁移/存储 | 权限/审计 | 测试 | Sprint |
|---|---|---|---|---|---|---|
| IAM 登录 | U-AUTH-01/02/03、U-SEC-01 | auth v1 | 0040 identities/sessions/login_events/devices | auth/login/session events | AUTH-001–012 | S2 |
| RBAC/租户 | A-ORG/A-WS/A-USER/A-ROLE | admin org/user/role APIs | 0041 memberships/roles/bindings | admin:* + policy decisions | RBAC-001–008 | S3 |
| 审计链 | A-AUDIT/A-LOGIN/A-RISK | admin audit/login/risk APIs | 0042 audit v2/hash/outbox/MinIO | append-only、sensitive read | AUDIT-001–004 | S3/S7 |
| 案件 | U-CASE-01–08 | user v2 cases/members/subjects/activity | 0044 case members/comments | case/subject permissions | USER-001/009/010 | S4 |
| 调查 | U-INV-01–05 | investigations/runtime/findings/evidence | 复用 0028–0035 + 0044 review | investigation/finding/evidence | USER-002–006 | S4/S5 |
| Canvas/图谱 | U-GRAPH-01 | canvases/branches/snapshots | 复用 0030 | canvas permissions + sensitive refs | USER-007/011/012 | S5 |
| Explorer | U-EXP-01–03 | explorer address/transaction APIs | ClickHouse 现有表/投影 | explorer read/provider fetch | DATA-001/002、USER-009 | S5 |
| 下载任务 | U-DL/U-JOB、A-JOB | downloads/jobs/events/diagnostics | 现有 Job/Checkpoint/Coverage | download/job/admin job | USER-002–004、DATA-006/007 | S5 |
| 导出/报告 | U-EXPORT/U-REPORT、A-EXPORT | exports/reports/tickets | 现有 exports/reports + 0044 approvals | export/report/audit download | USER-008、DATA-003–005 | S4/S5 |
| 通知/Inbox | U-INBOX/U-NOTIFY | notifications/read/reviews | 0043 | notification/inbox + delivery events | USER-010 | S4 |
| Provider/路由 | A-PROVIDER/A-ROUTING/A-PERF | admin provider/routing/performance | 现有 API Control + 0045 revisions | provider/routing/performance | ADMIN-003–005 | S6 |
| Key/成本/预算 | A-KEY/A-COST/A-BUDGET/A-REQ | keys/costs/budgets/requests | 现有 key/usage/budget + 0045 | key/cost/budget/request | ADMIN-007、AUDIT-002 | S6 |
| 告警/控制 | A-ALERT/A-CONTROL/A-KILL | alert/control/kill-switch | 现有 control + 0045 approvals | alert/control/kill switch | ADMIN-006/007 | S6 |
| 配置/Flag | A-CONFIG/A-FLAG | settings/feature flags | 0045 | setting/feature flag | ADMIN-009/010 | S6 |
| 数据治理 | A-DATA-01–04 | coverage/quality/storage/retention | 0046 + 0047/0048 | data quality/retention/deletion | DATA-008–010 | S6/S7 |
| 备份恢复 | A-BACKUP/A-VERSION | backup/restore/version | 0046 backup metadata | backup/restore/system | DATA-009/010 | S6/S7 |
| 全系统体验 | 两套系统所有页面 | 全部 | - | - | UX-001–005 | 每个 Sprint |

任何 Epic 合并前，矩阵对应的页面、API、迁移、权限/审计和测试至少形成一条完整垂直路径。缺少任意一列时只能标记 `PARTIAL`。
