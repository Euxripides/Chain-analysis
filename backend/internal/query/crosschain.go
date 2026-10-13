package query

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
	"github.com/blockchain-platform/blockchain-platform/internal/crosschain"
)

func (s *Service) CrossChainAnchor(ctx context.Context, a crosschain.Anchor) ([]crosschain.Movement, bool, error) {
	if s == nil || s.CH == nil {
		return nil, false, fmt.Errorf("chain index unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, clickhouse.InteractiveScanTimeout)
	defer cancel()
	where := "chain=? AND tx_hash=?"
	args := []any{a.Chain, a.TxHash}
	if a.EventIndex != nil {
		if a.Dataset == "token_transfers" {
			where += " AND log_index=?"
		} else if a.Dataset == "internal_transactions" {
			where += " AND call_index=?"
		} else {
			return nil, false, fmt.Errorf("invalid anchor event index")
		}
		args = append(args, *a.EventIndex)
	}
	rows, e := s.queryRows(clickhouse.InteractiveScanContext(ctx), a.Dataset, dedupSource(a.Dataset, where)+" ORDER BY "+explorerOrder(a.Dataset)+" LIMIT 401", args...)
	if e != nil {
		return nil, false, e
	}
	truncated := len(rows) > 400
	if truncated {
		rows = rows[:400]
	}
	movements, e := s.bridgeMovements(ctx, a.Chain, a.Dataset, rows)
	return movements, truncated, e
}

func (s *Service) CrossChainSearch(ctx context.Context, r crosschain.CandidateRequest) ([]crosschain.Movement, bool, error) {
	if e := r.Validate(); e != nil {
		return nil, false, e
	}
	if s == nil || s.CH == nil {
		return nil, false, fmt.Errorf("chain index unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, clickhouse.InteractiveScanTimeout)
	defer cancel()
	datasets := []string{"token_transfers"}
	if r.AssetKind == "native" {
		datasets = []string{"transactions", "internal_transactions"}
		if r.SearchChain == "solana" {
			datasets = []string{"internal_transactions"}
		}
	}
	out := []crosschain.Movement{}
	truncated := false
	for _, dataset := range datasets {
		q, args := bridgeSearchSQL(r, dataset, r.ScanLimit+1)
		rows, e := s.queryRows(clickhouse.InteractiveScanContext(ctx), dataset, q, args...)
		if e != nil {
			return nil, false, e
		}
		if len(rows) > r.ScanLimit {
			truncated = true
			rows = rows[:r.ScanLimit]
		}
		items, e := s.bridgeMovements(ctx, r.SearchChain, dataset, rows)
		if e != nil {
			return nil, false, e
		}
		out = append(out, items...)
	}
	// Remove traced root calls that duplicate the top-level value movement.
	top := map[string]bool{}
	for _, m := range out {
		if m.Dataset == "transactions" {
			top[m.TxHash+"|"+m.From+"|"+m.To+"|"+m.AmountRaw] = true
		}
	}
	filtered := []crosschain.Movement{}
	for _, m := range out {
		if m.Dataset == "internal_transactions" && m.Index == 0 && top[m.TxHash+"|"+m.From+"|"+m.To+"|"+m.AmountRaw] {
			continue
		}
		filtered = append(filtered, m)
	}
	if len(filtered) > r.ScanLimit {
		truncated = true
		filtered = filtered[:r.ScanLimit]
	}
	return filtered, truncated, nil
}

func bridgeSearchSQL(r crosschain.CandidateRequest, dataset string, limit int) (string, []any) {
	column := "to_address"
	other := "from_address"
	if r.SearchDirection == "out" {
		column, other = other, column
	}
	where := []string{"chain=?", column + " IN (?)", "timestamp>=?", "timestamp<=?"}
	args := []any{r.SearchChain, r.SearchAddresses, r.StartTime, r.EndTime}
	if r.Counterparty != "" {
		where = append(where, other+"=?")
		args = append(args, r.Counterparty)
	}
	postFilter := ""
	if dataset == "token_transfers" {
		where = append(where, "token_address=?")
		postFilter = "amount>0"
		args = append(args, r.TokenContract)
	} else {
		postFilter = "toUInt256OrZero(value_raw)>0"
		if dataset == "transactions" {
			postFilter += " AND status=1"
		} else {
			postFilter += " AND success=1"
		}
	}
	source := actionLatestStatusSource(dedupSource(dataset, strings.Join(where, " AND ")), postFilter)
	q := source + " ORDER BY " + stableActionOrder(dataset, "timestamp ASC") + " LIMIT ?"
	args = append(args, limit)
	return q, args
}

func bridgeRowString(v any) string { return fmt.Sprint(v) }
func bridgeRowUint(v any) uint32   { n, _ := strconv.ParseUint(fmt.Sprint(v), 10, 32); return uint32(n) }

func (s *Service) bridgeMovements(ctx context.Context, c, dataset string, rows []map[string]any) ([]crosschain.Movement, error) {
	statuses := map[string]uint8{}
	parentTimes := map[string]time.Time{}
	hashes := []string{}
	for _, row := range rows {
		hashes = append(hashes, bridgeRowString(row["tx_hash"]))
	}
	if dataset != "transactions" && len(hashes) > 0 {
		result, e := s.CH.DB().Query(clickhouse.InteractiveScanContext(ctx), "SELECT tx_hash,argMax(status,source_ingested_at),argMax(timestamp,source_ingested_at) FROM blockchain.transactions WHERE chain=? AND tx_hash IN (?) GROUP BY tx_hash", c, hashes)
		if e != nil {
			return nil, e
		}
		defer result.Close()
		for result.Next() {
			var h string
			var status uint8
			var timestamp time.Time
			if e := result.Scan(&h, &status, &timestamp); e != nil {
				return nil, e
			}
			statuses[h] = status
			parentTimes[h] = timestamp
		}
		if e := result.Err(); e != nil {
			return nil, e
		}
	}
	out := []crosschain.Movement{}
	for _, row := range rows {
		m := crosschain.Movement{Chain: c, TxHash: bridgeRowString(row["tx_hash"]), Dataset: dataset, From: bridgeRowString(row["from_address"]), To: bridgeRowString(row["to_address"]), Status: "unknown"}
		if ts, ok := row["timestamp"].(time.Time); ok {
			m.Timestamp = ts
		} else if ts, e := time.Parse(time.RFC3339Nano, bridgeRowString(row["timestamp"])); e == nil {
			m.Timestamp = ts
		}
		if m.Timestamp.Unix() > 0 {
			m.TimestampSource = "indexed_event"
		}
		if ts := parentTimes[m.TxHash]; m.Timestamp.Unix() <= 0 && ts.Unix() > 0 {
			m.Timestamp = ts
			m.TimestampSource = "indexed_parent_transaction"
		}
		if dataset == "token_transfers" {
			m.Index = bridgeRowUint(row["log_index"])
			m.TokenContract = bridgeRowString(row["token_address"])
			m.Token = bridgeRowString(row["token_symbol"])
			m.Decimals = int(bridgeRowUint(row["token_decimals"]))
			m.AmountRaw = bridgeRowString(row["amount_raw"])
		} else {
			m.Decimals = 18
			m.Token = map[string]string{"ethereum": "ETH", "bsc": "BNB", "polygon": "POL", "arbitrum": "ETH", "optimism": "ETH", "base": "ETH", "avalanche": "AVAX", "tron": "TRX", "solana": "SOL"}[c]
			if c == "tron" {
				m.Decimals = 6
			} else if c == "solana" {
				m.Decimals = 9
			}
			m.AmountRaw = bridgeRowString(row["value_raw"])
			if dataset == "internal_transactions" {
				m.Index = bridgeRowUint(row["internal_index"])
				if bridgeRowUint(row["success"]) == 0 {
					m.Status = "failed"
				}
			}
		}
		if dataset == "transactions" {
			if bridgeRowUint(row["status"]) == 1 {
				m.Status = "success"
			} else {
				m.Status = "failed"
			}
		} else if status, ok := statuses[m.TxHash]; ok {
			if status == 1 && m.Status != "failed" {
				m.Status = "success"
			} else {
				m.Status = "failed"
			}
		}
		if n, e := crosschain.Amount(m.AmountRaw); e == nil && m.Decimals <= 38 && !strings.Contains(m.AmountRaw, ".") {
			m.Amount = n.Shift(int32(-m.Decimals)).String()
		}
		out = append(out, m)
	}
	return out, nil
}
