package contractanalysis

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"math/big"
)

// DecodePrimitiveArray accepts the ABI encoding of one address[] or bytes4[].
// Lengths and offsets are bounded before any slice or allocation.
func DecodePrimitiveArray(raw []byte, kind string, limit int) ([]string, error) {
	if len(raw) < 64 || len(raw) > 1<<20 || len(raw)%32 != 0 {
		return nil, fmt.Errorf("invalid ABI array length")
	}
	offset := new(big.Int).SetBytes(raw[:32])
	if !offset.IsUint64() {
		return nil, fmt.Errorf("invalid ABI array offset")
	}
	start := offset.Uint64()
	if start != 32 {
		return nil, fmt.Errorf("invalid single-return ABI array offset")
	}
	n := new(big.Int).SetBytes(raw[32:64])
	if !n.IsUint64() || n.Uint64() > uint64(limit) || n.Uint64() > uint64((len(raw)-64)/32) {
		return nil, fmt.Errorf("ABI array exceeds bound")
	}
	if 64+n.Uint64()*32 != uint64(len(raw)) {
		return nil, fmt.Errorf("invalid trailing ABI data")
	}
	values := make([]string, 0, int(n.Uint64()))
	for i := 0; i < int(n.Uint64()); i++ {
		word := raw[64+i*32 : 96+i*32]
		switch kind {
		case "address":
			if !bytes.Equal(word[:12], make([]byte, 12)) {
				return nil, fmt.Errorf("noncanonical ABI address")
			}
			values = append(values, "0x"+hex.EncodeToString(word[12:]))
		case "bytes4":
			if !bytes.Equal(word[4:], make([]byte, 28)) {
				return nil, fmt.Errorf("noncanonical ABI bytes4")
			}
			values = append(values, "0x"+hex.EncodeToString(word[:4]))
		default:
			return nil, fmt.Errorf("unsupported ABI array type")
		}
	}
	return values, nil
}
