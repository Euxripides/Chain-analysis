package provider

import (
	"context"
	"encoding/json"
	"testing"
)

type stubProvider struct {
	name    string
	rows    string
	fetched []string
}

func (s *stubProvider) Name() string               { return s.name }
func (s *stubProvider) Capabilities() Capabilities { return Capabilities{Name: s.name} }
func (s *stubProvider) Health(context.Context) HealthStatus {
	return HealthStatus{Provider: s.name, State: HealthNormal}
}

func (s *stubProvider) Fetch(_ context.Context, req FetchRequest) (FetchResult, error) {
	s.fetched = append(s.fetched, req.Chain)
	return FetchResult{Rows: []json.RawMessage{json.RawMessage(s.rows)}}, nil
}

func TestRouterDispatchesByChainCaseInsensitively(t *testing.T) {
	fallback := &stubProvider{name: "ankr", rows: `{"src":"fallback"}`}
	solana := &stubProvider{name: "solana", rows: `{"src":"solana"}`}
	router := NewRouter(fallback, map[string]Provider{"solana": solana})

	for _, chain := range []string{"solana", "SOLANA", " Solana "} {
		res, err := router.Fetch(context.Background(), FetchRequest{Chain: chain})
		if err != nil {
			t.Fatalf("chain %q: unexpected error %v", chain, err)
		}
		if string(res.Rows[0]) != `{"src":"solana"}` {
			t.Fatalf("chain %q was not routed to the solana provider", chain)
		}
	}
	if len(solana.fetched) != 3 || len(fallback.fetched) != 0 {
		t.Fatalf("unexpected dispatch counts: solana=%v fallback=%v", solana.fetched, fallback.fetched)
	}

	if _, err := router.Fetch(context.Background(), FetchRequest{Chain: "bsc"}); err != nil {
		t.Fatalf("unregistered chain must fall back: %v", err)
	}
	if len(fallback.fetched) != 1 {
		t.Fatalf("fallback fetched %v, want exactly one call", fallback.fetched)
	}
	if router.Name() != "ankr" {
		t.Fatalf("router name must reflect the default provider, got %q", router.Name())
	}
}

func TestRouterWithoutFallbackFailsClosed(t *testing.T) {
	router := NewRouter(nil, nil)
	if _, err := router.Fetch(context.Background(), FetchRequest{Chain: "bsc"}); err == nil {
		t.Fatal("expected an error when no provider can serve the chain")
	}
}
