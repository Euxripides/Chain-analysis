package query

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shopspring/decimal"
)

type rowValueKind uint8

const (
	rowString rowValueKind = iota
	rowUint
	rowInt
	rowTime
	rowNullableTime
	rowDecimal
	rowStrings
)

type rowField struct {
	index int
	name  string
	kind  rowValueKind
}

type rowLayout struct {
	fields []rowField
	err    error
}

// Layouts are compiled once from the existing JSON tags. This keeps the query
// contract in the row structs instead of maintaining a second field-name list.
var rowLayouts = func() map[reflect.Type]rowLayout {
	result := map[reflect.Type]rowLayout{}
	for _, value := range []any{txRow{}, tokenTransferRow{}, nftTransferRow{}, logRow{}, balanceRow{}, activityRow{}, internalTransferRow{}} {
		t := reflect.TypeOf(value)
		result[t] = compileRowLayout(t)
	}
	return result
}()

func compileRowLayout(t reflect.Type) rowLayout {
	l := rowLayout{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" || f.Tag.Get("json") == "-" {
			continue
		}
		name := f.Tag.Get("json")
		if name == "" {
			name = f.Name
		}
		if strings.Contains(name, ",") {
			return rowLayout{err: fmt.Errorf("unsupported row JSON options: %s.%s", t.Name(), f.Name)}
		}
		field := rowField{index: i, name: name}
		switch f.Type {
		case reflect.TypeFor[time.Time]():
			field.kind = rowTime
		case reflect.TypeFor[*time.Time]():
			field.kind = rowNullableTime
		case reflect.TypeFor[decimal.Decimal]():
			field.kind = rowDecimal
		case reflect.TypeFor[[]string]():
			field.kind = rowStrings
		default:
			switch f.Type.Kind() {
			case reflect.String:
				field.kind = rowString
			case reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				field.kind = rowUint
			case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Int:
				field.kind = rowInt
			default:
				return rowLayout{err: fmt.Errorf("unsupported row field: %s.%s (%s)", t.Name(), f.Name, f.Type)}
			}
		}
		l.fields = append(l.fields, field)
	}
	return l
}

// rowStringValue preserves encoding/json's invalid-UTF-8 replacement. Most
// database strings are valid and can be shared without copying their bytes.
func rowStringValue(value string) string {
	if utf8.ValidString(value) {
		return value
	}
	return strings.Map(func(r rune) rune { return r }, value)
}

func rowTimeValue(value time.Time) (string, error) {
	// Retain MarshalJSON validation of years and timezone offsets, including
	// its full nanosecond precision. Only the surrounding quotes are removed.
	raw, err := value.MarshalJSON()
	if err != nil {
		return "", err
	}
	return string(raw[1 : len(raw)-1]), nil
}

func (l rowLayout) convert(value reflect.Value) (map[string]any, error) {
	if l.err != nil {
		return nil, l.err
	}
	result := make(map[string]any, len(l.fields)+2)
	for _, field := range l.fields {
		v := value.Field(field.index)
		var out any
		var err error
		switch field.kind {
		case rowString:
			out = rowStringValue(v.String())
		case rowUint:
			out = json.Number(strconv.FormatUint(v.Uint(), 10))
		case rowInt:
			out = json.Number(strconv.FormatInt(v.Int(), 10))
		case rowTime:
			out, err = rowTimeValue(v.Interface().(time.Time))
		case rowNullableTime:
			if !v.IsNil() {
				out, err = rowTimeValue(*v.Interface().(*time.Time))
			}
		case rowDecimal:
			d := v.Interface().(decimal.Decimal)
			if decimal.MarshalJSONWithoutQuotes {
				out = json.Number(d.String())
			} else {
				out = d.String()
			}
		case rowStrings:
			if !v.IsNil() {
				items := make([]any, v.Len())
				for i := range items {
					items[i] = rowStringValue(v.Index(i).String())
				}
				out = items
			}
		}
		if err != nil {
			return nil, fmt.Errorf("row field %s: %w", field.name, err)
		}
		result[field.name] = out
	}
	return result, nil
}

type datasetRowScanner struct {
	dataset string
	value   any
	row     reflect.Value
	layout  rowLayout
	dest    []any
}

func newDatasetRowScanner(dataset string, columns ...string) (*datasetRowScanner, error) {
	var value any
	switch dataset {
	case "transactions":
		value = &txRow{}
	case "token_transfers":
		value = &tokenTransferRow{}
	case "nft_transfers":
		value = &nftTransferRow{}
	case "logs":
		value = &logRow{}
	case "balances":
		value = &balanceRow{}
	case "activity":
		value = &activityRow{}
	case "internal_transactions":
		value = &internalTransferRow{}
	default:
		return nil, fmt.Errorf("unknown dataset %s", dataset)
	}
	row := reflect.ValueOf(value).Elem()
	scanner := &datasetRowScanner{dataset: dataset, value: value, row: row, layout: rowLayouts[row.Type()]}
	if scanner.layout.err != nil {
		return nil, scanner.layout.err
	}
	if len(columns) > 0 {
		// Bind the actual result order once per query. Partial projections and
		// schema column reordering still address the existing ch-tagged fields.
		fields := make(map[string]int, row.NumField())
		for i := 0; i < row.NumField(); i++ {
			field := row.Type().Field(i)
			name := field.Tag.Get("ch")
			if name == "" {
				name = field.Name
			}
			if name != "-" && field.PkgPath == "" {
				fields[name] = i
			}
		}
		scanner.dest = make([]any, len(columns))
		for i, column := range columns {
			index, ok := fields[column]
			if !ok {
				return nil, fmt.Errorf("missing destination name %q in %T", column, value)
			}
			scanner.dest[i] = row.Field(index).Addr().Interface()
		}
	}
	return scanner, nil
}

// A scanner belongs to one query. Returned maps and slices are independent of
// its reusable struct, so batching/retaining previous rows is safe.
func (s *datasetRowScanner) scan(rows interface{ ScanStruct(any) error }) (map[string]any, error) {
	s.row.SetZero()
	if err := rows.ScanStruct(s.value); err != nil {
		return nil, err
	}
	return s.result()
}

func (s *datasetRowScanner) scanColumns(rows interface{ Scan(...any) error }) (map[string]any, error) {
	s.row.SetZero()
	if err := rows.Scan(s.dest...); err != nil {
		return nil, err
	}
	return s.result()
}

func (s *datasetRowScanner) result() (map[string]any, error) {
	m, err := s.layout.convert(s.row)
	if err != nil {
		return nil, err
	}
	if s.dataset == "internal_transactions" {
		v := s.value.(*internalTransferRow)
		m["rejected"] = v.Success == 0 || strings.EqualFold(v.Error, "rejected")
		if strings.EqualFold(v.Chain, "tron") {
			m["asset_type"] = "TRX"
		}
	}
	return m, nil
}

func scanTypedOne(dataset string, rows interface{ ScanStruct(any) error }) (map[string]any, error) {
	scanner, err := newDatasetRowScanner(dataset)
	if err != nil {
		return nil, err
	}
	return scanner.scan(rows)
}
