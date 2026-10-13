package api

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
	"github.com/blockchain-platform/blockchain-platform/internal/method"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

var selectorHTTP = &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
	if r.URL.Host != "www.4byte.directory" || r.URL.Scheme != "https" || len(via) > 1 {
		return fmt.Errorf("selector redirect denied")
	}
	return nil
}}
var selectorCache = struct {
	sync.Mutex
	items map[string]selectorCacheEntry
}{items: map[string]selectorCacheEntry{}}

type selectorCacheEntry struct {
	Values []string
	Until  time.Time
}

func lookupSelectors(ctx context.Context, selector string) []string {
	selectorCache.Lock()
	cached, ok := selectorCache.items[selector]
	selectorCache.Unlock()
	if ok && time.Now().Before(cached.Until) {
		return append([]string{}, cached.Values...)
	}
	req, e := http.NewRequestWithContext(ctx, "GET", "https://www.4byte.directory/api/v1/signatures/?hex_signature="+selector, nil)
	if e != nil {
		return nil
	}
	res, e := selectorHTTP.Do(req)
	if e != nil {
		return nil
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil
	}
	raw, e := io.ReadAll(io.LimitReader(res.Body, (256<<10)+1))
	if e != nil || len(raw) > 256<<10 {
		return nil
	}
	var response struct {
		Results []struct {
			Text string `json:"text_signature"`
			Hex  string `json:"hex_signature"`
		} `json:"results"`
	}
	if json.Unmarshal(raw, &response) != nil {
		return nil
	}
	out := []string{}
	seen := map[string]bool{}
	for _, entry := range response.Results {
		if len(out) >= 100 {
			break
		}
		if len(entry.Text) > 512 || !strings.EqualFold(entry.Hex, selector) || !strings.EqualFold(method.SelectorOf(entry.Text), selector) || seen[entry.Text] {
			continue
		}
		if _, e = signatureMethod(entry.Text, nil); e != nil {
			continue
		}
		seen[entry.Text] = true
		out = append(out, entry.Text)
	}
	selectorCache.Lock()
	if len(selectorCache.items) > 1000 {
		selectorCache.items = map[string]selectorCacheEntry{}
	}
	selectorCache.items[selector] = selectorCacheEntry{Values: out, Until: time.Now().Add(time.Hour)}
	selectorCache.Unlock()
	return out
}

// bytecodeSelectorEvidence walks instructions, skipping PUSH operands, and only
// considers a PUSH4 followed by EQ within a small dispatcher window. Still heuristic.
func bytecodeSelectorEvidence(code []byte, selector string) []map[string]any {
	target, e := chain.HexToBytes(selector)
	if e != nil || len(target) != 4 {
		return nil
	}
	out := []map[string]any{}
	for pc := 0; pc < len(code); pc++ {
		op := code[pc]
		if op >= 0x60 && op <= 0x7f {
			size := int(op - 0x5f)
			if pc+size >= len(code) {
				break
			}
			if op == 0x63 && string(code[pc+1:pc+5]) == string(target) {
				next := pc + 5
				for n := 0; n < 4 && next < len(code); n++ {
					if code[next] == 0x14 {
						out = append(out, map[string]any{"opcode": "PUSH4/EQ candidate", "pc": pc, "selector": selector, "source": "heuristic_bytecode", "verified": false, "confidence": nil})
						break
					}
					if code[next] >= 0x60 && code[next] <= 0x7f {
						next += int(code[next] - 0x5f)
					}
					next++
				}
			}
			pc += size
		}
	}
	return out
}

func (s *Server) selectorFallback(ctx context.Context, q investigationRequest, p chainread.StatePoint, selector string, out map[string]any) {
	candidates := s.deps.Registry.Signatures(selector)
	source := "local_selector_database"
	if len(candidates) == 0 {
		for _, signature := range lookupSelectors(ctx, selector) {
			m, e := signatureMethod(signature, nil)
			if e == nil {
				candidates = append(candidates, method.MethodCandidate{Name: m.Name, Signature: signature, Source: "4byte_directory", Verified: false})
			}
		}
		source = "4byte_selector_database"
	}
	out["signature_candidates"] = candidates
	out["selector_source"] = source
	out["verified"] = false
	out["confidence"] = nil
	out["abi_source"] = nil
	decoded := []any{}
	for _, candidate := range candidates {
		signature := candidate.Signature
		m, e := signatureMethod(signature, nil)
		if e != nil {
			continue
		}
		parameters, e := method.DecodeInput(m, q.Input)
		if e == nil {
			decoded = append(decoded, map[string]any{"signature": signature, "parameters": parameters, "attribution_type": "inferred", "verified": false, "confidence": nil})
		}
	}
	out["candidate_decodings"] = decoded
	code, e := s.deps.ChainRead.Code(ctx, q.Chain, chain.Address(q.Address), p.Ref, investigationOptions())
	if e == nil {
		out["bytecode_evidence"] = bytecodeSelectorEvidence(code, selector)
	}
	out["reason"] = "Selector database and bytecode candidates are hypotheses; collisions or matching calldata do not prove the contract function"
}
