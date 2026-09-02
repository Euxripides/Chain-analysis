package chaintrace

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
)

func TestNormalizeParityAndDeriveActivities(t *testing.T) {
	raw := json.RawMessage(`[
		{"action":{"callType":"call","from":"0x1111111111111111111111111111111111111111","to":"0x2222222222222222222222222222222222222222","value":"0x10","gas":"0x5208","input":"0x"},"result":{"gasUsed":"0x100","output":"0x"},"traceAddress":[],"type":"call"},
		{"action":{"callType":"delegatecall","from":"0x2222222222222222222222222222222222222222","to":"0x3333333333333333333333333333333333333333","value":"0x0","gas":"0x100","input":"0xa9059cbb"},"result":{"gasUsed":"0x20","output":"0x"},"traceAddress":[0],"type":"call"}
	]`)
	root, source, err := Normalize(raw)
	if err != nil {
		t.Fatal(err)
	}
	frames, depth := Summarize(root)
	if source != SourceParity || frames != 2 || depth != 2 || root.Value != "16" {
		t.Fatalf("source=%s frames=%d depth=%d root=%+v", source, frames, depth, root)
	}
	hash, err := chain.ParseHash("0x" + strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	activities := ExtractInternalActivity(&TraceResult{Chain: "bsc", TxHash: hash, Root: root, FrameCount: frames})
	if len(activities) != 2 || activities[0].Activity != ActivityNativeTransfer ||
		activities[1].Activity != ActivityDelegateCall || activities[1].MethodID != "0xa9059cbb" {
		t.Fatalf("activities=%+v", activities)
	}
}

func TestNormalizeGethPreservesFailure(t *testing.T) {
	raw := json.RawMessage(`{"type":"CALL","from":"0x1111111111111111111111111111111111111111",` +
		`"to":"0x2222222222222222222222222222222222222222","value":"0x0","gas":"0x10",` +
		`"gasUsed":"0x8","input":"0x12345678","revertReason":"denied"}`)
	root, source, err := Normalize(raw)
	if err != nil {
		t.Fatal(err)
	}
	if source != SourceGeth || root.Error != "denied" || root.GasUsed != 8 {
		t.Fatalf("source=%s root=%+v", source, root)
	}
}

func TestNormalizeRejectsEmptyAndMalformedTrace(t *testing.T) {
	for _, raw := range []json.RawMessage{nil, json.RawMessage("null"), json.RawMessage("[]"), json.RawMessage(`{}`)} {
		if _, _, err := Normalize(raw); err == nil {
			t.Fatalf("raw=%s should fail", raw)
		}
	}
}

func TestRawObjectKeyUsesSafeProviderSegment(t *testing.T) {
	hash, err := chain.ParseHash("0x" + strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	got := RawObjectKey("Provider/../../Secret", "BSC", hash)
	want := "raw/rpc/providersecret/bsc/trace/" + string(hash) + ".json.gz"
	if got != want || strings.Contains(got, "..") {
		t.Fatalf("key=%q want=%q", got, want)
	}
}
