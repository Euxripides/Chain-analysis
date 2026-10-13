package query

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

var rowDatasets = []string{"transactions", "token_transfers", "nft_transfers", "logs", "balances", "activity", "internal_transactions"}

type fixtureScanner struct {
	value any
	err   error
}

func (f fixtureScanner) ScanStruct(dest any) error {
	if f.err != nil {
		return f.err
	}
	reflect.ValueOf(dest).Elem().Set(reflect.ValueOf(f.value).Elem())
	return nil
}

func rowFixture(dataset string, nullable bool) any {
	var row any
	switch dataset {
	case "transactions":
		row = &txRow{}
	case "token_transfers":
		row = &tokenTransferRow{}
	case "nft_transfers":
		row = &nftTransferRow{}
	case "logs":
		row = &logRow{}
	case "balances":
		row = &balanceRow{}
	case "activity":
		row = &activityRow{}
	case "internal_transactions":
		row = &internalTransferRow{}
	}
	v := reflect.ValueOf(row).Elem()
	at := time.Date(2026, 10, 3, 9, 8, 7, 123456789, time.FixedZone("CST", 8*3600))
	amount := decimal.RequireFromString("123456789012345678901234567890.000000000000000001")
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		switch f.Interface().(type) {
		case time.Time:
			f.Set(reflect.ValueOf(at))
		case *time.Time:
			if !nullable {
				f.Set(reflect.ValueOf(&at))
			}
		case decimal.Decimal:
			f.Set(reflect.ValueOf(amount))
		default:
			switch f.Kind() {
			case reflect.String:
				f.SetString(v.Type().Field(i).Name + " 中文,\n\"<>&\u2028\u2029")
			case reflect.Uint64:
				f.SetUint(math.MaxUint64 - uint64(i))
			case reflect.Uint32:
				f.SetUint(math.MaxUint32 - uint64(i))
			case reflect.Uint16:
				f.SetUint(uint64(i))
			case reflect.Uint8:
				f.SetUint(1)
			case reflect.Slice:
				if !nullable {
					f.Set(reflect.ValueOf([]string{"0xabc", "0xDEF", "逗号,换行\n"}))
				}
			}
		}
	}
	return row
}

// The existing JSON conversion is the independent compatibility oracle. Keep
// it in tests to check every field/tag when row schemas evolve.
func legacyRowMap(dataset string, value any) (map[string]any, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.UseNumber()
	var m map[string]any
	if err := d.Decode(&m); err != nil {
		return nil, err
	}
	if dataset == "internal_transactions" {
		v := value.(*internalTransferRow)
		m["rejected"] = v.Success == 0 || strings.EqualFold(v.Error, "rejected")
		if strings.EqualFold(v.Chain, "tron") {
			m["asset_type"] = "TRX"
		}
	}
	return m, nil
}

func TestQueryRowJSONContract(t *testing.T) {
	for _, dataset := range rowDatasets {
		for _, nullable := range []bool{false, true} {
			t.Run(dataset+map[bool]string{true: "/null", false: "/populated"}[nullable], func(t *testing.T) {
				v := rowFixture(dataset, nullable)
				got, err := scanTypedOne(dataset, fixtureScanner{value: v})
				if err != nil {
					t.Fatal(err)
				}
				want, err := legacyRowMap(dataset, v)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("row contract changed:\ngot  %#v\nwant %#v", got, want)
				}
			})
		}
	}
}

func TestQueryRowErrorAndInternalSemantics(t *testing.T) {
	boom := errors.New("scan failed")
	if _, err := scanTypedOne("transactions", fixtureScanner{err: boom}); !errors.Is(err, boom) {
		t.Fatalf("scan error: %v", err)
	}
	for _, row := range []*internalTransferRow{{Chain: "tron", Success: 0}, {Chain: "TrOn", Success: 1, Error: "rejected"}, {Chain: "eth", Success: 1}} {
		got, err := scanTypedOne("internal_transactions", fixtureScanner{value: row})
		want, wantErr := legacyRowMap("internal_transactions", row)
		if err != nil || wantErr != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("internal transfer: %v %v %v %v", got, want, err, wantErr)
		}
	}
}

func TestQueryRowEncodingEdges(t *testing.T) {
	for _, v := range []*logRow{
		{Topics: nil}, {Topics: []string{}},
		{Data: "invalid\xff\xfeUTF8", Topics: []string{"\xff\xfe", "\xed\xa0\x80", "😀"}},
		{Timestamp: time.Date(0, 1, 1, 0, 0, 0, 999999999, time.UTC)},
	} {
		got, err := scanTypedOne("logs", fixtureScanner{value: v})
		want, wantErr := legacyRowMap("logs", v)
		if err != nil || wantErr != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("encoding mismatch: %#v %#v %v %v", got, want, err, wantErr)
		}
	}
	for _, at := range []time.Time{time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("invalid", 24*3600))} {
		v := &txRow{Timestamp: at}
		if _, err := scanTypedOne("transactions", fixtureScanner{value: v}); err == nil {
			t.Fatal("invalid time accepted")
		}
		if _, err := legacyRowMap("transactions", v); err == nil {
			t.Fatal("fixture was not an invalid JSON time")
		}
	}
}

func TestQueryScannerDoesNotRetainPreviousRowData(t *testing.T) {
	scanner, err := newDatasetRowScanner("logs")
	if err != nil {
		t.Fatal(err)
	}
	source := &logRow{Data: "first", Topics: []string{"first-topic"}}
	first, err := scanner.scan(fixtureScanner{value: source})
	if err != nil {
		t.Fatal(err)
	}
	source.Data, source.Topics[0] = "second", "second-topic"
	second, err := scanner.scan(fixtureScanner{value: source})
	if err != nil {
		t.Fatal(err)
	}
	if first["data"] != "first" || first["topics"].([]any)[0] != "first-topic" || second["data"] != "second" {
		t.Fatal("reused scanner aliases earlier output")
	}
	// A partial projection must not inherit fields from the preceding scan.
	third, err := scanner.scan(partialLogScanner{})
	if err != nil {
		t.Fatal(err)
	}
	if third["topics"] != nil || third["data"] != "third" {
		t.Fatalf("stale fields: %v", third)
	}
}

type fixtureColumnScanner struct {
	values []any
	err    error
}

func (f fixtureColumnScanner) Scan(dest ...any) error {
	if f.err != nil {
		return f.err
	}
	if len(dest) != len(f.values) {
		return errors.New("wrong destination count")
	}
	for i, v := range f.values {
		reflect.ValueOf(dest[i]).Elem().Set(reflect.ValueOf(v))
	}
	return nil
}

func TestQueryBoundColumnOrderAndProjection(t *testing.T) {
	for _, dataset := range rowDatasets {
		t.Run(dataset, func(t *testing.T) {
			fixture := rowFixture(dataset, false)
			v := reflect.ValueOf(fixture).Elem()
			var columns []string
			var values []any
			// Reverse the table's normal column order: binding must use ch tags,
			// including call_index, whose JSON name is internal_index.
			for i := v.NumField() - 1; i >= 0; i-- {
				columns = append(columns, v.Type().Field(i).Tag.Get("ch"))
				values = append(values, v.Field(i).Interface())
			}
			scanner, err := newDatasetRowScanner(dataset, columns...)
			if err != nil {
				t.Fatal(err)
			}
			got, err := scanner.scanColumns(fixtureColumnScanner{values: values})
			want, wantErr := legacyRowMap(dataset, fixture)
			if err != nil || wantErr != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("bound columns changed output: %v %v %v %v", got, want, err, wantErr)
			}
		})
	}
	if _, err := newDatasetRowScanner("transactions", "missing_column"); err == nil {
		t.Fatal("unknown database column was silently omitted")
	}
	scanner, err := newDatasetRowScanner("logs", "topics", "data")
	if err != nil {
		t.Fatal(err)
	}
	topics := []string{"first"}
	first, err := scanner.scanColumns(fixtureColumnScanner{values: []any{topics, "one"}})
	if err != nil {
		t.Fatal(err)
	}
	topics[0] = "second"
	second, err := scanner.scanColumns(fixtureColumnScanner{values: []any{[]string(nil), "two"}})
	if err != nil || first["data"] != "one" || first["topics"].([]any)[0] != "first" || second["data"] != "two" || second["topics"] != nil || second["block_number"] != json.Number("0") {
		t.Fatalf("projected rows were retained: %v %v %v", first, second, err)
	}
	boom := errors.New("column scan failed")
	if _, err := scanner.scanColumns(fixtureColumnScanner{err: boom}); !errors.Is(err, boom) {
		t.Fatalf("column scan error lost: %v", err)
	}
}

type partialLogScanner struct{}

func (partialLogScanner) ScanStruct(dest any) error { dest.(*logRow).Data = "third"; return nil }

func BenchmarkQueryScanReused(b *testing.B) {
	for _, dataset := range []string{"token_transfers", "transactions", "logs"} {
		fixture := fixtureScanner{value: rowFixture(dataset, false)}
		scanner, err := newDatasetRowScanner(dataset)
		if err != nil {
			b.Fatal(err)
		}
		b.Run(dataset, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				row, err := scanner.scan(fixture)
				if err != nil {
					b.Fatal(err)
				}
				scanBenchmarkSink = row
			}
		})
	}
}

var scanBenchmarkSink map[string]any

func BenchmarkQueryScan(b *testing.B) {
	for _, dataset := range []string{"token_transfers", "transactions", "logs"} {
		fixture := fixtureScanner{value: rowFixture(dataset, false)}
		b.Run(dataset, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				row, err := scanTypedOne(dataset, fixture)
				if err != nil {
					b.Fatal(err)
				}
				scanBenchmarkSink = row
			}
		})
	}
}
