package tron

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

var base58Index = func() [256]int {
	var out [256]int
	for i := range out {
		out[i] = -1
	}
	for i := range base58Alphabet {
		out[base58Alphabet[i]] = i
	}
	return out
}()

// ValidateTronAddress validates encoding and Base58Check checksum only. It does
// not query account state, so a valid but unactivated address remains valid.
func ValidateTronAddress(input string) error {
	_, err := NormalizeTronAddress(input)
	return err
}

// NormalizeTronAddress returns the canonical, case-sensitive Base58Check form.
func NormalizeTronAddress(input string) (string, error) {
	s := strings.TrimSpace(input)
	if len(s) > 128 {
		return "", errors.New("TRON address is too long")
	}
	if len(s) == 42 && strings.HasPrefix(strings.ToUpper(s), "41") {
		return TronHexToBase58(s)
	}
	if len(s) != 34 || !strings.HasPrefix(s, "T") {
		return "", errors.New("TRON address must be T... Base58Check or 41... hex")
	}
	payload, err := decodeBase58(s)
	if err != nil {
		return "", err
	}
	if len(payload) != 25 || payload[0] != 0x41 {
		return "", errors.New("invalid TRON address payload")
	}
	want := checksum(payload[:21])
	if !equalBytes(payload[21:], want) {
		return "", errors.New("invalid TRON address checksum")
	}
	return s, nil
}

func TronBase58ToHex(input string) (string, error) {
	normalized, err := NormalizeTronAddress(input)
	if err != nil {
		return "", err
	}
	b, err := decodeBase58(normalized)
	if err != nil {
		return "", err
	}
	return strings.ToUpper(hex.EncodeToString(b[:21])), nil
}

// Base58TToTronHex41 returns the 21-byte Native REST representation.
func Base58TToTronHex41(input string) (string, error) { return TronBase58ToHex(input) }

// Base58TToEVMHex20 returns the 20-byte JSON-RPC representation.
func Base58TToEVMHex20(input string) (string, error) {
	hex41, err := TronBase58ToHex(input)
	if err != nil {
		return "", err
	}
	return "0x" + strings.ToLower(hex41[2:]), nil
}

// EVMHex20ToBase58T converts a JSON-RPC address into the canonical DB form.
func EVMHex20ToBase58T(input string) (string, error) {
	s := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(input), "0x"), "0X")
	if len(s) != 40 {
		return "", errors.New("TRON JSON-RPC address must be 20 bytes")
	}
	if _, err := hex.DecodeString(s); err != nil {
		return "", errors.New("invalid TRON JSON-RPC address")
	}
	return TronHexToBase58("41" + s)
}

func TronHexToBase58(input string) (string, error) {
	s := strings.TrimSpace(input)
	if len(s) != 42 || !strings.EqualFold(s[:2], "41") {
		return "", errors.New("TRON hex address must be 21 bytes and start with 41")
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return "", errors.New("invalid TRON hex address")
	}
	full := append(append([]byte(nil), b...), checksum(b)...)
	return encodeBase58(full), nil
}

func checksum(b []byte) []byte {
	a := sha256.Sum256(b)
	c := sha256.Sum256(a[:])
	return append([]byte(nil), c[:4]...)
}

func decodeBase58(s string) ([]byte, error) {
	n := new(big.Int)
	base := big.NewInt(58)
	for i := 0; i < len(s); i++ {
		v := -1
		if int(s[i]) < len(base58Index) {
			v = base58Index[s[i]]
		}
		if v < 0 {
			return nil, fmt.Errorf("invalid Base58 character at position %d", i)
		}
		n.Mul(n, base)
		n.Add(n, big.NewInt(int64(v)))
	}
	out := n.Bytes()
	leading := 0
	for leading < len(s) && s[leading] == '1' {
		leading++
	}
	if leading > 0 {
		out = append(make([]byte, leading), out...)
	}
	return out, nil
}

func encodeBase58(b []byte) string {
	leading := 0
	for leading < len(b) && b[leading] == 0 {
		leading++
	}
	n := new(big.Int).SetBytes(b)
	base, rem := big.NewInt(58), new(big.Int)
	var rev []byte
	for n.Sign() > 0 {
		n.QuoRem(n, base, rem)
		rev = append(rev, base58Alphabet[rem.Int64()])
	}
	for i := 0; i < leading; i++ {
		rev = append(rev, '1')
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return string(rev)
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}
