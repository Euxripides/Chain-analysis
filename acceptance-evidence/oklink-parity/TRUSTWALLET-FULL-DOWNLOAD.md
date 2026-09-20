# Trust Wallet Assets 全量下载与导入（2026-09-20 完成）

## 下载

| 项 | 值 |
| --- | --- |
| 通道 | git 协议（codeload 直连与 ghfast 镜像的 zip 吞吐均为 0；`info/refs` 实测 630KB/s） |
| 命令 | `git clone --depth 1 --no-checkout https://github.com/trustwallet/assets.git`（全局 insteadOf 已走 ghfast 镜像） |
| 克隆耗时 | **27m32s**，`.git` 363MB（46,541 个对象） |
| 落地 | `git checkout HEAD -- blockchains` 只用 **2.9s**（本地对象，无网络——上一轮就是卡在这步的重取 blob） |
| 工作树 | **561MB**，`logo.png` **15,752 个**，`info.json` **15,772 个** |
| 路径 | `/home/euripides/tw-assets-full` |

## 导入

```bash
cd backend && . ../deploy/.env && export PATH=/usr/local/go/bin:$PATH
go run ./cmd/tokenindex -trustwallet /home/euripides/tw-assets-full
# trustwallet: 目录 14323 行，带 logo 13520 行，跳过 180 个链目录（用时 2s）
```

- `token_logo_assets`：**14,323 行**（平台支持的 9 条链），其中 **13,520 行带 logo.png 的 sha256 与字节数**，
  14,323 行都有 `info.json` 的名称/符号/类型；
- 按链：eth 7,760 / bsc 3,954 / solana 1,352 / tron 343 / base 266 / arbitrum 264 / polygon 222 / avalanche 96 / optimism 66；
- 与 `token_metadata` 交集 1,230 行。

## 图标本地化（自托管）

- logo worker 实际运行在 **`blockchain-worker` 进程**（`cmd/worker` → `StartTokenMetadataBackground(..., true)`），
  不在 `blockchain-api`：这一点之前判断错了，导致扫描没有生效；在 worker 侧重建并重启后开始消化；
- 每轮批量由 50 提到 200（独立进程，不占 API 的交互预算）；
- 实测：自托管对象 1,148 → **1,163**（持续消化中，剩余 111），
  抽查 `GET /api/v1/tokens/eth/0x8d75959f1e61ec2571aa72798237101f084de63a/logo` → `200 image/png`，PNG 300×300。

## 仍未完成

- **CoinGecko 数字 ID**：官方 `/coins/list?include_platform=true` 与 `assets.coingecko.com` 在本机仍 000；
  已导入的聚合 tokenlist 快照（13,234 行）不含 `coingeckoId`。放开出网或提供 JSON 后
  `go run ./cmd/tokenindex -coingecko <file>` 一次性补齐。
