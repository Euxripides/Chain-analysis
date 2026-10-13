package query

import (
	"context"
	"fmt"
	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
	"strings"
	"time"
)

type AggregateRequest struct {
	ActionFilter
	GroupBy        []string
	OrderBy        string
	Offset         int64
	Cutoff         uint64
	IngestedBefore time.Time
}

var aggregateDimensions = map[string]string{
	"counterparty": "if(dst=target,src,dst)", "from_address": "src", "to_address": "dst",
	"token_contract": "asset", "direction": "if(src=target AND dst=target,'self',if(dst=target,'in','out'))", "day": "toString(toDate(ts))",
}
var aggregateMetrics = map[string]string{"sum_amount": "sum_amount", "tx_count": "tx_count", "record_count": "record_count", "first_time": "first_time", "last_time": "last_time"}

func aggregateSQL(r AggregateRequest) (string, []any, error) {
	if len(r.GroupBy) == 0 || len(r.GroupBy) > 4 {
		return "", nil, fmt.Errorf("group_by requires 1-4 dimensions")
	}
	if r.PageSize < 1 || r.PageSize > 1000 || r.Offset < 0 {
		return "", nil, fmt.Errorf("invalid pagination")
	}
	order, ok := aggregateMetrics[r.OrderBy]
	if !ok {
		return "", nil, fmt.Errorf("unsupported order_by metric")
	}
	groups := []string{}
	seen := map[string]bool{}
	for _, dim := range r.GroupBy {
		expr, ok := aggregateDimensions[dim]
		if !ok || seen[dim] {
			return "", nil, fmt.Errorf("invalid group_by dimension")
		}
		seen[dim] = true
		groups = append(groups, expr)
	}
	where := []string{"chain=?", "(from_address=? OR to_address=?)", "block_number<=?", "source_ingested_at<=?"}
	args := []any{r.Address, r.Chain, r.Address, r.Address, r.Cutoff, r.IngestedBefore}
	if r.TokenContract != "" {
		where = append(where, "token_address=?")
		args = append(args, r.TokenContract)
	}
	if r.Token != "" {
		where = append(where, "upper(token_symbol)=?")
		args = append(args, strings.ToUpper(r.Token))
	}
	if r.Direction == "in" {
		where = append(where, "to_address=? AND from_address!=?")
		args = append(args, r.Address, r.Address)
	} else if r.Direction == "out" {
		where = append(where, "from_address=? AND to_address!=?")
		args = append(args, r.Address, r.Address)
	}
	if r.Counterparty != "" {
		where = append(where, "(from_address=? OR to_address=?)")
		args = append(args, r.Counterparty, r.Counterparty)
	}
	if r.StartTime != nil {
		where = append(where, "timestamp>=?")
		args = append(args, *r.StartTime)
	}
	if r.EndTime != nil {
		where = append(where, "timestamp<=?")
		args = append(args, *r.EndTime)
	}
	inner := `SELECT tx_hash,log_index,token_address AS asset,argMax(amount,source_ingested_at) AS amt,argMax(timestamp,source_ingested_at) AS ts,argMax(from_address,source_ingested_at) AS src,argMax(to_address,source_ingested_at) AS dst,argMax(block_number,source_ingested_at) AS bn FROM blockchain.token_transfers WHERE ` + strings.Join(where, " AND ") + ` GROUP BY tx_hash,log_index,token_address`
	// Asset is always part of the key: unlike units are never summed together.
	key := "toJSONString(tuple(" + strings.Join(groups, ",") + ",asset))"
	grouped := `SELECT ` + key + ` AS group_key,asset,toString(sum(amt)) AS sum_amount,uniqExact(tx_hash) AS tx_count,count() AS record_count,min(ts) AS first_time,max(ts) AS last_time,max(bn) AS cutoff FROM (` + inner + `) GROUP BY group_key,asset`
	sorting := order
	if order == "sum_amount" {
		sorting = "toDecimal256(sum_amount,38)"
	}
	extra := ""
	if r.TokenContract != "" {
		extra = ",toString(sum(toDecimal256(sum_amount,38)) OVER ()) AS full_amount,max(cutoff) OVER () AS full_cutoff"
	}
	sql := `WITH ? AS target SELECT group_key,asset,sum_amount,tx_count,record_count,first_time,last_time,cutoff,count() OVER () AS total_groups,sum(record_count) OVER () AS total_records` + extra + ` FROM (` + grouped + `) ORDER BY ` + sorting + ` DESC,group_key ASC LIMIT ? OFFSET ?`
	args = append(args, r.PageSize+1, r.Offset)
	return sql, args, nil
}

func (s *Service) InvestigationAggregate(ctx context.Context, r AggregateRequest) (map[string]any, error) {
	sql, args, err := aggregateSQL(r)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, clickhouse.InteractiveScanTimeout)
	defer cancel()
	ctx = clickhouse.InteractiveScanContext(ctx)
	rows, err := s.CH.DB().Query(ctx, sql, args...)
	if err != nil {
		return nil, clickhouse.DescribeInteractiveScanError(err)
	}
	defer rows.Close()
	items := []map[string]any{}
	var groups, records uint64
	var fullAmount string
	var fullCutoff uint64
	for rows.Next() {
		var key, asset, amount string
		var txs, count, cut uint64
		var first, last time.Time
		destinations := []any{&key, &asset, &amount, &txs, &count, &first, &last, &cut, &groups, &records}
		if r.TokenContract != "" {
			destinations = append(destinations, &fullAmount, &fullCutoff)
		}
		if err = rows.Scan(destinations...); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{"group_key": key, "token_contract": asset, "sum_amount": amount, "tx_count": txs, "record_count": count, "first_time": first, "last_time": last, "data_cutoff_block": cut})
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	more := len(items) > r.PageSize
	if more {
		items = items[:r.PageSize]
	}
	// Totals use the same deduplication, filters, block and ingestion watermark.
	start := strings.Index(sql, "FROM (SELECT ")
	end := strings.LastIndex(sql, ") ORDER BY ")
	totals := []map[string]any{}
	if r.TokenContract != "" && len(items) > 0 {
		totals = append(totals, map[string]any{"token_contract": r.TokenContract, "total_amount": fullAmount, "total_records": records, "data_cutoff_block": fullCutoff})
	} else if start >= 0 && end > start {
		q := `WITH ? AS target SELECT asset,count(),sum(record_count),toString(sum(toDecimal256(sum_amount,38))),max(cutoff) ` + sql[start:end+1] + ` GROUP BY asset ORDER BY asset`
		tr, e := s.CH.DB().Query(ctx, q, args[:len(args)-2]...)
		if e != nil {
			return nil, clickhouse.DescribeInteractiveScanError(e)
		}
		groups, records = 0, 0
		for tr.Next() {
			var asset, amount string
			var g, n, b uint64
			if e = tr.Scan(&asset, &g, &n, &amount, &b); e != nil {
				tr.Close()
				return nil, e
			}
			groups += g
			records += n
			totals = append(totals, map[string]any{"token_contract": asset, "total_amount": amount, "total_records": n, "data_cutoff_block": b})
		}
		e = tr.Err()
		tr.Close()
		if e != nil {
			return nil, e
		}
	}
	out := map[string]any{"items": items, "total_groups": groups, "total_records": records, "total_transfer_count": records, "totals_by_asset": totals, "has_more": more, "snapshot_upper_block": r.Cutoff, "group_by": r.GroupBy, "page_size": r.PageSize, "evidence_tool": "get_token_transfers"}
	var observed uint64
	for _, t := range totals {
		if b := t["data_cutoff_block"].(uint64); b > observed {
			observed = b
		}
	}
	out["data_cutoff_block"] = observed
	if len(totals) == 1 {
		out["total_amount"] = totals[0]["total_amount"]
	}
	return out, nil
}

func (s *Service) InvestigationTokens(ctx context.Context, c, address string, block uint64, offset, limit int) ([]string, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, clickhouse.InteractiveScanTimeout)
	defer cancel()
	ctx = clickhouse.InteractiveScanContext(ctx)
	rows, err := s.CH.DB().Query(ctx, `SELECT DISTINCT token_address FROM blockchain.token_transfers WHERE chain=? AND (from_address=? OR to_address=?) AND block_number<=? AND token_address!='' ORDER BY token_address LIMIT ? OFFSET ?`, c, address, address, block, limit+1, offset)
	if err != nil {
		return nil, false, clickhouse.DescribeInteractiveScanError(err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var a string
		if err = rows.Scan(&a); err != nil {
			return nil, false, err
		}
		out = append(out, a)
	}
	if err = rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(out) > limit
	if more {
		out = out[:limit]
	}
	return out, more, nil
}

func investigationTokensSQL(c, address string, block uint64, ingestedBefore time.Time, after string, limit int) (string, []any, error) {
	if limit < 1 || limit > 100 || ingestedBefore.IsZero() {
		return "", nil, fmt.Errorf("invalid discovery snapshot")
	}
	return `SELECT DISTINCT token_address FROM blockchain.token_transfers WHERE chain=? AND (from_address=? OR to_address=?) AND block_number<=? AND source_ingested_at<=? AND token_address!='' AND token_address>? ORDER BY token_address LIMIT ?`, []any{c, address, address, block, ingestedBefore, after, limit + 1}, nil
}

func (s *Service) InvestigationTokensPage(ctx context.Context, c, address string, block uint64, ingestedBefore time.Time, after string, limit int) ([]string, bool, error) {
	sql, args, err := investigationTokensSQL(c, address, block, ingestedBefore, after, limit)
	if err != nil {
		return nil, false, err
	}
	ctx, cancel := context.WithTimeout(ctx, clickhouse.InteractiveScanTimeout)
	defer cancel()
	rows, err := s.CH.DB().Query(clickhouse.InteractiveScanContext(ctx), sql, args...)
	if err != nil {
		return nil, false, clickhouse.DescribeInteractiveScanError(err)
	}
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var token string
		if err = rows.Scan(&token); err != nil {
			return nil, false, err
		}
		result = append(result, token)
	}
	if err = rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(result) > limit
	if more {
		result = result[:limit]
	}
	return result, more, nil
}

type LedgerTransfer struct {
	Hash             string    `json:"tx_hash"`
	Index            uint64    `json:"log_index"`
	Block            uint64    `json:"block_number"`
	TransactionIndex uint64    `json:"-"`
	From             string    `json:"from_address"`
	To               string    `json:"to_address"`
	Token            string    `json:"token_contract"`
	Raw              string    `json:"amount_raw"`
	Amount           string    `json:"amount"`
	Time             time.Time `json:"timestamp"`
}

func investigationLedgerSQL(c, token string, addresses []string, start, end uint64, limit int) (string, []any) {
	return `SELECT * FROM (SELECT tx_hash,toUInt64(log_index) AS li,argMax(block_number,source_ingested_at) AS bn,toUInt64(0) AS ti,argMax(from_address,source_ingested_at) AS src,argMax(to_address,source_ingested_at) AS dst,token_address,argMax(amount_raw,source_ingested_at) AS raw,toString(argMax(amount,source_ingested_at)),argMax(timestamp,source_ingested_at) AS ts FROM blockchain.token_transfers WHERE chain=? AND token_address=? AND (from_address IN (?) OR to_address IN (?)) AND block_number>=? AND block_number<=? GROUP BY tx_hash,log_index,token_address) WHERE raw!='0' AND src!=dst ORDER BY bn,ti,li,tx_hash LIMIT ?`, []any{c, token, addresses, addresses, start, end, limit + 1}
}

func (s *Service) InvestigationLedger(ctx context.Context, c, token string, addresses []string, start, end uint64, limit int) ([]LedgerTransfer, bool, error) {
	return s.InvestigationLedgerAt(ctx, c, token, addresses, start, end, limit, time.Time{})
}

// InvestigationLedgerAt pins ingestion across graph expansion. Long-lived
// reproducibility still requires materializing these rows in object storage:
// a ClickHouse merge can retire an older version even with this watermark.
func (s *Service) InvestigationLedgerAt(ctx context.Context, c, token string, addresses []string, start, end uint64, limit int, watermark time.Time) ([]LedgerTransfer, bool, error) {
	if limit < 1 || limit > 20000 || len(addresses) == 0 || len(addresses) > 200 || start > end {
		return nil, false, fmt.Errorf("invalid bounded investigation ledger request")
	}
	ctx, cancel := context.WithTimeout(ctx, clickhouse.InteractiveScanTimeout)
	defer cancel()
	ctx = clickhouse.InteractiveScanContext(ctx)
	sql, args := investigationLedgerSQL(c, token, addresses, start, end, limit)
	if !watermark.IsZero() {
		sql = strings.Replace(sql, " GROUP BY tx_hash", " AND source_ingested_at<=? GROUP BY tx_hash", 1)
		args = append(args[:len(args)-1], watermark, limit+1)
	}
	rows, err := s.CH.DB().Query(ctx, sql, args...)
	if err != nil {
		return nil, false, clickhouse.DescribeInteractiveScanError(err)
	}
	defer rows.Close()
	out := []LedgerTransfer{}
	for rows.Next() {
		var v LedgerTransfer
		if err = rows.Scan(&v.Hash, &v.Index, &v.Block, &v.TransactionIndex, &v.From, &v.To, &v.Token, &v.Raw, &v.Amount, &v.Time); err != nil {
			return nil, false, err
		}
		out = append(out, v)
	}
	if err = rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(out) > limit
	if more {
		out = out[:limit]
	}
	return out, more, nil
}
