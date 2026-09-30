package download

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/ingest"
	"github.com/blockchain-platform/blockchain-platform/internal/solana"
	"github.com/blockchain-platform/blockchain-platform/internal/tron"
)

// rowBatch 一页标准化后的类型化批次（供同步 Ingest 与异步 Pipeline 共用）。
type rowBatch struct {
	txs       []ingest.TxRow
	transfers []ingest.TokenTransferRow
	nfts      []ingest.NftTransferRow
	logRows   []ingest.LogRow
	// solanaLogs 是 Solana 程序日志的原生扩展表行（blockchain.solana_program_logs）。
	// 与 logRows 双写：通用读路径仍走 blockchain.logs，原生字段走扩展表，
	// 避免为了一条链把查询层分叉成两个表（开发文档 §8.4 的落地取舍）。
	solanaLogs []ingest.SolanaProgramLogRow
	bals       []ingest.BalanceRow
	internals  []ingest.InternalTransferRow
	acts       []ingest.ActivityRow
	minB       int64
	maxB       int64
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
	// Solana 的 token_transfers / internal_transactions 由 Provider 解码成与 Ankr 同构的行，
	// 这里只做类型化与地址活动投影（ADR-023 D4/D10/D11）。
	if strings.EqualFold(chain, "solana") {
		providerName := sc.sourceProviderForDataset(chain, dataset)
		switch dataset {
		case "transactions":
			// Solana 的通用列表字段与原生扩展字段（fee_raw/signers/account_keys/program_ids/
			// source_*）由 solana.NormalizeSolanaTransaction 一次填齐。
			// 这里**不调用** resolveMethod：Solana 没有 EVM ABI，跑 4 字节选择器解析只会产出噪声。
			b.txs = make([]ingest.TxRow, 0, len(rows))
			b.acts = make([]ingest.ActivityRow, 0, len(rows))
			for _, raw := range rows {
				r, err := solana.NormalizeSolanaTransaction(raw, providerName)
				if err != nil {
					return b, fmt.Errorf("Solana transaction normalization failed: %w", err)
				}
				trackBlocks(&b.minB, &b.maxB, int64(r.BlockNumber))
				b.txs = append(b.txs, r)
				b.acts = append(b.acts, ingest.ActivityRow{Chain: chain, Address: address,
					Direction: "observed", ActivityType: "solana_signature", Timestamp: r.Timestamp,
					BlockNumber: r.BlockNumber, TxHash: r.TxHash, Dataset: dataset, Provider: providerName})
			}
			return b, nil
		case "token_transfers":
			b.transfers = make([]ingest.TokenTransferRow, 0, len(rows))
			b.acts = make([]ingest.ActivityRow, 0, len(rows))
			for _, raw := range rows {
				r, err := solana.NormalizeTokenTransferForChain(raw, chain, providerName)
				if err != nil {
					return b, fmt.Errorf("Solana token transfer normalization failed: %w", err)
				}
				trackBlocks(&b.minB, &b.maxB, int64(r.BlockNumber))
				b.transfers = append(b.transfers, r)
				if r.From != address && r.To != address {
					continue
				}
				dir, cp := directionOf(address, r.From, r.To)
				// 主体视角的代币账户：转出看 from ATA，转入看 to ATA（self 取转出侧并注明）。
				ownAccount, counterpartyAccount := r.FromTokenAccount, r.ToTokenAccount
				if dir == "in" {
					ownAccount, counterpartyAccount = r.ToTokenAccount, r.FromTokenAccount
				}
				b.acts = append(b.acts, ingest.ActivityRow{
					Chain: chain, Address: address, Counterparty: cp, Direction: dir,
					ActivityType: "transfer", Timestamp: r.Timestamp, BlockNumber: r.BlockNumber,
					TxHash: r.TxHash, LogIndex: r.LogIndex, TokenAddress: r.TokenAddress,
					TokenSymbol: r.TokenSymbol, TokenDecimals: r.TokenDecimals,
					AmountRaw: r.AmountRaw, Amount: r.Amount, Dataset: dataset, Provider: providerName,
					TokenAccount: ownAccount, CounterpartyTokenAccount: counterpartyAccount,
					FeeAmountRaw: r.FeeAmountRaw,
				})
			}
			return b, nil
		case "nft_transfers":
			// 经典 NFT：tokenId 留空，元数据没有来源就留空（ADR-024 D3）。
			for _, raw := range rows {
				r, err := solana.NormalizeNftTransfer(raw, providerName)
				if err != nil {
					return b, fmt.Errorf("Solana NFT normalization failed: %w", err)
				}
				trackBlocks(&b.minB, &b.maxB, int64(r.BlockNumber))
				b.nfts = append(b.nfts, r)
			}
			return b, nil
		case "balances":
			// 快照行：snapshotBlock 来自 Provider 实测的 getSlot 槽位（ADR-024 D2）。
			for _, raw := range rows {
				r, err := solana.NormalizeBalance(raw, providerName)
				if err != nil {
					return b, fmt.Errorf("Solana balance normalization failed: %w", err)
				}
				b.bals = append(b.bals, r)
			}
			return b, nil
		case "logs":
			// 程序日志是排障证据而不是资金流：只落 logs，不写地址活动。
			for _, raw := range rows {
				r, err := solana.NormalizeProgramLog(raw, providerName)
				if err != nil {
					return b, fmt.Errorf("Solana program log normalization failed: %w", err)
				}
				trackBlocks(&b.minB, &b.maxB, int64(r.BlockNumber))
				b.logRows = append(b.logRows, r)
				// 原生扩展行失败不影响通用行：扩展表是证据增强，不是主键来源。
				if native, nerr := solana.NormalizeSolanaProgramLog(raw, providerName); nerr == nil {
					b.solanaLogs = append(b.solanaLogs, native)
				} else {
					sc.log.Warn("Solana native program log row skipped", "err", nerr)
				}
			}
			return b, nil
		case "internal_transactions":
			// CPI 节点是程序调用：只落 internal_calls，不写地址活动（ADR-023 D11）。
			for _, raw := range rows {
				r, err := solana.NormalizeInternal(raw, providerName)
				if err != nil {
					return b, fmt.Errorf("Solana internal normalization failed: %w", err)
				}
				trackBlocks(&b.minB, &b.maxB, int64(r.BlockNumber))
				b.internals = append(b.internals, r)
				if r.AssetType == "SOL" && !r.Failed && r.ValueRaw != "0" && (r.From == address || r.To == address) {
					dir, cp := directionOf(address, r.From, r.To)
					b.acts = append(b.acts, ingest.ActivityRow{Chain: chain, Address: address, Counterparty: cp,
						Direction: dir, ActivityType: "native_transfer", Timestamp: r.Timestamp, BlockNumber: r.BlockNumber,
						TxHash: r.TxHash, LogIndex: r.CallIndex, AmountRaw: r.ValueRaw, Amount: tronAmount(r.ValueRaw),
						TokenSymbol: "SOL", TokenDecimals: 9, Dataset: dataset, Provider: providerName})
				}
			}
			return b, nil
		}
	}
	// 非 TRON 分支：Provider 名随链决定（EVM=ankr，Solana=solana），
	// 它同时决定 raw 路径、source_provider 列与 activity 的 provider 字段。
	providerName := sc.sourceProviderForDataset(chain, dataset)
	switch dataset {
	case "transactions":
		b.txs = make([]ingest.TxRow, 0, len(rows))
		b.acts = make([]ingest.ActivityRow, 0, len(rows))
		for _, raw := range rows {
			r, err := ingest.NormalizeTransaction(raw, chain, providerName)
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
				TxHash: r.TxHash, Dataset: "transactions", Provider: providerName,
			})
		}
		return b, nil
	case "token_transfers":
		b.transfers = make([]ingest.TokenTransferRow, 0, len(rows))
		b.acts = make([]ingest.ActivityRow, 0, len(rows))
		for _, raw := range rows {
			r, err := ingest.NormalizeTokenTransfer(raw, chain, providerName)
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
				Dataset: "token_transfers", Provider: providerName,
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
			r, err := ingest.NormalizeNftTransfer(raw, chain, providerName)
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
			r, err := ingest.NormalizeLog(raw, chain, providerName)
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
			r, err := ingest.NormalizeBalance(raw, chain, address, snapshotBlock, providerName)
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
