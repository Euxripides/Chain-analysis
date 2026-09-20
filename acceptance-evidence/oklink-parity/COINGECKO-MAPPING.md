# CoinGecko 映射落库（2026-09-20）

## 数据来源

| 项 | 值 |
| --- | --- |
| 文件 | `/home/euripides/cg-import/coins-list.json`（用户从能出网的机器下载后 scp 上来） |
| 大小 / 摘要 | 3763099 bytes / sha256 `13e3a302399dde8d43cbd42607e2005e2a1bff9914deb98e43a5cb11b5037429` |
| 内容 | `GET https://api.coingecko.com/api/v3/coins/list?include_platform=true` 原样响应：21,336 个币种、20,562 个带 platforms、35,925 条平台映射 |

为什么是文件而不是直连：本机到 CoinGecko 被**上游封堵**（本机有免密 sudo，但排查确认不是本机防火墙）——
`iptables OUTPUT` 策略 ACCEPT、`ufw` inactive、nft 只有 Docker NAT；
systemd-resolved 把 api.coingecko.com 解析成 107.181.166.244（还带 Teredo 段 IPv6），
AliDNS 给 52.58.1.161、DNSPod 给 69.50.221.20，pro-api 给 31.13.88.169（Facebook 段）；
`dig @8.8.8.8`/`@1.1.1.1` 超时；三个 IP 用 `--resolve` 带正确 SNI 连 443 全部超时。

## 落库结果

```
go run ./cmd/tokenindex -coingecko /home/euripides/cg-import/coins-list.json
coingecko: 币种 21336 行，平台映射 25492 行，已映射链 25492 行（用时 1s）
```

| 表 | 行数 | 说明 |
| --- | --- | --- |
| `coingecko_coins` | 22,057 | 官方 21,336 + 早前可达快照 3,981（去重合并） |
| `coingecko_platforms` | 29,203 | **official 25,492** + derived-name-symbol 3,711（迁移 0074 区分口径） |

按链（official）：eth 7,463 / solana 7,060 / bsc 4,987 / arbitrum 3,297 / base 3,276 /
polygon 1,189 / optimism 972 / avalanche 863 / tron 96。

旗舰映射实测：

```
tether      -> eth:0xdac17f958d2ee523a2206206994597c13d831ec7
tether      -> tron:TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t
tether      -> solana:Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB
usd-coin    -> arbitrum:0xaf88d065e77c8cc2239327c5edb3a432268e5831
binancecoin -> eth:0xb8c77482e45f1f44de1745f52c74426c631bdd52
shiba-inu   -> eth:0x95ad61b0a150d79219dcf64e1e6cc01f0b64c4ce
```

## 三方对账（`token_metadata` 36,481 行）

| 维度 | 行数 |
| --- | --- |
| 有官方 CoinGecko ID | 1,403 |
| 有 Trust Wallet 目录条目 | 1,230 |
| 两者都有 | 489 |
| 有自托管图标（`logo_object_key`） | 1,196 |
| 同时有图标与官方 ID | 535 |

## 复现

```bash
cd backend && . ../deploy/.env && export PATH=/usr/local/go/bin:$PATH
go run ./cmd/tokenindex -coingecko /home/euripides/cg-import/coins-list.json   # 官方口径，标 official
go run ./cmd/tokenindex -derive-platforms                                       # 仅在缺官方数据时用于推导
go run ./cmd/tokenindex -resolve-logos                                          # 图标 URL 回填
```

## 未做

- 3,711 行 derived-name-symbol 仍保留（口径已标注）；只要官方口径时可 `DELETE ... WHERE match_method<>'official'`；
- 未映射链（robinhood 1,082 / morph-l2 1,055 / the-open-network 828 / hyperevm 796 / mantle 689 …）
  平台键已原样保留在文件里，平台若要新增链，改 `coinGeckoPlatforms` 映射后重跑即可。
