package analysis

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/query"
	"github.com/shopspring/decimal"
)

const AlgorithmVersion = "basic-flow-v1.0"

type rowQuery interface {
	RowsForExport(context.Context, string, string, string, int, int) ([]map[string]any, error)
}
type Service struct{ query rowQuery }

func New(q *query.Service) *Service { return &Service{query: q} }

type Edge struct {
	Direction    string     `json:"direction"`
	Counterparty string     `json:"counterparty"`
	Asset        string     `json:"asset"`
	Amount       string     `json:"amount"`
	FlowRatio    string     `json:"flow_ratio"`
	Transactions int        `json:"transactions"`
	FirstSeen    *time.Time `json:"first_seen,omitempty"`
	LastSeen     *time.Time `json:"last_seen,omitempty"`
	Evidence     []string   `json:"evidence"`
}
type Result struct {
	AlgorithmVersion     string     `json:"algorithm_version"`
	Chain                string     `json:"chain"`
	Address              string     `json:"address"`
	TransactionCount     int        `json:"transaction_count"`
	UniqueCounterparties int        `json:"unique_counterparties"`
	FirstSeen            *time.Time `json:"first_seen,omitempty"`
	LastSeen             *time.Time `json:"last_seen,omitempty"`
	Edges                []Edge     `json:"edges"`
	FirstFunder          *Edge      `json:"first_funder,omitempty"`
	CashoutCandidates    []Edge     `json:"cashout_candidates"`
	EvidenceBoundary     string     `json:"evidence_boundary"`
}
type accumulator struct {
	direction, counterparty, asset string
	amount                         decimal.Decimal
	txs                            map[string]struct{}
	first, last                    *time.Time
	evidence                       []string
}

func (s *Service) Analyze(ctx context.Context, chain, address string) (Result, error) {
	if chain != "tron" {
		address = strings.ToLower(address)
	}
	rows, err := s.query.RowsForExport(ctx, "token_transfers", chain, address, 50000, 0)
	if err != nil {
		return Result{}, err
	}
	agg := map[string]*accumulator{}
	counterparties := map[string]struct{}{}
	allTx := map[string]struct{}{}
	var first, last *time.Time
	var firstFunder *Edge
	for _, row := range rows {
		from := strings.TrimSpace(asString(row["from_address"]))
		to := strings.TrimSpace(asString(row["to_address"]))
		dir, cp := "out", to
		if strings.EqualFold(to, address) {
			dir, cp = "in", from
		}
		if strings.EqualFold(from, address) && strings.EqualFold(to, address) {
			continue
		}
		if cp == "" {
			continue
		}
		asset := asString(row["token_symbol"])
		if asset == "" {
			asset = asString(row["token_address"])
		}
		if asset == "" {
			asset = "UNKNOWN"
		}
		amount, _ := decimal.NewFromString(asString(row["amount"]))
		tx := asString(row["tx_hash"])
		ts := asTime(row["timestamp"])
		key := dir + "\x00" + strings.ToLower(cp) + "\x00" + asset
		a := agg[key]
		if a == nil {
			a = &accumulator{direction: dir, counterparty: cp, asset: asset, amount: decimal.Zero, txs: map[string]struct{}{}}
			agg[key] = a
		}
		a.amount = a.amount.Add(amount)
		a.txs[tx] = struct{}{}
		if tx != "" && len(a.evidence) < 5 {
			a.evidence = append(a.evidence, tx)
		}
		updateBounds(&a.first, &a.last, ts)
		if dir == "in" && ts != nil && (firstFunder == nil || ts.Before(*firstFunder.FirstSeen)) {
			firstFunder = &Edge{Direction: dir, Counterparty: cp, Asset: asset, Amount: amount.String(), Transactions: 1, FirstSeen: ts, LastSeen: ts, Evidence: []string{tx}}
		}
		updateBounds(&first, &last, ts)
		counterparties[strings.ToLower(cp)] = struct{}{}
		allTx[tx] = struct{}{}
	}
	edges := make([]Edge, 0, len(agg))
	totals := map[string]decimal.Decimal{}
	for _, a := range agg {
		key := a.direction + "\x00" + a.asset
		totals[key] = totals[key].Add(a.amount)
	}
	for _, a := range agg {
		total := totals[a.direction+"\x00"+a.asset]
		ratio := decimal.Zero
		if !total.IsZero() {
			ratio = a.amount.Div(total)
		}
		edges = append(edges, Edge{Direction: a.direction, Counterparty: a.counterparty, Asset: a.asset, Amount: a.amount.String(), FlowRatio: ratio.StringFixed(8), Transactions: len(a.txs), FirstSeen: a.first, LastSeen: a.last, Evidence: a.evidence})
	}
	sort.Slice(edges, func(i, j int) bool {
		ai, _ := decimal.NewFromString(edges[i].Amount)
		aj, _ := decimal.NewFromString(edges[j].Amount)
		return ai.GreaterThan(aj)
	})
	if len(edges) > 100 {
		edges = edges[:100]
	}
	res := Result{AlgorithmVersion: AlgorithmVersion, Chain: chain, Address: address, TransactionCount: len(allTx), UniqueCounterparties: len(counterparties), FirstSeen: first, LastSeen: last, Edges: edges, EvidenceBoundary: "One-hop deterministic aggregation of indexed token transfers. Endpoint type and source-of-funds attribution are not inferred without labels or tracing."}
	bestOut := map[string]Edge{}
	for i := range edges {
		e := &edges[i]
		if e.Direction == "out" {
			current, ok := bestOut[e.Asset]
			cur, _ := decimal.NewFromString(current.Amount)
			amount, _ := decimal.NewFromString(e.Amount)
			if !ok || amount.GreaterThan(cur) {
				bestOut[e.Asset] = *e
			}
		}
	}
	res.FirstFunder = firstFunder
	for _, e := range bestOut {
		res.CashoutCandidates = append(res.CashoutCandidates, e)
	}
	sort.Slice(res.CashoutCandidates, func(i, j int) bool { return res.CashoutCandidates[i].Asset < res.CashoutCandidates[j].Asset })
	return res, nil
}
func asString(v any) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case decimal.Decimal:
		return x.String()
	case *decimal.Decimal:
		if x != nil {
			return x.String()
		}
	}
	return ""
}
func asTime(v any) *time.Time {
	switch x := v.(type) {
	case time.Time:
		return &x
	case *time.Time:
		return x
	case string:
		if t, e := time.Parse(time.RFC3339Nano, x); e == nil {
			return &t
		}
	}
	return nil
}
func updateBounds(first, last **time.Time, t *time.Time) {
	if t == nil {
		return
	}
	if *first == nil || t.Before(**first) {
		v := *t
		*first = &v
	}
	if *last == nil || t.After(**last) {
		v := *t
		*last = &v
	}
}
