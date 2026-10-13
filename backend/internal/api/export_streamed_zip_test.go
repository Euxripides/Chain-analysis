package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

func TestStreamedSingleAddressZIPIncludesAllDatasets(t *testing.T) {
	jobID := "00000000-0000-4000-8000-000000000001"
	exp := &model.Export{ID: "00000000-0000-4000-8000-000000000002", JobID: &jobID}
	job := &model.DownloadJob{Status: model.JobCompleted, Stats: map[string]any{"progress": 1}}
	datasets := []string{"transactions", "token_transfers"}
	parts := make([]streamedAddressPart, 0, len(datasets))
	for _, dataset := range datasets {
		path := filepath.Join(t.TempDir(), dataset+".xlsx")
		if err := os.WriteFile(path, []byte(dataset), 0600); err != nil {
			t.Fatal(err)
		}
		parts = append(parts, streamedAddressPart{dataset: dataset, path: path, rows: 1})
	}
	var output bytes.Buffer
	if err := writeStreamedSingleAddressZIP(&output, exp, job, "bsc", "0xsubject",
		datasets, "xlsx", true, parts); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(output.Bytes()), int64(output.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) != 3 {
		t.Fatalf("ZIP entries=%d, want 3", len(zr.File))
	}
	for i, entry := range zr.File {
		r, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(r)
		_ = r.Close()
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			var manifest map[string]any
			if err := json.Unmarshal(body, &manifest); err != nil {
				t.Fatal(err)
			}
			if entry.Name != "manifest.json" || manifest["include_decoded_input"] != true ||
				manifest["job_status"] != model.JobCompleted {
				t.Fatalf("manifest=%v", manifest)
			}
			continue
		}
		want := datasets[i-1]
		if entry.Name != "addresses/0xsubject/"+want+".xlsx" ||
			entry.Method != zip.Store || string(body) != want {
			t.Fatalf("entry=%q method=%d data=%q", entry.Name, entry.Method, body)
		}
	}
}

func TestStreamedQueryZIPWritesOneEntryPerDataset(t *testing.T) {
	exp := &model.Export{ID: "00000000-0000-4000-8000-000000000003"}
	datasets := []string{"transactions", "token_transfers"}
	var output bytes.Buffer
	rows, err := writeStreamedSingleAddressZIPEntries(&output, exp, nil, "bsc", "0xsubject",
		datasets, "csv", false, func(dataset string, entry io.Writer) (int64, error) {
			_, err := io.WriteString(entry, "hash\n"+dataset+"\n")
			return 1, err
		})
	if err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Fatalf("rows=%d, want 2", rows)
	}
	zr, err := zip.NewReader(bytes.NewReader(output.Bytes()), int64(output.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) != 3 {
		t.Fatalf("ZIP entries=%d, want 3", len(zr.File))
	}
	for i, dataset := range datasets {
		entry := zr.File[i+1]
		if entry.Name != "addresses/0xsubject/"+dataset+".csv" || entry.Method != zip.Deflate {
			t.Fatalf("entry=%q method=%d", entry.Name, entry.Method)
		}
		r, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(r)
		_ = r.Close()
		if err != nil || string(body) != "hash\n"+dataset+"\n" {
			t.Fatalf("dataset=%s body=%q err=%v", dataset, body, err)
		}
	}
}
