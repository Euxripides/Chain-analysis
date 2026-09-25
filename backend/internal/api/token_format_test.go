package api

import (
	"math/big"
	"testing"
)

// legacyFormatTokenAmount 是改动前的算法：decimal.NewFromBigInt(integer, -decimals).String()。
// 这里保留它作为参照实现，确保换成"预先构造除数 + Div"之后逐值一致——
// formatted_amount 是导出文件里人眼直接看的列，算错不会有人立刻发现。
func legacyFormatTokenAmount(raw string, decimals int) any {
	integer, ok := new(big.Int).SetString(raw, 10)
	if !ok {
		return nil
	}
	return legacyDecimalString(integer, decimals)
}

func legacyDecimalString(integer *big.Int, decimals int) string {
	// decimal.NewFromBigInt(integer, -int32(decimals)).String()
	div := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	return legacyDecimalFromBigInt(integer, div, decimals)
}

func legacyDecimalFromBigInt(integer, div *big.Int, decimals int) string {
	neg := integer.Sign() < 0
	abs := new(big.Int).Abs(integer)
	intPart, rem := new(big.Int).QuoRem(abs, div, new(big.Int))
	frac := rem.String()
	for len(frac) < decimals {
		frac = "0" + frac
	}
	frac = trimTrailingZeros(frac)
	out := intPart.String()
	if frac != "" {
		out += "." + frac
	}
	if neg {
		out = "-" + out
	}
	return out
}

func trimTrailingZeros(s string) string {
	end := len(s)
	for end > 0 && s[end-1] == '0' {
		end--
	}
	return s[:end]
}

func TestFormatTokenAmountMatchesLegacy(t *testing.T) {
	cases := []struct {
		raw      string
		decimals int
	}{
		{"1764025991317028396", 18},
		{"10000000000000000000", 18},
		{"1", 18},
		{"0", 18},
		{"268000000000000000000", 18},
		{"3564761408383706926091", 18},
		{"123456789", 6},
		{"123456789", 0},
		{"1", 0},
		{"1000000", 6},
		{"9999999999999999999999999999999999999999999999999999999999999999999999999999", 18},
	}
	for _, tc := range cases {
		plan := tokenMetaPlan{decimals: tc.decimals, decimalsKnown: true}
		got := formatTokenAmount(tc.raw, plan)
		want := legacyFormatTokenAmount(tc.raw, tc.decimals)
		if fmtSprint(got) != fmtSprint(want) {
			t.Errorf("raw=%s decimals=%d: got %v, want %v", tc.raw, tc.decimals, got, want)
		}
	}
}

// TestFormatTokenAmountRejectsUnknownDecimals 锁定"精度未知就置空"的行为：
// 伪造 18 位会让导出文件出现错误金额，比留空更糟（BUG-001 的口径）。
func TestFormatTokenAmountRejectsUnknownDecimals(t *testing.T) {
	if got := formatTokenAmount("123", tokenMetaPlan{}); got != nil {
		t.Fatalf("unknown decimals must yield nil, got %v", got)
	}
	if got := formatTokenAmount("not-a-number", tokenMetaPlan{decimals: 18, decimalsKnown: true}); got != nil {
		t.Fatalf("unparsable raw must yield nil, got %v", got)
	}
	if got := formatTokenAmount(nil, tokenMetaPlan{decimals: 18, decimalsKnown: true}); got != nil {
		t.Fatalf("nil raw must yield nil, got %v", got)
	}
}

// fmtSprint 避免测试文件再引入 fmt（保持断言只依赖被测量的值）。
func fmtSprint(v any) string {
	if v == nil {
		return "<nil>"
	}
	if s, ok := v.(string); ok {
		return s
	}
	return legacyFormatAny(v)
}

func legacyFormatAny(v any) string {
	switch x := v.(type) {
	case string:
		return x
	default:
		return ""
	}
}
