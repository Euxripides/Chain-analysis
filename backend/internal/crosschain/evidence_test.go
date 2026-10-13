package crosschain

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
)

func fixtureWord(n int64) []byte { return big.NewInt(n).FillBytes(make([]byte, 32)) }
func fixtureAddress(s string) []byte {
	b, _ := hex.DecodeString(s[2:])
	return append(make([]byte, 12), b...)
}
func fixtureFunds() Transfer {
	payer := "0x" + strings.Repeat("1", 40)
	recipient := "0x" + strings.Repeat("2", 40)
	relayer := "0x" + strings.Repeat("3", 40)
	input := "0x" + strings.Repeat("4", 40)
	output := "0x" + strings.Repeat("5", 40)
	sourcePool := DeploymentAddress("across", "ethereum", "spoke_pool")
	destPool := DeploymentAddress("across", "base", "spoke_pool")
	data := []byte{}
	for _, w := range [][]byte{fixtureAddress(input), fixtureAddress(output), fixtureWord(100), fixtureWord(99), fixtureWord(1), fixtureWord(200), fixtureWord(150), fixtureAddress(recipient), fixtureWord(0), fixtureWord(320), fixtureWord(0)} {
		data = append(data, w...)
	}
	dest := []byte{}
	for _, w := range [][]byte{fixtureAddress(input), fixtureAddress(output), fixtureWord(100), fixtureWord(99), fixtureWord(8453), fixtureWord(200), fixtureWord(150), fixtureWord(0), fixtureAddress(payer), fixtureAddress(recipient), AcrossMessageHash(nil), fixtureAddress(recipient), AcrossMessageHash(nil), fixtureWord(99), fixtureWord(0)} {
		dest = append(dest, w...)
	}
	a, _ := chain.ParseAddress(sourcePool)
	z, _ := chain.ParseAddress(destPool)
	hash1 := "0x" + strings.Repeat("a", 64)
	hash2 := "0x" + strings.Repeat("b", 64)
	topic := func(w []byte) string { return "0x" + hex.EncodeToString(w) }
	l := chainread.Log{Address: a, Topics: []string{EventTopic(acrossDepositNew), topic(fixtureWord(8453)), topic(fixtureWord(42)), topic(fixtureAddress(payer))}, Data: topic(data), BlockNumber: 21000000, TxHash: chain.Hash(hash1), LogIndex: 5}
	r := chainread.Log{Address: z, Topics: []string{EventTopic(acrossFillNew), topic(fixtureWord(1)), topic(fixtureWord(42)), topic(fixtureAddress(relayer))}, Data: topic(dest), BlockNumber: 5000000, TxHash: chain.Hash(hash2), LogIndex: 6}
	left, ok := DecodeAcross("ethereum", l)
	if !ok {
		panic("invalid fixture deposit")
	}
	right, ok := DecodeAcross("base", r)
	if !ok || right.PayloadSHA256 != left.PayloadSHA256 {
		panic("invalid fixture fill")
	}
	p := &ProtocolProof{Protocol: "across", Version: left.Version, Identifier: left.Identifier, SourceChain: "ethereum", DestinationChain: "base", PayloadSHA256: left.PayloadSHA256, Unique: true}
	t := Transfer{Status: "completed", Source: []Leg{{Chain: "ethereum", TxHash: hash1}}, Destination: []Leg{{Chain: "base", TxHash: hash2}}, Proof: &FundsProof{Protocol: p, BuiltAt: time.Now().UTC(), ReadGaps: []string{}}}
	for i, e := range []DecodedBridgeEvent{left, right} {
		c, h, pool, num, who, asset, raw := "ethereum", hash1, sourcePool, uint64(21000000), payer, input, "100"
		from, to := payer, pool
		if i == 1 {
			c, h, pool, num, who, asset, raw = "base", hash2, destPool, 5000000, recipient, output, "99"
			from, to = relayer, recipient
		}
		d, _ := FindDeployment("across", c, pool)
		ev := ProtocolEventProof{Chain: c, TxHash: h, EventIndex: uint32(e.Log.LogIndex), Role: e.Role, Identifier: p.Identifier, PayloadSHA256: p.PayloadSHA256, RawTopics: e.Log.Topics, RawData: e.Log.Data, Contract: ContractProof{Address: pool, VerifierAddress: pool, CodeSHA256: strings.Repeat("a", 64), HistoricalState: true, DeploymentSource: d.Source}}
		if i == 0 {
			p.Source = ev
		} else {
			p.Destination = ev
		}
		m := Movement{Chain: c, TxHash: h, Dataset: "token_transfers", Index: 10, Timestamp: time.Now().UTC(), From: from, To: to, TokenContract: asset, Decimals: 0, AmountRaw: raw, Amount: raw, Status: "success"}
		t.Proof.Legs = append(t.Proof.Legs, FundsLegProof{Role: e.Role, Chain: c, TxHash: h, Party: who, Asset: asset, AmountRaw: raw, Amount: raw, Decimals: 0, BridgeAsset: asset, BridgeAmountRaw: raw, Binding: p.Identifier, MessageBound: true, UniquePath: true, Movements: []Movement{m}, ReceiptSHA256: strings.Repeat("c", 64), Block: BlockProof{Chain: c, NetworkID: EVMNetworkIDs[c], Number: num, Hash: "0x" + strings.Repeat("d", 64), FinalizedNumber: num + 100, FinalizedHash: "0x" + strings.Repeat("e", 64), Policy: "rpc_finalized_and_fresh_canonical_block", Canonical: true, Finalized: true, CheckedAt: time.Now().UTC(), Source: "configured_rpc_gateway:test"}})
	}
	return t
}
func cloneFunds(t Transfer) Transfer {
	raw, _ := json.Marshal(t)
	var out Transfer
	json.Unmarshal(raw, &out)
	return out
}

func TestFundsGateRejects1000DeterministicFaults(t *testing.T) {
	base := fixtureFunds()
	ApplyFundsGate(&base)
	if !base.FundsVerified {
		t.Fatal(base.BlockingReasons)
	}
	faults := []struct {
		name   string
		mutate func(*Transfer, int)
	}{
		{"refund", func(x *Transfer, n int) { x.Refund = []Leg{{Chain: "base"}} }},
		{"failed", func(x *Transfer, n int) { x.Status = "failed" }},
		{"missing_source", func(x *Transfer, n int) { x.Source = nil }},
		{"api_only", func(x *Transfer, n int) { x.Proof = nil; x.OnChainVerified = true; x.PairingVerified = true }},
		{"missing_protocol", func(x *Transfer, n int) { x.Proof.Protocol = nil }},
		{"ambiguous_message", func(x *Transfer, n int) { x.Proof.Protocol.Unique = false }},
		{"source_chain_reuse", func(x *Transfer, n int) { x.Proof.Protocol.SourceChain = "bsc" }},
		{"different_payload", func(x *Transfer, n int) { x.Proof.Protocol.Destination.PayloadSHA256 = ProofSHA([]byte(fmt.Sprint(n))) }},
		{"fake_official_contract", func(x *Transfer, n int) {
			x.Proof.Protocol.Source.Contract.VerifierAddress = "0x" + strings.Repeat("9", 40)
		}},
		{"current_not_historical", func(x *Transfer, n int) { x.Proof.Protocol.Source.Contract.HistoricalState = false }},
		{"missing_implementation", func(x *Transfer, n int) {
			x.Proof.Protocol.Source.Contract.Implementation = "0x" + strings.Repeat("9", 40)
		}},
		{"different_destination_tx", func(x *Transfer, n int) { x.Destination[0].TxHash = "0x" + strings.Repeat("9", 64) }},
		{"missing_receipt", func(x *Transfer, n int) { x.Proof.Legs[0].ReceiptSHA256 = "" }},
		{"same_symbol_different_asset", func(x *Transfer, n int) { x.Proof.Legs[0].Asset = "0x" + strings.Repeat("8", 40) }},
		{"wrong_integer_amount", func(x *Transfer, n int) { x.Proof.Legs[0].AmountRaw = fmt.Sprint(n + 101) }},
		{"amount_display_precision", func(x *Transfer, n int) { x.Proof.Legs[0].Amount = "1" }},
		{"unknown_decimals", func(x *Transfer, n int) { x.Proof.Legs[0].Decimals = -1 }},
		{"not_message_bound", func(x *Transfer, n int) { x.Proof.Legs[0].MessageBound = false }},
		{"ambiguous_paths", func(x *Transfer, n int) { x.Proof.Legs[0].UniquePath = false }},
		{"truncation", func(x *Transfer, n int) { x.Proof.Legs[0].Truncated = true }},
		{"missing_page_timeout_or_429", func(x *Transfer, n int) {
			x.Proof.Legs[0].ReadGaps = []string{[]string{"missing_page", "timeout", "http_429"}[n%3]}
		}},
		{"not_finalized", func(x *Transfer, n int) { x.Proof.Legs[0].Block.Finalized = false }},
		{"reorg", func(x *Transfer, n int) { x.Proof.Legs[0].Block.Canonical = false }},
		{"wrong_network", func(x *Transfer, n int) { x.Proof.Legs[0].Block.NetworkID = "56" }},
		{"duplicate_event", func(x *Transfer, n int) {
			x.Proof.Legs[0].Movements = append(x.Proof.Legs[0].Movements, x.Proof.Legs[0].Movements[0])
		}},
		{"wrong_party_or_token_account_owner", func(x *Transfer, n int) { x.Proof.Legs[0].Party = "0x" + strings.Repeat("7", 40) }},
		{"rolled_back_movement", func(x *Transfer, n int) { x.Proof.Legs[0].Movements[0].Status = "failed" }},
		{"self_transfer", func(x *Transfer, n int) { x.Proof.Legs[0].Movements[0].To = x.Proof.Legs[0].Movements[0].From }},
		{"raw_event_content_changed", func(x *Transfer, n int) { x.Proof.Protocol.Source.RawData = "0x" + strings.Repeat("0", 640) }},
		{"multiple_funding_legs", func(x *Transfer, n int) { x.Proof.Legs = append(x.Proof.Legs, x.Proof.Legs[0]) }},
		{"fake_code_digest", func(x *Transfer, n int) { x.Proof.Protocol.Source.Contract.CodeSHA256 = "short" }},
		{"funding_tx_conflict", func(x *Transfer, n int) { x.Proof.Legs[0].TxHash = "0x" + strings.Repeat("9", 64) }},
		{"missing_funding_event", func(x *Transfer, n int) { x.Proof.Legs[0].Movements = nil }},
		{"wrong_counterparty", func(x *Transfer, n int) { x.Proof.Legs[0].Movements[0].To = "0x" + strings.Repeat("7", 40) }},
		{"amount_event_mismatch", func(x *Transfer, n int) { x.Proof.Legs[1].Movements[0].AmountRaw = "98" }},
		{"split_duplicate", func(x *Transfer, n int) { x.Proof.Legs[1].OtherDisbursements = x.Proof.Legs[1].Movements }},
		{"read_error", func(x *Transfer, n int) { x.Proof.ReadGaps = []string{"rpc_missing_required_input"} }},
		{"header_not_confirmed", func(x *Transfer, n int) { x.Proof.Legs[0].Block.FinalizedNumber = 1 }},
		{"fake_block_hash", func(x *Transfer, n int) { x.Proof.Legs[0].Block.Hash = "fake" }},
		{"wrong_role", func(x *Transfer, n int) { x.Proof.Protocol.Destination.Role = "source" }},
	}
	for n := 0; n < 1000; n++ {
		f := faults[n%len(faults)]
		x := cloneFunds(base)
		f.mutate(&x, n)
		ApplyFundsGate(&x)
		if x.FundsVerified {
			t.Fatalf("wrong promotion case %d %s", n, f.name)
		}
	}
	t.Logf("1000 deterministic faults rejected across %d fault classes", len(faults))
}
func TestStrictOutputCannotTrustLegacyFlags(t *testing.T) {
	good := fixtureFunds()
	bad := Transfer{Status: "completed", OnChainVerified: true, PairingVerified: true, FundsVerified: true, VerificationRuleVersion: FundsVerificationVersion}
	q := QueryResult{Transfers: []Transfer{good, bad}}
	EnforceFundsOutput("funds", &q)
	if len(q.Transfers) != 1 || len(q.PendingTransfers) != 1 || q.PendingTransfers[0].FundsVerified {
		t.Fatal(q)
	}
	q = QueryResult{Transfers: []Transfer{good, bad}}
	EnforceFundsOutput("discovery", &q)
	if len(q.Transfers) != 2 || q.Transfers[1].FundsVerified {
		t.Fatal(q)
	}
}
func TestAllConflictingAdapterRecordsAreBlocked(t *testing.T) {
	rows := []Transfer{}
	for i := 0; i < 3; i++ {
		x := fixtureFunds()
		h := "0x" + strings.Repeat(fmt.Sprint(i+1), 64)
		x.Destination[0].TxHash = h
		x.Proof.Protocol.Destination.TxHash = h
		x.Proof.Legs[1].TxHash = h
		x.Proof.Legs[1].Movements[0].TxHash = h
		rows = append(rows, x)
	}
	q := QueryResult{Transfers: rows}
	EnforceFundsOutput("funds", &q)
	if len(q.Transfers) != 0 || len(q.PendingTransfers) != 3 {
		t.Fatal(q)
	}
	for _, x := range q.PendingTransfers {
		if x.VerificationState != "conflict" {
			t.Fatal(x.BlockingReasons)
		}
	}
}
