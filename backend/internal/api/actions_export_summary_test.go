package api

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/parquet-go/parquet-go"
	"github.com/shopspring/decimal"
)

const summaryPayerA = "0x1111111111111111111111111111111111111111"
const summaryPayerB = "0x2222222222222222222222222222222222222222"
const summarySubject = "0x3333333333333333333333333333333333333333"
const summaryContract = "0x4444444444444444444444444444444444444444"

func summaryFixtureCSV(t *testing.T, records [][]string) []byte {
	t.Helper()
	var out bytes.Buffer
	w := csv.NewWriter(&out)
	_ = w.Write([]string{"chain", "block_number", "tx_hash", "from_address", "to_address", "token_address", "token_symbol", "token_decimals", "amount_raw", "custom_evidence"})
	for _, r := range records {
		if err := w.Write(r); err != nil {
			t.Fatal(err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func summaryFixtureRow(payer, to, block, raw, symbol, contract string) []string {
	return []string{"bsc", block, "0x" + strings.Repeat("a", 64), payer, to, contract, symbol, "18", raw, "quoted, original\nmultiline evidence"}
}

func summaryFixtureQuery() exportSummaryQuery {
	return exportSummaryQuery{JobID: "00000000-0000-4000-8000-000000000001", Chain: "bsc", Address: summarySubject, Token: "USDT", Direction: "in", PageSize: 1, DetailPageSize: 1}
}

func summarizeFixture(t *testing.T, raw []byte, exp *model.Export, q exportSummaryQuery) *exportAddressSummary {
	t.Helper()
	result, _, err := summarizeExportObject(context.Background(), bytes.NewReader(raw), int64(len(raw)), exp, q, "fixture-file-sha256")
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestExportSummaryExactTotalsAndIndependentCompletePagination(t *testing.T) {
	rows := [][]string{
		summaryFixtureRow(summaryPayerB, summarySubject, "10", "900719925474099312345678901234567890", "USDT", summaryContract),
		summaryFixtureRow(summaryPayerA, summarySubject, "11", "1", "USDT", summaryContract),
		summaryFixtureRow(summaryPayerA, summarySubject, "12", "2", "USDT", summaryContract),
		summaryFixtureRow(summarySubject, summaryPayerA, "99", "999", "USDT", summaryContract),
		summaryFixtureRow(summaryPayerB, summarySubject, "123", "555", "OTHER", summaryContract),
	}
	raw := summaryFixtureCSV(t, rows)
	q := summaryFixtureQuery()
	exp := &model.Export{ID: q.JobID, Dataset: "token_transfers", Format: "csv", Rows: 5}
	first := summarizeFixture(t, raw, exp, q)
	if first.TotalRecords != 3 || first.TotalAddresses != 2 || first.DatasetRows != 5 || first.TotalAmount != "900719925474099312.345678901234567893" || *first.TotalAmountRaw != "900719925474099312345678901234567893" || *first.DataCutoffBlock != "123" {
		t.Fatalf("wrong complete totals: %+v", first)
	}
	if !first.HasMore || !first.DetailsHasMore || first.Addresses[0].FromAddress != summaryPayerA || first.Addresses[0].RecordCount != 2 || first.Addresses[0].TotalAmount != "0.000000000000000003" {
		t.Fatalf("first page: %+v", first)
	}
	if first.Details[0].Fields["custom_evidence"] != "quoted, original\nmultiline evidence" {
		t.Fatal("lost original detail field")
	}
	q.Cursor = *first.NextCursor
	q.DetailCursor = *first.NextDetailCursor
	second := summarizeFixture(t, raw, exp, q)
	if second.HasMore || second.NextCursor != nil || second.Addresses[0].FromAddress != summaryPayerB || second.TotalAmount != first.TotalAmount || second.TotalRecords != first.TotalRecords {
		t.Fatal("address pagination truncated/changed global totals")
	}
	q.DetailCursor = *second.NextDetailCursor
	third := summarizeFixture(t, raw, exp, q)
	if third.DetailsHasMore || third.NextDetailCursor != nil || third.Details[0].Amount != "0.000000000000000002" {
		t.Fatal("detail pagination did not reach final record")
	}
	ids := map[string]bool{}
	total := decimal.Zero
	for _, page := range []*exportAddressSummary{first, second, third} {
		for _, d := range page.Details {
			if ids[d.RecordID] {
				t.Fatal("duplicate detail between pages")
			}
			ids[d.RecordID] = true
			amount, _ := decimal.NewFromString(d.Amount)
			total = total.Add(amount)
		}
	}
	if len(ids) != 3 || total.String() != first.TotalAmount {
		t.Fatal("detail amounts do not reconcile with total")
	}
}

func TestExportSummaryDirectionAndPayerFilter(t *testing.T) {
	rows := [][]string{summaryFixtureRow(summaryPayerA, summarySubject, "10", "1", "USDT", summaryContract), summaryFixtureRow(summarySubject, summaryPayerB, "11", "2", "USDT", summaryContract)}
	raw := summaryFixtureCSV(t, rows)
	q := summaryFixtureQuery()
	q.Direction = "out"
	q.FromAddress = summarySubject
	exp := &model.Export{ID: q.JobID, Dataset: "token_transfers", Format: "csv", Rows: 2}
	r := summarizeFixture(t, raw, exp, q)
	if r.TotalRecords != 1 || r.Addresses[0].FromAddress != summarySubject || r.TotalAmount != "0.000000000000000002" {
		t.Fatalf("direction/payer filter: %+v", r)
	}
	q.Direction, q.FromAddress = "in", summaryPayerB
	r = summarizeFixture(t, raw, exp, q)
	if r.TotalRecords != 0 || len(r.Addresses) != 0 || len(r.Details) != 0 || r.TotalAmount != "0" || r.NextCursor != nil || *r.DataCutoffBlock != "11" {
		t.Fatal("empty match fabricated values or lost dataset cutoff")
	}
}

func TestExportSummaryCursorsRejectTamperingOtherFilesFiltersAndKinds(t *testing.T) {
	raw := summaryFixtureCSV(t, [][]string{summaryFixtureRow(summaryPayerA, summarySubject, "10", "1", "USDT", summaryContract), summaryFixtureRow(summaryPayerB, summarySubject, "11", "2", "USDT", summaryContract)})
	base := summaryFixtureQuery()
	exp := &model.Export{ID: base.JobID, Dataset: "token_transfers", Format: "csv", Rows: 2}
	first := summarizeFixture(t, raw, exp, base)
	for _, kind := range []string{"tampered", "filter", "file", "kind", "job"} {
		t.Run(kind, func(t *testing.T) {
			q, copyExp, hash := base, *exp, "fixture-file-sha256"
			q.Cursor = *first.NextCursor
			switch kind {
			case "tampered":
				q.Cursor = "x" + q.Cursor[1:]
			case "filter":
				q.Direction = "out"
			case "file":
				hash = "changed-file"
			case "kind":
				q.Cursor = *first.NextDetailCursor
			case "job":
				copyExp.ID = "00000000-0000-4000-8000-000000000002"
			}
			_, _, err := summarizeExportObject(context.Background(), bytes.NewReader(raw), int64(len(raw)), &copyExp, q, hash)
			var e *exportSummaryError
			if !errors.As(err, &e) || e.code != "INVALID_CURSOR" {
				t.Fatalf("cursor accepted: %v", err)
			}
		})
	}
}

func TestExportSummaryCSVZipAndParquetProduceTheSameTotals(t *testing.T) {
	q := summaryFixtureQuery()
	row := summaryFixtureRow(summaryPayerA, summarySubject, "88", "123456789012345678901234567890123456789", "USDT", summaryContract)
	csvBytes := summaryFixtureCSV(t, [][]string{row})
	var parquetBytes bytes.Buffer
	w := parquet.NewGenericWriter[parquetExportRow](&parquetBytes)
	_, err := w.Write([]parquetExportRow{{Dataset: "token_transfers", Chain: "bsc", BlockNumber: 88, TxHash: row[2], FromAddress: summaryPayerA, ToAddress: summarySubject, TokenAddress: summaryContract, TokenSymbol: "USDT", TokenDecimals: 18, AmountRaw: row[8], Topics: []string{"first", "second"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"csv", "parquet"} {
		payload := csvBytes
		if format == "parquet" {
			payload = parquetBytes.Bytes()
		}
		for _, zipped := range []bool{false, true} {
			t.Run(format+strconv.FormatBool(zipped), func(t *testing.T) {
				var raw []byte
				if zipped {
					var buf bytes.Buffer
					archive := zip.NewWriter(&buf)
					manifest, _ := archive.Create("manifest.json")
					_, _ = manifest.Write([]byte(`{"partial":true}`))
					other, _ := archive.Create("addresses/subject/transactions.csv")
					_, _ = other.Write([]byte("irrelevant header\nignore me\n"))
					entry, _ := archive.Create("addresses/subject/token_transfers." + format)
					_, _ = entry.Write(payload)
					if err := archive.Close(); err != nil {
						t.Fatal(err)
					}
					raw = buf.Bytes()
				} else {
					raw = payload
				}
				exp := &model.Export{ID: q.JobID, Dataset: "token_transfers", Format: format, Zip: zipped, Rows: 1}
				r, partial, err := summarizeExportObject(context.Background(), bytes.NewReader(raw), int64(len(raw)), exp, q, "fixture")
				if err != nil {
					t.Fatal(err)
				}
				if r.TotalAmount != "123456789012345678901.234567890123456789" || r.TotalRecords != 1 || *r.DataCutoffBlock != "88" || partial != zipped {
					t.Fatalf("format parity: %+v partial=%v", r, partial)
				}
				if format == "parquet" && r.Details[0].Fields["topics"] != `["first","second"]` {
					t.Fatalf("repeated original fields lost: %+v", r.Details[0].Fields)
				}
			})
		}
	}
}

func TestExportSummaryRejectsAmbiguousAssetsMalformedAmountsAndMissingRows(t *testing.T) {
	q := summaryFixtureQuery()
	exp := &model.Export{ID: q.JobID, Dataset: "token_transfers", Format: "csv", Rows: 2}
	for _, kind := range []string{"contract", "amount", "missing_rows", "short_address", "csv_parse"} {
		t.Run(kind, func(t *testing.T) {
			rows := [][]string{summaryFixtureRow(summaryPayerA, summarySubject, "1", "1", "USDT", summaryContract), summaryFixtureRow(summaryPayerB, summarySubject, "2", "2", "USDT", summaryContract)}
			switch kind {
			case "contract":
				rows[1][5] = summaryPayerB
			case "amount":
				rows[1][8] = "1e30"
			case "missing_rows":
				rows = rows[:1]
			case "short_address":
				rows[1][3] = "0xabc"
			}
			raw := summaryFixtureCSV(t, rows)
			if kind == "csv_parse" {
				raw = append(raw, []byte("\"truncated quoted row\n")...)
			}
			if _, _, err := summarizeExportObject(context.Background(), bytes.NewReader(raw), int64(len(raw)), exp, q, "fixture"); err == nil {
				t.Fatal("incomplete or invalid export was reported as complete")
			}
		})
	}
}

func TestExportSummaryQueryBoundsAndSolanaCase(t *testing.T) {
	snapshot := exportSummarySnapshot{Chain: "bsc", Addresses: []string{summarySubject}}
	values := url.Values{"job_id": {"00000000-0000-4000-8000-000000000001"}, "token": {"usdt"}, "direction": {"in"}}
	q, err := parseExportSummaryQuery(values, snapshot)
	if err != nil || q.Address != summarySubject || q.Token != "USDT" || q.PageSize != 100 {
		t.Fatalf("query defaults: %+v %v", q, err)
	}
	for _, field := range []string{"page_size", "detail_page_size"} {
		values.Set(field, "501")
		if _, err := parseExportSummaryQuery(values, snapshot); err == nil {
			t.Fatal("unbounded page accepted")
		}
		values.Del(field)
	}
	if _, err := parseExportSummaryQuery(values, exportSummarySnapshot{Chain: "bsc", Addresses: []string{summaryPayerA, summaryPayerB}}); err == nil {
		t.Fatal("multi-address direction accepted without reference address")
	}
	values.Del("token")
	if _, err := parseExportSummaryQuery(values, snapshot); err == nil {
		t.Fatal("cross-token aggregation accepted")
	}
	values.Set("token_contract", "So11111111111111111111111111111111111111112")
	values.Set("address", "So11111111111111111111111111111111111111112")
	q, err = parseExportSummaryQuery(values, exportSummarySnapshot{Chain: "solana"})
	if err != nil || q.TokenContract != "So11111111111111111111111111111111111111112" {
		t.Fatalf("Solana case lost: %+v %v", q, err)
	}
}

func TestExportSummaryAmountsBeyond18DecimalsAndUnknownPrecision(t *testing.T) {
	amount, raw, _, err := exactExportTransferAmount(map[string]string{"amount_raw": "123456789012345678901234567890123456789", "token_decimals": "36"})
	if err != nil || raw != "123456789012345678901234567890123456789" || amount.String() != "123.456789012345678901234567890123456789" {
		t.Fatalf("precision loss: %s %v", amount, err)
	}
	if _, _, _, err := exactExportTransferAmount(map[string]string{"amount_raw": "12", "token_decimals": "0", "token_metadata_status": "unknown"}); err == nil {
		t.Fatal("unknown decimal precision was treated as token units")
	}
	encoded, _ := json.Marshal(amount.String())
	if encoded[0] != '"' {
		t.Fatal("amount was not a JSON string")
	}
}

func TestExportSummaryCancelledReadDoesNotReturnPartialTotals(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	q := summaryFixtureQuery()
	exp := &model.Export{ID: q.JobID, Dataset: "token_transfers", Format: "csv", Rows: 1}
	raw := summaryFixtureCSV(t, [][]string{summaryFixtureRow(summaryPayerA, summarySubject, "1", "1", "USDT", summaryContract)})
	if result, _, err := summarizeExportObject(ctx, bytes.NewReader(raw), int64(len(raw)), exp, q, "fixture"); err == nil || result != nil {
		t.Fatal("cancelled scan produced a success response")
	}
}

// Opt-in server artifact check. The fixture specification and expectations are
// supplied separately so that production records never become test fixtures in git.
func TestExportSummaryCompletedServerArtifacts(t *testing.T) {
	name := os.Getenv("EXPORT_SUMMARY_ARTIFACT_FIXTURES")
	if name == "" {
		t.Skip("EXPORT_SUMMARY_ARTIFACT_FIXTURES not set")
	}
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		ID, Path, Format, Dataset, Address string
		TokenContract                      string `json:"token_contract"`
		Zip                                bool
		Rows                               int64
		Expected                           struct {
			DatasetRows     int64  `json:"dataset_rows"`
			TotalRecords    int64  `json:"total_records"`
			TotalAddresses  int    `json:"total_addresses"`
			TotalAmount     string `json:"total_amount"`
			TotalAmountRaw  string `json:"total_amount_raw"`
			DataCutoffBlock string `json:"data_cutoff_block"`
			Addresses       []exportAddressStatistic
		}
	}
	if err := json.Unmarshal(data, &fixtures); err != nil || len(fixtures) == 0 {
		t.Fatalf("invalid fixture specification: %v", err)
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Format, func(t *testing.T) {
			raw, err := os.ReadFile(fixture.Path)
			if err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(raw)
			checksum := hex.EncodeToString(digest[:])
			exp := &model.Export{ID: fixture.ID, Dataset: fixture.Dataset, Format: fixture.Format, Zip: fixture.Zip, Rows: fixture.Rows}
			q := exportSummaryQuery{JobID: fixture.ID, Chain: "bsc", Address: fixture.Address, TokenContract: fixture.TokenContract, Direction: "in", PageSize: 17, DetailPageSize: 73}
			var got []exportAddressStatistic
			for {
				r, _, err := summarizeExportObject(context.Background(), bytes.NewReader(raw), int64(len(raw)), exp, q, checksum)
				if err != nil {
					t.Fatal(err)
				}
				want := fixture.Expected
				if r.DatasetRows != want.DatasetRows || r.TotalRecords != want.TotalRecords || r.TotalAddresses != want.TotalAddresses || r.TotalAmount != want.TotalAmount || *r.TotalAmountRaw != want.TotalAmountRaw || *r.DataCutoffBlock != want.DataCutoffBlock {
					t.Fatalf("real export totals differ from independent computation: %+v", r)
				}
				got = append(got, r.Addresses...)
				if !r.HasMore {
					break
				}
				q.Cursor = *r.NextCursor
			}
			if len(got) != len(fixture.Expected.Addresses) {
				t.Fatal("incomplete address pagination")
			}
			for i, g := range got {
				want := fixture.Expected.Addresses[i]
				if g.FromAddress != want.FromAddress || g.RecordCount != want.RecordCount || g.TotalAmount != want.TotalAmount || *g.TotalAmountRaw != *want.TotalAmountRaw {
					t.Fatalf("payer mismatch: %+v versus %+v", g, want)
				}
			}
			q.Cursor = ""
			detailIDs := map[string]bool{}
			amountTotal := decimal.Zero
			for {
				r, _, err := summarizeExportObject(context.Background(), bytes.NewReader(raw), int64(len(raw)), exp, q, checksum)
				if err != nil {
					t.Fatal(err)
				}
				for _, d := range r.Details {
					if detailIDs[d.RecordID] {
						t.Fatal("real detail duplicated between pages")
					}
					detailIDs[d.RecordID] = true
					amount, err := decimal.NewFromString(d.Amount)
					if err != nil || d.Fields["amount_raw"] != d.AmountRaw {
						t.Fatal("lost original exact detail")
					}
					amountTotal = amountTotal.Add(amount)
				}
				if !r.DetailsHasMore {
					break
				}
				q.DetailCursor = *r.NextDetailCursor
			}
			if int64(len(detailIDs)) != fixture.Expected.TotalRecords || amountTotal.String() != fixture.Expected.TotalAmount {
				t.Fatal("real paginated details do not reconcile")
			}
			t.Logf("%s: %d complete records, %d full payer addresses, exact total %s", fixture.Format, len(detailIDs), len(got), amountTotal.String())
		})
	}
}
