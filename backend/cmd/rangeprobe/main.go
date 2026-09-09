package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/config"
	"github.com/blockchain-platform/blockchain-platform/internal/download"
	"github.com/blockchain-platform/blockchain-platform/internal/ingest"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
	"github.com/blockchain-platform/blockchain-platform/internal/provider/ankr"
)

type pageEvidence struct {
	Shard             string    `json:"shard"`
	Page              int       `json:"page"`
	RangeStart        uint64    `json:"range_start"`
	RangeEndExclusive uint64    `json:"range_end_exclusive"`
	RequestFrom       uint64    `json:"request_from_block"`
	RequestTo         uint64    `json:"request_to_block_inclusive"`
	TokenHash         string    `json:"page_token_hash"`
	Rows              int       `json:"rows"`
	ResponseMin       uint64    `json:"response_min_block"`
	ResponseMax       uint64    `json:"response_max_block"`
	OutOfRange        int       `json:"out_of_range_rows"`
	RequestID         string    `json:"request_id"`
	Attempt           int       `json:"attempt"`
	StartedAt         time.Time `json:"started_at"`
	BodyCompleteAt    time.Time `json:"body_complete_at"`
}

func hash(s string) string {
	if s == "" {
		return ""
	}
	v := sha256.Sum256([]byte(s))
	return hex.EncodeToString(v[:])
}

func main() {
	address := flag.String("address", "", "BSC address")
	out := flag.String("out", "range-probe.json", "output")
	pages := flag.Int("pages", 3, "pages per range")
	timeout := flag.Duration("timeout", 10*time.Minute, "probe-only total request timeout")
	flag.Parse()
	if *address == "" {
		panic("address required")
	}
	cfg := config.Load()
	c := ankr.New(ankr.Options{Endpoint: cfg.Ankr.Endpoint, APIKey: cfg.Ankr.APIKey, MaxPageSize: 10000, RequestsPerMinute: cfg.Ankr.RequestsPerMinute, RequestTimeout: *timeout, ResponseHeaderTimeout: *timeout, ResponseSoftLimitBytes: cfg.Ankr.ResponseSoftLimitBytes, ResponseHardLimitBytes: cfg.Ankr.ResponseHardLimitBytes, ConnectTimeout: cfg.Ankr.ConnectTimeout, TLSHandshakeTimeout: cfg.Ankr.TLSHandshakeTimeout, BodyIdleTimeout: cfg.Ankr.BodyIdleTimeout}, nil)
	ranges := [][2]uint64{{44913751, 59885001}, {59885001, 74856251}}
	evidence := []pageEvidence{}
	fingerprints := map[string][]string{}
	for i, r := range ranges {
		token := ""
		name := fmt.Sprintf("R%d", i+1)
		for p := 1; p <= *pages; p++ {
			f, to, _ := download.ToAnkrInclusiveRange(r[0], r[1])
			id := fmt.Sprintf("probe-%s-p%d", name, p)
			res, err := c.Fetch(context.Background(), provider.FetchRequest{Chain: "bsc", Dataset: "transactions", Address: *address, FromBlock: &f, ToBlock: &to, PageSize: 10000, PageToken: token, RequestID: id, AttemptNo: 1})
			if err != nil {
				result := map[string]any{"address": *address, "dataset": "transactions", "page_size": 10000, "generated_at": time.Now(), "pages": evidence, "error": err.Error()}
				b, _ := json.MarshalIndent(result, "", "  ")
				_ = os.WriteFile(*out, b, 0o600)
				panic(err)
			}
			inv, _ := download.ValidateTransactionRange(r[0], r[1], res.Rows)
			ev := pageEvidence{Shard: name, Page: p, RangeStart: r[0], RangeEndExclusive: r[1], RequestFrom: f, RequestTo: to, TokenHash: hash(token), Rows: len(res.Rows), ResponseMin: inv.MinBlock, ResponseMax: inv.MaxBlock, OutOfRange: inv.OutOfRangeRows, RequestID: res.RequestID, Attempt: res.AttemptNo, StartedAt: res.StartedAt, BodyCompleteAt: res.BodyCompleteAt}
			evidence = append(evidence, ev)
			partial := map[string]any{"address": *address, "dataset": "transactions", "page_size": 10000, "generated_at": time.Now(), "pages": evidence}
			pb, _ := json.MarshalIndent(partial, "", "  ")
			if err := os.WriteFile(*out, pb, 0o600); err != nil {
				panic(err)
			}
			for _, raw := range res.Rows {
				var x map[string]any
				if json.Unmarshal(raw, &x) == nil {
					if h, ok := x["hash"].(string); ok {
						fingerprints[name] = append(fingerprints[name], h)
					} else if bn, ok := ingest.RawBlockNumber(raw); ok {
						fingerprints[name] = append(fingerprints[name], fmt.Sprint(bn))
					}
				}
			}
			token = res.NextPageToken
			if !res.HasMore {
				break
			}
		}
	}
	for k := range fingerprints {
		sort.Strings(fingerprints[k])
		s := sha256.Sum256([]byte(fmt.Sprint(fingerprints[k])))
		fingerprints[k] = []string{hex.EncodeToString(s[:])}
	}
	result := map[string]any{"address": *address, "dataset": "transactions", "page_size": 10000, "generated_at": time.Now(), "pages": evidence, "fingerprints": fingerprints}
	b, _ := json.MarshalIndent(result, "", "  ")
	if err := os.WriteFile(*out, b, 0o600); err != nil {
		panic(err)
	}
	fmt.Println(string(b))
}
