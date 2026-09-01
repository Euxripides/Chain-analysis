package api

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/parquet-go/parquet-go"
)

func TestWriteParquetProducesReadableFile(t *testing.T) {
	rows := []map[string]any{{
		"chain": "bsc", "block_number": 123, "tx_hash": "0xabc",
		"token_address": "0xtoken", "amount_raw": "1000000000000000001",
		"amount": "1.000000000000000001", "topics": []string{"0x1", "0x2"},
		"matched_address": "0xsubject", "case_names": "case-a|case-b", "tag_names": "tag-a",
	}}
	var out bytes.Buffer
	s := &Server{}
	n, err := s.writeParquetTo(context.Background(), &out, "token_transfers", rows)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("row count = %d, want 1", n)
	}
	data := out.Bytes()
	if len(data) < 8 || string(data[:4]) != "PAR1" || string(data[len(data)-4:]) != "PAR1" {
		t.Fatalf("not a parquet file: first=%q last=%q", data[:min(4, len(data))], data[max(0, len(data)-4):])
	}

	r := parquet.NewGenericReader[parquetExportRow](bytes.NewReader(data))
	defer r.Close()
	got := make([]parquetExportRow, 1)
	read, err := r.Read(got)
	if err != nil && err != io.EOF {
		t.Fatal(err)
	}
	if read != 1 || got[0].AmountRaw != "1000000000000000001" || got[0].CaseNames != "case-a|case-b" {
		t.Fatalf("unexpected parquet row: %#v", got[0])
	}
}

func TestMatchedExportAddresses(t *testing.T) {
	subject := "0xabc"
	for _, row := range []map[string]any{
		{"from_address": subject, "to_address": "0xdef"},
		{"from_address": "0xdef", "to_address": subject},
		{"address": subject},
	} {
		got := matchedExportAddresses(subject, row)
		if len(got) != 1 || got[0] != subject {
			t.Fatalf("matchedExportAddresses(%v) = %v", row, got)
		}
	}
}
