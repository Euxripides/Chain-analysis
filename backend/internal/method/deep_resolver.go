package method

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ---- 单笔深度解析（§23/§24/§40）----
//
// 硬约束：
//   - 只允许"用户打开交易详情 / 智能调查重点交易 / 报告需要参数级分析"触发；
//   - 禁止批量交易自动逐笔 Deep Decode（§62-1）；
//   - 必须先查本地缓存（§62-9）；
//   - 每次真实请求都要记 provider_usage（§62-10）。

// RawTxDetailFetcher Provider 侧单笔深度接口（ankr_getTransactionsByHash）。
// 由调用方注入，method 包不依赖任何具体 Provider。
type RawTxDetailFetcher interface {
	TxDetail(ctx context.Context, chain, txHash string, decodeTxData, decodeLogs bool) (json.RawMessage, error)
}

// DecodedLog 已解码事件日志（§23）。
type DecodedLog struct {
	LogIndex       uint32         `json:"log_index"`
	Address        string         `json:"address"`
	Topic0         string         `json:"topic0"`
	EventName      string         `json:"event_name"`
	EventSignature string         `json:"event_signature"`
	Source         string         `json:"event_source"`
	Verified       bool           `json:"event_verified"`
	Params         []DecodedParam `json:"params,omitempty"`
}

// DeepDecodeResult Provider 深度解码结果。
type DeepDecodeResult struct {
	MethodID  string         `json:"method_id"`
	Name      string         `json:"name"`
	Signature string         `json:"signature"`
	Verified  bool           `json:"verified"`
	Params    []DecodedParam `json:"params,omitempty"`
	Logs      []DecodedLog   `json:"logs,omitempty"`
}

// AnkrDeepResolver 解析 ankr_getTransactionsByHash 的解码结果（§23）。
type AnkrDeepResolver struct {
	Fetcher RawTxDetailFetcher
}

func NewAnkrDeepResolver(fetcher RawTxDetailFetcher) *AnkrDeepResolver {
	return &AnkrDeepResolver{Fetcher: fetcher}
}

func (d *AnkrDeepResolver) Available() bool { return d != nil && d.Fetcher != nil }

// Decode 请求一次深度解码。调用方负责缓存与用量记账。
func (d *AnkrDeepResolver) Decode(ctx context.Context, chain, txHash string) (DeepDecodeResult, error) {
	if !d.Available() {
		return DeepDecodeResult{}, fmt.Errorf("deep decode provider not configured")
	}
	raw, err := d.Fetcher.TxDetail(ctx, chain, txHash, true, true)
	if err != nil {
		return DeepDecodeResult{}, err
	}
	return ParseAnkrTxDetail(raw)
}

// ---- Ankr 响应解析 ----

type ankrDecodedInput struct {
	Name         string          `json:"name"`
	Type         string          `json:"type"`
	Size         int             `json:"size"`
	Value        json.RawMessage `json:"value"`
	ValueDecoded json.RawMessage `json:"valueDecoded"`
	Indexed      bool            `json:"indexed"`
}

type ankrMethod struct {
	ID        string             `json:"id"`
	Name      string             `json:"name"`
	String    string             `json:"string"`
	Signature string             `json:"signature"`
	Verified  bool               `json:"verified"`
	Inputs    []ankrDecodedInput `json:"inputs"`
}

type ankrEvent struct {
	Name      string             `json:"name"`
	String    string             `json:"string"`
	Signature string             `json:"signature"`
	Verified  bool               `json:"verified"`
	Inputs    []ankrDecodedInput `json:"inputs"`
}

type ankrLog struct {
	Address  string     `json:"address"`
	Topics   []string   `json:"topics"`
	Data     string     `json:"data"`
	LogIndex string     `json:"logIndex"`
	Event    *ankrEvent `json:"event"`
}

type ankrTxDetail struct {
	Hash   string      `json:"hash"`
	Method *ankrMethod `json:"method"`
	Logs   []ankrLog   `json:"logs"`
}

type ankrTxDetailResult struct {
	Transactions []ankrTxDetail `json:"transactions"`
}

// ParseAnkrTxDetail 将 Ankr 深度解码响应转成统一结构。
// 数值一律保持字符串，绝不经过 float64（§41/§62-13）。
func ParseAnkrTxDetail(raw json.RawMessage) (DeepDecodeResult, error) {
	var parsed ankrTxDetailResult
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return DeepDecodeResult{}, fmt.Errorf("parse tx detail: %w", err)
	}
	if len(parsed.Transactions) == 0 {
		return DeepDecodeResult{}, fmt.Errorf("transaction not found at provider")
	}
	tx := parsed.Transactions[0]
	out := DeepDecodeResult{}
	if tx.Method != nil {
		out.MethodID = normalizeProviderMethodID(tx.Method.ID)
		out.Name = tx.Method.Name
		out.Signature = normalizeProviderSignature(tx.Method.Signature, tx.Method.Name)
		if out.Signature == "" {
			out.Signature = normalizeProviderSignature(tx.Method.String, tx.Method.Name)
		}
		out.Verified = tx.Method.Verified
		out.Params = convertAnkrInputs(tx.Method.Inputs)
		if out.Name == "" && out.Signature != "" {
			out.Name = NameOf(out.Signature)
		}
		// 自洽校验：签名算出的 selector 必须与 method id 一致，否则不采信签名（§62-20）
		if out.Signature != "" && out.MethodID != "" && SelectorOf(out.Signature) != out.MethodID {
			out.Signature = ""
			out.Verified = false
		}
	}
	for _, log := range tx.Logs {
		item := DecodedLog{Address: strings.ToLower(log.Address), LogIndex: parseHexOrDecUint32(log.LogIndex)}
		if len(log.Topics) > 0 {
			item.Topic0 = strings.ToLower(log.Topics[0])
		}
		if log.Event != nil {
			item.EventName = log.Event.Name
			item.EventSignature = normalizeProviderSignature(log.Event.Signature, log.Event.Name)
			if item.EventSignature == "" {
				item.EventSignature = normalizeProviderSignature(log.Event.String, log.Event.Name)
			}
			item.Verified = log.Event.Verified
			item.Params = convertAnkrInputs(log.Event.Inputs)
			item.Source = SourceAnkr
			if item.EventSignature != "" && item.Topic0 != "" && Topic0Of(item.EventSignature) != item.Topic0 {
				item.EventSignature = ""
				item.Verified = false
			}
		}
		out.Logs = append(out.Logs, item)
	}
	return out, nil
}

// normalizeProviderSignature Ankr 的 string 字段有时是 "transfer(address to, uint256 value)"，
// 需要去掉参数名才是规范签名（keccak 计算依赖规范形式）。
func normalizeProviderSignature(raw, name string) string {
	sig := strings.TrimSpace(raw)
	if sig == "" {
		return ""
	}
	open := strings.IndexByte(sig, '(')
	if open < 0 {
		return ""
	}
	// Ankr 的 string 形态可能带 "function " 前缀和 " returns(...)" 后缀。
	close := matchingParen(sig, open)
	if close < 0 {
		return ""
	}
	head := strings.TrimSpace(sig[:open])
	head = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(head, "function "), "event "))
	if head == "" {
		head = name
	}
	parts := splitTopLevel(sig[open+1 : close])
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		field := strings.TrimSpace(part)
		if field == "" {
			continue
		}
		// "uint256 value" / "address indexed from" → 取第一个 token
		if space := strings.IndexByte(field, ' '); space > 0 {
			field = field[:space]
		}
		cleaned = append(cleaned, field)
	}
	return head + "(" + strings.Join(cleaned, ",") + ")"
}

func matchingParen(value string, open int) int {
	depth := 0
	for i := open; i < len(value); i++ {
		switch value[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// Ankr 实测 method.id 可能是 32 字节左侧补零，而不是 4 字节字面量。
func normalizeProviderMethodID(raw string) string {
	if selector := NormalizeSelector(raw); selector != "" {
		return selector
	}
	body := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(raw), "0x"), "0X")
	if len(body) == 64 && isHexDigits(body) && strings.Trim(body[:56], "0") == "" {
		return "0x" + strings.ToLower(body[56:])
	}
	return ""
}

func convertAnkrInputs(inputs []ankrDecodedInput) []DecodedParam {
	out := make([]DecodedParam, 0, len(inputs))
	for i, in := range inputs {
		name := in.Name
		if name == "" {
			name = "arg" + strconv.Itoa(i)
		}
		value := in.ValueDecoded
		if len(value) == 0 {
			value = in.Value
		}
		out = append(out, DecodedParam{Name: name, Type: in.Type, Value: normalizeJSONValue(value)})
	}
	return out
}

// normalizeJSONValue 数值安全：所有 JSON number 转字符串保留原始精度（§41）。
func normalizeJSONValue(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return strings.Trim(string(raw), `"`)
	}
	return stringifyNumbers(value)
}

func stringifyNumbers(value any) any {
	switch typed := value.(type) {
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = stringifyNumbers(item)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(typed))
		for k, v := range typed {
			out[k] = stringifyNumbers(v)
		}
		return out
	default:
		return value
	}
}

func parseHexOrDecUint32(value string) uint32 {
	s := strings.TrimSpace(value)
	if s == "" {
		return 0
	}
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		n, err := strconv.ParseUint(s[2:], 16, 32)
		if err != nil {
			return 0
		}
		return uint32(n)
	}
	n, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0
	}
	return uint32(n)
}

// DeepResolution 由深度解码结果构造 Method 解析结果（§9：ankr → high / verified）。
func (r DeepDecodeResult) DeepResolution() MethodResolution {
	if r.Name == "" {
		return MethodResolution{}
	}
	confidence := ConfidenceHigh
	if r.Verified {
		confidence = ConfidenceVerified
	}
	return MethodResolution{
		MethodID:   r.MethodID,
		Name:       r.Name,
		Signature:  r.Signature,
		Source:     SourceAnkr,
		Verified:   r.Verified,
		Confidence: confidence,
		MethodType: TypeContractCall,
		Candidates: []MethodCandidate{{Name: r.Name, Signature: r.Signature, Source: SourceAnkr, Verified: r.Verified}},
		ResolvedAt: time.Now().UTC(),
	}
}
