//go:build scopecalib

package api

import (
	"net/http"
	"os"
	"strconv"
	"time"
)

// P1-A negative calibration seam (calibration builds ONLY).
//
// The production build never contains the code below: scopecommit_calib_off.go
// replaces it with an empty function under //go:build !scopecalib, so the two
// binaries differ by exactly one build tag and nothing else.
//
// Purpose: the C1 scope race has two windows. The first one (body still in
// flight) is already covered by scopeBodyMiddleware. The second one -- the
// window this calibration exists to measure -- is "the authorization decision
// has already been taken, and the handler has not committed yet". A real
// handler is not normally slow enough to hold that window open on demand, so
// the calibration build inserts an explicit, bounded delay at one of two
// phases of one chosen route:
//
//	phase "pre"  : after the permission decision, after CSRF, BEFORE the
//	               commit gate acquires its lock. This is the window in which
//	               an unfixed build commits a write with a scope that was
//	               already switched away.
//	phase "post" : inside the commit gate critical section, immediately before
//	               the handler runs. This measures what a scope mutation does
//	               while the gate holds its lock.
//
// The delay never changes control flow: it either elapses or the request
// context is cancelled. It is inert unless SCOPE_CALIB_ENABLE=1 is set in the
// environment of a calibration process.
func scopeCommitCalibDelay(r *http.Request, phase string) {
	if os.Getenv("SCOPE_CALIB_ENABLE") != "1" {
		return
	}
	if want := os.Getenv("SCOPE_CALIB_ROUTE"); want != "" && want != r.URL.Path {
		return
	}
	if want := os.Getenv("SCOPE_CALIB_PHASE"); want != "" && want != phase {
		return
	}
	ms, err := strconv.Atoi(os.Getenv("SCOPE_CALIB_DELAY_MS"))
	if err != nil || ms <= 0 {
		return
	}
	timer := time.NewTimer(time.Duration(ms) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-r.Context().Done():
	}
}
