package api

import (
	"encoding/hex"
	"fmt"
	"github.com/blockchain-platform/blockchain-platform/internal/crosschain"
	"strings"
)

func (b *bridgeReadSession) matchingCCTP(a, z *bridgeRecord) ([2]crosschain.DecodedBridgeEvent, int, *crosschain.CCTPAssociationProof, []string) {
	var pair [2]crosschain.DecodedBridgeEvent
	var proof *crosschain.CCTPAssociationProof
	n := 0
	gaps := []string{}
	possible := false
	for _, h := range a.Hints {
		if h.Bridge == "cctp" && h.Kind == "sent" {
			possible = true
		}
	}
	if !possible {
		return pair, 0, nil, gaps
	}
	if b.s.crossChain == nil {
		return pair, 0, nil, []string{"cctp_attestation_reader_unavailable"}
	}
	if !a.CCTPRead {
		a.CCTPRead = true
		b.protocolCalls++
		var err error
		a.CCTPAttestations, err = b.s.crossChain.CCTPAttestations(b.ctx, a.Chain, a.Hash)
		if err != nil {
			a.CCTPError = err.Error()
		}
	}
	if a.CCTPError != "" {
		return pair, 0, nil, []string{"cctp_authoritative_association_unavailable:" + a.CCTPError}
	}
	for _, att := range a.CCTPAttestations {
		for _, src := range a.Logs {
			left, ok := crosschain.CCTPSourceMatches(a.Chain, src, att)
			if !ok || left.DestinationChain != z.Chain {
				continue
			}
			for _, dst := range z.Logs {
				right, ok := crosschain.CCTPReceivedMatches(z.Chain, dst, att)
				if !ok {
					continue
				}
				// Four historical contracts plus attested execution require a larger,
				// still bounded proof budget. No fuzzy search uses this allowance.
				b.maxCalls = 48
				if !z.NativeRead {
					b.evmNative(z)
				}
				calls := 0
				for _, call := range z.SuccessfulCalls {
					if strings.EqualFold(call.To, crosschain.CCTPTransmitterV2) && crosschain.CCTPReceiveCall(call.Input, att) {
						calls++
					}
				}
				if calls != 1 {
					gaps = append(gaps, "cctp_successful_attested_receive_call_unavailable_or_ambiguous")
					continue
				}
				m, _ := crosschain.DecodeCCTPV2Message(att.Message)
				minter, err := b.getter(a, crosschain.CCTPMessengerV2, "localMinter()", nil)
				if err != nil || len(minter) != 32 || strings.Trim(hex.EncodeToString(minter[:12]), "0") != "" || strings.Trim(hex.EncodeToString(minter), "0") == "" {
					gaps = append(gaps, "cctp_historical_minter_unavailable")
					continue
				}
				left.Counterparty = "0x" + hex.EncodeToString(minter[12:])
				valid := true
				for _, r := range []*bridgeRecord{a, z} {
					transmitter, err := b.getter(r, crosschain.CCTPMessengerV2, "localMessageTransmitter()", nil)
					if err != nil || len(transmitter) != 32 || hex.EncodeToString(transmitter) != hex.EncodeToString(wordAddress(crosschain.CCTPTransmitterV2)) {
						valid = false
					}
				}
				if !valid {
					gaps = append(gaps, "cctp_historical_messenger_transmitter_conflict")
					continue
				}
				burns := []crosschain.Movement{}
				for _, mv := range a.Movements {
					if mv.Status == "success" && mv.TokenContract == left.Asset && mv.From == left.Counterparty && mv.To == crosschain.ZeroEVMAddress && mv.AmountRaw == left.AmountRaw {
						burns = append(burns, mv)
					}
				}
				mints := 0
				for _, log := range z.Logs {
					if crosschain.CCTPMintEvent(z.Chain, log, att) {
						mints++
					}
				}
				if len(burns) != 1 || mints != 1 {
					gaps = append(gaps, "cctp_actual_burn_or_mint_event_unavailable_or_ambiguous")
					continue
				}
				var feeMovement *crosschain.Movement
				if m.FeeRaw != "0" {
					feeRecipient, err := b.getter(z, crosschain.CCTPMessengerV2, "feeRecipient()", nil)
					if err != nil || len(feeRecipient) != 32 || strings.Trim(hex.EncodeToString(feeRecipient[:12]), "0") != "" {
						gaps = append(gaps, "cctp_historical_fee_recipient_unavailable")
						continue
					}
					recipient := "0x" + hex.EncodeToString(feeRecipient[12:])
					fees := []crosschain.Movement{}
					for _, mv := range z.Movements {
						if mv.Status == "success" && mv.TokenContract == right.Asset && mv.From == crosschain.ZeroEVMAddress && mv.To == recipient && mv.AmountRaw == m.FeeRaw {
							fees = append(fees, mv)
						}
					}
					if len(fees) != 1 {
						gaps = append(gaps, "cctp_executed_fee_unavailable_or_ambiguous")
						continue
					}
					feeMovement = &fees[0]
				}
				raw, err := hex.DecodeString(strings.TrimPrefix(src.Data, "0x"))
				if err != nil {
					continue
				}
				sourceMessage, ok := crosschain.ABIBytes(raw, 0, 32)
				if !ok {
					continue
				}
				sig, _ := hex.DecodeString(strings.TrimPrefix(att.Attestation, "0x"))
				proof = &crosschain.CCTPAssociationProof{CCTPAttestation: att, SourceMessageSHA256: crosschain.ProofSHA(sourceMessage), ExecutedCallMessageSHA256: crosschain.ProofSHA(m.Raw), ExecutedAttestationSHA256: crosschain.ProofSHA(sig), SourceMinter: left.Counterparty, SourceBurn: burns[0], FeeRaw: m.FeeRaw, FeeMovement: feeMovement}
				pair = [2]crosschain.DecodedBridgeEvent{left, right}
				n++
			}
		}
	}
	if n == 0 && len(gaps) == 0 {
		gaps = append(gaps, "cctp_exact_source_attestation_destination_association_unavailable")
	}
	if n > 1 {
		gaps = append(gaps, fmt.Sprintf("cctp_multiple_associations:%d", n))
	}
	return pair, n, proof, gaps
}
