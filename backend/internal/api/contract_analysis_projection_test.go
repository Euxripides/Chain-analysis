package api

import (
	"encoding/json"
	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
	"math/big"
	"strings"
	"testing"
)

func TestPackedNestedBalanceProjectionRejectsComputedOrUnboundCode(t *testing.T) {
	asset := "0x" + strings.Repeat("a", 40)
	holder := "0x" + strings.Repeat("1", 40)
	result := ca.Result{Sections: map[string][]json.RawMessage{
		"methods":     {ca.Row(map[string]any{"method_id": "balance", "signature": "balanceOf(address)", "state_mutability": "view", "verified_signature": true, "code_address": asset, "storage_address": asset, "code_hash": contractKeccak([]byte{0x60, 0}), "parameters": []any{map[string]any{"source_ast_id": 42}}})},
		"expressions": {ca.Row(map[string]any{"method_id": "balance", "expression_id": "return", "op": "Return", "operands": []string{"member"}}), ca.Row(map[string]any{"method_id": "balance", "expression_id": "member", "op": "MemberAccess", "text": "ledger[holder][7].amount"})},
		"steps":       {ca.Row(map[string]any{"method_id": "balance", "kind": "return", "expression_id": "return"})},
		"storage":     {ca.Row(map[string]any{"method_id": "balance", "access": "read", "key_expression": "ledger[holder][7].amount", "layout_status": "verified_compiler_layout", "storage_projection": map[string]any{"base_slot": "3", "scalar_type": "uint128", "offset": 16, "path": []any{map[string]any{"kind": "mapping", "key_type": "address", "key_referenced_declaration": 42}, map[string]any{"kind": "mapping", "key_type": "uint256", "key_literal": "7"}, map[string]any{"kind": "member", "relative_slot": "0", "byte_offset": 16}}}})},
	}}
	pre := map[string]ca.StateAccount{asset: {Code: "0x6000"}}
	p := contractBalanceProjection(&result, asset, pre)
	if p == nil {
		t.Fatal("verified nested member projection missing")
	}
	slot, err := p.key(holder)
	if err != nil || len(slot) != 66 {
		t.Fatal(slot, err)
	}
	bigAmount, _ := new(big.Int).SetString("9007199254740993123456789", 10)
	word := new(big.Int).Add(new(big.Int).Lsh(new(big.Int).Set(bigAmount), 128), big.NewInt(999))
	value, err := p.value("0x" + word.Text(16))
	if err != nil || value != bigAmount.String() {
		t.Fatal("packed neighbor affected amount", value, err)
	}
	pre[asset] = ca.StateAccount{Code: "0x6001"}
	if contractBalanceProjection(&result, asset, pre) != nil {
		t.Fatal("wrong prestate code accepted")
	}
	pre[asset] = ca.StateAccount{Code: "0x6000"}
	e := rowMap(result.Sections["expressions"][1])
	e["op"] = "*"
	result.Sections["expressions"][1] = ca.Row(e)
	if contractBalanceProjection(&result, asset, pre) != nil {
		t.Fatal("computed balance was treated as direct stored amount")
	}
}
