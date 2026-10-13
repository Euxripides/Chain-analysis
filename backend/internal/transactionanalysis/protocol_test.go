package transactionanalysis

import (
	"strings"
	"testing"
)

func TestHOTWithdrawalIdentifiesPayoutButNotSourceChain(t *testing.T) {
	if event("withdraw(uint128,address,address,uint128,bytes)")[:10] != "0xd9e7c316" {
		t.Fatal("incorrect protocol ABI selector")
	}
	input := "0xd9e7c316" + word("53") + word("0") + strings.TrimPrefix(addr(recipient), "0x") + word("7631125135013813") + word("160") + word("65") + strings.Repeat("1", 130) + strings.Repeat("0", 62)
	out := Result{Chain: "bsc", Outcome: "success", Behavior: "资产转移", Transaction: map[string]any{"input": input, "to_address": hotTreasury}, Participants: []Participant{{Address: hotTreasury}}, Movements: []Movement{{From: hotTreasury, To: recipient, Asset: Asset{Kind: "native"}, AmountRaw: "7631125135013813"}}}
	identifyProtocol(&out)
	if out.Protocol == nil || !out.Protocol.PayoutMatched || out.Protocol.SourceChain != "" || out.Protocol.SourceHash != "" || out.Protocol.Nonce != "53" || out.Behavior != "HOT Bridge 提取到账" {
		t.Fatalf("incorrect protocol attribution: %+v", out)
	}
	out.Protocol = nil
	out.Transaction["to_address"] = treasury
	identifyProtocol(&out)
	if out.Protocol != nil {
		t.Fatal("selector alone treated as protocol evidence")
	}
}
func TestHOTQuotedInstructionIsNotExecutedFunds(t *testing.T) {
	input := "0xd9e7c316" + word("53") + word("0") + strings.TrimPrefix(addr(recipient), "0x") + word("100") + word("160") + word("65") + strings.Repeat("1", 130) + strings.Repeat("0", 62)
	out := Result{Chain: "bsc", Outcome: "failed", Behavior: "执行失败", Transaction: map[string]any{"input": input, "to_address": hotTreasury}, Movements: []Movement{}}
	identifyProtocol(&out)
	if out.Protocol == nil || out.Protocol.PayoutMatched || out.Behavior != "执行失败" {
		t.Fatal("failed withdrawal marked paid")
	}
}
func TestSolanaInstructionPrecisionAndAddressCase(t *testing.T) {
	in := base()
	in.Chain = "solana"
	d := 9
	in.Native = Asset{Kind: "native", Symbol: "SOL", Decimals: &d}
	in.Transaction["value_raw"] = "1000000000"
	in.Internals = []map[string]any{{"internal_index": uint32(0), "depth": uint16(0), "call_type": "system_transfer", "success": uint8(1), "from_address": "AbCNativeSource", "to_address": "aBCReceiver", "value_raw": "1000000001"}}
	out := Build(in)
	if len(out.Movements) != 1 || out.Movements[0].Amount != "1.000000001" || out.Movements[0].From != "AbCNativeSource" {
		t.Fatalf("Solana instruction mangled: %+v", out.Movements)
	}
	in.Transaction["from_address"] = "AbCNativeSource"
	in.Transaction["to_address"] = "aBCReceiver"
	in.Transaction["value_raw"] = "1000000001"
	out = Build(in)
	if len(out.Movements) != 1 {
		t.Fatal("Solana outer summary erased the underlying system transfer")
	}
}
func TestRemovedLogsDoNotReappearFromIndexedRows(t *testing.T) {
	in := base()
	l := logRow(transfer, 7, treasury, recipient, "100")
	l["removed"] = true
	in.Logs = []map[string]any{l}
	in.Tokens = []map[string]any{{"token_address": token, "log_index": uint32(7), "from_address": treasury, "to_address": recipient, "amount_raw": "100"}}
	if len(Build(in).Movements) != 0 {
		t.Fatal("removed log reintroduced from index")
	}
}
func TestIndexedRootValueWithMixedIntegerEncodingIsNotCountedTwice(t *testing.T) {
	in := base()
	in.Transaction["value_raw"] = "0x64"
	in.Internals = []map[string]any{{"internal_index": uint32(0), "depth": uint16(0), "call_type": "CALL", "success": uint8(1), "from_address": executor, "to_address": treasury, "value_raw": "100"}}
	if len(Build(in).Movements) != 1 {
		t.Fatal("decimal internal root duplicated hex outer value")
	}
}
