// Package addressnorm 提供按链地址规范化的唯一入口。
//
// 背景：历史上各调用方只对 tron 例外、其余链一律 strings.ToLower，而 Solana 的
// base58 地址**大小写敏感**（见 internal/solana/address.go）：折叠后既得到另一个
// （无效的）地址，也让"写入侧保留大小写、查询侧折叠"的链路互相查不到数据。
// 因此凡是要按链处理地址的地方（HTTP 入参校验、ClickHouse 查询键、
// workspace_addresses.normalized_address、导出匹配、图遍历去重键）都必须经过本包。
//
// 本包刻意只依赖 internal/solana 与 internal/tron：dataset、store、api 等包会
// import 本包，反向依赖会形成循环。
package addressnorm

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/solana"
	"github.com/blockchain-platform/blockchain-platform/internal/tron"
)

var (
	// ErrEmptyAddress 空地址（含只有空白）。
	ErrEmptyAddress = errors.New("address is empty")
	// ErrInvalidAddress 地址不符合所属链的格式。
	ErrInvalidAddress = errors.New("invalid address")
)

// evmAddrRe 与 dataset 的历史校验正则等价（先折叠大小写再匹配），
// 因此 "0X..." 前缀与混合大小写 hex 依旧被接受，EVM 行为不变。
var evmAddrRe = regexp.MustCompile(`^0x[0-9a-f]{40}$`)

// IsSolana 判定链是否为 solana（大小写不敏感、忽略首尾空白）。
func IsSolana(chain string) bool {
	return strings.EqualFold(strings.TrimSpace(chain), "solana")
}

// IsTron 判定链是否为 tron（大小写不敏感、忽略首尾空白）。
func IsTron(chain string) bool {
	return strings.EqualFold(strings.TrimSpace(chain), "tron")
}

// IsEVM 判定链是否按 EVM 规则处理地址：既不是 solana 也不是 tron 的链
// （含空链，与历史 else 分支一致）。
func IsEVM(chain string) bool {
	return !IsSolana(chain) && !IsTron(chain)
}

// Normalize 按链规范化地址：solana 保留 base58 大小写，tron 保留 Base58Check
// 规范形式，其余 EVM 链折叠为小写 0x+40hex。非法地址返回错误，调用方据此返回 4xx。
func Normalize(chain, address string) (string, error) {
	value := strings.TrimSpace(address)
	if value == "" {
		return "", ErrEmptyAddress
	}
	switch {
	case IsSolana(chain):
		normalized, err := solana.NormalizeSolanaAddress(value)
		if err != nil {
			return "", fmt.Errorf("%w: not a Solana base58 address (32 bytes)", ErrInvalidAddress)
		}
		return normalized, nil
	case IsTron(chain):
		normalized, err := tron.NormalizeTronAddress(value)
		if err != nil {
			return "", fmt.Errorf("%w: not a TRON Base58Check address", ErrInvalidAddress)
		}
		return normalized, nil
	default:
		lowered := strings.ToLower(value)
		if !evmAddrRe.MatchString(lowered) {
			return "", fmt.Errorf("%w: not an EVM 0x address", ErrInvalidAddress)
		}
		return lowered, nil
	}
}

// NormalizeKey 是查询/存储键的**宽容**归一化：合法地址的返回值与 Normalize 完全一致，
// 非法地址不报错（这些路径历史上就不做校验，且没有 error 通道可回传 4xx）。
//
// 关键约束：绝不折叠 Solana 大小写——非法输入也原样返回而不是小写，
// 否则"查询键"又会变成另一个地址。
func NormalizeKey(chain, address string) string {
	value := strings.TrimSpace(address)
	if value == "" {
		return ""
	}
	switch {
	case IsSolana(chain):
		if normalized, err := solana.NormalizeSolanaAddress(value); err == nil {
			return normalized
		}
		return value
	case IsTron(chain):
		if normalized, err := tron.NormalizeTronAddress(value); err == nil {
			return normalized
		}
		return value
	default:
		return strings.ToLower(value)
	}
}

// NormalizeList 去空白、按链校验并去重（保持首次出现顺序）。
// 任一地址非法即返回错误：HTTP 入参路径应据此拒绝整个请求，而不是静默丢地址。
func NormalizeList(chain string, addresses []string) ([]string, error) {
	out := make([]string, 0, len(addresses))
	seen := make(map[string]bool, len(addresses))
	for _, address := range addresses {
		normalized, err := Normalize(chain, address)
		if err != nil {
			return nil, err
		}
		if seen[normalized] {
			continue
		}
		seen[normalized] = true
		out = append(out, normalized)
	}
	return out, nil
}
