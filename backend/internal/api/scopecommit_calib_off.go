//go:build !scopecalib

package api

import "net/http"

// scopeCommitCalibDelay is the production no-op of the P1-A calibration seam.
// The real (delay-injecting) implementation lives in scopecommit_calib.go behind
// //go:build scopecalib, so no production binary can ever be slowed down by it.
func scopeCommitCalibDelay(r *http.Request, phase string) {
	_ = r
	_ = phase
}
