package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
	"github.com/blockchain-platform/blockchain-platform/internal/chaintrace"
	"github.com/blockchain-platform/blockchain-platform/internal/crosschain"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
	solanaprovider "github.com/blockchain-platform/blockchain-platform/internal/provider/solana"
	"github.com/blockchain-platform/blockchain-platform/internal/tron"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

var bridgeReadSlots = make(chan struct{}, 4)

// Public tool spelling remains ethereum; the existing RPC registry uses eth.
func bridgeRPCChain(c string) string {
	if c == "ethereum" {
		return "eth"
	}
	return c
}

type bridgeRecord struct {
	CCTPRead                    bool
	CCTPError                   string
	CCTPAttestations            []crosschain.CCTPAttestation
	SuccessfulCalls             []bridgeProtocolCall
	ReceiptSHA256               string
	TraceSHA256                 string
	Finality                    *crosschain.BlockProof
	Chain, Hash, Source, Status string
	Timestamp                   time.Time
	BlockNumber                 uint64
	BlockHash                   string
	Movements                   []crosschain.Movement
	Logs                        []chainread.Log
	Hints                       []crosschain.ProtocolHint
	Truncated                   bool
	NativeRead                  bool
	Gaps                        []string
	TokenAccounts               map[string]solanaprovider.BridgeTokenAccount
}

// One session shares exact-hash reads across verification and matching. No
// externally reported amount is inserted into the chain data store.
type bridgeReadSession struct {
	getters       map[string]bridgeGetterResult
	deployments   map[string]bridgeDeploymentResult
	fundsProofs   map[string]*crosschain.FundsProof
	s             *Server
	ctx           context.Context
	records       map[string]*bridgeRecord
	errors        map[string]error
	decimals      map[string]int
	calls         int
	protocolCalls int
	maxCalls      int
	traceID       string
}

type bridgeGetterResult struct {
	Data []byte
	Err  error
}
type bridgeDeploymentResult struct {
	Proof crosschain.ContractProof
	Err   error
}

func newBridgeReadSession(ctx context.Context, s *Server) *bridgeReadSession {
	traceID := metaFrom(ctx).TraceID
	if traceID == "" {
		traceID = uuid.NewString()
	}
	return &bridgeReadSession{s: s, ctx: ctx, records: map[string]*bridgeRecord{}, errors: map[string]error{}, decimals: map[string]int{}, traceID: traceID}
}
func (b *bridgeReadSession) budget() error {
	if err := b.ctx.Err(); err != nil {
		return fmt.Errorf("chain read time budget exhausted")
	}
	limit := b.maxCalls
	if limit == 0 {
		limit = 32
	}
	if b.calls >= limit {
		return fmt.Errorf("chain read request budget exhausted")
	}
	b.calls++
	return nil
}
func (b *bridgeReadSession) options() chainread.Options {
	return chainread.Options{Purpose: rpc.PurposeReceipt, Priority: rpc.PriorityInteractive, TraceID: b.traceID}
}

func (b *bridgeReadSession) record(c, h string, native bool) (*bridgeRecord, error) {
	canonical, err := crosschain.Hash(c, h)
	if err != nil {
		return nil, err
	}
	key := c + "|" + canonical
	if cached := b.records[key]; cached != nil {
		if native && !cached.NativeRead && c != "tron" && c != "solana" {
			b.evmNative(cached)
		}
		return cached, nil
	}
	if err := b.errors[key]; err != nil {
		return nil, err
	}
	if len(b.records)+len(b.errors) >= 16 {
		return nil, fmt.Errorf("chain record count budget exhausted")
	}
	select {
	case bridgeReadSlots <- struct{}{}:
		defer func() { <-bridgeReadSlots }()
	case <-b.ctx.Done():
		return nil, fmt.Errorf("chain read capacity budget exhausted")
	}
	ctx, cancel := context.WithTimeout(b.ctx, 12*time.Second)
	defer cancel()
	var record *bridgeRecord
	switch c {
	case "solana":
		record, err = b.solanaRecord(ctx, canonical)
	case "tron":
		record, err = b.tronRecord(ctx, canonical)
	default:
		record, err = b.evmRecord(ctx, c, canonical)
	}
	if err != nil {
		b.errors[key] = err
		return nil, err
	}
	boundBridgeMovements(record)
	b.records[key] = record
	if native && !record.NativeRead && c != "tron" && c != "solana" {
		b.evmNative(record)
	}
	return record, nil
}

func exactMovement(c, h, dataset string, index uint32, stamp time.Time, from, to, contract, raw string, decimals int) (crosschain.Movement, bool) {
	n, ok := new(big.Int).SetString(raw, 10)
	if !ok || n.Sign() <= 0 || n.BitLen() > 256 || from == "" || to == "" {
		return crosschain.Movement{}, false
	}
	m := crosschain.Movement{Chain: c, TxHash: h, Dataset: dataset, Index: index, Timestamp: stamp, TimestampSource: "chain_rpc", From: from, To: to, TokenContract: contract, Decimals: decimals, AmountRaw: n.String(), Status: "success"}
	if decimals >= 0 && decimals <= 38 {
		m.Amount = decimal.NewFromBigInt(n, int32(-decimals)).String()
	}
	return m, true
}

func (b *bridgeReadSession) evmRecord(ctx context.Context, c, h string) (*bridgeRecord, error) {
	if b.s.deps.ChainRead == nil {
		return nil, fmt.Errorf("EVM transaction reader unavailable")
	}
	hash, _ := chain.ParseHash(h)
	if err := b.budget(); err != nil {
		return nil, err
	}
	r, err := b.s.deps.ChainRead.Receipt(ctx, bridgeRPCChain(c), hash, b.options())
	if err != nil {
		return nil, fmt.Errorf("EVM receipt unavailable (%s)", rpc.AsError(err).Class)
	}
	if r == nil {
		return nil, fmt.Errorf("EVM receipt unavailable (not_found)")
	}
	if r.TxHash != hash || r.Status == nil || r.BlockNumber == nil || r.BlockHash == "" {
		return nil, fmt.Errorf("EVM receipt identity or confirmation unavailable")
	}
	if err := b.budget(); err != nil {
		return nil, err
	}
	block, err := b.s.deps.ChainRead.Block(ctx, bridgeRPCChain(c), chain.BlockRef{Number: r.BlockNumber}, false, b.options())
	if err != nil || block == nil || block.Number != *r.BlockNumber || !strings.EqualFold(block.Hash, r.BlockHash) || block.Timestamp == 0 || block.Timestamp > uint64(time.Now().Add(time.Hour).Unix()) {
		return nil, fmt.Errorf("EVM receipt and block identity mismatch or unavailable")
	}
	out := &bridgeRecord{Chain: c, Hash: h, Source: "rpc_receipt_and_matching_block", Status: "success", Timestamp: time.Unix(int64(block.Timestamp), 0).UTC(), BlockNumber: block.Number, BlockHash: block.Hash, Movements: []crosschain.Movement{}, Logs: r.Logs, Gaps: []string{}}
	receiptBytes, _ := json.Marshal(r)
	out.ReceiptSHA256 = fmt.Sprintf("%x", sha256.Sum256(receiptBytes))
	// Routing and proof parsers must see only logs belonging to this confirmed
	// receipt; a matching signature in an unrelated/removed log is not evidence.
	out.Logs = []chainread.Log{}
	for _, log := range r.Logs {
		if log.Removed || log.TxHash != hash || log.BlockNumber != block.Number {
			out.Gaps = append(out.Gaps, "receipt_log_identity_conflict")
			continue
		}
		out.Logs = append(out.Logs, log)
	}
	if *r.Status != 1 {
		out.Status = "failed"
		return out, nil
	}
	out.Hints = crosschain.DetectProtocolHints(c, out.Logs)
	if len(out.Logs) > 400 {
		out.Logs = out.Logs[:400]
		out.Truncated = true
		out.Hints = crosschain.DetectProtocolHints(c, out.Logs)
	}
	for _, l := range out.Logs {
		if l.Removed || l.TxHash != hash || l.BlockNumber != block.Number || len(l.Topics) != 3 || !strings.EqualFold(l.Topics[0], crosschain.EventTopic("Transfer(address,address,uint256)")) || len(l.Topics[1]) != 66 || len(l.Topics[2]) != 66 {
			continue
		}
		data, err := hex.DecodeString(strings.TrimPrefix(l.Data, "0x"))
		if err != nil || len(data) != 32 {
			continue
		}
		contract := l.Address.String()
		decimals := b.evmDecimals(ctx, c, contract, *r.BlockNumber)
		m, ok := exactMovement(c, h, "token_transfers", uint32(l.LogIndex), out.Timestamp, "0x"+l.Topics[1][26:], "0x"+l.Topics[2][26:], contract, new(big.Int).SetBytes(data).String(), decimals)
		if ok {
			out.Movements = append(out.Movements, m)
		}
	}
	return out, nil
}

func (b *bridgeReadSession) evmDecimals(ctx context.Context, c, contract string, block uint64) int {
	key := c + "|" + strings.ToLower(contract) + "|" + strconv.FormatUint(block, 10)
	if value, ok := b.decimals[key]; ok {
		return value
	}
	b.decimals[key] = -1
	if len(b.decimals) > 12 || b.budget() != nil {
		return -1
	}
	address, err := chain.ParseAddress(contract)
	if err != nil {
		return -1
	}
	raw, err := b.s.deps.ChainRead.CallContract(ctx, chainread.ContractCall{Chain: bridgeRPCChain(c), To: address, Data: []byte{0x31, 0x3c, 0xe5, 0x67}, BlockRef: chain.BlockRef{Number: &block}}, b.options())
	if err != nil || len(raw) != 32 {
		return -1
	}
	n := new(big.Int).SetBytes(raw)
	if !n.IsUint64() || n.Uint64() > 38 {
		return -1
	}
	b.decimals[key] = int(n.Uint64())
	return int(n.Uint64())
}

func (b *bridgeReadSession) evmNative(out *bridgeRecord) {
	defer boundBridgeMovements(out)
	out.NativeRead = true
	if out.Status != "success" || b.s.deps.ChainRead == nil {
		return
	}
	ctx, cancel := context.WithTimeout(b.ctx, 8*time.Second)
	defer cancel()
	hash, _ := chain.ParseHash(out.Hash)
	if b.budget() != nil {
		out.Gaps = append(out.Gaps, "native_read_budget_exhausted")
		return
	}
	tx, err := b.s.deps.ChainRead.Transaction(ctx, bridgeRPCChain(out.Chain), hash, b.options())
	if err != nil || tx == nil || tx.Hash != hash || tx.BlockNumber == nil || *tx.BlockNumber != out.BlockNumber || !strings.EqualFold(tx.BlockHash, out.BlockHash) {
		out.Gaps = append(out.Gaps, "native_transaction_unavailable_or_identity_mismatch")
		return
	}
	if tx.To != nil {
		if m, ok := exactMovement(out.Chain, out.Hash, "transactions", 0, out.Timestamp, tx.From.String(), tx.To.String(), "native", tx.ValueRaw, 18); ok {
			out.Movements = append(out.Movements, m)
		}
	}
	if b.s.deps.RPCGateway == nil || b.budget() != nil {
		out.Gaps = append(out.Gaps, "trace_unavailable_or_budget_exhausted")
		return
	}
	response, err := b.s.deps.RPCGateway.Call(ctx, rpc.Request{Chain: bridgeRPCChain(out.Chain), Method: "trace_transaction", Params: []any{out.Hash}, Class: rpc.ClassTrace, Priority: rpc.PriorityInteractive, Budget: rpc.BudgetContext{Purpose: rpc.PurposeTrace}, TraceID: b.traceID})
	if err != nil && (rpc.IsClass(err, rpc.ClassTraceUnsupported) || rpc.IsClass(err, rpc.ClassMethodUnsupported)) && b.budget() == nil {
		response, err = b.s.deps.RPCGateway.Call(ctx, rpc.Request{Chain: bridgeRPCChain(out.Chain), Method: "debug_traceTransaction", Params: []any{out.Hash, map[string]any{"tracer": "callTracer"}}, Class: rpc.ClassDebugTrace, Priority: rpc.PriorityInteractive, Budget: rpc.BudgetContext{Purpose: rpc.PurposeTrace}, TraceID: b.traceID})
	}
	if err != nil {
		out.Gaps = append(out.Gaps, "native_trace_"+string(rpc.AsError(err).Class))
		return
	}
	if len(response.Result) > 2<<20 {
		out.Gaps = append(out.Gaps, "native_trace_response_too_large")
		return
	}
	root, err := normalizeBridgeTrace(response.Result)
	if err != nil || root == nil || root.From != tx.From || root.To == nil || tx.To == nil || *root.To != *tx.To || root.Value != tx.ValueRaw {
		out.Gaps = append(out.Gaps, "trace_root_identity_mismatch")
		return
	}
	out.TraceSHA256 = fmt.Sprintf("%x", sha256.Sum256(response.Result))
	index := uint32(0)
	var walk func(*chaintrace.CallFrame, bool, int)
	walk = func(frame *chaintrace.CallFrame, failed bool, depth int) {
		if frame == nil || depth > 64 || index >= 400 {
			out.Truncated = true
			return
		}
		i := index
		index++
		failed = failed || frame.Error != ""
		kind := strings.ToUpper(frame.Type)
		if !failed && frame.To != nil && kind == "CALL" {
			out.SuccessfulCalls = append(out.SuccessfulCalls, bridgeProtocolCall{From: frame.From.String(), To: frame.To.String(), Input: frame.Input, Value: frame.Value})
		}
		if i != 0 && !failed && frame.To != nil && kind != "DELEGATECALL" && kind != "STATICCALL" && kind != "CALLCODE" {
			if m, ok := exactMovement(out.Chain, out.Hash, "internal_transactions", i, out.Timestamp, frame.From.String(), frame.To.String(), "native", frame.Value, 18); ok {
				out.Movements = append(out.Movements, m)
			}
		}
		for j := range frame.Children {
			walk(&frame.Children[j], failed, depth+1)
		}
	}
	walk(root, false, 0)
}

func boundBridgeMovements(record *bridgeRecord) {
	if len(record.Movements) > 400 {
		record.Movements = record.Movements[:400]
		record.Truncated = true
	}
}

func (b *bridgeReadSession) solanaRecord(ctx context.Context, h string) (*bridgeRecord, error) {
	if err := b.budget(); err != nil {
		return nil, err
	}
	tx, err := solanaTransaction(ctx, h)
	if err != nil {
		return nil, fmt.Errorf("Solana transaction unavailable")
	}
	raw, err := json.Marshal(map[string]any{"slot": tx["slot"], "blockTime": tx["block_time"], "transaction": tx["transaction"], "meta": tx["meta"]})
	if err != nil {
		return nil, err
	}
	decoded, err := solanaprovider.DecodeBridgeTransaction(raw, h)
	if err != nil {
		return nil, err
	}
	if decoded.Timestamp > time.Now().Add(time.Hour).Unix() {
		return nil, fmt.Errorf("Solana block time outside confirmed range")
	}
	out := &bridgeRecord{Chain: "solana", Hash: h, Source: "solana_transaction_instruction_decoder", Status: "success", Timestamp: time.Unix(decoded.Timestamp, 0).UTC(), Movements: []crosschain.Movement{}, NativeRead: true, Truncated: decoded.Truncated, Gaps: []string{}, TokenAccounts: decoded.TokenAccounts}
	out.ReceiptSHA256 = fmt.Sprintf("%x", sha256.Sum256(raw))
	if slot, ok := tx["slot"].(json.Number); ok {
		out.BlockNumber, _ = strconv.ParseUint(slot.String(), 10, 64)
	}
	if decoded.Failed {
		out.Status = "failed"
		return out, nil
	}
	if decoded.DecodeGaps > 0 {
		out.Gaps = append(out.Gaps, "unresolved_instruction_owner_or_amount")
	}
	for _, event := range decoded.Events {
		dataset := "token_transfers"
		if event.Native {
			dataset = "internal_transactions"
		}
		if m, ok := exactMovement("solana", h, dataset, event.Index, out.Timestamp, event.From, event.To, event.Mint, event.AmountRaw, event.Decimals); ok {
			out.Movements = append(out.Movements, m)
		}
	}
	return out, nil
}

type bridgeTronReader interface {
	TxReceipt(context.Context, string, string) (json.RawMessage, error)
	TxRecord(context.Context, string, string) (json.RawMessage, error)
}

func (b *bridgeReadSession) tronRecord(ctx context.Context, h string) (*bridgeRecord, error) {
	var p provider.Provider
	if b.s.deps.Routes != nil {
		p, _ = b.s.deps.Routes.Get("ankr")
	}
	if p == nil {
		p = b.s.deps.Prov
	}
	reader, ok := p.(bridgeTronReader)
	if !ok {
		return nil, fmt.Errorf("TRON transaction reader unavailable")
	}
	if b.budget() != nil {
		return nil, fmt.Errorf("TRON read budget exhausted")
	}
	receipt, err := reader.TxReceipt(ctx, "tron", h)
	if err != nil {
		return nil, fmt.Errorf("TRON receipt unavailable")
	}
	if b.budget() != nil {
		return nil, fmt.Errorf("TRON read budget exhausted")
	}
	transaction, err := reader.TxRecord(ctx, "tron", h)
	if err != nil {
		return nil, fmt.Errorf("TRON transaction unavailable")
	}
	return b.decodeTron(ctx, reader, h, transaction, receipt)
}

func (b *bridgeReadSession) decodeTron(ctx context.Context, reader bridgeTronReader, h string, transaction, receipt json.RawMessage) (*bridgeRecord, error) {
	var tx struct {
		ID  string `json:"txID"`
		Ret []struct {
			Result string `json:"contractRet"`
		} `json:"ret"`
		Raw struct {
			Contracts []struct {
				Type      string `json:"type"`
				Parameter struct {
					Value struct {
						Owner  string      `json:"owner_address"`
						To     string      `json:"to_address"`
						Amount json.Number `json:"amount"`
					} `json:"value"`
				} `json:"parameter"`
			} `json:"contract"`
		} `json:"raw_data"`
	}
	var r struct {
		ID      string `json:"id"`
		Time    int64  `json:"blockTimeStamp"`
		Receipt struct {
			Result string `json:"result"`
		} `json:"receipt"`
		Result string `json:"result"`
		Logs   []struct {
			Address string   `json:"address"`
			Topics  []string `json:"topics"`
			Data    string   `json:"data"`
		} `json:"log"`
		Internal []struct {
			From     string `json:"caller_address"`
			To       string `json:"transferTo_address"`
			Rejected bool   `json:"rejected"`
			Values   []struct {
				Value json.Number `json:"callValue"`
				Token string      `json:"tokenId"`
			} `json:"callValueInfo"`
		} `json:"internal_transactions"`
	}
	if json.Unmarshal(transaction, &tx) != nil || json.Unmarshal(receipt, &r) != nil || !crosschain.Same("tron", h, "0x"+strings.TrimPrefix(tx.ID, "0x")) || !crosschain.Same("tron", h, "0x"+strings.TrimPrefix(r.ID, "0x")) || r.Time <= 0 {
		return nil, fmt.Errorf("TRON receipt identity or block time unavailable")
	}
	stamp := time.UnixMilli(r.Time).UTC()
	if stamp.After(time.Now().Add(time.Hour)) {
		return nil, fmt.Errorf("TRON block time outside confirmed range")
	}
	out := &bridgeRecord{Chain: "tron", Hash: h, Source: "tron_transaction_and_receipt", Status: "unknown", Timestamp: stamp, Movements: []crosschain.Movement{}, NativeRead: true, Gaps: []string{}}
	if len(tx.Ret) > 0 && tx.Ret[0].Result == "SUCCESS" && r.Result != "FAILED" && (r.Receipt.Result == "" || r.Receipt.Result == "SUCCESS") {
		out.Status = "success"
	} else {
		out.Status = "failed"
		return out, nil
	}
	owner := ""
	for _, c := range tx.Raw.Contracts {
		if owner == "" {
			owner = tronAddress(c.Parameter.Value.Owner)
		}
		if c.Type == "TransferContract" {
			if m, ok := exactMovement("tron", h, "transactions", 0, stamp, tronAddress(c.Parameter.Value.Owner), tronAddress(c.Parameter.Value.To), "native", c.Parameter.Value.Amount.String(), 6); ok {
				out.Movements = append(out.Movements, m)
			}
		}
	}
	for i, l := range r.Logs {
		if i >= 400 {
			out.Truncated = true
			break
		}
		if len(l.Topics) != 3 || !strings.EqualFold(strings.TrimPrefix(l.Topics[0], "0x"), strings.TrimPrefix(crosschain.EventTopic("Transfer(address,address,uint256)"), "0x")) {
			continue
		}
		from, to := strings.TrimPrefix(l.Topics[1], "0x"), strings.TrimPrefix(l.Topics[2], "0x")
		if len(from) != 64 || len(to) != 64 {
			continue
		}
		data, err := hex.DecodeString(strings.TrimPrefix(l.Data, "0x"))
		if err != nil || len(data) != 32 {
			continue
		}
		contract := tronAddress(l.Address)
		decimals := -1
		key := "tron|" + contract
		if n, ok := b.decimals[key]; ok {
			decimals = n
		} else {
			b.decimals[key] = -1
			if getter, ok := reader.(interface {
				TxTokenDecimals(context.Context, string, string, string) (int, error)
			}); ok && len(b.decimals) < 12 && b.budget() == nil {
				if n, err := getter.TxTokenDecimals(ctx, "tron", contract, owner); err == nil && n >= 0 && n <= 38 {
					decimals = n
					b.decimals[key] = n
				}
			}
		}
		if m, ok := exactMovement("tron", h, "token_transfers", uint32(i), stamp, tronAddress("41"+from[24:]), tronAddress("41"+to[24:]), contract, new(big.Int).SetBytes(data).String(), decimals); ok {
			out.Movements = append(out.Movements, m)
		}
	}
	for i, l := range r.Internal {
		if i >= 400 {
			out.Truncated = true
			break
		}
		if l.Rejected {
			continue
		}
		for _, value := range l.Values {
			if value.Token != "" && value.Token != "_" {
				continue
			}
			if m, ok := exactMovement("tron", h, "internal_transactions", uint32(i), stamp, tronAddress(l.From), tronAddress(l.To), "native", value.Value.String(), 6); ok {
				out.Movements = append(out.Movements, m)
			}
		}
	}
	return out, nil
}

func tronAddress(value string) string {
	value = strings.TrimPrefix(value, "0x")
	if len(value) == 40 {
		value = "41" + value
	}
	address, err := tron.NormalizeTronAddress(value)
	if err != nil {
		return ""
	}
	return address
}
