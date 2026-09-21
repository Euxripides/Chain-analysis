package route

import (
	"context"
	"strings"
	"time"
)

// 模式字面量与 provider_performance_profiles.mode CHECK 一致。
const (
	ModeSafe  = "safe"
	ModeFast  = "fast"
	ModeTurbo = "turbo"
)

// Profile 是 (Provider, 链, 数据集, 操作类, 模式) 的性能档位（开发文档 §6.1/§10.1）。
//
// 字段与 provider_performance_profiles 的列一一对应，json tag 用库列名，
// 管理 API 可以直接透出而不必再维护一份 DTO。
type Profile struct {
	ProviderKey    string `json:"provider_key"`
	Chain          string `json:"chain"`
	Dataset        string `json:"dataset"`
	OperationClass string `json:"operation_class"`
	Mode           string `json:"mode"`

	ShardConcurrency      int     `json:"shard_concurrency"`
	RequestConcurrency    int     `json:"request_concurrency"`
	RequestsPerMinute     float64 `json:"requests_per_minute"`
	RequestsPerSecond     float64 `json:"requests_per_second"`
	CUPerMinute           int64   `json:"cu_per_minute"`
	PageSize              int     `json:"page_size"`
	BatchSize             int     `json:"batch_size"`
	RequestWeight         int     `json:"request_weight"`
	RequestTimeoutSeconds int     `json:"request_timeout_seconds"`
	HardCeiling           int     `json:"hard_ceiling"`

	Revision           int64      `json:"revision"`
	VerifiedAt         *time.Time `json:"verified_at,omitempty"`
	VerificationSource string     `json:"verification_source"`
}

// ProfileSource 是数据库档位读取端口（provider_performance_profiles）。
// route 不 import store：由 app.go 注入实现，测试可以用内存假实现。
type ProfileSource interface {
	PerformanceProfile(ctx context.Context, providerKey, chain, dataset, operation, mode string) (Profile, bool)
}

// SetProfileSource 注入数据库档位来源；为 nil 时只用 DefaultProfiles。
func (r *Registry) SetProfileSource(src ProfileSource) {
	r.mu.Lock()
	r.profiles = src
	r.mu.Unlock()
}

// Profile 解析档位：数据库 ProfileSource 优先，未命中回退 DefaultProfiles。
//
// mode 为空按 FAST：初始矩阵通过前 dRPC 默认 FAST（开发文档 §21.4）。
// 数据集/操作类是 Provider 的实现细节，匹配失败时返回 ok=false，由调用方决定
// 是报错还是用全局默认，不在这一层猜一个档位出来。
func (r *Registry) Profile(ctx context.Context, providerKey, chain, dataset, operation, mode string) (Profile, bool) {
	providerKey, chain, dataset = normalizeKey(providerKey), normalizeKey(chain), normalizeKey(dataset)
	operation, mode = strings.ToLower(strings.TrimSpace(operation)), normalizeMode(mode)
	if providerKey == "" || chain == "" || mode == "" {
		return Profile{}, false
	}

	r.mu.RLock()
	source := r.profiles
	r.mu.RUnlock()
	if source != nil {
		if profile, ok := source.PerformanceProfile(ctx, providerKey, chain, dataset, operation, mode); ok {
			return profile, true
		}
	}
	return lookupDefaultProfile(providerKey, chain, dataset, operation, mode)
}

// DefaultProfiles 返回内置 dRPC 两通道三档矩阵（开发文档 §6.2）。
//
// 为什么只内置 dRPC：Ankr/TronGrid 的档位由既有 ankr_production_profiles /
// ankr_performance_control 解析（§10.1：Resolver 优先读新表，Ankr 没有新 Profile 时回退旧表），
// 在这里再放一份 Ankr/TRON 默认值会形成第二个事实来源。
//
// CUPerMinute 是名义额度（单次 CU × 每分钟请求数）。免费/未知层级的 45,000 CU/分钟
// 是运行时闸门（DRPC_UNKNOWN_TIER_CU_PER_MINUTE），不能写进档位：否则付费账户的
// FAST/TURBO 会被错误地压到 24 RPM。RPM 与 RPS 同时给出且互相一致，避免消费方
// 只读其中一个字段时变成"无限流"。
//
// 返回值是副本：调用方修改它不会污染内置表。
func DefaultProfiles() []Profile {
	return append([]Profile(nil), defaultProfiles...)
}

var defaultProfiles = []Profile{
	// dRPC Solana Data API：transactions / token_transfers / nft_transfers 共用同一物理历史页，
	// 因此按操作类 solana_history + dataset 通配给出，避免同一页在三份档位里各写一遍。
	dataAPIDefault(OperationSolanaHistory, ModeSafe, 1, 2, 10, 1837, 100, 4, 60, 8),
	dataAPIDefault(OperationSolanaHistory, ModeFast, 2, 4, 30, 1837, 100, 4, 60, 8),
	dataAPIDefault(OperationSolanaHistory, ModeTurbo, 4, 8, 60, 1837, 100, 4, 60, 8),

	// dRPC Solana JSON-RPC：余额快照与程序日志明细。
	// ShardConcurrency=0 表示"不适用"（逐签名/逐账户补全没有历史 Shard 这一层，不是无限），
	// 限流由 RequestConcurrency + RPS 决定；Batch 首期恒为 1。
	rpcDefault(OperationSolanaBalance, ModeSafe, 8, 20, 20, 100, 1, 30, 48),
	rpcDefault(OperationSolanaBalance, ModeFast, 16, 40, 20, 100, 1, 30, 48),
	rpcDefault(OperationSolanaBalance, ModeTurbo, 32, 80, 20, 100, 1, 30, 48),
	rpcDefault(OperationSolanaDetail, ModeSafe, 8, 20, 20, 100, 1, 30, 48),
	rpcDefault(OperationSolanaDetail, ModeFast, 16, 40, 20, 100, 1, 30, 48),
	rpcDefault(OperationSolanaDetail, ModeTurbo, 32, 80, 20, 100, 1, 30, 48),
}

// dataAPIDefault 组装一档 Data API 档位：RPM 来自 §6.2 矩阵，CU 按单次 1837 CU 折算。
func dataAPIDefault(operation, mode string, shardConcurrency, requestConcurrency int, rpm float64, cuPerRequest int64, pageSize, requestWeight, timeoutSeconds, hardCeiling int) Profile {
	return Profile{
		ProviderKey: ProviderDRPC, Chain: ChainSolana, Dataset: wildcard,
		OperationClass: operation, Mode: mode,
		ShardConcurrency: shardConcurrency, RequestConcurrency: requestConcurrency,
		RequestsPerMinute: rpm, RequestsPerSecond: rpm / 60,
		CUPerMinute: int64(rpm) * cuPerRequest,
		PageSize:    pageSize, BatchSize: 1, RequestWeight: requestWeight,
		RequestTimeoutSeconds: timeoutSeconds, HardCeiling: hardCeiling,
		Revision: 1, VerificationSource: "initial",
	}
}

// rpcDefault 组装一档 JSON-RPC 档位：矩阵给的是 RPS，折算 CU 时按每分钟调用数计。
func rpcDefault(operation, mode string, requestConcurrency int, rps float64, cuPerCall int64, pageSize, batchSize, timeoutSeconds, hardCeiling int) Profile {
	rpm := rps * 60
	return Profile{
		ProviderKey: ProviderDRPC, Chain: ChainSolana, Dataset: wildcard,
		OperationClass: operation, Mode: mode,
		ShardConcurrency: 0, RequestConcurrency: requestConcurrency,
		RequestsPerMinute: rpm, RequestsPerSecond: rps,
		CUPerMinute: int64(rpm) * cuPerCall,
		PageSize:    pageSize, BatchSize: batchSize, RequestWeight: 1,
		RequestTimeoutSeconds: timeoutSeconds, HardCeiling: hardCeiling,
		Revision: 1, VerificationSource: "initial",
	}
}

// lookupDefaultProfile 先精确匹配 dataset，再退到 dataset 通配行。
func lookupDefaultProfile(providerKey, chain, dataset, operation, mode string) (Profile, bool) {
	var wildcardMatch *Profile
	for i := range defaultProfiles {
		profile := &defaultProfiles[i]
		if profile.ProviderKey != providerKey || profile.Chain != chain ||
			profile.OperationClass != operation || profile.Mode != mode {
			continue
		}
		if profile.Dataset == dataset {
			return *profile, true
		}
		if profile.Dataset == wildcard && wildcardMatch == nil {
			wildcardMatch = profile
		}
	}
	if wildcardMatch != nil {
		return *wildcardMatch, true
	}
	return Profile{}, false
}

// normalizeMode 统一小写并去空白；空串按 FAST（dRPC 初始默认档）。
func normalizeMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		return ModeFast
	}
	return mode
}
