package crosschain

import (
	"encoding/hex"
	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
	"github.com/shopspring/decimal"
	"math/big"
	"strings"
	"time"
)

const FundsVerificationVersion = "crosschain-funds-v3"

// These records are built from server-side exact transaction reads. They are
// output evidence, never accepted as a query argument or a caller-supplied hint.
type BlockProof struct {
	Chain           string    `json:"chain"`
	NetworkID       string    `json:"network_id"`
	Number          uint64    `json:"number"`
	Hash            string    `json:"hash"`
	FinalizedNumber uint64    `json:"finalized_number"`
	FinalizedHash   string    `json:"finalized_hash"`
	Policy          string    `json:"policy"`
	Canonical       bool      `json:"canonical"`
	Finalized       bool      `json:"finalized"`
	CheckedAt       time.Time `json:"checked_at"`
	Source          string    `json:"source"`
	Corroboration   string    `json:"corroboration"`
}

type ContractProof struct {
	Address                  string `json:"address"`
	VerifierAddress          string `json:"verifier_address"`
	CodeSHA256               string `json:"code_sha256"`
	Implementation           string `json:"implementation,omitempty"`
	ImplementationCodeSHA256 string `json:"implementation_code_sha256,omitempty"`
	HistoricalState          bool   `json:"historical_state"`
	DeploymentSource         string `json:"deployment_source"`
}

type ProtocolEventProof struct {
	ApplicationContract *ContractProof `json:"application_contract,omitempty"`
	Chain               string         `json:"chain"`
	TxHash              string         `json:"tx_hash"`
	EventIndex          uint32         `json:"event_index"`
	Contract            ContractProof  `json:"contract"`
	Role                string         `json:"role"`
	Identifier          string         `json:"identifier"`
	PayloadSHA256       string         `json:"payload_sha256"`
	RawTopics           []string       `json:"raw_topics"`
	RawData             string         `json:"raw_data"`
}

type ProtocolProof struct {
	LayerZero        *LayerZeroAssociationProof `json:"layerzero,omitempty"`
	CCTP             *CCTPAssociationProof      `json:"cctp,omitempty"`
	Protocol         string                     `json:"protocol"`
	Version          string                     `json:"version"`
	Identifier       string                     `json:"identifier"`
	SourceChain      string                     `json:"source_chain"`
	DestinationChain string                     `json:"destination_chain"`
	PayloadSHA256    string                     `json:"payload_sha256"`
	Source           ProtocolEventProof         `json:"source"`
	Destination      ProtocolEventProof         `json:"destination"`
	Unique           bool                       `json:"unique"`
}

type FundsLegProof struct {
	ProtocolParty      string                 `json:"protocol_party,omitempty"`
	SourceCall         *FundingSourceCall     `json:"source_call,omitempty"`
	NativeConversion   *NativeConversionProof `json:"native_conversion,omitempty"`
	Role               string                 `json:"role"`
	Chain              string                 `json:"chain"`
	TxHash             string                 `json:"tx_hash"`
	Party              string                 `json:"party"`
	Asset              string                 `json:"asset"`
	AmountRaw          string                 `json:"amount_raw"`
	Amount             string                 `json:"amount"`
	Decimals           int                    `json:"decimals"`
	BridgeAsset        string                 `json:"bridge_asset"`
	BridgeAmountRaw    string                 `json:"bridge_amount_raw"`
	Binding            string                 `json:"binding"`
	MessageBound       bool                   `json:"message_bound"`
	UniquePath         bool                   `json:"unique_path"`
	Movements          []Movement             `json:"movements"`
	OtherDisbursements []Movement             `json:"other_disbursements"`
	Block              BlockProof             `json:"block"`
	ReceiptSHA256      string                 `json:"receipt_sha256"`
	TraceSHA256        string                 `json:"trace_sha256,omitempty"`
	ReadGaps           []string               `json:"read_gaps"`
	Truncated          bool                   `json:"truncated"`
}

type FundsProof struct {
	WalletSettlement *WalletSettlementProof `json:"wallet_settlement,omitempty"`
	Protocol         *ProtocolProof         `json:"protocol,omitempty"`
	Legs             []FundsLegProof        `json:"legs"`
	ReadGaps         []string               `json:"read_gaps"`
	BuiltAt          time.Time              `json:"built_at"`
}

var EVMNetworkIDs = map[string]string{"ethereum": "1", "bsc": "56", "polygon": "137", "arbitrum": "42161", "optimism": "10", "base": "8453", "avalanche": "43114"}

func positiveRaw(s string) bool {
	n, ok := new(big.Int).SetString(s, 10)
	return ok && n.Sign() > 0 && n.BitLen() <= 256 && n.String() == s
}
func evidenceDigest(s string) bool { b, e := hex.DecodeString(s); return e == nil && len(b) == 32 }
func completeMovement(c, h string, m Movement) bool {
	if m.Chain != c || !Same(c, m.TxHash, h) || m.Status != "success" || m.Timestamp.Unix() <= 0 || m.Decimals < 0 || m.Decimals > 38 || !positiveRaw(m.AmountRaw) || m.From == "" || m.To == "" || Same(c, m.From, m.To) {
		return false
	}
	n, _ := new(big.Int).SetString(m.AmountRaw, 10)
	return decimal.NewFromBigInt(n, int32(-m.Decimals)).String() == m.Amount
}

// ApplyFundsGate is the only promotion point. Legacy event-match flags and
// candidate ranking scores deliberately do not take part in this decision.
func ApplyFundsGate(t *Transfer) {
	t.FundingScope = "unverified"
	t.FinalWalletVerified = false
	t.FinalWalletState = "not_verified"
	t.FundsVerified = false
	t.VerificationRuleVersion = FundsVerificationVersion
	t.VerificationState = "pending"
	t.BlockingReasons = []string{}
	add := func(reason string) {
		for _, r := range t.BlockingReasons {
			if r == reason {
				return
			}
		}
		t.BlockingReasons = append(t.BlockingReasons, reason)
	}
	if t.Status != "completed" {
		add("transfer_not_completed")
	}
	if len(t.Refund) > 0 {
		add("refund_is_not_completed_destination")
	}
	if len(t.Source) != 1 || len(t.Destination) != 1 {
		add("single_source_and_destination_required")
	}
	if t.Proof == nil {
		add("protocol_and_funding_proof_unavailable")
		return
	}
	for _, gap := range t.Proof.ReadGaps {
		add(gap)
	}
	p := t.Proof.Protocol
	if p == nil {
		add("independent_protocol_association_unavailable")
		if len(t.Proof.ReadGaps) == 0 {
			t.VerificationState = "unsupported"
		}
		return
	}
	if !p.Unique {
		add("protocol_association_ambiguous")
		t.VerificationState = "conflict"
	}
	if p.Identifier == "" || p.Version == "" || !evidenceDigest(p.PayloadSHA256) {
		add("protocol_identity_or_payload_unavailable")
	}
	if p.SourceChain == p.DestinationChain {
		add("protocol_chain_pair_invalid")
	}
	for side, e := range []ProtocolEventProof{p.Source, p.Destination} {
		role, c := "source", p.SourceChain
		if side == 1 {
			role, c = "destination", p.DestinationChain
		}
		if e.Role != role || e.Chain != c || e.Identifier != p.Identifier || e.PayloadSHA256 != p.PayloadSHA256 {
			add("protocol_event_identity_conflict")
			t.VerificationState = "conflict"
		}
		if _, err := Hash(c, e.TxHash); err != nil {
			add("protocol_transaction_identity_invalid")
		}
		if !Same(c, e.Contract.Address, e.Contract.VerifierAddress) || !TrustedDeployment(p.Protocol, c, e.Contract.VerifierAddress) || !e.Contract.HistoricalState || !evidenceDigest(e.Contract.CodeSHA256) || e.Contract.DeploymentSource == "" {
			add("historical_official_deployment_unverified")
		}
		if e.Contract.Implementation != "" && !evidenceDigest(e.Contract.ImplementationCodeSHA256) {
			add("historical_implementation_unverified")
		}
		if p.Protocol == "layerzero" || p.Protocol == "cctp" || p.Protocol == "wormhole" && side == 0 {
			app := e.ApplicationContract
			role := "usdt0_application"
			if p.Protocol == "wormhole" {
				role = "token_bridge"
			}
			if p.Protocol == "cctp" {
				role = "token_messenger_v2"
			}
			valid := false
			if app != nil {
				d, ok := FindDeployment(p.Protocol, c, app.VerifierAddress)
				valid = ok && (d.Role == role || p.Protocol == "layerzero" && (IsOFTDeployment(d) || d.Role == "stargate_pool")) && Same(c, app.Address, app.VerifierAddress)
			}
			if app == nil || !valid || !app.HistoricalState || !evidenceDigest(app.CodeSHA256) || app.DeploymentSource == "" || app.Implementation != "" && !evidenceDigest(app.ImplementationCodeSHA256) {
				add("historical_official_application_unverified")
			}
		}
		if e.Contract.Address == "" || len(e.RawTopics) == 0 || e.RawData == "" {
			add("raw_protocol_event_unavailable")
		}
		if len(t.Source) == 1 && len(t.Destination) == 1 {
			leg := t.Source[0]
			if side == 1 {
				leg = t.Destination[0]
			}
			if leg.Chain != c || !Same(c, leg.TxHash, e.TxHash) {
				add("reported_pair_and_proof_conflict")
				t.VerificationState = "conflict"
			}
		}
	}
	if len(t.Proof.Legs) != 2 {
		add("both_funding_legs_required")
	}
	for side, l := range t.Proof.Legs {
		if side > 1 {
			add("unexpected_funding_leg")
			break
		}
		e, role := p.Source, "source"
		if side == 1 {
			e, role = p.Destination, "destination"
		}
		if l.Role != role || l.Chain != e.Chain || !Same(l.Chain, l.TxHash, e.TxHash) {
			add("funding_leg_identity_conflict")
			t.VerificationState = "conflict"
		}
		if l.Party == "" || l.Asset == "" || l.BridgeAsset == "" || !positiveRaw(l.AmountRaw) || !positiveRaw(l.BridgeAmountRaw) || l.Decimals < 0 || l.Decimals > 38 {
			add("funding_party_asset_or_amount_unavailable")
		}
		if !l.MessageBound || l.Binding == "" {
			add("funding_event_not_bound_to_protocol")
		}
		if !l.UniquePath {
			add("unique_funding_path_unavailable")
			for _, gap := range l.ReadGaps {
				if strings.HasPrefix(gap, "multiple_protocol_bound_") {
					t.VerificationState = "conflict"
				}
			}
		}
		if l.Truncated || len(l.ReadGaps) > 0 {
			add("required_funding_read_incomplete")
		}
		if !evidenceDigest(l.ReceiptSHA256) {
			add("exact_receipt_evidence_unavailable")
		}
		block := l.Block
		_, hashErr := Hash(l.Chain, block.Hash)
		_, finalErr := Hash(l.Chain, block.FinalizedHash)
		if block.Chain != l.Chain || block.NetworkID == "" || !block.Canonical || !block.Finalized || block.Policy == "" || hashErr != nil || finalErr != nil || block.Number > block.FinalizedNumber || block.CheckedAt.IsZero() || block.Source == "" {
			add("canonical_finality_unverified")
		}
		if (l.Asset == "native" || l.SourceCall != nil || side == 1 && (p.Protocol == "wormhole" || p.Protocol == "layerzero" || p.Protocol == "cctp")) && !evidenceDigest(l.TraceSHA256) {
			add("successful_native_trace_evidence_unavailable")
		}
		if n, ok := new(big.Int).SetString(l.AmountRaw, 10); !ok || l.Decimals < 0 || l.Decimals > 38 || decimal.NewFromBigInt(n, int32(-l.Decimals)).String() != l.Amount {
			add("funding_display_amount_conflict")
		}
		if p.Protocol == "across" {
			addr, err := chain.ParseAddress(e.Contract.Address)
			decoded, valid := DecodeAcross(l.Chain, chainread.Log{Address: addr, Topics: e.RawTopics, Data: e.RawData, BlockNumber: l.Block.Number})
			if valid && l.SourceCall != nil {
				call := l.SourceCall
				if side != 0 || l.ProtocolParty != decoded.Party || len(l.Movements) == 0 || call.BlockTimestamp != l.Movements[0].Timestamp.Unix() || !Same(l.Chain, call.From, l.Party) || !Same(l.Chain, call.To, decoded.Counterparty) || !AcrossDepositCallMatches(call.Input, decoded, call.BlockTimestamp, call.From) {
					add("across_actual_payer_call_conflict")
				} else {
					decoded.Party = l.Party
				}
			}
			if err != nil || !valid || decoded.Identifier != p.Identifier || decoded.PayloadSHA256 != p.PayloadSHA256 || decoded.Role != l.Role || !Same(l.Chain, decoded.Asset, l.BridgeAsset) || decoded.AmountRaw != l.BridgeAmountRaw || !Same(l.Chain, decoded.Party, l.Party) {
				add("decoded_protocol_funding_conflict")
			}
			if valid && !validAcrossFunding(decoded, l) {
				add("protocol_funding_counterparty_conflict")
			}
		}
		if p.Protocol == "layerzero" && !validLayerZeroFunding(p, l, side) {
			add("layerzero_payload_or_funding_conflict")
		}
		if p.Protocol == "cctp" && !validCCTPFunding(p, l, side) {
			add("cctp_attested_message_or_funding_conflict")
		}
		if expected := EVMNetworkIDs[l.Chain]; expected != "" && block.NetworkID != "" && block.NetworkID != expected {
			add("network_identity_conflict")
			t.VerificationState = "conflict"
		}
		if len(l.Movements) == 0 {
			add("actual_funding_events_unavailable")
		}
		seen := map[string]bool{}
		partyFound := false
		for _, m := range l.Movements {
			if m.Chain != l.Chain || !Same(l.Chain, m.TxHash, l.TxHash) || m.Status != "success" || m.Timestamp.Unix() <= 0 || m.Decimals < 0 || m.Decimals > 38 || !positiveRaw(m.AmountRaw) || m.From == "" || m.To == "" || Same(m.Chain, m.From, m.To) {
				add("actual_funding_event_invalid")
			}
			if n, ok := new(big.Int).SetString(m.AmountRaw, 10); !ok || m.Decimals < 0 || m.Decimals > 38 || decimal.NewFromBigInt(n, int32(-m.Decimals)).String() != m.Amount {
				add("actual_event_display_amount_conflict")
			}
			if seen[m.Identity()] {
				add("duplicate_funding_event")
				t.VerificationState = "conflict"
			}
			seen[m.Identity()] = true
			party := m.From
			if side == 1 {
				party = m.To
			}
			if Same(m.Chain, party, l.Party) && Same(m.Chain, m.TokenContract, l.Asset) && m.AmountRaw == l.AmountRaw && m.Decimals == l.Decimals {
				partyFound = true
			}
		}
		if !partyFound {
			add("party_amount_event_unavailable")
		}
		for _, m := range l.OtherDisbursements {
			if seen[m.Identity()] || !completeMovement(l.Chain, l.TxHash, m) {
				add("split_disbursement_invalid")
			}
			seen[m.Identity()] = true
		}
		if conversion := l.NativeConversion; conversion != nil {
			for _, m := range append(append([]Movement(nil), conversion.NativeFunding...), conversion.TokenFunding...) {
				if seen[m.Identity()] || !completeMovement(l.Chain, l.TxHash, m) {
					add("native_conversion_funding_invalid")
				}
				seen[m.Identity()] = true
			}
		}
	}
	if len(t.BlockingReasons) == 0 {
		t.FundsVerified = true
		t.MessageOnly = false
		t.VerificationState = "verified"
		t.FundingScope = "protocol_bound_bridge_participants"
		t.FinalWalletState = "downstream_settlement_not_verified"
		applyWalletGate(t)
	}
}

// EnforceFundsOutput also catches conflicting adapters for the same input or
// message. Default output exposes only fully verified relationships. Discovery
// preserves all protocol records for inspection without promoting them.
func EnforceFundsOutput(mode string, result *QueryResult) {
	result.VerificationMode = mode
	result.PendingTransfers = []Transfer{}
	for i := range result.Transfers {
		ApplyFundsGate(&result.Transfers[i])
	}
	conflicts := map[int]bool{}
	for i := range result.Transfers {
		for j := i + 1; j < len(result.Transfers); j++ {
			a, b := &result.Transfers[i], &result.Transfers[j]
			if !a.FundsVerified || !b.FundsVerified || a.Proof == nil || b.Proof == nil || a.Proof.Protocol == nil || b.Proof.Protocol == nil {
				continue
			}
			pa, pb := a.Proof.Protocol, b.Proof.Protocol
			if pa.Protocol == pb.Protocol && pa.Identifier == pb.Identifier && pa.SourceChain == pb.SourceChain && pa.DestinationChain == pb.DestinationChain && (!Same(pa.SourceChain, pa.Source.TxHash, pb.Source.TxHash) || !Same(pa.DestinationChain, pa.Destination.TxHash, pb.Destination.TxHash) || pa.PayloadSHA256 != pb.PayloadSHA256) {
				conflicts[i], conflicts[j] = true, true
			}
		}
	}
	for i := range conflicts {
		t := &result.Transfers[i]
		t.FundsVerified = false
		t.VerificationState = "conflict"
		t.FundingScope = "unverified"
		t.FinalWalletVerified = false
		t.FinalWalletState = "not_verified"
		t.BlockingReasons = append(t.BlockingReasons, "adapter_protocol_pair_conflict")
	}
	if mode != "funds" {
		return
	}
	verified := []Transfer{}
	seen := map[string]bool{}
	for _, t := range result.Transfers {
		if !t.FundsVerified {
			result.PendingTransfers = append(result.PendingTransfers, t)
			continue
		}
		p := t.Proof.Protocol
		key := strings.Join([]string{p.Protocol, p.Identifier, p.SourceChain, p.Source.TxHash, p.DestinationChain, p.Destination.TxHash}, "|")
		if !seen[key] {
			verified = append(verified, t)
			seen[key] = true
		}
	}
	result.Transfers = verified
}
