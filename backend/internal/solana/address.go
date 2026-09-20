// Package solana 提供 Solana 链的地址规范化。
//
// Solana 地址是 base58btc 编码的 32 字节公钥（ed25519 账户或 PDA），
// **大小写敏感**：与 EVM 不同，任何 ToLower/ToUpper 都会得到另一个（无效的）地址。
// 因此本包是全链路唯一的 Solana 地址入口，调用方必须原样保留返回值。
package solana

import (
	"errors"
	"math/big"
	"strings"
)

// AddressBytes 是 Solana 公钥的字节长度（ed25519 公钥与 PDA 均为 32）。
const AddressBytes = 32

var (
	// ErrEmptyAddress 空地址。
	ErrEmptyAddress = errors.New("solana address is empty")
	// ErrInvalidAddress 含非 base58 字符，或解码后不是 32 字节。
	ErrInvalidAddress = errors.New("solana address must be base58 encoding of 32 bytes")
)

// base58Alphabet 是 Bitcoin/Solana 使用的 base58btc 字母表（无校验和）。
const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

var base58Index [256]int8

func init() {
	for i := range base58Index {
		base58Index[i] = -1
	}
	for i := 0; i < len(base58Alphabet); i++ {
		base58Index[base58Alphabet[i]] = int8(i)
	}
}

// DecodeBase58 解码 base58btc 字符串；前导 '1' 对应前导零字节，必须保留。
func DecodeBase58(s string) ([]byte, error) {
	if s == "" {
		return nil, ErrInvalidAddress
	}
	zeros := 0
	for zeros < len(s) && s[zeros] == '1' {
		zeros++
	}
	value := new(big.Int)
	radix := big.NewInt(58)
	for i := 0; i < len(s); i++ {
		digit := base58Index[s[i]]
		if digit < 0 {
			return nil, ErrInvalidAddress
		}
		value.Mul(value, radix)
		value.Add(value, big.NewInt(int64(digit)))
	}
	out := make([]byte, zeros, zeros+len(value.Bytes()))
	return append(out, value.Bytes()...), nil
}

// NormalizeSolanaAddress 校验地址并**原样返回**（只去掉首尾空白，绝不改变大小写）。
func NormalizeSolanaAddress(raw string) (string, error) {
	address := strings.TrimSpace(raw)
	if address == "" {
		return "", ErrEmptyAddress
	}
	decoded, err := DecodeBase58(address)
	if err != nil {
		return "", err
	}
	if len(decoded) != AddressBytes {
		return "", ErrInvalidAddress
	}
	return address, nil
}
