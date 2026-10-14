package api

import (
	"encoding/hex"
	"encoding/json"
	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
	"math/big"
	"strings"
)

// Receipt events describe emitted data. Settlement is checked separately against
// transaction state, including wrapped-native mint/burn omitted by Transfer logs.
func contractReceiptEvents(result *ca.Result, txHash, artifactID string, logs []chainread.Log, preRaw json.RawMessage, limit int) {
	var pre map[string]ca.StateAccount
	_ = json.Unmarshal(preRaw, &pre)
	zero := "0x" + strings.Repeat("0", 40)
	transfer := contractKeccak([]byte("Transfer(address,address,uint256)"))
	single := contractKeccak([]byte("TransferSingle(address,address,address,uint256,uint256)"))
	batch := contractKeccak([]byte("TransferBatch(address,address,address,uint256[],uint256[])"))
	deposit := contractKeccak([]byte("Deposit(address,uint256)"))
	withdrawal := contractKeccak([]byte("Withdrawal(address,uint256)"))
	appendEvent := func(log chainread.Log, event map[string]any) {
		eid := ca.ID(txHash, strconvUint(log.LogIndex), "log")
		event["tx_hash"], event["log_index"], event["asset"] = txHash, log.LogIndex, strings.ToLower(string(log.Address))
		event["settlement"], event["state_reconciliation"], event["evidence_ids"] = "receipt_event", "not_verified", []string{eid}
		result.Sections["token_events"] = append(result.Sections["token_events"], ca.Row(event))
	}
	for _, log := range logs {
		if log.Removed || len(log.Topics) == 0 {
			continue
		}
		data, err := hex.DecodeString(strings.TrimPrefix(log.Data, "0x"))
		if err != nil {
			continue
		}
		topic := strings.ToLower(log.Topics[0])
		before := len(result.Sections["token_events"])
		switch {
		case topic == transfer && len(log.Topics) >= 3:
			from, e1 := strictTopicAddress(log.Topics[1])
			to, e2 := strictTopicAddress(log.Topics[2])
			if e1 != nil || e2 != nil {
				continue
			}
			if len(log.Topics) == 3 && len(data) == 32 {
				appendEvent(log, map[string]any{"event": "Transfer", "token_standard": "ERC20", "schema_basis": "receipt_topic_and_abi_shape", "from": from, "to": to, "amount_raw": new(big.Int).SetBytes(data).String()})
			} else if len(log.Topics) == 4 && len(data) == 0 {
				tokenID, e := ca.RawInteger(log.Topics[3])
				if e != nil || len(log.Topics[3]) != 66 {
					continue
				}
				appendEvent(log, map[string]any{"event": "Transfer", "token_standard": "ERC721", "schema_basis": "receipt_topic_and_abi_shape", "from": from, "to": to, "amount_raw": "1", "token_id_raw": tokenID})
			}
		case (topic == single || topic == batch) && len(log.Topics) == 4:
			operator, e0 := strictTopicAddress(log.Topics[1])
			from, e1 := strictTopicAddress(log.Topics[2])
			to, e2 := strictTopicAddress(log.Topics[3])
			if e0 != nil || e1 != nil || e2 != nil {
				continue
			}
			ids, amounts := []string{}, []string{}
			if topic == single && len(data) == 64 {
				ids = []string{new(big.Int).SetBytes(data[:32]).String()}
				amounts = []string{new(big.Int).SetBytes(data[32:]).String()}
			} else if topic == batch {
				ids, amounts = contract1155Batch(data, limit-before)
			}
			for i, id := range ids {
				appendEvent(log, map[string]any{"event": map[bool]string{true: "TransferSingle", false: "TransferBatch"}[topic == single], "token_standard": "ERC1155", "schema_basis": "receipt_topic_and_abi_shape", "operator": operator, "from": from, "to": to, "token_id_raw": id, "amount_raw": amounts[i], "batch_index": i})
			}
		case (topic == deposit || topic == withdrawal) && len(log.Topics) == 2 && len(data) == 32:
			// The original WETH9 runtime is pinned by code hash, never by token name.
			code, _ := hex.DecodeString(strings.TrimPrefix(pre[strings.ToLower(string(log.Address))].Code, "0x"))
			if contractKeccak(code) != "0xd0a06b12ac47863b5c7be4185c2deaad1c61557033f56c7d4ea74429cbb25e23" {
				continue
			}
			holder, e := strictTopicAddress(log.Topics[1])
			if e != nil {
				continue
			}
			from, to, name := zero, holder, "Deposit"
			if topic == withdrawal {
				from, to, name = holder, zero, "Withdrawal"
			}
			appendEvent(log, map[string]any{"event": name, "token_standard": "ERC20", "protocol": "WETH9", "adapter_version": "weth9-1", "schema_basis": "transaction_prestate_runtime_code_hash", "from": from, "to": to, "amount_raw": new(big.Int).SetBytes(data).String()})
		}
		if len(result.Sections["token_events"]) > before {
			result.Sections["execution_evidence"] = append(result.Sections["execution_evidence"], ca.Row(map[string]any{"evidence_id": ca.ID(txHash, strconvUint(log.LogIndex), "log"), "tx_hash": txHash, "log_index": log.LogIndex, "artifact_id": artifactID, "kind": "receipt_log"}))
		}
		if len(result.Sections["token_events"]) >= limit {
			result.Sections["coverage"] = append(result.Sections["coverage"], ca.Row(map[string]any{"dimension": "receipt_event_projection", "tx_hash": txHash, "complete": false, "reason": "max_records"}))
			return
		}
	}
}

func contract1155Batch(data []byte, limit int) ([]string, []string) {
	if limit < 1 || len(data) < 128 || len(data) > 1<<20 || len(data)%32 != 0 {
		return nil, nil
	}
	read := func(head []byte) ([]string, int, int) {
		off := new(big.Int).SetBytes(head)
		if !off.IsUint64() || off.Uint64() < 64 || off.Uint64()%32 != 0 || off.Uint64() > uint64(len(data)-32) {
			return nil, 0, 0
		}
		start := int(off.Uint64())
		count := new(big.Int).SetBytes(data[start : start+32])
		if !count.IsUint64() || count.Uint64() > uint64(limit) || count.Uint64() > uint64((len(data)-start-32)/32) {
			return nil, 0, 0
		}
		end := start + 32 + int(count.Uint64())*32
		rows := make([]string, 0, int(count.Uint64()))
		for p := start + 32; p < end; p += 32 {
			rows = append(rows, new(big.Int).SetBytes(data[p:p+32]).String())
		}
		return rows, start, end
	}
	ids, s1, e1 := read(data[:32])
	amounts, s2, e2 := read(data[32:64])
	if ids == nil || amounts == nil || len(ids) != len(amounts) || s1 != 64 || s2 != e1 || e2 != len(data) {
		return nil, nil
	}
	return ids, amounts
}
