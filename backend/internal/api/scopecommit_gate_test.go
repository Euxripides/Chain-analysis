package api

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/blockchain-platform/blockchain-platform/internal/policy"
)

// scopeCommitGateStubScope is the unit-test stand-in for the live session
// lookup: its presence switches the gate off, exactly like the guard seam.
type scopeCommitGateStubScope struct{}

func (scopeCommitGateStubScope) LiveSessionWorkspace(ctx context.Context, sessionID string) (string, bool, error) {
	return "", false, nil
}

// The commit gate is only as complete as its two decision inputs: which requests
// are session-scoped state-changing requests, and which path shapes are carved
// out. Both are asserted here; the end-to-end behaviour is driven by
// scripts/s7/c1-scope-commit-latency-gate-v1.sh and
// scripts/s7/c1-auth007-scope-epoch-gate-v2.sh against a real API and a real
// PostgreSQL.
func TestScopeCommitGateAppliesOnlyToSessionScopedWrites(t *testing.T) {
	sessionPrincipal := policy.Principal{Authenticated: true, SessionID: "s-1", WorkspaceID: "w-1"}
	systemPrincipal := policy.Principal{Authenticated: true, System: true, SessionID: "s-1", WorkspaceID: "w-1"}
	orgPrincipal := policy.Principal{Authenticated: true, SessionID: "s-1", WorkspaceID: ""}

	cases := []struct {
		name       string
		method     string
		principal  policy.Principal
		permission string
		want       bool
	}{
		{"session write takes the gate", http.MethodPost, sessionPrincipal, "case:create", true},
		{"session patch takes the gate", http.MethodPatch, sessionPrincipal, "case:update", true},
		{"session delete takes the gate", http.MethodDelete, sessionPrincipal, "case:delete", true},
		{"safe GET never takes the gate", http.MethodGet, sessionPrincipal, "case:read", false},
		{"HEAD never takes the gate", http.MethodHead, sessionPrincipal, "case:read", false},
		{"system principal never takes the gate", http.MethodPost, systemPrincipal, "case:create", false},
		{"organisation principal without a session workspace never takes the gate", http.MethodPost, orgPrincipal, "case:create", false},
		// AUTH-007 / S3: admin:session:revoke owns a dedicated short transaction
		// with its own commit-time check and a deterministic lock order, so the
		// generic gate must never wrap it -- not even when the path target cannot
		// be resolved here.
		{"admin force logout is never wrapped by the generic gate", http.MethodPost, sessionPrincipal, "admin:session:revoke", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "/api/v2/cases", nil)
			if got := scopeCommitGateApplies(r, tc.principal, tc.permission); got != tc.want {
				t.Fatalf("scopeCommitGateApplies(%s, perm=%s) = %v, want %v", tc.method, tc.permission, got, tc.want)
			}
		})
	}
}

// TestScopeCommitGateSelfTargetListIsEnumerated pins the self-target carve-out
// list. Adding a permission here silently weakens the gate, so the list is
// asserted explicitly and every entry must carry the reason it is carved out.
func TestScopeCommitGateSelfTargetListIsEnumerated(t *testing.T) {
	want := map[string]bool{"admin:session:revoke": true}
	if len(scopeCommitGateSelfTargetPermissions) != len(want) {
		t.Fatalf("self-target list size = %d, want %d: %v", len(scopeCommitGateSelfTargetPermissions), len(want), scopeCommitGateSelfTargetPermissions)
	}
	for perm := range want {
		reason, ok := scopeCommitGateSelfTargetPermissions[perm]
		if !ok {
			t.Fatalf("permission %q must declare its self-target carve-out", perm)
		}
		if len(reason) < 20 {
			t.Fatalf("carve-out %q must document why it is needed, got %q", perm, reason)
		}
	}
}

// TestScopeCommitGateDedicatedListIsEnumerated pins the second carve-out list:
// the routes that own an equivalent commit-time transaction inside the handler.
// This one is stronger than the self-target list -- it exempts the WHOLE
// permission -- so it must stay short, explicit and documented.
func TestScopeCommitGateDedicatedListIsEnumerated(t *testing.T) {
	want := map[string]bool{"admin:session:revoke": true}
	if len(scopeCommitGateDedicatedPermissions) != len(want) {
		t.Fatalf("dedicated list size = %d, want %d: %v", len(scopeCommitGateDedicatedPermissions), len(want), scopeCommitGateDedicatedPermissions)
	}
	for perm := range want {
		reason, ok := scopeCommitGateDedicatedPermissions[perm]
		if !ok {
			t.Fatalf("permission %q must declare why the generic gate does not wrap it", perm)
		}
		if len(reason) < 20 {
			t.Fatalf("dedicated carve-out %q must document why it is needed, got %q", perm, reason)
		}
	}
}

// TestScopeCommitGateAdminRevokeIsOwnedByItsDedicatedTransaction is the
// regression test for the S3 defect. The generic gate used to wrap the
// cross-session revoke, and the resulting lock order (handler locks the target,
// release locks the caller) is what made two administrators revoking each other
// hang. The exemption must be total, and the route must still be able to name its
// target before the handler runs.
func TestScopeCommitGateAdminRevokeIsOwnedByItsDedicatedTransaction(t *testing.T) {
	caller := policy.Principal{Authenticated: true, SessionID: "s-caller", WorkspaceID: "w-1"}

	withTarget := func(target string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/admin/v1/sessions/"+target+"/commands/revoke", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", target)
		return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	}

	for _, target := range []string{"s-caller", "s-victim"} {
		if got := scopeCommitGateApplies(withTarget(target), caller, "admin:session:revoke"); got {
			t.Fatalf("admin revoke targeting %q must be owned by scopecommit_revoke.go, not by the generic gate", target)
		}
	}
	if got := scopeCommitGateApplies(withTarget("s-victim"), caller, "case:create"); !got {
		t.Fatal("an ordinary session-scoped write must still take the gate")
	}
	if got := scopeCommitGateTargetSessionID(withTarget("s-victim")); got != "s-victim" {
		t.Fatalf("the path target must be identifiable before the handler runs, got %q", got)
	}
}

// TestScopeCommitGateSkipsTheUnitTestStubSeam makes the only bypass explicit: a
// Server with an injected sessionScope stub (unit tests) has no pool and must not
// try to open one.
func TestScopeCommitGateSkipsTheUnitTestStubSeam(t *testing.T) {
	s := &Server{sessionScope: scopeCommitGateStubScope{}}
	r := httptest.NewRequest(http.MethodPost, "/api/v2/cases", nil)
	w, got, release, ok := s.enterScopeCommitGate(httptest.NewRecorder(), r,
		policy.Principal{Authenticated: true, SessionID: "s-1", WorkspaceID: "w-1"}, "case:create")
	if !ok {
		t.Fatal("the stub seam must not refuse the request")
	}
	if release == nil {
		t.Fatal("a no-op release function must be returned")
	}
	if w == nil || got == nil {
		t.Fatal("the stub seam must pass the original writer and request through unchanged")
	}
	if _, isGate := w.(*scopeGateWriter); isGate {
		t.Fatal("the stub seam must not buffer the response: there is no transaction to commit")
	}
	release()
}

// ---------------------------------------------------------------------------
// The retractable response buffer.
//
// These tests are the negative calibration of the P1 response-correctness
// defect: the first implementation forwarded the response to the client as soon
// as it crossed 256 KiB, so a client could see a success whose transaction was
// then rolled back. The invariant asserted here is stronger and absolute --
// nothing at all reaches the target writer before the commit decision.
// ---------------------------------------------------------------------------

func TestScopeGateWriterNeverSendsBeforeCommit(t *testing.T) {
	rec := httptest.NewRecorder()
	w := newScopeGateWriter(rec, nil)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if _, err := w.Write([]byte("{\"ok\":true}")); err != nil {
		t.Fatalf("buffered write failed: %v", err)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("the target received %d bytes before the commit decision", rec.Body.Len())
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("the target header was written before the commit decision (status %d)", rec.Code)
	}
	w.commit()
	if rec.Code != http.StatusCreated {
		t.Fatalf("committed status = %d, want 201", rec.Code)
	}
	if got := rec.Body.String(); got != "{\"ok\":true}" {
		t.Fatalf("committed body = %q", got)
	}
}

func TestScopeGateWriterSpillsPastMemoryBoundAndKeepsBytesInOrder(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SCOPE_COMMIT_GATE_RESPONSE_MAX_BYTES", "16")
	t.Setenv("SCOPE_COMMIT_GATE_SPOOL_DIR", dir)

	rec := httptest.NewRecorder()
	w := newScopeGateWriter(rec, nil)
	payload := bytes.Repeat([]byte("0123456789"), 12) // 120 bytes, 7.5x the memory bound
	for i := 0; i < len(payload); i += 7 {
		end := i + 7
		if end > len(payload) {
			end = len(payload)
		}
		if _, err := w.Write(payload[i:end]); err != nil {
			t.Fatalf("spooled write failed: %v", err)
		}
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("the target received %d bytes before the commit decision", rec.Body.Len())
	}
	if !w.spooled {
		t.Fatal("a response past the memory bound must be spilled to disk, not dropped and not streamed")
	}
	spoolPath := w.spoolPath
	if spoolPath == "" {
		t.Fatal("the spool file path must be known so that it can be removed")
	}
	info, err := os.Stat(spoolPath)
	if err != nil {
		t.Fatalf("the spool file must exist while the request is in flight: %v", err)
	}
	// Windows reports 0666 for every file and ignores Chmod; the permission that
	// matters is the one the production target (Linux) enforces.
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("the spool file mode = %o, want 600", perm)
		}
	}
	if w.responseBytes() != int64(len(payload)) {
		t.Fatalf("responseBytes = %d, want %d", w.responseBytes(), len(payload))
	}
	w.commit()
	if !bytes.Equal(rec.Body.Bytes(), payload) {
		t.Fatalf("committed body differs from the buffered body (%d vs %d bytes)", rec.Body.Len(), len(payload))
	}
	if _, err := os.Stat(spoolPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the spool file must be removed after commit, stat err = %v", err)
	}
}

func TestScopeGateWriterRefusesPastHardBoundAndCancelsHandler(t *testing.T) {
	t.Setenv("SCOPE_COMMIT_GATE_RESPONSE_MAX_BYTES", "16")
	t.Setenv("SCOPE_COMMIT_GATE_RESPONSE_HARD_MAX_BYTES", "24")
	t.Setenv("SCOPE_COMMIT_GATE_SPOOL_DIR", t.TempDir())

	cancelled := 0
	rec := httptest.NewRecorder()
	w := newScopeGateWriter(rec, func() { cancelled++ })
	if _, err := w.Write([]byte("0123456789")); err != nil {
		t.Fatalf("first write must be accepted: %v", err)
	}
	if _, err := w.Write([]byte("0123456789")); err != nil {
		t.Fatalf("second write must be accepted: %v", err)
	}
	if _, err := w.Write([]byte("0123456789")); !errors.Is(err, errScopeGateResponseTooLarge) {
		t.Fatalf("write past the hard bound err = %v, want errScopeGateResponseTooLarge", err)
	}
	if !w.responseTooLarge() {
		t.Fatal("the writer must remember that the hard bound was exceeded")
	}
	if cancelled != 1 {
		t.Fatalf("the handler must be cancelled exactly once, got %d", cancelled)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("the target received %d bytes: the failed response must never be streamed", rec.Body.Len())
	}
	// A later write must not resurrect the response either.
	if _, err := w.Write([]byte("tail")); !errors.Is(err, errScopeGateResponseTooLarge) {
		t.Fatalf("write after the hard bound err = %v", err)
	}
	w.discard()
	if rec.Body.Len() != 0 {
		t.Fatalf("discard must leave the target untouched, got %d bytes", rec.Body.Len())
	}
}

func TestScopeGateWriterDiscardSendsNothingAndRemovesTheSpool(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SCOPE_COMMIT_GATE_RESPONSE_MAX_BYTES", "8")
	t.Setenv("SCOPE_COMMIT_GATE_SPOOL_DIR", dir)

	rec := httptest.NewRecorder()
	w := newScopeGateWriter(rec, nil)
	w.Header().Set("X-Test", "1")
	w.WriteHeader(http.StatusCreated)
	if _, err := w.Write(bytes.Repeat([]byte("x"), 64)); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	spoolPath := w.spoolPath
	w.discard()
	if rec.Body.Len() != 0 || rec.Code != http.StatusOK {
		t.Fatalf("a rolled-back response must leave the target untouched (code %d, %d bytes)", rec.Code, rec.Body.Len())
	}
	if rec.Header().Get("X-Test") != "" {
		t.Fatal("a rolled-back response must not leak its headers to the target")
	}
	if spoolPath != "" {
		if _, err := os.Stat(spoolPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("the spool file must be removed on discard, stat err = %v", err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("spool dir unreadable: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("the spool directory must be empty after discard, found %d entries", len(entries))
	}
}

func TestScopeGateWriterDefersFlushUntilCommit(t *testing.T) {
	rec := httptest.NewRecorder()
	w := newScopeGateWriter(rec, nil)
	if _, err := w.Write([]byte("hello")); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	w.Flush()
	if rec.Body.Len() != 0 || rec.Flushed {
		t.Fatal("Flush must not forward anything before the commit decision")
	}
	w.commit()
	if rec.Body.String() != "hello" {
		t.Fatalf("committed body = %q", rec.Body.String())
	}
	if !rec.Flushed {
		t.Fatal("a requested flush must happen once the response is committed")
	}
}
