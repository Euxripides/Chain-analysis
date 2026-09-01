package chain

import "golang.org/x/crypto/sha3"

// keccak256 EIP-55 校验和需要 Keccak-256（非 FIPS SHA3）。
func keccak256(data []byte) []byte {
	h := sha3.NewLegacyKeccak256()
	h.Write(data)
	return h.Sum(nil)
}
