package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
	"github.com/blockchain-platform/blockchain-platform/internal/fundtrace"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

const nextVersion = "investigation-v2.1"

var nextOperations = []string{"get_investigation_capabilities", "reconcile_address_ledger", "trace_source_set", "explain_transaction", "match_bridge_transfer", "get_token_allowances", "get_control_history", "start_analysis", "get_analysis_status", "get_analysis_results", "cancel_analysis", "resume_analysis", "create_evidence_bundle", "get_evidence_bundle", "verify_evidence_bundle", "create_watch_rule", "list_watch_rules", "update_watch_rule", "set_watch_rule_state", "get_watch_events"}

type nextSource struct {
	TxHash    string  `json:"tx_hash"`
	LogIndex  *uint64 `json:"log_index"`
	AmountRaw string  `json:"amount_raw,omitempty"`
	SourceID  string  `json:"source_id,omitempty"`
}
type nextRequest struct {
	Chain             string          `json:"chain,omitempty"`
	Address           string          `json:"address,omitempty"`
	TokenContract     string          `json:"token_contract,omitempty"`
	Block             json.RawMessage `json:"block,omitempty"`
	Timestamp         string          `json:"timestamp,omitempty"`
	FromBlock         *uint64         `json:"from_block,omitempty"`
	ToBlock           *uint64         `json:"to_block,omitempty"`
	MaxRecords        int             `json:"max_records,omitempty"`
	MaxNodes          int             `json:"max_nodes,omitempty"`
	MaxHops           int             `json:"max_hops,omitempty"`
	AttributionModel  string          `json:"attribution_model,omitempty"`
	Sources           []nextSource    `json:"sources,omitempty"`
	Repair            bool            `json:"repair,omitempty"`
	Mode              string          `json:"mode,omitempty"`
	IdempotencyKey    string          `json:"idempotency_key,omitempty"`
	TxHash            string          `json:"tx_hash,omitempty"`
	IncludeTrace      bool            `json:"include_trace,omitempty"`
	Spenders          []string        `json:"spenders,omitempty"`
	Discover          bool            `json:"discover,omitempty"`
	DestinationChain  string          `json:"destination_chain,omitempty"`
	DestinationTxHash string          `json:"destination_tx_hash,omitempty"`
	Protocol          string          `json:"protocol,omitempty"`
	Operation         string          `json:"operation,omitempty"`
	Arguments         json.RawMessage `json:"arguments,omitempty"`
	AnalysisID        string          `json:"analysis_id,omitempty"`
	IncludeMetadata   *bool           `json:"include_metadata,omitempty"`
	Section           string          `json:"section,omitempty"`
	Cursor            string          `json:"cursor,omitempty"`
	PageSize          int             `json:"page_size,omitempty"`
	BundleID          string          `json:"bundle_id,omitempty"`
	Offset            int64           `json:"offset,omitempty"`
	ChunkSize         int             `json:"chunk_size,omitempty"`
	RuleID            string          `json:"rule_id,omitempty"`
	Name              string          `json:"name,omitempty"`
	RuleType          string          `json:"rule_type,omitempty"`
	IntervalSeconds   int             `json:"interval_seconds,omitempty"`
	Confirmations     uint64          `json:"confirmations,omitempty"`
	State             string          `json:"state,omitempty"`
	ExpectedRevision  int             `json:"expected_revision,omitempty"`
}

func decodeNext(raw []byte) (nextRequest, error) {
	var q nextRequest
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	d.UseNumber()
	if e := d.Decode(&q); e != nil {
		return q, e
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return q, fmt.Errorf("expected one request object")
	}
	return q, nil
}
func (s *Server) registerNextInvestigationRoutes(r chi.Router) {
	for _, name := range nextOperations {
		op := name
		prefix := "/analysis/"
		if strings.Contains(op, "evidence_bundle") {
			prefix = "/export/"
		}
		r.Post(prefix+op, func(w http.ResponseWriter, r *http.Request) { s.handleNextInvestigation(w, r, op) })
	}
}
func (s *Server) handleNextInvestigation(w http.ResponseWriter, r *http.Request, op string) {
	body, e := io.ReadAll(io.LimitReader(r.Body, (2<<20)+1))
	if e != nil || len(body) > 2<<20 {
		investigationError(w, fmt.Errorf("request exceeds 2 MiB"))
		return
	}
	q, e := decodeNext(body)
	if e == nil {
		e = s.validateNext(&q, op)
	}
	if e != nil {
		investigationError(w, e)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 50*time.Second)
	defer cancel()
	var data any
	if q.Mode == "async" && nextComputable(op) {
		data, e = s.nextSubmit(ctx, op, q)
	} else {
		data, e = s.nextDispatch(ctx, q, op)
	}
	if e != nil {
		investigationError(w, e)
		return
	}
	encoded, e := json.Marshal(data)
	if e != nil {
		investigationError(w, e)
		return
	}
	if len(encoded) > 8<<20 {
		investigationError(w, summaryError(422, "INLINE_RESULT_TOO_LARGE", "Result exceeds 8 MiB; use mode=async and cursor result pages"))
		return
	}
	actionOK(w, data, map[string]any{"source": "rpc_and_indexed_evidence", "dataset_version": nextVersion, "coverage": map[string]any{"complete": false, "basis": "Inspect operation-specific coverage dimensions"}})
}
func nextComputable(op string) bool {
	switch op {
	case "reconcile_address_ledger", "trace_source_set", "explain_transaction", "match_bridge_transfer", "get_token_allowances", "get_control_history":
		return true
	}
	return false
}
func (s *Server) validateNext(q *nextRequest, op string) error {
	if q.Mode == "" {
		q.Mode = "inline"
	}
	if q.Mode != "inline" && q.Mode != "async" {
		return fmt.Errorf("mode must be inline or async")
	}
	if len(q.IdempotencyKey) > 128 {
		return fmt.Errorf("idempotency_key exceeds 128 characters")
	}
	if q.PageSize == 0 {
		q.PageSize = 100
	}
	if q.PageSize < 1 || q.PageSize > 500 || len(q.Cursor) > 2048 {
		return fmt.Errorf("invalid pagination")
	}
	if q.MaxRecords == 0 {
		q.MaxRecords = 5000
	}
	if q.MaxRecords < 1 || q.MaxRecords > 20000 {
		return fmt.Errorf("max_records must be 1-20000")
	}
	if q.MaxNodes == 0 {
		q.MaxNodes = 50
	}
	if q.MaxNodes < 2 || q.MaxNodes > 100 {
		return fmt.Errorf("max_nodes must be 2-100")
	}
	if q.MaxHops == 0 {
		q.MaxHops = 3
	}
	if q.MaxHops < 1 || q.MaxHops > 5 {
		return fmt.Errorf("max_hops must be 1-5")
	}
	if q.AttributionModel == "" {
		q.AttributionModel = "both"
	}
	if q.AttributionModel != "both" && q.AttributionModel != "fifo" && q.AttributionModel != "proportional" {
		return fmt.Errorf("invalid attribution_model")
	}
	if q.FromBlock != nil && q.ToBlock != nil && *q.FromBlock > *q.ToBlock {
		return fmt.Errorf("from_block must not exceed to_block")
	}
	for _, id := range []string{q.AnalysisID, q.BundleID, q.RuleID} {
		if id != "" {
			if _, e := uuid.Parse(id); e != nil {
				return fmt.Errorf("invalid object UUID")
			}
		}
	}
	if q.Timestamp != "" && len(q.Block) > 0 {
		return fmt.Errorf("timestamp and block are mutually exclusive")
	}
	if q.Timestamp != "" {
		if _, e := time.Parse(time.RFC3339, q.Timestamp); e != nil {
			return fmt.Errorf("invalid timestamp")
		}
	}
	if nextComputable(op) || op == "create_watch_rule" || op == "update_watch_rule" || op == "get_investigation_capabilities" {
		var e error
		q.Chain, e = canonicalActionChain(q.Chain)
		if e != nil {
			return e
		}
		cfg, e := s.deps.ChainRead.Chains().Require(q.Chain)
		if e != nil || !cfg.IsEVM() {
			return fmt.Errorf("this operation requires an EVM chain")
		}
		for _, field := range []*string{&q.Address, &q.TokenContract} {
			if *field != "" {
				a, e := chain.ParseAddress(*field)
				if e != nil {
					return fmt.Errorf("invalid complete address")
				}
				*field = a.String()
			}
		}
		if (op == "reconcile_address_ledger" || op == "get_token_allowances" || op == "get_control_history" || op == "create_watch_rule" || op == "update_watch_rule") && q.Address == "" {
			return fmt.Errorf("address is required")
		}
		if (op == "reconcile_address_ledger" || op == "trace_source_set" || op == "get_token_allowances") && q.TokenContract == "" {
			return fmt.Errorf("exact ERC20 token_contract is required")
		}
		for _, field := range []*string{&q.TxHash, &q.DestinationTxHash} {
			if *field != "" {
				h, e := chain.ParseHash(*field)
				if e != nil {
					return e
				}
				*field = h.String()
			}
		}
		if (op == "explain_transaction" || op == "match_bridge_transfer") && q.TxHash == "" {
			return fmt.Errorf("tx_hash is required")
		}
		if op == "match_bridge_transfer" {
			q.DestinationChain, e = canonicalActionChain(q.DestinationChain)
			if e != nil {
				return e
			}
			if q.DestinationTxHash == "" {
				return fmt.Errorf("destination_tx_hash is required for protocol verification")
			}
			if q.Protocol != "cctp_v1" {
				return fmt.Errorf("supported verified bridge adapter: cctp_v1")
			}
		}
		if len(q.Spenders) > 200 {
			return fmt.Errorf("at most 200 spenders")
		}
		for i, a := range q.Spenders {
			v, e := chain.ParseAddress(a)
			if e != nil {
				return e
			}
			q.Spenders[i] = v.String()
		}
		if op == "get_token_allowances" && len(q.Spenders) == 0 && !q.Discover {
			return fmt.Errorf("provide spenders or discover=true with a bounded block range")
		}
		if q.Discover && (q.FromBlock == nil || q.ToBlock == nil || *q.ToBlock-*q.FromBlock > 100000) {
			return fmt.Errorf("discovery requires from_block/to_block spanning at most 100000 blocks")
		}
		if op == "get_control_history" && (q.FromBlock == nil || q.ToBlock == nil || *q.ToBlock-*q.FromBlock > 100000) {
			return fmt.Errorf("control history requires a range of at most 100000 blocks")
		}
		if op == "reconcile_address_ledger" && (q.FromBlock == nil || *q.FromBlock == 0) {
			return fmt.Errorf("from_block must be positive; opening balance is read at from_block-1")
		}
		if op == "trace_source_set" {
			if len(q.Sources) < 1 || len(q.Sources) > 100 {
				return fmt.Errorf("sources must contain 1-100 receipt references")
			}
			ids := map[string]bool{}
			events := map[string]bool{}
			for i := range q.Sources {
				v := &q.Sources[i]
				h, e := chain.ParseHash(v.TxHash)
				if e != nil {
					return e
				}
				v.TxHash = h.String()
				if v.AmountRaw != "" {
					if amount, e := nextRaw(v.AmountRaw); e != nil || amount.Sign() == 0 {
						return fmt.Errorf("source amount_raw must be a positive uint256 string")
					}
				}
				if v.SourceID == "" {
					v.SourceID = fmt.Sprintf("source-%d", i+1)
				}
				if len(v.SourceID) > 128 || ids[v.SourceID] {
					return fmt.Errorf("invalid or duplicate source_id")
				}
				ids[v.SourceID] = true
				key := v.TxHash
				if v.LogIndex != nil {
					key += fmt.Sprintf(":%d", *v.LogIndex)
				}
				if events[key] {
					return fmt.Errorf("duplicate source receipt reference")
				}
				events[key] = true
			}
		}
	}
	return nil
}
func (s *Server) nextPoint(ctx context.Context, q nextRequest) (chainread.StatePoint, error) {
	return s.statePoint(ctx, investigationRequest{Chain: q.Chain, Block: q.Block, Timestamp: q.Timestamp})
}
func (s *Server) nextDispatch(ctx context.Context, q nextRequest, op string) (any, error) {
	switch op {
	case "get_investigation_capabilities":
		return s.nextCapabilities(q), nil
	case "reconcile_address_ledger":
		return s.nextReconcile(ctx, q)
	case "trace_source_set":
		return s.nextJoint(ctx, q)
	case "explain_transaction":
		return s.nextExplain(ctx, q)
	case "match_bridge_transfer":
		return s.nextBridge(ctx, q)
	case "get_token_allowances":
		return s.nextAllowances(ctx, q)
	case "get_control_history":
		return s.nextControlHistory(ctx, q)
	case "start_analysis":
		if q.Operation == ca.Operation {
			request, e := ca.Decode(q.Arguments)
			if e != nil {
				return nil, e
			}
			request.Defaults()
			if e = request.Validate(); e != nil {
				return nil, e
			}
			request.IdempotencyKey = q.IdempotencyKey
			return s.contractSubmit(ctx, request)
		}
		inner, e := decodeNext(q.Arguments)
		if e != nil {
			return nil, e
		}
		if !nextComputable(q.Operation) {
			return nil, fmt.Errorf("operation cannot be queued")
		}
		if e = s.validateNext(&inner, q.Operation); e != nil {
			return nil, e
		}
		inner.IdempotencyKey = q.IdempotencyKey
		return s.nextSubmit(ctx, q.Operation, inner)
	case "get_analysis_status", "get_analysis_results", "cancel_analysis", "resume_analysis":
		return s.nextJobOperation(ctx, q, op)
	case "create_evidence_bundle", "get_evidence_bundle", "verify_evidence_bundle":
		return s.nextBundle(ctx, q, op)
	case "create_watch_rule", "list_watch_rules", "update_watch_rule", "set_watch_rule_state", "get_watch_events":
		return s.nextWatch(ctx, q, op)
	}
	return nil, fmt.Errorf("unsupported operation")
}
func (s *Server) nextCapabilities(q nextRequest) any {
	return map[string]any{"version": nextVersion, "chain": q.Chain, "operations": nextOperations, "balance_source": "rpc_state", "joint_models": []string{"fifo", "proportional"}, "source_overlap_policy": "reject", "bridge_adapters": []string{"cctp_v1"}, "archive_read": "provider dependent; exact historical failures are returned", "watch_delivery": "workspace event inbox only", "budgets": map[string]any{"sources": 100, "addresses_per_batch": 200, "max_records": 20000, "max_nodes": 100, "max_hops": 5, "max_allocation_work": fundtrace.MaxAllocationWork, "inline_timeout_seconds": 50, "worker_timeout_seconds": 300, "max_result_bytes": 32 << 20, "page_size": 500, "history_range_blocks": 100000}, "limitations": []string{"ERC20 ledger reconciliation excludes native internal transfers and gas", "Crosschain verification requires both receipt hashes; adapter requires registered CCTP emitters", "Watch rules poll confirmed snapshots and can miss changes between polls", "Decoded events/ABI permissions do not prove beneficial ownership"}}
}
