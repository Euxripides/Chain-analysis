// Package ingest：Ankr 原始行 → 标准化 ClickHouse 行。
package ingest

import (
	"encoding/json"
	"math/big"
	"strconv"
	"strings"
	"time"
)

func asString(m map[string]any, key string) string {
	if v, ok := m[key]; ok && v != nil {
		switch t := v.(type) {
		case string:
			return t
		case float64:
			return strconv.FormatFloat(t, 'f', -1, 64)
		case json.Number:
			return t.String()
		default:
			b, _ := json.Marshal(v)
			return string(b)
		}
	}
	return ""
}

func asUint64(m map[string]any, key string) uint64 {
	s := asString(m, key)
	if s == "" {
		return 0
	}
	if n, err := strconv.ParseUint(s, 10, 64); err == nil {
		return n
	}
	if n, err := strconv.ParseUint(strings.TrimPrefix(s, "0x"), 16, 64); err == nil {
		return n
	}
	return 0
}

func asUint32(m map[string]any, key string) uint32 { return uint32(asUint64(m, key)) }

func asBool(m map[string]any, key string) bool {
	if v, ok := m[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

// asTimeMs Ankr 时间戳为毫秒（number 或 string）。
func asTimeMs(m map[string]any, key string) time.Time {
	v, ok := m[key]
	if !ok || v == nil {
		return time.Now().UTC()
	}
	switch t := v.(type) {
	case float64:
		return time.UnixMilli(int64(t)).UTC()
	case json.Number:
		n, _ := t.Int64()
		return time.UnixMilli(n).UTC()
	case string:
		n, err := strconv.ParseInt(t, 10, 64)
		if err != nil {
			return time.Now().UTC()
		}
		if n > 1e12 { // 秒级时间戳补偿
			if n > 1e14 {
				n /= 1000
			}
		}
		return time.UnixMilli(n).UTC()
	}
	return time.Now().UTC()
}

func asBigInt(m map[string]any, key string) *big.Int {
	s := asString(m, key)
	if s == "" {
		return big.NewInt(0)
	}
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		if n, ok := new(big.Int).SetString(s[2:], 16); ok {
			return n
		}
		return big.NewInt(0)
	}
	if n, ok := new(big.Int).SetString(s, 10); ok {
		return n
	}
	return big.NewInt(0)
}

type TxRow struct {
	Chain       string
	BlockNumber uint64
	BlockHash   string
	TxHash      string
	Timestamp   time.Time
	From        string
	To          string
	ValueRaw    string
	Nonce       uint64
	Gas         uint64
	GasUsed     uint64
	GasPrice    string
	TxType      string
	Status      uint8
	Input       string
	MethodID    string
	Provider    string
}

func NormalizeTransaction(raw json.RawMessage, chain, provider string) (TxRow, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return TxRow{}, err
	}
	status := "0x0"
	if asString(m, "status") == "0x1" || asString(m, "status") == "1" {
		status = "0x1"
	}
	r := TxRow{
		Chain: chain, BlockNumber: asUint64(m, "blockHeight"), BlockHash: asString(m, "blockHash"),
		TxHash: asString(m, "transactionHash"), Timestamp: asTimeMs(m, "timestamp"),
		From: asString(m, "fromAddress"), To: asString(m, "toAddress"), ValueRaw: asString(m, "value"),
		Nonce: asUint64(m, "nonce"), Gas: asUint64(m, "gasLimit"), GasUsed: asUint64(m, "gasUsed"),
		GasPrice: asString(m, "gasPrice"), TxType: asString(m, "type"),
		Input: asString(m, "input"), MethodID: asString(m, "methodId"), Provider: provider,
	}
	if status == "0x1" {
		r.Status = 1
	}
	return r, nil
}

type TokenTransferRow struct {
	Chain        string
	BlockNumber  uint64
	Timestamp    time.Time
	TxHash       string
	LogIndex     uint32
	TokenAddress string
	TokenSymbol  string
	TokenDecimals uint16
	From         string
	To           string
	AmountRaw    string
	Amount       *big.Int
	Provider     string
}

func NormalizeTokenTransfer(raw json.RawMessage, chain, provider string) (TokenTransferRow, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return TokenTransferRow{}, err
	}
	return TokenTransferRow{
		Chain: chain, BlockNumber: asUint64(m, "blockHeight"), Timestamp: asTimeMs(m, "timestamp"),
		TxHash: asString(m, "transactionHash"), LogIndex: asUint32(m, "logIndex"),
		TokenAddress: asString(m, "contractAddress"), TokenSymbol: asString(m, "tokenSymbol"),
		TokenDecimals: uint16(asUint64(m, "tokenDecimals")),
		From: asString(m, "fromAddress"), To: asString(m, "toAddress"),
		AmountRaw: asString(m, "value"), Amount: asBigInt(m, "value"), Provider: provider,
	}, nil
}

type NftTransferRow struct {
	Chain         string
	BlockNumber   uint64
	Timestamp     time.Time
	TxHash        string
	LogIndex      uint32
	Contract      string
	TokenID       string
	From          string
	To            string
	TokenName     string
	TokenSymbol   string
	AmountRaw     string
	Provider      string
}

func NormalizeNftTransfer(raw json.RawMessage, chain, provider string) (NftTransferRow, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return NftTransferRow{}, err
	}
	return NftTransferRow{
		Chain: chain, BlockNumber: asUint64(m, "blockHeight"), Timestamp: asTimeMs(m, "timestamp"),
		TxHash: asString(m, "transactionHash"), LogIndex: asUint32(m, "logIndex"),
		Contract: asString(m, "contractAddress"), TokenID: asString(m, "tokenId"),
		From: asString(m, "fromAddress"), To: asString(m, "toAddress"),
		TokenName: asString(m, "tokenName"), TokenSymbol: asString(m, "tokenSymbol"),
		AmountRaw: asString(m, "value"), Provider: provider,
	}, nil
}

type LogRow struct {
	Chain       string
	BlockNumber uint64
	BlockHash   string
	TxHash      string
	LogIndex    uint32
	Address     string
	Topics      []string
	Data        string
	Removed     uint8
	Timestamp   time.Time
	Provider    string
}

func NormalizeLog(raw json.RawMessage, chain, provider string) (LogRow, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return LogRow{}, err
	}
	var topics []string
	if v, ok := m["topics"].([]any); ok {
		for _, t := range v {
			topics = append(topics, asString(map[string]any{"t": t}, "t"))
		}
	}
	removed := uint8(0)
	if asBool(m, "removed") {
		removed = 1
	}
	return LogRow{
		Chain: chain, BlockNumber: asUint64(m, "blockNumber"), BlockHash: asString(m, "blockHash"),
		TxHash: asString(m, "transactionHash"), LogIndex: asUint32(m, "logIndex"),
		Address: asString(m, "address"), Topics: topics, Data: asString(m, "data"),
		Removed: removed, Timestamp: asTimeMs(m, "timestamp"), Provider: provider,
	}, nil
}

type BalanceRow struct {
	Chain        string
	Address      string
	TokenSymbol  string
	TokenName    string
	TokenAddress string
	TokenType    string
	TokenDecimals uint16
	BalanceRaw   string
	Balance      *big.Int
	BalanceUSD   string
	TokenPrice   string
	SnapshotBlock uint64
	Timestamp    time.Time
	Provider     string
}

func NormalizeBalance(raw json.RawMessage, chain, address string, snapshot uint64, provider string) (BalanceRow, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return BalanceRow{}, err
	}
	return BalanceRow{
		Chain: chain, Address: asString(m, "holderAddress"), TokenSymbol: asString(m, "tokenSymbol"),
		TokenName: asString(m, "tokenName"), TokenAddress: asString(m, "tokenAddress"),
		TokenType: asString(m, "tokenType"), TokenDecimals: uint16(asUint64(m, "tokenDecimals")),
		BalanceRaw: asString(m, "balanceRawInteger"), Balance: asBigInt(m, "balanceRawInteger"),
		BalanceUSD: asString(m, "balanceUsd"), TokenPrice: asString(m, "tokenPrice"),
		SnapshotBlock: snapshot, Timestamp: time.Now().UTC(), Provider: provider,
	}, nil
}
