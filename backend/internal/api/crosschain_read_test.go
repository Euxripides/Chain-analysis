package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
	"github.com/blockchain-platform/blockchain-platform/internal/crosschain"
	"github.com/blockchain-platform/blockchain-platform/internal/provider/mockrpc"
	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
)

const bridgeHash = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const bridgeBlock = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
const bridgeWallet = "0x1111111111111111111111111111111111111111"
const bridgePool = "0x2222222222222222222222222222222222222222"

func bridgeFixture(t *testing.T) (*bridgeReadSession, *mockrpc.Provider) {
	t.Helper()
	p := mockrpc.New("bridge-fixture")
	caps := rpc.NewCapabilityStore()
	breakers := rpc.NewBreakers(20, .9, time.Second)
	health := rpc.NewHealthTracker()
	conc := rpc.NewConcurrencyController(4)
	router := rpc.NewRouter(caps, breakers, health, conc)
	c, _ := p.Capabilities(context.Background(), "bsc")
	c.Trace = true
	c.Debug = true
	caps.Set(p.Name(), "bsc", c, rpc.CapSourceStatic)
	router.Register("bsc", p, 100, 0)
	gateway := rpc.NewGateway(rpc.GatewayOptions{Router: router, Capabilities: caps, Breakers: breakers, Health: health, Concurrency: conc, Rate: rpc.NewRateController(10000), Cache: rpc.NewCache(100, true)})
	s := &Server{deps: Deps{ChainRead: chainread.New(gateway, nil), RPCGateway: gateway}}
	p.Handle("eth_getTransactionReceipt", func(req rpc.Request) (json.RawMessage, *rpc.Error) {
		return json.RawMessage(fmt.Sprintf(`{"transactionHash":"%s","status":"0x1","blockNumber":"0x64","blockHash":"%s","from":"%s","to":"%s","gasUsed":"0x1","cumulativeGasUsed":"0x1","logs":[]}`, bridgeHash, bridgeBlock, bridgeWallet, bridgePool)), nil
	})
	p.Handle("eth_getBlockByNumber", func(req rpc.Request) (json.RawMessage, *rpc.Error) {
		return json.RawMessage(fmt.Sprintf(`{"number":"0x64","hash":"%s","timestamp":"0x69ac0b00","parentHash":"%s","gasUsed":"0x1","gasLimit":"0x10000","transactions":[]}`, bridgeBlock, bridgeBlock)), nil
	})
	p.Handle("eth_getTransactionByHash", func(req rpc.Request) (json.RawMessage, *rpc.Error) {
		return json.RawMessage(fmt.Sprintf(`{"hash":"%s","blockNumber":"0x64","blockHash":"%s","from":"%s","to":"%s","nonce":"0x1","gas":"0x10000","value":"0x64","input":"0x"}`, bridgeHash, bridgeBlock, bridgeWallet, bridgePool)), nil
	})
	return newBridgeReadSession(context.Background(), s), p
}

func TestExactHashFallbackCacheIdentityAndReadBudget(t *testing.T) {
	b, p := bridgeFixture(t)
	record, err := b.record("bsc", bridgeHash, false)
	if err != nil || record.Status != "success" {
		t.Fatal(record, err)
	}
	if _, err = b.record("bsc", bridgeHash, false); err != nil || p.Calls("eth_getTransactionReceipt") != 1 || p.Calls("eth_getBlockByNumber") != 1 {
		t.Fatal("exact hash read was not shared", err)
	}
	if _, err = b.record("bsc", "0x"+strings.Repeat("c", 64), false); err == nil {
		t.Fatal("receipt for a different transaction accepted")
	}
	b.calls = 32
	if b.budget() == nil || b.calls != 32 {
		t.Fatal("read budget exceeded")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if newBridgeReadSession(canceled, b.s).budget() == nil {
		t.Fatal("canceled query made a chain read")
	}
}

func TestNativeTraceExcludesRevertedAncestorAndDelegateValues(t *testing.T) {
	b, p := bridgeFixture(t)
	for i := 0; i < 3; i++ {
		if err := b.s.deps.RPCGateway.Budgets().Allow(rpc.BudgetContext{Purpose: rpc.PurposeTrace}, 1, true); err != nil {
			t.Fatal(err)
		}
	}
	p.Handle("trace_transaction", func(req rpc.Request) (json.RawMessage, *rpc.Error) {
		return json.RawMessage(fmt.Sprintf(`{"type":"CALL","from":"%s","to":"%s","value":"0x64","calls":[{"type":"CALL","from":"%s","to":"%s","value":"0x32","error":"execution reverted","calls":[{"type":"CALL","from":"%s","to":"%s","value":"0x32"}]},{"type":"DELEGATECALL","from":"%s","to":"%s","value":"0x64"},{"type":"CALL","from":"%s","to":"%s","value":"0x19"}]}`, bridgeWallet, bridgePool, bridgePool, bridgeWallet, bridgeWallet, bridgePool, bridgePool, bridgeWallet, bridgePool, bridgeWallet)), nil
	})
	record, err := b.record("bsc", bridgeHash, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(record.Movements) != 2 || record.Movements[0].AmountRaw != "100" || record.Movements[1].AmountRaw != "25" {
		t.Fatal("reverted/delegated/root flows duplicated", record.Movements, record.Gaps)
	}
}

func TestEthereumRPCSpellingDoesNotChangePublicLegIdentity(t *testing.T) {
	if bridgeRPCChain("ethereum") != "eth" || bridgeRPCChain("arbitrum") != "arbitrum" {
		t.Fatal("RPC spelling did not match existing registry")
	}
	b, p := bridgeFixture(t)
	caps, _ := p.Capabilities(context.Background(), "eth")
	b.s.deps.RPCGateway.Capabilities().Set(p.Name(), "eth", caps, rpc.CapSourceStatic)
	b.s.deps.RPCGateway.Router().Register("eth", p, 100, 0)
	record, err := b.record("ethereum", bridgeHash, false)
	if err != nil || record.Chain != "ethereum" {
		t.Fatal("public chain identity or Ethereum receipt unavailable", record, err)
	}
}

func TestAmountVerificationRejectsWrongAssetUnknownPrecisionAndMixedParts(t *testing.T) {
	stamp := time.Unix(1779040000, 0).UTC()
	row, _ := exactMovement("bsc", bridgeHash, "token_transfers", 1, stamp, bridgeWallet, bridgePool, bridgePool, "1000000", 6)
	leg := crosschain.Leg{Chain: "bsc", TxHash: bridgeHash, Address: bridgeWallet, TokenContract: bridgePool, AmountRaw: "1000000"}
	if len(matchingParts(leg, legMovements(leg, 0, []crosschain.Movement{row}))) != 1 {
		t.Fatal("precise funding event failed")
	}
	wrong := row
	wrong.TokenContract = bridgeWallet
	if len(matchingParts(leg, legMovements(leg, 0, []crosschain.Movement{wrong}))) != 0 {
		t.Fatal("same amount wrong asset accepted")
	}
	unknown := row
	unknown.Decimals = -1
	if reportedAmountMatches(leg, unknown) {
		t.Fatal("unknown decimals verified")
	}
	a, z := row, row
	a.AmountRaw = "400000"
	z.AmountRaw = "600000"
	z.Index = 2
	z.To = bridgeWallet
	if len(matchingParts(leg, []crosschain.Movement{a, z})) != 0 {
		t.Fatal("different beneficiary branches summed")
	}
	z.To = a.To
	if len(matchingParts(leg, []crosschain.Movement{a, z})) != 2 {
		t.Fatal("same funding party split not reconciled")
	}
	leg.AmountRaw = ""
	leg.AmountsScope = "message_only"
	if reportedAmountMatches(leg, row) {
		t.Fatal("message-only became a funding match")
	}
	if actionScopeAllowedBridgeAnalysis(context.Background()) {
		t.Fatal("read-only request implicitly acquired analysis")
	}
}
