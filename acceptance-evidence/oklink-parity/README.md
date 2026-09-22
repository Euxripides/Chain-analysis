# 地址详情转账列表对齐参考站点（OKLink）

分两轮：2026-09-20 对齐**列形状**，2026-09-22 对齐**列表区整体形态**（工具栏 / 计数行 / 行内渲染 / 载入方式）。

## 第二轮（2026-09-22）：把"列表区长成参考站点那样"补齐

用户指出第一轮只做了七列与工具栏控件，页面整体仍不像参考站点。本轮按参考截图重做：

| 区域 | 现在的实现 | 证据 |
| --- | --- | --- |
| Tab 文案 | 9 个 Tab/深链接 key 一个没动，只把英文标签中文化：`Token Transfer → 代币转账`、`Internal → 内部交易` | `token-transfers-wide.png` |
| 工具栏 | 一行 34px 圆角胶囊：`代币: 全部` / `数量: 全部` / `时间窗 + 起止日期` / `全部方向` / `发送方/接收方: 全部` / 刷新图标 + 重置。既有筛选能力（代币-NFT、数量区间、时间窗快捷、方向、发送方与接收方）全部折进胶囊，全部写 URL | `toolbar-token-pill-open.png` |
| 计数行 | 左「共计 N 条交易记录（最多加载 1 万行）」（`summary.token_transfer_count` 真实计数），右「隐藏数量为 0 的转账」开关（`Switch`，写 `hide_zero=1`） | `token-transfers-wide.png` |
| 行渲染 | 哈希 `0x+14 位+...` 链接（不带复制图标）；方法名**原文**（`transfer`/`transferFrom`/`Swap`）灰底标签；主体侧 `In/Out` 彩色标签（In 绿 / Out 橙）；对手方地址链接色、主体地址正文色，两侧都可复制；数量正文色 + 千分位 + 方向符号 + 法币小字（`<$0.01`）；代币 32px 圆形图标 + 符号（链接色）+ 全称 | `token-transfers-wide.png`、`nft-transfers-wide.png` |
| 字号/密度 | 表头 14px 正文色（不再是 11px 灰色大写），单元格 14px，行高 60px，表头 48px，列宽百分比固定（`table-layout: fixed`） | 验收脚本断言 |
| 载入方式 | 页脚不再有「共 N 行 · 第 x/y 页 · DOM n 行（虚拟滚动）」与上一页/下一页（`DataTable` 新增 opt-in `hidePager`，其他页面逐字不变），改成居中「加载更多 / 加载全部（最多 10,000 行）」，翻页后多一个「回到最新」 | `token-transfers-wide.png` |
| 窄屏 | ≤599px 时胶囊换行、日期胶囊内部换行；390×844 实测文档宽 390 = 视口宽（此前会被 445px 的日期胶囊撑到 457px） | 探针实测 |

### 仍然与参考站点不同（有意保留）

- **左侧导航**：参考站点没有全局侧栏，我们有 240px 侧栏，所以同样 1440 视口下表格可用宽度是 1118px，
  列内容比参考站点更容易触发省略号（1920 下全部完整）。
- **Tab 数量**：参考站点 5 个，我们保留 9 个（对手方 / 语义事件 / 分析 / Coverage 是产品能力），
  只做文案中文化——`uc-v4-render-check.mjs` 的 9-Tab 契约未解冻。
- **可疑代币虚线框**：参考站点只给警示三角；虚线框是 2026-09-20 按另一张参考图加的，本轮保留。
- **时间列表头高亮**：按参考站点把「时间」表头着色（默认按时间倒序），但它不是可点排序控件。

## 第一轮（2026-09-20）：列形状与读路径

| 层 | 改动 | 提交 |
| --- | --- | --- |
| 读路径 | 活动行补 tokenmeta 元数据（符号/全称/精度/图标）与方法名；与完整 enrich 共用 `applyTokenMetadata` | `613a54a` |
| 缺陷修复 | `tokenTransferRow` 缺 0008 迁移四列 → `SELECT *` ScanStruct 失败，bsc 活动读 500 | `421e26f` |
| 缺陷修复 | 方法名回填的空值判断改用 `isEmptyCell`（`fmt.Sprint(nil)` 是 `"<nil>"`） | `ccf05be` |
| 前端 | 七列表格 + 工具栏 + 代币选择器 | `216c6d0` |
| 前端 | 活动 Tab 默认全宽，七列一屏放下 | `2a94dce` |

## 列形状（代币与 NFT 同一形状）

交易哈希 / 方法 / 时间（YYYY/MM/DD HH:mm:ss）/ 发送方 / 接收方 / 数量 / 代币

- 数量：按方向带 +/-，千分位，最多 8 位小数，法币估值放第二行小字；
- 发送方/接收方：主体侧显示 In/Out 标签，地址可复制、可跳转；
- 代币：自托管图标 + 符号 + 全称（NFT 为合集 + `#token_id` + 标准）；
  图标只认同源路径 `/api/v1/tokens/{chain}/{contract}/logo`（CSP `img-src 'self'`），
  没有 logo 对象时退回首字母。

## 实测（fixture：eth）

代币行（地址 `0xdead…dead`，全部时间）：

```
0x727316b3aab32f... | transfer | 2026/08/16 20:44:35 | 0x0a85...92c8d67fc0 | [In] 0xdead...000000dead | +868,500 | GIGA / GigaSwap
0x1db2680017968d... | transfer | 2026/04/22 23:56:11 | 0x28c6...743bf21d60 | [In] 0xdead...000000dead | +233,097.1 | SHIB / SHIBA INU
```

NFT 行（BAYC，`0xda308d…`）：

```
0xda308d3d7310d3... | transfer | 2022/02/21 16:57:52 | [Out] 0xfe34...c9148be0ce | 0x26db...b7345e8812 | -1 | BoredApeYachtClub / #4937 · ERC721
```

自托管图标实测：SHIB logo 512×512 PNG、USDC 181×181 PNG，浏览器内 `naturalWidth` 分别为
512 / 181，接口 200。

## 门禁与验收

- `pnpm run typecheck` 通过；
- `node scripts/uc-contract-freeze-check.mjs` 通过（路由 / 权限 / `/api` 字面量与基准一致）；
- `E2E_BROWSER_CHANNEL=chromium node scripts/uc-v4-render-check.mjs` → `ok = true | problems = []`；
- `node scripts/uc-oklink-parity-check.mjs` → **28 项全 PASS**（含工具栏一行胶囊、计数行、
  0x+14 位哈希、方法原文标签、In/Out 标签、数量正文色、无分页摘要、七列不横向滚动 1118 = 1118）。

## 未完成

- **代币图标回填（爬取）**：有 `LogoObjectKey` 的代币已经能出图；没有对象的代币
  （实测 GIGA / MILTON）仍是首字母兜底。回填入口：
  `POST /api/v1/admin/token-metadata/backfill`（需 `admin:dataset:manage`），
  logo 抓取由 `TOKEN_LOGO_ENABLED`（默认 true）与 `internal/tokenmeta/logo_worker.go` 的
  `ValidatePublicHTTPS` 控制，源可用 Trust Wallet assets（要求 EIP-55 校验和地址）。
  本机实测：oklink.com 出网被重置（`curl: (35) Recv failure`），GitHub 可达，
  Trust Wallet 源能否抓到要在部署机上再确认。
- **数量列超长值**：8 位小数的巨额（如 `+1,956,840,222.17028181`）在 1440 视口下仍会省略号，
  1920 下完整；完整值在 `title` 与行检查器里。
- **Solana NFT 实测**：Ankr 对 mint 账户返回 0 签名，经典 NFT 转账样本仍未取到，
  `nft_transfers` 的 Solana 实测仍阻塞（需要真实签名或 DAS 全索引提供方）。
