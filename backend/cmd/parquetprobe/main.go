// parquetprobe：导出文件验收用 Parquet 读取器。
//
// 使用与写出一致的显式 schema 读取行数，并对 amount_raw 做原值包含统计。
// 用“文件可打开 + 可逐行读取”作为 DATA-003 的解析证据，避免只验证 magic。
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/parquet-go/parquet-go"
)

type exportRow struct {
	Dataset          string   `parquet:"dataset"`
	Chain            string   `parquet:"chain"`
	BlockNumber      uint64   `parquet:"block_number"`
	BlockHash        string   `parquet:"block_hash"`
	TxHash           string   `parquet:"tx_hash"`
	Timestamp        string   `parquet:"timestamp"`
	FromAddress      string   `parquet:"from_address"`
	ToAddress        string   `parquet:"to_address"`
	Address          string   `parquet:"address"`
	LogIndex         uint64   `parquet:"log_index"`
	TokenAddress     string   `parquet:"token_address"`
	ContractAddress  string   `parquet:"contract_address"`
	TokenID          string   `parquet:"token_id"`
	TokenSymbol      string   `parquet:"token_symbol"`
	TokenName        string   `parquet:"token_name"`
	TokenDecimals    uint64   `parquet:"token_decimals"`
	AmountRaw        string   `parquet:"amount_raw"`
	Amount           string   `parquet:"amount"`
	ValueRaw         string   `parquet:"value_raw"`
	BalanceRaw       string   `parquet:"balance_raw"`
	Balance          string   `parquet:"balance"`
	BalanceUSD       string   `parquet:"balance_usd"`
	TokenPrice       string   `parquet:"token_price"`
	MethodID         string   `parquet:"method_id"`
	MethodName       string   `parquet:"method_name"`
	MethodSignature  string   `parquet:"method_signature"`
	MethodSource     string   `parquet:"method_source"`
	MethodVerified   string   `parquet:"method_verified"`
	MethodConfidence string   `parquet:"method_confidence"`
	MethodType       string   `parquet:"method_type"`
	DecodedInput     string   `parquet:"decoded_input"`
	EventName        string   `parquet:"event_name"`
	EventSignature   string   `parquet:"event_signature"`
	Input            string   `parquet:"input"`
	Data             string   `parquet:"data"`
	Topics           []string `parquet:"topics,list"`
	SourceProvider   string   `parquet:"source_provider"`
	SourceIngestedAt string   `parquet:"source_ingested_at"`
	MatchedAddress   string   `parquet:"matched_address"`
	CaseNames        string   `parquet:"case_names"`
	TagNames         string   `parquet:"tag_names"`
}

func main() {
	contains := flag.String("contains", "", "amount_raw 中统计包含该原值的行数")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: parquetprobe [-contains value] <file.parquet>")
		os.Exit(2)
	}
	path := flag.Arg(0)
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		fmt.Fprintf(os.Stderr, "stat: %v\n", err)
		os.Exit(1)
	}
	pfile, err := parquet.OpenFile(f, info.Size())
	if err != nil {
		fmt.Fprintf(os.Stderr, "parquet open: %v\n", err)
		os.Exit(1)
	}
	reader := parquet.NewGenericReader[exportRow](pfile)
	defer reader.Close()
	batch := make([]exportRow, 64)
	total, found := 0, 0
	for {
		n, err := reader.Read(batch)
		for i := 0; i < n; i++ {
			total++
			if *contains != "" && strings.Contains(batch[i].AmountRaw, *contains) {
				found++
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			fmt.Fprintf(os.Stderr, "read row %d: %v\n", total, err)
			os.Exit(1)
		}
	}
	fmt.Printf("rows=%d metadata_rows=%d contains=%d\n", total, pfile.NumRows(), found)
}
