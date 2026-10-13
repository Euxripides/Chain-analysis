package crosschain

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
)

func primaryFixtureLogs(t *testing.T, id, side string) (string, []chainread.Log) {
	t.Helper()
	raw, e := os.ReadFile("testdata/" + id + "-" + side + ".json")
	if e != nil {
		t.Fatal(e)
	}
	var v struct {
		Leg    Leg
		Result struct {
			Structured struct {
				Data struct {
					Receipt struct {
						Logs []struct {
							Address                                      string
							Topics                                       []string
							Data, BlockNumber, TransactionHash, LogIndex string
							Removed                                      bool
						}
					} `json:"result"`
				} `json:"data"`
			} `json:"structuredContent"`
		} `json:"result"`
	}
	if e = json.Unmarshal(raw, &v); e != nil {
		t.Fatal(e)
	}
	logs := []chainread.Log{}
	for _, l := range v.Result.Structured.Data.Receipt.Logs {
		a, e := chain.ParseAddress(l.Address)
		if e != nil {
			t.Fatal(e)
		}
		num, _ := chain.HexToUint64(l.BlockNumber)
		i, _ := chain.HexToUint64(l.LogIndex)
		logs = append(logs, chainread.Log{Address: a, Topics: l.Topics, Data: l.Data, BlockNumber: num, TxHash: chain.Hash(l.TransactionHash), LogIndex: i, Removed: l.Removed})
	}
	return v.Leg.Chain, logs
}

func TestWormholePublishedAndRedeemedAgainstIndependentRealReceipts(t *testing.T) {
	c, left := primaryFixtureLogs(t, "B49", "source")
	d, right := primaryFixtureLogs(t, "B49", "destination")
	var event DecodedBridgeEvent
	found := 0
	for _, l := range left {
		if e, ok := DecodeWormholePublished(c, l); ok {
			event = e
			found++
		}
	}
	if found != 1 || event.DestinationChain != d || event.NormalizedAmountRaw != "123676979877" {
		t.Fatal("published message unavailable", found, event)
	}
	fills := 0
	for _, l := range right {
		if WormholeRedeemed(d, l, event) {
			fills++
		}
	}
	if fills != 1 {
		t.Fatal("redeemed identifier mismatch", fills, event.Identifier)
	}
	event.Identifier += "1"
	for _, l := range right {
		if WormholeRedeemed(d, l, event) {
			t.Fatal("different sequence was matched")
		}
	}
}
func TestAcrossCompleteIdentityAgainstIndependentRealReceipts(t *testing.T) {
	for _, id := range []string{"B02", "B31", "B46", "B47", "H01", "H02"} {
		t.Run(id, func(t *testing.T) {
			a, src := primaryFixtureLogs(t, id, "source")
			z, dst := primaryFixtureLogs(t, id, "destination")
			left, right := []DecodedBridgeEvent{}, []DecodedBridgeEvent{}
			for _, l := range src {
				if e, ok := DecodeAcross(a, l); ok {
					left = append(left, e)
				}
			}
			for _, l := range dst {
				if e, ok := DecodeAcross(z, l); ok {
					right = append(right, e)
				}
			}
			if len(left) != 1 || len(right) != 1 || left[0].Identifier != right[0].Identifier || left[0].PayloadSHA256 != right[0].PayloadSHA256 {
				t.Fatalf("real message association failed %d %d %+v %+v", len(left), len(right), left, right)
			}
			if left[0].Party == "" || right[0].Party == "" || left[0].SourceChain != a || right[0].DestinationChain != z {
				t.Fatal("actual participants or chain identity lost")
			}
			fake := left[0].Log
			fake.Address = chain.Address("0x" + strings.Repeat("9", 40))
			if _, ok := DecodeAcross(a, fake); ok {
				t.Fatal("fake event emitter accepted")
			}
			removed := left[0].Log
			removed.Removed = true
			if _, ok := DecodeAcross(a, removed); ok {
				t.Fatal("removed event accepted")
			}
			wrong := right[0].Log
			wrong.Topics = append([]string(nil), wrong.Topics...)
			wrong.Topics[1] = "0x" + hex.EncodeToString(fixtureWord(56))
			e, ok := DecodeAcross(z, wrong)
			if ok && e.PayloadSHA256 == left[0].PayloadSHA256 {
				t.Fatal("same deposit ID on another origin chain accepted")
			}
		})
	}
}
func TestLayerZeroGUIDAndDeliveryRequireExactHeaderAndPayload(t *testing.T) {
	data := make([]byte, 153)
	data[0] = 1
	binary.BigEndian.PutUint64(data[1:9], 7)
	binary.BigEndian.PutUint32(data[9:13], 30101)
	copy(data[13:45], fixtureAddress("0x"+strings.Repeat("1", 40)))
	binary.BigEndian.PutUint32(data[45:49], 30110)
	copy(data[49:81], fixtureAddress("0x"+strings.Repeat("2", 40)))
	copy(data[81:113], Keccak(data[1:81]))
	copy(data[113:145], fixtureAddress("0x"+strings.Repeat("3", 40)))
	binary.BigEndian.PutUint64(data[145:153], 100)
	p, ok := DecodeLayerZeroPacket(data)
	if !ok || p.Nonce != "7" {
		t.Fatal("valid packet rejected")
	}
	for _, i := range []int{0, 1, 9, 13, 45, 49, 81} {
		other := append([]byte(nil), data...)
		other[i] ^= 1
		if _, ok := DecodeLayerZeroPacket(other); ok {
			t.Fatalf("changed header %d accepted with old GUID", i)
		}
	}
	call := []byte{}
	for _, w := range [][]byte{fixtureWord(30101), fixtureAddress(p.Sender), fixtureWord(7), fixtureAddress(p.Receiver), data[81:113], fixtureWord(224), fixtureWord(320), fixtureWord(40), data[113:145], append(data[145:153], make([]byte, 24)...), fixtureWord(0)} {
		call = append(call, w...)
	}
	input := EventTopic("lzReceive((uint32,bytes32,uint64),address,bytes32,bytes,bytes)")[:10] + hex.EncodeToString(call)
	if !LayerZeroReceiveCall(input, p) {
		t.Fatal("exact delivery call rejected")
	}
	other := p
	other.Message = append([]byte(nil), p.Message...)
	other.Message[39] ^= 1
	if LayerZeroReceiveCall(input, other) {
		t.Fatal("different payload accepted")
	}
}
func TestBoundedABIPayloadRejectsInvalidOffsetsAndPadding(t *testing.T) {
	for _, n := range []int64{1, 31, 33, 999999999} {
		d := append(fixtureWord(n), fixtureWord(0)...)
		if _, ok := ABIBytes(d, 0, 32); ok {
			t.Fatal("invalid offset", n)
		}
	}
	d := append(fixtureWord(32), fixtureWord(1)...)
	d = append(d, make([]byte, 32)...)
	d[64] = 1
	d[95] = 1
	if _, ok := ABIBytes(d, 0, 32); ok {
		t.Fatal("bad ABI padding accepted")
	}
}
