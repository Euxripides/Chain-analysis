package crosschain

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestLayerZeroRealFundingGateRejectsAlteredEvidence(t *testing.T) {
	for _, id := range []string{"B08", "B41", "H41", "H44", "H47", "H48", "N09", "N10"} {
		t.Run(id, func(t *testing.T) {
			raw, err := os.ReadFile("testdata/lz-" + id + "-proof.json")
			if err != nil {
				t.Fatal(err)
			}
			read := func() Transfer {
				var v Transfer
				if json.Unmarshal(raw, &v) != nil {
					t.Fatal("invalid proof fixture")
				}
				return v
			}
			v := read()
			v.MessageOnly = true
			ApplyFundsGate(&v)
			if !v.FundsVerified || v.MessageOnly {
				t.Fatalf("real proof: %v", v.BlockingReasons)
			}
			mutants := map[string]func(*Transfer){
				"absent application proof":  func(v *Transfer) { v.Proof.Protocol.LayerZero = nil },
				"changed GUID":              func(v *Transfer) { v.Proof.Protocol.Identifier = "0x" + strings.Repeat("f", 64) },
				"changed delivery input":    func(v *Transfer) { v.Proof.Protocol.LayerZero.DeliveryCall.Input = "0x" },
				"wrong delivery target":     func(v *Transfer) { v.Proof.Protocol.LayerZero.DeliveryCall.To = v.Proof.Legs[1].Party },
				"changed application event": func(v *Transfer) { v.Proof.Protocol.LayerZero.Applications[0].Event.RawData = "0x" },
				"wrong application code": func(v *Transfer) {
					v.Proof.Protocol.Source.ApplicationContract.Address = v.Proof.Protocol.Source.Contract.Address
				},
				"wrong token getter": func(v *Transfer) {
					v.Proof.Protocol.LayerZero.Applications[0].Getters["token()"] = "0x" + strings.Repeat("0", 24) + strings.Repeat("f", 40)
				},
				"wrong peer getter": func(v *Transfer) {
					for k := range v.Proof.Protocol.LayerZero.Applications[0].Getters {
						if strings.HasPrefix(k, "peers(") {
							v.Proof.Protocol.LayerZero.Applications[0].Getters[k] = "0x" + strings.Repeat("0", 64)
						}
					}
				},
				"wrong shared precision": func(v *Transfer) {
					v.Proof.Protocol.LayerZero.Applications[1].Getters["sharedDecimals()"] = "0x" + strings.Repeat("0", 63) + "5"
				},
				"changed principal":  func(v *Transfer) { v.Proof.Legs[0].BridgeAmountRaw = "1" },
				"wrong asset":        func(v *Transfer) { v.Proof.Legs[0].Asset = v.Proof.Legs[1].Party },
				"unrelated transfer": func(v *Transfer) { v.Proof.Legs[0].Movements[0].To = v.Proof.Legs[1].Party },
				"duplicate funding": func(v *Transfer) {
					v.Proof.Legs[0].Movements = append(v.Proof.Legs[0].Movements, v.Proof.Legs[0].Movements[0])
				},
				"wrong packet kind": func(v *Transfer) { v.Proof.Protocol.LayerZero.Kind = "bus" },
				"missing finality":  func(v *Transfer) { v.Proof.Legs[1].Block.Finalized = false },
			}
			if v.Proof.Legs[0].Asset != "native" {
				mutants["event after protocol"] = func(v *Transfer) { v.Proof.Legs[0].Movements[0].Index = 9999999 }
			}
			if v.Proof.Protocol.LayerZero.Applications[0].NativeSendCall != nil {
				mutants["native fee amount changed"] = func(v *Transfer) { v.Proof.Protocol.LayerZero.Applications[0].NativeFeeRaw = "1" }
				mutants["native fee sent elsewhere"] = func(v *Transfer) { v.Proof.Legs[0].OtherDisbursements[0].To = v.Proof.Legs[0].Party }
				mutants["native source call absent"] = func(v *Transfer) { v.Proof.Protocol.LayerZero.Applications[0].NativeSendCall = nil }
				mutants["native source amount changed"] = func(v *Transfer) { v.Proof.Protocol.LayerZero.Applications[0].NativeSendCall.Value = "1" }
			}
			if v.Proof.Protocol.LayerZero.Kind == "stargate_taxi" {
				mutants["pool mapping tampered"] = func(v *Transfer) {
					for k := range v.Proof.Protocol.LayerZero.Applications[1].Getters {
						if strings.HasPrefix(k, "stargateImpls(") {
							v.Proof.Protocol.LayerZero.Applications[1].Getters[k] = "0x" + strings.Repeat("0", 64)
						}
					}
				}
				mutants["messaging proof missing"] = func(v *Transfer) { v.Proof.Protocol.LayerZero.Applications[1].MessagingContract = nil }
			}
			if v.Proof.Protocol.LayerZero.Applications[0].TokenCall != nil {
				mutants["separate burn unauthorized"] = func(v *Transfer) { v.Proof.Protocol.LayerZero.Applications[0].TokenCall.From = v.Proof.Legs[0].Party }
			}
			for name, mutate := range mutants {
				t.Run(name, func(t *testing.T) {
					bad := read()
					mutate(&bad)
					ApplyFundsGate(&bad)
					if bad.FundsVerified {
						t.Fatal("altered proof was promoted")
					}
				})
			}
		})
	}
}

func TestStargateTaxiRealEventsAndForeignApplications(t *testing.T) {
	for _, id := range []string{"B41", "H41", "H48", "N10"} {
		t.Run(id, func(t *testing.T) {
			c, logs := primaryFixtureLogs(t, "lz-"+id, "source")
			z, dlogs := primaryFixtureLogs(t, "lz-"+id, "destination")
			packets := LayerZeroSourcePackets(c, logs)
			if len(packets) != 1 {
				t.Fatalf("source packet count %d", len(packets))
			}
			p := packets[0].Packet
			if _, _, _, ok := StargateTaxi(p); !ok {
				t.Fatal("taxi payload missing")
			}
			if _, n := LayerZeroOFT(c, logs, p, true); n != 0 {
				t.Fatal("messaging contract mistaken for pool")
			}
			for side, chain := range []string{c, z} {
				list := logs
				if side == 1 {
					list = dlogs
				}
				count := 0
				for _, d := range Deployments {
					if d.Chain != chain || d.Role != "stargate_pool" {
						continue
					}
					e, n := StargateOFT(chain, list, p, d.Address, side == 0)
					count += n
					if n == 1 && (e.Identifier != p.GUID || e.PayloadSHA256 != ProofSHA(append([]byte(p.GUID), p.Message...))) {
						t.Fatal("message binding lost")
					}
				}
				if count != 1 {
					t.Fatalf("pool events %d", count)
				}
			}
			if _, n := LayerZeroDelivered(z, dlogs, p); n != 1 {
				t.Fatal("delivery not bound")
			}
			for _, kind := range []byte{0, 2, 255} {
				bad := p
				bad.Message = append([]byte(nil), p.Message...)
				bad.Message[0] = kind
				if _, _, _, ok := StargateTaxi(bad); ok {
					t.Fatal("bus or foreign message accepted")
				}
			}
			for n := 0; n < 43; n++ {
				bad := p
				bad.Message = p.Message[:n]
				if _, _, _, ok := StargateTaxi(bad); ok {
					t.Fatal("truncated taxi accepted")
				}
			}
			bad := p
			bad.Message = append([]byte(nil), p.Message...)
			bad.Message[3] = 1
			if _, _, _, ok := StargateTaxi(bad); ok {
				t.Fatal("non-EVM recipient accepted")
			}
		})
	}
}

func TestUSDT0SeparateTokenCallCannotAuthorizeArbitraryBurn(t *testing.T) {
	e := DecodedBridgeEvent{Protocol: "layerzero", Role: "source", SourceChain: "arbitrum", DestinationChain: "polygon", Application: DeploymentAddress("layerzero", "arbitrum", "usdt0_application"), Asset: "0xfd086bc7cd5c481dcc9c85ebe478a1c0b69fcbb9", Party: "0x023fa838682c115c2cfba96ef3791cb5bd931fc7", AmountRaw: "1000000"}
	call := FundingSourceCall{From: e.Application, To: e.Asset, Value: "0", Input: "0x9dc29fac000000000000000000000000023fa838682c115c2cfba96ef3791cb5bd931fc700000000000000000000000000000000000000000000000000000000000f4240"}
	if !USDT0TokenCall(e, call) {
		t.Fatal("registered USDT0 burn rejected")
	}
	for _, mutate := range []func(*FundingSourceCall){func(c *FundingSourceCall) { c.From = e.Party }, func(c *FundingSourceCall) { c.To = e.Application }, func(c *FundingSourceCall) { c.Value = "1" }, func(c *FundingSourceCall) { c.Input = c.Input[:len(c.Input)-1] + "1" }, func(c *FundingSourceCall) { c.Input = "0x40c10f19" + c.Input[10:] }, func(c *FundingSourceCall) { c.Input += strings.Repeat("0", 64) }} {
		bad := call
		mutate(&bad)
		if USDT0TokenCall(e, bad) {
			t.Fatal("mutated burn authorized funding")
		}
	}
	e.Application = DeploymentAddress("layerzero", "arbitrum", "endpoint_v2")
	call.From = e.Application
	if USDT0TokenCall(e, call) {
		t.Fatal("endpoint is not token burner")
	}
}
