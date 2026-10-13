package api

import (
	"context"
	"encoding/hex"
	"math/big"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
	"github.com/blockchain-platform/blockchain-platform/internal/crosschain"
	"github.com/shopspring/decimal"
)

func (b *bridgeReadSession) indexed(leg crosschain.Leg) ([]crosschain.Movement, bool) {
	out := []crosschain.Movement{}
	truncated := false
	if b.s.query == nil {
		return out, false
	}
	datasets := []string{"token_transfers"}
	if bridgeNativeAsset(leg) {
		datasets = []string{"transactions", "internal_transactions"}
		if leg.Chain == "solana" {
			datasets = []string{"internal_transactions"}
		}
	}
	for _, d := range datasets {
		ctx, cancel := context.WithTimeout(b.ctx, 3*time.Second)
		rows, cut, err := b.s.query.CrossChainAnchor(ctx, crosschain.Anchor{Chain: leg.Chain, TxHash: leg.TxHash, Dataset: d})
		cancel()
		if err == nil {
			out = append(out, rows...)
			truncated = truncated || cut
		}
	}
	return out, truncated
}

func legMovements(leg crosschain.Leg, side int, rows []crosschain.Movement) []crosschain.Movement {
	out := []crosschain.Movement{}
	seen := map[string]bool{}
	for _, m := range rows {
		if m.Chain != leg.Chain || !crosschain.Same(leg.Chain, m.TxHash, leg.TxHash) || m.Status != "success" || m.Timestamp.Unix() <= 0 {
			continue
		}
		if crosschain.Same(leg.Chain, m.From, m.To) {
			continue
		}
		address := m.From
		if side != 0 {
			address = m.To
		}
		if leg.Address != "" && !crosschain.Same(leg.Chain, address, leg.Address) {
			continue
		}
		if leg.TokenContract != "" {
			if bridgeNativeAsset(leg) {
				if m.TokenContract != "native" && m.Dataset == "token_transfers" {
					continue
				}
			} else if !crosschain.Same(leg.Chain, m.TokenContract, leg.TokenContract) {
				continue
			}
		}
		if n, ok := new(big.Int).SetString(m.AmountRaw, 10); !ok || n.Sign() <= 0 {
			continue
		}
		if !seen[m.Identity()] {
			seen[m.Identity()] = true
			out = append(out, m)
		}
	}
	return out
}

func reportedAmountMatches(leg crosschain.Leg, m crosschain.Movement) bool {
	if m.Decimals < 0 || leg.TokenContract == "" && !bridgeNativeAsset(leg) {
		return false
	}
	if leg.AmountsScope == "quoted_expected" || leg.AmountsScope == "message_only" || strings.Contains(leg.AmountsScope, "not_per_transaction") {
		return false
	}
	if leg.NormalizedAmountRaw != "" {
		observed, ok := new(big.Int).SetString(m.AmountRaw, 10)
		expected, ok2 := new(big.Int).SetString(leg.NormalizedAmountRaw, 10)
		if !ok || !ok2 || m.Decimals < 0 {
			return false
		}
		if m.Decimals > 8 {
			observed.Quo(observed, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(m.Decimals-8)), nil))
		}
		return observed.Cmp(expected) == 0
	}
	if leg.AmountRaw != "" {
		if leg.Decimals != nil && m.Decimals >= 0 && *leg.Decimals != m.Decimals {
			return false
		}
		return leg.AmountRaw == m.AmountRaw
	}
	if leg.Amount != "" && m.Decimals >= 0 {
		n, err := decimal.NewFromString(leg.Amount)
		return err == nil && n.Equal(decimal.NewFromBigInt(func() *big.Int { x, _ := new(big.Int).SetString(m.AmountRaw, 10); return x }(), int32(-m.Decimals)))
	}
	return false
}

// Every part must share the same actual asset and address. Never sum unrelated
// assets, sender branches, duplicate event IDs or the traced root twice.
func matchingParts(leg crosschain.Leg, rows []crosschain.Movement) []crosschain.Movement {
	for _, m := range rows {
		if reportedAmountMatches(leg, m) {
			return []crosschain.Movement{m}
		}
	}
	if len(rows) > 24 {
		return nil
	}
	for i, a := range rows {
		for j := i + 1; j < len(rows); j++ {
			z := rows[j]
			if !sameFundingPart(a, z) {
				continue
			}
			sum := a
			sum.AmountRaw = addRaw(a.AmountRaw, z.AmountRaw)
			if reportedAmountMatches(leg, sum) {
				return []crosschain.Movement{a, z}
			}
			for k := j + 1; k < len(rows); k++ {
				c := rows[k]
				if !sameFundingPart(a, c) {
					continue
				}
				sum.AmountRaw = addRaw(addRaw(a.AmountRaw, z.AmountRaw), c.AmountRaw)
				if reportedAmountMatches(leg, sum) {
					return []crosschain.Movement{a, z, c}
				}
			}
		}
	}
	return nil
}
func addRaw(a, b string) string {
	x, _ := new(big.Int).SetString(a, 10)
	y, _ := new(big.Int).SetString(b, 10)
	return new(big.Int).Add(x, y).String()
}
func sameFundingPart(a, b crosschain.Movement) bool {
	return a.Decimals == b.Decimals && crosschain.Same(a.Chain, a.TokenContract, b.TokenContract) && crosschain.Same(a.Chain, a.From, b.From) && crosschain.Same(a.Chain, a.To, b.To) && !(a.Dataset == "transactions" && b.Dataset == "internal_transactions" && b.Index == 0) && !(b.Dataset == "transactions" && a.Dataset == "internal_transactions" && a.Index == 0)
}

func (b *bridgeReadSession) verify(t *crosschain.Transfer) {
	t.Verification = []crosschain.Verification{}
	t.OnChainVerified = false
	t.PairingVerified = false
	t.MessageOnly = false
	allAmounts, allRecords, count := true, true, 0
	for side, legs := range [][]crosschain.Leg{t.Source, t.Destination, t.Refund} {
		if len(legs) == 0 && side < 2 {
			allAmounts = false
			allRecords = false
		}
		for j, leg := range legs {
			count++
			v := crosschain.Verification{LegRole: []string{"source", "destination", "refund"}[side], Chain: leg.Chain, TxHash: leg.TxHash, Status: "not_available", Reason: "funding records unavailable", ObservedMovements: []crosschain.Movement{}}
			if bridgeNativeAsset(leg) && leg.TokenContract != "native" {
				leg.ReportedTokenContract, leg.TokenContract = leg.TokenContract, "native"
			}
			if count > 8 || b.ctx.Err() != nil {
				v.Status = "budget_exhausted"
				v.Reason = "verification record or time budget exhausted"
				allAmounts = false
				allRecords = false
				t.Verification = append(t.Verification, v)
				continue
			}
			if _, err := crosschain.Chain(leg.Chain); err != nil {
				v.Status = "unsupported_chain"
				allAmounts = false
				allRecords = false
				t.Verification = append(t.Verification, v)
				continue
			}
			if _, err := crosschain.Hash(leg.Chain, leg.TxHash); err != nil {
				v.Status = "invalid_identifier"
				allAmounts = false
				allRecords = false
				t.Verification = append(t.Verification, v)
				continue
			}
			rows, cut := b.indexed(leg)
			v.Source = "chain_index"
			v.Truncated = cut
			eligible := legMovements(leg, side, rows)
			parts := matchingParts(leg, eligible)
			var record *bridgeRecord
			if len(parts) == 0 || leg.AmountsScope == "message_only" || leg.NormalizedAmountRaw != "" {
				var err error
				record, err = b.record(leg.Chain, leg.TxHash, bridgeNativeAsset(leg))
				if err == nil {
					rows = record.Movements
					v.Source = record.Source
					v.Truncated = record.Truncated
					v.ReadGaps = record.Gaps
					if leg.AmountsScope == "message_only" {
						leg = deriveOFTLeg(leg, t.OrderID, side, record)
					}
					if leg.NormalizedAmountRaw != "" && leg.TokenContract == "" {
						leg = b.deriveWormholeAsset(leg, t.OrderID, side, record)
					}
					eligible = legMovements(leg, side, rows)
					parts = matchingParts(leg, eligible)
					for _, h := range record.Hints {
						if h.Identifier == t.OrderID || h.Bridge == "cctp" && h.SourceDomain+"/"+crosschain.CanonicalNonce(h.Identifier) == t.OrderID {
							v.IdentifierMatched = true
						}
					}
				} else {
					v.Reason = err.Error()
				}
			}
			if record != nil && len(parts) == 0 {
				leg = deriveWormholeFundingParty(leg, t.OrderID, side, record)
				leg = b.reconcileWrappedNative(leg, t.OrderID, side, record)
				eligible = legMovements(leg, side, record.Movements)
				parts = matchingParts(leg, eligible)
				v.ReadGaps = record.Gaps
			}
			if record != nil && leg.Chain == "solana" {
				if account, ok := record.TokenAccounts[leg.Address]; ok && account.Owner != "" && account.Mint == leg.TokenContract {
					leg.ReportedAddress, leg.Address, leg.AddressScope = leg.Address, account.Owner, "rpc_token_account_owner"
					eligible = legMovements(leg, side, record.Movements)
					parts = matchingParts(leg, eligible)
				}
			}
			if leg.Amount == "" && leg.AmountRaw != "" && len(eligible) > 0 && eligible[0].Decimals >= 0 {
				if raw, ok := new(big.Int).SetString(leg.AmountRaw, 10); ok {
					d := eligible[0].Decimals
					leg.Decimals = &d
					leg.Amount = decimal.NewFromBigInt(raw, int32(-d)).String()
				}
			}
			if side == 0 {
				t.Source[j] = leg
			} else if side == 1 {
				t.Destination[j] = leg
			} else {
				t.Refund[j] = leg
			}
			if leg.AmountsScope == "message_only" {
				t.MessageOnly = true
				v.Status = "message_only"
				v.Reason = "message pairing has no decoded application asset and beneficiary"
				allAmounts = false
				allRecords = false
			} else if len(parts) > 0 {
				v.Status = "verified"
				v.AmountsMatched = true
				v.Reason = "successful chain events match the reported asset, raw amount and available address"
				v.ObservedMovements = parts
			} else if leg.TokenContract == "" && !bridgeNativeAsset(leg) {
				v.Status = "asset_unavailable"
				v.Reason = "protocol did not provide an asset contract and it was not derived from a unique application event"
				allAmounts = false
				allRecords = false
				v.ObservedMovements = eligible
			} else if len(eligible) > 0 {
				v.Status = "amount_not_matched"
				v.Reason = "actual movements were read but the reported amount was not reconciled"
				allAmounts = false
				v.ObservedMovements = eligible
			} else {
				if record != nil && record.Status == "failed" {
					v.Status = "failed_transaction"
					v.Reason = "chain receipt indicates transaction failure"
				} else if record != nil {
					v.ObservedMovements = record.Movements
					if len(record.Gaps) > 0 {
						v.Reason = "funding events unavailable: " + strings.Join(record.Gaps, ", ")
					} else {
						v.Reason = "actual transaction read; no funding event matched the reported asset and beneficiary"
					}
				}
				allAmounts = false
				allRecords = false
			}
			if len(v.ObservedMovements) > 24 {
				v.ObservedMovements = v.ObservedMovements[:24]
				v.Truncated = true
			}
			t.Verification = append(t.Verification, v)
		}
	}
	t.PairingVerified = allRecords && count >= 2 && t.Status == "completed" && !t.MessageOnly
	t.OnChainVerified = allAmounts && count >= 2 && t.Status == "completed" && !t.MessageOnly
}

func (b *bridgeReadSession) deriveWormholeAsset(leg crosschain.Leg, id string, side int, record *bridgeRecord) crosschain.Leg {
	if record == nil || record.Status != "success" || leg.Chain == "solana" || b.s.deps.ChainRead == nil {
		return leg
	}
	bridge := leg.BridgeContract
	matched := false
	for _, hint := range record.Hints {
		if hint.Bridge == "wormhole" && hint.Identifier == id {
			matched = true
			if side == 1 {
				bridge = hint.Contract
			}
		}
	}
	if !matched || bridge == "" {
		return leg
	}
	origin, ok := new(big.Int).SetString(leg.OriginChainID, 10)
	token, err := hex.DecodeString(strings.TrimPrefix(leg.OriginToken, "0x"))
	if !ok || !origin.IsUint64() || origin.Uint64() > 65535 || err != nil || len(token) != 32 || b.budget() != nil {
		return leg
	}
	contract, err := chain.ParseAddress(bridge)
	if err != nil {
		return leg
	}
	selector, _ := hex.DecodeString(crosschain.EventTopic("wrappedAsset(uint16,bytes32)")[2:10])
	data := append(selector, origin.FillBytes(make([]byte, 32))...)
	data = append(data, token...)
	raw, err := b.s.deps.ChainRead.CallContract(b.ctx, chainread.ContractCall{Chain: bridgeRPCChain(leg.Chain), To: contract, Data: data, BlockRef: chain.BlockRef{Number: &record.BlockNumber}}, b.options())
	if err != nil || len(raw) != 32 || new(big.Int).SetBytes(raw).Sign() == 0 {
		return leg
	}
	leg.TokenContract = "0x" + hex.EncodeToString(raw[12:])
	leg.BridgeContract = bridge
	return leg
}

func deriveOFTLeg(leg crosschain.Leg, guid string, side int, record *bridgeRecord) crosschain.Leg {
	if record == nil || record.Status != "success" || len(guid) != 66 {
		return leg
	}
	for _, l := range record.Logs {
		if len(l.Topics) < 3 || !strings.EqualFold(l.Topics[1], guid) || len(l.Topics[2]) != 66 {
			continue
		}
		topic := crosschain.EventTopic("OFTSent(bytes32,uint32,address,uint256,uint256)")
		if side == 1 {
			topic = crosschain.EventTopic("OFTReceived(bytes32,uint32,address,uint256)")
		}
		if !strings.EqualFold(l.Topics[0], topic) {
			continue
		}
		data, err := hex.DecodeString(strings.TrimPrefix(l.Data, "0x"))
		if err != nil || len(data) < 64 {
			continue
		}
		address := "0x" + l.Topics[2][26:]
		raw := new(big.Int).SetBytes(data[32:64]).String()
		found := []crosschain.Movement{}
		for _, m := range record.Movements {
			wallet, app := m.From, m.To
			if side == 1 {
				wallet, app = m.To, m.From
			}
			if !crosschain.Same(leg.Chain, wallet, address) || m.AmountRaw != raw || m.Decimals < 0 {
				continue
			}
			if !crosschain.Same(leg.Chain, m.TokenContract, l.Address.String()) && !crosschain.Same(leg.Chain, app, l.Address.String()) {
				continue
			}
			found = append(found, m)
		}
		if len(found) != 1 {
			continue
		}
		m := found[0]
		leg.Address, leg.TokenContract, leg.AmountRaw, leg.Amount, leg.Timestamp = address, m.TokenContract, m.AmountRaw, m.Amount, m.Timestamp.Format(time.RFC3339)
		d := m.Decimals
		leg.Decimals = &d
		leg.AmountsScope = "layerzero_guid_application_event"
		return leg
	}
	return leg
}
