package chainread

import (
	"context"
	"fmt"
	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
)

// AddressHasState batches three cheap account reads through the existing gateway.
// A positive result proves state; a zero result does not prove an unused address.
func (s *Service) AddressHasState(ctx context.Context, slug string, address chain.Address, opt Options) (bool, error) {
	ref := chain.Latest()
	methods := []string{"eth_getBalance", "eth_getTransactionCount", "eth_getCode"}
	reqs := make([]rpc.Request, 3)
	for i, m := range methods {
		class := rpc.ClassBalance
		if i == 2 {
			class = rpc.ClassCode
		}
		reqs[i] = opt.request(slug, m, []any{string(address), ref.String()}, class, ref, rpc.PurposeInvestigation)
	}
	responses, callErr := s.gateway.BatchCall(ctx, reqs)
	var firstErr error
	for i, response := range responses {
		if response.Error != nil {
			firstErr = response.Error
			continue
		}
		value, err := decodeHexString(response.Result)
		if err != nil {
			firstErr = err
			continue
		}
		if i == 2 {
			code, err := chain.HexToBytes(value)
			if err != nil {
				firstErr = err
			} else if len(code) > 0 {
				return true, nil
			}
		} else {
			n, err := chain.HexToBigInt(value)
			if err != nil {
				firstErr = err
			} else if n.Sign() > 0 {
				return true, nil
			}
		}
	}
	if firstErr != nil {
		return false, firstErr
	}
	if callErr != nil {
		return false, callErr
	}
	if len(responses) != 3 {
		return false, fmt.Errorf("incomplete account state response")
	}
	return false, nil
}

func (s *Service) BlockByHash(ctx context.Context, slug string, hash chain.Hash, opt Options) (*Block, error) {
	response, err := s.call(ctx, opt.request(slug, "eth_getBlockByHash", []any{string(hash), false}, rpc.ClassBlock, chain.Latest(), rpc.PurposeTransactionVerify))
	if err != nil {
		return nil, err
	}
	if response.IsNull() {
		return nil, nil
	}
	return parseBlock(response.Result)
}
