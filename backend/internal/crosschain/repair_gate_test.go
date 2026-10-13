package crosschain

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func realProofFixture(t *testing.T, id string) *Transfer {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + id + "-funds-proof.json")
	if err != nil {
		t.Fatal(err)
	}
	var v Transfer
	if err = json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return &v
}

func TestRepairProofsAndCorruptions(t *testing.T) {
	for _, id := range []string{"B04", "B12", "B14", "B36", "B38", "H04", "H13"} {
		v := realProofFixture(t, id)
		ApplyFundsGate(v)
		if !v.FundsVerified {
			t.Fatalf("real proof %s rejected: %v", id, v.BlockingReasons)
		}
		if v.FinalWalletVerified || v.FundingScope != "protocol_bound_bridge_participants" {
			t.Fatal("bridge proof claimed final wallet")
		}
	}
	cctp := map[string]func(*Transfer){
		"source_hash":       func(v *Transfer) { v.Proof.Protocol.CCTP.SourceTxHash = "0x" + strings.Repeat("1", 64) },
		"attested_bytes":    func(v *Transfer) { v.Proof.Protocol.CCTP.Message = "0x" + strings.Repeat("0", 752) },
		"attestation":       func(v *Transfer) { v.Proof.Protocol.CCTP.Attestation = "0x" + strings.Repeat("0", 260) },
		"authority":         func(v *Transfer) { v.Proof.Protocol.CCTP.Evidence.Source = "https://example.com/messages" },
		"burn":              func(v *Transfer) { v.Proof.Protocol.CCTP.SourceBurn.AmountRaw = "1" },
		"minter":            func(v *Transfer) { v.Proof.Protocol.CCTP.SourceMinter = ZeroEVMAddress },
		"nonce":             func(v *Transfer) { v.Proof.Protocol.CCTP.Nonce = "0x" + strings.Repeat("0", 64) },
		"execution":         func(v *Transfer) { v.Proof.Protocol.CCTP.ExecutedCallMessageSHA256 = "" },
		"transmitter":       func(v *Transfer) { v.Proof.Protocol.Destination.Contract.HistoricalState = false },
		"application":       func(v *Transfer) { v.Proof.Protocol.Source.ApplicationContract = nil },
		"destination_trace": func(v *Transfer) { v.Proof.Legs[1].TraceSHA256 = "" },
	}
	for name, corrupt := range cctp {
		t.Run("CCTP_"+name, func(t *testing.T) {
			v := realProofFixture(t, "B04")
			corrupt(v)
			ApplyFundsGate(v)
			if v.FundsVerified {
				t.Fatal("corrupt CCTP evidence accepted")
			}
		})
	}
	across := map[string]func(*Transfer){
		"caller":             func(v *Transfer) { v.Proof.Legs[0].SourceCall.From = ZeroEVMAddress },
		"declared_depositor": func(v *Transfer) { v.Proof.Legs[0].ProtocolParty = ZeroEVMAddress },
		"call_bytes":         func(v *Transfer) { v.Proof.Legs[0].SourceCall.Input = "0xdeadbeef" },
		"block_time":         func(v *Transfer) { v.Proof.Legs[0].SourceCall.BlockTimestamp++ },
		"trace":              func(v *Transfer) { v.Proof.Legs[0].TraceSHA256 = "" },
		"actual_funding":     func(v *Transfer) { v.Proof.Legs[0].Movements[0].AmountRaw = "1" },
	}
	for name, corrupt := range across {
		t.Run("Across_"+name, func(t *testing.T) {
			v := realProofFixture(t, "H04")
			corrupt(v)
			ApplyFundsGate(v)
			if v.FundsVerified {
				t.Fatal("corrupt caller evidence accepted")
			}
		})
	}
}
