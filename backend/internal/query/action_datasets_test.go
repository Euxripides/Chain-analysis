package query

import (
	"strings"
	"testing"
	"time"
)

func TestDatasetFiltersDoNotInventNativeNFTAmounts(t *testing.T) {
	for _, dataset := range []string{"nft_transfers", "logs", "balances"} {
		for _, f := range []ActionFilter{{MinAmount: "1"}, {Status: "success"}, {MethodID: "0xa9059cbb"}} {
			if ValidateActionDatasetFilter(dataset, f) == nil {
				t.Fatalf("%s silently accepted unsupported filter %+v", dataset, f)
			}
		}
	}
	if err := ValidateActionDatasetFilter("nft_transfers", ActionFilter{TokenID: "340282366920938463463374607431768211455", TokenContract: "0x123", Direction: "in"}); err != nil {
		t.Fatal(err)
	}
	for _, dataset := range []string{"logs", "balances"} {
		if ValidateActionDatasetFilter(dataset, ActionFilter{Direction: "out"}) == nil {
			t.Fatal("snapshot/log acquired transfer direction")
		}
	}
}

func TestBalanceTimeFilterCannotResurrectAnOlderObservation(t *testing.T) {
	end := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	f := ActionFilter{Chain: "flare", Address: "0xAbC", EndTime: &end, TokenContract: "0xDeF"}
	sql, args := actionSnapshotSource("balances", f)
	if !strings.Contains(sql, "PARTITION BY chain,address,token_address") || !strings.Contains(sql, "WHERE _balance_rank=1) WHERE timestamp<=?") {
		t.Fatal(sql)
	}
	if len(args) != 4 || args[1] != "0xabc" || args[3] != "0xdef" {
		t.Fatal(args)
	}
	row := map[string]any{"timestamp": "2026-10-04T12:00:00Z", "snapshot_block": uint64(42), "balance_raw": "123456789012345678901234567890"}
	annotateBalanceSnapshot(row)
	if row["is_current_balance"] != false || row["as_of_block"] != uint64(42) || row["balance_raw"] != "123456789012345678901234567890" {
		t.Fatal(row)
	}
}

func TestSearchPreservesHashVariantsAndUsesPointPredicates(t *testing.T) {
	hash := strings.Repeat("a", 64)
	sql, args := localTransactionSearchSQL([]string{"flare", "gnosis"}, "0x"+hash)
	if strings.Contains(sql, "lower(tx_hash)") || !strings.Contains(sql, "blockchain.nft_transfers") || !strings.Contains(sql, "chain IN (?) AND tx_hash IN (?)") {
		t.Fatal(sql)
	}
	variants := args[1].([]string)
	if variants[0] != hash || variants[1] != "0x"+hash {
		t.Fatal(variants)
	}
	sql, args = localAddressSearchSQL([]string{"flare"}, "0xAbC", false)
	if strings.Count(sql, "LIMIT 1") != 5 || len(args) != 13 || strings.Contains(sql, "FINAL") {
		t.Fatal(sql, args)
	}
}

func TestExportLogTopicsAreRetainedAcrossScanReuse(t *testing.T) {
	holder := newColumnHolder("Array(String)").(*[]string)
	*holder = []string{"0x01", "0x02"}
	value := exportValue(holder).([]string)
	(*holder)[0] = "next-row"
	if value[0] != "0x01" || formatCellText(value) != "[0x01 0x02]" {
		t.Fatal(value)
	}
}
