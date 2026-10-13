package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/blockchain-platform/blockchain-platform/internal/addressnorm"
	"github.com/blockchain-platform/blockchain-platform/internal/assetidentity"
	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
	"github.com/blockchain-platform/blockchain-platform/internal/fundtrace"
	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
	"github.com/blockchain-platform/blockchain-platform/internal/query"
	"github.com/go-chi/chi/v5"
	"golang.org/x/sync/errgroup"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type investigationRequest struct {
	Chain            string           `json:"chain"`
	Address          string           `json:"address"`
	Token            string           `json:"token"`
	TokenContract    string           `json:"token_contract"`
	Tokens           []string         `json:"tokens"`
	Block            json.RawMessage  `json:"block"`
	BlockTag         string           `json:"blockTag"`
	Timestamp        string           `json:"timestamp"`
	Method           string           `json:"method"`
	Params           []any            `json:"params"`
	Call             map[string]any   `json:"call"`
	Position         string           `json:"position"`
	TxHash           string           `json:"tx_hash"`
	Filter           map[string]any   `json:"filter"`
	FullTransactions bool             `json:"full_transactions"`
	Function         string           `json:"function"`
	Args             []any            `json:"args"`
	OutputTypes      []string         `json:"output_types"`
	ABI              json.RawMessage  `json:"abi"`
	Input            string           `json:"input"`
	Logs             []map[string]any `json:"logs"`
	Direction        string           `json:"direction"`
	Counterparty     string           `json:"counterparty"`
	GroupBy          []string         `json:"group_by"`
	Metrics          []string         `json:"metrics"`
	OrderBy          string           `json:"order_by"`
	Page             int              `json:"page"`
	PageSize         int              `json:"page_size"`
	Cursor           string           `json:"cursor"`
	StartTime        string           `json:"start_time"`
	EndTime          string           `json:"end_time"`
	Start            string           `json:"start"`
	End              string           `json:"end"`
	IntervalSeconds  int64            `json:"interval_seconds"`
	JobID            string           `json:"job_id"`
	Offset           int64            `json:"offset"`
	ChunkSize        int              `json:"chunk_size"`
	SourceTx         string           `json:"source_tx"`
	Source           string           `json:"source"`
	Amount           string           `json:"amount"`
	LogIndex         *uint64          `json:"log_index"`
	MaxHops          int              `json:"max_hops"`
	TimeWindow       int64            `json:"time_window"`
	MaxNodes         int              `json:"max_nodes"`
	MaxRecords       int              `json:"max_records"`
	SourceAddress    string           `json:"source_address"`
	SourceTxHash     string           `json:"source_tx_hash"`
	MaxDepth         int              `json:"max_depth"`
	AttributionModel string           `json:"attribution_model"`
	Addresses        []string         `json:"addresses"`
	MaxConcurrent    int              `json:"max_concurrent"`
}

var investigationOperations = []string{"get_native_balance", "get_token_balance", "get_all_token_balances", "get_balance_at", "get_balance_history", "read_rpc", "rpc_call", "trace_fund_batch", "resolve_proxy", "batch_get_native_balances", "batch_get_token_balances", "eth_call", "eth_getCode", "eth_getStorageAt", "eth_getTransactionByHash", "eth_getTransactionReceipt", "eth_getLogs", "eth_getBalance", "eth_blockNumber", "eth_getBlockByNumber", "get_contract_abi", "decode_transaction_input", "decode_logs", "resolve_proxy_implementation", "read_contract", "aggregate_transfers", "get_export_file", "download_export", "trace_funds", "get_labels", "detect_exchange_deposit", "trace_to_exchange", "get_contract_control"}

func (s *Server) registerInvestigationRoutes(r chi.Router) {
	s.registerNextInvestigationRoutes(r)
	for _, name := range investigationOperations {
		op := name
		prefix := "/analysis/"
		if op == "get_export_file" || op == "download_export" {
			prefix = "/export/"
		}
		r.Post(prefix+op, func(w http.ResponseWriter, r *http.Request) { s.handleInvestigation(w, r, op) })
	}
}
func investigationError(w http.ResponseWriter, err error) {
	if errors.Is(err, context.DeadlineExceeded) {
		actionFail(w, 504, "QUERY_TIMEOUT", "Query budget expired; narrow the request and retry")
		return
	}
	if errors.Is(err, context.Canceled) {
		actionFail(w, 408, "QUERY_CANCELED", "Query canceled")
		return
	}
	if errors.Is(err, fundtrace.ErrAllocationBudget) {
		actionFail(w, 422, "ALLOCATION_BUDGET_EXCEEDED", "Allocation work budget reached; narrow time_window/max_records or use a single model")
		return
	}
	var re *rpc.Error
	if errors.As(err, &re) {
		actionFail(w, 503, strings.ToUpper(string(re.Class)), re.Public())
		return
	}
	var se *exportSummaryError
	if errors.As(err, &se) {
		writeExportSummaryError(w, err)
		return
	}
	if strings.Contains(err.Error(), "clickhouse") || strings.Contains(err.Error(), "code:") {
		if actionScanFailure(w, err) {
			return
		}
		actionFail(w, 503, "QUERY_UNAVAILABLE", "Indexed query is unavailable; retry or narrow the filters")
		return
	}
	if actionScanFailure(w, err) {
		return
	}
	actionFail(w, 400, "INVALID_QUERY", err.Error())
}
func (s *Server) handleInvestigation(w http.ResponseWriter, r *http.Request, op string) {
	var req investigationRequest
	dec := json.NewDecoder(io.LimitReader(r.Body, 2<<20))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	if err := dec.Decode(&req); err != nil {
		actionFail(w, 400, "INVALID_QUERY", "Invalid request: "+err.Error())
		return
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		actionFail(w, 400, "INVALID_QUERY", "Expected one JSON object")
		return
	}

	if op == "trace_fund_batch" {
		if req.Source != "" && req.SourceAddress != "" && req.Source != req.SourceAddress {
			investigationError(w, fmt.Errorf("conflicting source/source_address"))
			return
		}
		if req.SourceAddress != "" {
			req.Source = req.SourceAddress
		}
		if req.SourceTx != "" && req.SourceTxHash != "" && req.SourceTx != req.SourceTxHash {
			investigationError(w, fmt.Errorf("conflicting source transaction hashes"))
			return
		}
		if req.SourceTxHash != "" {
			req.SourceTx = req.SourceTxHash
		}
		if req.MaxHops != 0 && req.MaxDepth != 0 && req.MaxHops != req.MaxDepth {
			investigationError(w, fmt.Errorf("conflicting max_hops/max_depth"))
			return
		}
		if req.MaxDepth != 0 {
			req.MaxHops = req.MaxDepth
		}
		if req.MaxHops == 0 {
			req.MaxHops = 5
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 55*time.Second)
	defer cancel()
	if op == "get_export_file" || op == "download_export" {
		data, err := s.investigationExport(ctx, req)
		if err != nil {
			investigationError(w, err)
			return
		}
		actionOK(w, data, map[string]any{"source": "export_object_store", "cached": false, "dataset_version": "export-sha256-v1"})
		return
	}
	var err error
	req.Chain, err = canonicalActionChain(req.Chain)
	if err != nil {
		investigationError(w, err)
		return
	}
	if req.Address != "" {
		req.Address, err = addressnorm.Normalize(req.Chain, req.Address)
		if err != nil {
			investigationError(w, fmt.Errorf("invalid complete address"))
			return
		}
	}
	for _, field := range []*string{&req.Source} {
		if *field != "" {
			*field, err = addressnorm.Normalize(req.Chain, *field)
			if err != nil {
				investigationError(w, fmt.Errorf("invalid source address"))
				return
			}
		}
	}
	for _, field := range []*string{&req.TxHash, &req.SourceTx} {
		if *field != "" {
			h, e := chain.ParseHash(*field)
			if e != nil {
				investigationError(w, fmt.Errorf("invalid transaction hash"))
				return
			}
			*field = h.String()
		}
	}

	if req.Token != "" || req.TokenContract != "" {
		if req.TokenContract == "" && (op == "get_balance_at" || op == "get_balance_history") && strings.EqualFold(req.Token, mustChain(s, req.Chain).Native.Symbol) {
			req.Token = ""
		} else {
			req.Token, req.TokenContract, err = resolveActionAsset(req.Chain, req.Token, req.TokenContract)
			if err != nil {
				investigationError(w, err)
				return
			}
		}
	}
	if req.TokenContract == "" && strings.HasPrefix(req.Token, "0x") {
		req.TokenContract = req.Token
		req.Token = ""
	}
	if req.TokenContract != "" {
		req.TokenContract, err = addressnorm.Normalize(req.Chain, req.TokenContract)
		if err != nil {
			investigationError(w, fmt.Errorf("invalid token contract"))
			return
		}
	}
	if s.deps.ChainRead == nil {
		actionFail(w, 503, "CHAIN_READ_UNAVAILABLE", "Chain reader is unavailable")
		return
	}
	data, meta, err := s.investigationDispatch(ctx, req, op)
	if err != nil {
		investigationError(w, err)
		return
	}
	encoded, e := json.Marshal(data)
	if e != nil {
		actionFail(w, 500, "INTERNAL_ERROR", "Unable to encode investigation result")
		return
	}
	var public any
	reader := json.NewDecoder(strings.NewReader(string(encoded)))
	reader.UseNumber()
	if e = reader.Decode(&public); e != nil {
		actionFail(w, 500, "INTERNAL_ERROR", "Unable to encode investigation result")
		return
	}
	decorateInvestigation(public, req.Chain)
	actionOK(w, public, meta)
}
func investigationOptions() chainread.Options {
	return chainread.Options{Purpose: rpc.PurposeInvestigation, Priority: rpc.PriorityInteractive}
}
func investigationMeta(point *chainread.StatePoint) map[string]any {
	m := map[string]any{"source": "rpc_state", "cached": false, "dataset_version": "rpc-state-v1", "coverage": map[string]any{"complete": true, "reason": "Exact state of the selected block; token discovery is separately qualified"}}
	if point != nil {
		m["freshness"] = map[string]any{"as_of_block": point.Block, "as_of_time": point.Time}
	} else {
		m["freshness"] = map[string]any{"as_of_block": nil, "as_of_time": time.Now().UTC()}
	}
	return m
}
func requestRef(q investigationRequest) (chain.BlockRef, *time.Time, error) {
	if q.Timestamp != "" && (q.BlockTag != "" || (len(q.Block) > 0 && string(q.Block) != "null")) {
		return chain.BlockRef{}, nil, fmt.Errorf("timestamp and block are mutually exclusive")
	}
	str := q.BlockTag
	if len(q.Block) > 0 && string(q.Block) != "null" {
		if str != "" {
			return chain.BlockRef{}, nil, fmt.Errorf("use block or blockTag")
		}
		if json.Unmarshal(q.Block, &str) != nil {
			var n json.Number
			if json.Unmarshal(q.Block, &n) != nil {
				return chain.BlockRef{}, nil, fmt.Errorf("block must be an exact integer or tag")
			}
			str = n.String()
		}
	}
	if str == "" {
		str = "latest"
	}
	ref, err := chain.ParseBlockRef(str)
	if err != nil {
		return ref, nil, err
	}
	if q.Timestamp != "" {
		if str != "latest" {
			return ref, nil, fmt.Errorf("timestamp and block are mutually exclusive")
		}
		t, err := time.Parse(time.RFC3339, q.Timestamp)
		if err != nil {
			return ref, nil, fmt.Errorf("timestamp must be ISO8601")
		}
		return ref, &t, nil
	}
	return ref, nil, nil
}
func (s *Server) statePoint(ctx context.Context, q investigationRequest) (chainread.StatePoint, error) {
	ref, at, err := requestRef(q)
	if err != nil {
		return chainread.StatePoint{}, err
	}
	return s.deps.ChainRead.ResolveState(ctx, q.Chain, ref, at, investigationOptions())
}
func (s *Server) investigationDispatch(ctx context.Context, q investigationRequest, op string) (any, map[string]any, error) {

	if op == "rpc_call" {
		op = "read_rpc"
	}
	if op == "resolve_proxy" {
		op = "resolve_proxy_implementation"
	}
	if op == "batch_get_native_balances" || op == "batch_get_token_balances" {
		return s.investigationBalanceBatch(ctx, q, op)
	}
	if op == "trace_fund_batch" && q.SourceTx == "" {
		return s.investigationSelectSource(ctx, q)
	}
	if op == "read_rpc" || strings.HasPrefix(op, "eth_") {
		method := op
		params := q.Params
		if op == "read_rpc" {
			method = q.Method
		} else {
			ref, _, err := requestRef(q)
			if err != nil {
				return nil, nil, err
			}
			switch op {
			case "eth_call":
				params = []any{q.Call, ref.String()}
			case "eth_getCode", "eth_getBalance":
				params = []any{q.Address, ref.String()}
			case "eth_getStorageAt":
				params = []any{q.Address, q.Position, ref.String()}
			case "eth_getTransactionByHash", "eth_getTransactionReceipt":
				params = []any{q.TxHash}
			case "eth_getLogs":
				params = []any{q.Filter}
			case "eth_getBlockByNumber":
				params = []any{ref.String(), q.FullTransactions}
			case "eth_blockNumber":
				params = []any{}
			}
		}
		result, err := s.deps.ChainRead.ReadRPC(ctx, q.Chain, method, params, investigationOptions())
		if err != nil {
			return nil, nil, err
		}
		var value any
		d := json.NewDecoder(strings.NewReader(string(result.Result)))
		d.UseNumber()
		if err = d.Decode(&value); err != nil {
			return nil, nil, fmt.Errorf("invalid RPC response")
		}
		meta := investigationMeta(nil)
		meta["cached"] = result.CacheHit
		return map[string]any{"chain": q.Chain, "method": method, "result": value, "provider": result.Provider, "blockTag": func() any {
			_, r, _ := chainread.ValidateInvestigationRPC(mustChain(s, q.Chain), method, params)
			return r.String()
		}()}, meta, nil
	}
	if op == "aggregate_transfers" {
		return s.investigationAggregate(ctx, q)
	}
	if op == "trace_funds" || op == "trace_fund_batch" {
		return s.investigationTrace(ctx, q)
	}
	if op == "get_labels" || op == "detect_exchange_deposit" || op == "trace_to_exchange" {
		return s.investigationLabelsDispatch(ctx, q, op)
	}
	if op == "get_balance_history" {
		return s.investigationHistory(ctx, q)
	}
	stateQuery := q
	if op == "get_all_token_balances" && q.Cursor != "" {
		saved, e := decodeExportSummaryCursor(q.Cursor, investigationFingerprint(q), "balances")
		if e != nil {
			return nil, nil, e
		}
		block, e := strconv.ParseUint(saved.After, 10, 64)
		if e != nil {
			return nil, nil, fmt.Errorf("invalid cursor block")
		}
		stateQuery.Timestamp = ""
		stateQuery.Block = nil
		stateQuery.BlockTag = strconv.FormatUint(block, 10)
	}
	point, err := s.statePoint(ctx, stateQuery)
	if err != nil {
		return nil, nil, err
	}
	meta := investigationMeta(&point)
	if op == "get_contract_abi" || op == "decode_transaction_input" || op == "decode_logs" || op == "resolve_proxy_implementation" || op == "read_contract" || op == "get_contract_control" {
		data, err := s.investigationContract(ctx, q, op, point)
		if op == "get_contract_control" || op == "resolve_proxy_implementation" || op == "decode_transaction_input" || op == "decode_logs" {
			meta["coverage"] = map[string]any{"complete": false, "scope": "bounded_contract_probes", "reason": "Contract evidence and decoded fields are reported individually; custom control/proxy logic may remain unresolved"}
		}
		return data, meta, err
	}
	if q.Address == "" {
		return nil, nil, fmt.Errorf("address is required")
	}
	holder, err := chain.ParseAddress(q.Address)
	if err != nil {
		return nil, nil, err
	}
	if op == "get_native_balance" || op == "get_token_balance" || op == "get_balance_at" {
		token := q.TokenContract
		if op == "get_native_balance" {
			token = ""
		}
		if op == "get_token_balance" && token == "" {
			return nil, nil, fmt.Errorf("token contract is required")
		}
		if op == "get_balance_at" && q.Timestamp == "" {
			return nil, nil, fmt.Errorf("timestamp is required")
		}
		if token == "" && q.Token != "" && !strings.EqualFold(q.Token, mustChain(s, q.Chain).Native.Symbol) {
			return nil, nil, fmt.Errorf("token balances require a contract address")
		}
		snapshot, e := s.deps.ChainRead.SnapshotBalance(ctx, q.Chain, holder, chain.Address(token), point, investigationOptions())
		return snapshot, meta, e
	}
	if op == "get_all_token_balances" {
		if q.PageSize == 0 {
			q.PageSize = 20
		}
		if q.PageSize < 1 || q.PageSize > 100 {
			return nil, nil, fmt.Errorf("page_size must be 1-100")
		}
		fingerprint := investigationFingerprint(q)
		cur, err := decodeExportSummaryCursor(q.Cursor, fingerprint, "balances")
		if err != nil {
			return nil, nil, err
		}
		if q.Cursor != "" {
			point.Current = false
			meta = investigationMeta(&point)
		}
		ingestedBefore := time.Now().UTC()
		if q.Cursor != "" && len(q.Tokens) == 0 {
			if cur.IngestedBefore == "" {
				return nil, nil, summaryError(400, "INVALID_CURSOR", "Old discovery cursor has no ingestion snapshot; restart pagination")
			}
			ingestedBefore, err = time.Parse(time.RFC3339Nano, cur.IngestedBefore)
			if err != nil {
				return nil, nil, fmt.Errorf("invalid discovery cursor watermark")
			}
		}

		tokens := q.Tokens
		more := false
		if len(tokens) > 0 {
			if len(tokens) > 1000 {
				return nil, nil, fmt.Errorf("at most 1000 explicit tokens")
			}
			offset := int(cur.Offset)
			if offset > len(tokens) {
				return nil, nil, fmt.Errorf("cursor exceeds token list")
			}
			end := offset + q.PageSize
			if end > len(tokens) {
				end = len(tokens)
			}
			more = end < len(tokens)
			tokens = tokens[offset:end]
		} else {
			tokens, more, err = s.query.InvestigationTokensPage(ctx, q.Chain, q.Address, point.Block, ingestedBefore, cur.TokenAfter, q.PageSize)
			if err != nil {
				return nil, nil, err
			}
		}
		items := make([]any, len(tokens))
		jobs, gctx := errgroup.WithContext(ctx)
		jobs.SetLimit(4)
		for i, t := range tokens {
			i, t := i, t
			token, e := chain.ParseAddress(t)
			if e != nil {
				return nil, nil, fmt.Errorf("invalid explicit token address")
			}
			jobs.Go(func() error {
				value, e := s.deps.ChainRead.SnapshotBalance(gctx, q.Chain, holder, token, point, investigationOptions())
				if e != nil {
					items[i] = map[string]any{"token_contract": t, "error": rpc.AsError(e).Public(), "balance": nil, "balance_source": "unavailable"}
				} else {
					items[i] = value
				}
				return nil
			})
		}
		if e := jobs.Wait(); e != nil {
			return nil, nil, e
		}

		native, e := s.deps.ChainRead.SnapshotBalance(ctx, q.Chain, holder, "", point, investigationOptions())
		if e != nil {
			return nil, nil, e
		}
		var next any
		if more {
			next = encodeExportSummaryCursor(exportSummaryCursor{Version: 1, Query: fingerprint, Kind: "balances", After: strconv.FormatUint(point.Block, 10), Offset: cur.Offset + int64(len(tokens)), IngestedBefore: ingestedBefore.Format(time.RFC3339Nano), TokenAfter: tokens[len(tokens)-1]})
		}
		meta["coverage"] = map[string]any{"complete": false, "reason": "Token discovery uses indexed transfers or the explicit caller token list; tokens without observed activity may be absent"}
		return map[string]any{"chain": q.Chain, "address": q.Address, "native_balance": native, "items": items, "as_of_block": point.Block, "balance_source": "rpc_state", "is_current_balance": point.Current, "discovery_source": func() string {
			if len(q.Tokens) > 0 {
				return "caller_token_list"
			}
			return "indexed_transfers"
		}(), "discovery_complete": false, "discovery_ingested_before": ingestedBefore, "has_more": more, "next_cursor": next}, meta, nil
	}
	return nil, nil, fmt.Errorf("unsupported operation")
}
func mustChain(s *Server, c string) chain.Config {
	cfg, _ := s.deps.ChainRead.Chains().Get(c)
	return cfg
}
func investigationFingerprint(q investigationRequest) string {
	q.Cursor = ""
	q.Page = 0
	q.PageSize = 0
	raw, _ := json.Marshal(q)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func parseInvestigationTime(value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	v, e := time.Parse(time.RFC3339, value)
	if e != nil {
		return nil, fmt.Errorf("time must be ISO8601")
	}
	return &v, nil
}
func (s *Server) investigationAggregate(ctx context.Context, q investigationRequest) (any, map[string]any, error) {
	if q.Token == "" && q.TokenContract == "" {
		return nil, nil, fmt.Errorf("token or token_contract is required; assets must not be combined")
	}
	if q.Address == "" {
		return nil, nil, fmt.Errorf("address is required")
	}
	if q.Direction == "all" {
		q.Direction = ""
	}
	if q.Direction != "" && q.Direction != "in" && q.Direction != "out" {
		return nil, nil, fmt.Errorf("invalid direction")
	}
	if len(q.GroupBy) == 0 {
		q.GroupBy = []string{"counterparty"}
	}
	if q.OrderBy == "" {
		q.OrderBy = "sum_amount"
	}
	for _, m := range q.Metrics {
		if m != "sum_amount" && m != "tx_count" && m != "record_count" && m != "first_time" && m != "last_time" {
			return nil, nil, fmt.Errorf("unsupported metric")
		}
	}
	if q.PageSize == 0 {
		q.PageSize = 100
	}
	if q.Page == 0 {
		q.Page = 1
	}
	if q.Page < 1 {
		return nil, nil, fmt.Errorf("invalid page")
	}
	start, err := parseInvestigationTime(q.StartTime)
	if err != nil {
		return nil, nil, err
	}
	end, err := parseInvestigationTime(q.EndTime)
	if err != nil {
		return nil, nil, err
	}
	if start != nil && end != nil && end.Before(*start) {
		return nil, nil, fmt.Errorf("end_time precedes start_time")
	}
	if q.Counterparty != "" {
		q.Counterparty, err = addressnorm.Normalize(q.Chain, q.Counterparty)
		if err != nil {
			return nil, nil, err
		}
	}
	id := investigationFingerprint(q)
	cur, err := decodeExportSummaryCursor(q.Cursor, id, "aggregate")
	if err != nil {
		return nil, nil, err
	}
	var snap struct {
		Block uint64
		Time  time.Time
	}
	if q.Cursor == "" {
		point, e := s.statePoint(ctx, q)
		if e != nil {
			return nil, nil, e
		}
		snap.Block = point.Block
		snap.Time = time.Now().UTC()
		cur.Offset = int64(q.Page-1) * int64(q.PageSize)
	} else {
		if err = json.Unmarshal([]byte(cur.After), &snap); err != nil {
			return nil, nil, fmt.Errorf("invalid snapshot cursor")
		}
	}
	result, err := s.query.InvestigationAggregate(ctx, query.AggregateRequest{ActionFilter: query.ActionFilter{Chain: q.Chain, Address: q.Address, Token: q.Token, TokenContract: q.TokenContract, Direction: q.Direction, Counterparty: q.Counterparty, StartTime: start, EndTime: end, PageSize: q.PageSize}, GroupBy: q.GroupBy, OrderBy: q.OrderBy, Offset: cur.Offset, Cutoff: snap.Block, IngestedBefore: snap.Time})
	if err != nil {
		return nil, nil, err
	}
	for _, item := range result["items"].([]map[string]any) {
		var values []any
		if json.Unmarshal([]byte(item["group_key"].(string)), &values) == nil {
			for i, key := range q.GroupBy {
				if i < len(values) {
					item[key] = values[i]
				}
			}
		}
	}
	var next any
	if result["has_more"] == true {
		raw, _ := json.Marshal(snap)
		next = encodeExportSummaryCursor(exportSummaryCursor{Version: 1, Query: id, Kind: "aggregate", After: string(raw), Offset: cur.Offset + int64(q.PageSize)})
	}
	result["next_cursor"] = next
	result["as_of_ingestion_time"] = snap.Time
	meta := s.actionCoverageMeta(ctx, q.Chain, []string{q.Address}, []string{"token_transfers"})
	if coverage, ok := meta["coverage"].(map[string]any); ok {
		coverage["snapshot_upper_bound"] = snap.Block
		coverage["complete"] = false
		coverage["reason"] = "Totals include every matching indexed record at the block and ingestion cutoffs; indexed coverage is not proof of complete chain history through the current head"
	}
	return result, meta, nil
}
func (s *Server) investigationHistory(ctx context.Context, q investigationRequest) (any, map[string]any, error) {
	if q.StartTime == "" {
		q.StartTime = q.Start
	}
	if q.EndTime == "" {
		q.EndTime = q.End
	}
	start, e := parseInvestigationTime(q.StartTime)
	if e != nil || start == nil {
		return nil, nil, fmt.Errorf("start is required ISO8601")
	}
	end, e := parseInvestigationTime(q.EndTime)
	if e != nil || end == nil || end.Before(*start) {
		return nil, nil, fmt.Errorf("invalid end")
	}
	if q.IntervalSeconds == 0 {
		q.IntervalSeconds = 86400
	}
	if q.IntervalSeconds < 60 {
		return nil, nil, fmt.Errorf("interval_seconds must be at least 60")
	}
	if q.PageSize == 0 {
		q.PageSize = 8
	}
	if q.PageSize < 1 || q.PageSize > 32 {
		return nil, nil, fmt.Errorf("page_size must be 1-32")
	}
	id := investigationFingerprint(q)
	cur, e := decodeExportSummaryCursor(q.Cursor, id, "history")
	if e != nil {
		return nil, nil, e
	}
	if q.Cursor == "" && q.Page > 1 {
		cur.Offset = int64(q.Page-1) * int64(q.PageSize)
	}
	total := int64(end.Sub(*start)/time.Second)/q.IntervalSeconds + 1
	if q.TokenContract == "" && q.Token != "" && !strings.EqualFold(q.Token, mustChain(s, q.Chain).Native.Symbol) {
		return nil, nil, fmt.Errorf("token history requires an exact token contract")
	}
	if cur.Offset > total {
		return nil, nil, fmt.Errorf("cursor exceeds sample range")
	}
	count := min(int64(q.PageSize), total-cur.Offset)
	items := make([]any, int(count))
	holder, e := chain.ParseAddress(q.Address)
	if e != nil {
		return nil, nil, e
	}
	jobs, gctx := errgroup.WithContext(ctx)
	jobs.SetLimit(4)
	for k := int64(0); k < count; k++ {
		k := k
		jobs.Go(func() error {
			qq := q
			qq.Timestamp = start.Add(time.Duration((cur.Offset+k)*q.IntervalSeconds) * time.Second).Format(time.RFC3339)
			qq.Block = nil
			qq.BlockTag = ""
			p, e := s.statePoint(gctx, qq)
			if e != nil {
				return e
			}
			snapshot, e := s.deps.ChainRead.SnapshotBalance(gctx, q.Chain, holder, chain.Address(q.TokenContract), p, investigationOptions())
			if e != nil {
				return e
			}
			items[k] = snapshot
			return nil
		})
	}
	if e = jobs.Wait(); e != nil {
		return nil, nil, e
	}

	more := cur.Offset+int64(len(items)) < total
	var next any
	if more {
		next = encodeExportSummaryCursor(exportSummaryCursor{Version: 1, Query: id, Kind: "history", Offset: cur.Offset + int64(len(items))})
	}
	return map[string]any{"items": items, "total_samples": total, "interval_seconds": q.IntervalSeconds, "has_more": more, "next_cursor": next, "balance_source": "rpc_state", "history_type": "sampled_state_snapshots"}, investigationMeta(nil), nil
}

func investigationReadScope(path string) bool {
	if !strings.HasPrefix(path, "/api/v1/analysis/") {
		return false
	}
	name := strings.TrimPrefix(path, "/api/v1/analysis/")
	switch name {
	case "get_investigation_capabilities", "get_analysis_status", "list_watch_rules", "get_watch_events":
		return true
	}
	switch name {
	case "aggregate_transfers", "trace_funds", "trace_fund_batch", "detect_exchange_deposit", "trace_to_exchange":
		return false
	}
	for _, op := range investigationOperations {
		if op == name {
			return true
		}
	}
	return false
}

func decorateInvestigation(value any, c string) {
	switch v := value.(type) {
	case map[string]any:
		if contract, ok := v["token_contract"].(string); ok && contract != "" {
			symbol, _ := v["token_symbol"].(string)
			for k, val := range assetidentity.Describe(c, contract, symbol) {
				v[k] = val
			}
		}
		for _, child := range v {
			decorateInvestigation(child, c)
		}
	case []any:
		for _, child := range v {
			decorateInvestigation(child, c)
		}
	}
}
