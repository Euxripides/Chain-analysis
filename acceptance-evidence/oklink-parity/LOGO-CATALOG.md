# 代币图标与跨源目录（2026-09-20）

## 目标

1. 完整下载 Trust Wallet Assets，落库成图标目录；
2. 导入 CoinGecko `/coins/list?include_platform=true`，建立 CoinGecko ID ↔ 链 ↔ 合约映射；
3. 两源都没有的，前端按参考图构造（首字母圆底）并挂"未验证"警示三角。

## 实际结果

| 步骤 | 结果 | 说明 |
| --- | --- | --- |
| Trust Wallet | `token_logo_assets` 13,520 行 | 完整资产树（`blockchains/*/assets/*/logo.png`）。**批量端点不可用**：codeload 吞吐 0（卡在 16MB）、sparse clone 卡在 206MB；从本地已取到的 tree（32,358 条目 / 15,563 个 logo 路径）导出目录，bytes 由既有 logo worker 自托管抓取 |
| CoinGecko | `coingecko-tokenlist` 13,234 行 | 官方接口在本机被网络策略拦住（DNS 返回 Teredo 段 IPv6，v4/v6 均 000；DoH 与公共代理同样 000）。改经 npm registry 可达的 **CoinGecko 聚合 tokenlist 快照**（18,638 条，含 chainId/address/name/symbol/decimals/logoURI）。`assets.coingecko.com` 同样 000，故其 logoURI 只入目录不抓取 |
| 映射缺口 | 数字 ID 未落库 | 该快照的 `extensions` 里没有 `coingeckoId`（0/18,786）。官方接口一旦可达，直接跑 `go run ./cmd/tokenindex -coingecko <file>` 即可补齐 `coingecko_coins` / `coingecko_platforms` |

## 落库与回填

- 迁移 `0073_token_logo_catalog.sql`：`token_logo_assets` / `coingecko_coins` / `coingecko_platforms`；
- 工具 `cmd/tokenindex`：`-trustwallet <dir>`、`-coingecko <file>`、`-coingecko-url default`、
  `-coingecko-tokenlist <file>`、`-resolve-logos`（幂等 upsert）；
- `scripts/logo/tw-subset-import.py`：网络受限时的按需子集导入（Contents API 列目录 + raw 取 info.json）；
- `internal/tokenmeta/logo_worker.go` 新增存量扫描（每 5 分钟 50 条），把目录回填的 `logo_url` 本地化成
  `logo_object_key`，经 `/api/v1/tokens/{chain}/{contract}/logo` 同源给前端（CSP `img-src 'self'`）。

## 实测

- 目录 ∩ `token_metadata`：Trust Wallet 1,146 行；CoinGecko 1,077 行；`-resolve-logos` 本轮回填 33 行；
- 自托管对象：1,148 → 扫描持续消化剩余 126 行；
- 图标路由实测：`GET /api/v1/tokens/bsc/0x55d398326f99059ff775485246999027b3197955/logo`
  → `200 image/png`，PNG 300×300 / 21,909 字节（Binance-Peg USDT，即参考图里的 Tether 图标）；
- 前端：`scripts/uc-oklink-parity-check.mjs` 13 项全 PASS，其中"未验证代币有警示三角"命中 26 处。

## 复现命令

```bash
cd backend && . ../deploy/.env && export PATH=/usr/local/go/bin:$PATH
go run ./cmd/migrate
go run ./cmd/tokenindex -trustwallet <解压目录>          # 完整目录（拿到 zip/clone 后）
go run ./cmd/tokenindex -coingecko-tokenlist <tokenlist.json>
go run ./cmd/tokenindex -resolve-logos
go run ./cmd/tokenindex -coingecko <coins-list.json>     # 官方接口可达后补数字 ID
```
