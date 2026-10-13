package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
	"github.com/blockchain-platform/blockchain-platform/internal/provider/mockrpc"
	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
)

func optimizationReader(t *testing.T) (*chainread.Service, *mockrpc.Provider) {
	t.Helper()
	provider := mockrpc.New("optimization-mock")
	caps := rpc.NewCapabilityStore()
	breakers := rpc.NewBreakers(20, .9, time.Second)
	health := rpc.NewHealthTracker()
	concurrency := rpc.NewConcurrencyController(16)
	router := rpc.NewRouter(caps, breakers, health, concurrency)
	capabilities, _ := provider.Capabilities(context.Background(), "bsc")
	caps.Set(provider.Name(), "bsc", capabilities, rpc.CapSourceStatic)
	router.Register("bsc", provider, 100, 0)
	gateway := rpc.NewGateway(rpc.GatewayOptions{Router: router, Capabilities: caps, Breakers: breakers, Health: health, Concurrency: concurrency, Rate: rpc.NewRateController(10000), Cache: rpc.NewCache(100, true)})
	return chainread.New(gateway, rpc.NewBatcher(gateway, rpc.BatcherOptions{MaxSize: 50, MaxWait: time.Millisecond})), provider
}

func TestOptimizationAllowanceBatchPreservesOrderPrecisionAndPartialErrors(t *testing.T) {
	reader, provider := optimizationReader(t)
	s := &Server{deps: Deps{ChainRead: reader}}
	q := nextRequest{Chain: "bsc", Address: "0x1111111111111111111111111111111111111111", TokenContract: "0x2222222222222222222222222222222222222222"}
	spenders := make([]string, 200)
	for i := range spenders {
		spenders[i] = fmt.Sprintf("0x%040x", i+1)
	}
	exact := new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 200), big.NewInt(12345))
	unlimited := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	provider.Handle("eth_call", func(req rpc.Request) (json.RawMessage, *rpc.Error) {
		if req.BlockRef.Number == nil || *req.BlockRef.Number != 123 {
			t.Error("batch lost pinned block")
		}
		call := req.Params[0].(map[string]any)
		data := call["data"].(string)
		if !strings.HasPrefix(data, "0xdd62ed3e") || len(data) != 138 {
			t.Errorf("invalid allowance calldata: %s", data)
		}
		spender := "0x" + data[len(data)-40:]
		if spender == spenders[17] {
			return nil, &rpc.Error{Class: rpc.ClassExecutionReverted, Message: "reverted"}
		}
		value := exact
		if spender == spenders[199] {
			value = unlimited
		}
		raw, _ := json.Marshal(fmt.Sprintf("0x%064x", value))
		return raw, nil
	})
	results, e := s.nextAllowanceBatch(context.Background(), q, spenders, chainread.StatePoint{Block: 123, Ref: chain.AtBlock(123)})
	if e != nil || len(results) != 200 {
		t.Fatal(len(results), e)
	}
	for i, r := range results {
		if r.Key != spenders[i] {
			t.Fatal("spender association changed", i, r.Key)
		}
		if i == 17 {
			if r.Err == nil {
				t.Fatal("partial failure lost")
			}
			continue
		}
		expected := exact
		if i == 199 {
			expected = unlimited
		}
		if r.Err != nil || new(big.Int).SetBytes(r.Output).Cmp(expected) != 0 {
			t.Fatal("lost uint256 precision", i, r.Err)
		}
	}
	if provider.Calls("eth_call") != 200 || provider.BatchRequests() == 0 || provider.BatchRequests() > 4 {
		t.Fatal("RPC calls were not batched", provider.Calls("eth_call"), provider.BatchRequests())
	}
	t.Logf("200 methods in %d provider batch requests", provider.BatchRequests())
	// Failed watches must retain their previous baseline rather than invent zero.
	q.RuleType = "allowance_change"
	q.Spenders = []string{spenders[0], spenders[17]}
	if _, _, e = s.nextWatchValue(context.Background(), q, chainread.StatePoint{Block: 123, Ref: chain.AtBlock(123)}); e == nil {
		t.Fatal("watch accepted incomplete allowance state")
	}
}

func TestOptimizationSectionCountRejectsRoundedAndCorruptMetadata(t *testing.T) {
	for _, raw := range []string{`{"sections":{"evidence":9007199254740993}}`, `{"sections":{"evidence":0}}`} {
		var meta any
		if e := nextDecode([]byte(raw), &meta); e != nil {
			t.Fatal(e)
		}
		n, ok, e := nextSectionCount(meta, "evidence")
		if e != nil || !ok || (strings.Contains(raw, "9007") && n != 9007199254740993) {
			t.Fatal(n, ok, e)
		}
	}
	for _, value := range []any{json.Number("1.5"), json.Number("-1"), json.Number("9223372036854775808"), "100", float64(1)} {
		if _, _, e := nextSectionCount(map[string]any{"sections": map[string]any{"evidence": value}}, "evidence"); e == nil {
			t.Fatal("invalid count accepted", value)
		}
	}
	if _, ok, e := nextSectionCount(map[string]any{}, "evidence"); ok || e != nil {
		t.Fatal("legacy SQL fallback unavailable")
	}
}

func TestOptimizationWatchNameUsesUnicodeCharacters(t *testing.T) {
	for _, name := range []string{strings.Repeat("汉", 200), strings.Repeat("😀", 200)} {
		q := nextRequest{Name: name, RuleType: "balance_change"}
		if e := validateWatch(&q); e != nil {
			t.Fatal("valid Unicode watch name rejected", e)
		}
	}
	for _, name := range []string{"", strings.Repeat("汉", 201), string([]byte{0xff})} {
		q := nextRequest{Name: name, RuleType: "balance_change"}
		if e := validateWatch(&q); e == nil {
			t.Fatal("invalid watch name accepted")
		}
	}
}
