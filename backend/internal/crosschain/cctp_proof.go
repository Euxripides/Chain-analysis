package crosschain

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/url"
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
)

const CCTPMessengerV2 = "0x28b5a0e9c621a5badaa536219b3a228c8168cf5d"
const ZeroEVMAddress = "0x0000000000000000000000000000000000000000"

type CCTPAttestation struct {
	SourceChain  string   `json:"source_chain"`
	SourceTxHash string   `json:"source_tx_hash"`
	Message      string   `json:"message"`
	Attestation  string   `json:"attestation"`
	Nonce        string   `json:"nonce"`
	Evidence     Evidence `json:"evidence"`
}

type CCTPAssociationProof struct {
	CCTPAttestation
	SourceMessageSHA256       string    `json:"source_message_sha256"`
	ExecutedCallMessageSHA256 string    `json:"executed_call_message_sha256"`
	ExecutedAttestationSHA256 string    `json:"executed_attestation_sha256"`
	SourceMinter              string    `json:"source_minter"`
	SourceBurn                Movement  `json:"source_burn"`
	FeeRaw                    string    `json:"fee_raw"`
	FeeMovement               *Movement `json:"fee_movement,omitempty"`
}

// CCTPAttestations binds the off-chain nonce to an exact source transaction.
// Only a fixed official GET endpoint is used; caller-supplied proofs are absent.
func (s *Service) CCTPAttestations(ctx context.Context, c, h string) ([]CCTPAttestation, error) {
	domain := CCTPDomain(c)
	if EVMNetworkIDs[c] == "" || domain == "" {
		return nil, fmt.Errorf("cctp_source_chain_unsupported")
	}
	canonical, err := Hash(c, h)
	if err != nil {
		return nil, err
	}
	v, ev, err := s.fetch(ctx, "cctp", "https://iris-api.circle.com", "/v2/messages/"+url.PathEscape(domain), url.Values{"transactionHash": {canonical}}, nil, nil)
	if err != nil {
		return nil, err
	}
	if !Same(c, str(get(v, "sourceTxHash")), canonical) {
		return nil, fmt.Errorf("cctp_authority_source_transaction_conflict")
	}
	items := arr(get(v, "messages"))
	if items == nil || len(items) > 100 {
		return nil, fmt.Errorf("cctp_authority_message_set_invalid")
	}
	out := []CCTPAttestation{}
	for _, item := range items {
		if str(get(item, "status")) != "complete" || str(get(item, "cctpVersion")) != "2" {
			continue
		}
		msg := str(get(item, "message"))
		m, ok := DecodeCCTPV2Message(msg)
		att := str(get(item, "attestation"))
		sig, valid := proofHex(att)
		if !ok || m.SourceChain != c || !valid || len(sig) == 0 || len(sig)%65 != 0 || len(sig) > 65*100 {
			continue
		}
		if CanonicalNonce(str(get(item, "eventNonce"))) != CanonicalNonce(m.Nonce) {
			continue
		}
		out = append(out, CCTPAttestation{SourceChain: c, SourceTxHash: canonical, Message: msg, Attestation: att, Nonce: m.Nonce, Evidence: ev})
	}
	return out, nil
}

type CCTPMessage struct {
	Raw                                                               []byte
	SourceChain, DestinationChain, Nonce                              string
	Sender, Recipient, DestinationCaller, Asset, Party, MintRecipient string
	AmountRaw, FeeRaw, MaxFeeRaw                                      string
	Finality, MinFinality                                             uint32
}

func DecodeCCTPV2Message(s string) (CCTPMessage, bool) {
	m := CCTPMessage{}
	raw, ok := proofHex(s)
	if !ok || len(raw) < 376 || len(raw) > 65536 || binary.BigEndian.Uint32(raw[:4]) != 1 || binary.BigEndian.Uint32(raw[148:152]) != 1 {
		return m, false
	}
	m.Raw = raw
	m.SourceChain = CCTPDomains[fmt.Sprint(binary.BigEndian.Uint32(raw[4:8]))]
	m.DestinationChain = CCTPDomains[fmt.Sprint(binary.BigEndian.Uint32(raw[8:12]))]
	if EVMNetworkIDs[m.SourceChain] == "" || EVMNetworkIDs[m.DestinationChain] == "" || m.SourceChain == m.DestinationChain {
		return m, false
	}
	m.Nonce = "0x" + hex.EncodeToString(raw[12:44])
	fields := []*string{&m.Sender, &m.Recipient, &m.Asset, &m.MintRecipient, &m.Party}
	positions := []int{44, 76, 152, 184, 248}
	for i, p := range positions {
		var valid bool
		*fields[i], valid = abiAddress(raw[p : p+32])
		if !valid {
			return m, false
		}
	}
	if !bytes.Equal(raw[108:120], make([]byte, 12)) {
		return m, false
	}
	m.DestinationCaller = "0x" + hex.EncodeToString(raw[120:140])
	m.MinFinality = binary.BigEndian.Uint32(raw[140:144])
	m.Finality = binary.BigEndian.Uint32(raw[144:148])
	amount := new(big.Int).SetBytes(raw[216:248])
	fee := new(big.Int).SetBytes(raw[312:344])
	maxFee := new(big.Int).SetBytes(raw[280:312])
	if amount.Sign() <= 0 || maxFee.Cmp(amount) >= 0 || fee.Cmp(maxFee) > 0 {
		return m, false
	}
	m.AmountRaw, m.FeeRaw, m.MaxFeeRaw = amount.String(), fee.String(), maxFee.String()
	if m.Sender != CCTPMessengerV2 || m.Recipient != CCTPMessengerV2 || m.Asset != CCTPUSDC[m.SourceChain] {
		return m, false
	}
	return m, true
}

// Only fields explicitly assigned by Iris may differ from MessageSent bytes.
func CCTPSourceMatches(c string, l chainread.Log, a CCTPAttestation) (DecodedBridgeEvent, bool) {
	e := DecodedBridgeEvent{Protocol: "cctp", Version: "cctp-v2", Role: "source", Log: l}
	m, ok := DecodeCCTPV2Message(a.Message)
	d, valid := proofHex(l.Data)
	if !ok || !valid || m.SourceChain != c || a.SourceChain != c || !Same(c, a.SourceTxHash, l.TxHash.String()) || l.Removed || !Same(c, l.Address.String(), CCTPTransmitterV2) || len(l.Topics) != 1 || l.Topics[0] != EventTopic("MessageSent(bytes)") {
		return e, false
	}
	source, ok := ABIBytes(d, 0, 32)
	if !ok || len(source) != len(m.Raw) {
		return e, false
	}
	// Nonce, executed finality, fee and expiration are zero placeholders on source.
	for _, span := range [][2]int{{12, 44}, {144, 148}, {312, 376}} {
		if !bytes.Equal(source[span[0]:span[1]], make([]byte, span[1]-span[0])) {
			return e, false
		}
	}
	for _, span := range [][2]int{{0, 12}, {44, 144}, {148, 312}, {376, len(source)}} {
		if !bytes.Equal(source[span[0]:span[1]], m.Raw[span[0]:span[1]]) {
			return e, false
		}
	}
	if new(big.Int).SetBytes(m.Raw[12:44]).Sign() == 0 || m.Finality < 1000 || m.Finality < minCCTPFinality(m.MinFinality) {
		return e, false
	}
	e.SourceChain, e.DestinationChain = m.SourceChain, m.DestinationChain
	e.Identifier = CCTPDomain(c) + "/" + m.Nonce
	e.PayloadSHA256 = ProofSHA(m.Raw)
	e.Message = m.Raw
	e.Party, e.Asset, e.AmountRaw, e.Application = m.Party, m.Asset, m.AmountRaw, CCTPMessengerV2
	return e, true
}

func minCCTPFinality(n uint32) uint32 {
	if n > 1000 {
		return 2000
	}
	return 1000
}

func CCTPReceivedMatches(c string, l chainread.Log, a CCTPAttestation) (DecodedBridgeEvent, bool) {
	e := DecodedBridgeEvent{Protocol: "cctp", Version: "cctp-v2", Role: "destination", Log: l}
	m, ok := DecodeCCTPV2Message(a.Message)
	d, valid := proofHex(l.Data)
	if !ok || !valid || m.DestinationChain != c || l.Removed || !Same(c, l.Address.String(), CCTPTransmitterV2) || len(l.Topics) != 4 || l.Topics[0] != EventTopic("MessageReceived(address,uint32,bytes32,bytes32,uint32,bytes)") || !strings.EqualFold(l.Topics[2], m.Nonce) {
		return e, false
	}
	finalWord, ok := topicBytes(l.Topics[3])
	if !ok || new(big.Int).SetBytes(finalWord).Cmp(new(big.Int).SetUint64(uint64(m.Finality))) != 0 {
		return e, false
	}
	domain, ok := abiUint(abiWord(d, 0), 32)
	if !ok || domain != CCTPDomain(m.SourceChain) || !bytes.Equal(abiWord(d, 1), m.Raw[44:76]) {
		return e, false
	}
	callerWord, ok := topicBytes(l.Topics[1])
	if !ok {
		return e, false
	}
	caller, ok := abiAddress(callerWord)
	if !ok || m.DestinationCaller != ZeroEVMAddress && m.DestinationCaller != caller {
		return e, false
	}
	body, ok := ABIBytes(d, 2, 96)
	if !ok || !bytes.Equal(body, m.Raw[148:]) {
		return e, false
	}
	amount, _ := new(big.Int).SetString(m.AmountRaw, 10)
	fee, _ := new(big.Int).SetString(m.FeeRaw, 10)
	e.SourceChain, e.DestinationChain = m.SourceChain, m.DestinationChain
	e.Identifier = CCTPDomain(m.SourceChain) + "/" + m.Nonce
	e.PayloadSHA256 = ProofSHA(m.Raw)
	e.Message = m.Raw
	e.Party, e.Asset, e.AmountRaw, e.Application, e.Counterparty = m.MintRecipient, CCTPUSDC[c], new(big.Int).Sub(amount, fee).String(), CCTPMessengerV2, ZeroEVMAddress
	return e, true
}

func CCTPReceiveCall(input string, a CCTPAttestation) bool {
	raw, ok := proofHex(input)
	if !ok || len(raw) < 68 || !bytes.Equal(raw[:4], Keccak([]byte("receiveMessage(bytes,bytes)"))[:4]) {
		return false
	}
	msg, ok := ABIBytes(raw[4:], 0, 64)
	sig, valid := ABIBytes(raw[4:], 1, 64)
	want, wm := proofHex(a.Message)
	ws, wa := proofHex(a.Attestation)
	return ok && valid && wm && wa && len(sig) > 0 && len(sig)%65 == 0 && bytes.Equal(msg, want) && bytes.Equal(sig, ws)
}

func CCTPMintEvent(c string, l chainread.Log, a CCTPAttestation) bool {
	m, ok := DecodeCCTPV2Message(a.Message)
	d, valid := proofHex(l.Data)
	if !ok || !valid || c != m.DestinationChain || l.Removed || !Same(c, l.Address.String(), CCTPMessengerV2) || len(l.Topics) != 3 || l.Topics[0] != EventTopic("MintAndWithdraw(address,uint256,address,uint256)") || len(d) != 64 {
		return false
	}
	who, ok := topicBytes(l.Topics[1])
	token, valid := topicBytes(l.Topics[2])
	if !ok || !valid {
		return false
	}
	recipient, ok := abiAddress(who)
	asset, valid := abiAddress(token)
	if !ok || !valid || recipient != m.MintRecipient || asset != CCTPUSDC[c] {
		return false
	}
	amount, _ := new(big.Int).SetString(m.AmountRaw, 10)
	fee, _ := new(big.Int).SetString(m.FeeRaw, 10)
	return new(big.Int).SetBytes(d[:32]).Cmp(new(big.Int).Sub(amount, fee)) == 0 && new(big.Int).SetBytes(d[32:]).Cmp(fee) == 0
}

func validCCTPFunding(p *ProtocolProof, l FundsLegProof, side int) bool {
	a := p.CCTP
	if a == nil || p.Version != "cctp-v2" || !Same(p.SourceChain, a.SourceTxHash, p.Source.TxHash) || a.SourceChain != p.SourceChain || !evidenceDigest(a.Evidence.SHA256) || a.Evidence.FetchedAt.IsZero() || a.Evidence.PageTruncated {
		return false
	}
	u, err := url.Parse(a.Evidence.Source)
	if err != nil || u.Scheme != "https" || u.Host != "iris-api.circle.com" || u.Path != "/v2/messages/"+CCTPDomain(p.SourceChain) || !Same(p.SourceChain, u.Query().Get("transactionHash"), p.Source.TxHash) {
		return false
	}
	m, ok := DecodeCCTPV2Message(a.Message)
	sig, valid := proofHex(a.Attestation)
	if !ok || !valid || len(sig) == 0 || len(sig)%65 != 0 || a.Nonce != m.Nonce || p.PayloadSHA256 != ProofSHA(m.Raw) || a.ExecutedCallMessageSHA256 != p.PayloadSHA256 || a.ExecutedAttestationSHA256 != ProofSHA(sig) {
		return false
	}
	e := p.Source
	if side == 1 {
		e = p.Destination
	}
	log := chainread.Log{Address: chain.Address(e.Contract.Address), TxHash: chain.Hash(e.TxHash), Topics: e.RawTopics, Data: e.RawData, BlockNumber: l.Block.Number}
	var decoded DecodedBridgeEvent
	if side == 0 {
		decoded, ok = CCTPSourceMatches(l.Chain, log, a.CCTPAttestation)
	} else {
		decoded, ok = CCTPReceivedMatches(l.Chain, log, a.CCTPAttestation)
	}
	if !ok || decoded.Identifier != p.Identifier || decoded.PayloadSHA256 != p.PayloadSHA256 || l.Party != decoded.Party || l.Asset != decoded.Asset || l.BridgeAsset != decoded.Asset || l.AmountRaw != decoded.AmountRaw || l.BridgeAmountRaw != decoded.AmountRaw || l.Decimals != 6 || len(l.Movements) != 1 {
		return false
	}
	movement := l.Movements[0]
	if movement.TokenContract != l.Asset || movement.AmountRaw != l.AmountRaw {
		return false
	}
	if side == 0 {
		raw, _ := proofHex(log.Data)
		sent, ok := ABIBytes(raw, 0, 32)
		burn := a.SourceBurn
		return ok && a.SourceMessageSHA256 == ProofSHA(sent) && completeMovement(l.Chain, l.TxHash, burn) && burn.From == a.SourceMinter && burn.To == ZeroEVMAddress && burn.TokenContract == l.Asset && burn.AmountRaw == l.AmountRaw && burn.Decimals == 6 && movement.From == l.Party && movement.To == a.SourceMinter && movement.Index < burn.Index && burn.Index < e.EventIndex
	}
	if movement.From != ZeroEVMAddress || movement.To != l.Party || a.FeeRaw != m.FeeRaw {
		return false
	}
	if m.FeeRaw == "0" {
		return a.FeeMovement == nil
	}
	f := a.FeeMovement
	return f != nil && completeMovement(l.Chain, l.TxHash, *f) && f.From == ZeroEVMAddress && f.TokenContract == l.Asset && f.AmountRaw == m.FeeRaw && f.Decimals == 6 && f.Identity() != movement.Identity() && f.Index < e.EventIndex
}
