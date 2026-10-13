package query

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
)

// Statistics are calculated from canonical event identities on the server.
// address_activity has a historical key without log_index and is unsuitable
// for exact event totals. A refusal never returns a partial successful total.
const StatisticsEventBudget = 250000
const statisticsGroupBudget = 50000

var ErrStatisticsBudget = errors.New("statistics scan exceeds the interactive event budget")

func statisticsAddressKey(chain, value string) string {
	value = strings.TrimSpace(value)
	if chain == "tron" || chain == "solana" {
		return value
	}
	return strings.ToLower(value)
}

func tokenIsContract(chain, value string) bool {
	return (strings.HasPrefix(strings.ToLower(value), "0x") && len(value) == 42) || ((chain == "tron" || chain == "solana") && len(value) >= 32)
}

func statisticsTokenMatches(chain, input, contract, symbol string) bool {
	for _, term := range strings.Split(input, ",") {
		term = strings.TrimSpace(term)
		if term == "" {
			continue
		}
		if tokenIsContract(chain, term) {
			if contract == statisticsAddressKey(chain, term) {
				return true
			}
		} else if strings.Contains(strings.ToLower(symbol), strings.ToLower(term)) || strings.Contains(strings.ToLower(contract), strings.ToLower(term)) {
			return true
		}
	}
	return false
}

type StatisticsFilter struct {
	Chain        string     `json:"chain"`
	Addresses    []string   `json:"addresses"`
	Datasets     []string   `json:"datasets"`
	Token        string     `json:"token,omitempty"`
	Direction    string     `json:"direction,omitempty"`
	Counterparty string     `json:"counterparty,omitempty"`
	FromAddress  string     `json:"from_address,omitempty"`
	ToAddress    string     `json:"to_address,omitempty"`
	HideZero     bool       `json:"hide_zero,omitempty"`
	FromTime     *time.Time `json:"from_time,omitempty"`
	ToTime       *time.Time `json:"to_time,omitempty"`
	AsOf         *time.Time `json:"as_of,omitempty"`
	MinAmount    string     `json:"min_amount,omitempty"`
	MaxAmount    string     `json:"max_amount,omitempty"`
	BlockFrom    *int64     `json:"block_from,omitempty"`
	BlockTo      *int64     `json:"block_to,omitempty"`
	Interval     string     `json:"interval"`
	Timezone     string     `json:"timezone"`
}

func (f *StatisticsFilter) Normalize() error {
	f.Chain = strings.ToLower(strings.TrimSpace(f.Chain))
	if f.Chain == "" || len(f.Addresses) == 0 || len(f.Addresses) > 100 {
		return fmt.Errorf("chain and 1–100 addresses required")
	}
	set := map[string]bool{}
	addresses := []string{}
	for _, a := range f.Addresses {
		a = statisticsAddressKey(f.Chain, a)
		if a == "" {
			return fmt.Errorf("empty address")
		}
		if !set[a] {
			addresses = append(addresses, a)
			set[a] = true
		}
	}
	sort.Strings(addresses)
	f.Addresses = addresses
	if len(f.Datasets) == 0 {
		f.Datasets = []string{"token_transfers"}
	}
	datasets := []string{}
	set = map[string]bool{}
	for _, d := range f.Datasets {
		if d != "transactions" && d != "token_transfers" && d != "internal_transactions" {
			return fmt.Errorf("unsupported statistics dataset")
		}
		if !set[d] {
			datasets = append(datasets, d)
			set[d] = true
		}
	}
	sort.Strings(datasets)
	f.Datasets = datasets
	if f.Direction != "" && f.Direction != "in" && f.Direction != "out" && f.Direction != "self" && f.Direction != "internal" {
		return fmt.Errorf("invalid direction")
	}
	f.Token = strings.TrimSpace(f.Token)
	f.Counterparty = statisticsAddressKey(f.Chain, f.Counterparty)
	f.FromAddress = statisticsAddressKey(f.Chain, f.FromAddress)
	f.ToAddress = statisticsAddressKey(f.Chain, f.ToAddress)
	if len(f.Token) > 128 {
		return fmt.Errorf("token filter too long")
	}
	if f.FromTime != nil && f.ToTime != nil && f.FromTime.After(*f.ToTime) {
		return fmt.Errorf("from_time is after to_time")
	}
	if (f.BlockFrom != nil && *f.BlockFrom < 0) || (f.BlockTo != nil && *f.BlockTo < 0) || (f.BlockFrom != nil && f.BlockTo != nil && *f.BlockFrom > *f.BlockTo) {
		return fmt.Errorf("invalid block range")
	}
	for _, v := range []string{f.MinAmount, f.MaxAmount} {
		if v != "" && !decimalAmountPattern.MatchString(v) {
			return fmt.Errorf("invalid amount")
		}
	}
	if f.MinAmount != "" && f.MaxAmount != "" {
		a, _ := new(big.Rat).SetString(f.MinAmount)
		b, _ := new(big.Rat).SetString(f.MaxAmount)
		if a.Cmp(b) > 0 {
			return fmt.Errorf("min_amount exceeds max_amount")
		}
	}
	if f.Interval == "" {
		f.Interval = "day"
	}
	if f.Interval != "day" && f.Interval != "hour" {
		return fmt.Errorf("interval must be day or hour")
	}
	if f.Timezone == "" {
		f.Timezone = "Asia/Shanghai"
	}
	if _, err := time.LoadLocation(f.Timezone); err != nil {
		return fmt.Errorf("invalid timezone")
	}
	if f.AsOf == nil {
		v := time.Now().UTC()
		f.AsOf = &v
	}
	if f.AsOf.After(time.Now().Add(time.Minute)) {
		return fmt.Errorf("as_of is in the future")
	}
	return nil
}

type CanonicalEvent struct {
	Dataset      string    `json:"dataset"`
	TxHash       string    `json:"tx_hash"`
	Index        uint32    `json:"event_index"`
	TokenAddress string    `json:"token_address"`
	TokenSymbol  string    `json:"token_symbol"`
	Decimals     uint16    `json:"token_decimals"`
	Timestamp    time.Time `json:"timestamp"`
	From         string    `json:"from_address"`
	To           string    `json:"to_address"`
	AmountRaw    string    `json:"amount_raw"`
	IngestedAt   time.Time `json:"source_ingested_at"`
	Status       uint8     `json:"status"`
	BlockNumber  uint64    `json:"block_number"`
	Extras       string    `json:"-"`
}

// ScanCanonicalEvents uses indexed directions and argMax version tuples.
// Filtering by immutable chain/address/time precedes dedup; token/direction/
// amount filters follow it. All caller values are bound parameters.
func (s *Service) ScanCanonicalEvents(ctx context.Context, f StatisticsFilter, visit func(CanonicalEvent) error) error {
	if err := f.Normalize(); err != nil {
		return err
	}
	if s.CH == nil {
		return fmt.Errorf("local index unavailable")
	}
	ctx = clickhouse.InteractiveScanContext(ctx)
	seen := 0
	for _, dataset := range f.Datasets {
		table, index, contract, symbol, decimals, raw, version, status := "token_transfers", "log_index", "token_address", "token_symbol", "token_decimals", "amount_raw", "source_ingested_at", "toUInt8(1)"
		extra := "toJSONString(map('source_provider',toString(source_provider),'amount',toString(amount)))"
		cfg, _ := nativeChains.Get(f.Chain)
		if dataset == "transactions" || dataset == "internal_transactions" {
			table, index, contract, symbol, decimals, raw, status = "transactions", "toUInt32(0)", "''", "?", "toUInt16(?)", "value_raw", "status"
			extra = "toJSONString(map('block_hash',block_hash,'nonce',toString(nonce),'gas',toString(gas),'gas_used',toString(gas_used),'gas_price',gas_price,'tx_type',tx_type,'input',input,'method_id',method_id,'source_provider',toString(source_provider)))"
			if dataset == "internal_transactions" {
				table, index, version, status = "internal_calls", "call_index", "traced_at", "success"
				extra = "toJSONString(map('depth',toString(depth),'trace_path',trace_path,'call_type',toString(call_type),'method_id',method_id,'gas',toString(gas),'gas_used',toString(gas_used),'error',error,'source_provider',toString(source_provider)))"
			}
		}
		base := "chain=? AND (from_address IN (?) OR to_address IN (?)) AND " + version + "<=?"
		args := []any{}
		if dataset != "token_transfers" {
			args = append(args, cfg.Native.Symbol, nativeDecimalsForChain(f.Chain))
		}
		args = append(args, f.Chain, f.Addresses, f.Addresses, f.AsOf.UTC())
		if f.FromTime != nil {
			base += " AND timestamp>=?"
			args = append(args, f.FromTime.UTC())
		}
		if f.ToTime != nil {
			base += " AND timestamp<=?"
			args = append(args, f.ToTime.UTC())
		}
		if f.BlockFrom != nil {
			base += " AND block_number>=?"
			args = append(args, *f.BlockFrom)
		}
		if f.BlockTo != nil {
			base += " AND block_number<=?"
			args = append(args, *f.BlockTo)
		}
		q := fmt.Sprintf(`SELECT tx_hash, event_index, asset_contract, v.1,v.2,v.3,v.4,v.5,v.6,v.7,v.8,v.9,v.10
 FROM (SELECT tx_hash,%s AS event_index,%s AS asset_contract,
 argMax(tuple(timestamp,from_address,to_address,%s,%s,%s,%s,%s,%s,%s),%s) AS v
 FROM blockchain.%s WHERE %s GROUP BY tx_hash,event_index,asset_contract)`, index, contract, raw, symbol, decimals, version, status, "block_number", extra, version, table, base)
		outer := []string{}
		if f.Token != "" {
			parts := []string{}
			for _, rawTerm := range strings.Split(f.Token, ",") {
				term := strings.TrimSpace(rawTerm)
				if term == "" {
					continue
				}
				if tokenIsContract(f.Chain, term) {
					parts = append(parts, "asset_contract=?")
					args = append(args, statisticsAddressKey(f.Chain, term))
				} else {
					parts = append(parts, "(positionCaseInsensitive(v.5,?)>0 OR positionCaseInsensitive(asset_contract,?)>0)")
					args = append(args, term, term)
				}
			}
			if len(parts) > 0 {
				outer = append(outer, "("+strings.Join(parts, " OR ")+")")
			}
		}
		if f.FromAddress != "" {
			outer = append(outer, "v.2=?")
			args = append(args, f.FromAddress)
		}
		if f.ToAddress != "" {
			outer = append(outer, "v.3=?")
			args = append(args, f.ToAddress)
		}
		switch f.Direction {
		case "in":
			outer = append(outer, "v.3 IN (?) AND v.2 NOT IN (?)")
			args = append(args, f.Addresses, f.Addresses)
		case "out":
			outer = append(outer, "v.2 IN (?) AND v.3 NOT IN (?)")
			args = append(args, f.Addresses, f.Addresses)
		case "self":
			outer = append(outer, "v.2=v.3 AND v.2 IN (?)")
			args = append(args, f.Addresses)
		case "internal":
			outer = append(outer, "v.2!=v.3 AND v.2 IN (?) AND v.3 IN (?)")
			args = append(args, f.Addresses, f.Addresses)
		}
		if f.Counterparty != "" {
			outer = append(outer, "(v.2=? OR v.3=?)")
			args = append(args, f.Counterparty, f.Counterparty)
		}
		if len(outer) > 0 {
			q += " WHERE " + strings.Join(outer, " AND ")
		}
		q += " LIMIT ?"
		args = append(args, StatisticsEventBudget-seen+1)
		rows, err := s.CH.DB().Query(ctx, q, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var e CanonicalEvent
			e.Dataset = dataset
			if err := rows.Scan(&e.TxHash, &e.Index, &e.TokenAddress, &e.Timestamp, &e.From, &e.To, &e.AmountRaw, &e.TokenSymbol, &e.Decimals, &e.IngestedAt, &e.Status, &e.BlockNumber, &e.Extras); err != nil {
				rows.Close()
				return err
			}
			seen++
			if seen > StatisticsEventBudget {
				rows.Close()
				return ErrStatisticsBudget
			}
			if eventMatches(f, e) {
				if err := visit(e); err != nil {
					rows.Close()
					return err
				}
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func eventDirection(f StatisticsFilter, e CanonicalEvent) string {
	in, out := false, false
	for _, a := range f.Addresses {
		in = in || e.To == a
		out = out || e.From == a
	}
	if in && out {
		if e.From == e.To {
			return "self"
		}
		return "internal"
	}
	if in {
		return "in"
	}
	if out {
		return "out"
	}
	return ""
}

func eventMatches(f StatisticsFilter, e CanonicalEvent) bool {
	dir := eventDirection(f, e)
	if f.Direction != "" && f.Direction != dir {
		return false
	}
	if f.Token != "" && !statisticsTokenMatches(f.Chain, f.Token, e.TokenAddress, e.TokenSymbol) {
		return false
	}
	if (f.FromAddress != "" && e.From != f.FromAddress) || (f.ToAddress != "" && e.To != f.ToAddress) {
		return false
	}
	if f.HideZero {
		n, ok := eventAmount(e)
		if ok && n.Sign() == 0 {
			return false
		}
	}
	if f.Counterparty != "" {
		if (dir == "in" && e.From != f.Counterparty) || (dir == "out" && e.To != f.Counterparty) || ((dir == "internal" || dir == "self") && e.From != f.Counterparty && e.To != f.Counterparty) {
			return false
		}
	}
	if f.MinAmount != "" || f.MaxAmount != "" {
		n, ok := eventAmount(e)
		if !ok {
			return false
		}
		value := new(big.Rat).SetFrac(n, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(e.Decimals)), nil))
		if f.MinAmount != "" {
			bound, _ := new(big.Rat).SetString(f.MinAmount)
			if value.Cmp(bound) < 0 {
				return false
			}
		}
		if f.MaxAmount != "" {
			bound, _ := new(big.Rat).SetString(f.MaxAmount)
			if value.Cmp(bound) > 0 {
				return false
			}
		}
	}
	return true
}

func eventAmount(e CanonicalEvent) (*big.Int, bool) {
	if e.Decimals > 255 {
		return nil, false
	}
	if e.Dataset != "token_transfers" {
		n, err := parseNativeRaw(e.AmountRaw)
		return n, err == nil
	}
	if e.AmountRaw == "" {
		return nil, false
	}
	n, ok := new(big.Int).SetString(e.AmountRaw, 10)
	return n, ok && n.Sign() >= 0
}

type FlowTotal struct {
	Dataset      string     `json:"dataset"`
	TokenAddress string     `json:"token_address"`
	TokenSymbol  string     `json:"token_symbol"`
	Decimals     uint16     `json:"token_decimals"`
	InRaw        string     `json:"in_raw"`
	OutRaw       string     `json:"out_raw"`
	NetRaw       string     `json:"net_raw"`
	InternalRaw  string     `json:"internal_raw"`
	SelfRaw      string     `json:"self_raw"`
	Events       uint64     `json:"event_count"`
	Missing      uint64     `json:"missing_amount_rows"`
	Failed       uint64     `json:"failed_event_count"`
	First        *time.Time `json:"first_seen"`
	Last         *time.Time `json:"last_seen"`
	TxCount      uint64     `json:"transaction_count"`
}

type StatisticsGroup struct {
	Key             string    `json:"key"`
	Total           FlowTotal `json:"total"`
	Counterparty    string    `json:"counterparty,omitempty"`
	Address         string    `json:"address,omitempty"`
	Bucket          string    `json:"bucket,omitempty"`
	CommonAddresses []string  `json:"common_addresses,omitempty"`
	Share           string    `json:"share_percent,omitempty"`
}

type StatisticsResult struct {
	Filter            StatisticsFilter  `json:"filter"`
	Version           string            `json:"data_version"`
	Transactions      uint64            `json:"transaction_count"`
	Events            uint64            `json:"event_count"`
	Counterparties    uint64            `json:"counterparty_count"`
	Assets            []StatisticsGroup `json:"assets"`
	Trend             []StatisticsGroup `json:"trend"`
	Hours             []StatisticsGroup `json:"hours"`
	Distribution      []StatisticsGroup `json:"distribution"`
	CounterpartyItems []StatisticsGroup `json:"counterparties"`
	Addresses         []StatisticsGroup `json:"addresses"`
	TopTransfers      []CanonicalEvent  `json:"top_transfers"`
	First             *time.Time        `json:"first_seen"`
	Last              *time.Time        `json:"last_seen"`
}

type flowAccumulator struct {
	group                   StatisticsGroup
	in, out, internal, self big.Int
	txs                     map[string]bool
	addresses               map[string]bool
}

func newFlow(e CanonicalEvent) *flowAccumulator {
	return &flowAccumulator{group: StatisticsGroup{Total: FlowTotal{Dataset: e.Dataset, TokenAddress: e.TokenAddress, TokenSymbol: e.TokenSymbol, Decimals: e.Decimals}}, txs: map[string]bool{}, addresses: map[string]bool{}}
}

func (a *flowAccumulator) add(e CanonicalEvent, direction string) {
	t := &a.group.Total
	t.Events++
	a.txs[e.TxHash] = true
	if t.First == nil || e.Timestamp.Before(*t.First) {
		v := e.Timestamp
		t.First = &v
	}
	if t.Last == nil || e.Timestamp.After(*t.Last) {
		v := e.Timestamp
		t.Last = &v
	}
	if e.Status == 0 {
		t.Failed++
		return
	}
	n, ok := eventAmount(e)
	if !ok {
		t.Missing++
		return
	}
	switch direction {
	case "in":
		a.in.Add(&a.in, n)
	case "out":
		a.out.Add(&a.out, n)
	case "internal":
		a.internal.Add(&a.internal, n)
	case "self":
		a.self.Add(&a.self, n)
	}
}

func (a *flowAccumulator) finish() StatisticsGroup {
	g := a.group
	g.Total.InRaw = a.in.String()
	g.Total.OutRaw = a.out.String()
	g.Total.InternalRaw = a.internal.String()
	g.Total.SelfRaw = a.self.String()
	g.Total.NetRaw = new(big.Int).Sub(&a.in, &a.out).String()
	g.Total.TxCount = uint64(len(a.txs))
	for v := range a.addresses {
		g.CommonAddresses = append(g.CommonAddresses, v)
	}
	sort.Strings(g.CommonAddresses)
	return g
}

func assetKey(e CanonicalEvent) string {
	return fmt.Sprintf("%s|%s|%d", e.Dataset, e.TokenAddress, e.Decimals)
}

// Statistics retains aggregates and a bounded top list, never sends the event
// scan to the browser, and never mixes different contracts, chains or decimals.
func (s *Service) Statistics(ctx context.Context, f StatisticsFilter) (StatisticsResult, error) {
	if err := f.Normalize(); err != nil {
		return StatisticsResult{}, err
	}
	return calculateStatistics(f, func(visit func(CanonicalEvent) error) error { return s.ScanCanonicalEvents(ctx, f, visit) })
}

func calculateStatistics(f StatisticsFilter, scan func(func(CanonicalEvent) error) error) (StatisticsResult, error) {
	result := StatisticsResult{Filter: f, Assets: []StatisticsGroup{}, Trend: []StatisticsGroup{}, Hours: []StatisticsGroup{}, Distribution: []StatisticsGroup{}, CounterpartyItems: []StatisticsGroup{}, Addresses: []StatisticsGroup{}, TopTransfers: []CanonicalEvent{}}
	groups := map[string]*flowAccumulator{}
	txs := map[string]bool{}
	counterparties := map[string]bool{}
	loc, _ := time.LoadLocation(f.Timezone)
	add := func(prefix, key string, e CanonicalEvent, dir string) *flowAccumulator {
		k := prefix + "|" + assetKey(e) + "|" + key
		a := groups[k]
		if a == nil {
			a = newFlow(e)
			a.group.Key = k
			groups[k] = a
		}
		a.add(e, dir)
		return a
	}
	topByAsset := map[string][]CanonicalEvent{}
	err := scan(func(e CanonicalEvent) error {
		dir := eventDirection(f, e)
		txs[e.TxHash] = true
		result.Events++
		if result.First == nil || e.Timestamp.Before(*result.First) {
			v := e.Timestamp
			result.First = &v
		}
		if result.Last == nil || e.Timestamp.After(*result.Last) {
			v := e.Timestamp
			result.Last = &v
		}
		add("asset", "", e, dir)
		format := "2006-01-02T00:00:00Z07:00"
		if f.Interval == "hour" {
			format = "2006-01-02T15:00:00Z07:00"
		}
		bucket := e.Timestamp.In(loc).Format(format)
		add("trend", bucket, e, dir).group.Bucket = bucket
		hour := e.Timestamp.In(loc).Format("15")
		add("hour", hour, e, dir).group.Bucket = hour
		if n, ok := eventAmount(e); ok {
			value := new(big.Rat).SetFrac(n, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(e.Decimals)), nil))
			bucket := "10000+"
			for _, b := range []struct {
				bound int64
				label string
			}{{1, "0–1"}, {10, "1–10"}, {100, "10–100"}, {1000, "100–1000"}, {10000, "1000–10000"}} {
				if value.Cmp(new(big.Rat).SetInt64(b.bound)) < 0 {
					bucket = b.label
					break
				}
			}
			add("distribution", bucket, e, dir).group.Bucket = bucket
			if e.Status != 0 {
				key := assetKey(e)
				top := append(topByAsset[key], e)
				sort.SliceStable(top, func(i, j int) bool { a, _ := eventAmount(top[i]); b, _ := eventAmount(top[j]); return a.Cmp(b) > 0 })
				if len(top) > 100 {
					top = top[:100]
				}
				topByAsset[key] = top
			}
		}
		if (dir == "in" && e.From != "") || (dir == "out" && e.To != "") {
			cp := e.From
			if dir == "out" {
				cp = e.To
			}
			counterparties[cp] = true
			a := add("counterparty", cp, e, dir)
			a.group.Counterparty = cp
			for _, addr := range f.Addresses {
				if addr == e.From || addr == e.To {
					a.addresses[addr] = true
				}
			}
		}
		for _, addr := range f.Addresses {
			if addr != e.From && addr != e.To {
				continue
			}
			d := "in"
			if e.From == addr {
				d = "out"
			}
			if e.From == e.To {
				d = "self"
			}
			a := add("address", addr, e, d)
			a.group.Address = addr
		}
		if len(groups) > statisticsGroupBudget {
			return ErrStatisticsBudget
		}
		return nil
	})
	if err != nil {
		return StatisticsResult{}, err
	}
	assetKeys := make([]string, 0, len(topByAsset))
	for key := range topByAsset {
		assetKeys = append(assetKeys, key)
	}
	sort.Strings(assetKeys)
	for _, key := range assetKeys {
		result.TopTransfers = append(result.TopTransfers, topByAsset[key]...)
	}
	result.Transactions = uint64(len(txs))
	result.Counterparties = uint64(len(counterparties))
	denominators := map[string]uint64{}
	for k, a := range groups {
		if strings.HasPrefix(k, "asset|") {
			denominators[assetKey(CanonicalEvent{Dataset: a.group.Total.Dataset, TokenAddress: a.group.Total.TokenAddress, Decimals: a.group.Total.Decimals})] = a.group.Total.Events
		}
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		a := groups[k]
		g := a.finish()
		switch strings.SplitN(k, "|", 2)[0] {
		case "asset":
			result.Assets = append(result.Assets, g)
		case "trend":
			result.Trend = append(result.Trend, g)
		case "hour":
			result.Hours = append(result.Hours, g)
		case "distribution":
			result.Distribution = append(result.Distribution, g)
		case "address":
			result.Addresses = append(result.Addresses, g)
		case "counterparty":
			den := denominators[assetKey(CanonicalEvent{Dataset: g.Total.Dataset, TokenAddress: g.Total.TokenAddress, Decimals: g.Total.Decimals})]
			if den > 0 {
				g.Share = new(big.Rat).SetFrac(new(big.Int).SetUint64(g.Total.Events*100), new(big.Int).SetUint64(den)).FloatString(6)
			}
			result.CounterpartyItems = append(result.CounterpartyItems, g)
		}
	}
	encoded, _ := json.Marshal(f)
	h := sha256.Sum256(encoded)
	result.Version = hex.EncodeToString(h[:])
	return result, nil
}

func AsActivityRow(row map[string]any, chain, address, dataset string) {
	from, to := fmt.Sprint(row["from_address"]), fmt.Sprint(row["to_address"])
	row["address"], row["dataset"] = address, dataset
	direction, cp := "in", from
	if from == address {
		direction, cp = "out", to
	}
	if from == address && to == address {
		direction = "self"
	}
	row["direction"], row["counterparty"] = direction, cp
	if dataset == "token_transfers" {
		row["activity_type"] = "transfer"
		return
	}
	row["activity_type"] = "transaction"
	if dataset == "internal_transactions" {
		row["activity_type"] = "internal_transfer"
		row["log_index"] = row["internal_index"]
	}
	cfg, _ := nativeChains.Get(chain)
	row["token_symbol"], row["token_decimals"], row["token_address"] = cfg.Native.Symbol, nativeDecimalsForChain(chain), ""
	raw := fmt.Sprint(row["value_raw"])
	if n, err := parseNativeRaw(raw); err == nil {
		row["amount_raw"] = n.String()
	} else {
		row["amount_raw"] = nil
	}
}

func AsGroupActivityRow(row map[string]any, addresses []string) {
	e := CanonicalEvent{From: fmt.Sprint(row["from_address"]), To: fmt.Sprint(row["to_address"])}
	dir := eventDirection(StatisticsFilter{Addresses: addresses}, e)
	row["direction"] = dir
	if dir == "out" {
		row["counterparty"] = e.To
	} else if dir == "in" {
		row["counterparty"] = e.From
	} else {
		row["counterparty"] = ""
	}
}

func (e CanonicalEvent) ExportRow(chain string) map[string]any {
	row := map[string]any{}
	if e.Extras != "" {
		_ = json.Unmarshal([]byte(e.Extras), &row)
	}
	row["chain"], row["dataset"], row["tx_hash"], row["timestamp"], row["block_number"] = chain, e.Dataset, e.TxHash, e.Timestamp, e.BlockNumber
	row["from_address"], row["to_address"], row["amount_raw"], row["token_address"], row["token_symbol"], row["token_decimals"], row["source_ingested_at"], row["status"] = e.From, e.To, e.AmountRaw, e.TokenAddress, e.TokenSymbol, e.Decimals, e.IngestedAt, e.Status
	if e.Dataset == "token_transfers" {
		row["log_index"] = e.Index
	} else {
		row["value_raw"] = e.AmountRaw
		AsActivityRow(row, chain, "", e.Dataset)
	}
	if e.Dataset == "internal_transactions" {
		row["internal_index"], row["call_index"], row["success"] = e.Index, e.Index, e.Status
	}
	return row
}

// MatchExportRow preserves the dataset's original fields for non-aggregate
// datasets (including NFTs) while applying the same address/amount filters.
func MatchExportRow(f StatisticsFilter, dataset string, row map[string]any) bool {
	text := func(key string) string {
		v := row[key]
		if v == nil {
			return ""
		}
		return fmt.Sprint(v)
	}
	raw := text("amount_raw")
	if raw == "" {
		raw = text("value_raw")
	}
	contract := text("token_address")
	if contract == "" {
		contract = text("contract_address")
	}
	var decimals uint16
	switch v := row["token_decimals"].(type) {
	case uint16:
		decimals = v
	case uint8:
		decimals = uint16(v)
	case uint64:
		decimals = uint16(v)
	}
	if dataset == "transactions" || dataset == "internal_transactions" {
		decimals = uint16(nativeDecimalsForChain(f.Chain))
	}
	e := CanonicalEvent{Dataset: dataset, From: text("from_address"), To: text("to_address"), AmountRaw: raw, TokenAddress: contract, TokenSymbol: text("token_symbol"), Decimals: decimals}
	return eventMatches(f, e)
}
