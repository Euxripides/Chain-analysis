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

## 图标管线修复（同日收尾）

下载完成后，图标本地化曾停在 1,163 且无任何错误日志。逐层排查出四个真缺陷：

1. **进程放错**：logo worker 由 `cmd/worker` 的 `StartTokenMetadataBackground(..., true)` 启动，
   只重建 `blockchain-api` 不生效 → 必须重建并重启 `blockchain-worker`；
2. **扫描回查用了错的键**：`sweepPendingLogos` 通过 `Get` 回查元数据，TRON 这类大小写敏感地址
   在 `normalized_address` 上查不到，33 行永远排不进队列 → 改为直接从行里取
   `chain/contract_address/logo_url` 组队列元素；
3. **URL 大小写不合规 → 404**：早期用全小写地址拼 raw URL，而 Trust Wallet 目录名是 EIP-55
   校验和形式，78 行 bsc 行永远抓不到 → 新增 `tokenindex -repair-logos` 以目录里的源目录名重写；
4. **单协程静默停摆 + 落库归一化**：单消费者遇到卡住的上传会停住整条管线（改为 4 并发 +
   单条 60s 墙钟上限）；`localizeLogo` 落库与 `Get` 取键都要用 `normalizeAddressForChain`
   （全局 `normalizeAddress` 会把 base58 小写化，对象进了 MinIO、行却永远停在未本地化）。

收尾实测：自托管图标 **1,196**（待本地化 0），关键图标路由全部 200 image/png：

| 代币 | 路由 | 结果 |
| --- | --- | --- |
| TRON USDT | `/api/v1/tokens/tron/TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t/logo` | 200 image/png 21,909B |
| BSC USDT | `/api/v1/tokens/bsc/0x55d398326f99059ff775485246999027b3197955/logo` | 200 image/png 21,909B |
| ETH SHIB | `/api/v1/tokens/eth/0x95ad61b0a150d79219dcf64e1e6cc01f0b64c4ce/logo` | 200 image/png 99,246B |
