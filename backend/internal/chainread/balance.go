package chainread

import (
	"context"
	"math/big"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
)

// BalanceService 原生与代币余额（§84-§87）。
//
// latest 短 TTL；固定区块需要 Archive 能力并可长期缓存（§53/§87/§88）。
type BalanceService struct {
	reader *Service
}

func NewBalanceService(reader *Service) *BalanceService { return &BalanceService{reader: reader} }

// Native 原生余额（§85）。
func (b *BalanceService) Native(ctx context.Context, chainSlug string, address chain.Address,
	ref chain.BlockRef, opt Options) (*big.Int, error) {
	if opt.Purpose == "" {
		opt.Purpose = rpc.PurposeBalance
	}
	if ref.IsZero() {
		ref = chain.Latest()
	}
	return b.reader.Balance(ctx, chainSlug, address, ref, opt)
}

// balanceOfSignature ERC20 余额查询。
const balanceOfSignature = "balanceOf(address)"

// Token 单个代币余额（§86：经 ContractReadService，不自建 HTTP 客户端）。
func (b *BalanceService) Token(ctx context.Context, chainSlug string, token, holder chain.Address,
	ref chain.BlockRef, opt Options) (*big.Int, error) {
	if opt.Purpose == "" {
		opt.Purpose = rpc.PurposeBalance
	}
	if ref.IsZero() {
		ref = chain.Latest()
	}
	data, err := EncodeCall(balanceOfSignature, holder)
	if err != nil {
		return nil, err
	}
	output, err := b.reader.CallContract(ctx, ContractCall{
		Chain: chainSlug, To: token, Data: data, BlockRef: ref}, opt)
	if err != nil {
		return nil, err
	}
	return DecodeUint(output)
}

// Tokens 批量代币余额（一次 JSON-RPC Batch，§157/§243）。
func (b *BalanceService) Tokens(ctx context.Context, chainSlug string, holder chain.Address,
	tokens []chain.Address, ref chain.BlockRef, opt Options) ([]TokenBalance, error) {
	if len(tokens) == 0 {
		return nil, nil
	}
	if opt.Purpose == "" {
		opt.Purpose = rpc.PurposeBalance
	}
	if ref.IsZero() {
		ref = chain.Latest()
	}
	data, err := EncodeCall(balanceOfSignature, holder)
	if err != nil {
		return nil, err
	}
	calls := make([]ContractCall, len(tokens))
	keys := make([]string, len(tokens))
	for i, token := range tokens {
		calls[i] = ContractCall{Chain: chainSlug, To: token, Data: data, BlockRef: ref}
		keys[i] = string(token)
	}
	results, err := b.reader.BatchCallContract(ctx, calls, keys, opt)
	if err != nil {
		return nil, err
	}
	out := make([]TokenBalance, len(tokens))
	for i, result := range results {
		item := TokenBalance{Token: tokens[i]}
		if result.Err != nil {
			item.Err = rpc.AsError(result.Err).Public()
			out[i] = item
			continue
		}
		value, decodeErr := DecodeUint(result.Output)
		if decodeErr != nil {
			item.Err = "balance is not a uint256"
			out[i] = item
			continue
		}
		item.Balance = value
		item.Raw = value.String()
		out[i] = item
	}
	return out, nil
}
