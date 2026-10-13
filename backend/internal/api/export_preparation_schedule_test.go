package api

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPreparedCSVPublishesBeforeXLSXEncodingEnds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	input := filepath.Join(t.TempDir(), "input.jsonl")
	if err := os.WriteFile(input, []byte("shared snapshot"), 0600); err != nil {
		t.Fatal(err)
	}
	shared := []preparedDatasetParts{{dataset: "token_transfers", paths: map[string]string{"jsonl": input}}}
	xlsxPath := filepath.Join(t.TempDir(), "result.xlsx")
	encoding := make(chan struct{})
	release := make(chan struct{})
	csvReady := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- runPreparedExportFormats(ctx, []exportPreparation{{format: "xlsx"}, {format: "csv"}}, shared,
			func(ctx context.Context, input []preparedDatasetParts) ([]preparedDatasetParts, error) {
				close(encoding)
				select {
				case <-release:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				if _, err := os.ReadFile(input[0].paths["jsonl"]); err != nil {
					return nil, err
				}
				if err := os.WriteFile(xlsxPath, []byte("encoded"), 0600); err != nil {
					return nil, err
				}
				return []preparedDatasetParts{{dataset: "token_transfers", paths: map[string]string{"xlsx": xlsxPath}}}, nil
			}, func(ctx context.Context, p exportPreparation, ready []preparedDatasetParts) error {
				if p.format == "csv" {
					close(csvReady)
					return nil
				}
				_, err := os.ReadFile(ready[0].paths["xlsx"])
				return err
			})
	}()
	for _, signal := range []<-chan struct{}{encoding, csvReady} {
		select {
		case <-signal:
		case err := <-done:
			t.Fatalf("finished while XLSX should be blocked: %v", err)
		case <-ctx.Done():
			t.Fatal("CSV waited for blocked XLSX encoding")
		}
	}
	select {
	case err := <-done:
		t.Fatalf("returned before all consumers stopped: %v", err)
	default:
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(xlsxPath); !os.IsNotExist(err) {
		t.Fatalf("derived file was not cleaned: %v", err)
	}
	if _, err := os.Stat(input); err != nil {
		t.Fatalf("scheduler removed caller-owned input: %v", err)
	}
}

func TestPreparedFormatFailuresAreIndependent(t *testing.T) {
	for _, failure := range []string{"xlsx-encode", "csv-upload"} {
		t.Run(failure, func(t *testing.T) {
			boom := errors.New(failure)
			var mu sync.Mutex
			published := map[string]bool{}
			group := []exportPreparation{{format: "csv"}, {format: "xlsx"}, {format: "jsonl"}, {format: "parquet"}}
			err := runPreparedExportFormats(context.Background(), group, nil,
				func(context.Context, []preparedDatasetParts) ([]preparedDatasetParts, error) {
					if failure == "xlsx-encode" {
						return nil, boom
					}
					return nil, nil
				}, func(ctx context.Context, p exportPreparation, _ []preparedDatasetParts) error {
					if err := ctx.Err(); err != nil {
						return err
					}
					if p.format == "csv" && failure == "csv-upload" {
						return boom
					}
					mu.Lock()
					published[p.format] = true
					mu.Unlock()
					return nil
				})
			if !errors.Is(err, boom) || len(published) != 3 || !published["jsonl"] || !published["parquet"] {
				t.Fatalf("failure cancelled other formats: %v %v", err, published)
			}
		})
	}
}

func TestPreparedUploadsRemainBoundedAndObserveCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var active, peak atomic.Int32
	entered := make(chan struct{}, 4)
	done := make(chan error, 1)
	group := []exportPreparation{{format: "csv"}, {format: "jsonl"}, {format: "parquet"}, {format: "xlsx"}}
	go func() {
		done <- runPreparedExportFormats(ctx, group, nil,
			func(context.Context, []preparedDatasetParts) ([]preparedDatasetParts, error) { return nil, nil },
			func(ctx context.Context, _ exportPreparation, _ []preparedDatasetParts) error {
				n := active.Add(1)
				defer active.Add(-1)
				for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
				}
				entered <- struct{}{}
				<-ctx.Done()
				return ctx.Err()
			})
	}()
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("upload workers did not start")
		}
	}
	select {
	case <-entered:
		t.Fatal("more than two uploads started")
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || peak.Load() != 2 || active.Load() != 0 {
			t.Fatalf("cancel result: %v peak=%d active=%d", err, peak.Load(), active.Load())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled workers did not finish")
	}
}

func TestPreparedSchedulerRejectsDuplicateFormatsBeforeWork(t *testing.T) {
	for _, group := range [][]exportPreparation{{{format: "xlsx"}, {format: "xlsx"}}, {{format: "unknown"}}} {
		err := runPreparedExportFormats(context.Background(), group, nil,
			func(context.Context, []preparedDatasetParts) ([]preparedDatasetParts, error) {
				t.Fatal("invalid group began encoding")
				return nil, nil
			}, func(context.Context, exportPreparation, []preparedDatasetParts) error {
				t.Fatal("invalid group began publishing")
				return nil
			})
		if err == nil {
			t.Fatal("invalid group accepted")
		}
	}
}
