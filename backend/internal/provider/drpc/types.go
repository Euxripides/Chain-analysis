package drpc

// 本文件定义两件事：
//  1. dRPC Data API 历史页的响应模型（官方 Get Transactions History，开发文档 §3.1/§7.3）；
//  2. 规范 §3 冻结的 Row 契约：交给 ingest 的每一行都是 Ankr 同构 JSON，字段名固定。

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"
)

// solanaNativeMint 是 Solana 原生 SOL 的铸币标识（System Program id）。
//
// 关键实测（2026-09-22）：Data API 在原生 SOL 转账里返回的是这个地址，而**不是空串**。
// 早期实现按"token.address 为空即原生"判断，会把原生转账误当成 SPL Token 写进
// token_transfers（违反开发文档 §8.3），因此这里显式识别该常量。
const solanaNativeMint = "11111111111111111111111111111111"

// ---- 兼容解码：官方文档给的是 EVM 示例，Solana 复用同一端点 ----
//
// 不确定点（已标注，按"不伪造数据"处理）：官方文档只给出 EVM 示例，
// 没有 Solana 历史页的字段样例。因此对可能变化的编码做兼容解码
// （数字/字符串都接受），字段缺失就留空并计数，绝不猜造。

type flexString string

func (v *flexString) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		*v = ""
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*v = flexString(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(data, &n); err == nil {
		*v = flexString(n.String())
		return nil
	}
	*v = ""
	return nil
}

type flexInt64 int64

func (v *flexInt64) UnmarshalJSON(data []byte) error {
	*v = 0
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(data, &n); err == nil {
		if parsed, err := n.Int64(); err == nil {
			*v = flexInt64(parsed)
			return nil
		}
		if parsed, err := strconv.ParseFloat(n.String(), 64); err == nil {
			*v = flexInt64(parsed)
		}
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		if parsed, err := strconv.ParseInt(s, 10, 64); err == nil {
			*v = flexInt64(parsed)
		}
	}
	return nil
}

type flexUint64 uint64

func (v *flexUint64) UnmarshalJSON(data []byte) error {
	*v = 0
	var inner flexInt64
	if err := inner.UnmarshalJSON(data); err != nil {
		return err
	}
	if inner > 0 {
		*v = flexUint64(inner)
	}
	return nil
}

type flexUint16 uint16

func (v *flexUint16) UnmarshalJSON(data []byte) error {
	*v = 0
	var inner flexInt64
	if err := inner.UnmarshalJSON(data); err != nil {
		return err
	}
	if inner > 0 {
		*v = flexUint16(inner)
	}
	return nil
}

// ---- Data API 响应模型 ----

// dataAPIPage 是 /lambda/v1/transactions/{address}/history 的响应。
// next_page_token 原样保存（不解析内部结构，规范 §1.4 / 开发文档 §7.3）。
type dataAPIPage struct {
	NextPageToken string      `json:"next_page_token"`
	Data          []dataAPITx `json:"data"`
}

type dataAPITx struct {
	Type string `json:"type"`
	Hash string `json:"hash"`
	// Signature 是 Solana 上"交易标识"的常见叫法；官方文档未给 Solana 示例，
	// 因此 hash/signature 都接受，hash 优先，两者皆空时该行计入 skipped 而不是猜造签名。
	Signature        string     `json:"signature"`
	Block            flexUint64 `json:"block"`
	Slot             flexUint64 `json:"slot"`
	Timestamp        flexInt64  `json:"timestamp"` // 毫秒（官方请求参数 start/end 也是毫秒）
	SenderAddress    string     `json:"sender_address"`
	RecipientAddress string     `json:"recipient_address"`
	Status           string     `json:"status"`
	// Success 是 Solana 实测里与 status 并存的布尔结果；status 缺失时用它判定。
	Success *bool `json:"success"`
	// Owner 是"这条历史属于谁"（即被查询地址）。Solana 实测行里没有 sender_address，
	// 只有 owner，因此它是 from 的兜底来源 —— 且仅在单笔原生转账缺失发送方时才用。
	Owner      string            `json:"owner"`
	Fee        *dataAPIFee       `json:"fee"`
	Transfers  []dataAPITransfer `json:"transfers"`
	Attributes map[string]any    `json:"attributes"`
}

// dataAPIFee 兼容两种**真实**形态：
//
//	Solana（2026-09-22 实测）："fee": 7e-06        —— 数字，原生币（SOL）十进制金额
//	EVM（官方文档示例）      ："fee": {"token_symbol":..., "token_amount_string":...}
//
// 早期实现只按对象解码，真实 Solana 页面整页解码失败（json: cannot unmarshal number
// into Go struct field ... of type drpc.dataAPIFee），导致所有历史下载报
// DRPC_INVALID_RESPONSE —— 这正是"必须用真实端点验收"的原因。
type dataAPIFee struct {
	TokenSymbol       string     `json:"token_symbol"`
	TokenAddress      string     `json:"token_address"`
	TokenDecimals     flexUint16 `json:"token_decimals"`
	TokenAmountString flexString `json:"token_amount_string"`

	// nativeAmountText 保存数字形态的原始文本（如 "7e-06"），绝不经过 float64。
	nativeAmountText string
}

func (f *dataAPIFee) UnmarshalJSON(raw []byte) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil
	}
	if trimmed[0] == '{' {
		type plain dataAPIFee // 防止递归调用本方法
		var object plain
		if err := json.Unmarshal(trimmed, &object); err != nil {
			return err
		}
		*f = dataAPIFee(object)
		return nil
	}
	var number json.Number
	if err := json.Unmarshal(trimmed, &number); err == nil {
		f.nativeAmountText = number.String()
		return nil
	}
	var text string
	if err := json.Unmarshal(trimmed, &text); err == nil {
		f.nativeAmountText = strings.TrimSpace(text)
		return nil
	}
	// 其它形态（数组/bool）忽略：手续费是附加信息，不能让整页解码失败。
	return nil
}

// lamportsText 把手续费换算成 Row 契约要求的 lamports 文本。
//
// 数字形态用十进制换算（1 SOL = 1e9 lamports）：7e-06 SOL 用 float64 会得到
// 7000.000000000001，用 decimal 得到精确的 7000。
// 对象形态只在原生（无 token_address）时沿用 token_amount_string，代币计价手续费留空。
func (f *dataAPIFee) lamportsText() string {
	if f == nil {
		return ""
	}
	if f.nativeAmountText != "" {
		value, err := decimal.NewFromString(f.nativeAmountText)
		if err != nil {
			return ""
		}
		return value.Mul(decimal.NewFromInt(1_000_000_000)).Truncate(0).String()
	}
	if f.TokenAddress == "" {
		return string(f.TokenAmountString)
	}
	return ""
}

// sender 返回交易的行级发起方；Solana 实测没有 sender_address，用 owner 兜底。
func (tx dataAPITx) sender() string {
	if tx.SenderAddress != "" {
		return tx.SenderAddress
	}
	return tx.Owner
}

type dataAPITransfer struct {
	Direction        string       `json:"direction"`
	SenderAddress    string       `json:"sender_address"`
	RecipientAddress string       `json:"recipient_address"`
	Token            dataAPIToken `json:"token"`
	Decimals         flexUint16   `json:"decimals"`
	AmountString     flexString   `json:"amount_string"`
}

type dataAPIToken struct {
	Name    string `json:"name"`
	Symbol  string `json:"symbol"`
	Address string `json:"address"`
	// NFT 为 null 或缺失时表示可替代代币；非 null 对象才是 NFT（开发文档 §8.3：
	// "只有 token.nft 明确存在时写 nft_transfers"）。结构未知，只判断存在性。
	NFT json.RawMessage `json:"nft"`
}

func (t dataAPIToken) isNFT() bool {
	raw := string(t.NFT)
	return len(t.NFT) > 0 && raw != "null" && raw != "{}" && raw != "false"
}

// isNative 表示原生 SOL 转账：token.address 为空即原生（EVM 示例同构）。
func (t dataAPITransfer) isNative() bool {
	return t.Token.Address == "" || t.Token.Address == solanaNativeMint
}

// signature 返回交易标识：hash 优先，缺失时退回 signature。
func (tx dataAPITx) signature() string {
	if tx.Hash != "" {
		return tx.Hash
	}
	return tx.Signature
}

// slot 返回区块字段：block 优先，缺失时退回 slot。
func (tx dataAPITx) slot() uint64 {
	if tx.Block != 0 {
		return uint64(tx.Block)
	}
	return uint64(tx.Slot)
}

// unixSeconds 把 Data API 的时间戳（毫秒）换算成秒。
//
// 判断依据：官方示例 timestamp=1706034371000、start/end 也以毫秒描述；
// 但为避免上游单位变化导致 1970 年时间戳，>1e11 才当作毫秒。
func (tx dataAPITx) unixSeconds() int64 {
	ts := int64(tx.Timestamp)
	if ts <= 0 {
		return 0
	}
	if ts > 1e11 {
		return ts / 1000
	}
	return ts
}

// succeeded 把 Data API 的 status 映射成 Row 契约的 "1"/"0"。
//
// 已知的成功取值：success/confirmed/finalized/ok/true；失败：failed/error/reverted/false。
// 未知取值按 "0" 处理并在 chainPayloadJson 里保留原始 status——
// 契约只允许 1|0，未知状态不能声称为成功。
func (tx dataAPITx) succeeded() string {
	switch normalizeStatus(tx.Status) {
	case "success", "succeeded", "confirmed", "finalized", "ok", "true":
		return "1"
	case "":
		// status 缺失时才用 success 布尔；status 明确失败时不以 success 覆盖。
		if tx.Success != nil && *tx.Success {
			return "1"
		}
		return "0"
	default:
		return "0"
	}
}

// ---- 规范 §3 冻结的 Row 契约 ----

// txRow 是 transactions 数据集的行（Ankr 同构，供 ingest.NormalizeTransaction 消费）。
type txRow struct {
	BlockNumber string `json:"blockNumber"`
	BlockHash   string `json:"blockHash"`
	Hash        string `json:"hash"`
	Timestamp   int64  `json:"timestamp"`
	From        string `json:"from"`
	To          string `json:"to"`
	Value       string `json:"value"`
	Nonce       string `json:"nonce"`
	GasLimit    string `json:"gasLimit"`
	GasUsed     string `json:"gasUsed"`
	GasPrice    string `json:"gasPrice"`
	Type        string `json:"type"`
	Status      string `json:"status"`
	Input       string `json:"input"`
	FeeRaw      string `json:"feeRaw"`

	// Data API 不返回签名者账户表；这里给空数组而不是伪造 accountKeys。
	Signers     []string `json:"signers"`
	AccountKeys []string `json:"accountKeys"`
	ProgramIDs  []string `json:"programIds"`

	SourceAPI       string `json:"sourceApi"`
	SourceMethod    string `json:"sourceMethod"`
	SourceRequestID string `json:"sourceRequestId"`
	// ChainPayloadJSON 保存无法落到固定列的结构化扩展字段，不代替完整 Raw 响应。
	ChainPayloadJSON string `json:"chainPayloadJson"`
}

// tokenTransferRow 是 token_transfers 数据集的行。
type tokenTransferRow struct {
	BlockHeight     string `json:"blockHeight"`
	Timestamp       int64  `json:"timestamp"`
	TransactionHash string `json:"transactionHash"`
	// LogIndex 对 Solana 是**本地稳定事件序号**（页内/交易内确定，重放同一页结果一致），
	// 不是 EVM log index（开发文档 §8.3）。
	LogIndex        uint32 `json:"logIndex"`
	ContractAddress string `json:"contractAddress"`
	// TokenSymbol 按 §3 契约留空：符号由 tokenmeta 元数据管线补全，
	// Provider 只保证 decimals（§8.3 也只要求 Provider 返回 decimals）。
	TokenSymbol   string `json:"tokenSymbol"`
	TokenDecimals uint16 `json:"tokenDecimals"`
	FromAddress   string `json:"fromAddress"`
	ToAddress     string `json:"toAddress"`
	// ValueRawInteger / Value 同为 Provider 返回的原始整数串（amount_string），
	// 不做本地浮点换算，避免精度损失。
	ValueRawInteger string `json:"valueRawInteger"`
	Value           string `json:"value"`
	// Data API 只给 owner，不给代币账户；留空而不是把 owner 冒充 ATA（ADR-023 D10）。
	FromTokenAccount string `json:"fromTokenAccount"`
	ToTokenAccount   string `json:"toTokenAccount"`
	// 真实指令位置：Data API 不返回，写 0 / -1（-1 = 非内层指令，与 ClickHouse 列默认值一致），
	// 唯一性由 (signature, transfer 本地序号, mint) 保证（§9.3 幂等键）。
	InstructionIndex      uint32 `json:"instructionIndex"`
	InnerInstructionIndex int32  `json:"innerInstructionIndex"`
	SourceAPI             string `json:"sourceApi"`
	SourceRequestID       string `json:"sourceRequestId"`
}

// nftTransferRow 是 nft_transfers 数据集的行。
type nftTransferRow struct {
	BlockNumber     string `json:"blockNumber"`
	Timestamp       int64  `json:"timestamp"`
	TransactionHash string `json:"transactionHash"`
	LogIndex        uint32 `json:"logIndex"`
	ContractAddress string `json:"contractAddress"`
	FromAddress     string `json:"fromAddress"`
	ToAddress       string `json:"toAddress"`
	// NFT 是不可分割的，数量恒为 1（§3 契约）。
	Value string `json:"value"`
	// Solana 没有 ERC 标准：经典 NFT 由 SPL Token / Token-2022 承载，如实标注 SPL。
	TokenStandard   string `json:"tokenStandard"`
	SourceAPI       string `json:"sourceApi"`
	SourceRequestID string `json:"sourceRequestId"`
}

// programLogRow 是 logs 数据集的行（供 solana.NormalizeProgramLog 消费）。
type programLogRow struct {
	TransactionHash string `json:"transactionHash"`
	BlockNumber     string `json:"blockNumber"`
	Timestamp       int64  `json:"timestamp"`
	LogIndex        uint32 `json:"logIndex"`
	Address         string `json:"address"`
	Data            string `json:"data"`
	EventSource     string `json:"eventSource"`
	// ProgramID 留空：地址已经在 address 字段，Program 归属由 invoke 栈推出（§3 契约同此）。
	ProgramID       string `json:"programId"`
	InvokeDepth     uint16 `json:"invokeDepth"`
	Success         bool   `json:"success"`
	Removed         bool   `json:"removed"`
	SourceAPI       string `json:"sourceApi"`
	SourceRequestID string `json:"sourceRequestId"`
}

// balanceRow 是 balances 数据集的行（供 solana.NormalizeBalance 消费）。
type balanceRow struct {
	HolderAddress   string `json:"holderAddress"`
	ContractAddress string `json:"contractAddress"`
	// native | spl | spl-token-2022（§3 契约的小写取值）。
	TokenType         string `json:"tokenType"`
	TokenDecimals     uint16 `json:"tokenDecimals"`
	BalanceRawInteger string `json:"balanceRawInteger"`
	SnapshotBlock     string `json:"snapshotBlock"`
	Timestamp         int64  `json:"timestamp"`
}

// channelForDataset 把数据集映射到通道与操作类别（规范 §1.4 Channel 语义 + §1.2 路由表）。
func channelForDataset(dataset string) (channel, operation string, ok bool) {
	switch dataset {
	case DatasetTransactions, DatasetTokenTransfers, DatasetNFTTransfers:
		return ChannelDataAPI, OperationSolanaHistory, true
	case DatasetLogs:
		return ChannelSolanaRPC, OperationSolanaDetail, true
	case DatasetBalances:
		return ChannelSolanaRPC, OperationSolanaBalance, true
	default:
		return "", "", false
	}
}

func normalizeStatus(status string) string {
	out := make([]byte, 0, len(status))
	for i := 0; i < len(status); i++ {
		c := status[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out = append(out, c)
	}
	return string(out)
}

func encodeRows[T any](rows []T) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(rows))
	for _, row := range rows {
		payload, err := json.Marshal(row)
		if err != nil {
			// 单行编码失败不阻断整页：结构体全部是基础类型，实际不会发生。
			continue
		}
		out = append(out, payload)
	}
	return out
}
