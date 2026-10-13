package api

import (
	"encoding/json"
	"fmt"
)

// csv.Writer consumes a record synchronously. Callers reuse its slice across
// rows; only uncommon value types need fmt's general formatting machinery.
// Preserve existing CSV text exactly, including the legacy nil spelling.
func fillExportCSVRecord(record, columns []string, row map[string]any) {
	for i, column := range columns {
		switch value := row[column].(type) {
		case string:
			record[i] = value
		case json.Number:
			record[i] = string(value)
		default:
			record[i] = fmt.Sprint(value)
		}
	}
}
