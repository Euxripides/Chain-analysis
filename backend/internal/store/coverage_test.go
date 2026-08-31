package store

import (
	"reflect"
	"testing"
)

func TestMergeBlockRanges(t *testing.T) {
	iv := func(f, t int64) BlockInterval { return BlockInterval{From: f, To: t} }
	cases := []struct {
		name string
		in   []BlockInterval
		want []BlockInterval
	}{
		{"empty", nil, nil},
		{"single", []BlockInterval{iv(1, 10)}, []BlockInterval{iv(1, 10)}},
		{"overlap", []BlockInterval{iv(1, 10), iv(5, 20)}, []BlockInterval{iv(1, 20)}},
		{"adjacent", []BlockInterval{iv(1, 10), iv(11, 20)}, []BlockInterval{iv(1, 20)}},
		{"disjoint", []BlockInterval{iv(1, 10), iv(20, 30)}, []BlockInterval{iv(1, 10), iv(20, 30)}},
		{"contained", []BlockInterval{iv(1, 100), iv(10, 20)}, []BlockInterval{iv(1, 100)}},
		{"unsorted", []BlockInterval{iv(50, 60), iv(1, 10), iv(11, 49)}, []BlockInterval{iv(1, 60)}},
	}
	for _, c := range cases {
		got := MergeBlockRanges(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func BenchmarkMergeBlockRanges(b *testing.B) {
	in := make([]BlockInterval, 1000)
	for i := range in {
		in[i] = BlockInterval{From: int64(i * 1000), To: int64(i*1000 + 500)}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = MergeBlockRanges(in)
	}
}
