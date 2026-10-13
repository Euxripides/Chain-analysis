package api

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

func TestSharedExportZIPContainsEveryDatasetInEachFormat(t *testing.T) {
	formats := []string{"csv", "jsonl", "parquet", "xlsx"}
	datasets := []string{"transactions", "token_transfers"}
	parts := make([]preparedDatasetParts, 0, len(datasets))
	for _, dataset := range datasets {
		part := preparedDatasetParts{dataset: dataset, rows: 2, paths: make(map[string]string)}
		for _, format := range formats {
			path := filepath.Join(t.TempDir(), dataset+"."+format)
			if err := os.WriteFile(path, []byte(dataset+":"+format), 0600); err != nil {
				t.Fatal(err)
			}
			part.paths[format] = path
		}
		parts = append(parts, part)
	}
	jobID := "00000000-0000-4000-8000-000000000001"
	exp := &model.Export{ID: "00000000-0000-4000-8000-000000000002", JobID: &jobID}
	job := &model.DownloadJob{Chain: "bsc", Stats: map[string]any{"progress": 1}}
	for _, format := range formats {
		var output bytes.Buffer
		if err := writeSharedExportZIP(&output, exp, job, "0xsubject", format, parts); err != nil {
			t.Fatalf("%s: %v", format, err)
		}
		zr, err := zip.NewReader(bytes.NewReader(output.Bytes()), int64(output.Len()))
		if err != nil {
			t.Fatalf("%s: %v", format, err)
		}
		if len(zr.File) != len(datasets) {
			t.Fatalf("%s: %d ZIP entries", format, len(zr.File))
		}
		for i, file := range zr.File {
			r, err := file.Open()
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(r)
			_ = r.Close()
			if err != nil {
				t.Fatal(err)
			}
			wantName := datasets[i] + "." + format
			if file.Name != wantName || file.Method != zipEntryMethod(format) ||
				string(data) != datasets[i]+":"+format {
				t.Fatalf("%s: entry %q method=%d data=%q", format, file.Name, file.Method, data)
			}
		}
	}
}
