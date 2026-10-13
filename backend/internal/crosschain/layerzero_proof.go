package crosschain

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"math/big"
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
)

// A native Stargate send pays bridge principal and messaging fee together.
// Accept only the exact taxi call with no input dust or protocol-token fee.
func StargateNativeSend(p LayerZeroPacket, e DecodedBridgeEvent, call FundingSourceCall) (string, bool) {
	d, ok := proofHex(call.Input)
	if !ok || len(d) < 4+128+224 || !Same(e.SourceChain, call.From, e.Party) || !Same(e.SourceChain, call.To, e.Application) {
		return "", false
	}
	allowed := false
	for _, sig := range []string{"sendToken((uint32,bytes32,uint256,uint256,bytes,bytes,bytes),(uint256,uint256),address)", "send((uint32,bytes32,uint256,uint256,bytes,bytes,bytes),(uint256,uint256),address)"} {
		if hex.EncodeToString(d[:4]) == EventTopic(sig)[2:10] {
			allowed = true
		}
	}
	if !allowed {
		return "", false
	}
	d = d[4:]
	if new(big.Int).SetBytes(abiWord(d, 0)).Cmp(big.NewInt(128)) != 0 {
		return "", false
	}
	fee, ok := abiUint(abiWord(d, 1), 256)
	if !ok || !positiveRaw(fee) || new(big.Int).SetBytes(abiWord(d, 2)).Sign() != 0 {
		return "", false
	}
	ref, ok := abiAddress(abiWord(d, 3))
	if !ok || ref != e.Party {
		return "", false
	}
	tuple := d[128:]
	eid, ok := abiUint(abiWord(tuple, 0), 32)
	if !ok || eid != p.DestinationEID {
		return "", false
	}
	_, recipient, _, ok := StargateTaxi(p)
	if !ok {
		return "", false
	}
	to, ok := abiAddress(abiWord(tuple, 1))
	if !ok || to != recipient {
		return "", false
	}
	amount, ok := abiUint(abiWord(tuple, 2), 256)
	if !ok || amount != e.AmountRaw {
		return "", false
	}
	if _, ok = ABIBytes(tuple, 4, 224); !ok {
		return "", false
	}
	compose, ok := ABIBytes(tuple, 5, 224)
	if !ok {
		return "", false
	}
	cmd, ok := ABIBytes(tuple, 6, 224)
	if !ok || len(cmd) != 0 {
		return "", false
	}
	if len(p.Message) == 43 {
		if len(compose) != 0 {
			return "", false
		}
	} else {
		who, ok := abiAddress(p.Message[43:75])
		if !ok || who != e.Party || !bytes.Equal(p.Message[75:], compose) {
			return "", false
		}
	}
	principal, _ := new(big.Int).SetString(amount, 10)
	feeN, _ := new(big.Int).SetString(fee, 10)
	return fee, new(big.Int).Add(principal, feeN).String() == call.Value
}

// Output-only records; applications and configuration are obtained by the server
// at the receipt block. A query cannot supply this evidence.
type LayerZeroAssociationProof struct {
	Kind         string                       `json:"kind"`
	DeliveryCall FundingSourceCall            `json:"delivery_call"`
	Applications [2]LayerZeroApplicationProof `json:"applications"`
}

type LayerZeroApplicationProof struct {
	Event             ProtocolEventProof `json:"event"`
	MessagingContract *ContractProof     `json:"messaging_contract,omitempty"`
	Getters           map[string]string  `json:"historical_getters"`
	TokenCall         *FundingSourceCall `json:"token_call,omitempty"`
	NativeSendCall    *FundingSourceCall `json:"native_send_call,omitempty"`
	NativeFeeRaw      string             `json:"native_fee_raw,omitempty"`
}

func IsOFTDeployment(d Deployment) bool {
	return d.Protocol == "layerzero" && (d.Role == "usdt0_application" || d.Role == "oft_application")
}

// Stargate uses TokenMessaging as the packet sender; the OFT event is emitted
// by the pool selected by assetId, not by the packet sender itself.
func StargateTaxi(p LayerZeroPacket) (assetID, recipient, amountSD string, ok bool) {
	if len(p.Message) < 43 || p.Message[0] != 1 || len(p.Message) > 43 && len(p.Message) < 76 {
		return
	}
	assetID = new(big.Int).SetUint64(uint64(binary.BigEndian.Uint16(p.Message[1:3]))).String()
	recipient, ok = abiAddress(p.Message[3:35])
	amountSD = new(big.Int).SetUint64(binary.BigEndian.Uint64(p.Message[35:43])).String()
	ok = ok && assetID != "0" && positiveRaw(amountSD)
	return
}

func StargateOFT(c string, logs []chainread.Log, p LayerZeroPacket, app string, source bool) (DecodedBridgeEvent, int) {
	_, recipient, _, ok := StargateTaxi(p)
	if !ok {
		return DecodedBridgeEvent{}, 0
	}
	// Reuse the OFT event layout, substituting the registered pool and the taxi
	// recipient. GUID and remote EID still bind to the original endpoint packet.
	copy := p
	copy.Message = append(make([]byte, 0, 40), p.Message[3:43]...)
	if source {
		copy.Sender = app
	} else {
		copy.Receiver = app
	}
	e, n := LayerZeroOFT(c, logs, copy, source)
	if !source && e.Party != recipient {
		return DecodedBridgeEvent{}, 0
	}
	e.Version = "layerzero-v2-stargate-taxi"
	e.PayloadSHA256 = ProofSHA(append([]byte(p.GUID), p.Message...))
	e.Message = append([]byte(nil), p.Message...)
	return e, n
}

func getterBytes(a LayerZeroApplicationProof, name string) []byte {
	b, ok := proofHex(a.Getters[name])
	if !ok {
		return nil
	}
	return b
}
func getterAddress(a LayerZeroApplicationProof, name string) string {
	v, _ := abiAddress(getterBytes(a, name))
	return v
}
func getterUint(a LayerZeroApplicationProof, name string, bits int) (string, bool) {
	return abiUint(getterBytes(a, name), bits)
}

// Only the deployed USDT0 application's real token burn/mint call can authorize
// a zero-address movement when token and application are separate contracts.
func USDT0TokenCall(e DecodedBridgeEvent, call FundingSourceCall) bool {
	d, registered := FindDeployment("layerzero", e.SourceChain, e.Application)
	if e.Role == "destination" {
		d, registered = FindDeployment("layerzero", e.DestinationChain, e.Application)
	}
	if !registered || d.Role != "usdt0_application" || !strings.EqualFold(call.From, e.Application) || !strings.EqualFold(call.To, e.Asset) || call.Value != "0" {
		return false
	}
	raw, ok := proofHex(call.Input)
	if !ok || len(raw) != 68 {
		return false
	}
	sigs := []string{"burn(address,uint256)", "crosschainBurn(address,uint256)"}
	if e.Role == "destination" {
		sigs = []string{"mint(address,uint256)", "crosschainMint(address,uint256)"}
	}
	allowed := false
	for _, sig := range sigs {
		if hex.EncodeToString(raw[:4]) == EventTopic(sig)[2:10] {
			allowed = true
		}
	}
	party, p := abiAddress(raw[4:36])
	amount, n := abiUint(raw[36:], 256)
	return allowed && p && n && party == e.Party && amount == e.AmountRaw
}

func layerZeroEventLog(e ProtocolEventProof) (chainread.Log, bool) {
	a, err := chain.ParseAddress(e.Contract.Address)
	return chainread.Log{Address: a, LogIndex: uint64(e.EventIndex), Topics: e.RawTopics, Data: e.RawData}, err == nil
}

// Re-decode endpoint and application evidence at the sole promotion gate.
// Neither GUID labels nor precomputed MessageBound flags are sufficient.
func validLayerZeroFunding(p *ProtocolProof, l FundsLegProof, side int) bool {
	if p.LayerZero == nil || side < 0 || side > 1 {
		return false
	}
	proof := p.LayerZero
	sourceLog, ok := layerZeroEventLog(p.Source)
	if !ok {
		return false
	}
	packets := LayerZeroSourcePackets(p.SourceChain, []chainread.Log{sourceLog})
	if len(packets) != 1 {
		return false
	}
	packet := packets[0].Packet
	if packet.GUID != p.Identifier || LayerZeroChain(packet.DestinationEID) != p.DestinationChain || ProofSHA(append([]byte(packet.GUID), packet.Message...)) != p.PayloadSHA256 {
		return false
	}
	destLog, ok := layerZeroEventLog(p.Destination)
	if !ok {
		return false
	}
	if _, n := LayerZeroDelivered(p.DestinationChain, []chainread.Log{destLog}, packet); n != 1 {
		return false
	}
	if !Same(p.DestinationChain, proof.DeliveryCall.To, p.Destination.Contract.Address) || !LayerZeroReceiveCall(proof.DeliveryCall.Input, packet) {
		return false
	}
	a := proof.Applications[side]
	c, app, peer, eid := p.SourceChain, packet.Sender, packet.Receiver, packet.DestinationEID
	if side == 1 {
		c, app, peer, eid = p.DestinationChain, packet.Receiver, packet.Sender, packet.SourceEID
	}
	if a.Event.Chain != c || !Same(c, a.Event.TxHash, l.TxHash) || a.Event.Identifier != packet.GUID || a.Event.PayloadSHA256 != p.PayloadSHA256 {
		return false
	}
	if getterAddress(a, "endpoint()") != DeploymentAddress("layerzero", c, "endpoint_v2") || getterAddress(a, "peers(uint32):"+eid) != peer {
		return false
	}
	appLog, ok := layerZeroEventLog(a.Event)
	if !ok {
		return false
	}
	var e DecodedBridgeEvent
	var n int
	var normalized string
	if proof.Kind == "stargate_taxi" {
		assetID, _, amount, good := StargateTaxi(packet)
		if !good {
			return false
		}
		normalized = amount
		d, registered := FindDeployment("layerzero", c, app)
		mc := a.MessagingContract
		if !registered || d.Role != "stargate_messaging" || mc == nil || mc.Address != app || mc.VerifierAddress != app || !mc.HistoricalState || !evidenceDigest(mc.CodeSHA256) || mc.DeploymentSource != d.Source || mc.Implementation != "" && !evidenceDigest(mc.ImplementationCodeSHA256) {
			return false
		}
		app = getterAddress(a, "stargateImpls(uint16):"+assetID)
		d, registered = FindDeployment("layerzero", c, app)
		if !registered || d.Role != "stargate_pool" || app != appLog.Address.String() {
			return false
		}
		config := getterBytes(a, "getAddressConfig()")
		messenger, valid := abiAddress(abiWord(config, 3))
		if len(config) != 192 || !valid || messenger != mc.Address {
			return false
		}
		e, n = StargateOFT(c, []chainread.Log{appLog}, packet, app, side == 0)
	} else if proof.Kind == "oft" {
		d, registered := FindDeployment("layerzero", c, app)
		if !registered || !IsOFTDeployment(d) {
			return false
		}
		e, n = LayerZeroOFT(c, []chainread.Log{appLog}, packet, side == 0)
		normalized = new(big.Int).SetBytes(packet.Message[32:40]).String()
	} else {
		return false
	}
	if n != 1 || p.Version != e.Version || e.Identifier != p.Identifier || e.Role != l.Role || e.Party != l.Party || e.AmountRaw != l.BridgeAmountRaw {
		return false
	}
	protocolEvent := p.Source
	if side == 1 {
		protocolEvent = p.Destination
	}
	if protocolEvent.ApplicationContract == nil || protocolEvent.ApplicationContract.Address != e.Application {
		return false
	}
	token := getterAddress(a, "token()")
	if token == "0x0000000000000000000000000000000000000000" && proof.Kind == "stargate_taxi" {
		token = "native"
	}
	if token == "" || token != l.Asset || l.BridgeAsset != token {
		return false
	}
	e.Asset = token
	sd, good := getterUint(a, "sharedDecimals()", 8)
	if !good {
		return false
	}
	shared, _ := new(big.Int).SetString(sd, 10)
	if !shared.IsInt64() || shared.Int64() > int64(l.Decimals) || l.Decimals > 38 {
		return false
	}
	remoteSD, good := getterUint(proof.Applications[1-side], "sharedDecimals()", 8)
	if !good || remoteSD != sd {
		return false
	}
	amount, _ := new(big.Int).SetString(normalized, 10)
	amount.Mul(amount, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(l.Decimals)-shared.Int64()), nil))
	want := e.AmountRaw
	if side == 0 {
		want = e.NormalizedAmountRaw
	}
	if amount.String() != want || len(l.Movements) != 1 {
		return false
	}
	m := l.Movements[0]
	if m.TokenContract != l.Asset || m.AmountRaw != l.AmountRaw || m.Decimals != l.Decimals || m.Dataset == "token_transfers" && m.Index >= a.Event.EventIndex {
		return false
	}
	party, other := m.From, m.To
	if side == 1 {
		party, other = m.To, m.From
	}
	if !Same(c, party, l.Party) {
		return false
	}
	if side == 0 && proof.Kind == "stargate_taxi" && token == "native" {
		if a.NativeSendCall == nil || !evidenceDigest(l.TraceSHA256) {
			return false
		}
		fee, valid := StargateNativeSend(packet, e, *a.NativeSendCall)
		if !valid || fee != a.NativeFeeRaw || l.AmountRaw != a.NativeSendCall.Value || len(l.OtherDisbursements) != 1 || !Same(c, other, e.Application) {
			return false
		}
		f := l.OtherDisbursements[0]
		return f.TokenContract == "native" && f.AmountRaw == fee && f.Decimals == 18 && Same(c, f.From, e.Application) && Same(c, f.To, packet.Sender)
	}
	if e.AmountRaw != l.AmountRaw || len(l.OtherDisbursements) != 0 {
		return false
	}
	if Same(c, other, e.Application) {
		return true
	}
	if other != "0x0000000000000000000000000000000000000000" || proof.Kind != "oft" {
		return false
	}
	if Same(c, e.Asset, e.Application) {
		return true
	}
	return a.TokenCall != nil && evidenceDigest(l.TraceSHA256) && USDT0TokenCall(e, *a.TokenCall)
}
