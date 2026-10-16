package api

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	ca "github.com/blockchain-platform/blockchain-platform/internal/contractanalysis"
)

type contractCreatedFrame struct {
	frame, parent map[string]any
	path          string
	settled       bool
}

func contractCreationFrames(root map[string]any) ([]contractCreatedFrame, error) {
	frames := []contractCreatedFrame{}
	visited := 0
	var walk func(map[string]any, map[string]any, string, bool, int) error
	walk = func(frame, parent map[string]any, path string, live bool, depth int) error {
		visited++
		if visited > 2048 || depth > 48 {
			return fmt.Errorf("CREATION_TRACE_BUDGET")
		}
		if len(frame) == 0 {
			return fmt.Errorf("CREATION_TRACE_FRAME_INVALID")
		}
		live = live && (frame["error"] == nil || frame["error"] == "")
		if kind := strings.ToUpper(contractCreationText(frame["type"])); kind == "CREATE" || kind == "CREATE2" {
			frames = append(frames, contractCreatedFrame{frame, parent, path, live})
		}
		children, ok := frame["calls"].([]any)
		if frame["calls"] != nil && !ok {
			return fmt.Errorf("CREATION_TRACE_CHILDREN_INVALID")
		}
		for i, child := range children {
			if e := walk(contractCreationMap(child), frame, fmt.Sprintf("%s.%d", path, i), live, depth+1); e != nil {
				return e
			}
		}
		return nil
	}
	if e := walk(root, nil, "0", true, 0); e != nil {
		return nil, e
	}
	return frames, nil
}

func contractCreate2Address(creator, salt string, input []byte) (string, error) {
	if !ca.CompleteAddress.MatchString(creator) {
		return "", fmt.Errorf("CREATION_CREATOR_INVALID")
	}
	n, e := contractCreationInteger(salt)
	if e != nil || n.Sign() < 0 || n.BitLen() > 256 {
		return "", fmt.Errorf("CREATION_SALT_INVALID")
	}
	who, _ := hex.DecodeString(creator[2:])
	digest, _ := hex.DecodeString(contractKeccak(input)[2:])
	raw := append([]byte{0xff}, who...)
	raw = append(raw, n.FillBytes(make([]byte, 32))...)
	raw = append(raw, digest...)
	return "0x" + contractKeccak(raw)[26:], nil
}

// This is a historical identity fact for one creation frame. It does not turn
// a call tracer label into a proof or establish the latest account incarnation.
func contractFactoryCreationBinding(snapshot ca.Snapshot, model map[string]any, witness []byte, replayed ca.Result) (map[string]any, error) {
	w := rowMap(witness)
	tx, receipt, block, parent := contractCreationMap(w["raw_transaction"]), contractCreationMap(w["raw_receipt"]), contractCreationMap(w["raw_block"]), contractCreationMap(w["parent_block"])
	root, diff := contractCreationMap(w["call_trace"]), contractCreationMap(w["state_diff"])
	if snapshot.VMFamily != "" && snapshot.VMFamily != "evm" {
		return nil, fmt.Errorf("CREATION_VM_FAMILY_UNSUPPORTED")
	}
	hash, address := contractCreationText(tx["hash"]), contractCreationText(model["storage_address"])
	if !ca.CompleteHash.MatchString(hash) || !ca.CompleteAddress.MatchString(address) || !contractCreationEqual(model["code_address"], address) || !contractCreationEqual(tx["hash"], receipt["transactionHash"]) || !ca.CompleteHash.MatchString(contractCreationText(block["hash"])) || !ca.CompleteHash.MatchString(contractCreationText(parent["hash"])) || !contractCreationEqual(receipt["blockHash"], block["hash"]) || !contractCreationEqual(tx["blockHash"], block["hash"]) {
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
	if !ca.CompleteAddress.MatchString(contractCreationText(tx["from"])) || !contractCreationEqual(root["from"], tx["from"]) || !contractCreationEqual(root["input"], tx["input"]) {
		return nil, fmt.Errorf("CREATION_FACTORY_ROOT_IDENTITY_MISMATCH")
	}
	rootAddress := tx["to"]
	rootKind := "CALL"
	if contractCreationText(rootAddress) == "" {
		rootKind = "CREATE"
		rootAddress = receipt["contractAddress"]
		expected, e := contractCreationAddress(contractCreationText(tx["from"]), contractCreationText(tx["nonce"]))
		if e != nil || !contractCreationEqual(expected, rootAddress) {
			return nil, fmt.Errorf("CREATION_FACTORY_ROOT_ADDRESS_MISMATCH")
		}
	}
	if !strings.EqualFold(contractCreationText(root["type"]), rootKind) || !contractCreationEqual(root["to"], rootAddress) {
		return nil, fmt.Errorf("CREATION_FACTORY_ROOT_IDENTITY_MISMATCH")
	}
	frames, e := contractCreationFrames(root)
	if e != nil {
		return nil, e
	}
	var selected *contractCreatedFrame
	for i := range frames {
		item := &frames[i]
		if item.path != "0" && item.settled && contractCreationEqual(item.frame["to"], address) {
			if selected != nil {
				return nil, fmt.Errorf("CREATION_FRAME_IDENTITY_AMBIGUOUS")
			}
			selected = item
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("CREATION_TARGET_FRAME_UNAVAILABLE")
	}
	frame := selected.frame
	if !ca.CompleteAddress.MatchString(contractCreationText(frame["from"])) || selected.parent == nil {
		return nil, fmt.Errorf("CREATION_CREATOR_CONTEXT_UNAVAILABLE")
	}
	var proof map[string]any
	for _, raw := range replayed.Sections["replays"] {
		candidate := rowMap(raw)
		if contractCreationEqual(candidate["tx_hash"], hash) {
			if proof != nil {
				return nil, fmt.Errorf("CREATION_REPLAY_IDENTITY_AMBIGUOUS")
			}
			proof = candidate
		}
	}
	if proof["complete"] != true || proof["status"] != "matched_observations" || proof["original_call_trace_matches"] != true || proof["state_diff_complete"] != true || !contractCreationEqual(proof["block_hash"], block["hash"]) || !contractCreationSameQuantity(proof["chain_id"], snapshot.ChainID) {
		return nil, fmt.Errorf("CREATION_COMPLETE_REPLAY_REQUIRED")
	}
	observed, executed := contractCreationMap(proof["observed"]), contractCreationMap(proof["replayed"])
	if rootKind == "CREATE" {
		if !contractCreationEqual(observed["contract_address"], rootAddress) || !contractCreationEqual(executed["contract_address"], rootAddress) {
			return nil, fmt.Errorf("CREATION_FACTORY_ROOT_REPLAY_MISMATCH")
		}
	} else if contractCreationText(observed["contract_address"]) != "" || contractCreationText(executed["contract_address"]) != "" {
		return nil, fmt.Errorf("CREATION_FACTORY_ROOT_REPLAY_MISMATCH")
	}
	actualFrames, e := contractCreationFrames(contractCreationMap(proof["call_trace"]))
	if e != nil {
		return nil, e
	}
	matched := 0
	for _, actual := range actualFrames {
		if actual.path != selected.path || !actual.settled {
			continue
		}
		same := true
		for _, key := range []string{"type", "from", "to", "input", "output"} {
			same = same && contractCreationEqual(actual.frame[key], frame[key])
		}
		if same {
			matched++
		}
	}
	if matched != 1 {
		return nil, fmt.Errorf("CREATION_FRAME_REPLAY_MISMATCH")
	}
	var post map[string]any
	for key, item := range contractCreationMap(diff["post"]) {
		if strings.EqualFold(key, address) {
			post = contractCreationMap(item)
		}
	}
	runtime, e := contractCreationBytes(post["code"])
	if e != nil || len(runtime) == 0 || !strings.EqualFold(contractKeccak(runtime), contractCreationText(model["code_hash"])) || !contractCreationEqual(post["code"], frame["output"]) {
		return nil, fmt.Errorf("CREATION_RUNTIME_CODE_IDENTITY_MISMATCH")
	}
	identity := contractCreationMap(model["creation_identity"])
	initcode, e := contractCreationBytes(identity["initcode"])
	if e != nil || len(initcode) == 0 || len(initcode) > 49152 {
		return nil, fmt.Errorf("CREATION_RECOMPILED_INITCODE_UNAVAILABLE")
	}
	input, e := contractCreationBytes(frame["input"])
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
	opcode, e := contractFactoryCreationOpcode(replayed, hash, contractCreationText(block["hash"]), *selected)
	if e != nil {
		return nil, e
	}
	derived := address
	basis := "complete_offline_evm_replay_creation_nonce_transition"
	if strings.EqualFold(contractCreationText(frame["type"]), "CREATE2") {
		derived, e = contractCreate2Address(contractCreationText(frame["from"]), contractCreationText(opcode["create2_salt_raw"]), input)
		if e != nil || !contractCreationEqual(derived, address) || !contractCreationEqual(derived, opcode["derived_create2_address"]) {
			return nil, fmt.Errorf("CREATION_CREATE2_DERIVATION_MISMATCH")
		}
		basis = "create2_creator_salt_and_exact_initcode_hash"
	}
	return map[string]any{"schema_version": "contract-creation-binding-v2", "complete": true, "scope": "one_recompiled_and_replayed_factory_creation_frame", "tx_hash": hash, "block_number": bn.String(), "block_hash": block["hash"], "transaction_index": tx["transactionIndex"], "created_address": address, "creator": frame["from"], "creation_type": frame["type"], "trace_path": selected.path, "program_counter": opcode["pc"], "instruction_order": opcode["instruction_order"], "derived_create_address": derived, "address_derivation_basis": basis, "create2_salt_raw": opcode["create2_salt_raw"], "code_hash": model["code_hash"], "source_model_id": model["model_id"], "source_transition_complete": model["complete"], "compiler": identity["compiler"], "via_ir": identity["via_ir"], "constructor_arguments": arguments, "initcode_bytes": len(initcode), "initcode_sha256": ca.SHA256(initcode), "replay_id": proof["replay_id"], "opcode_call_id": opcode["opcode_call_id"], "opcode_artifact_id": opcode["artifact_id"], "creator_code_address": opcode["caller_code_address"], "creator_code_hash": opcode["caller_code_hash"], "creator_code_phase": opcode["caller_code_phase"], "later_history_and_upgrades_verified": false, "snapshot_lifecycle_continuity_verified": false}, nil
}

func contractFactoryCreationOpcode(result ca.Result, tx, block string, frame contractCreatedFrame) (map[string]any, error) {
	var selected map[string]any
	for _, raw := range result.Sections["execution_callsite_witnesses"] {
		row := rowMap(raw)
		if row["trace_path"] != frame.path || !contractCreationEqual(row["target"], frame.frame["to"]) || !contractCreationEqual(row["tx_hash"], tx) {
			continue
		}
		if selected != nil {
			return nil, fmt.Errorf("CREATION_OPCODE_IDENTITY_AMBIGUOUS")
		}
		selected = row
	}
	if selected == nil || selected["execution_opcode_identity_verified"] != true || selected["input_memory_matches"] != true || selected["bytecode_opcode_matches"] != true || selected["settled"] != true || !contractCreationEqual(selected["caller_storage_address"], frame.frame["from"]) || !contractCreationEqual(selected["block_hash"], block) || !contractCreationEqual(selected["call_type"], frame.frame["type"]) || !contractCreationEqual(selected["input"], frame.frame["input"]) || !contractCreationEqual(selected["return_data"], frame.frame["output"]) {
		return nil, fmt.Errorf("CREATION_EXACT_OPCODE_IDENTITY_REQUIRED")
	}
	last := strings.LastIndex(frame.path, ".")
	if last <= 0 || frame.parent == nil {
		return nil, fmt.Errorf("CREATION_OPCODE_PARENT_CONTEXT_MISMATCH")
	}
	parentPath := frame.path[:last]
	phase := "runtime"
	if kind := strings.ToUpper(contractCreationText(frame.parent["type"])); kind == "CREATE" || kind == "CREATE2" {
		phase = "creation"
	}
	pc, pe := contractCreationInteger(fmt.Sprint(selected["pc"]))
	order, oe := contractCreationInteger(fmt.Sprint(selected["instruction_order"]))
	if selected["parent_trace_path"] != parentPath || !contractCreationEqual(selected["caller_code_address"], frame.parent["to"]) || selected["caller_code_phase"] != phase || !ca.CompleteHash.MatchString(contractCreationText(selected["caller_code_hash"])) || contractCreationText(selected["opcode_call_id"]) == "" || pe != nil || oe != nil || pc.Sign() < 0 || !pc.IsUint64() || order.Sign() < 0 || !order.IsUint64() || order.Uint64() >= 20000 {
		return nil, fmt.Errorf("CREATION_OPCODE_PARENT_CONTEXT_MISMATCH")
	}
	artifact := contractCreationText(selected["artifact_id"])
	if artifact == "" {
		return nil, fmt.Errorf("CREATION_OPCODE_ARTIFACT_REQUIRED")
	}
	found := false
	for _, raw := range result.Sections["artifacts"] {
		item := rowMap(raw)
		if item["artifact_id"] != artifact || item["kind"] != "opcode_witness" {
			continue
		}
		if found {
			return nil, fmt.Errorf("CREATION_OPCODE_ARTIFACT_AMBIGUOUS")
		}
		text := contractCreationText(item["text"])
		if text == "" || len(text) > 16<<20 || ca.SHA256([]byte(text)) != item["sha256"] {
			return nil, fmt.Errorf("CREATION_OPCODE_ARTIFACT_HASH_MISMATCH")
		}
		body := rowMap([]byte(text))
		if body["schema_version"] != "contract-opcode-evidence-v1" || body["complete_replay"] != true || body["original_call_trace_matches"] != true || !contractCreationEqual(body["tx_hash"], tx) || !contractCreationEqual(body["block_hash"], block) {
			return nil, fmt.Errorf("CREATION_OPCODE_ARTIFACT_IDENTITY_MISMATCH")
		}
		calls, _ := contractCreationMap(body["normalized"])["calls"].([]any)
		matches := 0
		for _, value := range calls {
			call := contractCreationMap(value)
			if call["opcode_call_id"] != selected["opcode_call_id"] {
				continue
			}
			same := true
			for _, key := range []string{"opcode_call_id", "trace_path", "parent_trace_path", "pc", "instruction_order", "call_type", "caller_code_address", "caller_storage_address", "caller_code_hash", "caller_code_phase", "target", "input", "return_data", "create2_salt_raw", "derived_create2_address", "execution_opcode_identity_verified", "input_memory_matches", "bytecode_opcode_matches", "settled"} {
				same = same && fmt.Sprint(call[key]) == fmt.Sprint(selected[key])
			}
			if same {
				matches++
			}
		}
		if matches != 1 {
			return nil, fmt.Errorf("CREATION_OPCODE_ARTIFACT_RECORD_MISMATCH")
		}
		found = true
	}
	if !found {
		return nil, fmt.Errorf("CREATION_OPCODE_ARTIFACT_REQUIRED")
	}
	return selected, nil
}
