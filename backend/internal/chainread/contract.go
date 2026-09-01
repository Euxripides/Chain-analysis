package chainread

import (
	"context"
	"fmt"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
)

// ContractService 合约只读访问（§72-§74/§157）。
//
// Token Metadata、LP/Proxy 探针、余额查询都必须经过这里，
// 不允许任何模块自建 HTTP RPC 客户端（§234/§247-18/§247-19）。
type ContractService struct {
	reader *Service
}

func NewContractService(reader *Service) *ContractService { return &ContractService{reader: reader} }

// Call 按签名调用只读方法（§73 CallMethod）。
func (c *ContractService) Call(ctx context.Context, chainSlug string, contract chain.Address,
	signature string, ref chain.BlockRef, opt Options, args ...any) ([]byte, error) {
	data, err := EncodeCall(signature, args...)
	if err != nil {
		return nil, err
	}
	if ref.IsZero() {
		ref = chain.Latest()
	}
	return c.reader.CallContract(ctx, ContractCall{Chain: chainSlug, To: contract, Data: data, BlockRef: ref}, opt)
}

// CallBatched 与 Call 相同，但允许被自动聚合成 Batch（后台补全用，§44）。
func (c *ContractService) CallBatched(ctx context.Context, chainSlug string, contract chain.Address,
	signature string, ref chain.BlockRef, opt Options, args ...any) ([]byte, error) {
	data, err := EncodeCall(signature, args...)
	if err != nil {
		return nil, err
	}
	if ref.IsZero() {
		ref = chain.Latest()
	}
	return c.reader.CallContractBatched(ctx, ContractCall{Chain: chainSlug, To: contract, Data: data, BlockRef: ref}, opt)
}

// 标准合约签名（§74/§80）。
const (
	SignatureName        = "name()"
	SignatureSymbol      = "symbol()"
	SignatureDecimals    = "decimals()"
	SignatureTotalSupply = "totalSupply()"
	SignatureToken0      = "token0()"
	SignatureToken1      = "token1()"
	SignatureFactory     = "factory()"
	SignatureReserves    = "getReserves()"
	SignatureImpl        = "implementation()"
	SignatureOwner       = "owner()"
)

// TokenMetadata 单个合约的 symbol/name/decimals。
// 三个调用彼此独立：name() revert 不得丢弃成功的 symbol()/decimals()（V1.4 语义）。
func (c *ContractService) TokenMetadata(ctx context.Context, chainSlug string, contract chain.Address,
	opt Options) (TokenMetadata, error) {
	results, err := c.TokenMetadataBatch(ctx, chainSlug, []chain.Address{contract}, opt)
	if err != nil {
		return TokenMetadata{Contract: contract}, err
	}
	if len(results) == 0 {
		return TokenMetadata{Contract: contract}, fmt.Errorf("no token metadata result")
	}
	return results[0], nil
}

// TokenMetadataBatch 批量读取（§243：100 个合约必须走 Batch/限流，不能 300 个无控制并发）。
func (c *ContractService) TokenMetadataBatch(ctx context.Context, chainSlug string, contracts []chain.Address,
	opt Options) ([]TokenMetadata, error) {
	if len(contracts) == 0 {
		return nil, nil
	}
	if opt.Purpose == "" {
		opt.Purpose = rpc.PurposeTokenMetadata
	}
	nameData, err := EncodeCall(SignatureName)
	if err != nil {
		return nil, err
	}
	symbolData, _ := EncodeCall(SignatureSymbol)
	decimalsData, _ := EncodeCall(SignatureDecimals)

	calls := make([]ContractCall, 0, len(contracts)*3)
	keys := make([]string, 0, len(contracts)*3)
	for _, contract := range contracts {
		calls = append(calls,
			ContractCall{Chain: chainSlug, To: contract, Data: nameData, BlockRef: chain.Latest()},
			ContractCall{Chain: chainSlug, To: contract, Data: symbolData, BlockRef: chain.Latest()},
			ContractCall{Chain: chainSlug, To: contract, Data: decimalsData, BlockRef: chain.Latest()})
		keys = append(keys, string(contract)+"|name", string(contract)+"|symbol", string(contract)+"|decimals")
	}
	results, err := c.reader.BatchCallContract(ctx, calls, keys, opt)
	if err != nil {
		return nil, err
	}

	out := make([]TokenMetadata, len(contracts))
	for i, contract := range contracts {
		item := TokenMetadata{Contract: contract}
		base := i * 3
		if base+2 >= len(results) {
			item.NameError = "missing batch result"
			out[i] = item
			continue
		}
		if result := results[base]; result.Err != nil {
			item.NameError = rpc.AsError(result.Err).Public()
		} else if value, decodeErr := DecodeString(result.Output); decodeErr != nil {
			item.NameError = decodeErr.Error()
		} else {
			item.Name = &value
		}
		if result := results[base+1]; result.Err != nil {
			item.SymbolError = rpc.AsError(result.Err).Public()
		} else if value, decodeErr := DecodeString(result.Output); decodeErr != nil {
			item.SymbolError = decodeErr.Error()
		} else {
			item.Symbol = &value
		}
		if result := results[base+2]; result.Err != nil {
			item.DecimalsError = rpc.AsError(result.Err).Public()
		} else if value, decodeErr := DecodeUint8(result.Output); decodeErr != nil {
			item.DecimalsError = decodeErr.Error()
		} else {
			item.Decimals = &value
		}
		out[i] = item
	}
	return out, nil
}
