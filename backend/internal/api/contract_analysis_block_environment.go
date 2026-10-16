package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
)

type contractSystemAccount struct {
	address                string
	slots                  []*big.Int
	queueWords, queueLimit int64
}

func contractSystemStateGateway(call func(context.Context, rpc.Request) (rpc.Response, error), reserve func(context.Context) error) func(context.Context, rpc.Request) (rpc.Response, error) {
	return func(ctx context.Context, req rpc.Request) (rpc.Response, error) {
		if req.Method == "eth_getStorageAt" {
			if err := reserve(ctx); err != nil {
				return rpc.Response{}, &rpc.Error{Class: rpc.ClassBudgetExceeded, Message: "block system state budget exhausted", Cause: err}
			}
		}
		return call(ctx, req)
	}
}

// These parent-state observations support the VM's block boundary operations.
// They never replace a transaction tracer observation or prove a whole block.
func contractReadBlockSystemState(ctx context.Context, call func(context.Context, rpc.Request) (rpc.Response, error), chainSlug, fork string, block, parent map[string]any, slotBudget int, budget rpc.BudgetContext) (map[string]any, []any) {
	row := map[string]any{"schema_version": "contract-block-system-prestate-v1", "scope": "observed_parent_state_for_block_system_calls", "execution_rules": fork, "complete": false, "accounts": map[string]any{}, "whole_block_execution_verified": false}
	reads := []any{}
	if fork == "chain_rules_unavailable" {
		row["required"], row["reason"] = false, "BLOCK_SYSTEM_EXECUTION_RULES_UNAVAILABLE"
		return row, reads
	}
	if !strings.Contains("|cancun|prague|osaka|", "|"+fork+"|") {
		row["complete"] = true
		row["required"] = false
		return row, reads
	}
	row["required"] = true
	bn, e := contractCreationInteger(block["number"])
	pn, f := contractCreationInteger(parent["number"])
	clock, g := contractCreationInteger(block["timestamp"])
	if e != nil || f != nil || g != nil || !bn.IsUint64() || !pn.IsUint64() || !clock.IsUint64() || bn.Sign() == 0 || new(big.Int).Sub(bn, pn).Cmp(big.NewInt(1)) != 0 || !ca.CompleteHash.MatchString(contractCreationText(parent["hash"])) || !contractCreationEqual(block["parentHash"], parent["hash"]) {
		row["reason"] = "BLOCK_SYSTEM_PARENT_IDENTITY_MISMATCH"
		return row, reads
	}
	row["parent_block_hash"], row["parent_block_number"] = parent["hash"], parent["number"]
	ref := map[string]any{"blockHash": parent["hash"], "requireCanonical": true}
	used := 0
	if slotBudget > 96 {
		slotBudget = 96
	}
	if slotBudget < 0 {
		slotBudget = 0
	}
	slot := func(n *big.Int) string { return "0x" + fmt.Sprintf("%064x", n) }
	read := func(address, method string, key *big.Int) (string, error) {
		params := []any{address, ref}
		class := rpc.ClassBalance
		if method == "eth_getCode" {
			class = rpc.ClassCode
		}
		if method == "eth_getStorageAt" {
			if used >= slotBudget {
				return "", fmt.Errorf("BLOCK_SYSTEM_SLOT_BUDGET")
			}
			used++
			params = []any{address, slot(key), ref}
			class = rpc.ClassStorage
		}
		response, err := call(ctx, rpc.Request{Chain: chainSlug, Method: method, Params: params, Class: class, Priority: rpc.PriorityBackground, Budget: budget, BlockRef: chain.AtBlock(pn.Uint64())})
		meta := map[string]any{"kind": "block_system_parent_state", "address": address, "method": method, "parent_block_hash": parent["hash"], "provider": response.Provider, "cache_hit": response.CacheHit, "attempts": response.Attempts, "bytes": response.Bytes, "response_sha256": ca.SHA256(response.Result)}
		if key != nil {
			meta["slot"] = slot(key)
		}
		if err != nil {
			meta["error_class"] = rpc.AsError(err).Class
		}
		reads = append(reads, meta)
		if err != nil {
			return "", fmt.Errorf("BLOCK_SYSTEM_STATE_%s", rpc.AsError(err).Class)
		}
		var value string
		if json.Unmarshal(response.Result, &value) != nil {
			return "", fmt.Errorf("BLOCK_SYSTEM_STATE_INVALID_RESPONSE")
		}
		if method == "eth_getCode" {
			if _, err := contractCreationBytes(value); err != nil {
				return "", fmt.Errorf("BLOCK_SYSTEM_CODE_INVALID")
			}
			return value, nil
		}
		n, err := contractCreationInteger(value)
		if err != nil || n.Sign() < 0 || n.BitLen() > 256 {
			return "", fmt.Errorf("BLOCK_SYSTEM_QUANTITY_INVALID")
		}
		if method == "eth_getStorageAt" {
			return slot(n), nil
		}
		if method == "eth_getTransactionCount" && !n.IsUint64() {
			return "", fmt.Errorf("BLOCK_SYSTEM_NONCE_INVALID")
		}
		return "0x" + n.Text(16), nil
	}
	index := new(big.Int).Mod(clock, big.NewInt(8191))
	required := []contractSystemAccount{{address: "0x000f3df6d732807ef1319fb7b8bb8522d0beac02", slots: []*big.Int{new(big.Int).Set(index), new(big.Int).Add(index, big.NewInt(8191))}}}
	if fork == "prague" || fork == "osaka" {
		required = append(required, contractSystemAccount{address: "0x0000f90827f1c53a10cb7a02335b175320002935", slots: []*big.Int{new(big.Int).Mod(new(big.Int).Sub(bn, big.NewInt(1)), big.NewInt(8191))}})
		for _, q := range []struct {
			address      string
			words, limit int64
		}{{"0x00000961ef480eb55e80d19ad83579a64c007002", 3, 16}, {"0x0000bbddc7ce488642fb579f8b00f3a590007251", 4, 2}} {
			required = append(required, contractSystemAccount{address: q.address, slots: []*big.Int{big.NewInt(0), big.NewInt(1), big.NewInt(2), big.NewInt(3)}, queueWords: q.words, queueLimit: q.limit})
		}
	}
	required = append(required, contractSystemAccount{address: "0xfffffffffffffffffffffffffffffffffffffffe"})
	accounts := row["accounts"].(map[string]any)
	for _, item := range required {
		account := map[string]any{"storage": map[string]string{}}
		words := account["storage"].(map[string]string)
		for _, field := range []struct{ name, method string }{{"balance", "eth_getBalance"}, {"nonce", "eth_getTransactionCount"}, {"code", "eth_getCode"}} {
			value, err := read(item.address, field.method, nil)
			if err != nil {
				row["reason"] = err.Error()
				row["slot_reads"] = used
				return row, reads
			}
			account[field.name] = value
		}
		for _, key := range item.slots {
			value, err := read(item.address, "eth_getStorageAt", key)
			if err != nil {
				row["reason"] = err.Error()
				row["slot_reads"] = used
				return row, reads
			}
			words[slot(key)] = value
		}
		if item.queueWords > 0 {
			head, _ := contractCreationInteger(words[slot(big.NewInt(2))])
			tail, _ := contractCreationInteger(words[slot(big.NewInt(3))])
			count := new(big.Int).Sub(tail, head)
			if count.Sign() < 0 {
				row["reason"] = "BLOCK_SYSTEM_QUEUE_POINTERS_INVALID"
				return row, reads
			}
			if count.Cmp(big.NewInt(item.queueLimit)) > 0 {
				count.SetInt64(item.queueLimit)
			}
			for i := int64(0); i < count.Int64(); i++ {
				start := new(big.Int).Add(big.NewInt(4), new(big.Int).Mul(new(big.Int).Add(head, big.NewInt(i)), big.NewInt(item.queueWords)))
				for word := int64(0); word < item.queueWords; word++ {
					key := new(big.Int).Add(start, big.NewInt(word))
					if key.BitLen() > 256 {
						row["reason"] = "BLOCK_SYSTEM_QUEUE_SLOT_OVERFLOW"
						return row, reads
					}
					value, err := read(item.address, "eth_getStorageAt", key)
					if err != nil {
						row["reason"] = err.Error()
						row["slot_reads"] = used
						return row, reads
					}
					words[slot(key)] = value
				}
			}
		}
		accounts[item.address] = account
	}
	row["complete"], row["slot_reads"], row["account_count"] = true, used, len(accounts)
	return row, reads
}
