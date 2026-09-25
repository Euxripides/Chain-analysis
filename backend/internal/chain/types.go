// Package chain：链身份、链策略与 EVM 基础类型（V1.5 §22-§24/§153-§156）。
//
// 硬约束：
//   - Chain Registry 是唯一 Chain Policy 来源（§62-28/§247-28）；
//   - 业务层不得散落 switch chain（§247-27）；
//   - 所有链上数值使用 big.Int / string，禁止 float64（§155）；
//   - 地址库内统一小写，展示用 EIP-55 校验和（§156）。
package chain

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// Address EVM 地址，内部规范形式为小写 0x + 40 hex。
type Address string

// ParseAddress 校验并规范化地址。
func ParseAddress(value string) (Address, error) {
	s := strings.TrimSpace(value)
	if !strings.HasPrefix(s, "0x") && !strings.HasPrefix(s, "0X") {
		return "", fmt.Errorf("address must start with 0x: %q", redactShort(value))
	}
	body := s[2:]
	if len(body) != 40 {
		return "", fmt.Errorf("address must be 20 bytes")
	}
	if !isHex(body) {
		return "", fmt.Errorf("address is not valid hex")
	}
	return Address("0x" + strings.ToLower(body)), nil
}

// MustAddress 仅用于常量/测试。
func MustAddress(value string) Address {
	a, err := ParseAddress(value)
	if err != nil {
		panic(err)
	}
	return a
}

func (a Address) String() string { return string(a) }

// Checksum EIP-55 校验和展示形式（§156：UI 使用 checksum，DB key 使用小写）。
func (a Address) Checksum() string {
	body := strings.TrimPrefix(string(a), "0x")
	if len(body) != 40 {
		return string(a)
	}
	digest := keccak256([]byte(body))
	out := make([]byte, 0, 42)
	out = append(out, '0', 'x')
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c >= 'a' && c <= 'f' {
			// 每个 hex 位对应 digest 的一个 nibble
			nibble := digest[i/2]
			if i%2 == 0 {
				nibble >>= 4
			} else {
				nibble &= 0x0f
			}
			if nibble >= 8 {
				c = c - 'a' + 'A'
			}
		}
		out = append(out, c)
	}
	return string(out)
}

// Hash 32 字节交易/区块哈希。
type Hash string

func ParseHash(value string) (Hash, error) {
	s := strings.TrimSpace(value)
	if !strings.HasPrefix(s, "0x") && !strings.HasPrefix(s, "0X") {
		return "", fmt.Errorf("hash must start with 0x")
	}
	body := s[2:]
	if len(body) != 64 {
		// 报实际长度而不是笼统的「不是 32 字节」：用户端提示直接取这条 Message，
		// 少一位/多一位时要能一眼看出来（真实事故：粘贴到 62 位 hex 一直报错）。
		return "", fmt.Errorf("hash must be 64 hex characters (32 bytes), got %d", len(body))
	}
	if !isHex(body) {
		return "", fmt.Errorf("hash is not valid hex")
	}
	return Hash("0x" + strings.ToLower(body)), nil
}

func (h Hash) String() string { return string(h) }

// ---- BlockRef（§153）----

// 区块标签。具体链支持哪些标签由 Chain Registry 决定。
const (
	TagLatest    = "latest"
	TagSafe      = "safe"
	TagFinalized = "finalized"
	TagPending   = "pending"
	TagEarliest  = "earliest"
)

// BlockRef 统一区块引用。
type BlockRef struct {
	Tag    string  `json:"tag,omitempty"`
	Number *uint64 `json:"number,omitempty"`
}

func Latest() BlockRef            { return BlockRef{Tag: TagLatest} }
func Finalized() BlockRef         { return BlockRef{Tag: TagFinalized} }
func AtBlock(n uint64) BlockRef   { return BlockRef{Number: &n} }
func (b BlockRef) IsZero() bool   { return b.Tag == "" && b.Number == nil }
func (b BlockRef) IsFixed() bool  { return b.Number != nil }
func (b BlockRef) IsLatest() bool { return b.Number == nil && (b.Tag == "" || b.Tag == TagLatest) }

// String JSON-RPC 形式："latest" 或 "0x1b4"。
func (b BlockRef) String() string {
	if b.Number != nil {
		return "0x" + strconv.FormatUint(*b.Number, 16)
	}
	if b.Tag == "" {
		return TagLatest
	}
	return b.Tag
}

// ParseBlockRef 解析用户输入（十进制、0x 十六进制或标签）。
func ParseBlockRef(value string) (BlockRef, error) {
	s := strings.TrimSpace(strings.ToLower(value))
	switch s {
	case "", TagLatest:
		return Latest(), nil
	case TagSafe, TagFinalized, TagPending, TagEarliest:
		return BlockRef{Tag: s}, nil
	}
	if strings.HasPrefix(s, "0x") {
		n, err := strconv.ParseUint(s[2:], 16, 64)
		if err != nil {
			return BlockRef{}, fmt.Errorf("bad block ref %q", redactShort(value))
		}
		return AtBlock(n), nil
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return BlockRef{}, fmt.Errorf("bad block ref %q", redactShort(value))
	}
	return AtBlock(n), nil
}

// ---- Hex helpers（§154：Gateway 之上不再重复 parse hex）----

func HexToUint64(value string) (uint64, error) {
	s := strings.TrimSpace(value)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	if s == "" {
		return 0, fmt.Errorf("empty hex number")
	}
	return strconv.ParseUint(s, 16, 64)
}

func Uint64ToHex(n uint64) string { return "0x" + strconv.FormatUint(n, 16) }

// HexToBigInt 解析任意长度十六进制为 big.Int（wei / uint256 禁止走 float64，§155）。
func HexToBigInt(value string) (*big.Int, error) {
	s := strings.TrimSpace(value)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	if s == "" {
		return big.NewInt(0), nil
	}
	n, ok := new(big.Int).SetString(s, 16)
	if !ok {
		return nil, fmt.Errorf("invalid hex integer")
	}
	return n, nil
}

func HexToBytes(value string) ([]byte, error) {
	s := strings.TrimSpace(value)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	if s == "" {
		return nil, nil
	}
	if len(s)%2 != 0 {
		s = "0" + s
	}
	return hex.DecodeString(s)
}

func BytesToHex(b []byte) string {
	if len(b) == 0 {
		return "0x"
	}
	return "0x" + hex.EncodeToString(b)
}

func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		case c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return len(s) > 0
}

// redactShort 错误信息里不回显完整用户输入（§148/§224）。
func redactShort(value string) string {
	s := strings.TrimSpace(value)
	if len(s) <= 12 {
		return s
	}
	return s[:10] + "…"
}
