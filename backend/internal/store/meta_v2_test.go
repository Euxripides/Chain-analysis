package store

import "testing"

func TestNormalizeWorkspaceAddressPreservesTronBase58Case(t *testing.T) {
	const tronAddress = "TQn9Y2khEsLJW1ChVWFMSMeRDow5KcbLSE"
	if got := normalizeWorkspaceAddress("tron", "  "+tronAddress+"  "); got != tronAddress {
		t.Fatalf("TRON address case changed: got %q want %q", got, tronAddress)
	}
}

func TestNormalizeWorkspaceAddressLowercasesEVM(t *testing.T) {
	const input = "0xAbCdEf0000000000000000000000000000001234"
	const want = "0xabcdef0000000000000000000000000000001234"
	if got := normalizeWorkspaceAddress("bsc", input); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
