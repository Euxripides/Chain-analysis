package query

import (
	"encoding/json"
	"fmt"
	"math/big"
	"math/rand"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

func legacyNormalizeAmount(dataset string, row map[string]any) {
	rawKey, amountKey := "amount_raw", "amount"
	if dataset == "balances" {
		rawKey, amountKey = "balance_raw", "balance"
	}
	raw := strings.TrimSpace(fmt.Sprint(row[rawKey]))
	if raw == "" {
		return
	}
	d, err := strconv.ParseUint(fmt.Sprint(row["token_decimals"]), 10, 16)
	if err != nil || d > 76 {
		return
	}
	integer, ok := new(big.Int).SetString(raw, 10)
	if !ok {
		return
	}
	row[amountKey] = decimal.NewFromBigInt(integer, -int32(d)).String()
}

func TestNormalizeAmountMatchesBigIntegerDecimal(t *testing.T) {
	rawValues := []string{"0", "+0", "-0", "0000", "1", "-1", "+0001000000", "100100", "1234567890123456789012345678901234567890", "\u3000-00010\t", strings.Repeat("9", 1024), "", "+", "-", "  ", "1_000", "1e18", "0xff", "1.0", "--1", "1 2", "１２", "1\x00", "\xff1"}
	rng := rand.New(rand.NewSource(20261003))
	for i := 0; i < 1000; i++ {
		digits := make([]byte, 1+rng.Intn(100))
		for j := range digits {
			digits[j] = byte('0' + rng.Intn(10))
		}
		rawValues = append(rawValues, []string{"", "+", "-"}[i%3]+string(digits))
	}
	for _, raw := range rawValues {
		for d := 0; d <= 76; d++ {
			got, valid := formatScaledInteger(strings.TrimSpace(raw), d)
			integer, wantValid := new(big.Int).SetString(strings.TrimSpace(raw), 10)
			if valid != wantValid {
				t.Fatalf("validity changed for %q: %v != %v", raw, valid, wantValid)
			}
			if wantValid {
				want := decimal.NewFromBigInt(integer, -int32(d)).String()
				if got != want {
					t.Fatalf("raw=%q decimals=%d got=%q want=%q", raw, d, got, want)
				}
			}
		}
	}
	for _, dataset := range []string{"token_transfers", "activity", "balances"} {
		for _, d := range []any{json.Number("18"), 0, uint16(76), 77, 65536, -1, "", "18.0", nil} {
			for _, raw := range []any{"50225000000000000000000", "-00010", "bad", "", nil, uint64(100), json.Number("18446744073709551615")} {
				row := map[string]any{"amount_raw": raw, "balance_raw": raw, "amount": "keep", "balance": "keep", "token_decimals": d}
				want := make(map[string]any, len(row))
				for k, v := range row {
					want[k] = v
				}
				normalizeStoredAmount(dataset, row)
				legacyNormalizeAmount(dataset, want)
				if !reflect.DeepEqual(row, want) {
					t.Fatalf("%s changed for raw=%v decimals=%v: %v != %v", dataset, raw, d, row, want)
				}
			}
		}
	}
}

func BenchmarkNormalizeAmount(b *testing.B) {
	for _, legacy := range []bool{true, false} {
		name := "optimized"
		if legacy {
			name = "legacy"
		}
		b.Run(name, func(b *testing.B) {
			row := map[string]any{"amount_raw": "1234567890123456789012345678901234567890", "token_decimals": json.Number("18")}
			b.ReportAllocs()
			for b.Loop() {
				if legacy {
					legacyNormalizeAmount("token_transfers", row)
				} else {
					normalizeStoredAmount("token_transfers", row)
				}
			}
		})
	}
}
