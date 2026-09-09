package download

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

func TestToAnkrInclusiveRange(t *testing.T) {
	for _, tc := range []struct{ s, e, rf, rt uint64 }{{0, 1, 0, 0}, {1, 2, 1, 1}, {100, 200, 100, 199}} {
		f, to, err := ToAnkrInclusiveRange(tc.s, tc.e)
		if err != nil || f != tc.rf || to != tc.rt {
			t.Fatalf("got %d..%d %v", f, to, err)
		}
	}
	for _, tc := range [][2]uint64{{1, 1}, {2, 1}} {
		if _, _, err := ToAnkrInclusiveRange(tc[0], tc[1]); err == nil {
			t.Fatal("invalid range accepted")
		}
	}
}
func txRow(b uint64) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"blockNumber":"0x%x"}`, b))
}
func TestRangeViolationHardFail(t *testing.T) {
	r, err := ValidateTransactionRange(100, 200, []json.RawMessage{txRow(100), txRow(199), txRow(250)})
	if !errors.Is(err, ErrRangeViolation) || r.OutOfRangeRows != 1 {
		t.Fatalf("r=%+v err=%v", r, err)
	}
}
func TestTokenCrossShard(t *testing.T) {
	r := newTokenRegistry()
	if err := r.claim("A2", "A", 100, 200); err != nil {
		t.Fatal(err)
	}
	if err := r.claim("A2", "B", 100, 200); err != nil {
		t.Fatalf("same-range shared cursor rejected: %v", err)
	}
	r.releaseShard("A")
	if err := r.claim("A2", "C", 100, 200); err != nil {
		t.Fatalf("remaining same-range owner was lost: %v", err)
	}
	if err := r.claim("A2", "D", 200, 300); !errors.Is(err, ErrTokenCrossShard) {
		t.Fatalf("%v", err)
	}
}

func TestTokenRegistryConcurrentSameRange(t *testing.T) {
	r := newTokenRegistry()
	var wg sync.WaitGroup
	errs := make(chan error, 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := r.claim("shared-page", fmt.Sprint(i), 100, 200); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}
func TestConcurrentAttribution64(t *testing.T) {
	r := newTokenRegistry()
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 8; j++ {
				_ = rand.Intn(5)
				id := fmt.Sprintf("r-%d-%d", i, j)
				if err := validateAttribution(id, id); err != nil {
					errs <- err
				}
				if err := r.claim(id, fmt.Sprint(i), uint64(i*100), uint64(i*100+100)); err != nil {
					errs <- err
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}
func TestRecoveryRangeBinding(t *testing.T) {
	original, _ := NewRangeEnvelope(100, 200)
	restored, _ := NewRangeEnvelope(100, 200)
	if original != restored {
		t.Fatal("changed")
	}
	changed, _ := NewRangeEnvelope(101, 200)
	if changed == original {
		t.Fatal("mismatch not detected")
	}
}
