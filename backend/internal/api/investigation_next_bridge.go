package api

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
	"github.com/blockchain-platform/blockchain-platform/internal/method"
	"golang.org/x/crypto/sha3"
)

// Registry checked against Circle's official CCTP V1 mainnet table, 2026-10-03.
// Caller supplied emitters are never trusted. CCTP V2 requires its own adapter.
type cctpChain struct {
	Domain                 uint32
	Transmitter, Messenger string
}

var cctpV1 = map[string]cctpChain{
	"eth":       {0, "0x0a992d191deec32afe36203ad87d7d289a738f81", "0xbd3fa81b58ba92a82136038b25adec7066af3155"},
	"avalanche": {1, "0x8186359af5f57fbb40c6b14a588d2a59c0c29880", "0x6b25532e1060ce10cc3b0a99e5683b91bfde6982"},
	"optimism":  {2, "0x4d41f22c5a0e5c74090899e5a8fb597a8842b3e8", "0x2b4069517957735be00cee0fadae88a26365528f"},
	"arbitrum":  {3, "0xc30362313fbba5cf9163f0bb16a0e01f01a896ca", "0x19330d10d9cc8751218eaf51e8885d058642e08a"},
	"base":      {6, "0xad09780d193884d503182ad4588450c416d6f9d4", "0x1682ae6375c4e4a97e4b583bc394c861a46d8962"},
	"polygon":   {7, "0xf3be9355363857f3e001be68856a2f96b4c39ba9", "0x9daf8c91aefae50b9c0e69629d3f6ca40ca3b3fe"},
}

func abiDynamic(data []byte, slot int) ([]byte, error) {
	if slot < 0 || slot+32 > len(data) {
		return nil, fmt.Errorf("truncated ABI offset")
	}
	offset := new(big.Int).SetBytes(data[slot : slot+32])
	if !offset.IsUint64() || offset.Uint64() > uint64(len(data)) || offset.Uint64()%32 != 0 {
		return nil, fmt.Errorf("invalid ABI offset")
	}
	p := int(offset.Uint64())
	if p+32 > len(data) {
		return nil, fmt.Errorf("truncated ABI length")
	}
	n := new(big.Int).SetBytes(data[p : p+32])
	if !n.IsUint64() || n.Uint64() > 4096 || n.Uint64() > uint64(len(data)-p-32) {
		return nil, fmt.Errorf("invalid bounded ABI bytes")
	}
	return data[p+32 : p+32+int(n.Uint64())], nil
}

type cctpMessage struct {
	Source, Destination       uint32
	Nonce                     uint64
	Sender, Recipient, Caller string
	Body, Raw                 []byte
}

func parseCCTPMessage(raw []byte) (cctpMessage, error) {
	var m cctpMessage
	if len(raw) != 248 || binary.BigEndian.Uint32(raw[:4]) != 0 || binary.BigEndian.Uint32(raw[116:120]) != 0 {
		return m, fmt.Errorf("unsupported CCTP V1 message/body version or length")
	}
	m.Source = binary.BigEndian.Uint32(raw[4:8])
	m.Destination = binary.BigEndian.Uint32(raw[8:12])
	m.Nonce = binary.BigEndian.Uint64(raw[12:20])
	m.Sender = chain.BytesToHex(raw[20:52])
	m.Recipient = chain.BytesToHex(raw[52:84])
	m.Caller = chain.BytesToHex(raw[84:116])
	m.Body = raw[116:]
	m.Raw = raw
	return m, nil
}
func paddedAddress(address string) string { return "0x" + strings.Repeat("0", 24) + address[2:] }
func cctpMatches(source, destination cctpChain, src, dst *chainread.Receipt, destInput string) ([]any, error) {
	rows := []any{}
	for _, sent := range src.Logs {
		if sent.Removed || sent.Address.String() != source.Transmitter || len(sent.Topics) != 1 || !strings.EqualFold(sent.Topics[0], method.Topic0Of("MessageSent(bytes)")) {
			continue
		}
		data, e := chain.HexToBytes(sent.Data)
		if e != nil {
			continue
		}
		raw, e := abiDynamic(data, 0)
		if e != nil {
			continue
		}
		msg, e := parseCCTPMessage(raw)
		if e != nil {
			continue
		}
		if msg.Source != source.Domain || msg.Destination != destination.Domain || msg.Sender != paddedAddress(source.Messenger) || msg.Recipient != paddedAddress(destination.Messenger) {
			continue
		}
		for _, received := range dst.Logs {
			if received.Removed || received.Address.String() != destination.Transmitter || len(received.Topics) != 3 || !strings.EqualFold(received.Topics[0], method.Topic0Of("MessageReceived(address,uint32,uint64,bytes32,bytes)")) {
				continue
			}
			nonce, e := chain.HexToBigInt(received.Topics[2])
			if e != nil || !nonce.IsUint64() || nonce.Uint64() != msg.Nonce {
				continue
			}
			d, e := chain.HexToBytes(received.Data)
			if e != nil || len(d) < 96 {
				continue
			}
			domain := new(big.Int).SetBytes(d[:32])
			body, e := abiDynamic(d, 64)
			if e != nil || !domain.IsUint64() || domain.Uint64() != uint64(source.Domain) || chain.BytesToHex(d[32:64]) != msg.Sender || !bytes.Equal(body, msg.Body) {
				continue
			}
			if msg.Caller != "0x"+strings.Repeat("0", 64) && !strings.EqualFold(msg.Caller, received.Topics[1]) {
				continue
			}
			recipient, e := strictTopicAddress(chain.BytesToHex(msg.Body[36:68]))
			if e != nil {
				continue
			}
			burnToken, e := strictTopicAddress(chain.BytesToHex(msg.Body[4:36]))
			if e != nil {
				continue
			}
			amount := new(big.Int).SetBytes(msg.Body[68:100])
			if amount.Sign() == 0 {
				continue
			}
			var mint any
			lastReceived := uint64(0)
			hasPrevious := false
			for _, l := range dst.Logs {
				if l.LogIndex < received.LogIndex && l.Address.String() == destination.Transmitter && len(l.Topics) > 0 && strings.EqualFold(l.Topics[0], received.Topics[0]) && l.LogIndex >= lastReceived {
					lastReceived = l.LogIndex
					hasPrevious = true
				}
			}
			for _, l := range dst.Logs {
				if (hasPrevious && l.LogIndex <= lastReceived) || l.Removed || l.LogIndex >= received.LogIndex || l.Address.String() != destination.Messenger || len(l.Topics) != 3 || !strings.EqualFold(l.Topics[0], method.Topic0Of("MintAndWithdraw(address,uint256,address)")) {
					continue
				}
				a, e := strictTopicAddress(l.Topics[1])
				v, ev := chain.HexToBytes(l.Data)
				token, et := strictTopicAddress(l.Topics[2])
				if e == nil && ev == nil && et == nil && len(v) == 32 && a == recipient && new(big.Int).SetBytes(v).Cmp(amount) == 0 {
					mint = map[string]any{"token_contract": token, "recipient": recipient, "amount_raw": amount.String(), "evidence": l}
					break
				}
			}
			if mint == nil {
				continue
			}
			exact := false
			if strings.HasPrefix(strings.ToLower(destInput), method.SelectorOf("receiveMessage(bytes,bytes)")) {
				input, e := chain.HexToBytes(destInput)
				if e == nil && len(input) >= 68 {
					m, e := abiDynamic(input[4:], 0)
					exact = e == nil && bytes.Equal(m, raw)
				}
			}
			hash := sha3.NewLegacyKeccak256()
			hash.Write(raw)
			rows = append(rows, map[string]any{"source_domain": msg.Source, "destination_domain": msg.Destination, "nonce": fmt.Sprintf("%d", msg.Nonce), "message_hash": "0x" + hex.EncodeToString(hash.Sum(nil)), "message_bytes": chain.BytesToHex(raw), "burn_token_contract": burnToken, "amount_raw": amount.String(), "mint_recipient": recipient, "source_message": sent, "destination_message": received, "destination_mint": mint, "exact_message_bytes_verified": exact, "verification": "Registered emitters + domain/nonce/sender/body + mint recipient/amount", "source_burn_observed_in_this_transaction": false})
		}
	}
	return rows, nil
}
func (s *Server) nextBridge(ctx context.Context, q nextRequest) (any, error) {
	srcCfg, ok := cctpV1[q.Chain]
	dstCfg, ok2 := cctpV1[q.DestinationChain]
	if !ok || !ok2 {
		return nil, summaryError(422, "BRIDGE_ADAPTER_UNSUPPORTED", "CCTP V1 mainnet adapter supports Ethereum/Avalanche/Optimism/Arbitrum/Base/Polygon")
	}
	if q.Chain == q.DestinationChain {
		return nil, fmt.Errorf("bridge domains must differ")
	}
	receipts := []*chainread.Receipt{}
	points := []chainread.StatePoint{}
	for i, slug := range []string{q.Chain, q.DestinationChain} {
		hash := q.TxHash
		if i == 1 {
			hash = q.DestinationTxHash
		}
		h, _ := chain.ParseHash(hash)
		r, e := s.deps.ChainRead.Receipt(ctx, slug, h, investigationOptions())
		if e != nil {
			return nil, e
		}
		if r == nil || r.Status == nil || *r.Status != 1 || r.BlockNumber == nil {
			return nil, fmt.Errorf("successful confirmed receipts required on both chains")
		}
		b, e := s.deps.ChainRead.Block(ctx, slug, chain.AtBlock(*r.BlockNumber), false, investigationOptions())
		if e != nil {
			return nil, e
		}
		if e = validateTraceSource(r, b, h); e != nil {
			return nil, e
		}
		receipts = append(receipts, r)
		points = append(points, chainread.StatePoint{Block: b.Number, Hash: b.Hash})
	}
	h, _ := chain.ParseHash(q.DestinationTxHash)
	tx, e := s.deps.ChainRead.Transaction(ctx, q.DestinationChain, h, investigationOptions())
	if e != nil {
		return nil, e
	}
	input := ""
	if tx != nil && tx.To != nil && tx.To.String() == dstCfg.Transmitter {
		input = tx.Input
	}
	matches, e := cctpMatches(srcCfg, dstCfg, receipts[0], receipts[1], input)
	if e != nil {
		return nil, e
	}
	for i, slug := range []string{q.Chain, q.DestinationChain} {
		if e = s.nextBlockGuard(ctx, slug, points[i]); e != nil {
			return nil, e
		}
	}
	return map[string]any{"protocol": "cctp_v1", "registry_version": "circle-v1-mainnet-2026-10-03", "registry_source": "https://developers.circle.com/cctp/v1/evm-smart-contracts", "source_chain": q.Chain, "destination_chain": q.DestinationChain, "matched": len(matches) > 0, "matches": matches, "source_receipt": receipts[0], "destination_receipt": receipts[1], "limitations": []string{"V1 only; no automatic destination transaction discovery", "No match does not prove absence outside these two receipts", "Source MessageSent can be a replacement message; this response does not claim a fresh burn in that transaction", "Exact message bytes require direct receiveMessage calldata; nested calls retain a weaker protocol-field verification level"}}, nil
}
