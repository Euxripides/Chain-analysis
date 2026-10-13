package api

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/crosschain"
	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
)

// No confirmation-count substitute is used for a provider that cannot read
// finalized. The existing gateway keeps request quotas, retries and audit IDs.
func (b *bridgeReadSession) finalizedRecord(r *bridgeRecord) (crosschain.BlockProof, error) {
	if r.Finality != nil {
		return *r.Finality, nil
	}
	p := crosschain.BlockProof{Chain: r.Chain, Number: r.BlockNumber, Hash: r.BlockHash, Policy: "rpc_finalized_and_fresh_canonical_block", Source: "configured_rpc_gateway:" + b.traceID, Corroboration: "not_available_gateway_does_not_select_independent_provider"}
	if crosschain.EVMNetworkIDs[r.Chain] == "" || b.s.deps.ChainRead == nil || b.s.deps.RPCGateway == nil {
		return p, fmt.Errorf("chain_finality_reader_unsupported")
	}
	if err := b.budget(); err != nil {
		return p, fmt.Errorf("finality_read_budget_exhausted")
	}
	response, err := b.s.deps.RPCGateway.Call(b.ctx, rpc.Request{Chain: bridgeRPCChain(r.Chain), Method: "eth_chainId", Params: []any{}, Class: rpc.ClassNetwork, Priority: rpc.PriorityInteractive, Consistency: rpc.ConsistencyForceRefresh, Budget: rpc.BudgetContext{Purpose: rpc.PurposeReceipt}, TraceID: b.traceID})
	var raw string
	if err != nil || json.Unmarshal(response.Result, &raw) != nil {
		return p, fmt.Errorf("network_identity_unavailable")
	}
	network, ok := new(big.Int).SetString(strings.TrimPrefix(raw, "0x"), 16)
	if !ok || !strings.HasPrefix(raw, "0x") || network.String() != crosschain.EVMNetworkIDs[r.Chain] {
		return p, fmt.Errorf("network_identity_conflict")
	}
	p.NetworkID = network.String()
	opt := b.options()
	opt.Consistency = rpc.ConsistencyForceRefresh
	if b.budget() != nil {
		return p, fmt.Errorf("finality_read_budget_exhausted")
	}
	head, err := b.s.deps.ChainRead.Block(b.ctx, bridgeRPCChain(r.Chain), chain.Finalized(), false, opt)
	if err != nil || head == nil || head.Hash == "" {
		return p, fmt.Errorf("finalized_block_unavailable")
	}
	p.FinalizedNumber, p.FinalizedHash = head.Number, head.Hash
	if head.Number < r.BlockNumber {
		return p, fmt.Errorf("transaction_not_finalized")
	}
	if b.budget() != nil {
		return p, fmt.Errorf("canonical_block_read_budget_exhausted")
	}
	actual, err := b.s.deps.ChainRead.Block(b.ctx, bridgeRPCChain(r.Chain), chain.BlockRef{Number: &r.BlockNumber}, false, opt)
	if err != nil || actual == nil || actual.Number != r.BlockNumber || !strings.EqualFold(actual.Hash, r.BlockHash) {
		return p, fmt.Errorf("canonical_block_identity_conflict")
	}
	p.Canonical, p.Finalized, p.CheckedAt = true, true, time.Now().UTC()
	r.Finality = &p
	return p, nil
}

const bridgeImplementationSlot = "0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc"
const bridgeBeaconSlot = "0xa3f0ad74e5423aebfd80d3ef4346578335a9a72aeaee59ff6cb3582b35133d50"

func (b *bridgeReadSession) historicalDeployment(protocol string, r *bridgeRecord, emitter, verifier string) (crosschain.ContractProof, error) {
	key := strings.Join([]string{protocol, r.Chain, r.BlockHash, emitter, verifier}, "|")
	if b.deployments == nil {
		b.deployments = map[string]bridgeDeploymentResult{}
	}
	if cached, ok := b.deployments[key]; ok {
		return cached.Proof, cached.Err
	}
	p, err := b.readHistoricalDeployment(protocol, r, emitter, verifier)
	b.deployments[key] = bridgeDeploymentResult{p, err}
	return p, err
}

func (b *bridgeReadSession) readHistoricalDeployment(protocol string, r *bridgeRecord, emitter, verifier string) (crosschain.ContractProof, error) {
	p := crosschain.ContractProof{Address: emitter, VerifierAddress: verifier}
	d, ok := crosschain.FindDeployment(protocol, r.Chain, verifier)
	if !ok || r.BlockNumber < d.FromBlock {
		return p, fmt.Errorf("official_historical_deployment_unavailable")
	}
	p.DeploymentSource = d.Source
	a, err := chain.ParseAddress(verifier)
	if err != nil || b.s.deps.ChainRead == nil {
		return p, fmt.Errorf("historical_contract_reader_unavailable")
	}
	ref := chain.BlockRef{Number: &r.BlockNumber}
	if b.budget() != nil {
		return p, fmt.Errorf("historical_contract_budget_exhausted")
	}
	code, err := b.s.deps.ChainRead.Code(b.ctx, bridgeRPCChain(r.Chain), a, ref, b.options())
	if err != nil || len(code) == 0 {
		return p, fmt.Errorf("historical_contract_code_unavailable")
	}
	p.CodeSHA256 = crosschain.ProofSHA(code)
	if b.budget() != nil {
		return p, fmt.Errorf("historical_proxy_budget_exhausted")
	}
	slot, err := b.s.deps.ChainRead.StorageAt(b.ctx, bridgeRPCChain(r.Chain), a, bridgeImplementationSlot, ref, b.options())
	if err != nil || len(slot) != 32 {
		return p, fmt.Errorf("historical_proxy_state_unavailable")
	}
	if new(big.Int).SetBytes(slot).Sign() != 0 {
		if new(big.Int).SetBytes(slot[:12]).Sign() != 0 {
			return p, fmt.Errorf("historical_proxy_state_invalid")
		}
		p.Implementation = "0x" + hex.EncodeToString(slot[12:])
		impl, _ := chain.ParseAddress(p.Implementation)
		if b.budget() != nil {
			return p, fmt.Errorf("historical_proxy_budget_exhausted")
		}
		code, err = b.s.deps.ChainRead.Code(b.ctx, bridgeRPCChain(r.Chain), impl, ref, b.options())
		if err != nil || len(code) == 0 {
			return p, fmt.Errorf("historical_implementation_code_unavailable")
		}
		p.ImplementationCodeSHA256 = crosschain.ProofSHA(code)
	} else {
		if b.budget() != nil {
			return p, fmt.Errorf("historical_proxy_budget_exhausted")
		}
		beacon, err := b.s.deps.ChainRead.StorageAt(b.ctx, bridgeRPCChain(r.Chain), a, bridgeBeaconSlot, ref, b.options())
		if err != nil || len(beacon) != 32 {
			return p, fmt.Errorf("historical_beacon_state_unavailable")
		}
		if new(big.Int).SetBytes(beacon).Sign() != 0 {
			return p, fmt.Errorf("historical_beacon_proxy_unsupported")
		}
	}
	p.HistoricalState = true
	return p, nil
}
