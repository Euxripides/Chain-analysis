package method

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"
	"time"
)

// fakeTxData 内存实现，用于验证深度解码缓存语义（§23/§55）。
type fakeTxData struct {
	tx      map[string]any
	logs    []map[string]any
	decoded map[string]DecodedRecord
	updates int
}

func newFakeTxData(tx map[string]any) *fakeTxData {
	return &fakeTxData{tx: tx, decoded: map[string]DecodedRecord{}}
}

func (f *fakeTxData) GetTransaction(_ context.Context, _, _ string) (map[string]any, error) {
	if f.tx == nil {
		return nil, nil
	}
	out := map[string]any{}
	for k, v := range f.tx {
		out[k] = v
	}
	return out, nil
}

func (f *fakeTxData) GetLogs(_ context.Context, _, _ string) ([]map[string]any, error) {
	return f.logs, nil
}

func (f *fakeTxData) GetDecodedInput(_ context.Context, chain, txHash string) (*DecodedRecord, error) {
	if record, ok := f.decoded[chain+"|"+txHash]; ok {
		return &record, nil
	}
	return nil, nil
}

func (f *fakeTxData) PutDecodedInput(_ context.Context, record DecodedRecord) error {
	f.decoded[record.Chain+"|"+record.TxHash] = record
	return nil
}

func (f *fakeTxData) UpdateTransactionMethod(_ context.Context, _, _ string, resolution MethodResolution) error {
	f.updates++
	f.tx = applyMethodToRow(f.tx, resolution)
	return nil
}

// countingFetcher 统计真实 Provider 请求次数。
type countingFetcher struct {
	calls int
	body  json.RawMessage
}

func (c *countingFetcher) TxDetail(_ context.Context, _, _ string, _, _ bool) (json.RawMessage, error) {
	c.calls++
	return c.body, nil
}

const ankrDetailBody = `{"transactions":[{"hash":"0xabc","method":{"id":"0xa9059cbb","name":"transfer",
  "verified":true,"string":"transfer(address to, uint256 value)",
  "inputs":[{"name":"to","type":"address","value":"0x1111111111111111111111111111111111111111"},
            {"name":"value","type":"uint256","value":"1000000000000000000"}]},
  "logs":[{"address":"0xtoken","logIndex":"0x2","topics":["0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"],
  "event":{"name":"Transfer","verified":true,"string":"Transfer(address from, address to, uint256 value)","inputs":[]}}]}]}`

const ankrLiveShapeBody = `{"transactions":[{"hash":"0xabc","method":{
  "id":"0x00000000000000000000000000000000000000000000000000000000f2fde38b",
  "name":"transferOwnership","verified":false,
  "string":"function transferOwnership(address newOwner) returns()",
  "signature":"transferOwnership(address)",
  "inputs":[{"name":"newOwner","type":"address","size":20,"valueDecoded":"0x0000000000000000000000000000000000000000"}]}}]}`

func TestParseAnkrLiveResponseShape(t *testing.T) {
	result, err := ParseAnkrTxDetail(json.RawMessage(ankrLiveShapeBody))
	if err != nil {
		t.Fatal(err)
	}
	if result.MethodID != "0xf2fde38b" || result.Signature != "transferOwnership(address)" {
		t.Fatalf("method = %+v", result)
	}
	if len(result.Params) != 1 || result.Params[0].Value != "0x0000000000000000000000000000000000000000" {
		t.Fatalf("params = %+v", result.Params)
	}
	if got := normalizeProviderSignature("function transferOwnership(address newOwner) returns()", "transferOwnership"); got != "transferOwnership(address)" {
		t.Fatalf("normalized signature = %q", got)
	}
}

func TestDeepDecodeRepairsStaleSameSourceMetadata(t *testing.T) {
	row := txRowFixture()
	row["method_id"] = ""
	row["method_name"] = "transferOwnership"
	row["method_signature"] = "function transferOwnership(address)"
	row["method_source"] = SourceAnkr
	row["method_verified"] = uint8(0)
	data := newFakeTxData(row)
	fetcher := &countingFetcher{body: json.RawMessage(ankrLiveShapeBody)}
	service := NewDetailService(data, NewRegistry(), NewAnkrDeepResolver(fetcher), nil, testLogger())
	detail, err := service.Detail(context.Background(), "bsc", "0xabc", DecodeForceRefresh)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Method.ID != "0xf2fde38b" || detail.Method.Signature != "transferOwnership(address)" || data.updates != 1 {
		t.Fatalf("stale provider metadata was not repaired: detail=%+v updates=%d", detail.Method, data.updates)
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func txRowFixture() map[string]any {
	return map[string]any{
		"chain": "bsc", "tx_hash": "0xabc", "to_address": "0xtoken",
		"input":             transferInput,
		"method_id":         "0xa9059cbb",
		"method_name":       "transfer",
		"method_signature":  "transfer(address,uint256)",
		"method_source":     SourceLocalSignature,
		"method_verified":   uint8(0),
		"method_confidence": ConfidenceHigh,
		"method_type":       TypeContractCall,
	}
}

// §55 Deep Decode Cache：第一次 Ankr Request = 1，第二次同 Tx = 0。
func TestDeepDecodeCache(t *testing.T) {
	data := newFakeTxData(txRowFixture())
	fetcher := &countingFetcher{body: json.RawMessage(ankrDetailBody)}
	service := NewDetailService(data, NewRegistry(), NewAnkrDeepResolver(fetcher), nil, testLogger())

	first, err := service.Detail(context.Background(), "bsc", "0xabc", DecodeAuto)
	if err != nil {
		t.Fatal(err)
	}
	if fetcher.calls != 1 {
		t.Fatalf("first call provider requests = %d, want 1", fetcher.calls)
	}
	if first.DecodeCacheHit {
		t.Fatal("first call must not be a cache hit")
	}
	if first.DecodedInput["value"] != "1000000000000000000" {
		t.Fatalf("decoded input = %v", first.DecodedInput)
	}
	if first.Method.Source != SourceAnkr || !first.Method.Verified {
		t.Fatalf("provider decode must upgrade method source: %+v", first.Method)
	}

	second, err := service.Detail(context.Background(), "bsc", "0xabc", DecodeAuto)
	if err != nil {
		t.Fatal(err)
	}
	if fetcher.calls != 1 {
		t.Fatalf("second call provider requests = %d, want 0 extra", fetcher.calls-1)
	}
	if !second.DecodeCacheHit {
		t.Fatal("second call must hit the local cache")
	}
	if second.DecodedInput["to"] != "0x1111111111111111111111111111111111111111" {
		t.Fatalf("cached decoded input = %v", second.DecodedInput)
	}

	// force_refresh 才允许重新请求 Provider（§25）
	if _, err := service.Detail(context.Background(), "bsc", "0xabc", DecodeForceRefresh); err != nil {
		t.Fatal(err)
	}
	if fetcher.calls != 2 {
		t.Fatalf("force_refresh provider requests = %d, want 2", fetcher.calls)
	}
}

// §25 local_only 绝不产生 Provider 请求。
func TestDetailLocalOnlyNeverCallsProvider(t *testing.T) {
	data := newFakeTxData(txRowFixture())
	fetcher := &countingFetcher{body: json.RawMessage(ankrDetailBody)}
	service := NewDetailService(data, NewRegistry(), NewAnkrDeepResolver(fetcher), nil, testLogger())
	detail, err := service.Detail(context.Background(), "bsc", "0xabc", DecodeLocalOnly)
	if err != nil {
		t.Fatal(err)
	}
	if fetcher.calls != 0 {
		t.Fatalf("local_only must not call the provider, got %d", fetcher.calls)
	}
	if detail.Method.Name != "transfer" {
		t.Fatalf("local method must still be returned: %+v", detail.Method)
	}
	if detail.Input != transferInput {
		t.Fatal("raw input must be returned untouched")
	}
}

// §24/§38：原生转账没有参数可解，绝不能浪费一次 Provider 深度解码额度。
func TestDetailSkipsProviderForNonContractCalls(t *testing.T) {
	row := txRowFixture()
	row["method_type"] = TypeNativeTransfer
	row["method_id"] = ""
	row["method_name"] = NameNativeTransfer
	row["method_source"] = SourceHeuristic
	row["input"] = "0x"
	data := newFakeTxData(row)
	fetcher := &countingFetcher{body: json.RawMessage(ankrDetailBody)}
	service := NewDetailService(data, NewRegistry(), NewAnkrDeepResolver(fetcher), nil, testLogger())

	detail, err := service.Detail(context.Background(), "bsc", "0xabc", DecodeAuto)
	if err != nil {
		t.Fatal(err)
	}
	if fetcher.calls != 0 {
		t.Fatalf("native transfer must not trigger deep decode, got %d calls", fetcher.calls)
	}
	if detail.Method.Name != NameNativeTransfer {
		t.Fatalf("method = %+v", detail.Method)
	}
}

// 本地 ABI 可解码时不得触发 Provider（零成本优先，§24）。
func TestDetailPrefersLocalABIOverProvider(t *testing.T) {
	registry := NewRegistry()
	registry.SetABIs([]ABIRecord{{Chain: "bsc", ContractAddress: "0xtoken", ABIJSON: []byte(erc20ABI), Source: "verified_explorer", Verified: true}})
	data := newFakeTxData(txRowFixture())
	fetcher := &countingFetcher{body: json.RawMessage(ankrDetailBody)}
	service := NewDetailService(data, registry, NewAnkrDeepResolver(fetcher), nil, testLogger())

	detail, err := service.Detail(context.Background(), "bsc", "0xabc", DecodeAuto)
	if err != nil {
		t.Fatal(err)
	}
	if fetcher.calls != 0 {
		t.Fatalf("local ABI decode must avoid provider calls, got %d", fetcher.calls)
	}
	if detail.DecodeSource != SourceContractABI {
		t.Fatalf("decode source = %s", detail.DecodeSource)
	}
	if detail.DecodedInput["value"] != "1000000000000000000" {
		t.Fatalf("decoded input = %v", detail.DecodedInput)
	}
}

// 用量记账：缓存命中也必须可观测（§49）。
func TestDetailRecordsCacheHitUsage(t *testing.T) {
	data := newFakeTxData(txRowFixture())
	data.decoded["bsc|0xabc"] = DecodedRecord{
		Chain: "bsc", TxHash: "0xabc", MethodID: "0xa9059cbb", MethodName: "transfer",
		DecodedJSON:  `{"params":[{"name":"to","type":"address","value":"0x11"}]}`,
		DecodeSource: SourceAnkr, Verified: true, DecodedAt: time.Now().UTC(),
	}
	var usages []DeepDecodeUsage
	service := NewDetailService(data, NewRegistry(), nil, func(u DeepDecodeUsage) { usages = append(usages, u) }, testLogger())
	if _, err := service.Detail(context.Background(), "bsc", "0xabc", DecodeAuto); err != nil {
		t.Fatal(err)
	}
	if len(usages) != 1 || !usages[0].CacheHit {
		t.Fatalf("cache hit usage = %+v", usages)
	}
}

// 交易不存在时必须是明确的 404 语义，不能伪造一条空交易。
func TestDetailMissingTransaction(t *testing.T) {
	service := NewDetailService(newFakeTxData(nil), NewRegistry(), nil, nil, testLogger())
	if _, err := service.Detail(context.Background(), "bsc", "0xmissing", DecodeAuto); err != ErrTransactionNotFound {
		t.Fatalf("err = %v, want ErrTransactionNotFound", err)
	}
}

// §29 事件解析：本地事件签名库为 Logs 补 event_name。
func TestAnnotateLogsFromEventSignatures(t *testing.T) {
	registry := NewRegistry()
	registry.SetEventSignatures(SeedEventSignatures())
	data := newFakeTxData(txRowFixture())
	data.logs = []map[string]any{{
		"address": "0xtoken", "log_index": uint32(0), "data": "0x",
		"topics": []string{Topic0Of("Transfer(address,address,uint256)")},
	}}
	service := NewDetailService(data, registry, nil, nil, testLogger())
	detail, err := service.Detail(context.Background(), "bsc", "0xabc", DecodeLocalOnly)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Logs) != 1 || detail.Logs[0]["event_name"] != "Transfer" {
		t.Fatalf("logs = %+v", detail.Logs)
	}
	if detail.Logs[0]["event_verified"] != false {
		t.Fatal("signature-only event match must not be reported as verified")
	}
}

// §41 Provider 返回的数字必须转成字符串，绝不经过 float64。
func TestParseAnkrTxDetailKeepsPrecision(t *testing.T) {
	body := json.RawMessage(`{"transactions":[{"method":{"id":"0xa9059cbb","name":"transfer",
	  "string":"transfer(address,uint256)","verified":true,
	  "inputs":[{"name":"value","type":"uint256","value":123456789012345678901234567890}]}}]}`)
	result, err := ParseAnkrTxDetail(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Params) != 1 {
		t.Fatalf("params = %+v", result.Params)
	}
	value, ok := result.Params[0].Value.(string)
	if !ok || value != "123456789012345678901234567890" {
		t.Fatalf("uint256 precision lost: %#v", result.Params[0].Value)
	}
}

// Provider 签名与 selector 不自洽时不得采信（§62-20）。
func TestParseAnkrTxDetailRejectsInconsistentSignature(t *testing.T) {
	body := json.RawMessage(`{"transactions":[{"method":{"id":"0xa9059cbb","name":"transfer",
	  "string":"somethingElse(uint256)","verified":true,"inputs":[]}}]}`)
	result, err := ParseAnkrTxDetail(body)
	if err != nil {
		t.Fatal(err)
	}
	if result.Signature != "" || result.Verified {
		t.Fatalf("inconsistent signature must be dropped: %+v", result)
	}
}
