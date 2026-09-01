package method

import (
	"encoding/hex"
	"strings"

	"golang.org/x/crypto/sha3"
)

// Keccak256 EVM 使用的 Keccak-256（非 FIPS SHA3）。
func Keccak256(data []byte) []byte {
	h := sha3.NewLegacyKeccak256()
	h.Write(data)
	return h.Sum(nil)
}

// SelectorOf 由函数签名计算 4 字节 Selector：keccak256(signature)[:4]。
func SelectorOf(signature string) string {
	sig := strings.TrimSpace(signature)
	if sig == "" {
		return ""
	}
	return "0x" + hex.EncodeToString(Keccak256([]byte(sig))[:4])
}

// Topic0Of 由事件签名计算 topic0：keccak256(EventSignature)（§29）。
func Topic0Of(signature string) string {
	sig := strings.TrimSpace(signature)
	if sig == "" {
		return ""
	}
	return "0x" + hex.EncodeToString(Keccak256([]byte(sig)))
}

// NameOf 从 "transfer(address,uint256)" 提取 "transfer"。
func NameOf(signature string) string {
	if i := strings.IndexByte(signature, '('); i > 0 {
		return signature[:i]
	}
	return signature
}
