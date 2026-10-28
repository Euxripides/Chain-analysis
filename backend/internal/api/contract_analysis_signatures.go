package api

import (
	"context"
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

// contractOpenchainLookup returns candidates per selector for one batch; selectors absent from the map failed.
func contractOpenchainLookup(ctx context.Context, selectors []string) map[string][]string {
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, contractOpenchainURL+"?"+url.Values{"filter": {"true"}, "function": {strings.Join(selectors, ",")}}.Encode(), nil)
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
		OK     bool `json:"ok"`
		Result struct {
			Function map[string][]struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &body) != nil || !body.OK {
		return nil
	}
	out := map[string][]string{}
	for sel, list := range body.Result.Function {
		names := []string{}
		for _, item := range list {
			names = append(names, item.Name)
		}
		out[strings.ToLower(sel)] = contractValidSignatures(strings.ToLower(sel), names)
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
		for sel, names := range contractOpenchainLookup(ctx, batch) {
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
