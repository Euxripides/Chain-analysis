package chain

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// NativeAsset 链原生资产（§23）。
type NativeAsset struct {
	Symbol   string `json:"symbol"`
	Decimals int    `json:"decimals"`
}

// Config 单条链的策略配置（§22/§23）。
type Config struct {
	Slug string `json:"slug"`
	Type string `json:"type"` // evm / 未来非 EVM
	Name string `json:"name"`

	ChainID uint64      `json:"chain_id"`
	Native  NativeAsset `json:"native"`

	// ConfirmationDepth Safe Head 回退深度（§24：safe_head = head - reorg_buffer）
	ConfirmationDepth uint64 `json:"confirmation_depth"`

	// SupportsTraceConcept 链本身是否有 trace 概念（与 Provider 能力无关，§19）
	SupportsTraceConcept bool `json:"supports_trace_concept"`

	// BlockTags 该链支持的区块标签（§153：根据链能力）
	BlockTags []string `json:"block_tags"`

	// MaxLogBlockRange eth_getLogs 初始区间上限（§97/§98）
	MaxLogBlockRange uint64 `json:"max_log_block_range"`

	DefaultProvider    string `json:"default_provider"`
	ExplorerTxURL      string `json:"explorer_tx_url"`
	ExplorerAddressURL string `json:"explorer_address_url"`

	Enabled bool `json:"enabled"`
}

// IsEVM 是否 EVM 链。
func (c Config) IsEVM() bool { return strings.EqualFold(c.Type, "evm") }

// SupportsTag 该链是否支持某区块标签。
func (c Config) SupportsTag(tag string) bool {
	if tag == "" || tag == TagLatest {
		return true
	}
	for _, t := range c.BlockTags {
		if strings.EqualFold(t, tag) {
			return true
		}
	}
	return false
}

// SafeHead 链头 → 安全高度（§24）。绝不允许业务直接把 eth_blockNumber 当 Coverage Target。
func (c Config) SafeHead(head uint64) uint64 {
	if head <= c.ConfirmationDepth {
		return 0
	}
	return head - c.ConfirmationDepth
}

// Registry 唯一 Chain Policy 来源（§22/§247-28）。
type Registry struct {
	mu     sync.RWMutex
	chains map[string]Config
}

// defaultChains 首期 BSC，其余 EVM 链预置以便后续开启（§8 目标链）。
func defaultChains() []Config {
	evmTags := []string{TagLatest, TagSafe, TagFinalized, TagPending, TagEarliest}
	return []Config{
		{Slug: "bsc", Type: "evm", Name: "BNB Smart Chain", ChainID: 56,
			Native: NativeAsset{Symbol: "BNB", Decimals: 18}, ConfirmationDepth: 15,
			SupportsTraceConcept: true, BlockTags: evmTags, MaxLogBlockRange: 5000,
			DefaultProvider: "ankr", Enabled: true,
			ExplorerTxURL: "https://www.oklink.com/bsc/tx/", ExplorerAddressURL: "https://www.oklink.com/bsc/address/"},
		{Slug: "eth", Type: "evm", Name: "Ethereum", ChainID: 1,
			Native: NativeAsset{Symbol: "ETH", Decimals: 18}, ConfirmationDepth: 12,
			SupportsTraceConcept: true, BlockTags: evmTags, MaxLogBlockRange: 2000,
			DefaultProvider: "ankr", Enabled: true,
			ExplorerTxURL: "https://www.oklink.com/eth/tx/", ExplorerAddressURL: "https://www.oklink.com/eth/address/"},
		{Slug: "polygon", Type: "evm", Name: "Polygon PoS", ChainID: 137,
			Native: NativeAsset{Symbol: "POL", Decimals: 18}, ConfirmationDepth: 64,
			SupportsTraceConcept: true, BlockTags: evmTags, MaxLogBlockRange: 2000,
			DefaultProvider: "ankr", Enabled: true,
			ExplorerTxURL: "https://www.oklink.com/polygon/tx/", ExplorerAddressURL: "https://www.oklink.com/polygon/address/"},
		{Slug: "arbitrum", Type: "evm", Name: "Arbitrum One", ChainID: 42161,
			Native: NativeAsset{Symbol: "ETH", Decimals: 18}, ConfirmationDepth: 20,
			SupportsTraceConcept: true, BlockTags: evmTags, MaxLogBlockRange: 5000,
			DefaultProvider: "ankr", Enabled: true,
			ExplorerTxURL: "https://www.oklink.com/arbitrum/tx/", ExplorerAddressURL: "https://www.oklink.com/arbitrum/address/"},
		{Slug: "base", Type: "evm", Name: "Base", ChainID: 8453,
			Native: NativeAsset{Symbol: "ETH", Decimals: 18}, ConfirmationDepth: 20,
			SupportsTraceConcept: true, BlockTags: evmTags, MaxLogBlockRange: 5000,
			DefaultProvider: "ankr", Enabled: true,
			ExplorerTxURL: "https://www.oklink.com/base/tx/", ExplorerAddressURL: "https://www.oklink.com/base/address/"},
		{Slug: "optimism", Type: "evm", Name: "OP Mainnet", ChainID: 10,
			Native: NativeAsset{Symbol: "ETH", Decimals: 18}, ConfirmationDepth: 20,
			SupportsTraceConcept: true, BlockTags: evmTags, MaxLogBlockRange: 5000,
			DefaultProvider: "ankr", Enabled: true,
			ExplorerTxURL: "https://www.oklink.com/optimism/tx/", ExplorerAddressURL: "https://www.oklink.com/optimism/address/"},
		{Slug: "avalanche", Type: "evm", Name: "Avalanche C-Chain", ChainID: 43114,
			Native: NativeAsset{Symbol: "AVAX", Decimals: 18}, ConfirmationDepth: 20,
			SupportsTraceConcept: true, BlockTags: evmTags, MaxLogBlockRange: 2000,
			DefaultProvider: "ankr", Enabled: true,
			ExplorerTxURL: "https://www.oklink.com/avax/tx/", ExplorerAddressURL: "https://www.oklink.com/avax/address/"},
	}
}

// NewRegistry 用给定配置构造（测试与显式装配用）。
func NewRegistry(configs []Config) *Registry {
	r := &Registry{chains: make(map[string]Config, len(configs))}
	for _, c := range configs {
		c.Slug = strings.ToLower(strings.TrimSpace(c.Slug))
		if c.Slug == "" {
			continue
		}
		if c.Type == "" {
			c.Type = "evm"
		}
		r.chains[c.Slug] = c
	}
	return r
}

// DefaultRegistry 预置链表。
func DefaultRegistry() *Registry { return NewRegistry(defaultChains()) }

// LoadRegistry 预置链表 + JSON 覆盖（按 slug 合并）。
// 覆盖用于运维调整 confirmation_depth / 默认 Provider / 启用状态，无需改代码（§23/§247-28）。
func LoadRegistry(overrideJSON string) (*Registry, error) {
	r := DefaultRegistry()
	body := strings.TrimSpace(overrideJSON)
	if body == "" {
		return r, nil
	}
	var overrides []Config
	if err := json.Unmarshal([]byte(body), &overrides); err != nil {
		return nil, fmt.Errorf("chain registry override: %w", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, o := range overrides {
		slug := strings.ToLower(strings.TrimSpace(o.Slug))
		if slug == "" {
			continue
		}
		base, exists := r.chains[slug]
		if !exists {
			if o.Type == "" {
				o.Type = "evm"
			}
			o.Slug = slug
			r.chains[slug] = o
			continue
		}
		if o.ChainID != 0 {
			base.ChainID = o.ChainID
		}
		if o.Name != "" {
			base.Name = o.Name
		}
		if o.Native.Symbol != "" {
			base.Native = o.Native
		}
		if o.ConfirmationDepth != 0 {
			base.ConfirmationDepth = o.ConfirmationDepth
		}
		if o.MaxLogBlockRange != 0 {
			base.MaxLogBlockRange = o.MaxLogBlockRange
		}
		if o.DefaultProvider != "" {
			base.DefaultProvider = o.DefaultProvider
		}
		if len(o.BlockTags) > 0 {
			base.BlockTags = o.BlockTags
		}
		if o.ExplorerTxURL != "" {
			base.ExplorerTxURL = o.ExplorerTxURL
		}
		base.Enabled = o.Enabled
		base.SupportsTraceConcept = o.SupportsTraceConcept
		r.chains[slug] = base
	}
	return r, nil
}

// Get 查询链配置。
func (r *Registry) Get(slug string) (Config, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.chains[strings.ToLower(strings.TrimSpace(slug))]
	return c, ok
}

// MustGet 未知链返回错误，避免业务层用未注册链继续执行（§247-28）。
func (r *Registry) Require(slug string) (Config, error) {
	c, ok := r.Get(slug)
	if !ok {
		return Config{}, fmt.Errorf("unknown chain %q", strings.ToLower(strings.TrimSpace(slug)))
	}
	if !c.Enabled {
		return Config{}, fmt.Errorf("chain %s is disabled", c.Slug)
	}
	return c, nil
}

// List 按 slug 排序返回全部链。
func (r *Registry) List() []Config {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Config, 0, len(r.chains))
	for _, c := range r.chains {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out
}

// SafeHead 按链策略计算安全高度（§24）。未知链退化为不回退。
func (r *Registry) SafeHead(slug string, head uint64) uint64 {
	c, ok := r.Get(slug)
	if !ok {
		return head
	}
	return c.SafeHead(head)
}
