package query

import (
	"strings"
	"testing"
	"time"
)

func TestLiveInvestigationLedgerLatestPositiveOrderAndLookahead(t *testing.T) {
	ctx, ch, tables := actionCorrectnessFixture(t)
	const target = "0x1111111111111111111111111111111111111111"
	const payer = "0x2222222222222222222222222222222222222222"
	const third = "0x3333333333333333333333333333333333333333"
	const token = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const otherToken = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	insert := `INSERT INTO ` + tables["token_transfers"] + ` (chain,tx_hash,log_index,token_address,token_decimals,block_number,timestamp,from_address,to_address,amount_raw,amount,source_ingested_at) VALUES ('bsc',?,?,?,18,?,?,?,?,?,toDecimal256(?,38),?)`
	for _, row := range []struct {
		hash               string
		block              uint64
		index              uint32
		from, to, contract string
		raw, amount        string
		version            int
	}{
		{"earlier_block", 99, 8, payer, target, token, "1000000000000000000", "1", 0},
		{"later_log", 100, 7, target, third, token, "2000000000000000000", "2", 0},
		{"earlier_log", 100, 2, payer, target, token, "3000000000000000000", "3", 0},
		{"end_block_dust", 101, 0, payer, target, token, "1", "0.000000000000000001", 0},
		{"corrected_zero", 100, 3, payer, target, token, "1000000000000000000", "1", 0},
		{"corrected_zero", 100, 3, payer, target, token, "0", "0", 1},
		{"explicit_zero", 100, 4, payer, target, token, "0", "0", 0},
		{"self", 100, 5, target, target, token, "1000000000000000000", "1", 0},
		{"unrelated", 100, 6, payer, third, token, "1000000000000000000", "1", 0},
		{"other_token", 100, 9, payer, target, otherToken, "1000000000000000000", "1", 0},
		{"too_early", 98, 0, payer, target, token, "1000000000000000000", "1", 0},
		{"too_late", 102, 0, payer, target, token, "1000000000000000000", "1", 0},
	} {
		if err := ch.DB().Exec(ctx, insert, row.hash, row.index, row.contract, row.block, base.Add(time.Duration(row.block)*time.Second), row.from, row.to, row.raw, row.amount, base.Add(time.Duration(row.version)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	read := func(limit int) []LedgerTransfer {
		t.Helper()
		query, args := investigationLedgerSQL("bsc", token, []string{target}, 99, 101, limit)
		query = strings.ReplaceAll(query, "blockchain.token_transfers", tables["token_transfers"])
		rows, err := ch.DB().Query(ctx, query, args...)
		if err != nil {
			t.Fatal(err)
		}
		got := []LedgerTransfer{}
		for rows.Next() {
			var row LedgerTransfer
			if err := rows.Scan(&row.Hash, &row.Index, &row.Block, &row.TransactionIndex, &row.From, &row.To, &row.Token, &row.Raw, &row.Amount, &row.Time); err != nil {
				t.Fatal(err)
			}
			got = append(got, row)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	lookahead := read(2)
	if len(lookahead) != 3 || lookahead[0].Hash != "earlier_block" || lookahead[1].Hash != "earlier_log" || lookahead[2].Hash != "later_log" {
		t.Fatalf("ordered limit+1 ledger: %+v", lookahead)
	}
	all := read(20)
	want := []string{"earlier_block", "earlier_log", "later_log", "end_block_dust"}
	if len(all) != len(want) {
		t.Fatalf("ledger must exclude latest zero/self/unrelated records: %+v", all)
	}
	for i, row := range all {
		if row.Hash != want[i] || row.Token != token || row.From == row.To || row.Raw == "0" {
			t.Fatalf("ledger row %d: %+v", i, row)
		}
		if i > 0 && (row.Block < all[i-1].Block || (row.Block == all[i-1].Block && row.Index < all[i-1].Index)) {
			t.Fatalf("ledger is not sorted by block/log_index: %+v", all)
		}
	}
	if all[3].Raw != "1" || all[3].Amount != "0.000000000000000001" {
		t.Fatalf("positive base-unit dust was lost: %+v", all[3])
	}
	t.Log("Latest positive ledger excludes revised zero/self records, sorts block/log index and returns limit+1")
}
