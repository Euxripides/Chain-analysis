package contractanalysis

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"path"
	"regexp"
	"strings"
)

// SubmittedCode is caller material, never a provider verification document.
type SubmittedCode struct {
	Kind        string               `json:"kind"`
	Source      *SubmittedSource     `json:"source,omitempty"`
	Bytecode    string               `json:"bytecode,omitempty"`
	CodePhase   string               `json:"code_phase,omitempty"`
	Environment *DeclaredEnvironment `json:"environment,omitempty"`
}
type SubmittedSource struct {
	Language        string            `json:"language"`
	Files           map[string]string `json:"files"`
	CompilerVersion string            `json:"compiler_version"`
	Settings        json.RawMessage   `json:"settings,omitempty"`
	Target          string            `json:"target"`
}
type DeclaredEnvironment struct {
	ChainID          string `json:"chain_id,omitempty"`
	ExecutionAddress string `json:"execution_address,omitempty"`
	EVMRules         string `json:"evm_rules,omitempty"`
}

func (q Request) CodeOnly() bool { return q.Input != nil && q.Chain == "" && q.Address == "" }

func (i *SubmittedCode) JSON() ([]byte, error) {
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(i); err != nil {
		return nil, err
	}
	raw := bytes.TrimSuffix(output.Bytes(), []byte("\n"))
	if len(raw) > 2<<20 {
		return nil, fmt.Errorf("submitted input exceeds 2 MiB")
	}
	return raw, nil
}

func (i *SubmittedCode) Canonicalize() error {
	i.Bytecode = strings.ToLower(i.Bytecode)
	if i.Environment != nil {
		i.Environment.ExecutionAddress = strings.ToLower(i.Environment.ExecutionAddress)
	}
	if i.Source != nil && len(i.Source.Settings) > 0 {
		var settings any
		d := json.NewDecoder(bytes.NewReader(i.Source.Settings))
		d.UseNumber()
		if err := d.Decode(&settings); err != nil {
			return err
		}
		i.Source.Settings = Row(settings)
	}
	return nil
}

func (q Request) ValidateCodeInput() error {
	i := q.Input
	if i == nil {
		return nil
	}
	if q.Program != nil {
		return fmt.Errorf("program and input are mutually exclusive")
	}
	if (q.Chain == "") != (q.Address == "") {
		return fmt.Errorf("deployment binding requires chain and address together")
	}
	if q.CodeOnly() && (len(q.Block) > 0 || q.Timestamp != "" || q.FromBlock != nil || q.ToBlock != nil || len(q.TxHashes) > 0 || q.TxHash != "") {
		return fmt.Errorf("block, history and transactions require a deployment binding")
	}
	if i.Kind != "source" && i.Kind != "bytecode" {
		return fmt.Errorf("input.kind must be source or bytecode")
	}
	if i.Kind == "source" && i.Source == nil || i.Kind == "bytecode" && i.Source != nil {
		return fmt.Errorf("source is required only for source input")
	}
	if i.Kind == "bytecode" && i.Bytecode == "" {
		return fmt.Errorf("complete bytecode required")
	}
	if i.Bytecode != "" {
		if len(i.Bytecode) < 4 || len(i.Bytecode) > 2+49152*2 || len(i.Bytecode)%2 != 0 || !regexp.MustCompile(`^0x[0-9a-fA-F]+$`).MatchString(i.Bytecode) {
			return fmt.Errorf("bytecode must contain 1-49152 complete bytes")
		}
		if i.CodePhase != "runtime" && i.CodePhase != "creation" {
			return fmt.Errorf("bytecode code_phase must be runtime or creation")
		}
		if !q.CodeOnly() && i.CodePhase != "runtime" {
			return fmt.Errorf("creation code cannot be equated with deployed runtime code")
		}
	} else if i.CodePhase != "" && i.CodePhase != "runtime" {
		return fmt.Errorf("source compilation produces a runtime template")
	}
	if s := i.Source; s != nil {
		if s.Language != "Solidity" {
			return fmt.Errorf("submitted source supports Solidity; deployment-source Vyper support is unchanged")
		}
		if !regexp.MustCompile(`^0\.[0-9]+\.[0-9]+\+commit\.[0-9a-f]{8}$`).MatchString(s.CompilerVersion) {
			return fmt.Errorf("exact Solidity compiler version and commit required")
		}
		if len(s.Files) < 1 || len(s.Files) > 256 {
			return fmt.Errorf("source requires 1-256 named files")
		}
		total := 0
		for name, content := range s.Files {
			logical := name
			for strings.HasPrefix(logical, "./") {
				logical = strings.TrimPrefix(logical, "./")
			}
			if logical == "" || len(name) > 256 || path.Clean(logical) != logical || path.IsAbs(logical) || logical == ".." || strings.HasPrefix(logical, "../") || strings.ContainsAny(name, "\\:\x00\r\n\t") {
				return fmt.Errorf("source filenames must be relative canonical paths")
			}
			total += len(name) + len(content)
		}
		if total > 1536<<10 {
			return fmt.Errorf("source files exceed 1536 KiB")
		}
		parts := strings.Split(s.Target, ":")
		if len(parts) != 2 || !regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`).MatchString(parts[1]) {
			return fmt.Errorf("source target must be file:Contract")
		}
		if _, ok := s.Files[parts[0]]; !ok {
			return fmt.Errorf("target source file is missing")
		}
		if len(s.Settings) > 64<<10 {
			return fmt.Errorf("compiler settings exceed 64 KiB")
		}
		if len(s.Settings) > 0 {
			var settings map[string]json.RawMessage
			if err := json.Unmarshal(s.Settings, &settings); err != nil || settings == nil {
				return fmt.Errorf("compiler settings must be an object")
			}
			allowed := map[string]bool{"optimizer": true, "viaIR": true, "evmVersion": true, "libraries": true, "remappings": true, "metadata": true, "debug": true}
			for key := range settings {
				if !allowed[key] {
					return fmt.Errorf("unsupported submitted compiler setting: %s", key)
				}
			}
		}
	}
	if env := i.Environment; env != nil {
		if env.ChainID != "" {
			n, ok := new(big.Int).SetString(env.ChainID, 10)
			if !ok || n.Sign() <= 0 || n.BitLen() > 256 || n.String() != env.ChainID {
				return fmt.Errorf("declared chain_id must be a canonical positive uint256 string")
			}
		}
		if env.ExecutionAddress != "" && !CompleteAddress.MatchString(env.ExecutionAddress) {
			return fmt.Errorf("complete declared execution address required")
		}
		if env.EVMRules != "" && env.EVMRules != "shanghai" && env.EVMRules != "cancun" && env.EVMRules != "prague" {
			return fmt.Errorf("unsupported declared EVM rules")
		}
	}
	return nil
}

// A code-only result has no observed deployment. Keep the legacy numeric fields
// internally for old callers while omitting them entirely from code-only JSON.
func (s Snapshot) MarshalJSON() ([]byte, error) {
	type plain Snapshot
	raw, err := json.Marshal(plain(s))
	if err != nil || s.IdentityKind != "code_only" {
		return raw, err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	for _, key := range []string{"chain", "chain_id", "block_number", "block_hash", "block_time", "entry_address", "code_address", "storage_address", "fetched_at", "proxy"} {
		delete(fields, key)
	}
	return json.Marshal(fields)
}

// Validate the identity boundary after the isolated compiler has normalized a
// submitted source. Source hashes and engine digests cannot be replaced by it.
func ValidateInputResult(in Input, out Snapshot) error {
	if in.SubmittedInputJSON == "" {
		return nil
	}
	if SHA256([]byte(in.SubmittedInputJSON)) != in.Snapshot.InputSHA256 || out.InputSHA256 != in.Snapshot.InputSHA256 || out.InputKind != in.Snapshot.InputKind || out.IdentityKind != in.Snapshot.IdentityKind || out.EngineDigest != in.Snapshot.EngineDigest {
		return fmt.Errorf("engine submitted-input identity mismatch")
	}
	if !bytes.Equal(Row(in.Snapshot.DeclaredEnvironment), Row(out.DeclaredEnvironment)) {
		return fmt.Errorf("engine declared environment identity mismatch")
	}
	if out.IdentityKind == "code_only" && (out.Chain != "" || out.ChainID != "" || out.BlockNumber != 0 || out.BlockHash != "" || !out.BlockTime.IsZero() || out.EntryAddress != "" || out.CodeAddress != "" || out.StorageAddress != "" || !out.FetchedAt.IsZero() || len(out.Proxy) > 0) {
		return fmt.Errorf("code-only result contains an observed deployment")
	}
	return nil
}
