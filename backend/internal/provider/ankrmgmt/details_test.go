package ankrmgmt

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseUsageDetails(t *testing.T) {
	usage, err := parseUsageDetails(map[string]any{"text": "Usage (top 2 by credit cost):\n- aapi_v2 ankr_getTransactionsByAddress: 126 calls, 88,200 credits\n- eth eth_getBalance: 4 calls, 80 credits\nTotal: 1,609 calls, 475,400 credits.\n"}, 1000, 2000)
	if err != nil {
		t.Fatal(err)
	}
	rows := usage["data"].([]map[string]any)
	if usage["total_requests"] != int64(1609) || usage["total_credits"] != "475400" ||
		usage["from_ms"] != int64(1000) || usage["to_ms"] != int64(2000) || len(rows) != 2 ||
		rows[0]["network"] != "aapi_v2" || rows[0]["method"] != "ankr_getTransactionsByAddress" ||
		rows[0]["request_count"] != int64(126) || rows[0]["credits"] != "88200" {
		t.Fatalf("unexpected usage: %#v", usage)
	}
	for _, input := range []string{"no total", "Total: 3 calls, 12 credits."} {
		if _, err := parseUsageDetails(map[string]any{"text": input}, 1000, 2000); err == nil {
			t.Fatalf("incomplete usage accepted: %q", input)
		}
	}
	zero, err := parseUsageDetails(map[string]any{"text": "Usage (top 0 by credit cost):\nTotal: 0 calls, 0 credits."}, 1000, 2000)
	if err != nil || zero["total_requests"] != int64(0) {
		t.Fatalf("zero usage rejected: %#v, %v", zero, err)
	}
}

func TestParseKeyDetailsExcludesUnstructuredSecrets(t *testing.T) {
	const secret = "super-secret-do-not-persist"
	keys, err := parseKeyDetails(map[string]any{"text": "2 dedicated API key(s):\n- index 0: (none) [encrypted: false; config: (unrestricted)]\n- index 1: wx [encrypted: true; config: ethereum]\nRaw key: " + secret + "\n"})
	if err != nil {
		t.Fatal(err)
	}
	rows := keys["data"].([]map[string]any)
	if keys["count"] != 2 || len(rows) != 2 || rows[0]["name"] != "" ||
		rows[1]["name"] != "wx" || rows[1]["encrypted"] != true || rows[1]["config"] != "ethereum" {
		t.Fatalf("unexpected key metadata: %#v", keys)
	}
	encoded, err := json.Marshal(keys)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatal("unstructured key material leaked")
	}
	if _, err := parseKeyDetails(map[string]any{"text": "2 dedicated API key(s):\n- index 0: wx [encrypted: false; config: (unrestricted)]"}); err == nil {
		t.Fatal("incomplete key list accepted")
	}
}
