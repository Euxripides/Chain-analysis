package api

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/crosschain"
	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
)

func TestStrictFinalityRequiresNetworkCanonicalBlockAndFinalizedHead(t *testing.T) {
	for _, mode := range []string{"valid", "wrong_network", "unfinalized", "reorg", "missing_finalized", "budget"} {
		t.Run(mode, func(t *testing.T) {
			b, p := bridgeFixture(t)
			r, err := b.record("bsc", bridgeHash, false)
			if err != nil {
				t.Fatal(err)
			}
			p.Handle("eth_chainId", func(req rpc.Request) (json.RawMessage, *rpc.Error) {
				if mode == "wrong_network" {
					return json.RawMessage("\"0x1\""), nil
				}
				return json.RawMessage("\"0x38\""), nil
			})
			p.Handle("eth_getBlockByNumber", func(req rpc.Request) (json.RawMessage, *rpc.Error) {
				ref, _ := req.Params[0].(string)
				if mode == "missing_finalized" && ref == "finalized" {
					return json.RawMessage("null"), nil
				}
				n := "0x64"
				h := bridgeBlock
				if ref == "finalized" {
					n = "0x200"
					if mode == "unfinalized" {
						n = "0x1"
					}
				} else if mode == "reorg" {
					h = "0x" + strings.Repeat("c", 64)
				}
				return json.RawMessage(fmt.Sprintf("{\"number\":%q,\"hash\":%q,\"timestamp\":\"0x69ac0b00\",\"parentHash\":%q,\"gasUsed\":\"0x1\",\"gasLimit\":\"0x10000\",\"transactions\":[]}", n, h, bridgeBlock)), nil
			})
			if mode == "budget" {
				b.calls = 32
			}
			proof, err := b.finalizedRecord(r)
			if mode == "valid" {
				if err != nil || !proof.Canonical || !proof.Finalized || proof.NetworkID != "56" || p.Calls("eth_getBlockByNumber") != 3 {
					t.Fatal(proof, err, p.Calls("eth_getBlockByNumber"))
				}
			} else if err == nil {
				t.Fatal("unsafe finality accepted", mode, proof)
			}
		})
	}
}
func TestStrictFundingDoesNotTakeFirstMatchingBranch(t *testing.T) {
	r := &bridgeRecord{Chain: "bsc", Hash: bridgeHash, Movements: []crosschain.Movement{}}
	e := crosschain.DecodedBridgeEvent{Protocol: "across", Role: "source", Party: bridgeWallet, Counterparty: bridgePool, Asset: bridgePool, AmountRaw: "100"}
	first := crosschain.Movement{Chain: "bsc", TxHash: bridgeHash, Dataset: "token_transfers", Index: 1, From: bridgeWallet, To: bridgePool, TokenContract: bridgePool, AmountRaw: "100", Decimals: 0, Status: "success"}
	r.Movements = []crosschain.Movement{first}
	if len(boundTokenFunding(e, r)) != 1 {
		t.Fatal("direct funding missing")
	}
	other := first
	other.Index = 2
	r.Movements = append(r.Movements, other)
	if len(boundTokenFunding(e, r)) != 2 {
		t.Fatal("ambiguous direct events collapsed")
	}
	intermediate := "0x" + strings.Repeat("9", 40)
	first.To = intermediate
	other.From = intermediate
	other.To = bridgePool
	r.Movements = []crosschain.Movement{first, other}
	paths := boundTokenFunding(e, r)
	if len(paths) != 1 || len(paths[0]) != 2 {
		t.Fatal("router funding path missing", paths)
	}
	other.Status = "failed"
	r.Movements = []crosschain.Movement{first, other}
	if len(boundTokenFunding(e, r)) != 0 {
		t.Fatal("rolled back funding accepted")
	}
	other.Status = "success"
	other.TokenContract = bridgeWallet
	r.Movements = []crosschain.Movement{first, other}
	if len(boundTokenFunding(e, r)) != 0 {
		t.Fatal("two different assets treated as one path")
	}
}
