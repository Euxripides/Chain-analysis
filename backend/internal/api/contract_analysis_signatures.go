package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
	"github.com/blockchain-platform/blockchain-platform/internal/method"
)

// Public signature candidates for selectors the engine could not name (closed-source bytecode). openchain is
// queried in batches first, then 4byte for the remainder. Every candidate must hash back to its selector; the
// result is a lookup hint stored as signature_candidates and never replaces a verified signature.

const contractSignatureBatch = 40

var (
	contractOpenchainURL  = "https://api.openchain.xyz/signature-database/v1/lookup"
	contractOpenchainHTTP = &http.Client{Timeout: 6 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return fmt.Errorf("signature lookup redirect denied")
	}}
	contractSignatureCache = struct {
		sync.Mutex
		items map[string]selectorCacheEntry
	}{items: map[string]selectorCacheEntry{}}
	// contractFourByte is the per-selector fallback; tests replace it.
	contractFourByte = lookupSelectors
)

func contractValidSignatures(selector string, candidates []string) []string {
	out := []string{}
	for _, sig := range candidates {
		if len(sig) <= 512 && strings.EqualFold(method.SelectorOf(sig), selector) && !contains(out, sig) {
			if _, e := signatureMethod(sig, nil); e == nil {
				out = append(out, sig)
			}
		}
	}
	return out
}

// contractOpenchainLookup returns candidates per hash for one batch of kind "function" (selectors) or "event"
// (topic0); hashes absent from the map failed.
func contractOpenchainLookup(ctx context.Context, kind string, hashes []string) map[string][]string {
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, contractOpenchainURL+"?"+url.Values{"filter": {"true"}, kind: {strings.Join(hashes, ",")}}.Encode(), nil)
	if e != nil {
		return nil
	}
	res, e := contractOpenchainHTTP.Do(req)
	if e != nil {
		return nil
	}
	defer res.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if e != nil || res.StatusCode != http.StatusOK || len(raw) > 1<<20 {
		return nil
	}
	var body struct {
		OK     bool                                          `json:"ok"`
		Result map[string]map[string][]struct{ Name string } `json:"result"`
	}
	if json.Unmarshal(raw, &body) != nil || !body.OK {
		return nil
	}
	out := map[string][]string{}
	for hash, list := range body.Result[kind] {
		names := []string{}
		for _, item := range list {
			names = append(names, item.Name)
		}
		if kind == "event" {
			out[strings.ToLower(hash)] = contractValidEvents(strings.ToLower(hash), names)
		} else {
			out[strings.ToLower(hash)] = contractValidSignatures(strings.ToLower(hash), names)
		}
	}
	return out
}

func contractValidEvents(topic string, candidates []string) []string {
	out := []string{}
	for _, sig := range candidates {
		if len(sig) <= 512 && strings.EqualFold(method.Topic0Of(sig), topic) && !contains(out, sig) {
			out = append(out, sig)
		}
	}
	return out
}

// contractResolveSelectors returns validated candidates and their source for each selector.
func contractResolveSelectors(ctx context.Context, selectors []string) map[string]map[string]any {
	out := map[string]map[string]any{}
	pending := []string{}
	contractSignatureCache.Lock()
	for _, sel := range selectors {
		if c, ok := contractSignatureCache.items[sel]; ok && time.Now().Before(c.Until) {
			if len(c.Values) > 0 {
				out[sel] = map[string]any{"candidates": c.Values, "source": "cache"}
			}
		} else {
			pending = append(pending, sel)
		}
	}
	contractSignatureCache.Unlock()
	resolved := map[string]map[string]any{}
	for i := 0; i < len(pending); i += contractSignatureBatch {
		batch := pending[i:min(i+contractSignatureBatch, len(pending))]
		for sel, names := range contractOpenchainLookup(ctx, "function", batch) {
			if len(names) > 0 {
				resolved[sel] = map[string]any{"candidates": names, "source": "openchain"}
			}
		}
	}
	for _, sel := range pending {
		if resolved[sel] == nil {
			if names := contractValidSignatures(sel, contractFourByte(ctx, sel)); len(names) > 0 {
				resolved[sel] = map[string]any{"candidates": names, "source": "4byte"}
			}
		}
		entry := resolved[sel]
		values := []string{}
		if entry != nil {
			values = entry["candidates"].([]string)
			out[sel] = entry
		}
		contractSignatureCache.Lock()
		if len(contractSignatureCache.items) > 4096 {
			contractSignatureCache.items = map[string]selectorCacheEntry{}
		}
		contractSignatureCache.items[sel] = selectorCacheEntry{Values: values, Until: time.Now().Add(6 * time.Hour)}
		contractSignatureCache.Unlock()
	}
	return out
}

// contractNameSelectors fills signature_candidates on unnamed entry methods and adds a coverage row.
func contractNameSelectors(ctx context.Context, result *ca.Result) {
	selectors := []string{}
	for _, raw := range result.Sections["methods"] {
		m := rowMap(raw)
		sel, _ := m["selector"].(string)
		if sig, _ := m["signature"].(string); len(sel) == 10 && sig == "" && !contains(selectors, strings.ToLower(sel)) {
			selectors = append(selectors, strings.ToLower(sel))
		}
	}
	if len(selectors) == 0 {
		return
	}
	names := contractResolveSelectors(ctx, selectors)
	for i, raw := range result.Sections["methods"] {
		m := rowMap(raw)
		sel, _ := m["selector"].(string)
		entry := names[strings.ToLower(sel)]
		if sig, _ := m["signature"].(string); entry == nil || sig != "" {
			continue
		}
		candidates := []map[string]any{}
		for _, sig := range entry["candidates"].([]string) {
			candidates = append(candidates, map[string]any{"signature": sig, "source": "public_signature_" + fmt.Sprint(entry["source"]), "verified": false})
		}
		m["signature_candidates"] = candidates
		m["candidate_name"] = method.NameOf(entry["candidates"].([]string)[0])
		result.Sections["methods"][i] = ca.Row(m)
	}
	result.Sections["coverage"] = append(result.Sections["coverage"], ca.Row(map[string]any{"dimension": "selector_names", "complete": len(names) == len(selectors),
		"unnamed_selectors": len(selectors), "named_by_public_signatures": len(names), "basis": "openchain then 4byte; candidates hash to the selector but are unverified"}))
}

// contractCodeTopics returns the distinct PUSH32 constants of runtime code (event topics live there), walking
// opcodes so push data is never read as an instruction.
func contractCodeTopics(code []byte, limit int) []string {
	out := []string{}
	for pc := 0; pc < len(code) && len(out) < limit; pc++ {
		op := code[pc]
		if op < 0x60 || op > 0x7f {
			continue
		}
		size := int(op - 0x5f)
		if op == 0x7f && pc+32 < len(code) {
			topic := "0x" + hex.EncodeToString(code[pc+1:pc+33])
			if strings.Trim(topic[2:], "0f") != "" && !contains(out, topic) {
				out = append(out, topic)
			}
		}
		pc += size
	}
	return out
}

// contractNameEvents adds event_signatures rows for PUSH32 constants that name a known event.
func contractNameEvents(ctx context.Context, result *ca.Result, bytecode string) {
	code, e := hex.DecodeString(strings.TrimPrefix(bytecode, "0x"))
	if e != nil || len(code) == 0 {
		return
	}
	topics, rows := contractCodeTopics(code, 128), []json.RawMessage{}
	resolved := map[string][]string{}
	pending := []string{}
	contractSignatureCache.Lock()
	for _, t := range topics {
		if c, ok := contractSignatureCache.items["event:"+t]; ok && time.Now().Before(c.Until) {
			resolved[t] = c.Values
		} else {
			pending = append(pending, t)
		}
	}
	contractSignatureCache.Unlock()
	for i := 0; i < len(pending); i += contractSignatureBatch {
		batch := pending[i:min(i+contractSignatureBatch, len(pending))]
		found := contractOpenchainLookup(ctx, "event", batch)
		contractSignatureCache.Lock()
		for _, t := range batch {
			if names, ok := found[t]; ok || found != nil {
				resolved[t] = names
				contractSignatureCache.items["event:"+t] = selectorCacheEntry{Values: names, Until: time.Now().Add(6 * time.Hour)}
			}
		}
		contractSignatureCache.Unlock()
	}
	for _, t := range topics {
		if names := resolved[t]; len(names) > 0 {
			rows = append(rows, ca.Row(map[string]any{"topic0": t, "signature": names[0], "candidates": names, "code_address": result.Snapshot.CodeAddress,
				"source": "public_signature_openchain", "verified": false}))
		}
	}
	if len(rows) > 0 {
		result.Sections["event_signatures"] = append(result.Sections["event_signatures"], rows...)
	}
}
