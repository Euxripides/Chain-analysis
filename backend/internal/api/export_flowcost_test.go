package api

import (
	"context"
	"io"
	"log/slog"
	"os"
	"runtime/pprof"
	"strconv"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/app"
	"github.com/blockchain-platform/blockchain-platform/internal/config"
)

/*
 * 导出「点击到可下载」的真实全链路基线：查询 + 逐行富化 + CSV 序列化。
 *
 * 需要完整依赖（PG/MinIO/ClickHouse），未配置时跳过：
 *
 *	EXPORT_FLOW_SUBJECT=0x... go test ./internal/api/ -run TestExportFlowCost -v -timeout 20m
 *
 * 用途：判断流式直写还剩多少可省——若富化占大头，就得先解决富化再谈流式。
 */
func TestExportFlowCost(t *testing.T) {
	subject := os.Getenv("EXPORT_FLOW_SUBJECT")
	if subject == "" {
		t.Skip("EXPORT_FLOW_SUBJECT not set")
	}
	limit := 200000
	if raw := os.Getenv("EXPORT_FLOW_LIMIT"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}

	ctx := context.Background()
	app.TuneExportGC()
	cfg := config.Load()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	infra, err := app.Bootstrap(ctx, cfg, log)
	if err != nil {
		t.Skipf("bootstrap unavailable: %v", err)
	}

	srv := New(Deps{
		Cfg: cfg, Store: infra.Store, CH: infra.CH, Raw: infra.Raw, Log: log,
		TokenMeta: infra.TokenMeta, MethodData: infra.MethodData,
	})

	const dataset = "token_transfers"
	const chain = "bsc"

	// 1) 查询层：无 FINAL + 快路径 + 流式去重
	start := time.Now()
	rows, err := srv.query.RowsForExportRange(ctx, dataset, chain, subject, limit, 0, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	queryDur := time.Since(start)
	t.Logf("查询层               %8s  %d 行", queryDur.Round(time.Millisecond), len(rows))

	// 2) API 层去重（查询层已按去重键收敛，这里是幂等的第二遍）
	start = time.Now()
	rows = dedupeExportRows(dataset, rows)
	dedupeDur := time.Since(start)
	t.Logf("API 层去重           %8s  %d 行", dedupeDur.Round(time.Millisecond), len(rows))

	if prof := os.Getenv("EXPORT_FLOW_CPUPROFILE"); prof != "" {
		f, err := os.Create(prof)
		if err == nil {
			pprof.StartCPUProfile(f)
			defer pprof.StopCPUProfile()
		}
	}

	// 3) attachTokenMetadata：逐行写 token_contract/raw_value/formatted_amount 等
	start = time.Now()
	srv.attachTokenMetadata(ctx, chain, rows)
	tokenDur := time.Since(start)
	t.Logf("attachTokenMetadata  %8s", tokenDur.Round(time.Millisecond))

	// 3b) 单独量 TokenMeta.GetMany（判断 3.2 s 里有多少是查元数据、多少是逐行计算）
	if srv.deps.TokenMeta != nil {
		contracts := map[string]struct{}{}
		for _, r := range rows {
			if c, ok := r["token_address"].(string); ok && c != "" {
				contracts[c] = struct{}{}
			}
		}
		list := make([]string, 0, len(contracts))
		for c := range contracts {
			list = append(list, c)
		}
		start = time.Now()
		if _, err := srv.deps.TokenMeta.GetMany(ctx, chain, list); err != nil {
			t.Logf("GetMany err: %v", err)
		}
		t.Logf("TokenMeta.GetMany    %8s  %d 个合约", time.Since(start).Round(time.Millisecond), len(list))
	}

	// 4) attachDecodedInput（token_transfers + 未启用时立即返回）
	start = time.Now()
	srv.attachDecodedInput(ctx, chain, dataset, rows, false)
	decodedDur := time.Since(start)
	t.Logf("attachDecodedInput   %8s", decodedDur.Round(time.Millisecond))

	// 5) CSV 序列化（现网 streamCSV 口径，写 io.Discard 以只量 CPU）
	start = time.Now()
	if err := streamCSV(io.Discard, rows); err != nil {
		t.Fatalf("csv: %v", err)
	}
	csvDur := time.Since(start)
	t.Logf("CSV 序列化           %8s", csvDur.Round(time.Millisecond))

	total := queryDur + dedupeDur + tokenDur + decodedDur + csvDur
	t.Logf("合计（不含上传/SHA） %7s", total.Round(time.Millisecond))
}
