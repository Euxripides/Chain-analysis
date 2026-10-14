package api

import (
	"encoding/json"
	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
	"strings"
	"testing"
)

func TestTokenEventReconciliationUsesExactNetStateAndRejectsCounterfeitAmount(t *testing.T) {
	asset := "0x" + strings.Repeat("a", 40)
	from := "0x" + strings.Repeat("1", 40)
	to := "0x" + strings.Repeat("2", 40)
	keyFrom, _ := contractMappingSlot(from, "3")
	keyTo, _ := contractMappingSlot(to, "3")
	makeResult := func(amount string) ca.Result {
		return ca.Result{Sections: map[string][]json.RawMessage{
			"methods":      {ca.Row(map[string]any{"method_id": "getter", "signature": "balanceOf(address)", "state_mutability": "view", "verified_signature": true, "entry_kind": "generated_getter", "code_address": asset, "storage_address": asset, "code_hash": contractKeccak([]byte{0x60, 0})})},
			"storage":      {ca.Row(map[string]any{"method_id": "getter", "access": "read", "state_id": "ledger", "slot": "3", "layout_status": "verified_compiler_layout", "variable_type": "mapping(address => uint256)", "evidence_ids": []string{"layout"}})},
			"token_events": {ca.Row(map[string]any{"tx_hash": "tx", "asset": asset, "event": "Transfer", "from": from, "to": to, "amount_raw": amount})},
		}}
	}
	pre := ca.Row(map[string]ca.StateAccount{asset: {Code: "0x6000", Storage: map[string]string{keyFrom: "0x100000000000000000000000000000000", keyTo: "0x0"}}})
	diff := ca.Row(ca.StateDiff{Pre: map[string]ca.StateAccount{asset: {Storage: map[string]string{keyFrom: "0x100000000000000000000000000000000"}}}, Post: map[string]ca.StateAccount{asset: {Storage: map[string]string{keyTo: "0x100000000000000000000000000000000"}}}})
	result := makeResult("340282366920938463463374607431768211456")
	contractTokenReconciliation(&result, "tx", pre, diff)
	if len(result.Sections["token_balance_changes"]) != 2 {
		t.Fatal("balance rows missing")
	}
	if rowMap(result.Sections["reconciliations"][0])["status"] != "transaction_net_ledger_matched" {
		t.Fatal(string(result.Sections["reconciliations"][0]))
	}
	if rowMap(result.Sections["reconciliations"][0])["individual_event_settlement_verified"] != false {
		t.Fatal("net matching incorrectly proved individual events")
	}
	result = makeResult("1")
	contractTokenReconciliation(&result, "tx", pre, diff)
	if rowMap(result.Sections["reconciliations"][0])["status"] != "transaction_net_ledger_mismatch" {
		t.Fatal("false event amount accepted")
	}
	result = makeResult("1")
	contractTokenReconciliation(&result, "tx", nil, nil)
	if rowMap(result.Sections["reconciliations"][0])["status"] != "transaction_diff_unavailable" {
		t.Fatal("missing state was invented")
	}
}
