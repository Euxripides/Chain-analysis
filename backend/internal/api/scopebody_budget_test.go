package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestScopeBodyBudgetIsProcessWideAndReleased pins the two properties the
// process-wide body budget has to have for the middleware above it to be
// bounded: the count limit really refuses the request after the limit is
// reached, and every release returns the accounting to exactly zero.
func TestScopeBodyBudgetIsProcessWideAndReleased(t *testing.T) {
	total, maxInflight, readDeadline := scopeBodyLimits()
	if total <= 0 || maxInflight <= 0 || readDeadline <= 0 {
		t.Fatalf("body limits must be positive: total=%d inflight=%d deadline=%s", total, maxInflight, readDeadline)
	}
	used0, held0 := scopeBodyBudgetSnapshot()

	releases := make([]func(), 0, maxInflight)
	for i := 0; i < maxInflight; i++ {
		release, ok := scopeBodyBudgetAcquire(1)
		if !ok {
			t.Fatalf("reservation %d of %d must succeed (used=%d held=%d)", i+1, maxInflight, used0, held0)
		}
		releases = append(releases, release)
	}
	if _, ok := scopeBodyBudgetAcquire(1); ok {
		t.Fatal("the count limit must refuse the reservation after maxInflight are held")
	}

	releases[0]()
	extra, ok := scopeBodyBudgetAcquire(1)
	if !ok {
		t.Fatal("a released reservation must be reusable")
	}
	extra()
	for _, release := range releases[1:] {
		release()
	}
	if used, held := scopeBodyBudgetSnapshot(); used != 0 || held != 0 {
		t.Fatalf("the budget must return to zero, got used=%d held=%d", used, held)
	}

	// A reservation larger than the whole budget must never succeed, and the
	// failure must not leak accounting.
	oversized, ok := scopeBodyBudgetAcquire(total + 1)
	if ok {
		oversized()
		t.Fatal("a reservation above the whole budget must be refused")
	}
	if used, held := scopeBodyBudgetSnapshot(); used != 0 || held != 0 {
		t.Fatalf("a refused reservation must not leak, got used=%d held=%d", used, held)
	}
}

// TestScopeBodyReserveForNeverUnderReserves checks the reservation input: an
// undeclared length has to reserve the whole per-request bound, otherwise a
// chunked upload would walk straight past the process budget.
func TestScopeBodyReserveForNeverUnderReserves(t *testing.T) {
	declared := httptest.NewRequest(http.MethodPost, "/api/v2/cases", nil)
	declared.ContentLength = 1024
	if got := scopeBodyReserveFor(declared); got != 1024 {
		t.Fatalf("a declared length must be reserved as declared, got %d", got)
	}
	undeclared := httptest.NewRequest(http.MethodPost, "/api/v2/cases", nil)
	undeclared.ContentLength = -1
	if got := scopeBodyReserveFor(undeclared); got != maxScopeGatedBodyBytes {
		t.Fatalf("an undeclared length must reserve the whole bound, got %d want %d", got, maxScopeGatedBodyBytes)
	}
	oversized := httptest.NewRequest(http.MethodPost, "/api/v2/cases", nil)
	oversized.ContentLength = maxScopeGatedBodyBytes * 4
	if got := scopeBodyReserveFor(oversized); got != maxScopeGatedBodyBytes {
		t.Fatalf("an oversized declared length must reserve the whole bound, got %d want %d", got, maxScopeGatedBodyBytes)
	}
}
