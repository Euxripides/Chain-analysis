package api

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
)

func contractCreationMap(value any) map[string]any { m, _ := value.(map[string]any); return m }
func contractCreationText(value any) string        { s, _ := value.(string); return s }
func contractCreationEqual(a, b any) bool {
	x, y := contractCreationText(a), contractCreationText(b)
	return x != "" && y != "" && strings.EqualFold(x, y)
}
func contractCreationBytes(value any) ([]byte, error) {
	s := contractCreationText(value)
	if !strings.HasPrefix(s, "0x") || len(s) > 2+2*(1<<20) {
		return nil, fmt.Errorf("CREATION_BYTES_INVALID")
	}
	return hex.DecodeString(s[2:])
}
func contractCreationInteger(value any) (*big.Int, error) {
	s := contractCreationText(value)
	if s == "" {
		return nil, fmt.Errorf("CREATION_QUANTITY_MISSING")
	}
	text, e := ca.RawInteger(s)
	if e != nil {
		return nil, e
	}
	n, _ := new(big.Int).SetString(text, 10)
	return n, nil
}
func contractCreationSameQuantity(a, b any) bool {
	x, e := contractCreationInteger(a)
	y, f := contractCreationInteger(b)
	return e == nil && f == nil && x.Cmp(y) == 0
}

// CREATE uses keccak256(RLP([sender, nonce]))[12:]. The sender is exactly
// twenty bytes and a transaction nonce fits uint64, so the RLP list is short.
func contractCreationAddress(from, nonce string) (string, error) {
	if !ca.CompleteAddress.MatchString(from) {
		return "", fmt.Errorf("CREATION_SENDER_INVALID")
	}
	n, e := contractCreationInteger(nonce)
	if e != nil || !n.IsUint64() {
		return "", fmt.Errorf("CREATION_NONCE_INVALID")
	}
	sender, _ := hex.DecodeString(from[2:])
	encoded := []byte{0x80}
	if n.Sign() > 0 {
		encoded = n.Bytes()
		if n.Cmp(big.NewInt(128)) >= 0 {
			encoded = append([]byte{byte(0x80 + len(encoded))}, encoded...)
		}
	}
	payload := append([]byte{0x94}, sender...)
	payload = append(payload, encoded...)
	encoded = append([]byte{byte(0xc0 + len(payload))}, payload...)
	return "0x" + contractKeccak(encoded)[26:], nil
}
func contractCreationArguments(raw []byte, parameters []any) ([]map[string]any, error) {
	if len(parameters) > 32 || len(raw) != len(parameters)*32 {
		return nil, fmt.Errorf("CREATION_ARGUMENT_LENGTH_MISMATCH")
	}
	rows := []map[string]any{}
	for i, p := range parameters {
		parameter := contractCreationMap(p)
		typ := contractCreationText(parameter["type"])
		word := raw[i*32 : (i+1)*32]
		value := ""
		if strings.HasPrefix(typ, "bytes") {
			width, e := strconv.Atoi(strings.TrimPrefix(typ, "bytes"))
			if e != nil || width < 1 || width > 32 || !bytes.Equal(word[width:], make([]byte, 32-width)) {
				return nil, fmt.Errorf("CREATION_NONCANONICAL_BYTES_ARGUMENT")
			}
			value = "0x" + hex.EncodeToString(word[:width])
		} else {
			decoded, e := ca.DecodePacked(word, 0, typ)
			if e != nil {
				return nil, fmt.Errorf("CREATION_STATIC_ARGUMENT_UNSUPPORTED:%s", typ)
			}
			value = decoded
			var canonical *big.Int
			switch {
			case typ == "bool":
				canonical = big.NewInt(0)
				if decoded == "true" {
					canonical.SetInt64(1)
				}
			case strings.HasPrefix(typ, "address") || strings.HasPrefix(typ, "contract "):
				canonical, _ = new(big.Int).SetString(decoded[2:], 16)
			default:
				canonical, _ = new(big.Int).SetString(decoded, 10)
				if canonical != nil && canonical.Sign() < 0 {
					canonical.Add(canonical, new(big.Int).Lsh(big.NewInt(1), 256))
				}
			}
			if canonical == nil || canonical.Cmp(new(big.Int).SetBytes(word)) != 0 {
				return nil, fmt.Errorf("CREATION_NONCANONICAL_TYPED_ARGUMENT")
			}
		}
		rows = append(rows, map[string]any{"source_ast_id": parameter["source_ast_id"], "name": parameter["name"], "type": typ, "value": value, "abi_word": "0x" + hex.EncodeToString(word)})
	}
	return rows, nil
}

// This consumes a replay produced by the isolated runner in this job. It does
// not accept a provider match label or an externally supplied success flag as
// deployment identity, and does not establish later upgrade/history coverage.
func contractCreationBinding(snapshot ca.Snapshot, model map[string]any, witness []byte, replayed ca.Result) (map[string]any, error) {
	w := rowMap(witness)
	tx, receipt, block, parent := contractCreationMap(w["raw_transaction"]), contractCreationMap(w["raw_receipt"]), contractCreationMap(w["raw_block"]), contractCreationMap(w["parent_block"])
	trace, diff := contractCreationMap(w["call_trace"]), contractCreationMap(w["state_diff"])
	if snapshot.VMFamily != "" && snapshot.VMFamily != "evm" {
		return nil, fmt.Errorf("CREATION_VM_FAMILY_UNSUPPORTED")
	}
	if tx["to"] != nil && contractCreationText(tx["to"]) != "" || !contractCreationEqual(model["storage_address"], receipt["contractAddress"]) {
		return contractFactoryCreationBinding(snapshot, model, witness, replayed)
	}
	txHash, address := contractCreationText(tx["hash"]), contractCreationText(receipt["contractAddress"])
	if !ca.CompleteHash.MatchString(txHash) || !ca.CompleteAddress.MatchString(address) || !ca.CompleteHash.MatchString(contractCreationText(block["hash"])) || !ca.CompleteHash.MatchString(contractCreationText(parent["hash"])) || !contractCreationEqual(tx["hash"], receipt["transactionHash"]) || !contractCreationEqual(receipt["blockHash"], block["hash"]) || !contractCreationEqual(tx["blockHash"], block["hash"]) {
		return nil, fmt.Errorf("CREATION_TRANSACTION_IDENTITY_MISMATCH")
	}
	bn, e := contractCreationInteger(block["number"])
	pn, f := contractCreationInteger(parent["number"])
	if e != nil || f != nil || !bn.IsUint64() || bn.Uint64() > snapshot.BlockNumber || new(big.Int).Sub(bn, pn).Cmp(big.NewInt(1)) != 0 || !contractCreationEqual(block["parentHash"], parent["hash"]) || !contractCreationSameQuantity(tx["blockNumber"], block["number"]) || !contractCreationSameQuantity(receipt["blockNumber"], block["number"]) || !contractCreationSameQuantity(tx["transactionIndex"], receipt["transactionIndex"]) {
		return nil, fmt.Errorf("CREATION_BLOCK_IDENTITY_MISMATCH")
	}
	status, e := contractCreationInteger(receipt["status"])
	if e != nil || status.Cmp(big.NewInt(1)) != 0 {
		return nil, fmt.Errorf("CREATION_TRANSACTION_NOT_SUCCESSFUL")
	}
	if tx["chainId"] != nil && !contractCreationSameQuantity(tx["chainId"], snapshot.ChainID) {
		return nil, fmt.Errorf("CREATION_CHAIN_ID_MISMATCH")
	}
	expected, e := contractCreationAddress(contractCreationText(tx["from"]), contractCreationText(tx["nonce"]))
	if e != nil || !strings.EqualFold(expected, address) {
		return nil, fmt.Errorf("CREATION_DERIVED_ADDRESS_MISMATCH")
	}
	if !contractCreationEqual(model["code_address"], address) || !contractCreationEqual(model["storage_address"], address) {
		return nil, fmt.Errorf("CREATION_CODE_STORAGE_CONTEXT_MISMATCH")
	}
	if !strings.EqualFold(contractCreationText(trace["type"]), "CREATE") || trace["error"] != nil && trace["error"] != "" || !contractCreationEqual(trace["from"], tx["from"]) || !contractCreationEqual(trace["to"], address) || !contractCreationEqual(trace["input"], tx["input"]) {
		return nil, fmt.Errorf("CREATION_CALL_TRACE_IDENTITY_MISMATCH")
	}
	post := contractCreationMap(diff["post"])
	var deployed map[string]any
	for a, v := range post {
		if strings.EqualFold(a, address) {
			deployed = contractCreationMap(v)
		}
	}
	runtime, e := contractCreationBytes(deployed["code"])
	if e != nil || len(runtime) == 0 || !strings.EqualFold(contractKeccak(runtime), contractCreationText(model["code_hash"])) || !contractCreationEqual(deployed["code"], trace["output"]) {
		return nil, fmt.Errorf("CREATION_RUNTIME_CODE_IDENTITY_MISMATCH")
	}
	identity := contractCreationMap(model["creation_identity"])
	initcode, e := contractCreationBytes(identity["initcode"])
	if e != nil || len(initcode) == 0 || len(initcode) > 49152 {
		return nil, fmt.Errorf("CREATION_RECOMPILED_INITCODE_UNAVAILABLE")
	}
	input, e := contractCreationBytes(tx["input"])
	if e != nil || !bytes.HasPrefix(input, initcode) {
		return nil, fmt.Errorf("CREATION_INITCODE_PREFIX_MISMATCH")
	}
	parameters, ok := identity["constructor_parameters"].([]any)
	if !ok {
		return nil, fmt.Errorf("CREATION_CONSTRUCTOR_SCHEMA_UNAVAILABLE")
	}
	arguments, e := contractCreationArguments(input[len(initcode):], parameters)
	if e != nil {
		return nil, e
	}
	var proof map[string]any
	for _, raw := range replayed.Sections["replays"] {
		candidate := rowMap(raw)
		if contractCreationEqual(candidate["tx_hash"], txHash) {
			if proof != nil {
				return nil, fmt.Errorf("CREATION_REPLAY_IDENTITY_AMBIGUOUS")
			}
			proof = candidate
		}
	}
	observed, executed := contractCreationMap(proof["observed"]), contractCreationMap(proof["replayed"])
	if proof["complete"] != true || proof["status"] != "matched_observations" || proof["original_call_trace_matches"] != true || proof["state_diff_complete"] != true || !contractCreationEqual(proof["block_hash"], block["hash"]) || !contractCreationSameQuantity(proof["chain_id"], snapshot.ChainID) || !contractCreationEqual(observed["contract_address"], address) || !contractCreationEqual(executed["contract_address"], address) {
		return nil, fmt.Errorf("CREATION_COMPLETE_REPLAY_REQUIRED")
	}
	return map[string]any{"schema_version": "contract-creation-binding-v1", "complete": true, "scope": "one_recompiled_and_replayed_direct_creation", "tx_hash": txHash, "block_number": bn.String(), "block_hash": block["hash"], "transaction_index": tx["transactionIndex"], "created_address": address, "sender": tx["from"], "nonce": tx["nonce"], "derived_create_address": expected, "code_hash": model["code_hash"], "source_model_id": model["model_id"], "source_transition_complete": model["complete"], "compiler": identity["compiler"], "via_ir": identity["via_ir"], "constructor_arguments": arguments, "initcode_bytes": len(initcode), "initcode_sha256": ca.SHA256(initcode), "replay_id": proof["replay_id"], "later_history_and_upgrades_verified": false}, nil
}

func (s *Server) contractBindCreation(ctx context.Context, result *ca.Result, witness []byte, replayed ca.Result, witnessArtifact string) error {
	w := rowMap(witness)
	frames, e := contractCreationFrames(contractCreationMap(w["call_trace"]))
	if e != nil {
		result.Sections["coverage"] = append(result.Sections["coverage"], ca.Row(map[string]any{"dimension": "historical_creation_identity", "complete": false, "reason": e.Error()}))
		return nil
	}
	targets := map[string]bool{}
	for _, frame := range frames {
		if frame.settled {
			targets[strings.ToLower(contractCreationText(frame.frame["to"]))] = true
		}
	}
	for i, raw := range result.Sections["initial_state_models"] {
		model := rowMap(raw)
		if !targets[strings.ToLower(contractCreationText(model["storage_address"]))] {
			continue
		}
		binding, e := contractCreationBinding(result.Snapshot, model, witness, replayed)
		if e != nil {
			if model["deployment_identity_verified"] != true {
				model["deployment_identity_status"] = e.Error()
			}
			result.Sections["initial_state_models"][i] = ca.Row(model)
			continue
		}
		artifactID := ca.ID(contractCreationText(binding["tx_hash"]), contractCreationText(model["model_id"]), result.Snapshot.BlockHash, "creation-binding")
		binding["witness_artifact_id"], binding["proof_artifact_id"] = witnessArtifact, artifactID
		content := ca.Row(binding)
		object, e := s.contractSaveObject(ctx, "artifact", content, "application/json")
		if e != nil {
			return e
		}
		result.Sections["artifacts"] = append(result.Sections["artifacts"], ca.Row(ca.Artifact{ArtifactID: artifactID, Kind: "creation_binding_proof", Filename: fmt.Sprint(binding["tx_hash"]) + "-creation-binding.json", MediaType: "application/json", SHA256: object.SHA256, Bytes: object.Bytes, ObjectKey: object.Key}))
		contractApplyCreationBinding(result, i, binding, artifactID)
		result.Sections["coverage"] = append(result.Sections["coverage"], ca.Row(map[string]any{"dimension": "historical_creation_identity", "complete": true, "source_model_id": model["model_id"], "tx_hash": binding["tx_hash"], "code_hash": model["code_hash"], "proof_artifact_id": artifactID}))
	}
	return nil
}

func contractApplyCreationBinding(result *ca.Result, index int, binding map[string]any, artifactID string) {
	model := rowMap(result.Sections["initial_state_models"][index])
	status := "recompiled_and_replayed_direct_creation"
	if binding["scope"] == "one_recompiled_and_replayed_factory_creation_frame" {
		status = "recompiled_and_replayed_factory_creation_frame"
	}
	model["deployment_identity_verified"], model["deployment_identity_status"], model["deployment_binding"] = true, status, binding
	model["snapshot_lifecycle_continuity_verified"] = false
	result.Sections["initial_state_models"][index] = ca.Row(model)
	for i, row := range result.Sections["protocol_invariants"] {
		initial := rowMap(row)
		if initial["kind"] != "erc20_source_initialization" || initial["source_transition_model_id"] != model["model_id"] || !contractCreationEqual(initial["code_hash"], model["code_hash"]) || !contractCreationEqual(initial["storage_address"], model["storage_address"]) {
			continue
		}
		initial["deployment_identity_verified"], initial["deployment_binding_artifact_id"] = true, artifactID
		initial["assumption_discharges"] = []any{map[string]any{"assumption": "historical_deployment_identity_not_verified", "proof_artifact_id": artifactID}}
		result.Sections["protocol_invariants"][i] = ca.Row(initial)
		if initial["complete"] != true {
			continue
		}
		for j, other := range result.Sections["protocol_invariants"] {
			invariant := rowMap(other)
			if invariant["source_initialization_model_id"] != initial["model_id"] {
				continue
			}
			invariant["initial_invariant_basis"], invariant["deployment_binding_artifact_id"] = "proved_source_creation_with_replayed_deployment_identity", artifactID
			result.Sections["protocol_invariants"][j] = ca.Row(invariant)
		}
	}
}
