package query

import (
	"fmt"
	"testing"
)

// TestExportDedupeColumnsMatchReplacingMergeTreeSortKeys 冻结每个数据集的去重键。
//
// 这些键必须与 migrations/clickhouse 里各表 ReplacingMergeTree 的排序键一致：
// 去掉导出查询的 FINAL 之后，Go 侧就是唯一的版本收敛点，键写错会让导出文件里
// 出现重复行或错误地丢掉合法行（详见 export_dedupe.go 文件头）。
func TestExportDedupeColumnsMatchReplacingMergeTreeSortKeys(t *testing.T) {
	want := map[string][]string{
		"transactions":          {"tx_hash"},
		"token_transfers":       {"tx_hash", "log_index", "token_address"},
		"nft_transfers":         {"tx_hash", "log_index", "contract_address", "token_id"},
		"internal_transactions": {"tx_hash", "call_index"},
		"logs":                  {"tx_hash", "log_index"},
		"balances":              {"address", "token_address", "token_symbol"},
		"activity":              {"tx_hash"},
	}
	for dataset, cols := range want {
		got := exportDedupeColumns(dataset)
		if len(got) != len(cols) {
			t.Fatalf("%s: dedupe columns = %v, want %v", dataset, got, cols)
		}
		for i := range cols {
			if got[i] != cols[i] {
				t.Fatalf("%s: dedupe columns = %v, want %v", dataset, got, cols)
			}
		}
	}
	if got := exportDedupeColumns("unknown_dataset"); got != nil {
		t.Fatalf("unknown dataset must not claim a dedupe key, got %v", got)
	}
}

func TestExportSelectNoFinalOnlyForDedupableDatasets(t *testing.T) {
	for _, dataset := range []string{"transactions", "token_transfers", "nft_transfers", "logs", "balances", "internal_transactions"} {
		sel, ok := exportSelectNoFinal(dataset, "chain=?")
		if !ok {
			t.Fatalf("%s: expected a no-FINAL select", dataset)
		}
		if containsFinal(sel) {
			t.Fatalf("%s: generated select still contains FINAL: %s", dataset, sel)
		}
	}
	if _, ok := exportSelectNoFinal("unknown_dataset", "chain=?"); ok {
		t.Fatal("unknown dataset must not use the no-FINAL select")
	}
}

func containsFinal(sql string) bool {
	for i := 0; i+5 <= len(sql); i++ {
		if sql[i:i+5] == "FINAL" {
			return true
		}
	}
	return false
}

// TestDedupeExportRowsKeepsNewestVersion 证明去重保留首见行，并且列分隔符能区分
// ("ab","c") 与 ("a","bc") 这类拼接歧义。
func TestDedupeExportRowsKeepsNewestVersion(t *testing.T) {
	rows := []map[string]any{
		{"tx_hash": "0xaa", "log_index": 1, "token_address": "0xt", "source_ingested_at": "newer"},
		{"tx_hash": "0xaa", "log_index": 1, "token_address": "0xt", "source_ingested_at": "older"},
		{"tx_hash": "0xbb", "log_index": 2, "token_address": "0xt", "source_ingested_at": "only"},
	}
	got := dedupeExportRows("token_transfers", rows, 0)
	if len(got) != 2 {
		t.Fatalf("dedupe kept %d rows, want 2", len(got))
	}
	if got[0]["source_ingested_at"] != "newer" {
		t.Fatalf("dedupe kept %v, want the first-seen (newest) version", got[0]["source_ingested_at"])
	}

	ambiguous := []map[string]any{
		{"tx_hash": "ab", "log_index": "c", "token_address": ""},
		{"tx_hash": "a", "log_index": "bc", "token_address": ""},
	}
	if got := dedupeExportRows("token_transfers", ambiguous, 0); len(got) != 2 {
		t.Fatalf("column separator collapsed two distinct keys into %d row(s)", len(got))
	}
}

func TestDedupeExportRowsTrimsToLimit(t *testing.T) {
	rows := make([]map[string]any, 0, 10)
	for i := 0; i < 10; i++ {
		rows = append(rows, map[string]any{"tx_hash": fmt.Sprintf("0x%02d", i)})
	}
	if got := dedupeExportRows("transactions", rows, 4); len(got) != 4 {
		t.Fatalf("trim produced %d rows, want 4", len(got))
	}
	if got := dedupeExportRows("transactions", rows, 0); len(got) != 10 {
		t.Fatalf("limit=0 must not trim, got %d rows", len(got))
	}
}
