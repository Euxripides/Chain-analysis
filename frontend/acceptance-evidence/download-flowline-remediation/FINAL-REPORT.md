# 新建数据下载 · 全量整改最终汇报（§27）

生成时间：2026-09-21T15:05Z
依据文档：`download-new-complete-remediation-spec.md`（REM-001 … REM-024）
部署绑定 commit：**25e004d**（页面源码；HEAD `e916990` 的页面/脚本源码与之逐字节相同）

---

## 27.1 权威源码与部署

| 项 | 值 |
|---|---|
| 分支 | `main`（跟踪 `origin/main`，0 落后） |
| 页面源码 commit | `25e004d`（本轮整改：`296897c` 页面实现 → `87261c0` Dialog 断言 → `25e004d` summary 判据） |
| 证据 commit | `e916990`（只新增/更新 §21 证据，不含页面源码改动） |
| 工作区状态 | 我的改动全部已提交；`git status` 剩余 6 个 tracked modified + 125 个未跟踪项**均为其他并行会话的产物**（`acceptance-evidence/dual-system-v1/sprint7/ops`、`backend/internal/app`、`frontend/test-results-*` 等），未覆盖、未清理 |
| dist index.html sha256 | `5941ec43af8305c319f746bc456f1c7c5fabb5b17526b132706345a5fead5af2` |
| served index.html sha256 | `5941ec43af8305c319f746bc456f1c7c5fabb5b17526b132706345a5fead5af2` |
| 入口 chunk | `index-CMwPyh9_.js` sha256 `a3d998110c5b477fd6e0598332514ff0d32a81f484d5cebda6d6833e439e424d`（dist 与 served 一致） |
| 页面 chunk | `DownloadNewPage-BoERlvw1.js` sha256 `54d26aae6a1189f250b832225076a8610639b01882846f74f7d3619717ee1457`（dist 与 served 一致） |
| all_equal | **true** |

受控部署：`APP=user bash scripts/s7/deploy-frontend-controlled.sh` → 14/14 PASS。
关键点：**整改后从 commit 25e004d 重新构建得到的 index.html sha 与部署前完全一致**，
证明线上服务的字节确实来自该 commit 的源码，而不是"构建即部署"或缓存产物。
回滚材料：`frontend/apps/user-console/dist.bak-20260921T145141Z`。

---

## 27.2 已改文件（逐文件）

| 文件 | 改动 |
|---|---|
| `frontend/apps/user-console/src/pages/downloads/download-new.css` | ① 令牌映射到 §7.5.2 精确值（`--dl-canvas #f6f8fb`、`--dl-surface-subtle #f8fafc`、`--dl-text-strong/-text/-text-secondary/-text-tertiary`、`--dl-border/-strong`），旧名 `--dl-surface-soft`/`--dl-text-muted` 改为别名，不另建灰色；② 删除 `.dl-flow-page::before` 的两个 radial-gradient 光晕与 `::after` 的 32px 半透明网格（整页"灰雾"来源），画布改为单一不透明色 + `background-image:none`；③ 新增实体面规则：主工作区/章节卡/右栏预览/移动操作栏 `opacity:1;filter:none;backdrop-filter:none`；④ 状态芯片 `rgba(...,.08)`/`rgba(...,.3)` 改为不透明实色令牌 `--dl-success-soft/--dl-success-border/--dl-warning-border`；⑤ 地址区结构重写为 `.dl-address__input-grid` + `.dl-address__text-field` + `.dl-address__file-field`（`grid-template-rows: 20px 96px`）与唯一触发器 `.dl-address__file-trigger`，短屏桌面统一降为 84px；⑥ 解析中进度与"取消解析"改为覆盖在触发器内部（`.dl-address__import` 绝对定位，不新增第二张卡、不改变章节高度）；⑦ `fieldset/legend` 归零浏览器默认边框与内外边距；⑧ 新增 `.sr-only`；⑨ 删除 `.dl-filebtn`、`.dl-address__importicon/importtitle/importhint`、`.dl-address__grid/__field` 等已废弃规则及对应响应式覆盖 |
| `frontend/apps/user-console/src/pages/downloads/DownloadAddressPanel.tsx` | DOM 收敛为 §7.5.3 唯一结构：`input[type=file]`（`id=download-address-file`、`class=sr-only`、`data-testid=address-file-input`、`aria-label=导入地址文件`）只渲染一次；可见触发器 `data-testid=address-file-trigger` 为 `<button>`，点击 / Enter / Space 均调用 `fileInputRef.current.click()`；删除工具行的"导入 CSV / XLSX"按钮与右侧"从文件导入"卡；工具行只保留"解析导入"与"清空"；busy 时触发器显示真实文件名 + 局部不定进度 + 取消入口 |
| `frontend/apps/user-console/src/pages/downloads/DownloadNewPage.tsx` | 文件输入改为向面板传 `fileInputRef` + `onFileChange`（不在页面侧另建 input）；移除已不用的 `IMPORT_FILE_EXTENSIONS` 引用 |
| `frontend/apps/user-console/src/import/parse-core.ts` | `IMPORT_FILE_EXTENSIONS = [".txt", ".csv", ".xls", ".xlsx"]`（顺序即 accept 顺序）；`.txt` 与 `.csv` 共用分隔文本解析路径与 NUL 字节防线；扩展名拒绝文案同步为"仅接受 .txt / .csv / .xlsx / .xls"（修掉"UI 提示支持 TXT、解析器却拒绝 .txt"的不一致） |
| `frontend/scripts/download-flowline-remediation-acceptance.mjs` | 新增 REM-021…024 真实浏览器断言（文件入口唯一性、四视口 top/bottom/height ≤1px、画布 alpha=1 且无 background-image/伪元素、fieldset 边框为 0、页面内 `<hr>` 为 0、重复 wrapper 外框为 0、文字祖先 opacity/filter/backdrop-filter 违规为 0、触发器鼠标/Enter/Space 真实打开选择器）；原始测量落盘 `visual-clarity/computed-styles.json`；Dialog 时长改为探针元素实测 220/160/160ms 断言；复用策略可见性改为真实 DOM + 可访问名扫描；Dataset 7 项结论由真实运行推导；夹具 label 与后端 `datasetLabel()` 对齐；`summary.ctas_visible` 只对 `w>=1200` 判定 |
| `frontend/tests/udl01-worker-isolation-v5/v5b/v6.spec.ts` | 文件导入定位从已删除的 `.dl-address__import` 改为 `.dl-address__file-field`（唯一触发器 + 进度 + 取消都在同一字段内） |
| `scripts/s7/run-download-flowline-remediation.sh` | §22 报告合并：Dataset 三层结论改为从本次运行的逐项 `check` 推导（不再沿用上游预填值）；`zip64_branch_hit` 由布局自检 + 真实拒绝原因 + 0 服务端调用 + 未建任务共同推导 |
| `docs/acceptance/download-new-dialog-timing.md`（新增） | REM-009 的规范修正：共享 Dialog 权威时长（面板进入 `--cs-dur-standard` 220ms / 退出 160ms / 遮罩 160ms / reduced-motion `transition:none`），以及下载页禁止覆盖 `.cs-dialog-*` 的规则与验收方式 |

---

## 27.3 REM-001 … REM-024 逐项

| ID | 状态 | 修改 / 结论 | 证据 |
|---|---|---|---|
| REM-001 | **PASS** | 规范描述的"主分支只有 download.types.ts"已不成立：Flowline 实施文件在 `569c6a5` 进入 `main`，本轮整改文件在 `296897c`；`git log -- frontend/apps/user-console/src/pages/downloads` 可追溯，不存在双份实现 | `git log`：`569c6a5` → `0a0e5e3` → `296897c`；本报告 27.1 |
| REM-002 | **PASS** | 2309×1257 根页面 `scrollHeight 1257 = clientHeight 1257`，`scrollY=0`；创建 CTA bottom=1184 ≤ 1257-8，高度 48 | `report.json.viewports["reported-screen"]`、`viewport/2309x1257.png` |
| REM-003 | **PASS** | 1366×768 根不滚（768/768），左栏局部滚动（612/492），右栏 `.dl-task-preview__actions` footer 常驻（bottom 695/711 ≤ 760） | `report.json.viewports["short-desktop"]`、`viewport/1366x768.png` |
| REM-004 | **PASS** | 2309×1257 与 2560×1440 下 `.dl-flow-rail__inner` = 1600 ≤ 1600、`.dl-workspace` = 1920 ≤ 1920；Dataset 卡等宽（不被拉成长条）；左右留白对称 | `report.json.viewports[*].rail_inner_width / workspace_width` |
| REM-005 | **PASS** | 右栏五行 Grid：head/ring/path/body(滚动)/actions；未预览时不渲染"—"串字段；CTA 独占 footer，预览后仍 bottom=827 ≤ 892 | `report.json.viewports["desktop"].after_preview`、`viewport/desktop-with-preview.png` |
| REM-006 | **PASS** | 6 段真实浏览器视频（1440×900、100% 缩放、无后期剪辑），CLS=0、severe long task=0、页面隐藏暂停=真、可中断=真 | `motion/*.webm`、`report.json.motion` |
| REM-007 | **PASS** | 页面进入（标题→轨道→工作区，260ms+45ms×2 < 420ms）、地址解析 420ms 一次性脉冲、范围交叉淡入淡出、Dataset 勾选路径、步骤推进、Coverage 圆环 600ms、创建后节点一次性反馈；动画不增加业务请求（preview=1、create=1） | `report.json.motion.checks`、`report.json.request_counts` |
| REM-008 | **PASS** | 390×844 折叠态：`④ Coverage 预览 上次预览·已失效 [预览已失效] 重新预览`，琥珀图标 + 文字 + `role=status` + 可操作入口，灰度可辨；无页面内警告条、不闪烁、不自动请求 | `report.json.mobile_stale`（`chip_text=预览已失效`、`role_status=true`、`collapsed=true`）、`viewport/mobile-stale-390x844.png` |
| REM-009 | **PASS** | 未修改共享 renderer；下载页 CSS 无任何 `.cs-dialog-*` 时长覆盖（唯一命中是 `:has(.dl-dialog)` 的尺寸规则）；实测面板进入 220ms / 退出 160ms / 遮罩 160ms / 遮罩 `backdrop-filter:none`；关闭后 dialog=0、mask=0 | `report.json.dialog`、`docs/acceptance/download-new-dialog-timing.md` |
| REM-010 | **PASS** | 三个炸弹夹具都含 `[Content_Types].xml`+`xl/workbook.xml`，恶意体在 `xl/worksheets/sheet1.xml`；四例各自命中目标分支且 validate/preview/create 全为 0 | `r9/r9-07-evidence.json`（见 27.6） |
| REM-011 | **PASS** | runner 不再传 `--reporter`（只在 config 声明），`playwright-report.json` 真实生成、可解析、stats 与设计一致（expected=7 / skipped=7 / unexpected=0），`run-matrix.json` 无 MISSING，`run-matrix.rc` 进 gate | `r9/run-matrix.json`、`r9/playwright-report.json` |
| REM-012 | **PASS** | 旧不可达的 `/20000[01]/` 已删除；A1 用真实 BIFF8 断言"请求 200000 行、落盘 65535 行（格式上限）"；A2 改用 6 万行 `.xlsx`（<16MiB）验证超时与取消；manifest 同时记录 `requestedRows` 与 `actualFormatCapacity` | `udl01/v6/udl01-a-evidence.json`、fixture self-check |
| REM-013 | **PASS** | 权威生成器 `r9-make-fixtures-v5c.mjs`（runner 记录其 sha256），布局自检：`locator_offset = eocd_offset-20`、locator signature `0x07064b50`、ZIP64 EOCD signature `0x06064b50`、locator 指向的记录偏移一致；浏览器导入命中 `暂不支持 ZIP64 工作簿`，SheetJS chunk 请求 0、validate/preview/create 全 0、未建任务 | `udl01/v6/udl01-d-evidence.json` |
| REM-014 | **PASS** | 本地解析摘要与结果 Dialog 使用 `local-parse-summary/-row-count/-address-count/-address-header/-submit` 稳定 testid；v5/v5b/v6 不再依赖"本次送校验"整句文案 | `udl01/v6/udl01-e-evidence.json`、三个 spec 的 testid 断言 |
| REM-015 | **PASS** | 三层齐全：① 真实接口（arbitrum 白名单 6 项、available 5 项全可选、preview 只发真实支持项 + `prefer_local`）；② 组件 7 项夹具（第 7 项鼠标与键盘都被上限挡住、Dialog 点名、前 6 项可取消）；③ 服务端超限拒绝（preview 400 `VALIDATION_INVALID_ARGUMENT` + `field_errors.datasets`、create 400 无 job_id、任务数不增） | `dataset/dlflow-rem-001/002/003-*.json`、`dataset/dlflow-remediation-summary.json`（22/22 通过） |
| REM-016 | **PASS** | v5b：77 checks / 0 failed，A–E 全部 `expected`，fixtures/selfcheck/report-identity/crosscheck/summary 全 rc=0，API/Worker 身份冻结稳定；v6（最终 release gate）：86 checks / 0 failed，U5 预算文案探针绑定 served chunk（300ms→"300 毫秒"、180000ms→"180 秒"、无"0 秒"） | `udl01/v5b/gate.txt`、`udl01/v6/gate.txt` |
| REM-017 | **PASS** | 8 个视口自动产出报告（见 27.4），含根滚动、CTA rect、内部滚动、截图 | `report.json.viewports` |
| REM-018 | **PASS** | 3 个视口的字号/对比度实测：页面标题 24→28px、章节标题 16px、正文/标签 12–14px、指标数值 20px；最低对比度 7.22:1，全部 ≥4.5；无 text-shadow | `report.json.clarity` |
| REM-019 | **PASS** | 可见链名只出现一次（`slug_occurrences=1`，`TRON` 不再渲染成 `TRONtron`），提交值仍为 slug | `report.json.chain_label` |
| REM-020 | **PASS** | commit / dist / served / 页面 chunk 四方一致（见 27.1）；证据全部来自同一部署 | `source-binding.json`、`report.json.source_binding` |
| REM-021 | **PASS** | 画布 = `rgb(246,248,251)`、alpha=1、`background-image:none`、`::before/::after content:none`；`opacity=1/filter:none/backdrop-filter:none`；文字祖先违规 0；对比度失败 0；整页无 radial-gradient、无半透明白色叠层、无 glasshero | `report.json.visual_clarity`、`visual-clarity/computed-styles.json` |
| REM-022 | **PASS** | DOM：`input[type=file]` 恰好 1 个；`[data-testid=address-file-trigger]` 可见恰好 1 个；`导入 CSV / XLSX` 按钮 0 个；"解析导入" 1 个（只解析 textarea，不打开选择器）；触发器 click / Enter / Space 均真实打开文件选择器 | `report.json.address_import` |
| REM-023 | **PASS** | 1366×768 / 1440×900 / 1920×1080 / 2309×1257 四视口下 textarea 与文件触发器 top/bottom/height 差值**全部为 0px**（阈值 1px）；短屏同为 84px、其余 96px（两侧同步缩放） | `report.json.address_import.per_viewport` |
| REM-024 | **PASS** | 页面 2 个 fieldset 四边边框全为 `0px`；页面内 `<hr>` = 0；范围/性能组外重复 wrapper 外框 = 0；分段控件与性能卡保留自身 1px 控件边界；fieldset/legend 语义与焦点样式未删除 | `report.json.group_frames` |

**P0 项（REM-001/002/003/004/005/006/010/011/013/016/017/020/021/022/023/024）全部通过。**

---

## 27.4 视口证据

| 视口 | scrollH/clientH | scrollW/clientW | 创建 CTA rect (top/right/bottom/left, h) | 左栏内部滚动 | 截图 |
|---|---|---|---|---|---|
| 1366×768 | 768/768 | 1366/1366 | 647/1309/695/1003, 48 | 是（612/492，规范允许） | `viewport/1366x768.png` |
| 1440×900 | 900/900 | 1440/1440 | 779/1382/827/1076, 48 | 否（613/613） | `viewport/1440x900.png` |
| 1920×1080 | 1080/1080 | 1920/1920 | 959/1853/1007/1503, 48 | 否（784/784） | `viewport/1920x1080.png` |
| 2309×1257 | 1257/1257 | 2309/2309 | 1136/2222/1184/1856, 48 | 否（959/959） | `viewport/2309x1257.png` |
| 2560×1440 | 1440/1440 | 2560/2560 | 1319/2347/1367/1981, 48 | 否（1142/1142） | `viewport/2560x1440.png` |
| 1024×768 | 2165/768（自然滚动） | 1024/1024 | 2034/985/2082/259, 48 | 不强制 | `viewport/1024x768.png` |
| 768×1024 | 2474/1024（自然滚动） | 768/768 | 2351/733/2399/35, 48 | 不设置嵌套滚动 | `viewport/768x1024.png` |
| 390×844 | 1564/844（自然滚动） | 390/390 | 底部安全栏（`.dl-actionbar`） | 禁止嵌套滚动 | `viewport/390x844.png` |

2309×1257：Flowline 轨道 1600 ≤ 1600、工作区 1920 ≤ 1920。

**四个桌面视口的地址区测量**（阈值 1px）：

| 视口 | textarea top/bottom/h | 触发器 top/bottom/h | top Δ | bottom Δ | height Δ |
|---|---|---|---|---|---|
| 1366×768 | 276.09 / 360.09 / 84 | 276.09 / 360.09 / 84 | 0 | 0 | 0 |
| 1440×900 | 287.48 / 383.48 / 96 | 287.48 / 383.48 / 96 | 0 | 0 | 0 |
| 1920×1080 | 296.44 / 392.44 / 96 | 296.44 / 392.44 / 96 | 0 | 0 | 0 |
| 2309×1257 | 297.78 / 393.78 / 96 | 297.78 / 393.78 / 96 | 0 | 0 | 0 |

- 原生文件 input 数量：**1**；可见文件触发器数量：**1**；"导入 CSV / XLSX" 按钮：**0**；"解析导入"：**1**；打开文件选择器：click/Enter/Space **全部成功**。
- `.dl-flow-page` 背景：`rgb(246, 248, 251)`，alpha=**1**，`background-image: none`，`::before/::after` content = none，opacity=1，filter=none，backdrop-filter=none。
- 文字祖先 opacity/filter/backdrop-filter 违规数：**0**（5 个视口 × 7 类文字）。
- fieldset 边框：全 `0px`；页面 `<hr>`：**0**；重复 wrapper 外框：**0**。
- 未裁切清晰度截图：`visual-clarity/clarity-and-alignment-1440x900.png`、`visual-clarity/clarity-and-alignment-2309x1257.png`（人工复核：无灰雾、无重复文件入口、无多余横线框）。

---

## 27.5 动画证据

| 视频 | 内容 | 校验 |
|---|---|---|
| `motion/01-page-enter.webm` | 页面进入（标题→轨道→工作区） | 立即可操作、420ms 内稳定、CLS=0 |
| `motion/02-address-import.webm` | 地址解析 + 一次性数据脉冲 | busy 延迟真实、脉冲只执行一次、API 增量 = 1 |
| `motion/03-range-switch.webm` | 全量→区块→时间→全量快速切换 | 最终只显示全量、无残留字段/动画 |
| `motion/04-dataset-and-step.webm` | Dataset 键盘选择 + 步骤推进 | Space 可选、位置零位移、非颜色状态、根节点零滚动 |
| `motion/05-coverage-to-create.webm` | Coverage 圆环 → 创建任务 | 中心值真实（88.8%）、dasharray 与 ratio 一致、create=1 |
| `motion/06-reduced-motion.webm` | reduced motion 下同功能 | 光点 0、无位移/缩放、Dialog transform=none、功能完整 |

- 时长令牌：`micro .14 / fast .18 / standard .26 / spatial .42 / ring .6 / stagger .045`（秒），与 §8.3 一致；Dialog 时长不在页面令牌内（REM-009）。
- **CLS = 0**；**severe long task = 0**；页面隐藏后光点移动 0px（恢复后继续 92.6px，不堆积）；动画中途再次操作最终状态正确（可中断 = true）。
- reduced motion：循环光点 0 个，`stroke-dasharray` 直接终值，Dialog 无 scale/translate。

---

## 27.6 安全夹具与 runner

**R9-07（四例逐条命中，请求数全 0）**

| 夹具 | 实际拒绝原因 | validate/preview/create | pageErrors |
|---|---|---|---|
| renamed.xlsx | 扩展名为 .xlsx 但内容不是 ZIP/OOXML 包（疑似损坏或被改名） | 0 / 0 / 0 | 0 |
| bomb-size.xlsx | 声明解压后总大小 104858440 字节超过上限 67108864 | 0 / 0 / 0 | 0 |
| bomb-ratio.xlsx | 声明压缩比 8934.4 超过上限 200（疑似压缩炸弹） | 0 / 0 / 0 | 0 |
| bomb-lie.xlsx | 条目 xl/worksheets/sheet1.xml 真实解压后超过上限 67108864 字节（声明值不可信/疑似压缩炸弹） | 0 / 0 / 0 | 0 |

夹具自检：OOXML 必需条目 `[Content_Types].xml`+`xl/workbook.xml` 齐全、ZIP 魔数/EOCD/中央目录可遍历、每个文件 sha256 记录、文件均 < 16MiB、声明值与意图一致。

**R9 runner**：`expected_run=7 / actual_passed=7 / expected_excluded=7 / actual_skipped=7 / failed_or_missing=0 / mismatches=[]`。

**ZIP64 布局自检**：`eocd_offset=689`、`locator_offset=669`（= EOCD − 20）、locator signature `0x07064b50`、ZIP64 EOCD signature `0x06064b50`（offset 613，与 locator 声明一致），布局为 `ZIP64 EOCD → ZIP64 locator → classic EOCD`；浏览器命中 `暂不支持 ZIP64 工作簿（本导入只接受经典 ZIP）`，SheetJS chunk 请求 0、服务端调用 0、未建任务。

**UDL01 gate**：v5b 77/77、v6 86/86；两次运行的 API/Worker 二进制 sha 前后一致（`freeze_stable=1`）、无 Traceback、无 MISSING。

---

## 27.7 网络与功能证据

- preview / create 请求体都携带 `reuse_policy: "prefer_local"`（真实后端 DL-FLOW-002 与页面级 acceptance 双重验证）。
- 真实后端链路（DL-FLOW 20/20，uc-desktop + uc-mobile）：五步轨道、Dataset 上限、风险 Dialog、清晰度、预览失效、幂等键稳定。
- 页面级 acceptance 的请求序列：`validate-addresses` → `preview`(prefer_local) → `POST /api/v2/downloads`(prefer_local + `Idempotency-Key: 582ea152-…`)；一次预览 1 次、一次创建 1 次，视口与动画不增加请求。
- 多链按链分组、服务端 accepted 地址集合为权威（R9-01 覆盖：服务端 rejected 的案件/标签被移除后创建）。
- 复用策略在所有用户可见层不可见：`reuse_policy_visible=false`，命中项 `[]`（真实 DOM innerText + aria-label/title/placeholder/alt 扫描）。
- 控制台：console error 0、pageerror 0、CSP 违规 0。

---

## 27.8 测试结果（逐命令）

| 命令 | rc | 结果 | 报告 |
|---|---|---|---|
| `pnpm run typecheck` | 0 | 两个 app + 全部 packages 通过 | — |
| `pnpm run check:tokens` | 0 | 全部对比度断言通过 | — |
| `pnpm run check:contract` | 0 | 路由 28 / 权限 19 / API 字面量 66 / 案件 Tab 7 与基准一致 | — |
| `pnpm run check:architecture` | 0 | ok=true, problems=[] | — |
| `pnpm run check:css-shadowing` | 0 | 1961 条新层规则无冲突 | — |
| `pnpm run check:bundle` | 0 | 248.23 KB / 预算 250 KB | — |
| `E2E_BROWSER_CHANNEL=chromium pnpm run check:uc-render` | 0 | 用户端真实浏览器验收通过 | `frontend/acceptance-evidence/v4-render-check/report.json` |
| `scripts/s7/run-download-flowline.sh` | 0 | expected_run=20 / passed=20 / failed=0 | `acceptance-evidence/download-flowline/run-20260921T145200Z/run-matrix.json` |
| `scripts/s7/run-download-flowline-remediation.sh` | 0 | playwright_rc=0 report_check_rc=0 crosscheck_rc=0 acceptance_rc=0 report_merge_rc=0 traceback=0 | `frontend/acceptance-evidence/download-flowline-remediation/report.json` |
| `scripts/s7/run-r9-download-import-v4.sh` | 0 | playwright_rc=0 report_check_rc=0 run_matrix_rc=0 fixtures_rc=0 | `frontend/acceptance-evidence/download-flowline-remediation/r9/` |
| `scripts/s7/run-udl01-worker-isolation-v5b.sh` | 0 | 77 checks / 0 failed；A–E 全 expected；freeze_stable=1 | `frontend/acceptance-evidence/download-flowline-remediation/udl01/v5b/` |
| `scripts/s7/run-udl01-worker-isolation-v6.sh` | 0 | 86 checks / 0 failed；A–E 全 expected；freeze_stable=1 | `frontend/acceptance-evidence/download-flowline-remediation/udl01/v6/` |
| `APP=user bash scripts/s7/deploy-frontend-controlled.sh` | 0 | 14 PASS / 0 FAIL；重建 index sha 与部署前一致 | `acceptance-evidence/dual-system-v1/sprint7/frontend-deploy-user-20260921T145141Z/` |

failed / unexpected skip / not_run / MISSING 均为 **0**。

---

## 27.9 仍未完成 / 遗留

**没有未完成的 REM 项。** 以下为需要如实记录的非阻塞事项：

1. **环境干扰（已复现并已消除）**：21:37:28 期间另一个并行会话重新构建并重启了 `backend/bin/{api,worker}`，导致第一次 UDL01 v5b 整轮运行失败（UDL01-D/E 的登录页 `#identifier` 30s 未渲染、`freeze_stable=0`）。稳定后原样重跑，v5b 与 v6 均 PASS。这不是页面缺陷，也不是"既有失败"。
2. **夹具 label 校正**：页面级 acceptance 原来用 `Transactions / Token Transfers` 等英文复数 label，比真实后端 `datasetLabel()`（`普通交易 / Token Transfer / NFT Transfer / 事件日志 / 地址余额快照`）更长，在 136px 的桌面卡里会把单词切碎。夹具已改为与后端逐字一致；**不是**为了让断言通过的放宽（Dataset 卡几何断言只设下限，标签换行仍作为 note 记录在案）。
3. **超宽屏 Dataset 卡片区右侧留白**：`.dl-dataset-grid` 有 `max-width: 840px`（5–6 列逻辑），因此在 2309×1257 上卡片行右侧存在留白。这是"卡片不被拉成长条"的直接代价，符合 §6.8，但视觉上仍可再优化。
4. **环境不干净**：仓库工作区有 6 个 tracked modified + 125 个未跟踪项，全部来自其他并行会话；本轮未触碰、未清理，也未将其纳入证据。
5. **未运行**：`frontend/tests/download-flowline-redesign.spec.ts` 与 `download-flowline-remediation.spec.ts` 通过 `scripts/s7/*.sh` 运行（含 uc-desktop/uc-mobile 两个 project）；未单独运行 `pnpm playwright test` 裸命令，避免绕过 runner 的 fail-closed 报告检查。

---

## 附：§21 证据目录（55 个文件，4.6 MB）

```text
frontend/acceptance-evidence/download-flowline-remediation/
├── source-binding.json
├── report.json
├── request-log.json
├── console-errors.json
├── viewport/            （8 个视口 + dataset-cap + desktop-with-preview + mobile-stale）
├── visual-clarity/      （1440×900 / 2309×1257 未裁切截图 + computed-styles.json）
├── motion/              （6 段 webm）
├── r9/                  （playwright-report.json / run-matrix.json / r9-07-evidence.json / fixture-selfcheck）
├── dataset/             （三层 Dataset 证据 + summary）
└── udl01/{v5b,v6}/      （playwright-report / summary / a-d-e evidence / gate / bundle-binding）
```
