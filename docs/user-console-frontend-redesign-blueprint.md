# ChainSight 用户前端重构设计与实施蓝图

## 1. 文档用途

本文是 `frontend/apps/user-console` 的完整前端重构施工规范，供开发 Agent 直接按章节实施。范围包括用户端全部页面、共享组件、进度展示、通知与弹窗、加载/空/错误状态、动画、响应式、可访问性和验收测试。

本次重构的目标是把现有用户端从普通管理后台改造成高密度、强交互、可连续工作的区块链调查工作台，同时保持现有功能、接口、权限、审计、数据精度和操作结果不变。

本文只覆盖用户端：

- `frontend/apps/user-console/**`
- 用户端使用的 `frontend/packages/ui/**`
- 用户端使用的 `frontend/packages/design-tokens/**`
- 与用户端有关的 Playwright 测试

不得主动重构 `frontend/apps/admin-console/**`。共享组件发生视觉变化时，必须用用户端作用域或可选 variant 隔离，不能让后台页面被动改变。

## 2. 不可突破的功能边界

重构属于表现层和交互层改造，不是业务重写。以下内容必须保持：

1. 不修改现有 API 路径、请求方法、请求体、响应体、错误码、分页方式和 SSE 事件格式。
2. 不修改现有权限名称、`ROUTE_PERMISSIONS`、`Guard`、导航裁剪规则和服务端鉴权逻辑。
3. 不修改案件、调查、Finding、Evidence、报告、下载、任务、导出、Canvas、MFA 和 Session 的状态机。
4. 不把真实数据替换为静态演示数据，不用前端推算值冒充服务端结果。
5. 不截断地址、交易哈希、合约地址等底层值。界面允许视觉省略，但复制、title、详情和提交值必须保留完整原文。
6. 金额、数量、时间和比例保持接口精度；禁止为了动画先转为可能丢失精度的 JS `number`。
7. 所有写操作继续使用现有 CSRF、`Idempotency-Key`、`If-Match`/revision、权限和审计链路。
8. 所有警告、说明、成功、错误、风险和确认信息继续通过 `Dialog` 呈现；禁止新增页面内提示横幅、警告条、说明卡或常驻通知条。
9. 现有 `data-testid`、可访问名称、关键按钮文字和测试依赖的语义选择器原则上不得删除。确需调整时，先扩充测试，再改实现。
10. 不以构建成功或 HTTP 200 作为完成。必须验证真实页面、真实交互、真实状态和桌面/移动布局。

## 3. 当前技术基线

继续使用现有技术，不为视觉重构更换框架：

- React 19 + TypeScript + Vite
- React Router
- TanStack React Query
- Zustand
- Motion 12
- XYFlow
- ECharts
- AG Grid
- Lucide React
- 共享包 `@chain/ui`、`@chain/design-tokens`、`@chain/api-client`

Motion 已存在，优先使用 `motion/react` 完成布局、进入退出、共享元素和手势反馈。不要同时引入 GSAP、Anime.js 等第二套通用动画系统。XYFlow、ECharts、AG Grid 必须继续按路由懒加载，不能进入用户端公共首屏包。

## 4. 产品与视觉定位

### 4.1 产品定位

产品名称：ChainSight。

核心用户：区块链资金调查人员、案件分析人员、复核人员和需要下载/导出链上数据的业务人员。

主要使用场景：长时间桌面工作、频繁在案件、地址、交易、证据、任务和报告之间切换，需要快速识别状态并保持上下文。

### 4.2 设计方向

采用“取证操作台”视觉方向：

- Linear 式清晰层级和快速操作。
- 专业调查软件的对象、关系、证据和时间组织方式。
- Bento 式首页，但模块必须表达真实业务优先级，不做装饰性拼图。
- 关系图谱和资金路径作为标志性交互。
- 深色墨黑导航承载全局定位，冷白工作区承载长时间阅读。
- 动画用于说明关系、状态变化和空间连续性，不让所有元素持续运动。

界面不能做成：

- 通用 Ant Design 管理后台换皮。
- 满屏玻璃拟态或霓虹赛博朋克。
- 大面积渐变、粒子、自动漂浮卡片。
- 只适合展示、不适合长时间阅读数据的宣传页。
- 每张卡片都上浮、缩放、闪光的动画集合。

### 4.3 标志性设计

唯一重点视觉是“动态资金路径”：地址、交易、资金方向、Evidence 和 Finding 在图谱或局部路径组件中保持空间连续性。用户从表格选中一条交易、打开地址检查器、进入 Canvas 或回到调查工作台时，应看到同一对象被持续高亮，而不是每次重新建立上下文。

其余区域保持安静、精确、高密度。

## 5. 信息架构与导航

保留当前路由，不改变深链接。侧栏按以下分组显示，仍按权限动态裁剪：

1. 工作台
   - 我的概览 `/home`
   - 全局搜索 `/search`
2. 案件与调查
   - 案件中心 `/cases`
   - 调查任务 `/investigations`
   - 资金图谱 `/graph`
3. 数据工具
   - 地址与交易查询 `/explorer`
   - 数据下载 `/downloads/new`
   - 任务中心 `/jobs`
   - 导出中心 `/exports`
4. 协作
   - 待我处理 `/inbox`
   - 报告中心 `/reports`
   - 通知 `/notifications`
5. 个人
   - 个人资料与安全 `/profile`

### 5.1 桌面 AppShell

- 展开宽度 264px，收起宽度 72px。
- 左侧导航固定，主体独立滚动。
- 品牌区显示 ChainSight、产品标识和当前系统“用户调查工作台”。
- 当前导航项使用左侧 3px 指示条、浅色背景和高对比文字，不只依靠颜色。
- 收起后所有图标提供 tooltip，当前项仍有形状标记。
- 顶栏高度 56px，包含工作区切换、全局搜索入口、通知、用户菜单。
- 顶栏滚动时使用轻微半透明和模糊；不允许遮挡页面标题或焦点元素。
- 页面主体最大内容宽度不固定为窄列。列表/图谱/工作台页面使用全部可用宽度，表单页面使用合适的阅读宽度。

### 5.2 移动 AppShell

- `<900px` 使用抽屉导航，背景遮罩覆盖工作区。
- 顶栏保留菜单、当前页面短标题、通知和用户头像。
- 工作区切换放入用户菜单，避免顶栏拥挤。
- 打开抽屉后焦点进入抽屉，关闭后返回菜单按钮。
- 路由跳转后自动关闭抽屉。
- 页面不得产生全局横向滚动；宽表格只能在自己的滚动容器内滚动。

### 5.3 全局命令面板

新增 `CommandPalette`，快捷键为 `Ctrl+K` / `Cmd+K`，但不替代现有导航和页面操作。

命令来源必须由权限和现有路由生成：

- 跳转页面。
- 搜索地址、交易哈希、案件和调查。
- 新建案件、发起调查、创建下载。
- 打开最近访问对象。

要求：

- 无权限命令不出现。
- 输入 250ms debounce；旧请求可取消。
- 键盘上下选择、Enter 执行、Escape 关闭。
- 结果分组显示类型、标题、完整标识摘要和目标页面。
- 移动端用全屏 Dialog 呈现。
- 搜索失败、无结果和权限变化通过 Dialog 或面板内部业务状态呈现；不得用页面横幅。

### 5.4 路由、权限与源文件清单

以下路由是重构前后必须保持的公开契约。路由前缀由当前 Vite/Nginx 部署决定，表内使用应用内部路径。

| 路由 | 页面源文件 | 权限/行为 |
|---|---|---|
| `/` | `App.tsx` | 重定向 `/home` |
| `/login` | `pages/LoginPage.tsx` | 未登录可见；已登录重定向 `/home` |
| `/home` | `pages/HomePage.tsx` | `dashboard:read` |
| `/search` | `pages/SearchPage.tsx` | `search:read` |
| `/cases` | `pages/CasesPage.tsx` | `case:list` |
| `/cases/new` | `pages/CaseNewPage.tsx` | `case:create` |
| `/cases/:caseId/:tab` | `pages/CaseDetailPage.tsx` | `case:read` |
| `/investigations` | `pages/InvestigationsPage.tsx` | `investigation:list` |
| `/investigations/new` | `pages/InvestigationNewPage.tsx` | `investigation:create` |
| `/investigations/:id/workbench` | `pages/InvestigationWorkbenchPage.tsx` | `investigation:read` |
| `/investigations/:id/findings` | `pages/FindingsPage.tsx` | `finding:list` |
| `/investigations/:id/evidence/:evidenceId` | `pages/EvidenceDetailPage.tsx` | `evidence:read` |
| `/graph` | `pages/CanvasListPage.tsx` | `canvas:list` |
| `/canvases/:canvasId` | `pages/CanvasPage.tsx` | `canvas:read` |
| `/explorer` | `pages/ExplorerPage.tsx` | `explorer:read` |
| `/addresses/:chain/:address` | `pages/AddressDetailPage.tsx` | `explorer:read` |
| `/transactions/:chain/:hash` | `pages/TransactionDetailPage.tsx` | `explorer:read` |
| `/downloads` | `App.tsx` | 重定向 `/downloads/new` |
| `/downloads/new` | `pages/DownloadNewPage.tsx` | `download:create` |
| `/jobs` | `pages/JobsPage.tsx` | `job:list:self` |
| `/jobs/:jobId` | `pages/JobDetailPage.tsx` | `job:read` |
| `/exports` | `pages/ExportsPage.tsx` | `export:list` |
| `/inbox` | `pages/InboxPage.tsx` | `inbox:read` |
| `/reports` | `pages/ReportsPage.tsx` | `report:list` |
| `/reports/:id` | `pages/ReportDetailPage.tsx` | `report:read` |
| `/notifications` | `pages/NotificationsPage.tsx` | `notification:list` |
| `/profile` | `pages/ProfilePage.tsx` | `profile:read` |
| `*` | `App.tsx` | 页面不存在状态 |

开发 Agent 必须以 `App.tsx` 当前实现为最终核对来源。如果实施期间仓库新增了用户端路由，应先把新路由补入本文的施工清单，再完成对应设计和验收，不得让新页面落回旧视觉系统。

## 6. 设计令牌

先扩充 `packages/design-tokens/tokens.css`，组件不得散落硬编码颜色和时长。

### 6.1 颜色

| 语义 | 建议值 | 用途 |
|---|---:|---|
| Ink 950 | `#0A0E14` | 侧栏最深背景 |
| Ink 900 | `#111720` | 侧栏表面、暗色浮层 |
| Graphite 900 | `#111827` | 主文字 |
| Slate 600 | `#596273` | 次级文字，必须通过 4.5:1 |
| Canvas 50 | `#F5F6F8` | 页面底色 |
| Surface | `#FFFFFF` | 卡片、表单、弹窗 |
| Border | `#E3E6EB` | 普通边框 |
| Indigo 600 | `#4353E6` | 主操作、焦点、当前对象 |
| Cyan 500 | `#0EA5B7` | 实时数据、链路流动 |
| Amber 600 | `#B45309` | 等待、复核、注意 |
| Green 700 | `#067647` | 成功、完成、验证 |
| Red 700 | `#B42318` | 失败、危险、取消 |

颜色必须通过 token 暴露：背景、表面、文字、边框、交互、状态、图谱节点和图表序列分别命名。组件不直接写十六进制值。

### 6.2 字体

- 界面正文：`IBM Plex Sans`、`Noto Sans SC`、`Microsoft YaHei`、system-ui。
- 地址、哈希、金额、区块、Trace：`IBM Plex Mono`、Consolas、monospace。
- 字体若需要网络下载，改为项目内自托管；不能依赖生产环境访问 Google Fonts。
- 正文最小 13px；主要正文 14px；输入框 14px；移动端输入不得低于 16px，避免浏览器自动缩放。
- 数字统一 `font-variant-numeric: tabular-nums`。

### 6.3 间距与圆角

- 4px 基线：4、8、12、16、20、24、32、40、48。
- 普通控件圆角 8px，卡片 12px，主面板 16px，Dialog 16px。
- 不允许所有小元素都使用药丸形。药丸只用于状态、筛选 chip 和短标签。
- 桌面卡片内边距 20–24px，移动端 16px。

### 6.4 阴影与层级

- 卡片默认以边框分层，阴影很轻。
- 抽屉、Popover、命令面板、Dialog 才使用明显阴影。
- 统一层级：sticky 20、rail 30、popover 50、command 800、dialog 1000、dialog queue 1010。
- 不允许通过随意增加 `z-index: 9999` 解决遮挡。

## 7. 动效系统

### 7.1 动效等级

| 等级 | 时长 | 使用范围 |
|---|---:|---|
| Instant | 80–120ms | 按压、checkbox、图标反馈 |
| Fast | 140–180ms | hover、focus、tooltip、菜单 |
| Standard | 200–280ms | 抽屉、折叠、筛选区、卡片状态变化 |
| Spatial | 320–480ms | 页面局部共享元素、检查器、图谱聚类展开 |

进入使用 `ease-out`，退出比进入快约 20%，布局变化使用 spring 或 Motion layout。禁止全局统一 500ms 以上的慢动画。

### 7.2 必须实现的动画

- 页面主区域切换：淡入 + 8px 位移，180–240ms；浏览器后退不做夸张转场。
- 侧栏展开/收起：宽度与内容 opacity 同步，图标位置保持连续。
- 当前导航背景：`layoutId` 平滑移动。
- 卡片/列表首次加载：最多前 8 项使用 25–40ms stagger；长列表后续项不 stagger。
- 数字变化：只对可安全转换的统计数值做短暂滚动；金额和高精度值直接更新并用轻微背景闪现。
- Dialog：遮罩淡入，内容缩放 `0.98 → 1` 并上移 6px。
- 右侧检查器：从右侧进入，同时主画布使用 layout 动画缩放，不覆盖关键内容。
- 进度变化：宽度平滑追踪服务端值；状态改变时图标和文字同步变更。
- XYFlow：选中节点、展开聚类、聚焦路径使用 spring；只有 RUNNING 路径显示低速方向粒子。
- 图表：首次进入绘制，筛选变化做数据 morph；轮询刷新不得每次重新播放完整入场动画。

### 7.3 禁止的动画

- 表格每次分页时整表飞入。
- 地址、哈希或证据正文持续闪烁。
- 所有卡片无限浮动。
- 装饰性背景粒子常驻运行。
- 使用 `width/height/top/left` 驱动高频动画；优先 transform 和 opacity。
- loading 完成后先清空再重绘造成闪屏。

### 7.4 Reduced Motion

`prefers-reduced-motion: reduce` 时：

- 所有非必要进入、视差、粒子和共享元素动画关闭。
- 进度仍立即显示最新数值。
- Dialog、抽屉、菜单仍可操作，但接近即时出现。
- 图谱自动播放关闭，用户仍可点击逐步查看路径。
- 功能状态不得只靠动画表达。

## 8. 共享组件完整清单

所有页面先使用共享组件，不允许每个页面复制一套样式。

### 8.1 结构组件

1. `AppShell`
   - 桌面侧栏、移动抽屉、顶栏、工作区、通知、用户菜单。
2. `PageHeader`
   - 面包屑、标题、状态、摘要、主操作、次操作。
   - 标题区在移动端纵向排列；危险操作不能成为默认主按钮。
3. `PageSection`
   - 统一章节标题、计数、操作和内容间距。
4. `SplitPane`
   - 主区 + 检查器，可拖动宽度；记忆仅限本地视觉偏好。
5. `InspectorDrawer`
   - 地址、交易、Evidence、任务快速预览。
6. `StickyActionBar`
   - 长表单和批量操作；必须避开移动端 safe area。
7. `Tabs` / `SegmentedControl`
   - 支持键盘左右切换、深链接和明确选中态。

### 8.2 基础交互组件

必须覆盖：

- `Button`：primary、secondary、ghost、danger、icon；default/hover/active/focus/disabled/busy。
- `IconButton`：必须有 `aria-label` 和 tooltip。
- `Input`、`PasswordInput`、`SearchInput`、`Textarea`。
- `Select`、`Combobox`、`MultiSelect`。
- `Checkbox`、`Radio`、`Switch`。
- `DateTimeRange`、`BlockRange`。
- `FilterChip`、`FilterBar`、`SortMenu`。
- `CopyButton`：复制完整值，成功结果继续走 Dialog。
- `FileDropzone`：拖放、文件选择、取消解析、失败文件和解析进度。
- `AddressInputList`：批量地址、链识别、错误定位、去重结果。
- `ChainSelector`、`DatasetSelector`。
- `Pagination`、cursor 加载更多。

控件高度：紧凑 32px、默认 40px、移动触摸目标至少 44×44px。不能只依赖 hover 暴露关键操作。

### 8.3 数据展示组件

- `Card`：default、interactive、selected、flat；避免每张卡都 hover 上浮。
- `MetricCard`：数值、单位、状态和跳转；数据来源说明由 Dialog 展示。
- `StatusBadge`：保留形状 glyph，不只依赖颜色。
- `EntityLink`：案件、调查、地址、交易、任务、导出、Evidence。
- `HashValue`：视觉缩略、完整复制、完整 title、monospace。
- `Amount`：原始精度、符号、方向、法币值为空时显示 `—` 而不是 0。
- `KeyValueList`：元数据和 provenance。
- `Timeline`：案件动态、任务事件、审批记录。
- `EvidenceRail`：证据列表、类型、状态、完整 hash、当前选中项。
- `DataTable`：排序、筛选、选择、批量操作、键盘行激活、空状态、窗口化。
- `TreeTable`：地址→Dataset→Shard、调用树。
- `ChartPanel`：ECharts 的标题、legend、tooltip、空状态和数据时间。
- `GraphCanvas`：XYFlow 的 minimap、缩放、适配、框选、路径聚焦、检查器。

### 8.4 DataTable 规范

- 表头吸顶，第一关键列可按页面需要冻结。
- 默认每页窗口不超过 100 个 DOM 行。
- 行点击只负责选择/预览，详情使用明确链接或 Enter。
- 操作列不因横向滚动消失；桌面可固定右侧，移动端进入行操作菜单。
- 批量选择后出现上下文操作栏，未选择时不占空间。
- 加载时使用保持列宽的 skeleton，不能整体塌缩。
- 空结果显示当前筛选条件和可执行动作。
- 错误状态显示重试操作；详细错误继续通过 Dialog。
- 表格横向滚动区域要有左右边缘提示，不产生页面级滚动。

### 8.5 现有共享组件兼容矩阵

下列导出当前已被页面或测试使用。允许拆分内部文件，但必须保留原导出名称和行为，直到全部调用方迁移完成。

| 现有导出 | 重构要求 |
|---|---|
| `AppShell` | 增加 user variant 和动画，不改变 `navGroups`、`user`、`onLogout` 语义 |
| `GlobalSearchBox` | 可由 CommandPalette 入口替代视觉位置，但保留受控搜索行为和最小字符限制 |
| `BackLink` | 统一返回按钮样式，保持真实路由导航 |
| `RoleScopeNote` | 保留权限/范围图标语义；若替换，先迁移全部调用方 |
| `PageHeader` | 扩展 breadcrumb/status/actions，不破坏原 `title/subtitle/actions` API |
| `Card` | 增加 variant，保留 `title/actions/flush` |
| `Metric` | 内部迁移到 `MetricCard`，旧组件继续导出 |
| `Field` | 扩展 required/error/description，保留 label 与输入关联 |
| `StatusBadge` / `statusToneFor` | 保留状态映射和 glyph；不得只显示颜色 |
| `EntityLink` | 保留完整实体值、复制和目标路由行为 |
| `Amount` | 保留高精度字符串处理和方向语义 |
| `EmptyState` | 仅表达真实业务空状态，保留 action 插槽 |
| `ErrorState` | 保留安全错误文案和重试动作，详细信息走 Dialog |
| `SkeletonRows` | 内部可替换为多种 skeleton，旧参数继续工作 |
| `NoPermissionState` | 保留 permission 显示和 fail-closed 行为 |
| `EvidenceRail` | 升级选中、折叠和检查器联动，保留 `items/title` |
| `DataTable` | 保留窗口化、分页、表格摘要和现有 render API |
| `RelativeTime` | 保留完整时间 title，显示值按当前 locale 更新 |
| `icons` | 保留兼容导出；新代码直接使用统一 Lucide 图标封装 |
| `DialogProvider` / `useDialog` | 保留唯一提示通道和队列行为 |
| `dialogInputValue` / `dialogFormValues` | 保留现有返回编码兼容，避免修改调用方业务逻辑 |
| `QrCode` | 用于 MFA；保留真实内容、尺寸、纠错和无外部请求特性 |
| `TopToast` | 不再新增直接调用；保留兼容层，逐步将调用迁入 Dialog |

页面内部已有的 `KeyValues`、`useAsync`、`useAction`、格式化函数、下载导入 Worker 和 SSE hooks 属于业务兼容层。视觉重构可以提取其展示组件，但不得顺手改写请求、缓存、取消、revision 或错误处理语义。

## 9. 进度组件系统

进度必须覆盖下载、任务、地址、Dataset、Shard、文件解析、调查步骤、导出和报告生成。

### 9.1 组件类型

1. `LinearProgress`
   - determinate：0–100%，显示百分比和数值。
   - indeterminate：服务端没有总量时使用，不能伪造百分比。
2. `CompactProgress`
   - 用于表格行，包含状态 glyph、短进度条和百分比。
3. `ProgressRing`
   - 只用于空间受限的总览，不用于长数字。
4. `PipelineProgress`
   - 阶段：排队、规划、复用、本地查询、Provider 拉取、写入、校验、导出。
   - 显示完成、当前、等待、失败、暂停状态。
5. `HierarchicalProgress`
   - Job → Address → Dataset → Shard。
   - 父层数值直接使用服务端聚合，不用前端对子层简单平均。
6. `StreamingRate`
   - rows/s、Provider rows/s、bytes/s、ETA；每个指标保留现有计算依据入口。
7. `ParseProgressDialog`
   - 大文件解析、已解析行数、有效/无效/重复地址、取消按钮。

### 9.2 状态和语义

- `QUEUED`：空心圆，灰色。
- `RUNNING`：实心圆，青色或绿色，可使用低速脉冲；文字必须显示“运行中”。
- `PAUSED`：双竖线或三角形，琥珀色。
- `WAITING_DATA` / `WAITING_REVIEW`：琥珀色并显示具体等待对象。
- `COMPLETED`：对勾，绿色，进度固定 100%。
- `FAILED`：方形或错误图标，红色；错误详情通过 Dialog。
- `CANCELLED`：斜线圆，灰色；不能显示为失败。

### 9.3 实时更新规则

- 保留现有 SSE、轮询和重连逻辑，不改变事件频率。
- UI 对高频数值做展示节流，推荐 200–500ms 合并一次渲染，但最终值必须一致。
- 断线时保留最后数据并显示静态连接状态；连接说明使用 Dialog，不在页面插入警告横幅。
- 重连不得把进度重置为 0。
- 已完成的地址、Dataset 和 Shard 不因后续事件退回 RUNNING。
- 页面不可见时降低非必要动画和图表刷新，不能停止业务订阅。
- 所有 progressbar 提供 `role="progressbar"`、`aria-valuemin/max/now` 和可理解的名称。

## 10. 通知、弹窗和反馈系统

### 10.1 唯一提示通道

继续以 `DialogProvider` 和 `useDialog()` 为提示入口。`TopToast` 不作为新功能入口；若保留，仅作为兼容包装并最终转入 Dialog 队列。

Dialog 类型：

| 类型 | 用途 | 默认操作 |
|---|---|---|
| info | 数据来源、计算方式、能力边界、详细信息 | 知道了 |
| success | 创建、更新、复制、撤销等完成结果 | 完成 |
| warning | 输入不完整、不可继续、状态限制 | 返回修改 |
| error | API、解析、任务和权限错误 | 重试/关闭 |
| confirm | 写操作、状态变更、危险操作 | 取消/确认 |

### 10.2 Dialog 视觉结构

- 顶部：类型图标、标题、关闭按钮。
- 主体：正文、键值详情、表单字段或确认词。
- 底部：次操作在左或前，主操作在右或后。
- 危险确认使用红色主操作，不能将取消按钮做成危险色。
- 最大普通宽度 520px，详情宽度 720px；移动端宽度为视口减 24px，必要时全屏。
- 主体超长时内部滚动，标题和操作保持可见。
- 弹窗出现后第一个合理控件获焦；Tab 陷阱、Escape、遮罩关闭和关闭后焦点恢复继续有效。
- 必须确认保存的一次性内容继续禁止 Escape/遮罩关闭。
- 队列逐个显示，禁止两个弹窗互相覆盖。

### 10.3 Notification Center

通知页 `/notifications` 与右上角铃铛共享数据源：

- 铃铛显示未读数量，最大显示 `99+`。
- 点击打开 360–420px Popover，展示最近 8 条。
- 每条包含类型图标、标题、对象、时间、未读标记。
- “查看全部”进入 `/notifications`。
- 标记已读、全部已读继续调用现有接口；不存在的对象通过 Dialog 告知。
- Popover 不是提示通道，只展示服务端业务通知数据。

### 10.4 操作忙状态

- 点击提交后按钮进入 busy，保留原宽度，显示 spinner 和动词，如“创建中…”。
- 防止重复点击，但不在前端假定服务端成功。
- 成功后按现有流程刷新/跳转，再显示 success Dialog。
- 失败后保留输入和用户上下文。

## 11. 加载、空、错误和权限状态

所有页面和组件必须覆盖：

1. 首次加载：结构化 skeleton，保持最终布局尺寸。
2. 局部刷新：保留旧数据，在刷新按钮或局部区域显示 busy，不清空整个页面。
3. 空数据：说明当前没有什么，并提供与权限一致的下一步操作。
4. 筛选无结果：保留筛选栏，提供清除筛选。
5. 错误：显示错误状态和重试；详细错误、Trace 和原因通过 Dialog。
6. 无权限：使用 `NoPermissionState`，显示缺少的权限名称，不伪装成 404。
7. 404：页面不存在，提供回到工作台。
8. Session 失效：清除敏感页面内容并跳转登录，保留安全的 return-to。
9. 路由懒加载：使用与目标页面相似的 PageSkeleton，不能使用空白页面。

仓库规范禁止页面内提示内容。`EmptyState`、`ErrorState` 和 `NoPermissionState` 只用于表达当前业务区域本身的状态，不得被滥用为说明、风险提示或操作结果。

## 12. 全部页面重构规格

### 12.1 登录 `/login`

页面结构：左侧品牌与动态链路图，右侧登录区。左侧动画只在桌面显示，使用抽象地址节点和资金路径，不显示虚构金额。

必须保留：账号密码、本地账号/OIDC、工作区选择、MFA 第二步、恢复码、超时、必须修改初始密码、return-to 和错误 Dialog。

交互：

- 输入框明确 label，密码可显示/隐藏。
- Enter 提交当前步骤，busy 时防重复提交。
- MFA 使用 6 格视觉输入但保持一个真实输入值，恢复码允许普通文本。
- 工作区选择继续使用 Dialog，不自动选择第一个工作区。
- 移动端隐藏品牌画布，只保留小型品牌标识和表单。
- 登录失败不抖动整个页面，只轻微强调相关字段并打开错误 Dialog。

### 12.2 我的概览 `/home`

从“指标卡 + 快捷入口”升级为 Bento 工作台：

- A 区：问候、当前工作区、数据时间、全局主操作。
- B 区：关键业务指标，按服务端 cards 原样显示。
- C 区：进行中的调查和下载任务，显示真实进度与状态。
- D 区：待我处理，包括复核、审批和失败任务。
- E 区：最近案件/地址/Canvas。
- F 区：快捷操作，继续由 `quick_actions` 和权限控制。

不得新增接口时伪造 C、D、E 数据。现有 dashboard 响应没有相应字段时，先保留可由当前接口支持的区域，并在实施记录中标为未实现，不写假数据。

### 12.3 全局搜索 `/search`

- 页面顶部使用大型搜索输入，自动聚焦但不得抢夺深链接后的用户焦点。
- 结果按案件、调查、地址、交易、报告分组。
- 桌面采用结果列表 + 右侧快速预览；移动端点击进入详情。
- 搜索词在可见文本中高亮，绝不使用未清洗 HTML。
- 空输入、少于 2 字符、加载、错误、无结果状态明确。
- 保留 250ms debounce、取消旧请求、当前工作区限制。

### 12.4 案件中心 `/cases`

- 顶部显示标题、当前结果数、新建案件按钮。
- 筛选栏：状态、关键字、更新时间、负责人；只实现接口支持项。
- 桌面为高密度表格；移动端转换为案件卡片列表。
- 行显示案件号、名称、状态、对象数、调查数、成员、更新时间。
- 点击行或 Enter 打开 `/cases/:caseId/overview`。
- 批量操作仅在现有后端支持时出现。

### 12.5 新建案件 `/cases/new`

- 单列表单最大宽度约 760px，右侧不放无意义空卡。
- 分为基本信息、成员/范围和提交区；仅展示当前已有字段。
- 底部 StickyActionBar 提供取消与创建。
- 验证错误靠近字段；操作结果和风险仍通过 Dialog。
- 创建成功保持现有跳转和审计行为。

### 12.6 案件详情 `/cases/:caseId/:tab`

保留并重构全部 tab：概览、对象、调查、证据、报告、成员、动态。

统一页头显示案件名称、案件号、状态、更新时间和允许的操作。tab 使用 URL 深链接，刷新后保持当前 tab。

- 概览：关键字段、对象/调查/证据/报告数量和最近动态。
- 对象：地址/交易/其他 Subject 表格，新增对象使用 Dialog 表单或现有流程。
- 调查：调查列表和发起调查按钮。
- 证据：Evidence 列表、状态、来源和 hash。
- 报告：revision、审批状态和生成报告。
- 成员：成员、角色、加入时间和现有成员操作。
- 动态：业务 Timeline，显示动作、操作者、对象和时间。

### 12.7 调查任务 `/investigations`

- 高密度列表，显示目标、关联案件、Subject、模式、状态、Finding/Evidence 数、更新时间。
- 顶部筛选状态、模式、案件和关键字。
- RUNNING 行显示 CompactProgress 或当前步骤；没有真实进度时只显示状态，不伪造百分比。
- 点击进入 workbench，主操作是“发起调查”。

### 12.8 发起调查 `/investigations/new`

- 三步结构：选择案件与目标 → 配置 Subject/模式 → Coverage 预览与确认。
- 步骤条表达真实填写顺序，用户可以返回已完成步骤。
- Coverage 预览结果使用数据面板；计算说明通过 Dialog。
- 参数变化后明确标记预览已失效，并要求重新调用真实 preview。
- 提交前确认 Dialog 显示案件、地址、链、模式、范围和复用结果。

### 12.9 调查工作台 `/investigations/:id/workbench`

这是用户端核心页面。桌面使用三栏：

```text
┌─────────────── PageHeader / commands ───────────────┐
├──────────────┬───────────────────────┬──────────────┤
│ 阶段与步骤   │ Finding / 运行上下文  │ Evidence Rail│
│ 280-340px    │ 弹性主区              │ 320-380px    │
└──────────────┴───────────────────────┴──────────────┘
```

- 左栏：PipelineProgress、run、checkpoint、pending data jobs。
- 主栏：Finding 列表、当前 Finding 摘要、关联对象和调查输出。
- 右栏：EvidenceRail，可折叠，可打开 Evidence 检查器。
- Header 命令继续按权限和状态显示启动、暂停、恢复、重试、提交复核、取消。
- 危险操作保留原因输入、revision 和确认。
- SSE/刷新更新局部区域，不让整个页面跳动。
- `<1100px` 改为主栏 + 可切换抽屉；移动端按“进度 / Findings / Evidence”三个 tab 展示。

### 12.10 Findings 复核 `/investigations/:id/findings`

- 桌面左侧 Finding 列表，右侧详情；使用 SplitPane。
- 列表支持状态筛选、类型筛选和关键字。
- 详情显示标题、类型、nature、confidence、算法版本、状态和关联 Evidence。
- Confirm、Reject、Supersede 保留现有权限、原因、异常说明和确认 Dialog。
- Evidence 验证按钮和现有 `data-testid` 保留。
- 移动端先显示列表，选择后进入全屏详情，并提供返回列表。

### 12.11 Evidence 详情 `/investigations/:id/evidence/:evidenceId`

- 页头显示标题、类型、状态和允许的复核操作。
- 主区显示证据内容，右侧显示元数据和 Provenance。
- 内容 hash、来源对象、采集时间、算法版本完整保留。
- 质疑/验证操作继续要求原因和确认。
- 原始内容较长时使用可折叠代码/文本查看器，不默认折叠关键字段。

### 12.12 资金图谱列表 `/graph`

- Canvas 列表显示名称、关联案件、节点/边数量、revision、创建者、更新时间。
- 支持新建、打开、筛选和排序；仅呈现现有接口允许的操作。
- 卡片只用于最近/固定 Canvas；完整结果使用表格或紧凑列表。
- 小型图谱预览可以静态渲染，禁止每张卡持续动画。

### 12.13 Canvas `/canvases/:canvasId`

桌面采用全宽调查画布：

- 顶部工具栏：选择、框选、平移、添加对象、布局、适配、缩放、撤销/重做、保存。
- 左侧可折叠对象/图层面板。
- 中央 XYFlow 画布。
- 右侧 Inspector/EvidenceRail。
- 底部可选时间轴和路径播放控制。

节点：地址、交易、合约、实体、Evidence 使用不同形状和图标；颜色只是辅助。边显示方向、资产和金额，过长标签简化但可查看完整值。

必须保留：revision/并发冲突、Graph Patch、保存、快照、节点/边操作、移动端查看模式。大于 2,000 节点启用聚类/LOD；不能一次渲染全部复杂标签。

### 12.14 地址与交易查询 `/explorer`

- 查询区作为主工作区入口，链选择与输入并排。
- 自动识别地址或交易哈希，但最终仍使用现有判断和接口。
- 最近查询显示为可删除的紧凑列表，只保存在现有本地范围。
- 查询确认、链列表失败和删除记录继续使用 Dialog。
- 搜索成功按现有路由进入地址或交易详情。

### 12.15 地址详情 `/addresses/:chain/:address`

保留全部现有功能，并重组为：

- Header：链、完整地址、复制、标签/案件、数据时间。
- Overview：当前余额、历史净流、交易数、transfer 数、首次/最后活动；字段来源必须明确。
- Activity：交易、Token Transfer、NFT、Logs 等现有 tab。
- Fund Flow：ECharts/XYFlow 资金关系和对手方。
- Analysis：现有高级分析、关联钱包、模式和证据。
- Side Inspector：选中交易/对手方详情。

当前余额与 observed net flow 必须分开。USD 不可用显示 `—`。长表窗口化，图表筛选与表格联动。

### 12.16 交易详情 `/transactions/:chain/:hash`

- Header 显示完整交易哈希、链、状态、时间和复制。
- Overview：from、to、value、fee、block、nonce、method。
- Decode：Method、参数和本地 ABI 解码。
- Logs：事件表格。
- Trace：概要、调用树、补取操作。
- Object：关联案件、调查或 Evidence。

调用树使用 TreeTable，可折叠但默认展开关键失败路径。Trace 补取继续使用确认和结果 Dialog。原始 input/log/data 提供安全复制，不执行 HTML。

### 12.17 新建数据下载 `/downloads/new`

保留现有五部分业务流程，但改为可理解的 Stepper：

1. 对象/地址导入。
2. 链与区块/时间范围。
3. Dataset。
4. 复用策略。
5. Coverage 与成本预览。

桌面使用左侧步骤/表单、右侧 sticky 预览摘要；移动端预览移到提交前。

必须覆盖：粘贴地址、文件导入、Web Worker 解析、取消解析、链元数据、最多 100 地址、最多 6 Dataset、范围验证、preview 失效、reuse/refresh、真实 Coverage、成本、创建确认和错误 Trace。

解析进度使用 `ParseProgressDialog`；地址错误能定位到具体行。参数变化后清除旧 preview 的可提交资格，但可保留灰化的上次结果供对照。

### 12.18 任务中心 `/jobs`

- 顶部统计当前运行、排队、失败、完成数量，必须来自当前接口可证明的数据。
- 筛选：状态、链、创建时间、任务号/地址。
- 表格显示任务号、链、对象、Dataset、状态、Progress、rows/s、创建时间、操作。
- 点击行打开右侧快速预览；点击任务号进入详情。
- 批量取消继续要求原因和确认，只允许选择可取消任务。
- 失败详情、Trace 和计算方式通过 Dialog。
- 移动端使用任务卡片，进度与主操作始终可见。

### 12.19 任务详情 `/jobs/:jobId`

桌面采用顶部运行概览 + 分层 tabs：

- 概览：状态、总进度、rows、bytes、请求、成本、elapsed、ETA。
- Pipeline：阶段进度和当前活动。
- 地址进度：每地址状态、进度、rows、checkpoint。
- Dataset：每地址每 Dataset 的状态和进度。
- Shards：范围、页、cursor、重试、rows、raw object、错误。
- 结构化事件：Timeline，可打开事件详情。
- Exports：关联导出及下载状态。
- 证据链：请求、Trace、对象、文件和审计关联。

保留暂停、恢复、取消及原因输入。rows/s 与 Provider rows/s 分开展示。重连时保留最后进度。移动端 tabs 横向滚动或使用下拉，但页面本身不横向溢出。

### 12.20 导出中心 `/exports`

- 上半区为创建导出：按 Job 或按地址，显示现有格式和筛选字段。
- 下半区为导出列表：状态、来源、格式、行数、大小、hash、过期时间、操作。
- RUNNING 显示 CompactProgress；没有服务端百分比时使用 indeterminate。
- COMPLETED 提供下载；下载票据、过期和错误继续按现有接口处理。
- CSV/XLSX/JSONL/Parquet/ZIP 的格式标签使用图标+文字，不只靠颜色。
- 地址导出继续限制链和地址数量。

### 12.21 待我处理 `/inbox`

- 按优先级和时间排列待复核请求。
- 每项显示对象类型、标题、发起人、当前状态、提交时间和进入目标。
- 桌面支持右侧快速预览；移动端进入目标详情。
- 处理完成后列表做 layout 收拢，不重新加载成空白。
- 无待办时提供回到调查/报告中心的操作。

### 12.22 报告中心 `/reports`

- 显示报告名称、案件/调查、revision、状态、创建者、更新时间。
- 支持状态、案件和更新时间筛选。
- 明确区分草稿、待审批、已批准、已驳回等服务端状态。
- 点击打开报告详情，不在列表中暴露不支持的快速审批。

### 12.23 报告详情 `/reports/:id`

- Header：报告标题、revision、状态、下载/审批操作。
- 主区：报告元数据和内容/文件入口。
- 右侧：审批状态、审批记录 Timeline。
- 提交审批、批准、驳回继续使用现有输入、权限、revision 和确认。
- 多 revision 时明确当前 revision，不覆盖旧版本。

### 12.24 通知 `/notifications`

- tabs：全部、未读；只有接口支持时再增加类型筛选。
- 每条通知显示类型图标、标题、对象摘要、时间和未读点。
- 点击按现有 target 跳转；对象不可用时打开 info Dialog。
- 标记已读后平滑更新，不从列表突然消失，除非当前筛选为未读。
- 无通知时显示空状态，不使用虚构示例。

### 12.25 个人资料与安全 `/profile`

使用左侧设置导航或桌面双栏，分为：

- 基本资料。
- 修改密码。
- MFA 注册/验证/关闭/恢复码。
- 当前 Session。
- 设备。
- 退出其他设备。

安全操作不能为了视觉简化而隐藏确认步骤。QR、secret、恢复码保持现有一次性确认逻辑。Session/设备使用表格，移动端使用卡片。撤销当前 Session 后按现有流程退出。

### 12.26 未知路由和无权限页

- 未知路由显示“页面不存在”，提供返回工作台。
- 无权限显示缺少权限和返回入口。
- 不再把不存在路由描述成已经可用。
- 不提供已下线旧控制台链接。

## 13. 响应式规范

必须测试四个固定断点：390×844、768×1024、1024×768、1440×900，并补测 1920×1080 和 2560×1440 的宽屏密度。

| 宽度 | 页面策略 |
|---|---|
| `<640px` | 单列、抽屉导航、卡片列表、底部操作栏 |
| `640–899px` | 单列或 2 列，表格局部滚动，右栏改抽屉 |
| `900–1199px` | 桌面导航，工作台从三栏降为两栏 |
| `1200–1599px` | 标准桌面布局 |
| `≥1600px` | 扩展主工作区，侧栏/证据栏不无限放大 |

要求：

- 页面 `scrollWidth <= clientWidth + 1`。
- 长地址可以在详情区换行，但表格中使用视觉省略和完整 title。
- 固定/吸顶元素不能遮挡锚点、焦点和最后一行内容。
- iOS safe area 用于移动底部操作栏和全屏 Dialog。
- 不禁止浏览器缩放。

## 14. 可访问性

- 普通文字对比度至少 4.5:1，大字至少 3:1。
- 所有功能可用键盘完成。
- focus ring 始终可见，不能只通过 box-shadow 被深色背景吞掉。
- icon-only 控件有 `aria-label` 和 tooltip。
- 状态同时使用文字、形状和颜色。
- 图表提供文字摘要或对应数据表入口。
- XYFlow 节点可通过键盘选择；画布工具提供明确按钮。
- Dialog 保留焦点陷阱、Escape、焦点恢复、长内容滚动。
- 动态进度使用适度的 `aria-live`，避免每个 SSE 事件都朗读。
- 页面标题和主标题随路由正确更新。

## 15. 性能预算

- 用户端首屏 gzip JS 目标 `<350KB`。
- XYFlow、ECharts、AG Grid、XLSX 按路由/功能懒加载。
- 已缓存数据的路由切换 P75 `<1s`。
- 100 行以上的表格窗口化或服务端分页。
- 图谱超过 2,000 节点启用聚类/LOD。
- 动画只使用 transform/opacity 等合成友好属性。
- 不为每个表格行创建独立 IntersectionObserver 或高频定时器。
- 页面隐藏时降低动画和图表刷新。
- React Query key 继续包含 organization/workspace/object/filter；切换工作区清理不安全缓存。

## 16. 推荐代码结构

不要求一次搬完业务代码。先建立共享表现层，再逐页迁移：

```text
frontend/apps/user-console/src/
├─ app/
│  ├─ UserShell.tsx
│  ├─ routes.tsx
│  └─ navigation.tsx
├─ components/
│  ├─ command/CommandPalette.tsx
│  ├─ notifications/NotificationPopover.tsx
│  ├─ progress/
│  │  ├─ LinearProgress.tsx
│  │  ├─ CompactProgress.tsx
│  │  ├─ PipelineProgress.tsx
│  │  └─ HierarchicalProgress.tsx
│  ├─ entity/HashValue.tsx
│  ├─ layout/SplitPane.tsx
│  └─ layout/InspectorDrawer.tsx
├─ features/
│  ├─ dashboard/
│  ├─ search/
│  ├─ cases/
│  ├─ investigations/
│  ├─ findings/
│  ├─ evidence/
│  ├─ canvas/
│  ├─ explorer/
│  ├─ downloads/
│  ├─ jobs/
│  ├─ exports/
│  ├─ inbox/
│  ├─ reports/
│  ├─ notifications/
│  └─ profile/
├─ pages/
└─ styles/
   ├─ user-shell.css
   ├─ motion.css
   └─ responsive.css
```

共享包目标：

```text
frontend/packages/ui/
├─ AppShell.tsx
├─ Dialog.tsx
├─ primitives.tsx
├─ table/
├─ forms/
├─ feedback/
└─ layout/
```

共享组件必须提供 variant 或 scope。不要把用户端特有的资金路径、调查 Pipeline 和 Evidence 业务组件塞进通用 `@chain/ui`。

## 17. 文件级改造映射

| 当前文件 | 主要动作 |
|---|---|
| `frontend/packages/design-tokens/tokens.css` | 扩充语义颜色、动效、层级、组件尺寸；保留现有变量兼容层 |
| `frontend/packages/ui/AppShell.tsx` | 支持 user variant、通知入口、用户菜单、移动抽屉和 layout 动画 |
| `frontend/packages/ui/Dialog.tsx` | 统一图标、尺寸、busy、队列动画；保留现有 API 和可访问行为 |
| `frontend/packages/ui/primitives.tsx` | 拆分组件但保留原导出；扩展 PageHeader、Table、状态和 skeleton |
| `frontend/packages/ui/ui.css` | 按组件分层整理；禁止一次性删除旧 class |
| `frontend/apps/user-console/src/App.tsx` | 路由行为不变，引入新 Shell 和 PageSkeleton |
| `frontend/apps/user-console/src/sprint4.css` | 逐页迁移后删除重复规则 |
| `frontend/apps/user-console/src/sprint5/sprint5.css` | 逐页迁移后删除重复规则 |
| `frontend/apps/user-console/src/pages/*.tsx` | 按第 12 节逐页重构，不改数据 hooks 和 command 语义 |
| `frontend/tests/*.spec.ts` | 保留回归，增加 visual/interaction/mobile/reduced-motion 覆盖 |

禁止先删除所有旧 CSS 再重写。每迁移一个页面，确认没有旧 class 使用后再清理对应规则。

## 18. 实施顺序

### 阶段 U0：冻结基线

1. 阅读 `AGENTS.md`、`docs/code-standards.md` 和本文。
2. 运行 `git status --short`，不得覆盖现有修改。
3. 记录用户端全部路由、接口、权限、操作按钮、`data-testid` 和现有测试。
4. 保存 390、768、1024、1440 下的全部页面截图。
5. 运行并记录当前 build、用户端 Playwright、reduced-motion 和权限测试结果。

交付：`docs/implementation/user-console-redesign-baseline.md`。

### 阶段 U1：设计令牌和基础组件

1. token 扩展和 contrast 检查。
2. Button、Field、Badge、HashValue、Card、PageHeader。
3. Dialog 全状态。
4. Loading/Empty/Error/NoPermission/PageSkeleton。
5. DataTable、Tabs、SplitPane、InspectorDrawer。
6. 全套进度组件。

完成条件：组件测试、键盘、四断点和 reduced-motion 通过，旧页面仍能正常使用。

### 阶段 U2：Shell、登录和首页

1. AppShell。
2. 移动抽屉。
3. WorkspaceSwitcher。
4. NotificationPopover。
5. CommandPalette。
6. Login。
7. Home。

完成条件：登录/MFA/工作区选择/退出/权限导航无回归。

### 阶段 U3：案件和调查闭环

按顺序重构 Cases → CaseNew → CaseDetail → Investigations → InvestigationNew → Workbench → Findings → Evidence → Inbox → Reports → ReportDetail。

每完成一个页面就运行对应 Sprint 4 测试，不把所有回归堆到最后。

### 阶段 U4：数据与任务闭环

按顺序重构 Search → Explorer → AddressDetail → TransactionDetail → DownloadNew → Jobs → JobDetail → Exports。

重点验证：地址导入 Worker、真实 preview、进度、SSE、暂停/恢复、cursor、导出下载和错误 Dialog。

### 阶段 U5：图谱和个人安全

1. CanvasList。
2. Canvas。
3. Notifications。
4. Profile/MFA/Session/Device。

图谱必须单独做大数据性能测试；安全页面必须运行 MFA 和 Session 真实流程测试。

### 阶段 U6：清理与全量验收

1. 删除确认无引用的旧 CSS。
2. 检查 bundle 和重复依赖。
3. 全路由桌面/移动截图对照。
4. 全权限角色回归。
5. 真实后端交互验收。
6. 更新用户手册和测试证据。

## 19. 防止功能回归的实施规则

1. 优先包裹现有业务 hooks 和 handler，不在视觉重构中改写请求逻辑。
2. 每个页面先写“现有能力清单”，重构后逐项勾验。
3. 对写操作保留原按钮语义、权限判断、disabled 条件和确认流程。
4. 对路由保留原路径和参数名。
5. 对列表保留 cursor/分页和筛选参数。
6. 对实时页面保留 SSE 的 Last-Event-ID、重连和 scope 处理。
7. 对错误保留 `ApiError.userMessage`、Trace ID 和安全错误内容。
8. 对 DataTable 保留现有 `data-testid="table-summary"` 等测试契约。
9. 对 Findings 保留现有 `finding-*` test id。
10. 对 WorkspaceSwitcher 保留 `workspace-switcher` test id 和 audience。
11. 重构期间不能用 `any` 绕过现有模型错误。
12. 共享组件改变默认行为时，必须提供兼容 prop，并分页面迁移。

## 20. 测试与验收矩阵

### 20.1 自动测试

至少运行：

```text
pnpm run check:tokens
pnpm run build:user
pnpm run build:admin
pnpm exec playwright test -c playwright.dual.config.ts
pnpm exec playwright test tests/dual-system-sprint4.spec.ts
pnpm exec playwright test tests/dual-system-sprint5.spec.ts
pnpm exec playwright test tests/dual-system-nav-permission.spec.ts
pnpm exec playwright test tests/dual-system-cross-role.spec.ts
pnpm exec playwright test tests/dual-system-reduced-motion.spec.ts
```

如果仓库当前使用专用环境变量或配置，以现有测试说明为准，禁止为了得到绿色结果跳过真实前置条件。

### 20.2 每页必测状态

每个路由至少测试：

- 首次加载。
- 有数据。
- 空数据。
- API 错误和重试。
- 无权限。
- 最长标题、最长地址、最长哈希和大数字。
- 390、768、1024、1440。
- 键盘 Tab/Shift+Tab/Enter/Escape。
- reduced-motion。
- 浏览器 console error 为 0。
- 页面级横向溢出为 0。

### 20.3 关键业务流程

1. 登录 → 显式选择工作区 → MFA → 首页。
2. 新建案件 → 添加对象 → 发起调查 → 启动/暂停/恢复 → Finding 复核 → Evidence 验证 → 报告审批。
3. 查询地址 → 查看交易 → Trace/Logs → 加入调查或 Canvas。
4. 导入地址 → preview → 创建下载 → Job 分层进度 → 暂停/恢复 → 导出 → 下载文件。
5. Canvas 创建 → 节点/边交互 → 保存 → revision 冲突处理。
6. 资料修改 → 修改密码 → MFA → Session/设备撤销。

### 20.4 视觉验收

- 页面之间视觉语言一致，但布局按任务不同，不是所有页面复制同一套卡片。
- 信息层级在 5 秒内可识别：我在哪、当前对象是什么、状态是什么、下一步能做什么。
- 动画不阻塞点击，不让数据难以阅读。
- 进度、状态和通知在桌面/移动都清晰。
- 表格、图谱、Dialog 和抽屉无层级冲突。
- 没有页面内警告横幅、提示卡和常驻说明条。

## 21. 完成判定

用户端重构只有同时满足以下条件才能标记完成：

1. 第 12 节所有页面均已重构，不存在只改首页/登录页的情况。
2. 第 8–11 节全部组件和状态均有实际实现和页面使用证据。
3. 所有原有功能、权限、操作、接口、审计和测试选择器保持有效。
4. 四个强制断点没有页面级横向滚动、遮挡、不可关闭弹窗和操作丢失。
5. reduced-motion、键盘、焦点、颜色对比和触摸目标通过。
6. build、Playwright、真实后端业务流和文件结果均通过。
7. P0=0、P1=0、FAIL=0、NOT_VERIFIED=0。
8. 交付全部页面桌面/移动截图、测试结果、bundle 报告和已知限制。

不得使用“页面已经变好看”“组件基本完成”“构建通过”“接口返回 200”作为完成结论。

## 22. Agent 每阶段交付格式

```text
阶段：
完成页面/组件：
保留的接口与权限：
修改文件：
新增依赖：
功能行为变化：必须为无，或逐项说明并获得确认
运行测试及准确结果：
桌面证据：
移动证据：
真实数据/交互证据：
可访问性结果：
性能和 bundle 结果：
发现的问题：
回滚方式：
下一阶段：
```

任何页面存在未验证状态时，必须明确写 `NOT_VERIFIED`，不得推断为通过。
