package tron

import "testing"

func TestAddressRoundTripAndValidation(t *testing.T) {
	const address = "TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj"
	hexAddress, err := TronBase58ToHex(address)
	if err != nil {
		t.Fatal(err)
	}
	if len(hexAddress) != 42 || hexAddress[:2] != "41" {
		t.Fatalf("bad hex %q", hexAddress)
	}
	got, err := TronHexToBase58(hexAddress)
	if err != nil {
		t.Fatal(err)
	}
	if got != address {
		t.Fatalf("round trip: got %q want %q", got, address)
	}
	if normalized, err := NormalizeTronAddress(address); err != nil || normalized != address {
		t.Fatalf("normalize: %q %v", normalized, err)
	}
}

func TestAddressRejectsBadInputs(t *testing.T) {
	for _, input := range []string{
		"txlaq63xg1nazckpwkhvzw7csemlmeqcdj",
		"TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcda",
		"TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcd0",
		"40A614F803B6FD780986A42C78EC9C7F77E6DED13C",
		"41ZZ14F803B6FD780986A42C78EC9C7F77E6DED13C",
	} {
		if err := ValidateTronAddress(input); err == nil {
			t.Fatalf("expected invalid: %s", input)
		}
	}
}

func TestTronAddressRepresentations(t *testing.T) {
	const address = "TJmmqjb1DK9TTZbQXzRQ2AuA94z4gKAPFh"
	hex41, err := Base58TToTronHex41(address)
	if err != nil || len(hex41) != 42 || hex41[:2] != "41" {
		t.Fatalf("native conversion: %q %v", hex41, err)
	}
	evm, err := Base58TToEVMHex20(address)
	if err != nil || len(evm) != 42 || evm[:2] != "0x" {
		t.Fatalf("json-rpc conversion: %q %v", evm, err)
	}
	got, err := EVMHex20ToBase58T(evm)
	if err != nil || got != address {
		t.Fatalf("json-rpc roundtrip: %q %v", got, err)
	}
}
