package api

import (
	"encoding/hex"
	"fmt"
	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
	"math/big"
	"strconv"
	"strings"
)

type contractBalanceNode struct {
	Literal  string
	Read     *contractLedgerProjection
	Op       string
	Bits     uint
	Wrap     bool
	Children []*contractBalanceNode
}
type contractBalanceModel struct {
	Root        *contractBalanceNode
	CodeAddress string
	Evidence    []string
}

// A restricted, typed balanceOf expression evaluator. Calls, assembly, branches,
// missing words and changed runtime code fail closed. All quantities use big.Int.
func contractComputedBalance(result *ca.Result, asset string, pre map[string]ca.StateAccount) *contractBalanceModel {
	for _, raw := range result.Sections["methods"] {
		m := rowMap(raw)
		codeAddress := fmt.Sprint(m["code_address"])
		if m["signature"] != "balanceOf(address)" || m["verified_signature"] != true || m["state_mutability"] != "view" && m["state_mutability"] != "pure" || m["dispatch_role"] == "overridden_source_definition" || !strings.EqualFold(fmt.Sprint(m["storage_address"]), asset) {
			continue
		}
		code, err := hex.DecodeString(strings.TrimPrefix(pre[codeAddress].Code, "0x"))
		if err != nil || len(code) == 0 || contractKeccak(code) != m["code_hash"] {
			continue
		}
		parameters, _ := m["parameters"].([]any)
		if len(parameters) != 1 {
			continue
		}
		parameter, _ := parameters[0].(map[string]any)
		if parameter["source_ast_id"] == nil {
			continue
		}
		mid := m["method_id"]
		unsafe := false
		entryReturns := 0
		returnID := ""
		for _, raw := range result.Sections["steps"] {
			s := rowMap(raw)
			if s["method_id"] != mid {
				continue
			}
			switch s["kind"] {
			case "assembly", "try", "loop", "branch", "external_call", "guard":
				unsafe = true
			}
			if s["kind"] == "return" && s["return_scope"] != "internal" {
				entryReturns++
				returnID = fmt.Sprint(s["expression_id"])
				if len(stringList(s["conditions"])) != 0 {
					unsafe = true
				}
			}
		}
		for _, raw := range result.Sections["calls"] {
			if rowMap(raw)["method_id"] == mid {
				unsafe = true
			}
		}
		for _, raw := range result.Sections["storage"] {
			s := rowMap(raw)
			if s["method_id"] == mid && s["access"] == "write" {
				unsafe = true
			}
		}
		if unsafe || entryReturns != 1 {
			continue
		}
		expressions := map[string]map[string]any{}
		reads := map[string]*contractLedgerProjection{}
		for _, raw := range result.Sections["expressions"] {
			e := rowMap(raw)
			if e["method_id"] == mid {
				expressions[fmt.Sprint(e["expression_id"])] = e
			}
		}
		model := &contractBalanceModel{CodeAddress: codeAddress}
		for _, raw := range result.Sections["storage"] {
			s := rowMap(raw)
			if s["method_id"] != mid || s["access"] != "read" || s["layout_status"] != "verified_compiler_layout" {
				continue
			}
			p, ok := s["storage_projection"].(map[string]any)
			if !ok {
				continue
			}
			bits, err := strconv.ParseUint(strings.TrimPrefix(fmt.Sprint(p["scalar_type"]), "uint"), 10, 16)
			offset, oe := strconv.ParseUint(fmt.Sprint(p["offset"]), 10, 16)
			if err != nil || oe != nil || bits == 0 || bits > 256 || bits%8 != 0 || offset*8+bits > 256 {
				continue
			}
			var path []map[string]any
			if nextDecode(ca.Row(p["path"]), &path) != nil {
				continue
			}
			valid := true
			for _, step := range path {
				switch step["kind"] {
				case "mapping":
					if step["key_type"] == "address" && step["key_referenced_declaration"] == parameter["source_ast_id"] {
						continue
					}
					if !strings.HasPrefix(fmt.Sprint(step["key_type"]), "uint") || step["key_literal"] == nil {
						valid = false
					}
				case "member":
				default:
					valid = false
				}
			}
			if valid {
				reads[fmt.Sprint(s["key_expression_id"])] = &contractLedgerProjection{Base: fmt.Sprint(p["base_slot"]), Bits: uint(bits), Offset: uint(offset), Path: path}
				model.Evidence = append(model.Evidence, stringList(s["evidence_ids"])...)
			}
		}
		active := map[string]bool{}
		count := 0
		var build func(string) *contractBalanceNode
		build = func(key string) *contractBalanceNode {
			count++
			if count > 128 || active[key] {
				return nil
			}
			active[key] = true
			defer delete(active, key)
			e := expressions[key]
			if e == nil || e["unresolved_return_value"] != nil {
				return nil
			}
			if bound, ok := e["binding_expression_id"].(string); ok {
				return build(bound)
			}
			if value, ok := e["resolved_value"].(string); ok {
				if n, ok := new(big.Int).SetString(value, 10); ok && n.Sign() >= 0 && n.BitLen() <= 256 {
					return &contractBalanceNode{Literal: value}
				}
				return nil
			}
			if p := reads[key]; p != nil {
				return &contractBalanceNode{Read: p}
			}
			args := stringList(e["operands"])
			op := fmt.Sprint(e["op"])
			if op == "Return" && len(args) == 1 {
				return build(args[0])
			}
			if op == "Literal" && e["literal_kind"] == "number" {
				value := fmt.Sprint(e["value"])
				if n, ok := new(big.Int).SetString(value, 10); ok && n.Sign() >= 0 && n.BitLen() <= 256 {
					return &contractBalanceNode{Literal: value}
				}
				return nil
			}
			if op == "Identifier" {
				candidates := []map[string]any{}
				for _, raw := range result.Sections["definitions"] {
					d := rowMap(raw)
					if d["method_id"] == mid && d["declaration_id"] == e["referenced_declaration"] {
						candidates = append(candidates, d)
					}
				}
				if len(candidates) == 1 && candidates[0]["operation"] == "=" && len(stringList(candidates[0]["conditions"])) == 0 {
					return build(fmt.Sprint(candidates[0]["value_expression_id"]))
				}
				return nil
			}
			bits, err := strconv.ParseUint(strings.TrimPrefix(fmt.Sprint(e["type"]), "uint"), 10, 16)
			if err != nil || bits == 0 || bits > 256 || bits%8 != 0 {
				return nil
			}
			node := &contractBalanceNode{Op: op, Bits: uint(bits), Wrap: e["arithmetic"] == "wrap" || op == "<<"}
			if op == "FunctionCall" && e["arithmetic"] == "explicit_cast_truncation" && len(args) == 2 {
				node.Op = "uint_cast"
				node.Wrap = true
				args = args[1:]
			} else if !strings.Contains("|+|-|*|/|%|<<|>>|&|^|", "|"+op+"|") || len(args) != 2 {
				return nil
			}
			for _, arg := range args {
				child := build(arg)
				if child == nil {
					return nil
				}
				node.Children = append(node.Children, child)
			}
			return node
		}
		model.Root = build(returnID)
		if model.Root != nil {
			return model
		}
	}
	return nil
}

func (m contractBalanceModel) evaluate(address, asset string, pre map[string]ca.StateAccount, diff ca.StateDiff, after bool) (string, error) {
	if changed := diff.Post[m.CodeAddress].Code; changed != "" && !strings.EqualFold(changed, pre[m.CodeAddress].Code) {
		return "", fmt.Errorf("balance runtime changed")
	}
	var eval func(*contractBalanceNode) (*big.Int, error)
	eval = func(n *contractBalanceNode) (*big.Int, error) {
		if n.Literal != "" {
			v, ok := new(big.Int).SetString(n.Literal, 10)
			if !ok {
				return nil, fmt.Errorf("invalid literal")
			}
			return v, nil
		}
		if n.Read != nil {
			key, err := n.Read.key(address)
			if err != nil {
				return nil, err
			}
			word, observed := contractStorageWord(pre[asset].Storage, key)
			old, hasOld := contractStorageWord(diff.Pre[asset].Storage, key)
			post, hasPost := contractStorageWord(diff.Post[asset].Storage, key)
			if !observed {
				if hasOld {
					word = old
				} else if hasPost {
					word = "0x0"
				} else {
					return nil, fmt.Errorf("unobserved balance state")
				}
			}
			if after {
				if hasPost {
					word = post
				} else if hasOld {
					word = "0x0"
				}
			}
			value, err := n.Read.value(word)
			if err != nil {
				return nil, err
			}
			v, _ := new(big.Int).SetString(value, 10)
			return v, nil
		}
		left, err := eval(n.Children[0])
		if err != nil {
			return nil, err
		}
		v := new(big.Int).Set(left)
		if n.Op != "uint_cast" {
			right, err := eval(n.Children[1])
			if err != nil {
				return nil, err
			}
			switch n.Op {
			case "+":
				v.Add(v, right)
			case "-":
				v.Sub(v, right)
			case "*":
				v.Mul(v, right)
			case "/", "%":
				if right.Sign() == 0 {
					return nil, fmt.Errorf("division by zero")
				}
				if n.Op == "/" {
					v.Quo(v, right)
				} else {
					v.Rem(v, right)
				}
			case "<<", ">>":
				if !right.IsUint64() || right.Uint64() >= 256 {
					v.SetInt64(0)
				} else if n.Op == "<<" {
					v.Lsh(v, uint(right.Uint64()))
				} else {
					v.Rsh(v, uint(right.Uint64()))
				}
			case "&":
				v.And(v, right)
			case "^":
				v.Xor(v, right)
			default:
				return nil, fmt.Errorf("unsupported balance arithmetic")
			}
		}
		modulus := new(big.Int).Lsh(big.NewInt(1), n.Bits)
		if n.Wrap {
			v.Mod(v, modulus)
		} else if v.Sign() < 0 || v.Cmp(modulus) >= 0 {
			return nil, fmt.Errorf("checked balance overflow")
		}
		return v, nil
	}
	value, err := eval(m.Root)
	if err != nil {
		return "", err
	}
	return value.String(), nil
}
