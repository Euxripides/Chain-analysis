package query

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
)

// Existence checks stop after one indexed row per chain. They deliberately do
// not compute activity totals or merge complete chains for an interactive search.
func localAddressSearchSQL(chains []string, address string, activity bool) (string, []any) {
	parts := []string{}
	args := []any{}
	for _, chain := range chains {
		if activity {
			parts = append(parts, "SELECT chain FROM (SELECT chain FROM blockchain.address_activity WHERE chain=? AND address=? LIMIT 1)")
			args = append(args, chain, address)
			continue
		}
		for _, table := range []string{"transactions", "token_transfers", "nft_transfers", "logs", "address_balances"} {
			predicate := "address=?"
			values := []any{chain, address}
			if table != "logs" && table != "address_balances" {
				predicate = "(from_address=? OR to_address=?)"
				values = append(values, address)
			}
			parts = append(parts, "SELECT chain FROM (SELECT chain FROM blockchain."+table+" WHERE chain=? AND "+predicate+" LIMIT 1)")
			args = append(args, values...)
		}
	}
	return "SELECT DISTINCT chain FROM (" + strings.Join(parts, " UNION ALL ") + ") ORDER BY chain", args
}

func (s *Service) localSearchChains(ctx context.Context, sql string, args ...any) ([]string, error) {
	rows, err := s.CH.DB().Query(clickhouse.InteractiveScanContext(ctx), sql, args...)
	if err != nil {
		return nil, clickhouse.DescribeInteractiveScanError(err)
	}
	defer rows.Close()
	chains := []string{}
	for rows.Next() {
		var chain string
		if err := rows.Scan(&chain); err != nil {
			return nil, err
		}
		chains = append(chains, chain)
	}
	if err := rows.Err(); err != nil {
		return nil, clickhouse.DescribeInteractiveScanError(err)
	}
	return chains, nil
}

func (s *Service) SearchLocalAddress(ctx context.Context, chains []string, address string) ([]string, error) {
	if len(chains) == 0 {
		return []string{}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, clickhouse.InteractiveScanTimeout)
	defer cancel()
	sql, args := localAddressSearchSQL(chains, address, true)
	found, err := s.localSearchChains(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, chain := range found {
		seen[chain] = true
	}
	missing := []string{}
	for _, chain := range chains {
		if !seen[chain] {
			missing = append(missing, chain)
		}
	}
	if len(missing) > 0 {
		sql, args = localAddressSearchSQL(missing, address, false)
		extra, err := s.localSearchChains(ctx, sql, args...)
		if err != nil {
			return nil, err
		}
		found = append(found, extra...)
	}
	sort.Strings(found)
	return found, nil
}

func localTransactionSearchSQL(chains []string, hash string) (string, []any) {
	hash = strings.TrimPrefix(strings.ToLower(hash), "0x")
	variants := []string{hash, "0x" + hash}
	parts := []string{}
	args := []any{}
	for _, table := range []string{"transactions", "token_transfers", "nft_transfers", "logs", "internal_calls"} {
		parts = append(parts, fmt.Sprintf("SELECT chain FROM blockchain.%s WHERE chain IN (?) AND tx_hash IN (?)", table))
		args = append(args, chains, variants)
	}
	return "SELECT DISTINCT chain FROM (" + strings.Join(parts, " UNION ALL ") + ") ORDER BY chain", args
}

func (s *Service) SearchLocalTransaction(ctx context.Context, chains []string, hash string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, clickhouse.InteractiveScanTimeout)
	defer cancel()
	sql, args := localTransactionSearchSQL(chains, hash)
	return s.localSearchChains(ctx, sql, args...)
}
