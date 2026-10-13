package api

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"testing"
	"time"
)

func TestCSVRecordsMatchExistingFormatting(t *testing.T) {
	rows := []map[string]any{
		{"text": "金额,\"地址\"\n第二行😀", "number": json.Number("18446744073709551615"), "nil": nil, "array": []any{"a", "b"}, "other": time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)},
		{"text": "", "number": json.Number("-0.000000000000000001"), "nil": "changed", "array": []string{}, "other": true},
		{"number": uint64(18446744073709551615), "other": 3.25},
	}
	columns := []string{"text", "number", "nil", "array", "other", "absent"}
	var old, new bytes.Buffer
	oldWriter, newWriter := csv.NewWriter(&old), csv.NewWriter(&new)
	record := make([]string, len(columns))
	for _, row := range rows {
		want := make([]string, len(columns))
		for i, column := range columns {
			want[i] = fmt.Sprint(row[column])
		}
		if err := oldWriter.Write(want); err != nil {
			t.Fatal(err)
		}
		fillExportCSVRecord(record, columns, row)
		if err := newWriter.Write(record); err != nil {
			t.Fatal(err)
		}
	}
	oldWriter.Flush()
	newWriter.Flush()
	if oldWriter.Error() != nil || newWriter.Error() != nil || !bytes.Equal(old.Bytes(), new.Bytes()) {
		t.Fatalf("CSV bytes changed:\nold %q\nnew %q", old.Bytes(), new.Bytes())
	}
}

func BenchmarkExportCSVRecord(b *testing.B) {
	columns := []string{"chain", "block_number", "timestamp", "tx_hash", "log_index", "token_address", "token_symbol", "token_decimals", "from_address", "to_address", "amount_raw", "amount", "source_provider", "source_ingested_at"}
	row := map[string]any{}
	for _, column := range columns {
		row[column] = "0x1111111111111111111111111111111111111111"
	}
	row["block_number"], row["log_index"], row["token_decimals"] = json.Number("9007199254740993"), json.Number("2"), json.Number("18")
	for _, legacy := range []bool{true, false} {
		name := "reused"
		if legacy {
			name = "legacy"
		}
		b.Run(name, func(b *testing.B) {
			w := csv.NewWriter(io.Discard)
			record := make([]string, len(columns))
			b.ReportAllocs()
			for b.Loop() {
				if legacy {
					record = make([]string, len(columns))
					for i, col := range columns {
						record[i] = fmt.Sprint(row[col])
					}
				} else {
					fillExportCSVRecord(record, columns, row)
				}
				if err := w.Write(record); err != nil {
					b.Fatal(err)
				}
			}
			w.Flush()
			if err := w.Error(); err != nil {
				b.Fatal(err)
			}
		})
	}
}
