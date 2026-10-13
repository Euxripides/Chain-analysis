package query

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func (s *Service) BlockTransactions(ctx context.Context, chain, block, cursor string, limit int) ([]map[string]any, error) {
	where := "chain=?"
	args := []any{chain}
	if height, err := strconv.ParseUint(block, 10, 64); err == nil {
		where += " AND block_number=?"
		args = append(args, height)
	} else {
		where += " AND lower(replaceRegexpOne(block_hash,'^0x',''))=?"
		args = append(args, strings.ToLower(strings.TrimPrefix(block, "0x")))
	}
	if cursor != "" {
		where += " AND tx_hash>?"
		args = append(args, cursor)
	}
	args = append(args, limit+1)
	return s.queryRows(ctx, "transactions", "SELECT * FROM blockchain.transactions FINAL WHERE "+where+" ORDER BY tx_hash LIMIT ?", args...)
}

func (s *Service) TokenTransfers(ctx context.Context, chain, contract, hashCursor string, indexCursor uint32, timeCursor time.Time, limit int) ([]map[string]any, error) {
	where := "chain=? AND token_address=?"
	args := []any{chain, contract}
	if hashCursor != "" {
		where += " AND (timestamp,tx_hash,log_index)<(?,?,?)"
		args = append(args, timeCursor, hashCursor, indexCursor)
	}
	args = append(args, limit+1)
	return s.queryRows(ctx, "token_transfers", "SELECT * FROM blockchain.token_transfers FINAL WHERE "+where+" ORDER BY timestamp DESC,tx_hash DESC,log_index DESC LIMIT ?", args...)
}

func (s *Service) LocalTokenHolders(ctx context.Context, chain, contract string, limit int) ([]map[string]any, error) {
	rows, err := s.CH.DB().Query(ctx, `SELECT address,argMax(balance_raw,source_ingested_at),argMax(token_decimals,source_ingested_at),max(timestamp)
 FROM blockchain.address_balances WHERE chain=? AND token_address=? GROUP BY address
 ORDER BY length(argMax(balance_raw,source_ingested_at)) DESC,argMax(balance_raw,source_ingested_at) DESC,address LIMIT ?`, chain, contract, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var address, raw string
		var decimals uint16
		var timeValue time.Time
		if err := rows.Scan(&address, &raw, &decimals, &timeValue); err != nil {
			return nil, fmt.Errorf("token holders: %w", err)
		}
		items = append(items, map[string]any{"address": address, "amount_raw": raw, "token_decimals": decimals, "observed_at": timeValue})
	}
	return items, rows.Err()
}
