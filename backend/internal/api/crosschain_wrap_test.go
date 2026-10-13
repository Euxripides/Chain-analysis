package api

import (
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
	"github.com/blockchain-platform/blockchain-platform/internal/crosschain"
	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
)

const bridgeToken = "0x3333333333333333333333333333333333333333"
const bridgeHelper = "0x4444444444444444444444444444444444444444"

func wrapFixture(t *testing.T, side int) (*bridgeReadSession, crosschain.Leg, *bridgeRecord) {
	t.Helper()
	b, p := bridgeFixture(t)
	p.Handle("eth_call", func(req rpc.Request) (json.RawMessage, *rpc.Error) {
		return json.RawMessage(`"0x` + strings.Repeat("0", 24) + bridgeToken[2:] + `"`), nil
	})
	owner := bridgePool
	kind, signature := "sent", "Deposit(address,uint256)"
	from, to := bridgeWallet, owner
	if side == 1 {
		owner = bridgeHelper
		kind, signature = "received", "Withdrawal(address,uint256)"
		from, to = owner, bridgeWallet
	}
	token, _ := chain.ParseAddress(bridgeToken)
	data := strings.Repeat("0", 62) + "64"
	record := &bridgeRecord{Chain: "bsc", Hash: bridgeHash, Status: "success", BlockNumber: 100, NativeRead: true, Logs: []chainread.Log{{Address: token, Topics: []string{crosschain.EventTopic(signature), "0x" + strings.Repeat("0", 24) + owner[2:]}, Data: "0x" + data}}, Hints: []crosschain.ProtocolHint{{Bridge: "across", Kind: kind, Contract: bridgePool, Identifier: "12", Recipient: bridgeWallet}}}
	m, _ := exactMovement("bsc", bridgeHash, "internal_transactions", 1, time.Unix(1779040000, 0), from, to, "native", "100", 18)
	record.Movements = []crosschain.Movement{m}
	if side == 1 {
		linked, _ := exactMovement("bsc", bridgeHash, "token_transfers", 2, m.Timestamp, bridgePool, bridgeHelper, bridgeToken, "100", 18)
		withdraw, _ := exactMovement("bsc", bridgeHash, "internal_transactions", 3, m.Timestamp, bridgeToken, bridgeHelper, "native", "100", 18)
		record.Movements = append(record.Movements, linked, withdraw)
	}
	return b, crosschain.Leg{Chain: "bsc", TxHash: bridgeHash, Address: bridgeWallet, TokenContract: bridgeToken, AmountRaw: "100"}, record
}

func TestWrappedNativeSplitPreservesGrossAndAllDisbursements(t *testing.T) {
	b, leg, r := wrapFixture(t, 1)
	r.Movements[0].AmountRaw = "90"
	r.Movements[0].Amount = "0.00000000000000009"
	other, _ := exactMovement("bsc", bridgeHash, "internal_transactions", 4, r.Movements[0].Timestamp, bridgeHelper, bridgePool, "native", "10", 18)
	r.Movements = append(r.Movements, other)
	got := b.reconcileWrappedNative(leg, "12", 1, r)
	if got.AmountRaw != "90" || got.ReportedAmountRaw != "100" || got.Reconciliation == nil || len(got.Reconciliation.OtherDisbursements) != 1 || got.Reconciliation.OtherDisbursements[0].AmountRaw != "10" {
		t.Fatal("gross amount or other disbursement hidden", got)
	}
	r.Movements[3].AmountRaw = "11"
	if b.reconcileWrappedNative(leg, "12", 1, r).TokenContract == "native" {
		t.Fatal("split total does not reconcile to the withdrawal")
	}
}

func TestWrappedNativeRequiresGetterExactNativeAndBridgeIdentifier(t *testing.T) {
	for _, side := range []int{0, 1} {
		b, leg, r := wrapFixture(t, side)
		got := b.reconcileWrappedNative(leg, "12", side, r)
		if got.TokenContract != "native" || got.ReportedTokenContract != bridgeToken || got.BridgeContract != bridgePool {
			t.Fatalf("side %d exact wrapped-native path was not reconciled: %+v", side, got)
		}
		if b.reconcileWrappedNative(leg, "13", side, r).TokenContract == "native" {
			t.Fatal("unrelated deposit ID used")
		}
		r.Movements[0].AmountRaw = "101"
		if b.reconcileWrappedNative(leg, "12", side, r).TokenContract == "native" {
			t.Fatal("near amount became exact native proof")
		}
	}
	b, leg, r := wrapFixture(t, 1)
	r.Movements = r.Movements[:1]
	if b.reconcileWrappedNative(leg, "12", 1, r).TokenContract == "native" {
		t.Fatal("withdrawal helper was not linked to bridge pool")
	}
	b, leg, r = wrapFixture(t, 0)
	r.Movements = append(r.Movements, r.Movements[0])
	if b.reconcileWrappedNative(leg, "12", 0, r).TokenContract == "native" {
		t.Fatal("ambiguous native inputs accepted")
	}
}

func TestWormholeSwapPreservesSignerAndRejectsAmbiguousFunding(t *testing.T) {
	leg := crosschain.Leg{Chain: "bsc", TxHash: bridgeHash, Address: bridgeWallet, TokenContract: bridgeToken, NormalizedAmountRaw: "1", BridgeContract: bridgePool}
	m, _ := exactMovement("bsc", bridgeHash, "token_transfers", 4, time.Unix(1779040000, 0), bridgeHelper, bridgePool, bridgeToken, "10000000000", 18)
	r := &bridgeRecord{Status: "success", Hints: []crosschain.ProtocolHint{{Bridge: "wormhole", Kind: "sent", Identifier: "4/emitter/123"}}, Movements: []crosschain.Movement{m}}
	got := deriveWormholeFundingParty(leg, "4/emitter/123", 0, r)
	if got.Address != bridgeHelper || got.ReportedAddress != bridgeWallet || got.AddressScope != "wormhole_emitter_funding" {
		t.Fatal("signer and actual bridge input not distinguished", got)
	}
	if deriveWormholeFundingParty(leg, "4/emitter/124", 0, r).Address != bridgeWallet {
		t.Fatal("wrong message ID established funding")
	}
	r.Movements = append(r.Movements, m)
	r.Movements[1].Index++
	if deriveWormholeFundingParty(leg, "4/emitter/123", 0, r).Address != bridgeWallet {
		t.Fatal("ambiguous router inputs accepted")
	}
	r.Movements = r.Movements[:1]
	r.Movements[0].TokenContract = bridgeHelper
	if deriveWormholeFundingParty(leg, "4/emitter/123", 0, r).Address != bridgeWallet {
		t.Fatal("different asset accepted")
	}
}

func TestAcrossLegacyFillTopicAndMalformedFillHint(t *testing.T) {
	signature := "FilledV3Relay(address,address,uint256,uint256,uint256,uint256,uint32,uint32,uint32,address,address,address,address,bytes,(address,bytes,uint256,uint8))"
	if crosschain.EventTopic(signature) != "0x571749edf1d5c9599318cdbc4e28a6475d65e87fd3b2ddbe1e9a8d5e7a0f0ff7" {
		t.Fatal("Across fill tuple does not match the official event")
	}
	pool, _ := chain.ParseAddress(bridgePool)
	data := make([]byte, 384)
	wallet, _ := hex.DecodeString(bridgeWallet[2:])
	copy(data[9*32+12:10*32], wallet)
	log := chainread.Log{Address: pool, Topics: []string{crosschain.EventTopic(signature), "0x" + strings.Repeat("0", 63) + "1", "0x" + strings.Repeat("0", 62) + "0c", "0x" + strings.Repeat("0", 64)}, Data: "0x" + hex.EncodeToString(data)}
	hints := crosschain.DetectProtocolHints("bsc", []chainread.Log{log})
	if len(hints) != 1 || hints[0].Identifier != "12" || hints[0].Recipient != bridgeWallet {
		t.Fatal("official fill fields not decoded", hints)
	}
	log.Data = "0x"
	if len(crosschain.DetectProtocolHints("bsc", []chainread.Log{log})) != 0 {
		t.Fatal("malformed fill trusted")
	}
}

func TestRefundLegIsVerifiedSeparatelyAndNeverCompleted(t *testing.T) {
	b, _ := bridgeFixture(t)
	m, _ := exactMovement("bsc", bridgeHash, "token_transfers", 3, time.Unix(1779040000, 0), bridgePool, bridgeWallet, bridgeToken, "100", 6)
	b.records["bsc|"+bridgeHash] = &bridgeRecord{Status: "success", Movements: []crosschain.Movement{m}, NativeRead: true}
	transfer := crosschain.Transfer{Status: "refunded", Refund: []crosschain.Leg{{Chain: "bsc", TxHash: bridgeHash, Address: bridgeWallet, TokenContract: bridgeToken, AmountRaw: "100"}}}
	b.verify(&transfer)
	if len(transfer.Verification) != 1 || transfer.Verification[0].LegRole != "refund" || !transfer.Verification[0].AmountsMatched || transfer.OnChainVerified || transfer.PairingVerified {
		t.Fatal("refund misclassified as completed cross-chain funds", transfer)
	}
}

func TestCCTPCompletionRequiresOfficialReceiptNonce(t *testing.T) {
	b, _ := bridgeFixture(t)
	target := "0x" + strings.Repeat("c", 64)
	b.records["bsc|"+target] = &bridgeRecord{Status: "success", Hints: []crosschain.ProtocolHint{{Bridge: "cctp", Kind: "received", Contract: crosschain.CCTPTransmitterV2, SourceDomain: "0", Identifier: "0x01"}}}
	fresh := func() []crosschain.Transfer {
		return []crosschain.Transfer{{Bridge: "cctp", OrderID: "0/1", Status: "pending", Source: []crosschain.Leg{{Chain: "ethereum", TxHash: bridgeHash}}, Destination: []crosschain.Leg{{Chain: "bsc"}}}, {Bridge: "lifi", Source: []crosschain.Leg{{Chain: "ethereum", TxHash: bridgeHash}}, Destination: []crosschain.Leg{{Chain: "bsc", TxHash: target}}}}
	}
	ts := fresh()
	b.completeCCTPFromKnownPeers(ts)
	if ts[0].Destination[0].TxHash != target || ts[0].Status != "completed" {
		t.Fatal("official received nonce did not complete CCTP")
	}
	b.records["bsc|"+target].Hints[0].Identifier = "0x02"
	ts = fresh()
	b.completeCCTPFromKnownPeers(ts)
	if ts[0].Destination[0].TxHash != "" {
		t.Fatal("same transaction without exact nonce was accepted")
	}
	b.records["bsc|"+target].Hints[0].Identifier = "0x01"
	b.records["bsc|"+target].Hints[0].Contract = bridgePool
	ts = fresh()
	b.completeCCTPFromKnownPeers(ts)
	if ts[0].Destination[0].TxHash != "" {
		t.Fatal("spoofed event contract completed CCTP")
	}
}
