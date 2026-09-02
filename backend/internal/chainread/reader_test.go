package chainread

import (
	"context"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/provider/mockrpc"
	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
)

func testReader(t *testing.T) (*Service, *mockrpc.Provider) {
	t.Helper()
	provider := mockrpc.New("mock")
	caps := rpc.NewCapabilityStore()
	breakers := rpc.NewBreakers(20, 0.9, time.Second)
	health := rpc.NewHealthTracker()
	concurrency := rpc.NewConcurrencyController(16)
	router := rpc.NewRouter(caps, breakers, health, concurrency)
	providerCaps, _ := provider.Capabilities(context.Background(), "bsc")
	caps.Set(provider.Name(), "bsc", providerCaps, rpc.CapSourceStatic)
	router.Register("bsc", provider, 100, 0)
	gateway := rpc.NewGateway(rpc.GatewayOptions{
		Router: router, Capabilities: caps, Breakers: breakers, Health: health,
		Concurrency: concurrency, Rate: rpc.NewRateController(10000), Cache: rpc.NewCache(100, true),
	})
	return New(gateway, rpc.NewBatcher(gateway, rpc.BatcherOptions{MaxSize: 50, MaxWait: time.Millisecond})), provider
}

func TestHeadUsesRegistrySafeDepthAndCache(t *testing.T) {
	reader, provider := testReader(t)
	provider.SetHead(1000)
	head := NewHeadService(reader)
	first, err := head.Head(context.Background(), "bsc")
	if err != nil {
		t.Fatal(err)
	}
	second, err := head.Head(context.Background(), "bsc")
	if err != nil {
		t.Fatal(err)
	}
	if first.HeadBlock != 1000 || first.SafeBlock != 985 {
		t.Fatalf("head = %+v, want head=1000 safe=985", first)
	}
	if !second.CacheHit || provider.Calls("eth_blockNumber") != 1 {
		t.Fatalf("second cache_hit=%v provider calls=%d", second.CacheHit, provider.Calls("eth_blockNumber"))
	}
}

func TestAddressTypesUseOneGatewayBatch(t *testing.T) {
	reader, provider := testReader(t)
	addresses := []string{
		"0x1111111111111111111111111111111111111111",
		"0x2222222222222222222222222222222222222222",
		"0x3333333333333333333333333333333333333333",
	}
	result, err := NewAddressTypeService(reader).ResolveAddressTypes(context.Background(), "bsc", addresses)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != len(addresses) {
		t.Fatalf("result size=%d want=%d", len(result), len(addresses))
	}
	if provider.BatchRequests() != 1 || provider.Calls("eth_getCode") != len(addresses) {
		t.Fatalf("batch requests=%d method calls=%d", provider.BatchRequests(), provider.Calls("eth_getCode"))
	}
}

func TestTransactionRejectsCorruptedNumericData(t *testing.T) {
	raw := json.RawMessage(`{"hash":"0x` + strings.Repeat("1", 64) + `","from":"0x1111111111111111111111111111111111111111",` +
		`"to":null,"value":"not-hex","nonce":"0x1","gas":"0x5208","input":"0x"}`)
	if _, err := parseTransaction(raw); err == nil || !strings.Contains(err.Error(), "value") {
		t.Fatalf("err=%v, want invalid value", err)
	}
}

func TestBlockParsesFullTransactionObjects(t *testing.T) {
	hash := "0x" + strings.Repeat("a", 64)
	raw := json.RawMessage(`{"number":"0x10","hash":"0x` + strings.Repeat("1", 64) + `",` +
		`"parentHash":"0x` + strings.Repeat("2", 64) + `","timestamp":"0x20","gasUsed":"0x30",` +
		`"gasLimit":"0x40","transactions":[{"hash":"` + hash + `"}]}`)
	block, err := parseBlock(raw)
	if err != nil {
		t.Fatal(err)
	}
	if block.Number != 16 || len(block.Transactions) != 1 || block.Transactions[0] != hash {
		t.Fatalf("block=%+v", block)
	}
}

func TestReceiptRejectsInvalidLogAddress(t *testing.T) {
	txHash := "0x" + strings.Repeat("a", 64)
	raw := json.RawMessage(`{"transactionHash":"` + txHash + `","from":"0x1111111111111111111111111111111111111111",` +
		`"to":null,"gasUsed":"0x1","cumulativeGasUsed":"0x1","logs":[{"address":"bad",` +
		`"transactionHash":"` + txHash + `","blockNumber":"0x1","logIndex":"0x0"}]}`)
	if _, err := parseReceipt(raw); err == nil || !strings.Contains(err.Error(), "log[0]") {
		t.Fatalf("err=%v, want invalid log error", err)
	}
}

func TestLogsShrinkRangeAndRetrySameSegment(t *testing.T) {
	reader, provider := testReader(t)
	provider.FailNext(1, &rpc.Error{Class: rpc.ClassRangeTooLarge, Message: "range too wide"})
	logs := NewLogsService(reader)
	result, err := logs.Fetch(context.Background(), "bsc", LogFilter{FromBlock: 100, ToBlock: 200}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 0 || provider.Calls("eth_getLogs") != 2 || logs.Window("bsc") != 2500 {
		t.Fatalf("logs=%d calls=%d window=%d", len(result), provider.Calls("eth_getLogs"), logs.Window("bsc"))
	}
}

func TestBalanceKeepsUint256Precision(t *testing.T) {
	reader, provider := testReader(t)
	want := new(big.Int).Lsh(big.NewInt(1), 200)
	provider.Handle("eth_getBalance", func(req rpc.Request) (json.RawMessage, *rpc.Error) {
		return json.RawMessage(`"0x` + want.Text(16) + `"`), nil
	})
	address := chain.MustAddress("0x1111111111111111111111111111111111111111")
	got, err := reader.Balance(context.Background(), "bsc", address, chain.Latest(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Cmp(want) != 0 {
		t.Fatalf("balance=%s want=%s", got, want)
	}
}
