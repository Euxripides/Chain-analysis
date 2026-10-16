package api

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
)

// Geth's ordinary prestate tracer omits accounts that were empty at the actual
// transaction position. includeEmpty keeps those observations without replacing
// them with the state of the preceding block or an invented zero account.
func contractReadTrace(ctx context.Context, call func(context.Context, rpc.Request) (rpc.Response, error), chain, txHash, tracer string, diff bool, budget rpc.BudgetContext) (json.RawMessage, map[string]any, error) {
	include := tracer == "prestateTracer" && !diff
	makeRequest := func(keepEmpty bool) rpc.Request {
		config := map[string]any{"tracer": tracer, "timeout": "30s"}
		if tracer == "prestateTracer" {
			options := map[string]any{"diffMode": diff}
			if keepEmpty {
				options["includeEmpty"] = true
			}
			config["tracerConfig"] = options
		}
		return rpc.Request{Chain: chain, Method: "debug_traceTransaction", Params: []any{txHash, config}, Class: rpc.ClassDebugTrace, Priority: rpc.PriorityBackground, Budget: budget}
	}
	reads := []any{}
	observe := func(response rpc.Response, err error, sent bool) {
		item := map[string]any{"provider": response.Provider, "cache_hit": response.CacheHit, "attempts": response.Attempts, "bytes": response.Bytes, "include_empty_option_sent": sent}
		if err != nil {
			item["error_class"] = rpc.AsError(err).Class
		}
		reads = append(reads, item)
	}
	response, err := call(ctx, makeRequest(include))
	observe(response, err, include)
	attempts, size, fallback := response.Attempts, response.Bytes, false
	if include && rpc.IsClass(err, rpc.ClassInvalidParams) {
		// One compatibility retry shares the same Gateway budget. Other failures
		// never trigger a trace retry; missing accounts remain explicit gaps.
		fallback = true
		response, err = call(ctx, makeRequest(false))
		observe(response, err, false)
		attempts += response.Attempts
		size += response.Bytes
	}
	meta := map[string]any{"tracer": tracer, "diff_mode": diff, "provider": response.Provider, "cache_hit": response.CacheHit, "attempts": attempts, "gateway_calls": len(reads), "bytes": size, "include_empty_requested": include, "include_empty_compatibility_fallback": fallback, "reads": reads}
	if err != nil {
		return nil, meta, err
	}
	if len(response.Result) > ca.ArtifactLimit {
		return nil, meta, fmt.Errorf("TRACE_RESPONSE_TOO_LARGE")
	}
	return response.Result, meta, nil
}

// This checks only observed creation account coverage. Full replay separately
// validates balances, nonce, code, storage, gas, block environment and call tree.
func contractCreationPrestateCoverage(trace, prestate []byte) map[string]any {
	row := map[string]any{"dimension": "transaction_prestate_creation_accounts", "complete": false, "scope": "presence_of_each_traced_creation_target_at_transaction_position"}
	var root map[string]any
	var accounts map[string]json.RawMessage
	if json.Unmarshal(trace, &root) != nil || root == nil {
		row["reason"] = "CREATION_PRESTATE_CALL_TRACE_UNAVAILABLE"
		return row
	}
	if json.Unmarshal(prestate, &accounts) != nil || accounts == nil {
		row["reason"] = "CREATION_PRESTATE_ACCOUNTS_UNAVAILABLE"
		return row
	}
	frames, err := contractCreationFrames(root)
	if err != nil {
		row["reason"] = err.Error()
		return row
	}
	known := map[string]bool{}
	for address, raw := range accounts {
		var account ca.StateAccount
		if json.Unmarshal(raw, &account) != nil {
			continue
		}
		balance, err := contractCreationInteger(account.Balance)
		if err != nil || balance.Sign() < 0 {
			continue
		}
		known[strings.ToLower(address)] = true
	}
	missing, invalid := []string{}, []string{}
	targets := map[string]bool{}
	for _, frame := range frames {
		address := strings.ToLower(contractCreationText(frame.frame["to"]))
		if !ca.CompleteAddress.MatchString(address) {
			invalid = append(invalid, frame.path)
			continue
		}
		targets[address] = true
	}
	for address := range targets {
		if !known[address] {
			missing = append(missing, address)
		}
	}
	sort.Strings(missing)
	row["creation_frames_checked"], row["unique_targets_checked"], row["missing_addresses"], row["unresolved_frame_paths"] = len(frames), len(targets), missing, invalid
	row["complete"] = len(missing) == 0 && len(invalid) == 0
	if row["complete"] != true {
		row["reason"] = "CREATION_TRANSACTION_PRESTATE_INCOMPLETE"
	}
	return row
}
