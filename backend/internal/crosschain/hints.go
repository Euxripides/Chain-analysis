package crosschain

import (
	"encoding/hex"
	"math/big"
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
	"golang.org/x/crypto/sha3"
)

func EventTopic(signature string) string {
	h := sha3.NewLegacyKeccak256()
	h.Write([]byte(signature))
	return "0x" + hex.EncodeToString(h.Sum(nil))
}

// DetectProtocolHints uses exact event layouts. A signature is a routing clue,
// not proof that the emitting contract is an official bridge deployment.
func DetectProtocolHints(chain string, logs []chainread.Log) []ProtocolHint {
	out := []ProtocolHint{}
	for _, l := range logs {
		if l.Removed || len(l.Topics) == 0 {
			continue
		}
		data, err := hex.DecodeString(strings.TrimPrefix(l.Data, "0x"))
		if err != nil {
			continue
		}
		h := ProtocolHint{Contract: l.Address.String(), EventIndex: uint32(l.LogIndex)}
		switch strings.ToLower(l.Topics[0]) {
		case EventTopic("MessageReceived(address,uint32,uint64,bytes32,bytes)"), EventTopic("MessageReceived(address,uint32,bytes32,bytes32,uint32,bytes)"):
			if len(data) < 96 || len(l.Topics) < 3 {
				continue
			}
			domain := new(big.Int).SetBytes(data[:32])
			if !domain.IsUint64() {
				continue
			}
			h.Bridge, h.Kind, h.SourceDomain, h.Identifier = "cctp", "received", domain.String(), l.Topics[2]
		case EventTopic("MessageSent(bytes)"):
			if len(data) < 64 {
				continue
			}
			start := new(big.Int).SetBytes(data[:32])
			if !start.IsUint64() || start.Uint64() > uint64(len(data)-32) {
				continue
			}
			off := int(start.Uint64())
			size := new(big.Int).SetBytes(data[off : off+32])
			if !size.IsUint64() || size.Uint64() < 12 || size.Uint64() > uint64(len(data)-off-32) {
				continue
			}
			msg := data[off+32 : off+32+int(size.Uint64())]
			source := new(big.Int).SetBytes(msg[4:8]).String()
			destination := new(big.Int).SetBytes(msg[8:12]).String()
			if CCTPDomains[source] != chain || CCTPDomains[destination] == "" {
				continue
			}
			h.Bridge, h.Kind, h.SourceDomain, h.DestinationChain = "cctp", "sent", source, CCTPDomains[destination]
			version := new(big.Int).SetBytes(msg[:4]).Uint64()
			if version == 1 && len(msg) >= 44 {
				h.Identifier = "0x" + hex.EncodeToString(msg[12:44])
			} else if version == 0 && len(msg) >= 20 {
				h.Identifier = new(big.Int).SetBytes(msg[12:20]).String()
			} else {
				continue
			}
		case EventTopic("V3FundsDeposited(address,address,uint256,uint256,uint256,uint32,uint32,uint32,uint32,address,address,address,bytes)"), EventTopic("FundsDeposited(bytes32,bytes32,uint256,uint256,uint256,uint256,uint32,uint32,uint32,bytes32,bytes32,bytes32,bytes)"):
			if len(l.Topics) != 4 || len(data) < 320 || len(l.Topics[3]) != 66 {
				continue
			}
			id, ok := new(big.Int).SetString(strings.TrimPrefix(l.Topics[2], "0x"), 16)
			if !ok {
				continue
			}
			h.Bridge, h.Kind, h.Identifier, h.Recipient = "across", "sent", id.String(), "0x"+l.Topics[3][26:]
			h.DestinationChain = chainKey(jsonNumberFromWord(l.Topics[1]))
		case EventTopic("FilledV3Relay(address,address,uint256,uint256,uint256,uint256,uint32,uint32,uint32,address,address,address,address,bytes,(address,bytes,uint256,uint8))"), EventTopic("FilledRelay(bytes32,bytes32,uint256,uint256,uint256,uint256,uint256,uint32,uint32,bytes32,bytes32,bytes32,bytes32,bytes32,(bytes32,bytes32,uint256,uint8))"):
			if len(l.Topics) != 4 || len(data) < 384 {
				continue
			}
			id, ok := new(big.Int).SetString(strings.TrimPrefix(l.Topics[2], "0x"), 16)
			if !ok {
				continue
			}
			h.Bridge, h.Kind, h.Identifier, h.Recipient = "across", "received", id.String(), "0x"+hex.EncodeToString(data[9*32+12:10*32])
		case EventTopic("OFTSent(bytes32,uint32,address,uint256,uint256)"):
			if len(data) < 96 || len(l.Topics) < 3 || len(l.Topics[1]) != 66 || len(l.Topics[2]) != 66 {
				continue
			}
			h.Bridge, h.Kind, h.Identifier, h.Recipient = "layerzero", "sent", l.Topics[1], "0x"+l.Topics[2][len(l.Topics[2])-40:]
		case EventTopic("OFTReceived(bytes32,uint32,address,uint256)"):
			if len(data) < 64 || len(l.Topics) < 3 || len(l.Topics[1]) != 66 || len(l.Topics[2]) != 66 {
				continue
			}
			h.Bridge, h.Kind, h.Identifier, h.Recipient = "layerzero", "received", l.Topics[1], "0x"+l.Topics[2][len(l.Topics[2])-40:]
		case EventTopic("LogMessagePublished(address,uint64,uint32,bytes,uint8)"):
			if len(data) < 128 || len(l.Topics) < 2 || len(l.Topics[1]) != 66 {
				continue
			}
			sequence := new(big.Int).SetBytes(data[:32])
			if !sequence.IsUint64() {
				continue
			}
			h.Bridge, h.Kind, h.Identifier = "wormhole", "sent", WormholeChainID(chain)+"/"+strings.TrimPrefix(strings.ToLower(l.Topics[1]), "0x")+"/"+sequence.String()
		case EventTopic("TransferRedeemed(uint16,bytes32,uint64)"):
			if len(l.Topics) != 4 || len(l.Topics[2]) != 66 {
				continue
			}
			emitter, err := hex.DecodeString(strings.TrimPrefix(l.Topics[2], "0x"))
			if err != nil || len(emitter) != 32 {
				continue
			}
			id, ok := UintWord(l.Topics[1])
			if !ok || id > 65535 {
				continue
			}
			seq, ok := UintWord(l.Topics[3])
			if !ok {
				continue
			}
			h.Bridge, h.Kind, h.Identifier = "wormhole", "received", decimalUint(id)+"/"+strings.TrimPrefix(strings.ToLower(l.Topics[2]), "0x")+"/"+decimalUint(seq)
		default:
			continue
		}
		if len(out) < 64 {
			out = append(out, h)
		}
	}
	return out
}

func jsonNumberFromWord(word string) string {
	n, ok := new(big.Int).SetString(strings.TrimPrefix(word, "0x"), 16)
	if !ok {
		return ""
	}
	return n.String()
}
