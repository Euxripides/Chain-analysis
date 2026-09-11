package capability

import (
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/platform/contract"
)

func TestRegistryVersionsAreStableAndSorted(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(contract.Capability{Name: "flow.trace", Version: "1.0"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(contract.Capability{Name: "flow.trace", Version: "2.0"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(contract.Capability{Name: "flow.trace", Version: "2.0"}); err == nil {
		t.Fatal("duplicate registration accepted")
	}
	got, ok := r.Resolve("flow.trace", "")
	if !ok || got.Version != "2.0" {
		t.Fatalf("latest=%+v ok=%v", got, ok)
	}
	if len(r.List()) != 2 {
		t.Fatalf("list length=%d", len(r.List()))
	}
}
