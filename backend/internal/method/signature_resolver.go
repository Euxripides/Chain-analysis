package method

import (
	"fmt"
	"strings"
)

// ---- 签名候选选择（§12/§10）----
//
// 硬约束：Selector 只有 32 bit，碰撞客观存在。
//   - 禁止 "LIMIT 1 随便选一个并标 Verified"（§62-4）；
//   - 无法判定时保留 selector、method_name 可为空（§12）；
//   - 候选全部保留，供 UI / 人工复核。

// SignatureParamTypes 解析规范签名的参数类型列表，例如
// "swap((address,uint256)[],uint256)" → [tuple[] , uint256]。
func SignatureParamTypes(signature string) ([]*ABIType, error) {
	sig := strings.TrimSpace(signature)
	open := strings.IndexByte(sig, '(')
	if open < 0 || !strings.HasSuffix(sig, ")") {
		return nil, fmt.Errorf("bad signature %q", signature)
	}
	return parseCanonicalTypeList(sig[open+1 : len(sig)-1])
}

func parseCanonicalTypeList(body string) ([]*ABIType, error) {
	parts := splitTopLevel(body)
	out := make([]*ABIType, 0, len(parts))
	for _, part := range parts {
		t, err := parseCanonicalType(strings.TrimSpace(part))
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

func parseCanonicalType(raw string) (*ABIType, error) {
	if raw == "" {
		return nil, fmt.Errorf("empty type")
	}
	if strings.HasSuffix(raw, "]") {
		open := strings.LastIndexByte(raw, '[')
		if open < 0 {
			return nil, fmt.Errorf("bad array type %q", raw)
		}
		lengthPart := raw[open+1 : len(raw)-1]
		elem, err := parseCanonicalType(raw[:open])
		if err != nil {
			return nil, err
		}
		arrayLen := -1
		if lengthPart != "" {
			n := 0
			if _, err := fmt.Sscanf(lengthPart, "%d", &n); err != nil || n < 0 || n > 1<<20 {
				return nil, fmt.Errorf("bad array length %q", raw)
			}
			arrayLen = n
		}
		return &ABIType{Kind: kindArray, ArrayLen: arrayLen, Elem: elem,
			Canonical: elem.Canonical + "[" + lengthPart + "]"}, nil
	}
	if strings.HasPrefix(raw, "(") && strings.HasSuffix(raw, ")") {
		inner, err := parseCanonicalTypeList(raw[1 : len(raw)-1])
		if err != nil {
			return nil, err
		}
		fields := make([]ABIField, len(inner))
		for i, t := range inner {
			fields[i] = ABIField{Name: fmt.Sprintf("arg%d", i), Type: t}
		}
		return &ABIType{Kind: kindTuple, Fields: fields, Canonical: "(" + joinCanonical(fields) + ")"}, nil
	}
	return ParseABIType(raw, nil)
}

// splitTopLevel 按顶层逗号切分（忽略括号/方括号内部）。
func splitTopLevel(body string) []string {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil
	}
	var parts []string
	depth := 0
	start := 0
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '(', '[':
			depth++
		case ')', ']':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, body[start:i])
				start = i + 1
			}
		}
	}
	return append(parts, body[start:])
}

// EncodedArgSize 返回参数区最小编码长度与是否定长。
func EncodedArgSize(types []*ABIType) (minBytes int, exact bool) {
	exact = true
	for _, t := range types {
		minBytes += 32*t.HeadWords() + tailMinBytes(t)
		if t.Dynamic() {
			exact = false
		}
	}
	return minBytes, exact
}

func tailMinBytes(t *ABIType) int {
	if !t.Dynamic() {
		return 0
	}
	switch t.Kind {
	case kindString, kindBytes:
		return 32 // 长度字 + 空 payload
	case kindArray:
		if t.ArrayLen < 0 {
			return 32 // 长度字 + 0 个元素
		}
		total := 0
		for i := 0; i < t.ArrayLen; i++ {
			total += 32*t.Elem.HeadWords() + tailMinBytes(t.Elem)
		}
		return total
	case kindTuple:
		total := 0
		for _, f := range t.Fields {
			total += 32*f.Type.HeadWords() + tailMinBytes(f.Type)
		}
		return total
	default:
		return 0
	}
}

// InputCompatible 判断 input 参数区长度是否与签名兼容（§12：候选 >1 时的判据）。
// 无法解析签名或长度未知时返回 false（宁可保持 Unresolved，也不猜）。
func InputCompatible(signature string, argBytes int) bool {
	if argBytes < 0 {
		return false
	}
	types, err := SignatureParamTypes(signature)
	if err != nil {
		return false
	}
	// ABI 编码总是 32 字节对齐
	if argBytes%32 != 0 {
		return false
	}
	minBytes, exact := EncodedArgSize(types)
	if exact {
		return argBytes == minBytes
	}
	return argBytes >= minBytes
}

// ChooseCandidate 在候选集合中选择最终结果（§12/§30）。
// argBytes < 0 表示 input 参数长度不可用（此时不做兼容性判定）。
func ChooseCandidate(selector string, argBytes int, candidates []MethodCandidate) MethodResolution {
	if len(candidates) == 0 {
		return unresolved(selector, TypeContractCall, nil)
	}
	sorted := make([]MethodCandidate, len(candidates))
	copy(sorted, candidates)
	for i := range sorted {
		sorted[i].Verified = verifiedFor(sorted[i].Source, sorted[i].Verified)
	}
	sortCandidates(sorted)

	// manual_override 永远优先，且是唯一允许由人工标记为 Verified 的签名来源（§30）
	for _, c := range sorted {
		if c.Source == SourceManualOverride {
			return resolutionFrom(selector, c, ConfidenceVerified, sorted)
		}
	}

	if len(sorted) == 1 {
		return resolutionFrom(selector, sorted[0], singleCandidateConfidence(sorted[0].Source), sorted)
	}

	// 多候选：先记录碰撞，再尝试收敛（§9/§10）
	CollisionTotal.Inc()
	tier := topSourceTier(sorted)
	if len(tier) == 1 {
		return resolutionFrom(selector, tier[0], ConfidenceMedium, sorted)
	}
	var compatible []MethodCandidate
	for _, c := range tier {
		if InputCompatible(c.Signature, argBytes) {
			compatible = append(compatible, c)
		}
	}
	if len(compatible) == 1 {
		return resolutionFrom(selector, compatible[0], ConfidenceMedium, sorted)
	}
	// 仍无法判断：保留 selector 与全部候选，method_name 留空（§12/§62-20）
	return unresolved(selector, TypeContractCall, sorted)
}

// topSourceTier 取优先级最高的来源层（例如本地签名优先于公共签名库，§31）。
func topSourceTier(sorted []MethodCandidate) []MethodCandidate {
	best := SourceRank(sorted[0].Source)
	var out []MethodCandidate
	for _, c := range sorted {
		if SourceRank(c.Source) == best {
			out = append(out, c)
		}
	}
	return out
}

func singleCandidateConfidence(source string) string {
	switch source {
	case SourceLocalSignature:
		return ConfidenceHigh // §9：本地签名唯一候选 → high
	case SourcePublicSignature:
		return ConfidenceMedium // §31：公共库不能作为唯一可信来源
	default:
		return ConfidenceMedium
	}
}

func resolutionFrom(selector string, c MethodCandidate, confidence string, candidates []MethodCandidate) MethodResolution {
	return MethodResolution{
		MethodID:   selector,
		Name:       c.Name,
		Signature:  c.Signature,
		Source:     c.Source,
		Verified:   verifiedFor(c.Source, c.Verified),
		Confidence: confidence,
		MethodType: TypeContractCall,
		Candidates: candidates,
	}
}

// verifiedFor 只有 Contract ABI / 人工覆盖 / Provider 深度解码可标记 Verified。
// 签名库匹配（本地或公共）永远是 "Signature Match, Not Contract Verified"（§27/§31）。
func verifiedFor(source string, sourceVerified bool) bool {
	switch source {
	case SourceContractABI, SourceManualOverride:
		return true
	case SourceAnkr:
		return sourceVerified
	default:
		return false
	}
}

// CandidateVerified 统一 API / UI 候选的 verified 语义：它表示合约级验证，
// 不是“签名与 selector 自洽”。
func CandidateVerified(source string, sourceVerified bool) bool {
	return verifiedFor(source, sourceVerified)
}
