package clickhouse

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"
)

// BenchmarkBatchInsert §89：10K/50K/100K/500K 行每批的写入延迟（需要本地 ClickHouse）。
func BenchmarkBatchInsert(b *testing.B) {
	dsn := os.Getenv("CLICKHOUSE_URL")
	if dsn == "" {
		b.Skip("CLICKHOUSE_URL not set")
	}
	ctx := context.Background()
	conn, err := ch.Open(&ch.Options{Addr: []string{"127.0.0.1:9000"},
		Auth: ch.Auth{Database: "blockchain", Username: "blockchain", Password: "change_me_ch_2026"}})
	if err != nil {
		b.Skip("clickhouse unavailable:", err)
	}
	defer conn.Close()
	for _, batch := range []int{10_000, 50_000, 100_000, 500_000} {
		b.Run(fmt.Sprintf("batch_%d", batch), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				stmt, err := conn.PrepareBatch(ctx, "INSERT INTO blockchain.transactions "+
					"(chain, block_number, block_hash, tx_hash, timestamp, from_address, to_address, value_raw, nonce, gas, gas_used, gas_price, tx_type, status, input, method_id, source_provider) VALUES")
				if err != nil {
					b.Fatal(err)
				}
				ts := time.Now().UTC()
				for j := 0; j < batch; j++ {
					if err := stmt.Append("bench", uint64(j), "", fmt.Sprintf("0x%x", j), ts, "0x1", "0x2", "0x0", 0, 21000, 21000, "0x5", "0x0", 1, "0x", "", "bench"); err != nil {
						b.Fatal(err)
					}
				}
				if err := stmt.Send(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
