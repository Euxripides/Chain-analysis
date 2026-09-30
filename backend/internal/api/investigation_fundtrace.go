package api

import (
	"context"
	"fmt"
	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
	"github.com/blockchain-platform/blockchain-platform/internal/fundtrace"
	"github.com/blockchain-platform/blockchain-platform/internal/method"
	"github.com/blockchain-platform/blockchain-platform/internal/query"
	"github.com/shopspring/decimal"
	"math/big"
	"regexp"
	"strings"
	"time"
)

func (s *Server) investigationTrace(ctx context.Context, q investigationRequest) (any, map[string]any, error) {
	if q.TokenContract == "" {
		return nil, nil, fmt.Errorf("trace_funds requires the exact ERC20 token contract")
	}
	h, e := chain.ParseHash(q.SourceTx)
	if e != nil {
		return nil, nil, e
	}
	receipt, e := s.deps.ChainRead.Receipt(ctx, q.Chain, h, investigationOptions())
	if e != nil {
		return nil, nil, e
	}
	if receipt == nil || receipt.BlockNumber == nil || receipt.Status == nil || *receipt.Status != 1 {
		return nil, nil, fmt.Errorf("successful confirmed source transaction is required")
	}
	matches := []chainread.Log{}
	for _, log := range receipt.Logs {
		if strings.EqualFold(log.Address.String(), q.TokenContract) && len(log.Topics) == 3 && strings.EqualFold(log.Topics[0], method.Topic0Of("Transfer(address,address,uint256)")) && (q.LogIndex == nil || *q.LogIndex == log.LogIndex) {
			matches = append(matches, log)
		}
	}
	if len(matches) != 1 {
		return nil, nil, fmt.Errorf("source transaction must have exactly one matching Transfer; specify log_index to disambiguate")
	}
	log := matches[0]
	bytes, e := chain.HexToBytes(log.Data)
	if e != nil || len(bytes) != 32 {
		return nil, nil, fmt.Errorf("invalid source Transfer amount")
	}
	amount := new(big.Int).SetBytes(bytes)
	if amount.Sign() == 0 {
		return nil, nil, fmt.Errorf("zero source transfer")
	}
	block := *receipt.BlockNumber
	if block == 0 {
		return nil, nil, fmt.Errorf("source block must have a predecessor")
	}
	sourceBlock, e := s.deps.ChainRead.Block(ctx, q.Chain, chain.AtBlock(block), false, investigationOptions())
	if e != nil || sourceBlock == nil {
		return nil, nil, fmt.Errorf("source block unavailable")
	}
	point := chainread.StatePoint{Block: block, Hash: sourceBlock.Hash, Time: time.Unix(int64(sourceBlock.Timestamp), 0).UTC(), Ref: chain.AtBlock(block)}
	decimals, e := s.probeContract(ctx, q.Chain, q.TokenContract, "decimals()", nil, point)
	if e != nil {
		return nil, nil, e
	}
	d, e := chainread.DecodeUint8(decimals)
	if e != nil {
		return nil, nil, e
	}
	seed := amount
	if q.Amount != "" {
		if len(q.Amount) > 335 || !regexp.MustCompile(`^(0|[1-9][0-9]{0,77})(\.[0-9]{1,255})?$`).MatchString(q.Amount) {
			return nil, nil, fmt.Errorf("amount must be a bounded plain decimal string without exponent")
		}
		v, e := decimal.NewFromString(q.Amount)
		if e != nil || v.Sign() <= 0 {
			return nil, nil, fmt.Errorf("amount must be a positive decimal string")
		}
		raw := v.Shift(int32(d))
		if !raw.Equal(raw.Truncate(0)) {
			return nil, nil, fmt.Errorf("amount exceeds token precision")
		}
		seed = raw.BigInt()
		if seed.Cmp(amount) > 0 {
			return nil, nil, fmt.Errorf("amount exceeds the selected source transfer")
		}
	}
	if q.MaxHops == 0 {
		q.MaxHops = 3
	}
	if q.MaxHops < 1 || q.MaxHops > 5 {
		return nil, nil, fmt.Errorf("max_hops must be 1-5")
	}
	if q.TimeWindow == 0 {
		q.TimeWindow = 7 * 86400
	}
	if q.TimeWindow < 1 || q.TimeWindow > 366*86400 {
		return nil, nil, fmt.Errorf("time_window must be 1-31622400 seconds")
	}
	if q.MaxNodes == 0 {
		q.MaxNodes = 50
	}
	if q.MaxNodes < 2 || q.MaxNodes > 100 {
		return nil, nil, fmt.Errorf("max_nodes must be 2-100")
	}
	if q.MaxRecords == 0 {
		q.MaxRecords = 5000
	}
	if q.MaxRecords < 1 || q.MaxRecords > 20000 {
		return nil, nil, fmt.Errorf("max_records must be 1-20000")
	}
	head, e := s.deps.ChainRead.Block(ctx, q.Chain, chain.Latest(), false, investigationOptions())
	if e != nil {
		return nil, nil, e
	}
	endTime := point.Time.Add(time.Duration(q.TimeWindow) * time.Second)
	var endpoint chainread.StatePoint
	if endTime.Unix() > int64(head.Timestamp) {
		endpoint, e = s.deps.ChainRead.ResolveState(ctx, q.Chain, chain.Latest(), nil, investigationOptions())
	} else {
		endpoint, e = s.deps.ChainRead.ResolveState(ctx, q.Chain, chain.Latest(), &endTime, investigationOptions())
	}
	if e != nil {
		return nil, nil, e
	}
	from := topicAddress(log.Topics[1])
	to := topicAddress(log.Topics[2])
	if from == to {
		return nil, nil, fmt.Errorf("source cannot be a self transfer")
	}
	nodes := []string{to}
	seen := map[string]bool{to: true}
	ledger := map[string]query.LedgerTransfer{}
	truncated := false
	reasons := []string{}
	for hop := 0; hop <= q.MaxHops && len(nodes) > 0; hop++ {
		rows, more, e := s.query.InvestigationLedger(ctx, q.Chain, q.TokenContract, nodes, block, endpoint.Block, q.MaxRecords)
		if e != nil {
			return nil, nil, e
		}
		if more {
			truncated = true
			reasons = append(reasons, "record budget reached")
		}
		front := map[string]bool{}
		for _, a := range nodes {
			front[a] = true
		}
		next := []string{}
		for _, tx := range rows {
			key := fmt.Sprintf("%s:%d", tx.Hash, tx.Index)
			if len(ledger) >= q.MaxRecords {
				truncated = true
				continue
			}
			ledger[key] = tx
			if front[tx.From] && !seen[tx.To] && tx.To != "0x"+strings.Repeat("0", 40) {
				if len(seen) < q.MaxNodes && hop < q.MaxHops {
					seen[tx.To] = true
					next = append(next, tx.To)
				} else {
					truncated = true
				}
			}
		}
		nodes = next
	}
	seedTx := query.LedgerTransfer{Hash: q.SourceTx, Index: log.LogIndex, Block: block, From: from, To: to, Token: q.TokenContract, Raw: amount.String(), Amount: decimal.NewFromBigInt(amount, -int32(d)).String(), Time: point.Time}
	ledger[fmt.Sprintf("%s:%d", q.SourceTx, log.LogIndex)] = seedTx
	transfers := []query.LedgerTransfer{}
	accounts := map[string]bool{}
	for _, tx := range ledger {
		transfers = append(transfers, tx)
		accounts[tx.From] = true
		accounts[tx.To] = true
	}
	if len(accounts) > q.MaxNodes*2 {
		return nil, nil, fmt.Errorf("too many counterparties for archive baselines; narrow time_window")
	}
	baselines := map[string]string{}
	before := point
	before.Block = block - 1
	before.Ref = chain.AtBlock(block - 1)
	for address := range accounts {
		if address == "0x"+strings.Repeat("0", 40) {
			baselines[address] = "0"
			continue
		}
		raw, e := s.probeContract(ctx, q.Chain, q.TokenContract, "balanceOf(address)", []any{address}, before)
		if e != nil {
			return nil, nil, e
		}
		if len(raw) != 32 {
			return nil, nil, fmt.Errorf("invalid archive balanceOf baseline")
		}
		baselines[address] = new(big.Int).SetBytes(raw).String()
	}
	models := []fundtrace.Result{}
	for _, name := range []string{"proportional", "fifo"} {
		result, e := fundtrace.Allocate(name, transfers, baselines, q.SourceTx, log.LogIndex, seed.String(), q.MaxHops)
		if e != nil {
			return nil, nil, e
		}
		models = append(models, result)
		if len(result.Anomalies) > 0 {
			truncated = true
			reasons = append(reasons, "ledger has unexplained balance deficits")
		}
	}
	addresses := []string{}
	for a := range seen {
		addresses = append(addresses, a)
	}
	meta := s.actionCoverageMeta(ctx, q.Chain, addresses, []string{"token_transfers"})
	if coverage, ok := meta["coverage"].(map[string]any); ok {
		if coverage["complete"] != true {
			truncated = true
			reasons = append(reasons, "indexed history coverage is incomplete")
		}
		coverage["complete"] = !truncated
	}
	return map[string]any{"chain": q.Chain, "token_contract": q.TokenContract, "decimals": d, "source_transfer": seedTx, "seed_amount_raw": seed.String(), "seed_amount": decimal.NewFromBigInt(seed, -int32(d)).String(), "baseline_block": block - 1, "baseline_balances_raw": baselines, "data_cutoff_block": endpoint.Block, "models": models, "evidence": transfers, "complete": !truncated, "limitations": reasons, "interpretation": "Two accounting hypotheses over indexed ERC20 transfers; neither proves identity of fungible units", "max_hops": q.MaxHops}, meta, nil
}
func topicAddress(topic string) string {
	if len(topic) != 66 {
		return ""
	}
	return "0x" + strings.ToLower(topic[26:])
}
