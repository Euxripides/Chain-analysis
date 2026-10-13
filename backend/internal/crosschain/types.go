// Package crosschain provides read-only bridge correlation and bounded candidate matching.
package crosschain

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/addressnorm"
	"github.com/blockchain-platform/blockchain-platform/internal/solana"
	"github.com/shopspring/decimal"
)

var Chains = []string{"ethereum", "bsc", "tron", "polygon", "arbitrum", "optimism", "base", "avalanche", "solana"}
var Protocols = []string{"lifi", "near_intents", "bridgers", "layerzero", "cbridge", "across", "relay", "wormhole", "cctp"}

func Chain(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, c := range Chains {
		if value == c {
			return c, nil
		}
	}
	return "", fmt.Errorf("unsupported chain %q", value)
}

func Hash(c, value string) (string, error) {
	value = strings.TrimSpace(value)
	if c == "solana" {
		if len(value) > 88 {
			return "", fmt.Errorf("invalid complete Solana signature")
		}
		if raw, err := solana.DecodeBase58(value); err != nil || len(raw) != 64 {
			return "", fmt.Errorf("invalid complete Solana signature")
		}
		return value, nil
	}
	value = strings.ToLower(value)
	if c == "tron" {
		value = "0x" + strings.TrimPrefix(value, "0x")
	} else if !strings.HasPrefix(value, "0x") {
		return "", fmt.Errorf("complete EVM hash requires 0x prefix")
	}
	if raw, err := hex.DecodeString(strings.TrimPrefix(value, "0x")); err != nil || len(raw) != 32 {
		return "", fmt.Errorf("complete 32-byte transaction hash required")
	}
	return value, nil
}

func Same(c, a, b string) bool {
	return addressnorm.NormalizeKey(c, a) == addressnorm.NormalizeKey(c, b)
}

type QueryRequest struct {
	VerificationMode string         `json:"verification_mode,omitempty"`
	Bridge           string         `json:"bridge"`
	Chain            string         `json:"chain,omitempty"`
	TxHash           string         `json:"tx_hash,omitempty"`
	OrderID          string         `json:"order_id,omitempty"`
	TransferID       string         `json:"transfer_id,omitempty"`
	DepositAddress   string         `json:"deposit_address,omitempty"`
	DepositMemo      string         `json:"deposit_memo,omitempty"`
	MaxResults       int            `json:"max_results"`
	Hints            []ProtocolHint `json:"-"`
}

func (r *QueryRequest) Validate() error {
	if r.VerificationMode == "" {
		r.VerificationMode = "funds"
	}
	if r.VerificationMode != "funds" && r.VerificationMode != "discovery" {
		return fmt.Errorf("verification_mode must be funds or discovery")
	}
	if r.Bridge == "" {
		r.Bridge = "auto"
	}
	if r.Bridge != "auto" {
		found := false
		for _, b := range Protocols {
			if b == r.Bridge {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("unsupported bridge")
		}
	}
	if r.MaxResults == 0 {
		r.MaxResults = 20
	}
	if r.MaxResults < 1 || r.MaxResults > 50 {
		return fmt.Errorf("max_results must be 1..50")
	}
	var err error
	if r.Chain != "" {
		r.Chain, err = Chain(r.Chain)
		if err != nil {
			return err
		}
	}
	if r.TxHash != "" {
		if r.Chain == "" {
			return fmt.Errorf("chain required with tx_hash")
		}
		r.TxHash, err = Hash(r.Chain, r.TxHash)
		if err != nil {
			return err
		}
	}
	for _, v := range []string{r.OrderID, r.TransferID, r.DepositAddress, r.DepositMemo} {
		if len(v) > 256 || strings.ContainsAny(v, "\r\n\x00") {
			return fmt.Errorf("invalid bridge identifier")
		}
	}
	if r.TxHash == "" && r.OrderID == "" && r.TransferID == "" && r.DepositAddress == "" {
		return fmt.Errorf("provide tx_hash or a protocol query identifier")
	}
	if r.Bridge == "auto" && r.TxHash == "" {
		return fmt.Errorf("specify bridge for an order/deposit/transfer identifier")
	}
	if r.Bridge == "bridgers" && r.OrderID == "" {
		return fmt.Errorf("bridgers requires order_id; creating orders is not supported")
	}
	if r.Bridge == "cbridge" && r.TransferID == "" {
		return fmt.Errorf("cbridge requires transfer_id")
	}
	if r.Bridge == "lifi" || r.Bridge == "layerzero" || r.Bridge == "across" || r.Bridge == "wormhole" || r.Bridge == "cctp" {
		if r.TxHash == "" {
			return fmt.Errorf("tx_hash required for this bridge")
		}
	}
	if r.Bridge == "near_intents" && r.TxHash == "" && r.DepositAddress == "" {
		return fmt.Errorf("near_intents requires tx_hash or deposit_address")
	}
	if r.Bridge == "relay" && r.TxHash == "" && r.OrderID == "" {
		return fmt.Errorf("relay requires tx_hash or order_id")
	}
	if r.DepositAddress != "" && r.Bridge != "near_intents" || r.DepositMemo != "" && r.DepositAddress == "" {
		return fmt.Errorf("deposit_address/memo are supported only for explicit near_intents deposit queries")
	}
	if r.TransferID != "" && r.Bridge != "cbridge" {
		return fmt.Errorf("transfer_id is supported only for cbridge")
	}
	if r.OrderID != "" && (r.Bridge == "auto" || r.Bridge == "near_intents" || r.Bridge == "cbridge" || r.Bridge == "layerzero") {
		return fmt.Errorf("order_id is not supported for this query type")
	}
	if r.Chain != "" && r.TxHash == "" {
		return fmt.Errorf("chain is a transaction-hash constraint and requires tx_hash")
	}
	if r.TransferID != "" {
		v := strings.TrimPrefix(r.TransferID, "0x")
		if bytes, e := hex.DecodeString(v); e != nil || len(bytes) != 32 {
			return fmt.Errorf("transfer_id must be a complete 32-byte hex ID")
		}
	}
	return nil
}

type Leg struct {
	Chain                 string                 `json:"chain"`
	ChainID               string                 `json:"chain_id,omitempty"`
	TxHash                string                 `json:"tx_hash,omitempty"`
	Address               string                 `json:"address,omitempty"`
	TokenContract         string                 `json:"token_contract,omitempty"`
	Token                 string                 `json:"token,omitempty"`
	AmountRaw             string                 `json:"amount_raw,omitempty"`
	Amount                string                 `json:"amount,omitempty"`
	Decimals              *int                   `json:"decimals,omitempty"`
	Timestamp             string                 `json:"timestamp,omitempty"`
	AmountsScope          string                 `json:"amounts_scope,omitempty"`
	NormalizedAmountRaw   string                 `json:"normalized_amount_raw,omitempty"`
	OriginChainID         string                 `json:"origin_chain_id,omitempty"`
	OriginToken           string                 `json:"origin_token,omitempty"`
	BridgeContract        string                 `json:"bridge_contract,omitempty"`
	ReportedAddress       string                 `json:"reported_address,omitempty"`
	AddressScope          string                 `json:"address_scope,omitempty"`
	ReportedTokenContract string                 `json:"reported_token_contract,omitempty"`
	ReportedAmountRaw     string                 `json:"reported_amount_raw,omitempty"`
	Reconciliation        *FundingReconciliation `json:"reconciliation,omitempty"`
}

type FundingReconciliation struct {
	Kind               string     `json:"kind"`
	FundingPath        []Movement `json:"funding_path"`
	OtherDisbursements []Movement `json:"other_disbursements"`
}

// ProtocolHint is derived by the server from a receipt, never trusted from a request.
type ProtocolHint struct {
	Bridge           string `json:"bridge"`
	Kind             string `json:"kind"`
	Contract         string `json:"contract"`
	Identifier       string `json:"identifier"`
	SourceDomain     string `json:"source_domain,omitempty"`
	DestinationChain string `json:"destination_chain,omitempty"`
	Recipient        string `json:"recipient,omitempty"`
	EventIndex       uint32 `json:"event_index"`
}

type Evidence struct {
	Source             string    `json:"source"`
	FetchedAt          time.Time `json:"fetched_at"`
	SHA256             string    `json:"response_sha256"`
	MatchedIdentifiers []string  `json:"matched_identifiers"`
	PageTruncated      bool      `json:"page_truncated"`
}

type Transfer struct {
	FundingScope            string         `json:"funding_scope"`
	FinalWalletVerified     bool           `json:"final_wallet_verified"`
	FinalWalletState        string         `json:"final_wallet_state"`
	FundsVerified           bool           `json:"funds_verified"`
	VerificationState       string         `json:"verification_state"`
	BlockingReasons         []string       `json:"blocking_reasons"`
	VerificationRuleVersion string         `json:"verification_rule_version"`
	Proof                   *FundsProof    `json:"proof,omitempty"`
	Bridge                  string         `json:"bridge"`
	UnderlyingBridge        string         `json:"underlying_bridge,omitempty"`
	OrderID                 string         `json:"order_id,omitempty"`
	Status                  string         `json:"status"`
	RawStatus               string         `json:"raw_status"`
	Association             string         `json:"association"`
	Source                  []Leg          `json:"source"`
	Destination             []Leg          `json:"destination"`
	Refund                  []Leg          `json:"refund"`
	Evidence                Evidence       `json:"evidence"`
	OnChainVerified         bool           `json:"on_chain_verified"`
	Verification            []Verification `json:"verification"`
	PairingVerified         bool           `json:"pairing_verified"`
	MessageOnly             bool           `json:"message_only"`
}

type Verification struct {
	LegRole           string     `json:"leg_role"`
	Chain             string     `json:"chain"`
	TxHash            string     `json:"tx_hash"`
	Status            string     `json:"status"`
	AmountsMatched    bool       `json:"amounts_matched"`
	Reason            string     `json:"reason"`
	ObservedMovements []Movement `json:"observed_movements"`
	Source            string     `json:"source,omitempty"`
	Truncated         bool       `json:"truncated"`
	IdentifierMatched bool       `json:"identifier_matched"`
	ReadGaps          []string   `json:"read_gaps,omitempty"`
}

type Attempt struct {
	Bridge string `json:"bridge"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

type QueryResult struct {
	VerificationMode  string            `json:"verification_mode"`
	PendingTransfers  []Transfer        `json:"pending_transfers"`
	Transfers         []Transfer        `json:"transfers"`
	Attempts          []Attempt         `json:"attempts"`
	Truncated         bool              `json:"truncated"`
	SearchedComplete  bool              `json:"searched_complete"`
	StoppedAfterMatch bool              `json:"stopped_after_match"`
	ProtocolHints     []ProtocolHint    `json:"protocol_hints"`
	CandidateSearches []CandidateSearch `json:"candidate_searches"`
}

type CandidateSearch struct {
	AmountBasis string            `json:"amount_basis,omitempty"`
	Status      string            `json:"status"`
	Reason      string            `json:"reason,omitempty"`
	Request     *CandidateRequest `json:"request,omitempty"`
	Result      *MatchResult      `json:"result,omitempty"`
}

type Movement struct {
	Chain           string    `json:"chain"`
	TxHash          string    `json:"tx_hash"`
	Dataset         string    `json:"dataset"`
	Index           uint32    `json:"event_index"`
	Timestamp       time.Time `json:"timestamp"`
	TimestampSource string    `json:"timestamp_source,omitempty"`
	From            string    `json:"from_address"`
	To              string    `json:"to_address"`
	TokenContract   string    `json:"token_contract"`
	Token           string    `json:"token"`
	Decimals        int       `json:"decimals"`
	AmountRaw       string    `json:"amount_raw"`
	Amount          string    `json:"amount"`
	Status          string    `json:"status"`
}

func (m Movement) Identity() string {
	return fmt.Sprintf("%s:%s:%s:%d:%s", m.Chain, m.TxHash, m.Dataset, m.Index, m.TokenContract)
}

type Anchor struct {
	Chain      string  `json:"chain"`
	TxHash     string  `json:"tx_hash"`
	Dataset    string  `json:"dataset"`
	EventIndex *uint32 `json:"event_index,omitempty"`
}

type CandidateRequest struct {
	Anchor               Anchor    `json:"anchor"`
	SearchChain          string    `json:"search_chain"`
	SearchAddresses      []string  `json:"search_addresses"`
	SearchDirection      string    `json:"search_direction"`
	Direction            string    `json:"direction"`
	AssetKind            string    `json:"asset_kind"`
	TokenContract        string    `json:"token_contract,omitempty"`
	AssetRelation        string    `json:"asset_relation"`
	ExpectedAmount       string    `json:"expected_amount,omitempty"`
	AbsoluteTolerance    string    `json:"absolute_tolerance"`
	RelativeToleranceBPS int       `json:"relative_tolerance_bps"`
	StartTime            time.Time `json:"start_time"`
	EndTime              time.Time `json:"end_time"`
	Counterparty         string    `json:"counterparty,omitempty"`
	MaxParts             int       `json:"max_parts"`
	MaxResults           int       `json:"max_results"`
	ScanLimit            int       `json:"scan_limit"`
	MaxCombinations      int       `json:"max_combinations"`
}

// Plain decimal strings keep precision without accepting exponents that allocate huge big.Int values.
func Amount(value string) (decimal.Decimal, error) {
	if value == "" || len(value) > 100 {
		return decimal.Zero, fmt.Errorf("amount must be a decimal string of at most 100 characters")
	}
	dots := 0
	for _, c := range value {
		if c == '.' {
			dots++
			continue
		}
		if c < '0' || c > '9' {
			return decimal.Zero, fmt.Errorf("amount must be a nonnegative plain decimal string")
		}
	}
	if dots > 1 || value[0] == '.' || value[len(value)-1] == '.' {
		return decimal.Zero, fmt.Errorf("invalid decimal amount")
	}
	v, e := decimal.NewFromString(value)
	if e != nil || v.IsNegative() {
		return decimal.Zero, fmt.Errorf("invalid amount")
	}
	return v, nil
}

func (r *CandidateRequest) Validate() error {
	var e error
	r.Anchor.Chain, e = Chain(r.Anchor.Chain)
	if e != nil {
		return e
	}
	r.Anchor.TxHash, e = Hash(r.Anchor.Chain, r.Anchor.TxHash)
	if e != nil {
		return e
	}
	if r.Anchor.Dataset != "transactions" && r.Anchor.Dataset != "internal_transactions" && r.Anchor.Dataset != "token_transfers" {
		return fmt.Errorf("explicit anchor dataset required")
	}
	if r.Anchor.Dataset == "transactions" && r.Anchor.EventIndex != nil {
		return fmt.Errorf("transactions anchor has no event_index")
	}
	r.SearchChain, e = Chain(r.SearchChain)
	if e != nil {
		return e
	}
	if r.SearchChain == r.Anchor.Chain {
		return fmt.Errorf("search_chain must differ from anchor chain")
	}
	if len(r.SearchAddresses) < 1 || len(r.SearchAddresses) > 10 {
		return fmt.Errorf("search_addresses must contain 1..10 addresses")
	}
	r.SearchAddresses, e = addressnorm.NormalizeList(r.SearchChain, r.SearchAddresses)
	if e != nil {
		return e
	}
	if r.Counterparty != "" {
		r.Counterparty, e = addressnorm.Normalize(r.SearchChain, r.Counterparty)
		if e != nil {
			return e
		}
	}
	if r.SearchDirection != "in" && r.SearchDirection != "out" {
		return fmt.Errorf("search_direction must be in or out relative to search_addresses")
	}
	if r.Direction != "forward" && r.Direction != "backward" {
		return fmt.Errorf("direction must be forward or backward")
	}
	if r.AssetKind != "native" && r.AssetKind != "token" {
		return fmt.Errorf("asset_kind must be native or token")
	}
	if r.AssetKind == "token" {
		r.TokenContract, e = addressnorm.Normalize(r.SearchChain, r.TokenContract)
		if e != nil {
			return fmt.Errorf("complete target token contract/mint required: %w", e)
		}
	} else if r.TokenContract != "" {
		return fmt.Errorf("native asset must not specify token_contract")
	}
	if r.AssetRelation != "same_asset" && r.AssetRelation != "swap" {
		return fmt.Errorf("explicit asset_relation (same_asset or swap) required")
	}
	if r.AssetRelation == "swap" && r.ExpectedAmount == "" {
		return fmt.Errorf("swap candidates require expected_amount from a known quote or explicit user condition")
	}
	if r.ExpectedAmount != "" {
		v, e := Amount(r.ExpectedAmount)
		if e != nil || !v.IsPositive() {
			return fmt.Errorf("expected_amount must be positive")
		}
	}
	if r.AbsoluteTolerance == "" {
		r.AbsoluteTolerance = "0"
	}
	if _, e = Amount(r.AbsoluteTolerance); e != nil {
		return e
	}
	if r.RelativeToleranceBPS < 0 || r.RelativeToleranceBPS > 500 {
		return fmt.Errorf("relative_tolerance_bps must be 0..500")
	}
	if r.StartTime.IsZero() || r.EndTime.IsZero() || !r.EndTime.After(r.StartTime) || r.EndTime.Sub(r.StartTime) > 24*time.Hour {
		return fmt.Errorf("explicit search time range must be positive and at most 24 hours")
	}
	if r.MaxParts == 0 {
		r.MaxParts = 1
	}
	if r.MaxParts < 1 || r.MaxParts > 3 {
		return fmt.Errorf("max_parts must be 1..3")
	}
	if r.MaxResults == 0 {
		r.MaxResults = 20
	}
	if r.MaxResults < 1 || r.MaxResults > 50 {
		return fmt.Errorf("max_results must be 1..50")
	}
	if r.ScanLimit == 0 {
		r.ScanLimit = 500
	}
	if r.ScanLimit < 1 || r.ScanLimit > 1000 {
		return fmt.Errorf("scan_limit must be 1..1000")
	}
	if r.MaxCombinations == 0 {
		r.MaxCombinations = 5000
	}
	if r.MaxCombinations < 1 || r.MaxCombinations > 20000 {
		return fmt.Errorf("max_combinations must be 1..20000")
	}
	return nil
}

type Candidate struct {
	Movements             []Movement `json:"movements"`
	TotalAmount           string     `json:"total_amount"`
	AmountDifference      string     `json:"amount_difference"`
	TimeDifferenceSeconds int64      `json:"time_difference_seconds"`
	Score                 int        `json:"score"`
	Reasons               []string   `json:"reasons"`
	Association           string     `json:"association"`
}

type MatchResult struct {
	Anchor                Movement    `json:"anchor"`
	Candidates            []Candidate `json:"candidates"`
	ReferenceAmount       string      `json:"reference_amount"`
	AmountBasis           string      `json:"amount_basis"`
	AssetRelation         string      `json:"asset_relation"`
	AssetIdentityVerified bool        `json:"asset_identity_verified"`
	Scanned               int         `json:"scanned"`
	CombinationsTested    int         `json:"combinations_tested"`
	ScanTruncated         bool        `json:"scan_truncated"`
	ResultTruncated       bool        `json:"result_truncated"`
	CombinationsTruncated bool        `json:"combinations_truncated"`
}
