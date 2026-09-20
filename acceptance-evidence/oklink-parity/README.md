# 地址详情转账列表对齐参考站点（2026-09-20）

## 交付

| 层 | 改动 | 提交 |
| --- | --- | --- |
| 读路径 | 活动行补 tokenmeta 元数据（符号/全称/精度/图标）与方法名；与完整 enrich 共用 `applyTokenMetadata` | `613a54a` |
| 缺陷修复 | `tokenTransferRow` 缺 0008 迁移四列 → `SELECT *` ScanStruct 失败，bsc 活动读 500 | `421e26f` |
| 缺陷修复 | 方法名回填的空值判断改用 `isEmptyCell`（`fmt.Sprint(nil)` 是 `"<nil>"`） | `ccf05be` |
| 前端 | 七列表格 + 工具栏 + 代币选择器 | `216c6d0` |
| 前端 | 活动 Tab 默认全宽，七列一屏放下 | `2a94dce` |

## 列形状（代币与 NFT 同一形状）

交易哈希 / 方法 / 时间（YYYY/MM/DD HH:mm:ss）/ 发送方 / 接收方 / 数量 / 代币

- 数量：按方向带 +/−，千分位；
- 发送方/接收方：主体侧显示 Out/In 标签，地址可复制、可跳转；
- 代币：自托管图标 + 符号 + 全称（NFT 为合集 + `#token_id` + 标准）；
  图标只认同源路径 `/api/v1/tokens/{chain}/{contract}/logo`（CSP `img-src 'self'`），
  没有 logo 对象时退回首字母。

## 实测（fixture：eth）

代币行（地址 `0xdead…dead`，全部时间）：

```
0x7273…6a7e | Transfer | 2026/08/16 20:44:35 | 0x0a85…7fc0 | 0xdead…dead [In] | +868,500 | GIGA / GigaSwap
0x1db2…3342 | Transfer | 2026/04/22 23:56:11 | 0x286c…1d60 | 0xdead…dead [In] | +233,097.1 | SHIB / SHIBA INU
```

NFT 行（BAYC，`0xda308d…`）：

```
0xda30…588b | Transfer | 2022/02/21 16:57:52 | 0xfe34…e0ce [Out] | 0x26db…8812 | −1 | BAYC / #4937 · ERC721
```

自托管图标实测：SHIB logo 512×512 PNG、USDC 181×181 PNG，浏览器内 `naturalWidth` 分别为
512 / 181，接口 200。

## 门禁与验收

- `pnpm run typecheck` 通过；
- `node scripts/uc-contract-freeze-check.mjs` 通过（路由 / 权限 / `/api` 字面量与基准一致）；
- `E2E_BROWSER_CHANNEL=chromium node scripts/uc-v4-render-check.mjs` → `ok = true | problems = []`；
- `node scripts/uc-oklink-parity-check.mjs` → 12 项全 PASS（表格无横向滚动：1118 = 1118）。

## 未完成

- **代币图标回填（爬取）**：有 `LogoObjectKey` 的代币已经能出图；没有对象的代币
  （实测 GIGA / MILTON）仍是首字母兜底。回填入口：
  `POST /api/v1/admin/token-metadata/backfill`（需 `admin:dataset:manage`），
  logo 抓取由 `TOKEN_LOGO_ENABLED`（默认 true）与 `internal/tokenmeta/logo_worker.go` 的
  `ValidatePublicHTTPS` 控制，源可用 Trust Wallet assets（要求 EIP-55 校验和地址）。
- **分页页脚**：沿用 DataTable 的“共 N 行 / 第 x / y 页 + 上一页/下一页”，
  与游标式“加载下一页 / 加载全量 / 回到最新”并存；参考站点的页码跳转未做（接口只有游标）。
- **Solana NFT 实测**：Ankr 对 mint 账户返回 0 签名，经典 NFT 转账样本仍未取到，
  `nft_transfers` 的 Solana 实测仍阻塞（需要真实签名或 DAS 全索引提供方）。

