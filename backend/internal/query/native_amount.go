package query

import (
	"fmt"
	"math/big"
	"regexp"
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/shopspring/decimal"
)

// Historical EVM value_raw rows contain 0x values; current ingestion writes
// decimal strings. Keep comparisons in UInt256 so neither representation is
// rounded through Float64 or silently parsed as zero.
const nativeRawUIntSQL = `multiIf(match(value_raw,'^0[xX][0-9a-fA-F]{1,64}$'),
	reinterpretAsUInt256(reverse(unhex(leftPad(substring(value_raw,3),64,'0')))),
	match(value_raw,'^[0-9]+$'),toUInt256OrZero(value_raw),toUInt256(0))`

var maxNativeRaw = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
var nativeChains = chain.DefaultRegistry()
var decimalAmountPattern = regexp.MustCompile(`^\d+(\.\d+)?$`)

func nativeDecimalsForChain(chainSlug string) int {
	if config, ok := nativeChains.Get(chainSlug); ok {
		return config.Native.Decimals
	}
	return 18
}

func NativeAmountConditions(chain string, min, max string) ([]string, []any, error) {
	conditions := []string{}
	args := []any{}
	for _, b := range []struct {
		value, op string
		roundUp   bool
	}{{min, ">=", true}, {max, "<=", false}} {
		if b.value == "" {
			continue
		}
		raw, err := nativeAmountBound(b.value, nativeDecimalsForChain(chain), b.roundUp)
		if err != nil {
			return nil, nil, err
		}
		conditions = append(conditions, nativeRawUIntSQL+" "+b.op+" toUInt256(?)")
		args = append(args, raw)
	}
	return conditions, args, nil
}

func parseNativeRaw(raw string) (*big.Int, error) {
	base := 10
	if strings.HasPrefix(raw, "0x") || strings.HasPrefix(raw, "0X") {
		raw = raw[2:]
		base = 16
	}
	n, ok := new(big.Int).SetString(raw, base)
	if !ok || n.Sign() < 0 || n.Cmp(maxNativeRaw) > 0 {
		return nil, fmt.Errorf("invalid native raw value")
	}
	return n, nil
}

func nativeAmountBound(amount string, decimals int, roundUp bool) (string, error) {
	amount = strings.TrimSpace(amount)
	if !decimalAmountPattern.MatchString(amount) {
		return "", fmt.Errorf("invalid native amount")
	}
	v, ok := new(big.Rat).SetString(amount)
	if !ok || v.Sign() < 0 || decimals < 0 || decimals > 38 {
		return "", fmt.Errorf("invalid native amount")
	}
	v.Mul(v, new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)))
	q, remainder := new(big.Int).QuoRem(v.Num(), v.Denom(), new(big.Int))
	if roundUp && remainder.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	}
	if q.Cmp(maxNativeRaw) > 0 {
		return "", fmt.Errorf("native amount exceeds UInt256")
	}
	return q.String(), nil
}

func formatNativeRaw(raw string, decimals int) (string, error) {
	n, err := parseNativeRaw(raw)
	if err != nil {
		return "", err
	}
	return decimal.NewFromBigInt(n, -int32(decimals)).String(), nil
}
