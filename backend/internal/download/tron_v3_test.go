package download

import (
	"testing"
	"time"
)

func TestTronGridCheckpointCarriesAddressDatasetAndFingerprint(t *testing.T) {
	ts := time.UnixMilli(1700000000000).UTC()
	state := tronPageState(nil, "tron", "trongrid", "transactions", "TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj", "full", ts, "tx-last", "fp-next", nil, 10, 0, 0, "trongrid")
	if got := state["tron_provider"]; got != "trongrid" {
		t.Fatalf("provider=%v", got)
	}
	if got := state["tron_cursor_type"]; got != "fingerprint" {
		t.Fatalf("cursor type=%v", got)
	}
	if state["tron_dataset"] != "transactions" || state["tron_address"] != "TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj" || state["tron_fingerprint"] != "fp-next" || state["tron_last_tx_id"] != "tx-last" {
		t.Fatalf("checkpoint identity/state=%#v", state)
	}
}

func TestAnkrCheckpointDoesNotClaimFingerprintCursor(t *testing.T) {
	state := tronPageState(nil, "tron", "ankr", "token_transfers", "TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj", "block", time.Time{}, "", "", nil, 0, 10, 0, "ankr")
	if _, ok := state["tron_fingerprint"]; ok {
		t.Fatalf("Ankr checkpoint unexpectedly contains fingerprint: %#v", state)
	}
}
