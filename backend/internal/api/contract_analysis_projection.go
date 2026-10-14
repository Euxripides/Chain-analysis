package api

import (
	"encoding/hex"
	"fmt"
	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
	"math/big"
	"strconv"
	"strings"
)

type contractLedgerProjection struct {
	Base     string
	Bits     uint
	Offset   uint
	Path     []map[string]any
	Evidence []string
}

func contractBalanceProjection(result *ca.Result, asset string, pre map[string]ca.StateAccount) *contractLedgerProjection {
	for _, raw := range result.Sections["methods"] {
		method := rowMap(raw)
		if method["signature"] != "balanceOf(address)" || method["verified_signature"] != true || method["state_mutability"] != "view" && method["state_mutability"] != "pure" || method["dispatch_role"] == "overridden_source_definition" || !strings.EqualFold(fmt.Sprint(method["storage_address"]), asset) {
			continue
		}
		code, _ := hex.DecodeString(strings.TrimPrefix(pre[fmt.Sprint(method["code_address"])].Code, "0x"))
		if len(code) == 0 || contractKeccak(code) != method["code_hash"] {
			continue
		}
		parameters, _ := method["parameters"].([]any)
		if len(parameters) != 1 {
			continue
		}
		parameter, _ := parameters[0].(map[string]any)
		if parameter["source_ast_id"] == nil {
			continue
		}
		expressions := map[string]map[string]any{}
		for _, er := range result.Sections["expressions"] {
			e := rowMap(er)
			if e["method_id"] == method["method_id"] {
				expressions[fmt.Sprint(e["expression_id"])] = e
			}
		}
		var returned map[string]any
		count := 0
		for _, sr := range result.Sections["steps"] {
			step := rowMap(sr)
			if step["method_id"] != method["method_id"] || step["kind"] != "return" {
				continue
			}
			count++
			root := expressions[fmt.Sprint(step["expression_id"])]
			args := stringList(root["operands"])
			if root["op"] == "Return" && len(args) == 1 {
				returned = expressions[args[0]]
			}
		}
		if count != 1 || returned == nil || (returned["op"] != "IndexAccess" && returned["op"] != "MemberAccess") {
			continue
		}
		for _, raw := range result.Sections["storage"] {
			storage := rowMap(raw)
			if storage["method_id"] != method["method_id"] || storage["access"] != "read" || storage["key_expression"] != returned["text"] || storage["layout_status"] != "verified_compiler_layout" {
				continue
			}
			projection, _ := storage["storage_projection"].(map[string]any)
			scalar := fmt.Sprint(projection["scalar_type"])
			if !strings.HasPrefix(scalar, "uint") {
				continue
			}
			bits, err := strconv.ParseUint(strings.TrimPrefix(scalar, "uint"), 10, 16)
			if err != nil || bits == 0 || bits > 256 || bits%8 != 0 {
				continue
			}
			var paths []map[string]any
			if nextDecode(ca.Row(projection["path"]), &paths) != nil || len(paths) == 0 {
				continue
			}
			valid := true
			addressKeys := 0
			for _, p := range paths {
				switch p["kind"] {
				case "mapping":
					if p["key_type"] == "address" && p["key_referenced_declaration"] == parameter["source_ast_id"] {
						addressKeys++
					} else if strings.HasPrefix(fmt.Sprint(p["key_type"]), "uint") && p["key_literal"] != nil {
						value, ok := new(big.Int).SetString(fmt.Sprint(p["key_literal"]), 0)
						if !ok || value.Sign() < 0 || value.BitLen() > 256 {
							valid = false
						}
					} else {
						valid = false
					}
				case "member":
					n, ok := new(big.Int).SetString(fmt.Sprint(p["relative_slot"]), 10)
					if !ok || n.Sign() < 0 || n.BitLen() > 256 {
						valid = false
					}
				default:
					valid = false
				}
			}
			offset, offsetError := strconv.ParseUint(fmt.Sprint(projection["offset"]), 10, 16)
			if valid && addressKeys == 1 && offsetError == nil && offset*8+bits <= 256 {
				return &contractLedgerProjection{Base: fmt.Sprint(projection["base_slot"]), Bits: uint(bits), Offset: uint(offset), Path: paths, Evidence: stringList(storage["evidence_ids"])}
			}
		}
	}
	return nil
}

func (p contractLedgerProjection) key(address string) (string, error) {
	current, ok := new(big.Int).SetString(p.Base, 10)
	if !ok || current.Sign() < 0 || current.BitLen() > 256 {
		return "", fmt.Errorf("invalid projection base")
	}
	for _, step := range p.Path {
		if step["kind"] == "mapping" {
			data := make([]byte, 64)
			current.FillBytes(data[32:])
			if step["key_type"] == "address" {
				if !ca.CompleteAddress.MatchString(address) {
					return "", fmt.Errorf("invalid projection address")
				}
				b, _ := hex.DecodeString(address[2:])
				copy(data[12:32], b)
			} else {
				value, ok := new(big.Int).SetString(fmt.Sprint(step["key_literal"]), 0)
				if !ok || value.Sign() < 0 || value.BitLen() > 256 {
					return "", fmt.Errorf("invalid projection key")
				}
				value.FillBytes(data[:32])
			}
			current.SetString(strings.TrimPrefix(contractKeccak(data), "0x"), 16)
		} else if step["kind"] == "member" {
			value, ok := new(big.Int).SetString(fmt.Sprint(step["relative_slot"]), 10)
			if !ok {
				return "", fmt.Errorf("invalid member slot")
			}
			current.Add(current, value)
			current.Mod(current, new(big.Int).Lsh(big.NewInt(1), 256))
		}
	}
	return "0x" + fmt.Sprintf("%064x", current), nil
}
func (p contractLedgerProjection) value(word string) (string, error) {
	raw, err := ca.RawInteger(word)
	if err != nil {
		return "", err
	}
	n, _ := new(big.Int).SetString(raw, 10)
	n.Rsh(n, p.Offset*8)
	n.And(n, new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), p.Bits), big.NewInt(1)))
	return n.String(), nil
}
