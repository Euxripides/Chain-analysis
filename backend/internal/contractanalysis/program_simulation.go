package contractanalysis

import (
	"encoding/base64"
	"fmt"
	"github.com/blockchain-platform/blockchain-platform/internal/solana"
	"strconv"
)

type ProgramSimulationAccount struct {
	Address    string `json:"address"`
	Lamports   string `json:"lamports_raw"`
	Data       string `json:"data_base64"`
	Owner      string `json:"owner"`
	Executable bool   `json:"executable"`
	RentEpoch  string `json:"rent_epoch_raw"`
}
type ProgramSimulationMeta struct {
	Address  string `json:"address"`
	Signer   *bool  `json:"signer"`
	Writable *bool  `json:"writable"`
}
type ProgramSimulationInstruction struct {
	ProgramID string                  `json:"program_id"`
	Data      string                  `json:"data_base64"`
	Accounts  []ProgramSimulationMeta `json:"accounts"`
}
type ProgramSimulationDependency struct {
	ProgramID string `json:"program_id"`
	Loader    string `json:"loader"`
	ELF       string `json:"elf_base64"`
}
type ProgramSimulation struct {
	SchemaVersion string                         `json:"schema_version"`
	Environment   string                         `json:"environment"`
	Accounts      []ProgramSimulationAccount     `json:"accounts"`
	Instructions  []ProgramSimulationInstruction `json:"instructions"`
	Dependencies  []ProgramSimulationDependency  `json:"dependencies,omitempty"`
}

func validProgramLoader(value string) bool {
	return value == "BPFLoaderUpgradeab1e11111111111111111111111" || value == "BPFLoader1111111111111111111111111111111111" || value == "BPFLoader2111111111111111111111111111111111"
}
func (s *ProgramSimulation) Validate(primary string) error {
	if s.SchemaVersion != "solana-local-simulation-v1" || s.Environment != "pinned_mollusk_defaults" {
		return fmt.Errorf("explicit local simulation schema and environment required")
	}
	if len(s.Accounts) > 128 || len(s.Instructions) < 1 || len(s.Instructions) > 32 || len(s.Dependencies) > 16 {
		return fmt.Errorf("local simulation record budget exceeded")
	}
	identities := map[string]bool{}
	checkAddress := func(value string) error { _, err := solana.NormalizeSolanaAddress(value); return err }
	for i := range s.Accounts {
		a := &s.Accounts[i]
		if checkAddress(a.Address) != nil || checkAddress(a.Owner) != nil || identities[a.Address] {
			return fmt.Errorf("invalid or duplicate simulation account")
		}
		identities[a.Address] = true
		if a.RentEpoch == "" {
			a.RentEpoch = "0"
		}
		for _, value := range []string{a.Lamports, a.RentEpoch} {
			if value == "" {
				return fmt.Errorf("uint64 account amount required")
			}
			for _, c := range value {
				if c < '0' || c > '9' {
					return fmt.Errorf("simulation amounts must be unsigned integer strings")
				}
			}
			if _, err := strconv.ParseUint(value, 10, 64); err != nil {
				return fmt.Errorf("simulation amount exceeds uint64")
			}
		}
		data, err := base64.StdEncoding.DecodeString(a.Data)
		if err != nil || len(data) > 1<<20 {
			return fmt.Errorf("simulation account data unavailable or over budget")
		}
	}
	programs := map[string]bool{primary: true, "11111111111111111111111111111111": true}
	for _, p := range s.Dependencies {
		if checkAddress(p.ProgramID) != nil || programs[p.ProgramID] || !validProgramLoader(p.Loader) {
			return fmt.Errorf("invalid simulation dependency identity")
		}
		programs[p.ProgramID] = true
		data, err := base64.StdEncoding.DecodeString(p.ELF)
		if err != nil || len(data) > 4<<20 || len(data) < 4 || string(data[:4]) != "\x7fELF" {
			return fmt.Errorf("simulation dependency ELF invalid")
		}
	}
	for _, instruction := range s.Instructions {
		if !programs[instruction.ProgramID] || len(instruction.Accounts) > 128 {
			return fmt.Errorf("simulation instruction program or account budget invalid")
		}
		data, err := base64.StdEncoding.DecodeString(instruction.Data)
		if err != nil || len(data) > 4096 {
			return fmt.Errorf("simulation instruction data invalid")
		}
		for _, meta := range instruction.Accounts {
			if checkAddress(meta.Address) != nil || meta.Signer == nil || meta.Writable == nil {
				return fmt.Errorf("explicit simulation account permissions required")
			}
		}
	}
	return nil
}
