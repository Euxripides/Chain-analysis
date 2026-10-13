package query

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
)

// These tests execute the production SQL helpers against isolated tables with
// the actual schema. They never write to the indexed transaction tables.
func actionCorrectnessFixture(t *testing.T) (context.Context, *clickhouse.Conn, map[string]string) {
	t.Helper()
	if os.Getenv("MCP_PERFORMANCE_LIVE") != "1" {
		t.Skip("explicit isolated ClickHouse correctness fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	ch, err := clickhouse.New(ctx, os.Getenv("CLICKHOUSE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ch.Close() })
	tables := map[string]string{}
	for _, dataset := range []string{"transactions", "token_transfers", "internal_transactions"} {
		name := fmt.Sprintf("blockchain.mcp_action_fixture_%s_%d", tableName(dataset), time.Now().UnixNano())
		if err = ch.DB().Exec(ctx, "CREATE TABLE "+name+" AS blockchain."+tableName(dataset)+" ENGINE=Memory"); err != nil {
			t.Fatal(err)
		}
		tables[dataset] = name
		t.Cleanup(func() {
			cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
			defer done()
			if err := ch.DB().Exec(cleanup, "DROP TABLE IF EXISTS "+name); err != nil {
				t.Errorf("drop isolated fixture: %v", err)
			}
		})
	}
	return ctx, ch, tables
}

func TestLiveActionPrecisionAndLatestStatus(t *testing.T) {
	ctx, ch, tables := actionCorrectnessFixture(t)
	for _, tc := range []struct {
		chain, raw, amount, minimum, maximum string
	}{
		{"bsc", "1", "0.000000000000000001", "0.000000000000000001", "0.000000000000000001"},
		{"eth", "1500000000000000001", "1.500000000000000001", "1.5", "1.500000000000000001"},
		{"tron", "1000001", "1.000001", "1.000001", "1.000001"},
		{"solana", "1000000001", "1.000000001", "1.000000001", "1.000000001"},
	} {
		expr := actionNativeAmountSQL(tc.chain)
		var amount string
		var matched uint8
		query := "SELECT toString(" + expr + "),toUInt8(" + expr + ">=toDecimal256(?,18) AND " + expr + "<=toDecimal256(?,18)) FROM (SELECT ? AS value_raw)"
		if err := ch.DB().QueryRow(ctx, query, tc.minimum, tc.maximum, tc.raw).Scan(&amount, &matched); err != nil {
			t.Fatal(err)
		}
		if amount != tc.amount || matched != 1 {
			t.Fatalf("%s exact amount/filter: amount=%q matched=%d", tc.chain, amount, matched)
		}
	}
	const target = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const payer = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	insert := `INSERT INTO ` + tables["transactions"] + ` (chain,tx_hash,timestamp,from_address,to_address,value_raw,status,source_ingested_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for _, row := range []struct {
		hash, value string
		status      uint8
		time, ver   int
	}{
		{"failed", "99000000000000000000", 0, 0, 1},
		{"corrected_status", "1500000000000000000", 1, 1, 1},
		{"corrected_status", "1500000000000000000", 0, 1, 2},
		{"corrected_value", "3000000000000000000", 1, 2, 1},
		{"corrected_value", "0", 1, 2, 2},
		{"actual_funding", "1000000000000000001", 1, 3, 1},
	} {
		// Use whole seconds so fixture versions do not depend on the column or
		// driver preserving subsecond precision during parameter binding.
		if err := ch.DB().Exec(ctx, insert, "bsc", row.hash, base.Add(time.Duration(row.time)*time.Second), payer, target, row.value, row.status, base.Add(time.Duration(row.ver)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	var versions uint64
	if err := ch.DB().QueryRow(ctx, "SELECT uniqExact(source_ingested_at) FROM "+tables["transactions"]+" WHERE tx_hash='corrected_status'").Scan(&versions); err != nil || versions != 2 {
		t.Fatalf("fixture must preserve two distinct stored versions: versions=%d err=%v", versions, err)
	}
	first := strings.ReplaceAll(firstNativeFundingSQL("bsc"), "blockchain.transactions", tables["transactions"])
	var from, amount, raw, hash string
	var timestamp time.Time
	if err := ch.DB().QueryRow(ctx, first, "bsc", target, target).Scan(&from, &amount, &raw, &hash, &timestamp); err != nil {
		t.Fatal(err)
	}
	if hash != "actual_funding" || raw != "1000000000000000001" || amount != "1.000000000000000001" {
		t.Fatalf("first successful exact funding: hash=%s raw=%s amount=%s", hash, raw, amount)
	}
	latestTx := strings.ReplaceAll(dedupSource("transactions", "chain=? AND to_address=?"), "blockchain.transactions", tables["transactions"])
	for _, status := range []uint8{0, 1} {
		query := "SELECT tx_hash FROM (" + actionLatestStatusSource(latestTx, "status=?") + ") ORDER BY tx_hash"
		rows, err := ch.DB().Query(ctx, query, "bsc", target, status)
		if err != nil {
			t.Fatal(err)
		}
		got := []string{}
		for rows.Next() {
			var hash string
			if err = rows.Scan(&hash); err != nil {
				t.Fatal(err)
			}
			got = append(got, hash)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		want := "actual_funding,corrected_value"
		if status == 0 {
			want = "corrected_status,failed"
		}
		if strings.Join(got, ",") != want {
			t.Fatalf("latest status %d: %v", status, got)
		}
	}
	if err := ch.DB().Exec(ctx, `INSERT INTO `+tables["token_transfers"]+` (chain,tx_hash,log_index,token_address,timestamp,from_address,to_address,source_ingested_at) SELECT 'bsc',tx_hash,0,'token',min(timestamp),?, ?,max(source_ingested_at) FROM `+tables["transactions"]+` GROUP BY tx_hash`, payer, target); err != nil {
		t.Fatal(err)
	}
	join, args := actionTokenStatusJoin(ActionFilter{Chain: "bsc"}, 1)
	query := "SELECT tx_hash FROM (" + dedupSource("token_transfers", "chain=? AND to_address=?") + ") AS t" + join + " ORDER BY tx_hash"
	query = strings.ReplaceAll(strings.ReplaceAll(query, "blockchain.transactions", tables["transactions"]), "blockchain.token_transfers", tables["token_transfers"])
	rows, err := ch.DB().Query(ctx, query, append([]any{"bsc", target}, args...)...)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for rows.Next() {
		var hash string
		if err = rows.Scan(&hash); err != nil {
			t.Fatal(err)
		}
		got = append(got, hash)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || strings.Join(got, ",") != "actual_funding,corrected_value" {
		t.Fatalf("token transfer latest-status JOIN: got=%v err=%v", got, err)
	}
	if err := ch.DB().Exec(ctx, `INSERT INTO `+tables["internal_transactions"]+` (chain,tx_hash,call_index,from_address,to_address,success,traced_at,timestamp) VALUES ('bsc','movement',0,?,?,1,?,?),('bsc','movement',0,?,?,0,?,?)`, payer, target, base, base, payer, target, base.Add(time.Second), base); err != nil {
		t.Fatal(err)
	}
	latestCall := strings.ReplaceAll(dedupSource("internal_transactions", "chain='bsc'"), "blockchain.internal_calls", tables["internal_transactions"])
	var successful uint64
	if err := ch.DB().QueryRow(ctx, "SELECT count() FROM ("+actionLatestStatusSource(latestCall, "success=1")+")").Scan(&successful); err != nil || successful != 0 {
		t.Fatalf("corrected internal movement must be failed: successes=%d err=%v", successful, err)
	}
	t.Log("Exact native decimals, corrected transaction/internal statuses, and actual first funding verified")
}

func TestLiveActionTiePagination(t *testing.T) {
	ctx, ch, tables := actionCorrectnessFixture(t)
	const total = 53
	if err := ch.DB().Exec(ctx, `INSERT INTO `+tables["token_transfers"]+` (chain,tx_hash,log_index,token_address,timestamp,amount,source_ingested_at) SELECT 'bsc',concat('tie',toString(intDiv(number,4))),toUInt32(number%2),concat('token',toString(intDiv(number%4,2))),toDateTime64('2026-09-01 00:00:00',3,'UTC'),toDecimal256('1.000000000000000001',38),now64(3) FROM numbers(53)`); err != nil {
		t.Fatal(err)
	}
	source := strings.ReplaceAll(dedupSource("token_transfers", "chain='bsc'"), "blockchain.token_transfers", tables["token_transfers"])
	for _, primary := range []string{"timestamp ASC", "timestamp DESC", "amount DESC, timestamp DESC"} {
		seen := map[string]bool{}
		for offset := 0; offset < total; offset += 7 {
			rows, err := ch.DB().Query(ctx, "SELECT tx_hash,log_index,token_address FROM ("+source+") ORDER BY "+stableActionOrder("token_transfers", primary)+" LIMIT 7 OFFSET ?", offset)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var hash, token string
				var index uint32
				if err = rows.Scan(&hash, &index, &token); err != nil {
					t.Fatal(err)
				}
				key := fmt.Sprintf("%s:%d:%s", hash, index, token)
				if seen[key] {
					t.Fatalf("%s duplicated page key %s", primary, key)
				}
				seen[key] = true
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
		if len(seen) != total {
			t.Fatalf("%s paged %d/%d tied rows", primary, len(seen), total)
		}
	}
	// Internal movements have call_index, not log/instruction columns. Execute
	// both sorting-key shapes against the actual schema to catch invalid fields.
	for _, dataset := range []string{"transactions", "internal_transactions"} {
		rows, err := ch.DB().Query(ctx, "SELECT * FROM "+tables[dataset]+" ORDER BY "+stableActionOrder(dataset, "timestamp DESC")+" LIMIT 1")
		if err != nil {
			t.Fatalf("%s stable sorting schema: %v", dataset, err)
		}
		rows.Close()
	}
	t.Log("53 same-timestamp, same-amount transfer rows paged exactly once in all three sort modes")
}

func TestLiveActionAmountCorrectionFiltering(t *testing.T) {
	ctx, ch, tables := actionCorrectnessFixture(t)
	const target = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const payer = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	const contract = "0xcccccccccccccccccccccccccccccccccccccccc"
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for _, row := range []struct {
		hash, amount string
		version      int
	}{
		{"amount_dropped", "100", 0},
		{"amount_dropped", "1", 1},
		{"amount_raised", "1", 0},
		{"amount_raised", "100", 1},
	} {
		raw := row.amount + strings.Repeat("0", 18)
		version := base.Add(time.Duration(row.version) * time.Second)
		if err := ch.DB().Exec(ctx, `INSERT INTO `+tables["transactions"]+` (chain,tx_hash,timestamp,from_address,to_address,value_raw,status,source_ingested_at) VALUES ('bsc',?,?,?,?,?,1,?)`, row.hash, base, payer, target, raw, version); err != nil {
			t.Fatal(err)
		}
		if err := ch.DB().Exec(ctx, `INSERT INTO `+tables["token_transfers"]+` (chain,tx_hash,log_index,token_address,token_symbol,token_decimals,timestamp,from_address,to_address,amount_raw,amount,source_ingested_at) VALUES ('bsc',?,0,?,'TEST',18,?,?,?,?,toDecimal256(?,38),?)`, row.hash, contract, base, payer, target, raw, row.amount, version); err != nil {
			t.Fatal(err)
		}
		if err := ch.DB().Exec(ctx, `INSERT INTO `+tables["internal_transactions"]+` (chain,tx_hash,call_index,timestamp,from_address,to_address,value_raw,success,traced_at) VALUES ('bsc',?,0,?,?,?,?,1,?)`, row.hash, base, payer, target, raw, version); err != nil {
			t.Fatal(err)
		}
	}
	for _, dataset := range []string{"transactions", "token_transfers", "internal_transactions"} {
		amountExpr, scale := actionNativeAmountSQL("bsc"), 18
		if dataset == "token_transfers" {
			amountExpr, scale = "amount", 38
		}
		source := strings.ReplaceAll(dedupSource(dataset, "chain='bsc' AND to_address=?"), "blockchain."+tableName(dataset), tables[dataset])
		for _, filter := range []struct{ operator, want string }{{">=", "amount_raised"}, {"<=", "amount_dropped"}} {
			predicate := amountExpr + filter.operator + fmt.Sprintf("toDecimal256(?,%d)", scale)
			query := "SELECT tx_hash FROM (" + actionLatestStatusSource(source, predicate) + ") ORDER BY tx_hash"
			rows, err := ch.DB().Query(ctx, query, target, "10")
			if err != nil {
				t.Fatal(err)
			}
			got := []string{}
			for rows.Next() {
				var hash string
				if err := rows.Scan(&hash); err != nil {
					t.Fatal(err)
				}
				got = append(got, hash)
			}
			err = rows.Err()
			rows.Close()
			if err != nil || len(got) != 1 || got[0] != filter.want {
				t.Fatalf("%s corrected amount %s10: got=%v err=%v", dataset, filter.operator, got, err)
			}
		}
	}
	// Asset observed-flow aggregation must also select the latest transfer
	// versions: corrected 1 + corrected 100 is 101, with two records, not 202.
	flowSource := strings.ReplaceAll(dedupSource("token_transfers", "chain=? AND (from_address=? OR to_address=?)"), "blockchain.token_transfers", tables["token_transfers"])
	query := `SELECT token_address,any(token_symbol),any(token_decimals),toString(sumIf(amount,to_address=?)-sumIf(amount,from_address=?)),min(timestamp),max(timestamp),count() FROM (` + flowSource + `) GROUP BY token_address`
	var gotContract, symbol, net string
	var decimals uint16
	var first, last time.Time
	var count uint64
	if err := ch.DB().QueryRow(ctx, query, target, target, "bsc", target, target).Scan(&gotContract, &symbol, &decimals, &net, &first, &last, &count); err != nil {
		t.Fatal(err)
	}
	if gotContract != contract || symbol != "TEST" || decimals != 18 || net != "101" || count != 2 {
		t.Fatalf("latest asset flow: contract=%s symbol=%s decimals=%d net=%s count=%d", gotContract, symbol, decimals, net, count)
	}
	t.Log("Token and native corrected amounts filter after dedup; asset flow retains exactly the latest amounts")
}
