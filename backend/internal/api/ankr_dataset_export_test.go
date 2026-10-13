package api

import (
	"bytes"
	"context"
	"testing"

	"github.com/parquet-go/parquet-go"
)

func TestNFTAndSnapshotParquetPreserveEvidence(t *testing.T) {
	if table, _, _ := exportStreamQueryParts("balances", "flare", "0x123"); table != "address_balances" {
		t.Fatal("balance export queried", table)
	}
	for _, dataset := range []string{"nft_transfers", "balances", "logs"} {
		row := map[string]any{"chain": "flare", "token_id": "340282366920938463463374607431768211455", "token_standard": "ERC1155", "amount_raw": "123456789012345678901234567890", "balance_raw": "123456789012345678901234567890", "snapshot_block": uint64(123456789), "timestamp": "2026-10-04T13:00:00Z", "topics": []any{"0x01", "0x02"}}
		var out bytes.Buffer
		s := &Server{}
		if _, err := s.writeParquetTo(context.Background(), &out, dataset, []map[string]any{row}); err != nil {
			t.Fatal(err)
		}
		rows, err := parquet.Read[parquetExportRow](bytes.NewReader(out.Bytes()), int64(out.Len()))
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0].TokenID != row["token_id"] || rows[0].TokenStandard != "ERC1155" || rows[0].BalanceRaw != row["balance_raw"] || rows[0].SnapshotBlock != uint64(123456789) || len(rows[0].Topics) != 2 {
			t.Fatal(rows)
		}
	}
}
