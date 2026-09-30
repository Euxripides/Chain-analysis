package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
	"github.com/blockchain-platform/blockchain-platform/internal/method"
	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
	"golang.org/x/sync/singleflight"
	"io"
	"math/big"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

type investigationABI struct {
	ABI        json.RawMessage `json:"abi"`
	Source     string          `json:"abi_source"`
	Verified   bool            `json:"verified"`
	Address    string          `json:"abi_address"`
	Deployment json.RawMessage `json:"deployment,omitempty"`
	Fetched    time.Time       `json:"fetched_at"`
}

var investigationABICache = struct {
	sync.Mutex
	Entries map[string]investigationABI
}{Entries: map[string]investigationABI{}}
var investigationABIFlight singleflight.Group
var investigationABIHTTP = &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
	if req.URL.Host != "sourcify.dev" || req.URL.Scheme != "https" || len(via) > 2 {
		return fmt.Errorf("ABI redirect denied")
	}
	return nil
}}

func (s *Server) investigationABI(ctx context.Context, q investigationRequest, address string) (*method.ABI, investigationABI, error) {
	if len(q.ABI) > 0 && string(q.ABI) != "null" {
		a, e := method.ParseABI(q.ABI)
		return a, investigationABI{ABI: q.ABI, Source: "caller_supplied", Verified: false, Address: address, Fetched: time.Now().UTC()}, e
	}
	if a, rec, ok := s.deps.Registry.ABI(q.Chain, address); ok {
		return a, investigationABI{ABI: rec.ABIJSON, Source: rec.Source, Verified: rec.Verified, Address: address, Fetched: rec.UpdatedAt}, nil
	}
	cfg := mustChain(s, q.Chain)
	key := fmt.Sprintf("%d:%s", cfg.ChainID, address)
	investigationABICache.Lock()
	rec, ok := investigationABICache.Entries[key]
	investigationABICache.Unlock()
	if ok && time.Since(rec.Fetched) < time.Hour {
		a, e := method.ParseABI(rec.ABI)
		return a, rec, e
	}
	result, e, _ := investigationABIFlight.Do(key, func() (any, error) {
		url := fmt.Sprintf("https://sourcify.dev/server/v2/contract/%d/%s?fields=abi,deployment", cfg.ChainID, address)
		req, e := http.NewRequestWithContext(ctx, "GET", url, nil)
		if e != nil {
			return nil, e
		}
		res, e := investigationABIHTTP.Do(req)
		if e != nil {
			return nil, fmt.Errorf("verified ABI service is unavailable")
		}
		defer res.Body.Close()
		if res.StatusCode == 404 {
			return nil, fmt.Errorf("verified ABI is unavailable; provide abi explicitly")
		}
		if res.StatusCode != 200 {
			return nil, fmt.Errorf("verified ABI service is unavailable")
		}
		raw, e := io.ReadAll(io.LimitReader(res.Body, method.MaxABIJSONBytes+1))
		if e != nil || len(raw) > method.MaxABIJSONBytes {
			return nil, fmt.Errorf("ABI response exceeds limit")
		}
		var doc struct {
			ABI        json.RawMessage `json:"abi"`
			Deployment json.RawMessage `json:"deployment"`
			Match      string          `json:"match"`
			Address    string          `json:"address"`
			ChainID    string          `json:"chainId"`
		}
		if json.Unmarshal(raw, &doc) != nil || len(doc.ABI) == 0 {
			return nil, fmt.Errorf("invalid verified ABI response")
		}
		if !strings.EqualFold(doc.Address, address) || doc.ChainID != fmt.Sprint(cfg.ChainID) || (doc.Match != "exact_match" && doc.Match != "match") {
			return nil, fmt.Errorf("ABI verification identity mismatch")
		}
		if _, e = method.ParseABI(doc.ABI); e != nil {
			return nil, e
		}
		r := investigationABI{ABI: doc.ABI, Source: url, Verified: true, Address: address, Deployment: doc.Deployment, Fetched: time.Now().UTC()}
		investigationABICache.Lock()
		if len(investigationABICache.Entries) >= 1000 {
			investigationABICache.Entries = map[string]investigationABI{}
		}
		investigationABICache.Entries[key] = r
		investigationABICache.Unlock()
		return r, nil
	})
	if e != nil {
		return nil, investigationABI{}, e
	}
	rec = result.(investigationABI)
	a, e := method.ParseABI(rec.ABI)
	return a, rec, e
}

const implementationSlot = "0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc"
const adminSlot = "0xb53127684a568b3173ae13b9f8a6016e243e63b6e8ee1178d6a717850b5d6103"
const beaconSlot = "0xa3f0ad74e5423aebfd80d3ef4346578335a9a72aeaee59ff6cb3582b35133d50"

func nonzeroAddress(data []byte) string {
	if len(data) != 32 {
		return ""
	}
	if new(big.Int).SetBytes(data).Sign() == 0 {
		return ""
	}
	for _, b := range data[:12] {
		if b != 0 {
			return ""
		}
	}
	return "0x" + hex.EncodeToString(data[12:])
}
func (s *Server) investigationProxy(ctx context.Context, q investigationRequest, p chainread.StatePoint) (map[string]any, error) {
	address, e := chain.ParseAddress(q.Address)
	if e != nil {
		return nil, e
	}
	out := map[string]any{"address": q.Address, "as_of_block": p.Block, "implementation": nil, "proxy_admin": nil, "beacon": nil, "proxy_type": nil, "evidence": []any{}}
	evidence := []any{}
	failures := map[string]string{}
	for name, slot := range map[string]string{"implementation": implementationSlot, "proxy_admin": adminSlot, "beacon": beaconSlot} {
		v, e := s.deps.ChainRead.StorageAt(ctx, q.Chain, address, slot, p.Ref, investigationOptions())
		if e != nil {
			failures[name] = rpc.AsError(e).Public()
			continue
		}
		if a := nonzeroAddress(v); a != "" {
			out[name] = a
			evidence = append(evidence, map[string]any{"method": "eth_getStorageAt", "slot": slot, "value": "0x" + hex.EncodeToString(v), "block": p.Block})
			if name == "implementation" {
				out["proxy_type"] = "eip1967"
			}
		}
	}
	if beacon, ok := out["beacon"].(string); ok {
		b, e := s.probeContract(ctx, q.Chain, beacon, "implementation()", nil, p)
		if e != nil {
			failures["beacon_implementation"] = rpc.AsError(e).Public()
		} else if a := nonzeroAddress(b); a != "" {
			out["implementation"] = a
			out["proxy_type"] = "eip1967_beacon"
			evidence = append(evidence, map[string]any{"method": "implementation()", "address": beacon, "raw": "0x" + hex.EncodeToString(b), "block": p.Block})
		}
	}
	code, e := s.deps.ChainRead.Code(ctx, q.Chain, address, p.Ref, investigationOptions())
	if e != nil {
		return nil, e
	}
	if len(code) == 45 && hex.EncodeToString(code[:10]) == "363d3d373d3d3d363d73" && hex.EncodeToString(code[30:]) == "5af43d82803e903d91602b57fd5bf3" {
		out["implementation"] = "0x" + hex.EncodeToString(code[10:30])
		out["proxy_type"] = "eip1167"
		evidence = append(evidence, map[string]any{"method": "eth_getCode", "code": "0x" + hex.EncodeToString(code), "block": p.Block})
	}
	out["evidence"] = evidence
	out["probe_errors"] = failures
	out["resolution_complete"] = len(failures) == 0
	out["is_proxy"] = out["implementation"] != nil
	return out, nil
}
func (s *Server) probeContract(ctx context.Context, c, address, signature string, args []any, p chainread.StatePoint) ([]byte, error) {
	m, e := signatureMethod(signature, nil)
	if e != nil {
		return nil, e
	}
	data, e := method.EncodeInput(m, args)
	if e != nil {
		return nil, e
	}
	bytes, _ := chain.HexToBytes(data)
	return s.deps.ChainRead.CallContract(ctx, chainread.ContractCall{Chain: c, To: chain.Address(address), Data: bytes, BlockRef: p.Ref}, investigationOptions())
}
func signatureMethod(signature string, outputs []string) (*method.ABIMethod, error) {
	types, e := method.SignatureParamTypes(signature)
	if e != nil {
		return nil, e
	}
	m := &method.ABIMethod{Name: strings.Split(signature, "(")[0], Signature: signature, Selector: method.SelectorOf(signature)}
	for i, t := range types {
		m.Inputs = append(m.Inputs, method.ABIField{Name: fmt.Sprintf("arg%d", i), Type: t})
	}
	for i, t := range outputs {
		parsed, e := method.SignatureParamTypes("out(" + t + ")")
		if e != nil || len(parsed) != 1 {
			return nil, fmt.Errorf("invalid output type")
		}
		m.Outputs = append(m.Outputs, method.ABIField{Name: fmt.Sprintf("result%d", i), Type: parsed[0]})
	}
	return m, nil
}
func (s *Server) investigationContract(ctx context.Context, q investigationRequest, op string, p chainread.StatePoint) (any, error) {
	if q.TxHash != "" && (op == "decode_transaction_input" || op == "decode_logs") {
		h, e := chain.ParseHash(q.TxHash)
		if e != nil {
			return nil, e
		}
		if op == "decode_transaction_input" {
			tx, e := s.deps.ChainRead.Transaction(ctx, q.Chain, h, investigationOptions())
			if e != nil {
				return nil, e
			}
			if tx == nil || tx.To == nil {
				return nil, fmt.Errorf("contract transaction not found")
			}
			q.Address = tx.To.String()
			q.Input = tx.Input
			if tx.BlockNumber != nil {
				qq := q
				qq.Timestamp = ""
				qq.Block = nil
				qq.BlockTag = fmt.Sprint(*tx.BlockNumber)
				p, e = s.statePoint(ctx, qq)
				if e != nil {
					return nil, e
				}
			}
		} else {
			receipt, e := s.deps.ChainRead.Receipt(ctx, q.Chain, h, investigationOptions())
			if e != nil {
				return nil, e
			}
			if receipt == nil {
				return nil, fmt.Errorf("receipt not found")
			}
			for _, l := range receipt.Logs {
				item := map[string]any{"address": l.Address.String(), "topics": l.Topics, "data": l.Data, "block_number": fmt.Sprint(l.BlockNumber), "tx_hash": l.TxHash.String(), "log_index": l.LogIndex, "removed": l.Removed}
				q.Logs = append(q.Logs, item)
			}
		}
	}
	if op == "decode_logs" {
		if len(q.Logs) > 200 {
			return nil, fmt.Errorf("at most 200 logs per call")
		}
		out := []any{}
		for _, log := range q.Logs {
			qq := q
			qq.Address = fmt.Sprint(log["address"])
			topics := []string{}
			if ts, ok := log["topics"].([]any); ok {
				for _, t := range ts {
					topics = append(topics, fmt.Sprint(t))
				}
			}
			if ts, ok := log["topics"].([]string); ok {
				topics = ts
			}
			block := log["block_number"]
			if block == nil {
				block = log["blockNumber"]
			}
			lp := p
			if block != nil {
				qq.Block = nil
				qq.Timestamp = ""
				qq.BlockTag = fmt.Sprint(block)
				var e error
				lp, e = s.statePoint(ctx, qq)
				if e != nil {
					out = append(out, map[string]any{"log": log, "decoded": false, "error": rpc.AsError(e).Public()})
					continue
				}
			}
			a, rec, e := s.contractABIAt(ctx, qq, lp)
			item := map[string]any{"log": log, "decoded": false}
			if e != nil {
				item["decode_error"] = "Contract ABI or historical proxy resolution unavailable"
			}
			if e == nil && len(topics) > 0 {
				if event, ok := a.Event(topics[0]); ok {
					params, e := method.DecodeEventLog(event, topics, fmt.Sprint(log["data"]))
					if e == nil {
						item["decoded"] = true
						item["event"] = event.Name
						item["signature"] = event.Signature
						item["parameters"] = params
						item["abi_source"] = rec.Source
						item["verified"] = rec.Verified
					} else {
						item["error"] = e.Error()
					}
				}
			}
			out = append(out, item)
		}
		return map[string]any{"items": out, "as_of_block": p.Block}, nil
	}
	if q.Address == "" {
		return nil, fmt.Errorf("address is required")
	}
	if _, e := chain.ParseAddress(q.Address); e != nil {
		return nil, e
	}
	if op == "resolve_proxy_implementation" {
		return s.investigationProxy(ctx, q, p)
	}
	if op == "get_contract_control" {
		return s.investigationControl(ctx, q, p)
	}
	a, rec, e := s.contractABIAt(ctx, q, p)
	if op == "get_contract_abi" {
		if e != nil {
			return nil, e
		}
		return rec, nil
	}
	if op == "decode_transaction_input" {
		if len(q.Input) < 10 {
			return nil, fmt.Errorf("input must contain a four-byte selector")
		}
		selector := strings.ToLower(q.Input[:10])
		out := map[string]any{"address": q.Address, "method_id": selector, "input": q.Input, "decoded": false, "as_of_block": p.Block}
		if e == nil {
			if m, ok := a.Lookup(selector); ok {
				params, e := method.DecodeInput(m, q.Input)
				if e != nil {
					return nil, e
				}
				out["decoded"] = true
				out["function"] = m.Name
				out["signature"] = m.Signature
				out["parameters"] = params
				out["abi_source"] = rec.Source
				out["verified"] = rec.Verified
				return out, nil
			}
		}
		out["signature_candidates"] = s.deps.Registry.Signatures(selector)
		out["reason"] = "No unique contract ABI match; selector candidates are not proof of a function"
		return out, nil
	}
	if op == "read_contract" {
		var m *method.ABIMethod
		if e == nil {
			for _, list := range a.Methods {
				for _, candidate := range list {
					if candidate.Name == q.Function || candidate.Signature == q.Function {
						if m != nil {
							return nil, fmt.Errorf("overloaded function requires full canonical signature")
						}
						m = candidate
					}
				}
			}
		}
		if m == nil {
			if !strings.Contains(q.Function, "(") {
				return nil, fmt.Errorf("unknown ABI; provide full function signature and output_types or abi")
			}
			m, e = signatureMethod(q.Function, q.OutputTypes)
			if e != nil {
				return nil, e
			}
			rec = investigationABI{Source: "caller_signature", Address: q.Address}
		}
		if len(q.Args) > 1024 {
			return nil, fmt.Errorf("too many arguments")
		}
		input, e := method.EncodeInput(m, q.Args)
		if e != nil {
			return nil, e
		}
		result, e := s.deps.ChainRead.ReadRPC(ctx, q.Chain, "eth_call", []any{map[string]any{"to": q.Address, "data": input}, p.Ref.String()}, investigationOptions())
		if e != nil {
			return nil, e
		}
		var raw string
		if json.Unmarshal(result.Result, &raw) != nil {
			return nil, fmt.Errorf("invalid call result")
		}
		out := map[string]any{"address": q.Address, "signature": m.Signature, "input": input, "raw_output": raw, "as_of_block": p.Block, "abi_source": rec.Source, "verified": rec.Verified, "simulation": true}
		if len(m.Outputs) > 0 {
			decoded, e := method.DecodeOutput(m, raw)
			if e != nil {
				return nil, e
			}
			out["output"] = decoded
		}
		return out, nil
	}
	return nil, fmt.Errorf("unknown contract operation")
}
func (s *Server) contractABIAt(ctx context.Context, q investigationRequest, p chainread.StatePoint) (*method.ABI, investigationABI, error) {
	if len(q.ABI) > 0 {
		return s.investigationABI(ctx, q, q.Address)
	}
	proxy, e := s.investigationProxy(ctx, q, p)
	if e != nil {
		return nil, investigationABI{}, e
	}
	address := q.Address
	if impl, ok := proxy["implementation"].(string); ok {
		address = impl
	}
	return s.investigationABI(ctx, q, address)
}
func (s *Server) investigationControl(ctx context.Context, q investigationRequest, p chainread.StatePoint) (map[string]any, error) {
	proxy, e := s.investigationProxy(ctx, q, p)
	if e != nil {
		return nil, e
	}
	code, e := s.deps.ChainRead.Code(ctx, q.Chain, chain.Address(q.Address), p.Ref, investigationOptions())
	if e != nil {
		return nil, e
	}
	out := map[string]any{"address": q.Address, "as_of_block": p.Block, "is_contract": len(code) > 0, "owner": nil, "admin": nil, "deployer": nil, "proxy_admin": proxy["proxy_admin"], "implementation": proxy["implementation"], "upgradeability": map[string]any{"proxy_detected": proxy["is_proxy"], "type": proxy["proxy_type"], "complete": false}, "multisig_signers": []any{}, "privileged_roles": []any{}, "permission_candidates": []any{}, "evidence": proxy["evidence"], "analysis_complete": false}
	evidence := proxy["evidence"].([]any)
	errors := map[string]string{}
	for _, name := range []string{"owner", "admin"} {
		raw, e := s.probeContract(ctx, q.Chain, q.Address, name+"()", nil, p)
		if e != nil {
			errors[name] = rpc.AsError(e).Public()
			continue
		}
		if a := nonzeroAddress(raw); a != "" {
			out[name] = a
			evidence = append(evidence, map[string]any{"function": name + "()", "raw": "0x" + hex.EncodeToString(raw), "block": p.Block})
		}
	}
	for _, spec := range []struct{ Name, Signature, Output string }{{"multisig_signers", "getOwners()", "address[]"}, {"multisig_threshold", "getThreshold()", "uint256"}} {
		raw, e := s.probeContract(ctx, q.Chain, q.Address, spec.Signature, nil, p)
		if e != nil {
			continue
		}
		m, _ := signatureMethod(spec.Signature, []string{spec.Output})
		params, e := method.DecodeOutput(m, "0x"+hex.EncodeToString(raw))
		if e == nil && len(params) == 1 {
			out[spec.Name] = params[0].Value
			evidence = append(evidence, map[string]any{"function": spec.Signature, "raw": "0x" + hex.EncodeToString(raw), "block": p.Block})
		}
	}
	a, rec, e := s.contractABIAt(ctx, q, p)
	if e == nil {
		out["abi_source"] = rec.Source
		out["abi_verified"] = rec.Verified
		capabilities := []any{}
		roles := []any{}
		names := []string{}
		for _, list := range a.Methods {
			for _, m := range list {
				names = append(names, m.Signature)
				lower := strings.ToLower(m.Name)
				if strings.Contains(lower, "pause") || strings.Contains(lower, "withdraw") || strings.Contains(lower, "mint") || strings.Contains(lower, "upgrade") {
					capabilities = append(capabilities, map[string]any{"function": m.Signature, "authorization": "unproven", "source": "contract_abi", "note": "Function existence does not establish who may call it"})
				}
				if strings.HasSuffix(m.Name, "_ROLE") && len(m.Inputs) == 0 && len(roles) < 32 {
					raw, e := s.probeContract(ctx, q.Chain, q.Address, m.Signature, nil, p)
					if e != nil || len(raw) != 32 {
						continue
					}
					role := "0x" + hex.EncodeToString(raw)
					roles = append(roles, s.enumerableRole(ctx, q, p, m.Name, role))
				}
			}
		}
		sort.Strings(names)
		roles = append(roles, s.enumerableRole(ctx, q, p, "DEFAULT_ADMIN_ROLE", "0x"+strings.Repeat("0", 64)))
		out["privileged_roles"] = roles
		out["permission_candidates"] = capabilities
		var deployment struct {
			TransactionHash string `json:"transactionHash"`
		}
		if json.Unmarshal(rec.Deployment, &deployment) == nil && deployment.TransactionHash != "" {
			h, e := chain.ParseHash(deployment.TransactionHash)
			if e == nil {
				receipt, e := s.deps.ChainRead.Receipt(ctx, q.Chain, h, investigationOptions())
				if e == nil && receipt != nil && receipt.ContractAddress != nil && strings.EqualFold(receipt.ContractAddress.String(), rec.Address) {
					tx, e := s.deps.ChainRead.Transaction(ctx, q.Chain, h, investigationOptions())
					if e == nil && tx != nil {
						out["deployer"] = map[string]any{"address": tx.From, "contract": rec.Address, "tx_hash": deployment.TransactionHash, "source": rec.Source, "scope": "top_level_creation_sender"}
					}
				}
			}
		}
	}
	if admin, ok := out["proxy_admin"].(string); ok && admin != q.Address {
		raw, e := s.probeContract(ctx, q.Chain, admin, "owner()", nil, p)
		if e == nil {
			out["proxy_admin_owner"] = nonzeroAddress(raw)
			evidence = append(evidence, map[string]any{"address": admin, "function": "owner()", "raw": "0x" + hex.EncodeToString(raw), "block": p.Block})
		}
	}
	out["probe_errors"] = errors
	out["evidence"] = evidence
	out["limitations"] = []string{"Custom storage, non-enumerable roles and authorization logic require source review", "A zero owner does not prove absence of privileged control"}
	return out, nil
}
func (s *Server) enumerableRole(ctx context.Context, q investigationRequest, p chainread.StatePoint, name, role string) map[string]any {
	out := map[string]any{"name": name, "role": role, "members": []any{}, "enumeration_complete": false}
	raw, e := s.probeContract(ctx, q.Chain, q.Address, "getRoleMemberCount(bytes32)", []any{role}, p)
	if e != nil {
		return out
	}
	count, e := chainread.DecodeUint(raw)
	if e != nil || !count.IsUint64() {
		return out
	}
	n := count.Uint64()
	limit := n
	if limit > 100 {
		limit = 100
	}
	members := []any{}
	for i := uint64(0); i < limit; i++ {
		raw, e = s.probeContract(ctx, q.Chain, q.Address, "getRoleMember(bytes32,uint256)", []any{role, strconvUint(i)}, p)
		if e != nil {
			break
		}
		a := nonzeroAddress(raw)
		if a == "" {
			break
		}
		members = append(members, a)
	}
	out["member_count"] = count.String()
	out["members"] = members
	out["enumeration_complete"] = uint64(len(members)) == n
	out["source"] = "onchain_getRoleMember"
	return out
}
func strconvUint(n uint64) string { return fmt.Sprintf("%d", n) }
