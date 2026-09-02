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

func TestMergeCoveragePartsKeepsPerRangeMetrics(t *testing.T) {
	parts := []coveragePart{
		{BlockInterval: BlockInterval{From: 20, To: 30}, Rows: 4, Bytes: 40, HeadBlock: 30},
		{BlockInterval: BlockInterval{From: 1, To: 10}, Rows: 2, Bytes: 20, HeadBlock: 10},
		{BlockInterval: BlockInterval{From: 11, To: 19}, Rows: 3, Bytes: 30, HeadBlock: 19},
		{BlockInterval: BlockInterval{From: 100, To: 110}, Rows: 5, Bytes: 50, HeadBlock: 110},
	}
	got := mergeCoverageParts(parts)
	want := []coveragePart{
		{BlockInterval: BlockInterval{From: 1, To: 30}, Rows: 9, Bytes: 90, HeadBlock: 30},
		{BlockInterval: BlockInterval{From: 100, To: 110}, Rows: 5, Bytes: 50, HeadBlock: 110},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mergeCoverageParts() = %+v, want %+v", got, want)
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
