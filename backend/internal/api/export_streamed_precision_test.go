package api

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestStreamedXLSXPreservesExactNumbers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.jsonl")
	row := map[string]any{
		"block_number": json.Number("9007199254740993"),
		"uint_max":     json.Number("18446744073709551615"),
		"log_index":    json.Number("2"),
		"amount_raw":   "1234567890123456789012345678901234567890",
		"amount":       "1234567890123456789012.34567890123456789",
		"fraction":     json.Number("0.1234567890123456789"),
		"boundary":     json.Number("999999999999999"),
		"negative":     json.Number("-9007199254740993"),
	}
	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	columns := []string{"block_number", "uint_max", "log_index", "amount_raw", "amount", "fraction", "boundary", "negative"}
	var output bytes.Buffer
	if err := writeStreamedXLSX(&output, path, 1, columns); err != nil {
		t.Fatal(err)
	}
	book, err := excelize.OpenReader(bytes.NewReader(output.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	defer book.Close()
	for i, field := range columns {
		cell, err := excelize.CoordinatesToCellName(i+1, 2)
		if err != nil {
			t.Fatal(err)
		}
		got, err := book.GetCellValue("Sheet1", cell, excelize.Options{RawCellValue: true})
		want := row[field]
		if n, ok := want.(json.Number); ok {
			want = n.String()
		}
		if err != nil || got != want {
			t.Fatalf("%s: got %q want %q err=%v", field, got, want, err)
		}
		cellType, err := book.GetCellType("Sheet1", cell)
		if err != nil {
			t.Fatal(err)
		}
		if field == "log_index" || field == "boundary" {
			if cellType != excelize.CellTypeNumber && cellType != excelize.CellTypeUnset {
				t.Fatalf("ordinary integer became text: %s %v", field, cellType)
			}
		} else if cellType == excelize.CellTypeNumber || cellType == excelize.CellTypeUnset {
			t.Fatalf("precise value exposed to Excel numeric rounding: %s", field)
		}
	}
}
