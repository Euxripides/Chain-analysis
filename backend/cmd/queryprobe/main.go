// queryprobe：本地数据面探针，用于导出/解析验收定位真实 ClickHouse 行为。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
	"github.com/blockchain-platform/blockchain-platform/internal/config"
	"github.com/blockchain-platform/blockchain-platform/internal/query"
)

func main() {
	dataset := flag.String("dataset", "token_transfers", "dataset")
	chainSlug := flag.String("chain", "bsc", "chain")
	subject := flag.String("subject", "", "address")
	fromBlock := flag.Int64("from-block", 0, "from block (0 means unset)")
	toBlock := flag.Int64("to-block", 0, "to block (0 means unset)")
	limit := flag.Int("limit", 100, "limit")
	flag.Parse()
	if *subject == "" {
		fmt.Fprintln(os.Stderr, "subject required")
		os.Exit(2)
	}
	cfg := config.Load()
	ctx := context.Background()
	ch, err := clickhouse.New(ctx, cfg.ClickHouseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "clickhouse: %v\n", err)
		os.Exit(1)
	}
	defer ch.Close()
	svc := query.New(ch)
	var from, to *int64
	if *fromBlock != 0 {
		from = fromBlock
	}
	if *toBlock != 0 {
		to = toBlock
	}
	rows, err := svc.RowsForExportRange(ctx, *dataset, *chainSlug, *subject, *limit, 0, from, to, nil, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "query: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("rows=%d\n", len(rows))
	if len(rows) > 0 {
		fmt.Printf("first_keys=%d tx_hash=%v amount_raw=%v\n", len(rows[0]), rows[0]["tx_hash"], rows[0]["amount_raw"])
	}
}
