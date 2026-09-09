package download

import (
	"context"
	"encoding/json"
	"math/big"

	"github.com/blockchain-platform/blockchain-platform/internal/ingest"
	"github.com/blockchain-platform/blockchain-platform/internal/tron"
)

// rowBatch 一页标准化后的类型化批次（供同步 Ingest 与异步 Pipeline 共用）。
type rowBatch struct {
	txs       []ingest.TxRow
	transfers []ingest.TokenTransferRow
	nfts      []ingest.NftTransferRow
	logRows   []ingest.LogRow
	bals      []ingest.BalanceRow
	internals []ingest.InternalTransferRow
	acts      []ingest.ActivityRow
	minB      int64
	maxB      int64
}

// buildRows 标准化一页 raw rows（§50 Parse Plane）：
// 只做本地归一化 + 本地 Method/Event 解析 + 元数据观察抽取，绝不访问外部网络。
// Metadata/Activity 观察在 Parse 之后异步消费（§51/§52/§53）。
func (sc *Scheduler) buildRows(ctx context.Context, dataset string, rows []json.RawMessage, chain, address string, snapshotBlock uint64) (rowBatch, error) {
	var b rowBatch
	if chain == "tron" {
		providerName := sc.sourceProviderForDataset(chain, dataset)
		switch dataset {
		case "transactions":
			for _, raw := range rows {
				r, err := tron.NormalizeTransaction(raw)
				if err != nil {
					sc.log.Warn("TRON row normalization skipped", "dataset", dataset, "err", err)
					continue
				}
				trackBlocks(&b.minB, &b.maxB, int64(r.BlockNumber))
				b.txs = append(b.txs, r)
				if r.Status == 1 {
					dir, cp := directionOf(address, r.From, r.To)
					b.acts = append(b.acts, ingest.ActivityRow{Chain: chain, Address: address, Counterparty: cp, Direction: dir, ActivityType: "transaction", Timestamp: r.Timestamp, BlockNumber: r.BlockNumber, TxHash: r.TxHash, AmountRaw: r.ValueRaw, Amount: tronAmount(r.ValueRaw), TokenSymbol: "TRX", TokenDecimals: 6, Dataset: dataset, Provider: providerName})
				}
			}
		case "token_transfers":
			metadataSeen := map[string]bool{}
			for _, raw := range rows {
				r, err := tron.NormalizeTRC20(raw)
				if err != nil {
					sc.log.Warn("TRON row normalization skipped", "dataset", dataset, "err", err)
					continue
				}
				trackBlocks(&b.minB, &b.maxB, int64(r.BlockNumber))
				b.transfers = append(b.transfers, r)
				if r.TokenAddress != "" && !metadataSeen[r.TokenAddress] {
					metadataSeen[r.TokenAddress] = true
					if err := sc.store.UpsertTokenMetadata(ctx, chain, r.TokenAddress, "", r.TokenSymbol, int(r.TokenDecimals), "", "TRC20", providerName); err != nil {
						sc.log.Warn("TRON token metadata cache write failed", "token", r.TokenAddress, "err", err)
					}
				}
				dir, cp := directionOf(address, r.From, r.To)
				b.acts = append(b.acts, ingest.ActivityRow{Chain: chain, Address: address, Counterparty: cp, Direction: dir, ActivityType: "transfer", Timestamp: r.Timestamp, BlockNumber: r.BlockNumber, TxHash: r.TxHash, LogIndex: r.LogIndex, TokenAddress: r.TokenAddress, TokenSymbol: r.TokenSymbol, TokenDecimals: r.TokenDecimals, AmountRaw: r.AmountRaw, Amount: r.Amount, Dataset: dataset, Provider: providerName})
			}
		case "internal_transactions":
			for _, raw := range rows {
				r, err := tron.NormalizeInternal(raw)
				if err != nil {
					sc.log.Warn("TRON row normalization skipped", "dataset", dataset, "err", err)
					continue
				}
				trackBlocks(&b.minB, &b.maxB, int64(r.BlockNumber))
				b.internals = append(b.internals, r)
				if !r.Rejected {
					dir, cp := directionOf(address, r.From, r.To)
					b.acts = append(b.acts, ingest.ActivityRow{Chain: chain, Address: address, Counterparty: cp, Direction: dir, ActivityType: "internal_transfer", Timestamp: r.Timestamp, BlockNumber: r.BlockNumber, TxHash: r.TxHash, LogIndex: r.CallIndex, AmountRaw: r.ValueRaw, Amount: tronAmount(r.ValueRaw), TokenSymbol: "TRX", TokenDecimals: 6, Dataset: dataset, Provider: providerName})
				}
			}
		}
		return b, nil
	}
	switch dataset {
	case "transactions":
		b.txs = make([]ingest.TxRow, 0, len(rows))
		b.acts = make([]ingest.ActivityRow, 0, len(rows))
		for _, raw := range rows {
			r, err := ingest.NormalizeTransaction(raw, chain, "ankr")
			if err != nil {
				return b, err
			}
			// 只读内存解析；解析不到写 unknown 并投递异步补全，绝不阻塞主下载链路。
			sc.resolveMethod(ctx, chain, &r)
			trackBlocks(&b.minB, &b.maxB, int64(r.BlockNumber))
			b.txs = append(b.txs, r)
			dir, cp := directionOf(address, r.From, r.To)
			b.acts = append(b.acts, ingest.ActivityRow{
				Chain: chain, Address: address, Counterparty: cp, Direction: dir,
				ActivityType: "transaction", Timestamp: r.Timestamp, BlockNumber: r.BlockNumber,
				TxHash: r.TxHash, Dataset: "transactions", Provider: "ankr",
			})
		}
		return b, nil
	case "token_transfers":
		b.transfers = make([]ingest.TokenTransferRow, 0, len(rows))
		b.acts = make([]ingest.ActivityRow, 0, len(rows))
		for _, raw := range rows {
			r, err := ingest.NormalizeTokenTransfer(raw, chain, "ankr")
			if err != nil {
				return b, err
			}
			trackBlocks(&b.minB, &b.maxB, int64(r.BlockNumber))
			b.transfers = append(b.transfers, r)
			dir, cp := directionOf(address, r.From, r.To)
			b.acts = append(b.acts, ingest.ActivityRow{
				Chain: chain, Address: address, Counterparty: cp, Direction: dir,
				ActivityType: "transfer", Timestamp: r.Timestamp, BlockNumber: r.BlockNumber,
				TxHash: r.TxHash, LogIndex: r.LogIndex, TokenAddress: r.TokenAddress,
				TokenSymbol: r.TokenSymbol, TokenDecimals: r.TokenDecimals, AmountRaw: r.AmountRaw, Amount: r.Amount,
				Dataset: "token_transfers", Provider: "ankr",
			})
		}
		// Enrichment 批量去重、best-effort：绝不让已完成页失败（§52）。
		if sc.tokenMeta != nil {
			if err := sc.tokenMeta.ProcessPage(ctx, chain, "ankr_token_transfer", rows); err != nil {
				sc.log.Warn("token metadata page enrichment failed", "err", err)
			}
		}
		return b, nil
	case "nft_transfers":
		b.nfts = make([]ingest.NftTransferRow, 0, len(rows))
		for _, raw := range rows {
			r, err := ingest.NormalizeNftTransfer(raw, chain, "ankr")
			if err != nil {
				return b, err
			}
			trackBlocks(&b.minB, &b.maxB, int64(r.BlockNumber))
			b.nfts = append(b.nfts, r)
		}
		return b, nil
	case "logs":
		b.logRows = make([]ingest.LogRow, 0, len(rows))
		for _, raw := range rows {
			r, err := ingest.NormalizeLog(raw, chain, "ankr")
			if err != nil {
				return b, err
			}
			sc.resolveEvent(&r)
			trackBlocks(&b.minB, &b.maxB, int64(r.BlockNumber))
			b.logRows = append(b.logRows, r)
		}
		return b, nil
	case "balances":
		b.bals = make([]ingest.BalanceRow, 0, len(rows))
		for _, raw := range rows {
			r, err := ingest.NormalizeBalance(raw, chain, address, snapshotBlock, "ankr")
			if err != nil {
				return b, err
			}
			b.bals = append(b.bals, r)
		}
		return b, nil
	}
	return b, nil
}

func tronAmount(raw string) *big.Int {
	n, ok := new(big.Int).SetString(raw, 10)
	if !ok {
		return big.NewInt(0)
	}
	return n
}
