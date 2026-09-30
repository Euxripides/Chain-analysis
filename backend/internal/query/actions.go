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

	"github.com/blockchain-platform/blockchain-platform/internal/addressnorm"
	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
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

// normalizeActionAddress 归一化 Actions 查询键：Solana 保留 base58 大小写，
// 其余链沿用历史行为（EVM 小写、TRON 保留 Base58Check）。
func normalizeActionAddress(chain, address string) string {
	return addressnorm.NormalizeKey(chain, address)
}

func sameActionAddress(chain, a, b string) bool {
	if chain == "solana" {
		return a == b
	}
	return strings.EqualFold(a, b)
}

// ActionOverview 单地址概览。
//
// 2026-09-21：改成直接复用 ActionBatchOverview 的按 dataset 单次聚合实现。
// 原来这里是 7 个标量子查询，每个都是 "... FINAL WHERE (from_address=? OR to_address=?)"：
// 在 bsc 上单次 count 就要 16.5–27.2 s（整链合并读，跳数索引失效）。批量实现已经把
// 同一批数字算成 3 个「按地址分组、无 FINAL」的聚合，单地址走它既快又只有一份口径。
func (s *Service) ActionOverview(ctx context.Context, chain, address string) (ActionOverview, error) {
	normalized := normalizeActionAddress(chain, address)
	items, err := s.ActionBatchOverview(ctx, chain, []string{normalized})
	if err != nil {
		return ActionOverview{}, err
	}
	if len(items) == 0 {
		return ActionOverview{}, fmt.Errorf("overview returned no rows")
	}
	out := items[0]
	if chain == "solana" {
		return out, nil // The batch scan already includes the latest SOL snapshot.
	}
	// Latest native balance uses an empty token contract and the chain native symbol.
	var snapshots uint64
	if err := s.CH.DB().QueryRow(ctx, `SELECT count(),toString(argMax(balance, timestamp)) FROM blockchain.address_balances FINAL WHERE chain=? AND address=? AND token_address=''`, chain, normalized).Scan(&snapshots, &out.NativeBalance); err == nil && snapshots > 0 {
		out.NativeBalanceSource = "balance_snapshot"
	}
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
	// 查询键必须与写入侧一致（Solana 保留 base58 大小写）：只有归一化后的键才进 SQL，
	// 否则原始输入与入库键不一致的地址会静默返回 0 行。
	keys := make([]string, 0, len(addresses))
	for i, raw := range addresses {
		address := normalizeActionAddress(chain, raw)
		out[i] = ActionOverview{Chain: chain, Address: address, AddressType: "unknown", NativeBalance: "0", NativeBalanceSource: "unavailable"}
		byAddress[address] = &out[i]
		keys = append(keys, address)
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
	// 交互读预算：概览是 AI 的第一个调用，绝不能拖满网关 60 s。
	ctx, cancel := context.WithTimeout(ctx, clickhouse.InteractiveScanTimeout)
	defer cancel()
	ctx = clickhouse.InteractiveScanContext(ctx)
	var resultMu sync.Mutex
	g, queryCtx := errgroup.WithContext(ctx)
	if chain == "solana" {
		// One indexed scan supplies the latest stored SOL snapshot for the whole
		// batch. An absent snapshot remains explicitly unavailable, not a zero
		// balance inferred from partial transfer history.
		g.Go(func() error {
			rows, err := s.CH.DB().Query(queryCtx, `SELECT address,toString(argMax(balance,timestamp)) FROM blockchain.address_balances WHERE chain='solana' AND address IN (?) AND token_address='' GROUP BY address`, keys)
			if err != nil {
				return fmt.Errorf("batch Solana native balances: %w", err)
			}
			defer rows.Close()
			for rows.Next() {
				var address, balance string
				if err := rows.Scan(&address, &balance); err != nil {
					return fmt.Errorf("scan batch Solana native balances: %w", err)
				}
				resultMu.Lock()
				if item := byAddress[address]; item != nil {
					item.NativeBalance, item.NativeBalanceSource = balance, "balance_snapshot"
				}
				resultMu.Unlock()
			}
			return rows.Err()
		})
	}

	// Read each indexed direction separately. uniqExact on the immutable row
	// identity preserves ReplacingMergeTree semantics without FINAL forcing a
	// chain-wide merge/read and also prevents self-transfers from double count.
	g.Go(func() error {
		txSQL := `SELECT address,uniqExact(tx_hash),min(timestamp),max(timestamp)
		FROM (
			SELECT from_address AS address,tx_hash,timestamp FROM blockchain.transactions
				WHERE chain=? AND from_address IN (?)
			UNION ALL
			SELECT to_address AS address,tx_hash,timestamp FROM blockchain.transactions
				WHERE chain=? AND to_address IN (?))
		GROUP BY address`
		txArgs := []any{chain, keys, chain, keys}
		if chain == "solana" {
			txSQL = `SELECT address,uniqExact(tx_hash),min(timestamp),max(timestamp) FROM blockchain.address_activity WHERE chain='solana' AND address IN (?) AND dataset='transactions' AND activity_type='solana_signature' GROUP BY address`
			txArgs = []any{keys}
		}
		txRows, err := s.CH.DB().Query(queryCtx, txSQL, txArgs...)
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
		GROUP BY address`, chain, keys, chain, keys)
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
		GROUP BY address`, chain, keys, chain, keys)
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
	hasPrice := false
	for _, row := range rows {
		key := normalizeActionAddress(chain, fmt.Sprint(row["token_address"])) + "|" + strings.ToUpper(fmt.Sprint(row["token_symbol"]))
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
			hasPrice = true
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
		key := normalizeActionAddress(chain, contract) + "|" + strings.ToUpper(symbol)
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
	if hasObservedFlow || (len(out) > 0 && !hasPrice) {
		return out, "unavailable", nil
	}
	return out, total.String(), nil
}

func (s *Service) ActionRows(ctx context.Context, dataset string, f ActionFilter) (ActionPage, error) {
	direction := "DESC"
	if strings.EqualFold(f.Sort, "asc") {
		direction = "ASC"
	}
	return s.actionRows(ctx, dataset, f, "timestamp "+direction, true)
}

// actionRows 是逐笔明细查询的唯一实现。orderBy 与 withTotal 让"最大单笔"排行
// （getTopFlows group_by=transaction）复用同一套过滤、方向判定与状态回填，只替换
// 排序键并跳过不需要的 count 扫描。
func (s *Service) actionRows(ctx context.Context, dataset string, f ActionFilter, orderBy string, withTotal bool) (ActionPage, error) {
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
	if f.Chain == "solana" && dataset == "transactions" {
		if f.Direction == "in" || f.Direction == "out" || f.Counterparty != "" || f.MinAmount != "" || f.MaxAmount != "" {
			return ActionPage{}, fmt.Errorf("Solana signature lists do not have one direction, counterparty or amount; query native or SPL movements")
		}
		where = []string{"chain=?", "tx_hash IN (SELECT tx_hash FROM blockchain.address_activity WHERE chain='solana' AND address=? AND dataset='transactions' AND activity_type='solana_signature')"}
		args = []any{f.Chain, f.Address}
	}
	// 状态过滤是一个半连接（见下面的 LEFT ANY JOIN），它的参数必须排在主 where 参数之后，
	// 因为占位符在 SQL 文本里出现在 WHERE 之后。
	statusJoin := ""
	var statusJoinArgs []any
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
			/*
			 * 状态过滤只能按「每个 tx 的最新版本」判定，绝不能用 FINAL、也不能用 IN 子查询：
			 * 实测 bsc 上 FINAL 形态 70.6 s，`tx_hash IN (SELECT … 2360 万个 hash)` 形态 45.7 s
			 * （状态聚合本身 3.9 s，其余全是 IN 集合的构建与探测）。
			 * 改成 INNER JOIN … USING (tx_hash)（半连接语义；LEFT ANY JOIN 会保留未匹配的左行，
			 * 状态过滤就等于没生效）：连接键只出现一次，SELECT * 仍然只投影左表列（22 列，
			 * 与逐笔表完全一致），同样的答案实测 0.30–0.83 s。调用方给的时间窗同时下推到
			 * 状态聚合（同笔交易的转账时间就是交易时间，语义等价：3.9 s → 0.21 s）。
			 */
			statusWhere := []string{"chain=?", "status=?"}
			statusArgs := []any{f.Chain, boolToUint8(ok)}
			if f.StartTime != nil {
				statusWhere = append(statusWhere, "timestamp>=?")
				statusArgs = append(statusArgs, *f.StartTime)
			}
			if f.EndTime != nil {
				statusWhere = append(statusWhere, "timestamp<=?")
				statusArgs = append(statusArgs, *f.EndTime)
			}
			statusJoin = " INNER JOIN (SELECT tx_hash FROM (SELECT tx_hash,argMax(status,source_ingested_at) AS st FROM blockchain.transactions WHERE " +
				strings.Join(statusWhere, " AND ") + " GROUP BY tx_hash) WHERE st=?) AS s USING (tx_hash)"
			statusJoinArgs = append(statusJoinArgs, statusArgs...)
			statusJoinArgs = append(statusJoinArgs, boolToUint8(ok))
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
		amountSQL := "toDecimal256OrZero(value_raw,0) / multiIf(chain='tron',1000000,chain='solana',1000000000,1000000000000000000)"
		if f.Chain == "solana" && dataset == "internal_transactions" {
			amountSQL = solanaLamportsDecimalSQL
		}
		if f.MinAmount != "" {
			add(amountSQL+">=toDecimal256(?,18)", f.MinAmount)
		}
		if f.MaxAmount != "" {
			add(amountSQL+"<=toDecimal256(?,18)", f.MaxAmount)
		}
	}
	// 明细页与计数都必须走显式去重：FINAL 会按排序键合并整条链，
	// from_address/to_address 的 bloom_filter 跳数索引完全用不上（实测 27.2 s vs 0.27 s）。
	// 详见 dedupSource / dedupCount 的实测注释。
	whereSQL := strings.Join(where, " AND ")
	ctx, cancel := context.WithTimeout(ctx, clickhouse.InteractiveScanTimeout)
	defer cancel()
	ctx = clickhouse.InteractiveScanContext(ctx)
	// 连接形态的源：内部先去重，再用左表列做半连接（只投影左表列，保证行结构与数据集一致）。
	joinedSource := func() string {
		source := "SELECT * FROM blockchain." + tableName(dataset) + " FINAL WHERE " + whereSQL
		if dedupSupported(dataset) {
			source = dedupSource(dataset, whereSQL)
		}
		if statusJoin != "" {
			source = "SELECT * FROM (" + source + ") AS t" + statusJoin
		}
		return source
	}
	queryArgs := append(append([]any{}, args...), statusJoinArgs...)
	var total uint64
	if withTotal {
		countSQL := "SELECT count() FROM blockchain." + tableName(dataset) + " FINAL WHERE " + whereSQL
		if dedupSupported(dataset) {
			countSQL = dedupCount(dataset, whereSQL)
		}
		if statusJoin != "" {
			countSQL = "SELECT count() FROM (" + dedupSource(dataset, whereSQL) + ") AS t" + statusJoin
		}
		if err := s.CH.DB().QueryRow(ctx, countSQL, queryArgs...).Scan(&total); err != nil {
			return ActionPage{}, clickhouse.DescribeInteractiveScanError(err)
		}
	}
	offset := (f.Page - 1) * f.PageSize
	rows, err := s.queryRows(ctx, dataset, joinedSource()+" ORDER BY "+orderBy+" LIMIT ? OFFSET ?", append(queryArgs, f.PageSize, offset)...)
	if err != nil {
		return ActionPage{}, err
	}
	for _, row := range rows {
		if dataset == "token_transfers" {
			row["status"] = "unknown"
		}
		if f.Chain == "solana" && dataset == "transactions" {
			row["direction"] = "observed"
			continue
		}
		from, to := fmt.Sprint(row["from_address"]), fmt.Sprint(row["to_address"])
		switch {
		case sameActionAddress(f.Chain, from, f.Address) && sameActionAddress(f.Chain, to, f.Address):
			row["direction"] = "self"
		case sameActionAddress(f.Chain, to, f.Address):
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

// ClickHouse Decimal division by an integer literal truncates lamports to
// whole SOL. divideDecimal keeps all nine fractional digits without Float64.
const solanaLamportsDecimalSQL = "divideDecimal(toDecimal256OrZero(value_raw,0),toDecimal256(1000000000,0),9)"

func (s *Service) ActionFlowSummary(ctx context.Context, chain, address, token, contract string, start, end *time.Time) (FlowSummary, error) {
	address = normalizeActionAddress(chain, address)
	nativeSOL := chain == "solana" && strings.EqualFold(token, "SOL") && contract == ""
	where := []string{"chain=?", "(from_address=? OR to_address=?)"}
	args := []any{chain, address, address}
	if token != "" && !nativeSOL {
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
	table, amountExpr, key := "blockchain.token_transfers", "amount", "tx_hash,log_index,token_address"
	versionColumn := "source_ingested_at"
	if nativeSOL {
		table, amountExpr, key = "blockchain.internal_calls", solanaLamportsDecimalSQL, "tx_hash,call_index"
		versionColumn = "traced_at"
		where = append(where, "call_type IN ('system_transfer','system_create_account')", "success=1", "toDecimal256OrZero(value_raw,0)>0")
	}
	// ReplacingMergeTree deduplication is expressed as an explicit argMax group-by
	// on the sorting key instead of FINAL. Measured 2026-09-15 on the real
	// instance: with FINAL this aggregate read 85,074,805 rows and held
	// 6.87-10.62 GiB per call (23.4 s to 52.3 s, 12 real calls in 30 minutes),
	// could not use the bloom_filter skip indexes on from_address/to_address, and
	// was refused with Code 241 against the 19.60 GiB server ceiling. The same
	// answer through the deduplicated subquery below reads 1.0-1.4 M rows in
	// 0.3-3.3 s and 11-36 MiB. argMax(column, source_ingested_at) keeps exactly
	// the row FINAL keeps: one row per (chain, tx_hash, log_index, token_address)
	// with the greatest version, and the partition-key column is fixed by the
	// chain predicate. The outer predicates use the deduplicated aliases so the
	// planner cannot push a predicate into the aggregate subquery.
	inCond := "to_addr=target AND fr_addr!=target"
	outCond := "fr_addr=target AND to_addr!=target"
	args = append([]any{address}, args...)
	q := `WITH ? AS target SELECT toString(sumIf(amt,` + inCond + `)),toString(sumIf(amt,` + outCond + `)),toString(sumIf(amt,` + inCond + `)-sumIf(amt,` + outCond + `)),countIf(` + inCond + `),countIf(` + outCond + `),uniqExactIf(fr_addr,` + inCond + `),uniqExactIf(to_addr,` + outCond + `),nullIf(minIf(ts,` + inCond + `),toDateTime64(0,3,'UTC')),nullIf(maxIf(ts,` + inCond + `),toDateTime64(0,3,'UTC')),nullIf(minIf(ts,` + outCond + `),toDateTime64(0,3,'UTC')),nullIf(maxIf(ts,` + outCond + `),toDateTime64(0,3,'UTC')) FROM (SELECT ` + key + `,argMax(` + amountExpr + `,` + versionColumn + `) AS amt,argMax(timestamp,` + versionColumn + `) AS ts,argMax(from_address,` + versionColumn + `) AS fr_addr,argMax(to_address,` + versionColumn + `) AS to_addr FROM ` + table + ` WHERE ` + strings.Join(where, " AND ") + ` GROUP BY ` + key + `)`
	// The scan runs on the bounded interactive budget, so a pathological caller
	// is refused on its own budget instead of loading the shared server. Both the
	// deadline and the per-query setting are applied: clickhouse-go derives its
	// own max_execution_time from a context deadline and that derived value wins
	// over the setting (measured 64 s for the router's 60 s deadline), so the
	// deadline is what actually holds the scan inside the request budget.
	ctx, cancel := context.WithTimeout(ctx, clickhouse.InteractiveScanTimeout)
	defer cancel()
	ctx = clickhouse.InteractiveScanContext(ctx)
	var o FlowSummary
	err := s.CH.DB().QueryRow(ctx, q, args...).Scan(&o.TotalIn, &o.TotalOut, &o.NetFlow, &o.InTxCount, &o.OutTxCount, &o.UniqueInAddresses, &o.UniqueOutAddresses, &o.FirstInTime, &o.LastInTime, &o.FirstOutTime, &o.LastOutTime)
	if err != nil {
		return o, clickhouse.DescribeInteractiveScanError(err)
	}
	return o, nil
}

func (s *Service) ActionCounterparties(ctx context.Context, f ActionFilter, minTotal string, minCount, limit int, sortBy string) ([]map[string]any, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 1000 {
		limit = 1000
	}
	f.Address = normalizeActionAddress(f.Chain, f.Address)
	nativeSOL := f.Chain == "solana" && strings.EqualFold(f.Token, "SOL") && f.TokenContract == ""
	where := []string{"chain=?", "(from_address=? OR to_address=?)"}
	args := []any{f.Chain, f.Address, f.Address}
	if f.Token != "" && !nativeSOL {
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
	if nativeSOL {
		where = append(where, "call_type IN ('system_transfer','system_create_account')", "success=1", "toDecimal256OrZero(value_raw,0)>0")
	}
	having := []string{"count()>=?"}
	args = append(args, minCount)
	if minTotal != "" {
		having = append(having, "sum(amt)>=toDecimal256(?,38)")
		args = append(args, minTotal)
	}
	// 排序/HAVING 必须读去重后的别名（amt/fr_addr/to_addr），否则 ClickHouse 会把
	// 聚合别名替换进子查询的 WHERE（code 184：Aggregate function argMax(...) is found in WHERE）。
	order := map[string]string{"tx_count": "tx_count", "total_amount": "sum(amt)", "net_flow": "abs(sumIf(amt,to_addr=target AND fr_addr!=target)-sumIf(amt,fr_addr=target AND to_addr!=target))", "last_time": "last_tx_time"}[sortBy]
	if order == "" {
		order = "sum(amt)"
	}
	// FINAL 会按排序键合并整条链（同一地址实测 27.2 s vs 0.27 s），这里用排序键上的
	// argMax 显式去重后，再在外层做对手方聚合；跳数索引因此可以裁剪 granule。
	amountExpr, table, groupKey, symbolExpr := "amount", "blockchain.token_transfers", "tx_hash,log_index,token_address", "argMax(token_symbol,source_ingested_at)"
	versionColumn := "source_ingested_at"
	if nativeSOL {
		amountExpr, table, groupKey, symbolExpr = solanaLamportsDecimalSQL, "blockchain.internal_calls", "tx_hash,call_index", "'SOL'"
		versionColumn = "traced_at"
	}
	inner := "SELECT argMax(" + amountExpr + "," + versionColumn + ") AS amt," +
		"argMax(timestamp," + versionColumn + ") AS ts,argMax(from_address," + versionColumn + ") AS fr_addr," +
		"argMax(to_address," + versionColumn + ") AS to_addr," + symbolExpr + " AS sym " +
		"FROM " + table + " WHERE " + strings.Join(where, " AND ") + " GROUP BY " + groupKey
	q := `WITH ? AS target SELECT if(to_addr=target,fr_addr,to_addr) counterparty,count() tx_count,
		countIf(to_addr=target AND fr_addr!=target) incoming_tx_count,countIf(fr_addr=target AND to_addr!=target) outgoing_tx_count,
		toString(sumIf(amt,to_addr=target AND fr_addr!=target)) incoming_amount,
		toString(sumIf(amt,fr_addr=target AND to_addr!=target)) outgoing_amount,
		toString(sumIf(amt,to_addr=target AND fr_addr!=target)-sumIf(amt,fr_addr=target AND to_addr!=target)) net_flow,
		toString(sum(amt)) total_amount,min(ts) first_tx_time,max(ts) last_tx_time,any(sym) asset_symbol
		FROM (` + inner + ") GROUP BY counterparty HAVING " + strings.Join(having, " AND ") + " ORDER BY " + order + " DESC LIMIT ?"
	args = append([]any{f.Address}, args...)
	args = append(args, limit)
	ctx, cancel := context.WithTimeout(ctx, clickhouse.InteractiveScanTimeout)
	defer cancel()
	ctx = clickhouse.InteractiveScanContext(ctx)
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
	if strings.EqualFold(strings.TrimSpace(groupBy), "transaction") {
		// group_by=transaction 的契约是"最大单笔转账"（OpenAPI/MCP 工具描述均为
		// largest transfers）。此前直接复用默认 timestamp DESC 的明细页，返回的是
		// 最新几笔而非最大几笔；这里保留全部过滤/方向/状态回填，只把排序换成金额倒序。
		if limit <= 0 {
			limit = 20
		}
		// OpenAPI/MCP 对 limit 的上限即 200；与文档保持一致，不再放宽到明细页上限。
		if limit > 200 {
			limit = 200
		}
		if f.Chain == "solana" && strings.EqualFold(f.Token, "SOL") && f.TokenContract == "" {
			address := normalizeActionAddress(f.Chain, f.Address)
			where := []string{"chain='solana'", "(from_address=? OR to_address=?)", "call_type IN ('system_transfer','system_create_account')", "success=1", "toDecimal256OrZero(value_raw,0)>0"}
			args := []any{address, address}
			if f.Direction == "in" {
				where = append(where, "to_address=?")
				args = append(args, address)
			}
			if f.Direction == "out" {
				where = append(where, "from_address=?")
				args = append(args, address)
			}
			if f.StartTime != nil {
				where = append(where, "timestamp>=?")
				args = append(args, *f.StartTime)
			}
			if f.EndTime != nil {
				where = append(where, "timestamp<=?")
				args = append(args, *f.EndTime)
			}
			args = append(args, limit)
			ctx, cancel := context.WithTimeout(ctx, clickhouse.InteractiveScanTimeout)
			defer cancel()
			rows, err := s.queryRows(clickhouse.InteractiveScanContext(ctx), "internal_transactions", "SELECT * FROM blockchain.internal_calls FINAL WHERE "+strings.Join(where, " AND ")+" ORDER BY toDecimal256OrZero(value_raw,0) DESC,timestamp DESC LIMIT ?", args...)
			if err != nil {
				return nil, err
			}
			for _, row := range rows {
				amount, _ := decimal.NewFromString(fmt.Sprint(row["value_raw"]))
				row["amount"] = amount.Shift(-9).String()
				row["token_symbol"] = "SOL"
			}
			return rows, nil
		}
		f.Page = 1
		f.PageSize = limit
		p, err := s.actionRows(ctx, "token_transfers", f, "amount DESC, timestamp DESC", false)
		return p.Items, err
	}
	return s.ActionCounterparties(ctx, f, "", 0, limit, "total_amount")
}

func (s *Service) ActionFirstFunding(ctx context.Context, chain, address string) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, clickhouse.InteractiveScanTimeout)
	defer cancel()
	ctx = clickhouse.InteractiveScanContext(ctx)
	address = normalizeActionAddress(chain, address)
	out := map[string]any{"address": address, "first_native_funding": nil, "first_token_funding": []map[string]any{}}
	var from, amount, rawValue, hash string
	var ts time.Time
	// 逐 tx_hash 显式去重（transactions 的排序键就是 (chain, tx_hash)），不再 FINAL 整链合并。
	// 别名必须与列名不同：同名的聚合别名会被替换进子查询的 WHERE（code 184）。
	var err error
	if chain == "solana" {
		err = s.CH.DB().QueryRow(ctx, `SELECT from_address,toString(`+solanaLamportsDecimalSQL+`),value_raw,tx_hash,timestamp FROM blockchain.internal_calls FINAL WHERE chain='solana' AND to_address=? AND from_address!=? AND call_type IN ('system_transfer','system_create_account') AND success=1 AND toDecimal256OrZero(value_raw,0)>0 ORDER BY timestamp ASC,call_index ASC LIMIT 1`, address, address).Scan(&from, &amount, &rawValue, &hash, &ts)
	} else {
		err = s.CH.DB().QueryRow(ctx, `SELECT fr,amt,rawv,txh,ts FROM (SELECT argMax(from_address,source_ingested_at) AS fr,argMax(toString(toDecimal256OrZero(value_raw,0)/if(chain='tron',1000000,1000000000000000000)),source_ingested_at) AS amt,argMax(value_raw,source_ingested_at) AS rawv,argMax(tx_hash,source_ingested_at) AS txh,argMax(timestamp,source_ingested_at) AS ts FROM blockchain.transactions WHERE chain=? AND to_address=? AND from_address!=? AND toDecimal256OrZero(value_raw,0)>0 GROUP BY tx_hash) ORDER BY ts ASC LIMIT 1`, chain, address, address).Scan(&from, &amount, &rawValue, &hash, &ts)
	}
	if err == nil {
		out["first_native_funding"] = map[string]any{"from": from, "amount": amount, "raw_value": rawValue, "tx_hash": hash, "timestamp": ts}
		if chain == "solana" {
			out["first_native_funding"].(map[string]any)["source_type"] = "system_instruction"
		}
	}
	rows, e := s.queryRows(ctx, "token_transfers", dedupSource("token_transfers", "chain=? AND to_address=? AND from_address!=?")+" ORDER BY timestamp ASC LIMIT 20", chain, address, address)
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
	if chain != "solana" {
		hash = strings.ToLower(hash)
	}
	return s.queryRows(ctx, dataset, "SELECT * FROM blockchain."+tableName(dataset)+" FINAL WHERE chain=? AND tx_hash=? ORDER BY "+explorerOrder(dataset), chain, hash)
}

func (s *Service) ActionTransactionRecord(ctx context.Context, chain, hash string) (map[string]any, error) {
	if chain != "solana" {
		hash = strings.ToLower(hash)
	}
	rows, err := s.queryRows(ctx, "transactions", "SELECT * FROM blockchain.transactions FINAL WHERE chain=? AND tx_hash=? LIMIT 1", chain, hash)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return rows[0], nil
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
	nativeSOL := chain == "solana" && strings.EqualFold(token, "SOL") && tokenContract == ""
	if token != "" && !nativeSOL {
		where = append(where, "upper(token_symbol)=?")
		args = append(args, strings.ToUpper(token))
	}
	if tokenContract != "" {
		where = append(where, "token_address=?")
		args = append(args, normalizeActionAddress(chain, tokenContract))
	}
	if minAmount != "" && !nativeSOL {
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
	if nativeSOL {
		where = append(where, "call_type IN ('system_transfer','system_create_account')", "success=1", "toDecimal256OrZero(value_raw,0)>0")
		if minAmount != "" {
			where = append(where, solanaLamportsDecimalSQL+">=toDecimal256(?,9)")
			args = append(args, minAmount)
		}
		args = append(args, limit)
		ctx, cancel := context.WithTimeout(ctx, clickhouse.InteractiveScanTimeout)
		defer cancel()
		ctx = clickhouse.InteractiveScanContext(ctx)
		q := `SELECT fr_addr,to_addr,'SOL','',toString(sum(amt)),count(),min(ts),max(ts) FROM (SELECT tx_hash,call_index,argMax(from_address,traced_at) AS fr_addr,argMax(to_address,traced_at) AS to_addr,argMax(timestamp,traced_at) AS ts,argMax(` + solanaLamportsDecimalSQL + `,traced_at) AS amt FROM blockchain.internal_calls WHERE ` + strings.Join(where, " AND ") + ` GROUP BY tx_hash,call_index) GROUP BY fr_addr,to_addr ORDER BY sum(amt) DESC LIMIT ?`
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
		return out, rows.Err()
	}
	args = append(args, limit)
	ctx, cancel := context.WithTimeout(ctx, clickhouse.InteractiveScanTimeout)
	defer cancel()
	ctx = clickhouse.InteractiveScanContext(ctx)
	q := `SELECT from_address,to_address,any(token_symbol),any(token_address),toString(sum(amount)),count(),min(timestamp),max(timestamp) FROM (` +
		dedupSource("token_transfers", strings.Join(where, " AND ")) +
		`) GROUP BY from_address,to_address,token_address ORDER BY sum(amount) DESC LIMIT ?`
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
