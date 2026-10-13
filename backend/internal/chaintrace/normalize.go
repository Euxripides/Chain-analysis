package chaintrace

import (
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
)

// ---- 归一化（§101/§102）----

// maxFrames 防御性上限：超大 trace 不得无界展开（§121）。
const maxFrames = 50000
const maxTraceDepth = 1024
const NormalizationVersion = "trace-tree-v2"

// Normalize 自动识别 parity 数组或 geth 嵌套对象并转成 Canonical Call Tree。
func Normalize(raw json.RawMessage) (*CallFrame, string, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, "", fmt.Errorf("empty trace payload")
	}
	if trimmed[0] == '[' {
		frame, err := normalizeParity(raw)
		if err == nil {
			frame.NormalizationVersion = NormalizationVersion
		}
		return frame, SourceParity, err
	}
	frame, err := normalizeGeth(raw)
	if err == nil {
		frame.NormalizationVersion = NormalizationVersion
	}
	return frame, SourceGeth, err
}

// ---- parity trace_transaction ----

type parityAction struct {
	CallType      string `json:"callType"`
	From          string `json:"from"`
	To            string `json:"to"`
	Value         string `json:"value"`
	Gas           string `json:"gas"`
	Input         string `json:"input"`
	Init          string `json:"init"`
	Address       string `json:"address"`
	RefundAddress string `json:"refundAddress"`
	Balance       string `json:"balance"`
}

type parityResult struct {
	GasUsed string `json:"gasUsed"`
	Output  string `json:"output"`
	Address string `json:"address"`
	Code    string `json:"code"`
}

type parityTrace struct {
	Action       parityAction  `json:"action"`
	Result       *parityResult `json:"result"`
	Error        string        `json:"error"`
	Subtraces    *int          `json:"subtraces"`
	TraceAddress []int         `json:"traceAddress"`
	Type         string        `json:"type"`
}

func normalizeParity(raw json.RawMessage) (*CallFrame, error) {
	var items []parityTrace
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("invalid parity trace payload")
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("empty parity trace")
	}
	if len(items) > maxFrames {
		return nil, fmt.Errorf("parity trace exceeds complete frame limit")
	}
	// Nodes own stable allocations. Copy them into parent slices only after all
	// descendants are assembled; pointers into an appendable slice lose branches.
	byPath := map[string]*CallFrame{}
	children := map[string]int{}
	for _, item := range items {
		if len(item.TraceAddress) > maxTraceDepth {
			return nil, fmt.Errorf("parity trace depth exceeds limit")
		}
		for _, n := range item.TraceAddress {
			if n < 0 {
				return nil, fmt.Errorf("negative trace address")
			}
		}
		path := pathKey(item.TraceAddress)
		if byPath[path] != nil {
			return nil, fmt.Errorf("duplicate parity trace address")
		}
		byPath[path] = parityFrame(item)
		if len(item.TraceAddress) > 0 {
			children[pathKey(item.TraceAddress[:len(item.TraceAddress)-1])]++
		}
	}
	if byPath["root"] == nil {
		return nil, fmt.Errorf("parity trace has no root frame")
	}
	for _, item := range items {
		path := pathKey(item.TraceAddress)
		if item.Subtraces != nil && (*item.Subtraces < 0 || *item.Subtraces != children[path]) {
			return nil, fmt.Errorf("incomplete parity subtraces")
		}
		byPath[path].Children = make([]CallFrame, children[path])
		if len(item.TraceAddress) > 0 {
			parent := pathKey(item.TraceAddress[:len(item.TraceAddress)-1])
			if byPath[parent] == nil || item.TraceAddress[len(item.TraceAddress)-1] >= children[parent] {
				return nil, fmt.Errorf("parity trace parent or sibling missing")
			}
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return len(items[i].TraceAddress) > len(items[j].TraceAddress) })
	for _, item := range items {
		if len(item.TraceAddress) == 0 {
			continue
		}
		parent := byPath[pathKey(item.TraceAddress[:len(item.TraceAddress)-1])]
		parent.Children[item.TraceAddress[len(item.TraceAddress)-1]] = *byPath[pathKey(item.TraceAddress)]
	}
	return byPath["root"], nil
}

func parityFrame(item parityTrace) *CallFrame {
	frame := &CallFrame{Error: item.Error}
	switch strings.ToLower(item.Type) {
	case "create":
		frame.Type = CallTypeCreate
		frame.Input = item.Action.Init
		if item.Result != nil && item.Result.Address != "" {
			if address, err := chain.ParseAddress(item.Result.Address); err == nil {
				frame.To = &address
			}
		}
	case "suicide", "selfdestruct":
		frame.Type = CallTypeSelfDestruct
		if address, err := chain.ParseAddress(item.Action.RefundAddress); err == nil {
			frame.To = &address
		}
	default:
		frame.Type = canonicalCallType(item.Action.CallType)
		frame.Input = item.Action.Input
		if address, err := chain.ParseAddress(item.Action.To); err == nil {
			frame.To = &address
		}
	}
	from := item.Action.From
	if from == "" {
		from = item.Action.Address
	}
	if address, err := chain.ParseAddress(from); err == nil {
		frame.From = address
	}
	value := item.Action.Value
	if value == "" {
		value = item.Action.Balance
	}
	frame.Value = decimalValue(value)
	frame.Gas, _ = chain.HexToUint64(item.Action.Gas)
	if item.Result != nil {
		frame.GasUsed, _ = chain.HexToUint64(item.Result.GasUsed)
		frame.Output = item.Result.Output
	}
	return frame
}

// ---- geth debug_traceTransaction（callTracer）----

type gethFrame struct {
	Type    string      `json:"type"`
	From    string      `json:"from"`
	To      string      `json:"to"`
	Value   string      `json:"value"`
	Gas     string      `json:"gas"`
	GasUsed string      `json:"gasUsed"`
	Input   string      `json:"input"`
	Output  string      `json:"output"`
	Error   string      `json:"error"`
	Revert  string      `json:"revertReason"`
	Calls   []gethFrame `json:"calls"`
}

func normalizeGeth(raw json.RawMessage) (*CallFrame, error) {
	var item gethFrame
	if err := json.Unmarshal(raw, &item); err != nil {
		return nil, fmt.Errorf("invalid geth trace payload")
	}
	if item.Type == "" && len(item.Calls) == 0 {
		return nil, fmt.Errorf("geth trace has no root frame")
	}
	// Check completeness before conversion; never return a silently truncated tree.
	var check func(*gethFrame, int) error
	total := 0
	check = func(f *gethFrame, depth int) error {
		total++
		if total > maxFrames || depth > maxTraceDepth {
			return fmt.Errorf("geth trace exceeds complete tree limit")
		}
		for i := range f.Calls {
			if err := check(&f.Calls[i], depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := check(&item, 0); err != nil {
		return nil, err
	}
	count := 0
	frame := convertGeth(item, &count)
	return frame, nil
}

func convertGeth(item gethFrame, count *int) *CallFrame {
	*count++
	frame := &CallFrame{
		Type: canonicalCallType(item.Type), Input: item.Input, Output: item.Output,
		Error: firstNonEmpty(item.Error, item.Revert), Value: decimalValue(item.Value),
	}
	if address, err := chain.ParseAddress(item.From); err == nil {
		frame.From = address
	}
	if address, err := chain.ParseAddress(item.To); err == nil {
		frame.To = &address
	}
	frame.Gas, _ = chain.HexToUint64(item.Gas)
	frame.GasUsed, _ = chain.HexToUint64(item.GasUsed)
	for _, child := range item.Calls {
		if *count >= maxFrames {
			break
		}
		frame.Children = append(frame.Children, *convertGeth(child, count))
	}
	return frame
}

// ---- 公共工具 ----

func canonicalCallType(value string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "", "CALL":
		return CallTypeCall
	case "STATICCALL":
		return CallTypeStaticCall
	case "DELEGATECALL":
		return CallTypeDelegateCall
	case "CALLCODE":
		return CallTypeCallCode
	case "CREATE":
		return CallTypeCreate
	case "CREATE2":
		return CallTypeCreate2
	case "SELFDESTRUCT", "SUICIDE":
		return CallTypeSelfDestruct
	default:
		return strings.ToUpper(value)
	}
}

// decimalValue hex wei → 十进制字符串（§155：绝不用 float64）。
func decimalValue(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "0"
	}
	if strings.HasPrefix(trimmed, "0x") || strings.HasPrefix(trimmed, "0X") {
		parsed, err := chain.HexToBigInt(trimmed)
		if err != nil {
			return "0"
		}
		return parsed.String()
	}
	if _, ok := new(big.Int).SetString(trimmed, 10); ok {
		return trimmed
	}
	return "0"
}

func pathKey(path []int) string {
	if len(path) == 0 {
		return "root"
	}
	parts := make([]string, len(path))
	for i, p := range path {
		parts[i] = fmt.Sprint(p)
	}
	return strings.Join(parts, ".")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// Summarize 统计帧数与最大深度。
func Summarize(root *CallFrame) (frames, depth int) {
	if root == nil {
		return 0, 0
	}
	frames = 1
	depth = 1
	for i := range root.Children {
		childFrames, childDepth := Summarize(&root.Children[i])
		frames += childFrames
		if childDepth+1 > depth {
			depth = childDepth + 1
		}
	}
	return frames, depth
}
