package method

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
)

// EncodeInput complements the existing decoder. Numeric values never pass
// through float64; nested tuples/arrays use standard relative head/tail offsets.
func EncodeInput(m *ABIMethod, args []any) (string, error) {
	if m == nil || len(args) != len(m.Inputs) {
		return "", fmt.Errorf("argument count does not match ABI")
	}
	data, err := encodeSequence(fieldTypes(m.Inputs), args, 0)
	if err != nil {
		return "", err
	}
	if len(data) > maxDecodeBytes {
		return "", fmt.Errorf("encoded call exceeds limit")
	}
	return m.Selector + hex.EncodeToString(data), nil
}

func DecodeOutput(m *ABIMethod, output string) ([]DecodedParam, error) {
	if m == nil {
		return nil, fmt.Errorf("nil ABI method")
	}
	return DecodeInput(&ABIMethod{Selector: m.Selector, Inputs: m.Outputs}, m.Selector+strings.TrimPrefix(output, "0x"))
}

func abiWord(n *big.Int) []byte { word := make([]byte, 32); n.FillBytes(word); return word }

func encodeSequence(types []*ABIType, values []any, depth int) ([]byte, error) {
	if depth > maxDecodeDepth || len(types) != len(values) || len(values) > maxElements {
		return nil, fmt.Errorf("ABI encoding complexity exceeded")
	}
	headSize := 0
	for _, t := range types {
		headSize += t.HeadWords() * 32
		if headSize > maxDecodeBytes {
			return nil, fmt.Errorf("ABI encoding exceeds limit")
		}
	}
	head := make([]byte, 0, headSize)
	tail := []byte{}
	for i, t := range types {
		value, err := encodeValue(t, values[i], depth+1)
		if err != nil {
			return nil, fmt.Errorf("argument %d: %w", i, err)
		}
		if len(value)+headSize+len(tail) > maxDecodeBytes {
			return nil, fmt.Errorf("ABI encoding exceeds limit")
		}
		if t.Dynamic() {
			head = append(head, abiWord(big.NewInt(int64(headSize+len(tail))))...)
			tail = append(tail, value...)
		} else {
			head = append(head, value...)
		}
	}
	return append(head, tail...), nil
}

func encodeValue(t *ABIType, value any, depth int) ([]byte, error) {
	if depth > maxDecodeDepth {
		return nil, fmt.Errorf("ABI encoding nesting exceeded")
	}
	switch t.Kind {
	case kindUint, kindInt:
		str := ""
		switch v := value.(type) {
		case string:
			str = v
		case json.Number:
			str = v.String()
		case *big.Int:
			if v != nil {
				str = v.String()
			}
		default:
			return nil, fmt.Errorf("integer arguments must be decimal strings or exact JSON integers")
		}
		if len(str) > 80 {
			return nil, fmt.Errorf("integer argument exceeds uint256 encoding size")
		}
		base := 10
		if strings.HasPrefix(str, "0x") {
			base = 0
		}
		n, ok := new(big.Int).SetString(str, base)
		if !ok {
			return nil, fmt.Errorf("invalid integer argument")
		}
		if t.Kind == kindUint {
			if n.Sign() < 0 || n.BitLen() > t.Bits {
				return nil, fmt.Errorf("unsigned integer overflow")
			}
		} else {
			bound := new(big.Int).Lsh(big.NewInt(1), uint(t.Bits-1))
			if n.Cmp(new(big.Int).Neg(new(big.Int).Set(bound))) < 0 || n.Cmp(bound) >= 0 {
				return nil, fmt.Errorf("signed integer overflow")
			}
			if n.Sign() < 0 {
				n.Add(n, new(big.Int).Lsh(big.NewInt(1), 256))
			}
		}
		return abiWord(n), nil
	case kindAddress:
		str, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("address argument must be a string")
		}
		str = strings.TrimPrefix(strings.TrimPrefix(str, "0x"), "0X")
		raw, err := hex.DecodeString(str)
		if err != nil || len(raw) != 20 {
			return nil, fmt.Errorf("invalid address argument")
		}
		word := make([]byte, 32)
		copy(word[12:], raw)
		return word, nil
	case kindBool:
		v, ok := value.(bool)
		if !ok {
			return nil, fmt.Errorf("boolean argument must be boolean")
		}
		n := big.NewInt(0)
		if v {
			n.SetInt64(1)
		}
		return abiWord(n), nil
	case kindString, kindBytes, kindFixedBytes:
		str, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("text/bytes argument must be a string")
		}
		raw := []byte(str)
		if t.Kind != kindString {
			if !strings.HasPrefix(str, "0x") || len(str)%2 != 0 {
				return nil, fmt.Errorf("bytes argument must be even hexadecimal data")
			}
			var err error
			raw, err = hex.DecodeString(str[2:])
			if err != nil {
				return nil, err
			}
		}
		if len(raw) > maxDecodeBytes {
			return nil, fmt.Errorf("byte argument too large")
		}
		if t.Kind == kindFixedBytes {
			if len(raw) != t.ByteLen {
				return nil, fmt.Errorf("fixed bytes length mismatch")
			}
			word := make([]byte, 32)
			copy(word, raw)
			return word, nil
		}
		out := abiWord(big.NewInt(int64(len(raw))))
		out = append(out, raw...)
		for len(out)%32 != 0 {
			out = append(out, 0)
		}
		return out, nil
	case kindArray:
		list, ok := value.([]any)
		if !ok || len(list) > maxElements {
			return nil, fmt.Errorf("array argument must be a bounded array")
		}
		if t.ArrayLen >= 0 && len(list) != t.ArrayLen {
			return nil, fmt.Errorf("fixed array length mismatch")
		}
		types := make([]*ABIType, len(list))
		for i := range types {
			types[i] = t.Elem
		}
		out, err := encodeSequence(types, list, depth+1)
		if err != nil {
			return nil, err
		}
		if t.ArrayLen < 0 {
			out = append(abiWord(big.NewInt(int64(len(list)))), out...)
		}
		return out, nil
	case kindTuple:
		list, ok := value.([]any)
		if !ok {
			obj, found := value.(map[string]any)
			if !found {
				return nil, fmt.Errorf("tuple must be an array or a named object")
			}
			list = make([]any, len(t.Fields))
			for i, f := range t.Fields {
				v, found := obj[f.Name]
				if !found {
					return nil, fmt.Errorf("tuple field missing")
				}
				list[i] = v
			}
		}
		return encodeSequence(fieldTypes(t.Fields), list, depth+1)
	default:
		return nil, fmt.Errorf("unsupported ABI type")
	}
}
