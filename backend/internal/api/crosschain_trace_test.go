package api

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestParityTraceKeepsDeepTransfersAcrossSiblingSliceGrowth(t *testing.T) {
	// Siblings are encountered before descendants, as returned by common RPCs.
	item := func(path string, value string) string {
		return fmt.Sprintf(`{"type":"call","traceAddress":%s,"action":{"callType":"call","from":"%s","to":"%s","value":"%s"}}`, path, bridgeWallet, bridgePool, value)
	}
	raw := json.RawMessage("[" + item("[]", "0x64") + "," + item("[0]", "0x0") + "," + item("[1]", "0x0") + "," + item("[2]", "0x0") + "," + item("[0,0]", "0x32") + "," + item("[1,0]", "0x19") + "]")
	root, err := normalizeBridgeTrace(raw)
	if err != nil || len(root.Children) != 3 || len(root.Children[0].Children) != 1 || len(root.Children[1].Children) != 1 || root.Children[0].Children[0].Value != "50" || root.Children[1].Children[0].Value != "25" {
		t.Fatal("deep trace funding lost", root, err)
	}
	for _, bad := range []json.RawMessage{json.RawMessage("[" + item("[]", "0x64") + "," + item("[0,0]", "0x32") + "]"), json.RawMessage("[" + item("[]", "0x64") + "," + item("[0]", "0x1") + "," + item("[0]", "0x2") + "]")} {
		if _, err := normalizeBridgeTrace(bad); err == nil {
			t.Fatal("orphan/duplicate path created funding proof")
		}
	}
}
