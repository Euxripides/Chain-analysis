package ankr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

func TestPaginationKeepsBoundsOnEveryPage(t *testing.T) {
	var got []map[string]any
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			Params map[string]any `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			t.Error(err)
			return
		}
		got = append(got, q.Params)
		n := len(got)
		next := ""
		if n < 3 {
			next = string(rune('A' + n))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": n, "result": map[string]any{"transactions": []any{map[string]any{"blockNumber": "0x64"}}, "nextPageToken": next}})
	}))
	defer s.Close()
	c := New(Options{Endpoint: s.URL, APIKey: "test", MaxPageSize: 10000, RequestsPerMinute: 10000, RequestTimeout: 5 * time.Second}, nil)
	f, to := uint64(100), uint64(199)
	token := ""
	for i := 0; i < 3; i++ {
		res, err := c.Fetch(context.Background(), provider.FetchRequest{Chain: "bsc", Dataset: "transactions", Address: "0x1", FromBlock: &f, ToBlock: &to, PageSize: 10000, PageToken: token, RequestID: "r"})
		if err != nil {
			t.Fatal(err)
		}
		token = res.NextPageToken
	}
	if len(got) != 3 {
		t.Fatalf("requests=%d", len(got))
	}
	for i, p := range got {
		if p["fromBlock"] != "0x64" || p["toBlock"] != "0xc7" {
			t.Fatalf("page %d lost bounds: %#v", i+1, p)
		}
	}
}
