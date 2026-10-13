package transactionanalysis

import (
	"fmt"
	"math/big"
	"strings"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/chaintrace"
)

const executor = "0x1111111111111111111111111111111111111111"
const treasury = "0x2222222222222222222222222222222222222222"
const recipient = "0x3333333333333333333333333333333333333333"
const token = "0x4444444444444444444444444444444444444444"

func addr(s string) string { return "0x" + strings.Repeat("0", 24) + s[2:] }
func word(s string) string {
	n, ok := Integer(s)
	if !ok {
		panic(s)
	}
	return fmt.Sprintf("%064x", n)
}
func base() Input {
	d := 18
	return Input{Chain: "bsc", Hash: "0x" + strings.Repeat("a", 64), Native: Asset{Kind: "native", Symbol: "BNB", Decimals: &d}, Transaction: map[string]any{"status": uint8(1), "block_number": uint64(98845775), "from_address": executor, "to_address": treasury, "value_raw": "0x0", "gas": uint64(3000000), "gas_used": uint64(140632), "effective_gas_price": "52500000"}, Metadata: map[string]Asset{token: {Kind: "token", Contract: token, Symbol: "USDT", Decimals: &d}}, ReceiptPresent: true}
}
func logRow(sig string, index uint64, from, to, raw string) map[string]any {
	return map[string]any{"address": token, "log_index": index, "topics": []string{sig, addr(from), addr(to)}, "data": "0x" + word(raw), "source_provider": "receipt"}
}

func TestZeroOuterValueInternalReceiptFeeAndRoles(t *testing.T) {
	in := base()
	to := chain.Address(recipient)
	rootTo := chain.Address(treasury)
	in.Trace = &chaintrace.TraceResult{Root: &chaintrace.CallFrame{Type: "CALL", From: chain.Address(executor), To: &rootTo, Value: "0", Children: []chaintrace.CallFrame{{Type: "CALL", From: chain.Address(treasury), To: &to, Value: "7631125135013813"}}}}
	out := Build(in)
	if len(out.Movements) != 1 || out.Movements[0].Amount != "0.007631125135013813" || out.Fee.Amount != "0.00000738318" {
		t.Fatalf("wrong movement/fee: %+v", out)
	}
	for _, p := range out.Participants {
		if p.Address == executor {
			for _, r := range p.Roles {
				if r == "实际转出方" {
					t.Fatal("executor misidentified as actual debit")
				}
			}
		}
	}
	if out.BalanceSnapshots != nil || out.Coverage["balance_snapshots"] != false {
		t.Fatal("fabricated balance snapshot")
	}
}
func TestLogIdentityDedupAndNFTNotERC20(t *testing.T) {
	in := base()
	in.Logs = []map[string]any{logRow(transfer, 2, treasury, recipient, "1000000000000000001"), {"address": token, "log_index": uint64(3), "topics": []string{transfer, addr(treasury), addr(recipient), "0x" + word("125")}, "data": "0x"}}
	in.Tokens = []map[string]any{{"token_address": token, "log_index": uint32(2), "from_address": treasury, "to_address": recipient, "amount_raw": "1000000000000000001", "token_symbol": "USDT", "token_decimals": uint16(18)}}
	in.NFTs = []map[string]any{{"contract_address": token, "log_index": uint32(3), "token_id": "125", "from_address": treasury, "to_address": recipient, "amount_raw": "1", "token_standard": "erc721"}}
	out := Build(in)
	if len(out.Movements) != 2 || out.Movements[0].Amount != "1.000000000000000001" || out.Movements[1].Asset.Kind != "nft" || out.Movements[1].Asset.TokenID != "125" {
		t.Fatalf("identity/precision error: %+v", out.Movements)
	}
	if len(out.Deltas) != 4 {
		t.Fatalf("NFT/coin assets must remain separate: %d", len(out.Deltas))
	}
}
func TestFailureAncestorsAndDelegateValuesExcluded(t *testing.T) {
	in := base()
	to := chain.Address(recipient)
	rootTo := chain.Address(treasury)
	in.Transaction["value_raw"] = "100"
	in.Trace = &chaintrace.TraceResult{Root: &chaintrace.CallFrame{Type: "CALL", From: chain.Address(executor), To: &rootTo, Value: "100", Children: []chaintrace.CallFrame{{Type: "DELEGATECALL", From: chain.Address(treasury), To: &to, Value: "100"}, {Type: "CALL", From: chain.Address(treasury), To: &to, Value: "200", Error: "revert", Children: []chaintrace.CallFrame{{Type: "CALL", From: chain.Address(treasury), To: &to, Value: "300"}}}}}}
	out := Build(in)
	if len(out.Movements) != 1 || out.Movements[0].ID != "tx:value" {
		t.Fatalf("root/delegate/reverted child duplicated: %+v", out.Movements)
	}
	in.Transaction["status"] = uint8(0)
	in.Logs = []map[string]any{logRow(transfer, 2, treasury, recipient, "9")}
	out = Build(in)
	if len(out.Movements) != 0 || out.Fee.Amount == "" {
		t.Fatal("failed transaction has movements or lost actual gas fee")
	}
}
func TestApprovalsAreNotFundsAndAllowanceRemainsExact(t *testing.T) {
	in := base()
	max := "115792089237316195423570985008687907853269984665640564039457584007913129639935"
	in.Logs = []map[string]any{logRow(approval, 4, executor, treasury, max), logRow(approval, 5, executor, treasury, "0")}
	out := Build(in)
	if len(out.Movements) != 0 || len(out.Deltas) != 0 || len(out.Approvals) != 2 || out.Approvals[0].Operation != "无限额授权" || out.Approvals[0].AmountRaw != max || out.Approvals[1].Operation != "撤销授权" {
		t.Fatalf("bad approval semantics: %+v", out)
	}
}
func TestERC1155BatchPreservesItemIdentity(t *testing.T) {
	in := base()
	in.Logs = []map[string]any{{"address": token, "log_index": uint64(6), "topics": []string{batch, addr(executor), addr(treasury), addr(recipient)}, "data": "0x" + word("64") + word("160") + word("2") + word("1") + word("2") + word("2") + word("3") + word("4")}}
	out := Build(in)
	if len(out.Movements) != 2 || out.Movements[0].Asset.TokenID != "1" || out.Movements[0].AmountRaw != "3" || out.Movements[1].Asset.TokenID != "2" || out.Movements[1].AmountRaw != "4" {
		t.Fatalf("bad batch: %+v", out.Movements)
	}
	in.Logs[0]["data"] = "0x" + word("64") + word("9999999999999999999999999")
	if len(Build(in).Movements) != 0 {
		t.Fatal("malformed batch accepted")
	}
}
func TestUnknownPrecisionPendingAndMalformedAmounts(t *testing.T) {
	in := base()
	in.Metadata = nil
	in.Logs = []map[string]any{logRow(transfer, 2, treasury, recipient, "9007199254740993123456789")}
	out := Build(in)
	if out.Movements[0].Amount != "" || out.Movements[0].AmountRaw != "9007199254740993123456789" {
		t.Fatal("invented precision")
	}
	for _, v := range []any{"", "0x", "1e40", "-1", "1.5", float64(1)} {
		if _, ok := Integer(v); ok {
			t.Fatalf("accepted invalid integer %#v", v)
		}
	}
	in.Transaction["status"] = nil
	in.Transaction["source_provider"] = "rpc"
	in.Transaction["block_number"] = nil
	out = Build(in)
	if out.Outcome != "pending" || len(out.Movements) > 0 || out.Fee.Amount != "" {
		t.Fatal("pending transaction treated as executed")
	}
}
func TestFeeUsesEffectivePriceAndDoesNotGuessDynamicFee(t *testing.T) {
	in := base()
	in.Transaction["gas_price"] = "99999999"
	out := Build(in)
	if out.Fee.UnitPriceRaw != "52500000" {
		t.Fatal("max/declared fee used")
	}
	delete(in.Transaction, "effective_gas_price")
	in.Transaction["tx_type"] = "2"
	out = Build(in)
	if out.Fee.Amount != "" {
		t.Fatal("invented dynamic effective fee")
	}
	in.Transaction["tx_type"] = "0"
	out = Build(in)
	if out.Fee.UnitPriceRaw != "99999999" {
		t.Fatal("legacy actual gasPrice missing")
	}
}
func TestSelfTransferAndMultiAssetDeltasConserve(t *testing.T) {
	in := base()
	in.Logs = []map[string]any{logRow(transfer, 1, executor, treasury, "10000000000000000001"), logRow(transfer, 2, treasury, executor, "1000000000000000000"), logRow(transfer, 3, executor, executor, "99")}
	out := Build(in)
	sum := new(big.Int)
	for _, d := range out.Deltas {
		n, _ := new(big.Int).SetString(d.NetRaw, 10)
		sum.Add(sum, n)
		if d.Address == executor && d.NetRaw != "-9000000000000000001" {
			t.Fatalf("wrong net %s", d.NetRaw)
		}
	}
	if sum.Sign() != 0 {
		t.Fatal("transfer delta not conserved")
	}
}
