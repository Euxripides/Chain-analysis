package chainread

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/method"
)

// ---- 最小 ABI 编解码（只读合约调用用，§73）----
//
// 复用 V1.3 的 keccak 与 ABI 解码；这里只补齐调用侧需要的静态参数编码。

// EncodeCall 由函数签名与静态参数构造 calldata。
// 支持 address / uintN / intN / bool / bytes32，足以覆盖只读探针（§80-§82）。
func EncodeCall(signature string, args ...any) ([]byte, error) {
	selector := method.SelectorOf(signature)
	if selector == "" {
		return nil, fmt.Errorf("bad signature %q", signature)
	}
	data, err := chain.HexToBytes(selector)
	if err != nil {
		return nil, err
	}
	for i, arg := range args {
		word, err := encodeWord(arg)
		if err != nil {
			return nil, fmt.Errorf("arg %d: %w", i, err)
		}
		data = append(data, word...)
	}
	return data, nil
}

func encodeWord(arg any) ([]byte, error) {
	word := make([]byte, 32)
	switch value := arg.(type) {
	case chain.Address:
		raw, err := chain.HexToBytes(string(value))
		if err != nil || len(raw) != 20 {
			return nil, fmt.Errorf("invalid address argument")
		}
		copy(word[12:], raw)
	case string:
		address, err := chain.ParseAddress(value)
		if err != nil {
			return nil, fmt.Errorf("string argument must be an address")
		}
		raw, _ := chain.HexToBytes(string(address))
		copy(word[12:], raw)
	case *big.Int:
		if value == nil {
			return word, nil
		}
		if value.Sign() < 0 {
			return nil, fmt.Errorf("negative integers are not supported")
		}
		raw := value.Bytes()
		if len(raw) > 32 {
			return nil, fmt.Errorf("integer overflows 32 bytes")
		}
		copy(word[32-len(raw):], raw)
	case uint64:
		new(big.Int).SetUint64(value).FillBytes(word)
	case int:
		if value < 0 {
			return nil, fmt.Errorf("negative integers are not supported")
		}
		new(big.Int).SetInt64(int64(value)).FillBytes(word)
	case bool:
		if value {
			word[31] = 1
		}
	case []byte:
		if len(value) > 32 {
			return nil, fmt.Errorf("bytes32 argument is too long")
		}
		copy(word, value)
	default:
		return nil, fmt.Errorf("unsupported argument type %T", arg)
	}
	return word, nil
}

// DecodeString 解码 ABI 动态 string，并兼容返回 bytes32 的非标准代币。
func DecodeString(output []byte) (string, error) {
	if len(output) < 32 {
		return "", fmt.Errorf("short abi result")
	}
	if len(output) >= 64 {
		offset := new(big.Int).SetBytes(output[:32])
		if offset.IsUint64() {
			start := int(offset.Uint64())
			if start >= 0 && start+32 <= len(output) {
				length := new(big.Int).SetBytes(output[start : start+32])
				if length.IsUint64() {
					size := int(length.Uint64())
					if size >= 0 && start+32+size <= len(output) {
						value := strings.TrimRight(string(output[start+32:start+32+size]), "\x00")
						if strings.TrimSpace(value) != "" {
							return sanitizeText(value), nil
						}
					}
				}
			}
		}
	}
	value := strings.TrimRight(string(output[:32]), "\x00")
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("empty abi string")
	}
	return sanitizeText(value), nil
}

// DecodeUint 解码 uint256（禁止 float64，§155）。
func DecodeUint(output []byte) (*big.Int, error) {
	if len(output) < 32 {
		return nil, fmt.Errorf("short abi result")
	}
	return new(big.Int).SetBytes(output[len(output)-32:]), nil
}

// DecodeAddress 解码 address。
func DecodeAddress(output []byte) (chain.Address, error) {
	if len(output) < 32 {
		return "", fmt.Errorf("short abi result")
	}
	return chain.ParseAddress("0x" + hex.EncodeToString(output[len(output)-20:]))
}

// DecodeUint8 解码 decimals 这类小整数。
func DecodeUint8(output []byte) (int, error) {
	value, err := DecodeUint(output)
	if err != nil {
		return 0, err
	}
	if !value.IsInt64() || value.Int64() < 0 || value.Int64() > 255 {
		return 0, fmt.Errorf("value outside 0..255")
	}
	return int(value.Int64()), nil
}

// sanitizeText 去掉控制字符，避免脏数据进入元数据。
func sanitizeText(value string) string {
	cleaned := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, value)
	return strings.TrimSpace(cleaned)
}
