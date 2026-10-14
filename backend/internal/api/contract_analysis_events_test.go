package api

import (
	"encoding/hex"
	"encoding/json"
	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
	"math/big"
	"strings"
	"testing"
)

func TestReceiptNFTProjectionPreservesHugeIDsAndBatchAmounts(t *testing.T) {
	address := func(ch string) string { return "0x" + strings.Repeat("0", 24) + strings.Repeat(ch, 40) }
	word := func(n *big.Int) []byte { b := make([]byte, 32); n.FillBytes(b); return b }
	huge := new(big.Int).Lsh(big.NewInt(1), 200)
	encoded := append(word(big.NewInt(64)), word(big.NewInt(160))...)
	encoded = append(encoded, word(big.NewInt(2))...)
	encoded = append(encoded, word(huge)...)
	encoded = append(encoded, word(big.NewInt(7))...)
	encoded = append(encoded, word(big.NewInt(2))...)
	encoded = append(encoded, word(big.NewInt(3))...)
	encoded = append(encoded, word(huge)...)
	logs := []chainread.Log{
		{Address: chain.Address("0x" + strings.Repeat("a", 40)), Topics: []string{contractKeccak([]byte("Transfer(address,address,uint256)")), address("1"), address("2"), "0x" + hex.EncodeToString(word(huge))}, Data: "0x", LogIndex: 5},
		{Address: chain.Address("0x" + strings.Repeat("b", 40)), Topics: []string{contractKeccak([]byte("TransferBatch(address,address,address,uint256[],uint256[])")), address("3"), address("1"), address("2")}, Data: "0x" + hex.EncodeToString(encoded), LogIndex: 8},
	}
	result := ca.Result{Sections: map[string][]json.RawMessage{}}
	contractReceiptEvents(&result, "tx", "artifact", logs, nil, 20)
	if len(result.Sections["token_events"]) != 3 {
		t.Fatal("NFT projection count", len(result.Sections["token_events"]))
	}
	first := rowMap(result.Sections["token_events"][0])
	last := rowMap(result.Sections["token_events"][2])
	if first["token_standard"] != "ERC721" || first["token_id_raw"] != huge.String() || first["amount_raw"] != "1" || last["amount_raw"] != huge.String() {
		t.Fatal("NFT quantities lost", first, last)
	}
	// Forged lengths and overlapping offsets cannot produce rows or allocations.
	copy(encoded[64:96], word(huge))
	if ids, _ := contract1155Batch(encoded, 20); ids != nil {
		t.Fatal("huge length accepted")
	}
	logs[1].Data = "0x" + hex.EncodeToString(encoded)
	result = ca.Result{Sections: map[string][]json.RawMessage{}}
	contractReceiptEvents(&result, "tx", "artifact", logs[1:], nil, 20)
	if len(result.Sections["token_events"]) != 0 {
		t.Fatal("malformed batch accepted")
	}
}

func TestWrappedNativeEventNeedsPinnedHistoricalRuntime(t *testing.T) {
	log := chainread.Log{Address: chain.Address("0x" + strings.Repeat("a", 40)), Topics: []string{contractKeccak([]byte("Withdrawal(address,uint256)")), "0x" + strings.Repeat("0", 24) + strings.Repeat("1", 40)}, Data: "0x" + strings.Repeat("0", 63) + "1"}
	pre := ca.Row(map[string]ca.StateAccount{string(log.Address): {Code: "0x6000"}})
	result := ca.Result{Sections: map[string][]json.RawMessage{}}
	contractReceiptEvents(&result, "tx", "artifact", []chainread.Log{log}, pre, 20)
	if len(result.Sections["token_events"]) != 0 {
		t.Fatal("event name falsely identified WETH")
	}
}
