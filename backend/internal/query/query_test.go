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
