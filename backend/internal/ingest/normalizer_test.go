package ingest

import (
	"encoding/json"
	"testing"
)

func TestNormalizeTransaction(t *testing.T) {
	raw := json.RawMessage(`{"blockHeight":"123","blockHash":"0xabc","transactionHash":"0xdef","timestamp":1700000000123,"fromAddress":"0xaaa","toAddress":"0xbbb","value":"0x10","nonce":"1","gasLimit":"21000","gasUsed":"21000","gasPrice":"0x5","type":"0x0","status":"0x1","input":"0x","methodId":"0x12345678"}`)
	r, err := NormalizeTransaction(raw, "bsc", "ankr")
	if err != nil {
		t.Fatal(err)
	}
	if r.BlockNumber != 123 {
		t.Errorf("block = %d", r.BlockNumber)
	}
	if r.TxHash != "0xdef" {
		t.Errorf("tx = %s", r.TxHash)
	}
	if r.Status != 1 {
		t.Errorf("status = %d", r.Status)
	}
	if r.Timestamp.Year() != 2023 {
		t.Errorf("timestamp = %v", r.Timestamp)
	}
}

func TestNormalizeTokenTransferAmount(t *testing.T) {
	raw := json.RawMessage(`{"blockHeight":"1","timestamp":1700000000123,"transactionHash":"0x1","logIndex":"0","fromAddress":"0xa","toAddress":"0xb","value":"100500000000000000000","tokenName":"T","tokenSymbol":"T","tokenDecimals":18,"contractAddress":"0xc"}`)
	r, err := NormalizeTokenTransfer(raw, "bsc", "ankr")
	if err != nil {
		t.Fatal(err)
	}
	if r.Amount.String() != "100500000000000000000" {
		t.Errorf("amount = %s", r.Amount.String())
	}
	if r.LogIndex != 0 {
		t.Errorf("log index = %d", r.LogIndex)
	}
}

func BenchmarkNormalizeTransaction(b *testing.B) {
	raw := json.RawMessage(`{"blockHeight":"123","blockHash":"0xabc","transactionHash":"0xdef","timestamp":1700000000123,"fromAddress":"0xaaa","toAddress":"0xbbb","value":"0x10","nonce":"1","gasLimit":"21000","gasUsed":"21000","gasPrice":"0x5","type":"0x0","status":"0x1","input":"0x","methodId":"0x12345678"}`)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := NormalizeTransaction(raw, "bsc", "ankr"); err != nil {
			b.Fatal(err)
		}
	}
}

func TestNormalizeLogTopics(t *testing.T) {
	raw := json.RawMessage(`{"address":"0xa","topics":["0x1","0x2"],"data":"0x","blockNumber":"7","transactionHash":"0x7","logIndex":"3","removed":false,"timestamp":1700000000123}`)
	r, err := NormalizeLog(raw, "bsc", "ankr")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Topics) != 2 {
		t.Errorf("topics = %v", r.Topics)
	}
	if r.Removed != 0 {
		t.Errorf("removed = %d", r.Removed)
	}
}
