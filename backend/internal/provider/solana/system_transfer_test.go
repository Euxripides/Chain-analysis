package solana

import (
	"encoding/json"
	"testing"
)

func TestSystemTransferRowsPreserveActualEndpointsAndLamports(t *testing.T) {
	var tx txResult
	raw := `{"slot":420305276,"blockTime":1789875226,"transaction":{"message":{"accountKeys":[{"pubkey":"Alice","signer":true},{"pubkey":"Bob"}],"instructions":[{"programId":"11111111111111111111111111111111","parsed":{"type":"transfer","info":{"source":"Alice","destination":"Bob","lamports":"1000000001"}}}]}},"meta":{"err":null,"fee":5000}}`
	if err := json.Unmarshal([]byte(raw), &tx); err != nil {
		t.Fatal(err)
	}
	rows, err := systemTransferRows(&tx, "CaseSensitiveSignature")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected one native movement, got %d", len(rows))
	}
	got := rows[0]
	if got.From != "Alice" || got.To != "Bob" || got.ValueRaw != "1000000001" || got.AssetType != "SOL" || got.CallType != "system_transfer" || got.TxHash != "CaseSensitiveSignature" || got.BlockNumber != "420305276" {
		t.Fatalf("incorrect native movement: %+v", got)
	}
}

func TestSystemTransferRowsIgnoreFailedTransaction(t *testing.T) {
	var tx txResult
	raw := `{"slot":1,"blockTime":1789875226,"transaction":{"message":{"instructions":[{"programId":"11111111111111111111111111111111","parsed":{"type":"transfer","info":{"source":"Alice","destination":"Bob","lamports":10}}}]}},"meta":{"err":{"InstructionError":[0,"Custom"]}}}`
	if err := json.Unmarshal([]byte(raw), &tx); err != nil {
		t.Fatal(err)
	}
	rows, err := systemTransferRows(&tx, "FailedSignature")
	if err != nil || len(rows) != 0 {
		t.Fatalf("failed transaction must not create a fund flow: %v %+v", err, rows)
	}
}
