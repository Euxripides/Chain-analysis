package chainread

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
	"math/big"
	"strings"
	"testing"
	"time"
)

func TestInvestigationRPCDeniesWritesOverridesAndBroadLogs(t *testing.T) {
	r, _ := testReader(t)
	cfg, _ := r.chains.Get("bsc")
	a := "0x" + strings.Repeat("1", 40)
	cases := []struct {
		method string
		params []any
	}{{"eth_sendRawTransaction", []any{"0x00"}}, {"eth_call", []any{map[string]any{"to": a, "stateOverride": map[string]any{}}, "latest"}}, {"eth_call", []any{map[string]any{"to": a, "gas": "0x989681"}, "latest"}}, {"eth_call", []any{map[string]any{"to": a, "value": "0x1"}, "latest"}}, {"eth_getLogs", []any{map[string]any{"fromBlock": "latest", "toBlock": "latest"}}}, {"eth_getLogs", []any{map[string]any{"fromBlock": "0x0", "toBlock": "0x100000"}}}, {"eth_getStorageAt", []any{a, "-1", "latest"}}}
	for _, c := range cases {
		if _, _, e := ValidateInvestigationRPC(cfg, c.method, c.params); e == nil {
			t.Fatalf("accepted unsafe %s %v", c.method, c.params)
		}
	}
	if _, _, e := ValidateInvestigationRPC(cfg, "eth_getBalance", []any{a, "123"}); e != nil {
		t.Fatal(e)
	}
}
func TestStateTimeFloorAndPrecision(t *testing.T) {
	r, p := testReader(t)
	p.SetHead(10)
	p.Handle("eth_getBlockByNumber", func(req rpc.Request) (json.RawMessage, *rpc.Error) {
		params := req.Params
		str := params[0].(string)
		n := uint64(10)
		if str != "latest" {
			n, _ = chain.HexToUint64(str)
		}
		raw := fmt.Sprintf(`{"number":"0x%x","timestamp":"0x%x","hash":"0x%s","parentHash":"0x%s","gasUsed":"0x0","gasLimit":"0x0","transactions":[]}`, n, 1000+n*10, strings.Repeat("1", 64), strings.Repeat("2", 64))
		return json.RawMessage(raw), nil
	})
	at := time.Unix(1055, 0)
	point, e := r.ResolveState(context.Background(), "bsc", chain.Latest(), &at, Options{})
	if e != nil {
		t.Fatal(e)
	}
	if point.Block != 5 || point.Time.Unix() != 1050 || point.Current {
		t.Fatal(point)
	}
	raw, _ := new(big.Int).SetString("900719925474099312345678901234567890", 10)
	p.Handle("eth_getBalance", func(req rpc.Request) (json.RawMessage, *rpc.Error) {
		if req.Params[1] != "0x5" {
			t.Fatal("snapshot read was not pinned")
		}
		v, _ := json.Marshal("0x" + raw.Text(16))
		return v, nil
	})
	holder, _ := chain.ParseAddress("0x" + strings.Repeat("1", 40))
	snapshot, e := r.SnapshotBalance(context.Background(), "bsc", holder, "", point, Options{})
	if e != nil {
		t.Fatal(e)
	}
	if snapshot.Raw != raw.String() || snapshot.Balance == nil || *snapshot.Balance != "900719925474099312.34567890123456789" {
		t.Fatal(snapshot)
	}
	current, e := r.ResolveState(context.Background(), "bsc", chain.Latest(), nil, Options{})
	if e != nil || !current.Current || current.Block != 10 {
		t.Fatal(current, e)
	}
	future := time.Unix(1110, 0)
	if _, e = r.ResolveState(context.Background(), "bsc", chain.Latest(), &future, Options{}); e == nil {
		t.Fatal("future timestamp accepted")
	}
	if _, e = r.ResolveState(context.Background(), "bsc", chain.BlockRef{Tag: chain.TagPending}, nil, Options{}); e == nil {
		t.Fatal("pending state accepted as confirmed")
	}
}
