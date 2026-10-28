package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

// Token contracts: the largest holders by balance with their share of the probed totalSupply and whether each
// holder has code. Project vaults, pools and collection wallets usually sit at the top, so this is how an agent
// starting from a token finds the rest of the system. Holder balances come from Ankr's index, which can lag the
// snapshot block; code checks are at the snapshot block.

const contractTopHolders = 25

type contractHolder struct{ Address, Raw string }

type contractHoldersFunc func(limit int) (holders []contractHolder, total int64, err error)

type contractCodeFunc func(address string) (size int, err error)

func (s *Server) contractHolderSources(ctx context.Context, q ca.Request, result *ca.Result) (contractHoldersFunc, contractCodeFunc) {
	holders := func(limit int) ([]contractHolder, int64, error) {
		if s.deps.Prov == nil {
			return nil, 0, fmt.Errorf("holder provider unavailable")
		}
		res, e := s.deps.Prov.Fetch(ctx, provider.FetchRequest{Chain: q.Chain, Dataset: "token_holders", Address: contractProbeTarget(result.Snapshot), PageSize: limit, Priority: "background"})
		if e != nil {
			return nil, 0, e
		}
		out := []contractHolder{}
		for _, raw := range res.Rows {
			var row struct {
				HolderAddress     string `json:"holderAddress"`
				BalanceRawInteger string `json:"balanceRawInteger"`
			}
			if json.Unmarshal(raw, &row) == nil && contractHexAddress.MatchString(row.HolderAddress) && len(row.HolderAddress) == 42 {
				out = append(out, contractHolder{strings.ToLower(row.HolderAddress), row.BalanceRawInteger})
			}
		}
		return out, res.Total, nil
	}
	code := func(address string) (int, error) {
		b, e := s.deps.ChainRead.Code(ctx, q.Chain, chain.Address(address), chain.AtBlock(result.Snapshot.BlockNumber), contractOptions(ctx, q))
		return len(b), e
	}
	return holders, code
}

// contractProbeUint returns the decoded uint of a no-argument getter observed by the caller probe.
func contractProbeUint(result *ca.Result, getter string) (*big.Int, bool) {
	for _, raw := range result.Sections["caller_access_probes"] {
		r := rowMap(raw)
		if r["signature"] != getter && r["signature_candidate"] != getter {
			continue
		}
		if v, ok := new(big.Int).SetString(fmt.Sprint(r["returned_uint"]), 10); ok {
			return v, true
		}
	}
	return nil, false
}

// contractApplyTokenHolders adds the token_holders section for contracts that expose totalSupply().
func contractApplyTokenHolders(result *ca.Result, holders contractHoldersFunc, code contractCodeFunc) {
	supply, ok := contractProbeUint(result, "totalSupply()")
	if !ok || supply.Sign() <= 0 {
		return
	}
	coverage := map[string]any{"dimension": "token_holders", "complete": false, "target_address": contractProbeTarget(result.Snapshot)}
	list, total, err := holders(contractTopHolders)
	if err != nil {
		coverage["reason"] = "holder_index_unavailable"
		result.Sections["coverage"] = append(result.Sections["coverage"], ca.Row(coverage))
		return
	}
	top := []map[string]any{}
	for i, h := range list {
		balance, ok := new(big.Int).SetString(h.Raw, 10)
		if !ok {
			continue
		}
		ppm := new(big.Int).Div(new(big.Int).Mul(balance, big.NewInt(1_000_000)), supply)
		entry := map[string]any{"rank": i + 1, "address": h.Address, "balance_raw": h.Raw, "share_percent": fmt.Sprintf("%d.%04d", ppm.Int64()/10_000, ppm.Int64()%10_000)}
		if size, e := code(h.Address); e == nil {
			entry["is_contract"], entry["code_size"] = size > 0, size
		}
		top = append(top, entry)
	}
	row := map[string]any{"token_address": contractProbeTarget(result.Snapshot), "total_supply_raw": supply.String(), "holders_count": total,
		"top_holders": top, "block_number": result.Snapshot.BlockNumber,
		"basis": "Ankr token holder index (may lag the snapshot block), shares of totalSupply() at the snapshot block, eth_getCode at the snapshot block"}
	if decimals, ok := contractProbeUint(result, "decimals()"); ok {
		row["decimals"] = decimals.String()
	}
	result.Sections["token_holders"] = append(result.Sections["token_holders"], ca.Row(row))
	coverage["complete"], coverage["holders_count"], coverage["listed"] = total > 0 && int64(len(top)) >= total, total, len(top)
	result.Sections["coverage"] = append(result.Sections["coverage"], ca.Row(coverage))
}
