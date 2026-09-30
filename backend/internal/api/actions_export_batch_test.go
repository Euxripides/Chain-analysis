package api

import (
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

func TestNormalizeActionExportAddresses(t *testing.T) {
	a := "0x11111111111111111111111111111111111111aa"
	b := "0x2222222222222222222222222222222222222222"
	for _, tt := range []struct {
		name, chain, single string
		addresses, want     []string
		bad                 bool
	}{
		{"single compatibility", "bsc", a, nil, []string{a}, false},
		{"batch normalize dedupe stable order", "bsc", "", []string{" " + strings.ToUpper(a) + " ", b, a}, []string{a, b}, false},
		{"Solana case preserved", "solana", "", []string{"So11111111111111111111111111111111111111112", "11111111111111111111111111111111"}, []string{"So11111111111111111111111111111111111111112", "11111111111111111111111111111111"}, false},
		{"neither", "bsc", "", nil, nil, true},
		{"both", "bsc", a, []string{b}, nil, true},
		{"blank", "bsc", "", []string{" "}, nil, true},
		{"invalid member", "bsc", "", []string{a, "bad"}, nil, true},
		{"wrong chain", "solana", "", []string{a}, nil, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeActionExportAddresses(tt.chain, tt.single, tt.addresses)
			if (err != nil) != tt.bad || (!tt.bad && !reflect.DeepEqual(got, tt.want)) {
				t.Fatalf("got=%v err=%v want=%v", got, err, tt.want)
			}
		})
	}
	addresses := make([]string, 100)
	for i := range addresses {
		addresses[i] = a
	}
	if got, err := normalizeActionExportAddresses("bsc", "", addresses); err != nil || len(got) != 1 {
		t.Fatalf("100 inputs: %v %v", got, err)
	}
	if _, err := normalizeActionExportAddresses("bsc", "", append(addresses, a)); err == nil {
		t.Fatal("101 inputs must be rejected before deduplication")
	}
}

func TestActionMultiExportRejectsInvalidInputBeforeCreatingIntent(t *testing.T) {
	// A zero Server has no store: validation must finish before any export is
	// created, including correctly decoding underscore-delimited time filters.
	s := &Server{}
	for _, body := range []string{
		`{"chain":"bsc","addresses":["bad"],"data_types":["token_transfers"]}`,
		`{"chain":"bsc","address":"0x1111111111111111111111111111111111111111","addresses":["0x2222222222222222222222222222222222222222"],"data_types":["token_transfers"]}`,
		`{"chain":"bsc","addresses":["0x1111111111111111111111111111111111111111"],"data_types":["token_transfers"],"start_time":"2026-09-30T00:00:00Z","end_time":"2026-09-01T00:00:00Z"}`,
		`{"chain":"bsc","addresses":["0x1111111111111111111111111111111111111111"],"data_types":["token_transfers"],"token_contract":"bad"}`,
		`{"chain":"bsc","addresses":["0x1111111111111111111111111111111111111111"],"data_types":["token_transfers"],"format":"csv","compression":"zstd"}`,
	} {
		w := httptest.NewRecorder()
		s.handleActionCreateExport(w, httptest.NewRequest("POST", "/api/v1/export/create", strings.NewReader(body)))
		var response actionEnvelope
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if w.Code != 400 || response.Success {
			t.Fatalf("input=%s response=%d %s", body, w.Code, w.Body)
		}
	}
}

func TestActionExportContractAndSymbolFilters(t *testing.T) {
	contract := "0x44444444444444444444444444444444444444aa"
	other := "0x5555555555555555555555555555555555555555"
	rows := []map[string]any{
		{"id": 1, "token_address": strings.ToUpper(contract), "token_symbol": "usdt"},
		{"id": 2, "token_contract": contract, "token_symbol": "OTHER"},
		{"id": 3, "token_address": other, "token_symbol": "USDT"},
		{"id": 4, "token_symbol": "USDT"},
		{"id": 5, "token_address": "", "contract_address": contract, "token_symbol": "USDT"},
	}
	for _, tt := range []struct {
		name, token, contract string
		want                  []int
	}{
		{"contract only", "", contract, []int{1, 2, 5}},
		{"symbol only", " usdt ", "", []int{1, 3, 4, 5}},
		{"both filters intersect", "USDT", contract, []int{1, 5}},
		{"no matching contract", "", "0x6666666666666666666666666666666666666666", []int{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			job := &model.DownloadJob{Chain: "bsc", Stats: map[string]any{"gpt_actions_token_filter": tt.token, "gpt_actions_token_contract_filter": tt.contract}}
			got := filterGPTActionExportToken(job, append([]map[string]any(nil), rows...))
			ids := []int{}
			for _, row := range got {
				ids = append(ids, row["id"].(int))
			}
			if !reflect.DeepEqual(ids, tt.want) {
				t.Fatalf("got %v want %v", ids, tt.want)
			}
		})
	}
	if got := filterGPTActionExportToken(nil, rows); len(got) != len(rows) {
		t.Fatal("unfiltered export changed")
	}
	mint := "So11111111111111111111111111111111111111112"
	sol := &model.DownloadJob{Chain: "solana", Stats: map[string]any{"gpt_actions_token_contract_filter": mint}}
	got := filterGPTActionExportToken(sol, []map[string]any{{"mint": mint}, {"mint": strings.ToLower(mint)}})
	if len(got) != 1 || got[0]["mint"] != mint {
		t.Fatal("Solana mint case was folded")
	}
}

func TestActionContractFilterCannotBeSilentlyDroppedByColumnarWriter(t *testing.T) {
	for _, stats := range []map[string]any{
		{"gpt_actions_token_contract_filter": "0x4444444444444444444444444444444444444444"},
		{"gpt_actions_token_filter": "USDT", "gpt_actions_token_contract_filter": "0x4444444444444444444444444444444444444444"},
	} {
		if _, ok := columnarTokenFilter(&model.DownloadJob{RangeType: "time", Stats: stats}); ok {
			t.Fatal("contract filter would be lost by the columnar path")
		}
	}
	if _, ok := columnarTokenFilter(&model.DownloadJob{RangeType: "time", Stats: map[string]any{"gpt_actions_token_contract_filter": " "}}); !ok {
		t.Fatal("blank filter should not disable supported columnar exports")
	}
}
