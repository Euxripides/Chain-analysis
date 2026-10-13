package api

import (
	"context"
	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/method"
	"math/big"
	"time"
)

func (s *Server) explorerRPCDetail(ctx context.Context, slug, hash string, receipt bool) (*method.TransactionDetail, error) {
	if s.deps.ChainRead == nil || slug == "tron" || slug == "solana" {
		return nil, method.ErrTransactionNotFound
	}
	h, _ := chain.ParseHash(hash)
	tx, err := s.deps.ChainRead.Transaction(ctx, slug, h, lookupRPCOptions(ctx))
	if err != nil {
		return nil, err
	}
	if tx == nil {
		return nil, method.ErrTransactionNotFound
	}
	decimals := 18
	if cfg, ok := s.deps.ChainRead.Chains().Get(slug); ok {
		decimals = cfg.Native.Decimals
	}
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	row := map[string]any{"chain": slug, "tx_hash": hash, "from_address": string(tx.From), "to_address": "", "value_raw": tx.ValueRaw, "value": new(big.Rat).SetFrac(tx.Value, scale).FloatString(decimals), "input": tx.Input, "nonce": tx.Nonce, "gas": tx.Gas, "gas_price": tx.GasPrice, "block_number": tx.BlockNumber, "block_hash": tx.BlockHash, "transaction_index": tx.TransactionIndex, "source_provider": "rpc", "status": nil, "raw_status": "已上链，回执待获取"}
	if tx.To != nil {
		row["to_address"] = string(*tx.To)
	}
	if tx.BlockNumber == nil {
		row["raw_status"] = "待确认"
	}
	detail, err := s.detail.DetailFromRow(ctx, slug, hash, row, method.DecodeLocalOnly)
	if err != nil {
		return nil, err
	}
	if receipt && tx.BlockNumber != nil {
		rec, e := s.deps.ChainRead.Receipt(ctx, slug, h, lookupRPCOptions(ctx))
		if e != nil {
			row["receipt_error"] = "回执暂不可用"
			return detail, nil
		}
		if rec != nil {
			row["status"] = rec.Status
			row["raw_status"] = "已上链"
			row["gas_used"] = rec.GasUsed
			row["effective_gas_price"] = rec.EffectiveGasPrice
			if rec.Status != nil {
				if *rec.Status == 1 {
					row["raw_status"] = "成功"
				} else {
					row["raw_status"] = "失败"
				}
			}
			logs := make([]map[string]any, 0, len(rec.Logs))
			for _, l := range rec.Logs {
				logs = append(logs, map[string]any{"address": string(l.Address), "contract_address": string(l.Address), "topics": l.Topics, "data": l.Data, "log_index": l.LogIndex, "tx_hash": hash, "block_number": l.BlockNumber, "removed": l.Removed})
			}
			detail.Logs = logs
		}
		block, e := s.deps.ChainRead.Block(ctx, slug, chain.AtBlock(*tx.BlockNumber), false, lookupRPCOptions(ctx))
		if e == nil && block != nil {
			row["timestamp"] = time.Unix(int64(block.Timestamp), 0).UTC()
		}
	}
	return detail, nil
}
