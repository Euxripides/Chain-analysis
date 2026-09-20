package ingest

import (
	"encoding/json"
	"testing"
)

func TestNormalizeTokenTransferDefaultsAmountInvariant(t *testing.T) {
	raw := json.RawMessage(`{"blockHeight":"1","transactionHash":"0xabc","contractAddress":"0xAbCdEf","fromAddress":"0xFfF","toAddress":"0xEeE","valueRawInteger":"100","tokenDecimals":6}`)
	row, err := NormalizeTokenTransfer(raw, "bsc", "ankr")
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	// 没有费用概念的链：gross 缺省等于 amount、fee 缺省为 0（ADR-023 D6 不变量）。
	if row.AmountRaw != "100" || row.GrossAmountRaw != "100" || row.FeeAmountRaw != "0" {
		t.Fatalf("amount invariant defaults broken: %+v", row)
	}
	if row.From != "0xfff" || row.To != "0xeee" {
		t.Fatalf("evm addresses must still be lowercased: %+v", row)
	}
	if row.FromTokenAccount != "" || row.ToTokenAccount != "" {
		t.Fatalf("non-solana rows must not invent token accounts: %+v", row)
	}
}

func TestNormalizeTokenTransferCarriesSolanaEvidence(t *testing.T) {
	const sourceATA = "7KJjY7rArbydeLBF7gQ5LdqXRKRYyPArT99NEctsHsgU"
	const destATA = "HNhDsdRNVv4UPjxwnrLDfVay3SFjx4wQ984bcDfSfoB"
	raw := json.RawMessage(`{"blockHeight":"448611120","timestamp":1789875226,"transactionHash":"SIG","logIndex":768,
		"contractAddress":"EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v","tokenDecimals":6,
		"fromAddress":"5tzFkiKscXHK5ZXCGbXZxdw7gTjjD1mBwuoFbhUvuAi9","toAddress":"CGw7M5mw9KnnzF7kgBku7psEHRCuaUjmUHJM33atrhqs",
		"valueRawInteger":"990","grossAmountRaw":"1000","feeAmountRaw":"10",
		"fromTokenAccount":"` + sourceATA + `","toTokenAccount":"` + destATA + `"}`)
	row, err := NormalizeTokenTransfer(raw, "solana", "solana")
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if row.AmountRaw != "990" || row.GrossAmountRaw != "1000" || row.FeeAmountRaw != "10" {
		t.Fatalf("gross/net/fee must be carried through: %+v", row)
	}
	if row.FromTokenAccount != sourceATA || row.ToTokenAccount != destATA {
		t.Fatalf("ATAs must be preserved: %+v", row)
	}
	if row.From != "5tzFkiKscXHK5ZXCGbXZxdw7gTjjD1mBwuoFbhUvuAi9" || row.To != "CGw7M5mw9KnnzF7kgBku7psEHRCuaUjmUHJM33atrhqs" {
		t.Fatalf("solana base58 owners must not be folded: %+v", row)
	}
	if row.BlockNumber != 448611120 || row.LogIndex != 768 {
		t.Fatalf("unexpected identity: %+v", row)
	}
}
