package method

import "testing"

func FuzzDecodeInputNoPanic(f *testing.F) {
	abi, err := ParseABI([]byte(erc20ABI))
	if err != nil {
		f.Fatal(err)
	}
	method, ok := abi.Lookup("0xa9059cbb")
	if !ok {
		f.Fatal("transfer ABI missing")
	}
	for _, seed := range []string{transferInput, "0x", "0xa9059cbb", "0xa9059cbbzz", "0xa9059cbb" + string(make([]byte, 64))} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		_, _ = DecodeInput(method, input)
	})
}

func FuzzParseABINoPanic(f *testing.F) {
	for _, seed := range [][]byte{[]byte(erc20ABI), []byte(`[]`), []byte(`{"abi":[]}`), []byte(`"[]"`), {0xff, 0x00}} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > MaxABIJSONBytes+1 {
			raw = raw[:MaxABIJSONBytes+1]
		}
		_, _ = ParseABI(raw)
	})
}
