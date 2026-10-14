package api

import (
	"context"
	"encoding/hex"
	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
	"github.com/blockchain-platform/blockchain-platform/internal/method"
	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
	"strings"
)

// Loupe responses enumerate candidates. Each selector is checked against the
// actual facetAddress selector at the same immutable state point.
func (s *Server) contractDiamond(ctx context.Context, q ca.Request, p chainread.StatePoint, address string) ([]map[string]any, error) {
	call := func(signature string, argument []byte) ([]byte, error) {
		selector, _ := chain.HexToBytes(method.SelectorOf(signature))
		return s.deps.ChainRead.CallContract(ctx, chainread.ContractCall{Chain: q.Chain, To: chain.Address(address), Data: append(selector, argument...), BlockRef: chain.AtBlock(p.Block)}, contractOptions(ctx, q))
	}
	raw, err := call("facetAddresses()", nil)
	if err != nil {
		return nil, err
	}
	addresses, err := ca.DecodePrimitiveArray(raw, "address", q.Budget.MaxRecords)
	if err != nil {
		return nil, err
	}
	facets := []map[string]any{}
	seen := map[string]string{}
	for index, facet := range addresses {
		if index >= q.Budget.MaxContracts {
			facets = append(facets, map[string]any{"address": facet, "resolution": "max_contracts", "selectors": []string{}})
			continue
		}
		word := make([]byte, 32)
		bytes, _ := chain.HexToBytes(facet)
		copy(word[12:], bytes)
		response, e := call("facetFunctionSelectors(address)", word)
		if e != nil {
			facets = append(facets, map[string]any{"address": facet, "resolution": "selectors_unavailable"})
			continue
		}
		selectors, e := ca.DecodePrimitiveArray(response, "bytes4", q.Budget.MaxRecords)
		if e != nil {
			facets = append(facets, map[string]any{"address": facet, "resolution": "selectors_invalid"})
			continue
		}
		routes := map[string]string{}
		matched := []string{}
		for _, selector := range selectors {
			if old := seen[selector]; old != "" && old != facet {
				routes[selector] = "loupe_conflict"
				continue
			}
			seen[selector] = facet
			argument := make([]byte, 32)
			code, _ := hex.DecodeString(strings.TrimPrefix(selector, "0x"))
			copy(argument, code)
			actual, e := call("facetAddress(bytes4)", argument)
			if e != nil {
				routes[selector] = "routing_unavailable"
				if rpc.AsError(e).Class == rpc.ClassBudgetExceeded {
					break
				}
				continue
			}
			if target := nonzeroAddress(actual); strings.EqualFold(target, facet) {
				routes[selector] = "matched_loupe_route"
				matched = append(matched, selector)
			} else {
				routes[selector] = "routing_mismatch"
			}
		}
		facets = append(facets, map[string]any{"address": facet, "selectors": selectors, "verified_selectors": matched, "routes": routes, "routing_complete": len(routes) == len(selectors), "resolution": "loupe_and_selector_probes", "block_hash": p.Hash})
	}
	return facets, nil
}
