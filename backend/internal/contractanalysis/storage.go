package contractanalysis

import (
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

var integerType = regexp.MustCompile(`^(u?int)([0-9]*)$`)

func DecodePacked(word []byte, offset int, typ string) (string, error) {
	if len(word) != 32 || offset < 0 || offset >= 32 {
		return "", fmt.Errorf("invalid storage word")
	}
	bits := 0
	signed := false
	if typ == "bool" {
		bits = 8
	} else if typ == "address" || strings.HasPrefix(typ, "address ") || strings.HasPrefix(typ, "contract ") {
		bits = 160
	} else if m := integerType.FindStringSubmatch(typ); m != nil {
		bits = 256
		if m[2] != "" {
			bits, _ = strconv.Atoi(m[2])
		}
		signed = m[1] == "int"
	} else {
		return "", fmt.Errorf("unsupported packed type")
	}
	if bits < 8 || bits > 256 || bits%8 != 0 || offset+bits/8 > 32 {
		return "", fmt.Errorf("invalid packed width")
	}
	n := new(big.Int).Rsh(new(big.Int).SetBytes(word), uint(offset*8))
	mod := new(big.Int).Lsh(big.NewInt(1), uint(bits))
	n.And(n, new(big.Int).Sub(new(big.Int).Set(mod), big.NewInt(1)))
	if typ == "bool" {
		if n.Sign() == 0 {
			return "false", nil
		}
		if n.Cmp(big.NewInt(1)) == 0 {
			return "true", nil
		}
		return "", fmt.Errorf("noncanonical stored bool")
	}
	if bits == 160 && (typ == "address" || strings.HasPrefix(typ, "address ") || strings.HasPrefix(typ, "contract ")) {
		return fmt.Sprintf("0x%040x", n), nil
	}
	if signed && n.Bit(bits-1) == 1 {
		n.Sub(n, mod)
	}
	return n.String(), nil
}
