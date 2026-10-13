package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/crosschain"
	"github.com/go-chi/chi/v5"
)

func (s *Server) registerCrossChainRoutes(r chi.Router) {
	r.Get("/bridge/capabilities", s.handleBridgeCapabilities)
	r.Post("/bridge/transfer", s.handleBridgeTransfer)
	r.Post("/bridge/candidates", s.handleBridgeCandidates)
}

func decodeBridgeRequest(w http.ResponseWriter, r *http.Request, value any) error {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(value); e != nil {
		return e
	}
	if e := d.Decode(new(any)); e != io.EOF {
		return errors.New("exactly one JSON object required")
	}
	return nil
}

func (s *Server) handleBridgeCapabilities(w http.ResponseWriter, r *http.Request) {
	engine := s.crossChain
	if engine == nil {
		engine = crosschain.FromEnv()
	}
	actionOK(w, map[string]any{"protocols": engine.Capabilities(), "chain_validation": crosschain.Chains, "candidate_chains": crosschain.Chains, "limits": map[string]any{"max_time_range_hours": 24, "max_search_addresses": 10, "max_scan_records": 1000, "max_parts": 3, "max_combinations": 20000, "max_results": 50, "max_concurrent_candidate_queries": 2, "max_concurrent_chain_reads": 4, "max_chain_read_calls_per_request": 64, "default_chain_read_limit": 32, "attested_cctp_proof_limit": 48, "registered_oft_proof_limit": 48, "registered_stargate_taxi_proof_limit": 64, "max_transactions_per_request": 16, "max_funding_events_per_transaction": 400, "max_provider_page_size": 50, "max_provider_transactions_per_search": 8}, "candidate_matching": "explicit asset/address/time filters; same-asset or caller-supplied swap amount; scores are ranking only", "verification_modes": []string{"funds", "discovery"}, "default_verification_mode": "funds", "verification_rule_version": crosschain.FundsVerificationVersion, "strict_verification": crosschain.StrictCapabilities()}, map[string]any{"source": "server_capability_registry", "cached": false, "dataset_version": "crosschain-capabilities-v1", "automatic_probes": false})
}

func (s *Server) handleBridgeTransfer(w http.ResponseWriter, r *http.Request) {
	var req crosschain.QueryRequest
	if e := decodeBridgeRequest(w, r, &req); e != nil {
		actionFail(w, 400, "INVALID_BRIDGE_REQUEST", e.Error())
		return
	}
	if e := req.Validate(); e != nil {
		actionFail(w, 400, "INVALID_BRIDGE_REQUEST", e.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	engine := s.crossChain
	if engine == nil {
		actionFail(w, 503, "BRIDGE_SERVICE_UNAVAILABLE", "Bridge service is not initialized")
		return
	}
	reads := newBridgeReadSession(ctx, s)
	if req.Bridge == "cctp" || req.Bridge == "auto" {
		if record, err := reads.record(req.Chain, req.TxHash, false); err == nil {
			req.Hints = record.Hints
		}
	}
	result, e := engine.Query(ctx, req)
	if e != nil {
		actionFail(w, 400, "INVALID_BRIDGE_REQUEST", e.Error())
		return
	}
	if len(result.Transfers) == 0 && req.TxHash != "" {
		if record, err := reads.record(req.Chain, req.TxHash, true); err == nil {
			result.ProtocolHints = record.Hints
			for _, hint := range record.Hints {
				if len(req.Hints) == 0 && (req.Bridge == "auto" || req.Bridge == "cctp") && hint.Bridge == "cctp" && hint.Kind == "received" {
					follow := req
					follow.Bridge = "cctp"
					follow.Hints = record.Hints
					if found, err := engine.Query(ctx, follow); err == nil {
						result.Attempts = append(result.Attempts, found.Attempts...)
						result.Transfers = append(result.Transfers, found.Transfers...)
					}
					break
				}
			}
		} else {
			result.CandidateSearches = append(result.CandidateSearches, crosschain.CandidateSearch{Status: "anchor_unavailable", Reason: err.Error()})
		}
	}
	for i := range result.Transfers {
		reads.verify(&result.Transfers[i])
	}
	reads.completeCCTPFromKnownPeers(result.Transfers)
	for i := range result.Transfers {
		reads.verifyFunds(&result.Transfers[i])
	}
	for i := range result.Transfers {
		if req.TxHash != "" && !result.Transfers[i].OnChainVerified && !result.Transfers[i].MessageOnly && !result.Transfers[i].FundsVerified {
			search := reads.automaticCandidates(req, &result.Transfers[i])
			result.CandidateSearches = append(result.CandidateSearches, search)
			if search.Status == "identifier_matched" {
				reads.verifyFunds(&result.Transfers[i])
			}
		}
	}
	if len(result.Transfers) == 0 && len(result.CandidateSearches) == 0 {
		result.CandidateSearches = append(result.CandidateSearches, crosschain.CandidateSearch{Status: "missing_destination_constraints", Reason: "receipt did not expose a verified target chain, beneficiary and asset; bounded search cannot be started"})
	}
	crosschain.EnforceFundsOutput(req.VerificationMode, &result)
	actionOK(w, result, map[string]any{"source": "bridge_protocol_api_and_exact_chain_evidence", "cached": false, "dataset_version": crosschain.FundsVerificationVersion, "coverage": map[string]any{"complete": false, "scope": "protocol_query", "reason": "one bridge lookup does not establish complete historical coverage"}, "verification_source": "chain_index_then_exact_transaction_rpc", "chain_read_calls": reads.calls, "proof_protocol_read_calls": reads.protocolCalls, "chain_read_trace_id": reads.traceID, "automatic_downloads": false, "queried_at": time.Now().UTC(), "query": req})
}

func (s *Server) verifyBridgeTransfer(ctx context.Context, t *crosschain.Transfer) {
	newBridgeReadSession(ctx, s).verify(t)
}

func bridgeNativeAsset(leg crosschain.Leg) bool {
	contract := strings.ToLower(leg.TokenContract)
	if contract == "native" || contract == "0x0000000000000000000000000000000000000000" || contract == "0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee" || leg.Chain == "solana" && contract == "11111111111111111111111111111111" {
		return true
	}
	if contract != "" {
		return false
	}
	symbol := map[string]string{"ethereum": "ETH", "bsc": "BNB", "tron": "TRX", "polygon": "POL", "arbitrum": "ETH", "optimism": "ETH", "base": "ETH", "avalanche": "AVAX", "solana": "SOL"}[leg.Chain]
	return symbol != "" && strings.EqualFold(leg.Token, symbol)
}

func (s *Server) handleBridgeCandidates(w http.ResponseWriter, r *http.Request) {
	var req crosschain.CandidateRequest
	if e := decodeBridgeRequest(w, r, &req); e != nil {
		actionFail(w, 400, "INVALID_COLLISION_REQUEST", e.Error())
		return
	}
	if e := req.Validate(); e != nil {
		actionFail(w, 400, "INVALID_COLLISION_REQUEST", e.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	if s.crossChain == nil {
		actionFail(w, 503, "BRIDGE_SERVICE_UNAVAILABLE", "Bridge service is not initialized")
		return
	}
	release, e := s.crossChain.AcquireCandidate(ctx)
	if e != nil {
		actionFail(w, 429, "COLLISION_CAPACITY_EXCEEDED", "Candidate query capacity was not available within the request budget")
		return
	}
	defer release()
	reads := newBridgeReadSession(ctx, s)
	rows, truncated, e := reads.anchor(req.Anchor)
	if e != nil {
		if s.deps.Log != nil {
			s.deps.Log.Warn("cross-chain anchor query failed", "error", e.Error())
		}
		actionFail(w, 502, "ANCHOR_QUERY_FAILED", "Unable to read anchor movements within the query budget")
		return
	}
	if truncated {
		actionFail(w, 400, "ANCHOR_EVENT_REQUIRED", "Anchor contains too many movements; provide event_index")
		return
	}
	anchor, e := crosschain.SelectAnchor(req.Anchor, rows)
	if e != nil {
		actionFail(w, 400, "ANCHOR_UNAVAILABLE_OR_AMBIGUOUS", e.Error())
		return
	}
	items, scanTruncated, searchSources, e := reads.search(req)
	if e != nil {
		if s.deps.Log != nil {
			s.deps.Log.Warn("cross-chain candidate query failed", "error", e.Error())
		}
		actionFail(w, 502, "CANDIDATE_QUERY_FAILED", "Unable to read candidate movements within the query budget")
		return
	}
	result, e := crosschain.Match(ctx, req, anchor, items, scanTruncated)
	if e != nil {
		actionFail(w, 400, "COLLISION_FAILED", e.Error())
		return
	}
	datasets := []string{"token_transfers", "transactions"}
	if req.AssetKind == "native" {
		datasets = []string{"transactions", "internal_transactions"}
	}
	coverage := s.actionCoverageMeta(ctx, req.SearchChain, req.SearchAddresses, datasets)
	actionOK(w, result, map[string]any{"source": "chain_index_and_bounded_transaction_reads", "search_sources": searchSources, "chain_read_calls": reads.calls, "proof_protocol_read_calls": reads.protocolCalls, "chain_read_trace_id": reads.traceID, "cached": false, "dataset_version": "crosschain-candidates-v1", "search": req, "address_dataset_coverage": coverage, "coverage": map[string]any{"complete": false, "scope": "requested_time_range", "datasets": datasets, "reason": "time-range completeness is not asserted from address history alone; inspect address_dataset_coverage"}, "truncated": result.ScanTruncated || result.ResultTruncated || result.CombinationsTruncated, "matching_version": "1", "score_semantics": "ranking_only", "automatic_downloads": false})
}
