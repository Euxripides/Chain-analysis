// V1.3 Chainbase Adapter 单元测试（无网络：nil client / BLOCKED 能力语义）。
package chainbase

import (
	"context"
	"strings"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/provider"
	"github.com/blockchain-platform/blockchain-platform/internal/provider/count"
)

func TestAdapterIDAndCapsWithoutClient(t *testing.T) {
	a := NewAdapter(AdapterOptions{TXEnabled: true})
	if a.ID() != "chainbase" {
		t.Fatalf("id=%s", a.ID())
	}
	caps := a.Capabilities(context.Background())
	if caps.ProviderID != "chainbase" {
		t.Fatalf("caps provider=%s", caps.ProviderID)
	}
	// token transfers：默认 BLOCKED（Free Plan 实测，§73）——不得被 Router 选中
	dc, ok := caps.DatasetCap(56, count.DatasetTokenTransfers)
	if !ok || dc.Enabled {
		t.Fatalf("token transfers must be disabled, ok=%v dc=%+v", ok, dc)
	}
	if dc.RuntimeStatus != count.StatusBlocked {
		t.Fatalf("token runtime=%s want BLOCKED", dc.RuntimeStatus)
	}
	if !strings.Contains(dc.Reason, "BLOCKED") && !strings.Contains(dc.Reason, "block") {
		t.Fatalf("token block reason missing: %q", dc.Reason)
	}
	// txs：无 Key → NO_KEY/BLOCKED（不能产生网络流量）
	dt, ok2 := caps.DatasetCap(56, count.DatasetTransactions)
	if !ok2 || !dt.Enabled {
		t.Fatalf("transactions capability missing: %+v", dt)
	}
	if dt.RuntimeStatus != count.StatusBlocked {
		t.Fatalf("no client configured -> runtime must be BLOCKED(NO_KEY), got %s", dt.RuntimeStatus)
	}
	if !dt.RangeVerified || !dt.SupportsRange {
		t.Fatalf("transactions range flags wrong: %+v", dt)
	}
}

func TestAdapterTokenCountNeverFires(t *testing.T) {
	a := NewAdapter(AdapterOptions{TokenEnabled: false})
	_, err := a.Count(context.Background(), count.CountRequest{
		ChainID: 56, Dataset: count.DatasetTokenTransfers,
		Subject: count.SubjectRef{Type: count.SubjectAddress, Value: "0x1234"},
	})
	if err == nil {
		t.Fatalf("token count must fail without firing network")
	}
	if ce, ok := err.(*count.CountError); !ok || ce.Class != count.ErrUnsupported {
		t.Fatalf("expected UNSUPPORTED(BLOCKED), got %v", err)
	}
	if !strings.Contains(err.Error(), "BLOCKED") {
		t.Fatalf("error must carry BLOCKED reason: %v", err)
	}
}

func TestAdapterNoClientAuth(t *testing.T) {
	a := NewAdapter(AdapterOptions{TXEnabled: true})
	_, err := a.Count(context.Background(), count.CountRequest{
		ChainID: 56, Dataset: count.DatasetTransactions,
		Subject: count.SubjectRef{Type: count.SubjectAddress, Value: "0x1234"},
	})
	if ce, ok := err.(*count.CountError); !ok || ce.Class != count.ErrAuth {
		t.Fatalf("expected AUTH for missing key, got %v", err)
	}
	if h := a.Health(context.Background()); h.Healthy {
		t.Fatalf("no key -> unhealthy")
	}
}

func TestAdapterInvalidSubject(t *testing.T) {
	opts := AdapterOptions{Client: &Client{apiKey: "k"}}
	a := NewAdapter(opts)
	_, err := a.Count(context.Background(), count.CountRequest{
		ChainID: 56, Dataset: count.DatasetTransactions,
		Subject: count.SubjectRef{Type: count.SubjectContract, Value: "0x1234"},
	})
	if ce, ok := err.(*count.CountError); !ok || ce.Class != count.ErrUnsupported {
		t.Fatalf("expected UNSUPPORTED for contract subject, got %v", err)
	}
}

func TestClassifyCbErrorMapping(t *testing.T) {
	cases := []struct {
		class, msg string
		want       count.ErrorClass
	}{{
		class: "rate_limit", msg: "Per-second credit limit", want: count.ErrRateLimit,
	}, {
		class: "rate_limit", msg: "quota exhausted for your plan", want: count.ErrQuota,
	}, {
		class: "auth", msg: "api key invalid", want: count.ErrAuth,
	}, {
		class: "request_timeout", msg: "deadline", want: count.ErrTimeout,
	}, {
		class: "upstream", msg: "boom", want: count.ErrConnection,
	}, {
		class: "invalid_json", msg: "bad json", want: count.ErrInvalidResponse,
	}, {
		class: "invalid_request", msg: "bad param", want: count.ErrUnsupported,
	}}
	for _, tc := range cases {
		perr := &provider.Error{Class: provider.ErrorClass(tc.class), Message: tc.msg, Retryable: true}
		err := classifyCbErr(perr)
		ce, ok := err.(*count.CountError)
		if !ok || ce.Class != tc.want {
			t.Fatalf("class=%s msg=%s -> %v, want %s", tc.class, tc.msg, err, tc.want)
		}
	}
}
