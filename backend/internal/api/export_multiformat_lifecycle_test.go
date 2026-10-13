package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/xuri/excelize/v2"
)

func preparedLifecycleTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// These tests change process environment and must not run in parallel.
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, dir)
	}
	return dir
}

func preparedLifecycleInput(t *testing.T, dataset, content string, rows int64) preparedDatasetParts {
	t.Helper()
	path := filepath.Join(t.TempDir(), dataset+".jsonl")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return preparedDatasetParts{dataset: dataset, rows: rows,
		columns: []string{"value"}, paths: map[string]string{"jsonl": path}}
}

func assertPreparedLifecycleClean(t *testing.T, dir string, shared []preparedDatasetParts, contents []string) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "export-derived-*.xlsx"))
	if err != nil || len(paths) != 0 {
		t.Fatalf("derived files remain after cleanup: %v, %v", paths, err)
	}
	for i, part := range shared {
		if len(part.paths) != 1 || part.paths["jsonl"] == "" {
			t.Fatalf("shared path ownership changed: %v", part.paths)
		}
		data, err := os.ReadFile(part.paths["jsonl"])
		if err != nil || string(data) != contents[i] {
			t.Fatalf("shared %s changed or disappeared: %q, %v", part.dataset, data, err)
		}
	}
}

func TestPreparedXLSXFailureCleansEarlierDataset(t *testing.T) {
	dir := preparedLifecycleTempDir(t)
	contents := []string{"{\"value\":\"complete first dataset\"}\n", "{\"value\":\"valid prefix\"}\n{broken json\n"}
	shared := []preparedDatasetParts{
		preparedLifecycleInput(t, "transactions", contents[0], 1),
		preparedLifecycleInput(t, "token_transfers", contents[1], 2),
	}
	parts, err := buildPreparedXLSXParts(context.Background(), shared)
	var syntaxErr *json.SyntaxError
	if !errors.As(err, &syntaxErr) || parts != nil {
		t.Fatalf("corrupt second dataset must fail the derived batch: parts=%v, err=%v", parts, err)
	}
	assertPreparedLifecycleClean(t, dir, shared, contents)
}

// Cancel at the first cancellation check after the second derived file exists.
// This gives a deterministic mid-batch cancellation without polling or sleeps:
// the first workbook has completed and the second output is already owned.
type preparedLifecycleCancelContext struct {
	context.Context
	cancel   context.CancelFunc
	dir      string
	observed bool
}

func (ctx *preparedLifecycleCancelContext) Err() error {
	paths, _ := filepath.Glob(filepath.Join(ctx.dir, "export-derived-*.xlsx"))
	if len(paths) == 2 {
		ctx.observed = true
		ctx.cancel()
	}
	return ctx.Context.Err()
}

func TestPreparedXLSXCancellationCleansAllDerivedFiles(t *testing.T) {
	dir := preparedLifecycleTempDir(t)
	contents := []string{"{\"value\":\"first dataset\"}\n", "{\"value\":\"second dataset\"}\n"}
	shared := []preparedDatasetParts{
		preparedLifecycleInput(t, "transactions", contents[0], 1),
		preparedLifecycleInput(t, "token_transfers", contents[1], 1),
	}
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &preparedLifecycleCancelContext{Context: parent, cancel: cancel, dir: dir}
	parts, err := buildPreparedXLSXParts(ctx, shared)
	if !ctx.observed || !errors.Is(err, context.Canceled) || parts != nil {
		t.Fatalf("mid-batch cancellation: observed=%v, parts=%v, err=%v", ctx.observed, parts, err)
	}
	assertPreparedLifecycleClean(t, dir, shared, contents)
}

func TestPreparedEmptyXLSXDatasetsRemainReadable(t *testing.T) {
	dir := preparedLifecycleTempDir(t)
	shared := []preparedDatasetParts{
		preparedLifecycleInput(t, "transactions", "", 0),
		preparedLifecycleInput(t, "token_transfers", "", 0),
	}
	// Empty scans have no discovered columns.
	for i := range shared {
		shared[i].columns = nil
	}
	parts, err := buildPreparedXLSXParts(context.Background(), shared)
	if err != nil {
		t.Fatal(err)
	}
	defer removePreparedDatasetParts(parts)
	if len(parts) != len(shared) {
		t.Fatalf("got %d output datasets, want %d", len(parts), len(shared))
	}
	for i, part := range parts {
		if part.dataset != shared[i].dataset || part.rows != 0 || len(part.paths) != 1 || part.paths["xlsx"] == "" {
			t.Fatalf("invalid empty output ownership: %+v", part)
		}
		book, err := excelize.OpenFile(part.paths["xlsx"])
		if err != nil {
			t.Fatalf("open empty %s: %v", part.dataset, err)
		}
		sheets := book.GetSheetList()
		rows, readErr := book.GetRows("Sheet1")
		closeErr := book.Close()
		if !reflect.DeepEqual(sheets, []string{"Sheet1"}) || len(rows) != 0 || readErr != nil || closeErr != nil {
			t.Fatalf("invalid empty workbook: sheets=%v, rows=%v, read=%v, close=%v", sheets, rows, readErr, closeErr)
		}
	}
	removePreparedDatasetParts(parts)
	assertPreparedLifecycleClean(t, dir, shared, []string{"", ""})
}

type preparedLifecycleCancelWriter struct {
	bytes.Buffer
	cancel context.CancelFunc
	writes int
}

func (w *preparedLifecycleCancelWriter) Write(p []byte) (int, error) {
	w.writes++
	n, err := w.Buffer.Write(p)
	w.cancel()
	return n, err
}

func TestStreamedXLSXCancellationDuringArchiveWrite(t *testing.T) {
	preparedLifecycleTempDir(t)
	var input bytes.Buffer
	encoder := json.NewEncoder(&input)
	const count = 1024
	for i := 0; i < count; i++ {
		digest := sha256.Sum256([]byte(fmt.Sprint(i)))
		if err := encoder.Encode(map[string]string{"value": fmt.Sprintf("%x", digest)}); err != nil {
			t.Fatal(err)
		}
	}
	part := preparedLifecycleInput(t, "transactions", input.String(), count)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	output := &preparedLifecycleCancelWriter{cancel: cancel}
	err := writeStreamedXLSXContext(ctx, output, part.paths["jsonl"], count, part.columns)
	if !errors.Is(err, context.Canceled) || output.writes != 1 || output.Len() == 0 {
		t.Fatalf("archive write ignored cancellation: writes=%d, bytes=%d, err=%v", output.writes, output.Len(), err)
	}
	data, err := os.ReadFile(part.paths["jsonl"])
	if err != nil || !bytes.Equal(data, input.Bytes()) {
		t.Fatalf("archive cancellation changed its input: %v", err)
	}
	// A live spool handle would prevent deletion on Windows.
	if err := os.Remove(part.paths["jsonl"]); err != nil {
		t.Fatalf("archive cancellation leaked the input handle: %v", err)
	}
}
