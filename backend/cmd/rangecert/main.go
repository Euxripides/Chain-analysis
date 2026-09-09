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
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
	"github.com/blockchain-platform/blockchain-platform/internal/provider/ankr"
)

type pageEvidence struct {
	Cell       int    `json:"cell"`
	Shard      int    `json:"shard"`
	Page       int    `json:"page"`
	Start      uint64 `json:"range_start"`
	End        uint64 `json:"range_end_exclusive"`
	ReqFrom    uint64 `json:"request_from_block"`
	ReqTo      uint64 `json:"request_to_block_inclusive"`
	TokenHash  string `json:"page_token_hash"`
	Rows       int    `json:"rows"`
	Min        uint64 `json:"response_min_block"`
	Max        uint64 `json:"response_max_block"`
	OOR        int    `json:"out_of_range_rows"`
	RequestID  string `json:"request_id"`
	DurationMS int64  `json:"duration_ms"`
}

type cellResult struct {
	Ranges          int    `json:"ranges"`
	UniqueRows      int    `json:"unique_rows"`
	Fingerprint     string `json:"fingerprint"`
	Pages           int    `json:"pages"`
	OOR             int    `json:"out_of_range_rows"`
	TokenCrossShard int    `json:"token_cross_shard"`
}

func hash(s string) string {
	if s == "" {
		return ""
	}
	x := sha256.Sum256([]byte(s))
	return hex.EncodeToString(x[:])
}
func save(path, address string, start, end uint64, pages []pageEvidence, cells []cellResult, errText string) {
	r := map[string]any{"address": address, "dataset": "transactions", "range_start": start, "range_end_exclusive": end, "page_size": 10000, "generated_at": time.Now(), "pages": pages, "cells": cells}
	if errText != "" {
		r["error"] = errText
	}
	b, _ := json.MarshalIndent(r, "", "  ")
	_ = os.WriteFile(path, b, 0o600)
}

func main() {
	address := flag.String("address", "", "BSC address")
	start := flag.Uint64("start", 59400000, "inclusive")
	end := flag.Uint64("end", 59885001, "exclusive")
	out := flag.String("out", "range-certification.json", "output")
	timeout := flag.Duration("timeout", 10*time.Minute, "probe-only timeout")
	flag.Parse()
	if *address == "" || *end <= *start {
		panic("invalid arguments")
	}
	cfg := config.Load()
	c := ankr.New(ankr.Options{
		Endpoint: cfg.Ankr.Endpoint, APIKey: cfg.Ankr.APIKey, MaxPageSize: 10000,
		RequestsPerMinute: cfg.Ankr.RequestsPerMinute, RequestTimeout: *timeout, ResponseHeaderTimeout: *timeout,
		ResponseSoftLimitBytes: cfg.Ankr.ResponseSoftLimitBytes, ResponseHardLimitBytes: cfg.Ankr.ResponseHardLimitBytes,
		ConnectTimeout: cfg.Ankr.ConnectTimeout, TLSHandshakeTimeout: cfg.Ankr.TLSHandshakeTimeout, BodyIdleTimeout: cfg.Ankr.BodyIdleTimeout,
	}, nil)
	allPages := []pageEvidence{}
	cells := []cellResult{}
	for _, n := range []int{1, 2, 4, 8} {
		set := map[string]struct{}{}
		tokenOwners := map[string]string{}
		cellOOR, cross, cellPages := 0, 0, 0
		for shard := 0; shard < n; shard++ {
			a := *start + uint64(shard)*(*end-*start)/uint64(n)
			b := *start + uint64(shard+1)*(*end-*start)/uint64(n)
			token := ""
			for p := 1; ; p++ {
				f, to, _ := download.ToAnkrInclusiveRange(a, b)
				id := fmt.Sprintf("cert-%dR-s%d-p%d", n, shard+1, p)
				beg := time.Now()
				res, err := c.Fetch(context.Background(), provider.FetchRequest{Chain: "bsc", Dataset: "transactions", Address: *address, FromBlock: &f, ToBlock: &to, PageSize: 10000, PageToken: token, RequestID: id, AttemptNo: 1})
				if err != nil {
					save(*out, *address, *start, *end, allPages, cells, err.Error())
					panic(err)
				}
				inv, _ := download.ValidateTransactionRange(a, b, res.Rows)
				allPages = append(allPages, pageEvidence{Cell: n, Shard: shard + 1, Page: p, Start: a, End: b, ReqFrom: f, ReqTo: to, TokenHash: hash(token), Rows: len(res.Rows), Min: inv.MinBlock, Max: inv.MaxBlock, OOR: inv.OutOfRangeRows, RequestID: res.RequestID, DurationMS: time.Since(beg).Milliseconds()})
				cellPages++
				cellOOR += inv.OutOfRangeRows
				for _, raw := range res.Rows {
					var x map[string]any
					if json.Unmarshal(raw, &x) != nil {
						continue
					}
					tx, ok := x["hash"].(string)
					if !ok || tx == "" {
						save(*out, *address, *start, *end, allPages, cells, "transaction hash missing")
						panic("transaction hash missing")
					}
					set[tx] = struct{}{}
				}
				nextHash := hash(res.NextPageToken)
				if nextHash != "" {
					owner := fmt.Sprintf("%dR/%d", n, shard+1)
					if old, ok := tokenOwners[nextHash]; ok && old != owner {
						cross++
					} else {
						tokenOwners[nextHash] = owner
					}
				}
				save(*out, *address, *start, *end, allPages, cells, "")
				token = res.NextPageToken
				if !res.HasMore || len(res.Rows) < 10000 {
					break
				}
			}
		}
		keys := make([]string, 0, len(set))
		for k := range set {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fp := sha256.Sum256([]byte(fmt.Sprint(keys)))
		cells = append(cells, cellResult{Ranges: n, UniqueRows: len(keys), Fingerprint: hex.EncodeToString(fp[:]), Pages: cellPages, OOR: cellOOR, TokenCrossShard: cross})
		save(*out, *address, *start, *end, allPages, cells, "")
	}
	base := cells[0].Fingerprint
	exact := true
	for _, x := range cells {
		if x.Fingerprint != base || x.OOR != 0 || x.TokenCrossShard != 0 {
			exact = false
		}
	}
	raw, _ := os.ReadFile(*out)
	var final map[string]any
	_ = json.Unmarshal(raw, &final)
	final["exact_sets"] = exact
	final["completed_at"] = time.Now()
	b, _ := json.MarshalIndent(final, "", "  ")
	_ = os.WriteFile(*out, b, 0o600)
	fmt.Println(string(b))
}
