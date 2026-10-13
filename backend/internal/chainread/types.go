// Package chainread：类型化链读取服务（V1.5 §13/§14/§149-§151）。
//
// 业务模块只使用本包，不构造 JSON-RPC、不接触 Provider、不持有 API Key。
// 所有 hex → uint64/big.Int/Address/Hash 的转换在这里完成，业务层不再重复 parse（§154）。
package chainread

import (
	"math/big"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
)

// Options 每次调用的策略（用途、优先级、一致性、预算）。
type Options struct {
	// TransactionTTL only changes an explicitly requested interactive tx lookup.
	// Other callers keep the central method registry's cache policy.
	TransactionTTL time.Duration
	Purpose        string
	Priority       rpc.Priority
	Consistency    rpc.Consistency
	Budget         rpc.BudgetContext
	TraceID        string
}

// WithPurpose 便捷构造。
func WithPurpose(purpose string) Options { return Options{Purpose: purpose} }

func (o Options) budget(defaultPurpose string) rpc.BudgetContext {
	budget := o.Budget
	if budget.Purpose == "" {
		budget.Purpose = o.Purpose
	}
	if budget.Purpose == "" {
		budget.Purpose = defaultPurpose
	}
	if budget.Priority == "" {
		budget.Priority = o.Priority.OrDefault()
	}
	return budget
}

func (o Options) request(chainSlug, method string, params []any, class rpc.MethodClass,
	ref chain.BlockRef, defaultPurpose string) rpc.Request {
	return rpc.Request{
		Chain: chainSlug, Method: method, Params: params, Class: class,
		Priority: o.Priority.OrDefault(), Consistency: o.Consistency.OrDefault(),
		Budget: o.budget(defaultPurpose), BlockRef: ref, TraceID: o.TraceID,
	}
}

// HeadInfo 链头信息（§70）。
type HeadInfo struct {
	Chain     string    `json:"chain"`
	HeadBlock uint64    `json:"head_block"`
	SafeBlock uint64    `json:"safe_block"`
	FetchedAt time.Time `json:"fetched_at"`
	Provider  string    `json:"provider,omitempty"`
	CacheHit  bool      `json:"cache_hit"`
}

// Transaction 链上交易（数值一律 big.Int / string，§155）。
type Transaction struct {
	Hash             chain.Hash     `json:"hash"`
	From             chain.Address  `json:"from"`
	To               *chain.Address `json:"to"`
	Value            *big.Int       `json:"-"`
	ValueRaw         string         `json:"value"`
	Input            string         `json:"input"`
	Nonce            uint64         `json:"nonce"`
	Gas              uint64         `json:"gas"`
	GasPrice         string         `json:"gas_price"`
	BlockNumber      *uint64        `json:"block_number"`
	BlockHash        string         `json:"block_hash,omitempty"`
	TransactionIndex *uint64        `json:"transaction_index"`
	Type             string         `json:"type,omitempty"`
}

// Receipt 交易回执。
type Receipt struct {
	TxHash            chain.Hash     `json:"tx_hash"`
	Status            *uint64        `json:"status"`
	BlockNumber       *uint64        `json:"block_number"`
	BlockHash         string         `json:"block_hash,omitempty"`
	From              chain.Address  `json:"from"`
	To                *chain.Address `json:"to"`
	ContractAddress   *chain.Address `json:"contract_address"`
	GasUsed           uint64         `json:"gas_used"`
	CumulativeGasUsed uint64         `json:"cumulative_gas_used"`
	EffectiveGasPrice string         `json:"effective_gas_price,omitempty"`
	Logs              []Log          `json:"logs"`
}

// Log 事件日志。
type Log struct {
	Address     chain.Address `json:"address"`
	Topics      []string      `json:"topics"`
	Data        string        `json:"data"`
	BlockNumber uint64        `json:"block_number"`
	TxHash      chain.Hash    `json:"tx_hash"`
	LogIndex    uint64        `json:"log_index"`
	Removed     bool          `json:"removed"`
}

// Block 区块头（含可选交易哈希列表）。
type Block struct {
	Number       uint64   `json:"number"`
	Hash         string   `json:"hash"`
	ParentHash   string   `json:"parent_hash"`
	Timestamp    uint64   `json:"timestamp"`
	GasUsed      uint64   `json:"gas_used"`
	GasLimit     uint64   `json:"gas_limit"`
	Transactions []string `json:"transactions,omitempty"`
}

// ContractCall 只读合约调用（§73）。
type ContractCall struct {
	Chain    string
	To       chain.Address
	From     chain.Address
	Data     []byte
	BlockRef chain.BlockRef
}

// CallResult 批量合约读取的单项结果（§157：逐项成功/失败）。
type CallResult struct {
	Key    string
	Output []byte
	Err    error
}

// LogFilter eth_getLogs 过滤条件。
// 注意：address 语义是"发出事件的合约"，不是"任意钱包参与"（§96）。
type LogFilter struct {
	FromBlock uint64
	ToBlock   uint64
	Addresses []chain.Address
	Topics    []any
}

// AddressType 地址类型（§76）。
type AddressType string

const (
	AddressTypeEOA      AddressType = "eoa"
	AddressTypeContract AddressType = "contract"
	AddressTypeUnknown  AddressType = "unknown"
)

// AddressInfo 地址运行时画像（§75/§200）。
type AddressInfo struct {
	Chain     string        `json:"chain"`
	Address   chain.Address `json:"address"`
	Type      AddressType   `json:"address_type"`
	CodeSize  int           `json:"code_size"`
	BlockRef  string        `json:"block_ref"`
	FetchedAt time.Time     `json:"fetched_at"`
}

// TokenBalance 代币余额。
type TokenBalance struct {
	Token   chain.Address `json:"token"`
	Balance *big.Int      `json:"-"`
	Raw     string        `json:"balance"`
	Err     string        `json:"error,omitempty"`
}

// TokenMetadata 标准合约元数据（§74）。
type TokenMetadata struct {
	Contract      chain.Address `json:"contract"`
	Name          *string       `json:"name"`
	Symbol        *string       `json:"symbol"`
	Decimals      *int          `json:"decimals"`
	NameError     string        `json:"name_error,omitempty"`
	SymbolError   string        `json:"symbol_error,omitempty"`
	DecimalsError string        `json:"decimals_error,omitempty"`
}
