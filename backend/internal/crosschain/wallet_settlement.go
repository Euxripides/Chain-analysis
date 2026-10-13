package crosschain

// WalletSettlementProof covers direct receipt into a code-free EVM account at
// the confirmed transaction block. Contract routing requires a separate decoder.
type WalletSettlementProof struct {
	State              string   `json:"state"`
	Reason             string   `json:"reason,omitempty"`
	Chain              string   `json:"chain"`
	TxHash             string   `json:"tx_hash"`
	Address            string   `json:"address"`
	Asset              string   `json:"asset"`
	AmountRaw          string   `json:"amount_raw"`
	Decimals           int      `json:"decimals"`
	BlockNumber        uint64   `json:"block_number"`
	BlockHash          string   `json:"block_hash"`
	HistoricalCode     string   `json:"historical_code"`
	HistoricalCodeRead bool     `json:"historical_code_read"`
	ReceiptSHA256      string   `json:"receipt_sha256"`
	Movement           Movement `json:"movement"`
	OutgoingSameAsset  bool     `json:"outgoing_same_asset"`
}

func applyWalletGate(t *Transfer) {
	if !t.FundsVerified || t.Proof == nil || len(t.Proof.Legs) != 2 {
		return
	}
	p := t.Proof.WalletSettlement
	if p == nil {
		return
	}
	if p.State == "contract_settlement_pending" {
		t.FinalWalletState = p.State
	}
	l := t.Proof.Legs[1]
	if p.State != "direct_wallet_credit_verified" || !p.HistoricalCodeRead || p.HistoricalCode != "0x" || p.OutgoingSameAsset || p.Chain != l.Chain || !Same(l.Chain, p.TxHash, l.TxHash) || !Same(l.Chain, p.Address, l.Party) || p.Asset != l.Asset || p.AmountRaw != l.AmountRaw || p.Decimals != l.Decimals || p.BlockNumber != l.Block.Number || !Same(l.Chain, p.BlockHash, l.Block.Hash) || p.ReceiptSHA256 != l.ReceiptSHA256 {
		return
	}
	m := p.Movement
	if !completeMovement(l.Chain, l.TxHash, m) || m.To != l.Party || m.TokenContract != l.Asset || m.AmountRaw != l.AmountRaw || m.Decimals != l.Decimals {
		return
	}
	found := 0
	for _, v := range l.Movements {
		if v.Identity() == m.Identity() && v == m {
			found++
		}
	}
	if found == 1 {
		t.FinalWalletVerified = true
		t.FinalWalletState = "direct_wallet_credit_verified"
	}
}
