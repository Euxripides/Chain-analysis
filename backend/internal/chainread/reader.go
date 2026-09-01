package chainread

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
)

// Service 类型化链读取服务（§149）。所有方法最终都走 RPCGateway。
type Service struct {
	gateway *rpc.Gateway
	batcher *rpc.Batcher
	chains  *chain.Registry
}

// New 构造服务。batcher 可为 nil（不启用自动聚合）。
func New(gateway *rpc.Gateway, batcher *rpc.Batcher) *Service {
	return &Service{gateway: gateway, batcher: batcher, chains: gateway.Chains()}
}

// Gateway 暴露给需要能力/健康信息的上层（例如系统能力接口）。
func (s *Service) Gateway() *rpc.Gateway { return s.gateway }

// Chains 链注册表。
func (s *Service) Chains() *chain.Registry { return s.chains }

func (s *Service) call(ctx context.Context, req rpc.Request) (rpc.Response, error) {
	return s.gateway.Call(ctx, req)
}

// batched 走自动聚合（后台批量补全用，§44）；未配置时退化为直接调用。
func (s *Service) batched(ctx context.Context, req rpc.Request) (rpc.Response, error) {
	if s.batcher == nil {
		return s.gateway.Call(ctx, req)
	}
	return s.batcher.Call(ctx, req)
}

// BlockNumber 链头高度。
func (s *Service) BlockNumber(ctx context.Context, chainSlug string, opt Options) (uint64, bool, error) {
	response, err := s.call(ctx, opt.request(chainSlug, "eth_blockNumber", []any{},
		rpc.ClassChainHead, chain.Latest(), rpc.PurposeChainHead))
	if err != nil {
		return 0, false, err
	}
	value, err := decodeHexString(response.Result)
	if err != nil {
		return 0, response.CacheHit, err
	}
	head, err := chain.HexToUint64(value)
	return head, response.CacheHit, err
}

// Balance 原生余额（§85）。
func (s *Service) Balance(ctx context.Context, chainSlug string, address chain.Address,
	ref chain.BlockRef, opt Options) (*big.Int, error) {
	response, err := s.call(ctx, opt.request(chainSlug, "eth_getBalance",
		[]any{string(address), ref.String()}, rpc.ClassBalance, ref, rpc.PurposeBalance))
	if err != nil {
		return nil, err
	}
	value, err := decodeHexString(response.Result)
	if err != nil {
		return nil, err
	}
	return chain.HexToBigInt(value)
}

// Code 合约字节码（§75）。
func (s *Service) Code(ctx context.Context, chainSlug string, address chain.Address,
	ref chain.BlockRef, opt Options) ([]byte, error) {
	response, err := s.call(ctx, opt.request(chainSlug, "eth_getCode",
		[]any{string(address), ref.String()}, rpc.ClassCode, ref, rpc.PurposeContractClassification))
	if err != nil {
		return nil, err
	}
	value, err := decodeHexString(response.Result)
	if err != nil {
		return nil, err
	}
	return chain.HexToBytes(value)
}

// StorageAt 合约存储槽（Proxy 探针用，§82）。
func (s *Service) StorageAt(ctx context.Context, chainSlug string, address chain.Address,
	slot string, ref chain.BlockRef, opt Options) ([]byte, error) {
	response, err := s.call(ctx, opt.request(chainSlug, "eth_getStorageAt",
		[]any{string(address), slot, ref.String()}, rpc.ClassStorage, ref, rpc.PurposeContractClassification))
	if err != nil {
		return nil, err
	}
	value, err := decodeHexString(response.Result)
	if err != nil {
		return nil, err
	}
	return chain.HexToBytes(value)
}

// CallContract 只读合约调用（§73）。
func (s *Service) CallContract(ctx context.Context, call ContractCall, opt Options) ([]byte, error) {
	req := opt.request(call.Chain, "eth_call", callParams(call), rpc.ClassContractRead,
		call.BlockRef, rpc.PurposeContractClassification)
	response, err := s.call(ctx, req)
	if err != nil {
		return nil, err
	}
	value, err := decodeHexString(response.Result)
	if err != nil {
		return nil, err
	}
	return chain.HexToBytes(value)
}

// CallContractBatched 与 CallContract 相同，但允许被自动聚合（§44）。
func (s *Service) CallContractBatched(ctx context.Context, call ContractCall, opt Options) ([]byte, error) {
	req := opt.request(call.Chain, "eth_call", callParams(call), rpc.ClassContractRead,
		call.BlockRef, rpc.PurposeContractClassification)
	response, err := s.batched(ctx, req)
	if err != nil {
		return nil, err
	}
	value, err := decodeHexString(response.Result)
	if err != nil {
		return nil, err
	}
	return chain.HexToBytes(value)
}

// BatchCallContract 批量只读调用（§157）：单项失败不影响其它项。
func (s *Service) BatchCallContract(ctx context.Context, calls []ContractCall, keys []string, opt Options) ([]CallResult, error) {
	if len(calls) == 0 {
		return nil, nil
	}
	reqs := make([]rpc.Request, len(calls))
	for i, call := range calls {
		reqs[i] = opt.request(call.Chain, "eth_call", callParams(call), rpc.ClassContractRead,
			call.BlockRef, rpc.PurposeContractClassification)
	}
	responses, err := s.gateway.BatchCall(ctx, reqs)
	if err != nil && responses == nil {
		return nil, err
	}
	out := make([]CallResult, len(calls))
	for i := range calls {
		result := CallResult{}
		if i < len(keys) {
			result.Key = keys[i]
		}
		switch {
		case i >= len(responses):
			result.Err = fmt.Errorf("no response for call %d", i)
		case responses[i].Error != nil:
			result.Err = responses[i].Error
		default:
			value, decodeErr := decodeHexString(responses[i].Result)
			if decodeErr != nil {
				result.Err = decodeErr
				break
			}
			output, decodeErr := chain.HexToBytes(value)
			if decodeErr != nil {
				result.Err = decodeErr
				break
			}
			result.Output = output
		}
		out[i] = result
	}
	return out, nil
}

// Transaction 单笔交易（§90）。
func (s *Service) Transaction(ctx context.Context, chainSlug string, hash chain.Hash, opt Options) (*Transaction, error) {
	response, err := s.call(ctx, opt.request(chainSlug, "eth_getTransactionByHash",
		[]any{string(hash)}, rpc.ClassTransaction, chain.Latest(), rpc.PurposeTransactionVerify))
	if err != nil {
		return nil, err
	}
	if response.IsNull() {
		return nil, nil
	}
	return parseTransaction(response.Result)
}

// Receipt 交易回执（§90/§94）。
func (s *Service) Receipt(ctx context.Context, chainSlug string, hash chain.Hash, opt Options) (*Receipt, error) {
	response, err := s.call(ctx, opt.request(chainSlug, "eth_getTransactionReceipt",
		[]any{string(hash)}, rpc.ClassReceipt, chain.Latest(), rpc.PurposeReceipt))
	if err != nil {
		return nil, err
	}
	if response.IsNull() {
		return nil, nil
	}
	return parseReceipt(response.Result)
}

// Block 区块（full=false 只取交易哈希）。
func (s *Service) Block(ctx context.Context, chainSlug string, ref chain.BlockRef, full bool, opt Options) (*Block, error) {
	method := "eth_getBlockByNumber"
	params := []any{ref.String(), full}
	response, err := s.call(ctx, opt.request(chainSlug, method, params, rpc.ClassBlock, ref, rpc.PurposeTransactionVerify))
	if err != nil {
		return nil, err
	}
	if response.IsNull() {
		return nil, nil
	}
	return parseBlock(response.Result)
}

// ---- JSON 解析（hex → 强类型，§154）----

func callParams(call ContractCall) []any {
	args := map[string]any{"to": string(call.To), "data": chain.BytesToHex(call.Data)}
	if call.From != "" {
		args["from"] = string(call.From)
	}
	ref := call.BlockRef
	if ref.IsZero() {
		ref = chain.Latest()
	}
	return []any{args, ref.String()}
}

func decodeHexString(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", fmt.Errorf("empty rpc result")
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("rpc result is not a hex string")
	}
	return value, nil
}

type rawTransaction struct {
	Hash             string  `json:"hash"`
	From             string  `json:"from"`
	To               *string `json:"to"`
	Value            string  `json:"value"`
	Input            string  `json:"input"`
	Nonce            string  `json:"nonce"`
	Gas              string  `json:"gas"`
	GasPrice         string  `json:"gasPrice"`
	BlockNumber      *string `json:"blockNumber"`
	BlockHash        *string `json:"blockHash"`
	TransactionIndex *string `json:"transactionIndex"`
	Type             string  `json:"type"`
}

func parseTransaction(raw json.RawMessage) (*Transaction, error) {
	var item rawTransaction
	if err := json.Unmarshal(raw, &item); err != nil {
		return nil, fmt.Errorf("invalid transaction payload")
	}
	tx := &Transaction{Input: item.Input, Type: item.Type}
	hash, err := chain.ParseHash(item.Hash)
	if err != nil {
		return nil, err
	}
	tx.Hash = hash
	if from, err := chain.ParseAddress(item.From); err == nil {
		tx.From = from
	}
	if item.To != nil && *item.To != "" {
		if to, err := chain.ParseAddress(*item.To); err == nil {
			tx.To = &to
		}
	}
	value, err := chain.HexToBigInt(item.Value)
	if err != nil {
		value = big.NewInt(0)
	}
	tx.Value = value
	tx.ValueRaw = value.String()
	tx.Nonce, _ = chain.HexToUint64(item.Nonce)
	tx.Gas, _ = chain.HexToUint64(item.Gas)
	if gasPrice, err := chain.HexToBigInt(item.GasPrice); err == nil {
		tx.GasPrice = gasPrice.String()
	}
	if item.BlockNumber != nil {
		if n, err := chain.HexToUint64(*item.BlockNumber); err == nil {
			tx.BlockNumber = &n
		}
	}
	if item.BlockHash != nil {
		tx.BlockHash = strings.ToLower(*item.BlockHash)
	}
	if item.TransactionIndex != nil {
		if n, err := chain.HexToUint64(*item.TransactionIndex); err == nil {
			tx.TransactionIndex = &n
		}
	}
	return tx, nil
}

type rawLog struct {
	Address     string   `json:"address"`
	Topics      []string `json:"topics"`
	Data        string   `json:"data"`
	BlockNumber string   `json:"blockNumber"`
	TxHash      string   `json:"transactionHash"`
	LogIndex    string   `json:"logIndex"`
	Removed     bool     `json:"removed"`
}

type rawReceipt struct {
	TxHash            string   `json:"transactionHash"`
	Status            *string  `json:"status"`
	BlockNumber       *string  `json:"blockNumber"`
	BlockHash         *string  `json:"blockHash"`
	From              string   `json:"from"`
	To                *string  `json:"to"`
	ContractAddress   *string  `json:"contractAddress"`
	GasUsed           string   `json:"gasUsed"`
	CumulativeGasUsed string   `json:"cumulativeGasUsed"`
	EffectiveGasPrice string   `json:"effectiveGasPrice"`
	Logs              []rawLog `json:"logs"`
}

func parseReceipt(raw json.RawMessage) (*Receipt, error) {
	var item rawReceipt
	if err := json.Unmarshal(raw, &item); err != nil {
		return nil, fmt.Errorf("invalid receipt payload")
	}
	receipt := &Receipt{}
	if hash, err := chain.ParseHash(item.TxHash); err == nil {
		receipt.TxHash = hash
	}
	if item.Status != nil {
		if status, err := chain.HexToUint64(*item.Status); err == nil {
			receipt.Status = &status
		}
	}
	if item.BlockNumber != nil {
		if n, err := chain.HexToUint64(*item.BlockNumber); err == nil {
			receipt.BlockNumber = &n
		}
	}
	if item.BlockHash != nil {
		receipt.BlockHash = strings.ToLower(*item.BlockHash)
	}
	if from, err := chain.ParseAddress(item.From); err == nil {
		receipt.From = from
	}
	if item.To != nil && *item.To != "" {
		if to, err := chain.ParseAddress(*item.To); err == nil {
			receipt.To = &to
		}
	}
	if item.ContractAddress != nil && *item.ContractAddress != "" {
		if created, err := chain.ParseAddress(*item.ContractAddress); err == nil {
			receipt.ContractAddress = &created
		}
	}
	receipt.GasUsed, _ = chain.HexToUint64(item.GasUsed)
	receipt.CumulativeGasUsed, _ = chain.HexToUint64(item.CumulativeGasUsed)
	if price, err := chain.HexToBigInt(item.EffectiveGasPrice); err == nil {
		receipt.EffectiveGasPrice = price.String()
	}
	receipt.Logs = convertLogs(item.Logs)
	return receipt, nil
}

func convertLogs(items []rawLog) []Log {
	out := make([]Log, 0, len(items))
	for _, item := range items {
		log := Log{Topics: item.Topics, Data: item.Data, Removed: item.Removed}
		if address, err := chain.ParseAddress(item.Address); err == nil {
			log.Address = address
		}
		if hash, err := chain.ParseHash(item.TxHash); err == nil {
			log.TxHash = hash
		}
		log.BlockNumber, _ = chain.HexToUint64(item.BlockNumber)
		log.LogIndex, _ = chain.HexToUint64(item.LogIndex)
		out = append(out, log)
	}
	return out
}

type rawBlock struct {
	Number       string          `json:"number"`
	Hash         string          `json:"hash"`
	ParentHash   string          `json:"parentHash"`
	Timestamp    string          `json:"timestamp"`
	GasUsed      string          `json:"gasUsed"`
	GasLimit     string          `json:"gasLimit"`
	Transactions json.RawMessage `json:"transactions"`
}

func parseBlock(raw json.RawMessage) (*Block, error) {
	var item rawBlock
	if err := json.Unmarshal(raw, &item); err != nil {
		return nil, fmt.Errorf("invalid block payload")
	}
	block := &Block{Hash: strings.ToLower(item.Hash), ParentHash: strings.ToLower(item.ParentHash)}
	block.Number, _ = chain.HexToUint64(item.Number)
	block.Timestamp, _ = chain.HexToUint64(item.Timestamp)
	block.GasUsed, _ = chain.HexToUint64(item.GasUsed)
	block.GasLimit, _ = chain.HexToUint64(item.GasLimit)
	var hashes []string
	if err := json.Unmarshal(item.Transactions, &hashes); err == nil {
		block.Transactions = hashes
	}
	return block, nil
}

// nowUTC 便于测试替换。
var nowUTC = func() time.Time { return time.Now().UTC() }

// jsonUnmarshal 局部别名，便于其它文件复用同一份解析语义。
func jsonUnmarshal(raw json.RawMessage, dest any) error { return json.Unmarshal(raw, dest) }
