// Package ingest：Ankr 原始行 → 标准化 ClickHouse 行。
// 兼容两种字段形态：Ankr 官方文档形态（camelCase / ms 时间戳 / blockHeight）与
// 实测 RPC 风格（blockNumber/hash/from/to/十六进制秒级时间戳）。
package ingest

import (
	"encoding/json"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/method"
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

func pickString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s := asString(m, k); s != "" {
			return s
		}
	}
	return ""
}

// pickAddress 地址统一小写（Ankr 返回校验和大小写混合，查询需一致）。
func pickAddress(m map[string]any, keys ...string) string {
	return strings.ToLower(pickString(m, keys...))
}

// RawTimestamp 提取原始行的时间戳。
func RawTimestamp(raw json.RawMessage) (time.Time, bool) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return time.Time{}, false
	}
	if _, ok := m["timestamp"]; ok {
		return asTime(m, "timestamp"), true
	}
	if _, ok := m["block_timestamp"]; ok {
		return asTime(m, "block_timestamp"), true
	}
	return time.Time{}, false
}

// RawBlockNumber 提取原始行的块号（用于客户端侧范围过滤与降序停止）。
func RawBlockNumber(raw json.RawMessage) (uint64, bool) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return 0, false
	}
	for _, k := range []string{"blockNumber", "blockHeight", "block_number"} {
		if s := asString(m, k); s != "" {
			base := 10
			if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
				base = 16
				s = s[2:]
			}
			if n, err := strconv.ParseUint(s, base, 64); err == nil {
				return n, true
			}
		}
	}
	return 0, false
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

func fromUnixN(n float64) time.Time {
	// 阈值 1e11：以上为毫秒，以下为秒
	if n > 1e11 {
		return time.UnixMilli(int64(n)).UTC()
	}
	if n > 1e8 {
		return time.Unix(int64(n), 0).UTC()
	}
	return time.Now().UTC()
}

// asTime 时间戳兼容：秒 / 毫秒 / 十进制字符串 / 十六进制(0x)秒。
func asTime(m map[string]any, key string) time.Time {
	v, ok := m[key]
	if !ok || v == nil {
		return time.Now().UTC()
	}
	switch t := v.(type) {
	case float64:
		return fromUnixN(t)
	case json.Number:
		n, _ := t.Float64()
		return fromUnixN(n)
	case string:
		lower := strings.ToLower(t)
		if strings.HasPrefix(lower, "0x") {
			if n, err := strconv.ParseUint(lower[2:], 16, 64); err == nil {
				return fromUnixN(float64(n))
			}
		}
		if n, err := strconv.ParseFloat(t, 64); err == nil {
			return fromUnixN(n)
		}
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
	Chain        string
	BlockNumber  uint64
	BlockHash    string
	TxHash       string
	Timestamp    time.Time
	From         string
	To           string
	ValueRaw     string
	Nonce        uint64
	Gas          uint64
	GasUsed      uint64
	GasPrice     string
	TxType       string
	Status       uint8
	Input        string
	MethodID     string
	Provider     string
	NativeSymbol string
	Confirmation string
	RawType      string
	RawStatus    string

	// V1.3 Method 解析结果（§7）。原始 input 永远完整保留（§6/§62-2）。
	MethodName       string
	MethodSignature  string
	MethodSource     string
	MethodVerified   uint8
	MethodConfidence string
	MethodType       string
	MethodResolvedAt *time.Time
}

// ApplyMethod 写入 Method 解析结果（§16：Normalize 之后、入库之前）。
func (r *TxRow) ApplyMethod(res method.MethodResolution) {
	if res.MethodID != "" {
		r.MethodID = res.MethodID
	}
	if res.MethodType != "" {
		r.MethodType = res.MethodType
	}
	r.MethodName = res.Name
	r.MethodSignature = res.Signature
	r.MethodSource = res.Source
	if r.MethodSource == "" {
		r.MethodSource = method.SourceUnknown
	}
	r.MethodConfidence = res.Confidence
	if r.MethodConfidence == "" {
		r.MethodConfidence = method.ConfidenceUnknown
	}
	r.MethodVerified = 0
	if res.Verified {
		r.MethodVerified = 1
	}
	if res.Resolved() && !res.ResolvedAt.IsZero() {
		resolvedAt := res.ResolvedAt
		r.MethodResolvedAt = &resolvedAt
	} else {
		r.MethodResolvedAt = nil
	}
}

func NormalizeTransaction(raw json.RawMessage, chain, provider string) (TxRow, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return TxRow{}, err
	}
	status := asString(m, "status")
	r := TxRow{
		Chain:       chain,
		BlockNumber: asUint64(m, "blockNumber"),
		BlockHash:   pickString(m, "blockHash"),
		TxHash:      pickString(m, "hash", "transactionHash"),
		Timestamp:   asTime(m, "timestamp"),
		From:        pickAddress(m, "from", "fromAddress"),
		To:          pickAddress(m, "to", "toAddress"),
		ValueRaw:    pickString(m, "value"),
		Nonce:       asUint64(m, "nonce"),
		Gas:         asUint64(m, "gasLimit"),
		GasUsed:     asUint64(m, "gasUsed"),
		GasPrice:    pickString(m, "gasPrice"),
		TxType:      pickString(m, "type"),
		Input:       pickString(m, "input", "inputData"),
		Provider:    provider,
	}
	if r.BlockNumber == 0 {
		r.BlockNumber = asUint64(m, "blockHeight")
	}
	if r.Gas == 0 {
		r.Gas = asUint64(m, "gas")
	}
	if status == "0x1" || status == "1" || status == "true" {
		r.Status = 1
	}
	// §5：Method Selector 一律从 input 本地提取并做 Hex 校验，
	// 不信任 Provider 自带的 methodId；to = null 走 contract_creation 分类（§38）。
	methodType, selector := method.Classify(r.Input, r.ValueRaw, r.To == "")
	r.MethodType = methodType
	r.MethodID = selector
	r.MethodSource = method.SourceUnknown
	r.MethodConfidence = method.ConfidenceUnknown
	return r, nil
}

type TokenTransferRow struct {
	Chain         string
	BlockNumber   uint64
	Timestamp     time.Time
	TxHash        string
	LogIndex      uint32
	TokenAddress  string
	TokenSymbol   string
	TokenDecimals uint16
	From          string
	To            string
	AmountRaw     string
	Amount        *big.Int
	Provider      string
}

// InternalTransferRow reuses blockchain.internal_calls for TRON internal
// value movements. Rejected records are persisted for audit but are excluded
// from address_activity by the download normalizer.
type InternalTransferRow struct {
	Chain       string
	TxHash      string
	CallIndex   uint32
	BlockNumber uint64
	Timestamp   time.Time
	From        string
	To          string
	ValueRaw    string
	AssetType   string
	CallType    string
	Rejected    bool
	Provider    string
}

func NormalizeTokenTransfer(raw json.RawMessage, chain, provider string) (TokenTransferRow, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return TokenTransferRow{}, err
	}
	// 实测字段：valueRawInteger（原始整数）；value 为展示值
	amountRaw := pickString(m, "valueRawInteger", "value")
	return TokenTransferRow{
		Chain: chain, BlockNumber: asUint64(m, "blockHeight"), Timestamp: asTime(m, "timestamp"),
		TxHash: pickString(m, "transactionHash"), LogIndex: asUint32(m, "logIndex"),
		TokenAddress: pickAddress(m, "contractAddress"), TokenSymbol: pickString(m, "tokenSymbol"),
		TokenDecimals: uint16(asUint64(m, "tokenDecimals")),
		From:          pickAddress(m, "fromAddress", "from"), To: pickAddress(m, "toAddress", "to"),
		AmountRaw: amountRaw, Amount: asBigIntValue(m, "valueRawInteger", "value"), Provider: provider,
	}, nil
}

func asBigIntValue(m map[string]any, keys ...string) *big.Int {
	for _, k := range keys {
		if s := asString(m, k); s != "" {
			return asBigInt(m, k)
		}
	}
	return big.NewInt(0)
}

type NftTransferRow struct {
	Chain       string
	BlockNumber uint64
	Timestamp   time.Time
	TxHash      string
	LogIndex    uint32
	Contract    string
	TokenID     string
	From        string
	To          string
	TokenName   string
	TokenSymbol string
	AmountRaw   string
	Provider    string
}

func NormalizeNftTransfer(raw json.RawMessage, chain, provider string) (NftTransferRow, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return NftTransferRow{}, err
	}
	return NftTransferRow{
		Chain: chain, BlockNumber: asUint64(m, "blockHeight"), Timestamp: asTime(m, "timestamp"),
		TxHash: pickString(m, "transactionHash"), LogIndex: asUint32(m, "logIndex"),
		Contract: pickAddress(m, "contractAddress"), TokenID: pickString(m, "tokenId"),
		From: pickAddress(m, "fromAddress", "from"), To: pickAddress(m, "toAddress", "to"),
		TokenName:   pickString(m, "name", "collectionName", "tokenName"),
		TokenSymbol: pickString(m, "collectionSymbol", "tokenSymbol"),
		AmountRaw:   pickString(m, "value"), Provider: provider,
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

	// V1.3 事件解析预留（§29）：topic0 → event name/signature
	EventName      string
	EventSignature string
	EventSource    string
	EventVerified  uint8
}

// ApplyEvent 写入事件签名解析结果（§29）。
func (r *LogRow) ApplyEvent(name, signature, source string, verified bool) {
	r.EventName = name
	r.EventSignature = signature
	r.EventSource = source
	r.EventVerified = 0
	if verified {
		r.EventVerified = 1
	}
}

func NormalizeLog(raw json.RawMessage, chain, provider string) (LogRow, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return LogRow{}, err
	}
	var topics []string
	if v, ok := m["topics"].([]any); ok {
		for _, t := range v {
			if s, ok := t.(string); ok {
				topics = append(topics, s)
			}
		}
	}
	removed := uint8(0)
	if asBool(m, "removed") {
		removed = 1
	}
	return LogRow{
		Chain: chain, BlockNumber: asUint64(m, "blockNumber"), BlockHash: pickString(m, "blockHash"),
		TxHash: pickString(m, "transactionHash"), LogIndex: asUint32(m, "logIndex"),
		Address: pickAddress(m, "address"), Topics: topics, Data: pickString(m, "data"),
		Removed: removed, Timestamp: asTime(m, "timestamp"), Provider: provider,
	}, nil
}

// ActivityRow 地址活动汇总（§65/§87）。
type ActivityRow struct {
	Chain         string
	Address       string
	Counterparty  string
	Direction     string // in / out / self
	ActivityType  string // transaction / transfer
	Timestamp     time.Time
	BlockNumber   uint64
	TxHash        string
	LogIndex      uint32
	TokenAddress  string
	TokenSymbol   string
	TokenDecimals uint16
	AmountRaw     string
	Amount        *big.Int
	AmountUSD     string
	Dataset       string
	Provider      string
}

type BalanceRow struct {
	Chain         string
	Address       string
	TokenSymbol   string
	TokenName     string
	TokenAddress  string
	TokenType     string
	TokenDecimals uint16
	BalanceRaw    string
	Balance       *big.Int
	BalanceUSD    string
	TokenPrice    string
	SnapshotBlock uint64
	Timestamp     time.Time
	Provider      string
}

func NormalizeBalance(raw json.RawMessage, chain, address string, snapshot uint64, provider string) (BalanceRow, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return BalanceRow{}, err
	}
	holder := pickAddress(m, "holderAddress")
	if holder == "" {
		holder = address
	}
	return BalanceRow{
		Chain: chain, Address: holder, TokenSymbol: pickString(m, "tokenSymbol"),
		TokenName: pickString(m, "tokenName"), TokenAddress: pickAddress(m, "contractAddress"),
		TokenType: pickString(m, "tokenType"), TokenDecimals: uint16(asUint64(m, "tokenDecimals")),
		BalanceRaw: pickString(m, "balanceRawInteger", "balance"), Balance: asBigIntValue(m, "balanceRawInteger"),
		BalanceUSD: pickString(m, "balanceUsd"), TokenPrice: pickString(m, "tokenPrice"),
		SnapshotBlock: snapshot, Timestamp: time.Now().UTC(), Provider: provider,
	}, nil
}
