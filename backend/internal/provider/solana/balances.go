package solana

// 本文件实现 balances 数据集（ADR-024 D2）：按所有者做一次余额快照。
//
// 数据来源（2026-09-20 实测）：
//   - getBalance：SOL 余额
//   - getTokenAccountsByOwner（jsonParsed）：两个代币程序下的全部账户，含 mint/amount/decimals
//
// 边界（诚实标注）：
//   - 这是**取数时刻的快照**，snapshotBlock 记录当时的 getSlot 槽位；
//   - symbol / name / 价格取不到就留空，绝不用 mint 地址冒充符号；
//   - 账户数超过 maxBalanceAccounts 时**显式失败**，绝不静默截断
//     （截断会让 Coverage 误判"已完整下载"）。

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

// MethodBalance / MethodTokenAccounts 是 balances 实际使用的 RPC 方法。
const (
	MethodBalance       = "getBalance"
	MethodTokenAccounts = "getTokenAccountsByOwner"
	MethodSlot          = "getSlot"
)

// maxBalanceAccounts 是单地址快照的账户上限。
//
// 实测：3835 个代币账户的 jsonParsed 响应约 420KB，单账户约 110 字节；
// provider 的响应硬上限是 8MB，因此 20000 的上限留出充足余量。
// 超过上限一律显式失败（ADR-024 D2），不截断。
var maxBalanceAccounts = 20000

type balanceRow struct {
	HolderAddress     string `json:"holderAddress"`
	ContractAddress   string `json:"contractAddress"`
	TokenType         string `json:"tokenType"`
	TokenSymbol       string `json:"tokenSymbol"`
	TokenName         string `json:"tokenName"`
	TokenDecimals     uint16 `json:"tokenDecimals"`
	BalanceRawInteger string `json:"balanceRawInteger"`
	BalanceUSD        string `json:"balanceUsd"`
	TokenPrice        string `json:"tokenPrice"`
	SnapshotBlock     uint64 `json:"snapshotBlock"`
	Timestamp         int64  `json:"timestamp"`
}

type slotValue uint64

type balanceResult struct {
	Value uint64 `json:"value"`
}

type tokenAccountsResult struct {
	Value []struct {
		Pubkey  string `json:"pubkey"`
		Account struct {
			Owner string          `json:"owner"`
			Data  json.RawMessage `json:"data"`
		} `json:"account"`
	} `json:"value"`
}

type parsedTokenAccount struct {
	Parsed struct {
		Info struct {
			Mint        string `json:"mint"`
			Owner       string `json:"owner"`
			TokenAmount struct {
				Amount   string `json:"amount"`
				Decimals uint8  `json:"decimals"`
			} `json:"tokenAmount"`
		} `json:"info"`
	} `json:"parsed"`
}

// fetchBalancesPage 抓取一次快照并返回全部余额行（单页，不分页）。
func (c *Client) fetchBalancesPage(ctx context.Context, req provider.FetchRequest, started time.Time) (provider.FetchResult, error) {
	slotRaw, _, err := c.callWithSize(ctx, MethodSlot, nil)
	if err != nil {
		c.recordUsage(ctx, req, MethodSlot, 0, time.Since(started), "error", err)
		return provider.FetchResult{}, err
	}
	var slot slotValue
	if err := json.Unmarshal(slotRaw, &slot); err != nil {
		perr := &provider.Error{Provider: "solana", Class: provider.ClassInvalidJSON,
			Message: "invalid getSlot result: " + err.Error(), Retryable: true, Cause: err}
		c.recordUsage(ctx, req, MethodSlot, 0, time.Since(started), "error", perr)
		return provider.FetchResult{}, perr
	}
	snapshot := uint64(slot)
	timestamp := time.Now().Unix()

	rows := make([]balanceRow, 0, 16)
	var decoded int64

	// 原生 SOL
	nativeResult, nativeBytes, err := c.callWithSize(ctx, MethodBalance, []any{req.Address})
	decoded += nativeBytes
	if err != nil {
		c.recordUsage(ctx, req, MethodBalance, 0, time.Since(started), "error", err)
		return provider.FetchResult{}, err
	}
	var native balanceResult
	if err := json.Unmarshal(nativeResult, &native); err != nil {
		perr := &provider.Error{Provider: "solana", Class: provider.ClassInvalidJSON,
			Message: "invalid getBalance result: " + err.Error(), Retryable: true, Cause: err}
		c.recordUsage(ctx, req, MethodBalance, 0, time.Since(started), "error", perr)
		return provider.FetchResult{}, perr
	}
	rows = append(rows, balanceRow{
		HolderAddress: req.Address, ContractAddress: "", TokenType: "native",
		TokenDecimals: 9, BalanceRawInteger: strconv.FormatUint(native.Value, 10),
		SnapshotBlock: snapshot, Timestamp: timestamp,
	})

	// SPL Token 与 Token-2022
	for _, program := range []struct {
		id, kind string
	}{{ProgramSPLToken, "SPL"}, {ProgramToken2022, "SPL-2022"}} {
		params := []any{req.Address, map[string]any{"programId": program.id}, map[string]any{"encoding": "jsonParsed"}}
		result, size, err := c.callWithSize(ctx, MethodTokenAccounts, params)
		decoded += size
		if err != nil {
			c.recordUsage(ctx, req, MethodTokenAccounts, 0, time.Since(started), "error", err)
			return provider.FetchResult{}, err
		}
		var accounts tokenAccountsResult
		if err := json.Unmarshal(result, &accounts); err != nil {
			perr := &provider.Error{Provider: "solana", Class: provider.ClassInvalidJSON,
				Message: "invalid getTokenAccountsByOwner result: " + err.Error(), Retryable: true, Cause: err}
			c.recordUsage(ctx, req, MethodTokenAccounts, 0, time.Since(started), "error", perr)
			return provider.FetchResult{}, perr
		}
		// 同一 owner 可能对同一 mint 持有多个代币账户（ATA）：按 mint 求和后只落一行。
		// 否则 ReplacingMergeTree 的 (chain,address,token_symbol,token_address) 去重键会把
		// 多出来的账户**静默合并**掉（实测一次快照 3125 行落库只剩 3093 行）。
		perMint := map[string]*big.Int{}
		decimalsByMint := map[string]uint16{}
		for _, account := range accounts.Value {
			var parsed parsedTokenAccount
			if err := json.Unmarshal(account.Account.Data, &parsed); err != nil {
				// 单个账户解析不了就跳过（与 logs 的 parsed 形态问题同类），不丢整次快照。
				continue
			}
			info := parsed.Parsed.Info
			if info.Mint == "" {
				continue
			}
			amount, ok := new(big.Int).SetString(info.TokenAmount.Amount, 10)
			if !ok {
				continue
			}
			if current, exists := perMint[info.Mint]; exists {
				current.Add(current, amount)
			} else {
				perMint[info.Mint] = amount
			}
			decimalsByMint[info.Mint] = uint16(info.TokenAmount.Decimals)
		}
		mints := make([]string, 0, len(perMint))
		for mint := range perMint {
			mints = append(mints, mint)
		}
		sort.Strings(mints)
		for _, mint := range mints {
			rows = append(rows, balanceRow{
				HolderAddress: req.Address, ContractAddress: mint, TokenType: program.kind,
				TokenDecimals: decimalsByMint[mint], BalanceRawInteger: perMint[mint].String(),
				SnapshotBlock: snapshot, Timestamp: timestamp,
			})
		}
	}

	if len(rows) > maxBalanceAccounts {
		perr := &provider.Error{Provider: "solana", Class: provider.ClassResponseTooLarge,
			Message: fmt.Sprintf("balance snapshot for %s has %d accounts, above the %d cap; refusing to truncate",
				req.Address, len(rows), maxBalanceAccounts), Retryable: false}
		c.recordUsage(ctx, req, MethodTokenAccounts, len(rows), time.Since(started), "error", perr)
		return provider.FetchResult{}, perr
	}

	encoded := encodeRows(rows)
	c.recordUsage(ctx, req, MethodTokenAccounts, len(encoded), time.Since(started), "ok", nil)
	return provider.FetchResult{
		Rows: encoded, NextPageToken: "", HasMore: false,
		RequestID: req.RequestID, AttemptNo: req.AttemptNo, RequestPageSize: len(encoded),
		RequestFromBlock: req.FromBlock, RequestToBlock: req.ToBlock,
		DecodedBytes: decoded,
		StartedAt:    started, BodyCompleteAt: time.Now(),
	}, nil
}
