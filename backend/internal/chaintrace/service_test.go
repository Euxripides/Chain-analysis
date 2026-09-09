package chaintrace

import (
	"context"
	"errors"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
)

// Investigation E2E 曾以 mode=receipt 调用 /trace：该值不是合法 consistency，
// 若返回 generic error，writeRPCError 会把它误报为 ClassUpstream
// (“Provider temporarily unavailable”)，实际没有任何 Provider 请求发出。
func TestTransactionTraceRejectsInvalidModeAsInvalidParams(t *testing.T) {
	s := &Service{}
	_, err := s.TransactionTrace(context.Background(), "tron",
		chain.Hash("0x78dd8b17afc7158663f4a8a8799978a03c89b7a5b09b89ae4662462646389adf"),
		Options{Consistency: "receipt"})
	var pe *rpc.Error
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v, want *rpc.Error", err)
	}
	if pe.Class != rpc.ClassInvalidParams {
		t.Fatalf("class = %q, want invalid_params (must not surface as upstream)", pe.Class)
	}
}

// 无 Provider 声明 trace 能力（如 TRON 的 EVM-style trace）时必须诚实返回
// trace_unsupported，而不是 upstream。
func TestTransactionTraceUnsupportedWhenNoGateway(t *testing.T) {
	s := &Service{}
	_, err := s.TransactionTrace(context.Background(), "tron",
		chain.Hash("0x78dd8b17afc7158663f4a8a8799978a03c89b7a5b09b89ae4662462646389adf"),
		Options{Consistency: "auto"})
	var pe *rpc.Error
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v, want *rpc.Error", err)
	}
	if pe.Class != rpc.ClassTraceUnsupported {
		t.Fatalf("class = %q, want trace_unsupported", pe.Class)
	}
}
