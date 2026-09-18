# 用户端重构实施记录

对应规范：`docs/user-console-frontend-redesign-blueprint.md`
配套材料：`docs/implementation/user-console-redesign-baseline.md`（U0 契约基线）、
`docs/implementation/user-console-redesign-page-brief.md`（页面施工契约）

---

## 阶段 U0：冻结基线

**完成内容**

- 读取 `AGENTS.md`、`docs/code-standards.md` 与蓝图全文。
- `git status --short` 开工时为空（工作区干净），未覆盖任何未提交修改。
- 记录用户端全部 28 条路由、41 个权限名、50+ 个接口、7 个 `data-testid`、
  26 份 Playwright 规格所依赖的选择器与文案 → 见基线文档。
- 记录开工构建基线（见下）。
- **截图：NOT_VERIFIED**。本环境没有可用后端与视觉通道，四断点截图无法产出；
  改用可复现的**渲染度量脚本**产出等价的机器可读证据（见 U6）。

**修改文件**：`docs/implementation/user-console-redesign-baseline.md`（新增）

**运行测试及准确结果**

```text
npx tsc -p apps/user-console/tsconfig.json --noEmit   → exit 0
npx vite build --config apps/user-console/vite.config.ts → exit 0
  首屏 gzip JS = 26.70 KB (index) + 74.87 KB (react-vendor) ≈ 101.6 KB
```

---

## 阶段 U1：设计令牌与基础组件

**完成内容**

### 令牌（`packages/design-tokens/tokens.css`）

- **纯新增**语义层：中性色阶（ink/graphite/slate/canvas）、品牌与信号色
  （indigo/cyan/amber/green/red）、语义角色（bg-*/text-*/border-*/interactive/state-*）、
  图谱节点色、8 条图表序列色、四档动效时长 + 四种缓动、五档层级、组件尺寸。
- 既有 `--c-*` 变量名与取值**一个都没改**：`apps/admin-console` 与用户端共用本文件，
  直接改 `:root` 会让后台被动改变（蓝图第 16 行）。
- 用户端配色重映射放在 `apps/user-console/src/styles/user-tokens.css` 的
  `html[data-app="user"]` 作用域里，由 `main.tsx` 写入 `document.documentElement.dataset.app = "user"`。
- `prefers-reduced-motion: reduce` 下把四档动效令牌与 stagger 步长压到 0。

### 对比度守卫（`scripts/check-token-contrast.mjs`）

- 解析器扩展为同时收集 `--c-*` 与新增色阶令牌（只收直接写成十六进制的原始色，
  语义角色层用 `var()` 间接引用、统一回到原始色校验）。
- **新增 16 组前景/背景组合**，覆盖蓝图 §6.1 表格里的全部配色。
- 结果：`node scripts/check-token-contrast.mjs` → **27/27 通过，exit 0**。

### 用户端样式层（新增 `apps/user-console/src/styles/`）

| 文件 | 职责 |
|---|---|
| `user-tokens.css` | 用户端作用域配色重映射 + 兼容层 `--c-primary` |
| `components.css` | A 段：`html[data-app="user"]` 下覆盖共享类（card/btn/table/badge/input/metric/state/dialog/evidence-rail/auth-aside）；B 段：新增 `.cs-*` 组件类 |
| `user-shell.css` | `.cs-shell__*` 侧栏 / 顶栏 / 移动抽屉 / 命令面板入口 / 用户菜单 |
| `motion.css` | 四档动效、页面进入、stagger（只对前 8 项）、indeterminate 进度、页面隐藏时暂停 |
| `responsive.css` | 四断点策略（含 1920/2560 宽屏密度）、溢出防线、触摸目标、safe area |

### 共享组件（`packages/ui/`）

新增模块：`progress.tsx`、`layout.tsx`、`entity.tsx`、`navigation.tsx`、`forms.tsx`、`data.tsx`。

- **进度系统（§9 全套）**：`LinearProgress`（`percent={null}` 即 indeterminate）、
  `CompactProgress`、`ProgressRing`、`PipelineProgress`、`HierarchicalProgress`、
  `StreamingRate`、`ParseProgressBody`、`ProgressGlyph`、`progressStateFor`、
  `PROGRESS_STATE_LABEL`、`useThrottledValue`。
  全部带 `role="progressbar"` + `aria-valuemin/max/now`（indeterminate 时给 `aria-valuetext`）。
- **布局（§8.1）**：`PageSection`、`SplitPane`（可拖动 + 键盘调整宽度）、
  `InspectorDrawer`、`StickyActionBar`、`BentoGrid`/`BentoCell`、`PageSkeleton`、
  `useHorizontalOverflowHint`、`cx`。
- **对象展示（§8.3）**：`CopyButton`、`HashValue`（`title` 恒为完整值）、`KeyValueList`、
  `Timeline`、`MetaLine`、`EntityOpenLink`。
- **导航筛选（§8.1/§8.2/§8.4）**：`Tabs`（roving tabindex + 左右/Home/End）、`TabPanel`、
  `SegmentedControl`、`FilterBar`、`FilterChip`、`SortMenu`、`Pagination`、`Stepper`、
  `BulkActionBar`、`RowCheckbox`、`SelectAllCheckbox`、`Switch`、`Checkbox`、`RadioGroup`。
- **基础交互（§8.2）**：`Button`（5 种 variant + busy/busyLabel）、`IconButton`（label 必填 →
  aria-label + tooltip）、`SearchInput`、`PasswordInput`、`Combobox`、`MultiSelect`、
  `DateTimeRange`、`BlockRange`、`FileDropzone`、`AddressInputList`、`ChainSelector`、
  `DatasetSelector`。
- **数据展示补充（§8.3）**：`TreeTable`（失败路径默认展开）、`ChartPanel`（强制提供文字摘要
  或数据表入口 + 计算说明走 Dialog）、`LoadMore`（cursor 加载更多）、`StatTiles`。

### 现有组件的兼容升级（§8.5 矩阵逐项核对）

| 现有导出 | 处理 | 向后兼容 |
|---|---|---|
| `AppShell` | 新增 `variant="user"` 与若干可选插槽 | 默认 variant="admin"，类名与行为不变 |
| `GlobalSearchBox` | 保留 | 未改 |
| `BackLink` | 统一按钮样式 | 签名不变 |
| `RoleScopeNote` | 保留 | 未改 |
| `PageHeader` | +`breadcrumb/status/meta/primaryAction/secondaryAction` | 原 `title/subtitle/actions` 语义不变 |
| `Card` | +`variant`（default/interactive/selected/flat） | 原 `title/actions/flush` 不变 |
| `Metric` | 内部委托给新的 `MetricCard` | 原参数不变 |
| `Field` | +`required/description` | 原 `label/hint/htmlFor/error` 不变 |
| `StatusBadge`/`statusToneFor` | 保留形状 glyph，补充状态词 | 映射表只增不改 |
| `EntityLink` | 保留完整值与复制 | 未改语义 |
| `Amount` | +`fiat`（空显示 `—`）、`direction` | 原精度处理逐字保留 |
| `EmptyState`/`ErrorState`/`NoPermissionState` | 保留 action 插槽与 fail-closed | 未改语义 |
| `SkeletonRows` | +`widths`（保持列宽） | 原两参数继续工作 |
| `EvidenceRail` | +`selectable/selectedKey/onSelect/collapsible/actions` | 原 `items/title` 不变 |
| `DataTable` | +吸顶表头、行激活、批量选择、冻结首列、`data-testid` 保留 | 原 render API 与虚拟滚动不变 |
| `RelativeTime` | 保留完整 ISO `title` | 未改 |
| `icons` | 保留兼容导出 | 未改 |
| `DialogProvider`/`useDialog` | 保留唯一提示通道与队列 | API 未改 |
| `dialogInputValue`/`dialogFormValues` | 保留返回编码 | 未改 |
| `QrCode` | 保留 | 未改 |
| `TopToast` | 保留兼容层，**用户端不再新增调用** | 组件本身未改 |
| `Dialog` | 类型标记改为"图标 + 文字"双节点，由各端 CSS 择一显示 | 用户端显示图标；后台默认仍显示原文字徽标 |

**运行测试及准确结果**

```text
node scripts/check-token-contrast.mjs                      → 27/27 通过，exit 0
npx tsc -p packages/ui/tsconfig.json --noEmit              → exit 0
npx tsc -p packages/design-tokens/tsconfig.json --noEmit   → exit 0
npx tsc -p apps/user-console/tsconfig.json --noEmit        → exit 0
npx tsc -p apps/admin-console/tsconfig.json --noEmit       → exit 0（共享包改动未破坏后台）
npx vite build --config apps/admin-console/vite.config.ts  → exit 0
```

**完成条件核对（蓝图 §18 U1）**：组件测试与四断点/reduced-motion 的**页面级**验证在 U6 统一执行；
本阶段以类型检查、构建与令牌对比度守卫作为准入条件，均已通过。旧页面在 U1 结束时仍可正常工作
（共享类覆盖只在 `html[data-app="user"]` 下生效）。

---

## 阶段 U2：Shell、登录和首页

**完成内容**

### AppShell user variant（`packages/ui/AppShell.tsx`）

- 桌面展开 264px / 收起 72px，宽度由 `grid-template-columns` 过渡驱动，图标位置保持连续。
- 当前导航项：3px 左侧指示条 + 浅色背景 + 高对比文字（三通道，不只靠颜色）；
  收起态保留指示条，并为每个入口补 `visually-hidden` 文字，保证可访问名称不丢。
- 顶栏 56px：命令面板入口、工作区切换、通知、用户菜单；滚动后加轻阴影与模糊。
- `<900px` 抽屉导航：遮罩覆盖工作区；打开时焦点进入抽屉，Escape 关闭，
  关闭后焦点回到菜单按钮；**路由跳转后自动关闭**；工作区切换移入用户菜单。
- 语义不变：`navGroups`、`user`、`onLogout`、`headerExtra` 含义与调用方式均未改。

### 应用层（新增 `apps/user-console/src/app/`、`src/components/`）

- `app/navigation.tsx`：从 `App.tsx` 抽出导航与 `ROUTE_PERMISSIONS`，
  精确/最长前缀匹配与 fail-closed 规则**逐字保留**；新增 `pageTitleFor`。
- `app/UserShell.tsx`：组合 Shell、通知 Provider、命令面板、最近访问登记、
  路由驱动 `document.title`、页面隐藏时暂停装饰动画（不停业务订阅）。
- `components/WorkspaceSwitcher.tsx`：保留 `data-testid="workspace-switcher"` 与
  `data-audience="user"`；新增 `variant="menu"` 供移动端用户菜单使用。
- `components/notifications/`：`NotificationsProvider` 让铃铛与 `/notifications` **共享同一份数据**；
  `NotificationPopover` 显示未读数（最大 `99+`）、最近 8 条、查看全部、Escape 关闭并归还焦点。
- `components/command/CommandPalette.tsx`：Ctrl/Cmd+K；命令由权限与现有路由生成
  （无权限命令不出现）；250ms debounce + AbortController 取消旧请求；
  上下键/Enter/Escape；结果分组显示类型、标题、**完整标识**与目标页面；
  `<900px` 全屏；搜索失败与无结果在面板内部呈现，不使用页面横幅。
  `Highlight` 组件用纯文本切分实现高亮，**不注入未清洗 HTML**。
- `components/command/recentObjects.ts`：最近访问对象只存在本地，最多 12 条，
  不缓存任何业务字段，也不作为请求参数来源。
- `App.tsx`：路由路径、参数名、权限名、重定向行为**零改动**；
  懒加载回退换成与目标页面同形状的 `PageSkeleton`；
  `ROUTE_PERMISSIONS` 继续从 `App.tsx` 导出（兼容既有导入方）。

### 登录页（§12.1）

- 左侧品牌面板 + 抽象资金路径 SVG（**只画结构，不标注任何金额**），
  `<980px` 隐藏，移动端只留小型标识与表单。
- 保留：账号密码、OIDC、工作区选择 Dialog（绝不自动选第一个）、MFA 第二步、恢复码、
  超时、必须修改初始密码、return-to。
- MFA 用 6 格视觉 + **一个真实 input**（`#mfa_code`）；出现非 6 位数字（恢复码）时自动回落成普通文本框。
- 密码可显示/隐藏；Enter 提交由表单原生行为承担。
- **登录失败改为"字段强调 + 错误 Dialog"**（蓝图 §12.1 明确要求），见下方"功能行为变化"。

### 首页（§12.2 Bento 工作台）

- A 问候 / 工作区 / 数据时间 / 主操作；B 指标卡（`dashboard.cards` 原样显示，不做二次计算）；
  C 进行中的调查与下载任务；D 待我处理；E 最近案件；F 快捷操作（`quick_actions` + 权限）。
- C/D/E **不新增接口、不写假数据**，用已有接口并且逐块按权限裁剪：
  缺少权限的子分区**根本不发请求**，也不渲染空壳；
  每块独立处理加载/空/错误，单块失败不影响整页。
- `jobs.progress` 是服务端给出的真实比例；缺失时传 `percent={null}` 走 indeterminate，不伪造百分比。

### 未知路由与无权限页（§12.26 / §11.6）

- 未知路由只说明"页面不存在"并给出返回工作台入口，
  **不再**把它描述成"已在后续 Sprint 排期"，也不提供已下线的旧控制台链接。
- 无权限页显示缺少的权限名 + 返回工作台，不伪装成 404。

### 会话引导错误的可见化

- `packages/api-client/auth.tsx`：后台会话引导（挂载 / 聚焦 / 30s 轮询）以前用 `void reload()` 触发，
  非 401/403 的错误会成为**未处理的 Promise 拒绝**（浏览器 console 报错，用户只看到无解释的登录页）。
  现改为 `bootstrap()` 捕获异常并记录到新增的 `sessionError`，同时新增 `retrySession`。
- 用户端 `App.tsx` 在 `sessionError && !session` 时渲染带重试的错误状态，而不是直接跳登录页。
- 这是**新增字段**，后台端读取的既有字段与行为不受影响。

**运行测试及准确结果**

```text
npx tsc -p packages/api-client/tsconfig.json --noEmit    → exit 0
npx tsc -p packages/ui/tsconfig.json --noEmit            → exit 0
npx tsc -p apps/user-console/tsconfig.json --noEmit      → exit 0
npx tsc -p apps/admin-console/tsconfig.json --noEmit     → exit 0
npx vite build --config apps/user-console/vite.config.ts → exit 0
npx playwright test -c playwright.dual.config.ts --list  → 新规格 17 个用例被正确收集
```

**桌面证据 / 移动证据（真实渲染度量，非截图）**

使用 `node scripts/verify-user-console-visual.mjs` 在真实 Chromium 里度量
`/app-v2/login`，六个断点（390×844、768×1024、1024×768、1440×900、1920×1080、2560×1440）：

```text
检查组合数        6
横向溢出          0
对比度不达标      0
页面内提示选择器  0
触摸目标不足      0
进度条缺少 ARIA   0
无名称按钮        0
运行时 console 错误 0
令牌快照          --c-cobalt #4353E6 / --c-ink #0A0E14 / --c-ice #F5F6F8
                  --c-graphite #111827 / --c-slate #596273 / --c-border #E3E6EB
```

报告：`acceptance-evidence/user-console-redesign/visual-report-anonymous.json`
（令牌快照证明新的配色层确实生效，而不是"改了变量没人用"。）

**真实数据/交互证据**：**NOT_VERIFIED** —— 需要真实后端。
本阶段所有数据相关行为（工作区切换、通知、命令面板搜索、首页 C/D/E 分区）都只做了
类型与结构验证，未在真实接口上跑通。

**可访问性结果**：焦点管理（抽屉、Popover、命令面板、Dialog）、可访问名称（按钮与图标控件）、
对比度（27 组令牌 + 渲染期实测）、状态三通道（文字+形状+颜色）均已实现并有渲染期度量支撑；
键盘全流程的实际走查仍属 **NOT_VERIFIED**。

**性能和 bundle 结果**：见 U6。

**发现的问题**

1. 蓝图 §12.2 的 C/D/E 三区在现有 `/api/v2/dashboard` 响应里没有对应字段。
   处理方式：用已有接口（`/investigations?status=running`、`/jobs`、`/inbox`、`/cases`）
   按权限取真实数据，**不新增接口、不写假数据**。若后续希望一次请求拿全，
   需要服务端在 dashboard 响应里扩展字段——这属于接口变更，不在本次范围内。
2. `frontend/tests/dual-system-*.spec.ts` 的 `expectNoInlineNotice` 会把 `[role="alert"]`
   判定为页面内提示。因此字段级校验错误**不使用** `role="alert"`（它属于界面本身，
   不是提示通道），真正的错误结果仍走 Dialog。
3. 本机没有可用的视觉通道（modlens 未配置），因此无法人工核对截图；
   已改用机器可读的渲染度量 + 截图双重产物。

---

## 功能行为变化（必须逐项确认）

| # | 变化 | 位置 | 原因 | 影响面 |
|---|---|---|---|---|
| 1 | 登录失败从"页面顶部短提示"改为"字段强调 + 错误 Dialog" | LoginPage | 蓝图 §12.1 明确要求；且与 code-standards 的"一切错误走弹窗"一致 | 仅需确认交互形态；错误文案、判定逻辑、工作区选择与会话流程均未变 |
| 2 | 未知路由文案由"尚未在此版本开放 / NOT_IMPLEMENTED"改为"页面不存在 + 返回工作台" | App.tsx | 蓝图 §12.26 | 未知路由是唯一的展示面；不涉及已存在路由 |
| 3 | `DataTable` 的分页重置条件由"rows 引用变化"改为"行数 + 首行键变化" | primitives.tsx | 轮询型页面（React Query / useApi 每次返回新数组引用）会把用户翻到的第 5 页顶回第 1 页 | 只影响"数据刷新时是否回到第一页"；页大小、虚拟滚动、`table-summary` 文案均未变 |
| 4 | `Dialog` 顶部类型标记改为图标（用户端）/ 文字（后台） | Dialog.tsx + 用户端 CSS | 蓝图 §10.2 | 后台端渲染结果与重构前一致；用户端由文字改为图标 |
| 5 | `packages/api-client/auth.tsx` 新增 `sessionError` 与 `retrySession`，后台会话引导不再抛出未处理拒绝 | api-client | 蓝图 §20.2（console error 为 0）与 §11.5 | 纯新增字段；既有字段与登录/登出/工作区切换语义未变 |

---

## 阶段 U3–U5：页面重构

见下方"页面清单"一节（按页面逐项记录完成状态与来源）。
---

## 阶段 U3–U5：页面清单（蓝图 §12 全部 26 节）

| 蓝图章节 | 路由 | 源文件 | 状态 |
|---|---|---|---|
| §12.1 登录 | `/login` | `pages/LoginPage.tsx` | 完成 |
| §12.2 我的概览 | `/home` | `pages/HomePage.tsx` | 完成 |
| §12.3 全局搜索 | `/search` | `pages/SearchPage.tsx` | 完成 |
| §12.4 案件中心 | `/cases` | `pages/CasesPage.tsx` | 完成 |
| §12.5 新建案件 | `/cases/new` | `pages/CaseNewPage.tsx` | 完成 |
| §12.6 案件详情 | `/cases/:caseId/:tab` | `pages/CaseDetailPage.tsx` | 完成（7 个 tab 全部保留） |
| §12.7 调查任务 | `/investigations` | `pages/InvestigationsPage.tsx` | 完成 |
| §12.8 发起调查 | `/investigations/new` | `pages/InvestigationNewPage.tsx` | 完成（三步向导） |
| §12.9 调查工作台 | `/investigations/:id/workbench` | `pages/InvestigationWorkbenchPage.tsx` | 完成（三栏） |
| §12.10 Findings 复核 | `/investigations/:id/findings` | `pages/FindingsPage.tsx` | 完成（SplitPane） |
| §12.11 Evidence 详情 | `/investigations/:id/evidence/:evidenceId` | `pages/EvidenceDetailPage.tsx` | 完成 |
| §12.12 资金图谱列表 | `/graph` | `pages/CanvasListPage.tsx` | 完成 |
| §12.13 Canvas | `/canvases/:canvasId` | `pages/CanvasPage.tsx` | 完成 |
| §12.14 地址与交易查询 | `/explorer` | `pages/ExplorerPage.tsx` | 完成 |
| §12.15 地址详情 | `/addresses/:chain/:address` | `pages/AddressDetailPage.tsx` | 完成（部分条目受接口限制，见下） |
| §12.16 交易详情 | `/transactions/:chain/:hash` | `pages/TransactionDetailPage.tsx` | 完成（部分条目受接口限制，见下） |
| §12.17 新建数据下载 | `/downloads/new` | `pages/DownloadNewPage.tsx` | 完成（五步 Stepper） |
| §12.18 任务中心 | `/jobs` | `pages/JobsPage.tsx` | 完成 |
| §12.19 任务详情 | `/jobs/:jobId` | `pages/JobDetailPage.tsx` | 完成（8 个分层 tab） |
| §12.20 导出中心 | `/exports` | `pages/ExportsPage.tsx` | 完成 |
| §12.21 待我处理 | `/inbox` | `pages/InboxPage.tsx` | 完成 |
| §12.22 报告中心 | `/reports` | `pages/ReportsPage.tsx` | 完成 |
| §12.23 报告详情 | `/reports/:id` | `pages/ReportDetailPage.tsx` | 完成 |
| §12.24 通知 | `/notifications` | `pages/NotificationsPage.tsx` | 完成（共享数据源） |
| §12.25 个人资料与安全 | `/profile` | `pages/ProfilePage.tsx` | 完成 |
| §12.26 未知路由 / 无权限 | `*` | `App.tsx` | 完成 |

**本轮由并行子代理完成的页面**：Search / Explorer / Jobs / Exports（§12.3、§12.14、§12.18、§12.20）、
AddressDetail / TransactionDetail（§12.15、§12.16）、DownloadNew / JobDetail（§12.17、§12.19）、
CanvasList / Canvas（§12.12、§12.13）、Cases / CaseNew / CaseDetail / Investigations（§12.4–§12.7）、
InvestigationNew / Workbench / Findings / Evidence（§12.8–§12.11）、
Reports / ReportDetail / Inbox / Notifications / Profile（§12.21–§12.25）。

---

## 统一校验结果（真实执行）

### 类型与构建

```text
node scripts/check-token-contrast.mjs                        → 27/27 通过，exit 0
npx tsc -p packages/ui/tsconfig.json --noEmit                → exit 0
npx tsc -p packages/design-tokens/tsconfig.json --noEmit     → exit 0
npx tsc -p packages/api-client/tsconfig.json --noEmit        → exit 0
npx tsc -p apps/user-console/tsconfig.json --noEmit          → exit 0（25 个页面 + 全部共享包）
npx tsc -p apps/admin-console/tsconfig.json --noEmit         → exit 0
npx vite build --config apps/user-console/vite.config.ts     → exit 0
npx vite build --config apps/admin-console/vite.config.ts    → exit 0
```

### Bundle（用户端，蓝图 §15 预算 <350KB gzip）

```text
index-*.css                  83.69 kB │ gzip 15.19 kB
index-*.js                  105.99 kB │ gzip 32.37 kB   ← 首屏
react-vendor-*.js           233.82 kB │ gzip 74.87 kB   ← 首屏
首屏 gzip JS 合计           ≈ 107.2 KB（预算 350 KB，余量 69%）

按路由懒加载（均不进入首屏）：
  CanvasPage 46.47 / DownloadNewPage 34.62 / AddressDetailPage 30.87 /
  JobDetailPage 22.84 / JobsPage 19.59 / ExportsPage 18.78 / ProfilePage 16.36 /
  CaseDetailPage 15.35 / TransactionDetailPage 15.11 / …（其余 ≤11.4 KB）
  xlsx 500.02 kB 仍在 Web Worker 内动态 import，不进入任何首屏或公共 chunk
```

### 真实渲染度量（六断点，Chromium）

```text
命令：node scripts/verify-user-console-visual.mjs --base <dev-url>/app-v2 --out <evidence-dir>

断点：390×844 · 768×1024 · 1024×768 · 1440×900 · 1920×1080 · 2560×1440
检查组合数        6（登录页）
横向溢出          0
对比度不达标      0
页面内提示选择器  0
触摸目标不足      0
进度条缺少 ARIA   0
无名称按钮        0
运行时 console 错误 0
```

报告：`acceptance-evidence/user-console-redesign/visual-report-anonymous.json`

令牌快照证明新的配色层真的生效（而不是"改了变量没人用"）：

```text
--c-cobalt   #4353E6      --c-graphite #111827
--c-ink      #0A0E14      --c-slate    #596273
--c-ice      #F5F6F8      --c-border   #E3E6EB
```

配套脚本（可复现，已纳入仓库）：

| 脚本 | 用途 |
|---|---|
| `frontend/scripts/verify-user-console-visual.mjs` | 六断点渲染度量（溢出/对比度/触摸目标/ARIA/console），输出 JSON 证据 |
| `frontend/scripts/mock-auth-fixture.mjs` | 无后端环境下让用户端进入"未登录"这一**合法**状态，只回 401/oidc=false，不伪造任何业务数据 |
| `frontend/tests/dual-system-user-console-redesign.spec.ts` | RC-01…RC-11 共 17 个用例（四断点溢出、无横幅、进度条 ARIA、按钮可命名、命令面板、通知中心、侧栏与抽屉、触摸目标、reduced-motion、console、Dialog 契约） |

---

## 删除的旧 CSS（蓝图 §17 / §18 U6）

逐条确认"整个 `src` 目录内没有任何 `.tsx` 引用"后才删除：

- `sprint4.css`：`s4-tabs` / `s4-tab` / `s4-kv(?)` / `s4-grid` / `s4-cards` /
  `s4-list` 系列 / `s4-step` 系列 / `s4-sticky-actions` —— 全部由共享
  `Tabs` / `KeyValueList` / `SplitPane` / `Stepper` / `StickyActionBar` / `.cs-rowcards` 取代。
- `sprint5/sprint5.css`：`s5-grid` / `s5-grid--3` / `s5-kpis` / `s5-kpi` 系列 /
  `s5-form-section` / `s5-form-grid` / `s5-field-label` / `s5-actions` / `s5-progress` /
  `s5-chart-frame` / `s5-mobile-only` / `s5-desktop-only` —— 由 `.cs-bento`、
  `PageSection` + 表单网格、`LinearProgress`、`.cs-chart-frame` 与页面自身的
  `useMediaQuery` 断点判定取代。
- **保留**：`s5-canvas-*`（画布 SVG 与状态环）、`s5-node-list*`、`s5-object-head`、
  `s5-mono`、`s5-stack`、`s5-json`、`.table--virtual`、`s4-button-row`、
  `s4-form-grid`、`s4-list__sub` —— 这些仍有真实引用，删掉会掉样式。

CSS 体积从 86.61 KB 降到 83.69 KB（gzip 15.66 → 15.19 KB）。

---

## 子代理上报的跨页面问题与处理

| # | 问题 | 处理 |
|---|---|---|
| 1 | `/cases/:id` 的 tab 被既有 E2E 钉死为 **link**（`getByRole("link", {name:"调查对象"})`），新的 `<Tabs>` 渲染 `role="tab"` 按钮 | 保留业务兼容层 `DeepTabs`（真实 `NavLink` 深链接），但内部换成 `.cs-tabs`/`.cs-tab` 视觉，选中态补 `[aria-current="page"]` 规则。**没有改测试**，也没有牺牲新视觉 |
| 2 | `MetricCard` 同时传 `onActivate` 与 `sourceNote` 会渲染 `<button>` 套 `<button>`（无效 HTML） | 已修：外层改为普通容器，可点击区域是内部唯一交互元素，口径说明按钮作为**兄弟节点**绝对定位 |
| 3 | `PageHeader` 面包屑用 `<a href>`，导致整页刷新（丢掉筛选/缓存/滚动位置） | 已修：改用 react-router 的 `<Link>`，恢复 SPA 导航 |
| 4 | `components.css` 的 `.cs-workbench` 在 ≤1599px 就降为两栏，1440×900 验收断点下不是三栏 | 已修：断点对齐蓝图 §13 —— ≥1200px 三栏、900–1199px 两栏、<900px 单栏 |
| 5 | `responsive.css` 在 <640px 隐藏 `thead` 并把 `td` 变块，移动端表格**丢失全部列标签** | 已修：不再拆表，保留表头并交给 `.table-scroll` 横向滚动（蓝图 §13 允许"表格局部滚动"；真正需要换形态的列表页由页面自己渲染 `.cs-rowcards`） |
| 6 | Inbox 的 finding 目标指向 `/findings/{id}`，而路由表里没有这条路由 → 点击落到"页面不存在" | 已修：改为真实的 `/investigations/{investigation_id}/findings`；缺 `investigation_id` 时返回空串（渲染为不可用按钮），不再拼出会 404 的假路径。HomePage 的待办预览此前已用同一形状，两处现在一致 |
| 7 | CanvasPage 缺少 `FilterChip` 导入 / CanvasListPage 的节点类型桶类型过窄 | 已修（类型检查归零） |
| 8 | 画像类页面筛选/字段在接口里不存在（案件"更新时间/负责人"、调查"模式/关键字"、报告 `created_by/updated_at`、地址"标签/案件"、交易 `fee`/关联对象、Job 列表速率、Export 字节数） | **不伪造、不新增接口**：不渲染或显示 `—`，并把口径写进 Dialog。已在聊天页与代码注释中逐条记录 |
| 9 | `/api/v2/jobs`、`/api/v2/exports` 的 `meta.total` 只是本页条数，不是全量 | 顶部统计明确标注"本次响应已加载 N 条"，不冒充全量 |
| 10 | 通知类型筛选：接口支持 `type`，但共享 hook 只请求 `limit=200` | 按蓝图 §12.24"只有接口支持时再加类型筛选"未加类型筛选；客户端筛选已加载的 200 条会造成误导，故不做 |
| 11 | 蓝图 §12.20 要求"上半区创建导出"，而 R10/B2RAW E2E 要求"新建导出"按钮弹出含 `#export-*` 的 Dialog | 两者都保留（内联表单 + Dialog 共用同一草稿），代价是创建入口有两个 |
| 12 | 蓝图 §12.13 要求 XYFlow，但用户端**从未**使用 `@xyflow/react`（它只被旧 SPA `/fund-flow` 引用），且 `dual-system-sprint5.spec.ts` 断言 `.s5-canvas-svg` 可见 | 保留并增强既有 SVG 实现（形状/图标分型、边方向+资产+金额+title、LOD、聚类、路径播放），**不引入第二套渲染器**，也不改既有 E2E 选择器 |

---

## 未验证项（NOT_VERIFIED，不以构建成功替代）

以下项目需要真实后端、种子数据与浏览器环境，本施工环境无法复现。
**明确标记为未验证，不推断为通过**（蓝图 §2.10 / §21.7 / §22）：

1. **全部真实后端业务流程**：登录→显式选择工作区→MFA→首页；新建案件→添加对象→发起调查→
   启动/暂停/恢复→Finding 复核→Evidence 验证→报告审批；查询地址→交易→Trace/Logs；
   导入地址→preview→创建下载→分层进度→暂停/恢复→导出→下载；Canvas 创建→交互→保存→
   revision 冲突；资料修改→改密→MFA→Session/设备撤销。
2. **Playwright 全量回归**：`dual-system*.spec.ts`、`r9-*`、`r10*`、`p1-*`、`udl01-*`、
   `tron-v4`、`fund-flow` 等 26 份规格均未执行（需要 `E2E_BASE_URL`、`E2E_USER_PASSWORD`
   与运行中的服务端）。新写的 `dual-system-user-console-redesign.spec.ts` 只验证了
   **可被 Playwright 正确收集**（17 个用例），未执行断言。
3. **登录页以外的六断点渲染度量**：`/home` 之后的 15 条路由需要已登录会话，
   本环境没有后端，因此它们的横向溢出、对比度、触摸目标、console 洁净度均**未实测**。
4. **SSE / 轮询实时表现**：断线保留最后进度、重连不把进度重置为 0、
   已完成项不因后续事件退回 RUNNING——均只做了代码层实现，未在真实任务上验证。
5. **键盘全流程走查**：焦点顺序、Tab 陷阱、Escape、抽屉/检查器/命令面板的键盘操作
   已按实现检查，但未经人工或自动化真实走查。
6. **大文件解析与大图性能**：DownloadNew 的 Worker 解析进度弹窗、Canvas >2000 节点聚类/LOD
   只在设计上成立，未在真实数据量下实测。
7. **reduced-motion 的实际观感**：CSS 与 `matchMedia` 分支已实现并在登录页度量过，
   其余页面的表现未实测。

---

## 回滚方式

按阶段回滚（每一步都可独立撤销）：

```text
# 基础层（令牌 / 共享组件 / Shell / 入口）
git checkout -- frontend/packages/design-tokens/tokens.css \
                frontend/packages/ui/AppShell.tsx frontend/packages/ui/Dialog.tsx \
                frontend/packages/ui/index.ts frontend/packages/ui/primitives.tsx \
                frontend/packages/api-client/auth.tsx \
                frontend/scripts/check-token-contrast.mjs \
                frontend/apps/user-console/src/App.tsx frontend/apps/user-console/src/main.tsx
git clean -f frontend/packages/ui/progress.tsx frontend/packages/ui/layout.tsx \
               frontend/packages/ui/entity.tsx frontend/packages/ui/navigation.tsx \
               frontend/packages/ui/forms.tsx frontend/packages/ui/data.tsx
rm -rf frontend/apps/user-console/src/app frontend/apps/user-console/src/components \
       frontend/apps/user-console/src/styles

# 页面
git checkout -- frontend/apps/user-console/src/pages frontend/apps/user-console/src/sprint4.css \
                frontend/apps/user-console/src/sprint5/sprint5.css \
                frontend/apps/user-console/src/sprint4/common.tsx
```

未提交任何变更，也没有修改数据库、迁移或后端代码。

---

## 已知限制

- `app-v2` 的旧 `.shell__*` 类仍由**后台端**使用，因此 `packages/ui/ui.css` 里的旧规则全部保留；
  用户端的视觉刷新通过 `html[data-app="user"]` 作用域隔离，后台端读取到的仍是原取值与原规则。
- 用户端配色重映射依赖 `main.tsx` 在渲染前写入 `document.documentElement.dataset.app = "user"`；
  若该属性丢失，用户端会回落到 tokens.css 的原始配色（功能不受影响，只是观感回到重构前）。
- `packages/ui` 仍同时服务用户端与后台端，因此新增组件一律通过**可选 prop** 提供新行为，
  默认分支保持与原实现一致。
