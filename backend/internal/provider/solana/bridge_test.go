package solana

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBridgeDecoderReadsInstructionsAndRejectsFailedOrWrongSignature(t *testing.T) {
	raw := `{"slot":12,"blockTime":1779040000,"transaction":{"signatures":["CaseSensitiveHash"],"message":{"accountKeys":[],"instructions":[{"programId":"11111111111111111111111111111111","parsed":{"type":"transfer","info":{"source":"Alice","destination":"Bob","lamports":"1000000001"}}}]}},"meta":{"err":null,"preBalances":[999999999999],"postBalances":[1],"fee":5000}}`
	got, err := DecodeBridgeTransaction(json.RawMessage(raw), "CaseSensitiveHash")
	if err != nil || len(got.Events) != 1 || got.Events[0].AmountRaw != "1000000001" || got.Events[0].From != "Alice" {
		t.Fatal("whole transaction delta replaced instruction amount", got, err)
	}
	if _, err = DecodeBridgeTransaction(json.RawMessage(raw), "casesensitivehash"); err == nil {
		t.Fatal("signature case mismatch accepted")
	}
	failed := strings.Replace(raw, `"err":null`, `"err":{"InstructionError":[0,"failed"]}`, 1)
	got, err = DecodeBridgeTransaction(json.RawMessage(failed), "CaseSensitiveHash")
	if err != nil || !got.Failed || len(got.Events) > 0 {
		t.Fatal("failed instruction created funds", got, err)
	}
}

func TestBridgeTokenAccountOwnerChangesAreNotAttributedToPriorOwner(t *testing.T) {
	raw := `{"slot":12,"blockTime":1779040000,"transaction":{"signatures":["Hash"],"message":{"accountKeys":[{"pubkey":"SourceATA"},{"pubkey":"DestATA"}],"instructions":[{"programId":"TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA","parsed":{"type":"transferChecked","info":{"source":"SourceATA","destination":"DestATA","mint":"Mint","tokenAmount":{"amount":"100","decimals":6}}}}]}},"meta":{"err":null,"preTokenBalances":[{"accountIndex":0,"mint":"Mint","owner":"Alice","uiTokenAmount":{"amount":"1000","decimals":6}},{"accountIndex":1,"mint":"Mint","owner":"Bob","uiTokenAmount":{"amount":"0","decimals":6}}],"postTokenBalances":[{"accountIndex":0,"mint":"Mint","owner":"Alice","uiTokenAmount":{"amount":"900","decimals":6}},{"accountIndex":1,"mint":"Mint","owner":"Bob","uiTokenAmount":{"amount":"100","decimals":6}}]}}`
	got, err := DecodeBridgeTransaction(json.RawMessage(raw), "Hash")
	if err != nil || len(got.Events) != 1 || got.TokenAccounts["SourceATA"].Owner != "Alice" {
		t.Fatal("known owner/token transfer missing", got, err)
	}
	changed := strings.Replace(raw, `"owner":"Alice","uiTokenAmount":{"amount":"900"`, `"owner":"Mallory","uiTokenAmount":{"amount":"900"`, 1)
	got, err = DecodeBridgeTransaction(json.RawMessage(changed), "Hash")
	if err != nil || len(got.Events) != 0 || got.TokenAccounts["SourceATA"].Owner != "" {
		t.Fatal("changed account owner incorrectly attributed", got, err)
	}
	var value map[string]any
	json.Unmarshal([]byte(raw), &value)
	message := value["transaction"].(map[string]any)["message"].(map[string]any)
	message["instructions"] = append(message["instructions"].([]any), map[string]any{"programId": ProgramSPLToken, "parsed": map[string]any{"type": "setAuthority", "info": map[string]any{"account": "SourceATA"}}})
	encoded, _ := json.Marshal(value)
	got, err = DecodeBridgeTransaction(encoded, "Hash")
	if err != nil || len(got.Events) != 0 {
		t.Fatal("temporary authority change attributed by end-of-transaction owner", got, err)
	}
}

func TestBridgeDecoderDoesNotProduceFlowsFromBalanceDeltas(t *testing.T) {
	raw := `{"slot":12,"blockTime":1779040000,"transaction":{"signatures":["Hash"],"message":{"accountKeys":[],"instructions":[]}},"meta":{"err":null,"preBalances":[1000,0],"postBalances":[0,1000],"fee":0}}`
	got, err := DecodeBridgeTransaction(json.RawMessage(raw), "Hash")
	if err != nil || len(got.Events) > 0 {
		t.Fatal("balance-only record yielded funding event", got, err)
	}
	if _, err = DecodeBridgeTransaction(json.RawMessage(strings.Repeat("x", (8<<20)+1)), "Hash"); err == nil {
		t.Fatal("decode budget ignored")
	}
}
