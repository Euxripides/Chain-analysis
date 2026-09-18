# 用户端重构 · 页面实施简报（Agent 施工契约）

本文是 `docs/user-console-frontend-redesign-blueprint.md` 的执行附录，供页面迁移时逐条核对。
基础层（设计令牌、共享组件、Shell、命令面板、通知中心）已完成，**页面只允许消费，不允许再复制一套样式**。

## 1. 绝对不可改变的契约（蓝图 §2 / §19）

1. **API**：路径、方法、请求体、响应体、错误码、分页方式、SSE 事件格式一律不动。
2. **权限**：权限名、`ROUTE_PERMISSIONS`、`Guard`、导航裁剪规则不动。
3. **状态机**：案件 / 调查 / Finding / Evidence / 报告 / 下载 / 任务 / 导出 / Canvas / MFA / Session 的语义不动。
4. **数据**：不把真实数据换成静态演示数据；不用前端推算值冒充服务端结果。
5. **精度**：地址、交易哈希、合约地址**界面可视觉省略，但复制值、`title`、详情、提交值必须完整**；
   金额/数量/时间/比例保持接口精度，禁止为动画转成可能丢精度的 JS `number`。
6. **写操作**：继续使用现有 CSRF、`Idempotency-Key`、`If-Match`/revision、权限与审计链路。
7. **提示通道**：所有警告/说明/成功/错误/风险/确认**只能走 `useDialog()`**；
   禁止在页面里新增横幅、提示卡、警告条、说明段落（AGENTS.md、`docs/code-standards.md`）。
8. **测试选择器**：现有 `data-testid`（`table-summary`、`finding-*`、`workspace-switcher`、
   `server-verdict-summary` 等）、可访问名称、关键按钮文字不得删除或改名。
9. **业务 hook**：`useAsync` / `useAction` / `useApi` / 下载导入 Worker / SSE hooks / `command` 调用
   **只包裹，不改写**。请求参数、取消、revision、错误处理语义保持原样。
10. 不得用 `any` 绕过模型错误。

## 2. 页面迁移的标准动作

每个页面按下面顺序改造，**一次只动表现层**：

1. `PageHeader` 增加 `breadcrumb` / `status` / `meta` / `primaryAction` / `secondaryAction`。
   危险操作不能成为默认主按钮。
2. 列表页把裸 `<table>` 或旧 `s4-list` 换成 `DataTable`（保留原有加载/错误/空状态语义），
   或换成 `.cs-rowcards` 卡片列表（<900px）。
3. 章节用 `PageSection` 包住，标题 + 计数 + 操作统一。
4. 筛选区用 `FilterBar` + `FilterChip` / `SegmentedControl` / 原 `<select className="select">`（保留标签文字）。
5. 运行中的任务用 `CompactProgress` / `LinearProgress` / `PipelineProgress`；
   **服务端没有总量时必须传 `percent={null}`（indeterminate），不得伪造百分比**。
6. 详情页两栏用 `SplitPane`（主区 + `InspectorDrawer`）。
7. 长表单底部用 `StickyActionBar`；移动端自动避开 safe area。
8. 输入框统一 `.input` / `.select` / `<SearchInput>` / `<PasswordInput>`。
9. 状态展示用 `StatusBadge` / `StatusBadgeForState`（文字 + 形状 + 颜色三通道）。
10. 哈希/地址用 `HashValue`（复制完整值 + 完整 `title`）。
11. 元数据用 `KeyValueList`（替代页面内自建 `KeyValues` 时可保留旧组件，但新代码用新组件）。
12. 动态用 `Timeline`。

## 3. 共享组件速查（`@chain/ui`）

### 结构
- `PageSection({title, count, actions, children, id, className})`
- `SplitPane({main, aside, storageKey, defaultAsideWidth, minAsideWidth, maxAsideWidth, asideLabel})`
- `InspectorDrawer({title, subtitle, actions, onClose, children, ariaLabel})`
- `StickyActionBar({children, leading})`
- `BentoGrid({children})` / `BentoCell({area: "a".."f", children})`
- `PageSkeleton({shape: "table"|"cards"|"detail"|"workbench", rows})`
- `cx(...parts)`、`useHorizontalOverflowHint<T>()`

### 基础交互
- `Button({variant: "primary"|"secondary"|"ghost"|"danger"|"icon", size, busy, busyLabel, icon})`
- `IconButton({label, icon, onClick, busy, active})`（`label` 必填 → 生成 `aria-label` 与 tooltip）
- `SearchInput({value, onChange, onSubmit, label, minLength, big, busy})`
- `PasswordInput({value, onChange, label, onEnter})`
- `Combobox({options, value, onChange, label})`
- `MultiSelect({options, values, onChange, label, max})`、`DatasetSelector({datasets, values, onChange, max})`
- `Checkbox` / `Switch` / `RadioGroup` / `RowCheckbox` / `SelectAllCheckbox`
- `DateTimeRange({from, to, onFromChange, onToChange, label})`
- `BlockRange({from, to, onFromChange, onToChange, label})`
- `FileDropzone({onFiles, accept, multiple, label, hint, progress, onCancel, fileName, busy})`
- `ChainSelector({chains, value, onChange, label})`

### 导航与筛选
- `Tabs({items, value, onChange, ariaLabel})` + `TabPanel({tabKey, children})`
- `SegmentedControl({items, value, onChange, ariaLabel})`
- `FilterBar({children, onClear, showClear, ariaLabel})` / `FilterChip({children, active, onClick, onRemove})`
- `SortMenu({options, value, onChange, direction, onDirectionChange})`
- `Pagination({page, pageCount, totalLabel, onPage, busy})`
- `Stepper({steps, current, onJump})`
- `BulkActionBar({count, children, onClear})`

### 数据展示
- `Card({title, actions, flush, variant: "default"|"interactive"|"selected"|"flat"})`
- `MetricCard({label, value, unit, status, tone, href, onActivate, sourceNote, source})`、`Metric`（兼容）
- `StatusBadge({tone, children})` / `statusToneFor(value)` / `StatusBadgeForState({value})`
- `EntityLink({value, href, label, copyable})`
- `HashValue({value, head, tail, wrap, copyable, href, label})` / `CopyButton({value, label})`
- `Amount({rawAmount, decimals, displayAmount, unit, fiat, direction})`（`fiat` 为空显示 `—`）
- `KeyValueList({items: {label, value, wide?}[]})`
- `Timeline({items: {key, title, meta, tone, icon, aside}[]})`
- `MetaLine({items})`
- `EvidenceRail({items, title})`
- `DataTable({columns, rows, rowKey, loading, error, empty, onRetry, onRowActivate, rowActivateLabel, selectable, selectedKeys, onSelectedChange, rowTestId, caption, emptyAction})`
- `EmptyState` / `ErrorState` / `SkeletonRows({rows, columns, widths})` / `NoPermissionState`
- `RelativeTime({value})`

### 进度（蓝图 §9）
- `LinearProgress({percent, state, label, valueText, size})` ← `percent={null}` 即 indeterminate
- `CompactProgress({percent, state, label, waitingFor})`
- `ProgressRing({percent, state, size, thickness, label})`
- `PipelineProgress({stages: {key, label, state, meta, percent}[], title})`
- `HierarchicalProgress({nodes: {key, label, percent, state, meta, children}[]})`
- `StreamingRate({metrics: {key, label, value, hint}[]})`
- `ProgressGlyph({state})`、`progressStateFor(value)`、`PROGRESS_STATE_LABEL`、`useThrottledValue(value, ms)`
- `ParseProgressBody({...})`（作为 `dialog.open({body})` 的内容，不自建弹窗）

### 页面内保留的业务兼容层
`../sprint4/common` 与 `../sprint5/common` 里的 `useAsync` / `useAction` / `useApi` / `KeyValues` /
`DeepTabs` / `formatTime` 属于业务兼容层，**签名与语义不变**，可以继续调用。

## 4. 可用 CSS 类（`apps/user-console/src/styles/`）

| 类 | 用途 |
|---|---|
| `.cs-shell__*` | Shell 结构（不要在新页面里使用） |
| `.cs-page--reading` | 表单/阅读型页面收窄到 760px |
| `.cs-page--wide` | 列表/图谱/工作台显式全宽 |
| `.cs-workbench` | 工作台三栏网格（`grid-template-columns: 280-340px 1fr 320-380px`） |
| `.cs-workbench__stages` / `__main` / `__evidence` | 三栏子区 |
| `.cs-bento` / `.cs-bento__a…f` | Bento 网格 |
| `.cs-rowcards` / `.cs-rowcard` / `.cs-rowcard__head/__title/__meta/__actions` | 移动端卡片列表 |
| `.cs-objecthead` / `.cs-objecthead__top/__kind/__value` | 地址/交易/Evidence 页头 |
| `.cs-meta` / `.cs-meta__item` | 元信息条 |
| `.cs-recentlist` / `.cs-recentlist__item/__value/__meta` | 最近查询列表 |
| `.cs-search-hero` / `.cs-search-hero__row/__input/__big` | 查询入口 |
| `.cs-chart-frame` | 图表容器（自身滚动，不撑破页面） |
| `.cs-json`（sprint5.css 已有 `.s5-json`） | JSON / 原始内容查看器 |
| `.table-scroll` | 表格滚动容器（自动带左右边缘提示） |
| `.cs-stagger` | 首屏 stagger（只对前 8 项生效） |
| `.cs-flash` | 数值变化时的轻微背景闪现（用于不可安全转换的高精度值） |

## 5. 响应式硬约束（蓝图 §13）

- 断点：**390×844、768×1024、1024×768、1440×900**，补测 1920×1080、2560×1440。
- `document.scrollWidth <= document.clientWidth + 1`（页面级横向溢出为 0）。
- 宽表格只在 `.table-scroll` 内滚动；<640px 表格自动转成卡片块布局。
- 固定/吸顶元素不得遮挡锚点、焦点与最后一行内容。
- 移动端触摸目标 ≥44×44；输入框字号 ≥16px（已由 CSS 兜底）。
- 禁止浏览器缩放限制。

## 6. 每个页面的自查清单

- [ ] 首次加载 = 与最终布局同形状的 skeleton（不是空白）。
- [ ] 有数据 / 空数据（保留筛选条件 + 可执行动作）/ API 错误 + 重试 三态齐全。
- [ ] 无权限走 `NoPermissionState`，不伪装 404。
- [ ] 最长标题、最长地址、最长哈希、大数字不换行溢出、不丢精度。
- [ ] 所有 icon-only 控件有 `aria-label` + tooltip。
- [ ] 状态同时用文字、形状、颜色表达。
- [ ] 键盘可完成全部功能；Tab 顺序合理；Escape 关闭浮层。
- [ ] `prefers-reduced-motion: reduce` 下动画关闭但状态仍可读。
- [ ] 浏览器 console 无 error。
- [ ] 页面级横向溢出为 0。
- [ ] 没有页面内提示横幅 / 提示卡 / 常驻说明条。
