package ankrrpc

import (
	"context"
	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"strings"
	"testing"
)

func TestAnkrAllAdvancedMainnetEndpoints(t *testing.T) {
	key := strings.Repeat("a", 64)
	c := New(Options{APIKey: key})
	for _, slug := range chain.AnkrAdvancedMainnets() {
		endpoint, ok, err := c.endpoint(context.Background(), slug)
		if err != nil || !ok || endpoint != "https://rpc.ankr.com/"+slug+"/"+key {
			t.Fatalf("endpoint %s: %s %v %v", slug, endpoint, ok, err)
		}
		if strings.Contains(c.SafeEndpoint(slug), key) {
			t.Fatalf("credential exposed for %s", slug)
		}
	}
	if _, ok, err := c.endpoint(context.Background(), "eth_sepolia"); err != nil || ok {
		t.Fatal("testnet enabled by mainnet expansion")
	}
	override := New(Options{APIKey: "test-key", ChainPaths: map[string]string{"private": "custom"}})
	if endpoint, ok, _ := override.endpoint(context.Background(), "private"); !ok || endpoint != "https://rpc.ankr.com/custom/test-key" {
		t.Fatal("explicit override broken")
	}
}
