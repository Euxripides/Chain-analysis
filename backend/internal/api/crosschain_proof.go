package api

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
	"github.com/blockchain-platform/blockchain-platform/internal/crosschain"
)

type bridgeProtocolCall struct{ From, To, Input, Value string }

func protocolEvent(r *bridgeRecord, e crosschain.DecodedBridgeEvent) crosschain.ProtocolEventProof {
	return crosschain.ProtocolEventProof{Chain: r.Chain, TxHash: r.Hash, EventIndex: uint32(e.Log.LogIndex), Role: e.Role, Identifier: e.Identifier, PayloadSHA256: e.PayloadSHA256, RawTopics: append([]string(nil), e.Log.Topics...), RawData: e.Log.Data, Contract: crosschain.ContractProof{Address: e.Log.Address.String()}}
}
func matchingAcross(a, z *bridgeRecord) [][2]crosschain.DecodedBridgeEvent {
	matches := [][2]crosschain.DecodedBridgeEvent{}
	for _, log := range a.Logs {
		left, ok := crosschain.DecodeAcross(a.Chain, log)
		if !ok || left.Role != "source" || left.DestinationChain != z.Chain {
			continue
		}
		for _, log := range z.Logs {
			right, ok := crosschain.DecodeAcross(z.Chain, log)
			if ok && right.Role == "destination" && left.Identifier == right.Identifier && left.PayloadSHA256 == right.PayloadSHA256 {
				matches = append(matches, [2]crosschain.DecodedBridgeEvent{left, right})
			}
		}
	}
	return matches
}

func (b *bridgeReadSession) getter(r *bridgeRecord, app, sig string, extra []byte) (data []byte, err error) {
	key := strings.Join([]string{r.Chain, r.BlockHash, app, sig, hex.EncodeToString(extra)}, "|")
	if b.getters == nil {
		b.getters = map[string]bridgeGetterResult{}
	}
	if cached, ok := b.getters[key]; ok {
		return append([]byte(nil), cached.Data...), cached.Err
	}
	defer func() { b.getters[key] = bridgeGetterResult{append([]byte(nil), data...), err} }()
	address, err := chain.ParseAddress(app)
	if err != nil {
		return nil, err
	}
	if b.budget() != nil {
		return nil, fmt.Errorf("application_read_budget_exhausted")
	}
	selector, _ := hex.DecodeString(crosschain.EventTopic(sig)[2:10])
	return b.s.deps.ChainRead.CallContract(b.ctx, chainread.ContractCall{Chain: bridgeRPCChain(r.Chain), To: address, Data: append(selector, extra...), BlockRef: chain.BlockRef{Number: &r.BlockNumber}}, b.options())
}
func wordAddress(s string) []byte {
	raw, _ := hex.DecodeString(strings.TrimPrefix(s, "0x"))
	return append(make([]byte, 32-len(raw)), raw...)
}
func wordInt(s string) []byte {
	n, _ := new(big.Int).SetString(s, 10)
	if n == nil {
		return nil
	}
	return n.FillBytes(make([]byte, 32))
}
func (b *bridgeReadSession) wormholeAsset(r *bridgeRecord, e crosschain.DecodedBridgeEvent) (string, error) {
	if e.OriginChain == crosschain.WormholeChainID(r.Chain) {
		raw, err := hex.DecodeString(strings.TrimPrefix(e.OriginAsset, "0x"))
		if err != nil || len(raw) != 32 || new(big.Int).SetBytes(raw[:12]).Sign() != 0 {
			return "", fmt.Errorf("origin_asset_not_evm")
		}
		return "0x" + hex.EncodeToString(raw[12:]), nil
	}
	raw, _ := hex.DecodeString(strings.TrimPrefix(e.OriginAsset, "0x"))
	token, err := b.getter(r, crosschain.DeploymentAddress("wormhole", r.Chain, "token_bridge"), "wrappedAsset(uint16,bytes32)", append(wordInt(e.OriginChain), raw...))
	if err != nil || len(token) != 32 || new(big.Int).SetBytes(token[:12]).Sign() != 0 || new(big.Int).SetBytes(token).Sign() == 0 {
		return "", fmt.Errorf("historical_wrapped_asset_unavailable")
	}
	return "0x" + hex.EncodeToString(token[12:]), nil
}
func (b *bridgeReadSession) matchingWormhole(a, z *bridgeRecord) ([2]crosschain.DecodedBridgeEvent, int, []string) {
	var result [2]crosschain.DecodedBridgeEvent
	count := 0
	gaps := []string{}
	for _, log := range a.Logs {
		e, ok := crosschain.DecodeWormholePublished(a.Chain, log)
		if !ok || e.DestinationChain != z.Chain {
			continue
		}
		if e.Message[0] == 1 && new(big.Int).SetBytes(e.Message[101:133]).Sign() != 0 {
			gaps = append(gaps, "wormhole_relayer_fee_funds_decoder_unavailable")
			continue
		}
		var redeemed chainread.Log
		n := 0
		for _, log := range z.Logs {
			if crosschain.WormholeRedeemed(z.Chain, log, e) {
				redeemed = log
				n++
			}
		}
		if n != 1 {
			continue
		}
		if !z.NativeRead {
			b.evmNative(z)
		}
		calls := 0
		for _, call := range z.SuccessfulCalls {
			if crosschain.Same(z.Chain, call.To, redeemed.Address.String()) && crosschain.WormholeRedeemCall(call.Input, e) {
				calls++
			}
		}
		if calls != 1 {
			gaps = append(gaps, "redeem_payload_call_unavailable_or_ambiguous")
			continue
		}
		left, right := e, e
		right.Role, right.Log = "destination", redeemed
		left.Counterparty = crosschain.DeploymentAddress("wormhole", a.Chain, "token_bridge")
		right.Counterparty = crosschain.DeploymentAddress("wormhole", z.Chain, "token_bridge")
		var err error
		left.Asset, err = b.wormholeAsset(a, e)
		if err != nil {
			gaps = append(gaps, err.Error())
			continue
		}
		right.Asset, err = b.wormholeAsset(z, e)
		if err != nil {
			gaps = append(gaps, err.Error())
			continue
		}
		// The source payer is derived from the unique asset transfer to the bridge;
		// the API signer can be a router's caller instead of the funding party.
		for side, r := range []*bridgeRecord{a, z} {
			leg := &left
			if side == 1 {
				leg = &right
			}
			dec := b.evmDecimals(b.ctx, r.Chain, leg.Asset, r.BlockNumber)
			if dec < 0 {
				gaps = append(gaps, "wormhole_asset_precision_unavailable")
				break
			}
			scale := dec - 8
			if scale < 0 {
				scale = 0
			}
			leg.AmountRaw = new(big.Int).Mul(new(big.Int).SetBytes(e.Message[1:33]), new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)).String()
			if side == 0 {
				payers := []string{}
				for _, m := range r.Movements {
					if m.Status == "success" && crosschain.Same(r.Chain, m.TokenContract, leg.Asset) && m.AmountRaw == leg.AmountRaw && crosschain.Same(r.Chain, m.To, leg.Counterparty) {
						payers = append(payers, m.From)
					}
				}
				if len(payers) == 1 {
					leg.Party = payers[0]
				} else {
					leg.Party = ""
				}
			}
		}
		if left.Party == "" || left.AmountRaw == "" || right.AmountRaw == "" {
			continue
		}
		result = [2]crosschain.DecodedBridgeEvent{left, right}
		count++
	}
	return result, count, gaps
}

func exactBoundFunding(e crosschain.DecodedBridgeEvent, r *bridgeRecord) []crosschain.Movement {
	out := []crosschain.Movement{}
	for _, m := range r.Movements {
		party, other := m.From, m.To
		if e.Role == "destination" {
			party, other = m.To, m.From
		}
		if m.Status != "success" || m.Decimals < 0 || !crosschain.Same(r.Chain, m.TokenContract, e.Asset) || m.AmountRaw != e.AmountRaw || !crosschain.Same(r.Chain, party, e.Party) || crosschain.Same(r.Chain, m.From, m.To) {
			continue
		}
		// Mint/burn is permitted only for OFT where token()==application, or the
		// verified Wormhole wrapped token; ordinary ERC20 transfers use the actual
		// bridge/relayer counterparty derived from the protocol execution.
		zero := strings.TrimPrefix(strings.ToLower(other), "0x") == strings.Repeat("0", 40)
		allowZero := e.Protocol == "layerzero" && crosschain.Same(r.Chain, e.Asset, e.Application) || e.Protocol == "wormhole" && e.OriginChain != crosschain.WormholeChainID(r.Chain)
		if crosschain.Same(r.Chain, other, e.Counterparty) || zero && allowZero {
			out = append(out, m)
		}
	}
	return out
}

// Enumerate all short paths for one exact protocol amount and actual asset.
// Identical amounts on different branches are an ambiguity, not a tie-breaker.
func boundTokenFunding(e crosschain.DecodedBridgeEvent, r *bridgeRecord) [][]crosschain.Movement {
	start, end := e.Party, e.Counterparty
	if e.Role == "destination" {
		start, end = e.Counterparty, e.Party
	}
	paths := [][]crosschain.Movement{}
	var walk func(string, []crosschain.Movement, map[string]bool)
	walk = func(at string, path []crosschain.Movement, seen map[string]bool) {
		if len(path) >= 3 || len(paths) > 1 {
			return
		}
		for _, m := range r.Movements {
			if m.Dataset != "token_transfers" || m.Status != "success" || m.Decimals < 0 || m.AmountRaw != e.AmountRaw || !crosschain.Same(r.Chain, m.TokenContract, e.Asset) || !crosschain.Same(r.Chain, m.From, at) || crosschain.Same(r.Chain, m.From, m.To) || seen[m.Identity()] {
				continue
			}
			if len(path) > 0 && m.Index <= path[len(path)-1].Index {
				continue
			}
			next := append(append([]crosschain.Movement(nil), path...), m)
			if crosschain.Same(r.Chain, m.To, end) {
				paths = append(paths, next)
				continue
			}
			seen[m.Identity()] = true
			walk(m.To, next, seen)
			delete(seen, m.Identity())
		}
	}
	walk(start, nil, map[string]bool{})
	return paths
}

func (b *bridgeReadSession) fundingProof(e crosschain.DecodedBridgeEvent, r *bridgeRecord, side int) crosschain.FundsLegProof {
	l := crosschain.FundsLegProof{Role: e.Role, Chain: r.Chain, TxHash: r.Hash, Party: e.Party, Asset: e.Asset, AmountRaw: e.AmountRaw, BridgeAsset: e.Asset, BridgeAmountRaw: e.AmountRaw, Binding: e.Protocol + ":" + e.Identifier + ":executed_asset_party_amount", ReadGaps: []string{}, Movements: []crosschain.Movement{}, OtherDisbursements: []crosschain.Movement{}, ReceiptSHA256: r.ReceiptSHA256, Truncated: r.Truncated}
	matched := exactBoundFunding(e, r)
	if e.Protocol == "across" {
		paths := boundTokenFunding(e, r)
		if len(paths) == 1 {
			matched = paths[0]
			l.Movements = matched
			l.Decimals, l.Amount = matched[0].Decimals, matched[0].Amount
			l.MessageBound, l.UniquePath = true, true
		} else if len(paths) > 1 {
			l.ReadGaps = append(l.ReadGaps, "multiple_protocol_bound_funding_paths")
			return l
		}
		if len(paths) == 0 && e.Role == "source" {
			path, other := crosschain.SplitTokenFunding(e, r.Movements)
			if len(path) > 0 {
				matched, l.Movements, l.OtherDisbursements = path, path, other
				l.AmountRaw, l.Amount, l.Decimals = path[0].AmountRaw, path[0].Amount, path[0].Decimals
				l.MessageBound, l.UniquePath = true, true
			}
		}
	}
	if l.UniquePath {
		// The enumerated Across path already passed uniqueness.
	} else if len(matched) == 1 {
		l.Movements = matched
		l.Decimals, l.Amount = matched[0].Decimals, matched[0].Amount
		l.MessageBound, l.UniquePath = true, true
	} else if len(matched) > 1 {
		l.ReadGaps = append(l.ReadGaps, "multiple_protocol_bound_funding_events")
	} else if e.Protocol == "across" {
		dec := b.evmDecimals(b.ctx, r.Chain, e.Asset, r.BlockNumber)
		leg := crosschain.Leg{Chain: r.Chain, TxHash: r.Hash, Address: e.Party, TokenContract: e.Asset, AmountRaw: e.AmountRaw, Decimals: &dec}
		reconciled := b.reconcileWrappedNative(leg, e.DepositID, side, r)
		if reconciled.Reconciliation != nil {
			path := reconciled.Reconciliation.FundingPath
			l.Party, l.Asset, l.AmountRaw, l.Amount, l.Decimals = reconciled.Address, reconciled.TokenContract, reconciled.AmountRaw, reconciled.Amount, 18
			l.Movements = path
			for _, m := range path {
				party := m.From
				if side == 1 {
					party = m.To
				}
				if crosschain.Same(r.Chain, party, l.Party) && m.TokenContract == "native" && m.AmountRaw == l.AmountRaw {
					l.Amount = m.Amount
				}
			}
			l.OtherDisbursements = reconciled.Reconciliation.OtherDisbursements
			conversion := &crosschain.NativeConversionProof{Token: e.Asset, Pool: reconciled.BridgeContract, HistoricalGetter: true, NativeFunding: []crosschain.Movement{}, TokenFunding: []crosschain.Movement{}}
			count := 0
			for _, log := range r.Logs {
				owner, raw, valid := crosschain.DecodeNativeConversion(log.Topics, log.Data, side == 0)
				if valid && raw == e.AmountRaw && crosschain.Same(r.Chain, log.Address.String(), e.Asset) {
					conversion.Owner, conversion.EventIndex, conversion.RawTopics, conversion.RawData = owner, uint32(log.LogIndex), log.Topics, log.Data
					count++
				}
			}
			if count == 1 {
				if side == 0 {
					for _, m := range r.Movements {
						if m.TokenContract == "native" && m.Status == "success" && m.AmountRaw == e.AmountRaw && crosschain.Same(r.Chain, m.From, conversion.Owner) && crosschain.Same(r.Chain, m.To, e.Asset) {
							conversion.NativeFunding = append(conversion.NativeFunding, m)
						}
					}
				} else {
					copy := e
					copy.Party = conversion.Owner
					paths := boundTokenFunding(copy, r)
					if len(paths) == 1 {
						conversion.TokenFunding = paths[0]
					}
				}
				l.NativeConversion = conversion
			}
			l.MessageBound, l.UniquePath = true, true
		}
	}
	if !l.UniquePath {
		if e.Protocol == "across" && side == 0 {
			b.acrossCallerFunding(e, r, &l)
		}
	}
	if !l.UniquePath {
		l.ReadGaps = append(l.ReadGaps, "unique_protocol_bound_funding_path_unavailable")
	}
	// Only reads required by this proof are blockers. Native paths and delivery
	// payloads require complete traces; unrelated earlier candidate failures do not.
	if l.Asset == "native" || l.SourceCall != nil || e.Protocol == "layerzero" && r.NativeRead || side == 1 && (e.Protocol == "wormhole" || e.Protocol == "cctp") {
		l.TraceSHA256 = r.TraceSHA256
		l.ReadGaps = append(l.ReadGaps, r.Gaps...)
	}
	p, err := b.finalizedRecord(r)
	l.Block = p
	if err != nil {
		l.ReadGaps = append(l.ReadGaps, err.Error())
	}
	return l
}

func (b *bridgeReadSession) verifyFunds(t *crosschain.Transfer) {
	t.Proof = &crosschain.FundsProof{BuiltAt: time.Now().UTC(), Legs: []crosschain.FundsLegProof{}, ReadGaps: []string{}}
	defer func() {
		crosschain.ApplyFundsGate(t)
		if !t.FundsVerified {
			return
		}
		b.verifyDirectWallet(t)
		crosschain.ApplyFundsGate(t)
		for i, p := range t.Proof.Legs {
			leg := &t.Source[0]
			if i == 1 {
				leg = &t.Destination[0]
			}
			if leg.Address != "" && !crosschain.Same(p.Chain, leg.Address, p.Party) {
				leg.ReportedAddress = leg.Address
			}
			if leg.TokenContract != "" && !crosschain.Same(p.Chain, leg.TokenContract, p.Asset) {
				leg.ReportedTokenContract = leg.TokenContract
			}
			if leg.AmountRaw != "" && leg.AmountRaw != p.AmountRaw {
				leg.ReportedAmountRaw = leg.AmountRaw
			}
			leg.Reconciliation = nil
			leg.AddressScope = "protocol_bound_bridge_funding_party"
			leg.Address, leg.TokenContract, leg.AmountRaw, leg.Amount, leg.AmountsScope = p.Party, p.Asset, p.AmountRaw, p.Amount, "strict_chain_funding_evidence"
			leg.Timestamp = p.Movements[0].Timestamp.UTC().Format(time.RFC3339)
			dec := p.Decimals
			leg.Decimals = &dec
		}
	}()
	if len(t.Source) != 1 || len(t.Destination) != 1 {
		return
	}
	key := strings.Join([]string{t.Source[0].Chain, t.Source[0].TxHash, t.Destination[0].Chain, t.Destination[0].TxHash}, "|")
	if b.fundsProofs == nil {
		b.fundsProofs = map[string]*crosschain.FundsProof{}
	}
	if cached := b.fundsProofs[key]; cached != nil {
		raw, _ := json.Marshal(cached)
		_ = json.Unmarshal(raw, t.Proof)
		return
	}
	defer func() {
		if t.Proof.Protocol != nil {
			b.fundsProofs[key] = t.Proof
		}
	}()
	var records [2]*bridgeRecord
	for i, leg := range []crosschain.Leg{t.Source[0], t.Destination[0]} {
		r, err := b.record(leg.Chain, leg.TxHash, false)
		if err != nil {
			t.Proof.ReadGaps = append(t.Proof.ReadGaps, fmt.Sprintf("%s_exact_receipt_unavailable:%s", []string{"source", "destination"}[i], err.Error()))
			return
		}
		if r.Status != "success" {
			t.Proof.ReadGaps = append(t.Proof.ReadGaps, "transaction_not_successful")
			return
		}
		if r.Truncated {
			t.Proof.ReadGaps = append(t.Proof.ReadGaps, "protocol_receipt_truncated")
			return
		}
		for _, gap := range r.Gaps {
			if gap == "receipt_log_identity_conflict" {
				t.Proof.ReadGaps = append(t.Proof.ReadGaps, gap)
				return
			}
		}
		records[i] = r
	}
	a, z := records[0], records[1]
	matches := matchingAcross(a, z)
	var pair [2]crosschain.DecodedBridgeEvent
	n := len(matches)
	var cctpProof *crosschain.CCTPAssociationProof
	var layerZeroProof *crosschain.LayerZeroAssociationProof
	if n > 0 {
		pair = matches[0]
	}
	if n == 0 {
		var gaps []string
		pair, n, layerZeroProof, gaps = b.matchingLayerZero(a, z)
		t.Proof.ReadGaps = append(t.Proof.ReadGaps, gaps...)
	}
	if n == 0 {
		var gaps []string
		pair, n, gaps = b.matchingWormhole(a, z)
		t.Proof.ReadGaps = append(t.Proof.ReadGaps, gaps...)
	}
	if n == 0 {
		var gaps []string
		pair, n, cctpProof, gaps = b.matchingCCTP(a, z)
		t.Proof.ReadGaps = append(t.Proof.ReadGaps, gaps...)
	}
	if n == 0 {
		return
	}
	left, right := pair[0], pair[1]
	p := &crosschain.ProtocolProof{Protocol: left.Protocol, Version: left.Version, Identifier: left.Identifier, SourceChain: a.Chain, DestinationChain: z.Chain, PayloadSHA256: left.PayloadSHA256, Source: protocolEvent(a, left), Destination: protocolEvent(z, right), Unique: n == 1}
	t.Proof.Protocol = p
	p.CCTP = cctpProof
	p.LayerZero = layerZeroProof
	for i, e := range pair {
		r := records[i]
		verifier := e.Log.Address.String()
		if e.Protocol == "layerzero" {
			verifier = crosschain.DeploymentAddress(e.Protocol, r.Chain, "endpoint_v2")
		}
		contract, err := b.historicalDeployment(e.Protocol, r, e.Log.Address.String(), verifier)
		if i == 0 {
			p.Source.Contract = contract
		} else {
			p.Destination.Contract = contract
		}
		if e.Protocol == "layerzero" || e.Protocol == "cctp" || e.Protocol == "wormhole" && i == 0 {
			application := e.Application
			if e.Protocol == "wormhole" {
				application = crosschain.DeploymentAddress(e.Protocol, r.Chain, "token_bridge")
			}
			app, appErr := b.historicalDeployment(e.Protocol, r, application, application)
			if i == 0 {
				p.Source.ApplicationContract = &app
			} else {
				p.Destination.ApplicationContract = &app
			}
			if appErr != nil {
				t.Proof.ReadGaps = append(t.Proof.ReadGaps, appErr.Error())
			}
		}
		if err != nil {
			t.Proof.ReadGaps = append(t.Proof.ReadGaps, err.Error())
		}
		leg := b.fundingProof(e, r, i)
		if p.LayerZero != nil {
			layerZeroNativeFunding(e, r, p.LayerZero.Applications[i], &leg)
		}
		t.Proof.Legs = append(t.Proof.Legs, leg)
	}
}
