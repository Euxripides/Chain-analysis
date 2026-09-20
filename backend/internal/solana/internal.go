package solana

import (
	"encoding/json"
	"errors"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/ingest"
)

// NormalizeInternal 把 Provider 解码出的 CPI 节点行转成 internal_calls 行（ADR-023 D11）。
//
// 与 TRON 的内部转账不同，Solana 的 CPI 节点是**程序调用**而不是资金移动：
// from/to 都是 program id，value_raw 只在节点确实是 System Program 转账时才非 0。
// 调用方据此不把它写进 address_activity（避免程序调用污染地址资金流口径）。
func NormalizeInternal(raw json.RawMessage) (ingest.InternalTransferRow, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return ingest.InternalTransferRow{}, err
	}
	txHash := strings.TrimSpace(stringField(m, "txHash"))
	if txHash == "" {
		return ingest.InternalTransferRow{}, errors.New("solana CPI row missing txHash")
	}
	valueRaw := strings.TrimSpace(stringField(m, "valueRaw"))
	if valueRaw == "" {
		valueRaw = "0"
	}
	if _, ok := new(big.Int).SetString(valueRaw, 10); !ok {
		return ingest.InternalTransferRow{}, errors.New("solana CPI row has invalid integer value")
	}
	blockNumber, err := strconv.ParseUint(strings.TrimSpace(stringField(m, "blockNumber")), 10, 64)
	if err != nil {
		return ingest.InternalTransferRow{}, errors.New("solana CPI row has invalid block number")
	}
	timestamp := int64(numberField(m, "timestamp"))
	if timestamp <= 0 {
		timestamp = time.Now().Unix()
	}
	callType := strings.TrimSpace(stringField(m, "callType"))
	if callType == "" {
		callType = "cpi"
	}
	assetType := strings.TrimSpace(stringField(m, "assetType"))
	if assetType == "" {
		assetType = "PROGRAM"
	}
	return ingest.InternalTransferRow{
		Chain: "solana", TxHash: txHash,
		CallIndex:   uint32(numberField(m, "callIndex")),
		BlockNumber: blockNumber,
		Timestamp:   time.Unix(timestamp, 0).UTC(),
		From:        strings.TrimSpace(stringField(m, "from")),
		To:          strings.TrimSpace(stringField(m, "to")),
		ValueRaw:    valueRaw,
		AssetType:   assetType,
		CallType:    callType,
		Depth:       uint16(numberField(m, "depth")),
		TracePath:   strings.TrimSpace(stringField(m, "tracePath")),
		MethodID:    strings.TrimSpace(stringField(m, "methodId")),
		Failed:      boolField(m, "failed"),
		ErrorText:   strings.TrimSpace(stringField(m, "error")),
		Provider:    "solana",
	}, nil
}

func stringField(m map[string]any, key string) string {
	switch value := m[key].(type) {
	case string:
		return value
	case float64:
		return strconv.FormatInt(int64(value), 10)
	case json.Number:
		return value.String()
	default:
		return ""
	}
}

func numberField(m map[string]any, key string) int64 {
	switch value := m[key].(type) {
	case float64:
		return int64(value)
	case json.Number:
		n, _ := value.Int64()
		return n
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		return n
	default:
		return 0
	}
}

func boolField(m map[string]any, key string) bool {
	switch value := m[key].(type) {
	case bool:
		return value
	case string:
		parsed, _ := strconv.ParseBool(value)
		return parsed
	default:
		return false
	}
}
