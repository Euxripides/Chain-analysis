package ingest

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/method"
)

// §5/§62-3：Selector 一律本地从 input 提取，不信任 Provider 的 methodId。
func TestNormalizeExtractsSelectorLocally(t *testing.T) {
	input := "0xa9059cbb" + strings.Repeat("0", 128)
	raw := json.RawMessage(`{"blockHeight":"1","transactionHash":"0x1","fromAddress":"0xa","toAddress":"0xB",
	  "value":"0x0","status":"0x1","input":"` + input + `","methodId":"0xdeadbeef"}`)
	row, err := NormalizeTransaction(raw, "bsc", "ankr")
	if err != nil {
		t.Fatal(err)
	}
	if row.MethodID != "0xa9059cbb" {
		t.Fatalf("method_id = %s, want the locally extracted selector", row.MethodID)
	}
	if row.MethodType != method.TypeContractCall {
		t.Fatalf("method_type = %s", row.MethodType)
	}
	// §6/§62-2：完整 input 必须原样保留
	if row.Input != input {
		t.Fatal("raw input must be stored in full")
	}
}

// §38 Native Transfer：input=0x 且 value>0。
func TestNormalizeNativeTransfer(t *testing.T) {
	raw := json.RawMessage(`{"blockHeight":"1","transactionHash":"0x1","fromAddress":"0xa","toAddress":"0xb","value":"0x10","input":"0x"}`)
	row, err := NormalizeTransaction(raw, "bsc", "ankr")
	if err != nil {
		t.Fatal(err)
	}
	if row.MethodType != method.TypeNativeTransfer || row.MethodID != "" {
		t.Fatalf("row = %s/%s", row.MethodType, row.MethodID)
	}
}

// §38 Empty Call：input=0x 且 value=0，不能误判成 ERC20 transfer。
func TestNormalizeEmptyCall(t *testing.T) {
	raw := json.RawMessage(`{"blockHeight":"1","transactionHash":"0x1","fromAddress":"0xa","toAddress":"0xb","value":"0x0","input":"0x"}`)
	row, err := NormalizeTransaction(raw, "bsc", "ankr")
	if err != nil {
		t.Fatal(err)
	}
	if row.MethodType != method.TypeEmptyCall {
		t.Fatalf("method_type = %s", row.MethodType)
	}
}

// §38 Contract Creation：to = null，部署字节码不得按 Selector 解析。
func TestNormalizeContractCreation(t *testing.T) {
	raw := json.RawMessage(`{"blockHeight":"1","transactionHash":"0x1","fromAddress":"0xa","toAddress":null,"value":"0x0","input":"0x60806040523480156100"}`)
	row, err := NormalizeTransaction(raw, "bsc", "ankr")
	if err != nil {
		t.Fatal(err)
	}
	if row.MethodType != method.TypeContractCreation {
		t.Fatalf("method_type = %s", row.MethodType)
	}
	if row.MethodID != "" {
		t.Fatalf("contract creation must not carry a selector, got %s", row.MethodID)
	}
}

// §36/§55：Method Unknown 不影响交易本体入库，行仍然完整可写。
func TestUnknownMethodKeepsRowComplete(t *testing.T) {
	raw := json.RawMessage(`{"blockHeight":"9","transactionHash":"0x9","fromAddress":"0xa","toAddress":"0xb","value":"0x0","input":"0x12ab34cd0000","status":"0x1"}`)
	row, err := NormalizeTransaction(raw, "bsc", "ankr")
	if err != nil {
		t.Fatal(err)
	}
	row.ApplyMethod(method.MethodResolution{
		MethodID: "0x12ab34cd", Source: method.SourceUnknown,
		Confidence: method.ConfidenceUnknown, MethodType: method.TypeContractCall,
	})
	if row.MethodName != "" || row.MethodSource != method.SourceUnknown {
		t.Fatalf("unknown must not be disguised as resolved: %+v", row)
	}
	if row.MethodVerified != 0 || row.MethodResolvedAt != nil {
		t.Fatal("unknown method must not be marked verified/resolved")
	}
	if row.TxHash != "0x9" || row.BlockNumber != 9 || row.Status != 1 || row.Input == "" {
		t.Fatalf("transaction body must stay complete: %+v", row)
	}
}

// 解析结果写回：verified 与 resolved_at 必须可追踪（§62-16）。
func TestApplyMethodResolved(t *testing.T) {
	row := TxRow{}
	resolution := method.MethodResolution{
		MethodID: "0xa9059cbb", Name: "transfer", Signature: "transfer(address,uint256)",
		Source: method.SourceContractABI, Verified: true, Confidence: method.ConfidenceVerified,
		MethodType: method.TypeContractCall,
	}
	resolution.ResolvedAt = resolution.ResolvedAt.UTC()
	row.ApplyMethod(resolution)
	if row.MethodVerified != 1 || row.MethodConfidence != method.ConfidenceVerified {
		t.Fatalf("row = %+v", row)
	}
	if row.MethodName != "transfer" || row.MethodSignature != "transfer(address,uint256)" {
		t.Fatalf("row = %+v", row)
	}
}

// §29 Logs 事件字段写回。
func TestApplyEvent(t *testing.T) {
	row := LogRow{}
	row.ApplyEvent("Transfer", "Transfer(address,address,uint256)", method.SourceLocalSignature, false)
	if row.EventName != "Transfer" || row.EventVerified != 0 || row.EventSource != method.SourceLocalSignature {
		t.Fatalf("row = %+v", row)
	}
}
