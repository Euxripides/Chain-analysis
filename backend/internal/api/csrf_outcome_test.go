package api

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/iam"
)

// TestCSRFOutcomeForSessionError pins the classification rule that closes the P1-1 /
// AUTH-007 masking defect: a session that is positively gone is csrfSessionNotLive (which
// the callers translate into 401 AUTHN_SESSION_MISSING), while a failure that is NOT proof
// the session is gone stays a CSRF-class refusal (403). Loosening this table would either
// reintroduce the masking (a dead session reported as a CSRF failure) or weaken CSRF (an
// infrastructure error treated as "the user is simply logged out").
func TestCSRFOutcomeForSessionError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want csrfOutcome
	}{
		{"live session", nil, csrfOK},
		{"revoked or expired session", iam.ErrSessionInvalid, csrfSessionNotLive},
		{"wrapped session invalid", fmt.Errorf("authenticate: %w", iam.ErrSessionInvalid), csrfSessionNotLive},
		{"rotated token / reuse", iam.ErrSessionReuse, csrfSessionNotLive},
		{"workspace membership revoked", iam.ErrWorkspaceAccessRevoked, csrfSessionNotLive},
		{"pool exhausted", errors.New("pool exhausted"), csrfTokenInvalid},
		{"cancelled context", context.Canceled, csrfTokenInvalid},
	}
	for _, tc := range cases {
		if got := csrfOutcomeForSessionError(tc.err); got != tc.want {
			t.Fatalf("%s: outcome = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// functionBody returns the source of one top-level function, found by its signature.
func functionBody(source, signature string) (string, error) {
	start := strings.Index(source, signature)
	if start < 0 {
		return "", errors.New("function not found in authmw.go: " + signature)
	}
	body := source[start:]
	if end := strings.Index(body[1:], "\\nfunc "); end > 0 {
		body = body[:end+1]
	}
	return body, nil
}

// csrfOrderViolation reports the first ordering violation of the authorization layer.
// Two contracts are checked, and both were previously only half enforced:
//
//  1. require/requireAny run the scope guard BEFORE the CSRF gate. The guard doc comment
//     used to claim the opposite (after the CSRF check) while the call sites already did
//     the right thing, so nothing but a human reading the call site protected the order.
//  2. checkCSRFOutcome authenticates the session BEFORE comparing CSRF tokens, which is
//     what lets a session that died under the request be reported as 401 instead of 403.
func csrfOrderViolation(source string) error {
	for _, fn := range []string{"func (s *Server) require(", "func (s *Server) requireAny("} {
		body, err := functionBody(source, fn)
		if err != nil {
			return err
		}
		guard := strings.Index(body, "s.guardSessionWorkspace(w, r,")
		csrf := strings.Index(body, "s.checkCSRFOutcome(r,")
		if guard < 0 || csrf < 0 {
			return fmt.Errorf("%s: guard at %d, CSRF gate at %d; both calls are required", fn, guard, csrf)
		}
		if guard > csrf {
			return fmt.Errorf("%s: the CSRF gate runs before the scope guard", fn)
		}
	}
	body, err := functionBody(source, "func (s *Server) checkCSRFOutcome(")
	if err != nil {
		return err
	}
	auth := strings.Index(body, ".Authenticate(r.Context(), sessionCookie.Value, audience)")
	token := strings.Index(body, "cfg.CSRFSecretCookieCandidates(audience)")
	if auth < 0 || token < 0 {
		return fmt.Errorf("checkCSRFOutcome: authenticate at %d, token lookup at %d", auth, token)
	}
	if auth > token {
		return errors.New("checkCSRFOutcome compares CSRF tokens before it knows whether the session is still alive")
	}
	return nil
}

// TestCSRFGateOrderingCalibration asserts the two order contracts on the real source and
// then proves the detector detects by feeding it deliberately reordered copies.
func TestCSRFGateOrderingCalibration(t *testing.T) {
	raw, err := os.ReadFile("authmw.go")
	if err != nil {
		t.Fatalf("read authmw.go: %v", err)
	}
	source := string(raw)
	if err := csrfOrderViolation(source); err != nil {
		t.Fatalf("authorization layer ordering contract violated: %v", err)
	}

	// Calibration 1: move the session authentication after the token comparison.
	authLine := "\tsess, err := s.iam.Sessions().Authenticate(r.Context(), sessionCookie.Value, audience)\n"
	tokenLine := "\tcookie, ok := cookieByName(r, cfg.CSRFSecretCookieCandidates(audience))\n"
	moved := strings.Replace(source, authLine, "", 1)
	if moved == source {
		t.Fatal("calibration setup failed: the session authentication line was not found")
	}
	moved = strings.Replace(moved, tokenLine, tokenLine+authLine, 1)
	if err := csrfOrderViolation(moved); err == nil {
		t.Fatal("NEGATIVE CALIBRATION FAILED: token-first checkCSRFOutcome was accepted")
	}

	// Calibration 2: move the scope guard after the CSRF gate in require.
	guardBlock := "\t\tif !s.guardSessionWorkspace(w, r, p, permission) {\n\t\t\treturn\n\t\t}\n"
	calibDelay := "\t\tscopeCommitCalibDelay(r, \"pre\")"
	late := strings.Replace(source, guardBlock, "", 1)
	if late == source {
		t.Fatal("calibration setup failed: the require guard block was not found")
	}
	late = strings.Replace(late, calibDelay, guardBlock+calibDelay, 1)
	if late == source {
		t.Fatal("calibration setup failed: the calibration seam was not found")
	}
	if err := csrfOrderViolation(late); err == nil {
		t.Fatal("NEGATIVE CALIBRATION FAILED: CSRF-before-guard ordering was accepted")
	}
}
