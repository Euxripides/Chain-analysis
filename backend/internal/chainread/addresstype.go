package chainread

import (
	"context"

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
