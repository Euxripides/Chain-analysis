package api

import (
	"archive/zip"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/parquet-go/parquet-go"
	"github.com/shopspring/decimal"

	"github.com/blockchain-platform/blockchain-platform/internal/addressnorm"
	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

// Cursors are signed and tied to the immutable object digest and query, not a
// public download ticket. A process restart expires existing cursors.
var exportSummaryCursorKey = func() []byte {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(err)
	}
	return key
}()

var exportSummarySlots = make(chan struct{}, 2)

type exportSummaryError struct {
	status        int
	code, message string
}

func (e *exportSummaryError) Error() string { return e.message }
func summaryError(status int, code, message string) error {
	return &exportSummaryError{status: status, code: code, message: message}
}

type exportSummaryQuery struct {
	JobID          string `json:"job_id"`
	Chain          string `json:"chain"`
	Address        string `json:"address"`
	Token          string `json:"token"`
	TokenContract  string `json:"token_contract"`
	Direction      string `json:"direction"`
	FromAddress    string `json:"from_address"`
	PageSize       int    `json:"-"`
	DetailPageSize int    `json:"-"`
	Cursor         string `json:"-"`
	DetailCursor   string `json:"-"`
}

type exportSummarySnapshot struct {
	Chain     string   `json:"chain"`
	Addresses []string `json:"addresses"`
	Datasets  []string `json:"datasets"`
}

func parseExportSummaryQuery(values url.Values, snapshot exportSummarySnapshot) (exportSummaryQuery, error) {
	q := exportSummaryQuery{JobID: strings.TrimSpace(values.Get("job_id")),
		Chain: strings.ToLower(strings.TrimSpace(snapshot.Chain)), Address: strings.TrimSpace(values.Get("address")),
		Token: strings.ToUpper(strings.TrimSpace(values.Get("token"))), TokenContract: strings.TrimSpace(values.Get("token_contract")),
		Direction: values.Get("direction"), FromAddress: strings.TrimSpace(values.Get("from_address")),
		Cursor: values.Get("cursor"), DetailCursor: values.Get("detail_cursor")}
	if q.Chain == "" {
		return q, summaryError(422, "INVALID_EXPORT_DATA", "Export snapshot is missing its chain")
	}
	if q.Token == "" && q.TokenContract == "" {
		return q, summaryError(400, "INVALID_QUERY", "token or token_contract is required")
	}
	var selectionErr error
	q.Token, q.TokenContract, selectionErr = resolveActionAsset(q.Chain, q.Token, q.TokenContract)
	if selectionErr != nil {
		return q, summaryError(400, "INVALID_QUERY", selectionErr.Error())
	}
	if q.Direction == "" {
		q.Direction = "all"
	}
	if q.Direction != "all" && q.Direction != "in" && q.Direction != "out" {
		return q, summaryError(400, "INVALID_QUERY", "direction must be all, in or out")
	}
	if q.Address == "" && len(snapshot.Addresses) == 1 {
		q.Address = snapshot.Addresses[0]
	}
	if q.Direction != "all" && q.Address == "" {
		return q, summaryError(400, "INVALID_QUERY", "address is required for direction on a multi-address export")
	}
	for _, field := range []*string{&q.Address, &q.FromAddress, &q.TokenContract} {
		if *field == "" {
			continue
		}
		value, err := addressnorm.Normalize(q.Chain, *field)
		if err != nil {
			return q, summaryError(400, "INVALID_QUERY", "Invalid complete address or token_contract")
		}
		*field = value
	}
	for name, target := range map[string]*int{"page_size": &q.PageSize, "detail_page_size": &q.DetailPageSize} {
		*target = 100
		if value := values.Get(name); value != "" {
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 || n > 500 {
				return q, summaryError(400, "INVALID_QUERY", name+" must be between 1 and 500")
			}
			*target = n
		}
	}
	return q, nil
}

type exportSummaryCursor struct {
	IngestedBefore string `json:"ingested_before,omitempty"`
	TokenAfter     string `json:"token_after,omitempty"`
	Version        int    `json:"v"`
	Query          string `json:"q"`
	Kind           string `json:"kind"`
	After          string `json:"after,omitempty"`
	Offset         int64  `json:"offset,omitempty"`
}

func encodeExportSummaryCursor(c exportSummaryCursor) string {
	body, _ := json.Marshal(c)
	mac := hmac.New(sha256.New, exportSummaryCursorKey)
	_, _ = mac.Write(body)
	return base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func decodeExportSummaryCursor(raw, query, kind string) (exportSummaryCursor, error) {
	c := exportSummaryCursor{Version: 1, Query: query, Kind: kind}
	if raw == "" {
		return c, nil
	}
	invalid := summaryError(400, "INVALID_CURSOR", "Cursor is invalid, expired, or belongs to another export or filter; restart pagination without a cursor")
	if len(raw) > 2048 {
		return c, invalid
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 2 {
		return c, invalid
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return c, invalid
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return c, invalid
	}
	mac := hmac.New(sha256.New, exportSummaryCursorKey)
	_, _ = mac.Write(body)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return c, invalid
	}
	if err := json.Unmarshal(body, &c); err != nil || c.Version != 1 || c.Query != query || c.Kind != kind || c.Offset < 0 {
		return c, invalid
	}
	return c, nil
}

type exportAddressStatistic struct {
	FromAddress    string  `json:"from_address"`
	RecordCount    int64   `json:"record_count"`
	TotalAmount    string  `json:"total_amount"`
	TotalAmountRaw *string `json:"total_amount_raw"`
	FirstBlock     string  `json:"first_block"`
	LastBlock      string  `json:"last_block"`
}

type exportTransferDetail struct {
	RecordID    string            `json:"record_id"`
	FromAddress string            `json:"from_address"`
	ToAddress   string            `json:"to_address"`
	TxHash      string            `json:"tx_hash"`
	BlockNumber string            `json:"block_number"`
	Amount      string            `json:"amount"`
	AmountRaw   string            `json:"amount_raw"`
	Fields      map[string]string `json:"fields"`
}

type exportAddressSummary struct {
	JobID            string                   `json:"job_id"`
	Status           string                   `json:"status"`
	SourceSHA256     string                   `json:"source_sha256"`
	Filters          exportSummaryQuery       `json:"filters"`
	ExportTotalRows  int64                    `json:"export_total_rows"`
	DatasetRows      int64                    `json:"dataset_rows"`
	TotalRecords     int64                    `json:"total_records"`
	TotalAddresses   int                      `json:"total_addresses"`
	TotalAmount      string                   `json:"total_amount"`
	TotalAmountRaw   *string                  `json:"total_amount_raw"`
	DataCutoffBlock  *string                  `json:"data_cutoff_block"`
	Addresses        []exportAddressStatistic `json:"addresses"`
	NextCursor       *string                  `json:"next_cursor"`
	HasMore          bool                     `json:"has_more"`
	Details          []exportTransferDetail   `json:"details"`
	NextDetailCursor *string                  `json:"next_detail_cursor"`
	DetailsHasMore   bool                     `json:"details_has_more"`
	PageSize         int                      `json:"page_size"`
	DetailPageSize   int                      `json:"detail_page_size"`
}

func (s *Server) handleActionExportAddressSummary(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("job_id"))
	if _, err := uuid.Parse(id); err != nil {
		actionFail(w, 400, "INVALID_QUERY", "Invalid job_id")
		return
	}
	scope, err := s.systemScope(r.Context())
	if err != nil {
		actionFail(w, 500, "INTERNAL_ERROR", "Unable to resolve Actions workspace")
		return
	}
	var org, workspace string
	var snapshotJSON []byte
	err = s.deps.Store.Pool.QueryRow(r.Context(), `SELECT COALESCE(organization_id::text,''), COALESCE(workspace_id::text,''), COALESCE(query_snapshot,'{}'::jsonb) FROM exports WHERE id=$1`, id).Scan(&org, &workspace, &snapshotJSON)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (org != scope.OrganizationID || workspace != scope.WorkspaceID)) {
		actionFail(w, 404, "JOB_NOT_FOUND", "Export job not found")
		return
	}
	if err != nil {
		actionFail(w, 500, "INTERNAL_ERROR", "Unable to read export metadata")
		return
	}
	exp, err := s.deps.Store.GetExport(r.Context(), id)
	if err != nil || exp == nil {
		actionFail(w, 500, "INTERNAL_ERROR", "Unable to read export metadata")
		return
	}
	if exp.Status != "COMPLETED" {
		actionFail(w, 409, "EXPORT_NOT_COMPLETED", "Only COMPLETED exports can be summarized")
		return
	}
	var snapshot exportSummarySnapshot
	if err := json.Unmarshal(snapshotJSON, &snapshot); err != nil {
		actionFail(w, 422, "INVALID_EXPORT_DATA", "Invalid export snapshot")
		return
	}
	q, err := parseExportSummaryQuery(r.URL.Query(), snapshot)
	if err != nil {
		writeExportSummaryError(w, err)
		return
	}
	select {
	case exportSummarySlots <- struct{}{}:
		defer func() { <-exportSummarySlots }()
	default:
		actionFail(w, 503, "EXPORT_SUMMARY_BUSY", "Export analysis is busy; retry later")
		return
	}
	reader, size, _, err := s.openExportObject(r.Context(), exp.ObjectRef)
	if err != nil {
		actionFail(w, 503, "EXPORT_FILE_UNAVAILABLE", "Unable to read the completed export file")
		return
	}
	defer reader.Close()
	file, err := os.CreateTemp("", "export-address-summary-*")
	if err != nil {
		actionFail(w, 503, "EXPORT_FILE_UNAVAILABLE", "Unable to stage the completed export file")
		return
	}
	defer os.Remove(file.Name())
	defer file.Close()
	digest := sha256.New()
	n, err := io.Copy(io.MultiWriter(file, digest), reader)
	if err != nil || n != size {
		actionFail(w, 503, "EXPORT_FILE_UNAVAILABLE", "Completed export file could not be read in full")
		return
	}
	checksum := hex.EncodeToString(digest.Sum(nil))
	if exp.SHA256 != "" && !strings.EqualFold(checksum, exp.SHA256) {
		actionFail(w, 422, "EXPORT_INTEGRITY_ERROR", "Completed export checksum does not match its metadata")
		return
	}
	result, partial, err := summarizeExportObject(r.Context(), file, size, exp, q, checksum)
	if err != nil {
		writeExportSummaryError(w, err)
		return
	}
	actionOK(w, result, map[string]any{"source": "completed_export_file", "dataset": "token_transfers", "group_by": "from_address", "address_order": "from_address_asc", "detail_order": "entry_name_then_record_number", "count_semantics": "export_records", "cutoff_basis": "highest_observed_block_in_exported_token_transfers", "partial_export": partial, "amount_precision": "exact_decimal_string"})
}

func writeExportSummaryError(w http.ResponseWriter, err error) {
	var e *exportSummaryError
	if errors.As(err, &e) {
		actionFail(w, e.status, e.code, e.message)
		return
	}
	actionFail(w, 422, "INVALID_EXPORT_DATA", "Unable to read complete token-transfer records from this export")
}

type exportSummaryAccumulator struct {
	count       int64
	amount, raw decimal.Decimal
	rawComplete bool
	first, last uint64
}

// Only one transfer dataset is counted. All its rows are streamed to completion;
// pagination bounds the response, never the population used for global totals.
func summarizeExportObject(ctx context.Context, file io.ReaderAt, size int64, exp *model.Export, q exportSummaryQuery, checksum string) (*exportAddressSummary, bool, error) {
	queryJSON, _ := json.Marshal(q)
	fingerprint := sha256.Sum256(append([]byte(exp.ID+":"+checksum+":"), queryJSON...))
	queryID := hex.EncodeToString(fingerprint[:])
	addressCursor, err := decodeExportSummaryCursor(q.Cursor, queryID, "addresses")
	if err != nil {
		return nil, false, err
	}
	detailCursor, err := decodeExportSummaryCursor(q.DetailCursor, queryID, "details")
	if err != nil {
		return nil, false, err
	}
	result := &exportAddressSummary{JobID: exp.ID, Status: "completed", SourceSHA256: checksum, Filters: q,
		ExportTotalRows: exp.Rows, Addresses: []exportAddressStatistic{}, Details: []exportTransferDetail{}, PageSize: q.PageSize, DetailPageSize: q.DetailPageSize}
	groups := map[string]*exportSummaryAccumulator{}
	total, rawTotal := decimal.Zero, decimal.Zero
	rawComplete := true
	var cutoff uint64
	var asset string
	consume := func(entry string, number int64, row map[string]string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if ds := row["dataset"]; ds != "" && ds != "token_transfers" {
			return summaryError(422, "INVALID_EXPORT_DATA", "Token-transfer part contains a different dataset")
		}
		if chain := row["chain"]; chain != "" && chain != q.Chain {
			return summaryError(422, "INVALID_EXPORT_DATA", "Export row chain differs from its snapshot")
		}
		block, err := strconv.ParseUint(row["block_number"], 10, 64)
		if err != nil {
			return summaryError(422, "INVALID_EXPORT_DATA", "Transfer is missing a valid block_number")
		}
		result.DatasetRows++
		if block > cutoff || result.DatasetRows == 1 {
			cutoff = block
		}
		contract := addressnorm.NormalizeKey(q.Chain, firstSummaryValue(row, "token_contract", "token_address", "contract_address"))
		if q.Token != "" && !strings.EqualFold(row["token_symbol"], q.Token) {
			return nil
		}
		if q.TokenContract != "" && contract != q.TokenContract {
			return nil
		}
		from, to := addressnorm.NormalizeKey(q.Chain, row["from_address"]), addressnorm.NormalizeKey(q.Chain, row["to_address"])
		if q.Address != "" {
			if q.Direction == "in" && to != q.Address {
				return nil
			}
			if q.Direction == "out" && from != q.Address {
				return nil
			}
			if q.Direction == "all" && from != q.Address && to != q.Address {
				return nil
			}
		}
		if q.FromAddress != "" && from != q.FromAddress {
			return nil
		}
		if from == "" || to == "" || row["tx_hash"] == "" || contract == "" {
			return summaryError(422, "INVALID_EXPORT_DATA", "Transfer is missing a full payer, recipient, transaction hash or token contract")
		}
		for _, value := range []string{from, to, contract} {
			if _, err := addressnorm.Normalize(q.Chain, value); err != nil {
				return summaryError(422, "INVALID_EXPORT_DATA", "Matched transfer contains an invalid or abbreviated address")
			}
		}
		amount, raw, decimals, err := exactExportTransferAmount(row)
		if err != nil {
			return err
		}
		identity := contract + ":" + decimals
		if asset != "" && asset != identity {
			return summaryError(422, "AMBIGUOUS_TOKEN", "Matched transfers have different token contracts or decimal scales; use one token_contract")
		}
		asset = identity
		group := groups[from]
		if group == nil {
			group = &exportSummaryAccumulator{rawComplete: true, first: block, last: block}
			groups[from] = group
		}
		group.count++
		group.amount = group.amount.Add(amount)
		if block < group.first {
			group.first = block
		}
		if block > group.last {
			group.last = block
		}
		total = total.Add(amount)
		if raw == "" {
			group.rawComplete, rawComplete = false, false
		} else {
			rawAmount, _ := decimal.NewFromString(raw)
			group.raw, rawTotal = group.raw.Add(rawAmount), rawTotal.Add(rawAmount)
		}
		if result.TotalRecords >= detailCursor.Offset && len(result.Details) < q.DetailPageSize {
			result.Details = append(result.Details, exportTransferDetail{RecordID: entry + ":" + strconv.FormatInt(number, 10), FromAddress: row["from_address"], ToAddress: row["to_address"], TxHash: row["tx_hash"], BlockNumber: row["block_number"], Amount: amount.String(), AmountRaw: raw, Fields: row})
		}
		result.TotalRecords++
		return nil
	}
	partial, err := readExportTransferRows(ctx, file, size, exp, consume)
	if err != nil {
		return nil, partial, err
	}
	if exp.Dataset == "token_transfers" && result.DatasetRows != exp.Rows {
		return nil, partial, summaryError(422, "EXPORT_ROW_COUNT_MISMATCH", "Read record count differs from the completed export metadata")
	}
	if detailCursor.Offset > result.TotalRecords {
		return nil, partial, summaryError(400, "INVALID_CURSOR", "Detail cursor exceeds the matched record count")
	}
	result.TotalAmount, result.TotalAddresses = total.String(), len(groups)
	if rawComplete {
		value := rawTotal.String()
		result.TotalAmountRaw = &value
	}
	if result.DatasetRows > 0 {
		value := strconv.FormatUint(cutoff, 10)
		result.DataCutoffBlock = &value
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	start := sort.SearchStrings(keys, addressCursor.After)
	if addressCursor.After != "" {
		if start >= len(keys) || keys[start] != addressCursor.After {
			return nil, partial, summaryError(400, "INVALID_CURSOR", "Address cursor is not present in this export summary")
		}
		start++
	}
	end := min(start+q.PageSize, len(keys))
	for _, key := range keys[start:end] {
		g := groups[key]
		item := exportAddressStatistic{FromAddress: key, RecordCount: g.count, TotalAmount: g.amount.String(), FirstBlock: strconv.FormatUint(g.first, 10), LastBlock: strconv.FormatUint(g.last, 10)}
		if g.rawComplete {
			value := g.raw.String()
			item.TotalAmountRaw = &value
		}
		result.Addresses = append(result.Addresses, item)
	}
	result.HasMore = end < len(keys)
	if result.HasMore {
		value := encodeExportSummaryCursor(exportSummaryCursor{Version: 1, Query: queryID, Kind: "addresses", After: keys[end-1]})
		result.NextCursor = &value
	}
	result.DetailsHasMore = detailCursor.Offset+int64(len(result.Details)) < result.TotalRecords
	if result.DetailsHasMore {
		value := encodeExportSummaryCursor(exportSummaryCursor{Version: 1, Query: queryID, Kind: "details", Offset: detailCursor.Offset + int64(len(result.Details))})
		result.NextDetailCursor = &value
	}
	return result, partial, nil
}

func firstSummaryValue(row map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(row[key]); value != "" && value != "<nil>" && value != "<null>" {
			return value
		}
	}
	return ""
}

func exactExportTransferAmount(row map[string]string) (decimal.Decimal, string, string, error) {
	invalid := summaryError(422, "INVALID_EXPORT_DATA", "Matched transfer has missing or invalid exact amount/decimal metadata")
	raw := firstSummaryValue(row, "amount_raw", "raw_value")
	scale := firstSummaryValue(row, "token_decimals")
	if raw != "" && scale != "" {
		n, err := strconv.ParseInt(scale, 10, 32)
		if err != nil || n < 0 || n > 255 {
			return decimal.Zero, "", "", invalid
		}
		for _, c := range raw {
			if c < '0' || c > '9' {
				return decimal.Zero, "", "", invalid
			}
		}
		amount, err := decimal.NewFromString(raw)
		if err != nil {
			return decimal.Zero, "", "", invalid
		}
		// Unknown decimals must never turn base units into a fabricated token amount.
		switch row["token_metadata_status"] {
		case "missing", "unknown", "unavailable", "pending", "error":
			return decimal.Zero, "", "", invalid
		}
		return amount.Shift(-int32(n)), raw, strconv.FormatInt(n, 10), nil
	}
	value := firstSummaryValue(row, "formatted_amount", "amount")
	if value == "" {
		return decimal.Zero, "", "", invalid
	}
	// Only plain decimal strings are accepted; no binary floating point conversion.
	for _, c := range value {
		if (c < '0' || c > '9') && c != '.' {
			return decimal.Zero, "", "", invalid
		}
	}
	amount, err := decimal.NewFromString(value)
	if err != nil || amount.IsNegative() {
		return decimal.Zero, "", "", invalid
	}
	if raw != "" {
		return decimal.Zero, "", "", invalid
	}
	return amount, "", scale, nil
}

type exportTransferConsumer func(string, int64, map[string]string) error

func readExportTransferRows(ctx context.Context, file io.ReaderAt, size int64, exp *model.Export, consume exportTransferConsumer) (bool, error) {
	if !exp.Zip && !strings.HasSuffix(exp.ObjectRef, ".zip") {
		if exp.Dataset != "token_transfers" {
			return false, summaryError(422, "DATASET_NOT_EXPORTED", "This export does not contain token_transfers")
		}
		return false, readTransferPart(ctx, io.NewSectionReader(file, 0, size), size, exp.Format, "token_transfers."+exp.Format, consume)
	}
	archive, err := zip.NewReader(file, size)
	if err != nil {
		return false, err
	}
	parts := append([]*zip.File(nil), archive.File...)
	sort.Slice(parts, func(i, j int) bool { return parts[i].Name < parts[j].Name })
	found, partial := false, false
	names := map[string]bool{}
	for _, part := range parts {
		if err := ctx.Err(); err != nil {
			return partial, err
		}
		if names[part.Name] {
			return partial, summaryError(422, "INVALID_EXPORT_DATA", "Export ZIP contains duplicate entry names")
		}
		names[part.Name] = true
		if part.Name == "manifest.json" {
			r, err := part.Open()
			if err != nil {
				return partial, err
			}
			var manifest struct {
				Partial bool `json:"partial"`
			}
			err = json.NewDecoder(io.LimitReader(r, 1<<20)).Decode(&manifest)
			_ = r.Close()
			if err != nil {
				return partial, err
			}
			partial = manifest.Partial
			continue
		}
		name := path.Base(part.Name)
		if name != "token_transfers.csv" && name != "token_transfers.parquet" {
			continue
		}
		found = true
		r, err := part.Open()
		if err != nil {
			return partial, err
		}
		if strings.HasSuffix(name, ".csv") {
			err = readTransferCSV(r, part.Name, consume)
		} else {
			// Parquet requires ReaderAt. Spool one ZIP member at a time, never all
			// transfers or all archive members in memory.
			tmp, e := os.CreateTemp("", "export-summary-parquet-*")
			if e != nil {
				_ = r.Close()
				return partial, e
			}
			n, e := io.Copy(tmp, r)
			if e == nil {
				e = readTransferParquet(tmp, n, part.Name, consume)
			}
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
			err = e
		}
		_ = r.Close()
		if err != nil {
			return partial, err
		}
	}
	if !found {
		return partial, summaryError(422, "DATASET_NOT_EXPORTED", "This export does not contain token_transfers")
	}
	return partial, nil
}

func readTransferPart(ctx context.Context, reader *io.SectionReader, size int64, format, name string, consume exportTransferConsumer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	switch format {
	case "csv":
		return readTransferCSV(reader, name, consume)
	case "parquet":
		return readTransferParquet(reader, size, name, consume)
	default:
		return summaryError(422, "UNSUPPORTED_EXPORT_FORMAT", "Export summary supports CSV and Parquet, including ZIP archives")
	}
}

func readTransferCSV(reader io.Reader, name string, consume exportTransferConsumer) error {
	csvReader := csv.NewReader(reader)
	header, err := csvReader.Read()
	if err == io.EOF {
		return nil
	}
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for i, key := range header {
		key = strings.TrimPrefix(key, "\ufeff")
		header[i] = key
		if key == "" || seen[key] {
			return summaryError(422, "INVALID_EXPORT_DATA", "CSV has empty or duplicate column names")
		}
		seen[key] = true
	}
	for number := int64(1); ; number++ {
		values, err := csvReader.Read()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		row := make(map[string]string, len(header))
		for i, key := range header {
			row[key] = values[i]
		}
		if err := consume(name, number, row); err != nil {
			return err
		}
	}
}

func readTransferParquet(reader io.ReaderAt, size int64, name string, consume exportTransferConsumer) error {
	file, err := parquet.OpenFile(reader, size)
	if err != nil {
		return err
	}
	columns := file.Schema().Columns()
	r := parquet.NewReader(file)
	defer r.Close()
	batch := make([]parquet.Row, 256)
	var number int64
	for {
		n, err := r.ReadRows(batch)
		for _, record := range batch[:n] {
			row := make(map[string]string, len(columns))
			arrays := map[string][]string{}
			for _, value := range record {
				key := strings.Join(columns[value.Column()], ".")
				if strings.HasSuffix(key, ".list.element") {
					key = strings.TrimSuffix(key, ".list.element")
					if _, exists := arrays[key]; !exists {
						arrays[key] = []string{}
					}
					if !value.IsNull() {
						arrays[key] = append(arrays[key], value.String())
					}
					continue
				}
				if value.IsNull() {
					row[key] = ""
					continue
				}
				if (key == "amount" || key == "amount_raw" || key == "raw_value" || key == "formatted_amount") && (value.Kind() == parquet.Float || value.Kind() == parquet.Double) {
					return summaryError(422, "INVALID_EXPORT_DATA", "Parquet monetary columns must preserve decimal strings")
				}
				if _, exists := row[key]; exists {
					return summaryError(422, "INVALID_EXPORT_DATA", "Unexpected repeated Parquet transfer column")
				}
				row[key] = value.String()
			}
			for key, values := range arrays {
				encoded, _ := json.Marshal(values)
				row[key] = string(encoded)
			}
			number++
			if err := consume(name, number, row); err != nil {
				return err
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
