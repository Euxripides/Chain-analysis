package crosschain

import (
	"context"
	"encoding/hex"
	"fmt"
	"github.com/blockchain-platform/blockchain-platform/internal/addressnorm"
	"github.com/blockchain-platform/blockchain-platform/internal/solana"
	"math/big"
	"net/url"
	"strconv"
	"strings"
)

var wormholeChains = map[string]string{"1": "solana", "2": "ethereum", "4": "bsc", "5": "polygon", "6": "avalanche", "23": "arbitrum", "24": "optimism", "30": "base"}
var CCTPDomains = map[string]string{"0": "ethereum", "1": "avalanche", "2": "optimism", "3": "arbitrum", "5": "solana", "6": "base", "7": "polygon"}

// Mainnet deployment identities from Circle's USDC and CCTP references,
// fetched and SHA-256 frozen under qa/specs. Token symbols are not identities.
var CCTPUSDC = map[string]string{
	"ethereum":  "0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48",
	"avalanche": "0xb97ef9ef8734c71904d8002f8b6bc66dd9c48a6e",
	"optimism":  "0x0b2c639c533813f4aa9d7837caf62653d097ff85",
	"arbitrum":  "0xaf88d065e77c8cc2239327c5edb3a432268e5831",
	"base":      "0x833589fcd6edb6e08f4c7c32d4f71b54bda02913",
	"polygon":   "0x3c499c542cef5e3811e1192ce70d8cc03d5c3359",
	"solana":    "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v",
}

const CCTPTransmitterV2 = "0x81d40f21f12a8f0e3252bccb954d722d4c464b64"

func WormholeChainID(chain string) string {
	for id, c := range wormholeChains {
		if chain == c {
			return id
		}
	}
	return ""
}
func CCTPDomain(chain string) string {
	for id, c := range CCTPDomains {
		if chain == c {
			return id
		}
	}
	return ""
}

func protocolAddress(c, value string) string {
	if address, err := addressnorm.Normalize(c, value); err == nil {
		return address
	}
	bytes, err := hex.DecodeString(strings.TrimPrefix(value, "0x"))
	if err != nil || len(bytes) != 32 {
		return ""
	}
	if c == "solana" {
		return solana.EncodeBase58(bytes)
	}
	for _, v := range bytes[:12] {
		if v != 0 {
			return ""
		}
	}
	address, err := addressnorm.Normalize(c, "0x"+hex.EncodeToString(bytes[12:]))
	if err != nil {
		return ""
	}
	return address
}

// CanonicalNonce compares V1 decimal and V2 bytes32 nonces without truncation.
func CanonicalNonce(value string) string {
	base := 10
	if strings.HasPrefix(strings.ToLower(value), "0x") {
		base = 16
		value = value[2:]
	}
	n, ok := new(big.Int).SetString(value, base)
	if !ok || n.Sign() < 0 || n.BitLen() > 256 {
		return ""
	}
	return n.String()
}

func (s *Service) wormhole(ctx context.Context, r QueryRequest) ([]Transfer, error) {
	v, ev, err := s.fetch(ctx, "wormhole", "https://api.wormholescan.io", "/api/v1/operations", url.Values{"txHash": {externalHash(r.Chain, r.TxHash)}, "pageSize": {"50"}}, nil, nil)
	if err != nil {
		return nil, err
	}
	items := arr(get(v, "operations"))
	if items == nil {
		return nil, fmt.Errorf("Wormholescan operations array missing")
	}
	ev.PageTruncated = len(items) >= 50
	out := []Transfer{}
	for _, item := range items {
		id := str(get(item, "id"))
		emitter := strings.TrimPrefix(strings.ToLower(str(get(item, "emitterAddress", "hex"))), "0x")
		sequence := CanonicalNonce(str(get(item, "sequence")))
		emitterBytes, decodeErr := hex.DecodeString(emitter)
		seq, ok := new(big.Int).SetString(sequence, 10)
		if decodeErr != nil || len(emitterBytes) != 32 || !ok || !seq.IsUint64() || id != str(get(item, "emitterChain"))+"/"+emitter+"/"+sequence {
			continue
		}
		a := emptyLeg(wormholeChains[str(get(item, "sourceChain", "chainId"))], str(get(item, "sourceChain", "transaction", "txHash")))
		b := emptyLeg(wormholeChains[str(get(item, "targetChain", "chainId"))], str(get(item, "targetChain", "transaction", "txHash")))
		a.Address, a.Timestamp = protocolAddress(a.Chain, str(get(item, "sourceChain", "from"))), str(get(item, "sourceChain", "timestamp"))
		a.BridgeContract = str(get(item, "emitterAddress", "native"))
		a.OriginChainID, b.OriginChainID = str(get(item, "content", "payload", "tokenChain")), str(get(item, "content", "payload", "tokenChain"))
		a.OriginToken, b.OriginToken = str(get(item, "content", "payload", "tokenAddress")), str(get(item, "content", "payload", "tokenAddress"))
		b.Timestamp = str(get(item, "targetChain", "timestamp"))
		properties := get(item, "content", "standarizedProperties")
		b.Address = protocolAddress(b.Chain, str(get(properties, "toAddress")))
		if b.Address == "" {
			b.Address = str(get(item, "content", "payload", "toAddress"))
			if b.Chain != "solana" && len(strings.TrimPrefix(b.Address, "0x")) == 64 {
				b.Address = "0x" + b.Address[len(b.Address)-40:]
			}
		}
		b.Address = protocolAddress(b.Chain, b.Address)
		if str(get(properties, "tokenChain")) == str(get(item, "targetChain", "chainId")) {
			b.TokenContract = protocolAddress(b.Chain, str(get(properties, "tokenAddress")))
		}
		if str(get(properties, "tokenChain")) == str(get(item, "sourceChain", "chainId")) {
			a.TokenContract = protocolAddress(a.Chain, str(get(properties, "tokenAddress")))
		}
		amount := str(get(item, "content", "payload", "amount"))
		if n, ok := new(big.Int).SetString(amount, 10); ok && n.Sign() > 0 && n.BitLen() <= 256 {
			a.NormalizedAmountRaw, b.NormalizedAmountRaw = amount, amount
			a.AmountsScope, b.AmountsScope = "wormhole_normalized_up_to_8_decimals", "wormhole_normalized_up_to_8_decimals"
		} else {
			a.AmountsScope, b.AmountsScope = "message_only", "message_only"
		}
		t := Transfer{UnderlyingBridge: strings.Join(stringList(get(properties, "appIds")), ","), OrderID: id, RawStatus: str(get(item, "targetChain", "status")), Status: "pending", Evidence: ev, Source: []Leg{a}, Destination: []Leg{}}
		if b.TxHash != "" {
			t.Destination = []Leg{b}
			if strings.EqualFold(t.RawStatus, "completed") {
				t.Status = "completed"
			}
		}
		out = append(out, t)
	}
	return out, nil
}

func stringList(value any) []string {
	out := []string{}
	for _, item := range arr(value) {
		if v := str(item); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func (s *Service) cctp(ctx context.Context, r QueryRequest) ([]Transfer, error) {
	domain := CCTPDomain(r.Chain)
	query := url.Values{"transactionHash": {r.TxHash}}
	var received *ProtocolHint
	for i := range r.Hints {
		h := &r.Hints[i]
		if h.Bridge == "cctp" && h.Kind == "received" && h.SourceDomain != "" && CanonicalNonce(h.Identifier) != "" && strings.EqualFold(h.Contract, CCTPTransmitterV2) {
			domain = h.SourceDomain
			query = url.Values{"nonce": {h.Identifier}}
			received = h
			break
		}
	}
	if domain == "" {
		return nil, &upstreamError{"unsupported_chain", "this chain has no implemented CCTP domain"}
	}
	v, ev, err := s.fetch(ctx, "cctp", "https://iris-api.circle.com", "/v2/messages/"+url.PathEscape(domain), query, nil, nil)
	if err != nil {
		return nil, err
	}
	items := arr(get(v, "messages"))
	if items == nil {
		return nil, fmt.Errorf("Circle messages array missing")
	}
	out := []Transfer{}
	for _, item := range items {
		d := get(item, "decodedMessage")
		body := get(d, "decodedMessageBody")
		if str(get(d, "sourceDomain")) != domain || body == nil {
			continue
		}
		nonce := str(get(item, "eventNonce"))
		if nonce == "" {
			nonce = str(get(d, "nonce"))
		}
		if CanonicalNonce(nonce) == "" {
			continue
		}
		a := emptyLeg(CCTPDomains[domain], str(get(v, "sourceTxHash")))
		if _, err := Hash(a.Chain, a.TxHash); err != nil {
			continue
		}
		a.Address, a.TokenContract, a.AmountRaw = protocolAddress(a.Chain, str(get(body, "messageSender"))), protocolAddress(a.Chain, str(get(body, "burnToken"))), str(get(body, "amount"))
		if a.Address == "" || a.TokenContract != CCTPUSDC[a.Chain] {
			continue
		}
		b := emptyLeg(CCTPDomains[str(get(d, "destinationDomain"))], "")
		b.Address = protocolAddress(b.Chain, str(get(body, "mintRecipient")))
		if b.Address == "" {
			continue
		}
		b.TokenContract = CCTPUSDC[b.Chain]
		b.AmountRaw = a.AmountRaw
		if fee := str(get(body, "feeExecuted")); fee != "" {
			amount, ok := new(big.Int).SetString(a.AmountRaw, 10)
			cost, ok2 := new(big.Int).SetString(fee, 10)
			if !ok || !ok2 || cost.Sign() < 0 || amount.Cmp(cost) < 0 {
				continue
			}
			b.AmountRaw = new(big.Int).Sub(amount, cost).String()
		} else if str(get(item, "cctpVersion")) == "2" {
			b.AmountRaw = ""
		}
		decimals := 6
		a.Decimals, b.Decimals = &decimals, &decimals
		a.Amount, b.Amount = scaled(a.AmountRaw, 6), scaled(b.AmountRaw, 6)
		a.Token, b.Token = "USDC", "USDC"
		a.AmountsScope, b.AmountsScope = "cctp_message_burn", "cctp_message_mint_after_executed_fee"
		t := Transfer{OrderID: domain + "/" + CanonicalNonce(nonce), RawStatus: str(get(item, "status")), Status: "pending", Source: []Leg{a}, Destination: []Leg{b}, Evidence: ev}
		// An attestation being complete does not establish that a mint occurred.
		if received != nil && CanonicalNonce(received.Identifier) == CanonicalNonce(nonce) && b.Chain == r.Chain {
			b.TxHash = r.TxHash
			t.Destination = []Leg{b}
			t.Status = "completed"
		}
		out = append(out, t)
	}
	return out, nil
}

func UintWord(hexWord string) (uint64, bool) {
	n, ok := new(big.Int).SetString(strings.TrimPrefix(hexWord, "0x"), 16)
	return func() (uint64, bool) {
		if !ok || !n.IsUint64() {
			return 0, false
		}
		return n.Uint64(), true
	}()
}

func decimalUint(n uint64) string { return strconv.FormatUint(n, 10) }
