package solana

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/ingest"
)

// NormalizeProgramLog 把 Provider 解析出的 Solana 程序日志行转成事件日志行（ADR-024 D1）。
//
// 与 EVM 日志的差别必须显式保留，绝不伪装：
//   - Topics 恒为空（Solana 没有 topics / indexed 参数）；
//   - EventSignature 为空、EventVerified=0（没有 EVM 签名，也没有 ABI 可校验）；
//   - EventSource 标注真实来源（solana_program_log / solana_program_data），
//     供下游按 event_source 排除 EVM 式解码。
func NormalizeProgramLog(raw json.RawMessage) (ingest.LogRow, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return ingest.LogRow{}, err
	}
	txHash := strings.TrimSpace(stringField(m, "transactionHash"))
	if txHash == "" {
		return ingest.LogRow{}, errors.New("solana program log row missing transactionHash")
	}
	blockNumber, err := strconv.ParseUint(strings.TrimSpace(stringField(m, "blockNumber")), 10, 64)
	if err != nil {
		return ingest.LogRow{}, errors.New("solana program log row has invalid block number")
	}
	source := strings.TrimSpace(stringField(m, "eventSource"))
	if source == "" {
		source = "solana_program_log"
	}
	removed := uint8(0)
	if boolField(m, "removed") {
		removed = 1
	}
	timestamp := numberField(m, "timestamp")
	if timestamp <= 0 {
		timestamp = time.Now().Unix()
	}
	return ingest.LogRow{
		Chain: "solana", BlockNumber: blockNumber, TxHash: txHash,
		LogIndex:  uint32(numberField(m, "logIndex")),
		Address:   strings.TrimSpace(stringField(m, "address")),
		Topics:    nil,
		Data:      stringField(m, "data"),
		Removed:   removed,
		Timestamp: time.Unix(timestamp, 0).UTC(),
		Provider:  "solana",
		EventName: strings.TrimSpace(stringField(m, "eventName")),
		// 没有 EVM 签名与 ABI 校验：留空/0，而不是编一个签名出来。
		EventSignature: "",
		EventSource:    source,
		EventVerified:  0,
	}, nil
}
