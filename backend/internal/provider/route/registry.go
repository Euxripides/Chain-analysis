// Package route 提供运行时 Provider 路由（(chain, dataset) → Provider/通道/操作类）与性能档位解析。
//
// 为什么单独成包：下载引擎只认 provider.Provider，而"哪条链的哪个数据集由哪个 Provider 服务"
// 由数据库策略行与内置默认表共同决定（实施规范 §1.2）。把这张表留在 app.go 会让
// "配置改了但下载仍按旧链级特判"这类回归无法单测。本包不 import internal/config：
// TRON 的 config.ProviderForDataset 结果由 app.go 经 SetTronRouter 注入，避免包循环。
package route

import (
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

// Name 是 Registry 作为 provider.Provider 的名字：它标识分派层，而不是某条链的实现。
const Name = "route"

// 内置默认表使用的 Provider Key（与 app.go 注册的运行时 Key 一致）。
const (
	ProviderDRPC     = "drpc"
	ProviderAnkr     = "ankr"
	ProviderTronGrid = "trongrid"
)

// API 通道：与 provider_performance_profiles 的通道口径、row 契约里的 sourceApi 对齐。
const (
	ChannelDataAPI      = "data_api"
	ChannelSolanaRPC    = "solana_rpc"
	ChannelAnkrAdvanced = "ankr_advanced"
	ChannelTronGrid     = "trongrid"
	ChannelAnkrTronNode = "ankr_tron_node"
)

// 链与数据集字面量：内置表按这些值匹配，调用方传大小写/空白变体也能命中。
const (
	ChainSolana = "solana"
	ChainTron   = "tron"

	DatasetTransactions         = "transactions"
	DatasetTokenTransfers       = "token_transfers"
	DatasetNFTTransfers         = "nft_transfers"
	DatasetBalances             = "balances"
	DatasetLogs                 = "logs"
	DatasetInternalTransactions = "internal_transactions"

	// wildcard 同时用于策略行的 dataset 与内置通配路由。
	wildcard = "*"
)

// 操作类：Provider 内部的抓取/计量口径，决定用哪一档 Profile。
const (
	OperationSolanaHistory = "solana_history"
	OperationSolanaBalance = "solana_balance"
	OperationSolanaDetail  = "solana_detail"
	OperationEVMHistory    = "evm_history"
	OperationTronHistory   = "tron_history"
)

// Route 是一次 (chain, dataset) 解析结果。
//
// PolicyRevision 必须随 Shard 落库（download_shards.routing_policy_revision），
// 否则事后无法判断某批数据是按哪一版路由策略抓的（开发文档 §9.1）。内置默认表不是
// 数据库策略行，因此 PolicyKey 标注为 builtin:* 且 revision 恒为 0。
type Route struct {
	ProviderKey    string `json:"provider_key"`
	APIChannel     string `json:"api_channel"`
	OperationClass string `json:"operation_class"`
	PolicyKey      string `json:"routing_policy_key"`
	PolicyRevision int64  `json:"routing_policy_revision"`
}

// RuntimeProvider 是已注册的运行时实现（后台探针经 Get 取用）。
type RuntimeProvider struct {
	Key         string
	DisplayName string
	Channels    []string
	Provider    provider.Provider
}

// PolicyRow 是 routing_policies 的解析投影，由 store 读取后经 LoadPolicies 注入。
type PolicyRow struct {
	PolicyKey       string
	Chain           string
	Dataset         string
	Enabled         bool
	PrimaryProvider string
	APIChannel      string
	OperationClass  string
	Revision        int64
}

// Registry 解析路由与档位，并作为 provider.Provider 分派抓取请求。所有方法可并发调用。
type Registry struct {
	log *slog.Logger

	mu        sync.RWMutex
	providers map[string]RuntimeProvider
	policies  map[string]PolicyRow // "chain\x00dataset" -> revision 最大的那一行
	fallback  provider.Provider

	// Solana 默认路由受 DRPC_ENABLED 开关控制（实施规范 §1.2）：未启用时必须未命中，
	// 让调用方回退旧的 provider/solana；否则 Solana 下载会被通配路由引到 Ankr Advanced API。
	solanaEnabled  bool
	solanaProvider string
	// tronRouter 由 app.go 注入 config.TronConfig.ProviderForDataset 的适配结果。
	// 用未命名的函数类型而不是自定义类型：调用方传 config 侧的具名函数类型也能直接赋值。
	tronRouter func(dataset string) (providerKey, channel string)

	profiles ProfileSource
}

// NewRegistry 构造路由注册表。未注册任何 Provider、未开启 Solana 路由时，
// Resolve 一律未命中——调用方据此回退旧的链级特判。
func NewRegistry(log *slog.Logger) *Registry {
	if log == nil {
		log = slog.Default()
	}
	return &Registry{
		log:       log,
		providers: make(map[string]RuntimeProvider, 8),
		policies:  make(map[string]PolicyRow),
	}
}

var _ provider.Provider = (*Registry)(nil)

// Register 注册运行时 Provider。同 key 重复注册以最后一次为准：配置热更新后
// 探针与下载引擎必须指向同一个实现，而不是各留一份。
func (r *Registry) Register(key, displayName string, channels []string, p provider.Provider) {
	key = normalizeKey(key)
	if key == "" || p == nil {
		r.logger().Warn("ignore provider registration without key or implementation", "provider", key)
		return
	}
	if displayName == "" {
		displayName = key
	}
	r.mu.Lock()
	r.providers[key] = RuntimeProvider{
		Key: key, DisplayName: displayName,
		Channels: dedupeStrings(channels), Provider: p,
	}
	r.mu.Unlock()
}

// Get 按 provider_key 取运行时实现（后台探针用）。找不到返回 ok=false，
// 调用方不需要知道是"没注册"还是"注册了空实现"。
func (r *Registry) Get(key string) (provider.Provider, bool) {
	r.mu.RLock()
	rp, ok := r.providers[normalizeKey(key)]
	r.mu.RUnlock()
	if !ok || rp.Provider == nil {
		return nil, false
	}
	return rp.Provider, true
}

// List 返回已注册 Provider 的快照，按 Key 排序：后台展示必须有稳定顺序。
func (r *Registry) List() []RuntimeProvider {
	r.mu.RLock()
	out := make([]RuntimeProvider, 0, len(r.providers))
	for _, rp := range r.providers {
		rp.Channels = append([]string(nil), rp.Channels...)
		out = append(out, rp)
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// SetFallback 设置路由未命中或命中但 Provider 未注册时的兜底实现（通常是 Ankr）。
// 不设置时未命中将返回 unsupported 错误，绝不静默取到别的链的数据。
func (r *Registry) SetFallback(p provider.Provider) {
	r.mu.Lock()
	r.fallback = p
	r.mu.Unlock()
}

// SetSolanaRoute 开关 Solana 内置路由，并指定其 Provider Key（空则用 drpc）。
//
// app.go 必须传入 cfg.DRPC.Enabled（以及 SOLANA_PROVIDER=drpc 的判定结果）：
// 未启用时 Resolve("solana", *) 返回未命中，调用方继续用旧 provider/solana，
// 这是 dRPC 未配置时不回归的唯一保障。
func (r *Registry) SetSolanaRoute(enabled bool, providerKey string) {
	providerKey = normalizeKey(providerKey)
	if providerKey == "" {
		providerKey = ProviderDRPC
	}
	r.mu.Lock()
	r.solanaEnabled, r.solanaProvider = enabled, providerKey
	r.mu.Unlock()
}

// SetTronRouter 注入 TRON 的 dataset→Provider 解析结果（config.TronConfig.ProviderForDataset）。
//
// 为什么用回调注入：route 包不能 import config（config 已经 import 了本包将要替代的链级
// 判定语义，直接依赖会形成循环）。channel 可留空，此时按 providerKey 取默认通道；
// 未注入时 TRON 路由一律未命中，调用方保持现有 TRON 分支不变。
func (r *Registry) SetTronRouter(fn func(dataset string) (providerKey, channel string)) {
	r.mu.Lock()
	r.tronRouter = fn
	r.mu.Unlock()
}

// LoadPolicies 注入数据库策略行快照（全量替换）。优先级高于内置默认表。
//
// 同一 (chain, dataset) 出现多行时保留 revision 最大者：策略是按版本生效的，
// 结果不能依赖 SQL 返回顺序。
func (r *Registry) LoadPolicies(rows []PolicyRow) {
	index := make(map[string]PolicyRow, len(rows))
	for _, row := range rows {
		chain := normalizeKey(row.Chain)
		if chain == "" {
			continue // 没有链的策略行无法参与 (chain, dataset) 解析
		}
		dataset := normalizeKey(row.Dataset)
		if dataset == "" {
			dataset = wildcard
		}
		key := policyIndexKey(chain, dataset)
		if prev, ok := index[key]; ok && prev.Revision > row.Revision {
			continue
		}
		index[key] = row
	}
	r.mu.Lock()
	r.policies = index
	r.mu.Unlock()
}

// Resolve 解析 (chain, dataset) -> Route。
//
// 优先级：1) 数据库 routing_policies（LoadPolicies 注入）2) 内置默认表。
// 未命中返回 ok=false，调用方回退旧的链级特判（sourceProviderForDataset）。
func (r *Registry) Resolve(chain, dataset string) (Route, bool) {
	chain, dataset = normalizeKey(chain), normalizeKey(dataset)
	if chain == "" || dataset == "" {
		return Route{}, false
	}

	r.mu.RLock()
	row, hasPolicy := lookupPolicy(r.policies, chain, dataset)
	solanaEnabled, solanaProvider, tronRouter := r.solanaEnabled, r.solanaProvider, r.tronRouter
	r.mu.RUnlock()

	if hasPolicy {
		// 显式停用或缺少 Provider 的策略行必须 fail closed：继续落到内置表会让运维
		// "以为已经停掉了某条路由"。这里不回读 r 的其它字段，避免持锁递归。
		if !row.Enabled || normalizeKey(row.PrimaryProvider) == "" {
			return Route{}, false
		}
		resolved := Route{
			ProviderKey:    normalizeKey(row.PrimaryProvider),
			APIChannel:     strings.TrimSpace(row.APIChannel),
			OperationClass: strings.TrimSpace(row.OperationClass),
			PolicyKey:      strings.TrimSpace(row.PolicyKey),
			PolicyRevision: row.Revision,
		}
		if resolved.APIChannel == "" || resolved.OperationClass == "" {
			resolved = completeFromBuiltin(chain, dataset, resolved, solanaEnabled, solanaProvider, tronRouter)
		}
		return resolved, true
	}
	return resolveBuiltin(chain, dataset, solanaEnabled, solanaProvider, tronRouter)
}

// completeFromBuiltin 用内置表补齐数据库行缺失的通道/操作类，但仅当 Provider 相同。
// 通道与操作类是 Provider 的实现细节，跨 Provider 套用会造出 "ankr + data_api" 这种假元数据。
func completeFromBuiltin(chain, dataset string, resolved Route, solanaEnabled bool, solanaProvider string, tronRouter func(string) (string, string)) Route {
	builtin, ok := resolveBuiltin(chain, dataset, solanaEnabled, solanaProvider, tronRouter)
	if !ok || builtin.ProviderKey != resolved.ProviderKey {
		return resolved
	}
	if resolved.APIChannel == "" {
		resolved.APIChannel = builtin.APIChannel
	}
	if resolved.OperationClass == "" {
		resolved.OperationClass = builtin.OperationClass
	}
	return resolved
}

// resolveBuiltin 查内置默认路由表（实施规范 §1.2）。纯函数：路由开关以入参快照传入，
// 避免在持锁路径上再次取锁。
func resolveBuiltin(chain, dataset string, solanaEnabled bool, solanaProvider string, tronRouter func(string) (string, string)) (Route, bool) {
	switch chain {
	case ChainSolana:
		if !solanaEnabled {
			return Route{}, false
		}
		if solanaProvider == "" {
			solanaProvider = ProviderDRPC
		}
		switch dataset {
		case DatasetTransactions, DatasetTokenTransfers, DatasetNFTTransfers:
			return builtinRoute(solanaProvider, ChannelDataAPI, OperationSolanaHistory, chain, dataset), true
		case DatasetBalances:
			return builtinRoute(solanaProvider, ChannelSolanaRPC, OperationSolanaBalance, chain, dataset), true
		case DatasetLogs:
			return builtinRoute(solanaProvider, ChannelSolanaRPC, OperationSolanaDetail, chain, dataset), true
		}
		return Route{}, false
	case ChainTron:
		switch dataset {
		case DatasetTransactions, DatasetTokenTransfers, DatasetInternalTransactions:
			if tronRouter == nil {
				return Route{}, false
			}
			providerKey, channel := tronRouter(dataset)
			providerKey, channel = normalizeKey(providerKey), normalizeKey(channel)
			if providerKey == "" {
				return Route{}, false
			}
			if channel == "" {
				channel = tronChannelFor(providerKey)
			}
			return builtinRoute(providerKey, channel, OperationTronHistory, chain, dataset), true
		}
		return Route{}, false
	default:
		// "其它非 tron" 通配为 Ankr Advanced API。这里额外排除 solana：dRPC 未启用时
		// Solana 必须继续由旧 provider/solana 承载，通配行命中 solana 会把 token_transfers
		// 这类旧链路能做的数据集错误地引到明确拒绝 solana 的 Ankr Advanced API。
		return builtinRoute(ProviderAnkr, ChannelAnkrAdvanced, OperationEVMHistory, chain, wildcard), true
	}
}

// tronChannelFor 把 TRON Provider Key 映射到 API 通道：内置表的
// "trongrid/ankr_tron_node" 二选一；未知 Key 留空，由调用方决定是否按通道限流。
func tronChannelFor(providerKey string) string {
	switch providerKey {
	case ProviderTronGrid:
		return ChannelTronGrid
	case ProviderAnkr:
		return ChannelAnkrTronNode
	default:
		return ""
	}
}

func builtinRoute(providerKey, channel, operation, chain, dataset string) Route {
	return Route{
		ProviderKey: providerKey, APIChannel: channel, OperationClass: operation,
		PolicyKey: "builtin:" + chain + ":" + dataset, PolicyRevision: 0,
	}
}

// lookupPolicy 先精确匹配 (chain, dataset)，再退到该链的 dataset 通配行。
func lookupPolicy(policies map[string]PolicyRow, chain, dataset string) (PolicyRow, bool) {
	if row, ok := policies[policyIndexKey(chain, dataset)]; ok {
		return row, true
	}
	row, ok := policies[policyIndexKey(chain, wildcard)]
	return row, ok
}

func policyIndexKey(chain, dataset string) string { return chain + "\x00" + dataset }

// normalizeKey 统一小写并去空白：chain/dataset/provider_key 在库内与内置表里都是小写字面量。
func normalizeKey(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func dedupeStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, item := range in {
		item = normalizeKey(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}

func (r *Registry) logger() *slog.Logger {
	if r.log == nil {
		return slog.Default()
	}
	return r.log
}
