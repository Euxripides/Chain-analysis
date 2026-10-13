package crosschain

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

type Config struct {
	NearJWT, LifiKey, AcrossKey, RelayKey string
	// Bases is a test seam; production URLs are fixed, never supplied by the caller.
	Bases map[string]string
}

type Service struct {
	config     Config
	client     *http.Client
	slots      chan struct{}
	candidates chan struct{}
	mu         sync.Mutex
	nearNext   time.Time
}

func New(cfg Config) *Service {
	return &Service{config: cfg, client: &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, slots: make(chan struct{}, 4), candidates: make(chan struct{}, 2)}
}

func (s *Service) AcquireCandidate(ctx context.Context) (func(), error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	select {
	case s.candidates <- struct{}{}:
		return func() { <-s.candidates }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func FromEnv() *Service {
	return New(Config{NearJWT: os.Getenv("CROSSCHAIN_NEAR_JWT"), LifiKey: os.Getenv("CROSSCHAIN_LIFI_API_KEY"), AcrossKey: os.Getenv("CROSSCHAIN_ACROSS_API_KEY"), RelayKey: os.Getenv("CROSSCHAIN_RELAY_API_KEY")})
}

type Capability struct {
	Bridge        string   `json:"bridge"`
	Implemented   bool     `json:"implemented"`
	Configured    bool     `json:"configured"`
	Queries       []string `json:"queries"`
	EvidenceScope string   `json:"evidence_scope"`
	Credential    string   `json:"credential_setting,omitempty"`
}

func (s *Service) Capabilities() []Capability {
	return []Capability{
		{"lifi", true, true, []string{"source_or_destination_tx_hash"}, "records indexed by LI.FI; bridge directory is not complete historical coverage", "CROSSCHAIN_LIFI_API_KEY (optional)"},
		{"near_intents", true, s.config.NearJWT != "", []string{"tx_hash", "deposit_address_and_optional_memo"}, "1Click Explorer records; HOT treasury labels do not independently link orders", "CROSSCHAIN_NEAR_JWT"},
		{"bridgers", true, true, []string{"order_id"}, "read existing order details only; no channel/device identity is guessed", ""},
		{"layerzero", true, true, []string{"tx_hash"}, "message association; token amounts require application event decoding", ""},
		{"cbridge", true, true, []string{"transfer_id"}, "existing transfer status and source/destination links", ""},
		{"across", true, true, []string{"deposit_tx_hash"}, "public read lookup; configured bearer key is optional, permission errors remain explicit", "CROSSCHAIN_ACROSS_API_KEY (optional)"},
		{"relay", true, s.config.RelayKey != "", []string{"source_or_destination_tx_hash", "order_id"}, "Requests v3; visibility depends on partner permissions", "CROSSCHAIN_RELAY_API_KEY"},
		{"wormhole", true, true, []string{"source_or_destination_tx_hash"}, "Wormholescan operations and emitter/sequence; normalized amounts require token precision verification", ""},
		{"cctp", true, true, []string{"source_tx_hash", "v2_evm_destination_tx_hash_with_receipt_nonce"}, "Circle messages on mapped mainnet domains; reverse lookup decodes official V2 EVM receipts, attestation status alone does not establish mint completion", ""},
	}
}

type upstreamError struct {
	status string
	reason string
}

func (e *upstreamError) Error() string { return e.reason }

func (s *Service) fetch(ctx context.Context, protocol, base, path string, values url.Values, body any, headers map[string]string) (any, Evidence, error) {
	if override := s.config.Bases[protocol]; override != "" {
		base = override
	}
	u := strings.TrimRight(base, "/") + path
	if len(values) > 0 {
		u += "?" + values.Encode()
	}
	ev := Evidence{Source: u, FetchedAt: time.Now().UTC(), MatchedIdentifiers: []string{}}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-ctx.Done():
		return nil, ev, &upstreamError{"timeout", "bridge query budget expired"}
	}
	if protocol == "near_intents" {
		s.mu.Lock()
		now := time.Now()
		if now.Before(s.nearNext) {
			s.mu.Unlock()
			return nil, ev, &upstreamError{"rate_limited", "NEAR Explorer is limited to one request per five seconds"}
		}
		s.nearNext = now.Add(5 * time.Second)
		s.mu.Unlock()
	}
	method := "GET"
	var reader io.Reader
	if body != nil {
		method = "POST"
		raw, e := json.Marshal(body)
		if e != nil {
			return nil, ev, e
		}
		reader = bytes.NewReader(raw)
	}
	req, e := http.NewRequestWithContext(ctx, method, u, reader)
	if e != nil {
		return nil, ev, e
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "blockchain-platform-crosschain/1.0")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, e := s.client.Do(req)
	if e != nil {
		if errors.Is(e, context.DeadlineExceeded) || ctx.Err() != nil {
			return nil, ev, &upstreamError{"timeout", "bridge query budget expired"}
		}
		return nil, ev, &upstreamError{"upstream_error", "bridge endpoint could not be read within the request budget"}
	}
	defer resp.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if e != nil || len(raw) > 2<<20 {
		return nil, ev, &upstreamError{"invalid_response", "bridge response exceeds 2 MiB or could not be read"}
	}
	h := sha256.Sum256(raw)
	ev.FetchedAt = time.Now().UTC()
	ev.SHA256 = hex.EncodeToString(h[:])
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return nil, ev, &upstreamError{"unauthorized", "bridge API credentials are missing, expired or lack access"}
	}
	if resp.StatusCode == 429 {
		return nil, ev, &upstreamError{"rate_limited", "bridge API rate limit reached"}
	}
	if resp.StatusCode == 404 {
		return nil, ev, &upstreamError{"not_found", "bridge API returned no record for the supplied identifier"}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, ev, &upstreamError{"upstream_error", fmt.Sprintf("bridge API returned HTTP %d", resp.StatusCode)}
	}
	var out any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if e := decoder.Decode(&out); e != nil {
		return nil, ev, &upstreamError{"invalid_response", "bridge API did not return readable JSON"}
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, ev, &upstreamError{"invalid_response", "bridge API returned trailing JSON"}
	}
	return out, ev, nil
}

func (s *Service) Query(ctx context.Context, r QueryRequest) (QueryResult, error) {
	if e := r.Validate(); e != nil {
		return QueryResult{}, e
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	out := QueryResult{Transfers: []Transfer{}, Attempts: []Attempt{}, ProtocolHints: r.Hints, CandidateSearches: []CandidateSearch{}, SearchedComplete: true}
	if out.ProtocolHints == nil {
		out.ProtocolHints = []ProtocolHint{}
	}
	selected := []string{r.Bridge}
	if r.Bridge == "auto" {
		selected = []string{"lifi", "layerzero", "across", "wormhole", "cctp", "near_intents", "relay"}
		// Receipt clues choose which independent protocol gets the first attempt.
		for i := len(r.Hints) - 1; i >= 0; i-- {
			p := r.Hints[i].Bridge
			for j, item := range selected {
				if item == p {
					selected = append([]string{p}, append(selected[:j], selected[j+1:]...)...)
					break
				}
			}
		}
	}
	for _, p := range selected {
		if ctx.Err() != nil {
			out.Attempts = append(out.Attempts, Attempt{p, "timeout", "query budget exhausted"})
			out.SearchedComplete = false
			continue
		}
		var transfers []Transfer
		var e error
		switch p {
		case "lifi":
			transfers, e = s.lifi(ctx, r)
		case "near_intents":
			transfers, e = s.near(ctx, r)
		case "bridgers":
			transfers, e = s.bridgers(ctx, r)
		case "layerzero":
			transfers, e = s.layerzero(ctx, r)
		case "cbridge":
			transfers, e = s.cbridge(ctx, r)
		case "across":
			transfers, e = s.across(ctx, r)
		case "relay":
			transfers, e = s.relay(ctx, r)
		case "wormhole":
			transfers, e = s.wormhole(ctx, r)
		case "cctp":
			transfers, e = s.cctp(ctx, r)
		}
		if e != nil {
			status := "invalid_response"
			reason := "bridge response could not be parsed"
			if up, ok := e.(*upstreamError); ok {
				status = up.status
				reason = up.reason
			}
			out.Attempts = append(out.Attempts, Attempt{p, status, reason})
			if status != "not_found" {
				out.SearchedComplete = false
			}
			continue
		}
		filtered := []Transfer{}
		for _, t := range transfers {
			if t.Evidence.PageTruncated {
				out.Truncated = true
				out.SearchedComplete = false
			}
			t.Bridge = p
			t.OnChainVerified = false
			t.Verification = []Verification{}
			t.Association = "protocol_linked"
			if t.Source == nil {
				t.Source = []Leg{}
			}
			if t.Destination == nil {
				t.Destination = []Leg{}
			}
			if t.Refund == nil {
				t.Refund = []Leg{}
			}
			if matched := matchQuery(r, t); len(matched) > 0 {
				t.Evidence.MatchedIdentifiers = matched
				filtered = append(filtered, t)
			}
		}
		status := "not_found"
		reason := "no exact matching record in the returned page"
		if len(filtered) > 0 {
			status = "found"
			reason = ""
		}
		if len(transfers) > 0 && len(filtered) == 0 {
			status = "unrelated_response"
			out.SearchedComplete = false
		}
		out.Attempts = append(out.Attempts, Attempt{p, status, reason})
		out.Transfers = append(out.Transfers, filtered...)
		if len(transfers) > r.MaxResults {
			out.Truncated = true
			out.SearchedComplete = false
		}
		if r.Bridge == "auto" && completeProtocolPair(filtered) {
			out.StoppedAfterMatch = true
			out.SearchedComplete = false
			break
		}
	}
	if len(out.Transfers) > r.MaxResults {
		out.Transfers = out.Transfers[:r.MaxResults]
		out.Truncated = true
		out.SearchedComplete = false
	}
	return out, nil
}

func completeProtocolPair(transfers []Transfer) bool {
	for _, t := range transfers {
		if len(t.Source) == 0 || len(t.Destination) == 0 {
			continue
		}
		complete := true
		// A cross-chain hash echo needs chain evidence. Continue other adapters;
		// identical strings alone neither establish a pair nor prove it is false.
		for _, a := range t.Source {
			for _, z := range t.Destination {
				if a.Chain == z.Chain || strings.EqualFold(a.TxHash, z.TxHash) {
					complete = false
				}
			}
		}
		for _, legs := range [][]Leg{t.Source, t.Destination} {
			for _, leg := range legs {
				if _, err := Hash(leg.Chain, leg.TxHash); err != nil || leg.AmountsScope == "message_only" {
					complete = false
				}
			}
		}
		if complete {
			return true
		}
	}
	return false
}

func matchQuery(r QueryRequest, t Transfer) []string {
	matched := []string{}
	if r.TxHash != "" {
		found := false
		for _, legs := range [][]Leg{t.Source, t.Destination, t.Refund} {
			for _, leg := range legs {
				if leg.Chain == r.Chain && leg.TxHash != "" && Same(r.Chain, leg.TxHash, r.TxHash) {
					found = true
				}
			}
		}
		if !found {
			return nil
		}
		matched = append(matched, "chain+tx_hash")
	}
	if r.OrderID != "" {
		if t.OrderID != r.OrderID {
			return nil
		}
		matched = append(matched, "order_id")
	}
	if r.TransferID != "" {
		if !strings.EqualFold(strings.TrimPrefix(t.OrderID, "0x"), strings.TrimPrefix(r.TransferID, "0x")) {
			return nil
		}
		matched = append(matched, "transfer_id")
	}
	if r.DepositAddress != "" {
		found := false
		for _, v := range t.Evidence.MatchedIdentifiers {
			if v == "deposit_address" {
				found = true
			}
		}
		if !found {
			return nil
		}
		matched = append(matched, "deposit_address")
		if r.DepositMemo != "" {
			matched = append(matched, "deposit_memo")
		}
	}
	return matched
}

func obj(v any) map[string]any { m, _ := v.(map[string]any); return m }
func arr(v any) []any          { a, _ := v.([]any); return a }
func str(v any) string {
	switch n := v.(type) {
	case string:
		return n
	case json.Number:
		return n.String()
	case nil:
		return ""
	default:
		return ""
	}
}
func get(v any, keys ...string) any {
	for _, k := range keys {
		v = obj(v)[k]
	}
	return v
}
func chainKey(v any) string {
	value := strings.ToLower(str(v))
	aliases := map[string]string{"1": "ethereum", "56": "bsc", "137": "polygon", "42161": "arbitrum", "10": "optimism", "8453": "base", "43114": "avalanche", "1151111081099710": "solana", "11111111111111": "solana", "728126428": "tron", "eth": "ethereum", "sol": "solana", "arb": "arbitrum", "op": "optimism", "pol": "polygon", "avax": "avalanche"}
	if c := aliases[value]; c != "" {
		return c
	}
	return value
}
func normalizeStatus(v string) string {
	switch strings.ToLower(v) {
	case "done", "success", "succeeded", "completed", "receive_complete", "filled", "delivered":
		return "completed"
	case "failed", "failure", "error", "transfer_failed":
		return "failed"
	case "refund", "refunded", "refund_complete":
		return "refunded"
	case "not_found", "invalid":
		return "not_found"
	default:
		return "pending"
	}
}
func emptyLeg(c, h string) Leg {
	normalized, e := Hash(c, h)
	if e == nil {
		h = normalized
	}
	return Leg{Chain: c, TxHash: h}
}
