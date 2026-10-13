package method

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// ---- Transaction Detail Service（§23/§25/§50）----
//
// 智能调查、资金分析、前端详情页都必须经过本服务，不允许绕过它直接调用 Provider（§50）。

// ErrTransactionNotFound 本地没有这笔交易（未下载）。
var ErrTransactionNotFound = errors.New("transaction not found in local dataset")

// DecodeMode 解码模式（§25）。
type DecodeMode string

const (
	DecodeLocalOnly    DecodeMode = "local_only"
	DecodeAuto         DecodeMode = "auto"
	DecodeForceRefresh DecodeMode = "force_refresh"
)

// ParseDecodeMode 解析 ?decode= 参数，默认 auto。
func ParseDecodeMode(value string) (DecodeMode, error) {
	switch DecodeMode(strings.TrimSpace(strings.ToLower(value))) {
	case "", DecodeAuto:
		return DecodeAuto, nil
	case DecodeLocalOnly:
		return DecodeLocalOnly, nil
	case DecodeForceRefresh:
		return DecodeForceRefresh, nil
	default:
		return "", fmt.Errorf("decode must be local_only, auto or force_refresh")
	}
}

// DecodedRecord transaction_decoded_inputs 一行（§22）。
type DecodedRecord struct {
	Chain           string    `json:"chain"`
	TxHash          string    `json:"tx_hash"`
	MethodID        string    `json:"method_id"`
	MethodName      string    `json:"method_name"`
	MethodSignature string    `json:"method_signature"`
	DecodedJSON     string    `json:"decoded_json"`
	DecodedLogsJSON string    `json:"decoded_logs_json"`
	DecodeSource    string    `json:"decode_source"`
	Verified        bool      `json:"verified"`
	DecodedAt       time.Time `json:"decoded_at"`
}

// TxDataStore 数据面访问（ClickHouse）。接口化以便单测替换。
type TxDataStore interface {
	GetTransaction(ctx context.Context, chain, txHash string) (map[string]any, error)
	GetLogs(ctx context.Context, chain, txHash string) ([]map[string]any, error)
	GetDecodedInput(ctx context.Context, chain, txHash string) (*DecodedRecord, error)
	PutDecodedInput(ctx context.Context, record DecodedRecord) error
	UpdateTransactionMethod(ctx context.Context, chain, txHash string, resolution MethodResolution) error
}

// DeepDecodeUsage 一次深度解码的用量记账（§49）。
type DeepDecodeUsage struct {
	Chain      string
	TxHash     string
	Method     string
	Status     string
	DurationMs int
	CacheHit   bool
	Error      string
	Priority   string
}

// UsageRecorder 由 API 层注入，写 provider_usage。
type UsageRecorder func(usage DeepDecodeUsage)

// MethodInfo API 返回的 Method 信息（§54）。
type MethodInfo struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Signature  string            `json:"signature"`
	Source     string            `json:"source"`
	Verified   bool              `json:"verified"`
	Confidence string            `json:"confidence"`
	Type       string            `json:"method_type"`
	Category   string            `json:"category"`
	Risk       bool              `json:"risk"`
	Candidates []MethodCandidate `json:"candidates,omitempty"`
}

// MethodInfoFrom 由解析结果构造展示信息。
func MethodInfoFrom(res MethodResolution) MethodInfo {
	return MethodInfo{
		ID: res.MethodID, Name: res.Name, Signature: res.Signature,
		Source: res.Source, Verified: res.Verified, Confidence: res.Confidence,
		Type: res.MethodType, Category: Category(res.Name), Risk: IsRiskMethod(res.Name),
		Candidates: res.Candidates,
	}
}

// TransactionDetail 交易详情（§42/§54）。
type TransactionDetail struct {
	Chain          string           `json:"chain"`
	TxHash         string           `json:"tx_hash"`
	Transaction    map[string]any   `json:"transaction"`
	Input          string           `json:"input"`
	Method         MethodInfo       `json:"method"`
	DecodedInput   map[string]any   `json:"decoded_input,omitempty"`
	DecodedParams  []DecodedParam   `json:"decoded_params,omitempty"`
	DecodeSource   string           `json:"decode_source"`
	DecodeVerified bool             `json:"decode_verified"`
	DecodeCacheHit bool             `json:"decode_cache_hit"`
	DecodedAt      *time.Time       `json:"decoded_at,omitempty"`
	Logs           []map[string]any `json:"logs"`
	DecodedLogs    []DecodedLog     `json:"decoded_logs,omitempty"`
	DecodeError    string           `json:"decode_error,omitempty"`
}

// DetailService 交易详情 + 按需深度解码（§25）。
type DetailService struct {
	Data     TxDataStore
	Registry *Registry
	ABI      *ABIResolver
	Deep     *AnkrDeepResolver
	Usage    UsageRecorder
	Log      *slog.Logger
}

func NewDetailService(data TxDataStore, registry *Registry, deep *AnkrDeepResolver, usage UsageRecorder, log *slog.Logger) *DetailService {
	return &DetailService{Data: data, Registry: registry, ABI: NewABIResolver(registry), Deep: deep, Usage: usage, Log: log}
}

func (s *DetailService) LocalTransaction(ctx context.Context, chain, hash string) (map[string]any, error) {
	return s.Data.GetTransaction(ctx, chain, hash)
}

// Detail 返回交易详情。
//
//	local_only    → 只读本地（不产生任何 Provider 请求）
//	auto          → 本地 decode 缓存命中直接返回；否则先本地 ABI 解码，仍失败才调用 Provider
//	force_refresh → 跳过缓存强制重新深度解码
func (s *DetailService) Detail(ctx context.Context, chain, txHash string, mode DecodeMode) (*TransactionDetail, error) {
	chain = strings.ToLower(strings.TrimSpace(chain))
	txHash = strings.TrimSpace(txHash)
	if chain != "solana" {
		txHash = strings.ToLower(txHash)
	}
	if chain == "" || txHash == "" {
		return nil, fmt.Errorf("chain and tx_hash are required")
	}
	row, err := s.Data.GetTransaction(ctx, chain, txHash)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, ErrTransactionNotFound
	}
	return s.DetailFromRow(ctx, chain, txHash, row, mode)
}

// DetailFromRow shares the established ABI/detail pipeline with a verified RPC
// transaction. It never creates a download or inserts an unverified local row.
func (s *DetailService) DetailFromRow(ctx context.Context, chain, txHash string, row map[string]any, mode DecodeMode) (*TransactionDetail, error) {
	if row == nil {
		return nil, ErrTransactionNotFound
	}

	detail := &TransactionDetail{
		Chain: chain, TxHash: txHash, Transaction: row,
		Input:  stringField(row, "input"),
		Method: methodInfoFromRow(row),
	}
	contract := stringField(row, "to_address")

	logs, err := s.Data.GetLogs(ctx, chain, txHash)
	if err != nil {
		s.warn("load logs failed", err)
	}
	detail.Logs = s.annotateLogs(chain, logs)

	if mode != DecodeLocalOnly {
		DeepDecodeRequestsTotal.Inc()
	}

	// 1) 本地 Decode 缓存优先（§62-9）
	if mode != DecodeForceRefresh {
		if cached, err := s.Data.GetDecodedInput(ctx, chain, txHash); err == nil && cached != nil {
			DeepDecodeCacheHitTotal.Inc()
			detail.applyCached(cached)
			s.recordUsage(DeepDecodeUsage{Chain: chain, TxHash: txHash, Method: "cache", Status: "ok", CacheHit: true, Priority: "interactive"})
			return detail, nil
		}
	}

	// 2) 本地 ABI 解码（零成本，永远优先于 Provider）
	if mode != DecodeForceRefresh {
		if m, params, ok := s.ABI.DecodeInput(chain, contract, detail.Input); ok {
			record := DecodedRecord{
				Chain: chain, TxHash: txHash, MethodID: m.Selector, MethodName: m.Name,
				MethodSignature: m.Signature, DecodedJSON: encodeParams(params),
				DecodeSource: SourceContractABI, Verified: detail.Method.Verified, DecodedAt: time.Now().UTC(),
			}
			if err := s.Data.PutDecodedInput(ctx, record); err != nil {
				s.warn("persist decoded input failed", err)
			}
			detail.applyParams(params, SourceContractABI, detail.Method.Verified, record.DecodedAt)
			return detail, nil
		}
	}

	if mode == DecodeLocalOnly {
		return detail, nil
	}

	// 没有 Selector 就没有参数可解（原生转账 / 空调用 / 合约创建，§38）：
	// 绝不为这类交易浪费一次 Provider 深度解码额度（§24）。
	if detail.Method.Type != "" && detail.Method.Type != TypeContractCall {
		return detail, nil
	}

	// 3) Provider 深度解码（按需，且必须记账）
	if s.Deep == nil || !s.Deep.Available() {
		detail.DecodeError = "deep decode provider not configured"
		return detail, nil
	}
	started := time.Now()
	result, err := s.Deep.Decode(ctx, chain, txHash)
	usage := DeepDecodeUsage{
		Chain: chain, TxHash: txHash, Method: "ankr_getTransactionsByHash",
		DurationMs: int(time.Since(started).Milliseconds()), CacheHit: false, Status: "ok", Priority: "interactive",
	}
	if err != nil {
		DeepDecodeErrorTotal.Inc()
		usage.Status = "error"
		usage.Error = err.Error()
		s.recordUsage(usage)
		detail.DecodeError = err.Error()
		return detail, nil
	}
	s.recordUsage(usage)
	ResolveAnkrTotal.Inc()

	record := DecodedRecord{
		Chain: chain, TxHash: txHash, MethodID: result.MethodID, MethodName: result.Name,
		MethodSignature: result.Signature, DecodedJSON: encodeParams(result.Params),
		DecodedLogsJSON: encodeJSON(result.Logs), DecodeSource: SourceAnkr,
		Verified: result.Verified, DecodedAt: time.Now().UTC(),
	}
	if err := s.Data.PutDecodedInput(ctx, record); err != nil {
		s.warn("persist decoded input failed", err)
	}
	detail.applyParams(result.Params, SourceAnkr, result.Verified, record.DecodedAt)
	detail.DecodedLogs = result.Logs

	// Provider 解码结果优先级高于本地签名匹配（§30）：回写 transactions 的轻量 Method 字段
	if resolution := result.DeepResolution(); shouldReplaceMethod(detail.Method, resolution) {
		if err := s.Data.UpdateTransactionMethod(ctx, chain, txHash, resolution); err != nil {
			s.warn("update transaction method failed", err)
		} else {
			detail.Method = MethodInfoFrom(resolution)
			detail.Transaction = applyMethodToRow(detail.Transaction, resolution)
		}
	}
	return detail, nil
}

func shouldReplaceMethod(current MethodInfo, candidate MethodResolution) bool {
	if !candidate.Resolved() {
		return false
	}
	if Better(candidate.Source, current.Source) {
		return true
	}
	if candidate.Source != current.Source {
		return false
	}
	return candidate.MethodID != current.ID || candidate.Name != current.Name ||
		candidate.Signature != current.Signature || candidate.Verified != current.Verified ||
		candidate.Confidence != current.Confidence
}

// annotateLogs 事件名解析（§29）：先查合约 ABI，再查本地事件签名库。
func (s *DetailService) annotateLogs(chain string, logs []map[string]any) []map[string]any {
	for _, log := range logs {
		topics := stringSlice(log["topics"])
		if len(topics) == 0 {
			continue
		}
		address := stringField(log, "address")
		if event, params, ok := s.ABI.DecodeLog(chain, address, topics, stringField(log, "data")); ok {
			log["event_name"] = event.Name
			log["event_signature"] = event.Signature
			log["event_source"] = SourceContractABI
			log["event_verified"] = true
			log["event_params"] = ParamsToMap(params)
			continue
		}
		if record, ok := s.Registry.EventSignature(strings.ToLower(topics[0])); ok {
			log["event_name"] = record.Name
			log["event_signature"] = record.Signature
			log["event_source"] = record.Source
			log["event_verified"] = false
		}
	}
	return logs
}

func (s *DetailService) recordUsage(usage DeepDecodeUsage) {
	if s.Usage != nil {
		s.Usage(usage)
	}
}

// LocalLogs 只读本地日志，**不要求 transactions 行存在**。
//
// 为什么需要它：`Detail` 在本地没有交易行时直接返回 ErrTransactionNotFound，
// 而 transactions 与 logs 是两个独立数据集 —— 只下载了逐笔/日志的地址上，
// 交易行缺失但日志是真实存在的证据，不该跟着 404 一起被丢掉。
func (s *DetailService) LocalLogs(ctx context.Context, chain, txHash string) []map[string]any {
	chain = strings.ToLower(strings.TrimSpace(chain))
	txHash = strings.ToLower(strings.TrimSpace(txHash))
	logs, err := s.Data.GetLogs(ctx, chain, txHash)
	if err != nil {
		s.warn("load logs failed", err)
		return []map[string]any{}
	}
	annotated := s.annotateLogs(chain, logs)
	if annotated == nil {
		return []map[string]any{}
	}
	return annotated
}

// PartialDetail 构造「交易行缺失、但有本地证据」的详情骨架：
// Transaction 为 nil（调用方序列化成 null，页面按缺省值渲染），日志照常返回。
// 是否算「有证据」由调用方决定（它才知道逐笔/内部调用是否命中）。
func (s *DetailService) PartialDetail(ctx context.Context, chain, txHash string) *TransactionDetail {
	chain = strings.ToLower(strings.TrimSpace(chain))
	txHash = strings.ToLower(strings.TrimSpace(txHash))
	return &TransactionDetail{
		Chain:  chain,
		TxHash: txHash,
		Logs:   s.LocalLogs(ctx, chain, txHash),
	}
}

func (s *DetailService) warn(message string, err error) {
	if s.Log != nil {
		s.Log.Warn(message, "err", err)
	}
}

func (d *TransactionDetail) applyCached(record *DecodedRecord) {
	d.DecodeSource = record.DecodeSource
	d.DecodeVerified = record.Verified
	d.DecodeCacheHit = true
	decodedAt := record.DecodedAt
	d.DecodedAt = &decodedAt
	params := decodeParams(record.DecodedJSON)
	d.DecodedParams = params
	if len(params) > 0 {
		d.DecodedInput = ParamsToMap(params)
	}
	if record.DecodedLogsJSON != "" {
		var logs []DecodedLog
		if err := json.Unmarshal([]byte(record.DecodedLogsJSON), &logs); err == nil {
			d.DecodedLogs = logs
		}
	}
	// 缓存里的方法名比 transactions 行更精确时用它补齐展示
	if d.Method.Name == "" && record.MethodName != "" {
		d.Method.Name = record.MethodName
		d.Method.Signature = record.MethodSignature
		d.Method.Source = record.DecodeSource
		d.Method.Verified = record.Verified
		d.Method.Category = Category(record.MethodName)
		d.Method.Risk = IsRiskMethod(record.MethodName)
	}
}

func (d *TransactionDetail) applyParams(params []DecodedParam, source string, verified bool, at time.Time) {
	d.DecodedParams = params
	if len(params) > 0 {
		d.DecodedInput = ParamsToMap(params)
	}
	d.DecodeSource = source
	d.DecodeVerified = verified
	d.DecodeCacheHit = false
	decodedAt := at
	d.DecodedAt = &decodedAt
}

func encodeParams(params []DecodedParam) string {
	if len(params) == 0 {
		return ""
	}
	return encodeJSON(map[string]any{"params": params})
}

func decodeParams(raw string) []DecodedParam {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var wrapper struct {
		Params []DecodedParam `json:"params"`
	}
	if err := json.Unmarshal([]byte(raw), &wrapper); err != nil {
		return nil
	}
	return wrapper.Params
}

func encodeJSON(value any) string {
	if value == nil {
		return ""
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(raw)
}

func stringField(row map[string]any, key string) string {
	if row == nil {
		return ""
	}
	value, ok := row[key]
	if !ok || value == nil {
		return ""
	}
	if s, ok := value.(string); ok {
		return s
	}
	return fmt.Sprint(value)
}

func stringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			out = append(out, fmt.Sprint(item))
		}
		return out
	default:
		return nil
	}
}

// methodInfoFromRow 由 ClickHouse 行构造 Method 展示信息。
func methodInfoFromRow(row map[string]any) MethodInfo {
	source := stringField(row, "method_source")
	if source == "" {
		source = SourceUnknown
	}
	confidence := stringField(row, "method_confidence")
	if confidence == "" {
		confidence = ConfidenceUnknown
	}
	verified := false
	switch typed := row["method_verified"].(type) {
	case bool:
		verified = typed
	case float64:
		verified = typed != 0
	case uint8:
		verified = typed != 0
	default:
		verified = stringField(row, "method_verified") == "1"
	}
	name := stringField(row, "method_name")
	return MethodInfo{
		ID: stringField(row, "method_id"), Name: name,
		Signature: stringField(row, "method_signature"), Source: source,
		Verified: verified, Confidence: confidence, Type: stringField(row, "method_type"),
		Category: Category(name), Risk: IsRiskMethod(name),
	}
}

func applyMethodToRow(row map[string]any, resolution MethodResolution) map[string]any {
	if row == nil {
		return nil
	}
	row["method_id"] = resolution.MethodID
	row["method_name"] = resolution.Name
	row["method_signature"] = resolution.Signature
	row["method_source"] = resolution.Source
	row["method_confidence"] = resolution.Confidence
	if resolution.Verified {
		row["method_verified"] = 1
	} else {
		row["method_verified"] = 0
	}
	return row
}
