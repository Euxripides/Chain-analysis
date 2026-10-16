package contractanalysis

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
)

// CallFrame is Geth callTracer's schema. Unknown types remain call evidence and
// never become settlement edges. Monetary values stay exact integer strings.
type CallFrame struct {
	Type         string      `json:"type"`
	From         string      `json:"from"`
	To           string      `json:"to"`
	Value        string      `json:"value"`
	Gas          string      `json:"gas"`
	GasUsed      string      `json:"gasUsed"`
	Input        string      `json:"input"`
	Output       string      `json:"output"`
	Error        string      `json:"error"`
	RevertReason string      `json:"revertReason"`
	Calls        []CallFrame `json:"calls"`
}

func RawInteger(value string) (string, error) {
	base := 10
	if strings.HasPrefix(value, "0x") {
		base = 16
		value = value[2:]
	}
	if value == "" {
		value = "0"
	}
	n, ok := new(big.Int).SetString(value, base)
	if !ok || n.Sign() < 0 || n.BitLen() > 256 {
		return "", fmt.Errorf("invalid uint256")
	}
	return n.String(), nil
}
func TraceRows(raw []byte, txHash string, success bool, limit int) (map[string][]json.RawMessage, error) {
	var root CallFrame
	if e := json.Unmarshal(raw, &root); e != nil || root.Type == "" {
		return nil, fmt.Errorf("invalid callTracer response")
	}
	out := map[string][]json.RawMessage{"execution_calls": {}, "executed_funds": {}, "execution_evidence": {}}
	count := 0
	var walk func(CallFrame, string, string, string, string, string, bool) error
	walk = func(frame CallFrame, path, storage, caller, parentCode, parentValue string, ancestorSuccess bool) error {
		count++
		if count > limit {
			return fmt.Errorf("complete trace exceeds max_records")
		}
		kind := strings.ToUpper(frame.Type)
		settled := ancestorSuccess && frame.Error == ""
		code := strings.ToLower(frame.To)
		if kind == "SELFDESTRUCT" {
			code = parentCode
			storage = strings.ToLower(frame.From)
		} else if kind == "CALLCODE" {
			caller = strings.ToLower(frame.From)
		} else if kind != "DELEGATECALL" && kind != "CALLCODE" {
			storage = code
			caller = strings.ToLower(frame.From)
		}
		reportedValue := frame.Value
		if kind == "DELEGATECALL" {
			reportedValue = parentValue
		} else if kind == "STATICCALL" {
			reportedValue = "0"
		}
		value, e := RawInteger(reportedValue)
		if e != nil {
			return e
		}
		eid := ID(txHash, path, "call")
		callID := ID(txHash, path)
		selector := ""
		if len(frame.Input) >= 10 {
			selector = strings.ToLower(frame.Input[:10])
		}
		out["execution_calls"] = append(out["execution_calls"], Row(map[string]any{"call_id": callID, "tx_hash": txHash, "trace_path": path, "call_type": kind,
			"from": strings.ToLower(frame.From), "to": strings.ToLower(frame.To), "code_address": code, "storage_address": storage, "effective_caller": caller,
			"value_raw": value, "input": frame.Input, "output": frame.Output, "selector": selector, "gas_used": frame.GasUsed,
			"settled": settled, "error": frame.Error, "revert_reason": frame.RevertReason, "evidence_ids": []string{eid}}))
		out["execution_evidence"] = append(out["execution_evidence"], Row(map[string]any{"evidence_id": eid, "kind": "observed_execution", "tx_hash": txHash, "trace_path": path,
			"code_address": code, "storage_address": storage, "source": "debug_traceTransaction/callTracer"}))
		if settled && value != "0" && !strings.EqualFold(frame.From, frame.To) && (kind == "CALL" || kind == "CREATE" || kind == "CREATE2" || kind == "SELFDESTRUCT") {
			out["executed_funds"] = append(out["executed_funds"], Row(map[string]any{"fund_id": ID(txHash, path, "native"), "tx_hash": txHash, "trace_path": path,
				"asset": "native", "from": strings.ToLower(frame.From), "to": strings.ToLower(frame.To), "amount_raw": value, "unit": "wei", "settlement": "executed_" + strings.ToLower(kind), "evidence_ids": []string{eid}}))
		}
		for i, child := range frame.Calls {
			if e = walk(child, fmt.Sprintf("%s.%d", path, i), storage, caller, code, value, settled); e != nil {
				return e
			}
		}
		return nil
	}
	if e := walk(root, "0", "", "", "", "0", success); e != nil {
		return nil, e
	}
	return out, nil
}

type StateAccount struct {
	Balance string            `json:"balance,omitempty"`
	Nonce   json.Number       `json:"nonce,omitempty"`
	Code    string            `json:"code,omitempty"`
	Storage map[string]string `json:"storage,omitempty"`
}
type StateDiff struct {
	Pre  map[string]StateAccount `json:"pre"`
	Post map[string]StateAccount `json:"post"`
}

func StateDiffRows(raw []byte, txHash string, limit int) ([]json.RawMessage, error) {
	var diff StateDiff
	if e := json.Unmarshal(raw, &diff); e != nil || diff.Pre == nil || diff.Post == nil {
		return nil, fmt.Errorf("invalid prestateTracer diff response")
	}
	addresses := map[string]bool{}
	for a := range diff.Pre {
		addresses[a] = true
	}
	for a := range diff.Post {
		addresses[a] = true
	}
	rows := []json.RawMessage{}
	for address := range addresses {
		pre, post := diff.Pre[address], diff.Post[address]
		slots := map[string]bool{}
		for k := range pre.Storage {
			slots[k] = true
		}
		for k := range post.Storage {
			slots[k] = true
		}
		for slot := range slots {
			before, beforePresent := pre.Storage[slot]
			after, afterPresent := post.Storage[slot]
			// Missing post storage for a changed pre slot means the value was cleared.
			// Missing pre storage means a previously empty slot was introduced.
			if !beforePresent {
				before = "0x0"
			}
			if !afterPresent {
				after = "0x0"
			}
			b, e := RawInteger(before)
			if e != nil {
				return nil, e
			}
			a, e := RawInteger(after)
			if e != nil {
				return nil, e
			}
			if b == a {
				continue
			}
			if len(rows) >= limit {
				return nil, fmt.Errorf("state diff exceeds max_records")
			}
			rows = append(rows, Row(map[string]any{"state_change_id": ID(txHash, address, slot), "tx_hash": txHash, "storage_address": strings.ToLower(address), "slot": slot,
				"before_raw": b, "after_raw": a, "before_hex": before, "after_hex": after, "granularity": "transaction", "evidence_ids": []string{ID(txHash, "state-diff")}}))
		}
		if pre.Balance != "" || post.Balance != "" || pre.Nonce != "" || post.Nonce != "" || pre.Code != "" || post.Code != "" {
			if len(rows) >= limit {
				return nil, fmt.Errorf("state diff exceeds max_records")
			}
			rows = append(rows, Row(map[string]any{"state_change_id": ID(txHash, address, "account"), "tx_hash": txHash, "storage_address": strings.ToLower(address),
				"before_balance_hex": pre.Balance, "after_balance_hex": post.Balance, "before_nonce": string(pre.Nonce), "after_nonce": string(post.Nonce), "before_code": pre.Code, "after_code": post.Code,
				"granularity": "transaction", "evidence_ids": []string{ID(txHash, "state-diff")}}))
		}
	}
	return rows, nil
}
