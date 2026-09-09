package tron

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

type scriptedProvider struct {
	name   string
	result provider.FetchResult
	err    error
	calls  []provider.FetchRequest
}

func (p *scriptedProvider) Name() string { return p.name }
func (p *scriptedProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{Name: p.name}
}
func (p *scriptedProvider) Health(context.Context) provider.HealthStatus {
	return provider.HealthStatus{Provider: p.name}
}
func (p *scriptedProvider) Fetch(_ context.Context, req provider.FetchRequest) (provider.FetchResult, error) {
	p.calls = append(p.calls, req)
	return p.result, p.err
}

type fixedResolver struct {
	latest uint64
	blocks map[int64]uint64
}

func (r fixedResolver) LatestBlock(context.Context) (uint64, error) { return r.latest, nil }
func (r fixedResolver) ResolveBlockByTimestamp(_ context.Context, value time.Time) (uint64, error) {
	return r.blocks[value.Unix()], nil
}

func hybridRow(block uint64) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"block_number": block})
	return raw
}

func TestHybridRoutesFullAndTimeToPrimaryAndBlockToAnkr(t *testing.T) {
	primary := &scriptedProvider{name: "trongrid"}
	fallback := &scriptedProvider{name: "ankr"}
	h := &HybridProvider{Primary: primary, Fallback: fallback, Resolver: fixedResolver{latest: 1000}, Options: HybridOptions{FallbackEnabled: true, MaxWindows: 10, MaxWindowSpan: 5000}}
	for _, mode := range []string{"full", "time"} {
		if _, err := h.Fetch(context.Background(), provider.FetchRequest{Chain: "tron", Dataset: "token_transfers", QueryMode: mode}); err != nil {
			t.Fatal(err)
		}
	}
	from, to := uint64(10), uint64(20)
	if _, err := h.Fetch(context.Background(), provider.FetchRequest{Chain: "tron", Dataset: "token_transfers", QueryMode: "block", FromBlock: &from, ToBlock: &to}); err != nil {
		t.Fatal(err)
	}
	if len(primary.calls) != 2 || len(fallback.calls) != 1 || *fallback.calls[0].FromBlock != 10 || *fallback.calls[0].ToBlock != 20 {
		t.Fatalf("primary=%d fallback=%#v", len(primary.calls), fallback.calls)
	}
}

func TestHybridRetriesAreExhaustedBeforeFallbackAndOnlyScansMissingRange(t *testing.T) {
	primary := &scriptedProvider{name: "trongrid", err: &provider.Error{Provider: "trongrid", Class: provider.ClassUpstream, Retryable: true, Message: "retry budget exhausted"}}
	fallback := &scriptedProvider{name: "ankr", result: provider.FetchResult{Rows: []json.RawMessage{hybridRow(20)}}}
	h := &HybridProvider{Primary: primary, Fallback: fallback, Resolver: fixedResolver{latest: 1000}, Options: HybridOptions{FallbackEnabled: true, MaxWindows: 10, MaxWindowSpan: 100}}
	cursor := encodeHybridCursor(hybridCursor{Provider: "trongrid", Token: "fp", CoveredStart: 501, CoveredEnd: 1000})
	_, err := h.Fetch(context.Background(), provider.FetchRequest{Chain: "tron", Dataset: "token_transfers", QueryMode: "full", PageToken: cursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(fallback.calls) != 1 || *fallback.calls[0].FromBlock != 0 || *fallback.calls[0].ToBlock != 500 {
		t.Fatalf("fallback request=%#v", fallback.calls)
	}
}

func TestHybridEmptyPrimaryPageIsEOFNotFallback(t *testing.T) {
	primary := &scriptedProvider{name: "trongrid", result: provider.FetchResult{Rows: nil, HasMore: false}}
	fallback := &scriptedProvider{name: "ankr"}
	h := &HybridProvider{Primary: primary, Fallback: fallback, Resolver: fixedResolver{latest: 1000}, Options: HybridOptions{FallbackEnabled: true, MaxWindows: 10, MaxWindowSpan: 100}}
	if _, err := h.Fetch(context.Background(), provider.FetchRequest{Chain: "tron", Dataset: "token_transfers", QueryMode: "full"}); err != nil {
		t.Fatal(err)
	}
	if len(fallback.calls) != 0 {
		t.Fatal("normal EOF triggered fallback")
	}
}

func TestHybridCircuitHalfOpenProbeReturnsToNormal(t *testing.T) {
	primary := &scriptedProvider{name: "trongrid", err: &provider.Error{Provider: "trongrid", Class: provider.ClassUpstream, Retryable: true, Message: "forced outage"}}
	fallback := &scriptedProvider{name: "ankr"}
	h := &HybridProvider{Primary: primary, Fallback: fallback, Resolver: fixedResolver{latest: 10}, Options: HybridOptions{FallbackEnabled: true, MaxWindows: 10, MaxWindowSpan: 100}}
	from, to := uint64(1), uint64(2)
	if _, err := h.Fetch(context.Background(), provider.FetchRequest{Chain: "tron", Dataset: "token_transfers", QueryMode: "block", FromBlock: &from, ToBlock: &to}); err != nil {
		t.Fatal(err)
	}
	// Block mode never touches primary, so explicitly open the breaker as the
	// failed full/time route does, then age it past the production cooldown.
	h.setPrimaryHealth(provider.HealthOpen, "forced outage")
	h.mu.Lock()
	h.primaryOpenedAt = time.Now().Add(-31 * time.Second)
	h.mu.Unlock()
	primary.err = nil
	if _, err := h.Fetch(context.Background(), provider.FetchRequest{Chain: "tron", Dataset: "token_transfers", QueryMode: "full"}); err != nil {
		t.Fatal(err)
	}
	status := h.Health(context.Background())
	if status.State != provider.HealthNormal || len(primary.calls) != 1 {
		t.Fatalf("status=%s primary calls=%d", status.State, len(primary.calls))
	}
}

func TestHybridFallbackCostGuard(t *testing.T) {
	primary := &scriptedProvider{name: "trongrid", err: &provider.Error{Provider: "trongrid", Class: provider.ClassRateLimit, Retryable: true, Message: "exhausted"}}
	h := &HybridProvider{Primary: primary, Fallback: &scriptedProvider{name: "ankr"}, Resolver: fixedResolver{latest: 100000}, Options: HybridOptions{FallbackEnabled: true, MaxWindows: 10, MaxWindowSpan: 5000}}
	_, err := h.Fetch(context.Background(), provider.FetchRequest{Chain: "tron", Dataset: "token_transfers", QueryMode: "full"})
	var cost *FallbackTooExpensiveError
	if !errors.As(err, &cost) || cost.Windows != 21 || cost.EstimatedRequests != 42 {
		t.Fatalf("cost error=%#v", err)
	}
}

func TestHybridCursorRoundTripKeepsProviderNeutralCheckpoint(t *testing.T) {
	want := hybridCursor{Provider: "ankr", Token: "ankr:block:opaque", CoveredStart: 12, CoveredEnd: 99}
	got, err := decodeHybridCursor(encodeHybridCursor(want))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got=%#v want=%#v", got, want)
	}
}
