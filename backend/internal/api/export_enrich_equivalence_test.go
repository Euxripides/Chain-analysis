package api

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/app"
	"github.com/blockchain-platform/blockchain-platform/internal/config"
)

/*
 * 流式富化（按块）与批量富化（一次全量）必须产出**逐列相同**的行。
 *
 * 这条不变式比查询层的等价性更容易破：两边的实现是分开的两段代码
 * （attachTokenMetadata vs streamEnrichment.enrich），任何一边漏写一个派生列、
 * 或者对"合约已知但元数据缺失"的处理不同，导出的 CSV 列集与取值就会不一致。
 * 本轮就修过一个这样的缺陷（缺元数据的行少三列）。
 *
 * 需要真实依赖，未配置时跳过：
 *
 *	EXPORT_EQUIV_SUBJECT=0x... go test ./internal/api/ -run TestStreamEnrichMatchesBatch -v
 */
func TestStreamEnrichMatchesBatch(t *testing.T) {
	subject := os.Getenv("EXPORT_EQUIV_SUBJECT")
	if subject == "" {
		t.Skip("EXPORT_EQUIV_SUBJECT not set")
	}
	ctx := context.Background()
	app.TuneExportGC()
	cfg := config.Load()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	infra, err := app.Bootstrap(ctx, cfg, log)
	if err != nil {
		t.Skipf("bootstrap unavailable: %v", err)
	}
	srv := New(Deps{
		Cfg: cfg, Store: infra.Store, CH: infra.CH, Raw: infra.Raw, Log: log,
		TokenMeta: infra.TokenMeta, MethodData: infra.MethodData,
	})

	// 取两批行：一批走批量富化，一批走流式富化。用同一份数据的两份拷贝，
	// 避免原地修改互相影响。
	limit := 20000
	rows, err := srv.query.RowsForExportRange(ctx, "token_transfers", "bsc", subject, limit, 0, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(rows) == 0 {
		t.Skip("no rows")
	}
	batchRows := copyRows(rows)
	streamRows := copyRows(rows)

	// 批量：一次全量富化（生产路径 writeSingleExport 的调用方式）。
	srv.attachTokenMetadata(ctx, "bsc", batchRows)

	// 流式：按 exportStreamChunk 分块富化（生产路径 writeSingleExportStream 的方式）。
	enrich := newStreamEnrichment(srv, "bsc", "", false)
	for start := 0; start < len(streamRows); start += exportStreamChunk {
		end := start + exportStreamChunk
		if end > len(streamRows) {
			end = len(streamRows)
		}
		enrich.enrich(ctx, subject, streamRows[start:end])
	}

	keyOf := func(r map[string]any) string {
		return fmt.Sprintf("%v|%v|%v", r["tx_hash"], r["log_index"], r["token_address"])
	}
	byKey := func(rs []map[string]any) map[string]map[string]any {
		out := make(map[string]map[string]any, len(rs))
		for _, r := range rs {
			out[keyOf(r)] = r
		}
		return out
	}
	batch := byKey(batchRows)
	stream := byKey(streamRows)
	if len(batch) != len(stream) {
		t.Fatalf("row key count differs: batch=%d stream=%d", len(batch), len(stream))
	}

	diffCols := map[string]int{}
	diffRows := 0
	var sample string
	for key, brow := range batch {
		srow, ok := stream[key]
		if !ok {
			t.Fatalf("key %s missing from stream rows", key)
		}
		rowDiff := false
		if len(brow) != len(srow) {
			t.Fatalf("column count differs for %s: batch=%d stream=%d", key, len(brow), len(srow))
		}
		for col, bval := range brow {
			sval, ok := srow[col]
			if !ok {
				diffCols[col+"(missing)"]++
				rowDiff = true
				continue
			}
			if fmt.Sprint(bval) != fmt.Sprint(sval) {
				diffCols[col]++
				rowDiff = true
				if sample == "" {
					sample = fmt.Sprintf("row=%s col=%s batch=%q stream=%q", key, col, fmt.Sprint(bval), fmt.Sprint(sval))
				}
			}
		}
		if rowDiff {
			diffRows++
		}
	}
	if diffRows > 0 {
		keys := make([]string, 0, len(diffCols))
		for k := range diffCols {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		t.Fatalf("%d/%d rows differ (cols=%v); first: %s", diffRows, len(batch), keys, sample)
	}
	t.Logf("stream enrich == batch enrich over %d rows × %d columns", len(batch), len(batchRows[0]))
}

func copyRows(rows []map[string]any) []map[string]any {
	out := make([]map[string]any, len(rows))
	for i, r := range rows {
		m := make(map[string]any, len(r))
		for k, v := range r {
			m[k] = v
		}
		out[i] = m
	}
	return out
}
