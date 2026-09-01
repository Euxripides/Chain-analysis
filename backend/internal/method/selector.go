package method

import (
	"math/big"
	"strings"
)

// ExtractMethodID 提取 Method Selector（§5）。
//
// 规则：
//   - 必须是 0x 前缀且长度 >= 10；
//   - 必须通过 Hex 校验（Provider 不保证永远返回合法 input，§5）；
//   - 统一小写。
//
// 不满足时返回空字符串，由调用方按 method_type 分类处理。
func ExtractMethodID(input string) string {
	s := strings.TrimSpace(input)
	if len(s) < 10 {
		return ""
	}
	if !strings.HasPrefix(s, "0x") && !strings.HasPrefix(s, "0X") {
		return ""
	}
	selector := strings.ToLower(s[:10])
	if !isHexDigits(selector[2:]) {
		return ""
	}
	return selector
}

// ValidSelector 校验一个外部传入的 selector 字面量（API 查询用）。
func ValidSelector(selector string) bool {
	s := strings.ToLower(strings.TrimSpace(selector))
	return len(s) == 10 && strings.HasPrefix(s, "0x") && isHexDigits(s[2:])
}

// NormalizeSelector 规范化外部输入的 selector（允许省略 0x）。
func NormalizeSelector(selector string) string {
	s := strings.ToLower(strings.TrimSpace(selector))
	if len(s) == 8 && isHexDigits(s) {
		s = "0x" + s
	}
	if ValidSelector(s) {
		return s
	}
	return ""
}

func isHexDigits(s string) bool {
	if s == "" {
		return false
	}
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
	return true
}

// emptyInput 判断 input 是否为"无调用数据"。
func emptyInput(input string) bool {
	s := strings.ToLower(strings.TrimSpace(input))
	s = strings.TrimPrefix(s, "0x")
	return s == "" || strings.Trim(s, "0") == ""
}

// positiveValue 判断交易原生转账金额是否 > 0（十进制或 0x 十六进制）。
func positiveValue(value string) bool {
	s := strings.TrimSpace(value)
	if s == "" {
		return false
	}
	base := 10
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		s = s[2:]
		base = 16
	}
	n, ok := new(big.Int).SetString(s, base)
	if !ok {
		return false
	}
	return n.Sign() > 0
}

// Classify 特殊交易分类（§38）。
//
//	to == null            → contract_creation（input 是部署字节码 + Constructor Args，禁止按 Selector 解析）
//	input == 0x, value >0 → native_transfer（必须与 ERC20 transfer() 区分）
//	input == 0x, value==0 → empty_call
//	其余                   → contract_call
//
// 返回 methodType 与合法 selector（仅 contract_call 有 selector）。
func Classify(input, value string, toIsNull bool) (methodType, methodID string) {
	if toIsNull {
		return TypeContractCreation, ""
	}
	if emptyInput(input) {
		if positiveValue(value) {
			return TypeNativeTransfer, ""
		}
		return TypeEmptyCall, ""
	}
	selector := ExtractMethodID(input)
	if selector == "" {
		// 非法 / 过短 input 既不是原生转账，也不是空调用。保持可观察的
		// invalid_input 分类，避免污染交易语义和统计结果。
		return TypeInvalidInput, ""
	}
	return TypeContractCall, selector
}

// StructuralResolution 结构化分类的解析结果（§38）。
// 这类结果来自交易结构本身而非签名匹配，Source 记为 heuristic 且 Verified=false，
// 避免与"已验证的合约 ABI 解析"混淆（§62-16/§62-20）。
func StructuralResolution(methodType string) (MethodResolution, bool) {
	switch methodType {
	case TypeContractCreation:
		return MethodResolution{Name: NameContractCreation, Source: SourceHeuristic,
			Confidence: ConfidenceHigh, MethodType: methodType}, true
	case TypeNativeTransfer:
		return MethodResolution{Name: NameNativeTransfer, Source: SourceHeuristic,
			Confidence: ConfidenceHigh, MethodType: methodType}, true
	case TypeEmptyCall:
		return MethodResolution{Name: NameEmptyCall, Source: SourceHeuristic,
			Confidence: ConfidenceHigh, MethodType: methodType}, true
	case TypeInvalidInput:
		return MethodResolution{Name: NameInvalidInput, Source: SourceHeuristic,
			Confidence: ConfidenceHigh, MethodType: methodType}, true
	}
	return MethodResolution{}, false
}

// ArgBytes 返回 input 中 selector 之后的参数字节数（用于候选兼容性判断，§12）。
// 返回 -1 表示 input 非法或不含参数区。
func ArgBytes(input string) int {
	s := strings.TrimSpace(input)
	if len(s) < 10 {
		return -1
	}
	body := s[10:]
	if len(body)%2 != 0 || !isHexDigits(body) && body != "" {
		return -1
	}
	return len(body) / 2
}
