package chain

import "testing"

func TestParseAddress(t *testing.T) {
	a, err := ParseAddress("0xB8c77482e45F1F44dE1745F52C74426C631bDD52")
	if err != nil {
		t.Fatal(err)
	}
	if a != "0xb8c77482e45f1f44de1745f52c74426c631bdd52" {
		t.Fatalf("normalized = %s", a)
	}
	// §156：UI 展示使用 EIP-55 校验和
	if got := a.Checksum(); got != "0xB8c77482e45F1F44dE1745F52C74426C631bDD52" {
		t.Fatalf("checksum = %s", got)
	}
	for _, bad := range []string{"", "0x", "b8c77482e45f1f44de1745f52c74426c631bdd52", "0xzz", "0x1234"} {
		if _, err := ParseAddress(bad); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
}

func TestBlockRef(t *testing.T) {
	if got := Latest().String(); got != "latest" {
		t.Fatalf("latest = %s", got)
	}
	if got := AtBlock(436).String(); got != "0x1b4" {
		t.Fatalf("fixed = %s", got)
	}
	if !AtBlock(1).IsFixed() || AtBlock(1).IsLatest() {
		t.Fatal("fixed block ref classification wrong")
	}
	if !Latest().IsLatest() || Latest().IsFixed() {
		t.Fatal("latest block ref classification wrong")
	}
	ref, err := ParseBlockRef("0x1b4")
	if err != nil || !ref.IsFixed() || *ref.Number != 436 {
		t.Fatalf("parse hex block ref = %+v %v", ref, err)
	}
	ref, err = ParseBlockRef("436")
	if err != nil || *ref.Number != 436 {
		t.Fatalf("parse decimal block ref = %+v %v", ref, err)
	}
	if _, err := ParseBlockRef("nonsense"); err == nil {
		t.Fatal("expected parse error")
	}
}

// §24：Safe Head 必须由 Chain Registry 决定，不能直接用 head。
func TestSafeHead(t *testing.T) {
	r := DefaultRegistry()
	bsc, ok := r.Get("bsc")
	if !ok {
		t.Fatal("bsc missing from default registry")
	}
	if bsc.ChainID != 56 || bsc.Native.Symbol != "BNB" {
		t.Fatalf("bsc config = %+v", bsc)
	}
	if got := r.SafeHead("bsc", 1000); got != 1000-bsc.ConfirmationDepth {
		t.Fatalf("safe head = %d", got)
	}
	if got := r.SafeHead("bsc", 3); got != 0 {
		t.Fatalf("safe head underflow = %d", got)
	}
}

func TestRegistryOverride(t *testing.T) {
	r, err := LoadRegistry(`[{"slug":"bsc","confirmation_depth":30,"enabled":true,"supports_trace_concept":true}]`)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := r.Get("bsc")
	if c.ConfirmationDepth != 30 {
		t.Fatalf("override depth = %d", c.ConfirmationDepth)
	}
	if c.ChainID != 56 {
		t.Fatalf("override must keep base fields, chain_id = %d", c.ChainID)
	}
	if _, err := r.Require("does-not-exist"); err == nil {
		t.Fatal("unknown chain must fail loudly")
	}
}

func TestHexHelpers(t *testing.T) {
	n, err := HexToUint64("0x1b4")
	if err != nil || n != 436 {
		t.Fatalf("HexToUint64 = %d %v", n, err)
	}
	big, err := HexToBigInt("0xde0b6b3a7640000")
	if err != nil || big.String() != "1000000000000000000" {
		t.Fatalf("HexToBigInt = %v %v", big, err)
	}
	if got := Uint64ToHex(436); got != "0x1b4" {
		t.Fatalf("Uint64ToHex = %s", got)
	}
	b, err := HexToBytes("0xa9059cbb")
	if err != nil || len(b) != 4 || BytesToHex(b) != "0xa9059cbb" {
		t.Fatalf("bytes roundtrip failed: %v %v", b, err)
	}
}
