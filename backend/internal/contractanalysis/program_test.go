package contractanalysis

import (
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/solana"
)

func TestProgramSchemaPreservesNativeIdentity(t *testing.T) {
	address := "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA"
	signature := solana.EncodeBase58(make([]byte, 64))
	p := ProgramRequest{VMFamily: "solana_sbf", Address: address, TxHashes: []string{signature}}
	if err := p.Normalize(); err != nil {
		t.Fatal(err)
	}
	if p.Address != address || p.SchemaVersion != ProgramRequestVersion {
		t.Fatalf("identity changed: %#v", p)
	}
	p.TxHashes = []string{"0x" + signature}
	if p.Normalize() == nil {
		t.Fatal("EVM hash accepted as Solana signature")
	}
	p = ProgramRequest{VMFamily: "tron_tvm", Address: "TR7NHqjeKQxGTCi8q8ZY4pL8otgjLj6t"}
	if p.Normalize() == nil {
		t.Fatal("truncated TRON address accepted")
	}
	p = ProgramRequest{VMFamily: "tron_tvm", Address: "TR7NHqjeKQxGTCi8q8ZY4pL8otgjLj6t" + "unknown"}
	if p.Normalize() == nil {
		t.Fatal("invalid checksum accepted")
	}
}

func TestProgramRequestHasIndependentStrictSchema(t *testing.T) {
	if _, err := DecodeProgram([]byte(`{"schema_version":"contract-program-request-v1","vm_family":"solana_sbf","address":"11111111111111111111111111111111","block":12}`)); err == nil {
		t.Fatal("historical slot accepted without a historical code provider")
	}
	if _, err := DecodeProgram([]byte(`{} {}`)); err == nil {
		t.Fatal("second JSON request accepted")
	}
}
