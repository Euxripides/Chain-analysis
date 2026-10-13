package query

import (
	"strings"
	"testing"
	"time"
)

func TestLiveTokenDiscoveryKeysetBackfillSnapshot(t *testing.T) {
	ctx, ch, tables := actionCorrectnessFixture(t)
	const target = "0x1111111111111111111111111111111111111111"
	const sender = "0x2222222222222222222222222222222222222222"
	token := func(letter string) string { return "0x" + strings.Repeat(letter, 40) }
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	watermark := base.Add(time.Second)
	insert := func(hash, contract string, block uint64, ingested time.Time) {
		t.Helper()
		query := `INSERT INTO ` + tables["token_transfers"] + ` (chain,tx_hash,log_index,token_address,block_number,timestamp,from_address,to_address,source_ingested_at) VALUES ('bsc',?,0,?,?,?,?,?,?)`
		if err := ch.DB().Exec(ctx, query, hash, contract, block, base, sender, target, ingested); err != nil {
			t.Fatal(err)
		}
	}
	for _, letter := range []string{"b", "c", "e"} {
		insert("initial_"+letter, token(letter), 200, base)
	}
	// A repeated transfer for B must not become a second discovered asset.
	insert("second_b", token("b"), 199, base)
	// Both snapshot bounds matter independently.
	insert("future_block", token("f"), 201, base)
	read := func(at time.Time, after string, limit int) []string {
		t.Helper()
		query, args, err := investigationTokensSQL("bsc", target, 200, at, after, limit)
		if err != nil {
			t.Fatal(err)
		}
		query = strings.ReplaceAll(query, "blockchain.token_transfers", tables["token_transfers"])
		rows, err := ch.DB().Query(ctx, query, args...)
		if err != nil {
			t.Fatal(err)
		}
		got := []string{}
		for rows.Next() {
			var value string
			if err := rows.Scan(&value); err != nil {
				t.Fatal(err)
			}
			got = append(got, value)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	first := read(watermark, "", 2)
	if len(first) != 3 || first[0] != token("b") || first[1] != token("c") || first[2] != token("e") {
		t.Fatalf("first page and lookahead: %v", first)
	}
	// Late historical backfill would shift an OFFSET page. The same cursor must
	// retain the original population and never repeat C or expose A/D.
	insert("late_a", token("a"), 190, watermark.Add(time.Second))
	insert("late_d", token("d"), 190, watermark.Add(time.Second))
	next := read(watermark, first[1], 2)
	if len(next) != 1 || next[0] != token("e") {
		t.Fatalf("backfill changed pinned page: %v", next)
	}
	if end := read(watermark, next[0], 2); len(end) != 0 {
		t.Fatalf("original snapshot has unexpected final-page assets: %v", end)
	}
	fresh := read(watermark.Add(2*time.Second), "", 10)
	want := []string{token("a"), token("b"), token("c"), token("d"), token("e")}
	if strings.Join(fresh, ",") != strings.Join(want, ",") {
		t.Fatalf("new snapshot should discover late backfill while retaining block bound: %v", fresh)
	}
	t.Log("Pinned discovery B/C -> E survived earlier/later backfill; fresh snapshot discovered A/D")
}
