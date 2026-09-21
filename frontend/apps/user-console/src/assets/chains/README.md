# 链图标（本地资源，禁止远程热链）

文件来源：**Trust Wallet Assets**（MIT）官方链标识，按"链标识用于识别"的通用做法随仓库本地分发。

```text
https://github.com/trustwallet/assets/tree/master/blockchains/<chain>/info/logo.png
```

与 `ChainIcon` 的映射（键 = 服务端 `/api/v2/meta/chains` 返回的 chain slug）：

| slug | 文件 | 上游目录 |
|---|---|---|
| `ethereum` | `ethereum.png` | `blockchains/ethereum` |
| `bsc` | `bsc.png` | `blockchains/smartchain` |
| `arbitrum` | `arbitrum.png` | `blockchains/arbitrum` |
| `optimism` | `optimism.png` | `blockchains/optimism` |
| `base` | `base.png` | `blockchains/base` |
| `polygon` | `polygon.png` | `blockchains/polygon` |
| `solana` | `solana.png` | `blockchains/solana` |
| `avalanche` | `avalanche.png` | `blockchains/avalanchec` |
| `tron` | `tron.png` | `blockchains/tron` |

未登记的链不猜图标：`ChainIcon` 回落为"圆形底色 + 链名称首字母"（重构规范 §11）。

商标归各链方所有，此处仅用于标识数据来源；新增链时按上表补一个文件并登记到
`pages/downloads/components/ChainIcon.tsx` 的 `CHAIN_LOGOS`。
