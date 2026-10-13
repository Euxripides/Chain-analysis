package api

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/audit"
	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
	"github.com/blockchain-platform/blockchain-platform/internal/explorer"
	"github.com/blockchain-platform/blockchain-platform/internal/policy"
	"github.com/blockchain-platform/blockchain-platform/internal/query"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/parquet-go/parquet-go"
	"github.com/xuri/excelize/v2"
)

type blueDatabaseAudit struct{ store *store.Store }

func (a blueDatabaseAudit) Write(ctx context.Context, e audit.Event) error {
	data, err := json.Marshal(e.Detail)
	if err != nil {
		return err
	}
	_, err = a.store.Pool.Exec(ctx, `INSERT INTO audit_events(id,user_id,action,resource_type,resource_id,detail) VALUES($1,$2,$3,$4,$5,$6)`, uuid.NewString(), e.ActorID, e.Action, e.ResourceType, e.ResourceID, data)
	return err
}

// This test uses owned loopback ClickHouse/Postgres/MinIO services. Identity is
// injected; deployed cookie, CSRF and permission middleware have separate QA.
func TestBlueExplorerDatasetExportsLive(t *testing.T) {
	dsn := os.Getenv("EXPORT_PERF_TEST_DSN")
	if !strings.Contains(dsn, "127.0.0.1:19006/") {
		t.Skip("owned blue ClickHouse service required")
	}
	srv, _, _, principal := newExportReadinessFixture(t, []string{"token_transfers", "transactions"})
	ctx, cancel := context.WithTimeout(withPrincipal(context.Background(), principal), 2*time.Minute)
	defer cancel()
	ch, err := clickhouse.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer ch.Close()
	srv.deps.CH = ch
	srv.query = query.New(ch)
	srv.explorer = explorer.New(ch, nil)
	srv.auditSink = blueDatabaseAudit{srv.deps.Store}
	addr := func() string { return "0x" + strings.ReplaceAll(uuid.NewString(), "-", "") + "00000000" }
	a, b, c, contract, otherContract := addr(), addr(), addr(), addr(), addr()
	hash := func() string { return "0x" + strings.Repeat(strings.ReplaceAll(uuid.NewString(), "-", ""), 2) }
	now := time.Now().UTC().Truncate(time.Millisecond)
	at := now.Add(-time.Hour)
	asOf := now.Add(-time.Minute)
	h0 := hash()
	insert := func(h string, index uint32, from, to, raw, token string, dec uint16, ingested time.Time) {
		t.Helper()
		err := ch.DB().Exec(ctx, `INSERT INTO blockchain.token_transfers(chain,block_number,timestamp,tx_hash,log_index,token_address,token_symbol,token_decimals,from_address,to_address,amount_raw,amount,source_provider,source_ingested_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, "bsc", uint64(909), at, h, index, token, "USDT", dec, from, to, raw, "0", "blue_fixture", ingested)
		if err != nil {
			t.Fatal(err)
		}
	}
	insert(h0, 0, c, a, "999", contract, 18, now.Add(-5*time.Minute))
	insert(h0, 0, c, a, "100000000000000000001", contract, 18, now.Add(-4*time.Minute))
	insert(h0, 1, c, a, "1", contract, 18, now.Add(-4*time.Minute))
	insert(hash(), 0, a, c, "20000000000000000000", contract, 18, now.Add(-4*time.Minute))
	insert(hash(), 0, a, b, "3000000000000000000", contract, 18, now.Add(-4*time.Minute))
	insert(hash(), 0, a, a, "1000000000000000000", contract, 18, now.Add(-4*time.Minute))
	insert(hash(), 0, c, b, "2000000000000000000", contract, 18, now.Add(-4*time.Minute))
	insert(hash(), 0, c, a, "1000000", otherContract, 6, now.Add(-4*time.Minute))
	insert(hash(), 0, c, a, "99000000000000000000", contract, 18, now.Add(time.Hour))
	nativeHash := hash()
	if err := ch.DB().Exec(ctx, `INSERT INTO blockchain.transactions(chain,block_number,block_hash,timestamp,tx_hash,from_address,to_address,value_raw,status,gas,gas_used,gas_price,source_ingested_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`, "bsc", uint64(909), hash(), at, nativeHash, c, a, "0xde0b6b3a7640001", uint8(1), uint64(21000), uint64(21000), "12345678901234567890", now.Add(-4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	f := query.StatisticsFilter{Chain: "bsc", Addresses: []string{a, b}, Datasets: []string{"token_transfers"}, AsOf: &asOf}
	result, err := srv.query.Statistics(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	if result.Events != 7 || result.Transactions != 6 || len(result.Assets) != 2 {
		t.Fatalf("canonical counts: %+v", result)
	}
	for _, asset := range result.Assets {
		if asset.Total.TokenAddress == contract && (asset.Total.InRaw != "102000000000000000002" || asset.Total.OutRaw != "20000000000000000000" || asset.Total.InternalRaw != "3000000000000000000" || asset.Total.SelfRaw != "1000000000000000000") {
			t.Fatalf("raw totals: %+v", asset.Total)
		}
	}
	nativeF := f
	nativeF.Datasets = []string{"transactions"}
	native, err := srv.query.Statistics(ctx, nativeF)
	if err != nil || len(native.Assets) != 1 || native.Assets[0].Total.InRaw != "1000000000000000001" {
		t.Fatalf("native hex precision: %+v %v", native, err)
	}
	// Token cursors order by time/hash/log index and don't lose same-tx logs.
	first, err := srv.query.TokenTransfers(ctx, "bsc", contract, "", 0, time.Time{}, 2)
	if err != nil || len(first) != 3 {
		t.Fatalf("token page: %d %v", len(first), err)
	}
	seen := map[string]bool{}
	for _, row := range first[:2] {
		seen[objectString(row["tx_hash"])+objectString(row["log_index"])] = true
	}
	last := first[1]
	stamp, err := explorerObjectTime(last["timestamp"])
	if err != nil {
		t.Fatal(err)
	}
	parsedIndex, err := strconv.ParseUint(fmt.Sprint(last["log_index"]), 10, 32)
	if err != nil {
		t.Fatal(err)
	}
	index := uint32(parsedIndex)
	next, err := srv.query.TokenTransfers(ctx, "bsc", contract, objectString(last["tx_hash"]), index, stamp, 200)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range next {
		key := objectString(row["tx_hash"]) + objectString(row["log_index"])
		if seen[key] {
			t.Fatal("token cursor duplicates")
		}
		seen[key] = true
	}
	if len(seen) != 7 {
		t.Fatalf("token cursor lost rows: %d", len(seen))
	}
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(withPrincipal(r.Context(), principal)))
		})
	})
	router.Post("/exports/{id}/files/{dataset}/ticket", srv.handleExportFileTicket)
	router.Get("/api/v2/exports/download/{ticket}", srv.handleExportV2Download)
	router.Head("/api/v2/exports/download/{ticket}", srv.handleExportV2Download)
	router.Get("/exports/{id}", srv.handleExportV2Detail)
	server := httptest.NewServer(router)
	defer server.Close()
	for _, format := range []string{"csv", "jsonl", "xlsx", "parquet"} {
		t.Run(format, func(t *testing.T) {
			filter := f
			filter.Token = contract
			filter.Direction = "in"
			filter.FromAddress = c
			filter.MinAmount = "2"
			req := exportV2CreateRequest{Chain: "bsc", Format: format, FileLayout: "dataset", Content: "details", Filters: &filter}
			snapshot, _ := json.Marshal(map[string]any{"filters": filter, "file_layout": "dataset"})
			exp, err := srv.deps.Store.CreateExportV2(ctx, principal.WorkspaceID, principal.OrganizationID, &principal.IdentityID, nil, "token_transfers+transactions", format, true, snapshot)
			if err != nil {
				t.Fatal(err)
			}
			srv.runDatasetExport(ctx, exp, nil, req, []string{a, b}, []string{"token_transfers", "transactions"}, &filter, true)
			ready, err := srv.deps.Store.GetExport(ctx, exp.ID)
			if err != nil || ready.Status != "COMPLETED" {
				t.Fatalf("export %v %+v", err, ready)
			}
			files, err := srv.exportFiles(ctx, exp.ID)
			if err != nil || len(files) != 2 || files[0].Rows != 2 || files[1].Rows != 0 {
				t.Fatalf("manifest %+v %v", files, err)
			}
			archive := downloadExportReadiness(t, srv, principal, exp.ID)
			zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
			if err != nil || len(zr.File) != 2 {
				t.Fatalf("dataset ZIP: %v", err)
			}
			for _, file := range files {
				var data []byte
				for _, entry := range zr.File {
					if entry.Name == file.Name {
						r, e := entry.Open()
						if e != nil {
							t.Fatal(e)
						}
						data, e = io.ReadAll(r)
						r.Close()
						if e != nil {
							t.Fatal(e)
						}
					}
				}
				sum := sha256.Sum256(data)
				if int64(len(data)) != file.Bytes || hex.EncodeToString(sum[:]) != file.SHA256 {
					t.Fatal("manifest integrity differs from archive")
				}
				if file.Dataset == "token_transfers" {
					verifyBlueExportValues(t, format, data, 2, "100000000000000000001")
				}
				response, err := server.Client().Post(server.URL+"/exports/"+exp.ID+"/files/"+file.Dataset+"/ticket", "application/json", nil)
				if err != nil {
					t.Fatal(err)
				}
				var ticket struct {
					Data struct {
						URL string `json:"url"`
					} `json:"data"`
				}
				err = json.NewDecoder(response.Body).Decode(&ticket)
				response.Body.Close()
				if err != nil || response.StatusCode != 200 {
					t.Fatalf("file ticket %d %v", response.StatusCode, err)
				}
				ticketID := ticket.Data.URL[strings.LastIndex(ticket.Data.URL, "/")+1:]
				foreign := principal
				for _, change := range []func(*policy.Principal){func(p *policy.Principal) { p.IdentityID = uuid.NewString() }, func(p *policy.Principal) { p.OrganizationID = uuid.NewString() }, func(p *policy.Principal) { p.WorkspaceID = uuid.NewString() }} {
					foreign = principal
					change(&foreign)
					if _, e := srv.redeemExportTicket(ctx, ticketID, foreign, "GET", ""); e == nil {
						t.Fatal("Foreign scope redeemed ticket")
					}
				}
				headRequest, _ := http.NewRequest("HEAD", server.URL+ticket.Data.URL, nil)
				head, err := server.Client().Do(headRequest)
				if err != nil {
					t.Fatal(err)
				}
				head.Body.Close()
				if head.StatusCode != 200 || head.ContentLength != file.Bytes {
					t.Fatalf("HEAD %d bytes=%d want=%d", head.StatusCode, head.ContentLength, file.Bytes)
				}
				var unused bool
				if e := srv.deps.Store.Pool.QueryRow(ctx, "SELECT used_at IS NULL FROM export_tickets WHERE id=$1", ticketID).Scan(&unused); e != nil || !unused {
					t.Fatal("HEAD or wrong scope consumed ticket")
				}
				downloaded, err := server.Client().Get(server.URL + ticket.Data.URL)
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(downloaded.Body)
				downloaded.Body.Close()
				if err != nil || downloaded.StatusCode != 200 || !bytes.Equal(data, body) {
					t.Fatalf("per-file download mismatch %d %v", downloaded.StatusCode, err)
				}
				reused, _ := server.Client().Get(server.URL + ticket.Data.URL)
				reused.Body.Close()
				if reused.StatusCode != 410 {
					t.Fatalf("file ticket reused: %d", reused.StatusCode)
				}
				if len(data) > 0 {
					rangeRequest, _ := http.NewRequest("GET", server.URL+ticket.Data.URL, nil)
					rangeRequest.Header.Set("Range", "bytes=0-7")
					ranged, e := server.Client().Do(rangeRequest)
					if e != nil {
						t.Fatal(e)
					}
					part, e := io.ReadAll(ranged.Body)
					ranged.Body.Close()
					end := 8
					if end > len(data) {
						end = len(data)
					}
					if e != nil || ranged.StatusCode != 206 || !bytes.Equal(data[:end], part) {
						t.Fatalf("Range continuation %d %v", ranged.StatusCode, e)
					}
				}
			}
		})
	}
	// The unfiltered typed stream must retain all native fields, including the
	// signed Solana instruction fields present in the deployed row structures.
	unfiltered, err := srv.deps.Store.CreateExportV2(ctx, principal.WorkspaceID, principal.OrganizationID, &principal.IdentityID, nil, "transactions+token_transfers", "jsonl", true, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	srv.runDatasetExport(ctx, unfiltered, nil, exportV2CreateRequest{Chain: "bsc", Format: "jsonl", FileLayout: "dataset", Content: "details"}, []string{a, b}, []string{"transactions", "token_transfers"}, nil, true)
	ready, _ := srv.deps.Store.GetExport(ctx, unfiltered.ID)
	if ready.Status != "COMPLETED" || ready.Rows != 9 {
		t.Fatalf("Unfiltered typed stream: %+v", ready)
	}
	unfilteredData := downloadExportReadiness(t, srv, principal, unfiltered.ID)
	archive, _ := zip.NewReader(bytes.NewReader(unfilteredData), int64(len(unfilteredData)))
	for _, entry := range archive.File {
		if entry.Name != "transactions.jsonl" {
			continue
		}
		reader, _ := entry.Open()
		data, _ := io.ReadAll(reader)
		reader.Close()
		if !bytes.Contains(data, []byte(`"gas_price":"12345678901234567890"`)) || !bytes.Contains(data, []byte(`"value_raw":"0xde0b6b3a7640001"`)) {
			t.Fatalf("Native source fields lost: %s", data)
		}
	}
	if _, err := srv.deps.Store.Pool.Exec(ctx, "UPDATE exports SET expires_at=now()-interval '1 second' WHERE id=$1", unfiltered.ID); err != nil {
		t.Fatal(err)
	}
	if err := srv.cleanupDatasetExports(ctx); err != nil {
		t.Fatal(err)
	}
	expiredFiles, _ := srv.exportFiles(ctx, unfiltered.ID)
	for _, file := range expiredFiles {
		if _, _, err := srv.statExportObject(ctx, file.ObjectRef); err == nil {
			t.Fatal("Expired child remained")
		}
	}
	if _, _, err := srv.statExportObject(ctx, ready.ObjectRef); err == nil {
		t.Fatal("Expired root remained")
	}
	for _, format := range []string{"csv", "xlsx", "jsonl", "parquet"} {
		var output bytes.Buffer
		if err := writeStatisticsExport(ctx, &output, format, nil); err != nil {
			t.Fatal(err)
		}
		if format == "csv" {
			rows, err := csv.NewReader(&output).ReadAll()
			if err != nil || len(rows) != 1 || len(rows[0]) != 24 {
				t.Fatal("Empty aggregate CSV lacks fields")
			}
		}
		if format == "xlsx" {
			book, err := excelize.OpenReader(&output)
			if err != nil {
				t.Fatal(err)
			}
			rows, _ := book.GetRows("Sheet1")
			if len(rows) != 1 || len(rows[0]) != 24 {
				t.Fatal("Empty aggregate XLSX lacks fields")
			}
			book.Close()
		}
		cancelled, stop := context.WithCancel(ctx)
		stop()
		if writeStatisticsExport(cancelled, io.Discard, format, nil) == nil {
			t.Fatal("Cancelled export succeeded")
		}
	}
	nftHash, tokenID := hash(), "123456789012345678901234567890"
	if err := ch.DB().Exec(ctx, `INSERT INTO blockchain.nft_transfers(chain,block_number,timestamp,tx_hash,log_index,contract_address,token_id,from_address,to_address,token_symbol,amount_raw,source_ingested_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, "bsc", uint64(909), at, nftHash, uint32(1), contract, tokenID, c, a, "NFT", "1", now.Add(-4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	nft, err := srv.deps.Store.CreateExportV2(ctx, principal.WorkspaceID, principal.OrganizationID, &principal.IdentityID, nil, "nft_transfers", "jsonl", false, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	nftFilter := query.StatisticsFilter{Chain: "bsc", Addresses: []string{a}, Datasets: []string{"nft_transfers"}, Token: contract, Direction: "in", FromAddress: c, ToAddress: a, AsOf: &asOf}
	srv.runDatasetExport(ctx, nft, nil, exportV2CreateRequest{Chain: "bsc", Format: "jsonl", FileLayout: "dataset", Content: "details"}, []string{a}, []string{"nft_transfers"}, &nftFilter, false)
	nftReady, _ := srv.deps.Store.GetExport(ctx, nft.ID)
	if nftReady.Status != "COMPLETED" || nftReady.Rows != 1 {
		t.Fatalf("NFT filtered typed stream: %+v", nftReady)
	}
	nftData := downloadExportReadiness(t, srv, principal, nft.ID)
	if !bytes.Contains(nftData, []byte(tokenID)) || !bytes.Contains(nftData, []byte(nftHash)) {
		t.Fatal("NFT identity lost")
	}
	// Aggregate export reads full canonical scope, never the UI's current page.
	for _, content := range []string{"statistics", "counterparties"} {
		filter := f
		req := exportV2CreateRequest{Chain: "bsc", Format: "csv", FileLayout: "dataset", Content: content}
		exp, err := srv.deps.Store.CreateExportV2(ctx, principal.WorkspaceID, principal.OrganizationID, &principal.IdentityID, nil, "token_transfers", "csv", false, []byte(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		srv.runDatasetExport(ctx, exp, nil, req, []string{a, b}, []string{"token_transfers"}, &filter, false)
		ready, _ := srv.deps.Store.GetExport(ctx, exp.ID)
		if ready.Status != "COMPLETED" || ready.Rows == 0 {
			t.Fatalf("aggregate export %+v", ready)
		}
		data := downloadExportReadiness(t, srv, principal, exp.ID)
		if !bytes.Contains(data, []byte("102000000000000000002")) {
			t.Fatal("aggregate raw value missing")
		}
	}
	var audited int
	if err := srv.deps.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='export.completed'`).Scan(&audited); err != nil || audited != 8 {
		t.Fatalf("completion audit %d %v", audited, err)
	}
	t.Log("Real ClickHouse canonical events + Postgres manifests/audits + MinIO SHA256 + all four formats + root and dataset tickets PASS")
}

func verifyBlueExportValues(t *testing.T, format string, data []byte, count int, raw string) {
	t.Helper()
	switch format {
	case "csv":
		rows, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
		if err != nil || len(rows) != count+1 || !bytes.Contains(data, []byte(raw)) {
			t.Fatalf("CSV integrity rows=%d %v", len(rows), err)
		}
	case "jsonl":
		lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
		if len(lines) != count || !bytes.Contains(data, []byte(raw)) {
			t.Fatal("JSONL precision/count")
		}
	case "xlsx":
		book, err := excelize.OpenReader(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		defer book.Close()
		rows, err := book.GetRows(book.GetSheetList()[0])
		if err != nil || len(rows) != count+1 {
			t.Fatalf("XLSX rows %d %v", len(rows), err)
		}
		found := false
		for _, row := range rows {
			for _, cell := range row {
				found = found || cell == raw
			}
		}
		if !found {
			t.Fatal("XLSX raw integer changed")
		}
		zr, _ := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		filter := false
		for _, entry := range zr.File {
			if strings.HasPrefix(entry.Name, "xl/worksheets/sheet") {
				r, _ := entry.Open()
				xml, _ := io.ReadAll(r)
				r.Close()
				filter = filter || bytes.Contains(xml, []byte("autoFilter"))
			}
		}
		if !filter {
			t.Fatal("XLSX filter missing")
		}
	case "parquet":
		r := parquet.NewGenericReader[parquetExportRow](bytes.NewReader(data))
		defer r.Close()
		rows := make([]parquetExportRow, count+1)
		n, err := r.Read(rows)
		if n != count || (err != nil && err != io.EOF) {
			t.Fatalf("Parquet rows %d %v", n, err)
		}
		found := false
		for _, row := range rows[:n] {
			found = found || row.AmountRaw == raw
		}
		if !found {
			t.Fatal("Parquet raw integer changed")
		}
	}
}
