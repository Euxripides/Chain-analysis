package api

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/crosschain"
)

// Permission is set by the existing Actions authentication middleware. A read
// credential does not acquire analysis privileges by using the transfer route.
type bridgeAnalysisPermission struct{}

func (b *bridgeReadSession) automaticCandidates(req crosschain.QueryRequest, t *crosschain.Transfer) crosschain.CandidateSearch {
	if !actionScopeAllowedBridgeAnalysis(b.ctx) {
		return crosschain.CandidateSearch{Status: "analysis_scope_required", Reason: "automatic candidate matching requires an analysis-scoped credential"}
	}
	if t.OnChainVerified || t.FundsVerified {
		return crosschain.CandidateSearch{Status: "not_needed", Reason: "both funding legs already verified"}
	}
	if t.Status == "refunded" || t.Status == "failed" {
		return crosschain.CandidateSearch{Status: "not_needed", Reason: "transfer is refunded or failed"}
	}
	side, index := -1, -1
	for i, legs := range [][]crosschain.Leg{t.Source, t.Destination} {
		for j, l := range legs {
			if l.Chain == req.Chain && crosschain.Same(req.Chain, l.TxHash, req.TxHash) {
				if side != -1 {
					return crosschain.CandidateSearch{Status: "anchor_ambiguous", Reason: "input hash occurs in several transfer legs"}
				}
				side, index = i, j
			}
		}
	}
	if side < 0 {
		return crosschain.CandidateSearch{Status: "anchor_unavailable", Reason: "input hash is absent from the protocol transfer"}
	}
	peers := t.Destination
	var local crosschain.Leg
	if side == 0 {
		local = t.Source[index]
	} else {
		peers = t.Source
		local = t.Destination[index]
	}
	if len(peers) != 1 {
		return crosschain.CandidateSearch{Status: "peer_ambiguous", Reason: "automatic search requires exactly one opposite leg"}
	}
	peer := peers[0]
	basis := "protocol_reported_amount"
	// A known peer hash can reveal one actual movement even when the API quote
	// differs. This is an explicit search constraint, never an association proof.
	if peer.TxHash != "" && peer.Address != "" && peer.TokenContract != "" {
		if observed, e := b.record(peer.Chain, peer.TxHash, bridgeNativeAsset(peer)); e == nil && !observed.Truncated && observed.Status == "success" {
			movements := legMovements(peer, 1-side, observed.Movements)
			if len(movements) == 1 && movements[0].Decimals >= 0 && movements[0].Amount != "" {
				peer.Amount, peer.AmountRaw = movements[0].Amount, movements[0].AmountRaw
				basis = "unique_observed_peer_movement_search_constraint"
			}
		}
	}
	if peer.Chain == local.Chain || peer.Address == "" || peer.TokenContract == "" || peer.Amount == "" || strings.Contains(peer.AmountsScope, "not_per_transaction") || peer.AmountsScope == "message_only" {
		return crosschain.CandidateSearch{Status: "missing_destination_constraints", Reason: "opposite leg lacks a unique chain, beneficiary, asset or exact expected amount"}
	}
	record, err := b.record(local.Chain, local.TxHash, bridgeNativeAsset(local))
	if err != nil {
		return crosschain.CandidateSearch{Status: "anchor_unavailable", Reason: err.Error()}
	}
	anchors := legMovements(local, side, record.Movements)
	// Prefer the exactly reconciled event. If a protocol amount differs, one
	// unique observed funding event still supplies a candidate anchor.
	if parts := matchingParts(local, anchors); len(parts) == 1 {
		anchors = parts
	}
	if len(anchors) != 1 || anchors[0].Amount == "" {
		return crosschain.CandidateSearch{Status: "anchor_ambiguous", Reason: "funding anchor is missing or has multiple events"}
	}
	anchor := anchors[0]
	event := anchor.Index
	search := crosschain.CandidateRequest{Anchor: crosschain.Anchor{Chain: anchor.Chain, TxHash: anchor.TxHash, Dataset: anchor.Dataset, EventIndex: &event}, SearchChain: peer.Chain, SearchAddresses: []string{peer.Address}, SearchDirection: "in", Direction: "forward", AssetKind: "token", TokenContract: peer.TokenContract, AssetRelation: "swap", ExpectedAmount: peer.Amount, AbsoluteTolerance: "0", StartTime: anchor.Timestamp.Add(-10 * time.Minute), EndTime: anchor.Timestamp.Add(time.Hour), MaxParts: 3, MaxResults: 10, ScanLimit: 100, MaxCombinations: 1000}
	if anchor.Dataset == "transactions" {
		search.Anchor.EventIndex = nil
	}
	if side == 1 {
		search.SearchDirection = "out"
		search.Direction = "backward"
		search.StartTime = anchor.Timestamp.Add(-time.Hour)
		search.EndTime = anchor.Timestamp.Add(10 * time.Minute)
	}
	if stamp, e := time.Parse(time.RFC3339, peer.Timestamp); e == nil && stamp.Sub(anchor.Timestamp) > -23*time.Hour && stamp.Sub(anchor.Timestamp) < 23*time.Hour {
		search.StartTime = minTime(anchor.Timestamp, stamp).Add(-5 * time.Minute)
		search.EndTime = maxTime(anchor.Timestamp, stamp).Add(5 * time.Minute)
	}
	if bridgeNativeAsset(peer) {
		search.AssetKind = "native"
		search.TokenContract = ""
	}
	if err := search.Validate(); err != nil {
		return crosschain.CandidateSearch{Status: "constraints_unavailable", Reason: "protocol-derived search constraints are invalid"}
	}
	result := crosschain.CandidateSearch{Request: &search, AmountBasis: basis}
	release, err := b.s.crossChain.AcquireCandidate(b.ctx)
	if err != nil {
		result.Status = "budget_exhausted"
		result.Reason = "candidate capacity unavailable"
		return result
	}
	defer release()
	rows, cut, _, err := b.search(search)
	if err != nil {
		result.Status = "search_unavailable"
		result.Reason = err.Error()
		return result
	}
	matched, err := crosschain.Match(b.ctx, search, anchor, rows, cut)
	if err != nil {
		result.Status = "search_failed"
		result.Reason = fmt.Sprint(err)
		return result
	}
	result.Result = &matched
	result.Status = "searched"
	// A candidate is promoted only by the CCTP domain/nonce in the actual
	// official destination receipt, never by its time/amount score.
	if t.Bridge == "cctp" && side == 0 && peer.TxHash == "" {
		hashes := map[string]bool{}
		for _, candidate := range matched.Candidates {
			for _, m := range candidate.Movements {
				r, e := b.record(m.Chain, m.TxHash, false)
				if e != nil {
					continue
				}
				for _, h := range r.Hints {
					if h.Bridge == "cctp" && h.Kind == "received" && strings.EqualFold(h.Contract, crosschain.CCTPTransmitterV2) && h.SourceDomain+"/"+crosschain.CanonicalNonce(h.Identifier) == t.OrderID {
						hashes[m.TxHash] = true
					}
				}
			}
		}
		if len(hashes) == 1 {
			for hash := range hashes {
				t.Destination[0].TxHash = hash
				t.Status = "completed"
			}
			b.verify(t)
			result.Status = "identifier_matched"
		}
	}
	return result
}

func actionScopeAllowedBridgeAnalysis(ctx context.Context) bool {
	value, _ := ctx.Value(bridgeAnalysisPermission{}).(bool)
	return value
}
func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func (b *bridgeReadSession) completeCCTPFromKnownPeers(transfers []crosschain.Transfer) {
	for i := range transfers {
		t := &transfers[i]
		if t.Bridge != "cctp" || len(t.Source) != 1 || len(t.Destination) != 1 || t.Destination[0].TxHash != "" {
			continue
		}
		hashes := map[string]bool{}
		for j := range transfers {
			other := &transfers[j]
			if i == j {
				continue
			}
			sourceMatch := false
			for _, leg := range other.Source {
				if leg.Chain == t.Source[0].Chain && crosschain.Same(leg.Chain, leg.TxHash, t.Source[0].TxHash) {
					sourceMatch = true
				}
			}
			if !sourceMatch {
				continue
			}
			for _, leg := range other.Destination {
				if leg.Chain != t.Destination[0].Chain || leg.TxHash == "" {
					continue
				}
				record, err := b.record(leg.Chain, leg.TxHash, false)
				if err != nil || record.Status != "success" {
					continue
				}
				for _, hint := range record.Hints {
					if hint.Bridge == "cctp" && hint.Kind == "received" && strings.EqualFold(hint.Contract, crosschain.CCTPTransmitterV2) && hint.SourceDomain+"/"+crosschain.CanonicalNonce(hint.Identifier) == t.OrderID {
						hashes[leg.TxHash] = true
					}
				}
			}
		}
		if len(hashes) == 1 {
			for hash := range hashes {
				t.Destination[0].TxHash = hash
			}
			t.Status = "completed"
			b.verify(t)
		}
	}
}
