package api

import (
	"encoding/binary"
	"math/big"
	"strings"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
	"github.com/blockchain-platform/blockchain-platform/internal/method"
)

func abiTestWord(v uint64) []byte {
	b := make([]byte, 32)
	binary.BigEndian.PutUint64(b[24:], v)
	return b
}
func abiTestBytes(raw []byte, headSize int) []byte {
	b := append(abiTestWord(uint64(headSize)), abiTestWord(uint64(len(raw)))...)
	b = append(b, raw...)
	for (len(b)-32)%32 != 0 {
		b = append(b, 0)
	}
	return b
}
func TestNextCCTPRejectsSpoofedEmitterNonceAndMint(t *testing.T) {
	source, destination := cctpV1["eth"], cctpV1["base"]
	raw := make([]byte, 248)
	binary.BigEndian.PutUint32(raw[8:12], 6)
	binary.BigEndian.PutUint64(raw[12:20], 123)
	sender, _ := chain.HexToBytes(paddedAddress(source.Messenger))
	recipient, _ := chain.HexToBytes(paddedAddress(destination.Messenger))
	copy(raw[20:52], sender)
	copy(raw[52:84], recipient)
	burnToken := paddedAddress("0x" + strings.Repeat("1", 40))
	mintRecipient := paddedAddress("0x" + strings.Repeat("2", 40))
	tokenBytes, _ := chain.HexToBytes(burnToken)
	recipientBytes, _ := chain.HexToBytes(mintRecipient)
	copy(raw[120:152], tokenBytes)
	copy(raw[152:184], recipientBytes)
	amount, _ := new(big.Int).SetString("12345678901234567890123", 10)
	copy(raw[184:216], amount.FillBytes(make([]byte, 32)))
	sent := chainread.Log{Address: chain.Address(source.Transmitter), Topics: []string{method.Topic0Of("MessageSent(bytes)")}, Data: chain.BytesToHex(abiTestBytes(raw, 32))}
	receivedData := append(abiTestWord(0), sender...)
	receivedData = append(receivedData, abiTestWord(96)...)
	tail := abiTestBytes(raw[116:], 32)[32:]
	receivedData = append(receivedData, tail...)
	received := chainread.Log{Address: chain.Address(destination.Transmitter), LogIndex: 2, Topics: []string{method.Topic0Of("MessageReceived(address,uint32,uint64,bytes32,bytes)"), paddedAddress("0x" + strings.Repeat("3", 40)), chain.BytesToHex(abiTestWord(123))}, Data: chain.BytesToHex(receivedData)}
	mint := chainread.Log{Address: chain.Address(destination.Messenger), LogIndex: 1, Topics: []string{method.Topic0Of("MintAndWithdraw(address,uint256,address)"), mintRecipient, paddedAddress("0x" + strings.Repeat("4", 40))}, Data: chain.BytesToHex(amount.FillBytes(make([]byte, 32)))}
	src, dst := &chainread.Receipt{Logs: []chainread.Log{sent}}, &chainread.Receipt{Logs: []chainread.Log{mint, received}}
	matches, e := cctpMatches(source, destination, src, dst, "")
	if e != nil || len(matches) != 1 {
		t.Fatal(matches, e)
	}
	forged := sent
	forged.Address = chain.Address("0x" + strings.Repeat("5", 40))
	src.Logs = []chainread.Log{forged}
	matches, _ = cctpMatches(source, destination, src, dst, "")
	if len(matches) != 0 {
		t.Fatal("spoofed emitter accepted")
	}
	src.Logs = []chainread.Log{sent}
	wrong := received
	wrong.Topics = append([]string(nil), received.Topics...)
	wrong.Topics[2] = chain.BytesToHex(abiTestWord(124))
	dst.Logs = []chainread.Log{mint, wrong}
	matches, _ = cctpMatches(source, destination, src, dst, "")
	if len(matches) != 0 {
		t.Fatal("wrong nonce accepted")
	}
	wrongMint := mint
	wrongMint.Data = chain.BytesToHex(abiTestWord(1))
	dst.Logs = []chainread.Log{wrongMint, received}
	matches, _ = cctpMatches(source, destination, src, dst, "")
	if len(matches) != 0 {
		t.Fatal("wrong mint accepted")
	}
	for _, bad := range [][]byte{nil, make([]byte, 31), append(abiTestWord(1), abiTestWord(100)...)} {
		if _, e = abiDynamic(bad, 0); e == nil {
			t.Fatal("bad ABI bytes accepted")
		}
	}
}
