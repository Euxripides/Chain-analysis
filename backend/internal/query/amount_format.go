package query

import (
	"encoding/json"
	"fmt"
	"strings"
)

func rowText(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case json.Number:
		return string(v)
	default:
		return fmt.Sprint(value)
	}
}

// formatScaledInteger performs only decimal point placement. It matches
// big.Int.SetString(raw, 10) followed by decimal.NewFromBigInt(...).String()
// without parsing a large integer twice. The caller supplies 0..76 decimals.
func formatScaledInteger(raw string, decimals int) (string, bool) {
	negative := false
	if len(raw) > 0 && (raw[0] == '-' || raw[0] == '+') {
		negative = raw[0] == '-'
		raw = raw[1:]
	}
	if len(raw) == 0 {
		return "", false
	}
	for i := range raw {
		if raw[i] < '0' || raw[i] > '9' {
			return "", false
		}
	}
	raw = strings.TrimLeft(raw, "0")
	if raw == "" {
		return "0", true
	}
	var amount string
	if decimals == 0 {
		amount = raw
	} else if len(raw) > decimals {
		point := len(raw) - decimals
		fraction := strings.TrimRight(raw[point:], "0")
		amount = raw[:point]
		if fraction != "" {
			amount += "." + fraction
		}
	} else {
		amount = "0." + strings.Repeat("0", decimals-len(raw)) + strings.TrimRight(raw, "0")
	}
	if negative {
		amount = "-" + amount
	}
	return amount, true
}
