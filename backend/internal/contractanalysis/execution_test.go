package contractanalysis

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExecutionRevertsDelegateAndCaughtFailure(t *testing.T) {
	raw := []byte(`{"type":"CALL","from":"0x1111111111111111111111111111111111111111","to":"0x2222222222222222222222222222222222222222","value":"0x0","calls":[{"type":"CALL","from":"a","to":"b","value":"0xa","error":"execution reverted","calls":[{"type":"CALL","from":"b","to":"c","value":"0x9"}]},{"type":"DELEGATECALL","from":"a","to":"implementation","value":"0x20"},{"type":"STATICCALL","from":"a","to":"view","value":"0x20"},{"type":"CALL","from":"a","to":"recipient","value":"0x100000000000000000000000000000000"}]}`)
	rows, e := TraceRows(raw, "0x"+strings.Repeat("a", 64), true, 20)
	if e != nil {
		t.Fatal(e)
	}
	if len(rows["executed_funds"]) != 1 {
		t.Fatalf("wrong settled movements: %s", rows["executed_funds"])
	}
	var fund map[string]any
	json.Unmarshal(rows["executed_funds"][0], &fund)
	if fund["amount_raw"] != "340282366920938463463374607431768211456" {
		t.Fatalf("precision lost: %v", fund)
	}
	failed, e := TraceRows(raw, "tx", false, 20)
	if e != nil || len(failed["executed_funds"]) != 0 {
		t.Fatal("failed root settled funds")
	}
	if _, e = TraceRows(raw, "tx", true, 2); e == nil {
		t.Fatal("trace cap must reject incomplete tree")
	}
	var delegate map[string]any
	json.Unmarshal(rows["execution_calls"][3], &delegate)
	if delegate["storage_address"] != "0x2222222222222222222222222222222222222222" {
		t.Fatal("delegate storage context lost", delegate)
	}
}
func TestStateDiffClearingAndExactIntegers(t *testing.T) {
	rows, e := StateDiffRows([]byte(`{"pre":{"a":{"storage":{"0x01":"0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"}}},"post":{"a":{"storage":{"0x02":"0x10"}}}}`), "tx", 10)
	if e != nil || len(rows) != 2 {
		t.Fatal(e, len(rows))
	}
	found := false
	for _, raw := range rows {
		var r map[string]any
		json.Unmarshal(raw, &r)
		if r["slot"] == "0x01" {
			found = true
			if r["after_raw"] != "0" || r["before_raw"] != "115792089237316195423570985008687907853269984665640564039457584007913129639935" {
				t.Fatal(r)
			}
		}
	}
	if !found {
		t.Fatal("cleared slot absent")
	}
}
func TestRequestBudgetsAndProtocol(t *testing.T) {
	q, e := Decode([]byte(`{"budget":{"max_contracts":0},"unknown":true}`))
	if e == nil {
		t.Fatal(q)
	}
	q = Request{}
	q.Defaults()
	if e = q.Validate(); e != nil {
		t.Fatal(e)
	}
	q.Budget.MaxContracts = 51
	if q.Validate() == nil {
		t.Fatal("unbounded dependency expansion")
	}
	if (Runner{Image: "node:latest"}).Ready() {
		t.Fatal("mutable engine identity allowed")
	}
}
func TestCodeOnlyDiffAndAccountLimit(t *testing.T) {
	raw := []byte(`{"pre":{"a":{"code":"0x6000","nonce":9007199254740993}},"post":{"a":{"code":"0x6001","nonce":9007199254740994},"b":{"nonce":1}}}`)
	rows, e := StateDiffRows(raw, "tx", 2)
	if e != nil || len(rows) != 2 {
		t.Fatal(e, rows)
	}
	found := false
	for _, row := range rows {
		if strings.Contains(string(row), "9007199254740994") && strings.Contains(string(row), "0x6001") {
			found = true
		}
	}
	if !found {
		t.Fatal("code or exact nonce missing")
	}
	if _, e := StateDiffRows(raw, "tx", 1); e == nil {
		t.Fatal("account rows exceeded budget")
	}
}
func TestHistoricalEthereumRules(t *testing.T) {
	if EthereumHardfork("1", 19000000, 1705173443) != "shanghai" {
		t.Fatal("wrong historical rules")
	}
	if EthereumHardfork("56", 19000000, 1705173443) != "chain_rules_unavailable" {
		t.Fatal("Ethereum rules leaked to BSC")
	}
	if EthereumHardfork("1", 22200000, 1746612311) != "prague" {
		t.Fatal("activation boundary")
	}
}
func TestSelfCallAndSelfdestructFunds(t *testing.T) {
	rows, err := TraceRows([]byte(`{"type":"CALL","from":"sender","to":"proxy","calls":[{"type":"CALL","from":"proxy","to":"proxy","value":"0x10"},{"type":"DELEGATECALL","from":"proxy","to":"implementation","calls":[{"type":"SELFDESTRUCT","from":"proxy","to":"beneficiary","value":"0x20"}]}]}`), "tx", true, 10)
	if err != nil || len(rows["executed_funds"]) != 1 {
		t.Fatal(err, rows["executed_funds"])
	}
	frame := rowTestMap(rows["execution_calls"][3])
	if frame["code_address"] != "implementation" || frame["storage_address"] != "proxy" || frame["effective_caller"] != "sender" {
		t.Fatal(frame)
	}
}
func rowTestMap(raw json.RawMessage) map[string]any {
	var row map[string]any
	_ = json.Unmarshal(raw, &row)
	return row
}
