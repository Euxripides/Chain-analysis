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
func NormalizeProgramLog(raw json.RawMessage, provider string) (ingest.LogRow, error) {
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
		return ingest.LogRow{}, errors.New("solana program log has no verifiable timestamp")
	}
	return ingest.LogRow{
		Chain: "solana", BlockNumber: blockNumber, TxHash: txHash,
		LogIndex:  uint32(numberField(m, "logIndex")),
		Address:   strings.TrimSpace(stringField(m, "address")),
		Topics:    nil,
		Data:      stringField(m, "data"),
		Removed:   removed,
		Timestamp: time.Unix(timestamp, 0).UTC(),
		Provider:  providerOrDefault(provider),
		EventName: strings.TrimSpace(stringField(m, "eventName")),
		// 没有 EVM 签名与 ABI 校验：留空/0，而不是编一个签名出来。
		EventSignature: "",
		EventSource:    source,
		EventVerified:  0,
	}, nil
}

// NormalizeSolanaProgramLog 把同一份程序日志行转成 solana_program_logs 行（规范 §8.4）。
//
// 与 NormalizeProgramLog 是双写关系（规范 §2.3）：blockchain.logs 供跨链事件面查询，
// solana_program_logs 保留 Solana 原生列（slot/signature/program_id/message/invoke_depth）。
// success 缺省为 1：Provider 没有给出失败证据就不判失败，显式 false 才记 0。
func NormalizeSolanaProgramLog(raw json.RawMessage, provider string) (ingest.SolanaProgramLogRow, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return ingest.SolanaProgramLogRow{}, err
	}
	signature := strings.TrimSpace(stringField(m, "transactionHash"))
	if signature == "" {
		return ingest.SolanaProgramLogRow{}, errors.New("solana program log row missing transactionHash")
	}
	slot, err := strconv.ParseUint(strings.TrimSpace(stringField(m, "blockNumber")), 10, 64)
	if err != nil {
		return ingest.SolanaProgramLogRow{}, errors.New("solana program log row has invalid block number")
	}
	timestamp := numberField(m, "timestamp")
	if timestamp <= 0 {
		return ingest.SolanaProgramLogRow{}, errors.New("solana native program log has no verifiable timestamp")
	}
	// §3 行契约把 programId 放在 address 上（programId 字段留空），两个键都认。
	programID := strings.TrimSpace(stringField(m, "programId"))
	if programID == "" {
		programID = strings.TrimSpace(stringField(m, "address"))
	}
	success := uint8(1)
	if _, ok := m["success"]; ok && !boolField(m, "success") {
		success = 0
	}
	return ingest.SolanaProgramLogRow{
		Chain: "solana", Slot: slot, Timestamp: time.Unix(timestamp, 0).UTC(),
		Signature: signature, LogIndex: uint32(numberField(m, "logIndex")),
		ProgramID: programID, Message: stringField(m, "data"),
		InvokeDepth: uint16(numberField(m, "invokeDepth")), Success: success,
		SourceProvider:  providerOrDefault(provider),
		SourceAPI:       strings.TrimSpace(stringField(m, "sourceApi")),
		SourceRequestID: strings.TrimSpace(stringField(m, "sourceRequestId")),
	}, nil
}
