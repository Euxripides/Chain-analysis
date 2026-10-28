package api

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
	"github.com/blockchain-platform/blockchain-platform/internal/method"
	"github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
)

const (
	probeOwner  = "0x00000000000000000000000000000000000000a1"
	probeMinter = "0x00000000000000000000000000000000000000b2"
)

func probeRevert(data []byte, message string) error {
	return &rpc.Error{Class: rpc.ClassExecutionReverted, Message: message, RevertData: data}
}

func probeErrorString(text string) []byte {
	sel, _ := hex.DecodeString(strings.TrimPrefix(contractSelErrorString, "0x"))
	word := func(n int) []byte { w := make([]byte, 32); w[31] = byte(n); return w }
	padded := append([]byte(text), make([]byte, (32-len(text)%32)%32)...)
	return append(append(append(sel, word(32)...), word(len(text))...), padded...)
}

// A fake contract behind eth_call: OZ v5 owner and role checks, a legacy empty revert gated on the owner,
// an argument check that fails for every caller, and an open function.
func probeFakeContract(t *testing.T) contractCallFunc {
	minter := method.Topic0Of("MINTER_ROLE")
	return func(from string, data []byte) ([]byte, error) {
		sel := "0x" + hex.EncodeToString(data[:4])
		switch sel {
		case method.SelectorOf("owner()"):
			return contractAddressWord(probeOwner), nil
		case method.SelectorOf("getRoleMember(bytes32,uint256)"):
			if "0x"+hex.EncodeToString(data[4:36]) == minter {
				return contractAddressWord(probeMinter), nil
			}
			return contractAddressWord(probeOwner), nil
		case method.SelectorOf("hasRole(bytes32,address)"):
			w := make([]byte, 32)
			if strings.HasSuffix(hex.EncodeToString(data[36:68]), strings.TrimPrefix(probeMinter, "0x")) {
				w[31] = 1
			}
			return w, nil
		case method.SelectorOf("setFeeReceiver(address)"):
			if from != probeOwner {
				return nil, probeRevert(append(mustHex(contractSelOwnable), contractAddressWord(from)...), "execution reverted")
			}
			return nil, nil
		case method.SelectorOf("mint(address,uint256)"):
			if from != probeMinter {
				role, _ := hex.DecodeString(strings.TrimPrefix(minter, "0x"))
				return nil, probeRevert(append(append(mustHex(contractSelAccessControl), contractAddressWord(from)...), role...), "execution reverted")
			}
			return nil, nil
		case method.SelectorOf("legacyWithdraw(uint256)"):
			if from != probeOwner {
				return nil, probeRevert(nil, "execution reverted")
			}
			return nil, probeRevert(probeErrorString("amount is zero"), "execution reverted: amount is zero")
		case method.SelectorOf("stake(uint256)"):
			return nil, probeRevert(probeErrorString("amount is zero"), "execution reverted: amount is zero")
		case method.SelectorOf("textGated()"):
			return nil, probeRevert(probeErrorString("Ownable: caller is not the owner"), "execution reverted: Ownable: caller is not the owner")
		case method.SelectorOf("claim()"):
			return nil, nil
		}
		t.Fatalf("unexpected selector %s", sel)
		return nil, nil
	}
}

func mustHex(selector string) []byte {
	b, _ := hex.DecodeString(strings.TrimPrefix(selector, "0x"))
	return b
}

func probeResult(signatures ...string) *ca.Result {
	result := &ca.Result{Sections: map[string][]json.RawMessage{}, Metrics: map[string]any{}}
	result.Snapshot.EntryAddress, result.Snapshot.StorageAddress, result.Snapshot.CodeAddress = "0x00000000000000000000000000000000000000c3", "0x00000000000000000000000000000000000000c3", "0x00000000000000000000000000000000000000d4"
	result.Snapshot.BlockNumber, result.Snapshot.StateGranularity = 7, "block_end"
	for _, sig := range signatures {
		result.Sections["methods"] = append(result.Sections["methods"], ca.Row(map[string]any{"method_id": "m-" + sig, "selector": method.SelectorOf(sig), "behaviors": []string{"修改合约状态"}}))
	}
	return result
}

func probeRows(t *testing.T, result *ca.Result) map[string]map[string]any {
	rows := map[string]map[string]any{}
	for _, raw := range result.Sections["caller_access_probes"] {
		r := rowMap(raw)
		rows[r["selector"].(string)] = r
	}
	return rows
}

func TestCallerAccessProbesClassifyRestrictionsFromChainBehaviour(t *testing.T) {
	result := probeResult("owner()", "getRoleMember(bytes32,uint256)", "hasRole(bytes32,address)", "setFeeReceiver(address)", "mint(address,uint256)",
		"legacyWithdraw(uint256)", "stake(uint256)", "textGated()", "claim()")
	contractApplyAccessProbes(result, probeFakeContract(t))
	rows := probeRows(t, result)
	want := map[string]string{"setFeeReceiver(address)": "caller_restricted", "mint(address,uint256)": "caller_restricted", "textGated()": "caller_restricted",
		"legacyWithdraw(uint256)": "caller_restricted_by_differential", "stake(uint256)": "inconclusive", "claim()": "callable_by_probe_caller"}
	for sig, classification := range want {
		if got := rows[method.SelectorOf(sig)]["classification"]; got != classification {
			t.Fatalf("%s classification = %v, want %s; row %v", sig, got, classification, rows[method.SelectorOf(sig)])
		}
	}
	fee := rows[method.SelectorOf("setFeeReceiver(address)")]
	if fee["revert_signal"] != "ownable_unauthorized" || stringList(fee["confirmed_callers"])[0] != probeOwner {
		t.Fatalf("owner gate not confirmed: %v", fee)
	}
	mint := rows[method.SelectorOf("mint(address,uint256)")]
	if mint["required_role_name"] != "MINTER_ROLE" || !contains(stringList(mint["confirmed_callers"]), probeMinter) || contains(stringList(mint["confirmed_callers"]), probeOwner) {
		t.Fatalf("role gate: %v", mint)
	}
	if holders := mint["role_holder_checks"].([]any); len(holders) != 2 {
		t.Fatalf("hasRole checks = %v", holders)
	}
	if got := stringList(rows[method.SelectorOf("legacyWithdraw(uint256)")]["confirmed_callers"]); len(got) != 1 || got[0] != probeOwner {
		t.Fatalf("differential owner = %v", got)
	}
	for _, raw := range result.Sections["methods"] {
		m := rowMap(raw)
		if m["method_id"] == "m-setFeeReceiver(address)" && !contains(stringList(m["behaviors"]), "调用者条件（链上模拟）") {
			t.Fatalf("method summary missing: %v", m)
		}
		if m["method_id"] == "m-claim()" && contains(stringList(m["behaviors"]), "调用者条件（链上模拟）") {
			t.Fatal("open method marked restricted")
		}
	}
	cov := rowMap(result.Sections["coverage"][0])
	if cov["dimension"] != "caller_access_probes" || fmt.Sprint(cov["caller_restricted"]) != "3" || cov["complete"] != false {
		t.Fatalf("coverage = %v", cov)
	}
}

func TestCallerAccessProbesStopOnBudgetAndBoundCalls(t *testing.T) {
	sigs := []string{}
	for i := 0; i < 200; i++ {
		sigs = append(sigs, "f"+strings.Repeat("x", i%7)+string(rune('a'+i%26))+hex.EncodeToString([]byte{byte(i)})+"()")
	}
	calls := 0
	result := probeResult(sigs...)
	contractApplyAccessProbes(result, func(string, []byte) ([]byte, error) {
		calls++
		if calls > 5 {
			return nil, &rpc.Error{Class: rpc.ClassBudgetExceeded}
		}
		return nil, probeRevert(nil, "execution reverted")
	})
	cov := rowMap(result.Sections["coverage"][0])
	if calls != 6 || cov["stopped"] != string(rpc.ClassBudgetExceeded) || fmt.Sprint(cov["not_probed"]) != fmt.Sprint(len(sigs)-5) {
		t.Fatalf("calls=%d coverage=%v", calls, cov)
	}
	calls = 0
	result = probeResult(sigs...)
	contractApplyAccessProbes(result, func(string, []byte) ([]byte, error) { calls++; return nil, probeRevert(nil, "execution reverted") })
	if calls > contractProbeMaxCalls || len(result.Sections["caller_access_probes"]) != len(sigs) {
		t.Fatalf("calls=%d rows=%d", calls, len(result.Sections["caller_access_probes"]))
	}
}

func TestClassifyRevertRecognisesAccessControlTextAndRole(t *testing.T) {
	role := strings.Repeat("ab", 32)
	r := contractClassifyRevert(probeErrorString("AccessControl: account 0x00000000000000000000000000000000000000c3 is missing role 0x"+role), "")
	if r.Signal != "access_control_role" || r.Role != "0x"+role {
		t.Fatalf("%+v", r)
	}
	if r := contractClassifyRevert(nil, "execution reverted: caller is not the owner"); r.Signal != "access_error_text" {
		t.Fatalf("%+v", r)
	}
	if r := contractClassifyRevert(mustHex(method.SelectorOf("Unauthorized()")), ""); r.Signal != "access_error_name" {
		t.Fatalf("%+v", r)
	}
	if r := contractClassifyRevert(probeErrorString("trading not open"), ""); r.Signal != "error_string" || r.access() {
		t.Fatalf("%+v", r)
	}
	if r := contractClassifyRevert(append(mustHex(contractSelErrorString), make([]byte, 40)...), ""); r.Reason != "" {
		t.Fatalf("malformed Error(string) decoded: %+v", r)
	}
}

func TestCallerAccessProbeTargetsTheStorageContext(t *testing.T) {
	proxy := ca.Snapshot{EntryAddress: "0xe1", CodeAddress: "0xc1", StorageAddress: "0xe1"}
	dependency := ca.Snapshot{EntryAddress: "0xe1", CodeAddress: "0xd2", StorageAddress: "0xd2"}
	if contractProbeTarget(proxy) != "0xe1" || contractProbeTarget(dependency) != "0xd2" || contractProbeTarget(ca.Snapshot{EntryAddress: "0xe1"}) != "0xe1" {
		t.Fatal("probe target must follow the storage context")
	}
}
