package tron

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/ingest"
)

func decodeMap(raw json.RawMessage) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	return m, nil
}

func stringValue(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case nil:
		return ""
	default:
		return fmt.Sprint(x)
	}
}

func uintValue(v any) uint64        { n, _ := strconv.ParseUint(stringValue(v), 10, 64); return n }
func mapValue(v any) map[string]any { m, _ := v.(map[string]any); return m }

func canonicalAddress(v any) string {
	s := strings.TrimSpace(stringValue(v))
	if s == "" {
		return ""
	}
	n, err := NormalizeTronAddress(s)
	if err != nil {
		return s
	}
	return n
}

func timestampValue(v any) time.Time {
	ms, _ := strconv.ParseInt(stringValue(v), 10, 64)
	if ms <= 0 {
		return time.Unix(0, 0).UTC()
	}
	return time.UnixMilli(ms).UTC()
}

func NormalizeTransaction(raw json.RawMessage) (ingest.TxRow, error) {
	m, err := decodeMap(raw)
	if err != nil {
		return ingest.TxRow{}, err
	}
	txID := stringValue(m["txID"])
	if txID == "" {
		return ingest.TxRow{}, errors.New("TRON transaction missing txID")
	}
	contractType, rawStatus := "OTHER", "UNKNOWN"
	from, to, amount, input := "", "", "0", ""
	rawData := mapValue(m["raw_data"])
	if contracts, ok := rawData["contract"].([]any); ok && len(contracts) > 0 {
		contractIndex := int(uintValue(m["_tron_target_contract_index"]))
		if contractIndex < 0 || contractIndex >= len(contracts) {
			contractIndex = 0
		}
		contract := mapValue(contracts[contractIndex])
		if typ := stringValue(contract["type"]); typ != "" {
			contractType = typ
		}
		parameter := mapValue(contract["parameter"])
		value := mapValue(parameter["value"])
		from = canonicalAddress(value["owner_address"])
		to = canonicalAddress(value["to_address"])
		if to == "" {
			to = canonicalAddress(value["contract_address"])
		}
		if v := stringValue(value["amount"]); v != "" {
			amount = v
		}
		if v := stringValue(value["call_value"]); v != "" {
			amount = v
		}
		input = stringValue(value["data"])
	}
	status := uint8(0)
	if ret, ok := m["ret"].([]any); ok && len(ret) > 0 {
		rawStatus = strings.ToUpper(stringValue(mapValue(ret[0])["contractRet"]))
		if rawStatus == "SUCCESS" || rawStatus == "SUCESS" {
			status = 1
		}
	}
	return ingest.TxRow{Chain: "tron", BlockNumber: uintValue(m["blockNumber"]), TxHash: txID,
		Timestamp: timestampValue(m["block_timestamp"]), From: from, To: to, ValueRaw: amount,
		TxType: normalizedContractType(contractType), Status: status, Input: input, Provider: rowProvider(m),
		NativeSymbol: "TRX", Confirmation: "SOLIDIFIED", RawType: contractType, RawStatus: rawStatus}, nil
}

func NormalizeTRC20(raw json.RawMessage) (ingest.TokenTransferRow, error) {
	m, err := decodeMap(raw)
	if err != nil {
		return ingest.TokenTransferRow{}, err
	}
	txID := stringValue(m["transaction_id"])
	if txID == "" {
		return ingest.TokenTransferRow{}, errors.New("TRC20 transfer missing transaction_id")
	}
	info := mapValue(m["token_info"])
	rawAmount := stringValue(m["value"])
	amount, ok := new(big.Int).SetString(rawAmount, 10)
	if !ok {
		return ingest.TokenTransferRow{}, errors.New("TRC20 transfer has invalid integer amount")
	}
	index, hasProviderIndex := m["_tron_event_index"]
	logIndex := uint32(uintValue(index))
	if !hasProviderIndex {
		logIndex = stableIndex(txID, raw)
	}
	return ingest.TokenTransferRow{Chain: "tron", BlockNumber: uintValue(m["block_number"]),
		Timestamp: timestampValue(m["block_timestamp"]), TxHash: txID, LogIndex: logIndex,
		TokenAddress: canonicalAddress(info["address"]), TokenSymbol: stringValue(info["symbol"]),
		TokenDecimals: uint16(uintValue(info["decimals"])), From: canonicalAddress(m["from"]), To: canonicalAddress(m["to"]),
		AmountRaw: rawAmount, Amount: amount, Provider: rowProvider(m)}, nil
}

func NormalizeInternal(raw json.RawMessage) (ingest.InternalTransferRow, error) {
	m, err := decodeMap(raw)
	if err != nil {
		return ingest.InternalTransferRow{}, err
	}
	txID := stringValue(m["tx_id"])
	if txID == "" {
		return ingest.InternalTransferRow{}, errors.New("TRON internal transfer missing tx_id")
	}
	data := mapValue(m["data"])
	callValue := mapValue(data["call_value"])
	valueRaw := stringValue(callValue["_"])
	if valueRaw == "" {
		valueRaw = "0"
	}
	if _, ok := new(big.Int).SetString(valueRaw, 10); !ok {
		return ingest.InternalTransferRow{}, errors.New("TRON internal transfer has invalid integer amount")
	}
	internalID := stringValue(m["internal_tx_id"])
	index := stableIndex(txID, []byte(internalID))
	if _, ok := m["_tron_internal_index"]; ok {
		index = uint32(uintValue(m["_tron_internal_index"]))
	}
	rejected, _ := data["rejected"].(bool)
	assetType := "TRX"
	if tokenID := stringValue(data["token_id"]); tokenID != "" {
		assetType = "TRC10:" + tokenID
	}
	return ingest.InternalTransferRow{Chain: "tron", TxHash: txID, CallIndex: index,
		BlockNumber: uintValue(m["block_number"]), Timestamp: timestampValue(m["block_timestamp"]),
		From: canonicalAddress(m["from_address"]), To: canonicalAddress(m["to_address"]), ValueRaw: valueRaw,
		AssetType: assetType, CallType: stringValue(data["note"]), Rejected: rejected, Provider: rowProvider(m)}, nil
}

func rowProvider(m map[string]any) string {
	if name := strings.TrimSpace(stringValue(m["_provider"])); name != "" {
		return name
	}
	return "trongrid"
}

func normalizedContractType(raw string) string {
	switch raw {
	case "TransferContract", "TriggerSmartContract", "TransferAssetContract":
		return raw
	default:
		return "OTHER"
	}
}

func stableIndex(txID string, raw []byte) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(txID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(raw)
	n := h.Sum32()
	if n == 0 {
		return 1
	}
	return n
}
