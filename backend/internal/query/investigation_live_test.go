package query

import (
	"context"
	"fmt"
	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLiveInvestigationFullPaging(t *testing.T) {
	if os.Getenv("INVESTIGATION_LIVE") != "1" {
		t.Skip("explicit isolated ClickHouse fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ch, e := clickhouse.New(ctx, os.Getenv("CLICKHOUSE_URL"))
	if e != nil {
		t.Fatal(e)
	}
	defer ch.Close()
	name := fmt.Sprintf("blockchain.investigation_fixture_%d", time.Now().UnixNano())
	if e = ch.DB().Exec(ctx, "CREATE TABLE "+name+" AS blockchain.token_transfers ENGINE=Memory"); e != nil {
		t.Fatal(e)
	}
	defer ch.DB().Exec(context.Background(), "DROP TABLE IF EXISTS "+name)
	target := "0x" + strings.Repeat("a", 40)
	token := "0x" + strings.Repeat("b", 40)
	sql := `INSERT INTO ` + name + ` (chain,block_number,timestamp,tx_hash,log_index,token_address,token_symbol,token_decimals,from_address,to_address,amount_raw,amount,source_provider,source_ingested_at) SELECT 'bsc',123,toDateTime64('2026-09-29 00:00:00',3,'UTC'),concat('tx',toString(number)),0,?,'TEST',18,concat('payer',toString(number)),?,'1000000000000000001',toDecimal256('1.000000000000000001',38),'fixture',now64(3) FROM numbers(501)`
	if e = ch.DB().Exec(ctx, sql, token, target); e != nil {
		t.Fatal(e)
	}
	if e = ch.DB().Exec(ctx, sql, token, target); e != nil {
		t.Fatal(e)
	} // duplicate versions must not double counts
	seen := map[string]bool{}
	totalAmount := ""
	for offset := int64(0); offset < 501; offset += 97 {
		r := AggregateRequest{ActionFilter: ActionFilter{Chain: "bsc", Address: target, TokenContract: token, Direction: "in", PageSize: 97}, GroupBy: []string{"counterparty"}, OrderBy: "sum_amount", Offset: offset, Cutoff: 123, IngestedBefore: time.Now().Add(time.Minute)}
		sql, args, e := aggregateSQL(r)
		if e != nil {
			t.Fatal(e)
		}
		sql = strings.ReplaceAll(sql, "blockchain.token_transfers", name)
		rows, e := ch.DB().Query(ctx, sql, args...)
		if e != nil {
			t.Fatal(e)
		}
		returned := 0
		for rows.Next() {
			var key, asset, amount string
			var txs, n, b, groups, records uint64
			var first, last time.Time
			var fullAmount string
			var fullCutoff uint64
			if e = rows.Scan(&key, &asset, &amount, &txs, &n, &first, &last, &b, &groups, &records, &fullAmount, &fullCutoff); e != nil {
				t.Fatal(e)
			}
			if fullAmount != "501.000000000000000501" || fullCutoff != 123 {
				t.Fatal(fullAmount, fullCutoff)
			}
			if groups != 501 || records != 501 || n != 1 || amount != "1.000000000000000001" {
				t.Fatalf("dedup/precision lost groups=%d records=%d n=%d amount=%s", groups, records, n, amount)
			}
			if returned < 97 {
				if seen[key] {
					t.Fatal("duplicate page key")
				}
				seen[key] = true
			}
			returned++
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			t.Fatal(e)
		}
		if returned > 98 {
			t.Fatal("page bound violated")
		}
	}
	if len(seen) != 501 {
		t.Fatalf("only %d groups paged", len(seen))
	}
	if e = ch.DB().QueryRow(ctx, `SELECT toString(sum(amt)) FROM (SELECT argMax(amount,source_ingested_at) AS amt FROM `+name+` GROUP BY tx_hash,log_index,token_address)`).Scan(&totalAmount); e != nil {
		t.Fatal(e)
	}
	if totalAmount != "501.000000000000000501" {
		t.Fatal(totalAmount)
	}
	t.Logf("501 groups paged with no omission, dedup from 1002 rows, exact total=%s", totalAmount)
}
