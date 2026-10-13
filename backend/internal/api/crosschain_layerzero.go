package api

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/crosschain"
)

func (b *bridgeReadSession) lzGetter(r *bridgeRecord, proof *crosschain.LayerZeroApplicationProof, app, sig, argument string) ([]byte, error) {
	var arg []byte
	key := sig
	if argument != "" {
		arg = wordInt(argument)
		key += ":" + argument
	}
	raw, err := b.getter(r, app, sig, arg)
	if err == nil {
		proof.Getters[key] = "0x" + hex.EncodeToString(raw)
	}
	return raw, err
}

func lzAddress(raw []byte) string {
	if len(raw) != 32 || new(big.Int).SetBytes(raw[:12]).Sign() != 0 {
		return ""
	}
	return "0x" + hex.EncodeToString(raw[12:])
}

func (b *bridgeReadSession) lzApplication(r *bridgeRecord, p crosschain.LayerZeroPacket, side int, kind string) (crosschain.DecodedBridgeEvent, crosschain.LayerZeroApplicationProof, error) {
	proof := crosschain.LayerZeroApplicationProof{Getters: map[string]string{}}
	var e crosschain.DecodedBridgeEvent
	app, peer, eid := p.Sender, p.Receiver, p.DestinationEID
	if side == 1 {
		app, peer, eid = p.Receiver, p.Sender, p.SourceEID
	}
	endpoint, err := b.lzGetter(r, &proof, app, "endpoint()", "")
	if err != nil || lzAddress(endpoint) != crosschain.DeploymentAddress("layerzero", r.Chain, "endpoint_v2") {
		return e, proof, fmt.Errorf("historical_application_endpoint_unverified")
	}
	remote, err := b.lzGetter(r, &proof, app, "peers(uint32)", eid)
	if err != nil || lzAddress(remote) != peer {
		return e, proof, fmt.Errorf("historical_application_peer_unverified")
	}
	if kind == "stargate_taxi" {
		assetID, _, _, ok := crosschain.StargateTaxi(p)
		if !ok {
			return e, proof, fmt.Errorf("stargate_non_taxi_or_invalid_payload")
		}
		messenger := app
		mapping, err := b.lzGetter(r, &proof, messenger, "stargateImpls(uint16)", assetID)
		app = lzAddress(mapping)
		d, registered := crosschain.FindDeployment("layerzero", r.Chain, app)
		if err != nil || !registered || d.Role != "stargate_pool" {
			return e, proof, fmt.Errorf("historical_stargate_asset_pool_unverified")
		}
		config, err := b.lzGetter(r, &proof, app, "getAddressConfig()", "")
		if err != nil || len(config) != 192 || lzAddress(config[96:128]) != messenger {
			return e, proof, fmt.Errorf("historical_stargate_pool_messaging_conflict")
		}
		mc, err := b.historicalDeployment("layerzero", r, messenger, messenger)
		if err != nil {
			return e, proof, err
		}
		proof.MessagingContract = &mc
	}
	var n int
	if kind == "stargate_taxi" {
		e, n = crosschain.StargateOFT(r.Chain, r.Logs, p, app, side == 0)
	} else {
		e, n = crosschain.LayerZeroOFT(r.Chain, r.Logs, p, side == 0)
	}
	if n != 1 {
		return e, proof, fmt.Errorf("application_funds_decoder_unavailable_or_ambiguous")
	}
	proof.Event = protocolEvent(r, e)
	token, err := b.lzGetter(r, &proof, app, "token()", "")
	e.Asset = lzAddress(token)
	if err != nil || e.Asset == "" {
		return e, proof, fmt.Errorf("historical_application_asset_unavailable")
	}
	if e.Asset == "0x0000000000000000000000000000000000000000" {
		if kind != "stargate_taxi" {
			return e, proof, fmt.Errorf("historical_application_asset_unavailable")
		}
		e.Asset = "native"
	}
	sd, err := b.lzGetter(r, &proof, app, "sharedDecimals()", "")
	if err != nil || len(sd) != 32 || !new(big.Int).SetBytes(sd).IsUint64() || new(big.Int).SetBytes(sd).Uint64() > 38 {
		return e, proof, fmt.Errorf("application_shared_precision_unavailable")
	}
	// Require actual call evidence for USDT0's separate token contract. Other
	// adapters retain lock/unlock funding; zero-address events are not universal.
	d, _ := crosschain.FindDeployment("layerzero", r.Chain, app)
	if d.Role == "usdt0_application" && r.Chain != "ethereum" {
		if !r.NativeRead {
			b.evmNative(r)
		}
		var calls []crosschain.FundingSourceCall
		for _, call := range r.SuccessfulCalls {
			candidate := crosschain.FundingSourceCall{From: call.From, To: call.To, Input: call.Input, Value: call.Value, BlockTimestamp: r.Timestamp.Unix()}
			if crosschain.USDT0TokenCall(e, candidate) {
				calls = append(calls, candidate)
			}
		}
		if len(calls) != 1 {
			return e, proof, fmt.Errorf("unique_usdt0_token_burn_mint_call_unavailable")
		}
		proof.TokenCall = &calls[0]
		e.Counterparty = "0x0000000000000000000000000000000000000000"
	}
	if kind == "stargate_taxi" && e.Asset == "native" && side == 0 {
		if !r.NativeRead {
			b.evmNative(r)
		}
		count := 0
		for _, call := range r.SuccessfulCalls {
			candidate := crosschain.FundingSourceCall{From: call.From, To: call.To, Input: call.Input, Value: call.Value, BlockTimestamp: r.Timestamp.Unix()}
			if fee, ok := crosschain.StargateNativeSend(p, e, candidate); ok {
				proof.NativeSendCall, proof.NativeFeeRaw = &candidate, fee
				count++
			}
		}
		if count != 1 {
			return e, proof, fmt.Errorf("stargate_native_principal_fee_call_unavailable_or_ambiguous")
		}
	}
	return e, proof, nil
}

func layerZeroNativeFunding(e crosschain.DecodedBridgeEvent, r *bridgeRecord, app crosschain.LayerZeroApplicationProof, l *crosschain.FundsLegProof) {
	if app.NativeSendCall == nil || app.MessagingContract == nil || e.Role != "source" || e.Asset != "native" {
		return
	}
	var incoming, fees []crosschain.Movement
	for _, m := range r.Movements {
		if m.TokenContract != "native" || m.Status != "success" || m.Decimals != 18 {
			continue
		}
		if crosschain.Same(r.Chain, m.From, e.Party) && crosschain.Same(r.Chain, m.To, e.Application) && m.AmountRaw == app.NativeSendCall.Value {
			incoming = append(incoming, m)
		}
		if crosschain.Same(r.Chain, m.From, e.Application) && crosschain.Same(r.Chain, m.To, app.MessagingContract.Address) && m.AmountRaw == app.NativeFeeRaw {
			fees = append(fees, m)
		}
	}
	if len(incoming) != 1 || len(fees) != 1 {
		l.ReadGaps = append(l.ReadGaps, "stargate_native_principal_fee_path_unavailable_or_ambiguous")
		return
	}
	l.Movements, l.OtherDisbursements = incoming, fees
	l.AmountRaw, l.Amount, l.Decimals = incoming[0].AmountRaw, incoming[0].Amount, 18
	l.MessageBound, l.UniquePath = true, true
	var gaps []string
	for _, gap := range l.ReadGaps {
		if gap != "unique_protocol_bound_funding_path_unavailable" {
			gaps = append(gaps, gap)
		}
	}
	l.ReadGaps = gaps
	l.Binding = "layerzero:" + e.Identifier + ":native_principal_plus_executed_messaging_fee"
}

func (b *bridgeReadSession) matchingLayerZero(a, z *bridgeRecord) ([2]crosschain.DecodedBridgeEvent, int, *crosschain.LayerZeroAssociationProof, []string) {
	var result [2]crosschain.DecodedBridgeEvent
	var association *crosschain.LayerZeroAssociationProof
	count := 0
	gaps := []string{}
	for _, item := range crosschain.LayerZeroSourcePackets(a.Chain, a.Logs) {
		p := item.Packet
		if crosschain.LayerZeroChain(p.DestinationEID) != z.Chain {
			continue
		}
		delivered, n := crosschain.LayerZeroDelivered(z.Chain, z.Logs, p)
		if n != 1 {
			if n > 1 {
				gaps = append(gaps, "duplicate_delivery_event")
			}
			continue
		}
		sd, sOK := crosschain.FindDeployment("layerzero", a.Chain, p.Sender)
		dd, dOK := crosschain.FindDeployment("layerzero", z.Chain, p.Receiver)
		kind := "oft"
		if !sOK || !dOK {
			gaps = append(gaps, "official_application_deployment_unavailable")
			continue
		}
		limit := 48
		if sd.Role == "stargate_messaging" && dd.Role == "stargate_messaging" {
			kind, limit = "stargate_taxi", 64
		} else if !crosschain.IsOFTDeployment(sd) || !crosschain.IsOFTDeployment(dd) {
			gaps = append(gaps, "application_funds_decoder_unavailable_or_ambiguous")
			continue
		}
		if b.maxCalls < limit {
			b.maxCalls = limit
		}
		if !z.NativeRead {
			b.evmNative(z)
		}
		proof := &crosschain.LayerZeroAssociationProof{Kind: kind}
		calls := 0
		for _, call := range z.SuccessfulCalls {
			if crosschain.Same(z.Chain, call.To, delivered.Address.String()) && crosschain.LayerZeroReceiveCall(call.Input, p) {
				proof.DeliveryCall = crosschain.FundingSourceCall{From: call.From, To: call.To, Input: call.Input, Value: call.Value, BlockTimestamp: z.Timestamp.Unix()}
				calls++
			}
		}
		if calls != 1 {
			gaps = append(gaps, "exact_delivery_payload_call_unavailable_or_ambiguous")
			continue
		}
		var pair [2]crosschain.DecodedBridgeEvent
		failed := false
		for side, r := range []*bridgeRecord{a, z} {
			e, ap, err := b.lzApplication(r, p, side, kind)
			if err != nil {
				gaps = append(gaps, err.Error())
				failed = true
				break
			}
			proof.Applications[side], pair[side] = ap, e
			if e.Asset == "native" && !r.NativeRead {
				b.evmNative(r)
			}
		}
		if failed {
			continue
		}
		shared := -1
		for side, r := range []*bridgeRecord{a, z} {
			v := proof.Applications[side].Getters["sharedDecimals()"]
			raw, _ := hex.DecodeString(strings.TrimPrefix(v, "0x"))
			precision := int(new(big.Int).SetBytes(raw).Uint64())
			if side == 0 {
				shared = precision
			} else if shared != precision {
				failed = true
				break
			}
			normalized := new(big.Int).SetBytes(p.Message[32:40])
			if kind == "stargate_taxi" {
				_, _, n, _ := crosschain.StargateTaxi(p)
				normalized, _ = new(big.Int).SetString(n, 10)
			}
			dec := 18
			if pair[side].Asset != "native" {
				dec = b.evmDecimals(b.ctx, r.Chain, pair[side].Asset, r.BlockNumber)
			}
			amount := pair[side].AmountRaw
			if side == 0 {
				amount = pair[side].NormalizedAmountRaw
			}
			if dec < shared || dec > 38 || new(big.Int).Mul(normalized, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(dec-shared)), nil)).String() != amount {
				failed = true
				break
			}
		}
		if failed {
			gaps = append(gaps, "application_payload_amount_or_precision_conflict")
			continue
		}
		pair[0].Log, pair[1].Log = item.Log, delivered
		result, association = pair, proof
		count++
	}
	return result, count, association, gaps
}
