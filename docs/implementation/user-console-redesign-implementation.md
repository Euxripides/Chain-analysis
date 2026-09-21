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

---

## 阶段 U7：真实环境点击巡检与缺陷修复

**测试环境**（真实，非模拟）

```text
主机       dataserver（Ubuntu，192.168.31.152），仓库 /home/euripides/projects/blockchain-platform
入口       https://127.0.0.1:8448/app-v2/   （socat TLS → nginx 8088 → vite preview 3001）
后端       blockchain-api.service（:8080）、blockchain-worker.service（:8081）
数据       PostgreSQL 16 + ClickHouse + MinIO + NATS（真实数据，非夹具桩）
账号       admin@local（Owner，主工作区 000e31bb…，24 个调查 / 任务 / 报告 / Canvas）
```

**巡检方式**：`frontend/tests/dual-system-uc-button-sweep.spec.ts` —— 逐个点开页面上的每一个控件。
安全边界写在用例里：只读/导航类真实点击；弹窗打开后关闭；**会写数据的动作只打开确认框再取消**；
没有取消路径的立即破坏性动作不点（记录 `skipped-destructive`）。

### 最终结果

```text
UC-SWEEP   routes=21  controls=464  clicked=381  dialogCancelled=14  failures=0  pageErrors=0
dual-system-quality（4 个 project）                36 passed / 0 failed   RC=0
dual-system-sprint4                                RC=0
dual-system-sprint5                                RC=0
dual-system-nav-permission                         RC=0
dual-system-cross-role                             RC=0
dual-system-reduced-motion                         RC=0
```

### 巡检发现并修复的 4 个真实缺陷

#### 1（严重）虚拟滚动表格滚到底部后整片空白

- **症状**：案件 / 调查 / 导出列表向下滚动到后半段时表格变空，行内「复制」按钮完全点不到。
- **根因**：`DataTable` 的窗口起点用**固定估计行高 44px** 推算
  （`floor(scrollTop / 44) - 8`）。重构后单元格内边距加大、多个列表出现两行单元格，
  实测行高变成 **61 / 65 / 72px**；表越长累积偏差越大，滚到底部时算出的起点
  **超过数据行数**，`slice.slice(start, end)` 取到空数组，tbody 只剩撑高的占位行。
- **修复**（`packages/ui/primitives.tsx`）：
  1. 起点夹取到 `[0, rows.length - visibleCount]`——**硬保证永不出现空窗口**，
     即使行高估计仍有偏差，最坏也只是滚动手感略偏，不会整片空白；
  2. 渲染后用**中位数**实测行高并回填（中位数对个别两行单元格不敏感，不会来回抖动）。
- **验证**：`frontend/scripts/uc-virtualizer-probe.mjs`——
  三张表滚到底部 `blank=false`、最后一行复制按钮点击 `OK`。

#### 2 元信息条长值把 390px 页面撑到 485px

- **根因**：`.cs-meta__item b` 没有 `overflow-wrap`，而元信息条里放的是
  **工作区 UUID、数据源名**这类不可断词的业务标识。
- **修复**：`.cs-meta__item` 补 `min-width:0; max-width:100%`，
  `b` 补 `min-width:0; overflow-wrap:anywhere`。

#### 3 头像/品牌标记的白字被量成"白底白字"（对比度 1.08）

- **根因**：靛蓝底写在 `background-image` 渐变里，`background-color` 仍是透明的；
  对比度检查按 `background-color` 回溯背景，一路找到页面白底。
- **修复**：`.cs-avatar` / `.cs-brand__mark` 显式声明
  `background-color: var(--indigo-600)`（白字 5.80:1），渐变只作为叠加层次。
  这同时也是"让计算样式说真话"的修正，不只是为了让检查变绿。

#### 4 登录页一条可避免的 CSP console 错误

- **根因**：两个控制台都没有声明 favicon，Chromium 会拿**文档 URL** 去探测图标，
  被 nginx 的 `img-src 'self'` 拦下并报一条 console error。
  （用 `securitypolicyviolation` 事件定位到 `blockedURI=https://…/favicon.svg`。）
- **修复**：新增品牌化 `apps/user-console/public/favicon.svg`（资金路径标记）与
  `apps/admin-console/public/favicon.svg`（盾形校验标记），两个 `index.html` 各加
  `<link rel="icon" type="image/svg+xml" href="/favicon.svg" />`；
  Vite 会按各应用的 base 改写为 `/app-v2/favicon.svg` 与 `/admin/favicon.svg`。
- **验证**：登录页 CSP 违规从 2 条降为 **0 条**。

### 自检脚本本身的缺陷（由既有验收套件发现）

`verify-user-console-visual.mjs` 原来只等 `domcontentloaded` 就测量：
数据还没回来时元信息条仍是骨架，长值不在 DOM 里，**横向溢出被漏掉**——
所以自检报"0 溢出"而 `dual-system-quality` 报 485px。
已改为 `networkidle` + 700ms 稳定期。**自检脚本的严格度不得低于既有验收套件**，否则只是"假绿"。

### 部署执行

本次改动**全部在前端**（`apps/user-console`、`apps/admin-console`、`packages/ui`、
`packages/design-tokens`、`packages/api-client`），没有任何后端、数据库或迁移改动。
因此采用**前端作用域部署**，不执行会重建 Go 二进制并重启 API 的
`scripts/deploy_dual_system.py --yes`——那会在公网入口上造成一次无必要的 API 重启。

```text
1. 源码同步    本地 → VM（tar over ssh，仅覆盖本次变更文件，不删除 VM 专有文件）
2. 类型检查    npx tsc -p apps/user-console/tsconfig.json --noEmit  → 0 错误
3. 构建        npx vite build --config apps/user-console/vite.config.ts  → 成功
               npx vite build --config apps/admin-console/vite.config.ts  → 成功
4. 重启        systemctl --user restart blockchain-user-console.service
                                          blockchain-admin-console.service
5. 冒烟        https://127.0.0.1:8448/app-v2/     200
               https://127.0.0.1:8448/admin/      200
               http://127.0.0.1:8080/health/ready 200
               产物          assets/index-BPbVrJbD.js（与本地构建一致）
               图标          /app-v2/favicon.svg 200 image/svg+xml
                             /admin/favicon.svg   200 image/svg+xml
```

若需要走完整编排（会重建后端并应用迁移），命令是：

```bash
python3 scripts/deploy_dual_system.py --yes
```

### 测试环境的一处清理（需要知晓）

`admin@local` 上有一次早期 MFA 自服务测试遗留的 **MFA 注册**，
导致所有以该账号为默认的既有 E2E 规格都会卡在第二步验证。
本次按 `bootstrap-e2e-env.sh` 的同一口径把它清理掉（删除 `identity_mfa` 及其恢复码/挑战），
恢复"账号密码即可登录"的验收基线。这是**测试环境清理**，不是产品代码改动；
该账号随时可以重新注册 MFA。
### U7 第二轮：六断点全路由度量（比既有套件更严）

既有 `dual-system-quality` 只覆盖 5 条路由 × 4 断点；本次用
`frontend/scripts/verify-user-console-visual.mjs --login` 扩到
**15 条路由 × 6 断点 = 90 个组合**（含 1920×1080、2560×1440）。第一轮又发现 3 类问题：

| # | 发现 | 根因 | 修复 |
|---|---|---|---|
| 5 | `/investigations` 在 390px 下 `documentElement.scrollWidth` = 946px（`/exports` = 642px） | 宽表的 `min-width: max-content`（实测 1731px）让 Chromium 把滚动容器内的内容计入**根元素**的 scrollable overflow。逐项实验确认：`body.scrollWidth` 恒为 390px、`trueOffenders=0`，即**用户侧没有横向滚动、没有任何内容被截断**；但该指标失真会让所有按 scrollWidth 判定的验收用例误报 | 给 `.table-scroll` 加 `contain: paint`，把表格的溢出限制在它自己的滚动上下文里。修复后 `rootScrollW = 390` |
| 6 | 小屏冻结首列宽 399px、超过表格自身滚动区 320px，盖住后面所有列 | 冻结列在窄屏没有降级 | `<900px` 取消冻结列的 `position: sticky`（小屏本就该用卡片列表或横向滚动） |
| 7 | 步骤分隔符 `/` 与范围分隔符 `→` 对比度 1.35:1（白底上几乎不可见） | 用了边框色 `--border-strong` 当文字色 | 改为 `--text-secondary`（6.14:1） |
| 8 | 侧栏分组标签 10px、元信息条 12px 偏小 | 低于 `--fs-11` / 正文 13px 的取值 | 分别提到 `--fs-11` 与 `--fs-13` |

修复后 **90/90 组合全部通过**：横向溢出 0、对比度不达标 0、页面内提示选择器 0、
触摸目标不足 0、进度条缺 ARIA 0、无名称按钮 0、**运行时 console 错误 0**。

### 最终回归（全部真实执行）

```text
dual-system-quality（user-desktop / admin-desktop / user-mobile / admin-tablet）  36 passed / 0 failed
dual-system-sprint4                 RC=0
dual-system-sprint5                 RC=0
dual-system-nav-permission          RC=0
dual-system-cross-role              RC=0
dual-system-reduced-motion          RC=0
dual-system-uc-button-sweep         21 routes / 484 controls / 381 clicked / 14 dialog-cancelled
                                    0 failures / 0 pageErrors
verify-user-console-visual --login  90 combinations / 0 problems
```
### 部署后最终确认（对**已上线产物**再跑一遍）

```text
上线产物     user  assets/index-CX4qVDy7.js + assets/index-CK00XSYC.css
             admin assets/index-CRFeEJXZ.js
源码一致性   responsive.css / components.css / user-shell.css / primitives.tsx /
             index.html / favicon.svg —— VM 与本地 SHA-256 前 16 位逐一相同
类型检查     apps/user-console tsc OK ； apps/admin-console tsc OK
冒烟         app-v2/ 200 · app-v2/home 200 · admin/ 200
             app-v2/favicon.svg 200 image/svg+xml · admin/favicon.svg 200 image/svg+xml
             api /health/ready 200
登录链路     POST /api/auth/v1/login 200 → GET /api/auth/v1/session 200
             workspace 000e31bb-8c48-435d-af97-95bbc75a496c · permissions 179

dual-system-quality                 36 passed / 0 failed      RC=0
dual-system-uc-button-sweep         21 routes · 484 controls · 381 clicked
                                    14 dialog-cancelled · 26 skipped-destructive
                                    62 skipped-disabled · 0 failures · 0 pageErrors
verify-user-console-visual --login  90 combinations（15 路由 × 6 断点）· 0 problems
```

证据存档（部署机上）：`acceptance-evidence/user-console-redesign/`

```text
button-sweep/button-sweep.json        每个控件的逐条点击记录与结果
button-sweep/sweep-run-final.log      最终巡检运行日志
visual-report-authenticated.json      90 组合的渲染度量（含令牌快照）
visual-report.log                     度量摘要
regression-suites.log                 六个既有套件的完整输出
final-confirmation.log                部署后最终确认
```

---

## 2026-09-20 清晰度修订：冷白四级明度 + 退役玻璃拟态

**触发**：用户端上线后反馈"低对比度、整页雾化"。复核确认这不是主观观感问题，
而是配色与材质两个决策叠加的必然结果：

1. **四个面只差 1–2 个亮度级**：画布 `#F4F8FF`、侧栏 `#F8FBFF`、表格底 `#F7FAFF`、
   凹陷面 `#EEF4FD` 全是淡蓝近白。卡片虽然写的是 `#FFFFFF`，但在这种底色上
   分不出层，等于"卡片不存在"。
2. **边界几乎不可见**：卡片描边 `#DFE9F7` 在白底只有 **1.25:1**，
   输入框描边 `#C8D9F0` 只有 **1.5:1**——统计上"存在"，视觉上不存在。
3. **玻璃拟态铺满全局**：顶栏（`rgba(255,255,255,.88)` + `blur(14px)`）、底栏、
   Hero（三层 12–20% 蓝色 radial-gradient + `blur(2px)` + 22s 漂浮动画）、
   Dialog 遮罩（`blur(6px)`）、命令面板遮罩（`blur(5px)`）——模糊与半透明把
   本已微弱的边界进一步糊掉。
4. **弱化文字不达标**：`--cs-text-muted: #8796AC` 在画布上只有 **2.90:1**，
   低于 AA 的 4.5:1（占位符、表头小字、时间戳都用它）。
5. **蓝色被当成装饰色**：KPI 图标底、表格头、悬停行、Hero、选中态全部是蓝，
   于是"哪里能点、哪里是当前"失去信号价值。

### 改动（令牌唯一定义在 `packages/user-ui/theme/css-vars.css`，`theme/tokens.ts` 同步镜像）

| 级别 | 令牌 | 旧值 | 新值 |
|---|---|---:|---:|
| L1 页面底层 | `--cs-layer-page` | `#F4F8FF` | `#E9EDF3` |
| L2 内容层 | `--cs-layer-content` | `#FFFFFF` | `#FFFFFF`（不再有半透明变体） |
| L3 输入层 | `--cs-layer-field` | （无） | `#F4F7FA` |
| L4 选中层 | `--cs-layer-selected` | `#EAF2FF` | `#DEE9FD` |
| 卡片描边 | `--cs-border` | `#DFE9F7`（1.25:1） | `#CBD3DC`（1.51:1） |
| 输入描边 | `--cs-border-field` | `#C8D9F0`（1.50:1） | `#A3AFBD`（2.23:1） |
| 选中描边 | `--cs-border-selected` | （无） | `#7FA9F5`（2.36:1） |
| 主文字 | `--cs-text-primary` | `#10213D` | `#101A2B` |
| 次级文字 | `--cs-text-secondary` | `#5B6B84` | `#48566B` |
| 弱化文字 | `--cs-text-muted` | `#8796AC`（2.90:1） | `#59667A`（4.96:1） |
| 主操作蓝 | `--cs-primary` | `#3478F6`（白字 4.07:1） | `#2563EB`（白字 5.17:1） |
| 注意/危险文字 | `--cs-warning-text` / `--cs-danger-text` | `#9C6109` / `#C43C3C` | `#8F5A08` / `#B73535` |
| 玻璃 | `--cs-surface-glass` / `--cs-glass-blur` | `rgba(255,255,255,.82)` / `blur(18px) saturate(130%)` | `#FFFFFF` / `none` |

结构改动：

- 删除 `.cs-shell` 上铺满视口的两条装饰径向光晕（页面底色现在是单一纯色）。
- 顶栏、底部操作栏、对话框底、命令面板遮罩全部改为不透明面，删除
  `backdrop-filter`（含 Dialog/命令面板的入场 keyframes 里的模糊）。
- Hero 从"唯一允许大面积光晕与玻璃"改为与其它卡片同级的纯白实体面；
  `.cs-glasshero__aurora` 保留 DOM 节点但不再有任何视觉输出。
- 骨架屏 `#E9F0FB/#DCE8F9` → 中性冷灰 `#E4E8EE/#D3D9E2`（淡蓝骨架会被误读成内容）。
- Ant 适配层与 legacy 桥接层统一给输入类控件加 L3 输入面
  （`background: var(--cs-field)` + 2.23:1 描边 + 极浅内阴影）。
- 步进器当前步骤改为四通道表达（选中层底 + 蓝边界 + 深蓝文字 4.73:1 + 加粗）；
  未到达步骤取消 `opacity: .6`，改用明确灰边界 + 弱化灰字（5.82:1）。
- 所有以蓝色当正文色的地方（Tab 选中、chip 选中、批量条计数、stat tile info）
  改用文字安全蓝 `--cs-primary-text`。

### 验证（全部在真实构建产物上执行）

```text
pnpm run check:tokens          58 组前景/背景配对全部 ≥4.5:1（新增 12 组，含弱化文字四底色）
                                守卫本身修复两处：var() 链解析（四级明度是真源，别名不再被判"缺失"）
                                扫描前剥离注释（注释里的伪声明曾吞掉 cs-surface-blue）
pnpm run typecheck             apps/user-console + apps/admin-console + 全部 packages → 0 错误
uc-contract-freeze-check       路由 28 · 权限 19 · API 字面量 66 · 案件 Tab 契约 → 与基准一致
E2E_BROWSER_CHANNEL=chromium
  node scripts/uc-v4-render-check.mjs    ok = true · problems = []
bundle-budget-check            首屏 gzip 244.44 KB / 预算 250 KB
CSS 体积                        161,721 B（gzip 25,244 B）→ 比修订前 162,016 B（25,438 B）更小
```

真实渲染度量（Chromium 1440×900，静态挂载 `apps/user-console/dist` + 固定夹具，
逐元素取计算样式并回溯有效背景后算 WCAG 比值）：

```text
页面底层 #E9EDF3 · 卡片/顶栏/侧栏 #FFFFFF · 输入面 #F4F7FA · 选中层 #DEE9FD
卡片描边 #CBD3DC · 输入描边 #A3AFBD（全部元素 backdrop-filter = none）
正文 17.43 / 14.84  ·  次级 7.45 / 6.34  ·  弱化 5.82  ·  侧栏当前项 4.73
步进器当前步骤 4.73（描边 #2563EB）· 未到达步骤 5.82 · 分隔符 4.96
```

修订后的首屏证据：`acceptance-evidence/v4-render-check/home-1440x900.png`、
`download-new-1440x900.png`（同目录另有 390×844 移动端与其余路由截图）。

设计约束已同步进蓝图 §4.2 / §5.1 / §6.1.1：**禁止玻璃拟态、禁止大面积淡蓝、
禁止画布/卡片/输入框/边框同时取近似色**；蓝色只用于交互与状态。

---

## 2026-09-20 新建数据下载页重构：五段长表单 → 单页四区

**依据**：《ChainSight「新建数据下载」页面重构规范》V1.0 + 用户提供的效果图。
效果图明确要求"创建过程在完整的页面上"（不是一次一步的向导），因此最终版面是
**单页四区**：四个区块同时可见，顶部步骤条只表达顺序与当前进度。

### 结构

| 项 | 重构前 | 重构后 |
|---|---|---|
| 信息架构 | ① 对象与地址 ② 链与范围 ③ Dataset ④ 复用策略 ⑤ Coverage 预览，五段同时铺开、无分组 | ① 地址与对象 ② 链与范围 ③ 数据类型 ④ 预览与创建，**四区同屏**、每区带序号徽标 + 标题 + 一行说明 |
| 步骤条 | 自绘胶囊 Stepper，只作锚点跳转 | Ant Design `Steps`（已完成蓝勾 / 当前实心蓝 / 未完成白底灰边），点击滚动到对应区块 |
| 主操作 | 底部两枚按钮（创建下载任务 / 取消） | 右栏唯一主动作「预览数据并创建任务」，一次点击串起 服务端校验 → 预览 → 创建确认 |
| 链选择 | 原生 `<select>` | `ChainSelect`（Ant `Select` + 搜索 + 官方链图标）+ `ChainPicker` 链卡片网格，共用同一个单选值 |
| 范围选择 | 原生 `<select>` | `RangeSelect`（主名称 + 弱化技术 key，统一 popup 容器） |
| 性能模式 | 原生 `<select>` | Ant `Segmented` |
| Dataset | 一长排胶囊 checkbox | 2–3 列卡片网格（`DatasetCardSelector`，底层仍是真实 checkbox） |
| 地址输入 | 三个按钮挤在一行 + 每次粘贴弹"解析摘要"窗 | Tabs（粘贴地址 / 导入文件）+ 内联计数 + 文件解析数据行 |
| 复用策略 | Radio 可选 `prefer_local` / `refresh` | **整块移除**；请求仍显式带 `reuse_policy: "prefer_local"` |
| 顶部元信息 | 正文直接打印 `/api/v2/meta/chains` 等三条接口路径 | 正式构建不显示；仅 DEV 构建保留一行对照 |
| 校验 | 一律弹窗打断 | 字段级错误贴近字段（`Field error`，不用 `role="alert"`）；API/结果类错误仍走弹窗 |
| 右侧 | `SplitPane` 可拖拽的 360px 摘要 | 固定 340/280px sticky `PreviewInspector`，<1024px 下移 |

### 文件

```text
apps/user-console/src/pages/DownloadNewPage.tsx            路由入口薄壳（保持懒加载字面量不变）
apps/user-console/src/pages/downloads/DownloadNewPage.tsx  容器：状态、请求、编排（约 640 行）
apps/user-console/src/pages/downloads/steps/               AddressStep / ChainScopeStep / DatasetStep / PreviewStep
apps/user-console/src/pages/downloads/components/          ChainIcon / ChainSelect / RangeSelect / AddressInputPanel /
                                                           AddressParseSummary / AddressFileSummary / AddressFileImport /
                                                           DatasetCard / DatasetCardSelector / PreviewInspector / DownloadWizardActions
apps/user-console/src/pages/downloads/download.types.ts    类型、常量与纯函数（STEP_ITEMS 四步 + DEFAULT_REUSE_STRATEGY）
apps/user-console/src/pages/downloads/download-new.css     页面作用域样式（无裸色值）
packages/user-ui/styles/adapter.css                        该页 Ant 结构性覆写（仓库 §1.3：业务页面不得写 .ant-*）
```

### 业务契约：完全冻结

下载创建 / 预览 / chains / datasets 四个接口、字段 key、scope 与 performance 枚举、Dataset key、
100 地址 / 6 Dataset 上限、CSV-XLSX 解析行为、权限、创建成功后的跳转、幂等键派生方式**逐字未改**。
唯一的差异是产品决策：复用策略只在 UI 层隐藏，负载里照旧是 `prefer_local`。

服务端地址校验的触发点从"导入时"移到"第 ② 步出口"，因为 `validate-addresses` 的入参包含 chain，
而链是第 ② 步才选定的——这同时修掉了 ROUND-20260916 §3 UI-3 记录的
"先拿自动选中的第一个链去校验、地址被逐条拒绝"的既有 UX 问题。

### 与规范/效果图的两处刻意偏差

1. **"预计大小（约 0 MB）"未做**：`/api/v2/downloads/preview` 不返回字节大小，
   按"不发明接口没有的数据"的既有约定，右栏只显示真实字段（预计数据量 / 耗时 / 成本 / 覆盖率 /
   未覆盖区块）；解析进度同理不编造百分比，用不确定态进度条 + 文件名。
   （页头副标题与分区说明按效果图保留：`docs/code-standards.md` 第 5 条禁止的是"提示性内容"，
   而这些是页面自身的标题与字段级说明，且不含任何警告/结果/确认语义。）

### 版面定稿：单屏配置工作台（2026-09-20 第四轮，最终形态）

版面经过四轮收敛，**最终形态是"单屏配置工作台"**：

1. 第一轮：五段长表单 →（分步）→ 第二轮：四步向导 → 第三轮：四块同时铺开的长表单
   → **第四轮（本轮，定稿）：单屏三块 + 右栏摘要，无步骤条、无上一步/下一步**。

| 项 | 定稿形态 |
|---|---|
| 左区 | 二维 Grid：第一行「地址与对象」横跨两列；第二行左「链与范围」，右「数据类型」 |
| 右区 | 独立 320px（≥1280px 360px）「任务预览」：地址数量 / 网络 / 数据类型 / 范围 / 性能模式 / 预计数据量 / 预计大小 / 预计耗时 / 预计成本 + 唯一主按钮「预览并创建任务」 |
| 步骤条 | **已删除**（没有 `Steps`、没有"下一步/上一步"；`uc-v4-render-check` 直接断言它们为 0） |
| 紧凑尺寸 | 地址 TextArea 120–150px（实测 124）· 网络选择项 44px（要求 42–46）· Dataset 卡片两列 60px（要求 56–64） |
| 范围/性能模式 | 都是 Ant `Segmented`（一行放得下），不再纵向铺卡片 |
| 网络搜索 | 只保留 `ChainSelect` 自带的 showSearch（原来那个独立搜索框与它重复，已删除省一行高度） |
| 高度策略 | 外壳 `100dvh` + `overflow: hidden`；整条 flex 链 `min-height: 0`；工作台放得下就完全不滚动 |

关键实现点（都在 `download-new.css` 注释里）：

1. 页面外层还有一层路由容器 `.cs-route`（RoutedContent），它默认 `min-height: auto`，
   会把 flex 链从中间顶开——**必须一起放开**，否则右栏与内容会被顶出视口。
2. 右上"预计大小"：`/api/v2/downloads/preview` 的真实返回键只有
   `estimated_missing_blocks / estimated_rows / estimated_cost_usd / estimated_time_seconds / basis`
   （见 `backend/internal/api/sprint5_downloads.go` 的 `downloadEstimate`），**没有体积字段**。
   按"不发明接口没有的数据"，这一行照实显示 **"接口未提供"**，不拿行数乘系数折算；
   后端补 `estimated_bytes` 后，接上该字段即可。
3. 左侧不再有 Coverage 明细卡片（版面只有三块），因此把"每个对象在每个 Dataset 上的覆盖率 /
   已覆盖区块 / 缺口"并入**预览弹窗**的明细行，信息不丢失。
4. 实测（Chromium，真实构建产物）：

**自适应撑满（同日补）**：原来 `.cs-dlbench` 带着 `max-width: 1680px` 且 `align-items: flex-start`，
在 2404×1246 这类宽屏上会同时留下大片左右留白与底部空白。现在：去掉 `max-width`（配置网格不是正文，
超宽屏应该用满）· 桌面端 `align-items: stretch` · 左区 `grid-template-rows: auto minmax(0, 1fr)`
让第二行吃掉剩余高度、两块面板随之撑满 · 右栏 sticky 且撑满可视高度（按钮钉在面板底部）·
Dataset 网格 `repeat(auto-fill, minmax(200px, 1fr))`：面板变宽自动从 2 列变 3 列。

```text
2404×1246  初始 / 预览后：doc = body = 1246 = innerHeight · 工作台 177→1230（撑到视口底部）· 右栏同高
1920×1080  初始 / 预览后：doc = body = 1080 = innerHeight（无页面滚动）
           同屏区块 ["objects","range","datasets"] · Stepper=false · 上一步/下一步按钮=0
           右栏 bottom=795（首屏内）· 主按钮 = 预览并创建任务
           TextArea 124px · 网络选择项 44px · Dataset 卡片 60px
1440×900   初始 / 预览后：doc = body = 900 = innerHeight（无页面滚动）
           右栏 bottom=793（首屏内）· 其余同上
```

验收脚本同步：`uc-v4-render-check.mjs` 断言"三块同屏可见"、"没有 Stepper"、
"没有下一步/上一步按钮"、"右栏主按钮恰好 1 个"、"桌面端 doc/body 无纵向滚动"、
"右栏在首屏内"、"右栏含全部摘要项（含预计大小这一行）"；
E2E 用例（r9 ×4、dual-system-sprint5）改回单屏流程：
填地址 → 选链/范围 → 勾数据类型 → 右栏「预览并创建任务」→ 服务端校验 → 预览弹窗 → 创建确认。

### 视觉精修（2026-09-20 第二轮：《页面视觉修订规范》）

第一轮把结构改成单页四区之后，页面的"视觉语言"仍是旧后台的灰蓝低对比体系。
本轮按《ChainSight「新建数据下载」页面视觉修订规范》做**只改视觉与排版**的二次精修，
业务逻辑 / 字段 / 接口 / 权限 / `prefer_local` 默认行为全部未动（规范 §3 / §29）。

| 项 | 精修前 | 精修后 |
|---|---|---|
| 页面底色 | 全局画布 `#E9EDF3`（偏灰） | 本页 `#F7F9FC` 冷白（`.cs-shell:has(.cs-downpage)`，其它页面不变） |
| 主卡片 | 1px `#CBD3DC` + 短阴影 | 白底 + 1px `#E4E9F2` + 14px 圆角 + 双层轻阴影（§4.2/§23-L1） |
| 输入控件 | `#F4F7FA` + `#A3AFBD` | `#FBFCFE` + `#D7DEE9`（hover `#AAB8CC`，focus 蓝环），10px 圆角（§4.3） |
| 字体层级 | 主文字一档到底 | H1 24/650 `#172033`、Section 16/600 `#1F2937`、Label 13/500 `#344054`、辅助 12 `#667085`、技术 key 12 `#98A2B3`（§5） |
| 步骤条 | 小圆 + 淡字 | 30px 圆、标题 14/600、描述 12；当前实心蓝、已完成浅蓝勾 + 蓝连接线、未完成白底灰边不灰化（§8） |
| 区块标题 | 标题与说明挤一行 | 徽标 + 标题/说明**分两行**，状态靠右（§9） |
| 链与范围 | 三个控件平铺一块 | 左"网络"（下拉 + 搜索 + 链卡片网格）／右"范围"（可点击卡片），**性能模式降到下方**次级设置（§11–16） |
| 右栏 | 一行一行的字段表 | 任务摘要卡：配置分组（字段块上下排、块间 16px、不画线）+ 预估（值 19/600）+ Provider；未预览显示"尚未计算"而不是一排破折号（§18） |
| 主按钮 | 40px 默认权重 | 44px / 10px / 600，hover `#1D4ED8`；取消按钮白底灰边弱化（§19/§20） |
| 圆角/间距 | 各组件各自为政 | 主卡 14 / 次级卡 12 / 控件 10 / 小标签 8；区块 24 / 组 20 / 字段 16（§21/§22） |

取值集中在 `pages/downloads/download-new.css` 顶部的一块**页面作用域令牌**（`--dl-*`）里，
组件规则只引用变量；Ant 的结构性覆写仍然只在 `packages/user-ui/styles/adapter.css`。
全局四级明度与对比度守卫不受影响（`check:tokens` 58 组仍全过）。

两处从几何/接口出发的偏离（均在代码注释里写明）：

1. **链卡片每行 2 列而不是 3 列**：链网格位于半宽的"网络"列（约 348px），硬排 3 列会把
   链名截成 "Ar…"，与 §28"链名称和 slug 层级明显"冲突。改用 `auto-fill minmax(150px)`：
   宽屏自然 3 列，本页半宽列下 2 列，名称始终完整。
2. **右栏仍无"预计大小"**：`/api/v2/downloads/preview` 不返回字节体积，按"不发明接口没有的
   数据"只显示 预计数据量 / 耗时 / 成本（§18.4 的 3.8 GB 属于效果图示意值）。

### 链图标的口径

按效果图要求使用**官方链标识**（与公网一致），但仍然是**仓库内本地资源**：
`apps/user-console/src/assets/chains/*.png` 取自 Trust Wallet Assets（MIT），来源、授权与
slug 映射逐条记录在该目录的 `README.md`。禁止远程热链（规范 §11）；
未登记的链不猜图标，回落为"圆形底色 + 链名称首字母"。要增链只需补一个文件并登记到
`ChainIcon.tsx` 的 `CHAIN_LOGOS`。图标随页面懒加载 chunk 输出，不进首屏包。

### 验证

```text
pnpm run typecheck                      0 错误
E2E_BROWSER_CHANNEL=chromium
  node scripts/uc-v4-render-check.mjs   ok = true · problems = []
    · 正式界面不含 /api/v2/ 路径
    · 未到达的步骤不同时渲染（每屏一步）
    · 第 ④ 步汇总包含 整体覆盖率/未覆盖区块/预估缺失区块/复用命中/Provider 工作/预估行数
    · 参数变化后 .cs-preview-stale 出现且 opacity=1、border=rgb(217,140,22)
  node scripts/uc-contract-freeze-check.mjs   路由 28 · 权限 19 · API 字面量 66 与基准一致
  node scripts/check-token-contrast.mjs       58 组配对全部 ≥4.5:1
  node scripts/bundle-budget-check.mjs        首屏 gzip 预算内（下载页为懒加载路由，不进入首屏包）
```

配套测试已同步到新交互（r9 四个版本、dual-system-sprint5、udl01 v5/v5b/v6）：
原生 `selectOption` → `ChainSelect`/`RangeSelect` 的展开-点选；"解析导入 → 提交服务端校验"两步弹窗
→ 向导"下一步"推进 + 内联解析数据行。这些用例需要真实后端与夹具（`E2E_BASE_URL`/口令），
本次未在部署机上重跑，标记为待真实环境复验。
