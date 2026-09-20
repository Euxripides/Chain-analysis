package solana

// 本文件实现 Solana 的指令级解码（ADR-023）：
//   * SPL Token / Token-2022 的转账指令 → token_transfers 行（gross/net/fee + ATA）
//   * logMessages 的 invoke 栈 → CPI 调用树 → internal_transactions 行
//
// 金额口径（ADR-023 D6）：gross 来自指令，net 来自 pre/postTokenBalances 的账户差额
// （余额是权威事实），fee = gross - net。指令里显式带费时两者对账，不一致要计数上报，
// 绝不静默采用其中一个。

import (
	"encoding/base64"
	"encoding/json"
	"math/big"
	"strconv"
	"strings"

	soladdr "github.com/blockchain-platform/blockchain-platform/internal/solana"
)

// 程序 id（ADR-023 D5：两张判别表分开维护）。
const (
	ProgramSPLToken  = "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA"
	ProgramToken2022 = "TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb"
	SystemProgram    = "11111111111111111111111111111111"
)

// SPL Token 基础指令判别值。
const (
	discTransfer        = 3
	discTransferChecked = 12
	// Token-2022 TransferFee 扩展：26 = TransferFeeExtension，子指令 3 = TransferCheckedWithFee。
	discTransferFeeExtension  = 26
	subTransferCheckedWithFee = 3
)

// parsedSemantic 是 jsonParsed 编码下节点给出的语义（type + info）。
type parsedSemantic struct {
	Type string          `json:"type"`
	Info json.RawMessage `json:"info"`
}

// UnmarshalJSON 容忍节点返回的两种形态：对象 {type, info} 与字符串。
//
// 实测（2026-09-20）：某些程序的指令在 jsonParsed 下 parsed 是字符串而不是对象，
// 严格按对象解析会让**整页**解码失败（一笔交易毁掉一个 shard）。字符串形态没有可用
// 语义，按"未解析"处理，让调用方回退到原始 data；绝不因此丢弃整页。
func (p *parsedSemantic) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	if strings.HasPrefix(trimmed, "\"") {
		return nil
	}
	type alias parsedSemantic
	var value alias
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*p = parsedSemantic(value)
	return nil
}

func (p *parsedSemantic) infoMap() map[string]any {
	if p == nil || len(p.Info) == 0 {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(p.Info, &m) != nil {
		return nil
	}
	return m
}

// parsedInstructionRaw 兼容 jsonParsed（programId/accounts 为字符串、带 parsed）
// 与 json（programIdIndex/accounts 为下标、data 为 base58）两种形态。
type parsedInstructionRaw struct {
	ProgramID      string            `json:"programId"`
	ProgramIDIndex *int              `json:"programIdIndex"`
	Accounts       []json.RawMessage `json:"accounts"`
	Data           json.RawMessage   `json:"data"`
	Parsed         *parsedSemantic   `json:"parsed"`
}

func (ix parsedInstructionRaw) programID(keys []string) string {
	if ix.ProgramID != "" {
		return ix.ProgramID
	}
	if ix.ProgramIDIndex != nil && *ix.ProgramIDIndex >= 0 && *ix.ProgramIDIndex < len(keys) {
		return keys[*ix.ProgramIDIndex]
	}
	return ""
}

func (ix parsedInstructionRaw) accountList(keys []string) []string {
	out := make([]string, 0, len(ix.Accounts))
	for _, raw := range ix.Accounts {
		var idx int
		if err := json.Unmarshal(raw, &idx); err == nil {
			if idx >= 0 && idx < len(keys) {
				out = append(out, keys[idx])
			} else {
				out = append(out, "")
			}
			continue
		}
		var key string
		if err := json.Unmarshal(raw, &key); err == nil {
			out = append(out, key)
			continue
		}
		out = append(out, "")
	}
	return out
}

// dataBytes 返回指令 data 的原始字节（base58 为主，兼容 base64）；解析不出返回 nil。
func (ix parsedInstructionRaw) dataBytes() []byte {
	if len(ix.Data) == 0 || string(ix.Data) == "null" {
		return nil
	}
	var s string
	if err := json.Unmarshal(ix.Data, &s); err != nil || s == "" {
		return nil
	}
	if decoded, err := soladdr.DecodeBase58(s); err == nil {
		return decoded
	}
	if decoded, err := base64.StdEncoding.DecodeString(s); err == nil {
		return decoded
	}
	return nil
}

type tokenBalanceEntry struct {
	AccountIndex  int    `json:"accountIndex"`
	Mint          string `json:"mint"`
	Owner         string `json:"owner"`
	UITokenAmount struct {
		Amount   string `json:"amount"`
		Decimals uint8  `json:"decimals"`
	} `json:"uiTokenAmount"`
}

type innerInstructionGroup struct {
	Index        int                    `json:"index"`
	Instructions []parsedInstructionRaw `json:"instructions"`
}

// tokenAccountState 是某笔交易里一个代币账户（ATA）的前后状态。
type tokenAccountState struct {
	Key      string
	Index    int
	Mint     string
	Owner    string
	Decimals uint16
	Pre      *big.Int
	Post     *big.Int
}

// net 是该账户在本交易内的净变化（post - pre）。
func (s *tokenAccountState) net() *big.Int {
	if s == nil {
		return big.NewInt(0)
	}
	pre, post := big.NewInt(0), big.NewInt(0)
	if s.Pre != nil {
		pre = s.Pre
	}
	if s.Post != nil {
		post = s.Post
	}
	return new(big.Int).Sub(post, pre)
}

// buildTokenAccountIndex 把 pre/postTokenBalances 变成 ATA → 状态的映射。
// accountIndex 指向 message.accountKeys 的下标，条目自带 owner 与 mint。
func buildTokenAccountIndex(keys []string, pre, post []tokenBalanceEntry) map[string]*tokenAccountState {
	index := map[string]*tokenAccountState{}
	pick := func(entry tokenBalanceEntry) *tokenAccountState {
		if entry.AccountIndex < 0 || entry.AccountIndex >= len(keys) {
			return nil
		}
		key := keys[entry.AccountIndex]
		state, ok := index[key]
		if !ok {
			state = &tokenAccountState{Key: key, Index: entry.AccountIndex}
			index[key] = state
		}
		if entry.Mint != "" {
			state.Mint = entry.Mint
		}
		if entry.Owner != "" {
			state.Owner = entry.Owner
		}
		if entry.UITokenAmount.Amount != "" {
			state.Decimals = uint16(entry.UITokenAmount.Decimals)
		}
		return state
	}
	for _, entry := range pre {
		if state := pick(entry); state != nil {
			if amount, ok := new(big.Int).SetString(entry.UITokenAmount.Amount, 10); ok {
				state.Pre = amount
			}
		}
	}
	for _, entry := range post {
		if state := pick(entry); state != nil {
			if amount, ok := new(big.Int).SetString(entry.UITokenAmount.Amount, 10); ok {
				state.Post = amount
			}
		}
	}
	return index
}

// splTransfer 是一条已解码的代币转账指令。
type splTransfer struct {
	OuterIndex  int
	InnerIndex  int // 0 = 外层；≥1 = 该外层下的 CPI 内层序号
	Kind        string
	Mint        string
	Source      string // 代币账户
	Destination string
	Authority   string
	Decimals    uint16
	Gross       *big.Int
	DecodedFee  *big.Int // 指令显式带的手续费（Token-2022）；nil = 未解码出
}

func littleEndianUint64(data []byte, start int) (*big.Int, bool) {
	if start < 0 || start+8 > len(data) {
		return nil, false
	}
	value := big.NewInt(0)
	for i := 7; i >= 0; i-- {
		value.Lsh(value, 8)
		value.Or(value, big.NewInt(int64(data[start+i])))
	}
	return value, true
}

// decodeSPLTransfer 解析一条指令；不是转账指令时返回 false。
func decodeSPLTransfer(programID string, kind string, ix parsedInstructionRaw, keys []string) (splTransfer, bool) {
	if programID != ProgramSPLToken && programID != ProgramToken2022 {
		return splTransfer{}, false
	}
	accounts := ix.accountList(keys)
	info := ix.Parsed.infoMap()

	// jsonParsed：节点已给出语义（type 在 parsed.type 上，不在 info 里）。
	if info != nil {
		parsedType := ""
		if ix.Parsed != nil {
			parsedType = ix.Parsed.Type
		}
		switch parsedType {
		case "transfer", "transferChecked", "transferCheckedWithFee":
		default:
			return splTransfer{}, false
		}
		transfer := splTransfer{Kind: parsedType}
		transfer.Source, _ = info["source"].(string)
		transfer.Destination, _ = info["destination"].(string)
		transfer.Authority, _ = info["authority"].(string)
		transfer.Mint, _ = info["mint"].(string)
		if amount, ok := stringFromAny(info["amount"]); ok {
			transfer.Gross, _ = new(big.Int).SetString(amount, 10)
		}
		if tokenAmount, ok := info["tokenAmount"].(map[string]any); ok {
			if amount, ok := stringFromAny(tokenAmount["amount"]); ok {
				transfer.Gross, _ = new(big.Int).SetString(amount, 10)
			}
			if decimals, ok := numberFromAny(tokenAmount["decimals"]); ok {
				transfer.Decimals = uint16(decimals)
			}
		}
		if feeAmount, ok := info["feeAmount"].(map[string]any); ok {
			if amount, ok := stringFromAny(feeAmount["amount"]); ok {
				transfer.DecodedFee, _ = new(big.Int).SetString(amount, 10)
			}
		}
		if transfer.Gross == nil {
			return splTransfer{}, false
		}
		return transfer, true
	}

	// raw data：按判别表自己解（jsonParsed 未识别 Token-2022 扩展时走这里）。
	data := ix.dataBytes()
	if len(data) == 0 {
		return splTransfer{}, false
	}
	transfer := splTransfer{}
	switch {
	case data[0] == discTransfer && len(accounts) >= 2:
		amount, ok := littleEndianUint64(data, 1)
		if !ok {
			return splTransfer{}, false
		}
		transfer.Kind = "transfer"
		transfer.Source, transfer.Destination = accounts[0], accounts[1]
		if len(accounts) > 2 {
			transfer.Authority = accounts[2]
		}
		transfer.Gross = amount
	case data[0] == discTransferChecked && len(accounts) >= 3 && len(data) >= 10:
		amount, ok := littleEndianUint64(data, 1)
		if !ok {
			return splTransfer{}, false
		}
		transfer.Kind = "transferChecked"
		transfer.Source, transfer.Destination = accounts[0], accounts[2]
		transfer.Mint = accounts[1]
		if len(accounts) > 3 {
			transfer.Authority = accounts[3]
		}
		transfer.Decimals = uint16(data[9])
		transfer.Gross = amount
	case data[0] == discTransferFeeExtension && len(data) > 1 && data[1] == subTransferCheckedWithFee && len(accounts) >= 3:
		amount, ok := littleEndianUint64(data, 2)
		if !ok {
			return splTransfer{}, false
		}
		transfer.Kind = "transferCheckedWithFee"
		transfer.Source, transfer.Destination = accounts[0], accounts[2]
		transfer.Mint = accounts[1]
		if len(accounts) > 3 {
			transfer.Authority = accounts[3]
		}
		if len(data) > 10 {
			transfer.Decimals = uint16(data[10])
		}
		if fee, ok := littleEndianUint64(data, 11); ok {
			transfer.DecodedFee = fee
		}
		transfer.Gross = amount
	default:
		return splTransfer{}, false
	}
	_ = kind
	return transfer, true
}

func stringFromAny(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, t != ""
	case float64:
		return strconv.FormatInt(int64(t), 10), true
	case json.Number:
		return t.String(), true
	default:
		return "", false
	}
}

func numberFromAny(v any) (int64, bool) {
	switch t := v.(type) {
	case float64:
		return int64(t), true
	case json.Number:
		n, err := t.Int64()
		return n, err == nil
	case string:
		n, err := strconv.ParseInt(t, 10, 64)
		return n, err == nil
	default:
		return 0, false
	}
}

// tokenTransferRow 与 Ankr 的 token transfer 行同构，供 ingest.NormalizeTokenTransfer 消费。
type tokenTransferRow struct {
	BlockHeight      string `json:"blockHeight"`
	Timestamp        int64  `json:"timestamp"`
	TransactionHash  string `json:"transactionHash"`
	LogIndex         uint32 `json:"logIndex"`
	ContractAddress  string `json:"contractAddress"`
	TokenSymbol      string `json:"tokenSymbol"`
	TokenDecimals    uint16 `json:"tokenDecimals"`
	FromAddress      string `json:"fromAddress"`
	ToAddress        string `json:"toAddress"`
	ValueRawInteger  string `json:"valueRawInteger"`
	Value            string `json:"value"`
	GrossAmountRaw   string `json:"grossAmountRaw"`
	FeeAmountRaw     string `json:"feeAmountRaw"`
	FromTokenAccount string `json:"fromTokenAccount"`
	ToTokenAccount   string `json:"toTokenAccount"`
}

// tokenTransferStats 记录解码过程中的异常信号：它们必须可见，绝不静默丢数据。
type tokenTransferStats struct {
	Transfers           int
	OwnerUnresolved     int
	FeeFromBalances     int
	FeeMismatch         int
	SkippedNoBalance    int
	UnsupportedPrograms int
}

// buildTokenTransferRows 按执行顺序（外层 → 该外层的 CPI 内层）解码转账并生成行。
func buildTokenTransferRows(slot uint64, blockTime int64, signature string, keys []string, outer []parsedInstructionRaw, inner []innerInstructionGroup, index map[string]*tokenAccountState) ([]tokenTransferRow, tokenTransferStats) {
	var rows []tokenTransferRow
	var stats tokenTransferStats

	emit := func(outerIndex, innerIndex int, ix parsedInstructionRaw) {
		programID := ix.programID(keys)
		transfer, ok := decodeSPLTransfer(programID, "", ix, keys)
		if !ok {
			return
		}
		source := index[transfer.Source]
		destination := index[transfer.Destination]
		if source == nil || destination == nil || source.Owner == "" || destination.Owner == "" {
			stats.OwnerUnresolved++
			return
		}
		mint := transfer.Mint
		if mint == "" {
			mint = source.Mint
		}
		if mint == "" {
			mint = destination.Mint
		}
		if mint == "" {
			// 没有 mint 的转账无法归属代币（也不该编造）：计入未解析。
			stats.SkippedNoBalance++
			return
		}
		decimals := transfer.Decimals
		if decimals == 0 && source.Decimals != 0 {
			decimals = source.Decimals
		}
		gross := transfer.Gross
		if gross == nil || gross.Sign() <= 0 {
			return
		}
		net := destination.net()
		if net.Sign() < 0 {
			net = big.NewInt(0)
		}
		fee := new(big.Int).Sub(gross, net)
		if fee.Sign() < 0 {
			fee = big.NewInt(0)
		}
		if fee.Sign() > 0 && transfer.DecodedFee == nil {
			stats.FeeFromBalances++
		}
		if transfer.DecodedFee != nil && transfer.DecodedFee.Cmp(fee) != 0 {
			// 指令显式费用与余额差额不一致：以余额为准（余额是链上事实），但必须计数。
			stats.FeeMismatch++
		}
		logIndex := uint32(outerIndex)*256 + uint32(innerIndex)
		rows = append(rows, tokenTransferRow{
			BlockHeight: strconv.FormatUint(slot, 10), Timestamp: blockTime,
			TransactionHash: signature, LogIndex: logIndex,
			ContractAddress: mint, TokenSymbol: "", TokenDecimals: decimals,
			FromAddress: source.Owner, ToAddress: destination.Owner,
			ValueRawInteger: net.String(), Value: net.String(),
			GrossAmountRaw: gross.String(), FeeAmountRaw: fee.String(),
			FromTokenAccount: transfer.Source, ToTokenAccount: transfer.Destination,
		})
		stats.Transfers++
	}

	for outerIndex, ix := range outer {
		emit(outerIndex, 0, ix)
	}
	for _, group := range inner {
		outerIndex := group.Index
		for i, ix := range group.Instructions {
			emit(outerIndex, i+1, ix)
		}
	}
	return rows, stats
}

// cpiNode 是 CPI 调用树的一个节点（来自 logMessages 的 invoke 栈）。
type cpiNode struct {
	Program   string
	Parent    string
	Depth     int
	TracePath string
	Failed    bool
	Error     string
	CallIndex uint32
}

type cpiFrame struct {
	program    string
	path       string
	childCount int
	nodeIndex  int
}

// buildCPITree 从 logMessages 还原调用树。
//
// 标准 Solana RPC 不返回 CPI 树；官方做法就是解析 "Program <id> invoke [n]" /
// "success" / "failed: ..." 日志并按栈还原嵌套。depth 来自日志，trace_path 由父路径
// 与兄弟序号拼出；call_index 采用 ADR-023 D3 的合成键格式 outer*256+ordinal，
// 其中 depth-1 节点的 outer 按外层指令顺序对应，ordinal 是该外层下的 CPI 序号。
func buildCPITree(logs []string, outerPrograms []string) []cpiNode {
	var nodes []cpiNode
	var stack []*cpiFrame
	rootOrdinal := 0
	rootChildren := map[int]int{}

	for _, raw := range logs {
		line := strings.TrimSpace(raw)
		if !strings.HasPrefix(line, "Program ") {
			continue
		}
		rest := strings.TrimPrefix(line, "Program ")
		switch {
		case strings.Contains(rest, " invoke ["):
			idx := strings.Index(rest, " invoke [")
			program := strings.TrimSpace(rest[:idx])
			depthText := strings.TrimSuffix(strings.TrimSpace(rest[idx+len(" invoke ["):]), "]")
			depth, err := strconv.Atoi(depthText)
			if err != nil {
				continue
			}
			outerIndex, ordinal := 0, 0
			tracePath := ""
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				parent.childCount++
				outerIndex = parentOuterIndex(parent)
				ordinal = parent.childCount
				tracePath = parent.path + "." + strconv.Itoa(parent.childCount-1)
			} else {
				outerIndex = rootOrdinal
				if outerIndex < len(outerPrograms) {
					rootOrdinal++
				}
				rootChildren[outerIndex]++
				ordinal = 0
				tracePath = strconv.Itoa(outerIndex)
			}
			callIndex := uint32(outerIndex)*256 + uint32(ordinal)
			parentProgram := ""
			if len(stack) > 0 {
				parentProgram = stack[len(stack)-1].program
			}
			nodes = append(nodes, cpiNode{Program: program, Parent: parentProgram, Depth: depth, TracePath: tracePath, CallIndex: callIndex})
			stack = append(stack, &cpiFrame{program: program, path: tracePath, nodeIndex: len(nodes) - 1})
		case strings.HasSuffix(rest, " success"):
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case strings.Contains(rest, " failed"):
			if len(stack) > 0 {
				frame := stack[len(stack)-1]
				nodes[frame.nodeIndex].Failed = true
				if idx := strings.Index(rest, " failed: "); idx >= 0 {
					nodes[frame.nodeIndex].Error = strings.TrimSpace(rest[idx+len(" failed: "):])
				} else {
					nodes[frame.nodeIndex].Error = "failed"
				}
				stack = stack[:len(stack)-1]
			}
		}
	}
	return nodes
}

// parentOuterIndex 让子节点与根节点共用同一个 outer 序号，保证 call_index 不越界串号。
func parentOuterIndex(frame *cpiFrame) int {
	if frame == nil {
		return 0
	}
	if idx := strings.Index(frame.path, "."); idx >= 0 {
		if value, err := strconv.Atoi(frame.path[:idx]); err == nil {
			return value
		}
	}
	if value, err := strconv.Atoi(frame.path); err == nil {
		return value
	}
	return 0
}

// ---- 程序日志（logs，ADR-024 D1）----

// programLogRow 与 EVM 日志行同构但语义不同：topics 恒空、来源由 eventSource 标注。
type programLogRow struct {
	BlockNumber     string   `json:"blockNumber"`
	TransactionHash string   `json:"transactionHash"`
	LogIndex        uint32   `json:"logIndex"`
	Address         string   `json:"address"`
	Topics          []string `json:"topics"`
	Data            string   `json:"data"`
	Removed         bool     `json:"removed"`
	Timestamp       int64    `json:"timestamp"`
	EventName       string   `json:"eventName"`
	EventSource     string   `json:"eventSource"`
	EventSignature  string   `json:"eventSignature"`
	EventVerified   bool     `json:"eventVerified"`
}

// buildProgramLogRows 把 meta.logMessages 逐行转成日志行。
//
// 归属：用 invoke 栈推断当前 program（与 CPI 树同一份栈语义）；序号从 1 连续递增，
// 只对真正的日志行（Program log: / Program data:）编号——invoke/success/consumed
// 这些运行时行不是日志内容，不占序号。
func buildProgramLogRows(slot uint64, blockTime int64, signature string, logs []string) []programLogRow {
	rows := make([]programLogRow, 0, len(logs))
	var stack []string
	index := uint32(0)
	appendRow := func(program, data, source string) {
		index++
		rows = append(rows, programLogRow{
			BlockNumber: strconv.FormatUint(slot, 10), TransactionHash: signature,
			LogIndex: index, Address: program, Topics: []string{}, Data: data,
			Removed: false, Timestamp: blockTime, EventName: "",
			EventSource: source, EventSignature: "", EventVerified: false,
		})
	}
	for _, raw := range logs {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "Program ") {
			rest := strings.TrimPrefix(line, "Program ")
			if idx := strings.Index(rest, " invoke ["); idx >= 0 {
				stack = append(stack, strings.TrimSpace(rest[:idx]))
				continue
			}
			if strings.HasSuffix(rest, " success") || strings.Contains(rest, " failed") {
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
				continue
			}
		}
		program := ""
		if len(stack) > 0 {
			program = stack[len(stack)-1]
		}
		switch {
		case strings.HasPrefix(line, "Program log: "):
			appendRow(program, strings.TrimPrefix(line, "Program log: "), "solana_program_log")
		case strings.HasPrefix(line, "Program data: "):
			appendRow(program, strings.TrimPrefix(line, "Program data: "), "solana_program_data")
		}
	}
	return rows
}

// cpiRow 与 TRON internal 行同构，供 solana.NormalizeInternal 消费。
type cpiRow struct {
	TxHash      string `json:"txHash"`
	CallIndex   uint32 `json:"callIndex"`
	BlockNumber string `json:"blockNumber"`
	Timestamp   int64  `json:"timestamp"`
	From        string `json:"from"`
	To          string `json:"to"`
	ValueRaw    string `json:"valueRaw"`
	AssetType   string `json:"assetType"`
	CallType    string `json:"callType"`
	Depth       uint16 `json:"depth"`
	TracePath   string `json:"tracePath"`
	MethodID    string `json:"methodId"`
	Failed      bool   `json:"failed"`
	Error       string `json:"error"`
}
