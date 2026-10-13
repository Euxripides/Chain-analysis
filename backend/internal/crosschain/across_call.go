package crosschain

import (
	"bytes"
	"math/big"
)

type FundingSourceCall struct {
	From           string `json:"from"`
	To             string `json:"to"`
	Input          string `json:"input"`
	Value          string `json:"value"`
	BlockTimestamp int64  `json:"block_timestamp"`
}

// Match every deposit argument against the emitted source event, including the
// declared depositor. The executed caller is a separate funding role.
func AcrossDepositCallMatches(input string, e DecodedBridgeEvent, stamp int64, caller string) bool {
	raw, ok := proofHex(input)
	event, valid := proofHex(e.Log.Data)
	if !ok || !valid || e.Protocol != "across" || e.Role != "source" || len(raw) < 4+384 {
		return false
	}
	found := false
	shift := 0
	for _, sig := range []string{
		"depositV3(address,address,address,address,uint256,uint256,uint256,address,uint32,uint32,uint32,bytes)",
		"deposit(bytes32,bytes32,bytes32,bytes32,uint256,uint256,uint256,bytes32,uint32,uint32,uint32,bytes)",
	} {
		if bytes.Equal(raw[:4], Keccak([]byte(sig))[:4]) {
			found = true
		}
	}
	if bytes.Equal(raw[:4], Keccak([]byte("unsafeDeposit(bytes32,bytes32,bytes32,bytes32,uint256,uint256,uint256,bytes32,uint256,uint32,uint32,uint32,bytes)"))[:4]) {
		found = true
		shift = 1
	}
	if !found || len(e.Log.Topics) != 4 {
		return false
	}
	d := raw[4:]
	if len(d) < 384+32*shift {
		return false
	}
	depositor, ok := topicBytes(e.Log.Topics[3])
	chain, valid := topicBytes(e.Log.Topics[1])
	if !ok || !valid {
		return false
	}
	if !bytes.Equal(abiWord(d, 0), depositor) || !bytes.Equal(abiWord(d, 6), chain) {
		return false
	}
	if shift == 1 {
		who, valid := proofHex(caller)
		if !valid || len(who) != 20 {
			return false
		}
		id := new(big.Int).SetBytes(Keccak(append(append(append([]byte(nil), who...), depositor...), abiWord(d, 8)...)))
		if id.String() != e.DepositID {
			return false
		}
	}
	for _, v := range [][2]int{{1, 7}, {2, 0}, {3, 1}, {4, 2}, {5, 3}, {7, 8}, {8 + shift, 4}, {9 + shift, 5}} {
		if !bytes.Equal(abiWord(d, v[0]), abiWord(event, v[1])) {
			return false
		}
	}
	ex := new(big.Int).SetBytes(abiWord(d, 10+shift))
	if !ex.IsUint64() || ex.BitLen() > 32 {
		return false
	}
	deadline := ex.Uint64()
	if deadline > 0 && deadline <= 31536000 {
		if stamp <= 0 {
			return false
		}
		deadline += uint64(stamp)
	}
	if deadline > 4294967295 || new(big.Int).SetBytes(abiWord(event, 6)).Cmp(new(big.Int).SetUint64(deadline)) != 0 {
		return false
	}
	msg, ok := ABIBytes(d, 11+shift, 384+shift*32)
	sent, valid := ABIBytes(event, 9, 320)
	return ok && valid && bytes.Equal(msg, sent)
}
