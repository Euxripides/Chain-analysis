package api

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
	"github.com/blockchain-platform/blockchain-platform/internal/method"
	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
	"golang.org/x/crypto/sha3"
)

func contractOptions(ctx context.Context, q ca.Request) chainread.Options {
	opt := investigationOptions()
	if w, ok := ctx.Value(nextWorkerKey{}).(*nextWorkerState); ok {
		opt.Budget = rpc.BudgetContext{OrganizationID: w.Scope.OrganizationID, WorkspaceID: w.Scope.WorkspaceID, JobID: w.JobID, Purpose: rpc.PurposeContractAnalysis, MaxRequests: q.Budget.MaxRPCRequests, MaxCredits: q.Budget.MaxCredits, Priority: rpc.PriorityBackground}
		opt.Priority = rpc.PriorityBackground
	}
	return opt
}
func contractKeccak(code []byte) string {
	h := sha3.NewLegacyKeccak256()
	h.Write(code)
	return "0x" + hex.EncodeToString(h.Sum(nil))
}
func (s *Server) contractBlockGuard(ctx context.Context, q ca.Request, point chainread.StatePoint, final ...bool) error {
	if len(final) > 0 && final[0] {
		ctx = context.WithValue(ctx, contractFinalGuardKey{}, true)
	}
	opt := contractOptions(ctx, q)
	opt.Consistency = rpc.ConsistencyForceRefresh
	b, e := s.deps.ChainRead.Block(ctx, q.Chain, chain.AtBlock(point.Block), false, opt)
	if e != nil {
		return e
	}
	if b == nil || !strings.EqualFold(b.Hash, point.Hash) {
		return summaryError(409, "SNAPSHOT_REORG", "Snapshot block hash changed; start a new analysis")
	}
	return nil
}
func (s *Server) contractCodeInput(ctx context.Context, q ca.Request, p chainread.StatePoint, address, storage, entry, engine string) (ca.Input, error) {
	code, e := s.deps.ChainRead.Code(ctx, q.Chain, chain.Address(address), chain.AtBlock(p.Block), contractOptions(ctx, q))
	if e != nil {
		return ca.Input{}, e
	}
	if len(code) == 0 {
		return ca.Input{}, summaryError(422, "NO_CONTRACT_CODE", "No runtime contract code at the selected block")
	}
	if len(code) > 49152 {
		return ca.Input{}, summaryError(422, "CODE_BUDGET_EXCEEDED", "Runtime bytecode exceeds engine input budget")
	}
	snap := ca.Snapshot{Chain: q.Chain, ChainID: fmt.Sprint(mustChain(s, q.Chain).ChainID), BlockNumber: p.Block, BlockHash: p.Hash, BlockTime: p.Time, EntryAddress: entry, CodeAddress: address, StorageAddress: storage, CodeHash: contractKeccak(code), CodeSHA256: ca.SHA256(code), EVMRules: "opcode_model_shanghai_with_explicit_later_opcode_gaps", EngineDigest: engine, StateGranularity: "block_end", FetchedAt: time.Now().UTC()}
	return ca.Input{Snapshot: snap, Bytecode: "0x" + hex.EncodeToString(code), Engines: q.Engines}, nil
}
func (s *Server) contractSource(ctx context.Context, input ca.Input) (json.RawMessage, error) {
	u := fmt.Sprintf("https://sourcify.dev/server/v2/contract/%s/%s?fields=all", input.Snapshot.ChainID, input.Snapshot.CodeAddress)
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if e != nil {
		return nil, e
	}
	res, e := investigationABIHTTP.Do(req)
	if e != nil {
		return nil, fmt.Errorf("SOURCE_FETCH_UNAVAILABLE")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("SOURCE_HTTP_%d", res.StatusCode)
	}
	raw, e := io.ReadAll(io.LimitReader(res.Body, (16<<20)+1))
	if e != nil || len(raw) > 16<<20 {
		return nil, fmt.Errorf("SOURCE_RESPONSE_TOO_LARGE")
	}
	var doc struct {
		ChainID      string `json:"chainId"`
		Address      string `json:"address"`
		Match        string `json:"match"`
		RuntimeMatch string `json:"runtimeMatch"`
		Runtime      struct {
			Onchain string `json:"onchainBytecode"`
		} `json:"runtimeBytecode"`
	}
	if e = json.Unmarshal(raw, &doc); e != nil {
		return nil, fmt.Errorf("SOURCE_RESPONSE_INVALID")
	}
	if doc.ChainID != input.Snapshot.ChainID || !strings.EqualFold(doc.Address, input.Snapshot.CodeAddress) {
		return nil, fmt.Errorf("SOURCE_IDENTITY_MISMATCH")
	}
	// Preserve a mismatching public verification as a conflict, but the engine
	// must refuse its AST/layout before exposing any verified semantics.
	return raw, nil
}
func (s *Server) contractSaveObject(ctx context.Context, kind string, raw []byte, media string) (ca.Object, error) {
	w, ok := ctx.Value(nextWorkerKey{}).(*nextWorkerState)
	if !ok || w.JobID == "" {
		return ca.Object{}, fmt.Errorf("contract worker context required")
	}
	digest := ca.SHA256(raw)
	key := fmt.Sprintf("contract-analysis/%s/%s/%s/%s-%s", w.Scope.OrganizationID, w.Scope.WorkspaceID, w.JobID, kind, digest)
	n, e := s.deps.Raw.Put(ctx, key, raw, media)
	if e == nil && n != int64(len(raw)) {
		e = fmt.Errorf("object write length mismatch")
	}
	return ca.Object{Key: key, SHA256: digest, Bytes: n}, e
}
func (s *Server) contractMaterialize(ctx context.Context, q ca.Request, boundary []byte) (ca.FrozenInput, error) {
	var frozen ca.FrozenInput
	reused, e := s.nextFrozen(ctx, &frozen)
	if e != nil {
		return frozen, e
	}
	if reused {
		return frozen, nil
	}
	var b struct {
		Chain  string               `json:"chain"`
		Point  chainread.StatePoint `json:"point"`
		Engine string               `json:"engine_digest"`
	}
	if e = nextDecode(boundary, &b); e != nil {
		return frozen, e
	}
	b.Point.Ref = chain.AtBlock(b.Point.Block)
	if e = s.contractBlockGuard(ctx, q, b.Point); e != nil {
		return frozen, e
	}
	frozen = ca.FrozenInput{Version: ca.Version, Request: q, SourceObjects: map[string]ca.Object{}, Contracts: []ca.Input{}, Frontier: []map[string]any{}, Transactions: []json.RawMessage{}, Coverage: []map[string]any{}}
	root, e := s.contractCodeInput(ctx, q, b.Point, q.Address, q.Address, q.Address, b.Engine)
	if e != nil {
		return frozen, e
	}
	frozen.Snapshot = root.Snapshot
	// Follow standard delegation identities while retaining each entry code and
	// the original storage context. Cycles and unknown routing remain explicit.
	seen := map[string]bool{}
	input := root
	for len(frozen.Contracts) < q.Budget.MaxContracts {
		key := input.Snapshot.CodeAddress
		if seen[key] {
			frozen.Frontier = append(frozen.Frontier, map[string]any{"kind": "proxy_cycle", "address": key})
			break
		}
		seen[key] = true
		proxy, resolveErrors := s.contractResolveProxy(ctx, q, b.Point, input)
		input.Snapshot.Proxy = proxy
		for _, reason := range resolveErrors {
			frozen.Coverage = append(frozen.Coverage, map[string]any{"dimension": "proxy", "address": key, "complete": false, "reason": reason})
		}
		if q.IncludeSource != nil && *q.IncludeSource {
			source, err := s.contractSource(ctx, input)
			if err == nil {
				object, err := s.contractSaveObject(ctx, "source", source, "application/json")
				if err != nil {
					return frozen, err
				}
				frozen.SourceObjects[input.Snapshot.CodeAddress+":"+input.Snapshot.CodeHash] = object
			} else {
				frozen.Coverage = append(frozen.Coverage, map[string]any{"dimension": "source", "address": key, "complete": false, "reason": err.Error()})
			}
		}
		frozen.Contracts = append(frozen.Contracts, input)
		impl, _ := proxy["implementation"].(string)
		if impl == "" {
			break
		}
		if len(frozen.Contracts) >= q.Budget.MaxContracts {
			frozen.Frontier = append(frozen.Frontier, map[string]any{"kind": "proxy_implementation", "address": impl, "storage_address": q.Address, "reason": "max_contracts"})
			break
		}
		input, e = s.contractCodeInput(ctx, q, b.Point, impl, q.Address, q.Address, b.Engine)
		if e != nil {
			frozen.Frontier = append(frozen.Frontier, map[string]any{"kind": "proxy_implementation", "address": impl, "reason": "code_unavailable"})
			break
		}
	}
	for _, txHash := range q.TxHashes {
		record, err := s.contractTransactionInput(ctx, q, txHash)
		if err != nil {
			frozen.Coverage = append(frozen.Coverage, map[string]any{"dimension": "transaction", "tx_hash": txHash, "complete": false, "reason": err.Error()})
			continue
		}
		object, err := s.contractSaveObject(ctx, "transaction", ca.Row(record), "application/json")
		if err != nil {
			return frozen, err
		}
		frozen.Transactions = append(frozen.Transactions, ca.Row(map[string]any{"tx_hash": txHash, "object": object}))
	}
	if len(frozen.Contracts) > 0 {
		frozen.Snapshot = frozen.Contracts[0].Snapshot
	}
	if e = s.nextBlockGuard(ctx, q.Chain, b.Point); e != nil {
		return frozen, e
	}
	if e = s.nextSaveFrozen(ctx, frozen); e != nil {
		return frozen, e
	}
	return frozen, nil
}
func (s *Server) contractResolveProxy(ctx context.Context, q ca.Request, p chainread.StatePoint, input ca.Input) (map[string]any, []string) {
	proxy := map[string]any{"resolution": "standard_probes", "evidence": []any{}}
	errors := []string{}
	code, _ := chain.HexToBytes(input.Bytecode)
	if len(code) == 45 && hex.EncodeToString(code[:10]) == "363d3d373d3d3d363d73" && hex.EncodeToString(code[30:]) == "5af43d82803e903d91602b57fd5bf3" {
		proxy["implementation"] = "0x" + hex.EncodeToString(code[10:30])
		proxy["proxy_type"] = "eip1167"
		proxy["evidence"] = []any{map[string]any{"kind": "bytecode", "pc": 10, "code_hash": input.Snapshot.CodeHash}}
		return proxy, errors
	}
	if len(code) == 23 && hex.EncodeToString(code[:3]) == "ef0100" {
		proxy["implementation"] = "0x" + hex.EncodeToString(code[3:])
		proxy["proxy_type"] = "eip7702"
		proxy["evidence"] = []any{map[string]any{"kind": "bytecode", "pc": 3, "code_hash": input.Snapshot.CodeHash}}
		return proxy, errors
	}
	hasDelegate := false
	for pc := 0; pc < len(code); pc++ {
		op := code[pc]
		if op == 0xf4 {
			hasDelegate = true
		}
		if op >= 0x60 && op <= 0x7f {
			pc += int(op - 0x5f)
		}
	}
	if !hasDelegate {
		proxy["resolution"] = "no_delegate_opcode"
		return proxy, errors
	}
	evidence := []any{}
	for _, probe := range []struct{ name, slot string }{{"implementation", implementationSlot}, {"admin", adminSlot}, {"beacon", beaconSlot}} {
		value, e := s.deps.ChainRead.StorageAt(ctx, q.Chain, chain.Address(input.Snapshot.StorageAddress), probe.slot, chain.AtBlock(p.Block), contractOptions(ctx, q))
		if e != nil {
			errors = append(errors, "proxy_storage_unavailable:"+probe.name)
			continue
		}
		address := nonzeroAddress(value)
		if address != "" {
			proxy[probe.name] = address
			evidence = append(evidence, map[string]any{"slot": probe.slot, "value": "0x" + hex.EncodeToString(value), "address": input.Snapshot.StorageAddress, "block_number": p.Block, "block_hash": p.Hash})
		}
	}
	if proxy["implementation"] != nil {
		proxy["proxy_type"] = "eip1967_candidate"
	}
	if beacon, ok := proxy["beacon"].(string); ok {
		data, _ := chain.HexToBytes(method.SelectorOf("implementation()"))
		raw, e := s.deps.ChainRead.CallContract(ctx, chainread.ContractCall{Chain: q.Chain, To: chain.Address(beacon), Data: data, BlockRef: chain.AtBlock(p.Block)}, contractOptions(ctx, q))
		if e == nil {
			if a := nonzeroAddress(raw); a != "" {
				proxy["implementation"] = a
				proxy["proxy_type"] = "eip1967_beacon"
				evidence = append(evidence, map[string]any{"method": "implementation()", "address": beacon, "raw": "0x" + hex.EncodeToString(raw), "block_number": p.Block})
			}
		} else {
			errors = append(errors, "beacon_implementation_unavailable")
		}
	}
	proxy["evidence"] = evidence
	if proxy["implementation"] == nil && proxy["beacon"] == nil {
		facets, e := s.contractDiamond(ctx, q, p, input.Snapshot.StorageAddress)
		if e == nil && len(facets) > 0 {
			proxy["facets"] = facets
			proxy["proxy_type"] = "eip2535_loupe_candidates"
		}
	}
	if impl, ok := proxy["implementation"].(string); ok && strings.EqualFold(impl, input.Snapshot.CodeAddress) {
		proxy["active_implementation"] = impl
		delete(proxy, "implementation")
	}
	if proxy["implementation"] == nil && bytes.Contains(code, []byte{0xf4}) {
		proxy["custom_delegate_resolution"] = "requires_call_trace_or_facet_evidence"
	}
	return proxy, errors
}
func (s *Server) contractRPCTrace(ctx context.Context, q ca.Request, txHash, tracer string, diff bool) (json.RawMessage, map[string]any, error) {
	opt := contractOptions(ctx, q)
	config := map[string]any{"tracer": tracer, "timeout": "30s"}
	if tracer == "prestateTracer" {
		config["tracerConfig"] = map[string]any{"diffMode": diff}
	}
	response, e := s.deps.RPCGateway.Call(ctx, rpc.Request{Chain: q.Chain, Method: "debug_traceTransaction", Params: []any{txHash, config}, Class: rpc.ClassDebugTrace, Priority: rpc.PriorityBackground, Budget: opt.Budget})
	meta := map[string]any{"tracer": tracer, "diff_mode": diff, "provider": response.Provider, "cache_hit": response.CacheHit, "attempts": response.Attempts, "bytes": response.Bytes}
	if e != nil {
		return nil, meta, e
	}
	if len(response.Result) > ca.ArtifactLimit {
		return nil, meta, fmt.Errorf("TRACE_RESPONSE_TOO_LARGE")
	}
	return response.Result, meta, nil
}
func (s *Server) contractTransactionInput(ctx context.Context, q ca.Request, txHash string) (map[string]any, error) {
	h, _ := chain.ParseHash(txHash)
	opt := contractOptions(ctx, q)
	tx, e := s.deps.ChainRead.Transaction(ctx, q.Chain, h, opt)
	if e != nil {
		return nil, e
	}
	receipt, e := s.deps.ChainRead.Receipt(ctx, q.Chain, h, opt)
	if e != nil {
		return nil, e
	}
	if tx == nil || receipt == nil || tx.BlockNumber == nil || receipt.BlockNumber == nil || tx.BlockHash == "" || tx.BlockHash != receipt.BlockHash || *tx.BlockNumber != *receipt.BlockNumber {
		return nil, fmt.Errorf("TRANSACTION_RECEIPT_IDENTITY_MISMATCH")
	}
	var snapshotBlock uint64
	if json.Unmarshal(q.Block, &snapshotBlock) == nil && *tx.BlockNumber > snapshotBlock {
		return nil, fmt.Errorf("SELECTED_TRANSACTION_AFTER_SNAPSHOT")
	}
	record := map[string]any{"tx_hash": txHash, "transaction": tx, "receipt": receipt, "coverage": []any{}, "provider_reads": []any{}}
	coverage := []any{}
	reads := []any{}
	for _, probe := range []struct {
		key, tracer string
		diff        bool
	}{{"call_trace", "callTracer", false}, {"prestate", "prestateTracer", false}, {"state_diff", "prestateTracer", true}} {
		raw, meta, err := s.contractRPCTrace(ctx, q, txHash, probe.tracer, probe.diff)
		reads = append(reads, meta)
		if err != nil {
			coverage = append(coverage, map[string]any{"dimension": probe.key, "complete": false, "reason": rpc.AsError(err).Public()})
		} else {
			record[probe.key] = raw
			coverage = append(coverage, map[string]any{"dimension": probe.key, "complete": true, "basis": "transaction_tracer"})
		}
	}
	record["coverage"] = coverage
	record["provider_reads"] = reads
	price, ok := new(big.Int).SetString(receipt.EffectiveGasPrice, 10)
	if !ok && strings.HasPrefix(receipt.EffectiveGasPrice, "0x") {
		price, ok = new(big.Int).SetString(receipt.EffectiveGasPrice[2:], 16)
	}
	if ok {
		record["network_fee_raw"] = new(big.Int).Mul(price, new(big.Int).SetUint64(receipt.GasUsed)).String()
	}
	block, e := s.deps.ChainRead.Block(ctx, q.Chain, chain.AtBlock(*tx.BlockNumber), false, opt)
	if e != nil || block == nil || block.Hash != tx.BlockHash {
		return nil, fmt.Errorf("TRANSACTION_BLOCK_REORG")
	}
	record["block"] = block
	if *tx.BlockNumber == 0 {
		return nil, fmt.Errorf("GENESIS_TRANSACTION_REPLAY_UNSUPPORTED")
	}
	for _, request := range []struct {
		key, method string
		params      []any
		class       rpc.MethodClass
	}{{"raw_transaction", "eth_getTransactionByHash", []any{txHash}, rpc.ClassTransaction}, {"raw_receipt", "eth_getTransactionReceipt", []any{txHash}, rpc.ClassReceipt}, {"raw_block", "eth_getBlockByNumber", []any{chain.AtBlock(*tx.BlockNumber).String(), false}, rpc.ClassBlock}, {"parent_block", "eth_getBlockByNumber", []any{chain.AtBlock(*tx.BlockNumber - 1).String(), false}, rpc.ClassBlock}} {
		response, err := s.deps.RPCGateway.Call(ctx, rpc.Request{Chain: q.Chain, Method: request.method, Params: request.params, Class: request.class, Budget: opt.Budget, Priority: rpc.PriorityBackground})
		if err == nil && len(response.Result) < 2<<20 {
			record[request.key] = response.Result
		} else {
			record["replay_witness_status"] = "raw_header_or_transaction_unavailable"
		}
	}
	if rawReceipt, ok := record["raw_receipt"].(json.RawMessage); ok {
		var r map[string]json.RawMessage
		if json.Unmarshal(rawReceipt, &r) == nil {
			components := map[string]any{"execution_gas_fee_raw": record["network_fee_raw"], "l1_data_fee_raw": nil, "blob_fee_raw": nil, "unit": "wei", "total_complete": false}
			for _, field := range []string{"l1Fee", "blobGasUsed", "blobGasPrice"} {
				if raw, exists := r[field]; exists {
					var value string
					if json.Unmarshal(raw, &value) == nil {
						if raw, err := ca.RawInteger(value); err == nil {
							components[field] = raw
						}
					}
				}
			}
			if fee, ok := components["l1Fee"].(string); ok {
				components["l1_data_fee_raw"] = fee
			}
			if gas, ok := components["blobGasUsed"].(string); ok {
				if price, ok := components["blobGasPrice"].(string); ok {
					g, _ := new(big.Int).SetString(gas, 10)
					p, _ := new(big.Int).SetString(price, 10)
					components["blob_fee_raw"] = new(big.Int).Mul(g, p).String()
				}
			}
			// On Ethereum, these are the complete receipt-level fee components.
			// L2 adapters must identify their chain-specific fee rules first.
			if inputFee, ok := record["network_fee_raw"].(string); ok && mustChain(s, q.Chain).ChainID == 1 {
				total, _ := new(big.Int).SetString(inputFee, 10)
				if blob, ok := components["blob_fee_raw"].(string); ok {
					n, _ := new(big.Int).SetString(blob, 10)
					total.Add(total, n)
				}
				components["total_fee_raw"] = total.String()
				components["total_complete"] = components["blobGasUsed"] == nil || components["blob_fee_raw"] != nil
			}
			record["network_fee_components"] = components
		}
	}
	if len(ca.Row(record)) > 32<<20 {
		return nil, fmt.Errorf("TRANSACTION_EVIDENCE_EXCEEDS_32_MIB")
	}
	return record, nil
}
func (s *Server) contractReadState(ctx context.Context, q ca.Request, result *ca.Result) {
	if result.Snapshot.StateGranularity == "transaction_prestate" {
		result.Sections["coverage"] = append(result.Sections["coverage"], ca.Row(map[string]any{"dimension": "snapshot_storage", "complete": false, "reason": "transaction_state_available_in_prestate_artifact_end_of_block_reads_excluded"}))
		return
	}
	read := map[string]string{}
	remaining := q.Budget.MaxStateReads
	for i, row := range result.Sections["storage"] {
		var item map[string]any
		if nextDecode(row, &item) != nil {
			continue
		}
		if item["slot_role"] == "layout_base" {
			item["snapshot_read_status"] = "mapping_or_aggregate_keys_not_selected"
			result.Sections["storage"][i] = ca.Row(item)
			continue
		}
		slot, ok := item["slot"].(string)
		address, _ := item["storage_address"].(string)
		if !ok || slot == "" || address == "" {
			continue
		}
		n, ok := new(big.Int).SetString(slot, 10)
		if !ok || n.Sign() < 0 {
			continue
		}
		key := address + ":" + slot
		raw, present := read[key]
		if !present {
			if remaining <= 0 || s.contractReserveState(ctx) != nil {
				item["snapshot_read_status"] = "max_state_reads"
				result.Sections["storage"][i] = ca.Row(item)
				continue
			}
			remaining--
			b, e := s.deps.ChainRead.StorageAt(ctx, q.Chain, chain.Address(address), "0x"+n.Text(16), chain.AtBlock(result.Snapshot.BlockNumber), contractOptions(ctx, q))
			if e != nil {
				item["snapshot_read_status"] = "unavailable"
				result.Sections["storage"][i] = ca.Row(item)
				continue
			}
			raw = "0x" + hex.EncodeToString(b)
			read[key] = raw
		}
		item["snapshot_slot_hex"] = raw
		item["snapshot_block_number"] = result.Snapshot.BlockNumber
		item["snapshot_block_hash"] = result.Snapshot.BlockHash
		if item["slot_role"] == "layout_base" {
			item["snapshot_read_status"] = "layout_base_slot_only"
			result.Sections["storage"][i] = ca.Row(item)
			continue
		}
		typ, _ := item["type"].(string)
		offset := 0
		if number, ok := item["offset"].(json.Number); ok {
			offset, _ = strconv.Atoi(number.String())
		}
		if strings.HasPrefix(typ, "address") || strings.HasPrefix(typ, "contract ") {
			bytes, _ := chain.HexToBytes(raw)
			if len(bytes) == 32 && offset >= 0 && offset <= 12 {
				address := "0x" + hex.EncodeToString(bytes[12-offset:32-offset])
				item["snapshot_address"] = address
			}
		}
		if bytes, e := chain.HexToBytes(raw); e == nil {
			if value, e := ca.DecodePacked(bytes, offset, typ); e == nil {
				item["snapshot_value"] = value
				item["snapshot_value_type"] = typ
			}
		}
		// A base mapping slot is layout evidence; reading it does not enumerate keys.
		if strings.HasPrefix(typ, "mapping") || strings.Contains(typ, "[") {
			item["snapshot_read_status"] = "layout_base_slot_only"
		} else {
			item["snapshot_read_status"] = "slot_word"
		}
		result.Sections["storage"][i] = ca.Row(item)
	}
	result.Sections["coverage"] = append(result.Sections["coverage"], ca.Row(map[string]any{"dimension": "snapshot_storage", "complete": false, "read_slots": len(read), "budget_remaining": remaining, "basis": "declared_slots_only_mapping_keys_not_enumerated"}))
}
