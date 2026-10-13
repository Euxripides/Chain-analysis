package chainread

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
	"github.com/shopspring/decimal"
)

var InvestigationRPCMethods = map[string]bool{
	"eth_call": true, "eth_getCode": true, "eth_getStorageAt": true,
	"eth_getTransactionByHash": true, "eth_getTransactionReceipt": true, "eth_getLogs": true,
	"eth_getBalance": true, "eth_blockNumber": true, "eth_getBlockByNumber": true,
}

// ReadRPC is the bounded public read surface. It always uses the existing
// gateway; callers cannot supply an endpoint, credentials, or state overrides.
func (s *Service) ReadRPC(ctx context.Context, slug, method string, params []any, opt Options) (rpc.Response, error) {
	cfg, err := s.chains.Require(slug)
	if err != nil {
		return rpc.Response{}, err
	}
	if !cfg.IsEVM() {
		return rpc.Response{}, fmt.Errorf("standard EVM RPC requires an EVM chain")
	}
	normalized, ref, err := ValidateInvestigationRPC(cfg, method, params)
	if err != nil {
		return rpc.Response{}, err
	}
	result, err := s.gateway.Call(ctx, opt.request(slug, method, normalized, "", ref, rpc.PurposeInvestigation))
	if err == nil && len(result.Result) > 8<<20 {
		return rpc.Response{}, fmt.Errorf("RPC result exceeds 8 MiB; narrow the query")
	}
	return result, err
}

func strictRPCData(v any, max int) (string, error) {
	str, ok := v.(string)
	if !ok || !strings.HasPrefix(str, "0x") || len(str)%2 != 0 || len(str)/2-1 > max {
		return "", fmt.Errorf("invalid bounded hex data")
	}
	if _, err := hex.DecodeString(str[2:]); err != nil {
		return "", fmt.Errorf("invalid hex data")
	}
	return str, nil
}

func rpcQuantity(v any) (*big.Int, error) {
	str, ok := v.(string)
	if !ok || !strings.HasPrefix(str, "0x") || len(str) < 3 || len(str) > 66 {
		return nil, fmt.Errorf("quantity must be a hex string")
	}
	n, ok := new(big.Int).SetString(str[2:], 16)
	if !ok || n.Sign() < 0 {
		return nil, fmt.Errorf("invalid quantity")
	}
	return n, nil
}

func ValidateInvestigationRPC(cfg chain.Config, method string, params []any) ([]any, chain.BlockRef, error) {
	if !InvestigationRPCMethods[method] {
		return nil, chain.BlockRef{}, fmt.Errorf("RPC method is not on the investigation read allowlist")
	}
	p := append([]any(nil), params...)
	ref := chain.Latest()
	expected := map[string]int{"eth_call": 2, "eth_getCode": 2, "eth_getStorageAt": 3, "eth_getTransactionByHash": 1, "eth_getTransactionReceipt": 1, "eth_getLogs": 1, "eth_getBalance": 2, "eth_blockNumber": 0, "eth_getBlockByNumber": 2}[method]
	if len(p) != expected {
		return nil, ref, fmt.Errorf("%s requires %d parameters", method, expected)
	}
	blockIndex := map[string]int{"eth_call": 1, "eth_getCode": 1, "eth_getStorageAt": 2, "eth_getBalance": 1, "eth_getBlockByNumber": 0}
	if i, ok := blockIndex[method]; ok {
		value, ok := p[i].(string)
		if !ok {
			return nil, ref, fmt.Errorf("blockTag must be a string")
		}
		var err error
		ref, err = chain.ParseBlockRef(value)
		if err != nil {
			return nil, ref, err
		}
		if !ref.IsFixed() && !cfg.SupportsTag(ref.Tag) {
			return nil, ref, fmt.Errorf("unsupported blockTag")
		}
		p[i] = ref.String()
	}
	switch method {
	case "eth_getBalance", "eth_getCode", "eth_getStorageAt":
		value, ok := p[0].(string)
		if !ok {
			return nil, ref, fmt.Errorf("address must be a string")
		}
		a, err := chain.ParseAddress(value)
		if err != nil {
			return nil, ref, err
		}
		p[0] = a.String()
		if method == "eth_getStorageAt" {
			slot, err := rpcQuantity(p[1])
			if err != nil {
				return nil, ref, err
			}
			p[1] = "0x" + slot.Text(16)
		}
	case "eth_getTransactionByHash", "eth_getTransactionReceipt":
		value, ok := p[0].(string)
		if !ok {
			return nil, ref, fmt.Errorf("hash must be a string")
		}
		h, err := chain.ParseHash(value)
		if err != nil {
			return nil, ref, err
		}
		p[0] = h.String()
	case "eth_getBlockByNumber":
		if _, ok := p[1].(bool); !ok {
			return nil, ref, fmt.Errorf("full_transactions must be boolean")
		}
	case "eth_call":
		obj, ok := p[0].(map[string]any)
		if !ok {
			return nil, ref, fmt.Errorf("call must be an object")
		}
		call := map[string]any{}
		for k, v := range obj {
			switch k {
			case "to", "from":
				str, ok := v.(string)
				if !ok {
					return nil, ref, fmt.Errorf("call address must be a string")
				}
				a, err := chain.ParseAddress(str)
				if err != nil {
					return nil, ref, err
				}
				call[k] = a.String()
			case "data", "input":
				str, err := strictRPCData(v, 128<<10)
				if err != nil {
					return nil, ref, err
				}
				call[k] = str
			case "gas":
				n, err := rpcQuantity(v)
				if err != nil || !n.IsUint64() || n.Uint64() > 10000000 {
					return nil, ref, fmt.Errorf("eth_call gas must not exceed 10000000")
				}
				call[k] = "0x" + n.Text(16)
			case "value":
				n, err := rpcQuantity(v)
				if err != nil || n.Sign() != 0 {
					return nil, ref, fmt.Errorf("investigation eth_call only accepts zero value")
				}
				call[k] = "0x0"
			default:
				return nil, ref, fmt.Errorf("unsupported eth_call field")
			}
		}
		if call["to"] == nil {
			return nil, ref, fmt.Errorf("eth_call requires to")
		}
		if call["data"] != nil && call["input"] != nil && call["data"] != call["input"] {
			return nil, ref, fmt.Errorf("data and input disagree")
		}
		if call["gas"] == nil {
			call["gas"] = "0x989680"
		}
		p[0] = call
	case "eth_getLogs":
		obj, ok := p[0].(map[string]any)
		if !ok {
			return nil, ref, fmt.Errorf("log filter must be an object")
		}
		filter := map[string]any{}
		for k, v := range obj {
			if k != "address" && k != "topics" && k != "fromBlock" && k != "toBlock" && k != "blockHash" {
				return nil, ref, fmt.Errorf("unsupported log filter field")
			}
			filter[k] = v
		}
		if filter["blockHash"] != nil {
			if filter["fromBlock"] != nil || filter["toBlock"] != nil {
				return nil, ref, fmt.Errorf("blockHash cannot be combined with a range")
			}
			h, err := chain.ParseHash(fmt.Sprint(filter["blockHash"]))
			if err != nil {
				return nil, ref, err
			}
			filter["blockHash"] = h.String()
		} else {
			from, err := chain.ParseBlockRef(fmt.Sprint(filter["fromBlock"]))
			if err != nil || !from.IsFixed() {
				return nil, ref, fmt.Errorf("logs require an explicit fromBlock")
			}
			to, err := chain.ParseBlockRef(fmt.Sprint(filter["toBlock"]))
			if err != nil || !to.IsFixed() || *to.Number < *from.Number {
				return nil, ref, fmt.Errorf("logs require an explicit valid toBlock")
			}
			limit := cfg.MaxLogBlockRange
			if limit == 0 {
				limit = 2000
			}
			if *to.Number-*from.Number >= limit {
				return nil, ref, fmt.Errorf("logs range exceeds %d blocks", limit)
			}
			filter["fromBlock"] = from.String()
			filter["toBlock"] = to.String()
			ref = from
		}
		if value, exists := filter["address"]; exists {
			values := []any{value}
			if many, ok := value.([]any); ok {
				values = many
			}
			if len(values) == 0 || len(values) > 20 {
				return nil, ref, fmt.Errorf("logs accept 1-20 contract addresses")
			}
			out := []string{}
			for _, v := range values {
				a, err := chain.ParseAddress(fmt.Sprint(v))
				if err != nil {
					return nil, ref, err
				}
				out = append(out, a.String())
			}
			if len(out) == 1 {
				filter["address"] = out[0]
			} else {
				filter["address"] = out
			}
		}
		if value, exists := filter["topics"]; exists {
			topics, ok := value.([]any)
			if !ok || len(topics) > 4 {
				return nil, ref, fmt.Errorf("logs accept at most four topic positions")
			}
			for _, topic := range topics {
				if topic == nil {
					continue
				}
				choices := []any{topic}
				if many, ok := topic.([]any); ok {
					choices = many
				}
				if len(choices) > 20 {
					return nil, ref, fmt.Errorf("too many topic alternatives")
				}
				for _, v := range choices {
					if v == nil {
						continue
					}
					if _, err := chain.ParseHash(fmt.Sprint(v)); err != nil {
						return nil, ref, err
					}
				}
			}
		}
		p[0] = filter
	}
	return p, ref, nil
}

type StatePoint struct {
	Block         uint64         `json:"as_of_block"`
	Hash          string         `json:"as_of_block_hash"`
	Time          time.Time      `json:"as_of_time"`
	Current       bool           `json:"is_current_balance"`
	RequestedTime *time.Time     `json:"requested_time,omitempty"`
	Ref           chain.BlockRef `json:"-"`
}

// ResolveState pins every asset in a snapshot to one actual block. Timestamp
// lookup selects the last block at or before the requested time, never after it.
func (s *Service) ResolveState(ctx context.Context, slug string, ref chain.BlockRef, at *time.Time, opt Options) (StatePoint, error) {
	var point StatePoint
	cfg, err := s.chains.Require(slug)
	if err != nil || !cfg.IsEVM() {
		return point, fmt.Errorf("archive state snapshots require an EVM chain")
	}
	if ref.Tag == chain.TagPending {
		return point, fmt.Errorf("pending state cannot provide a confirmed as_of_block")
	}
	requestedCurrent := at == nil && ref.IsLatest()
	block, err := s.Block(ctx, slug, ref, false, opt)
	if err != nil {
		return point, err
	}
	if block == nil {
		return point, fmt.Errorf("block not found")
	}
	if at != nil {
		if at.Unix() > int64(block.Timestamp) {
			return point, fmt.Errorf("timestamp is newer than the available chain head")
		}
		genesis, err := s.Block(ctx, slug, chain.AtBlock(0), false, opt)
		if err != nil {
			return point, err
		}
		if genesis == nil || at.Unix() < int64(genesis.Timestamp) {
			return point, fmt.Errorf("timestamp predates chain genesis")
		}
		low, high := uint64(0), block.Number
		for low < high {
			mid := low + (high-low+1)/2
			candidate, e := s.Block(ctx, slug, chain.AtBlock(mid), false, opt)
			if e != nil {
				return point, e
			}
			if candidate == nil {
				return point, fmt.Errorf("historical block unavailable")
			}
			if int64(candidate.Timestamp) <= at.Unix() {
				low = mid
			} else {
				high = mid - 1
			}
		}
		block, err = s.Block(ctx, slug, chain.AtBlock(low), false, opt)
		if err != nil || block == nil {
			return point, fmt.Errorf("historical block unavailable")
		}
	}
	point = StatePoint{Block: block.Number, Hash: block.Hash, Time: time.Unix(int64(block.Timestamp), 0).UTC(), Current: requestedCurrent, RequestedTime: at, Ref: chain.AtBlock(block.Number)}
	return point, nil
}

type BalanceSnapshot struct {
	Chain    string  `json:"chain"`
	Address  string  `json:"address"`
	Token    string  `json:"token_contract"`
	Native   bool    `json:"native"`
	Raw      string  `json:"balance_raw"`
	Balance  *string `json:"balance"`
	Decimals *int    `json:"decimals"`
	Symbol   *string `json:"token_symbol"`
	Source   string  `json:"balance_source"`

	Provider       string    `json:"provider"`
	RPCProvider    string    `json:"rpc_provider"`
	QueriedAt      time.Time `json:"queried_at"`
	BlockNumber    uint64    `json:"block_number"`
	BlockTimestamp time.Time `json:"block_timestamp"`
	SourceDetail   string    `json:"balance_source_detail"`
	StatePoint
	MetadataErrors map[string]string `json:"metadata_errors,omitempty"`
}

func (s *Service) SnapshotBalance(ctx context.Context, slug string, holder, token chain.Address, point StatePoint, opt Options) (BalanceSnapshot, error) {
	out := BalanceSnapshot{Chain: slug, Address: holder.String(), Token: token.String(), Native: token == "", Source: "rpc", SourceDetail: "rpc_state", StatePoint: point, BlockNumber: point.Block, BlockTimestamp: point.Time, QueriedAt: time.Now().UTC()}
	var raw *big.Int
	if token == "" {
		result, err := s.ReadRPC(ctx, slug, "eth_getBalance", []any{holder.String(), point.Ref.String()}, opt)
		if err != nil {
			return out, err
		}
		var value string
		if err = json.Unmarshal(result.Result, &value); err != nil {
			return out, fmt.Errorf("invalid native balance result")
		}
		raw, err = chain.HexToBigInt(value)
		if err != nil {
			return out, err
		}
		out.Provider = result.Provider
		cfg, _ := s.chains.Get(slug)
		d, sym := cfg.Native.Decimals, cfg.Native.Symbol
		out.Decimals = &d
		out.Symbol = &sym
	} else {
		data, err := EncodeCall("balanceOf(address)", holder)
		if err != nil {
			return out, err
		}
		result, err := s.ReadRPC(ctx, slug, "eth_call", []any{map[string]any{"to": token.String(), "data": chain.BytesToHex(data)}, point.Ref.String()}, opt)
		if err != nil {
			return out, err
		}
		var value string
		if err = json.Unmarshal(result.Result, &value); err != nil {
			return out, fmt.Errorf("invalid token balance result")
		}
		bytes, err := chain.HexToBytes(value)
		if err != nil || len(bytes) != 32 {
			return out, fmt.Errorf("balanceOf did not return one uint256")
		}
		raw = new(big.Int).SetBytes(bytes)
		out.Provider = result.Provider
		out.MetadataErrors = map[string]string{}
		for _, field := range []string{"decimals", "symbol"} {
			call, _ := EncodeCall(field + "()")
			value, err := s.CallContract(ctx, ContractCall{Chain: slug, To: token, Data: call, BlockRef: point.Ref}, opt)
			if err != nil {
				out.MetadataErrors[field] = rpc.AsError(err).Public()
				continue
			}
			if field == "decimals" {
				d, e := DecodeUint8(value)
				if e != nil {
					out.MetadataErrors[field] = e.Error()
				} else {
					out.Decimals = &d
				}
			} else {
				sym, e := DecodeString(value)
				if e != nil {
					out.MetadataErrors[field] = e.Error()
				} else {
					out.Symbol = &sym
				}
			}
		}
	}
	out.RPCProvider = out.Provider
	out.Raw = raw.String()
	if out.Decimals != nil {
		value := decimal.NewFromBigInt(raw, -int32(*out.Decimals)).String()
		out.Balance = &value
	}
	return out, nil
}

func StatePointDescription(point StatePoint) string {
	return "block " + strconv.FormatUint(point.Block, 10)
}
