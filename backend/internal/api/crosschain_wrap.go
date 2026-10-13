package api

import (
	"encoding/hex"
	"math/big"
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
	"github.com/blockchain-platform/blockchain-platform/internal/crosschain"
)

// Reconcile only the exact log, historical pool getter and successful native
// funding path. Split disbursements remain explicit; no fee recipient is guessed.
func (b *bridgeReadSession) reconcileWrappedNative(leg crosschain.Leg, id string, side int, record *bridgeRecord) crosschain.Leg {
	if side > 1 || record == nil || record.Status != "success" || leg.Chain == "solana" || leg.Chain == "tron" || leg.TokenContract == "" || leg.AmountRaw == "" || bridgeNativeAsset(leg) || b.s.deps.ChainRead == nil {
		return leg
	}
	for _, log := range record.Logs {
		owner, raw, valid := crosschain.DecodeNativeConversion(log.Topics, log.Data, side == 0)
		if !valid || raw != leg.AmountRaw || !crosschain.Same(leg.Chain, log.Address.String(), leg.TokenContract) {
			continue
		}
		pool := ""
		ambiguous := false
		for _, hint := range record.Hints {
			if hint.Bridge != "across" || hint.Identifier != id {
				continue
			}
			if side == 0 {
				if hint.Kind != "sent" || !crosschain.Same(leg.Chain, hint.Contract, owner) || !crosschain.Same(leg.Chain, hint.Recipient, leg.Address) {
					continue
				}
			} else {
				if hint.Kind != "received" {
					continue
				}
				if !crosschain.Same(leg.Chain, hint.Contract, owner) {
					linked := false
					for _, m := range record.Movements {
						if m.Dataset == "token_transfers" && m.AmountRaw == leg.AmountRaw && crosschain.Same(leg.Chain, m.TokenContract, leg.TokenContract) && crosschain.Same(leg.Chain, m.From, hint.Contract) && crosschain.Same(leg.Chain, m.To, owner) {
							linked = true
						}
					}
					if !linked {
						continue
					}
				}
			}
			if pool != "" && !crosschain.Same(leg.Chain, pool, hint.Contract) {
				ambiguous = true
			}
			pool = hint.Contract
		}
		if pool == "" || ambiguous {
			continue
		}
		address, e := chain.ParseAddress(pool)
		if e != nil || b.budget() != nil {
			continue
		}
		selector, _ := hex.DecodeString(crosschain.EventTopic("wrappedNativeToken()")[2:10])
		token, e := b.s.deps.ChainRead.CallContract(b.ctx, chainread.ContractCall{Chain: bridgeRPCChain(leg.Chain), To: address, Data: selector, BlockRef: chain.BlockRef{Number: &record.BlockNumber}}, b.options())
		if e != nil || len(token) != 32 || strings.Trim(hex.EncodeToString(token[:12]), "0") != "" || !crosschain.Same(leg.Chain, "0x"+hex.EncodeToString(token[12:]), leg.TokenContract) {
			continue
		}
		if !record.NativeRead {
			b.evmNative(record)
		}
		path, other := wrappedNativeFunding(leg, side, owner, record.Movements)
		if len(path) == 0 {
			continue
		}
		leg.ReportedTokenContract, leg.TokenContract, leg.AmountsScope = leg.TokenContract, "native", "chain_reconciled_wrapped_native"
		leg.BridgeContract = pool
		leg.Reconciliation = &crosschain.FundingReconciliation{Kind: "wrapped_native", FundingPath: path, OtherDisbursements: other}
		if side == 0 && path[0].AmountRaw != leg.AmountRaw {
			leg.ReportedAmountRaw, leg.AmountRaw, leg.Amount = leg.AmountRaw, path[0].AmountRaw, path[0].Amount
			leg.AmountsScope, leg.Reconciliation.Kind = "chain_reconciled_wrapped_native_split", "wrapped_native_split"
		}
		if side == 1 {
			beneficiary := path[len(path)-1]
			if beneficiary.AmountRaw != leg.AmountRaw {
				leg.ReportedAmountRaw, leg.AmountRaw, leg.Amount = leg.AmountRaw, beneficiary.AmountRaw, beneficiary.Amount
				leg.AmountsScope = "chain_reconciled_wrapped_native_split"
				leg.Reconciliation.Kind = "wrapped_native_split"
			}
		}
		return leg
	}
	return leg
}

func wrappedNativeFunding(leg crosschain.Leg, side int, owner string, rows []crosschain.Movement) ([]crosschain.Movement, []crosschain.Movement) {
	native := []crosschain.Movement{}
	for _, m := range rows {
		if m.Dataset != "token_transfers" && m.Status == "success" && m.TokenContract == "native" && !crosschain.Same(leg.Chain, m.From, m.To) {
			native = append(native, m)
		}
	}
	if side == 0 {
		return crosschain.SourceNativeFunding(crosschain.DecodedBridgeEvent{Role: "source", Party: leg.Address, Counterparty: owner, AmountRaw: leg.AmountRaw}, native)
	}
	withdrawals := []crosschain.Movement{}
	outputs := []crosschain.Movement{}
	beneficiaries := []crosschain.Movement{}
	other := []crosschain.Movement{}
	sum := new(big.Int)
	for _, m := range native {
		if m.AmountRaw == leg.AmountRaw && crosschain.Same(leg.Chain, m.From, leg.TokenContract) && crosschain.Same(leg.Chain, m.To, owner) {
			withdrawals = append(withdrawals, m)
		}
		if !crosschain.Same(leg.Chain, m.From, owner) {
			continue
		}
		n, ok := new(big.Int).SetString(m.AmountRaw, 10)
		if !ok || n.Sign() <= 0 {
			return nil, nil
		}
		sum.Add(sum, n)
		outputs = append(outputs, m)
		if crosschain.Same(leg.Chain, m.To, leg.Address) {
			beneficiaries = append(beneficiaries, m)
		} else {
			other = append(other, m)
		}
	}
	if len(withdrawals) != 1 || len(beneficiaries) != 1 || len(outputs) > 3 || sum.String() != leg.AmountRaw {
		return nil, nil
	}
	return []crosschain.Movement{withdrawals[0], beneficiaries[0]}, other
}

// The signer can swap before bridging. In that case the bridge input is the
// unique actual transfer into the reported emitter, tied to the Wormhole ID.
// Keep the protocol-reported signer separately from the observed funding party.
func deriveWormholeFundingParty(leg crosschain.Leg, id string, side int, record *bridgeRecord) crosschain.Leg {
	if side != 0 || record == nil || record.Status != "success" || leg.NormalizedAmountRaw == "" || leg.TokenContract == "" || leg.BridgeContract == "" {
		return leg
	}
	matched := false
	for _, h := range record.Hints {
		if h.Bridge == "wormhole" && h.Kind == "sent" && h.Identifier == id {
			matched = true
		}
	}
	if !matched {
		return leg
	}
	found := []crosschain.Movement{}
	for _, m := range record.Movements {
		if m.Dataset != "token_transfers" || m.Status != "success" || !crosschain.Same(leg.Chain, m.TokenContract, leg.TokenContract) || !crosschain.Same(leg.Chain, m.To, leg.BridgeContract) || crosschain.Same(leg.Chain, m.From, m.To) || strings.Trim(m.From, "0x") == "" || !reportedAmountMatches(leg, m) {
			continue
		}
		found = append(found, m)
	}
	if len(found) != 1 || crosschain.Same(leg.Chain, found[0].From, leg.Address) {
		return leg
	}
	leg.ReportedAddress, leg.Address, leg.AddressScope = leg.Address, found[0].From, "wormhole_emitter_funding"
	return leg
}
