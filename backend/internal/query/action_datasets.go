package query

import (
	"context"
	"fmt"
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
)

// Reject filters without a meaning for the selected dataset rather than
// silently ignoring them or applying native-coin scaling to NFT quantities.
func ValidateActionDatasetFilter(dataset string, f ActionFilter) error {
	switch dataset {
	case "transactions", "token_transfers", "internal_transactions":
		if f.TokenID != "" {
			return fmt.Errorf("token_id requires nft_transfers")
		}
		return nil
	case "nft_transfers", "logs", "balances":
		if f.MinAmount != "" || f.MaxAmount != "" || f.Status != "" || f.MethodID != "" {
			return fmt.Errorf("amount, status and method filters are not supported for %s", dataset)
		}
	default:
		return fmt.Errorf("unsupported dataset")
	}
	if dataset != "nft_transfers" && (f.Direction != "" && f.Direction != "all" || f.Counterparty != "" || f.FromAddress != "" || f.ToAddress != "" || f.TokenID != "") {
		return fmt.Errorf("direction and transfer-party filters require a transfer dataset")
	}
	if dataset == "logs" && (f.Token != "" || f.TokenContract != "") {
		return fmt.Errorf("logs use address as the emitting contract; token filters are unsupported")
	}
	if f.StartTime != nil && f.EndTime != nil && f.StartTime.After(*f.EndTime) {
		return fmt.Errorf("start_time must not exceed end_time")
	}
	return nil
}

func actionSnapshotSource(dataset string, f ActionFilter) (string, []any) {
	where := "chain=? AND address=?"
	args := []any{f.Chain, normalizeActionAddress(f.Chain, f.Address)}
	source := dedupSource("logs", where)
	if dataset == "balances" {
		// The physical monthly partitions and token_symbol key can retain old
		// snapshots. Select one latest observation per asset before filtering.
		source = "SELECT * EXCEPT (_balance_rank) FROM (SELECT *, row_number() OVER (PARTITION BY chain,address,token_address ORDER BY timestamp DESC,source_ingested_at DESC) AS _balance_rank FROM blockchain.address_balances WHERE " + where + ") WHERE _balance_rank=1"
	}
	filters := []string{}
	if f.StartTime != nil {
		filters = append(filters, "timestamp>=?")
		args = append(args, *f.StartTime)
	}
	if f.EndTime != nil {
		filters = append(filters, "timestamp<=?")
		args = append(args, *f.EndTime)
	}
	if dataset == "balances" {
		if f.Token != "" {
			filters = append(filters, "upper(token_symbol)=?")
			args = append(args, strings.ToUpper(f.Token))
		}
		if f.TokenContract != "" {
			filters = append(filters, "token_address=?")
			args = append(args, normalizeActionAddress(f.Chain, f.TokenContract))
		}
	}
	if len(filters) > 0 {
		source = "SELECT * FROM (" + source + ") WHERE " + strings.Join(filters, " AND ")
	}
	return source, args
}

func annotateBalanceSnapshot(row map[string]any) {
	row["balance_source"] = "balance_snapshot"
	row["is_current_balance"] = false
	row["as_of_time"] = row["timestamp"]
	row["as_of_block"] = row["snapshot_block"]
}

func (s *Service) actionSnapshotRows(ctx context.Context, dataset string, f ActionFilter) (ActionPage, error) {
	if f.Page <= 0 {
		f.Page = 1
	}
	if f.PageSize <= 0 {
		f.PageSize = 100
	}
	if f.PageSize > 500 {
		f.PageSize = 500
	}
	ctx, cancel := context.WithTimeout(ctx, clickhouse.InteractiveScanTimeout)
	defer cancel()
	ctx = clickhouse.InteractiveScanContext(ctx)
	source, args := actionSnapshotSource(dataset, f)
	var total uint64
	if err := s.CH.DB().QueryRow(ctx, "SELECT count() FROM ("+source+")", args...).Scan(&total); err != nil {
		return ActionPage{}, clickhouse.DescribeInteractiveScanError(err)
	}
	direction := "DESC"
	if strings.EqualFold(f.Sort, "asc") {
		direction = "ASC"
	}
	order := stableActionOrder("logs", "timestamp "+direction)
	if dataset == "balances" {
		order = "timestamp " + direction + ", token_address ASC"
	}
	offset := (f.Page - 1) * f.PageSize
	rows, err := s.queryRows(ctx, dataset, source+" ORDER BY "+order+" LIMIT ? OFFSET ?", append(args, f.PageSize, offset)...)
	if err != nil {
		return ActionPage{}, err
	}
	if dataset == "balances" {
		for _, row := range rows {
			annotateBalanceSnapshot(row)
		}
	}
	return ActionPage{Items: rows, Page: f.Page, PageSize: f.PageSize, Total: total, HasMore: uint64(offset+len(rows)) < total}, nil
}
