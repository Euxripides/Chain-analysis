// Package advanced implements deterministic Phase 5 investigation signals.
// It intentionally separates indexed facts from heuristic relationships and
// never presents token net flow as fiat-denominated realized profit.
package advanced

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/dbtx"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

const ProjectionVersion = "1.0"
const AlgorithmVersion = "advanced-investigation-v1.0"

type rowQuery interface {
	RowsForExport(context.Context, string, string, string, int, int) ([]map[string]any, error)
}

type Service struct {
	pool  *dbtx.Pool
	query rowQuery
}

func New(pool *dbtx.Pool, query rowQuery) *Service { return &Service{pool: pool, query: query} }

type Signal struct {
	Type       string         `json:"type"`
	Confidence float64        `json:"confidence"`
	Summary    string         `json:"summary"`
	Metrics    map[string]any `json:"metrics"`
	Evidence   []string       `json:"evidence"`
	Nature     string         `json:"nature"`
}

type AssetFlow struct {
	Asset       string `json:"asset"`
	Inflow      string `json:"inflow"`
	Outflow     string `json:"outflow"`
	NetFlow     string `json:"net_flow"`
	BasisStatus string `json:"basis_status"`
}

type RelatedWallet struct {
	Address       string   `json:"address"`
	Score         float64  `json:"score"`
	Transactions  int      `json:"transactions"`
	Directions    []string `json:"directions"`
	Evidence      []string `json:"evidence"`
	RelationKind  string   `json:"relation_kind"`
	EvidenceLevel string   `json:"evidence_level"`
}

type Result struct {
	Chain             string          `json:"chain"`
	Subject           string          `json:"subject"`
	Signals           []Signal        `json:"signals"`
	AssetFlows        []AssetFlow     `json:"asset_flows"`
	RelatedWallets    []RelatedWallet `json:"related_wallets"`
	ProjectionVersion string          `json:"projection_version"`
	AlgorithmVersion  string          `json:"algorithm_version"`
	DataVersion       string          `json:"data_version"`
	ComputedAt        *time.Time      `json:"computed_at,omitempty"`
	EvidenceBoundary  string          `json:"evidence_boundary"`
	Limitations       []string        `json:"limitations"`
}

type movement struct {
	tx, counterparty, asset, direction string
	amount                             decimal.Decimal
	at                                 time.Time
}

type walletAgg struct {
	txs, evidence       map[string]struct{}
	directions          map[string]struct{}
	inAmount, outAmount decimal.Decimal
}

func (s *Service) Rebuild(ctx context.Context, chain, subject string) (Result, error) {
	rows, err := s.query.RowsForExport(ctx, "token_transfers", chain, subject, 50000, 0)
	if err != nil {
		return Result{}, err
	}
	res := analyzeRows(chain, subject, rows)
	raw, _ := json.Marshal(res)
	err = s.pool.QueryRow(ctx, `INSERT INTO advanced_analysis_results(chain,subject,projection_version,algorithm_version,data_version,result,computed_at)
		VALUES($1,$2,$3,$4,$5,$6,now())
		ON CONFLICT(chain,subject,projection_version) DO UPDATE SET algorithm_version=EXCLUDED.algorithm_version,data_version=EXCLUDED.data_version,result=EXCLUDED.result,computed_at=now()
		RETURNING computed_at`, chain, subject, ProjectionVersion, AlgorithmVersion, res.DataVersion, raw).Scan(&res.ComputedAt)
	return res, err
}

func (s *Service) Get(ctx context.Context, chain, subject string) (Result, error) {
	var raw []byte
	var computed time.Time
	err := s.pool.QueryRow(ctx, `SELECT result,computed_at FROM advanced_analysis_results WHERE chain=$1 AND lower(subject)=lower($2) AND projection_version=$3`, chain, subject, ProjectionVersion).Scan(&raw, &computed)
	if errors.Is(err, pgx.ErrNoRows) {
		return emptyResult(chain, subject), nil
	}
	if err != nil {
		return Result{}, err
	}
	var res Result
	if err = json.Unmarshal(raw, &res); err != nil {
		return Result{}, err
	}
	res.ComputedAt = &computed
	return res, nil
}

func analyzeRows(chain, subject string, rows []map[string]any) Result {
	moves := make([]movement, 0, len(rows))
	assets := map[string][2]decimal.Decimal{}
	wallets := map[string]*walletAgg{}
	inWallets, outWallets := map[string]struct{}{}, map[string]struct{}{}
	dataVersion := "empty"
	var latest time.Time
	for _, row := range rows {
		from, to := text(row["from_address"]), text(row["to_address"])
		direction, cp := "out", to
		if strings.EqualFold(to, subject) {
			direction, cp = "in", from
		}
		if cp == "" || strings.EqualFold(from, to) {
			continue
		}
		asset := text(row["token_symbol"])
		if asset == "" {
			asset = text(row["token_address"])
		}
		if asset == "" {
			asset = "UNKNOWN"
		}
		amount, _ := decimal.NewFromString(text(row["amount"]))
		at := timestamp(row["timestamp"])
		txHash := text(row["tx_hash"])
		moves = append(moves, movement{tx: txHash, counterparty: cp, asset: asset, direction: direction, amount: amount, at: at})
		flow := assets[asset]
		if direction == "in" {
			flow[0] = flow[0].Add(amount)
			inWallets[strings.ToLower(cp)] = struct{}{}
		} else {
			flow[1] = flow[1].Add(amount)
			outWallets[strings.ToLower(cp)] = struct{}{}
		}
		assets[asset] = flow
		key := strings.ToLower(cp)
		w := wallets[key]
		if w == nil {
			w = &walletAgg{txs: map[string]struct{}{}, evidence: map[string]struct{}{}, directions: map[string]struct{}{}, inAmount: decimal.Zero, outAmount: decimal.Zero}
			wallets[key] = w
		}
		w.txs[txHash], w.evidence[txHash], w.directions[direction] = struct{}{}, struct{}{}, struct{}{}
		if direction == "in" {
			w.inAmount = w.inAmount.Add(amount)
		} else {
			w.outAmount = w.outAmount.Add(amount)
		}
		if t := timestamp(row["source_ingested_at"]); t.After(latest) {
			latest = t
		}
	}
	if !latest.IsZero() {
		dataVersion = latest.UTC().Format(time.RFC3339Nano)
	}
	sort.Slice(moves, func(i, j int) bool { return moves[i].at.Before(moves[j].at) })
	res := emptyResult(chain, subject)
	res.DataVersion = dataVersion
	for asset, flow := range assets {
		res.AssetFlows = append(res.AssetFlows, AssetFlow{Asset: asset, Inflow: flow[0].String(), Outflow: flow[1].String(), NetFlow: flow[0].Sub(flow[1]).String(), BasisStatus: "NET_FLOW_ONLY"})
	}
	sort.Slice(res.AssetFlows, func(i, j int) bool { return res.AssetFlows[i].Asset < res.AssetFlows[j].Asset })
	if len(inWallets) >= 5 {
		res.Signals = append(res.Signals, signal("FAN_IN", .92, fmt.Sprintf("检测到 %d 个直接入账对手方", len(inWallets)), map[string]any{"counterparties": len(inWallets)}, evidence(moves, "in")))
	}
	if len(outWallets) >= 5 {
		res.Signals = append(res.Signals, signal("FAN_OUT", .92, fmt.Sprintf("检测到 %d 个直接出账对手方", len(outWallets)), map[string]any{"counterparties": len(outWallets)}, evidence(moves, "out")))
	}
	maxOutShare := largestOutboundShare(moves)
	if len(inWallets) >= 5 && maxOutShare >= .5 {
		res.Signals = append(res.Signals, signal("CONSOLIDATION", .82, "多来源资金集中流向主要出口", map[string]any{"inbound_counterparties": len(inWallets), "largest_outbound_share": maxOutShare}, evidence(moves, "")))
	}
	if len(outWallets) >= 5 && maxOutShare > 0 && maxOutShare <= .4 {
		res.Signals = append(res.Signals, signal("DISTRIBUTION", .82, "出账分散至多个直接对手方", map[string]any{"outbound_counterparties": len(outWallets), "largest_outbound_share": maxOutShare}, evidence(moves, "out")))
	}
	if count, refs := rapidForward(moves); count > 0 {
		res.Signals = append(res.Signals, signal("RAPID_FORWARD", .78, fmt.Sprintf("识别到 %d 组 24 小时内同资产近额转移", count), map[string]any{"matches": count, "window_hours": 24, "amount_ratio_min": .8, "amount_ratio_max": 1.2}, refs))
	}
	for address, w := range wallets {
		score := float64(len(w.txs)) / 20 * .5
		if score > .5 {
			score = .5
		}
		if len(w.directions) == 2 {
			score += .25
		}
		share := float64(len(w.txs)) / float64(max(1, len(moves)))
		if share > 1 {
			share = 1
		}
		score += share * .2
		if score < .2 {
			continue
		}
		directions := keys(w.directions)
		refs := keys(w.evidence)
		if len(refs) > 5 {
			refs = refs[:5]
		}
		res.RelatedWallets = append(res.RelatedWallets, RelatedWallet{Address: address, Score: round(score), Transactions: len(w.txs), Directions: directions, Evidence: refs, RelationKind: "DIRECT_COUNTERPARTY_HEURISTIC", EvidenceLevel: "INFERRED"})
	}
	sort.Slice(res.RelatedWallets, func(i, j int) bool { return res.RelatedWallets[i].Score > res.RelatedWallets[j].Score })
	if len(res.RelatedWallets) > 20 {
		res.RelatedWallets = res.RelatedWallets[:20]
	}
	return res
}

func emptyResult(chain, subject string) Result {
	return Result{Chain: chain, Subject: subject, Signals: []Signal{}, AssetFlows: []AssetFlow{}, RelatedWallets: []RelatedWallet{}, ProjectionVersion: ProjectionVersion, AlgorithmVersion: AlgorithmVersion, DataVersion: "", EvidenceBoundary: "Signals are deterministic summaries of indexed one-hop token transfers. Related-wallet scores are heuristic, not identity or common-control findings. NET_FLOW_ONLY is not realized profit without acquisition cost and historical prices.", Limitations: []string{"Peel-chain confirmation requires multi-hop tracing.", "Exposure requires a versioned label source.", "Realized profit requires price and cost-basis data."}}
}

func signal(kind string, confidence float64, summary string, metrics map[string]any, refs []string) Signal {
	return Signal{Type: kind, Confidence: confidence, Summary: summary, Metrics: metrics, Evidence: refs, Nature: "DETERMINISTIC_PATTERN"}
}
func largestOutboundShare(moves []movement) float64 {
	total, byWallet := decimal.Zero, map[string]decimal.Decimal{}
	for _, m := range moves {
		if m.direction == "out" {
			total = total.Add(m.amount)
			byWallet[m.counterparty] = byWallet[m.counterparty].Add(m.amount)
		}
	}
	if total.IsZero() {
		return 0
	}
	maxShare := 0.0
	for _, amount := range byWallet {
		share, _ := amount.Div(total).Float64()
		if share > maxShare {
			maxShare = share
		}
	}
	return round(maxShare)
}
func rapidForward(moves []movement) (int, []string) {
	queues := map[string][]movement{}
	count := 0
	refs := []string{}
	for _, m := range moves {
		if m.at.IsZero() {
			continue
		}
		if m.direction == "in" {
			queues[m.asset] = append(queues[m.asset], m)
			continue
		}
		q := queues[m.asset]
		cutoff := m.at.Add(-24 * time.Hour)
		first := 0
		for first < len(q) && q[first].at.Before(cutoff) {
			first++
		}
		q = q[first:]
		for i, incoming := range q {
			if incoming.amount.IsZero() {
				continue
			}
			ratio, _ := m.amount.Div(incoming.amount).Float64()
			if ratio >= .8 && ratio <= 1.2 {
				count++
				refs = appendUnique(refs, incoming.tx, m.tx)
				q = append(q[:i], q[i+1:]...)
				break
			}
		}
		queues[m.asset] = q
	}
	if len(refs) > 10 {
		refs = refs[:10]
	}
	return count, refs
}
func evidence(moves []movement, direction string) []string {
	refs := []string{}
	for _, m := range moves {
		if direction == "" || m.direction == direction {
			refs = appendUnique(refs, m.tx)
			if len(refs) == 10 {
				break
			}
		}
	}
	return refs
}
func appendUnique(values []string, candidates ...string) []string {
	for _, c := range candidates {
		if c == "" {
			continue
		}
		found := false
		for _, v := range values {
			if v == c {
				found = true
				break
			}
		}
		if !found {
			values = append(values, c)
		}
	}
	return values
}
func keys(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for v := range values {
		if v != "" {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}
func round(v float64) float64 { return float64(int(v*1000+.5)) / 1000 }
func text(v any) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(v))
}
func timestamp(v any) time.Time {
	switch x := v.(type) {
	case time.Time:
		return x
	case *time.Time:
		if x != nil {
			return *x
		}
	case string:
		t, _ := time.Parse(time.RFC3339Nano, x)
		return t
	}
	return time.Time{}
}
