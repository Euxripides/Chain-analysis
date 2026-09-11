package semantic

import "testing"

func TestClassify(t *testing.T) {
	cases := map[string]string{"swapExactTokensForTokens": "SWAP", "bridge": "BRIDGE", "addLiquidity": "ADD_LIQUIDITY", "removeLiquidity": "REMOVE_LIQUIDITY", "deposit": "DEPOSIT", "withdraw": "WITHDRAW"}
	for method, want := range cases {
		got, _ := classify(method, "", "")
		if got != want {
			t.Fatalf("%s: got %s want %s", method, got, want)
		}
	}
	if got, _ := classify("transfer", "", "0x"); got != "" {
		t.Fatalf("ordinary transfer classified as %s", got)
	}
}
