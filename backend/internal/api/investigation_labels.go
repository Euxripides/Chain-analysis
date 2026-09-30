package api

import (
	"context"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
	"github.com/blockchain-platform/blockchain-platform/internal/method"
	"github.com/blockchain-platform/blockchain-platform/internal/query"
	"strings"
	"time"
)

//go:embed exchange_labels.json
var investigationExchangeLabels []byte

func exchangeLabels(c, address string) []map[string]any {
	var records []map[string]any
	json.Unmarshal(investigationExchangeLabels, &records)
	out := []map[string]any{}
	for _, v := range records {
		if v["chain"] == c && strings.EqualFold(fmt.Sprint(v["address"]), address) {
			out = append(out, v)
		}
	}
	return out
}
func (s *Server) investigationLabels(ctx context.Context, q investigationRequest) (map[string]any, error) {
	if q.Address == "" {
		return nil, fmt.Errorf("address is required")
	}
	labels := exchangeLabels(q.Chain, q.Address)
	evidence := []any{}
	errors := []string{}
	scope, e := s.systemScope(ctx)
	if e != nil {
		return nil, summaryError(500, "INTERNAL_ERROR", "Unable to resolve workspace")
	}
	if s.deps.Store != nil {
		ws, e := s.deps.Store.GetWorkspaceAddress(ctx, scope.WorkspaceID, q.Chain, q.Address)
		if e != nil {
			return nil, summaryError(500, "INTERNAL_ERROR", "Unable to read workspace labels")
		}
		if ws != nil && ws.DisplayName != "" {
			labels = append(labels, map[string]any{"label": ws.DisplayName, "category": "known_project_or_workspace_address", "entity": nil, "label_source": "workspace_address", "confidence": nil, "first_seen": ws.FirstDataAt, "observed_at": ws.UpdatedAt, "attribution": "user_supplied"})
		}
	}
	if mustChain(s, q.Chain).IsEVM() {
		p, e := s.statePoint(ctx, q)
		if e != nil {
			return nil, e
		}
		code, e := s.deps.ChainRead.Code(ctx, q.Chain, chain.Address(q.Address), p.Ref, investigationOptions())
		if e != nil {
			return nil, e
		}
		evidence = append(evidence, map[string]any{"method": "eth_getCode", "block": p.Block, "code_size": len(code)})
		add := func(name string, confidence float64, proof any) {
			labels = append(labels, map[string]any{"label": name, "category": name, "label_source": "onchain_probe", "confidence": confidence, "first_seen": nil, "as_of_block": p.Block, "evidence": proof})
		}
		if len(code) > 0 {
			add("contract", 1, evidence[0])
			proxy, e := s.investigationProxy(ctx, q, p)
			if e == nil && proxy["is_proxy"] == true {
				add("proxy", 1, proxy)
			}
			owners, e := s.probeContract(ctx, q.Chain, q.Address, "getOwners()", nil, p)
			if e == nil {
				m, _ := signatureMethod("getOwners()", []string{"address[]"})
				decoded, e := method.DecodeOutput(m, "0x"+hex.EncodeToString(owners))
				if e == nil {
					add("multisig", 0.8, decoded)
				}
			}
			decimals, e := s.probeContract(ctx, q.Chain, q.Address, "decimals()", nil, p)
			if e == nil {
				if d, e := chainread.DecodeUint8(decimals); e == nil {
					if total, e := s.probeContract(ctx, q.Chain, q.Address, "totalSupply()", nil, p); e == nil && len(total) == 32 {
						add("token_contract", 0.9, map[string]any{"decimals": d, "total_supply_raw": "0x" + hex.EncodeToString(total)})
					}
				}
			}
			pair := map[string]any{}
			for _, f := range []string{"token0", "token1", "factory"} {
				raw, e := s.probeContract(ctx, q.Chain, q.Address, f+"()", nil, p)
				if e == nil {
					if address := nonzeroAddress(raw); address != "" {
						pair[f] = address
					}
				}
			}
			if len(pair) == 3 {
				add("dex_pair", 0.9, pair)
			} else {
				if pair["factory"] != nil {
					raw, e := s.probeContract(ctx, q.Chain, q.Address, "WETH()", nil, p)
					if e == nil && nonzeroAddress(raw) != "" {
						add("router", 0.85, map[string]any{"factory": pair["factory"], "WETH": nonzeroAddress(raw)})
					}
				}
			}
			a, rec, e := s.contractABIAt(ctx, q, p)
			if e == nil {
				for _, list := range a.Methods {
					for _, m := range list {
						lower := strings.ToLower(m.Name)
						if strings.Contains(lower, "bridge") || strings.Contains(lower, "crosschain") {
							add("bridge_candidate", 0.5, map[string]any{"function": m.Signature, "abi_source": rec.Source})
							break
						}
					}
				}
			}
		} else {
			add("externally_owned_or_empty_address", 0.8, evidence[0])
		}
	} else {
		errors = append(errors, "Onchain contract probes currently support EVM chains")
	}
	return map[string]any{"chain": q.Chain, "address": q.Address, "labels": labels, "evidence": evidence, "registry_version": "2026-09-30", "registry_complete": false, "classification_errors": errors, "note": "Unlabeled addresses remain unknown; first_seen is null when the label source does not establish it"}, nil
}
func (s *Server) investigationLabelsDispatch(ctx context.Context, q investigationRequest, op string) (any, map[string]any, error) {
	if op == "get_labels" {
		data, e := s.investigationLabels(ctx, q)
		meta := investigationMeta(nil)
		meta["source"] = "public_labels_workspace_and_onchain_probes"
		meta["coverage"] = map[string]any{"complete": false, "reason": "Versioned public label registry and heuristic classifications are partial"}
		return data, meta, e
	}
	if q.Source != "" {
		q.Address = q.Source
	}
	if q.Address == "" {
		return nil, nil, fmt.Errorf("address/source is required")
	}
	if !mustChain(s, q.Chain).IsEVM() {
		return nil, nil, fmt.Errorf("exchange path detection currently requires an EVM chain")
	}
	direct := exchangeLabels(q.Chain, q.Address)
	labelMeta := investigationMeta(nil)
	labelMeta["source"] = "versioned_public_exchange_registry"
	labelMeta["coverage"] = map[string]any{"complete": false, "reason": "Public exchange label registry is partial"}
	if len(direct) > 0 {
		return map[string]any{"address": q.Address, "classification": "direct_exchange_wallet", "labels": direct, "paths": []any{map[string]any{"addresses": []string{q.Address}, "exchange_label": direct[0]}}, "registry_complete": false}, labelMeta, nil
	}
	if q.MaxHops == 0 {
		q.MaxHops = 3
	}
	if op == "detect_exchange_deposit" {
		q.MaxHops = 1
	}
	if q.MaxHops < 1 || q.MaxHops > 5 {
		return nil, nil, fmt.Errorf("max_hops must be 1-5")
	}
	if q.MaxNodes == 0 {
		q.MaxNodes = 50
	}
	if q.MaxNodes < 1 || q.MaxNodes > 200 {
		return nil, nil, fmt.Errorf("max_nodes must be 1-200")
	}
	p, e := s.statePoint(ctx, q)
	if e != nil {
		return nil, nil, e
	}
	start, e := parseInvestigationTime(q.StartTime)
	if e != nil {
		return nil, nil, e
	}
	end, e := parseInvestigationTime(q.EndTime)
	if e != nil {
		return nil, nil, e
	}
	type node struct {
		Address  string
		Path     []string
		Proof    []any
		Earliest *time.Time
		Token    string
	}
	queue := []node{{Address: q.Address, Path: []string{q.Address}, Earliest: start, Token: q.TokenContract}}
	watermark := time.Now().UTC()
	seen := map[string]bool{q.Address: true}
	paths := []any{}
	truncated := false
	visited := []string{}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		visited = append(visited, n.Address)
		if len(n.Path)-1 >= q.MaxHops {
			continue
		}
		offset := int64(0)
		for {
			r, e := s.query.InvestigationAggregate(ctx, query.AggregateRequest{ActionFilter: query.ActionFilter{Chain: q.Chain, Address: n.Address, Direction: "out", Token: q.Token, TokenContract: n.Token, StartTime: n.Earliest, EndTime: end, PageSize: 100}, GroupBy: []string{"counterparty"}, OrderBy: "sum_amount", Offset: offset, Cutoff: p.Block, IngestedBefore: watermark})
			if e != nil {
				return nil, nil, e
			}
			for _, item := range r["items"].([]map[string]any) {
				var key []string
				if json.Unmarshal([]byte(item["group_key"].(string)), &key) != nil || len(key) < 1 {
					continue
				}
				cp := key[0]
				path := append(append([]string{}, n.Path...), cp)
				proof := append(append([]any{}, n.Proof...), map[string]any{"from_address": n.Address, "to_address": cp, "aggregate": item, "evidence_query": map[string]any{"tool": "get_token_transfers", "chain": q.Chain, "address": n.Address, "counterparty": cp, "token_contract": item["token_contract"], "direction": "out", "start_time": n.Earliest, "end_time": q.EndTime}})
				labels := exchangeLabels(q.Chain, cp)
				if len(labels) > 0 {
					if len(paths) >= 1000 {
						truncated = true
						continue
					}
					paths = append(paths, map[string]any{"addresses": path, "exchange_label": labels[0], "evidence": proof, "classification": "suspected_user_deposit_or_intermediary", "confidence": 0.5, "label_source": "indexed_transfer_path", "first_seen": item["first_time"], "caveat": "Transfer to an exchange wallet does not establish ownership or a dedicated user deposit account"})
					continue
				}
				if !seen[cp] && len(path)-1 < q.MaxHops {
					if len(seen) >= q.MaxNodes {
						truncated = true
						continue
					}
					seen[cp] = true
					first := item["first_time"].(time.Time)
					queue = append(queue, node{Address: cp, Path: path, Proof: proof, Earliest: &first, Token: item["token_contract"].(string)})
				}
			}
			if r["has_more"] != true {
				break
			}
			offset += 100
			if len(seen) >= q.MaxNodes || offset >= 10000 {
				truncated = true
				break
			}
		}
	}
	meta := s.actionCoverageMeta(ctx, q.Chain, visited, []string{"token_transfers"})
	classification := "unknown"
	if len(paths) > 0 {
		classification = "suspected_user_deposit_or_intermediary"
	}
	return map[string]any{"address": q.Address, "classification": classification, "paths": paths, "max_hops": q.MaxHops, "truncated": truncated, "registry_complete": false, "data_cutoff_block": p.Block, "note": "No path is not evidence of absence; registry and indexed coverage may be incomplete"}, meta, nil
}
