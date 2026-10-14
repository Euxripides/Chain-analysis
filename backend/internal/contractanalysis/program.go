package contractanalysis

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/solana"
	"github.com/blockchain-platform/blockchain-platform/internal/tron"
)

const ProgramRequestVersion = "contract-program-request-v1"

// ProgramRequest is independent of the EVM address/hash and engine schema.
// The stored Request envelope retains the shared job, scope and budget protocol.
type ProgramRequest struct {
	Simulation     *ProgramSimulation `json:"simulation,omitempty"`
	SchemaVersion  string             `json:"schema_version"`
	VMFamily       string             `json:"vm_family"`
	Address        string             `json:"address"`
	TxHashes       []string           `json:"tx_hashes,omitempty"`
	Budget         Budget             `json:"budget"`
	IdempotencyKey string             `json:"idempotency_key,omitempty"`
}

func DecodeProgram(raw []byte) (ProgramRequest, error) {
	var p ProgramRequest
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	err := d.Decode(&p)
	if err == nil && d.Decode(new(any)) != io.EOF {
		err = fmt.Errorf("expected one program request")
	}
	return p, err
}

func (p *ProgramRequest) Normalize() error {
	if p.SchemaVersion == "" {
		p.SchemaVersion = ProgramRequestVersion
	}
	if p.SchemaVersion != ProgramRequestVersion {
		return fmt.Errorf("unsupported program request version")
	}
	switch p.VMFamily {
	case "tron_tvm":
		a, err := tron.NormalizeTronAddress(p.Address)
		if err != nil {
			return err
		}
		p.Address = a
		for i, h := range p.TxHashes {
			if !CompleteHash.MatchString("0x" + strings.TrimPrefix(h, "0x")) {
				return fmt.Errorf("complete TRON transaction hash required")
			}
			p.TxHashes[i] = strings.ToLower(strings.TrimPrefix(h, "0x"))
		}
	case "solana_sbf":
		a, err := solana.NormalizeSolanaAddress(p.Address)
		if err != nil {
			return err
		}
		p.Address = a
		for _, h := range p.TxHashes {
			b, err := solana.DecodeBase58(h)
			if err != nil || len(b) != 64 {
				return fmt.Errorf("complete case-sensitive Solana signature required")
			}
		}
	default:
		return fmt.Errorf("unsupported program execution family")
	}
	if len(p.IdempotencyKey) > 128 {
		return fmt.Errorf("idempotency key exceeds limit")
	}
	if p.Simulation != nil {
		if p.VMFamily != "solana_sbf" {
			return fmt.Errorf("local simulation requires solana_sbf")
		}
		if err := p.Simulation.Validate(p.Address); err != nil {
			return err
		}
	}
	return nil
}
