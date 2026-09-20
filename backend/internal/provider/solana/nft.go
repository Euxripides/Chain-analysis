package solana

// 本文件实现 nft_transfers（ADR-024 D3）。
//
// Solana 的经典 NFT 就是 decimals=0 && supply=1 的 SPL/Token-2022 mint，
// 转账指令与普通代币完全相同，因此这里只做**归类**，不重复解码。
//
// 边界（诚实标注）：
//   - 只做经典 NFT；压缩 NFT（Bubblegum）不移动 SPL 代币，不在范围内；
//   - 判定不出的 mint 保守留在 token_transfers 并计数，绝不两边都写；
//   - 元数据（name/symbol）没有可靠来源 → 留空；tokenId 留空（Solana 没有 tokenId）。

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
)

// MethodAccountInfo 是 NFT 归类用的 mint 账户查询。
const MethodAccountInfo = "getAccountInfo"

// maxMintCacheEntries 限制分类缓存大小（进程内、有界，避免无上限增长）。
const maxMintCacheEntries = 4096

type mintClass struct {
	isNFT bool
	known bool
}

// mintClasses 是进程内的 mint → 是否经典 NFT 缓存（含负缓存）。
var mintClasses = newMintCache()

type mintCache struct {
	mu    sync.Mutex
	items map[string]mintClass
}

func newMintCache() *mintCache { return &mintCache{items: map[string]mintClass{}} }

func (m *mintCache) lookup(mint string) (mintClass, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	class, ok := m.items[mint]
	return class, ok
}

func (m *mintCache) store(mint string, class mintClass) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.items) >= maxMintCacheEntries {
		// 简单的整体清空：缓存只是加速手段，清空不会影响正确性。
		m.items = map[string]mintClass{}
	}
	m.items[mint] = class
}

// classifyMint 查询 mint 账户并判定经典 NFT；查不到返回 known=false（保守处理）。
func (c *Client) classifyMint(ctx context.Context, mint string) mintClass {
	if cached, ok := mintClasses.lookup(mint); ok {
		return cached
	}
	class := mintClass{}
	result, _, err := c.callWithSize(ctx, MethodAccountInfo, []any{mint, map[string]any{"encoding": "jsonParsed"}})
	if err == nil {
		var account struct {
			Value struct {
				Data struct {
					Parsed struct {
						Info struct {
							Decimals uint8  `json:"decimals"`
							Supply   string `json:"supply"`
						} `json:"info"`
					} `json:"parsed"`
				} `json:"data"`
			} `json:"value"`
		}
		if err := json.Unmarshal(result, &account); err == nil {
			info := account.Value.Data.Parsed.Info
			if strings.TrimSpace(info.Supply) != "" {
				class.known = true
				class.isNFT = info.Decimals == 0 && strings.TrimSpace(info.Supply) == "1"
			}
		}
	}
	if !class.known {
		c.log.Warn("solana mint classification unknown", "mint", mint)
	}
	mintClasses.store(mint, class)
	return class
}

// classifyPageMints 对本页出现过的 distinct mint 做一次归类，返回 unknown 计数。
func (c *Client) classifyPageMints(ctx context.Context, rows []tokenTransferRow) (map[string]mintClass, int) {
	classes := map[string]mintClass{}
	unknown := 0
	for _, row := range rows {
		mint := row.ContractAddress
		if mint == "" {
			continue
		}
		if _, ok := classes[mint]; ok {
			continue
		}
		class := c.classifyMint(ctx, mint)
		classes[mint] = class
		if !class.known {
			unknown++
		}
	}
	return classes, unknown
}

// nftTransferRow 与 Ankr 的 NFT 转账行同构；tokenId 恒为空（Solana 没有 tokenId）。
type nftTransferRow struct {
	BlockHeight     string `json:"blockHeight"`
	Timestamp       int64  `json:"timestamp"`
	TransactionHash string `json:"transactionHash"`
	LogIndex        uint32 `json:"logIndex"`
	ContractAddress string `json:"contractAddress"`
	TokenID         string `json:"tokenId"`
	FromAddress     string `json:"fromAddress"`
	ToAddress       string `json:"toAddress"`
	TokenName       string `json:"name"`
	TokenSymbol     string `json:"tokenSymbol"`
	// Solana 没有 ERC 标准：经典 NFT 由 SPL Token / Token-2022 承载，如实标注。
	TokenStandard string `json:"tokenStandard"`
	Value         string `json:"value"`
}

// toNFTRows 只保留判定为经典 NFT 的转账。
func toNFTRows(rows []tokenTransferRow, classes map[string]mintClass) []nftTransferRow {
	out := make([]nftTransferRow, 0, len(rows))
	for _, row := range rows {
		class, ok := classes[row.ContractAddress]
		if !ok || !class.known || !class.isNFT {
			continue
		}
		out = append(out, nftTransferRow{
			BlockHeight: row.BlockHeight, Timestamp: row.Timestamp, TransactionHash: row.TransactionHash,
			LogIndex: row.LogIndex, ContractAddress: row.ContractAddress,
			TokenID:     "",
			FromAddress: row.FromAddress, ToAddress: row.ToAddress,
			TokenName: "", TokenSymbol: "",
			TokenStandard: "SPL",
			Value:         row.GrossAmountRaw,
		})
	}
	return out
}

// filterFungibleTransfers 从 token_transfers 中剔除已判定为 NFT 的转账，
// 避免同一笔转账同时出现在两个数据集（ADR-024 D3）。
func filterFungibleTransfers(rows []tokenTransferRow, classes map[string]mintClass) ([]tokenTransferRow, int) {
	kept := make([]tokenTransferRow, 0, len(rows))
	skipped := 0
	for _, row := range rows {
		if class, ok := classes[row.ContractAddress]; ok && class.known && class.isNFT {
			skipped++
			continue
		}
		kept = append(kept, row)
	}
	return kept, skipped
}
