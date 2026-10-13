package crosschain

import (
	"math/big"
	"strings"
)

// SplitTokenFunding accepts a single funding input and accounts for every
// same-asset input/output of its router in the complete transaction receipt.
// The extra outputs are recorded as disbursements, without guessing their role.
func SplitTokenFunding(e DecodedBridgeEvent, rows []Movement) ([]Movement, []Movement) {
	if e.Role != "source" {
		return nil, nil
	}
	type route struct{ path, other []Movement }
	routes := []route{}
	for _, first := range rows {
		if first.Dataset != "token_transfers" || first.Status != "success" || first.Decimals < 0 || !positiveRaw(first.AmountRaw) || !Same(first.Chain, first.From, e.Party) || !Same(first.Chain, first.TokenContract, e.Asset) || Same(first.Chain, first.To, e.Counterparty) || Same(first.Chain, first.From, first.To) {
			continue
		}
		inputs, outputs := []Movement{}, []Movement{}
		var last *Movement
		for _, m := range rows {
			if m.Dataset != "token_transfers" || m.Status != "success" || !Same(first.Chain, m.TokenContract, e.Asset) {
				continue
			}
			if Same(first.Chain, m.To, first.To) {
				inputs = append(inputs, m)
			}
			if Same(first.Chain, m.From, first.To) {
				outputs = append(outputs, m)
				if Same(first.Chain, m.To, e.Counterparty) && m.AmountRaw == e.AmountRaw {
					if last != nil {
						last = nil
						break
					}
					copy := m
					last = &copy
				}
			}
		}
		if len(inputs) != 1 || inputs[0].Identity() != first.Identity() || last == nil || len(outputs) < 2 || len(outputs) > 4 {
			continue
		}
		other := []Movement{}
		for _, m := range outputs {
			if m.Identity() != last.Identity() {
				other = append(other, m)
			}
		}
		path := []Movement{first, *last}
		if validSplitPath(e, path, other) {
			routes = append(routes, route{path, other})
		}
	}
	if len(routes) != 1 {
		return nil, nil
	}
	return routes[0].path, routes[0].other
}

func validSplitPath(e DecodedBridgeEvent, path, other []Movement) bool {
	if e.Role != "source" || len(path) != 2 || len(other) == 0 || len(other) > 3 {
		return false
	}
	a, z := path[0], path[1]
	if !Same(a.Chain, a.From, e.Party) || !Same(a.Chain, a.To, z.From) || !Same(a.Chain, z.To, e.Counterparty) || z.AmountRaw != e.AmountRaw || a.Decimals != z.Decimals || z.Index <= a.Index {
		return false
	}
	sum := new(big.Int)
	seen := map[string]bool{}
	for _, m := range append([]Movement{z}, other...) {
		if m.Chain != a.Chain || !Same(a.Chain, m.TxHash, a.TxHash) || m.Dataset != "token_transfers" || m.Status != "success" || m.Decimals != a.Decimals || !Same(a.Chain, m.TokenContract, e.Asset) || !Same(a.Chain, m.From, a.To) || Same(a.Chain, m.From, m.To) || m.Index <= a.Index || seen[m.Identity()] || !positiveRaw(m.AmountRaw) {
			return false
		}
		seen[m.Identity()] = true
		n, _ := new(big.Int).SetString(m.AmountRaw, 10)
		sum.Add(sum, n)
	}
	return sum.String() == a.AmountRaw && Same(a.Chain, a.TokenContract, e.Asset)
}

// NativeConversionProof preserves the actual wrapping event and the getter
// result at the transaction block. A mint by an arbitrary token is insufficient.
type NativeConversionProof struct {
	Token            string     `json:"token"`
	Owner            string     `json:"owner"`
	Pool             string     `json:"pool"`
	EventIndex       uint32     `json:"event_index"`
	RawTopics        []string   `json:"raw_topics"`
	RawData          string     `json:"raw_data"`
	HistoricalGetter bool       `json:"historical_getter"`
	NativeFunding    []Movement `json:"native_funding"`
	TokenFunding     []Movement `json:"token_funding"`
}

// DecodeNativeConversion supports standard WETH Deposit/Withdrawal events and
// the zero-address Transfer emitted by Arbitrum's wrapped native token on mint.
func DecodeNativeConversion(topics []string, data string, source bool) (string, string, bool) {
	b, ok := proofHex(data)
	if !ok || len(b) != 32 || new(big.Int).SetBytes(b).Sign() <= 0 {
		return "", "", false
	}
	owner := ""
	if len(topics) == 2 {
		sig := "Withdrawal(address,uint256)"
		if source {
			sig = "Deposit(address,uint256)"
		}
		if !strings.EqualFold(topics[0], EventTopic(sig)) {
			return "", "", false
		}
		w, valid := topicBytes(topics[1])
		if !valid {
			return "", "", false
		}
		owner, ok = abiAddress(w)
	} else if len(topics) == 3 && strings.EqualFold(topics[0], EventTopic("Transfer(address,address,uint256)")) {
		zeroIndex, ownerIndex := 1, 2
		if !source {
			zeroIndex, ownerIndex = 2, 1
		}
		zero, valid := topicBytes(topics[zeroIndex])
		if !valid || new(big.Int).SetBytes(zero).Sign() != 0 {
			return "", "", false
		}
		w, valid := topicBytes(topics[ownerIndex])
		if !valid {
			return "", "", false
		}
		owner, ok = abiAddress(w)
	} else {
		return "", "", false
	}
	return owner, new(big.Int).SetBytes(b).String(), ok
}

func SourceNativeFunding(e DecodedBridgeEvent, rows []Movement) ([]Movement, []Movement) {
	paths := [][]Movement{}
	var walk func(string, []Movement)
	walk = func(at string, path []Movement) {
		if len(path) >= 4 || len(paths) > 1 {
			return
		}
		for _, m := range rows {
			if m.Dataset == "token_transfers" || m.TokenContract != "native" || m.Status != "success" || !positiveRaw(m.AmountRaw) || !Same(m.Chain, m.From, at) || Same(m.Chain, m.From, m.To) {
				continue
			}
			if len(path) > 0 {
				prev := path[len(path)-1]
				a, _ := new(big.Int).SetString(prev.AmountRaw, 10)
				b, _ := new(big.Int).SetString(m.AmountRaw, 10)
				if m.Index <= prev.Index || b.Cmp(a) > 0 {
					continue
				}
			}
			next := append(append([]Movement(nil), path...), m)
			if Same(m.Chain, m.To, e.Counterparty) {
				if m.AmountRaw == e.AmountRaw {
					paths = append(paths, next)
				}
				continue
			}
			walk(m.To, next)
		}
	}
	walk(e.Party, nil)
	if len(paths) != 1 {
		return nil, nil
	}
	path, other := paths[0], []Movement{}
	for i := 1; i < len(path); i++ {
		prev, next := path[i-1], path[i]
		inputCount := 0
		sum := new(big.Int)
		for _, m := range rows {
			if m.Dataset == "token_transfers" || m.TokenContract != "native" || m.Status != "success" {
				continue
			}
			if Same(m.Chain, m.To, next.From) {
				inputCount++
			}
			if Same(m.Chain, m.From, next.From) {
				if m.Index <= prev.Index || !positiveRaw(m.AmountRaw) || Same(m.Chain, m.From, m.To) {
					return nil, nil
				}
				n, _ := new(big.Int).SetString(m.AmountRaw, 10)
				sum.Add(sum, n)
				if m.Identity() != next.Identity() {
					other = append(other, m)
				}
			}
		}
		if inputCount != 1 || sum.String() != prev.AmountRaw {
			return nil, nil
		}
	}
	if len(other) > 3 {
		return nil, nil
	}
	return path, other
}

func validAcrossFunding(e DecodedBridgeEvent, l FundsLegProof) bool {
	if l.Asset != "native" {
		if !Same(l.Chain, l.Asset, e.Asset) || !Same(l.Chain, l.Party, e.Party) {
			return false
		}
		if e.Role == "source" && len(l.OtherDisbursements) > 0 {
			return len(l.Movements) == 2 && l.AmountRaw == l.Movements[0].AmountRaw && validSplitPath(e, l.Movements, l.OtherDisbursements)
		}
		if len(l.OtherDisbursements) > 0 || l.AmountRaw != e.AmountRaw || len(l.Movements) == 0 || len(l.Movements) > 3 {
			return false
		}
		start, end := e.Party, e.Counterparty
		if e.Role == "destination" {
			start, end = e.Counterparty, e.Party
		}
		for i, m := range l.Movements {
			if m.Dataset != "token_transfers" || !Same(l.Chain, m.From, start) || !Same(l.Chain, m.TokenContract, e.Asset) || m.AmountRaw != e.AmountRaw || i > 0 && m.Index <= l.Movements[i-1].Index {
				return false
			}
			start = m.To
		}
		return Same(l.Chain, start, end)
	}
	c := l.NativeConversion
	if c == nil || !c.HistoricalGetter || !Same(l.Chain, c.Token, e.Asset) || !Same(l.Chain, c.Pool, e.Log.Address.String()) {
		return false
	}
	owner, raw, ok := DecodeNativeConversion(c.RawTopics, c.RawData, e.Role == "source")
	if !ok || !Same(l.Chain, owner, c.Owner) || raw != e.AmountRaw || !Same(l.Chain, l.Party, e.Party) {
		return false
	}
	validNative := func(m Movement) bool {
		return m.Chain == l.Chain && Same(l.Chain, m.TxHash, l.TxHash) && m.Status == "success" && m.Dataset != "token_transfers" && m.TokenContract == "native" && positiveRaw(m.AmountRaw) && !Same(l.Chain, m.From, m.To)
	}
	if e.Role == "source" {
		if !Same(l.Chain, c.Owner, e.Counterparty) || len(l.Movements) == 0 || len(l.Movements) > 4 || l.AmountRaw != l.Movements[0].AmountRaw || len(c.NativeFunding) != 1 {
			return false
		}
		at := e.Party
		for _, m := range l.Movements {
			if !validNative(m) || !Same(l.Chain, m.From, at) {
				return false
			}
			at = m.To
		}
		path, others := SourceNativeFunding(e, append(append([]Movement(nil), l.Movements...), l.OtherDisbursements...))
		if len(path) != len(l.Movements) || len(others) != len(l.OtherDisbursements) {
			return false
		}
		m := c.NativeFunding[0]
		return Same(l.Chain, at, c.Owner) && validNative(m) && Same(l.Chain, m.From, c.Owner) && Same(l.Chain, m.To, c.Token) && m.AmountRaw == e.AmountRaw
	}
	if !Same(l.Chain, e.Counterparty, c.Owner) {
		copy := e
		copy.Party = c.Owner
		tokenLeg := l
		tokenLeg.Asset, tokenLeg.Party, tokenLeg.AmountRaw = e.Asset, c.Owner, e.AmountRaw
		tokenLeg.Movements, tokenLeg.OtherDisbursements = c.TokenFunding, nil
		if !validAcrossFunding(copy, tokenLeg) {
			return false
		}
	}
	if len(l.Movements) != 2 || len(l.OtherDisbursements) > 3 {
		return false
	}
	w, z := l.Movements[0], l.Movements[1]
	if !validNative(w) || !validNative(z) || !Same(l.Chain, w.From, c.Token) || !Same(l.Chain, w.To, c.Owner) || w.AmountRaw != e.AmountRaw || !Same(l.Chain, z.From, c.Owner) || !Same(l.Chain, z.To, e.Party) || z.AmountRaw != l.AmountRaw {
		return false
	}
	sum := new(big.Int)
	for _, m := range append([]Movement{z}, l.OtherDisbursements...) {
		if !validNative(m) || !Same(l.Chain, m.From, c.Owner) {
			return false
		}
		n, _ := new(big.Int).SetString(m.AmountRaw, 10)
		sum.Add(sum, n)
	}
	return sum.String() == e.AmountRaw
}
