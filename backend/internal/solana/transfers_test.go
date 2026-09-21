package solana

import (
	"encoding/json"
	"testing"
)

const (
	tokenOwner     = "5tzFkiKscXHK5ZXCGbXZxdw7gTjjD1mBwuoFbhUvuAi9"
	tokenDest      = "CGw7M5mw9KnnzF7kgBku7psEHRCuaUjmUHJM33atrhqs"
	tokenMint      = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"
	tokenSourceATA = "7KJjY7rArbydeLBF7gQ5LdqXRKRYyPArT99NEctsHsgU"
	tokenDestATA   = "HNhDsdRNVv4UPjxwnrLDfVay3SFjx4wQ984bcDfSfoB"
)

const solanaTransferRow = `{"blockHeight":"448611120","timestamp":1789875226,"transactionHash":"SIG","logIndex":768,"contractAddress":"EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v","tokenDecimals":6,"fromAddress":"5tzFkiKscXHK5ZXCGbXZxdw7gTjjD1mBwuoFbhUvuAi9","toAddress":"CGw7M5mw9KnnzF7kgBku7psEHRCuaUjmUHJM33atrhqs","valueRawInteger":"990","grossAmountRaw":"1000","feeAmountRaw":"10","fromTokenAccount":"7KJjY7rArbydeLBF7gQ5LdqXRKRYyPArT99NEctsHsgU","toTokenAccount":"HNhDsdRNVv4UPjxwnrLDfVay3SFjx4wQ984bcDfSfoB","instructionIndex":3,"innerInstructionIndex":1,"sourceApi":"data_api","sourceRequestId":"req-1"}`

// Solana 地址是 base58，大小写敏感：即使调用方传了会触发泛型层小写化的链名，
// Solana 入口也必须用 raw 原文覆盖回来（折叠不可逆）。
func TestNormalizeTokenTransferForChainKeepsBase58Case(t *testing.T) {
	raw := json.RawMessage(solanaTransferRow)
	for _, chain := range []string{"solana", "Solana", "bsc"} {
		row, err := NormalizeTokenTransferForChain(raw, chain, "drpc")
		if err != nil {
			t.Fatalf("chain %q: normalize transfer: %v", chain, err)
		}
		if row.From != tokenOwner || row.To != tokenDest || row.TokenAddress != tokenMint {
			t.Fatalf("chain %q: base58 case must be preserved: %+v", chain, row)
		}
		if row.FromTokenAccount != tokenSourceATA || row.ToTokenAccount != tokenDestATA {
			t.Fatalf("chain %q: ATAs must be preserved: %+v", chain, row)
		}
		if row.Provider != "drpc" || row.Chain != chainOrDefault(chain) {
			t.Fatalf("chain %q: provider/chain must be carried: %+v", chain, row)
		}
		// 泛型层的金额不变量必须继续成立（复用而不是重写归一化）。
		if row.AmountRaw != "990" || row.GrossAmountRaw != "1000" || row.FeeAmountRaw != "10" {
			t.Fatalf("chain %q: amount invariant broken: %+v", chain, row)
		}
		if row.InstructionIndex != 3 || row.InnerInstructionIndex != 1 {
			t.Fatalf("chain %q: instruction position must be parsed: %+v", chain, row)
		}
		if row.SourceAPI != "data_api" || row.SourceRequestID != "req-1" {
			t.Fatalf("chain %q: source evidence must be parsed: %+v", chain, row)
		}
	}
}

// innerInstructionIndex=0 是真实的内层指令位置，不能被当成"缺失"抹成 -1；
// 缺字段与空 provider 回退则要给出契约默认值。
func TestNormalizeTokenTransferForChainInnerIndexAndDefaults(t *testing.T) {
	explicit := json.RawMessage(`{"blockHeight":"1","timestamp":1,"transactionHash":"SIG","logIndex":0,"contractAddress":"Mint","fromAddress":"A","toAddress":"B","valueRawInteger":"1","instructionIndex":0,"innerInstructionIndex":0,"sourceApi":"data_api"}`)
	row, err := NormalizeTokenTransferForChain(explicit, "", "")
	if err != nil {
		t.Fatalf("normalize transfer: %v", err)
	}
	if row.InnerInstructionIndex != 0 {
		t.Fatalf("explicit inner index 0 must survive: %+v", row)
	}
	if row.Provider != "solana" || row.Chain != "solana" {
		t.Fatalf("empty chain/provider must fall back to solana: %+v", row)
	}

	missing := json.RawMessage(`{"blockHeight":"1","timestamp":1,"transactionHash":"SIG","logIndex":1,"contractAddress":"Mint","fromAddress":"A","toAddress":"B","valueRawInteger":"1"}`)
	row, err = NormalizeTokenTransferForChain(missing, "solana", "drpc")
	if err != nil {
		t.Fatalf("normalize transfer: %v", err)
	}
	if row.InnerInstructionIndex != -1 || row.InstructionIndex != 0 {
		t.Fatalf("missing inner index must stay -1: %+v", row)
	}
	if row.SourceAPI != "" || row.SourceRequestID != "" {
		t.Fatalf("missing evidence must stay empty: %+v", row)
	}
}
