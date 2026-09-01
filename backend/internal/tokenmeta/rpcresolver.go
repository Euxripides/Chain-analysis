package tokenmeta

import (
	"context"
	"fmt"
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
)

// ContractResolver 通过统一 RPC 基础设施解析 Token Metadata（V1.5 §72/§74/§175/§234）。
//
// V1.4 曾直接用 Ankr 客户端拼 eth_call；V1.5 起必须走：
//
//	TokenMetadataRPCResolver → ContractReadService → RPCGateway → Router → Provider
//
// 由此自动获得限流、重试、缓存、Singleflight、预算与统一用量记账，
// 并且换 Provider 不需要改动 Token Metadata 业务代码（§247-18/§247-29）。
type ContractResolver struct {
	contract *chainread.ContractService
}

func NewContractResolver(contract *chainread.ContractService) *ContractResolver {
	return &ContractResolver{contract: contract}
}

// ResolveTokenMetadata 单合约解析。
// 三个只读调用被合并成一次 JSON-RPC Batch（§41/§243），且彼此独立：
// name() revert 不得丢弃成功的 symbol()/decimals()。
func (r *ContractResolver) ResolveTokenMetadata(ctx context.Context, chainSlug, contract string) (provider.TokenMetadataResult, error) {
	var out provider.TokenMetadataResult
	if r == nil || r.contract == nil {
		return out, fmt.Errorf("contract read service is not configured")
	}
	address, err := chain.ParseAddress(strings.TrimSpace(contract))
	if err != nil {
		return out, err
	}
	metadata, err := r.contract.TokenMetadata(ctx, chainSlug, address, chainread.Options{
		Purpose: rpc.PurposeTokenMetadata, Priority: rpc.PriorityBackground,
	})
	if err != nil {
		return out, err
	}
	out.Name, out.Symbol, out.Decimals = metadata.Name, metadata.Symbol, metadata.Decimals
	out.NameError, out.SymbolError, out.DecimalsError = metadata.NameError, metadata.SymbolError, metadata.DecimalsError
	if out.Name == nil && out.Symbol == nil && out.Decimals == nil {
		return out, fmt.Errorf("all token metadata calls failed: name=%s symbol=%s decimals=%s",
			out.NameError, out.SymbolError, out.DecimalsError)
	}
	return out, nil
}

// ResolveBatch 批量解析（100 个合约一次性走 Batch，而不是 300 个无控制并发 HTTP，§243）。
func (r *ContractResolver) ResolveBatch(ctx context.Context, chainSlug string, contracts []string) (map[string]provider.TokenMetadataResult, error) {
	out := map[string]provider.TokenMetadataResult{}
	if r == nil || r.contract == nil || len(contracts) == 0 {
		return out, nil
	}
	addresses := make([]chain.Address, 0, len(contracts))
	for _, contract := range contracts {
		address, err := chain.ParseAddress(strings.TrimSpace(contract))
		if err != nil {
			continue
		}
		addresses = append(addresses, address)
	}
	results, err := r.contract.TokenMetadataBatch(ctx, chainSlug, addresses, chainread.Options{
		Purpose: rpc.PurposeTokenMetadata, Priority: rpc.PriorityBackground,
	})
	if err != nil {
		return out, err
	}
	for _, item := range results {
		out[string(item.Contract)] = provider.TokenMetadataResult{
			Name: item.Name, Symbol: item.Symbol, Decimals: item.Decimals,
			NameError: item.NameError, SymbolError: item.SymbolError, DecimalsError: item.DecimalsError,
		}
	}
	return out, nil
}

var _ RPCResolver = (*ContractResolver)(nil)
