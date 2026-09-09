package tron

import (
	"encoding/json"
	"testing"
)

func TestNormalizeTRC20LargeAmount(t *testing.T) {
	raw := json.RawMessage(`{"transaction_id":"abc","block_timestamp":1700000000000,"from":"TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj","to":"TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj","value":"340282366920938463463374607431768211455","token_info":{"address":"TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t","symbol":"USDT","decimals":6}}`)
	r, err := NormalizeTRC20(raw)
	if err != nil {
		t.Fatal(err)
	}
	if r.AmountRaw != "340282366920938463463374607431768211455" || r.Amount.String() != r.AmountRaw {
		t.Fatalf("amount lost: %#v", r)
	}
	if r.LogIndex == 0 {
		t.Fatal("stable event index not assigned")
	}
}

func TestNormalizeFailedAndUnknownTransaction(t *testing.T) {
	raw := json.RawMessage(`{"txID":"abc","blockNumber":1,"block_timestamp":1700000000000,"ret":[{"contractRet":"REVERT"}],"raw_data":{"contract":[{"type":"VoteWitnessContract","parameter":{"value":{"owner_address":"41A614F803B6FD780986A42C78EC9C7F77E6DED13C"}}}]}}`)
	r, err := NormalizeTransaction(raw)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != 0 || r.TxType != "OTHER" || r.RawType != "VoteWitnessContract" {
		t.Fatalf("unexpected row: %#v", r)
	}
}

func TestNormalizeRejectedInternal(t *testing.T) {
	raw := json.RawMessage(`{"internal_tx_id":"i1","tx_id":"t1","block_timestamp":1700000000000,"from_address":"41A614F803B6FD780986A42C78EC9C7F77E6DED13C","to_address":"41A614F803B6FD780986A42C78EC9C7F77E6DED13C","data":{"note":"call","rejected":true,"call_value":{"_":100}}}`)
	r, err := NormalizeInternal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Rejected || r.ValueRaw != "100" {
		t.Fatalf("unexpected row: %#v", r)
	}
}

func TestIdenticalTRC20EventsInSameTransactionRemainDistinctAndStable(t *testing.T) {
	first := []json.RawMessage{
		json.RawMessage(`{"transaction_id":"same","_tron_event_index":3,"block_timestamp":1700000000000,"from":"TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj","to":"TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj","value":"1","token_info":{"address":"TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t","symbol":"USDT","decimals":6}}`),
		json.RawMessage(`{"transaction_id":"same","_tron_event_index":4,"block_timestamp":1700000000000,"from":"TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj","to":"TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj","value":"1","token_info":{"address":"TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t","symbol":"USDT","decimals":6}}`),
	}
	second := first
	a1, err := NormalizeTRC20(first[0])
	if err != nil {
		t.Fatal(err)
	}
	a2, err := NormalizeTRC20(first[1])
	if err != nil {
		t.Fatal(err)
	}
	b1, _ := NormalizeTRC20(second[0])
	b2, _ := NormalizeTRC20(second[1])
	if a1.LogIndex == a2.LogIndex {
		t.Fatal("identical transfers in one transaction collapsed to one index")
	}
	if a1.LogIndex != b1.LogIndex || a2.LogIndex != b2.LogIndex {
		t.Fatal("event indexes changed on duplicate page replay")
	}
}

func TestNormalizeTRC20AllowsMissingMetadata(t *testing.T) {
	raw := json.RawMessage(`{"transaction_id":"abc","block_timestamp":1700000000000,"from":"TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj","to":"TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj","value":"10"}`)
	row, err := NormalizeTRC20(raw)
	if err != nil {
		t.Fatal(err)
	}
	if row.TokenAddress != "" || row.TokenSymbol != "" || row.AmountRaw != "10" {
		t.Fatalf("unexpected missing-metadata row: %#v", row)
	}
}
