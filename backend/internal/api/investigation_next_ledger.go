package api

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
	"sync"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
	"github.com/blockchain-platform/blockchain-platform/internal/dataset"
	"github.com/blockchain-platform/blockchain-platform/internal/fundtrace"
	"github.com/blockchain-platform/blockchain-platform/internal/method"
	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
	"github.com/blockchain-platform/blockchain-platform/internal/query"
	"github.com/shopspring/decimal"
	"golang.org/x/sync/errgroup"
)

func nextRaw(s string) (*big.Int, error) {
	if len(s) == 0 || len(s) > 78 {
		return nil, fmt.Errorf("invalid uint256 amount")
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return nil, fmt.Errorf("amount_raw must be a decimal integer string")
		}
	}
	v, ok := new(big.Int).SetString(s, 10)
	if !ok || v.BitLen() > 256 {
		return nil, fmt.Errorf("invalid uint256 amount")
	}
	return v, nil
}
func nextHash(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (s *Server) nextBlockGuard(ctx context.Context, slug string, point chainread.StatePoint) error {
	opt := investigationOptions()
	opt.Consistency = rpc.ConsistencyForceRefresh
	b, e := s.deps.ChainRead.Block(ctx, slug, chain.AtBlock(point.Block), false, opt)
	if e != nil {
		return e
	}
	if b == nil || !strings.EqualFold(b.Hash, point.Hash) {
		return summaryError(409, "SNAPSHOT_REORG", "Snapshot block hash changed; start a new analysis")
	}
	return nil
}

func nextCoverageComplete(c *dataset.CoveragePreviewResponse) bool {
	if c == nil || len(c.Items) == 0 {
		return false
	}
	for _, item := range c.Items {
		if len(item.Gaps) != 0 || item.CoveredBlocks != item.TotalBlocks || item.TotalBlocks <= 0 {
			return false
		}
	}
	return true
}
func (s *Server) nextCoverage(ctx context.Context, q nextRequest, start, end uint64) (*dataset.CoveragePreviewResponse, error) {
	a, b := int64(start), int64(end)
	if start > uint64(^uint64(0)>>1) || end > uint64(^uint64(0)>>1) {
		return nil, fmt.Errorf("block out of supported range")
	}
	return s.svc.Preview(ctx, dataset.EnsureRequest{Chain: q.Chain, Subjects: []string{q.Address}, Datasets: []string{"token_transfers"}, Range: dataset.RangeSpec{Type: "block", FromBlock: &a, ToBlock: &b}})
}

type reconciliationInput struct {
	Opening, Closing chainread.BalanceSnapshot
	Ledger           []query.LedgerTransfer
	More             bool
	Coverage         *dataset.CoveragePreviewResponse
	Watermark        time.Time
}

func (s *Server) nextReconcile(ctx context.Context, q nextRequest) (any, error) {
	var in reconciliationInput
	cached, e := s.nextFrozen(ctx, &in)
	if e != nil {
		return nil, e
	}
	if !cached {
		point, e := s.nextPoint(ctx, q)
		if e != nil {
			return nil, e
		}
		if q.ToBlock != nil {
			point, e = s.nextPoint(ctx, nextRequest{Chain: q.Chain, Block: json.RawMessage(fmt.Sprintf("%d", *q.ToBlock))})
			if e != nil {
				return nil, e
			}
		}
		start := *q.FromBlock
		if start > point.Block {
			return nil, fmt.Errorf("from_block exceeds cutoff")
		}
		before, e := s.nextPoint(ctx, nextRequest{Chain: q.Chain, Block: json.RawMessage(fmt.Sprintf("%d", start-1))})
		if e != nil {
			return nil, e
		}
		holder, _ := chain.ParseAddress(q.Address)
		token, _ := chain.ParseAddress(q.TokenContract)
		in.Opening, e = s.deps.ChainRead.SnapshotBalance(ctx, q.Chain, holder, token, before, investigationOptions())
		if e != nil {
			return nil, e
		}
		in.Closing, e = s.deps.ChainRead.SnapshotBalance(ctx, q.Chain, holder, token, point, investigationOptions())
		if e != nil {
			return nil, e
		}
		in.Watermark = time.Now().UTC()
		in.Ledger, in.More, e = s.query.InvestigationLedgerAt(ctx, q.Chain, q.TokenContract, []string{q.Address}, start, point.Block, q.MaxRecords, in.Watermark)
		if e != nil {
			return nil, e
		}
		in.Coverage, e = s.nextCoverage(ctx, q, start, point.Block)
		if e != nil {
			return nil, e
		}
		if e = s.nextBlockGuard(ctx, q.Chain, before); e != nil {
			return nil, e
		}
		if e = s.nextBlockGuard(ctx, q.Chain, point); e != nil {
			return nil, e
		}
		if e = s.nextSaveFrozen(ctx, in); e != nil {
			return nil, e
		}
	}
	opening, e := nextRaw(in.Opening.Raw)
	if e != nil {
		return nil, e
	}
	closing, e := nextRaw(in.Closing.Raw)
	if e != nil {
		return nil, e
	}
	incoming, outgoing := new(big.Int), new(big.Int)
	for _, tx := range in.Ledger {
		v, e := nextRaw(tx.Raw)
		if e != nil {
			return nil, e
		}
		if tx.To == q.Address {
			incoming.Add(incoming, v)
		}
		if tx.From == q.Address {
			outgoing.Add(outgoing, v)
		}
	}
	expected := new(big.Int).Sub(new(big.Int).Add(opening, incoming), outgoing)
	diff := new(big.Int).Sub(closing, expected)
	if cached {
		for _, p := range []chainread.StatePoint{in.Opening.StatePoint, in.Closing.StatePoint} {
			if e := s.nextBlockGuard(ctx, q.Chain, p); e != nil {
				return nil, e
			}
		}
	}
	complete := !in.More && nextCoverageComplete(in.Coverage)
	reasons := []string{}
	if in.More {
		reasons = append(reasons, "record budget reached; totals cover retained rows only")
	}
	if !complete {
		reasons = append(reasons, "indexed coverage incomplete")
	}
	if diff.Sign() != 0 {
		reasons = append(reasons, "balance mismatch: missing/incorrect records, rebases or nonstandard token effects require investigation")
	}
	amount := func(v *big.Int) any {
		if in.Closing.Decimals == nil {
			return nil
		}
		return decimal.NewFromBigInt(v, -int32(*in.Closing.Decimals)).String()
	}
	var repair any
	if q.Repair {
		if !complete {
			scope, e := s.nextTenant(ctx)
			if e != nil {
				return nil, e
			}
			a, b := int64(*q.FromBlock), int64(in.Closing.Block)
			repair, e = s.svc.Ensure(ctx, dataset.EnsureRequest{OrganizationID: scope.OrganizationID, WorkspaceID: scope.WorkspaceID, Source: "mcp_reconciliation", Mode: "balanced", Subjects: []string{q.Address}, Chain: q.Chain, Datasets: []string{"token_transfers"}, Range: dataset.RangeSpec{Type: "block", FromBlock: &a, ToBlock: &b}, ReusePolicy: "prefer_local"})
			if e != nil {
				return nil, e
			}
		} else {
			repair = map[string]any{"status": "no_coverage_gap", "reverification_required": diff.Sign() != 0}
		}
	}
	return map[string]any{"chain": q.Chain, "address": q.Address, "token_contract": q.TokenContract, "opening_balance": in.Opening, "closing_balance": in.Closing, "from_block": *q.FromBlock, "data_cutoff_block": in.Closing.Block, "data_cutoff_hash": in.Closing.Hash, "incoming_raw": incoming.String(), "outgoing_raw": outgoing.String(), "expected_balance_raw": expected.String(), "difference_raw": diff.String(), "difference": amount(diff), "amounts_complete": !in.More, "record_count": len(in.Ledger), "evidence": in.Ledger, "coverage": in.Coverage, "ledger_complete": complete, "balanced": diff.Sign() == 0, "reconciled": complete && diff.Sign() == 0, "repair": repair, "repair_requires_fresh_reconciliation": q.Repair, "limitations": reasons, "balance_source": "rpc_state", "model": "standard_erc20_transfer_ledger", "snapshot_id": nextHash(in), "source_ingested_before": in.Watermark}, nil
}

type jointInput struct {
	ExternalSenders map[string]bool        `json:"external_untagged_senders"`
	Chain           string                 `json:"chain"`
	Token           string                 `json:"token_contract"`
	Baseline        chainread.StatePoint   `json:"baseline"`
	Cutoff          chainread.StatePoint   `json:"cutoff"`
	Sources         []fundtrace.JointSeed  `json:"sources"`
	SourcePoints    []chainread.StatePoint `json:"source_points"`
	Ledger          []query.LedgerTransfer `json:"ledger"`
	Balances        map[string]string      `json:"baseline_balances_raw"`
	Decimals        *int                   `json:"decimals"`
	Complete        bool                   `json:"complete"`
	Limitations     []string               `json:"limitations"`
	Watermark       time.Time              `json:"ingested_before"`
}

func strictTopicAddress(topic string) (string, error) {
	if len(topic) != 66 || !strings.EqualFold(topic[:26], "0x"+strings.Repeat("0", 24)) {
		return "", fmt.Errorf("malformed indexed address")
	}
	a, e := chain.ParseAddress("0x" + topic[26:])
	return a.String(), e
}
func (s *Server) nextJointInput(ctx context.Context, q nextRequest) (jointInput, error) {
	in := jointInput{Chain: q.Chain, Token: q.TokenContract, Complete: true, Limitations: []string{}, Balances: map[string]string{}, Watermark: time.Now().UTC()}
	cutoff, e := s.nextPoint(ctx, q)
	if e != nil {
		return in, e
	}
	if q.ToBlock != nil {
		cutoff, e = s.nextPoint(ctx, nextRequest{Chain: q.Chain, Block: json.RawMessage(fmt.Sprintf("%d", *q.ToBlock))})
		if e != nil {
			return in, e
		}
	}
	in.Cutoff = cutoff
	seedRows := []query.LedgerTransfer{}
	arrivals := map[string]tracePosition{}
	first := uint64(^uint64(0))
	for _, source := range q.Sources {
		if e = ctx.Err(); e != nil {
			return in, e
		}
		h, _ := chain.ParseHash(source.TxHash)
		receipt, e := s.deps.ChainRead.Receipt(ctx, q.Chain, h, investigationOptions())
		if e != nil {
			return in, e
		}
		if receipt == nil || receipt.Status == nil || *receipt.Status != 1 || receipt.BlockNumber == nil || *receipt.BlockNumber == 0 {
			return in, fmt.Errorf("source requires a successful confirmed receipt")
		}
		b, e := s.deps.ChainRead.Block(ctx, q.Chain, chain.AtBlock(*receipt.BlockNumber), false, investigationOptions())
		if e != nil {
			return in, e
		}
		if e = validateTraceSource(receipt, b, h); e != nil {
			return in, e
		}
		logs := []chainread.Log{}
		for _, l := range receipt.Logs {
			if !l.Removed && l.BlockNumber == b.Number && l.TxHash == h && strings.EqualFold(l.Address.String(), q.TokenContract) && len(l.Topics) == 3 && strings.EqualFold(l.Topics[0], method.Topic0Of("Transfer(address,address,uint256)")) && (source.LogIndex == nil || *source.LogIndex == l.LogIndex) {
				logs = append(logs, l)
			}
		}
		if len(logs) != 1 {
			return in, fmt.Errorf("source %s requires an unambiguous Transfer log_index", source.SourceID)
		}
		l := logs[0]
		raw, e := chain.HexToBytes(l.Data)
		if e != nil || len(raw) != 32 {
			return in, fmt.Errorf("malformed source amount")
		}
		amount := new(big.Int).SetBytes(raw)
		from, e := strictTopicAddress(l.Topics[1])
		if e != nil {
			return in, e
		}
		to, e := strictTopicAddress(l.Topics[2])
		if e != nil {
			return in, e
		}
		if from == to || amount.Sign() == 0 {
			return in, fmt.Errorf("source must be nonzero and non-self")
		}
		tracked := cloneNext(amount)
		if source.AmountRaw != "" {
			tracked, e = nextRaw(source.AmountRaw)
			if e != nil || tracked.Sign() == 0 || tracked.Cmp(amount) > 0 {
				return in, fmt.Errorf("source amount_raw exceeds receipt or is invalid")
			}
		}
		if b.Number > cutoff.Block {
			return in, fmt.Errorf("source is after cutoff")
		}
		if b.Number < first {
			first = b.Number
		}
		point := chainread.StatePoint{Block: b.Number, Hash: b.Hash, Time: time.Unix(int64(b.Timestamp), 0).UTC(), Ref: chain.AtBlock(b.Number)}
		in.SourcePoints = append(in.SourcePoints, point)
		in.Sources = append(in.Sources, fundtrace.JointSeed{ID: source.SourceID, Hash: source.TxHash, Index: l.LogIndex, Raw: tracked.String()})
		seedRows = append(seedRows, query.LedgerTransfer{Hash: source.TxHash, Index: l.LogIndex, Block: b.Number, From: from, To: to, Token: q.TokenContract, Raw: amount.String(), Time: point.Time})
		pos := tracePosition{Block: b.Number, Index: l.LogIndex}
		old, ok := arrivals[to]
		if !ok || pos.Block < old.Block || (pos.Block == old.Block && pos.Index < old.Index) {
			arrivals[to] = pos
		}
	}
	in.Baseline, e = s.nextPoint(ctx, nextRequest{Chain: q.Chain, Block: json.RawMessage(fmt.Sprintf("%d", first-1))})
	if e != nil {
		return in, e
	}
	if raw, err := s.probeContract(ctx, q.Chain, q.TokenContract, "decimals()", nil, in.SourcePoints[0]); err == nil {
		if d, err := chainread.DecodeUint8(raw); err == nil {
			v := int(d)
			in.Decimals = &v
		}
	}
	nodes := []string{}
	seen := map[string]bool{}
	for a := range arrivals {
		seen[a] = true
		nodes = append(nodes, a)
	}
	if len(nodes) > q.MaxNodes {
		return in, fmt.Errorf("source recipients exceed max_nodes")
	}
	sort.Strings(nodes)
	ledger := map[string]query.LedgerTransfer{}
	for _, row := range seedRows {
		ledger[fmt.Sprintf("%s:%d", row.Hash, row.Index)] = row
	}
	for hop := 0; hop <= q.MaxHops && len(nodes) > 0; hop++ {
		rows, more, e := s.query.InvestigationLedgerAt(ctx, q.Chain, q.TokenContract, nodes, first, cutoff.Block, q.MaxRecords, in.Watermark)
		if e != nil {
			return in, e
		}
		if more {
			in.Complete = false
			in.Limitations = append(in.Limitations, "indexed record budget reached")
		}
		front := map[string]bool{}
		for _, a := range nodes {
			front[a] = true
		}
		next := []string{}
		for _, row := range rows {
			key := fmt.Sprintf("%s:%d", row.Hash, row.Index)
			if original, ok := ledger[key]; ok {
				if original.Raw != row.Raw || original.From != row.From || original.To != row.To || original.Block != row.Block {
					return in, summaryError(422, "SOURCE_LEDGER_CONFLICT", "Indexed source disagrees with receipt")
				}
			} else if len(ledger) >= q.MaxRecords {
				in.Complete = false
				continue
			} else {
				ledger[key] = row
			}
			if added, limited := extendTraceFrontier(row, front, seen, arrivals, q.MaxNodes, hop < q.MaxHops); added {
				next = append(next, row.To)
			} else if limited {
				in.Complete = false
				in.Limitations = append(in.Limitations, "node/hop frontier limited")
			}
		}
		sort.Strings(next)
		nodes = next
	}
	accounts := map[string]bool{}
	in.ExternalSenders = map[string]bool{}
	for _, tx := range ledger {
		in.Ledger = append(in.Ledger, tx)
		if seen[tx.From] {
			accounts[tx.From] = true
		} else {
			in.ExternalSenders[tx.From] = true
		}
	}
	if len(accounts) > q.MaxNodes*2 {
		return in, fmt.Errorf("archive baseline account budget exceeded")
	}
	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(4)
	for address := range accounts {
		a := address
		g.Go(func() error {
			v := "0"
			if a != "0x"+strings.Repeat("0", 40) {
				raw, e := s.probeContract(gctx, q.Chain, q.TokenContract, "balanceOf(address)", []any{a}, in.Baseline)
				if e != nil {
					return e
				}
				if len(raw) != 32 {
					return fmt.Errorf("invalid archive baseline")
				}
				v = new(big.Int).SetBytes(raw).String()
			}
			mu.Lock()
			in.Balances[a] = v
			mu.Unlock()
			return nil
		})
	}
	if e = g.Wait(); e != nil {
		return in, e
	}
	addresses := []string{}
	for a := range seen {
		addresses = append(addresses, a)
	}
	a, b := int64(first), int64(cutoff.Block)
	coverage, e := s.svc.Preview(ctx, dataset.EnsureRequest{Chain: q.Chain, Subjects: addresses, Datasets: []string{"token_transfers"}, Range: dataset.RangeSpec{Type: "block", FromBlock: &a, ToBlock: &b}})
	if e != nil {
		return in, e
	}
	if !nextCoverageComplete(coverage) {
		in.Complete = false
		in.Limitations = append(in.Limitations, "indexed history coverage incomplete")
	}
	sort.Slice(in.Ledger, func(i, j int) bool {
		a, b := in.Ledger[i], in.Ledger[j]
		if a.Block != b.Block {
			return a.Block < b.Block
		}
		if a.Index != b.Index {
			return a.Index < b.Index
		}
		return a.Hash < b.Hash
	})
	for _, p := range append(in.SourcePoints, in.Baseline, cutoff) {
		if e = s.nextBlockGuard(ctx, q.Chain, p); e != nil {
			return in, e
		}
	}
	return in, nil
}
func cloneNext(v *big.Int) *big.Int { return new(big.Int).Set(v) }
func (s *Server) nextJoint(ctx context.Context, q nextRequest) (any, error) {
	var in jointInput
	cached, e := s.nextFrozen(ctx, &in)
	if e != nil {
		return nil, e
	}
	if !cached {
		in, e = s.nextJointInput(ctx, q)
		if e != nil {
			return nil, e
		}
		if e = s.nextSaveFrozen(ctx, in); e != nil {
			return nil, e
		}
	}
	models := []fundtrace.JointResult{}
	if cached {
		for _, p := range append(in.SourcePoints, in.Baseline, in.Cutoff) {
			if e := s.nextBlockGuard(ctx, q.Chain, p); e != nil {
				return nil, e
			}
		}
	}
	complete := in.Complete
	for _, model := range traceModels(q.AttributionModel) {
		r, e := fundtrace.AllocateJoint(ctx, model, in.Ledger, in.Balances, in.Sources, q.MaxHops, in.ExternalSenders)
		if e != nil {
			var overlap *fundtrace.SourceOverlapError
			if errors.As(e, &overlap) {
				return nil, summaryError(422, "SOURCE_OVERLAP", overlap.Error())
			}
			return nil, e
		}
		if len(r.Anomalies) > 0 {
			complete = false
		}
		models = append(models, r)
	}
	total := new(big.Int)
	for _, seed := range in.Sources {
		v, _ := nextRaw(seed.Raw)
		total.Add(total, v)
	}
	var amount any
	if in.Decimals != nil {
		amount = decimal.NewFromBigInt(total, -int32(*in.Decimals)).String()
	}
	comparison := map[string]any{}
	if len(models) == 2 {
		for id, fifo := range models[0].Sources {
			other := models[1].Sources[id]
			comparison[id] = map[string]any{"same_remaining": nextHash(fifo.Remaining) == nextHash(other.Remaining), "fifo": fifo, "proportional": other}
		}
	}
	return map[string]any{"chain": q.Chain, "token_contract": q.TokenContract, "sources": in.Sources, "total_seed_amount_raw": total.String(), "total_seed_amount": amount, "decimals": in.Decimals, "models": models, "comparison": comparison, "evidence": in.Ledger, "baseline": in.Baseline, "baseline_balances_raw": in.Balances, "data_cutoff_block": in.Cutoff.Block, "data_cutoff_hash": in.Cutoff.Hash, "snapshot_id": nextHash(in), "ingested_before": in.Watermark, "complete": complete, "limitations": in.Limitations, "source_overlap_policy": "reject", "attribution_type": "inferred", "remaining_balance_meaning": "Modeled tagged balances at the retained ledger cutoff; incomplete history is not a current state balance"}, nil
}
