package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
	"github.com/blockchain-platform/blockchain-platform/internal/chaintrace"
	"github.com/blockchain-platform/blockchain-platform/internal/method"
)

func (s *Server) nextLogs(ctx context.Context, q nextRequest, addresses []chain.Address, topics []any) ([]chainread.Log, error) {
	if q.FromBlock == nil || q.ToBlock == nil || *q.ToBlock < *q.FromBlock || *q.ToBlock-*q.FromBlock > 100000 {
		return nil, fmt.Errorf("bounded from_block/to_block required")
	}
	reader := chainread.NewLogsService(s.deps.ChainRead)
	out := []chainread.Log{}
	for from := *q.FromBlock; from <= *q.ToBlock; {
		to := min(from+1999, *q.ToBlock)
		rows, e := reader.Fetch(ctx, q.Chain, chainread.LogFilter{FromBlock: from, ToBlock: to, Addresses: addresses, Topics: topics}, investigationOptions())
		if e != nil {
			return nil, e
		}
		if len(out)+len(rows) > q.MaxRecords {
			return nil, summaryError(422, "LOG_BUDGET_EXCEEDED", "Event result exceeds max_records; split the block range")
		}
		for _, row := range rows {
			if row.Removed {
				return nil, summaryError(409, "SNAPSHOT_REORG", "Removed log encountered")
			}
			out = append(out, row)
		}
		if to == *q.ToBlock {
			break
		}
		from = to + 1
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].BlockNumber != out[j].BlockNumber {
			return out[i].BlockNumber < out[j].BlockNumber
		}
		return out[i].LogIndex < out[j].LogIndex
	})
	return out, nil
}

// nextAllowanceBatch uses the normal gateway's provider limits, routing and
// per-item errors, while pinning every call to the same state snapshot.
func (s *Server) nextAllowanceBatch(ctx context.Context, q nextRequest, spenders []string, point chainread.StatePoint) ([]chainread.CallResult, error) {
	if len(spenders) == 0 {
		return []chainread.CallResult{}, nil
	}
	if s.deps.ChainRead == nil {
		return nil, summaryError(503, "RPC_UNAVAILABLE", "State reader unavailable")
	}
	m, e := signatureMethod("allowance(address,address)", nil)
	if e != nil {
		return nil, e
	}
	calls := make([]chainread.ContractCall, len(spenders))
	for i, spender := range spenders {
		data, err := method.EncodeInput(m, []any{q.Address, spender})
		if err != nil {
			return nil, err
		}
		raw, err := chain.HexToBytes(data)
		if err != nil {
			return nil, err
		}
		calls[i] = chainread.ContractCall{Chain: q.Chain, To: chain.Address(q.TokenContract), Data: raw, BlockRef: point.Ref}
	}
	return s.deps.ChainRead.BatchCallContract(ctx, calls, spenders, investigationOptions())
}

func (s *Server) nextAllowances(ctx context.Context, q nextRequest) (any, error) {
	point, e := s.nextPoint(ctx, q)
	if e != nil {
		return nil, e
	}
	if q.ToBlock != nil && *q.ToBlock > point.Block {
		return nil, fmt.Errorf("discovery range ends after state snapshot")
	}
	token, _ := chain.ParseAddress(q.TokenContract)
	seen := map[string]bool{}
	spenders := []string{}
	for _, a := range q.Spenders {
		if !seen[a] {
			seen[a] = true
			spenders = append(spenders, a)
		}
	}
	evidence := []chainread.Log{}
	if q.Discover {
		evidence, e = s.nextLogs(ctx, q, []chain.Address{token}, []any{method.Topic0Of("Approval(address,address,uint256)"), "0x" + strings.Repeat("0", 24) + q.Address[2:]})
		if e != nil {
			return nil, e
		}
		for _, l := range evidence {
			if len(l.Topics) != 3 {
				continue
			}
			a, e := strictTopicAddress(l.Topics[2])
			if e != nil {
				return nil, e
			}
			if !seen[a] {
				seen[a] = true
				spenders = append(spenders, a)
			}
		}
	}
	if len(spenders) > 200 {
		return nil, summaryError(422, "SPENDER_BUDGET_EXCEEDED", "More than 200 spenders discovered; split the discovery interval")
	}
	sort.Strings(spenders)
	rows := make([]map[string]any, len(spenders))
	results, e := s.nextAllowanceBatch(ctx, q, spenders, point)
	if e != nil {
		return nil, e
	}
	for i, a := range spenders {
		row := map[string]any{"owner": q.Address, "spender": a, "token_contract": q.TokenContract, "balance_source": "rpc_state", "as_of_block": point.Block, "is_current_balance": point.Current}
		result := results[i]
		if result.Err != nil || len(result.Output) != 32 {
			row["success"] = false
			row["error"] = "allowance state read unavailable"
		} else {
			v := new(big.Int).SetBytes(result.Output)
			row["allowance_raw"] = v.String()
			row["success"] = true
			row["unlimited_uint256"] = v.BitLen() == 256 && v.Cmp(new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))) == 0
		}
		rows[i] = row
	}
	if e = s.nextBlockGuard(ctx, q.Chain, point); e != nil {
		return nil, e
	}
	complete := true
	for _, r := range rows {
		if r["success"] != true {
			complete = false
		}
	}
	return map[string]any{"chain": q.Chain, "address": q.Address, "token_contract": q.TokenContract, "snapshot": point, "allowances": rows, "record_count": len(rows), "reads_complete": complete, "spender_discovery_complete": false, "discovery_scope": map[string]any{"from_block": q.FromBlock, "to_block": q.ToBlock, "explicit_spenders": q.Spenders}, "evidence": evidence, "limitations": []string{"Approval events discover candidates only; allowance values always come from eth_call", "Spenders outside the requested interval or without standard Approval events remain undiscovered", "Permit/Permit2 and nonstandard NFT/operator approvals require separate adapters"}}, nil
}

var controlEvents = []string{"OwnershipTransferred(address,address)", "AdminChanged(address,address)", "Upgraded(address)", "BeaconUpgraded(address)", "RoleGranted(bytes32,address,address)", "RoleRevoked(bytes32,address,address)", "Paused(address)", "Unpaused(address)"}

func (s *Server) nextControlHistory(ctx context.Context, q nextRequest) (any, error) {
	a, _ := chain.ParseAddress(q.Address)
	topics := []string{}
	names := map[string]string{}
	for _, sig := range controlEvents {
		t := method.Topic0Of(sig)
		topics = append(topics, t)
		names[strings.ToLower(t)] = sig
	}
	logs, e := s.nextLogs(ctx, q, []chain.Address{a}, []any{topics})
	if e != nil {
		return nil, e
	}
	beforeBlock := *q.FromBlock
	if beforeBlock > 0 {
		beforeBlock--
	}
	before, e := s.nextPoint(ctx, nextRequest{Chain: q.Chain, Block: json.RawMessage(fmt.Sprintf("%d", beforeBlock))})
	if e != nil {
		return nil, e
	}
	after, e := s.nextPoint(ctx, nextRequest{Chain: q.Chain, Block: json.RawMessage(fmt.Sprintf("%d", *q.ToBlock))})
	if e != nil {
		return nil, e
	}
	states := []any{}
	for _, p := range []chainread.StatePoint{before, after} {
		data, e := s.investigationContract(ctx, investigationRequest{Chain: q.Chain, Address: q.Address}, "get_contract_control", p)
		if e != nil {
			return nil, e
		}
		states = append(states, data)
	}
	rows := []any{}
	for _, l := range logs {
		signature := ""
		if len(l.Topics) > 0 {
			signature = names[strings.ToLower(l.Topics[0])]
		}
		rows = append(rows, map[string]any{"event_signature": signature, "evidence": l, "verification": "Standard event signature at contract emitter; custom authorization semantics require bytecode/ABI review"})
	}
	if e = s.nextBlockGuard(ctx, q.Chain, before); e != nil {
		return nil, e
	}
	if e = s.nextBlockGuard(ctx, q.Chain, after); e != nil {
		return nil, e
	}
	return map[string]any{"chain": q.Chain, "address": q.Address, "from_block": *q.FromBlock, "data_cutoff_block": after.Block, "data_cutoff_hash": after.Hash, "events": rows, "record_count": len(rows), "boundary_states": states, "event_range_complete": true, "control_history_complete": false, "limitations": []string{"Only standard ownership/admin/upgrade/role/pause events are selected", "Boundary state includes all transactions in each block; events do not expose exact intra-transaction storage snapshots", "Custom privileges, silent storage changes and earlier history are not proven absent"}}, nil
}

type fundsLeg struct {
	Asset    string `json:"asset"`
	From     string `json:"from"`
	To       string `json:"to"`
	Raw      string `json:"amount_raw"`
	Kind     string `json:"kind"`
	Evidence any    `json:"evidence"`
}

func nativeTraceLegs(root *chaintrace.CallFrame) []fundsLeg {
	out := []fundsLeg{}
	var walk func(*chaintrace.CallFrame, bool, string)
	walk = func(f *chaintrace.CallFrame, reverted bool, path string) {
		if f == nil {
			return
		}
		reverted = reverted || f.Error != ""
		if !reverted && f.To != nil && (f.Type == "CALL" || f.Type == "CREATE" || f.Type == "CREATE2" || f.Type == "SELFDESTRUCT") {
			v, e := nextRaw(f.Value)
			if e == nil && v.Sign() > 0 {
				out = append(out, fundsLeg{"native", f.From.String(), f.To.String(), v.String(), "trace_value", map[string]any{"trace_path": path, "call_type": f.Type}})
			}
		}
		for i := range f.Children {
			walk(&f.Children[i], reverted, fmt.Sprintf("%s/%d", path, i))
		}
	}
	walk(root, false, "0")
	return out
}
func (s *Server) nextExplain(ctx context.Context, q nextRequest) (any, error) {
	h, _ := chain.ParseHash(q.TxHash)
	tx, e := s.deps.ChainRead.Transaction(ctx, q.Chain, h, investigationOptions())
	if e != nil {
		return nil, e
	}
	receipt, e := s.deps.ChainRead.Receipt(ctx, q.Chain, h, investigationOptions())
	if e != nil {
		return nil, e
	}
	if tx == nil || receipt == nil || receipt.BlockNumber == nil || receipt.Status == nil {
		return nil, summaryError(409, "TRANSACTION_NOT_CONFIRMED", "Confirmed transaction and receipt required")
	}
	block, e := s.deps.ChainRead.Block(ctx, q.Chain, chain.AtBlock(*receipt.BlockNumber), false, investigationOptions())
	if e != nil {
		return nil, e
	}
	if e = validateTraceSource(receipt, block, h); e != nil {
		return nil, e
	}
	success := *receipt.Status == 1
	legs := []fundsLeg{}
	gaps := []string{}
	var trace any
	if success {
		for _, l := range receipt.Logs {
			if l.Removed || len(l.Topics) != 3 || !strings.EqualFold(l.Topics[0], method.Topic0Of("Transfer(address,address,uint256)")) {
				continue
			}
			raw, e := chain.HexToBytes(l.Data)
			from, ef := strictTopicAddress(l.Topics[1])
			to, et := strictTopicAddress(l.Topics[2])
			if e != nil || len(raw) != 32 || ef != nil || et != nil {
				gaps = append(gaps, "malformed Transfer event retained in raw receipt")
				continue
			}
			legs = append(legs, fundsLeg{l.Address.String(), from, to, new(big.Int).SetBytes(raw).String(), "erc20_transfer_event", l})
		}
		if q.IncludeTrace && s.deps.Trace != nil {
			result, err := s.deps.Trace.TransactionTrace(ctx, q.Chain, h, chaintrace.Options{Consistency: "auto", MaxTraceCalls: 1})
			if err == nil && result != nil && result.Root != nil {
				trace = result
				legs = append(legs, nativeTraceLegs(result.Root)...)
			} else {
				gaps = append(gaps, "native internal trace unavailable")
			}
		}
		if trace == nil {
			to := tx.To
			if to == nil {
				to = receipt.ContractAddress
			}
			if to != nil && tx.Value != nil && tx.Value.Sign() > 0 {
				legs = append(legs, fundsLeg{"native", tx.From.String(), to.String(), tx.Value.String(), "transaction_value", map[string]any{"tx_hash": q.TxHash}})
			}
			gaps = append(gaps, "internal native movements not enumerated")
		}
	}
	var gas any
	price, e := nextRaw(receipt.EffectiveGasPrice)
	if e == nil {
		gas = new(big.Int).Mul(new(big.Int).SetUint64(receipt.GasUsed), price).String()
	} else {
		gaps = append(gaps, "effective gas price unavailable")
	}
	net := map[string]map[string]*big.Int{}
	for _, l := range legs {
		if net[l.Asset] == nil {
			net[l.Asset] = map[string]*big.Int{}
		}
		for _, a := range []string{l.From, l.To} {
			if net[l.Asset][a] == nil {
				net[l.Asset][a] = new(big.Int)
			}
		}
		v, _ := nextRaw(l.Raw)
		net[l.Asset][l.From].Sub(net[l.Asset][l.From], v)
		net[l.Asset][l.To].Add(net[l.Asset][l.To], v)
	}
	amounts := map[string]map[string]string{}
	for asset, accounts := range net {
		amounts[asset] = map[string]string{}
		for a, v := range accounts {
			amounts[asset][a] = v.String()
		}
	}
	decoded, _, decodeErr := s.investigationDispatch(ctx, investigationRequest{Chain: q.Chain, TxHash: q.TxHash}, "decode_transaction_input")
	if decodeErr != nil {
		gaps = append(gaps, "calldata ABI decoding unavailable")
	}
	if e = s.nextBlockGuard(ctx, q.Chain, chainread.StatePoint{Block: block.Number, Hash: block.Hash}); e != nil {
		return nil, e
	}
	return map[string]any{"chain": q.Chain, "tx_hash": q.TxHash, "success": success, "transaction": tx, "receipt": receipt, "decoded_input": decoded, "funds_legs": legs, "net_transfer_deltas_raw": amounts, "gas_cost_raw": gas, "gas_payer": tx.From, "gas_scope": "gasUsed * effectiveGasPrice; L1/blob/protocol-specific extra fees are not included", "trace": trace, "data_cutoff_block": block.Number, "data_cutoff_hash": block.Hash, "limitations": gaps, "interpretation": "Committed transfer events and successful native value frames; event net deltas are not a transaction-level state proof or identity attribution"}, nil
}
