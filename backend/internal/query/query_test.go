package query

import (
	"testing"
	"time"
)

func TestExplorerCursorConditionMatchesStableOrder(t *testing.T) {
	cur := &ExplorerCursor{Timestamp: time.Unix(1, 0), TxHash: "0xabc", LogIndex: 7, TokenAddress: "0xtoken"}
	condition, args := explorerCursorCondition("token_transfers", cur)
	if condition != "(timestamp, tx_hash, log_index, token_address) < (?, ?, ?, ?)" {
		t.Fatalf("condition = %q", condition)
	}
	if len(args) != 4 {
		t.Fatalf("args = %d, want 4", len(args))
	}
	if order := explorerOrder("token_transfers"); order != "timestamp DESC, tx_hash DESC, log_index DESC, token_address DESC" {
		t.Fatalf("order = %q", order)
	}
}

func TestNormalizeStoredAmountUsesRowTokenDecimals(t *testing.T) {
	row := map[string]any{
		"amount_raw":     "50225000000000000000000",
		"amount":         "0.00000000000000050225",
		"token_decimals": 18,
	}
	normalizeStoredAmount("token_transfers", row)
	if got := row["amount"]; got != "50225" {
		t.Fatalf("amount = %v, want 50225", got)
	}
}

func TestActivityCandidateMatchesDirection(t *testing.T) {
	transfer := map[string]any{"from_address": "0xfrom", "to_address": "0xto"}
	if !activityCandidateMatches(map[string]any{"address": "0xto", "counterparty": "0xfrom", "direction": "in"}, transfer) {
		t.Fatal("incoming transfer did not match")
	}
	if activityCandidateMatches(map[string]any{"address": "0xfrom", "counterparty": "0xwrong", "direction": "out"}, transfer) {
		t.Fatal("wrong counterparty matched")
	}
}
