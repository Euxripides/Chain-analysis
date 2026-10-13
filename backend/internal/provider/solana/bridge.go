package solana

import (
	"encoding/json"
	"fmt"
	"strings"
)

// FundingEvent is a decoded instruction, not a transaction-level balance delta.
type FundingEvent struct {
	Index                     uint32
	From, To, Mint, AmountRaw string
	Decimals                  int
	Native                    bool
}

type BridgeTransaction struct {
	Timestamp     int64
	Events        []FundingEvent
	Programs      []string
	Failed        bool
	DecodeGaps    int
	Truncated     bool
	TokenAccounts map[string]BridgeTokenAccount
}

type BridgeTokenAccount struct{ Owner, Mint string }

// DecodeBridgeTransaction reuses the same SPL/system instruction decoder as
// address ingestion. It performs no network calls and writes no dataset rows.
func DecodeBridgeTransaction(raw json.RawMessage, signature string) (BridgeTransaction, error) {
	out := BridgeTransaction{Events: []FundingEvent{}, Programs: []string{}, TokenAccounts: map[string]BridgeTokenAccount{}}
	var tx txResult
	if len(raw) > 8<<20 {
		return out, fmt.Errorf("Solana transaction exceeds decode budget")
	}
	if err := json.Unmarshal(raw, &tx); err != nil {
		return out, fmt.Errorf("invalid Solana transaction record")
	}
	if tx.Meta == nil || tx.BlockTime == nil || *tx.BlockTime <= 0 {
		return out, fmt.Errorf("Solana transaction metadata or block time unavailable")
	}
	matched := false
	for _, value := range tx.Transaction.Signatures {
		if value == signature {
			matched = true
		}
	}
	if !matched {
		return out, fmt.Errorf("Solana transaction signature does not match requested hash")
	}
	out.Timestamp, out.Failed = *tx.BlockTime, tx.Meta.Err != nil
	if out.Failed {
		return out, nil
	}
	count := len(tx.Transaction.Message.Instructions)
	seenGroups := map[int]bool{}
	for _, group := range tx.Meta.InnerInstructions {
		count += len(group.Instructions)
		if group.Index < 0 || group.Index >= len(tx.Transaction.Message.Instructions) || len(group.Instructions) > 255 || seenGroups[group.Index] {
			return out, fmt.Errorf("Solana instruction indices exceed decoder identity limits")
		}
		seenGroups[group.Index] = true
	}
	if count > 4096 {
		return out, fmt.Errorf("Solana instruction count exceeds decode budget")
	}
	keys := tx.accountKeys()
	index := buildTokenAccountIndex(keys, tx.Meta.PreTokenBalances, tx.Meta.PostTokenBalances)
	if err := addInitializedTokenAccounts(keys, tx.Transaction.Message.Instructions, tx.Meta.InnerInstructions, index); err != nil {
		return out, err
	}
	conflicted := map[string]bool{}
	known := map[string]BridgeTokenAccount{}
	for _, entries := range [][]tokenBalanceEntry{tx.Meta.PreTokenBalances, tx.Meta.PostTokenBalances} {
		for _, entry := range entries {
			if entry.AccountIndex < 0 || entry.AccountIndex >= len(keys) {
				continue
			}
			address := keys[entry.AccountIndex]
			current := BridgeTokenAccount{Owner: entry.Owner, Mint: entry.Mint}
			if previous, ok := known[address]; ok && (previous.Owner != "" && current.Owner != "" && previous.Owner != current.Owner || previous.Mint != "" && current.Mint != "" && previous.Mint != current.Mint) {
				conflicted[address] = true
			}
			known[address] = current
		}
	}
	changedAuthority := func(ix parsedInstructionRaw) {
		program := ix.programID(keys)
		if program != ProgramSPLToken && program != ProgramToken2022 {
			return
		}
		if ix.Parsed != nil && strings.EqualFold(ix.Parsed.Type, "setAuthority") {
			if account, ok := ix.Parsed.infoMap()["account"].(string); ok {
				conflicted[account] = true
			}
		}
		if data := ix.dataBytes(); len(data) > 0 && data[0] == 6 {
			if accounts := ix.accountList(keys); len(accounts) > 0 {
				conflicted[accounts[0]] = true
			}
		}
	}
	for _, ix := range tx.Transaction.Message.Instructions {
		changedAuthority(ix)
	}
	for _, group := range tx.Meta.InnerInstructions {
		for _, ix := range group.Instructions {
			changedAuthority(ix)
		}
	}
	for address, state := range index {
		if !conflicted[address] && state.Owner != "" && state.Mint != "" {
			out.TokenAccounts[address] = BridgeTokenAccount{Owner: state.Owner, Mint: state.Mint}
		}
	}
	rows, stats := buildTokenTransferRows(tx.Slot, tx.blockTimeValue(), signature, keys, tx.Transaction.Message.Instructions, tx.Meta.InnerInstructions, index)
	out.DecodeGaps = stats.OwnerUnresolved + stats.SkippedNoBalance + stats.FeeMismatch
	for _, row := range rows {
		if conflicted[row.FromTokenAccount] || conflicted[row.ToTokenAccount] {
			out.DecodeGaps++
			continue
		}
		out.Events = append(out.Events, FundingEvent{Index: row.LogIndex, From: row.FromAddress, To: row.ToAddress, Mint: row.ContractAddress, AmountRaw: row.ValueRawInteger, Decimals: int(row.TokenDecimals)})
	}
	native, err := systemTransferRows(&tx, signature)
	if err != nil {
		return out, err
	}
	for _, row := range native {
		if row.CallType == "system_transfer" {
			out.Events = append(out.Events, FundingEvent{Index: row.CallIndex, From: row.From, To: row.To, Mint: "native", AmountRaw: row.ValueRaw, Decimals: 9, Native: true})
		}
	}
	if len(out.Events) > 400 {
		out.Events = out.Events[:400]
		out.Truncated = true
	}
	seen := map[string]bool{}
	add := func(ix parsedInstructionRaw) {
		p := ix.programID(keys)
		if p != "" && !seen[p] && len(out.Programs) < 64 {
			seen[p] = true
			out.Programs = append(out.Programs, p)
		}
	}
	for _, ix := range tx.Transaction.Message.Instructions {
		add(ix)
	}
	for _, group := range tx.Meta.InnerInstructions {
		for _, ix := range group.Instructions {
			add(ix)
		}
	}
	return out, nil
}
