package api

import (
	"encoding/hex"
	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/crosschain"
)

func (b *bridgeReadSession) verifyDirectWallet(t *crosschain.Transfer) {
	if !t.FundsVerified || t.Proof == nil || len(t.Proof.Legs) != 2 || t.Proof.WalletSettlement != nil {
		return
	}
	l := t.Proof.Legs[1]
	p := &crosschain.WalletSettlementProof{State: "not_verified", Chain: l.Chain, TxHash: l.TxHash, Address: l.Party, Asset: l.Asset, AmountRaw: l.AmountRaw, Decimals: l.Decimals, BlockNumber: l.Block.Number, BlockHash: l.Block.Hash, ReceiptSHA256: l.ReceiptSHA256}
	t.Proof.WalletSettlement = p
	r, err := b.record(l.Chain, l.TxHash, false)
	if err != nil {
		p.Reason = "destination_record_unavailable"
		return
	}
	a, err := chain.ParseAddress(l.Party)
	if err != nil || crosschain.EVMNetworkIDs[l.Chain] == "" || b.s.deps.ChainRead == nil {
		p.Reason = "wallet_account_reader_unsupported"
		return
	}
	if b.budget() != nil {
		p.Reason = "wallet_code_read_budget_exhausted"
		return
	}
	code, err := b.s.deps.ChainRead.Code(b.ctx, bridgeRPCChain(l.Chain), a, chain.BlockRef{Number: &r.BlockNumber}, b.options())
	if err != nil {
		p.Reason = "historical_wallet_code_unavailable"
		return
	}
	p.HistoricalCodeRead = true
	p.HistoricalCode = "0x" + hex.EncodeToString(code)
	if len(code) > 0 {
		p.State = "contract_settlement_pending"
		p.Reason = "destination_contract_routing_requires_application_decoder"
		return
	}
	for _, m := range r.Movements {
		if m.Status == "success" && crosschain.Same(l.Chain, m.TokenContract, l.Asset) && crosschain.Same(l.Chain, m.From, l.Party) && !crosschain.Same(l.Chain, m.From, m.To) {
			p.OutgoingSameAsset = true
			p.Reason = "recipient_has_same_transaction_outgoing_funds"
			return
		}
	}
	matches := []crosschain.Movement{}
	for _, m := range l.Movements {
		if crosschain.Same(l.Chain, m.To, l.Party) && m.TokenContract == l.Asset && m.AmountRaw == l.AmountRaw {
			matches = append(matches, m)
		}
	}
	if len(matches) != 1 || r.Truncated {
		p.Reason = "wallet_credit_missing_or_ambiguous"
		return
	}
	p.Movement = matches[0]
	p.State = "direct_wallet_credit_verified"
}
