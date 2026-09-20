package solana

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

// balancesRPC 只为 balances 需要的三个方法服务；代币账户只对 SPL Token 程序返回一条。
func balancesRPC(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		result := "{}"
		switch req.Method {
		case MethodVersion:
			result = `{"solana-core":"test"}`
		case MethodSlot:
			result = "448700000"
		case MethodBalance:
			result = `{"context":{"slot":448700000},"value":1234567890}`
		case MethodTokenAccounts:
			program := ""
			if len(req.Params) > 1 {
				var options struct {
					ProgramID string `json:"programId"`
				}
				_ = json.Unmarshal(req.Params[1], &options)
				program = options.ProgramID
			}
			if program == ProgramSPLToken {
				// 同一 mint 两个代币账户：必须按 mint 求和（40+2），而不是落两行被去重键合并。
				result = `{"context":{"slot":448700000},"value":[{"pubkey":"ATA1","account":{"owner":"TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA","data":{"program":"spl-token","parsed":{"type":"account","info":{"mint":"Mint1111111111111111111111111111111111111","owner":"Holder1111111111111111111111111111111111","tokenAmount":{"amount":"40","decimals":6}}}}}},{"pubkey":"ATA2","account":{"owner":"TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA","data":{"program":"spl-token","parsed":{"type":"account","info":{"mint":"Mint1111111111111111111111111111111111111","owner":"Holder1111111111111111111111111111111111","tokenAmount":{"amount":"2","decimals":6}}}}}}]}`
			} else {
				result = `{"context":{"slot":448700000},"value":[]}`
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":`+result+`}`)
	}))
}

func TestFetchBalancesSnapshot(t *testing.T) {
	server := balancesRPC(t)
	defer server.Close()
	client := newTestClient(t, server.URL)

	res, err := client.Fetch(context.Background(), provider.FetchRequest{
		Chain: "solana", Dataset: "balances", Address: "Holder1111111111111111111111111111111111",
	})
	if err != nil {
		t.Fatalf("balances fetch failed: %v", err)
	}
	if res.HasMore || res.NextPageToken != "" {
		t.Fatalf("snapshot must be a single page: %+v", res)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected native + one SPL account, got %d rows", len(res.Rows))
	}
	var native, token balanceRow
	if err := json.Unmarshal(res.Rows[0], &native); err != nil {
		t.Fatalf("decode native row: %v", err)
	}
	if err := json.Unmarshal(res.Rows[1], &token); err != nil {
		t.Fatalf("decode token row: %v", err)
	}
	if native.TokenType != "native" || native.TokenDecimals != 9 || native.BalanceRawInteger != "1234567890" {
		t.Fatalf("unexpected native row: %+v", native)
	}
	if token.TokenType != "SPL" || token.ContractAddress == "" || token.BalanceRawInteger != "42" || token.TokenDecimals != 6 {
		t.Fatalf("unexpected token row: %+v", token)
	}
	// 快照槽位来自 getSlot；符号/价格没有来源必须留空（ADR-024 D2）。
	if native.SnapshotBlock != 448700000 || token.SnapshotBlock != 448700000 {
		t.Fatalf("snapshot slot not propagated: %+v / %+v", native, token)
	}
	if token.TokenSymbol != "" || token.TokenName != "" || token.TokenPrice != "" || token.BalanceUSD != "" {
		t.Fatalf("metadata must stay empty rather than faked: %+v", token)
	}
}

func TestFetchBalancesRefusesToTruncate(t *testing.T) {
	original := maxBalanceAccounts
	maxBalanceAccounts = 1
	defer func() { maxBalanceAccounts = original }()

	server := balancesRPC(t)
	defer server.Close()
	client := newTestClient(t, server.URL)
	if _, err := client.Fetch(context.Background(), provider.FetchRequest{
		Chain: "solana", Dataset: "balances", Address: "Holder1111111111111111111111111111111111",
	}); err == nil {
		t.Fatal("exceeding the account cap must fail explicitly, never truncate silently")
	}
}

func TestBalanceRowEncodingIsStable(t *testing.T) {
	// 行字段名是 ingest/solana 归一化的契约：改名字必须同时改归一化与测试。
	row := balanceRow{HolderAddress: "H", ContractAddress: "M", TokenType: "SPL", TokenDecimals: 6, BalanceRawInteger: "7", SnapshotBlock: 9, Timestamp: 10}
	payload, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"holderAddress", "contractAddress", "tokenType", "tokenDecimals", "balanceRawInteger", "snapshotBlock", "timestamp"} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("missing %s in %s", key, fmt.Sprint(decoded))
		}
	}
}
