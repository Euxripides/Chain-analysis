package crosschain

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
	"golang.org/x/crypto/sha3"
)

// DecodedBridgeEvent contains amounts and participants decoded from the actual
// protocol event. API estimates and caller search filters are not used here.
type DecodedBridgeEvent struct {
	Protocol, Version, Role, SourceChain, DestinationChain string
	Identifier, PayloadSHA256, DepositID                   string
	Party, Asset, AmountRaw, Counterparty, Application     string
	Log                                                    chainread.Log
	Message                                                []byte
	OriginAsset, OriginChain, NormalizedAmountRaw          string
}

func Keccak(data []byte) []byte   { h := sha3.NewLegacyKeccak256(); h.Write(data); return h.Sum(nil) }
func ProofSHA(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }
func proofHex(s string) ([]byte, bool) {
	if !strings.HasPrefix(s, "0x") || len(s)%2 != 0 {
		return nil, false
	}
	b, e := hex.DecodeString(s[2:])
	return b, e == nil
}
func abiWord(data []byte, index int) []byte {
	if index < 0 || index >= len(data)/32 {
		return nil
	}
	return data[index*32 : (index+1)*32]
}
func abiUint(w []byte, bits int) (string, bool) {
	if len(w) != 32 {
		return "", false
	}
	n := new(big.Int).SetBytes(w)
	return n.String(), n.BitLen() <= bits
}
func abiAddress(w []byte) (string, bool) {
	if len(w) != 32 || !bytes.Equal(w[:12], make([]byte, 12)) {
		return "", false
	}
	s := "0x" + hex.EncodeToString(w[12:])
	return s, strings.Trim(s[2:], "0") != ""
}
func topicBytes(s string) ([]byte, bool) { b, ok := proofHex(s); return b, ok && len(b) == 32 }

// ABIBytes rejects unaligned offsets, head overlap, oversized bodies, and bad
// padding. Its bounds are checked before converting any integer to an index.
func ABIBytes(data []byte, word, headBytes int) ([]byte, bool) {
	off := new(big.Int).SetBytes(abiWord(data, word))
	if len(abiWord(data, word)) != 32 || !off.IsUint64() {
		return nil, false
	}
	o := off.Uint64()
	if o%32 != 0 || o < uint64(headBytes) || o > uint64(len(data)) || uint64(len(data))-o < 32 {
		return nil, false
	}
	size := new(big.Int).SetBytes(data[o : o+32])
	if !size.IsUint64() {
		return nil, false
	}
	n := size.Uint64()
	if n > 65536 || n > uint64(len(data))-o-32 {
		return nil, false
	}
	end := o + 32 + n
	padEnd := (end + 31) / 32 * 32
	if padEnd > uint64(len(data)) || !bytes.Equal(data[end:padEnd], make([]byte, padEnd-end)) {
		return nil, false
	}
	return append([]byte(nil), data[o+32:end]...), true
}

const acrossDepositOld = "V3FundsDeposited(address,address,uint256,uint256,uint256,uint32,uint32,uint32,uint32,address,address,address,bytes)"
const acrossDepositNew = "FundsDeposited(bytes32,bytes32,uint256,uint256,uint256,uint256,uint32,uint32,uint32,bytes32,bytes32,bytes32,bytes)"
const acrossFillOld = "FilledV3Relay(address,address,uint256,uint256,uint256,uint256,uint32,uint32,uint32,address,address,address,address,bytes,(address,bytes,uint256,uint8))"
const acrossFillNew = "FilledRelay(bytes32,bytes32,uint256,uint256,uint256,uint256,uint256,uint32,uint32,bytes32,bytes32,bytes32,bytes32,bytes32,(bytes32,bytes32,uint256,uint8))"

type acrossRelay struct{ Source, Destination, ID, Depositor, Recipient, Exclusive, Input, Output, InputAmount, OutputAmount, FillDeadline, ExclusivityDeadline, MessageHash string }

func DecodeAcross(c string, l chainread.Log) (DecodedBridgeEvent, bool) {
	e := DecodedBridgeEvent{Protocol: "across", Version: "across-v3", Log: l}
	dep, ok := FindDeployment("across", c, l.Address.String())
	if !ok || dep.Role != "spoke_pool" || l.BlockNumber < dep.FromBlock || l.Removed || len(l.Topics) != 4 {
		return e, false
	}
	d, ok := proofHex(l.Data)
	if !ok || len(d)%32 != 0 {
		return e, false
	}
	source := l.Topics[0] == EventTopic(acrossDepositOld) || l.Topics[0] == EventTopic(acrossDepositNew)
	old := l.Topics[0] == EventTopic(acrossDepositOld) || l.Topics[0] == EventTopic(acrossFillOld)
	if !source && l.Topics[0] != EventTopic(acrossFillOld) && l.Topics[0] != EventTopic(acrossFillNew) {
		return e, false
	}
	min := 320
	if !source {
		min = 480
		if old {
			min = 384
		}
	}
	if len(d) < min {
		return e, false
	}
	chainWord, ok := topicBytes(l.Topics[1])
	if !ok {
		return e, false
	}
	network, ok := abiUint(chainWord, 64)
	if !ok {
		return e, false
	}
	other := chainKey(network)
	if EVMNetworkIDs[other] == "" || other == c {
		return e, false
	}
	idWord, ok := topicBytes(l.Topics[2])
	if !ok {
		return e, false
	}
	bits := 256
	if old {
		bits = 32
	}
	id, ok := abiUint(idWord, bits)
	if !ok {
		return e, false
	}
	whoWord, ok := topicBytes(l.Topics[3])
	if !ok {
		return e, false
	}
	who, ok := abiAddress(whoWord)
	if !ok {
		return e, false
	}
	r := acrossRelay{ID: id}
	if source {
		r.Source, r.Destination = c, other
		e.Role = "source"
	} else {
		r.Source, r.Destination = other, c
		e.Role = "destination"
	}
	r.Input, ok = abiAddress(abiWord(d, 0))
	if !ok {
		return e, false
	}
	r.Output, ok = abiAddress(abiWord(d, 1))
	if !ok {
		return e, false
	}
	r.InputAmount, _ = abiUint(abiWord(d, 2), 256)
	r.OutputAmount, _ = abiUint(abiWord(d, 3), 256)
	if !positiveRaw(r.InputAmount) || !positiveRaw(r.OutputAmount) {
		return e, false
	}
	if source {
		r.Depositor = who
		r.Recipient, ok = abiAddress(abiWord(d, 7))
		if !ok {
			return e, false
		}
		r.Exclusive = "0x" + hex.EncodeToString(abiWord(d, 8))
		r.FillDeadline, ok = abiUint(abiWord(d, 5), 32)
		if !ok {
			return e, false
		}
		r.ExclusivityDeadline, ok = abiUint(abiWord(d, 6), 32)
		if !ok {
			return e, false
		}
		if _, ok = abiUint(abiWord(d, 4), 32); !ok {
			return e, false
		}
		msg, valid := ABIBytes(d, 9, 320)
		if !valid {
			return e, false
		}
		r.MessageHash = hex.EncodeToString(AcrossMessageHash(msg))
		e.Party, e.Asset, e.AmountRaw, e.Counterparty = r.Depositor, r.Input, r.InputAmount, l.Address.String()
	} else {
		r.Depositor, ok = abiAddress(abiWord(d, 8))
		if !ok {
			return e, false
		}
		r.Recipient, ok = abiAddress(abiWord(d, 9))
		if !ok {
			return e, false
		}
		r.Exclusive = "0x" + hex.EncodeToString(abiWord(d, 7))
		r.FillDeadline, ok = abiUint(abiWord(d, 5), 32)
		if !ok {
			return e, false
		}
		r.ExclusivityDeadline, ok = abiUint(abiWord(d, 6), 32)
		if !ok {
			return e, false
		}
		var recipient, amount, fillType string
		if old {
			msg, valid := ABIBytes(d, 10, 384)
			if !valid {
				return e, false
			}
			r.MessageHash = hex.EncodeToString(AcrossMessageHash(msg))
			tuple := new(big.Int).SetBytes(abiWord(d, 11))
			if !tuple.IsUint64() || tuple.Uint64()%32 != 0 || tuple.Uint64() < 384 || tuple.Uint64() > uint64(len(d)) || uint64(len(d))-tuple.Uint64() < 128 {
				return e, false
			}
			td := d[tuple.Uint64():]
			recipient, ok = abiAddress(abiWord(td, 0))
			if !ok {
				return e, false
			}
			if _, valid = ABIBytes(td, 1, 128); !valid {
				return e, false
			}
			amount, _ = abiUint(abiWord(td, 2), 256)
			fillType, ok = abiUint(abiWord(td, 3), 8)
			if !ok {
				return e, false
			}
		} else {
			r.MessageHash = hex.EncodeToString(abiWord(d, 10))
			recipient, ok = abiAddress(abiWord(d, 11))
			if !ok {
				return e, false
			}
			amount, _ = abiUint(abiWord(d, 13), 256)
			fillType, ok = abiUint(abiWord(d, 14), 8)
			if !ok {
				return e, false
			}
		}
		if !positiveRaw(amount) || fillType != "0" && fillType != "1" && fillType != "2" {
			return e, false
		}
		e.Party, e.Asset, e.AmountRaw, e.Counterparty = recipient, r.Output, amount, who
		if fillType == "2" {
			e.Counterparty = l.Address.String()
		}
	}
	raw, _ := json.Marshal(r)
	e.SourceChain, e.DestinationChain = r.Source, r.Destination
	e.DepositID = id
	e.Identifier = strings.Join([]string{"across-v3", r.Source, DeploymentAddress("across", r.Source, "spoke_pool"), r.ID, r.Destination}, "/")
	e.PayloadSHA256 = ProofSHA(raw)
	return e, true
}

// LayerZero's GUID identifies the complete header. The delivery call must also
// contain the exact source payload; a same-name OFT event alone is insufficient.
type LayerZeroPacket struct {
	Nonce, SourceEID, DestinationEID, Sender, Receiver, GUID string
	Message                                                  []byte
}

func DecodeLayerZeroPacket(payload []byte) (LayerZeroPacket, bool) {
	p := LayerZeroPacket{}
	if len(payload) < 153 || len(payload) > 65536 || payload[0] != 1 {
		return p, false
	}
	p.Nonce = fmt.Sprint(binary.BigEndian.Uint64(payload[1:9]))
	p.SourceEID = fmt.Sprint(binary.BigEndian.Uint32(payload[9:13]))
	p.DestinationEID = fmt.Sprint(binary.BigEndian.Uint32(payload[45:49]))
	var ok bool
	p.Sender, ok = abiAddress(payload[13:45])
	if !ok {
		return p, false
	}
	p.Receiver, ok = abiAddress(payload[49:81])
	if !ok {
		return p, false
	}
	p.GUID = "0x" + hex.EncodeToString(payload[81:113])
	p.Message = append([]byte(nil), payload[113:]...)
	if p.GUID != "0x"+hex.EncodeToString(Keccak(payload[1:81])) || LayerZeroChain(p.SourceEID) == "" || LayerZeroChain(p.DestinationEID) == "" || p.SourceEID == p.DestinationEID {
		return p, false
	}
	return p, true
}
func LayerZeroSourcePackets(c string, logs []chainread.Log) []struct {
	Packet LayerZeroPacket
	Log    chainread.Log
} {
	out := []struct {
		Packet LayerZeroPacket
		Log    chainread.Log
	}{}
	for _, l := range logs {
		if l.Removed || !Same(c, l.Address.String(), DeploymentAddress("layerzero", c, "endpoint_v2")) || len(l.Topics) != 1 || l.Topics[0] != EventTopic("PacketSent(bytes,bytes,address)") {
			continue
		}
		d, ok := proofHex(l.Data)
		if !ok {
			continue
		}
		raw, ok := ABIBytes(d, 0, 96)
		if !ok {
			continue
		}
		p, ok := DecodeLayerZeroPacket(raw)
		if ok && LayerZeroChain(p.SourceEID) == c {
			out = append(out, struct {
				Packet LayerZeroPacket
				Log    chainread.Log
			}{p, l})
		}
	}
	return out
}
func LayerZeroDelivered(c string, logs []chainread.Log, p LayerZeroPacket) (chainread.Log, int) {
	var found chainread.Log
	n := 0
	for _, l := range logs {
		if l.Removed || !Same(c, l.Address.String(), DeploymentAddress("layerzero", c, "endpoint_v2")) || len(l.Topics) != 1 || l.Topics[0] != EventTopic("PacketDelivered((uint32,bytes32,uint64),address)") {
			continue
		}
		d, ok := proofHex(l.Data)
		if !ok || len(d) != 128 {
			continue
		}
		eid, a := abiUint(abiWord(d, 0), 32)
		sender, b := abiAddress(abiWord(d, 1))
		nonce, v := abiUint(abiWord(d, 2), 64)
		receiver, z := abiAddress(abiWord(d, 3))
		if a && b && v && z && eid == p.SourceEID && sender == p.Sender && nonce == p.Nonce && receiver == p.Receiver {
			found = l
			n++
		}
	}
	return found, n
}
func LayerZeroOFT(c string, logs []chainread.Log, p LayerZeroPacket, source bool) (DecodedBridgeEvent, int) {
	e := DecodedBridgeEvent{Protocol: "layerzero", Version: "layerzero-v2-oft", Identifier: p.GUID, SourceChain: LayerZeroChain(p.SourceEID), DestinationChain: LayerZeroChain(p.DestinationEID), PayloadSHA256: ProofSHA(append([]byte(p.GUID), p.Message...)), Message: p.Message}
	n := 0
	if len(p.Message) < 40 {
		return e, 0
	}
	app, sig, eid := p.Receiver, "OFTReceived(bytes32,uint32,address,uint256)", p.SourceEID
	e.Role = "destination"
	if source {
		app, sig, eid = p.Sender, "OFTSent(bytes32,uint32,address,uint256,uint256)", p.DestinationEID
		e.Role = "source"
	}
	recipient, ok := abiAddress(p.Message[:32])
	if !ok {
		return e, 0
	}
	for _, l := range logs {
		if l.Removed || !Same(c, l.Address.String(), app) || len(l.Topics) != 3 || l.Topics[0] != EventTopic(sig) || !strings.EqualFold(l.Topics[1], p.GUID) {
			continue
		}
		d, ok := proofHex(l.Data)
		min := 64
		if source {
			min = 96
		}
		if !ok || len(d) != min {
			continue
		}
		remote, v := abiUint(abiWord(d, 0), 32)
		whoWord, v2 := topicBytes(l.Topics[2])
		who, v3 := abiAddress(whoWord)
		raw, v4 := abiUint(abiWord(d, 1), 256)
		if !v || !v2 || !v3 || !v4 || remote != eid || !positiveRaw(raw) {
			continue
		}
		if !source && who != recipient {
			continue
		}
		e.Party, e.AmountRaw, e.Application, e.Log = who, raw, app, l
		if source {
			received, ok := abiUint(abiWord(d, 2), 256)
			if !ok || !positiveRaw(received) {
				continue
			}
			e.NormalizedAmountRaw = received
		}
		e.Counterparty = app
		n++
	}
	return e, n
}
func LayerZeroReceiveCall(input string, p LayerZeroPacket) bool {
	d, ok := proofHex(input)
	if !ok || len(d) < 4+224 || hex.EncodeToString(d[:4]) != EventTopic("lzReceive((uint32,bytes32,uint64),address,bytes32,bytes,bytes)")[2:10] {
		return false
	}
	d = d[4:]
	eid, a := abiUint(abiWord(d, 0), 32)
	sender, b := abiAddress(abiWord(d, 1))
	nonce, c := abiUint(abiWord(d, 2), 64)
	receiver, v := abiAddress(abiWord(d, 3))
	message, z := ABIBytes(d, 5, 224)
	return a && b && c && v && z && eid == p.SourceEID && sender == p.Sender && nonce == p.Nonce && receiver == p.Receiver && "0x"+hex.EncodeToString(abiWord(d, 4)) == p.GUID && bytes.Equal(message, p.Message)
}

func DecodeWormholePublished(c string, l chainread.Log) (DecodedBridgeEvent, bool) {
	e := DecodedBridgeEvent{Protocol: "wormhole", Version: "wormhole-token-bridge", Role: "source", SourceChain: c, Log: l}
	if l.Removed || !Same(c, l.Address.String(), DeploymentAddress("wormhole", c, "core_bridge")) || len(l.Topics) != 2 || l.Topics[0] != EventTopic("LogMessagePublished(address,uint64,uint32,bytes,uint8)") {
		return e, false
	}
	emitterWord, ok := topicBytes(l.Topics[1])
	if !ok {
		return e, false
	}
	emitter, ok := abiAddress(emitterWord)
	if !ok || !Same(c, emitter, DeploymentAddress("wormhole", c, "token_bridge")) {
		return e, false
	}
	d, ok := proofHex(l.Data)
	if !ok || len(d) < 128 {
		return e, false
	}
	seq, ok := abiUint(abiWord(d, 0), 64)
	if !ok {
		return e, false
	}
	payload, ok := ABIBytes(d, 2, 128)
	if !ok || len(payload) < 133 || payload[0] != 1 && payload[0] != 3 {
		return e, false
	}
	if payload[0] == 1 && len(payload) != 133 {
		return e, false
	}
	if payload[0] == 3 && len(payload) < 133 {
		return e, false
	}
	e.Identifier = WormholeChainID(c) + "/" + hex.EncodeToString(emitterWord) + "/" + seq
	e.Message = payload
	e.PayloadSHA256 = ProofSHA(payload)
	e.NormalizedAmountRaw = new(big.Int).SetBytes(payload[1:33]).String()
	e.OriginAsset = "0x" + hex.EncodeToString(payload[33:65])
	e.OriginChain = fmt.Sprint(binary.BigEndian.Uint16(payload[65:67]))
	e.Party, ok = abiAddress(payload[67:99])
	if !ok {
		return e, false
	}
	e.DestinationChain = wormholeChains[fmt.Sprint(binary.BigEndian.Uint16(payload[99:101]))]
	e.Counterparty = emitter
	return e, EVMNetworkIDs[e.DestinationChain] != "" && e.DestinationChain != c && positiveRaw(e.NormalizedAmountRaw)
}
func WormholeRedeemed(c string, l chainread.Log, e DecodedBridgeEvent) bool {
	if l.Removed || !Same(c, l.Address.String(), DeploymentAddress("wormhole", c, "token_bridge")) || len(l.Topics) != 4 || l.Topics[0] != EventTopic("TransferRedeemed(uint16,bytes32,uint64)") {
		return false
	}
	origin, ok := UintWord(l.Topics[1])
	if !ok || origin > 65535 {
		return false
	}
	seq, ok := UintWord(l.Topics[3])
	if !ok {
		return false
	}
	return fmt.Sprint(origin)+"/"+strings.TrimPrefix(strings.ToLower(l.Topics[2]), "0x")+"/"+fmt.Sprint(seq) == e.Identifier
}

// The VAA is accepted only from a successful CALL to the deployed TokenBridge,
// not from an arbitrary occurrence of its bytes in transaction input.
func WormholeRedeemCall(input string, e DecodedBridgeEvent) bool {
	d, ok := proofHex(input)
	if !ok || len(d) < 68 {
		return false
	}
	sel := hex.EncodeToString(d[:4])
	allowed := false
	for _, s := range []string{"completeTransfer(bytes)", "completeTransferWithPayload(bytes)", "completeTransferAndUnwrapETH(bytes)", "completeTransferAndUnwrapETHWithPayload(bytes)"} {
		if sel == EventTopic(s)[2:10] {
			allowed = true
		}
	}
	if !allowed {
		return false
	}
	vaa, ok := ABIBytes(d[4:], 0, 32)
	if !ok || len(vaa) < 6 || vaa[0] != 1 {
		return false
	}
	sigs := int(vaa[5])
	off := 6 + sigs*66
	if sigs == 0 || off > len(vaa) || len(vaa)-off < 51 {
		return false
	}
	body := vaa[off:]
	origin := fmt.Sprint(binary.BigEndian.Uint16(body[8:10]))
	seq := fmt.Sprint(binary.BigEndian.Uint64(body[42:50]))
	return origin+"/"+hex.EncodeToString(body[10:42])+"/"+seq == e.Identifier && bytes.Equal(body[51:], e.Message)
}

// Across uses bytes32(0) for an empty message.
func AcrossMessageHash(data []byte) []byte {
	if len(data) == 0 {
		return make([]byte, 32)
	}
	return Keccak(data)
}
