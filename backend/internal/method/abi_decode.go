package method

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
)

// DecodedParam 单个已解码参数（§41：数值安全）。
type DecodedParam struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value any    `json:"value"`
}

// maxDecodeBytes 防御性上限：单笔 input 参数区超过 1MiB 直接拒绝解码。
const maxDecodeBytes = 1 << 20

// maxElements 单个数组/bytes 的元素上限，避免恶意长度触发大分配。
const maxElements = 100000

// 解码预算同时限制递归深度和输出节点数。仅限制 input 字节数不足以防止
// 重叠 offset / 深层 tuple 产生指数级对象或栈耗尽。
const maxDecodeDepth = 32

type decodeState struct {
	nodes int
}

func (s *decodeState) enter(depth, nodes int) error {
	if depth > maxDecodeDepth {
		return fmt.Errorf("decode nesting exceeds %d", maxDecodeDepth)
	}
	if nodes < 0 || s.nodes > maxElements-nodes {
		return fmt.Errorf("decoded value count exceeds %d", maxElements)
	}
	s.nodes += nodes
	return nil
}

// DecodeInput 按 ABI 方法解码 input 参数（§21）。
// 解码失败返回错误：宁可不展示 Decoded Input，也不能展示错误参数。
func DecodeInput(m *ABIMethod, input string) (out []DecodedParam, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			out = nil
			err = fmt.Errorf("abi decoder panic prevented: %v", recovered)
		}
	}()
	if m == nil {
		return nil, fmt.Errorf("nil abi method")
	}
	body := strings.TrimSpace(input)
	body = strings.TrimPrefix(strings.TrimPrefix(body, "0x"), "0X")
	if len(body) < 8 {
		return nil, fmt.Errorf("input too short for a method call")
	}
	if !strings.EqualFold("0x"+body[:8], m.Selector) {
		return nil, fmt.Errorf("selector mismatch: input=0x%s abi=%s", body[:8], m.Selector)
	}
	args := body[8:]
	if len(args)%2 != 0 {
		return nil, fmt.Errorf("odd length argument data")
	}
	if len(args)/2 > maxDecodeBytes {
		return nil, fmt.Errorf("argument data too large: %d bytes", len(args)/2)
	}
	data, err := hex.DecodeString(args)
	if err != nil {
		return nil, fmt.Errorf("argument data is not hex: %w", err)
	}
	values, err := decodeSequenceState(fieldTypes(m.Inputs), data, 0, &decodeState{}, 0)
	if err != nil {
		return nil, err
	}
	out = make([]DecodedParam, len(m.Inputs))
	for i, f := range m.Inputs {
		out[i] = DecodedParam{Name: f.Name, Type: f.Type.Canonical, Value: values[i]}
	}
	return out, nil
}

// DecodeEventLog 解码事件（§29）：indexed 参数取自 topics，非 indexed 取自 data。
func DecodeEventLog(e *ABIEvent, topics []string, data string) (out []DecodedParam, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			out = nil
			err = fmt.Errorf("event decoder panic prevented: %v", recovered)
		}
	}()
	if e == nil {
		return nil, fmt.Errorf("nil abi event")
	}
	body := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(data), "0x"), "0X")
	if len(body)%2 != 0 {
		return nil, fmt.Errorf("odd length event data")
	}
	if len(body)/2 > maxDecodeBytes {
		return nil, fmt.Errorf("event data too large")
	}
	raw, err := hex.DecodeString(body)
	if err != nil {
		return nil, fmt.Errorf("event data is not hex: %w", err)
	}
	var nonIndexed []*ABIType
	for i, f := range e.Inputs {
		if i < len(e.Indexed) && e.Indexed[i] {
			continue
		}
		nonIndexed = append(nonIndexed, f.Type)
	}
	indexedCount := 0
	for _, indexed := range e.Indexed {
		if indexed {
			indexedCount++
		}
	}
	wantTopics := indexedCount
	if !e.Anonymous {
		wantTopics++
	}
	if len(topics) != wantTopics {
		return nil, fmt.Errorf("event topic count %d, want %d", len(topics), wantTopics)
	}
	values, err := decodeSequenceState(nonIndexed, raw, 0, &decodeState{}, 0)
	if err != nil {
		return nil, err
	}
	out = make([]DecodedParam, 0, len(e.Inputs))
	topicIdx := 0
	if !e.Anonymous {
		topicIdx = 1 // 非匿名事件的 topics[0] 是 topic0
	}
	valueIdx := 0
	for i, f := range e.Inputs {
		param := DecodedParam{Name: f.Name, Type: f.Type.Canonical}
		if i < len(e.Indexed) && e.Indexed[i] {
			if topicIdx < len(topics) {
				param.Value = decodeTopic(f.Type, topics[topicIdx])
			}
			topicIdx++
		} else {
			if valueIdx < len(values) {
				param.Value = values[valueIdx]
			}
			valueIdx++
		}
		out = append(out, param)
	}
	return out, nil
}

// decodeTopic indexed 参数：动态类型只存 keccak 哈希，按原样返回 0x hex。
func decodeTopic(t *ABIType, topic string) any {
	raw, err := hex.DecodeString(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(topic)), "0x"))
	if err != nil || len(raw) != 32 {
		return topic
	}
	if t.Dynamic() {
		return "0x" + hex.EncodeToString(raw)
	}
	value, err := decodeStatic(t, raw, 0)
	if err != nil {
		return "0x" + hex.EncodeToString(raw)
	}
	return value
}

func fieldTypes(fields []ABIField) []*ABIType {
	out := make([]*ABIType, len(fields))
	for i, f := range fields {
		out[i] = f.Type
	}
	return out
}

// decodeSequence 解码一段 head/tail 编码的参数序列。base 是该序列数据块的起点。
func decodeSequence(types []*ABIType, data []byte, base int) ([]any, error) {
	return decodeSequenceState(types, data, base, &decodeState{}, 0)
}

func decodeSequenceState(types []*ABIType, data []byte, base int, state *decodeState, depth int) ([]any, error) {
	if err := state.enter(depth, len(types)); err != nil {
		return nil, err
	}
	out := make([]any, len(types))
	head := base
	for i, t := range types {
		if t.Dynamic() {
			offset, err := readWordAsInt(data, head)
			if err != nil {
				return nil, fmt.Errorf("param %d offset: %w", i, err)
			}
			value, err := decodeValueState(t, data, base+offset, state, depth+1)
			if err != nil {
				return nil, fmt.Errorf("param %d: %w", i, err)
			}
			out[i] = value
			head += 32
			continue
		}
		value, err := decodeValueState(t, data, head, state, depth+1)
		if err != nil {
			return nil, fmt.Errorf("param %d: %w", i, err)
		}
		out[i] = value
		head += 32 * t.HeadWords()
	}
	return out, nil
}

func decodeValue(t *ABIType, data []byte, offset int) (any, error) {
	return decodeValueState(t, data, offset, &decodeState{}, 0)
}

func decodeValueState(t *ABIType, data []byte, offset int, state *decodeState, depth int) (any, error) {
	if err := state.enter(depth, 1); err != nil {
		return nil, err
	}
	switch t.Kind {
	case kindBytes, kindString:
		length, err := readWordAsInt(data, offset)
		if err != nil {
			return nil, err
		}
		if length > maxElements {
			return nil, fmt.Errorf("byte length %d exceeds limit", length)
		}
		start := offset + 32
		if start+length > len(data) || start < 0 {
			return nil, fmt.Errorf("byte payload out of range")
		}
		raw := data[start : start+length]
		if t.Kind == kindString {
			return string(raw), nil
		}
		return "0x" + hex.EncodeToString(raw), nil
	case kindArray:
		count := t.ArrayLen
		base := offset
		if count < 0 {
			n, err := readWordAsInt(data, offset)
			if err != nil {
				return nil, err
			}
			count = n
			base = offset + 32
		}
		if count > maxElements {
			return nil, fmt.Errorf("array length %d exceeds limit", count)
		}
		types := make([]*ABIType, count)
		for i := range types {
			types[i] = t.Elem
		}
		values, err := decodeSequenceState(types, data, base, state, depth+1)
		if err != nil {
			return nil, err
		}
		return values, nil
	case kindTuple:
		values, err := decodeSequenceState(fieldTypes(t.Fields), data, offset, state, depth+1)
		if err != nil {
			return nil, err
		}
		out := make(map[string]any, len(t.Fields))
		for i, f := range t.Fields {
			out[f.Name] = values[i]
		}
		return out, nil
	default:
		return decodeStatic(t, data, offset)
	}
}

// decodeStatic 解码单字（32 字节）静态值。
func decodeStatic(t *ABIType, data []byte, offset int) (any, error) {
	word, err := readWord(data, offset)
	if err != nil {
		return nil, err
	}
	switch t.Kind {
	case kindAddress:
		// 地址规范化：统一小写 0x（§41）
		return "0x" + hex.EncodeToString(word[12:]), nil
	case kindBool:
		for _, b := range word[:31] {
			if b != 0 {
				return nil, fmt.Errorf("invalid bool encoding")
			}
		}
		if word[31] > 1 {
			return nil, fmt.Errorf("invalid bool encoding")
		}
		return word[31] == 1, nil
	case kindFixedBytes:
		return "0x" + hex.EncodeToString(word[:t.ByteLen]), nil
	case kindUint:
		// uint256 必须以字符串输出，禁止 float64（§41/§62-13）
		return new(big.Int).SetBytes(word).String(), nil
	case kindInt:
		return signedFromWord(word, t.Bits).String(), nil
	default:
		return nil, fmt.Errorf("unsupported static type %s", t.Canonical)
	}
}

// signedFromWord 按位宽做二进制补码还原。
func signedFromWord(word []byte, bits int) *big.Int {
	value := new(big.Int).SetBytes(word)
	if bits <= 0 || bits > 256 {
		bits = 256
	}
	limit := new(big.Int).Lsh(big.NewInt(1), uint(bits))
	value.Mod(value, limit)
	half := new(big.Int).Rsh(new(big.Int).Set(limit), 1)
	if value.Cmp(half) >= 0 {
		value.Sub(value, limit)
	}
	return value
}

func readWord(data []byte, offset int) ([]byte, error) {
	if offset < 0 || offset+32 > len(data) {
		return nil, fmt.Errorf("word at %d out of range (len=%d)", offset, len(data))
	}
	return data[offset : offset+32], nil
}

// readWordAsInt 读取一个 32 字节字并作为长度/偏移量返回（拒绝超大值）。
func readWordAsInt(data []byte, offset int) (int, error) {
	word, err := readWord(data, offset)
	if err != nil {
		return 0, err
	}
	value := new(big.Int).SetBytes(word)
	if !value.IsInt64() {
		return 0, fmt.Errorf("offset/length overflows int64")
	}
	n := value.Int64()
	if n < 0 || n > int64(maxDecodeBytes) {
		return 0, fmt.Errorf("offset/length %d out of bounds", n)
	}
	return int(n), nil
}

// ParamsToMap 展平为 name → value（§54 decoded_input 形态）。
func ParamsToMap(params []DecodedParam) map[string]any {
	out := make(map[string]any, len(params))
	for i, p := range params {
		name := p.Name
		if name == "" {
			name = fmt.Sprintf("arg%d", i)
		}
		if _, exists := out[name]; exists {
			name = fmt.Sprintf("%s_%d", name, i)
		}
		out[name] = p.Value
	}
	return out
}
