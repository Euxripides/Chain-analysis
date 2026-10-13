package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/crosschain"
	"github.com/blockchain-platform/blockchain-platform/internal/ingest"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

func (b *bridgeReadSession) anchor(a crosschain.Anchor) ([]crosschain.Movement, bool, error) {
	rows := []crosschain.Movement{}
	cut := false
	if b.s.query != nil {
		ctx, cancel := context.WithTimeout(b.ctx, 3*time.Second)
		var err error
		rows, cut, err = b.s.query.CrossChainAnchor(ctx, a)
		cancel()
		if err != nil {
			rows = nil
		}
		if cut {
			return rows, true, nil
		}
	}
	if len(rows) > 0 {
		if _, err := crosschain.SelectAnchor(a, rows); err == nil {
			return rows, cut, nil
		}
	}
	record, err := b.record(a.Chain, a.TxHash, a.Dataset != "token_transfers")
	if err != nil {
		return nil, false, err
	}
	out := []crosschain.Movement{}
	for _, m := range record.Movements {
		if m.Dataset == a.Dataset && (a.EventIndex == nil || m.Index == *a.EventIndex) {
			out = append(out, m)
		}
	}
	return out, record.Truncated, nil
}

func withinCandidateScope(req crosschain.CandidateRequest, m crosschain.Movement) bool {
	if m.Chain != req.SearchChain || m.Timestamp.Before(req.StartTime) || m.Timestamp.After(req.EndTime) || m.Status != "success" || m.Amount == "" {
		return false
	}
	if req.AssetKind == "token" {
		if m.Dataset != "token_transfers" || !crosschain.Same(req.SearchChain, m.TokenContract, req.TokenContract) {
			return false
		}
	} else if m.Dataset == "token_transfers" {
		return false
	}
	address, other := m.To, m.From
	if req.SearchDirection == "out" {
		address, other = m.From, m.To
	}
	if req.Counterparty != "" && !crosschain.Same(req.SearchChain, other, req.Counterparty) {
		return false
	}
	for _, a := range req.SearchAddresses {
		if crosschain.Same(req.SearchChain, address, a) {
			return true
		}
	}
	return false
}

func (b *bridgeReadSession) search(req crosschain.CandidateRequest) ([]crosschain.Movement, bool, []string, error) {
	rows := []crosschain.Movement{}
	sources := []string{}
	cut := false
	if b.s.query != nil {
		ctx, cancel := context.WithTimeout(b.ctx, 4*time.Second)
		indexed, truncated, err := b.s.query.CrossChainSearch(ctx, req)
		cancel()
		if err == nil {
			rows = append(rows, indexed...)
			cut = truncated
			sources = append(sources, "chain_index")
		}
	}
	// A native protocol lookup supplies exact transaction identifiers, never
	// the test oracle. Fetch those transactions before scanning an address page.
	if b.s.crossChain != nil && len(rows) < req.ScanLimit {
		ctx, cancel := context.WithTimeout(b.ctx, 10*time.Second)
		result, err := b.s.crossChain.Query(ctx, crosschain.QueryRequest{Bridge: "auto", Chain: req.Anchor.Chain, TxHash: req.Anchor.TxHash, MaxResults: 5})
		cancel()
		if err == nil {
			for _, t := range result.Transfers {
				for _, legs := range [][]crosschain.Leg{t.Source, t.Destination, t.Refund} {
					for _, leg := range legs {
						if leg.Chain != req.SearchChain || leg.TxHash == "" {
							continue
						}
						record, err := b.record(leg.Chain, leg.TxHash, req.AssetKind == "native")
						if err != nil {
							cut = true
							continue
						}
						for _, m := range record.Movements {
							if withinCandidateScope(req, m) {
								rows = append(rows, m)
							}
						}
						sources = append(sources, "protocol_transaction_rpc")
						cut = cut || record.Truncated
					}
				}
			}
		}
	}
	if len(rows) == 0 {
		if req.SearchChain == "solana" {
			found, truncated, err := b.solanaSearch(req)
			if err == nil {
				rows = append(rows, found...)
				cut = cut || truncated
				sources = append(sources, "bounded_solana_signatures")
			} else {
				cut = true
			}
		} else if b.s.deps.Prov != nil {
			found, truncated, err := b.providerSearch(req)
			if err == nil {
				rows = append(rows, found...)
				cut = cut || truncated
				sources = append(sources, "bounded_provider_address_page")
			} else {
				cut = true
			}
		}
	}
	seen := map[string]bool{}
	out := []crosschain.Movement{}
	for _, m := range rows {
		if withinCandidateScope(req, m) && !seen[m.Identity()] {
			seen[m.Identity()] = true
			out = append(out, m)
		}
	}
	if len(out) > req.ScanLimit {
		out = out[:req.ScanLimit]
		cut = true
	}
	if len(sources) == 0 {
		return out, true, sources, fmt.Errorf("candidate sources were unavailable within the read budget")
	}
	return out, cut, sources, nil
}

func (b *bridgeReadSession) solanaSearch(req crosschain.CandidateRequest) ([]crosschain.Movement, bool, error) {
	out := []crosschain.Movement{}
	truncated := false
	read := 0
	for _, address := range req.SearchAddresses {
		if b.budget() != nil {
			return out, true, fmt.Errorf("candidate read budget exhausted")
		}
		raw, _, err := solanaRPCCall(b.ctx, "getSignaturesForAddress", []any{address, map[string]any{"limit": min(req.ScanLimit, 50), "commitment": "finalized"}})
		if err != nil {
			return out, true, fmt.Errorf("Solana candidate signature page unavailable")
		}
		var signatures []struct {
			Hash string `json:"signature"`
			Time int64  `json:"blockTime"`
			Err  any    `json:"err"`
		}
		if jsonUnmarshal(raw, &signatures) != nil {
			return out, true, fmt.Errorf("Solana candidate page unreadable")
		}
		if len(signatures) >= min(req.ScanLimit, 50) {
			truncated = true
		}
		for _, s := range signatures {
			stamp := time.Unix(s.Time, 0)
			if s.Err != nil || stamp.Before(req.StartTime) || stamp.After(req.EndTime) {
				continue
			}
			if read >= 8 {
				return out, true, nil
			}
			read++
			record, err := b.record("solana", s.Hash, req.AssetKind == "native")
			if err != nil {
				truncated = true
				continue
			}
			for _, m := range record.Movements {
				if withinCandidateScope(req, m) {
					out = append(out, m)
				}
			}
			truncated = truncated || record.Truncated
		}
	}
	return out, truncated, nil
}

func (b *bridgeReadSession) providerSearch(req crosschain.CandidateRequest) ([]crosschain.Movement, bool, error) {
	out := []crosschain.Movement{}
	truncated := false
	datasets := []string{"token_transfers"}
	if req.AssetKind == "native" {
		datasets = []string{"transactions", "internal_transactions"}
	}
	readHashes := map[string]bool{}
	for _, address := range req.SearchAddresses {
		for _, dataset := range datasets {
			if b.budget() != nil {
				return out, true, fmt.Errorf("candidate provider budget exhausted")
			}
			ctx, cancel := context.WithTimeout(b.ctx, 8*time.Second)
			page, err := b.s.deps.Prov.Fetch(ctx, provider.FetchRequest{Chain: req.SearchChain, Dataset: dataset, Address: address, QueryMode: "time", FromTime: &req.StartTime, ToTime: &req.EndTime, PageSize: min(req.ScanLimit, 50), Priority: "interactive"})
			cancel()
			if err != nil {
				return out, true, fmt.Errorf("bounded candidate page unavailable")
			}
			truncated = truncated || page.HasMore || page.NextPageToken != ""
			if len(page.Rows) > 50 {
				page.Rows = page.Rows[:50]
				truncated = true
			}
			for _, raw := range page.Rows {
				h := ""
				stamp := time.Time{}
				if dataset == "token_transfers" {
					row, err := ingest.NormalizeTokenTransfer(raw, req.SearchChain, b.s.deps.Prov.Name())
					if err != nil || !crosschain.Same(req.SearchChain, row.TokenAddress, req.TokenContract) {
						continue
					}
					h, stamp = row.TxHash, row.Timestamp
				} else {
					var obj map[string]any
					if jsonUnmarshal(raw, &obj) != nil {
						continue
					}
					h = mapString(obj, "transactionHash", "hash", "tx_hash", "txID")
					stamp = mapTime(obj)
					if h == "" {
						continue
					}
				}
				if stamp.IsZero() || stamp.Before(req.StartTime) || stamp.After(req.EndTime) {
					continue
				}
				if readHashes[h] {
					continue
				}
				if len(readHashes) >= 8 {
					return out, true, nil
				}
				readHashes[h] = true
				record, err := b.record(req.SearchChain, h, req.AssetKind == "native")
				if err != nil {
					truncated = true
					continue
				}
				for _, m := range record.Movements {
					if withinCandidateScope(req, m) {
						out = append(out, m)
					}
				}
				truncated = truncated || record.Truncated
			}
		}
	}
	return out, truncated, nil
}

func mapString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}
func mapTime(m map[string]any) time.Time {
	for _, k := range []string{"timestamp", "blockTimestamp", "blockTimeStamp", "timeStamp"} {
		value := fmt.Sprint(m[k])
		if stamp, err := time.Parse(time.RFC3339, value); err == nil {
			return stamp
		}
		if stamp, err := strconv.ParseInt(value, 10, 64); err == nil {
			if stamp > 1000000000000 {
				return time.UnixMilli(stamp)
			}
			return time.Unix(stamp, 0)
		}
	}
	return time.Time{}
}
func jsonUnmarshal(raw []byte, value any) error {
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.UseNumber()
	return d.Decode(value)
}
