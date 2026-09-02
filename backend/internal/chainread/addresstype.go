package chainread

import (
	"context"
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
)

// AddressTypeService EOA / Contract 判定（§75/§76）。
//
// code == 0x → EOA candidate；code != 0x → Contract。
// 注意历史 selfdestruct、precompile 与链自带系统合约的边界情况，
// 因此对外统一叫 "candidate"，后续再细分（§76）。
type AddressTypeService struct {
	reader *Service
}

// ResolveAddressTypes 为列表页提供批量兼容接口。每个 eth_getCode 仍然经
// RPCGateway，并由 Batcher 在同一业务上下文内聚合，避免业务层直连 Provider。
func (a *AddressTypeService) ResolveAddressTypes(ctx context.Context, chainSlug string, addresses []string) (map[string]bool, error) {
	result := make(map[string]bool, len(addresses))
	requests := make([]rpc.Request, 0, len(addresses))
	keys := make([]string, 0, len(addresses))
	seen := make(map[string]bool, len(addresses))
	for _, raw := range addresses {
		normalized := strings.ToLower(strings.TrimSpace(raw))
		address, err := chain.ParseAddress(normalized)
		if err != nil {
			return nil, err
		}
		if seen[normalized] {
			continue
		}
		seen[normalized] = true
		requests = append(requests, Options{Purpose: rpc.PurposeContractClassification, Priority: rpc.PriorityBackground}.
			request(chainSlug, "eth_getCode", []any{string(address), chain.Latest().String()},
				rpc.ClassCode, chain.Latest(), rpc.PurposeContractClassification))
		keys = append(keys, normalized)
	}
	responses, err := a.reader.gateway.BatchCall(ctx, requests)
	if err != nil && responses == nil {
		return nil, err
	}
	for i, response := range responses {
		if response.Error != nil {
			return nil, response.Error
		}
		value, err := decodeHexString(response.Result)
		if err != nil {
			return nil, err
		}
		code, err := chain.HexToBytes(value)
		if err != nil {
			return nil, err
		}
		result[keys[i]] = len(code) > 0
	}
	return result, nil
}

func NewAddressTypeService(reader *Service) *AddressTypeService {
	return &AddressTypeService{reader: reader}
}

// Classify 判定地址类型。重复请求由 Gateway 的缓存与 Singleflight 收敛（§227）。
func (a *AddressTypeService) Classify(ctx context.Context, chainSlug string, address chain.Address,
	ref chain.BlockRef, opt Options) (AddressInfo, error) {
	if opt.Purpose == "" {
		opt.Purpose = rpc.PurposeContractClassification
	}
	if ref.IsZero() {
		ref = chain.Latest()
	}
	code, err := a.reader.Code(ctx, chainSlug, address, ref, opt)
	if err != nil {
		return AddressInfo{Chain: chainSlug, Address: address, Type: AddressTypeUnknown,
			BlockRef: ref.String(), FetchedAt: nowUTC()}, err
	}
	info := AddressInfo{
		Chain: chainSlug, Address: address, CodeSize: len(code),
		BlockRef: ref.String(), FetchedAt: nowUTC(),
	}
	if len(code) == 0 {
		info.Type = AddressTypeEOA
	} else {
		info.Type = AddressTypeContract
	}
	return info, nil
}

// IsContract 便捷判定。
func (a *AddressTypeService) IsContract(ctx context.Context, chainSlug string, address chain.Address, opt Options) (bool, error) {
	info, err := a.Classify(ctx, chainSlug, address, chain.Latest(), opt)
	if err != nil {
		return false, err
	}
	return info.Type == AddressTypeContract, nil
}
