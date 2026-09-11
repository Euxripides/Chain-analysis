package query

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/shopspring/decimal"
	"golang.org/x/sync/errgroup"
)

// ActionFilter is the shared, strictly-whitelisted filter surface exposed to
// GPT Actions. Values are always bound parameters; callers cannot inject SQL.
type ActionFilter struct {
	Chain, Address, Direction, Token, TokenContract, Counterparty string
	FromAddress, ToAddress, Status, MethodID, Sort                string
	StartTime, EndTime                                            *time.Time
	MinAmount, MaxAmount                                          string
	Page, PageSize                                                int
}

type ActionPage struct {
	Items    []map[string]any `json:"items"`
	Page     int              `json:"page"`
	PageSize int              `json:"page_size"`
	Total    uint64           `json:"total"`
	HasMore  bool             `json:"has_more"`
}

type ActionOverview struct {
	Chain                    string     `json:"chain"`
	Address                  string     `json:"address"`
	AddressType              string     `json:"address_type"`
	NativeBalance            string     `json:"native_balance"`
	NativeBalanceSource      string     `json:"native_balance_source"`
	FirstActivity            *time.Time `json:"first_activity"`
	LastActivity             *time.Time `json:"last_activity"`
	TransactionCount         uint64     `json:"transaction_count"`
	TokenTransferCount       uint64     `json:"token_transfer_count"`
	InternalTransactionCount uint64     `json:"internal_transaction_count"`
	UniqueInCounterparties   uint64     `json:"unique_in_counterparties"`
	UniqueOutCounterparties  uint64     `json:"unique_out_counterparties"`
}

func normalizeActionAddress(chain, address string) string {
	address = strings.TrimSpace(address)
	if chain != "tron" {
		return strings.ToLower(address)
	}
	return address
}

func (s *Service) ActionOverview(ctx context.Context, chain, address string) (ActionOverview, error) {
	address = normalizeActionAddress(chain, address)
	out := ActionOverview{Chain: chain, Address: address, AddressType: "unknown", NativeBalance: "0", NativeBalanceSource: "unavailable"}
	q := `SELECT
		(SELECT count() FROM blockchain.transactions FINAL WHERE chain=? AND (from_address=? OR to_address=?)),
		(SELECT count() FROM blockchain.token_transfers FINAL WHERE chain=? AND (from_address=? OR to_address=?)),
		(SELECT count() FROM blockchain.internal_calls FINAL WHERE chain=? AND (from_address=? OR to_address=?)),
		(SELECT uniqExact(from_address) FROM blockchain.token_transfers FINAL WHERE chain=? AND to_address=? AND from_address!=?),
		(SELECT uniqExact(to_address) FROM blockchain.token_transfers FINAL WHERE chain=? AND from_address=? AND to_address!=?),
		(SELECT nullIf(min(timestamp), toDateTime64(0,3,'UTC')) FROM
			(SELECT timestamp FROM blockchain.transactions FINAL WHERE chain=? AND (from_address=? OR to_address=?)
			 UNION ALL SELECT timestamp FROM blockchain.token_transfers FINAL WHERE chain=? AND (from_address=? OR to_address=?))),
		(SELECT nullIf(max(timestamp), toDateTime64(0,3,'UTC')) FROM
			(SELECT timestamp FROM blockchain.transactions FINAL WHERE chain=? AND (from_address=? OR to_address=?)
			 UNION ALL SELECT timestamp FROM blockchain.token_transfers FINAL WHERE chain=? AND (from_address=? OR to_address=?)))`
	err := s.CH.DB().QueryRow(ctx, q,
		chain, address, address, chain, address, address, chain, address, address,
		chain, address, address, chain, address, address,
		chain, address, address, chain, address, address,
		chain, address, address, chain, address, address,
	).Scan(&out.TransactionCount, &out.TokenTransferCount, &out.InternalTransactionCount,
		&out.UniqueInCounterparties, &out.UniqueOutCounterparties, &out.FirstActivity, &out.LastActivity)
	if err != nil {
		return out, err
	}
	// Latest native balance uses an empty token contract and the chain native symbol.
	_ = s.CH.DB().QueryRow(ctx, `SELECT toString(argMax(balance, timestamp)) FROM blockchain.address_balances FINAL WHERE chain=? AND address=? AND token_address=''`, chain, address).Scan(&out.NativeBalance)
	return out, nil
}

// ActionBatchOverview aggregates all requested addresses with one scan per
// dataset. The old handler called ActionOverview once per address; on real
// ClickHouse data each scalar-subquery query can take tens of seconds, so a
// modest batch reliably exceeded the HTTP deadline and surfaced as a generic
// INTERNAL_ERROR.
func (s *Service) ActionBatchOverview(ctx context.Context, chain string, addresses []string) ([]ActionOverview, error) {
	out := make([]ActionOverview, len(addresses))
	byAddress := make(map[string]*ActionOverview, len(addresses))
	for i, raw := range addresses {
		address := normalizeActionAddress(chain, raw)
		out[i] = ActionOverview{Chain: chain, Address: address, AddressType: "unknown", NativeBalance: "0", NativeBalanceSource: "unavailable"}
		byAddress[address] = &out[i]
	}
	if len(addresses) == 0 {
		return out, nil
	}

	mergeTimes := func(item *ActionOverview, first, last time.Time) {
		if !first.IsZero() && (item.FirstActivity == nil || first.Before(*item.FirstActivity)) {
			value := first
			item.FirstActivity = &value
		}
		if !last.IsZero() && (item.LastActivity == nil || last.After(*item.LastActivity)) {
			value := last
			item.LastActivity = &value
		}
	}
	var resultMu sync.Mutex
	g, queryCtx := errgroup.WithContext(ctx)

	// Read each indexed direction separately. uniqExact on the immutable row
	// identity preserves ReplacingMergeTree semantics without FINAL forcing a
	// chain-wide merge/read and also prevents self-transfers from double count.
	g.Go(func() error {
		txRows, err := s.CH.DB().Query(queryCtx, `SELECT address,uniqExact(tx_hash),min(timestamp),max(timestamp)
		FROM (
			SELECT from_address AS address,tx_hash,timestamp FROM blockchain.transactions
				WHERE chain=? AND from_address IN (?)
			UNION ALL
			SELECT to_address AS address,tx_hash,timestamp FROM blockchain.transactions
				WHERE chain=? AND to_address IN (?))
		GROUP BY address`, chain, addresses, chain, addresses)
		if err != nil {
			return fmt.Errorf("batch transactions overview: %w", err)
		}
		defer txRows.Close()
		for txRows.Next() {
			var address string
			var count uint64
			var first, last time.Time
			if err := txRows.Scan(&address, &count, &first, &last); err != nil {
				return fmt.Errorf("scan batch transactions overview: %w", err)
			}
			resultMu.Lock()
			if item := byAddress[address]; item != nil {
				item.TransactionCount = count
				mergeTimes(item, first, last)
			}
			resultMu.Unlock()
		}
		if err := txRows.Err(); err != nil {
			return fmt.Errorf("iterate batch transactions overview: %w", err)
		}
		return nil
	})

	g.Go(func() error {
		transferRows, err := s.CH.DB().Query(queryCtx, `SELECT address,
		uniqExact(tuple(tx_hash,log_index,token_address)),
		uniqExactIf(counterparty,direction='in' AND counterparty!=address),
		uniqExactIf(counterparty,direction='out' AND counterparty!=address),min(timestamp),max(timestamp)
		FROM (
			SELECT from_address AS address,to_address AS counterparty,'out' AS direction,
				tx_hash,log_index,token_address,timestamp FROM blockchain.token_transfers
				WHERE chain=? AND from_address IN (?)
			UNION ALL
			SELECT to_address AS address,from_address AS counterparty,'in' AS direction,
				tx_hash,log_index,token_address,timestamp FROM blockchain.token_transfers
				WHERE chain=? AND to_address IN (?))
		GROUP BY address`, chain, addresses, chain, addresses)
		if err != nil {
			return fmt.Errorf("batch token transfer overview: %w", err)
		}
		defer transferRows.Close()
		for transferRows.Next() {
			var address string
			var count, uniqueIn, uniqueOut uint64
			var first, last time.Time
			if err := transferRows.Scan(&address, &count, &uniqueIn, &uniqueOut, &first, &last); err != nil {
				return fmt.Errorf("scan batch token transfer overview: %w", err)
			}
			resultMu.Lock()
			if item := byAddress[address]; item != nil {
				item.TokenTransferCount = count
				item.UniqueInCounterparties = uniqueIn
				item.UniqueOutCounterparties = uniqueOut
				mergeTimes(item, first, last)
			}
			resultMu.Unlock()
		}
		if err := transferRows.Err(); err != nil {
			return fmt.Errorf("iterate batch token transfer overview: %w", err)
		}
		return nil
	})

	g.Go(func() error {
		internalRows, err := s.CH.DB().Query(queryCtx, `SELECT address,uniqExact(tuple(tx_hash,call_index))
		FROM (
			SELECT from_address AS address,tx_hash,call_index FROM blockchain.internal_calls
				WHERE chain=? AND from_address IN (?)
			UNION ALL
			SELECT to_address AS address,tx_hash,call_index FROM blockchain.internal_calls
				WHERE chain=? AND to_address IN (?))
		GROUP BY address`, chain, addresses, chain, addresses)
		if err != nil {
			return fmt.Errorf("batch internal transaction overview: %w", err)
		}
		defer internalRows.Close()
		for internalRows.Next() {
			var address string
			var count uint64
			if err := internalRows.Scan(&address, &count); err != nil {
				return fmt.Errorf("scan batch internal transaction overview: %w", err)
			}
			resultMu.Lock()
			if item := byAddress[address]; item != nil {
				item.InternalTransactionCount = count
			}
			resultMu.Unlock()
		}
		if err := internalRows.Err(); err != nil {
			return fmt.Errorf("iterate batch internal transaction overview: %w", err)
		}
		return nil
	})
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) ActionAssets(ctx context.Context, chain, address string) ([]map[string]any, string, error) {
	address = normalizeActionAddress(chain, address)
	rows, err := s.queryRows(ctx, "balances", `SELECT * FROM blockchain.address_balances FINAL WHERE chain=? AND address=? ORDER BY timestamp DESC LIMIT 2000`, chain, address)
	if err != nil {
		return nil, "0", err
	}
	seen := map[string]bool{}
	out := make([]map[string]any, 0, len(rows))
	total := decimal.Zero
	for _, row := range rows {
		key := strings.ToLower(fmt.Sprint(row["token_address"])) + "|" + strings.ToUpper(fmt.Sprint(row["token_symbol"]))
		if seen[key] {
			continue
		}
		seen[key] = true
		row["symbol"] = row["token_symbol"]
		row["contract"] = row["token_address"]
		row["decimals"] = row["token_decimals"]
		row["usd_value"] = row["balance_usd"]
		row["balance_source"] = "balance_snapshot"
		row["is_current_balance"] = true
		row["as_of_time"] = row["timestamp"]
		row["as_of_block"] = nil
		if v, e := decimal.NewFromString(fmt.Sprint(row["balance_usd"])); e == nil {
			total = total.Add(v)
		}
		out = append(out, row)
	}
	hasObservedFlow := false
	flowRows, flowErr := s.CH.DB().Query(ctx, `SELECT token_address,any(token_symbol),any(token_decimals),
			toString(sumIf(amount,to_address=?)-sumIf(amount,from_address=?)),min(timestamp),max(timestamp),count()
			FROM blockchain.token_transfers FINAL
			WHERE chain=? AND (from_address=? OR to_address=?)
			GROUP BY token_address
			ORDER BY abs(sumIf(amount,to_address=?)-sumIf(amount,from_address=?)) DESC LIMIT 2000`,
		address, address, chain, address, address, address, address)
	if flowErr != nil {
		return nil, "0", flowErr
	}
	defer flowRows.Close()
	for flowRows.Next() {
		var contract, symbol, balance string
		var decimals uint16
		var first, last time.Time
		var transferCount uint64
		if err := flowRows.Scan(&contract, &symbol, &decimals, &balance, &first, &last, &transferCount); err != nil {
			return nil, "0", err
		}
		key := strings.ToLower(contract) + "|" + strings.ToUpper(symbol)
		if seen[key] {
			continue
		}
		seen[key] = true
		hasObservedFlow = true
		out = append(out, map[string]any{
			"token_address": contract, "token_symbol": symbol, "token_decimals": decimals,
			"contract": contract, "symbol": symbol, "decimals": decimals, "balance": balance,
			"usd_value": nil, "balance_usd": nil, "balance_source": "observed_net_flow",
			"is_current_balance": false, "first_transfer_time": first, "last_transfer_time": last,
			"transfer_count": transferCount, "as_of_time": last, "as_of_block": nil,
		})
	}
	if err := flowRows.Err(); err != nil {
		return nil, "0", err
	}
	if hasObservedFlow {
		return out, "unavailable", nil
	}
	return out, total.String(), nil
}

func (s *Service) ActionRows(ctx context.Context, dataset string, f ActionFilter) (ActionPage, error) {
	if f.Page <= 0 {
		f.Page = 1
	}
	if f.PageSize <= 0 {
		f.PageSize = 100
	}
	if f.PageSize > 500 {
		f.PageSize = 500
	}
	f.Address = normalizeActionAddress(f.Chain, f.Address)
	where := []string{"chain=?", "(from_address=? OR to_address=?)"}
	args := []any{f.Chain, f.Address, f.Address}
	add := func(sql string, value any) { where = append(where, sql); args = append(args, value) }
	if f.Direction == "in" {
		add("to_address=?", f.Address)
	} else if f.Direction == "out" {
		add("from_address=?", f.Address)
	}
	if f.Counterparty != "" {
		cp := normalizeActionAddress(f.Chain, f.Counterparty)
		where = append(where, "(from_address=? OR to_address=?)")
		args = append(args, cp, cp)
	}
	if f.FromAddress != "" {
		add("from_address=?", normalizeActionAddress(f.Chain, f.FromAddress))
	}
	if f.ToAddress != "" {
		add("to_address=?", normalizeActionAddress(f.Chain, f.ToAddress))
	}
	if f.StartTime != nil {
		add("timestamp>=?", *f.StartTime)
	}
	if f.EndTime != nil {
		add("timestamp<=?", *f.EndTime)
	}
	if f.MethodID != "" && dataset == "transactions" {
		add("method_id=?", strings.ToLower(f.MethodID))
	}
	if f.Status != "" {
		ok := f.Status == "success" || f.Status == "1" || f.Status == "true"
		if dataset == "internal_transactions" {
			add("success=?", boolToUint8(ok))
		} else if dataset == "transactions" {
			add("status=?", boolToUint8(ok))
		} else if dataset == "token_transfers" {
			where = append(where, "tx_hash IN (SELECT tx_hash FROM blockchain.transactions FINAL WHERE chain=? AND status=?)")
			args = append(args, f.Chain, boolToUint8(ok))
		}
	}
	if dataset == "token_transfers" {
		if f.Token != "" {
			add("upper(token_symbol)=?", strings.ToUpper(f.Token))
		}
		if f.TokenContract != "" {
			add("token_address=?", normalizeActionAddress(f.Chain, f.TokenContract))
		}
		if f.MinAmount != "" {
			add("amount>=toDecimal256(?,38)", f.MinAmount)
		}
		if f.MaxAmount != "" {
			add("amount<=toDecimal256(?,38)", f.MaxAmount)
		}
	} else if f.MinAmount != "" || f.MaxAmount != "" {
		amountSQL := "toDecimal256OrZero(value_raw,0) / if(chain='tron',1000000,1000000000000000000)"
		if f.MinAmount != "" {
			add(amountSQL+">=toDecimal256(?,18)", f.MinAmount)
		}
		if f.MaxAmount != "" {
			add(amountSQL+"<=toDecimal256(?,18)", f.MaxAmount)
		}
	}
	table := tableName(dataset)
	base := " FROM blockchain." + table + " FINAL WHERE " + strings.Join(where, " AND ")
	var total uint64
	if err := s.CH.DB().QueryRow(ctx, "SELECT count()"+base, args...).Scan(&total); err != nil {
		return ActionPage{}, err
	}
	direction := "DESC"
	if strings.EqualFold(f.Sort, "asc") {
		direction = "ASC"
	}
	offset := (f.Page - 1) * f.PageSize
	rows, err := s.queryRows(ctx, dataset, "SELECT *"+base+" ORDER BY timestamp "+direction+" LIMIT ? OFFSET ?", append(args, f.PageSize, offset)...)
	if err != nil {
		return ActionPage{}, err
	}
	for _, row := range rows {
		if dataset == "token_transfers" {
			row["status"] = "unknown"
		}
		from, to := fmt.Sprint(row["from_address"]), fmt.Sprint(row["to_address"])
		switch {
		case strings.EqualFold(from, f.Address) && strings.EqualFold(to, f.Address):
			row["direction"] = "self"
		case strings.EqualFold(to, f.Address):
			row["direction"] = "in"
		default:
			row["direction"] = "out"
		}
	}
	if dataset == "token_transfers" && len(rows) > 0 {
		hashes := make([]string, 0, len(rows))
		for _, row := range rows {
			hashes = append(hashes, fmt.Sprint(row["tx_hash"]))
		}
		statusRows, statusErr := s.CH.DB().Query(ctx, `SELECT tx_hash,argMax(status,source_ingested_at) FROM blockchain.transactions WHERE chain=? AND tx_hash IN (?) GROUP BY tx_hash`, f.Chain, hashes)
		if statusErr == nil {
			defer statusRows.Close()
			statuses := map[string]uint8{}
			for statusRows.Next() {
				var hash string
				var status uint8
				if statusRows.Scan(&hash, &status) == nil {
					statuses[hash] = status
				}
			}
			for _, row := range rows {
				if status, ok := statuses[fmt.Sprint(row["tx_hash"])]; ok {
					if status == 1 {
						row["status"] = "success"
					} else {
						row["status"] = "failed"
					}
				}
			}
		}
	}
	return ActionPage{Items: rows, Page: f.Page, PageSize: f.PageSize, Total: total, HasMore: uint64(offset+len(rows)) < total}, nil
}

func boolToUint8(v bool) uint8 {
	if v {
		return 1
	}
	return 0
}

type FlowSummary struct {
	TotalIn, TotalOut, NetFlow                                   string
	InTxCount, OutTxCount, UniqueInAddresses, UniqueOutAddresses uint64
	FirstInTime, LastInTime, FirstOutTime, LastOutTime           *time.Time
}

func (s *Service) ActionFlowSummary(ctx context.Context, chain, address, token, contract string, start, end *time.Time) (FlowSummary, error) {
	address = normalizeActionAddress(chain, address)
	where := []string{"chain=?", "(from_address=? OR to_address=?)"}
	args := []any{chain, address, address}
	if token != "" {
		where = append(where, "upper(token_symbol)=?")
		args = append(args, strings.ToUpper(token))
	}
	if contract != "" {
		where = append(where, "token_address=?")
		args = append(args, normalizeActionAddress(chain, contract))
	}
	if start != nil {
		where = append(where, "timestamp>=?")
		args = append(args, *start)
	}
	if end != nil {
		where = append(where, "timestamp<=?")
		args = append(args, *end)
	}
	inCond := "to_address=target AND from_address!=target"
	outCond := "from_address=target AND to_address!=target"
	args = append([]any{address}, args...)
	q := `WITH ? AS target SELECT toString(sumIf(amount,` + inCond + `)),toString(sumIf(amount,` + outCond + `)),toString(sumIf(amount,` + inCond + `)-sumIf(amount,` + outCond + `)),countIf(` + inCond + `),countIf(` + outCond + `),uniqExactIf(from_address,` + inCond + `),uniqExactIf(to_address,` + outCond + `),nullIf(minIf(timestamp,` + inCond + `),toDateTime64(0,3,'UTC')),nullIf(maxIf(timestamp,` + inCond + `),toDateTime64(0,3,'UTC')),nullIf(minIf(timestamp,` + outCond + `),toDateTime64(0,3,'UTC')),nullIf(maxIf(timestamp,` + outCond + `),toDateTime64(0,3,'UTC')) FROM blockchain.token_transfers FINAL WHERE ` + strings.Join(where, " AND ")
	var o FlowSummary
	err := s.CH.DB().QueryRow(ctx, q, args...).Scan(&o.TotalIn, &o.TotalOut, &o.NetFlow, &o.InTxCount, &o.OutTxCount, &o.UniqueInAddresses, &o.UniqueOutAddresses, &o.FirstInTime, &o.LastInTime, &o.FirstOutTime, &o.LastOutTime)
	return o, err
}

func (s *Service) ActionCounterparties(ctx context.Context, f ActionFilter, minTotal string, minCount, limit int, sortBy string) ([]map[string]any, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 1000 {
		limit = 1000
	}
	f.Address = normalizeActionAddress(f.Chain, f.Address)
	where := []string{"chain=?", "(from_address=? OR to_address=?)"}
	args := []any{f.Chain, f.Address, f.Address}
	if f.Token != "" {
		where = append(where, "upper(token_symbol)=?")
		args = append(args, strings.ToUpper(f.Token))
	}
	if f.TokenContract != "" {
		where = append(where, "token_address=?")
		args = append(args, normalizeActionAddress(f.Chain, f.TokenContract))
	}
	if f.Direction != "" {
		if f.Direction == "in" {
			where = append(where, "to_address=? AND from_address!=?")
			args = append(args, f.Address, f.Address)
		} else if f.Direction == "out" {
			where = append(where, "from_address=? AND to_address!=?")
			args = append(args, f.Address, f.Address)
		}
	}
	if f.StartTime != nil {
		where = append(where, "timestamp>=?")
		args = append(args, *f.StartTime)
	}
	if f.EndTime != nil {
		where = append(where, "timestamp<=?")
		args = append(args, *f.EndTime)
	}
	having := []string{"count()>=?"}
	args = append(args, minCount)
	if minTotal != "" {
		having = append(having, "sum(amount)>=toDecimal256(?,38)")
		args = append(args, minTotal)
	}
	order := map[string]string{"tx_count": "tx_count", "total_amount": "sum(amount)", "net_flow": "abs(sumIf(amount,to_address=target AND from_address!=target)-sumIf(amount,from_address=target AND to_address!=target))", "last_time": "last_tx_time"}[sortBy]
	if order == "" {
		order = "sum(amount)"
	}
	q := `WITH ? AS target SELECT if(to_address=target,from_address,to_address) counterparty,count() tx_count,
		countIf(to_address=target AND from_address!=target) incoming_tx_count,countIf(from_address=target AND to_address!=target) outgoing_tx_count,
		toString(sumIf(amount,to_address=target AND from_address!=target)) incoming_amount,
		toString(sumIf(amount,from_address=target AND to_address!=target)) outgoing_amount,
		toString(sumIf(amount,to_address=target AND from_address!=target)-sumIf(amount,from_address=target AND to_address!=target)) net_flow,
		toString(sum(amount)) total_amount,min(timestamp) first_tx_time,max(timestamp) last_tx_time,any(token_symbol) asset_symbol
		FROM blockchain.token_transfers FINAL WHERE ` + strings.Join(where, " AND ") + " GROUP BY counterparty HAVING " + strings.Join(having, " AND ") + " ORDER BY " + order + " DESC LIMIT ?"
	args = append([]any{f.Address}, args...)
	args = append(args, limit)
	rows, err := s.CH.DB().Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var cp, ia, oa, nf, ta, sym string
		var tc, ic, oc uint64
		var ft, lt time.Time
		if err := rows.Scan(&cp, &tc, &ic, &oc, &ia, &oa, &nf, &ta, &ft, &lt, &sym); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"counterparty": cp, "tx_count": tc, "incoming_tx_count": ic, "outgoing_tx_count": oc, "incoming_amount": ia, "outgoing_amount": oa, "net_flow": nf, "total_amount": ta, "first_tx_time": ft, "last_tx_time": lt, "token_symbol": sym})
	}
	return out, rows.Err()
}

func (s *Service) ActionTopFlows(ctx context.Context, f ActionFilter, groupBy string, limit int) ([]map[string]any, error) {
	if groupBy == "transaction" {
		if limit > 0 {
			f.PageSize = limit
		}
		p, err := s.ActionRows(ctx, "token_transfers", f)
		return p.Items, err
	}
	return s.ActionCounterparties(ctx, f, "", 0, limit, "total_amount")
}

func (s *Service) ActionFirstFunding(ctx context.Context, chain, address string) (map[string]any, error) {
	address = normalizeActionAddress(chain, address)
	out := map[string]any{"address": address, "first_native_funding": nil, "first_token_funding": []map[string]any{}}
	var from, amount, rawValue, hash string
	var ts time.Time
	err := s.CH.DB().QueryRow(ctx, `SELECT from_address,toString(toDecimal256OrZero(value_raw,0)/if(chain='tron',1000000,1000000000000000000)),value_raw,tx_hash,timestamp FROM blockchain.transactions FINAL WHERE chain=? AND to_address=? AND from_address!=? AND toDecimal256OrZero(value_raw,0)>0 ORDER BY timestamp ASC LIMIT 1`, chain, address, address).Scan(&from, &amount, &rawValue, &hash, &ts)
	if err == nil {
		out["first_native_funding"] = map[string]any{"from": from, "amount": amount, "raw_value": rawValue, "tx_hash": hash, "timestamp": ts}
	}
	rows, e := s.queryRows(ctx, "token_transfers", `SELECT * FROM blockchain.token_transfers FINAL WHERE chain=? AND to_address=? AND from_address!=? ORDER BY timestamp ASC LIMIT 20`, chain, address, address)
	if e != nil {
		return nil, e
	}
	out["first_token_funding"] = rows
	return out, nil
}

func (s *Service) ActionTransactionRows(ctx context.Context, dataset, chain, hash string) ([]map[string]any, error) {
	if dataset != "token_transfers" && dataset != "logs" && dataset != "internal_transactions" {
		return nil, fmt.Errorf("unsupported transaction dataset")
	}
	return s.queryRows(ctx, dataset, "SELECT * FROM blockchain."+tableName(dataset)+" FINAL WHERE chain=? AND tx_hash=? ORDER BY "+explorerOrder(dataset), chain, strings.ToLower(hash))
}

type ActionEdge struct {
	From, To, Token, TokenContract, Amount string
	TxCount                                uint64
	FirstTime, LastTime                    time.Time
}

func (s *Service) ActionEdges(ctx context.Context, chain string, from []string, token, tokenContract, minAmount string, start, end *time.Time, limit int) ([]ActionEdge, error) {
	if len(from) == 0 {
		return nil, nil
	}
	for i := range from {
		from[i] = normalizeActionAddress(chain, from[i])
	}
	where := []string{"chain=?", "from_address IN (?)"}
	args := []any{chain, from}
	if token != "" {
		where = append(where, "upper(token_symbol)=?")
		args = append(args, strings.ToUpper(token))
	}
	if tokenContract != "" {
		where = append(where, "token_address=?")
		args = append(args, normalizeActionAddress(chain, tokenContract))
	}
	if minAmount != "" {
		where = append(where, "amount>=toDecimal256(?,38)")
		args = append(args, minAmount)
	}
	if start != nil {
		where = append(where, "timestamp>=?")
		args = append(args, *start)
	}
	if end != nil {
		where = append(where, "timestamp<=?")
		args = append(args, *end)
	}
	if limit <= 0 {
		limit = 500
	}
	args = append(args, limit)
	q := `SELECT from_address,to_address,any(token_symbol),any(token_address),toString(sum(amount)),count(),min(timestamp),max(timestamp) FROM blockchain.token_transfers FINAL WHERE ` + strings.Join(where, " AND ") + ` GROUP BY from_address,to_address,token_address ORDER BY sum(amount) DESC LIMIT ?`
	rows, err := s.CH.DB().Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ActionEdge{}
	for rows.Next() {
		var e ActionEdge
		if err := rows.Scan(&e.From, &e.To, &e.Token, &e.TokenContract, &e.Amount, &e.TxCount, &e.FirstTime, &e.LastTime); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, _ := decimal.NewFromString(out[i].Amount)
		b, _ := decimal.NewFromString(out[j].Amount)
		return a.GreaterThan(b)
	})
	return out, rows.Err()
}
