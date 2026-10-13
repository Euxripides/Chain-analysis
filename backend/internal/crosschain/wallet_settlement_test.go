package crosschain

import "testing"

func TestWalletCreditNeedsSeparateHistoricalAndMovementProof(t *testing.T) {
	v := realProofFixture(t, "B04")
	l := v.Proof.Legs[1]
	p := &WalletSettlementProof{State: "direct_wallet_credit_verified", Chain: l.Chain, TxHash: l.TxHash, Address: l.Party, Asset: l.Asset, AmountRaw: l.AmountRaw, Decimals: l.Decimals, BlockNumber: l.Block.Number, BlockHash: l.Block.Hash, HistoricalCode: "0x", HistoricalCodeRead: true, ReceiptSHA256: l.ReceiptSHA256, Movement: l.Movements[0]}
	v.Proof.WalletSettlement = p
	ApplyFundsGate(v)
	if !v.FundsVerified || !v.FinalWalletVerified {
		t.Fatal("complete direct credit proof rejected")
	}
	mutations := map[string]func(){
		"contract_recipient": func() { p.HistoricalCode = "0x6000" },
		"missing_read":       func() { p.HistoricalCodeRead = false },
		"outgoing":           func() { p.OutgoingSameAsset = true },
		"wrong_amount":       func() { p.AmountRaw = "1" },
		"wrong_event":        func() { p.Movement.Index++ },
		"wrong_block":        func() { p.BlockNumber++ },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			old := *p
			mutate()
			ApplyFundsGate(v)
			if !v.FundsVerified || v.FinalWalletVerified || v.FinalWalletState == "direct_wallet_credit_verified" {
				t.Fatal("invalid wallet evidence promoted or bridge proof lost")
			}
			*p = old
		})
	}
}
