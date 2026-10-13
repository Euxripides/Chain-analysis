package chain

import "strings"

// AnkrAdvancedMainnets is the explicit mainnet allowlist for ankr_* history
// methods. Node API support alone does not establish address-history support.
// Source: https://www.ankr.com/docs/advanced-api/overview/ (2026-10-04).
func AnkrAdvancedMainnets() []string {
	return []string{"arbitrum", "avalanche", "base", "bsc", "eth", "fantom", "flare", "gnosis", "linea", "optimism", "polygon", "scroll", "somnia_mainnet", "story_mainnet", "taiko", "telos", "xlayer"}
}

func IsAnkrAdvancedMainnet(slug string) bool {
	slug = strings.ToLower(strings.TrimSpace(slug))
	if slug == "ethereum" {
		slug = "eth"
	}
	for _, candidate := range AnkrAdvancedMainnets() {
		if slug == candidate {
			return true
		}
	}
	return false
}

// AnkrRPCPath shares the network identity with the history allowlist.
func AnkrRPCPath(slug string) (string, bool) {
	slug = strings.ToLower(strings.TrimSpace(slug))
	if slug == "ethereum" {
		slug = "eth"
	}
	return slug, IsAnkrAdvancedMainnet(slug)
}

func additionalAnkrMainnets() []Config {
	items := []struct {
		slug, name, symbol, explorer string
		id                           uint64
	}{
		{"fantom", "Fantom Opera", "FTM", "https://ftmscan.com", 250},
		{"flare", "Flare", "FLR", "https://flare-explorer.flare.network", 14},
		{"gnosis", "Gnosis", "xDAI", "https://gnosisscan.io", 100},
		{"linea", "Linea", "ETH", "https://lineascan.build", 59144},
		{"scroll", "Scroll", "ETH", "https://scrollscan.com", 534352},
		{"somnia_mainnet", "Somnia", "SOMI", "https://explorer.somnia.network", 5031},
		{"story_mainnet", "Story", "IP", "https://www.storyscan.io", 1514},
		{"taiko", "Taiko", "ETH", "https://taikoscan.io", 167000},
		{"telos", "Telos EVM", "TLOS", "https://www.teloscan.io", 40},
		{"xlayer", "X Layer", "OKB", "https://www.oklink.com/xlayer", 196},
	}
	configs := make([]Config, 0, len(items))
	for _, item := range items {
		configs = append(configs, Config{
			Slug: item.slug, Type: "evm", Name: item.name, ChainID: item.id,
			Native: NativeAsset{Symbol: item.symbol, Decimals: 18}, ConfirmationDepth: 20,
			SupportsTraceConcept: true, BlockTags: []string{TagLatest, TagPending, TagEarliest},
			MaxLogBlockRange: 2000, DefaultProvider: "ankr", Enabled: true,
			ExplorerTxURL: item.explorer + "/tx/", ExplorerAddressURL: item.explorer + "/address/",
		})
	}
	return configs
}
