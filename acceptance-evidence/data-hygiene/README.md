# L1 合成数据清退 + L3 代币元数据/图标重建（2026-09-23）

## L1：清退合成压测数据

### 怎么发现是合成数据

`blockchain.*` 里混进了一批**压测生成**的行，指纹三条互证：

```
chain=bsc  timestamp=2026-07-20 19:28:42.066  tx_hash=0x1dcd6500  log_index=0  token_symbol=MCK
           token_address=0x1623de1d5e1ed559  from=0x1111111111111111111111111111111111111111
           to=0x1623de1d5e1ed552             token_decimals=18
chain=bench  600,000 行交易，全部落在 2026-08-31 14:36:20.957 ~ 14:36:21.445（半秒内）
```

- tx_hash 是十六进制计数器（`0x1dcd6500` = 500,000,000）；
- from 是常量 `0x1111…1111`，to/token 地址是计数器截出来的短串；
- 符号统一 `MCK`，还有一条链名直接叫 `bench`。

判定用的是**指纹**而不是时间窗：EVM 哈希 66 位、TRON 64 位、Solana base58 签名 87/88 位，
其余长度（实测 3~11 位）只可能来自生成器。清退前先验证过：正常长度的行里 `MCK` = 0 行。

### 清退范围与结果（逐表 before/after）

| 表 | 清退前 | 清退后 | 删除 | 残留异常 |
| --- | --- | --- | --- | --- |
| `blockchain.logs` | 12,411,229 | 795,051 | 11,616,178 | 0 |
| `blockchain.transactions` | 32,900,702 | 22,698,402 | 10,212,300 | 0 |
| `blockchain.token_transfers` | 92,304,547 | 66,558,341 | 25,746,206 | 0 |
| `blockchain.address_activity` | 108,284,836 | 90,451,912 | 17,832,924 | 0 |
| `blockchain.internal_calls` | 2,742 | 2,742 | 0（4 条短 hash 是合法 Solana 签名） | 0 |

合计清退 **65,407,608 行**；`chain='bench'` 0 行、`token_symbol='MCK'` 0 行；
近 7 天真实数据仍在写入（transfers 39,135 行 / activity 59,584 行，抽样时间 2026-09-23 01:5x UTC）。

执行方式：ClickHouse **轻量 DELETE**（`DELETE FROM … WHERE`，只打删除标记，不重写分区），
单表 1~21 秒。**没有**用 `ALTER TABLE … DELETE`（mutation 会重写 8.5 GiB 分区）。
脚本：`scripts/ops/purge-synthetic-bench-data.sql`。

### 备份与回滚材料

- ClickHouse：`ALTER TABLE … FREEZE WITH NAME 'mck-purge-20260922'`（硬链接快照，apparent 25 GiB、
  不额外占盘），位置 `/data/blockchain-platform/clickhouse/shadow/mck%2Dpurge%2D20260922/store/**`；
- 抽样留证：每表 2,000 行待删数据 Parquet + 指纹分布 TSV（见本目录 `before-*-fingerprint.tsv`）；
- PostgreSQL：`pg-tokenmeta.dump`（token_metadata / token_logo_assets / resolve_queue / backfill_jobs）；
- 清单与 sha256：`MANIFEST.md`、`MANIFEST.sha256`（本目录是副本，原始备份在
  `~/backups/blockchain-platform/mck-purge-20260922/`）。

确认不再需要回滚后删除快照：
`rm -rf /data/blockchain-platform/clickhouse/shadow/mck%2Dpurge%2D20260922`

## L3：代币元数据 / 图标重建

### 清退后的真实口径

`token_transfers` 里真实出现过的代币合约 **36,729 个**（bsc 36,193 / tron 497 / eth 38 / solana 1）：

| 指标 | 重建前 | 重建后 |
| --- | --- | --- |
| 有 `token_metadata` 行 | 36,635（99.7%） | 36,635 |
| 有 `logo_url` | 1,186 | **1,743** |
| 有自托管图标 `logo_object_key` | 1,198 | **1,754** |
| 真实代币里有图标的比例 | 3.3% | **4.8%**（1,754 / 36,729） |

分链（重建后 / 全部真实代币）：bsc 1,702 / 36,193 · tron 33 / 497 · eth 19 / 38 · solana 0 / 1。

也就是说：**元数据不缺（99.7% 都有），缺的是图标 URL**——剩下 34,971 个（95.2%）在公开目录里
根本没有图标（抽样实测见下）。本轮把"目录里有、但没接上"的那部分全部接上了：**+556 个图标**。

### 页面实测（真实站点 1888×967）

地址 `0x70398354b91bb2a537ad6eebe8718c4a9ab628fe`（bsc，代币转账 Tab，全部时间）：

```
代币单元格 25 个：真实图标 21 个 / 首字母兜底 4 个
BTCB  img src=/api/v1/tokens/bsc/0x7130…/logo  naturalWidth=120
USDT  img src=/api/v1/tokens/bsc/0x55d3…/logo  naturalWidth=300
```

截图：`l3-icons-after-rebuild.png`。

### 修掉的两个真问题（本轮改动）

1. **`resolveLogos` 只认 `source='trustwallet'`**，把 `coingecko-tokenlist` 目录里那 1 万多个
   logo_url 全部丢掉；而且 EVM 地址是**大小写敏感**等值比较（目录里 1,550 行是 EIP-55 校验和形态，
   `token_metadata.normalized_address` 是小写），匹配不上。
   改成：两个来源都纳入、按优先级取一个（trustwallet 优先）、EVM 链大小写归一、
   非 EVM（TRON base58 / Solana）保持原样。
   实测：`tokenindex -resolve-logos` 从 **0 行 → 559 行**。
2. **logo worker 只拨 DNS 的第一个 IP**：`raw.githubusercontent.com` 同时解析出 IPv6/IPv4，
   IPv6 那条路会间歇性被 reset（同进程实测 9 次里 1 次），一失败就白等一轮 5 分钟扫描。
   改成逐个尝试所有解析到的公网 IP（私网/特殊地址仍在尝试前整体拒绝，SSRF 约束不放宽）。

### 抓不动的图床 → 可配置图片代理

实测出网：`raw.githubusercontent.com` 主要可达但抖动；**`assets.coingecko.com` 完全不通**
（直连 25s 超时，Go 客户端同样超时）——而 CoinGecko tokenlist 目录里 13,209 个 logo_url 全在这个域名上。

因此给 worker 加了**可选镜像通道**（`deploy/.env`）：

```
TOKEN_LOGO_MIRROR_BASE=https://images.weserv.nl/?url=
TOKEN_LOGO_MIRROR_HOSTS=assets.coingecko.com
```

命中 MirrorHosts 的源站改走代理抓取（实测代理 200 image/jpeg、0.8s），
`token_metadata.logo_url` 仍保存**原始事实 URL**，代理只是抓取通道，留空即关闭。
配置项已登记进 `deploy/.env.example`。

### 还有多少是真的没图标

对"有元数据、没图标"的 bsc 代币随机抽 40 个，逐个按 EIP-55 校验和路径去 Trust Wallet
仓库探（jsDelivr 镜像，HEAD）：

```
命中 1 / 未命中 43（含对照组 PancakeSwap CAKE = 200 命中）
其中 Fox Market 0x44404e… / 0x7f2d86…、Staked FOX 0xf5a04d… 全部 404
```

结论：**这些代币在 Trust Wallet / 公开目录里就没有图标**（多为 spam/未知合约）。
OKLink 上能看到它们的图标，靠的是它自建的图标库/人工提交——不是某个公开源能拉到的。
要"一模一样"，只能给平台加人工图标入口（admin 白名单：指定 URL 或直接上传），本轮未做。

## 复现命令

```bash
# L1 清退（先跑 freeze + 抽样留证，见脚本头部注释）
curl -sS "http://127.0.0.1:8123/?user=blockchain&password=$CH_PW" \
  --data-binary "DELETE FROM blockchain.token_transfers WHERE length(tx_hash) NOT IN (64,66,87,88)"

# L3 回填 logo_url（重建 tokenindex 后）
cd backend && go build -o bin/tokenindex ./cmd/tokenindex
set -a; . ../deploy/.env; set +a
./bin/tokenindex -resolve-logos

# L3 本地化由 API 进程内的 logo worker 自动做（每 5 分钟一轮、每轮 200 张、4 并发）
psql "$MIGRATION_DATABASE_URL" -c "select count(*) filter (where coalesce(logo_url,'')<>'') as with_url,
       count(*) filter (where coalesce(logo_object_key,'')<>'') as localized from token_metadata;"
```

## L3 续：人工/外部图标入库能力（"和 OKLink 一样"缺的就是这块）

公开目录已经榨干（TW / CoinGecko / PancakeSwap / TW chain tokenlist 全部试过），
剩下 34,972 个代币的图标**在任何可达公开源里都不存在**：DexScreener、GeckoTerminal、
CoinGecko API 在本机出网被切断（proxy 只能取图，不能取 JSON/HTML）；BscScan 代币页 403，
而且按 `/token/images/<slug>_32.png` 逐个试探（FOX / sFOX 等）全部 404 —— **BscScan 自己也没有**。
OKLink 能给它们出图，是因为它有自建图标库（人工/社区提交）。所以补上这条入口：

**接口**（`admin:dataset:manage`）：

```
POST   /api/v1/admin/tokens/{chain}/{contract}/logo   # JSON {"url": "https://…"} 或直接 POST 图片字节
DELETE /api/v1/admin/tokens/{chain}/{contract}/logo   # 清除图标（纠正错误图标用）
```

- URL 形态与 worker 扫描共用同一套校验（https + 公网 IP + ≤2MB + PNG/JPEG/WebP + ≤4096px），
  配了图片镜像的 host 同样自动走镜像；
- 上传形态直接把字节写进 MinIO `token-assets/{chain}/{contract}/logo.<ext>`；
- 落库即生效（读路径按 `logo_object_key` 取图，不需要等 5 分钟扫描）；
- `metadata_source` 标记为 `curated`，与目录来源可区分。

**批量**：`cmd/tokenindex -logos <file>`，每行 `chain<TAB>address<TAB>图标URL或本地图片路径`。
一次补几百个就是一条命令；`SetLogoFromURL`/`SetLogoBytes`/`ClearLogo` 与接口共用同一实现。

**实测（round-trip）**：CAKE（bsc `0x0e09fa…`）清空对象键 → `/logo` 立刻 404；
用 `-logos` 重新指派同一 URL（走镜像抓取）→ `/logo` 立刻 200。图标设置/清除两条路都验证过。

**待补清单**：`l3-missing-logo-worklist-top300.tsv` —— 近 30 天转账笔数最高的 300 个无图标代币
（chain / address / symbol / name / 30d 笔数 / 区块浏览器链接）。补前几十个就能覆盖列表里
绝大多数可见内容；素材需要从项目官网、社区或自有图标库取——这一步是人工/外部来源，本轮只把
入口和清单准备好。

## 门禁

- `go build ./...`、`go test -count=1 ./internal/tokenmeta/` 通过（新增镜像 URL 改写单测）；
- ClickHouse/PostgreSQL 侧对账 SQL 全部为 0 残留异常（见上表）；
- 前端未改动，`deploy/.env.example` 补齐两个环境变量。
