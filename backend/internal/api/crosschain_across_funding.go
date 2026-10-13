package api

import (
	"encoding/hex"
	"github.com/blockchain-platform/blockchain-platform/internal/crosschain"
	"strings"
)

func (b *bridgeReadSession) acrossCallerFunding(e crosschain.DecodedBridgeEvent, r *bridgeRecord, l *crosschain.FundsLegProof) {
	if !r.NativeRead {
		b.evmNative(r)
	}
	calls := []bridgeProtocolCall{}
	for _, call := range r.SuccessfulCalls {
		if crosschain.Same(r.Chain, call.To, e.Counterparty) && crosschain.AcrossDepositCallMatches(call.Input, e, r.Timestamp.Unix(), call.From) {
			calls = append(calls, call)
		}
	}
	if len(calls) != 1 || calls[0].From == "" {
		return
	}
	call := calls[0]
	matches := []crosschain.Movement{}
	for _, m := range r.Movements {
		if m.Status == "success" && m.Decimals >= 0 && m.AmountRaw == e.AmountRaw && crosschain.Same(r.Chain, m.From, call.From) && crosschain.Same(r.Chain, m.To, e.Counterparty) && m.TokenContract == e.Asset {
			matches = append(matches, m)
		}
	}
	if len(matches) > 1 {
		return
	}
	if len(matches) == 0 && call.Value == e.AmountRaw {
		token, err := b.getter(r, e.Counterparty, "wrappedNativeToken()", nil)
		if err != nil || len(token) != 32 || hex.EncodeToString(token) != hex.EncodeToString(wordAddress(e.Asset)) {
			return
		}
		conversions := []crosschain.NativeConversionProof{}
		for _, log := range r.Logs {
			owner, amount, ok := crosschain.DecodeNativeConversion(log.Topics, log.Data, true)
			if !ok || amount != e.AmountRaw || !crosschain.Same(r.Chain, owner, e.Counterparty) || !crosschain.Same(r.Chain, log.Address.String(), e.Asset) {
				continue
			}
			conversions = append(conversions, crosschain.NativeConversionProof{Token: e.Asset, Owner: owner, Pool: e.Counterparty, EventIndex: uint32(log.LogIndex), RawTopics: log.Topics, RawData: log.Data, HistoricalGetter: true})
		}
		if len(conversions) != 1 {
			return
		}
		conversion := conversions[0]
		for _, m := range r.Movements {
			if m.Status != "success" || m.TokenContract != "native" || m.AmountRaw != e.AmountRaw {
				continue
			}
			if crosschain.Same(r.Chain, m.From, call.From) && crosschain.Same(r.Chain, m.To, e.Counterparty) {
				matches = append(matches, m)
			}
			if crosschain.Same(r.Chain, m.From, e.Counterparty) && crosschain.Same(r.Chain, m.To, e.Asset) {
				conversion.NativeFunding = append(conversion.NativeFunding, m)
			}
		}
		if len(matches) != 1 || len(conversion.NativeFunding) != 1 {
			return
		}
		l.NativeConversion = &conversion
	}
	if len(matches) != 1 {
		return
	}
	m := matches[0]
	if m.TokenContract != "native" && m.Index >= uint32(e.Log.LogIndex) {
		return
	}
	l.ProtocolParty = e.Party
	l.Party = m.From
	l.Asset = m.TokenContract
	l.AmountRaw = m.AmountRaw
	l.Amount = m.Amount
	l.Decimals = m.Decimals
	l.Movements = matches
	l.OtherDisbursements = nil
	l.SourceCall = &crosschain.FundingSourceCall{From: call.From, To: call.To, Input: call.Input, Value: call.Value, BlockTimestamp: r.Timestamp.Unix()}
	l.Binding = strings.Join([]string{e.Protocol, e.Identifier, "executed_deposit_caller_funding"}, ":")
	l.MessageBound = true
	l.UniquePath = true
}
