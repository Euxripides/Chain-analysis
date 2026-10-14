package api

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
	"math/big"
	"sort"
	"strings"
)

func contractLedgerSlot(result *ca.Result, asset string, pre map[string]ca.StateAccount) (string, []string, bool) {
	for _, raw := range result.Sections["methods"] {
		m := rowMap(raw)
		if m["signature"] != "balanceOf(address)" || m["verified_signature"] != true || m["state_mutability"] != "view" && m["state_mutability"] != "pure" || !strings.EqualFold(fmt.Sprint(m["storage_address"]), asset) || m["dispatch_role"] == "overridden_source_definition" {
			continue
		}
		code, _ := hex.DecodeString(strings.TrimPrefix(pre[fmt.Sprint(m["code_address"])].Code, "0x"))
		if len(code) == 0 || contractKeccak(code) != m["code_hash"] {
			continue
		}
		var ledger map[string]any
		stateIDs := map[string]bool{}
		for _, sr := range result.Sections["storage"] {
			s := rowMap(sr)
			if s["method_id"] != m["method_id"] || s["access"] != "read" {
				continue
			}
			stateIDs[fmt.Sprint(s["state_id"])] = true
			if strings.ReplaceAll(fmt.Sprint(s["variable_type"]), " ", "") == "mapping(address=>uint256)" && s["slot"] != nil && (s["layout_status"] == "verified_compiler_layout" || s["layout_status"] == "derived_solidity_storage_rules") {
				ledger = s
			}
		}
		if len(stateIDs) != 1 || ledger == nil {
			continue
		}
		direct := m["entry_kind"] == "generated_getter"
		if !direct {
			expressions := map[string]map[string]any{}
			for _, er := range result.Sections["expressions"] {
				e := rowMap(er)
				if e["method_id"] == m["method_id"] {
					expressions[fmt.Sprint(e["expression_id"])] = e
				}
			}
			parameters, _ := m["parameters"].([]any)
			if len(parameters) != 1 {
				continue
			}
			param, _ := parameters[0].(map[string]any)
			returns := 0
			for _, sr := range result.Sections["steps"] {
				step := rowMap(sr)
				if step["method_id"] != m["method_id"] || step["kind"] != "return" {
					continue
				}
				returns++
				root := expressions[fmt.Sprint(step["expression_id"])]
				operands := stringList(root["operands"])
				if root["op"] != "Return" || len(operands) != 1 {
					direct = false
					break
				}
				index := expressions[operands[0]]
				args := stringList(index["operands"])
				if index["op"] != "IndexAccess" || len(args) != 2 || index["text"] != ledger["key_expression"] {
					direct = false
					break
				}
				key := expressions[args[1]]
				if key["op"] != "Identifier" || key["referenced_declaration"] != param["source_ast_id"] {
					direct = false
					break
				}
				direct = true
			}
			if returns == 0 {
				direct = false
			}
		}
		if direct {
			return fmt.Sprint(ledger["slot"]), stringList(ledger["evidence_ids"]), true
		}
	}
	return "", nil, false
}
func contractMappingSlot(address, slot string) (string, error) {
	if !ca.CompleteAddress.MatchString(address) {
		return "", fmt.Errorf("invalid mapping address")
	}
	n, ok := new(big.Int).SetString(slot, 10)
	if !ok || n.Sign() < 0 || n.BitLen() > 256 {
		return "", fmt.Errorf("invalid mapping slot")
	}
	word := make([]byte, 64)
	addr, _ := hex.DecodeString(address[2:])
	copy(word[12:32], addr)
	n.FillBytes(word[32:])
	return contractKeccak(word), nil
}
func contractStorageWord(storage map[string]string, slot string) (string, bool) {
	target, ok := new(big.Int).SetString(strings.TrimPrefix(slot, "0x"), 16)
	if !ok {
		return "", false
	}
	for key, value := range storage {
		n, ok := new(big.Int).SetString(strings.TrimPrefix(key, "0x"), 16)
		if ok && n.Cmp(target) == 0 {
			return value, true
		}
	}
	return "", false
}
func contractTokenReconciliation(result *ca.Result, txHash string, preRaw, diffRaw json.RawMessage) {
	var pre map[string]ca.StateAccount
	var diff ca.StateDiff
	valid := json.Unmarshal(preRaw, &pre) == nil && json.Unmarshal(diffRaw, &diff) == nil && diff.Pre != nil && diff.Post != nil
	group := map[string]map[string]*big.Int{}
	zero := "0x" + strings.Repeat("0", 40)
	for _, raw := range result.Sections["token_events"] {
		event := rowMap(raw)
		if event["tx_hash"] != txHash || (event["event"] != "Transfer" && event["protocol"] != "WETH9") || event["token_standard"] == "ERC721" || event["token_standard"] == "ERC1155" {
			continue
		}
		asset := strings.ToLower(fmt.Sprint(event["asset"]))
		if group[asset] == nil {
			group[asset] = map[string]*big.Int{}
		}
		amount, ok := new(big.Int).SetString(fmt.Sprint(event["amount_raw"]), 10)
		if !ok {
			continue
		}
		for _, direction := range []struct {
			key  string
			sign int64
		}{{"from", -1}, {"to", 1}} {
			address := strings.ToLower(fmt.Sprint(event[direction.key]))
			if address == zero {
				continue
			}
			if group[asset][address] == nil {
				group[asset][address] = new(big.Int)
			}
			group[asset][address].Add(group[asset][address], new(big.Int).Mul(amount, big.NewInt(direction.sign)))
		}
	}
	assets := []string{}
	for asset := range group {
		assets = append(assets, asset)
	}
	sort.Strings(assets)
	for _, asset := range assets {
		slot, evidence, known := contractLedgerSlot(result, asset, pre)
		projection := contractBalanceProjection(result, asset, pre)
		balanceModel := contractComputedBalance(result, asset, pre)
		basis := "verified_direct_balanceOf_mapping_and_transaction_diff"
		if projection != nil {
			known = true
			evidence = projection.Evidence
			basis = "verified_balanceOf_storage_projection_and_transaction_diff"
		}
		if balanceModel != nil {
			known = true
			evidence = balanceModel.Evidence
			basis = "verified_typed_balanceOf_expression_and_transaction_diff"
		}
		status := "ledger_unavailable"
		checked := 0
		unknown := []string{}
		mismatches := []string{}
		if valid && known {
			addresses := []string{}
			for a := range group[asset] {
				addresses = append(addresses, a)
			}
			sort.Strings(addresses)
			for _, address := range addresses {
				if balanceModel != nil {
					before, be := balanceModel.evaluate(address, asset, pre, diff, false)
					after, ae := balanceModel.evaluate(address, asset, pre, diff, true)
					if be != nil || ae != nil {
						unknown = append(unknown, address)
						continue
					}
					bn, _ := new(big.Int).SetString(before, 10)
					an, _ := new(big.Int).SetString(after, 10)
					delta := new(big.Int).Sub(an, bn)
					match := delta.Cmp(group[asset][address]) == 0
					checked++
					if !match {
						mismatches = append(mismatches, address)
					}
					result.Sections["token_balance_changes"] = append(result.Sections["token_balance_changes"], ca.Row(map[string]any{"balance_change_id": ca.ID(txHash, asset, address), "tx_hash": txHash, "asset": asset, "address": address, "before_raw": before, "after_raw": after, "delta_raw": delta.String(), "event_net_delta_raw": group[asset][address].String(), "net_matched": match, "unit": "token_raw", "basis": basis, "evidence_ids": append(append([]string{}, evidence...), ca.ID(txHash, "state-diff"))}))
					continue
				}
				key, e := contractMappingSlot(address, slot)
				if projection != nil {
					key, e = projection.key(address)
				}
				if e != nil {
					unknown = append(unknown, address)
					continue
				}
				before, observed := contractStorageWord(pre[asset].Storage, key)
				post, hasPost := contractStorageWord(diff.Post[asset].Storage, key)
				old, hasOld := contractStorageWord(diff.Pre[asset].Storage, key)
				if !observed {
					if hasOld {
						before = old
						observed = true
					} else if hasPost {
						before = "0x0"
						observed = true
					}
				}
				if !observed {
					unknown = append(unknown, address)
					continue
				}
				after := before
				if hasPost {
					after = post
				} else if hasOld {
					after = "0x0"
				}
				b, err := ca.RawInteger(before)
				if projection != nil {
					b, err = projection.value(before)
				}
				if err != nil {
					unknown = append(unknown, address)
					continue
				}
				a, err := ca.RawInteger(after)
				if projection != nil {
					a, err = projection.value(after)
				}
				if err != nil {
					unknown = append(unknown, address)
					continue
				}
				bn, _ := new(big.Int).SetString(b, 10)
				an, _ := new(big.Int).SetString(a, 10)
				delta := new(big.Int).Sub(an, bn)
				match := delta.Cmp(group[asset][address]) == 0
				checked++
				if !match {
					mismatches = append(mismatches, address)
				}
				result.Sections["token_balance_changes"] = append(result.Sections["token_balance_changes"], ca.Row(map[string]any{"balance_change_id": ca.ID(txHash, asset, address), "tx_hash": txHash, "asset": asset, "address": address, "slot": key, "before_raw": b, "after_raw": a, "delta_raw": delta.String(), "event_net_delta_raw": group[asset][address].String(), "net_matched": match, "unit": "token_raw", "basis": basis, "evidence_ids": append(append([]string{}, evidence...), ca.ID(txHash, "state-diff"))}))
			}
			status = "transaction_net_ledger_matched"
			if checked == 0 {
				status = "state_keys_unobserved"
			}
			if len(unknown) > 0 {
				status = "state_keys_unobserved"
			}
			if len(mismatches) > 0 {
				status = "transaction_net_ledger_mismatch"
			}
		} else if !valid {
			status = "transaction_diff_unavailable"
		}
		result.Sections["reconciliations"] = append(result.Sections["reconciliations"], ca.Row(map[string]any{"reconciliation_id": ca.ID(txHash, asset, "ledger"), "tx_hash": txHash, "asset": asset, "status": status, "addresses_checked": checked, "unobserved_addresses": unknown, "mismatched_addresses": mismatches, "granularity": "transaction_net_changes", "individual_event_settlement_verified": false}))
		for i, raw := range result.Sections["token_events"] {
			event := rowMap(raw)
			if event["tx_hash"] == txHash && event["asset"] == asset {
				event["state_reconciliation"] = status
				event["reconciliation_id"] = ca.ID(txHash, asset, "ledger")
				result.Sections["token_events"][i] = ca.Row(event)
			}
		}
	}
}
