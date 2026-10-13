package assetidentity

import "testing"

func TestContractOverridesSpoofedSymbol(t *testing.T) {
	fake := "0x1111111111111111111111111111111111111111"
	symbol, contract, e := Resolve("bsc", "USDT", fake)
	if e != nil || symbol != "" || contract != fake {
		t.Fatal(symbol, contract, e)
	}
	_, official, e := Resolve("bsc", "USDT", "")
	if e != nil || official == fake || official != "0x55d398326f99059ff775485246999027b3197955" {
		t.Fatal(official, e)
	}
	for _, s := range []string{"USDT", "U5DT", "UႽDΤ", "U5DТ", "ＵＳＤＴ"} {
		d := Describe("bsc", fake, s)
		if d["spam_suspected"] != true || d["verified_token"] != false {
			t.Fatal(s, d)
		}
	}
	d := Describe("bsc", official, "BSC-USD")
	if d["verified_token"] != true || d["spam_suspected"] != false {
		t.Fatal(d)
	}
	if _, _, e := Resolve("bsc", "U5DT", ""); e == nil {
		t.Fatal("Unknown symbol accepted as an asset identity")
	}
}

func TestBSCUSDAliasCannotMergeDifferentContract(t *testing.T) {
	d := Describe("bsc", "0x0000000000000000000000000000000000000001", "BSC-USD")
	if d["verified_token"] != false || d["symbol_confusable"] != true {
		t.Fatal(d)
	}
	d = Describe("bsc", records[0].Contract, "BSC-USD")
	if d["verified_token"] != true || d["spam_suspected"] != false {
		t.Fatal(d)
	}
}
