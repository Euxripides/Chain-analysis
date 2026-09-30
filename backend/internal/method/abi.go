package method

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// ---- ABI 解析（§21）----
//
// 只做本地解析：由 ABI 得到 selector → method_name / method_signature，并解码参数。
// 数值安全（§41）：uint256/int256 一律输出十进制字符串，绝不经过 float64。

type rawComponent struct {
	Name       string         `json:"name"`
	Type       string         `json:"type"`
	Indexed    bool           `json:"indexed"`
	Components []rawComponent `json:"components"`
}

type rawEntry struct {
	Type            string         `json:"type"`
	Name            string         `json:"name"`
	Inputs          []rawComponent `json:"inputs"`
	Outputs         []rawComponent `json:"outputs"`
	StateMutability string         `json:"stateMutability"`
	Anonymous       bool           `json:"anonymous"`
}

// ABIField 一个具名参数。
type ABIField struct {
	Name string
	Type *ABIType
}

// ABIMethod ABI 中的一个函数。
type ABIMethod struct {
	Name            string
	Signature       string
	Selector        string
	Inputs          []ABIField
	Outputs         []ABIField
	StateMutability string
}

// ABIEvent ABI 中的一个事件（§29 预留）。
type ABIEvent struct {
	Name      string
	Signature string
	Topic0    string
	Inputs    []ABIField
	Indexed   []bool
	Anonymous bool
}

// ABI 已解析的合约 ABI。一个 selector 理论上可能对应多个函数条目（异常 ABI），
// 因此保留全部条目，Lookup 只在唯一时返回（§10：禁止随便挑一个）。
type ABI struct {
	Methods         map[string][]*ABIMethod
	Events          map[string]*ABIEvent
	AnonymousEvents []*ABIEvent
}

const (
	MaxABIJSONBytes = 4 << 20
	maxABIEntries   = 10000
	maxABIFields    = 1024
	maxABIDepth     = 32
	maxABIArrayLen  = 100000
)

// ParseABI 解析 ABI JSON。兼容三种形态：
//   - 顶层数组 [...]
//   - {"abi": [...]}
//   - 被再次 JSON 字符串化的 ABI（部分浏览器/Provider 会这样返回）
func ParseABI(raw []byte) (*ABI, error) {
	return parseABI(raw, 0)
}

func parseABI(raw []byte, wrapperDepth int) (*ABI, error) {
	if len(raw) > MaxABIJSONBytes {
		return nil, fmt.Errorf("abi exceeds %d byte limit", MaxABIJSONBytes)
	}
	if wrapperDepth > 2 {
		return nil, fmt.Errorf("abi wrapper nesting exceeds limit")
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return nil, fmt.Errorf("empty abi")
	}
	if trimmed[0] == '"' {
		var inner string
		if err := json.Unmarshal([]byte(trimmed), &inner); err != nil {
			return nil, fmt.Errorf("abi string: %w", err)
		}
		return parseABI([]byte(inner), wrapperDepth+1)
	}
	var entries []rawEntry
	if trimmed[0] == '{' {
		var wrapper struct {
			ABI json.RawMessage `json:"abi"`
		}
		if err := json.Unmarshal([]byte(trimmed), &wrapper); err != nil {
			return nil, fmt.Errorf("abi object: %w", err)
		}
		if len(wrapper.ABI) == 0 {
			return nil, fmt.Errorf("abi object missing \"abi\" key")
		}
		return parseABI(wrapper.ABI, wrapperDepth+1)
	}
	if err := json.Unmarshal([]byte(trimmed), &entries); err != nil {
		return nil, fmt.Errorf("abi array: %w", err)
	}
	if len(entries) > maxABIEntries {
		return nil, fmt.Errorf("abi has %d entries, limit is %d", len(entries), maxABIEntries)
	}
	out := &ABI{Methods: map[string][]*ABIMethod{}, Events: map[string]*ABIEvent{}}
	for _, entry := range entries {
		switch entry.Type {
		case "function", "":
			if entry.Name == "" {
				continue
			}
			fields, err := parseFieldsDepth(entry.Inputs, 0)
			if err != nil {
				continue // 单条不可解析不应让整个 ABI 失败
			}
			outputs, err := parseFieldsDepth(entry.Outputs, 0)
			if err != nil {
				continue
			}
			signature := entry.Name + "(" + joinCanonical(fields) + ")"
			selector := SelectorOf(signature)
			out.Methods[selector] = append(out.Methods[selector], &ABIMethod{
				Name: entry.Name, Signature: signature, Selector: selector, Inputs: fields, Outputs: outputs, StateMutability: entry.StateMutability,
			})
		case "event":
			if entry.Name == "" {
				continue
			}
			fields, err := parseFieldsDepth(entry.Inputs, 0)
			if err != nil {
				continue
			}
			indexed := make([]bool, len(entry.Inputs))
			for i, in := range entry.Inputs {
				indexed[i] = in.Indexed
			}
			signature := entry.Name + "(" + joinCanonical(fields) + ")"
			topic0 := Topic0Of(signature)
			event := &ABIEvent{Name: entry.Name, Signature: signature, Topic0: topic0,
				Inputs: fields, Indexed: indexed, Anonymous: entry.Anonymous}
			if entry.Anonymous {
				out.AnonymousEvents = append(out.AnonymousEvents, event)
			} else {
				out.Events[topic0] = event
			}
		}
	}
	if len(out.Methods) == 0 && len(out.Events) == 0 && len(out.AnonymousEvents) == 0 {
		return nil, fmt.Errorf("abi has no functions or events")
	}
	return out, nil
}

// Lookup 按 selector 精确匹配。存在碰撞条目时返回 false，交由上层保持 Unresolved。
func (a *ABI) Lookup(selector string) (*ABIMethod, bool) {
	if a == nil {
		return nil, false
	}
	list := a.Methods[strings.ToLower(selector)]
	if len(list) != 1 {
		return nil, false
	}
	return list[0], true
}

// Event 按 topic0 精确匹配（§29）。
func (a *ABI) Event(topic0 string) (*ABIEvent, bool) {
	if a == nil {
		return nil, false
	}
	e, ok := a.Events[strings.ToLower(topic0)]
	return e, ok
}

func parseFields(components []rawComponent) ([]ABIField, error) {
	return parseFieldsDepth(components, 0)
}

func parseFieldsDepth(components []rawComponent, depth int) ([]ABIField, error) {
	if depth > maxABIDepth {
		return nil, fmt.Errorf("abi nesting exceeds %d", maxABIDepth)
	}
	if len(components) > maxABIFields {
		return nil, fmt.Errorf("abi tuple has %d fields, limit is %d", len(components), maxABIFields)
	}
	out := make([]ABIField, 0, len(components))
	for i, c := range components {
		t, err := parseABITypeDepth(c.Type, c.Components, depth+1)
		if err != nil {
			return nil, err
		}
		if _, _, err := abiTypeComplexity(t, depth+1); err != nil {
			return nil, err
		}
		name := c.Name
		if name == "" {
			name = "arg" + strconv.Itoa(i)
		}
		out = append(out, ABIField{Name: name, Type: t})
	}
	return out, nil
}

func joinCanonical(fields []ABIField) string {
	parts := make([]string, len(fields))
	for i, f := range fields {
		parts[i] = f.Type.Canonical
	}
	return strings.Join(parts, ",")
}

// ---- ABI 类型系统 ----

const (
	kindUint       = "uint"
	kindInt        = "int"
	kindAddress    = "address"
	kindBool       = "bool"
	kindString     = "string"
	kindBytes      = "bytes"
	kindFixedBytes = "fixedbytes"
	kindArray      = "array"
	kindTuple      = "tuple"
)

// ABIType 一个已解析的 ABI 类型。
type ABIType struct {
	Kind      string
	Bits      int // uint/int 位宽
	ByteLen   int // bytesN 长度
	ArrayLen  int // -1 表示动态数组
	Elem      *ABIType
	Fields    []ABIField
	Canonical string
}

// ParseABIType 解析类型字符串（支持 tuple / 嵌套数组）。
func ParseABIType(raw string, components []rawComponent) (*ABIType, error) {
	t, err := parseABITypeDepth(raw, components, 0)
	if err != nil {
		return nil, err
	}
	if _, _, err := abiTypeComplexity(t, 0); err != nil {
		return nil, err
	}
	return t, nil
}

func parseABITypeDepth(raw string, components []rawComponent, depth int) (*ABIType, error) {
	if depth > maxABIDepth {
		return nil, fmt.Errorf("abi type nesting exceeds %d", maxABIDepth)
	}
	name := strings.TrimSpace(raw)
	if name == "" {
		return nil, fmt.Errorf("empty abi type")
	}
	if strings.HasSuffix(name, "]") {
		open := strings.LastIndexByte(name, '[')
		if open < 0 {
			return nil, fmt.Errorf("bad array type %q", raw)
		}
		lengthPart := name[open+1 : len(name)-1]
		elem, err := parseABITypeDepth(name[:open], components, depth+1)
		if err != nil {
			return nil, err
		}
		arrayLen := -1
		if lengthPart != "" {
			n, err := strconv.Atoi(lengthPart)
			if err != nil || n < 0 || n > maxABIArrayLen {
				return nil, fmt.Errorf("bad array length in %q", raw)
			}
			arrayLen = n
		}
		return &ABIType{Kind: kindArray, ArrayLen: arrayLen, Elem: elem,
			Canonical: elem.Canonical + "[" + lengthPart + "]"}, nil
	}
	switch {
	case name == "tuple":
		fields, err := parseFieldsDepth(components, depth+1)
		if err != nil {
			return nil, err
		}
		return &ABIType{Kind: kindTuple, Fields: fields, Canonical: "(" + joinCanonical(fields) + ")"}, nil
	case name == "address":
		return &ABIType{Kind: kindAddress, Canonical: "address"}, nil
	case name == "bool":
		return &ABIType{Kind: kindBool, Canonical: "bool"}, nil
	case name == "string":
		return &ABIType{Kind: kindString, Canonical: "string"}, nil
	case name == "bytes":
		return &ABIType{Kind: kindBytes, Canonical: "bytes"}, nil
	case name == "function":
		return &ABIType{Kind: kindFixedBytes, ByteLen: 24, Canonical: "function"}, nil
	case strings.HasPrefix(name, "bytes"):
		n, err := strconv.Atoi(name[len("bytes"):])
		if err != nil || n < 1 || n > 32 {
			return nil, fmt.Errorf("bad fixed bytes type %q", raw)
		}
		return &ABIType{Kind: kindFixedBytes, ByteLen: n, Canonical: name}, nil
	case name == "uint" || name == "int":
		// 规范签名里 uint/int 必须展开为 uint256/int256
		kind := kindUint
		if name == "int" {
			kind = kindInt
		}
		return &ABIType{Kind: kind, Bits: 256, Canonical: name + "256"}, nil
	case strings.HasPrefix(name, "uint"), strings.HasPrefix(name, "int"):
		kind := kindUint
		digits := name[len("uint"):]
		if strings.HasPrefix(name, "int") {
			kind = kindInt
			digits = name[len("int"):]
		}
		bits, err := strconv.Atoi(digits)
		if err != nil || bits <= 0 || bits > 256 || bits%8 != 0 {
			return nil, fmt.Errorf("bad integer type %q", raw)
		}
		return &ABIType{Kind: kind, Bits: bits, Canonical: name}, nil
	default:
		return nil, fmt.Errorf("unsupported abi type %q", raw)
	}
}

// abiTypeComplexity 在解码前限制类型树节点与静态 head 大小，防止固定数组
// 乘法溢出或微小 ABI 构造超大内存需求。
func abiTypeComplexity(t *ABIType, depth int) (nodes, staticWords int, err error) {
	if t == nil || depth > maxABIDepth {
		return 0, 0, fmt.Errorf("abi type nesting exceeds %d", maxABIDepth)
	}
	nodes = 1
	if t.Dynamic() {
		staticWords = 1
	} else {
		switch t.Kind {
		case kindArray:
			childNodes, childWords, childErr := abiTypeComplexity(t.Elem, depth+1)
			if childErr != nil {
				return 0, 0, childErr
			}
			if t.ArrayLen > 0 && (childWords > maxDecodeBytes/32/t.ArrayLen || childNodes > maxElements/t.ArrayLen) {
				return 0, 0, fmt.Errorf("abi static array complexity exceeds decode budget")
			}
			nodes += childNodes * t.ArrayLen
			staticWords = childWords * t.ArrayLen
		case kindTuple:
			for _, field := range t.Fields {
				childNodes, childWords, childErr := abiTypeComplexity(field.Type, depth+1)
				if childErr != nil {
					return 0, 0, childErr
				}
				if nodes > maxElements-childNodes || staticWords > maxDecodeBytes/32-childWords {
					return 0, 0, fmt.Errorf("abi tuple complexity exceeds decode budget")
				}
				nodes += childNodes
				staticWords += childWords
			}
		default:
			staticWords = 1
		}
	}
	if nodes > maxElements || staticWords > maxDecodeBytes/32 {
		return 0, 0, fmt.Errorf("abi type complexity exceeds decode budget")
	}
	return nodes, staticWords, nil
}

// Dynamic 是否为动态类型（决定 head/tail 编码布局）。
func (t *ABIType) Dynamic() bool {
	switch t.Kind {
	case kindString, kindBytes:
		return true
	case kindArray:
		if t.ArrayLen < 0 {
			return true
		}
		return t.Elem.Dynamic()
	case kindTuple:
		for _, f := range t.Fields {
			if f.Type.Dynamic() {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// HeadWords 静态类型在 head 区占用的 32 字节字数。
func (t *ABIType) HeadWords() int {
	if t.Dynamic() {
		return 1
	}
	switch t.Kind {
	case kindArray:
		return t.ArrayLen * t.Elem.HeadWords()
	case kindTuple:
		total := 0
		for _, f := range t.Fields {
			total += f.Type.HeadWords()
		}
		return total
	default:
		return 1
	}
}
